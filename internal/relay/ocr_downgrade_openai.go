package relay

// ocr_downgrade_openai.go —— L2 协议适配层:OpenAI Chat (image_url) 入站图片本地 OCR 降级。
//
// 三层分离架构的 L2(OpenAI Chat 协议):覆盖第三方号池(DeepSeek / Moonshot / Qwen / 自建
// OpenAI 兼容网关)与 NVIDIA 池的 OpenAI Chat 入站。这些号池的目标模型大多不支持多模态,
// 入站若携带 image_url 数组形态的 content 块,直送上游会触发 400 或被当作字面字符串污染;
// 更关键的是本链路的 `ChatMessage.Content` 刻意是 string 且非 omitempty(见 nvidia_translate_types.go
// 的注释:NVIDIA/多数 OpenAI 兼容上游用 serde 反序列化要求每条 message 显式带 content 字段),
// 标准 json.Unmarshal 遇数组形态的 content 会直接拒收整条请求 → 客户端拿到 400。
//
// 因此本方法在 raw body 层完成降级:把"数组形态且至少含一个 image_url/input_image 块"的
// content 重写为单一纯字符串(合并该条消息里所有 text 块 + OCR 降级后的图片描述),让下游
// OpenAIChatRequest 的 Unmarshal 成功、上游段只见 text、零多模态负担。
//
// 与号池入口层解耦:passthroughForward(handleRoutedForward 调用)/ handleNvidia 的 OpenAI
// Chat 入站分支调本方法,本方法不感知号池身份,任何 OpenAI Chat 兼容入站均可复用。L1
// OCRService 只对本方法吐 base64/text,URL 抓取 / SSRF 防护 / urlCache 均内建在 L1 入口。
//
// 窗口语义与 DowngradeAnthropicImagesToText 一致:仅末尾 ocrRecentWindowMessages 条内的图
// 允许 cache miss 真打 OCR 上游;窗口外的图只查缓存(命中→复用历史文本,未命中→占位兜底),
// 避免 Claude Code 等无状态客户端每轮重发完整历史时把几十张老图重新 OCR 烧爆号池配额。
// 三态归一(base64 / Data URL / 网络 URL)统一走 L1 入口的 resolveB64NoFetch / resolveB64WithFetch,
// SSRF 防护与 urlCache 二级缓存复用全部内建其中,与 Anthropic 链路完全一致。
//
// 不破坏原则:content 为 string 或无 image 块的数组时原样返回入参 body,不触碰上游已可消化的
// 形态,零行为变更;仅在含 image_url/input_image 块时才重写对应消息的 content 为纯字符串。

import (
	"encoding/json"
	"strings"
)

// openAIImgBlockType 是 OpenAI Chat / Responses 协议中承载图片的 content 块类型。
// OpenAI Chat Completions Vision 用 "image_url";Responses API 用 "input_image"。
// 两者块内取 url 的形态不同(Chat 的 image_url 是 {url:"..."} 对象,Responses 的
// image_url 也可能是字符串),由 extractOpenAIImageUrl 统一兼容。
const (
	openAIImgBlockTypeChat     = "image_url"
	openAIImgBlockTypeResponses = "input_image"
)

// openAITextBlockTypes 是 OpenAI Chat / Responses 协议中承载文本的 content 块类型集合。
// 合并 content 时把这些块的 text 字段按原顺序拼回,保证用户文案不丢。
var openAITextBlockTypes = map[string]bool{
	"text":        true, // OpenAI Chat Completions
	"input_text":  true, // Responses API(user/input)
	"output_text": true, // Responses API(assistant/output)
}

