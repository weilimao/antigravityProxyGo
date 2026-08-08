package relay

// ocr_batch.go —— OCR 多图单请求批量上游(L1 批量原语)。
//
// 背景:三层分离架构的 L1 此前对每张图各发一次上游(ocrImageUncached /
// ocrImageUncachedViaRoute),一条消息带 N 张截图即 N 次串行上游 ≈ N×~3s 延迟 +
// N 次号池配额。本文件把「同一入站请求里的多张 miss 图」合并进单次上游调用,
// 模型按 [[图k]](k=1..N)标记按顺序输出每张图的分析,回译拆分后逐张回填缓存与
// L2 text 块,把 N 次上游降到 1 次。
//
// 设计要点(详见 plans/cryptic-coalescing-mccarthy.md):
//   - 只覆盖「已 cache miss 且已得 b64」的 in-window 候选;cache hit / 窗外图路径
//     完全不动,不参与批量(由 L2 收集候选,本文件不感知窗口);
//   - 跨消息不混批:L2 按消息分组调 OcrImageBatch,一条消息的 miss 图一批,
//     blast radius 受限、复用该消息的 promptCtx;
//   - 解析失败整体回退逐图:批量响应没按 [[图k]](1..N) 拆全 → ok=false → 全批回退,
//     L2 对每张图调原 OcrImage(单图),保证 worst case 不劣于现状(最多 N+1 次上游);
//   - 缓存契约零变动:成功拆分后逐张走 ocrLRUCache.set(各 item 自己的 ocrCacheKey
//     =ownerKey|ocrModel|sha256(b64)[:16],三维 image-only),与单图 success 路径一致;
//   - singleflight:批量路径不接入(miss 候选已是 cache miss,并发重复同批是边角小
//     浪费,接入需复合键,复杂度不值;现有单图 OcrImage 的 singleflight 保留);
//   - 重试:批量 attempt 交 ocrCallWithRetry(传输层 EOF / 429/5xx 重试,4xx 非 429 /
//     编解码 / 空候选 / 拆分失败 不重试,30s 总超时上界),与单图同款;
//   - 出站形态沿用单图两条路径:Google 族 Gemini generateContent(多 InlineData 一次性),
//     非 Google 族 18444 /route OpenAI Chat(多 image_url 一次性,每次重设 X-Antigravity-OCR-Self
//     自递归守卫头)。两条上游对多图请求体都是字节级透传,无需改下游。
//
// 与 L2 的契约:L2 一条消息收集到 ≥2 张 miss 候选时调本文件 OcrImageBatch,=1 张时
// 仍调原 OcrImage(单图零回归)。OcrImageBatch 返回的 []OcrBatchResult 与入参 items
// 逐项对齐(L2 按原位置写回)。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// OcrBatchItem 是 OcrImageBatch 的入参项:一张待 OCR 的 b64 图。
// B64   : 已归一为标准 base64(由 L2 经 resolveB64WithFetch/parseDataURL 拿到)。
// Mime  : 标准 image/* ;空时 OcrImage 内部兜底 image/jpeg,这里原样透传给上游。
// PromptCtx : 当前消息的用户提问上下文文本(同口径 userPromptCtx),仅用于批量
//
//	prompt 的靶向分析方向,不参与缓存键(image-only)。
type OcrBatchItem struct {
	B64       string
	Mime      string
	PromptCtx string
}

// OcrBatchResult 是 OcrImageBatch 的返回项,与入参 items 逐项对齐。
//   - Text: OCR 识别出的纯文本(成功)或空串(失败);
//   - Err : 失败原因(nil=成功);
//   - Ok  : true=OCR 成功(写 success 长 TTL);
//   - CachedHit: true=该项命中 ocrCache 直接返回(未进批量上游)。
type OcrBatchResult struct {
	Text      string
	Err       error
	Ok        bool
	CachedHit bool
}

