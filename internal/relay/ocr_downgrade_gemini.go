package relay

// ocr_downgrade_gemini.go —— L2 协议适配层:Gemini InlineData 图片本地 OCR 降级。
//
// 三层分离架构的 L2(Gemini 协议):Gemini 入站请求中,当目标模型不支持多模态
// (非 gemini 系 / 未显式声明 Multimodal)但请求却含 InlineData 图片时,用 L1 OCRService 把
// 每张图 OCR 成纯文本,把 InlineData 部分原地改为 Text 部分并拼接识别结果,再送上游。
// 与号池入口层解耦:由 dispatchToGemini 调用。
//
// 历史背景:本逻辑原内联在 APICompatHandler.dispatchToGemini(compat_dispatch.go:244-306),
// 没有抽成函数、不可被第三方号池复用且不可独立单测。现抽为 *OCRService 方法,行为逐行等价。
//
// 多模态判据由 L1 ocr_capability.go 的 modelSupportsImage 统一承载(配置优先 Multimodal 声明位 →
// 启发式模型名前缀白名单),替代原 `strings.Contains(..., "gemini")` 粗粒度判据。
//
// 本降级不区分窗口:Gemini 直连链路的号池配额模型与 NVIDIA 池不同,历史图重复 OCR 的痛点
// 由 L1 cache + singleflight 直接覆盖(同图同会话命中即返),故无需 window 逻辑。

import (
	"fmt"
	"strings"
)

