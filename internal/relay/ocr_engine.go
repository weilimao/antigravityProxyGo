package relay

// ocr_engine.go —— 入站 image 自愈降级的 OCR 引擎核心(L1)。
//
// 三层分离架构的 L1:给一张图(base64 / URL / Data URL)返回识别文本,
// 内含两级缓存(b64→text)、singleflight 合并并发、失败短 TTL 熔断。
// 与协议适配层(L2: ocr_downgrade_anthropic.go / ocr_downgrade_gemini.go /
// ocr_downgrade_openai.go)和号池入口层(handleNvidia / dispatchToGemini /
// passthroughForward)解耦:号池入口只调 L2,L2 只调 L1,L1 不感知协议与号池身份。
//
// 历史背景:本引擎原为 APICompatHandler 的方法(getOcrModel / ocrImageViaLocalGemini /
// ocrImageCacheOnlyLookup / ocrImageViaLocalGeminiUncached),三处状态字段
// (ocrCache / ocrInflight / ocrCounters)长在 handler 上,与号池职责耦合,导致
// URL 图完善与第三方号池复用都要碰 handler。现抽离为独立 OCRService,receiver
// 从 *APICompatHandler 改为 *OCRService,方法签名与行为逐行等价(零回归)。
//
// 依赖:包级 const defaultOcrModel 与 var localProxyAddr(留 compat.go,跨文件可见);
// settingsMgr 注入失败时回退 defaultOcrModel,保持旧行为兼容。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"antigravity-proxy/internal/settings"
	"golang.org/x/sync/singleflight"
)

// OCRService 是入站 image 自愈降级的本地 Gemini OCR 引擎,与号池入口解耦。
// 一进程一实例(挂在 APICompatHandler.ocr 上),承载:
//   - cache: b64→text 的进程内 LRU + SQLite 持久化两级缓存(成功 TTL 24h / 失败 30s);
//   - urlCache: url→b64 的进程内 LRU + TTL 缓存(成功 TTL 24h),让 URL 图免重复下载(P2);
//   - inflight: 同图并发(singleflight)合并为 1 次真上游调用,防缓存击穿;
//   - counters: 命中/未命中原子计数,供日志显示降级收益;
//   - settingsMgr / client: 读 OCR 模型配置 + 发本地 Gemini 请求。
//
// nil cache 降级为零缓存(纯走上游),保持旧行为兼容,供单测或异常注入用。
type OCRService struct {
	cache     *ocrLRUCache
	urlCache  *urlB64LRUCache
	inflight  singleflight.Group
	counters  ocrCacheCounters
	settingsMgr settings.ManagerInterface
	client      *http.Client
	logFn       func(string)
}

// NewOCRService 构造一个 OCR 引擎。settingsMgr / client / logFn 可为 nil:
// settingsMgr=nil → getOcrModel 回退 defaultOcrModel;
// client=nil → 调用方必须保证不打上游(或使用方后续注入);
// cache 默认参数(容量 256 / 成功 TTL 24h / 失败 30s)与原 NewAPICompatHandler 内联构造等价;
// urlCache 默认容量 urlB64CacheCap(64)。
func NewOCRService(settingsMgr settings.ManagerInterface, client *http.Client, logFn func(string)) *OCRService {
	return &OCRService{
		cache:       newOcrLRUCache(0, 0, 0),
		urlCache:    newUrlB64LRUCache(0),
		settingsMgr: settingsMgr,
		client:      client,
		logFn:       logFn,
	}
}

// getOcrModel 返回当前生效的 OCR 模型名。
// settingsMgr 为 nil(relay 单测未注入)时回退 defaultOcrModel,保持旧行为;
// 配置空值时 settings 层已兜底,这里双保险再判一次。
func (s *OCRService) getOcrModel() string {
	if s == nil || s.settingsMgr == nil {
		return defaultOcrModel
	}
	m := strings.TrimSpace(s.settingsMgr.GetOcrModel())
	if m == "" {
		return defaultOcrModel
	}
	return m
}

// logf 是 OCRService 的日志助手:统一的 [OCR] 前缀,供 L2 协议适配层
// (Anthropic / Gemini / OpenAI 降级)发出逐图 OCR 进度/成功/失败日志。
// logFn 为 nil 时静默(单测未注入),与原 h.log 的 nil 守卫语义一致。
func (s *OCRService) logf(format string, args ...interface{}) {
	if s == nil || s.logFn == nil {
		return
	}
	s.logFn(fmt.Sprintf("[OCR] "+format, args...))
}

