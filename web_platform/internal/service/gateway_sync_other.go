package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type localOtherAccount struct {
	ID           string   `json:"id"`
	Email        string   `json:"email"`
	AccessToken  string   `json:"access_token"`
	Provider     string   `json:"provider"`
	BaseURL      string   `json:"baseUrl"`
	DefaultModel string   `json:"defaultModel"`
	GroupID      string   `json:"groupId"`
	GroupName    string   `json:"groupName"`
	Formats      []string `json:"formats"`
	Enabled      bool     `json:"enabled"`
}

type localOtherAccountsFile struct {
	Accounts []*localOtherAccount `json:"accounts"`
}

// findLocalAccountsFile 探测本地指定账号文件（如 accounts_other.json / accounts_nvidia.json）存储路径
func findLocalAccountsFile(filename string) string {
	candidates := make([]string, 0, 8)
	if appData := os.Getenv("APPDATA"); appData != "" {
		candidates = append(candidates,
			filepath.Join(appData, "antigravity-proxy-desktop", "remote_sandbox", filename),
			filepath.Join(appData, "antigravity-proxy-desktop", filename),
		)
	}
	if home := os.Getenv("USERPROFILE"); home != "" {
		candidates = append(candidates,
			filepath.Join(home, ".antigravity-proxy", filename),
		)
	}
	candidates = append(candidates,
		filename,
		filepath.Join("..", filename),
		filepath.Join("data", filename),
	)

	for _, p := range candidates {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}

// findLocalAccountsOtherFile 探测本地 accounts_other.json 存储路径
func findLocalAccountsOtherFile() string {
	return findLocalAccountsFile("accounts_other.json")
}

// loadLocalOtherAccounts 读取并反序列化本地 Other 账号文件
func loadLocalOtherAccounts() ([]*localOtherAccount, error) {
	filePath := findLocalAccountsOtherFile()
	if filePath == "" {
		return nil, fmt.Errorf("未找到本地 accounts_other.json 配置文件")
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("读取 accounts_other.json 失败: %w", err)
	}

	var parsed localOtherAccountsFile
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("解析 accounts_other.json 失败: %w", err)
	}
	return parsed.Accounts, nil
}

// loadLocalOtherGroups 从本地账号配置汇总各 Other 渠道/分组元信息
func (s *GatewaySyncService) loadLocalOtherGroups() ([]map[string]interface{}, error) {
	accounts, err := loadLocalOtherAccounts()
	if err != nil {
		return nil, err
	}

	type groupAgg struct {
		groupId      string
		groupName    string
		formats      map[string]bool
		accountCount int
		enabledCount int
	}

	aggMap := make(map[string]*groupAgg)
	for _, acc := range accounts {
		if acc == nil {
			continue
		}
		gid := strings.TrimSpace(acc.GroupID)
		if gid == "" {
			continue
		}
		g, exists := aggMap[gid]
		if !exists {
			gName := strings.TrimSpace(acc.GroupName)
			if gName == "" {
				gName = gid
			}
			g = &groupAgg{
				groupId:   gid,
				groupName: gName,
				formats:   make(map[string]bool),
			}
			aggMap[gid] = g
		}
		g.accountCount++
		if acc.Enabled {
			g.enabledCount++
		}
		for _, f := range acc.Formats {
			fTrim := strings.TrimSpace(f)
			if fTrim != "" {
				g.formats[fTrim] = true
			}
		}
	}

	result := make([]map[string]interface{}, 0, len(aggMap))
	for _, g := range aggMap {
		fmtList := make([]string, 0, len(g.formats))
		for f := range g.formats {
			fmtList = append(fmtList, f)
		}
		sort.Strings(fmtList)
		result = append(result, map[string]interface{}{
			"groupId":      g.groupId,
			"groupName":    g.groupName,
			"formats":      fmtList,
			"accountCount": g.accountCount,
			"enabledCount": g.enabledCount,
		})
	}

	// 稳定排序：按组名升序
	sort.Slice(result, func(i, j int) bool {
		return fmt.Sprintf("%v", result[i]["groupId"]) < fmt.Sprintf("%v", result[j]["groupId"])
	})

	return result, nil
}

