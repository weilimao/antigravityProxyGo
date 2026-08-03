package relay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// compat_ocr.go: 入站 image 块本地 Gemini OCR 自愈降级
// (getOcrModel/ocrImageViaLocalGemini/ocrOwnerKey/ocrSessionDisplay/
//  ocrImageCacheOnlyLookup/ocrImageViaLocalGeminiUncached)。
// 从 compat.go 拆分而出,仅作物理搬移,逻辑与原文件逐行等价。
// 依赖 compat.go 包级 const defaultOcrModel 与 var localProxyAddr(留主文件,跨文件可见)。

// settingsMgr 为 nil(relay 单测未注入)时回退 defaultOcrModel,保持旧行为;
// 配置空值时 settings 层已兜底,这里双保险再判一次。
func (h *APICompatHandler) getOcrModel() string {
	if h == nil || h.settingsMgr == nil {
		return defaultOcrModel
	}
	m := strings.TrimSpace(h.settingsMgr.GetOcrModel())
	if m == "" {
		return defaultOcrModel
	}
	return m
}

// ocrImageViaLocalGemini 调用本地 Gemini(默认 gemini-2.5-flash,前端可配)对一张 base64 图做 OCR,
// 返回识别出的纯文本。失败返回 "" + error。
//
// 抽出供两条入站链路复用,避免各写一份:
//   - Gemini 入站自愈(dispatchToGemini 内,目标模型非 gemini 时图转文)
//   - NVIDIA 入站 image 降级(handleNvidia 内,Anthropic image block 本地 OCR 抹成 text)
//
// 缓存策略(ocrCache + singleflight):
//   - 键 = UserKey|ocrModel|sha256(b64)[:16],按用户与 OCR 模型双重隔离;
//   - 命中即返回历史 OCR 文本,跳过 gemini 调用与 ~3s 延迟;
//   - miss 时 singleflight 合并同图并发为 1 次真上游调用,防缓存击穿;
//   - OCR 失败也缓存(短 TTL),熔断窗口内不再重打挂的 OCR 服务;
//   - 切换 ocrModel 后键变化,自动重新 OCR 新模型(配置改了立刻生效)。
// nil ocrCache 降级为零缓存(纯走上游),保持旧行为兼容,供单测或异常注入用。
//
// 返回的 cachedHit 标识本次结果是否来自缓存命中(true=命中即返,未触达上游;
// false=cache miss 真打了一次 gemini 上游,或 nil 缓存的纯走上游场景)。
// 供调用方(downgradeAnthropicImagesToText / dispatchToGemini)在日志里透出
// "本轮这张图是命中还是重新 OCR",消除"每次请求都打印 OCR 降级"日志的歧义。
func (h *APICompatHandler) ocrImageViaLocalGemini(userSession *RelaySession, b64Data string, mimeType string, userPromptText ...string) (text string, err error, cachedHit bool) {
	if h == nil || userSession == nil {
		return "", fmt.Errorf("ocrImageViaLocalGemini: nil handler or session"), false
	}
	if strings.TrimSpace(b64Data) == "" {
		return "", fmt.Errorf("ocrImageViaLocalGemini: empty image data"), false
	}
	if mimeType == "" {
		mimeType = "image/jpeg"
	}
	ocrModel := h.getOcrModel()

	promptCtx := ""
	if len(userPromptText) > 0 {
		promptCtx = strings.TrimSpace(userPromptText[0])
	}

	// 缓存键首维:会话级隔离键(sessionKey 非空时优先,粒度比 UserKey 更细,按会话隔离;
	// 空则回退 UserKey,保持单测/未传场景的旧行为兼容)。sessionKey 由调用方(handleNvidia /
	// dispatchToGemini)经 ExtractSessionKey 算出,与 antigravity 链路同款口径,使同用户不同会话
	// 不共享缓存槽,且日志里能看到一致的会话 ID。
	ownerKey := ocrOwnerKey(userSession)

	// 命中缓存直接返回,跳过 gemini 调用与 ~3s 延迟(含失败条目短 TTL 熔断)。
	if h.ocrCache != nil {
		key := ocrCacheKey(ownerKey, ocrModel, b64Data, promptCtx)
		if e, ok := h.ocrCache.get(key); ok {
			h.ocrCounters.hits.Add(1)
			return e.text, e.err, true
		}
		h.ocrCounters.misses.Add(1)
	}

	// singleflight:同步相邻并发对同图(同模型)的请求,首调用真打上游,其余阻塞等待结果共享。
	callKey := ocrCacheKey(ownerKey, ocrModel, b64Data, promptCtx)
	v, callErr, _ := h.ocrInflight.Do(callKey, func() (interface{}, error) {
		text, err := h.ocrImageViaLocalGeminiUncached(userSession, b64Data, mimeType, ocrModel, promptCtx)
		ok := err == nil && strings.TrimSpace(text) != ""
		if h.ocrCache != nil {
			// 成功长 TTL / 失败短 TTL;失败也缓存命中即返回 err,避免熔断窗口内重打挂的服务。
			cachedText := text
			if !ok {
				cachedText = ""
			}
			h.ocrCache.set(callKey, cachedText, err, ok)
		}
		return text, err
	})
	if callErr != nil {
		return "", callErr, false
	}
	text, _ = v.(string)
	return text, nil, false
}

