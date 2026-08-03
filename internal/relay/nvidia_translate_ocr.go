package relay

import (
	"fmt"
	"strings"
)

// nvidia_translate_ocr.go: 入站 image 块本地 Gemini OCR 降级(原地改写为 text 块)。
// 从 nvidia_translate.go 拆分而出,仅作物理搬移,逻辑与原文件逐行等价。

// nvidiaImageOcrDescHeader 把 OCR 识别出的纯文本包装成一段带上下文标记的描述,
// 供 downgradeAnthropicImagesToText 把 image 块原地改写为 text 块时使用。
//
// ocrModel 参数化:文案里展示真实使用的 OCR 模型,前端改模型后文案随之变化,
// 与 compat.go Gemini 入站自愈链路的 inline 文案语义完全一致。
func nvidiaImageOcrDescHeader(ocrModel, ocrText string) string {
	if strings.TrimSpace(ocrModel) == "" {
		ocrModel = "gemini-2.5-flash"
	}
	return fmt.Sprintf("\n\n[本地中继服务已自动调用 %s 协助分析了用户发送的截图，内容提取如下：]\n%s\n[图片分析内容结束]\n", ocrModel, ocrText)
}

// imageNotExtractablePlaceholder 用于 image 块无法本地 OCR 时的占位文本(url 类型、
// 空数据、或 OCR 服务不可达)。绝不阻断主请求,确保用户至少能看到"此处有图但未能识别"的信号。
const imageNotExtractablePlaceholder = "[用户发送了一张图片，但本地中继未能识别其内容（OCR 不可用或图源不可直取），请提示用户改用 analyze_clipboard_image 工具或补充文字描述]"

// ocrRecentWindowMessages 是 downwards 降级时"真打上游 OCR"的消息窗口口径。
// 仅 req.Messages 末尾 N 条内的图片在 cache miss 时才真打 gemini 上游;窗口之外的图片
// 只查缓存(命中→复用历史 OCR 文本,未命中→占位文本兜底,绝不重新 OCR)。
//
// 取 10 的依据:Claude Code 客户端无状态,每轮重发完整历史;若窗口全开就会把几十张老图
// 全部重新 OCR 烧爆配额,若窗口过小则用户在长会话里翻回去追问老图时无法复用 OCR 结果。
// 10 条覆盖大多数追问场景下"用户当前关注的消息段",与前端历史面板可视线量级匹配,
// 同时给缓存(成功 TTL 24h)+ LRU 容量上限留出回收空间,兼顾实时性与配额成本。
const ocrRecentWindowMessages = 10

