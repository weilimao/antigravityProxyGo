package relay

// ocr_downgrade_anthropic.go —— L2 协议适配层:Anthropic image content block 本地 OCR 降级。
//
// 三层分离架构的 L2(Anthropic 协议):扫 AnthropicRequest 所有消息的 content 块,把
// type=="image" 的块用 L1 OCRService 降级为 text 块。与号池入口层解耦:NVIDIA 池收到
// Anthropic 入站时调本方法,但本方法不感知"NVIDIA"身份,任何号池的 Anthropic 入站均可复用。
//
// 历史背景:本逻辑原为 APICompatHandler.downgradeAnthropicImagesToText(nvidia_translate_ocr.go),
// 文件名带 nvidia 暗示号池特化,实则与号池无关。现搬移到本文件,receiver 改为 *OCRService,
// 行为逐行等价(零回归)。P2 阶段在本文件扩展 URL/Data URL 图片支持。

import (
	"strings"
)

// nvidiaImageOcrDescHeader 把 OCR 识别出的纯文本包装成一段带上下文标记的描述,
// 供 DowngradeAnthropicImagesToText 把 image 块原地改写为 text 块时使用。
// 同时被 DowngradeGeminiImagesToText 复用(Gemini 入站自愈链路文案与 Anthropic 链路一致)。
//
// ocrModel 参数化:文案里展示真实使用的 OCR 模型,前端改模型后文案随之变化。
func nvidiaImageOcrDescHeader(ocrModel, ocrText string) string {
	if strings.TrimSpace(ocrModel) == "" {
		ocrModel = "gemini-2.5-flash"
	}
	return ocrDescHeaderRaw(ocrModel, ocrText)
}

// ocrDescHeaderRaw 是 nvidiaImageOcrDescHeader 的无默认值兜底版,
// 供 Gemini 链路同样以 ocrModel 参数化生成文案,语义完全一致。
func ocrDescHeaderRaw(ocrModel, ocrText string) string {
	return "\n\n[本地中继服务已自动调用 " + ocrModel + " 协助分析了用户发送的截图，内容提取如下：]\n" + ocrText + "\n[图片分析内容结束]\n"
}

// imageNotExtractablePlaceholder 用于 image 块无法本地 OCR 时的占位文本(url 类型数据
// 无法直取、空数据、或 OCR 服务不可达)。绝不阻断主请求,确保用户至少能看到"此处有图
// 但未能识别"的信号。跨 L2 协议层(Anthropic / Gemini / OpenAI)共享同一占位文案。
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

