// Package pricing: aigen.go —— AI 一键生成计费配置的后端实现。
//
// 用途:用户在「计费配置」面板点 AI 一键生成按钮后,前端把"模型统计里出现、但
// 尚未登记单价的候选模型基名[]"传过来,本生成器调用一个 Gemini 模型(默认
// gemini-2.5-flash-lite,经用户选定的 Antigravity 账号直连 daily-cloudcode-pa
// /v1internal:streamGenerateContent?alt=sse)生成建议单价(USD/每百万 tokens)。
//
// 链路复用:与 internal/stats/packet.go 的 AnalyzePackets 完全同构 ——
//   - 同样的 v1internal 信封(外层 project/requestId/request/model/userAgent/
//     requestType/enabledCreditTypes,内层 request 是 Gemini generateContent 体);
//   - 同样的 SSE 抽取(candidates[0].content.parts[0].text 拼接);
//   - 同样的 401 + refreshToken 自动 refreshAccount 重试一次。
// 因 pricing 不应反向 import stats(那会拉进抓包/磁盘等无关依赖),SSE 抽取与
// 信封构造就地复制为本包内私有函数,语义与 packet.go 逐字对齐,零行为漂移。
//
// 取数策略:先尝试携带 googleSearch grounding 工具做联网检索以拿实时公开定价;
// 若上游拒收工具(HTTP 非 200 或错误文本含 grounding/tool/search 关键词)则
// 去掉 tools 再发一次,降级为纯 LLM 训练知识生成。grounding 兜底失败不影响
// 最终可用性,只影响单价新发布模型的准确性,最终由用户人工确认兜底。
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
		client:           netutil.NewClient(120 * time.Second),
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

// Generate 对 models 候选列表生成单价,返回 {模型基名: ModelRate}。
// accountId 必须指向一个已登录且 token 有效的 Antigravity 账号。
//
// 返回 map 的键严格等于输入 models 各元素(按下标对齐),与 AI 返回 name 字段解耦,
// 避免大小写/空白漂移导致前端表格行对不上。AI 返回条目缺失时填 {0,0,0} 不报错,
// 让用户在表格里手动补。models 为空时直接返回空 map(不算错误)。
func (g *AIPriceGenerator) Generate(models []string, accountId string) (map[string]ModelRate, error) {
	result := make(map[string]ModelRate, len(models))
	if len(models) == 0 {
		return result, nil
	}
	if g == nil || g.getAccountTokens == nil {
		return result, errors.New("AIPriceGenerator 未就绪")
	}

	accessToken, refreshToken, projectID, err := g.getAccountTokens(accountId)
	if err != nil {
		return result, fmt.Errorf("获取账号 Token 失败: %w", err)
	}
	if accessToken == "" {
		return result, errors.New("该账号暂无有效的 Access Token")
	}
	if projectID == "" {
		projectID = "favorable-synapse-ttvcb"
	}

	// 先尝试带 googleSearch grounding 联网检索,失败再降级纯知识。
	text, err := g.generateOnce(accessToken, projectID, models, true)
	// grounding 失败(非 401,即非鉴权问题)则去掉 tools 重试一次纯知识。
	if err != nil && !isAuthError(err) && isGroundingRejection(err) {
		g.logf("grounding 工具被上游拒收(%v),降级为纯知识库生成", err)
		text, err = g.generateOnce(accessToken, projectID, models, false)
	}
	// 401 鉴权失败 + 有 refreshToken → 刷新 token 后纯知识重试一次(与 packet.go:590 一致)。
	if err != nil && isAuthError(err) && refreshToken != "" && g.refreshAccount != nil {
		g.logf("Token 过期(账号 %s),刷新后重试...", accountId)
		newToken, refreshErr := g.refreshAccount(accountId)
		if refreshErr != nil {
			return result, fmt.Errorf("账号 Token 过期且自动刷新失败: %v", refreshErr)
		}
		// 刷新后直接用纯知识重试(grounding 已被证明拒收则不重试工具;
		// 但若 401 发生在 grounding 阶段而上游其实支持工具,保险起见按原 wantTools 重试)。
		text, err = g.generateOnce(newToken, projectID, models, true)
		if err != nil && !isAuthError(err) && isGroundingRejection(err) {
			text, err = g.generateOnce(newToken, projectID, models, false)
		}
	}
	if err != nil {
		return result, err
	}

	extracted := extractPricingJSON(text)
	for _, name := range models {
		rate, ok := extracted[name]
		if !ok {
			// AI 没按顺序返回或缺位:补零,前端表格仍渲染该行让用户手动填。
			rate = ModelRate{Input: 0, Output: 0, Cached: 0}
		} else if rate.Cached <= 0 && rate.Input > 0 {
			// 无官方缓存定价时按输入单价 0.25 倍兜底估算,与 defaultPricing
			// (claude 3→0.75=0.25×3、gpt-oss 0.15→0.0375=0.25×0.15)保持口径一致。
			rate.Cached = rate.Input * 0.25
		}
		result[name] = rate
	}
	return result, nil
}