// ocrBatchMaxImages 是单次批量上游一次最多携带的图片数上界。
// 取 8:兼顾「把多截图请求压到 1 次上游」的收益与单请求体大小/上游多图处理的稳定性
// (ImageData 各可达数 MB,8 张 ≈ 数十 MB 量级仍可接受;更多图拆多批,L2 再调一次即可)。
// 单消息超此数的罕见,L2 仍按本上限分批调(本文件对超限不报错,只是 L2 循环分批)。
const ocrBatchMaxImages = 8

// batchImageMarkerPrefix 是批量回译标记前缀,格式为「[[图k]]」(k 为 1 起的图序号)。
// 模型在批量 prompt 里被要求按此格式顺序输出每张图的分析;splitBatchOcrText 据此切片。
// 单一标记格式 + 强 prompt 最可靠,避免鲁棒变体([[1]]/[图1])误切。
const batchImageMarkerPrefix = "[[图"

// OcrImageBatch 对一组 b64 miss 图做批量上游 OCR,返回逐项对齐的结果。
//
// 行为:
//  1. 逐项查 ocrCache:命中直接回填 OcrBatchResult{CachedHit:true, Ok:true, Text},不进批量上游;
//  2. miss 项(去重同 b64)按 ocrBatchMaxImages 分批:每批 ≤ ocrBatchMaxImages,
//     合并一次上游调用(Google 族 Gemini 多 InlineData / 非 Google 族 /route 多 image_url);
//  3. 上游响应按 [[图k]] 标记拆 N 段:全拆出 → 逐张 set success 长 TTL + 回填;拆不全 →
//     ok=false,该批整体回退逐图(本文件内逐项调 s.OcrImage,保证不劣于现状)。
//
// 入参 items 长度可 0/1/N:L2 在 =1 时本就不调本方法(走 OcrImage),但这里对短输入
// 也正确(0 → 空,1 → 走 batchOne 里 len==1 分支=等价单图但成功写 set)。
// 返回的 []OcrBatchResult 长度恒等于 len(items),按入参顺序对齐。
func (s *OCRService) OcrImageBatch(userSession *RelaySession, items []OcrBatchItem) []OcrBatchResult {
	results := make([]OcrBatchResult, len(items))
	if s == nil || userSession == nil || len(items) == 0 {
		return results
	}
	ocrModel := s.getOcrModel()
	ownerKey := ocrOwnerKey(userSession)

	// 1) 逐项查缓存:命中直接回填,不进批量。
	type batchIdx struct{ pos int } // 缓存未命中待批量的原始 items 下标
	var pendingIdx []int
	for i, it := range items {
		if strings.TrimSpace(it.B64) == "" {
			results[i] = OcrBatchResult{Err: fmt.Errorf("OcrImageBatch: empty image data")}
			continue
		}
		if s.cache != nil {
			key := ocrCacheKey(ownerKey, ocrModel, it.B64)
			if e, ok := s.cache.get(key); ok {
				s.counters.hits.Add(1)
				results[i] = OcrBatchResult{Text: e.text, Err: e.err, Ok: e.ok, CachedHit: true}
				continue
			}
			// 与 OcrImage 在缓存查找边界的计数契约一致:miss 计一次(按图计)。
			// 回退逐图路径下会由 OcrImage 再次 miss 计数,属罕见批量解析失败场景的可接受计数重叠,
			// 不影响命中/未命中的相对量级与日志体感(常态批量成功时每图恰计 1 次 miss)。
			s.counters.misses.Add(1)
		}
		pendingIdx = append(pendingIdx, i)
	}
	if len(pendingIdx) == 0 {
		return results
	}

	// 2) 按 ocrBatchMaxImages 分批送上游。pendingIdx 内是全 miss 的原始下标,按顺序切片。
	for start := 0; start < len(pendingIdx); start += ocrBatchMaxImages {
		end := start + ocrBatchMaxImages
		if end > len(pendingIdx) {
			end = len(pendingIdx)
		}
		batchPos := pendingIdx[start:end] // 该批对应 items 的原始下标集合
		s.ocrBatchOne(userSession, ocrModel, ownerKey, items, results, batchPos)
	}
	return results
}

