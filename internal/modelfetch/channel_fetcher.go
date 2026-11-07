package modelfetch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/netutil"
)

var (
	storedProjectResolverMu sync.RWMutex
	storedProjectResolver   func(email string) string
)

// SetStoredProjectResolver 注入全局账号专属 Project ID 查询闭包（通常绑定 quotaSvc.GetStoredProject）。
func SetStoredProjectResolver(fn func(email string) string) {
	storedProjectResolverMu.Lock()
	storedProjectResolver = fn
	storedProjectResolverMu.Unlock()
}

func resolveStoredProjectForEmail(email string) string {
	storedProjectResolverMu.RLock()
	fn := storedProjectResolver
	storedProjectResolverMu.RUnlock()
	if fn == nil || strings.TrimSpace(email) == "" {
		return ""
	}
	return strings.TrimSpace(fn(email))
}

// FetchChannelAvailableModels provides headless support for fetching channel models
func FetchChannelAvailableModels(accountMgr *account.Manager, channel string) ([]string, error) {
	ch := strings.ToLower(strings.TrimSpace(channel))
	if ch == "" {
		ch = "google"
	}

	// 0. 对于 Google / Antigravity / GCP 号池，收集所有可用候选账号并按付费权重排序探测
	if isGoogleChannel(ch) {
		var googleCandidates []*account.Account
		if accountMgr != nil {
			rawAccounts := accountMgr.GetRawAccountsByProvider("all")
			for _, acc := range rawAccounts {
				if acc != nil && acc.Enabled && acc.GetAccessToken() != "" {
					accProv := strings.ToLower(strings.TrimSpace(acc.Provider))
					if (accProv == "" || isGoogleChannel(accProv)) && accProv != "2fa" && accProv != "nvidia" && accProv != "workbuddy" && accProv != "opencode" {
						googleCandidates = append(googleCandidates, acc)
					}
				}
			}
		}

		if len(googleCandidates) == 0 {
			return nil, fmt.Errorf("号池 [%s] 下暂无已启用的有效账号，请先在【账号池】中添加该号池账号", channel)
		}

		// 权重排序：具备 Argon 配额桶的账号与 Pro / Ultra 付费层级账号具备更高的模型权限（如 Gemini 4 Argon、Claude 5.5 等），优先排在前面探测
		candidatePriority := func(acc *account.Account) int {
			score := 1
			t := strings.ToLower(strings.TrimSpace(acc.Tier))
			if strings.Contains(t, "ultra") {
				score = 3
			} else if strings.Contains(t, "pro") {
				score = 2
			}
			if acc.HasArgonBucket != nil && *acc.HasArgonBucket {
				score += 10
			}
			return score
		}
		sort.SliceStable(googleCandidates, func(i, j int) bool {
			return candidatePriority(googleCandidates[i]) > candidatePriority(googleCandidates[j])
		})

		ua := ""
		if accountMgr != nil {
			ua = accountMgr.GetAntigravityUserAgent()
		}
		if strings.TrimSpace(ua) == "" {
			ua = account.FormatAntigravityUserAgent(account.DefaultAntigravityCliVersion)
		}

		// 跨账号探测与模型全集聚合（前 3 个代表性账号必探，且对尚未记录 SupportedModels 的账号补齐探测以建立模型能力画像）
		modelSet := make(map[string]bool)
		var firstErr error
		probedCount := 0
		for _, cand := range googleCandidates {
			if probedCount >= 3 && len(cand.SupportedModels) > 0 {
				for _, m := range cand.SupportedModels {
					modelSet[m] = true
				}
				continue
			}
			if probedCount > 0 && geminiInternalTransport == nil {
				time.Sleep(time.Duration(120+rand.Intn(230)) * time.Millisecond)
			}
			mList, err := fetchGeminiInternalModels(cand, ua)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			if accountMgr != nil && cand.ID != "" {
				accountMgr.SetAccountSupportedModels(cand.ID, mList)
			}
			for _, m := range mList {
				modelSet[m] = true
			}
			probedCount++
		}

		if len(modelSet) == 0 {
			if firstErr != nil {
				return nil, fmt.Errorf("打 Google 上游 v1internal:fetchAvailableModels 失败: %w", firstErr)
			}
			return nil, fmt.Errorf("Google 上游返回的模型列表为空")
		}

		var list []string
		for k := range modelSet {
			list = append(list, k)
		}
		sort.Strings(list)
		return list, nil
	}

	var activeAcc *account.Account
	if accountMgr != nil {
		rawAccounts := accountMgr.GetRawAccountsByProvider("all")
		for _, acc := range rawAccounts {
			if acc != nil && acc.Enabled {
				accProv := strings.ToLower(strings.TrimSpace(acc.Provider))
				if isGoogleChannel(ch) {
					// 只要账号属于 Google 族 (antigravity, project, google, gcp, gemini-cli 或空) 且 token 不为空
					if (accProv == "" || isGoogleChannel(accProv)) && accProv != "2fa" && acc.GetAccessToken() != "" {
						activeAcc = acc
						break
					}
				} else {
					if accProv == ch {
						activeAcc = acc
						break
					}
				}
			}
		}

		// 兜底：若寻找指定 Google 族标签未命中，回退查寻任意未冷却、具备 AccessToken 的 Antigravity/Google 账号
		if activeAcc == nil && isGoogleChannel(ch) {
			for _, acc := range rawAccounts {
				if acc != nil && acc.Enabled && acc.GetAccessToken() != "" && acc.Provider != "nvidia" && acc.Provider != "2fa" && acc.Provider != "workbuddy" && acc.Provider != "opencode" {
					activeAcc = acc
					break
				}
			}
		}
	}

	if activeAcc == nil {
		return nil, fmt.Errorf("号池 [%s] 下暂无已启用的有效账号，请先在【账号池】中添加该号池账号", channel)
	}

	// 1. 如果是 OpenAI 兼容第三方号池
	if ch == "nvidia" || ch == "deepseek" || ch == "qwen" || ch == "anthropic" || ch == "moonshot" || ch == "other" || ch == "grok" || activeAcc.Provider == "nvidia" || activeAcc.Provider == "grok" {
		baseURL := activeAcc.BaseURL
		apiKey := activeAcc.GetAccessToken()
		if baseURL == "" && ch == "nvidia" {
			baseURL = account.DefaultNvidiaBaseURL
		}
		if baseURL == "" {
			return nil, fmt.Errorf("账号 %s 未配置 BaseURL，无法打上游获取模型", activeAcc.Email)
		}

		models, err := FetchModels(baseURL, apiKey)
		if err != nil {
			return nil, fmt.Errorf("打上游 [%s] 获取模型失败: %w", baseURL, err)
		}
		if len(models) == 0 {
			return nil, fmt.Errorf("上游 [%s] 返回的模型列表为空", baseURL)
		}
		return models, nil
	}

	// 2. 如果是 WorkBuddy 号池
	if ch == "workbuddy" || activeAcc.Provider == "workbuddy" {
		details, err := FetchWorkBuddyAvailableModelsDetailed(accountMgr)
		if err != nil {
			return nil, err
		}
		return details.AllModels, nil
	}

	// 3. 如果是 OpenCode 号池
	if ch == "opencode" || activeAcc.Provider == "opencode" {
		models, err := fetchOpenCodeModels(activeAcc)
		if err != nil {
			return nil, fmt.Errorf("打 OpenCode 上游获取模型失败 (账号 %s): %w", activeAcc.Email, err)
		}
		if len(models) == 0 {
			return nil, fmt.Errorf("OpenCode 上游返回的模型列表为空")
		}
		return models, nil
	}

	return nil, fmt.Errorf("未知或不支持的号池渠道: %s", channel)
}