// generateOnce 用 token 调一次上游生成定价文本。wantTools=true 时带 googleSearch 工具,
// false 时纯知识。返回拼接后的纯文本或带分类的错误。
//
// 错误分类(供 Generate 决定重试策略):
//   - 鉴权错误(isAuthError):含 "HTTP 401" 文本 → 上层走 refreshAccount 重试
//   - grounding 拒收(isGroundingRejection):HTTP 400 + grounding/tool/search 关键词 → 上层去 tools 重试
//   - 其他: 直接返回,上层不重试
func (g *AIPriceGenerator) generateOnce(token, projectID string, models []string, wantTools bool) (string, error) {
	if g == nil || g.client == nil {
		return "", errors.New("AIPriceGenerator: nil service or client")
	}
	prompt := buildPricingPrompt(models)
	reqBodyMap := buildAIRequestEnvelope(projectID, prompt, g.model, wantTools)
	jsonBody, err := json.Marshal(reqBodyMap)
	if err != nil {
		return "", fmt.Errorf("marshal AI pricing request: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.endpointURL, bytes.NewBuffer(jsonBody))
	if err != nil {
		return "", fmt.Errorf("create AI pricing request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "antigravity/ide/2.8.4 windows/amd64")
	req.Header.Set("X-Goog-Api-Client", "gl-node/22.21.1")

	resp, err := g.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("execute AI pricing request: %w", err)
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
		return "", errors.New(errMsg)
	}

	return extractSSEText(bodyBytes)
}

// buildAIRequestEnvelope 构造 v1internal 信封,与 packet.go:527-554 逐段对齐。
// wantTools=true 时在内层 request tools 里加一个 googleSearch 顶层工具做联网检索。
func buildAIRequestEnvelope(projectID, prompt, model string, wantTools bool) map[string]interface{} {
	requestInner := map[string]interface{}{
		"contents": []interface{}{
			map[string]interface{}{
				"role": "user",
				"parts": []interface{}{
					map[string]interface{}{"text": prompt},
				},
			},
		},
		"generationConfig": map[string]interface{}{
			"maxOutputTokens": AIMaxOutputTokens,
			"thinkingConfig": map[string]interface{}{
				"includeThoughts": false,
				"thinkingBudget":  0,
			},
		},
	}
	if wantTools {
		requestInner["tools"] = []interface{}{
			map[string]interface{}{"google_search": map[string]interface{}{}},
		}
	}
	return map[string]interface{}{
		"project":   projectID,
		"requestId": fmt.Sprintf("aiprice/%d", time.Now().UnixNano()),
		"request":   requestInner,
		"model":     model,
		"userAgent": "antigravity",
		// requestType 与 enabledCreditTypes 与 AnalyzePackets 对齐,
		// 让上游把这次调用计入 GOOGLE_ONE_AI 计费档(与抓包分析路径同信任域)。
		"requestType":        "chat",
		"enabledCreditTypes": []string{"GOOGLE_ONE_AI"},
	}
}

// extractSSEText 从 daily-cloudcode-pa 的 SSE 响应里抽取候选文本,
// 逻辑与 packet.go:617-688 的 SSE/JSON 双形态抽取逐字对齐(就地复制,避免 import stats):
//   - 优先按 "data:" 行解析,逐行取 response.candidates[0].content.parts[0].text 拼接;
//   - 非 SSE 形态时按普通 JSON 解析同一候选路径。
func extractSSEText(bodyBytes []byte) (string, error) {
	bodyStr := strings.TrimSpace(string(bodyBytes))
	if strings.HasPrefix(bodyStr, "data:") {
		var fullText strings.Builder
		lines := strings.Split(bodyStr, "\n")
		for _, line := range lines {
			cleanLine := strings.TrimSpace(line)
			if !strings.HasPrefix(cleanLine, "data:") {
				continue
			}
			jsonStr := strings.TrimSpace(cleanLine[5:])
			var data map[string]interface{}
			if json.Unmarshal([]byte(jsonStr), &data) != nil {
				continue
			}
			if t := firstCandidateText(data); t != "" {
				fullText.WriteString(t)
			}
		}
		if fullText.Len() > 0 {
			return fullText.String(), nil
		}
		return "", errors.New("AI 定价 SSE 响应中未包含任何文本内容")
	}

	// 普通 JSON 形态(非流式响应)。
	var respJson map[string]interface{}
	if json.Unmarshal(bodyBytes, &respJson) == nil {
		if t := firstCandidateText(respJson); t != "" {
			return t, nil
		}
	}
	return "", fmt.Errorf("解析 AI 定价响应失败,原始响应前300字符: %s", truncateBody(bodyBytes))
}

// firstCandidateText 从一个响应对象里抽取 candidates[0].content.parts[0].text,
// 兼容响应直接就是候选对象或被包在 "response" 键下两种形态(与 packet.go 同款)。
func firstCandidateText(data map[string]interface{}) string {
	var resObj interface{}
	if val, ok := data["response"]; ok {
		resObj = val
	} else {
		resObj = data
	}
	resMap, ok := resObj.(map[string]interface{})
	if !ok {
		return ""
	}
	candidates, ok := resMap["candidates"].([]interface{})
	if !ok || len(candidates) == 0 {
		return ""
	}
	candidateMap, ok := candidates[0].(map[string]interface{})
	if !ok {
		return ""
	}
	content, ok := candidateMap["content"].(map[string]interface{})
	if !ok {
		return ""
	}
	parts, ok := content["parts"].([]interface{})
	if !ok || len(parts) == 0 {
		return ""
	}
	partMap, ok := parts[0].(map[string]interface{})
	if !ok {
		return ""
	}
	if text, ok := partMap["text"].(string); ok {
		return text
	}
	return ""
}

// extractPricingJSON 从 AI 返回的纯文本里取第一个 '[' 到最后一个 ']' 的子串,
// json.Unmarshal 到 []aiPriceEntry,再以 "name 低小写" → ModelRate 的 map 返回。
// 兼容裸数组、```json 代码块包裹、带前导解释文本三种形态。
// 解析失败或提取不到数组时返回空 map(Generate 会给每个输入模型补零)。
func extractPricingJSON(text string) map[string]ModelRate {
	result := map[string]ModelRate{}
	start := strings.Index(text, "[")
	end := strings.LastIndex(text, "]")
	if start < 0 || end < 0 || end <= start {
		return result
	}
	jsonStr := text[start : end+1]
	var entries []aiPriceEntry
	if err := json.Unmarshal([]byte(jsonStr), &entries); err != nil {
		return result
	}
	for _, e := range entries {
		if strings.TrimSpace(e.Name) == "" {
			continue
		}
		result[strings.ToLower(strings.TrimSpace(e.Name))] = ModelRate{
			Input:  e.Input,
			Output: e.Output,
			Cached: e.Cached,
		}
	}
	return result
}

// aiPriceEntry 是 AI 返回的定价数组单元素结构。
type aiPriceEntry struct {
	Name   string  `json:"name"`
	Input  float64 `json:"input"`
	Output float64 `json:"output"`
	Cached float64 `json:"cached"`
}

// buildPricingPrompt 构造让 AI 给一批模型生成 USD/每百万 tokens 单价的提示词。
// 强约束:仅输出 JSON 数组、顺序与输入一致、不许返回 0/留空、无缓存价按输入 0.25× 估算、
// 不确定给最合理估算。模型名作为键保留原始基名(已由前端清洗掉前缀)直接回传。
func buildPricingPrompt(models []string) string {
	var b strings.Builder
	b.WriteString("你是资深云模型计费定价分析师。请基于你的训练知识,为下面这批 AI 模型给出公开标准定价,")
	b.WriteString("单位统一为 USD/每百万 Tokens(United States Dollar per one million tokens),")
	b.WriteString("包含输入(input)、输出(output)、缓存命中(cached)三类单价。\n\n")
	b.WriteString("严格要求:\n")
	b.WriteString("1. 仅输出一个 JSON 数组,绝对不要 markdown 代码块标记(```),不要任何解释性前言或总结,")
	b.WriteString("回答的第一个字符必须是 '['。\n")
	b.WriteString("2. 数组长度与顺序必须与下方输入模型列表完全一致,每个元素形如 ")
	b.WriteString(`{"name":"模型名","input":数值,"output":数值,"cached":数值}。` + "\n")
	b.WriteString("3. name 字段回传我给你的模型原始名称,不要改名不要加引号外符号。\n")
	b.WriteString("4. input/output 是厂家公布的标准公开单价,精确到 6 位小数;如果你确信该模型有公开定价,")
	b.WriteString("必须给真实数值,不许返回 0、不许留空、不许写 null。\n")
	b.WriteString("5. cached 是缓存命中输入的单价(各厂常用输入价的 1/4)。若该模型厂家明确公布缓存定价用公布值;")
	b.WriteString("若未公布,则按 input 单价乘以 0.25 估算填入 cached 字段,不要留空。\n")
	b.WriteString("6. 若是较新的模型你不确定准确数字,给出你认为最合理的估算值(基于同系列相邻型号的定价规律),")
	b.WriteString("不允许返回 0 或空。\n\n")
	b.WriteString("需定价的模型列表(共 ")
	b.WriteString(fmt.Sprintf("%d 个):", len(models)))
	for i, m := range models {
		if i%6 == 0 {
			b.WriteString("\n")
		}
		b.WriteString(m)
		if i < len(models)-1 {
			b.WriteString(", ")
		}
	}
	b.WriteString("\n\n现在请直接输出 JSON 数组,第一个字符必须是 '['。")
	return b.String()
}

// isAuthError 判断错误是否为鉴权失败,上层据此触发 refreshAccount 重试。
func isAuthError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "HTTP 401")
}

// isGroundingRejection 判断错误是否疑似上游拒收 googleSearch 工具,
// 上层据此决定是否去掉 tools 降级为纯知识库生成。
// 命中条件:HTTP 400 且错误文本含 grounding/tool/search 任一关键词。
func isGroundingRejection(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "http 400") {
		return false
	}
	return strings.Contains(msg, "grounding") ||
		strings.Contains(msg, "google_search") ||
		strings.Contains(msg, "googlesearch") ||
		strings.Contains(msg, "tool") ||
		strings.Contains(msg, "search")
}

// truncateBody 把过长的错误响应体截断,避免污染日志与错误串(与 packet.go 路径同款)。
func truncateBody(b []byte) string {
	if len(b) > 300 {
		return string(b[:300])
	}
	return string(b)
}