// DowngradeAnthropicImagesToText 扫 AnthropicRequest 所有消息的 content 块,
// 把 type=="image" 的块用本地 Gemini OCR 降级:OCR 成功则替换为
// [{"type":"text","text":"<OCR 识别文本>"}];OCR 失败则替换为占位文本。
// 绝不向号池上游直送 image_url,避免非多模态模型解析失败(400)。
//
// 设计要点:
//   - 原地替换 block(blocks[bi]),不动数组顺序、不增删 block,保证 [Image #N] 芯片
//     与 text/tool_result 块的相对位置不变,后续 AnthropicToOpenAIChat 的 text 合并 +
//     tool_result 拆分逻辑零变更。
//   - 降级后 Type=="text"、Source 置空 → AnthropicToOpenAIChat 走 case "text" 正常消化,
//     ChatMessage.Content 永远是 string,上游段零侵入。
//   - 失败不中止:返回 error 供调用方记日志,但仍把 block 改写成占位文本后继续,优先保证可用性。
//
// 多图批量优化(真·单请求批量):一条消息内 ≥2 张窗内 miss 图合并进一次上游 OCR 调用,
// 模型按 [[图k]] 标记顺序输出,回译拆分后逐张回填 blocks[bi] 与缓存,把 N 次上游降到 1 次。
// 仅窗内 miss 图参与批量(cache hit / 窗外图路径完全不动);=1 张窗内图仍走原 OcrImage 单图路径
// (零回归,所有单图现有测试与体感不变);拆分失败整体回退逐图(由 L1 内部兜底,结果质量不劣于现状)。
// 详见 ocr_batch.go 与 plans/cryptic-coalescing-mccarthy.md。
//
// 返回:成功降级的 image 块数 + 遇到的最后一个错误(若有) + ocrHits/ocrMisses/ocrSkipped。
// ocrHits   = 命中缓存直接返回(窗内命中,纳秒级不烧配额) + 窗外缓存复用的总数;
// ocrMisses = 窗内 cache miss 真打上游的图数(含批量合并的一次上游里覆盖的 N 张 与单图 1 张);
// ocrSkipped = 窗外图缓存未命中 → 走占位文本兜底的块数(绝不重新 OCR,省配额)。
func (s *OCRService) DowngradeAnthropicImagesToText(req *AnthropicRequest, userSession *RelaySession) (replaced int, lastErr error, ocrHits, ocrMisses, ocrSkipped int) {
	if s == nil || req == nil {
		return 0, nil, 0, 0, 0
	}
	// 窗口起点:消息数 <= 窗口口径时全覆盖;> 窗口口径时只覆盖末尾 N 条,前序消息内的图视为"窗外"。
	msgCount := len(req.Messages)
	windowStart := 0
	if msgCount > ocrRecentWindowMessages {
		windowStart = msgCount - ocrRecentWindowMessages
	}
	ocrModel := s.getOcrModel()

	// anthImgCand 是一条消息内单个窗内 miss 图的降级候选:记录它在 blocks 中的位置 bi
	// 与已归一的 b64/mime,供第二遍按原位置写回。
	type anthImgCand struct {
		bi   int
		b64  string
		mime string
	}

	for mi := range req.Messages {
		inWindow := mi >= windowStart
		// 收集同消息或上下文的用户文案(与原实现一致)。
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

		// 第一遍:逐图解析 b64。窗外图(cache 命中→复用 / 未命中→占位)与解析失败图当场写回;
		// 窗内图解析出 b64 后收集为候选(不在此 probe 缓存,交 L1 统一判别,避免与
		// OcrImage(OcrImageBatch) 内置 cache.get 双查)。
		var cands []anthImgCand
		for bi := range blocks {
			if blocks[bi].Type != "image" || blocks[bi].Source == nil {
				continue
			}
			src := blocks[bi].Source
			mime := src.MediaType
			if mime == "" {
				mime = "image/jpeg"
			}
			// 三态归一:base64 / Data URL / 网络 URL。
			// 窗内 URL 图允许下载(miss 真打链路);窗外 URL 图仅查 urlCache,未命中走占位(省下载+省配额)。
			// base64 与 Data URL 在两路径都不触网(本地直取/本地解析)。
			var (
				b64Data string
				imgMime = mime
				ready   bool
			)
			if inWindow {
				b, m, ferr := s.resolveB64WithFetch(src)
				if ferr != nil {
					// URL/下载失败 → 占位兜底,不阻断主请求。
					blocks[bi].Source = nil
					blocks[bi].Type = "text"
					blocks[bi].Text = imageNotExtractablePlaceholder
					continue
				}
				b64Data, imgMime, ready = b, m, true
			} else {
				b, m, ok := s.resolveB64NoFetch(src)
				if !ok {
					blocks[bi].Source = nil
					blocks[bi].Type = "text"
					blocks[bi].Text = imageNotExtractablePlaceholder
					continue
				}
				b64Data, imgMime, ready = b, m, true
			}
			if !ready || strings.TrimSpace(b64Data) == "" {
				blocks[bi].Source = nil
				blocks[bi].Type = "text"
				blocks[bi].Text = imageNotExtractablePlaceholder
				continue
			}
			// 窗外历史图:只查缓存复用,绝不重新打上游。命中→复用历史 OCR 文本(replaced+1,ocrHits+1);
			// 未命中→占位文本兜底(ocrSkipped+1),省下昂贵的号池 OCR 配额。
			// 缓存键按 image-only(不含 promptCtx),故窗外复用与当前提问解耦,只按图片身份命中。
			if !inWindow {
				cachedText, hit := s.OcrImageCacheOnlyLookup(userSession, b64Data)
				if hit && strings.TrimSpace(cachedText) != "" {
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
			// 窗内图:解析出 b64 即收集为候选,交第二遍(=1 单图 / ≥2 批量)。
			cands = append(cands, anthImgCand{bi: bi, b64: b64Data, mime: imgMime})
		}
		if len(cands) == 0 {
			continue
		}

		// 第二遍:对窗内候选批量/单图 OCR 并按原位置写回 blocks[bi]。
		// =1 张走原 OcrImage 单图路径(零回归,所有单图现有测试与体感不变);
		// ≥2 张走 OcrImageBatch 合并一次上游(按 [[图k]] 标记拆分回填,解析失败整体回退逐图)。
		if len(cands) == 1 {
			cd := cands[0]
			// 窗内图:走完整缓存+singleflight+miss 真打上游链路(与原实现逐行等价)。
			ocrText, ocrErr, cachedHit := s.OcrImage(userSession, cd.b64, cd.mime, userPromptCtx)
			if cachedHit {
				ocrHits++
			} else {
				ocrMisses++
			}
			if ocrErr != nil || strings.TrimSpace(ocrText) == "" {
				lastErr = ocrErr
				blocks[cd.bi].Source = nil
				blocks[cd.bi].Type = "text"
				blocks[cd.bi].Text = imageNotExtractablePlaceholder
				continue
			}
			blocks[cd.bi].Source = nil
			blocks[cd.bi].Type = "text"
			blocks[cd.bi].Text = nvidiaImageOcrDescHeader(ocrModel, ocrText)
			replaced++
			continue
		}

		// ≥2 张:构造批量入参,一次 OcrImageBatch 合并上游。
		// OcrImageBatch 内部先逐项查缓存(命中即返 CachedHit=true,不进批量),仅把 miss 项
		// 按消息合并一次上游;成功拆分逐张写 success 长 TTL,拆分失败整体回退逐图(调原 OcrImage)。
		// L2 只消费返回的 []OcrBatchResult(逐项对齐),按 CachedHit 计 ocrHits/ocrMisses、
		// 按 Ok/Text/Err 写回(descHeader / 占位),口径与单图路径完全一致。
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
			if res.Err != nil || strings.TrimSpace(res.Text) == "" {
				if res.Err != nil {
					lastErr = res.Err
				}
				blocks[cd.bi].Source = nil
				blocks[cd.bi].Type = "text"
				blocks[cd.bi].Text = imageNotExtractablePlaceholder
				continue
			}
			blocks[cd.bi].Source = nil
			blocks[cd.bi].Type = "text"
			blocks[cd.bi].Text = nvidiaImageOcrDescHeader(ocrModel, res.Text)
			replaced++
		}
	}
	return replaced, lastErr, ocrHits, ocrMisses, ocrSkipped
}
