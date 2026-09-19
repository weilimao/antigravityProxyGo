package modelfetch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/netutil"
)

// FetchChannelAvailableModels provides headless support for fetching channel models
func FetchChannelAvailableModels(accountMgr *account.Manager, channel string) ([]string, error) {
	ch := strings.ToLower(strings.TrimSpace(channel))
	if ch == "" {
		ch = "google"
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
		models, err := fetchWorkBuddyModels(activeAcc)
		if err != nil {
			return nil, fmt.Errorf("打 WorkBuddy 上游获取模型失败 (账号 %s): %w", activeAcc.Email, err)
		}
		if len(models) == 0 {
			return nil, fmt.Errorf("WorkBuddy 上游返回的模型列表为空")
		}
		return models, nil
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

	// 4. 对于 Google / Antigravity / GCP 号池，真正发起 v1internal:fetchAvailableModels 请求
	models, err := fetchGeminiInternalModels(activeAcc)
	if err != nil {
		return nil, fmt.Errorf("打 Google 上游 v1internal:fetchAvailableModels 失败 (账号 %s): %w", activeAcc.Email, err)
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("Google 上游返回的模型列表为空")
	}
	return models, nil
}

func isGoogleChannel(c string) bool {
	return c == "antigravity" || c == "project" || c == "google" || c == "gcp" || c == "gemini-cli"
}

func fetchGeminiInternalModels(acc *account.Account) ([]string, error) {
	token := acc.GetAccessToken()
	if token == "" {
		return nil, fmt.Errorf("账号 AccessToken 为空")
	}

	projectID := acc.ProjectID
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
	req.Header.Set("User-Agent", "antigravity/ide/2.1.1 windows/amd64")

	client := &http.Client{Timeout: 30 * time.Second, Transport: netutil.NewTransport()}
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
		Models map[string]interface{} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("解析上游响应 JSON 失败: %w", err)
	}

	var list []string
	for k := range parsed.Models {
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
	if baseURL == "" {
		baseURL = account.DefaultWorkBuddyBaseURL
	}

	configURL := strings.TrimRight(baseURL, "/") + "/v3/config"
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, configURL, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	req.Header.Set("User-Agent", "WorkBuddy/5.5.2")
	if token := acc.GetAccessToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
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


