package relay

// ocr_downgrade_gemini.go —— L2 协议适配层:Gemini InlineData 图片本地 OCR 降级。
//
// 三层分离架构的 L2(Gemini 协议):Gemini 入站请求中,当目标模型不支持多模态(非 gemini 系)
// 但请求却含 InlineData 图片时,用 L1 OCRService 把每张图 OCR 成纯文本,把 InlineData 部分
// 原地改为 Text 部分并拼接识别结果,再送上游。与号池入口层解耦:由 dispatchToGemini 调用。
//
// 历史背景:本逻辑原内联在 APICompatHandler.dispatchToGemini(compat_dispatch.go:244-306),
// 没有抽成函数、不可被第三方号池复用且不可独立单测。现抽为 *OCRService 方法,行为逐行等价。
//
// 本降级不区分窗口:Gemini 直连链路的号池配额模型与 NVIDIA 池不同,历史图重复 OCR 的痛点
// 由 L1 cache + singleflight 直接覆盖(同图同会话命中即返),故无需 window 逻辑。

import (
	"fmt"
	"strings"
)

// DowngradeGeminiImagesToText 对 GeminiRequest 做多模态自愈:当 targetModelToQuery 不是
// gemini 系(不支持多模态)且请求含 InlineData 图片时,逐张 OCR 转文本部分。
//
// 入参 targetModelToQuery 用于判定是否需要降级(含 "gemini" 子串则跳过,原样返回)。
// 返回 (downgradedCount, ocrHits, ocrMisses, lastErr):
//   - downgradedCount: 实际把 InlineData 改成 Text 部分的张数(OCR 成功);
//   - ocrHits/ocrMisses: 经 L1 缓存命中/未命中计数,供日志显示降级收益;
//   - lastErr: OCR 过程遇到的最后一个错误(若有),失败不中止,继续处理其余图。
//
// 行为与原 compat_dispatch.go:244-306 内联逻辑逐行等价:
//   - 仅当 !strings.Contains(strings.ToLower(targetModelToQuery), "gemini") 时降级;
//   - userPromptCtx 取同消息内所有 Text 部分拼接(不跨消息,与原实现一致),
//     仅用于 miss 真打 gemini 上游时的靶向 ocrPrompt,不参与 OcrImage 缓存键(image-only);
//   - OCR 失败/空文本时仅记 continue, InlineData 保留(原实现即如此,上游可能拒图但降级不强制覆盖)。
func (s *OCRService) DowngradeGeminiImagesToText(geminiReq *GeminiRequest, userSession *RelaySession, targetModelToQuery string) (downgradedCount, ocrHits, ocrMisses int, lastErr error) {
	if s == nil || geminiReq == nil {
		return 0, 0, 0, nil
	}
	// 目标模型是 gemini 系则原生支持多模态,无需降级。
	if strings.Contains(strings.ToLower(targetModelToQuery), "gemini") {
		return 0, 0, 0, nil
	}
	ocrModel := s.getOcrModel()

	for i, c := range geminiReq.Contents {
		var userTextBuilder strings.Builder
		for _, p := range c.Parts {
			if p.Text != "" {
				if userTextBuilder.Len() > 0 {
					userTextBuilder.WriteString("\n")
				}
				userTextBuilder.WriteString(p.Text)
			}
		}
		userPromptCtx := userTextBuilder.String()

		for j, p := range c.Parts {
			if p.InlineData == nil || p.InlineData.Data == "" {
				continue
			}
			mime := p.InlineData.MimeType
			if mime == "" {
				mime = "image/jpeg"
			}
			ocrText, ocrErr, cachedHit := s.OcrImage(userSession, p.InlineData.Data, mime, userPromptCtx)
			if cachedHit {
				ocrHits++
			} else {
				ocrMisses++
			}
			if ocrErr != nil {
				lastErr = fmt.Errorf("ocr 调用失败(字节 %d): %w", len(p.InlineData.Data), ocrErr)
				continue
			}
			if strings.TrimSpace(ocrText) == "" {
				continue
			}
			// 文案模型名随 ocrModel 参数化。
			descHeader := ocrDescHeaderRaw(ocrModel, ocrText)
			geminiReq.Contents[i].Parts[j].InlineData = nil
			if geminiReq.Contents[i].Parts[j].Text != "" {
				geminiReq.Contents[i].Parts[j].Text += descHeader
			} else {
				geminiReq.Contents[i].Parts[j].Text = descHeader
			}
			downgradedCount++
		}
	}
	return downgradedCount, ocrHits, ocrMisses, lastErr
}
