// Package modelfetch 负责从上游 OpenAI 兼容端点拉取可用模型列表。
//
// 核心能力:对单个 Base URL 生成一组「候选模型端点」并按序尝试。
// 这是为了解决第三方聚合站 / 把 Anthropic 协议挂在兼容子路径上的官方供应商
// (DeepSeek、Kimi、智谱 GLM 等)在「获取模型」时容易遇到 404 的问题:
// 例如 https://api.deepseek.com/anthropic 本身不开放 /v1/models,但它剥离
// /anthropic 后的根域 https://api.deepseek.com/v1/models 可列模型。
// 候选兜底 + 404/405 续试的策略,让只要上游任一协议端点开放模型列表即能命中,
// 而不必要求当前 Base URL 路径恰好开放 /v1/models。
//
// 设计移植自 cc-switch 的 services/model_fetch.rs,裁掉了本项目用不到的
// is_full_url / models_url_override 入参,只保留候选生成 + 续试 + 兼容子路径
// 剥离 + 版本段判定四项核心。
package modelfetch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"antigravity-proxy/internal/netutil"
)

// FETCH_TIMEOUT_SECS 单次候选请求的超时秒数。404/405 由上游即时返回不占此时长,
// 仅当某候选连接挂起时才会触发,避免单点不响应拖垮整轮拉取。
const FETCH_TIMEOUT_SECS = 15

// ERROR_BODY_MAX_CHARS 失败响应体截断长度:避免把几十 KB 的 HTML 404 页整页保留到错误串里污染 UI。
const ERROR_BODY_MAX_CHARS = 512

// KNOWN_COMPAT_SUFFIXES 已知的「Anthropic 协议兼容子路径」后缀,按长度降序排列。
// 长度降序是关键:确保最长前缀优先命中(否则 /anthropic 会提前匹配掉 /api/anthropic,
// 把它剥成残缺的 https://.../api 根而非干净的 https://... 根)。
var KNOWN_COMPAT_SUFFIXES = []string{
	"/api/claudecode",
	"/api/anthropic",
	"/apps/anthropic",
	"/api/coding",
	"/claudecode",
	"/anthropic",
	"/step_plan",
	"/coding",
	"/claude",
}

// maxCandidates 候选列表容量上限。候选项本就稀疏(主候选 + 最多 2 条剥离兜底),
// 限制上界避免极端路径下候选数膨胀。
const maxCandidates = 4

// FetchModels 拉取上游可用模型列表,按候选端点顺序尝试。
// 兼容 {data:[{id}]} 与 {models:[{id}]} 两种响应形态,去重空 id 后排序返回。
//
// 行为契约:
//   - 任一候选返回 2xx 且可解析 → 立即返回模型列表(后续候选不再尝试)。
//   - 候选返回 404 / 405 → 视为该候选不支持模型列表端点,记错误后续试下一个候选。
//   - 候选返回其他非 2xx(401/403/500 等)→ 立即返回该错误(fail-fast,不续试)。
//   - 候选发生网络错误(连接失败 / DNS 解析失败)→ 立即返回该错误(同 host 多候选同源失败,续试无价值)。
//   - 所有候选均为 404/405 → 返回带 "All candidates failed" 前缀的汇总错误。
func FetchModels(baseURL, apiKey string) ([]string, error) {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" {
		return nil, fmt.Errorf("baseURL 为空,无法构造模型列表端点")
	}

	candidates, err := BuildCandidates(trimmed)
	if err != nil {
		return nil, err
	}

	client := netutil.NewClient(FETCH_TIMEOUT_SECS * time.Second)
	var lastErr string

	for _, endpoint := range candidates {
		models, perr := fetchOneEndpoint(client, endpoint, apiKey)
		if perr == nil {
			return models, nil
		}
		if !isEndpointNotFound(perr) {
			// 非 404/405(含网络错误、其他 HTTP 状态、解析错误):fail-fast。
			return nil, perr
		}
		lastErr = perr.Error()
	}

	return nil, fmt.Errorf("All candidates failed: %s", fallbackStr(lastErr, "no candidates"))
}

func fallbackStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// fetchOneEndpoint 对单个端点发起请求并解析。
// 成功返回模型列表(nil error);失败返回结构化错误(errKind 标注 404/405/网络/其他)。
func fetchOneEndpoint(client *http.Client, endpoint, apiKey string) ([]string, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("创建模型获取请求失败: %v", err)
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("网络请求失败: %v", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %v", err)
	}

	if resp.StatusCode == http.StatusOK {
		return parseModelsBody(bodyBytes)
	}

	bodySnip := truncateErrBody(bodyBytes)
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, bodySnip)
	}
	return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, bodySnip)
}

// isEndpointNotFound 判断错误是否为「该候选不支持模型列表端点」的可续试错误(404/405)。
// 解析失败 / 网络错误 / 其他 HTTP 状态均不视为可续试。
func isEndpointNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.HasPrefix(msg, "HTTP 404:") || strings.HasPrefix(msg, "HTTP 405:")
}