// downgradeAnthropicImagesToText 扫 AnthropicRequest 所有消息的 content 块,
// 把 type=="image" 的块用本地 Gemini OCR 降级:OCR 成功则替换为
// [{"type":"text","text":"<OCR 识别文本>"}];OCR 失败则替换为占位文本。
// 绝不向 NVIDIA 上游直送 image_url,避免非多模态模型解析失败(400)。
//
// 设计要点:
//   - 原地替换 block(blocks[bi]),不动数组顺序、不增删 block,保证 [Image #N] 芯片
//     与 text/tool_result 块的相对位置不变,后续 AnthropicToOpenAIChat 的 text 合并 +
//     tool_result 拆分逻辑零变更。
//   - 降级后 Type=="text"、Source 置空 → AnthropicToOpenAIChat 走 case "text" 正常消化,
//     ChatMessage.Content 永远是 string,上游段零侵入。
//   - 失败不中止:返回 error 供调用方记日志,但仍把 block 改写成占位文本后继续,优先保证可用性。
//
// 返回:成功降级的 image 块数 + 遇到的最后一个错误(若有) + ocrHits/ocrMisses/ocrSkipped。
// ocrHits   = 命中缓存直接返回(窗口内命中,纳秒级不烧配额) + 窗口外缓存复用的总数;
// ocrMisses = 窗口内 cache miss 真打 gemini 上游的图数(含成功与失败);
// ocrSkipped = 窗口外图缓存未命中 → 走占位文本兜底的块数(绝不重新 OCR,省配额)。
//
// 最近 N 条消息窗口:仅对 req.Messages 末尾 ocrRecentWindowMessages 条内的图片走"miss 即真打上游";
// 窗口之外的图片只查缓存(ocrImageCacheOnlyLookup):命中则复用历史 OCR 文本(不烧配额),
// 未命中写占位文本兜底。这样既防 NVIDIA 上游 400(永远只见 text 块),又避免 Claude Code 每轮
// 重发完整历史时把几十张老图全部重新 OCR 烧爆 antigravity 号池配额。
func (h *APICompatHandler) downgradeAnthropicImagesToText(req *AnthropicRequest, userSession *RelaySession) (replaced int, lastErr error, ocrHits, ocrMisses, ocrSkipped int) {
	if req == nil {
		return 0, nil, 0, 0, 0
	}
	// 窗口起点:消息数 <= 窗口口径时全覆盖;> 窗口口径时只覆盖末尾 N 条,前序消息内的图视为"窗外"。
	msgCount := len(req.Messages)
	windowStart := 0
	if msgCount > ocrRecentWindowMessages {
		windowStart = msgCount - ocrRecentWindowMessages
	}
	var ocrModel string
	for mi := range req.Messages {
		inWindow := mi >= windowStart
		// 收集同消息或上下文的用户文案
		var userTextBuilder strings.Builder
		for _, b := range req.Messages[mi].Content {
			if b.Type == "text" && b.Text != "" {
				if userTextBuilder.Len() > 0 {
					userTextBuilder.WriteString("\n")
				}
				userTextBuilder.WriteString(b.Text)
			}
		}
		if userTextBuilder.Len() == 0 && mi > 0 {
			for prev := mi - 1; prev >= 0; prev-- {
				if req.Messages[prev].Role == "user" {
					for _, b := range req.Messages[prev].Content {
						if b.Type == "text" && b.Text != "" {
							if userTextBuilder.Len() > 0 {
								userTextBuilder.WriteString("\n")
							}
							userTextBuilder.WriteString(b.Text)
						}
					}
					if userTextBuilder.Len() > 0 {
						break
					}
				}
			}
		}
		userPromptCtx := userTextBuilder.String()

		blocks := req.Messages[mi].Content
		for bi := range blocks {
			if blocks[bi].Type != "image" || blocks[bi].Source == nil {
				continue
			}
			src := blocks[bi].Source
			mime := src.MediaType
			if mime == "" {
				mime = "image/jpeg"
			}
			// 非 base64(如 url 类型)本机无法直取 → 占位文本兜底,不调 OCR,不计数。
			if src.Type != "base64" || src.Data == "" {
				blocks[bi].Source = nil
				blocks[bi].Type = "text"
				blocks[bi].Text = imageNotExtractablePlaceholder
				continue
			}
			// 窗外历史图:只查缓存复用,绝不重新打上游。命中→复用历史 OCR 文本(replaced+1);
			// 未命中→占位文本兜底,跳过(ocrSkipped+1),省下昂贵的 antigravity 号池 ORC 配额。
			if !inWindow {
				cachedText, hit := h.ocrImageCacheOnlyLookup(userSession, src.Data, userPromptCtx)
				if hit && strings.TrimSpace(cachedText) != "" {
					if ocrModel == "" {
						ocrModel = h.getOcrModel()
					}
					blocks[bi].Source = nil
					blocks[bi].Type = "text"
					blocks[bi].Text = nvidiaImageOcrDescHeader(ocrModel, cachedText)
					replaced++
					ocrHits++
				} else {
					blocks[bi].Source = nil
					blocks[bi].Type = "text"
					blocks[bi].Text = imageNotExtractablePlaceholder
					ocrSkipped++
				}
				continue
			}
			// 窗内图:走完整缓存+singleflight+miss 真打上游链路。
			ocrText, ocrErr, cachedHit := h.ocrImageViaLocalGemini(userSession, src.Data, mime, userPromptCtx)
			if cachedHit {
				ocrHits++
			} else {
				ocrMisses++
			}
			if ocrErr != nil || strings.TrimSpace(ocrText) == "" {
				lastErr = ocrErr
				blocks[bi].Source = nil
				blocks[bi].Type = "text"
				blocks[bi].Text = imageNotExtractablePlaceholder
				continue
			}
			if ocrModel == "" {
				ocrModel = h.getOcrModel()
			}
			blocks[bi].Source = nil
			blocks[bi].Type = "text"
			blocks[bi].Text = nvidiaImageOcrDescHeader(ocrModel, ocrText)
			replaced++
		}
	}
	return replaced, lastErr, ocrHits, ocrMisses, ocrSkipped
}