// ocrBatchOne 处理一批(≤ ocrBatchMaxImages)miss 图:合并一次上游调用,拆分回填或回退逐图。
//
// batchPos 是该批在 items / results 里的原始下标集合(顺序即图序)。promptCtx 取本批
// 任一项的 PromptCtx(同消息同 promptCtx,L2 收集时保证一致;零项为空也无妨)。
func (s *OCRService) ocrBatchOne(userSession *RelaySession, ocrModel, ownerKey string, items []OcrBatchItem, results []OcrBatchResult, batchPos []int) {
	n := len(batchPos)
	if n == 0 {
		return
	}

	// 本批的 promptCtx:取首项即可(同消息口径一致)。
	promptCtx := strings.TrimSpace(items[batchPos[0]].PromptCtx)

	// 构造本批的 b64+mime 顺序视图与去重表(同图同会话在批内重复时,上游只发送一次,
	// 拆分后结果回填给所有相同 b64 的项,避免重复烧上游)。
	b64Seq := make([]string, 0, n)
	mimeSeq := make([]string, 0, n)
	dedup := make(map[string]int, n) // b64 -> 在 b64Seq 中的下标(0 起)
	for _, pos := range batchPos {
		b64 := items[pos].B64
		if _, seen := dedup[b64]; !seen {
			dedup[b64] = len(b64Seq)
			b64Seq = append(b64Seq, b64)
			mimeSeq = append(mimeSeq, items[pos].Mime)
		}
	}
	uniqN := len(b64Seq)

	// 单张唯一图:等价单图,但走批量路径也正确(构造单图 prompt 拆分)。直接调 batchUpstream
	// 拆分即可;不为单图特判走 OcrImage,以保持批量路径单一(回退逐图由拆分失败兜底)。
	segs, segErr, upstreamHit := s.batchUpstream(userSession, ocrModel, promptCtx, b64Seq, mimeSeq)

	// 拆分成功 → 逐张写 success 长 TTL + 回填所有相同 b64 的项。
	if segErr == nil && len(segs) == uniqN {
		for _, pos := range batchPos {
			b64 := items[pos].B64
			idx := dedup[b64] // 该 b64 在去重序列中的下标
			text := segs[idx]
			ok := strings.TrimSpace(text) != ""
			if s.cache != nil {
				key := ocrCacheKey(ownerKey, ocrModel, b64)
				cachedText := text
				if !ok {
					cachedText = ""
				}
				s.cache.set(key, cachedText, nil, ok)
			}
			if ok {
				results[pos] = OcrBatchResult{Text: text, Ok: true}
			} else {
				results[pos] = OcrBatchResult{Err: fmt.Errorf("ocr batch: empty segment for image")}
			}
		}
		_ = upstreamHit // 拆分成功不计 miss(批量体感见日志);counters 已在 miss 路径自增
		return
	}

	// 拆分失败 / 上游错误 → 整批回退逐图:对每个原始下标调原 OcrImage(单图)。
	// 最坏 N+1 次上游,但结果质量不劣于现状(单图 OCR 文本照常落块、缓存照常写)。
	// s.logf 自带 s.logFn 的 nil 守卫(见 ocr_engine.go),未注入日志时静默,无需在此再判 nil。
	if segErr != nil {
		s.logf("⚠️ 批量上游失败,回退逐图(本批 %d 张): %v", n, segErr)
	} else {
		s.logf("⚠️ 批量响应未能按 [[图k]] 拆全(本批 %d 张),回退逐图", n)
	}
	for _, pos := range batchPos {
		text, err, _ := s.OcrImage(userSession, items[pos].B64, items[pos].Mime, items[pos].PromptCtx)
		results[pos] = OcrBatchResult{Text: text, Err: err, Ok: err == nil && strings.TrimSpace(text) != ""}
	}
}