// parseModelsBody 解析 OpenAI 兼容的模型列表响应体,兼容两种形态:
//   - {"data":[{"id":"..."}]}
//   - {"models":[{"id":"..."}]}
//
// 去重空 id 后按字典序排序返回。
func parseModelsBody(bodyBytes []byte) ([]string, error) {
	var parseRes struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []struct {
			ID string `json:"id"`
		} `json:"models"`
	}
	if err := json.Unmarshal(bodyBytes, &parseRes); err != nil {
		return nil, fmt.Errorf("解析模型数据失败: %v", err)
	}

	modelSet := make(map[string]bool)
	for _, item := range parseRes.Data {
		if item.ID != "" {
			modelSet[item.ID] = true
		}
	}
	for _, item := range parseRes.Models {
		if item.ID != "" {
			modelSet[item.ID] = true
		}
	}
	models := make([]string, 0, len(modelSet))
	for m := range modelSet {
		models = append(models, m)
	}
	sort.Strings(models)
	return models, nil
}

// BuildCandidates 为单个 Base URL 构造「模型列表端点」的候选 URL 列表。
//
// 候选顺序(去重保序):
//  1. baseURL 已以版本段 /v{N} 结尾(如 /v1、智谱 /api/coding/paas/v4):
//     版本号已在路径里,模型端点是 {base}/models(不能再补 /v1,否则 .../v{N}/v1/models → 404)。
//     版本段非 /v1 时,再追加 {base}/v1/models 作为兜底次候选。
//  2. 否则:推 {base}/v1/models。
//  3. 若 baseURL 命中 KNOWN_COMPAT_SUFFIXES,剥离后缀得到 root,再推 {root}/v1/models、{root}/models。
//
// 入参 baseURL 应为已 trim 的非空字符串。
func BuildCandidates(baseURL string) ([]string, error) {
	trimmed := strings.TrimSpace(strings.TrimRight(baseURL, "/"))
	if trimmed == "" {
		return nil, fmt.Errorf("baseURL 为空,无法构造模型列表端点")
	}

	var candidates []string

	if endsWithVersionSegment(trimmed) {
		candidates = append(candidates, trimmed+"/models")
		// 版本段非 /v1 时,保留旧的 /v1/models 作为兜底次候选(正确路径已排在前)。
		if !strings.HasSuffix(trimmed, "/v1") {
			candidates = append(candidates, trimmed+"/v1/models")
		}
	} else {
		candidates = append(candidates, trimmed+"/v1/models")
	}

	if stripped, ok := stripCompatSuffix(trimmed); ok {
		root := strings.TrimRight(stripped, "/")
		if root != "" && strings.Contains(root, "://") {
			candidates = append(candidates, root+"/v1/models")
			candidates = append(candidates, root+"/models")
		}
	}

	return dedupePreserveOrder(candidates), nil
}

// stripCompatSuffix 若 baseURL 以任一已知兼容子路径结尾,返回剥离后的剩余部分。
// 依赖 KNOWN_COMPAT_SUFFIXES 按长度降序,确保最长前缀优先命中。
func stripCompatSuffix(baseURL string) (string, bool) {
	for _, suffix := range KNOWN_COMPAT_SUFFIXES {
		if strings.HasSuffix(baseURL, suffix) {
			return baseURL[:len(baseURL)-len(suffix)], true
		}
	}
	return "", false
}

// endsWithVersionSegment 判断 URL 是否以 OpenAI 风格的版本段 /v{N} 结尾(N 为一个或多个数字),
// 例如 /v1、.../paas/v4。这类 URL 版本号已在路径中,模型端点应为 {base}/models。
func endsWithVersionSegment(url string) bool {
	last := ""
	if idx := strings.LastIndex(url, "/"); idx >= 0 {
		last = url[idx+1:]
	} else {
		last = url
	}
	if !strings.HasPrefix(last, "v") {
		return false
	}
	digits := last[1:]
	if digits == "" {
		return false
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return false
		}
	}
	return true
}

// truncateErrBody 截断失败响应体到 ERROR_BODY_MAX_CHARS 字符(rune 计),避免 HTML 404 页占用错误串。
func truncateErrBody(body []byte) string {
	if len(body) <= ERROR_BODY_MAX_CHARS {
		return string(body)
	}
	runeCount := 0
	for i := range body {
		runeCount++
		if runeCount == ERROR_BODY_MAX_CHARS {
			return string(body[:i]) + "…"
		}
	}
	return string(body)
}

// dedupePreserveOrder 对候选列表去重并保持首次出现顺序。
// 候选本就稀疏(含不同路径),线性去重足够,不值得上 map 查重的复杂度。
func dedupePreserveOrder(candidates []string) []string {
	seen := make(map[string]bool, len(candidates))
	out := make([]string, 0, min(len(candidates), maxCandidates))
	for _, c := range candidates {
		if seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
		if len(out) >= maxCandidates {
			break
		}
	}
	return out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