func isGoogleChannel(c string) bool {
	return c == "antigravity" || c == "project" || c == "google" || c == "gcp" || c == "gemini-cli"
}

var geminiInternalTransport http.RoundTripper

func getGeminiTransport() http.RoundTripper {
	if geminiInternalTransport != nil {
		return geminiInternalTransport
	}
	return netutil.NewTransport()
}

func fetchGeminiInternalModels(acc *account.Account, ua string) ([]string, error) {
	token := acc.GetAccessToken()
	if token == "" {
		return nil, fmt.Errorf("账号 AccessToken 为空")
	}

	projectID := strings.TrimSpace(acc.ProjectID)
	if projectID == "" && acc.Email != "" {
		projectID = resolveStoredProjectForEmail(acc.Email)
	}
	if projectID == "" {
		projectID = resolveStoredProjectForEmail("default")
	}
	if projectID == "" {
		projectID = "favorable-synapse-ttvcb"
	}

	reqBody, _ := json.Marshal(map[string]string{
		"project": projectID,
	})

	targetURL := "https://daily-cloudcode-pa.googleapis.com/v1internal:fetchAvailableModels"
	req, err := http.NewRequestWithContext(context.Background(), "POST", targetURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(ua) == "" {
		ua = account.FormatAntigravityUserAgent(account.DefaultAntigravityCliVersion)
	}
	req.Header.Set("User-Agent", ua)

	var client *http.Client
	if geminiInternalTransport != nil {
		client = &http.Client{Timeout: 30 * time.Second, Transport: geminiInternalTransport}
	} else {
		accKey := acc.ID
		if strings.TrimSpace(accKey) == "" {
			accKey = acc.Email
		}
		client = netutil.GetAccountClient(accKey, 30*time.Second)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("网络连接失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}

	var parsed struct {
		Models map[string]struct {
			DisplayName string `json:"displayName"`
			Disabled    bool   `json:"disabled"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("解析上游响应 JSON 失败: %w", err)
	}

	var list []string
	for k, v := range parsed.Models {
		if v.Disabled || strings.HasSuffix(strings.ToLower(k), "-disabled") {
			continue
		}
		list = append(list, k)
	}
	sort.Strings(list)
	return list, nil
}

// FetchOtherGroupModels provides headless support for fetching Other pool group models
func FetchOtherGroupModels(accountMgr *account.Manager, groupID string, directBaseURL string, directAPIKey string) ([]string, error) {
	baseURL := directBaseURL
	apiKey := directAPIKey
	if baseURL == "" && accountMgr != nil {
		probeAcc := accountMgr.GetEnabledOtherAccounts(groupID)
		if len(probeAcc) > 0 {
			baseURL = probeAcc[0].BaseURL
			if apiKey == "" {
				apiKey = probeAcc[0].GetAccessToken()
			}
		}
	} else if apiKey == "" && accountMgr != nil {
		probeAcc := accountMgr.GetEnabledOtherAccounts(groupID)
		if len(probeAcc) > 0 {
			apiKey = probeAcc[0].GetAccessToken()
		}
	}

	if baseURL == "" {
		return nil, fmt.Errorf("组 [%s] 下暂无已启用账号或未提供 baseURL", groupID)
	}

	models, err := FetchModels(baseURL, apiKey)
	if err != nil {
		return nil, err
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("上游返回的模型列表为空")
	}
	return models, nil
}

// fetchWorkBuddyModels 从 WorkBuddy 官方配置端点 /v3/config 拉取可用模型列表。
func fetchWorkBuddyModels(acc *account.Account) ([]string, error) {
	baseURL := strings.TrimSpace(acc.BaseURL)
	if baseURL == "" || strings.Contains(baseURL, "codebuddy.ai") {
		if acc.IsWorkBuddyDomestic() {
			baseURL = account.DefaultWorkBuddyDomesticBaseURL
		} else {
			baseURL = account.DefaultWorkBuddyBaseURL
		}
	}

	configURL := strings.TrimRight(baseURL, "/") + "/v3/config"
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, configURL, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	headers := account.BuildWorkBuddyHeaders(acc)
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: 15 * time.Second, Transport: netutil.NewTransport()}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("网络连接失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}

	var parsed struct {
		Code int `json:"code"`
		Data struct {
			Models []struct {
				ID string `json:"id"`
			} `json:"models"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("解析上游响应 JSON 失败: %w", err)
	}

	seen := make(map[string]bool)
	var list []string
	for _, m := range parsed.Data.Models {
		id := strings.TrimSpace(m.ID)
		if id != "" && !seen[id] {
			seen[id] = true
			list = append(list, id)
		}
	}
	if len(list) == 0 {
		list = account.WorkBuddySupportedModels
	}
	return list, nil
}

// fetchOpenCodeModels 从 OpenCode 端点拉取可用模型列表，若上游暂无模型端点或失败则以内置清单兜底。
func fetchOpenCodeModels(acc *account.Account) ([]string, error) {
	baseURL := strings.TrimSpace(acc.BaseURL)
	if baseURL == "" {
		baseURL = account.DefaultOpenCodeBaseURL
	}
	apiKey := acc.GetAccessToken()

	// 优先尝试 OpenAI 规范的 FetchModels 端点查询
	if apiKey != "" {
		models, err := FetchModels(baseURL, apiKey)
		if err == nil && len(models) > 0 {
			return models, nil
		}
	}

	// 兜底返回 OpenCode 预置支持的模型列表
	if len(account.OpenCodeSupportedModels) > 0 {
		list := make([]string, len(account.OpenCodeSupportedModels))
		copy(list, account.OpenCodeSupportedModels)
		sort.Strings(list)
		return list, nil
	}

	return nil, fmt.Errorf("OpenCode 上游返回的模型列表为空")
}

// WorkBuddyFetchDetails 封装 WorkBuddy 渠道的模型双端探测结果。
type WorkBuddyFetchDetails struct {
	AllModels   []string `json:"allModels"`
	DomModels   []string `json:"domModels"`
	IntlModels  []string `json:"intlModels"`
	HasDomestic bool     `json:"hasDomestic"`
	HasIntl     bool     `json:"hasIntl"`
}

// FetchWorkBuddyAvailableModelsDetailed 分别探测 WorkBuddy 国内端与国际端可用模型并汇总。
func FetchWorkBuddyAvailableModelsDetailed(accountMgr *account.Manager) (*WorkBuddyFetchDetails, error) {
	var domAcc *account.Account
	var intlAcc *account.Account

	if accountMgr != nil {
		for _, acc := range accountMgr.GetRawAccountsByProvider("all") {
			if acc != nil && acc.Enabled && acc.Provider == "workbuddy" {
				if acc.IsWorkBuddyDomestic() {
					if domAcc == nil {
						domAcc = acc
					}
				} else {
					if intlAcc == nil {
						intlAcc = acc
					}
				}
			}
		}
	}

	if domAcc == nil && intlAcc == nil {
		return nil, fmt.Errorf("号池 [workbuddy] 下暂无已启用的有效账号，请先在【账号池】中添加该号池账号")
	}

	res := &WorkBuddyFetchDetails{
		HasDomestic: domAcc != nil,
		HasIntl:     intlAcc != nil,
		DomModels:   []string{},
		IntlModels:  []string{},
		AllModels:   []string{},
	}

	var domErr, intlErr error
	if domAcc != nil {
		res.DomModels, domErr = fetchWorkBuddyModels(domAcc)
	}
	if intlAcc != nil {
		res.IntlModels, intlErr = fetchWorkBuddyModels(intlAcc)
	}

	if domAcc != nil && intlAcc != nil && domErr != nil && intlErr != nil {
		return nil, fmt.Errorf("打 WorkBuddy 双端上游获取模型均失败: 国内版错误(%v), 国际版错误(%v)", domErr, intlErr)
	} else if domAcc != nil && intlAcc == nil && domErr != nil {
		return nil, fmt.Errorf("打 WorkBuddy 国内版上游获取模型失败 (账号 %s): %w", domAcc.Email, domErr)
	} else if intlAcc != nil && domAcc == nil && intlErr != nil {
		return nil, fmt.Errorf("打 WorkBuddy 国际版上游获取模型失败 (账号 %s): %w", intlAcc.Email, intlErr)
	}

	seen := make(map[string]bool)
	var merged []string
	for _, m := range res.DomModels {
		if !seen[m] {
			seen[m] = true
			merged = append(merged, m)
		}
	}
	for _, m := range res.IntlModels {
		if !seen[m] {
			seen[m] = true
			merged = append(merged, m)
		}
	}

	if len(merged) == 0 {
		merged = account.WorkBuddySupportedModels
	}
	res.AllModels = merged
	return res, nil
}



