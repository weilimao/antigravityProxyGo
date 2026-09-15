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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

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
	cache       *ocrLRUCache
	urlCache    *urlB64LRUCache
	inflight    singleflight.Group
	counters    ocrCacheCounters
	settingsMgr settings.ManagerInterface
	client      *http.Client
	logFn       func(string)
	// routeResolver:按入站模型名解析目标号池 Provider / GroupID / 上游模型。
	// 由 APICompatHandler 注入闭包捕获其 resolveRoutedTarget;nil 时保持旧行为
	// (一律走本地 18443 Gemini,不跨池)。用于非 Google 族前缀模型(如 nvidia/xxx、
	// other/openai/xxx)的跨号池 OCR 出站。
	routeResolver func(model string) (provider, groupID, targetModel string, matched bool)
	// mappingResolver:按模型名查 RelayModelMapping 的 Multimodal 声明位,供 modelSupportsImage
	// (OCR 降级闸)做"配置优先"判定。由 APICompatHandler 注入闭包捕获其 getRelayModelMappingSafe;
	// nil 时降级闸走启发式兜底(纯模型名前缀白名单),保持单测/未注入旧行为兼容。
	mappingResolver mappingResolver
}

// SetRouteResolver 注入前缀路由解析回调(闭包捕获 APICompatHandler.resolveRoutedTarget)。
// 解耦设计:OCRService 不持有整个 handler,只依赖一个纯函数契约;nil 回调 = 旧行为。
func (s *OCRService) SetRouteResolver(fn func(model string) (provider, groupID, targetModel string, matched bool)) {
	if s != nil {
		s.routeResolver = fn
	}
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

// getOcrModels 返回当前生效的候选 OCR 模型列表。
// 若未配置或为空列表，则回退为包含单个 getOcrModel() 的切片。
func (s *OCRService) getOcrModels() []string {
	if s == nil || s.settingsMgr == nil {
		return []string{defaultOcrModel}
	}
	models := s.settingsMgr.GetOcrModels()
	if len(models) == 0 {
		return []string{s.getOcrModel()}
	}
	return models
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

// isOcrRouteResolverEnabled 返回是否已注入跨号池路由解析回调。
func (s *OCRService) isOcrRouteResolverEnabled() bool {
	return s != nil && s.routeResolver != nil
}

// resolveOcrTarget 解析当前 OCR 模型名对应的目标号池分流信息。
// 返回 (isGoogle, provider, groupID, upstreamModel):
//   - isGoogle=true:模型属 Google 家族(google/antigravity/gcp/project/gemini-cli/空),
//     保持旧行为,OCR 走本地 18443 Gemini 原生端点。
//   - isGoogle=false:模型属其它号池(nvidia/other/deepseek 等),OCR 出站改走
//     18444 /route 入口,按 model 前缀路由到对应号池多模态模型。
//   - 未注入 routeResolver 或模型未命中任何路由规则:强制视为 Google 族(旧行为),
//     并 logf 警告,避免静默跨池失败。
func (s *OCRService) resolveOcrTarget(ocrModel string) (isGoogle bool, provider, groupID, upstreamModel string) {
	// 未注入解析回调或在入站原始模型上无匹配 → 回退 Google 族旧行为。
	if !s.isOcrRouteResolverEnabled() {
		return true, "", "", ocrModel
	}
	provider, groupID, targetModel, matched := s.routeResolver(ocrModel)
	if !matched {
		s.logf("ocr model %q 未命中任何模型映射/路由规则,回退本地 Gemini(18443)", ocrModel)
		return true, "", "", ocrModel
	}
	if isGoogleProvider(provider) {
		// 命中 Google 族号池:仍走 18443 Gemini 原生端点,上游模型名可能被改写
		// (如 google/gemini-2.5-flash → gemini-2.5-flash),用 targetModel 更稳。
		return true, provider, groupID, targetModel
	}
	// 命中非 Google 族号池(nvidia / other / deepseek 等):改走 18444 /route。
	return false, provider, groupID, targetModel
}

// OcrImage 对一张 base64 图做 OCR,返回识别出的纯文本。失败返回 "" + error。
// 支持多模型候选并发竞速模式:首包成功识别者胜出并立即取消其余候选。
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

	ocrModels := s.getOcrModels()
	var cacheModelKey string
	if len(ocrModels) > 1 {
		cacheModelKey = "race:" + strings.Join(ocrModels, ",")
	} else if len(ocrModels) == 1 {
		cacheModelKey = ocrModels[0]
	} else {
		cacheModelKey = defaultOcrModel
		ocrModels = []string{defaultOcrModel}
	}

	promptCtx := ""
	if len(userPromptText) > 0 {
		promptCtx = strings.TrimSpace(userPromptText[0])
	}

	// 缓存键首维:会话级隔离键
	ownerKey := ocrOwnerKey(userSession)

	// 命中缓存直接返回
	if s.cache != nil {
		key := ocrCacheKey(ownerKey, cacheModelKey, b64Data)
		if e, ok := s.cache.get(key); ok {
			s.counters.hits.Add(1)
			return e.text, e.err, true
		}
		// 若为多候选竞速，检查是否有任一候选模型的单模型缓存命中
		if len(ocrModels) > 1 {
			for _, m := range ocrModels {
				singleKey := ocrCacheKey(ownerKey, m, b64Data)
				if e, ok := s.cache.get(singleKey); ok && e.ok && strings.TrimSpace(e.text) != "" {
					s.counters.hits.Add(1)
					s.cache.set(key, e.text, nil, true)
					return e.text, nil, true
				}
			}
		}
		s.counters.misses.Add(1)
	}

	// singleflight:合并同图并发为 1 次真上游调用
	callKey := ocrCacheKey(ownerKey, cacheModelKey, b64Data)
	v, callErr, _ := s.inflight.Do(callKey, func() (interface{}, error) {
		var text string
		var err error
		if len(ocrModels) > 1 {
			text, err = s.ocrImageRace(context.Background(), userSession, b64Data, mimeType, ocrModels, promptCtx)
		} else {
			text, err = s.ocrImageUncached(userSession, b64Data, mimeType, ocrModels[0], promptCtx)
		}
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
func (s *OCRService) OcrImageCacheOnlyLookup(userSession *RelaySession, b64Data string) (string, bool) {
	if s == nil || userSession == nil || s.cache == nil {
		return "", false
	}
	if strings.TrimSpace(b64Data) == "" {
		return "", false
	}
	ocrModels := s.getOcrModels()
	ownerKey := ocrOwnerKey(userSession)

	var cacheModelKey string
	if len(ocrModels) > 1 {
		cacheModelKey = "race:" + strings.Join(ocrModels, ",")
	} else if len(ocrModels) == 1 {
		cacheModelKey = ocrModels[0]
	} else {
		cacheModelKey = defaultOcrModel
	}

	key := ocrCacheKey(ownerKey, cacheModelKey, b64Data)
	if e, ok := s.cache.get(key); ok && e.ok && strings.TrimSpace(e.text) != "" {
		s.counters.hits.Add(1)
		return e.text, true
	}

	// 多模型竞速下，若复合 key 未命中，检查任一候选模型的缓存
	if len(ocrModels) > 1 {
		for _, m := range ocrModels {
			singleKey := ocrCacheKey(ownerKey, m, b64Data)
			if e, ok := s.cache.get(singleKey); ok && e.ok && strings.TrimSpace(e.text) != "" {
				s.counters.hits.Add(1)
				s.cache.set(key, e.text, nil, true)
				return e.text, true
			}
		}
	}
	return "", false
}

type ocrRaceResult struct {
	model string
	text  string
	err   error
}

// ocrImageRace 对传入的多个候选模型并发发起 OCR 请求，首个成功识别的模型胜出，
// 并立即调用 cancel() 终止其余候选请求；若所有候选均失败，则返回最后一次失败的错误。
func (s *OCRService) ocrImageRace(parent context.Context, userSession *RelaySession, b64Data string, mimeType string, ocrModels []string, promptCtx string) (string, error) {
	if len(ocrModels) == 0 {
		return "", fmt.Errorf("ocr race: no models specified")
	}
	if len(ocrModels) == 1 {
		return s.ocrImageUncachedWithContext(parent, userSession, b64Data, mimeType, ocrModels[0], promptCtx)
	}

	base := parent
	if base == nil {
		base = context.Background()
	}
	raceCtx, cancel := context.WithCancel(base)
	defer cancel()

	resultCh := make(chan ocrRaceResult, len(ocrModels))
	var wg sync.WaitGroup

	s.logf("开始 OCR 并发竞速,候选模型列表: %v", ocrModels)

	for _, model := range ocrModels {
		wg.Add(1)
		go func(m string) {
			defer wg.Done()
			text, err := s.ocrImageUncachedWithContext(raceCtx, userSession, b64Data, mimeType, m, promptCtx)
			select {
			case resultCh <- ocrRaceResult{model: m, text: text, err: err}:
			case <-raceCtx.Done():
			}
		}(model)
	}

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	var lastErr error
	var failCount int

	for res := range resultCh {
		if res.err == nil && strings.TrimSpace(res.text) != "" {
			s.logf("OCR 竞速优胜者: 模型 %s 胜出 (识别文本长度: %d), 立即取消其余模型", res.model, len(res.text))
			cancel() // 首包胜出，立即取消其他进行中的请求
			return res.text, nil
		}
		failCount++
		lastErr = res.err
		s.logf("OCR 候选模型 %s 失败: %v (%d/%d)", res.model, res.err, failCount, len(ocrModels))
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("all %d ocr candidates returned empty text", len(ocrModels))
	}
	return "", fmt.Errorf("all %d ocr candidates failed in race, last error: %w", len(ocrModels), lastErr)
}

// ocrImageUncached 是 OcrImage 的纯上游调用实现(使用 Background context)。
func (s *OCRService) ocrImageUncached(userSession *RelaySession, b64Data string, mimeType string, ocrModel string, userPromptText ...string) (string, error) {
	return s.ocrImageUncachedWithContext(context.Background(), userSession, b64Data, mimeType, ocrModel, userPromptText...)
}

// ocrImageUncachedWithContext 是支持外部 context 取消的纯上游调用实现。
func (s *OCRService) ocrImageUncachedWithContext(ctx context.Context, userSession *RelaySession, b64Data string, mimeType string, ocrModel string, userPromptText ...string) (string, error) {
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

	ocrPrompt := buildSingleOcrPrompt(promptCtx)

	isGoogle, _, _, upstreamModel := s.resolveOcrTarget(ocrModel)

	if !isGoogle {
		return s.ocrImageUncachedViaRouteWithContext(ctx, userSession, ocrPrompt, mimeType, b64Data, ocrModel, upstreamModel)
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

	result := ocrCallWithRetry(ctx, "ocr", s.logf, func(attemptCtx context.Context) ocrAttemptResult {
		return s.ocrGeminiAttempt(attemptCtx, userSession, upstreamModel, ocrReqBytes)
	})
	return result.text, result.err
}

// ocrGeminiAttempt 是 Google 族路径「一次真打 18443 Gemini + 解析响应」的纯上游尝试,
// 供 ocrImageUncached 经 ocrCallWithRetry 重试调用。
//
// 每次重试都新建独立 http.Request(绑定本次 ctx)并发 Do;响应体读完即 Close。
// 把"解析响应"也放进同一 attempt 闭包:2xx 但候选为空属确定性失败(上游安全拦截的一种),
// 在此返回带 status 文本的 error 交 isOcrRetryableErr 判为不重试,避免对它狂打上游。
//
// 错误文本约定(供 isOcrRetryableErr 解析):非 200 → "ocr service returned status %d: %s"
// (含 "status " 关键词,retryableStatusFromErr 据此提取状态码定 4xx/5xx 重试性)。
func (s *OCRService) ocrGeminiAttempt(ctx context.Context, userSession *RelaySession, upstreamModel string, ocrReqBytes []byte) ocrAttemptResult {
	if s == nil || s.client == nil {
		return ocrAttemptResult{err: fmt.Errorf("ocrGeminiAttempt: nil service or client")}
	}

	// 模型名参数化:默认 gemini-2.5-flash,前端可改任意 Gemini 系模型。
	ocrURL := fmt.Sprintf("http://%s/v1beta/models/%s:generateContent", localProxyAddr, upstreamModel)
	ocrHTTPReq, errReq := http.NewRequestWithContext(ctx, http.MethodPost, ocrURL, bytes.NewReader(ocrReqBytes))
	if errReq != nil {
		return ocrAttemptResult{err: fmt.Errorf("create ocr request: %w", errReq)}
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
		return ocrAttemptResult{err: fmt.Errorf("execute ocr request: %w", errDo)}
	}
	defer ocrResp.Body.Close()

	if ocrResp.StatusCode != http.StatusOK {
		errBytes, _ := io.ReadAll(ocrResp.Body)
		return ocrAttemptResult{err: fmt.Errorf("ocr service returned status %d: %s", ocrResp.StatusCode, string(errBytes))}
	}

	respBytes, _ := io.ReadAll(ocrResp.Body)
	var gemResp GeminiResponse
	if errUnmarshal := json.Unmarshal(respBytes, &gemResp); errUnmarshal != nil {
		return ocrAttemptResult{err: fmt.Errorf("unmarshal ocr response: %w", errUnmarshal)}
	}
	if len(gemResp.Candidates) == 0 || len(gemResp.Candidates[0].Content.Parts) == 0 {
		return ocrAttemptResult{err: fmt.Errorf("ocr response candidates are empty")}
	}
	return ocrAttemptResult{text: gemResp.Candidates[0].Content.Parts[0].Text}
}

// ocrImageUncachedViaRoute 处理非 Google 族前缀模型(如 nvidia/xxx、other/openai/xxx)
// 的跨号池 OCR 出站。
func (s *OCRService) ocrImageUncachedViaRoute(userSession *RelaySession, ocrPrompt, mimeType, b64Data, ocrModel, upstreamModel string) (string, error) {
	return s.ocrImageUncachedViaRouteWithContext(context.Background(), userSession, ocrPrompt, mimeType, b64Data, ocrModel, upstreamModel)
}

// ocrImageUncachedViaRouteWithContext 是支持外部 context 取消的跨号池 OCR 出站实现。
func (s *OCRService) ocrImageUncachedViaRouteWithContext(ctx context.Context, userSession *RelaySession, ocrPrompt, mimeType, b64Data, ocrModel, upstreamModel string) (string, error) {
	if s == nil || s.client == nil {
		return "", fmt.Errorf("ocrImageUncachedViaRoute: nil service or client")
	}
	if userSession == nil {
		return "", fmt.Errorf("ocrImageUncachedViaRoute: nil session")
	}

	reqModel := strings.TrimSpace(ocrModel)
	if reqModel == "" {
		reqModel = defaultOcrModel
	}
	ocrOpenAIReq := map[string]interface{}{
		"model": reqModel,
		"messages": []map[string]interface{}{
			{
				"role": "user",
				"content": []map[string]interface{}{
					{"type": "text", "text": ocrPrompt},
					{"type": "image_url", "image_url": map[string]interface{}{
						"url": "data:" + mimeType + ";base64," + b64Data,
					}},
				},
			},
		},
	}
	ocrReqBytes, errMarshal := json.Marshal(ocrOpenAIReq)
	if errMarshal != nil {
		return "", fmt.Errorf("marshal ocr route request: %w", errMarshal)
	}

	s.logf("跨号池 OCR(经 /route):model %s → upstream %s | 图 %s | 字节 %d", reqModel, upstreamModel, mimeType, len(b64Data))

	result := ocrCallWithRetry(ctx, "ocr route", s.logf, func(attemptCtx context.Context) ocrAttemptResult {
		return s.ocrRouteAttempt(attemptCtx, userSession, ocrReqBytes)
	})
	return result.text, result.err
}

// ocrRouteAttempt 是非 Google 族路径「一次真打 18444 /route + 解析响应」的纯上游尝试,
// 供 ocrImageUncachedViaRoute 经 ocrCallWithRetry 重试调用。
//
// 每次重试都新建独立 http.Request(绑定本次 ctx)并发 Do;**所有头都在此重设**,
// 特别是 X-Antigravity-OCR-Self 自递归守卫头 —— 因重试会重建请求,守卫头必须在每次尝试都带上,
// 否则重试请求会被下游号池降级入口误判为普通入站、再次触发 OCR 形成自递归。
//
// 错误文本约定(供 isOcrRetryableErr 解析):非 200 → "ocr route service returned status %d: %s"
// (含 "status " 关键词,retryableStatusFromErr 据此提取状态码定 4xx/5xx 重试性)。
func (s *OCRService) ocrRouteAttempt(ctx context.Context, userSession *RelaySession, ocrReqBytes []byte) ocrAttemptResult {
	if s == nil || s.client == nil {
		return ocrAttemptResult{err: fmt.Errorf("ocrRouteAttempt: nil service or client")}
	}
	if userSession == nil {
		return ocrAttemptResult{err: fmt.Errorf("ocrRouteAttempt: nil session")}
	}

	// 打到本地 18444 /route/v1/chat/completions 入口,由 handleRoutedForward 按前缀路由。
	ocrURL := fmt.Sprintf("http://%s/route/v1/chat/completions", localRelayAddr)
	ocrHTTPReq, errReq := http.NewRequestWithContext(ctx, http.MethodPost, ocrURL, bytes.NewReader(ocrReqBytes))
	if errReq != nil {
		return ocrAttemptResult{err: fmt.Errorf("create ocr route request: %w", errReq)}
	}
	ocrHTTPReq.Header.Set("Content-Type", "application/json")
	ocrHTTPReq.Header.Set("Authorization", "Bearer "+userSession.UserKey)
	ocrHTTPReq.Header.Set("X-Relay-User-Id", userSession.UserID)
	if userSession.APIKeyID != "" {
		ocrHTTPReq.Header.Set("X-Relay-Api-Key-Id", userSession.APIKeyID)
	}
	// 自递归守卫:下游 nvidia/passthrough 降级入口识别此头后跳过 image→文本降级。
	// 重试下每次重建请求都重设,守卫语义不破。
	ocrHTTPReq.Header.Set("X-Antigravity-OCR-Self", "1")
	ocrHTTPReq.Header.Set("X-Antigravity-Original-Path", "/route/v1/chat/completions/ocr-fallback")
	ocrHTTPReq.Header.Set("X-Antigravity-Original-Method", "POST")

	ocrResp, errDo := s.client.Do(ocrHTTPReq)
	if errDo != nil {
		return ocrAttemptResult{err: fmt.Errorf("execute ocr route request: %w", errDo)}
	}
	defer ocrResp.Body.Close()

	if ocrResp.StatusCode != http.StatusOK {
		errBytes, _ := io.ReadAll(ocrResp.Body)
		return ocrAttemptResult{err: fmt.Errorf("ocr route service returned status %d: %s", ocrResp.StatusCode, string(errBytes))}
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
		return ocrAttemptResult{err: fmt.Errorf("unmarshal ocr route response: %w", errUnmarshal)}
	}
	if len(openAIResp.Choices) == 0 {
		return ocrAttemptResult{err: fmt.Errorf("ocr route response choices are empty")}
	}
	// content 可能是 string,也可能是多个分段(数组)。统一提取为纯文本字符串。
	text := contentToString(openAIResp.Choices[0].Message.Content)
	if strings.TrimSpace(text) == "" {
		return ocrAttemptResult{err: fmt.Errorf("ocr route response content is empty")}
	}
	return ocrAttemptResult{text: text}
}

// contentToString 把 OpenAI Chat 响应里 message.content 的 string / []string 形态归一为纯文本。
func contentToString(content interface{}) string {
	switch v := content.(type) {
	case string:
		return v
	case []interface{}:
		var sb strings.Builder
		for _, seg := range v {
			if part, ok := seg.(string); ok {
				sb.WriteString(part)
			} else if m, ok := seg.(map[string]interface{}); ok {
				if t, ok := m["text"].(string); ok {
					sb.WriteString(t)
				}
			}
		}
		return sb.String()
	case []string:
		return strings.Join(v, "")
	}
	return ""
}