// fetchUpstreamOtherGroupModels 直接向本地账号配置中的上游 BaseURL 发起获取模型列表请求
func (s *GatewaySyncService) fetchUpstreamOtherGroupModels(groupId string) (map[string]interface{}, error) {
	accounts, err := loadLocalOtherAccounts()
	if err != nil {
		return nil, err
	}

	var targetAcc *localOtherAccount
	// 优先挑选已启用且带有 BaseURL 的账号
	for _, acc := range accounts {
		if acc != nil && acc.GroupID == groupId && acc.Enabled && strings.TrimSpace(acc.BaseURL) != "" {
			targetAcc = acc
			break
		}
	}
	// 兜底找任意未禁用或该组任意具备 BaseURL 的账号
	if targetAcc == nil {
		for _, acc := range accounts {
			if acc != nil && acc.GroupID == groupId && strings.TrimSpace(acc.BaseURL) != "" {
				targetAcc = acc
				break
			}
		}
	}

	if targetAcc == nil {
		return nil, fmt.Errorf("组 [%s] 下未找到有效配置了 BaseURL 的账号", groupId)
	}

	baseURL := strings.TrimSpace(targetAcc.BaseURL)
	apiKey := strings.TrimSpace(targetAcc.AccessToken)
	isAnthropic := false
	for _, f := range targetAcc.Formats {
		if strings.EqualFold(strings.TrimSpace(f), "anthropic") {
			isAnthropic = true
			break
		}
	}

	models, err := requestUpstreamModels(baseURL, apiKey, isAnthropic)
	if err != nil {
		return nil, fmt.Errorf("向上游 [%s] 获取模型失败: %w", baseURL, err)
	}

	return map[string]interface{}{
		"success":  true,
		"models":   models,
		"snapshot": []string{},
		"added":    models,
	}, nil
}

// requestUpstreamModels 请求 OpenAI / Anthropic 兼容端点的模型列表
func requestUpstreamModels(baseURL, apiKey string, isAnthropic bool) ([]string, error) {
	trimmed := strings.TrimRight(baseURL, "/")
	candidates := make([]string, 0, 4)

	if isAnthropic {
		candidates = append(candidates, trimmed+"/v1/models", trimmed+"/models")
	} else {
		if strings.HasSuffix(trimmed, "/v1") {
			candidates = append(candidates, trimmed+"/models", strings.TrimSuffix(trimmed, "/v1")+"/v1/models")
		} else {
			candidates = append(candidates, trimmed+"/models", trimmed+"/v1/models")
		}
	}

	client := &http.Client{Timeout: 15 * time.Second}
	var lastErr error

	for _, endpoint := range candidates {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, endpoint, nil)
		if err != nil {
			lastErr = err
			continue
		}

		if apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
			if isAnthropic {
				req.Header.Set("x-api-key", apiKey)
				req.Header.Set("anthropic-version", "2023-06-01")
			}
		}
		req.Header.Set("Accept", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		bodyBytes, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}

		if resp.StatusCode == http.StatusOK {
			models, parseErr := parseUpstreamModelsJSON(bodyBytes)
			if parseErr == nil && len(models) > 0 {
				sort.Strings(models)
				return models, nil
			}
			lastErr = parseErr
			continue
		}

		lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("所有上游候选端点均未返回有效模型列表")
}

// parseUpstreamModelsJSON 解析多种标准格式的 models 响应
func parseUpstreamModelsJSON(data []byte) ([]string, error) {
	// 兼容标准 OpenAI 格式: {"data": [{"id": "model_name"}, ...]}
	var standardResp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &standardResp); err == nil && len(standardResp.Data) > 0 {
		models := make([]string, 0, len(standardResp.Data))
		for _, m := range standardResp.Data {
			mID := strings.TrimSpace(m.ID)
			if mID != "" {
				models = append(models, mID)
			}
		}
		if len(models) > 0 {
			return models, nil
		}
	}

	// 兼容直接数组形式: [{"id": "model_name"}, ...]
	var listResp []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &listResp); err == nil && len(listResp) > 0 {
		models := make([]string, 0, len(listResp))
		for _, m := range listResp {
			mID := strings.TrimSpace(m.ID)
			if mID != "" {
				models = append(models, mID)
			}
		}
		if len(models) > 0 {
			return models, nil
		}
	}

	// 兼容字符串数组: ["model-1", "model-2"]
	var strResp []string
	if err := json.Unmarshal(data, &strResp); err == nil && len(strResp) > 0 {
		models := make([]string, 0, len(strResp))
		for _, s := range strResp {
			sTrim := strings.TrimSpace(s)
			if sTrim != "" {
				models = append(models, sTrim)
			}
		}
		if len(models) > 0 {
			return models, nil
		}
	}

	return nil, fmt.Errorf("未能从上游响应提取有效模型列表")
}

