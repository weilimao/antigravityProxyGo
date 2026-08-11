// Package pricing: aigen.go —— AI 一键生成计费配置的后端实现(主流程)。
//
// 用途:用户在「计费配置」面板点 AI 一键生成按钮后,前端把"模型统计里出现、但
// 尚未登记单价的候选模型基名[]"传过来,本生成器调用一个 Gemini 模型(默认
// gemini-2.5-flash-lite,经用户选定的 Antigravity 账号直连 daily-cloudcode-pa
// /v1internal:streamGenerateContent?alt=sse)生成建议单价(USD/每百万 tokens)。
//
// 链路复用:与 internal/stats/packet.go 的 AnalyzePackets 完全同构 ——
//   - 同样的 v1internal 信封(见 aigen_envelope.go buildAIRequestEnvelope);
//   - 同样的 SSE 抽取(见 aigen_extract.go extractSSETextWithMeta);
//   - 同样的 401 + refreshToken 自动 refreshAccount 重试一次。
// 因 pricing 不应反向 import stats(那会拉进抓包/磁盘等无关依赖),SSE 抽取与
// 信封构造就地复制为本包内私有函数,语义与 packet.go 逐字对齐,零行为漂移。
//
// 取数策略:先尝试携带 googleSearch grounding 工具做联网检索以拿实时公开定价;
// 若上游拒收工具(HTTP 400 且错误文本含 grounding 关键词)则去掉 tools 再发一次,
// 降级为纯 LLM 训练知识生成。grounding 兜底失败不影响最终可用性,只影响单份
// 新发布模型的准确性,最终由用户人工确认兜底。
//
// 进度推送:Generate 接 progressFn func(stage, status string) 回调,在取 token /
// 联网 / 降级 / 刷新 / 解析 / 完成 六个阶段边界回调,上层 IPC 入口把回调桥接到
// wails EventsEmit("pricing:ai-progress", ...),前端实时显示处理阶段。
//
// 不经过本地 18443 MITM 代理:此请求直连 daily-cloudcode-pa.googleapis.com,
// 因此 internal/proxy/json_schema_clean.go 的 functionDeclarations 剥离逻辑
// 对本请求无效,googleSearch 顶层工具不会被拦。
package pricing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"antigravity-proxy/internal/netutil"
)

// AIEndpointDefault 是 Antigravity 官方号池的流式生成端点,与 internal/stats/packet.go
// AnalyzePackets 写死的目标 URL 一致。可经 NewAIPriceGenerator 的 opts.endpointURL 覆盖,
// 供 httptest 单测注入本地服务器地址。
const AIEndpointDefault = "https://daily-cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse"

// AIModelDefault 是生成定价用的 Gemini 模型,与 AnalyzePackets 的 "gemini-2.5-flash-lite"
// 对齐:价格表生成是纯知识与轻量联网检索任务,低端模型已足够且省配额。
const AIModelDefault = "gemini-2.5-flash-lite"

// AIMaxOutputTokens 限制单次生成 token 上限,价格表本身很短(模型数×1行JSON)。
const AIMaxOutputTokens = 8192

// AIPriceResult 是 Generate 返回给上层(IPC → 前端)的单模型定价结果。
// 它与 ModelRate 严格分离:ModelRate 仍是 pricing.json 存储格式与计费匹配引擎的
// 唯一契约(扩展会连锁破坏 CalculateCostBreakdown / 前端 get-pricing-res 渲染 /
// quota 等),AIPriceResult 只在 ai-generate 通道承载质检标记与来源,不入库不参与计费。
//
// 到达前端的 AIPriceResult 由 aiPricingController 渲染成可编辑表格行,
// 用户最终确认时仍只取 Rate 的 input/output/cached 组 batch 走 update-pricing-batch。
type AIPriceResult struct {
	Rate           ModelRate `json:"rate"`           // 估算/检索到的单价(前端入编辑框)
	Grounded       bool      `json:"grounded"`        // 本次结果是否真经联网检索取到 grounding 来源
	Estimated      bool      `json:"estimated"`       // AI 自标估算或后端 Cached 0.25× 兜底估算
	AnchorConflict bool      `json:"anchorConflict"`  // 与 realPricingAnchor 真实厂商标价偏差 > 2x
	Sources        []WebRef  `json:"sources"`        // grounding 引用的官方价页 URL,前端可点开核对
}