// batchUpstream 发一次批量上游调用(Google 族 Gemini / 非 Google 族 /route),解析后按
// [[图k]] 拆 N 段。返回 (segs, err, upstreamHit):
//   - segs 长度 == uniqN 且 err==nil:拆分成功;
//   - err != nil:上游调用失败(含重试耗尽),segs 为空;
//   - err == nil 但 segs 不全:上游成功但拆分失败(segs 为空),交调用方回退逐图。
//
// 错误文本约定(供 isOcrRetryableErr 解析):非 200 → "ocr batch service returned status %d: %s"
// (Google 族) / "ocr batch route service returned status %d: %s"(/route),含 "status " 关键词。
func (s *OCRService) batchUpstream(userSession *RelaySession, ocrModel, promptCtx string, b64Seq, mimeSeq []string) (segs []string, err error, upstreamHit bool) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("batchUpstream: nil service or client"), false
	}
	if len(b64Seq) == 0 {
		return nil, fmt.Errorf("batchUpstream: empty batch"), false
	}
	uniqN := len(b64Seq)
	isGoogle, _, _, upstreamModel := s.resolveOcrTarget(ocrModel)
	batchPrompt := buildBatchOcrPrompt(promptCtx, uniqN)

	if !isGoogle {
		// 非 Google 族:/route OpenAI Chat 多 image_url。
		reqBytes, mErr := buildBatchRouteRequest(ocrModel, batchPrompt, b64Seq, mimeSeq)
		if mErr != nil {
			return nil, fmt.Errorf("marshal batch route request: %w", mErr), false
		}
		result := ocrCallWithRetry(nil, "ocr batch route", s.logf, func(ctx context.Context) ocrAttemptResult {
			return s.ocrBatchRouteAttempt(ctx, userSession, reqBytes)
		})
		if result.err != nil {
			return nil, result.err, true
		}
		segs, ok := splitBatchOcrText(result.text, uniqN)
		if !ok {
			return nil, nil, true // 上游成功但拆分失败,交上层回退(无 err 但 segs 空)
		}
		return segs, nil, true
	}

	// Google 族:Gemini generateContent 多 InlineData。
	ocrReq := buildBatchGeminiRequest(batchPrompt, b64Seq, mimeSeq)
	ocrReqBytes, mErr := json.Marshal(ocrReq)
	if mErr != nil {
		return nil, fmt.Errorf("marshal batch ocr request: %w", mErr), false
	}
	result := ocrCallWithRetry(nil, "ocr batch", s.logf, func(ctx context.Context) ocrAttemptResult {
		return s.ocrBatchGeminiAttempt(ctx, userSession, upstreamModel, ocrReqBytes)
	})
	if result.err != nil {
		return nil, result.err, true
	}
	segs, ok := splitBatchOcrText(result.text, uniqN)
	if !ok {
		return nil, nil, true
	}
	return segs, nil, true
}

// buildBatchOcrPrompt 构造批量 OCR 的统一 prompt:含本批图片数、[[图k]] 标记输出约定、
// 转写铁律 / 不确定标注 / 空间结构三保真条款与每图段格式约定。promptCtx 为空时走通用提取
// 模板,非空时附带用户提问上下文,并按用户关注点做靶向分析。铁律核心句与不确定标注条款
// 消费 ocr_prompt.go 的 ocrFidelityCore / ocrUncertaintyClause 共享常量,与单图 prompt
// 共享单一信息源,改一处同步单图/批量生效。
func buildBatchOcrPrompt(promptCtx string, n int) string {
	header := fmt.Sprintf("你是一个顶级的多模态视觉分析助手。本批共包含 %d 张图片,请对每一张图分别分析与提取。每张图都是用户直接截取的屏幕画面，%s"+batchMarkerRule(n), n, ocrFidelityCore)
	if strings.TrimSpace(promptCtx) != "" {
		return header + fmt.Sprintf("\n\n【用户提问上下文】：用户在发送这批图片时附带的提问/说明文本为：\n\"%s\"\n\n请结合上述用户的提问与关注点，对每张图做靶向分析。", promptCtx)
	}
	return header
}