// ocrOwnerKey 返回 OCR 缓存键的首维隔离键:会话级 sessionKey 优先,空则回退 UserKey。
// 抽出便于 ocrImageViaLocalGemini 与 ocrImageCacheOnlyLookup 共享一致的取键口径。
// 设计:sessionKey 非空 → 按会话隔离(同用户多会话不共享缓存,语义更准且与日志会话 ID 对齐);
//      sessionKey 空 → 回退 UserKey(单测与未传 sessionKey 的旧调用,行为不变)。
func ocrOwnerKey(userSession *RelaySession) string {
	if userSession == nil {
		return ""
	}
	if strings.TrimSpace(userSession.SessionKey) != "" {
		return userSession.SessionKey
	}
	return userSession.UserKey
}

// ocrSessionDisplay 返回日志里展示的会话 ID:优先 userSession.SessionKey(由 handleNvidia 入口
// 经 ExtractSessionKey + auth:acc: 前缀算出,与 antigravity 号池链路同款口径),空则回退 UserID,
// 再空则 "-"。供 NVIDIA 选号/降级日志透出"哪个会话在打",便于排查同用户多会话的缓存隔离与配额归属。
func ocrSessionDisplay(userSession *RelaySession) string {
	if userSession == nil {
		return "-"
	}
	if k := strings.TrimSpace(userSession.SessionKey); k != "" {
		return k
	}
	if u := strings.TrimSpace(userSession.UserID); u != "" {
		return u
	}
	return "-"
}

// ocrImageCacheOnlyLookup 仅查 OCR 缓存,命中返回历史 OCR 文本(true),未命中返回("",false)。
// 绝不触达 singleflight / gemini 上游,供"最近 N 条消息窗口"之外的图片块复用:
//   - 命中(图在窗口内 OCR 过且仍驻留 LRU)→ 复用历史文本,不烧 antigravity 配额;
//   - 未命中 → 调用方写 imageNotExtractablePlaceholder 占位文本,绝不重新 OCR。
// 与 ocrImageViaLocalGemini 共享 ownerKey(会话级)+ ocrModel + 图指纹 + promptCtx 四维键,
// 故窗口内 OCR 入缓存的图,被推出窗口后仍可被本方法命中复用。
func (h *APICompatHandler) ocrImageCacheOnlyLookup(userSession *RelaySession, b64Data string, userPromptText ...string) (string, bool) {
	if h == nil || userSession == nil || h.ocrCache == nil {
		return "", false
	}
	if strings.TrimSpace(b64Data) == "" {
		return "", false
	}
	ocrModel := h.getOcrModel()
	promptCtx := ""
	if len(userPromptText) > 0 {
		promptCtx = strings.TrimSpace(userPromptText[0])
	}
	key := ocrCacheKey(ocrOwnerKey(userSession), ocrModel, b64Data, promptCtx)
	if e, ok := h.ocrCache.get(key); ok && e.ok && strings.TrimSpace(e.text) != "" {
		// 仅复用成功条目;失败短 TTL 条目不在此复用(让调用方走占位,语义更清晰)。
		h.ocrCounters.hits.Add(1)
		return e.text, true
	}
	return "", false
}