// AIPriceGenerator 用一个 Antigravity 账号 token 直连 daily-cloudcode-pa,
// 对一批候选模型名生成 USD/每百万 tokens 单价。
//
// 依赖注入(与 PacketCapturer 的 getAccountTokens/refreshAccount 闭包逐字同构):
//   - getAccountTokens: 按账号 id 取 (accessToken, refreshToken, projectID, error)
//   - refreshAccount:  按账号 id 刷新 token,返回新 token
//   - logFn:            日志回调,可为 nil(单测未注入)
//
// 可注入字段(供单测 httptest 替换上游):
//   - endpointURL: 默认 AIEndpointDefault
//   - client:      默认 netutil.NewClient(120s),共享 transport 防连接泄漏
//   - model:       默认 AIModelDefault
type AIPriceGenerator struct {
	getAccountTokens func(id string) (string, string, string, error)
	refreshAccount   func(id string) (string, error)
	logFn            func(string)
	endpointURL      string
	client           *http.Client
	model            string
}

// AIPriceGeneratorOption 是 NewAIPriceGenerator 的可选项,用于覆盖默认端点/客户端/模型。
type AIPriceGeneratorOption func(*AIPriceGenerator)

// WithAIEndpoint 覆盖直连端点(单测注入 httptest.Server.URL)。
func WithAIEndpoint(url string) AIPriceGeneratorOption {
	return func(g *AIPriceGenerator) { g.endpointURL = url }
}

// WithAIClient 覆盖 http.Client(单测注入指向 httptest 的 client)。
func WithAIClient(c *http.Client) AIPriceGeneratorOption {
	return func(g *AIPriceGenerator) { g.client = c }
}

// WithAIModel 覆盖生成模型(单测注入小模型)。
func WithAIModel(m string) AIPriceGeneratorOption {
	return func(g *AIPriceGenerator) { g.model = m }
}

// NewAIPriceGenerator 构造一个 AI 定价生成器。getAccountTokens/refreshAccount 必填,
// logFn 可为 nil。未指定 opts 时用 AIEndpointDefault / netutil.NewClient(120s) /
// AIModelDefault。与 app_lifecycle.go 构造 PacketCapturer 的闭包注入一一对应。
func NewAIPriceGenerator(
	getAccountTokens func(id string) (string, string, string, error),
	refreshAccount func(id string) (string, error),
	logFn func(string),
	opts ...AIPriceGeneratorOption,
) *AIPriceGenerator {
	g := &AIPriceGenerator{
		getAccountTokens: getAccountTokens,
		refreshAccount:   refreshAccount,
		logFn:            logFn,
		endpointURL:       AIEndpointDefault,
		client:            netutil.NewClient(120 * time.Second),
		model:             AIModelDefault,
	}
	for _, opt := range opts {
		opt(g)
	}
	return g
}

// logf 统一的 [AI-Price] 前缀日志,nil logFn 静默(单测未注入),与 OCRService.logf 同款守卫。
func (g *AIPriceGenerator) logf(format string, args ...interface{}) {
	if g == nil || g.logFn == nil {
		return
	}
	g.logFn(fmt.Sprintf("[AI-Price] "+format, args...))
}

// notifyProgress 是 progressFn 的 nil-safe 守卫包装,与 logf 同款空值静默。
func (g *AIPriceGenerator) notifyProgress(progressFn func(stage, status string), stage, status string) {
	if progressFn == nil {
		return
	}
	progressFn(stage, status)
}