// batchMarkerRule 返回批量输出格式约定的尾部说明(含精确转写铁律 / 不确定标注 / 空间
// 结构三保真技能,供 buildBatchOcrPrompt 拼接)。铁律与不确定标注与单图 prompt 共享
// 常量,空间结构因批量内联括号排版与单图列表不同而模板内联锚定两者的协调一致。
func batchMarkerRule(n int) string {
	return fmt.Sprintf(`
请按以下要求逐张输出:

0. 精确转写铁律:%s
1. 输出格式:为每张图单独输出一段,且必须以形如「[[图k]]」的标记行作为该段的开头(k 为该图的序号,从 1 到 %d,严格按顺序)。例如第 1 张图的分析以「[[图1]]」开头,第 2 张以「[[图2]]」开头,依此类推。
2. 每段内容包括:该图的图像总体概览 + 图中所有文字/代码/终端命令/报错堆栈的原样逐字提取(保持原始缩进与换行,不要自动修正错别字,代码与报错用 Markdown 代码块包裹;%s)+ 视觉布局与逻辑关系描述(若含 UI 或 IDE,注明高亮项、报错弹窗、按钮状态及其位置关系;若含流程图/表格,还原节点连线方向或行列表格数据)。
3. 严格顺序:必须按 1,2,...,%d 的顺序输出所有 %d 段,不得跳号、不得合并、不得遗漏任何一张。
4. 严禁包含任何前言、引言或客套话(包括"好的"等开场白),直接从「[[图1]]」开始输出。`, ocrFidelityCore, n, ocrUncertaintyClause, n, n)
}

// buildBatchGeminiRequest 构造 Google 族批量 Gemini generateContent 请求体:
// 单条 user 消息,Parts = [{Text:batchPrompt}, {InlineData:img1}, {InlineData:img2}, ...]。
func buildBatchGeminiRequest(batchPrompt string, b64Seq, mimeSeq []string) GeminiRequest {
	parts := make([]GeminiPart, 0, 1+len(b64Seq))
	parts = append(parts, GeminiPart{Text: batchPrompt})
	for i, b64 := range b64Seq {
		mime := mimeSeq[i]
		if mime == "" {
			mime = "image/jpeg"
		}
		parts = append(parts, GeminiPart{InlineData: &GeminiBlob{MimeType: mime, Data: b64}})
	}
	return GeminiRequest{
		Contents: []GeminiContent{
			{Role: "user", Parts: parts},
		},
	}
}

// buildBatchRouteRequest 构造非 Google 族批量 /route OpenAI Chat 请求体:
// 单条 user 消息,content = [{type:text,text:batchPrompt}, {type:image_url,image_url:{url:"data:..."}} × N]。
func buildBatchRouteRequest(ocrModel, batchPrompt string, b64Seq, mimeSeq []string) ([]byte, error) {
	reqModel := strings.TrimSpace(ocrModel)
	if reqModel == "" {
		reqModel = defaultOcrModel
	}
	content := make([]map[string]interface{}, 0, 1+len(b64Seq))
	content = append(content, map[string]interface{}{
		"type": "text",
		"text": batchPrompt,
	})
	for i, b64 := range b64Seq {
		mime := mimeSeq[i]
		if mime == "" {
			mime = "image/jpeg"
		}
		content = append(content, map[string]interface{}{
			"type": "image_url",
			"image_url": map[string]interface{}{
				"url": "data:" + mime + ";base64," + b64,
			},
		})
	}
	req := map[string]interface{}{
		"model": reqModel,
		"messages": []map[string]interface{}{
			{
				"role":    "user",
				"content": content,
			},
		},
	}
	return json.Marshal(req)
}