// ocrImageViaLocalGeminiUncached 是 ocrImageViaLocalGemini 的纯上游调用实现,
// 无缓存、无 singleflight,纯粹把 base64 发给 18443 的指定 Gemini 模型跑 OCR。
// 抽出来便于 (a) 缓存层 miss 后复用 (b) 单测直接打 mock 校验上游请求形态。
// ocrModel 由调用方传入(取自 h.getOcrModel()),用于动态拼写 18443 URL,默认 gemini-2.5-flash。
func (h *APICompatHandler) ocrImageViaLocalGeminiUncached(userSession *RelaySession, b64Data string, mimeType string, ocrModel string, userPromptText ...string) (string, error) {
	if strings.TrimSpace(ocrModel) == "" {
		ocrModel = defaultOcrModel
	}

	promptCtx := ""
	if len(userPromptText) > 0 {
		promptCtx = strings.TrimSpace(userPromptText[0])
	}

	var ocrPrompt string
	if promptCtx != "" {
		ocrPrompt = fmt.Sprintf("你是一个顶级的多模态视觉分析助手。请深入分析图片内容并准确提取关键信息。\n\n【用户提问上下文】：用户在发送此图片时附带的提问/说明文本为：\n\"%s\"\n\n请按以下要求分析：\n1. [重点靶向分析]：结合上述用户的提问与关注点，重点分析图片中与问题相关的代码行、报错提示、界面元素或逻辑关系。\n2. [文字与代码精准提取 (OCR)]：\n   - 原样逐字提取图中涉及的代码、控制台报错或文本，保持原始缩进与排版，不要自动修正错别字，用 Markdown 代码块包裹。\n3. [画面结构与视觉布局]：描述界面 UI 状态、高亮提示框或图表节点关系。\n4. [输出要求]：直接输出结构化结果，严禁包含任何前言或客套话。", promptCtx)
	} else {
		ocrPrompt = "你是一个顶级的多模态视觉分析助手。请深入分析图片内容并准确提取关键信息。要求如下：\n1. [图像总体概览]：用一句话概括图片类型（如：IDE代码截图、控制台报错、UI界面、架构流程图等）及核心意图。\n2. [文字与代码精准提取 (OCR)]：\n   - 提取图中所有的文本、代码、终端命令与报错堆栈。\n   - 代码与报错日志必须【原样逐字提取】，严格保留缩进、换行与标点符号，严禁自动修改拼写错误。使用 Markdown 代码块包裹。\n3. [视觉布局与逻辑关系]：\n   - 若包含 UI 界面，注明高亮项、报错弹窗或按钮状态；\n   - 若包含流程图/表格，还原节点间的关系或表格数据。\n4. [输出要求]：直接输出结构化的提取与分析结果，不要包含任何前言、引言或客套话。"
	}

	ocrReq := GeminiRequest{
		Contents: []GeminiContent{
			{
				Role: "user",
				Parts: []GeminiPart{
					{Text: ocrPrompt},
					{InlineData: &GeminiBlob{MimeType: mimeType, Data: b64Data}},
				},
			},
		},
	}
	ocrReqBytes, errMarshal := json.Marshal(ocrReq)
	if errMarshal != nil {
		return "", fmt.Errorf("marshal ocr request: %w", errMarshal)
	}

	// 模型名参数化:默认 gemini-2.5-flash,前端可改任意 Gemini 系模型。
	ocrURL := fmt.Sprintf("http://%s/v1beta/models/%s:generateContent", localProxyAddr, ocrModel)
	ocrHTTPReq, errReq := http.NewRequest(http.MethodPost, ocrURL, bytes.NewReader(ocrReqBytes))
	if errReq != nil {
		return "", fmt.Errorf("create ocr request: %w", errReq)
	}
	ocrHTTPReq.Header.Set("Content-Type", "application/json")
	ocrHTTPReq.Header.Set("Authorization", "Bearer "+userSession.UserKey)
	ocrHTTPReq.Header.Set("X-Relay-User-Id", userSession.UserID)
	if userSession.APIKeyID != "" {
		ocrHTTPReq.Header.Set("X-Relay-Api-Key-Id", userSession.APIKeyID)
	}
	ocrHTTPReq.Header.Set("X-Antigravity-Original-Path", "/v1internal:generateContent/ocr-fallback")
	ocrHTTPReq.Header.Set("X-Antigravity-Original-Method", "POST")

	ocrResp, errDo := h.client.Do(ocrHTTPReq)
	if errDo != nil {
		return "", fmt.Errorf("execute ocr request: %w", errDo)
	}
	defer ocrResp.Body.Close()

	if ocrResp.StatusCode != http.StatusOK {
		errBytes, _ := io.ReadAll(ocrResp.Body)
		return "", fmt.Errorf("ocr service returned status %d: %s", ocrResp.StatusCode, string(errBytes))
	}

	respBytes, _ := io.ReadAll(ocrResp.Body)
	var gemResp GeminiResponse
	if errUnmarshal := json.Unmarshal(respBytes, &gemResp); errUnmarshal != nil {
		return "", fmt.Errorf("unmarshal ocr response: %w", errUnmarshal)
	}
	if len(gemResp.Candidates) == 0 || len(gemResp.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("ocr response candidates are empty")
	}
	return gemResp.Candidates[0].Content.Parts[0].Text, nil
}