// DowngradeGeminiImagesToText 对 GeminiRequest 做多模态自愈:当 targetModelToQuery 不是
// 多模态模型(非 gemini 系 / 未显式声明 Multimodal)且请求含 InlineData 图片时,逐张 OCR 转文本部分。
//
// 入参 targetModelToQuery 用于判定是否需要降级(多模态则跳过,原样返回)。
// 返回 (downgradedCount, ocrHits, ocrMisses, lastErr):
//   - downgradedCount: 实际把 InlineData 改成 Text 部分的张数(OCR 成功);
//   - ocrHits/ocrMisses: 经 L1 缓存命中/未命中计数,供日志显示降级收益;
//   - lastErr: OCR 过程遇到的最后一个错误(若有),失败不中止,继续处理其余图。
//
// 行为与原 compat_dispatch.go:244-306 内联逻辑逐行等价(仅判据升级):
//   - 由原 `strings.Contains(strings.ToLower(targetModelToQuery), "gemini")` 改为 s.modelSupportsImage
//     (配置优先 Multimodal 声明位 → 启发式模型名前缀白名单),既覆盖 gemini 全系,也放行
//     qwen-vl / gpt-4o 等其它原生多模态上游,同时尊重用户显式声明的 false 否决;
//   - userPromptCtx 取同消息内所有 Text 部分拼接(不跨消息,与原实现一致),
//     仅用于 miss 真打 gemini 上游时的靶向 ocrPrompt,不参与 OcrImage 缓存键(image-only);
//   - OCR 失败/空文本时仅记 continue, InlineData 保留(原实现即如此,上游可能拒图但降级不强制覆盖)。
//
// 多图批量优化(真·单请求批量):一条消息内 ≥2 张 InlineData miss 图合并进一次上游 OCR 调用,
// 模型按 [[图k]] 标记顺序输出,回译拆分后逐张回填 Parts[j] 与缓存,把 N 次上游降到 1 次。
// 仅 miss 图参与批量(cache hit 由 OcrImageBatch 命中即返,不进批量上游);=1 张仍走原 OcrImage
// 单图路径(零回归,所有单图现有测试与体感不变);拆分失败整体回退逐图(由 L1 内部兜底,结果质量
// 不劣于现状)。详见 ocr_batch.go 与 plans/cryptic-coalescing-mccarthy.md。
func (s *OCRService) DowngradeGeminiImagesToText(geminiReq *GeminiRequest, userSession *RelaySession, targetModelToQuery string) (downgradedCount, ocrHits, ocrMisses int, lastErr error) {
	if s == nil || geminiReq == nil {
		return 0, 0, 0, nil
	}
	// 目标模型原生支持多模态(配置优先 / 启发式兜底),无需降级,原样返回。
	if s.modelSupportsImage(targetModelToQuery) {
		return 0, 0, 0, nil
	}
	ocrModel := s.getOcrModel()

	// geminiImgCand 是一条消息内单个 InlineData 图的降级候选:记录它在 Parts 中的位置 j
	// 与已归一的 b64/mime,供第二遍按原位置写回。
	type geminiImgCand struct {
		j    int
		b64  string
		mime string
	}

	for i, c := range geminiReq.Contents {
		// 收集本消息内所有 Text 部分作为 OCR 靶向上下文(不跨消息,与原实现一致)。
		// promptCtx 不参与缓存键(image-only),仅用于 miss 真打上游时的靶向 ocrPrompt。
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

		// 第一遍:收集本消息内所有 InlineData 图作为降级候选。
		// Gemini 链路无窗口逻辑:历史图重复 OCR 痛点由 L1 cache + singleflight 直接覆盖,
		// 故所有 InlineData 都纳入候选(等价于全部"窗内"),cache命中/miss 由 L1 统一判别。
		var cands []geminiImgCand
		for j, p := range c.Parts {
			if p.InlineData == nil || p.InlineData.Data == "" {
				continue
			}
			mime := p.InlineData.MimeType
			if mime == "" {
				mime = "image/jpeg"
			}
			cands = append(cands, geminiImgCand{j: j, b64: p.InlineData.Data, mime: mime})
		}
		if len(cands) == 0 {
			continue
		}

		// 第二遍:对候选批量/单图 OCR 并按原位置写回 Parts[j]。
		// =1 张走原 OcrImage 单图路径(零回归,所有单图现有测试与体感不变);
		// ≥2 张走 OcrImageBatch 合并一次上游(按 [[图k]] 标记拆分回填,解析失败整体回退逐图)。
		if len(cands) == 1 {
			cd := cands[0]
			ocrText, ocrErr, cachedHit := s.OcrImage(userSession, cd.b64, cd.mime, userPromptCtx)
			if cachedHit {
				ocrHits++
			} else {
				ocrMisses++
			}
			if ocrErr != nil {
				lastErr = fmt.Errorf("ocr 调用失败(字节 %d): %w", len(cd.b64), ocrErr)
				continue
			}
			if strings.TrimSpace(ocrText) == "" {
				continue
			}
			// 文案模型名随 ocrModel 参数化。
			descHeader := ocrDescHeaderRaw(ocrModel, ocrText)
			geminiReq.Contents[i].Parts[cd.j].InlineData = nil
			if geminiReq.Contents[i].Parts[cd.j].Text != "" {
				geminiReq.Contents[i].Parts[cd.j].Text += descHeader
			} else {
				geminiReq.Contents[i].Parts[cd.j].Text = descHeader
			}
			downgradedCount++
			continue
		}

		// ≥2 张:构造批量入参,一次 OcrImageBatch 合并上游。
		// OcrImageBatch 内部先逐项查缓存(命中即返 CachedHit=true,不进批量),仅把 miss 项
		// 按消息合并一次上游;成功拆分逐张写 success 长 TTL,拆分失败整体回退逐图(调原 OcrImage)。
		// L2 只消费返回的 []OcrBatchResult(逐项对齐),按 CachedHit 计 ocrHits/ocrMisses、按 Ok/Text 写回。
		items := make([]OcrBatchItem, len(cands))
		for k, cd := range cands {
			items[k] = OcrBatchItem{B64: cd.b64, Mime: cd.mime, PromptCtx: userPromptCtx}
		}
		results := s.OcrImageBatch(userSession, items)
		for k, res := range results {
			cd := cands[k]
			if res.CachedHit {
				ocrHits++
			} else {
				ocrMisses++
			}
			if res.Err != nil {
				lastErr = fmt.Errorf("ocr 调用失败(字节 %d): %w", len(cd.b64), res.Err)
				continue
			}
			if strings.TrimSpace(res.Text) == "" {
				continue
			}
			descHeader := ocrDescHeaderRaw(ocrModel, res.Text)
			geminiReq.Contents[i].Parts[cd.j].InlineData = nil
			if geminiReq.Contents[i].Parts[cd.j].Text != "" {
				geminiReq.Contents[i].Parts[cd.j].Text += descHeader
			} else {
				geminiReq.Contents[i].Parts[cd.j].Text = descHeader
			}
			downgradedCount++
		}
	}
	return downgradedCount, ocrHits, ocrMisses, lastErr
}