// ocrBatchGeminiAttempt 是 Google 族批量「一次真打 18443 Gemini + 解析响应」的纯上游尝试,
// 供 batchUpstream 经 ocrCallWithRetry 重试调用。与 ocrGeminiAttempt 同款链路,仅请求体为批量。
//
// 错误文本约定:非 200 → "ocr batch service returned status %d: %s"(含 "status " 关键词)。
// 候选为空属确定性失败(上游安全拦截的一种),在此返回带 status 文本的 error,不重试。
func (s *OCRService) ocrBatchGeminiAttempt(ctx context.Context, userSession *RelaySession, upstreamModel string, ocrReqBytes []byte) ocrAttemptResult {
	if s == nil || s.client == nil {
		return ocrAttemptResult{err: fmt.Errorf("ocrBatchGeminiAttempt: nil service or client")}
	}
	ocrURL := fmt.Sprintf("http://%s/v1beta/models/%s:generateContent", localProxyAddr, upstreamModel)
	ocrHTTPReq, errReq := http.NewRequestWithContext(ctx, http.MethodPost, ocrURL, bytes.NewReader(ocrReqBytes))
	if errReq != nil {
		return ocrAttemptResult{err: fmt.Errorf("create batch ocr request: %w", errReq)}
	}
	ocrHTTPReq.Header.Set("Content-Type", "application/json")
	ocrHTTPReq.Header.Set("Authorization", "Bearer "+userSession.UserKey)
	ocrHTTPReq.Header.Set("X-Relay-User-Id", userSession.UserID)
	if userSession.APIKeyID != "" {
		ocrHTTPReq.Header.Set("X-Relay-Api-Key-Id", userSession.APIKeyID)
	}
	ocrHTTPReq.Header.Set("X-Antigravity-Original-Path", "/v1internal:generateContent/ocr-batch")
	ocrHTTPReq.Header.Set("X-Antigravity-Original-Method", "POST")

	ocrResp, errDo := s.client.Do(ocrHTTPReq)
	if errDo != nil {
		return ocrAttemptResult{err: fmt.Errorf("execute batch ocr request: %w", errDo)}
	}
	defer ocrResp.Body.Close()

	if ocrResp.StatusCode != http.StatusOK {
		errBytes, _ := io.ReadAll(ocrResp.Body)
		return ocrAttemptResult{err: fmt.Errorf("ocr batch service returned status %d: %s", ocrResp.StatusCode, string(errBytes))}
	}

	respBytes, _ := io.ReadAll(ocrResp.Body)
	var gemResp GeminiResponse
	if errUnmarshal := json.Unmarshal(respBytes, &gemResp); errUnmarshal != nil {
		return ocrAttemptResult{err: fmt.Errorf("unmarshal batch ocr response: %w", errUnmarshal)}
	}
	if len(gemResp.Candidates) == 0 || len(gemResp.Candidates[0].Content.Parts) == 0 {
		return ocrAttemptResult{err: fmt.Errorf("ocr batch response candidates are empty")}
	}
	// 批量响应可能跨多个 Part 输出(Gemini 偶尔把长文本分段),拼接全文再交 splitBatchOcrText。
	var sb strings.Builder
	for _, p := range gemResp.Candidates[0].Content.Parts {
		if p.Text != "" {
			sb.WriteString(p.Text)
		}
	}
	text := sb.String()
	if strings.TrimSpace(text) == "" {
		return ocrAttemptResult{err: fmt.Errorf("ocr batch response text is empty")}
	}
	return ocrAttemptResult{text: text}
}