// OcrImage 调用本地 Gemini(默认 gemini-2.5-flash,前端可配)对一张 base64 图做 OCR,
// 返回识别出的纯文本。失败返回 "" + error。
//
// 抽出供各号池协议适配层(L2)复用,避免每个号池各写一份:
//   - Anthropic 入站降级(DowngradeAnthropicImagesToText,NVIDIA 池)
//   - Gemini 入站自愈(DowngradeGeminiImagesToText,dispatchToGemini 内,目标模型非 gemini 时图转文)
//   - 第三方号池(Passthrough / Route,DowngradeOpenAIChatImagesToText)
//
// 缓存策略(cache + singleflight):
//   - 键 = UserKey|ocrModel|sha256(b64)[:16] + 可选 promptCtx 维度,按用户、OCR 模型、用户提问三维隔离;
//   - 命中即返回历史 OCR 文本,跳过 gemini 调用与 ~3s 延迟;
//   - miss 时 singleflight 合并同图并发为 1 次真上游调用,防缓存击穿;
//   - OCR 失败也缓存(短 TTL),熔断窗口内不再重打挂的 OCR 服务;
//   - 切换 ocrModel 后键变化,自动重新 OCR 新模型(配置改了立刻生效)。
// nil cache 降级为零缓存(纯走上游),保持旧行为兼容。
//
// 返回的 cachedHit 标识本次结果是否来自缓存命中(true=命中即返,未触达上游;
// false=cache miss 真打了一次 gemini 上游,或 nil 缓存的纯走上游场景)。
// 供调用方在日志里透出"本轮这张图是命中还是重新 OCR"。
//
// P1 契约:签名与原 APICompatHandler.ocrImageViaLocalGemini 逐字一致,仅 receiver 改为
// *OCRService,userSession 入参不变(会话级隔离键经 ocrOwnerKey 从 *RelaySession 提取),
// 保证测试调用点仅需 h.ocrImageViaLocalGemini → h.ocr.OcrImage 的最小替换。
func (s *OCRService) OcrImage(userSession *RelaySession, b64Data string, mimeType string, userPromptText ...string) (text string, err error, cachedHit bool) {
	if s == nil || userSession == nil {
		return "", fmt.Errorf("OcrImage: nil service or session"), false
	}
	if strings.TrimSpace(b64Data) == "" {
		return "", fmt.Errorf("OcrImage: empty image data"), false
	}
	if mimeType == "" {
		mimeType = "image/jpeg"
	}
	ocrModel := s.getOcrModel()

	promptCtx := ""
	if len(userPromptText) > 0 {
		promptCtx = strings.TrimSpace(userPromptText[0])
	}

	// 缓存键首维:会话级隔离键(sessionKey 非空时优先,粒度比 UserKey 更细,按会话隔离;
	// 空则回退 UserKey,保持单测/未传场景的旧行为兼容)。
	ownerKey := ocrOwnerKey(userSession)

	// 命中缓存直接返回,跳过 gemini 调用与 ~3s 延迟(含失败条目短 TTL 熔断)。
	if s.cache != nil {
		key := ocrCacheKey(ownerKey, ocrModel, b64Data, promptCtx)
		if e, ok := s.cache.get(key); ok {
			s.counters.hits.Add(1)
			return e.text, e.err, true
		}
		s.counters.misses.Add(1)
	}

	// singleflight:同步相邻并发对同图(同模型)的请求,首调用真打上游,其余阻塞等待结果共享。
	callKey := ocrCacheKey(ownerKey, ocrModel, b64Data, promptCtx)
	v, callErr, _ := s.inflight.Do(callKey, func() (interface{}, error) {
		text, err := s.ocrImageUncached(userSession, b64Data, mimeType, ocrModel, promptCtx)
		ok := err == nil && strings.TrimSpace(text) != ""
		if s.cache != nil {
			cachedText := text
			if !ok {
				cachedText = ""
			}
			s.cache.set(callKey, cachedText, err, ok)
		}
		return text, err
	})
	if callErr != nil {
		return "", callErr, false
	}
	text, _ = v.(string)
	return text, nil, false
}

// OcrImageCacheOnlyLookup 仅查 OCR 缓存,命中返回历史 OCR 文本(true),未命中返回("",false)。
// 绝不触达 singleflight / gemini 上游,供"最近 N 条消息窗口"之外的图片块复用:
//   - 命中(图在窗口内 OCR 过且仍驻留 LRU / SQLite)→ 复用历史文本,不烧 antigravity 配额;
//   - 未命中 → 调用方写 imageNotExtractablePlaceholder 占位文本,绝不重新 OCR。
// 与 OcrImage 共享 ownerKey(会话级)+ ocrModel + 图指纹 + promptCtx 多维键,
// 故窗口内 OCR 入缓存的图,被推出窗口后仍可被本方法命中复用。
func (s *OCRService) OcrImageCacheOnlyLookup(userSession *RelaySession, b64Data string, userPromptText ...string) (string, bool) {
	if s == nil || userSession == nil || s.cache == nil {
		return "", false
	}
	if strings.TrimSpace(b64Data) == "" {
		return "", false
	}
	ocrModel := s.getOcrModel()
	promptCtx := ""
	if len(userPromptText) > 0 {
		promptCtx = strings.TrimSpace(userPromptText[0])
	}
	key := ocrCacheKey(ocrOwnerKey(userSession), ocrModel, b64Data, promptCtx)
	if e, ok := s.cache.get(key); ok && e.ok && strings.TrimSpace(e.text) != "" {
		// 仅复用成功条目;失败短 TTL 条目不在此复用(让调用方走占位,语义更清晰)。
		s.counters.hits.Add(1)
		return e.text, true
	}
	return "", false
}

// ocrImageUncached 是 OcrImage 的纯上游调用实现,无缓存、无 singleflight,
// 纯粹把 base64 发给 18443 的指定 Gemini 模型跑 OCR。
// 抽出来便于 (a) 缓存层 miss 后复用 (b) 单测直接打 mock 校验上游请求形态。
// ocrModel 由调用方传入(取自 s.getOcrModel()),用于动态拼写 18443 URL,默认 gemini-2.5-flash。
func (s *OCRService) ocrImageUncached(userSession *RelaySession, b64Data string, mimeType string, ocrModel string, userPromptText ...string) (string, error) {
	if s == nil || s.client == nil {
		return "", fmt.Errorf("ocrImageUncached: nil service or client")
	}
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

	ocrResp, errDo := s.client.Do(ocrHTTPReq)
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