// Generate 对 models 候选列表生成单价,返回 {模型基名: AIPriceResult}。
// accountId 必须指向一个已登录且 token 有效的 Antigravity 账号。
// progressFn 是阶段进度回调(可为 nil):在取 token / 联网 / 降级 / 刷新 / 解析 / 完成
// 边界被调用,上层经 wails EventsEmit 推送前端实时进度。
//
// 返回 map 的键严格等于输入 models 各元素(按下标对齐),与 AI 返回 name 字段解耦,
// 避免大小写/空白漂移导致前端表格行对不上。AI 返回条目缺失时填 {0,0,0} 不报错,
// 让用户在表格里手动补并标 Estimated。models 为空时直接返回空 map(不算错误)。
//
// AIPriceResult 承载 Rate + 三个质检标记:
//   - Grounded:       本次结果是否真经联网检索取到 grounding 来源(groundingChunks 非空)
//   - Estimated:      AI 自标估算(prompt 第 6 条)或后端 Cached 0.25× 兜底估算
//   - AnchorConflict: 与 realPricingAnchor 真实厂商标价偏差 > 2x,前端打红色警示
//   - Sources:        grounding 引用的官方价页 URL,前端可点开核对
func (g *AIPriceGenerator) Generate(models []string, accountId string, progressFn func(stage, status string)) (map[string]AIPriceResult, error) {
	result := make(map[string]AIPriceResult, len(models))
	if len(models) == 0 {
		return result, nil
	}
	if g == nil || g.getAccountTokens == nil {
		return result, errors.New("AIPriceGenerator 未就绪")
	}

	g.notifyProgress(progressFn, "fetch-token", "正在获取账号凭证...")
	accessToken, refreshToken, projectID, err := g.getAccountTokens(accountId)
	if err != nil {
		g.notifyProgress(progressFn, "error", "获取账号 Token 失败: "+err.Error())
		return result, fmt.Errorf("获取账号 Token 失败: %w", err)
	}
	if accessToken == "" {
		g.notifyProgress(progressFn, "error", "该账号暂无有效的 Access Token")
		return result, errors.New("该账号暂无有效的 Access Token")
	}
	if projectID == "" {
		projectID = "favorable-synapse-ttvcb"
	}

	// 先尝试带 googleSearch grounding 联网检索,失败再降级纯知识。
	g.notifyProgress(progressFn, "grounding-search", "正在联网检索各厂商官方定价页...")
	// text 与 sources 一起追踪本次(及可能降级重试后的最终)联网结果。
	text, sources, err := g.generateOnce(accessToken, projectID, models, true)
	// grounding 失败(非 401,即非鉴权问题)则去掉 tools 重试一次纯知识。
	if err != nil && !isAuthError(err) && isGroundingRejection(err) {
		g.logf("grounding 工具被上游拒收(%v),降级为纯知识库生成", err)
		g.notifyProgress(progressFn, "grounding-degraded", "联网检索被上游拒收,降级为知识库估算(结果将标「未联网」)")
		text, sources, err = g.generateOnce(accessToken, projectID, models, false)
	}
	// 401 鉴权失败 + 有 refreshToken → 刷新 token 后重试一次(与 packet.go:590 一致)。
	if err != nil && isAuthError(err) && refreshToken != "" && g.refreshAccount != nil {
		g.logf("Token 过期(账号 %s),刷新后重试...", accountId)
		g.notifyProgress(progressFn, "token-refresh", "账号凭证过期,正在自动刷新...")
		newToken, refreshErr := g.refreshAccount(accountId)
		if refreshErr != nil {
			g.notifyProgress(progressFn, "error", "账号 Token 过期且自动刷新失败")
			return result, fmt.Errorf("账号 Token 过期且自动刷新失败: %v", refreshErr)
		}
		g.notifyProgress(progressFn, "grounding-search", "凭证已刷新,正在联网检索厂商官方定价页...")
		// 刷新后优先按原 wantTools=true 重试(若 401 发生在 grounding 阶段而上游其实支持工具)。
		text, sources, err = g.generateOnce(newToken, projectID, models, true)
		if err != nil && !isAuthError(err) && isGroundingRejection(err) {
			text, sources, err = g.generateOnce(newToken, projectID, models, false)
		}
	}
	if err != nil {
		g.notifyProgress(progressFn, "error", "生成失败: "+err.Error())
		return result, err
	}

	g.notifyProgress(progressFn, "parse-result", "正在解析定价结果...")
	grounded := len(sources) > 0
	extracted := extractPricingJSON(text)
	for _, name := range models {
		key := strings.ToLower(strings.TrimSpace(name))
		parsed, ok := extracted[key]
		if !ok {
			// AI 没按顺序返回或缺位:补零并标估算,前端表格仍渲染该行让用户手动填。
			result[name] = AIPriceResult{
				Rate:      ModelRate{Input: 0, Output: 0, Cached: 0},
				Estimated: true,
				Grounded:  false,
			}
			continue
		}
		rate := parsed.Rate
		estimated := parsed.Estimated
		if rate.Cached <= 0 && rate.Input > 0 {
			// 无官方缓存定价时按输入单价 0.25 倍兜底估算,与 defaultPricing
			// (claude 3→0.75=0.25×3、gpt-oss 0.15→0.0375=0.25×0.15)保持口径一致。
			// 兜底估算一律标 Estimated,前端提示用户核对 cached。
			rate.Cached = rate.Input * 0.25
			estimated = true
		}
		// 单条来源 URL(AI 在 prompt 第 8 条返回的 source_url)优先并入 Sources,
		// 与 SSE 帧里抽取的 grounding 来源合并展示。
		rowSources := sources
		if parsed.SourceURL != "" {
			seen := false
			for _, s := range rowSources {
				if s.URI == parsed.SourceURL {
					seen = true
					break
				}
			}
			if !seen {
				if rowSources == nil {
					rowSources = []WebRef{{URI: parsed.SourceURL}}
				} else {
					rowSources = append(rowSources, WebRef{URI: parsed.SourceURL})
				}
			}
		}
		result[name] = AIPriceResult{
			Rate:          rate,
			Grounded:      grounded,
			Estimated:     estimated,
			AnchorConflict: anchorConflictForModel(key, rate),
			Sources:       rowSources,
		}
	}
	g.notifyProgress(progressFn, "done", "生成完成")
	return result, nil
}