// ocrBatchRouteAttempt 是非 Google 族批量「一次真打 18444 /route + 解析响应」的纯上游尝试,
// 供 batchUpstream 经 ocrCallWithRetry 重试调用。与 ocrRouteAttempt 同款链路,仅请求体为批量。
//
// 每次重试都新建独立 http.Request 并重设所有头(含 X-Antigravity-OCR-Self 自递归守卫头),
// 守卫语义在重试下不破。错误文本约定:非 200 → "ocr batch route service returned status %d: %s"。
func (s *OCRService) ocrBatchRouteAttempt(ctx context.Context, userSession *RelaySession, ocrReqBytes []byte) ocrAttemptResult {
	if s == nil || s.client == nil {
		return ocrAttemptResult{err: fmt.Errorf("ocrBatchRouteAttempt: nil service or client")}
	}
	if userSession == nil {
		return ocrAttemptResult{err: fmt.Errorf("ocrBatchRouteAttempt: nil session")}
	}
	ocrURL := fmt.Sprintf("http://%s/route/v1/chat/completions", localRelayAddr)
	ocrHTTPReq, errReq := http.NewRequestWithContext(ctx, http.MethodPost, ocrURL, bytes.NewReader(ocrReqBytes))
	if errReq != nil {
		return ocrAttemptResult{err: fmt.Errorf("create batch ocr route request: %w", errReq)}
	}
	ocrHTTPReq.Header.Set("Content-Type", "application/json")
	ocrHTTPReq.Header.Set("Authorization", "Bearer "+userSession.UserKey)
	ocrHTTPReq.Header.Set("X-Relay-User-Id", userSession.UserID)
	if userSession.APIKeyID != "" {
		ocrHTTPReq.Header.Set("X-Relay-Api-Key-Id", userSession.APIKeyID)
	}
	ocrHTTPReq.Header.Set("X-Antigravity-OCR-Self", "1")
	ocrHTTPReq.Header.Set("X-Antigravity-Original-Path", "/route/v1/chat/completions/ocr-batch")
	ocrHTTPReq.Header.Set("X-Antigravity-Original-Method", "POST")

	ocrResp, errDo := s.client.Do(ocrHTTPReq)
	if errDo != nil {
		return ocrAttemptResult{err: fmt.Errorf("execute batch ocr route request: %w", errDo)}
	}
	defer ocrResp.Body.Close()

	if ocrResp.StatusCode != http.StatusOK {
		errBytes, _ := io.ReadAll(ocrResp.Body)
		return ocrAttemptResult{err: fmt.Errorf("ocr batch route service returned status %d: %s", ocrResp.StatusCode, string(errBytes))}
	}

	respBytes, _ := io.ReadAll(ocrResp.Body)
	var openAIResp struct {
		Choices []struct {
			Message struct {
				Content interface{} `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if errUnmarshal := json.Unmarshal(respBytes, &openAIResp); errUnmarshal != nil {
		return ocrAttemptResult{err: fmt.Errorf("unmarshal batch ocr route response: %w", errUnmarshal)}
	}
	if len(openAIResp.Choices) == 0 {
		return ocrAttemptResult{err: fmt.Errorf("ocr batch route response choices are empty")}
	}
	text := contentToString(openAIResp.Choices[0].Message.Content)
	if strings.TrimSpace(text) == "" {
		return ocrAttemptResult{err: fmt.Errorf("ocr batch route response content is empty")}
	}
	return ocrAttemptResult{text: text}
}

// splitBatchOcrText 把批量上游响应按 [[图k]](k=1..n) 拆为 n 段纯文本(每段去首尾空白)。
//
// 标记格式:「[[图1]]」「[[图2]]」…「[[图n]]」,k 为 1 起。拆分规则:
//   - 必须能定位到所有 n 个标记(1..n 全到);任一缺失 → ok=false,segs 为空(交上层回退逐图);
//   - 第 k 段 = 第 k 个标记之后到第 k+1 个标记之前的内容(最后一段到文末);
//   - 每段去首尾空白;空串段保留(上层据此判该图 OCR 为空)。
//
// 实现按 marker 前缀顺序扫描:先找 [[图1]],再从其后找 [[图2]],依此类推。这样比正则更稳:
// 模型若把图序号写乱(如 [[图1]]..[[图3]]..[[图2]])会被「某序号找不到」判失败回退。
func splitBatchOcrText(text string, n int) (segs []string, ok bool) {
	if n <= 0 {
		return nil, false
	}
	segs = make([]string, 0, n)
	cursor := 0
	for k := 1; k <= n; k++ {
		marker := batchImageMarkerPrefix + fmt.Sprintf("%d]]", k)
		idx := strings.Index(text[cursor:], marker)
		if idx < 0 {
			return nil, false
		}
		segStart := cursor + idx + len(marker)
		// 段尾:下一个标记(若有的话)首次出现位置;末段到文末。
		var segEnd int
		if k < n {
			nextMarker := batchImageMarkerPrefix + fmt.Sprintf("%d]]", k+1)
			// 从 segStart 起找下一个标记;允许在段中间出现尚未消费的其它标记不干扰(顺序扫描保证)。
			nidx := strings.Index(text[segStart:], nextMarker)
			if nidx < 0 {
				return nil, false
			}
			segEnd = segStart + nidx
		} else {
			segEnd = len(text)
		}
		segs = append(segs, strings.TrimSpace(text[segStart:segEnd]))
		cursor = segEnd
	}
	if len(segs) != n {
		return nil, false
	}
	return segs, true
}