// fetchUpstreamChannelModels 通用号池（如 nvidia、grok 等）在网关未提供端点时的本地账号与上游拉取降级
func (s *GatewaySyncService) fetchUpstreamChannelModels(channel string) (map[string]interface{}, error) {
	ch := strings.ToLower(strings.TrimSpace(channel))
	if ch == "" {
		return nil, fmt.Errorf("渠道参数为空")
	}

	// 针对各渠道确定对应的账号分区文件
	filename := fmt.Sprintf("accounts_%s.json", ch)
	filePath := findLocalAccountsFile(filename)
	if filePath == "" {
		return nil, fmt.Errorf("号池 [%s] 暂无已保存账号文件 (未找到 %s)", channel, filename)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("读取号池 [%s] 账号配置失败: %w", channel, err)
	}

	var parsed struct {
		Accounts []struct {
			ID          string `json:"id"`
			Email       string `json:"email"`
			AccessToken string `json:"access_token"`
			BaseURL     string `json:"baseUrl"`
			Enabled     bool   `json:"enabled"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("解析号池 [%s] 账号配置失败: %w", channel, err)
	}

	var targetToken, targetBaseURL string
	for _, acc := range parsed.Accounts {
		if acc.Enabled && strings.TrimSpace(acc.AccessToken) != "" {
			targetToken = strings.TrimSpace(acc.AccessToken)
			targetBaseURL = strings.TrimSpace(acc.BaseURL)
			break
		}
	}
	if targetToken == "" {
		for _, acc := range parsed.Accounts {
			if strings.TrimSpace(acc.AccessToken) != "" {
				targetToken = strings.TrimSpace(acc.AccessToken)
				targetBaseURL = strings.TrimSpace(acc.BaseURL)
				break
			}
		}
	}

	if targetToken == "" {
		return nil, fmt.Errorf("号池 [%s] 下暂无具备有效 AccessToken 的账号，请先在【账号池】中添加账号", channel)
	}

	if ch == "workbuddy" {
		models := []string{
			"deepseek-v4.1-flash",
			"deepseek-v4.1-coder",
			"deepseek-v3",
			"deepseek-r1",
			"claude-3-7-sonnet",
			"claude-3-5-sonnet",
			"claude-3-5-haiku",
			"gpt-4o",
			"gpt-4o-mini",
			"o3-mini",
			"gemini-2.5-flash",
			"gemini-2.5-pro",
			"qwen2.5-coder-32b",
			"glm-4-plus",
			"kimi-k1.5",
			"minimax-01",
		}
		return map[string]interface{}{
			"success":  true,
			"models":   models,
			"snapshot": []string{},
			"added":    models,
		}, nil
	}

	if targetBaseURL == "" {
		if ch == "nvidia" {
			targetBaseURL = "https://integrate.api.nvidia.com/v1"
		} else if ch == "grok" {
			targetBaseURL = "https://api.x.ai/v1"
		} else if ch == "workbuddy" {
			targetBaseURL = "https://www.codebuddy.ai"
		} else {
			return nil, fmt.Errorf("号池 [%s] 账号未配置 BaseURL，无法请求上游模型列表", channel)
		}
	}

	models, err := requestUpstreamModels(targetBaseURL, targetToken, false)
	if err != nil {
		return nil, fmt.Errorf("向上游 [%s] 获取模型失败: %w", targetBaseURL, err)
	}

	return map[string]interface{}{
		"success":  true,
		"models":   models,
		"snapshot": []string{},
		"added":    models,
	}, nil
}