// generateOnce 用 token 调一次上游生成定价文本与 grounding 来源。wantTools=true 时带
// googleSearch 工具,false 时纯知识。返回拼接后的纯文本 + grounding 来源引用,或带分类的错误。
//
// 错误分类(供 Generate 决定重试策略):
//   - 鉴权错误(isAuthError):含 "HTTP 401" 文本 → 上层走 refreshAccount 重试
//   - grounding 拒收(isGroundingRejection):HTTP 400 + grounding 关键词 → 上层去 tools 重试
//   - 其他: 直接返回,上层不重试
func (g *AIPriceGenerator) generateOnce(token, projectID string, models []string, wantTools bool) (string, []WebRef, error) {
	if g == nil || g.client == nil {
		return "", nil, errors.New("AIPriceGenerator: nil service or client")
	}
	prompt := buildPricingPrompt(models)
	reqBodyMap := buildAIRequestEnvelope(projectID, prompt, g.model, wantTools)
	jsonBody, err := json.Marshal(reqBodyMap)
	if err != nil {
		return "", nil, fmt.Errorf("marshal AI pricing request: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.endpointURL, bytes.NewBuffer(jsonBody))
	if err != nil {
		return "", nil, fmt.Errorf("create AI pricing request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "antigravity/ide/2.8.4 windows/amd64")
	req.Header.Set("X-Goog-Api-Client", "gl-node/22.21.1")

	resp, err := g.client.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("execute AI pricing request: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		errMsg := fmt.Sprintf("HTTP %d: %s", resp.StatusCode, truncateBody(bodyBytes))
		// 探测 error.message 是否含 grounding/tool/search 拒收关键词。
		var errProbe struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(bodyBytes, &errProbe)
		if errProbe.Error.Message != "" {
			errMsg = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, errProbe.Error.Message)
		}
		return "", nil, errors.New(errMsg)
	}

	ex, err := extractSSETextWithMeta(bodyBytes)
	if err != nil {
		return "", nil, err
	}
	return ex.Text, ex.Sources, nil
}