// DowngradeOpenAIChatImagesToText 扫 OpenAI Chat Completions 请求体的 messages[].content,
// 把"数组形态且含 image_url/input_image 块"的 content 重写为单一纯字符串,文本块按原顺序
// 拼接,图片块用 L1 OCRService 降级为 OCR 描述文本(失败/窗外未命中 → 占位文本兜底)。
//
// 入参:
//   - bodyBytes: 入站 OpenAI Chat 请求体(可能含数组形态 content 的 messages);
//   - userSession: 会话信息,经 ocrOwnerKey 落会话级 OCR 缓存隔离键。
//
// 返回:
//   - newBody: 降级后的请求体。无图重写时 == bodyBytes(原样返回,零变更);
//     含图重写时为新的 JSON bytes(被改写消息的 content 已是纯字符串,下游 Unmarshal 成功);
//   - replaced: 成功降级为 OCR 描述文本的 image 块数(不含占位兜底的块);
//   - lastErr: OCR 过程遇到的最后一个错误(若有),不中止链路;
//   - ocrHits/ocrMisses/ocrSkipped: 与 DowngradeAnthropicImagesToText 同口径
//     (命中=缓存直接返回;未命中=窗内真打上游;窗外占位=窗外图缓存未命中走 placeholder)。
//
// 解析失败(body 非 JSON 对象 / 无 messages / messages 非数组)时原样返回,绝不阻断主请求。
//
// 多图批量优化(真·单请求批量):一条消息内 ≥2 张窗内 miss 图合并进一次上游 OCR 调用,
// 模型按 [[图k]] 标记顺序输出,回译拆分后逐张回填 segment 列表与缓存,把 N 次上游降到 1 次。
// 仅窗内 miss 图参与批量(cache hit / 窗外图路径完全不动);=1 张窗内图仍走原 OcrImage 单图路径
// (零回归,所有单图现有测试与体感不变);拆分失败整体回退逐图(由 L1 内部兜底,结果质量不劣于现状)。
// 详见 ocr_batch.go 与 plans/cryptic-coalescing-mccarthy.md。
func (s *OCRService) DowngradeOpenAIChatImagesToText(bodyBytes []byte, userSession *RelaySession) (newBody []byte, replaced int, lastErr error, ocrHits, ocrMisses, ocrSkipped int) {
	if s == nil || len(bodyBytes) == 0 {
		return bodyBytes, 0, nil, 0, 0, 0
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(bodyBytes, &obj); err != nil {
		return bodyBytes, 0, nil, 0, 0, 0
	}
	rawMsgs, ok := obj["messages"]
	if !ok {
		// 无 messages 字段(如纯 Responses 形态走 input[],或异常体)→ 不归本方法管,原样返回。
		return bodyBytes, 0, nil, 0, 0, 0
	}
	var msgs []map[string]json.RawMessage
	if err := json.Unmarshal(rawMsgs, &msgs); err != nil || len(msgs) == 0 {
		return bodyBytes, 0, nil, 0, 0, 0
	}

	// 窗口语径与 Anthropic 链路一致:末尾 N 条内允许 miss 真打上游,窗外只查缓存。
	msgCount := len(msgs)
	windowStart := 0
	if msgCount > ocrRecentWindowMessages {
		windowStart = msgCount - ocrRecentWindowMessages
	}

	ocrModel := s.getOcrModel()
	anyChanged := false
	for i := range msgs {
		inWindow := i >= windowStart
		rawContent, ok := msgs[i]["content"]
		if !ok || len(rawContent) == 0 {
			continue
		}
		trimmed := strings.TrimSpace(string(rawContent))
		if trimmed == "" || trimmed == "null" || trimmed[0] != '[' {
			// string 形态(或空/null)的 content:无 image 块,不处理。
			continue
		}
		var blocks []map[string]interface{}
		if err := json.Unmarshal(rawContent, &blocks); err != nil {
			continue
		}
		// 仅当数组里至少含一个图片块时才重写;否则原样保留(不破坏上游可消化的非图数组)。
		hasImg := false
		for _, b := range blocks {
			if isOpenAIImageBlock(b) {
				hasImg = true
				break
			}
		}
		if !hasImg {
			continue
		}
		anyChanged = true

		// 收集本消息内的文本作为 OCR 上下文(与 Anthropic 链路一致,优先本消息文案)。
		userPromptCtx := collectOpenAIUserText(blocks)
		// 本消息无文本时回望前序 user 消息补文案(与 Anthropic 链路一致)。
		if strings.TrimSpace(userPromptCtx) == "" {
			userPromptCtx = collectPrevOpenAIUserText(msgs, i)
		}

		// openAISeg 是最终 merged(以 \n 拼接)里的一个有序段:文本块直接定稿 text;
		// 图片块的窗内 miss 候选先标 defer=true、text 留空,第二遍回填后 text 落 OCR 描述/占位。
		// 严格保持 content 数组原顺序(只对"产生输出"的块建段:非空文本块 / 图片块),
		// 与原实现的跳过空文本、跳过非图非文本块的体感逐行一致。
		type openAISeg struct {
			text  string
			defer_ bool // true=第二遍回填(窗内 miss 候选),text 字段此时为空待填
			b64   string
			mime  string
		}
		segs := make([]openAISeg, 0, len(blocks))
		// 第一遍:逐块走文本直拼 / 图片解析。文本与窗外/失败图片当场定稿(写进 segs),
		// 窗内 miss 图片留 defer=true 空段,交第二遍回填。
		for _, b := range blocks {
			t, _ := b["type"].(string)
			if openAITextBlockTypes[t] {
				if txt, ok := b["text"].(string); ok && txt != "" {
					segs = append(segs, openAISeg{text: txt})
				}
				// 空文本块:不产生输出段,跳过(与原实现一致)。
				continue
			}
			if t != openAIImgBlockTypeChat && t != openAIImgBlockTypeResponses {
				// 非文本非图片块(如 input_audio 等):本链路上游不支持,降级时丢弃以保 content 纯字符串。
				continue
			}
			// 图片块:三态归一到 AnthropicImageSource,复用 L1 入口解析层。
			src := buildAnthropicImageSourceFromOpenAIBlock(b)
			if inWindow {
				b64, imgMime, ferr := s.resolveB64WithFetch(src)
				if ferr != nil || strings.TrimSpace(b64) == "" {
					// 解析/下载失败 → 当场定稿占位(不进批量),与单图路径一致。
					segs = append(segs, openAISeg{text: imageNotExtractablePlaceholder})
					if ferr != nil {
						lastErr = ferr
					}
					continue
				}
				// 窗内 miss 候选:留 defer 空段,交第二遍批量/单图回填。
				segs = append(segs, openAISeg{defer_: true, b64: b64, mime: imgMime})
			} else {
				// 窗外:仅查缓存,绝不真打上游。
				b64, _, ok := s.resolveB64NoFetch(src)
				if !ok {
					// 窗外未解析出 b64 → 占位,不计 replaced 也不计 skipped(与单图路径一致)。
					segs = append(segs, openAISeg{text: imageNotExtractablePlaceholder})
					continue
				}
				cachedText, hit := s.OcrImageCacheOnlyLookup(userSession, b64)
				if hit && strings.TrimSpace(cachedText) != "" {
					segs = append(segs, openAISeg{text: nvidiaImageOcrDescHeader(ocrModel, cachedText)})
					replaced++
					ocrHits++
				} else {
					segs = append(segs, openAISeg{text: imageNotExtractablePlaceholder})
					ocrSkipped++
				}
			}
		}

		// 第二遍:对窗内 miss 候选批量/单图 OCR 并按序回填对应 seg 的 text。
		// 先收集所有 defer 段的下标(即 content 数组里窗内 miss 图的原顺序),再按其数量
		// 走 =1 单图 / ≥2 批量,保证 content 数组原顺序零错位。
		var deferIdx []int
		for k, seg := range segs {
			if seg.defer_ {
				deferIdx = append(deferIdx, k)
			}
		}
		if len(deferIdx) == 1 {
			k := deferIdx[0]
			seg := &segs[k]
			ocrText, ocrErr, cachedHit := s.OcrImage(userSession, seg.b64, seg.mime, userPromptCtx)
			if cachedHit {
				ocrHits++
			} else {
				ocrMisses++
			}
			if ocrErr != nil || strings.TrimSpace(ocrText) == "" {
				if ocrErr != nil {
					lastErr = ocrErr
				}
				seg.text = imageNotExtractablePlaceholder
			} else {
				seg.text = nvidiaImageOcrDescHeader(ocrModel, ocrText)
				replaced++
			}
		} else if len(deferIdx) >= 2 {
			// ≥2 张:构造批量入参,一次 OcrImageBatch 合并上游。
			// OcrImageBatch 内部先逐项查缓存(命中即返 CachedHit=true,不进批量),仅把 miss 项
			// 按消息合并一次上游;成功拆分逐张写 success 长 TTL,拆分失败整体回退逐图(调原 OcrImage)。
			// L2 只消费返回的 []OcrBatchResult(逐项对齐),按 CachedHit 计 ocrHits/ocrMisses、
			// 按 Ok/Text/Err 写回(描述 / 占位),口径与单图路径完全一致。
			items := make([]OcrBatchItem, len(deferIdx))
			for k, idx := range deferIdx {
				seg := &segs[idx]
				items[k] = OcrBatchItem{B64: seg.b64, Mime: seg.mime, PromptCtx: userPromptCtx}
			}
			results := s.OcrImageBatch(userSession, items)
			for k, res := range results {
				seg := &segs[deferIdx[k]]
				if res.CachedHit {
					ocrHits++
				} else {
					ocrMisses++
				}
				if res.Err != nil || strings.TrimSpace(res.Text) == "" {
					if res.Err != nil {
						lastErr = res.Err
					}
					seg.text = imageNotExtractablePlaceholder
					continue
				}
				seg.text = nvidiaImageOcrDescHeader(ocrModel, res.Text)
				replaced++
			}
		}

		// 按原顺序合并所有段(文本块 + 图片块描述/占位),\n 分隔。
		var merged strings.Builder
		for k, seg := range segs {
			if seg.text == "" {
				// defer 段理论上必被第二遍回填;text 仍空则补占位兜底(防御)。
				seg.text = imageNotExtractablePlaceholder
				segs[k].text = seg.text
			}
			if merged.Len() > 0 {
				merged.WriteString("\n")
			}
			merged.WriteString(seg.text)
		}
		// content 重写为合并后的纯字符串。即便图片全部走占位,也保留文本块 + 占位文案,绝不丢原文。
		newContent, merr := json.Marshal(merged.String())
		if merr != nil {
			// 理论不至:merged 是 string,Marshal 必成功;兜底防御:跳过本条,不改 content。
			continue
		}
		msgs[i]["content"] = newContent
	}

	if !anyChanged {
		return bodyBytes, 0, lastErr, ocrHits, ocrMisses, ocrSkipped
	}
	newMsgs, merr := json.Marshal(msgs)
	if merr != nil {
		return bodyBytes, replaced, lastErr, ocrHits, ocrMisses, ocrSkipped
	}
	obj["messages"] = newMsgs
	out, err := json.Marshal(obj)
	if err != nil {
		return bodyBytes, replaced, lastErr, ocrHits, ocrMisses, ocrSkipped
	}
	return out, replaced, lastErr, ocrHits, ocrMisses, ocrSkipped
}

// isOpenAIImageBlock 判定 content 块是否为图片块(image_url / input_image)。
func isOpenAIImageBlock(b map[string]interface{}) bool {
	t, _ := b["type"].(string)
	return t == openAIImgBlockTypeChat || t == openAIImgBlockTypeResponses
}

// extractOpenAIImageUrl 从图片块里取 url,兼容三种形态:
//   - Chat Completions:image_url 为 {"url":"..."} 对象;
//   - 兼容非标:image_url 为字符串;
//   - Responses:顶层 "url" 字段。
func extractOpenAIImageUrl(b map[string]interface{}) string {
	if iu, ok := b["image_url"]; ok {
		switch v := iu.(type) {
		case string:
			return v
		case map[string]interface{}:
			if u, ok := v["url"].(string); ok {
				return u
			}
		}
	}
	if u, ok := b["url"].(string); ok {
		return u
	}
	return ""
}

// buildAnthropicImageSourceFromOpenAIBlock 把 OpenAI 图片块归一为 AnthropicImageSource,
// 复用 L1 入口的 resolveB64NoFetch / resolveB64WithFetch(三态归一 + SSRF + urlCache)。
// Type 字段标注 url/base64 供 resolve 层分流,MediaType 留空让 resolve 层按 data URL / 下载
// 结果填入(空时 resolve 层兜底 image/jpeg)。
func buildAnthropicImageSourceFromOpenAIBlock(b map[string]interface{}) *AnthropicImageSource {
	urlStr := strings.TrimSpace(extractOpenAIImageUrl(b))
	if urlStr == "" {
		return nil
	}
	src := &AnthropicImageSource{MediaType: ""}
	switch {
	case strings.HasPrefix(strings.ToLower(urlStr), "data:"):
		// Data URL(data:image/..;base64,..):resolve 层 parseDataURL 提取 mime + payload。
		src.Type = "base64"
		src.Data = urlStr
	case looksLikeHTTPURL(urlStr):
		// 网络 URL:resolve 层查 urlCache / SSRF 下载。
		src.Type = "url"
		src.Url = urlStr
	default:
		// 裸 base64:resolve 层直接用(兜底 image/jpeg)。
		src.Type = "base64"
		src.Data = urlStr
	}
	return src
}

// collectOpenAIUserText 收集一条消息 content 数组里所有文本块的 text,按顺序拼接(换行分隔)。
// 供 OCR 作为用户提问上下文,与 Anthropic 链路 userTextBuilder 同口径。
func collectOpenAIUserText(blocks []map[string]interface{}) string {
	var sb strings.Builder
	for _, b := range blocks {
		t, _ := b["type"].(string)
		if !openAITextBlockTypes[t] {
			continue
		}
		if txt, ok := b["text"].(string); ok && txt != "" {
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(txt)
		}
	}
	return sb.String()
}

// collectPrevOpenAIUserText 回望 i 之前最近一条 user 消息,以其文本作为 OCR 上下文兜底。
// 处理两种 content 形态:string(直接当文本) / 数组(拼接文本块)。与 Anthropic 链路 prev
// 回望同口径,仅兼容 OpenAI 的双形态 content。
func collectPrevOpenAIUserText(msgs []map[string]json.RawMessage, i int) string {
	for prev := i - 1; prev >= 0; prev-- {
		var role string
		if r, ok := msgs[prev]["role"]; ok {
			_ = json.Unmarshal(r, &role)
		}
		if strings.ToLower(strings.TrimSpace(role)) != "user" {
			continue
		}
		pc, ok := msgs[prev]["content"]
		if !ok {
			continue
		}
		ptrim := strings.TrimSpace(string(pc))
		if ptrim == "" || ptrim == "null" {
			continue
		}
		if ptrim[0] == '[' {
			var pblocks []map[string]interface{}
			if err := json.Unmarshal(pc, &pblocks); err != nil {
				continue
			}
			if txt := collectOpenAIUserText(pblocks); txt != "" {
				return txt
			}
			continue
		}
		var txt string
		if err := json.Unmarshal(pc, &txt); err == nil && txt != "" {
			return txt
		}
	}
	return ""
}
