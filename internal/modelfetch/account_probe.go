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

// DefaultCoreGatedModels 定义重点监控与展示的高权限/灰度模型集合
var DefaultCoreGatedModels = []string{
	"gemini-4-argon-high",
	"gemini-4-argon-medium",
	"gemini-4-argon-low",
	"claude-opus-5-5-high",
	"claude-opus-5-5-medium",
	"claude-opus-5-5-low",
	"claude-sonnet-5-5-high",
}

// AccountModelCapabilityInfo 是单账号模型能力检测结果
type AccountModelCapabilityInfo struct {
	ID              string   `json:"id"`
	Email           string   `json:"email"`
	Tier            string   `json:"tier"`
	Provider        string   `json:"provider"`
	Enabled         bool     `json:"enabled"`
	HasArgonBucket  *bool    `json:"hasArgonBucket"`
	SupportedCount  int      `json:"supportedCount"`
	SupportedModels []string `json:"supportedModels"`
	MissingModels   []string `json:"missingModels"`
	Error           string   `json:"error,omitempty"`
}

// ProbeSummary 是全池模型探测统计摘要
type ProbeSummary struct {
	TotalAccounts        int      `json:"totalAccounts"`
	ProbedAccounts       int      `json:"probedAccounts"`
	ArgonSupportedCount  int      `json:"argonSupportedCount"`
	Opus55SupportedCount int      `json:"opus55SupportedCount"`
	CoreGatedModels      []string `json:"coreGatedModels"`
	AllPoolModels        []string `json:"allPoolModels"`
}

// AccountProbeResponse 是对外返回的完整响应
type AccountProbeResponse struct {
	Success bool                         `json:"success"`
	Error   string                       `json:"error,omitempty"`
	Summary ProbeSummary                 `json:"summary"`
	Results []AccountModelCapabilityInfo `json:"results"`
}

// ProbeAccountsModels 批量或针对指定账号探测 Google/Antigravity 号池真实可用模型列表，
// 更新 Account.SupportedModels 并计算各账号缺失的核心受限模型。
func ProbeAccountsModels(
	accountMgr *account.Manager,
	getStoredProject func(string) string,
	refreshToken func(*account.Account) (string, error),
	channel string,
	targetAccountIDs []string,
) *AccountProbeResponse {
	if accountMgr == nil {
		return &AccountProbeResponse{Success: false, Error: "AccountManager is nil"}
	}

	ch := strings.ToLower(strings.TrimSpace(channel))
	if ch == "" {
		ch = "antigravity"
	}

	var candidates []*account.Account
	rawList := accountMgr.GetRawAccountsByProvider("all")
	targetIDSet := make(map[string]bool)
	for _, id := range targetAccountIDs {
		if strings.TrimSpace(id) != "" {
			targetIDSet[strings.TrimSpace(id)] = true
		}
	}

	for _, acc := range rawList {
		if acc == nil || !acc.Enabled {
			continue
		}
		if len(targetIDSet) > 0 && !targetIDSet[acc.ID] {
			continue
		}
		prov := strings.ToLower(strings.TrimSpace(acc.Provider))
		if isGoogleChannel(ch) {
			if prov == "" || isGoogleChannel(prov) {
				candidates = append(candidates, acc)
			}
		} else if prov == ch {
			candidates = append(candidates, acc)
		}
	}

	if len(candidates) == 0 {
		return &AccountProbeResponse{
			Success: false,
			Error:   fmt.Sprintf("渠道 [%s] 下未找到启用的待检测账号", ch),
		}
	}

	ua := accountMgr.GetAntigravityUserAgent()
	if strings.TrimSpace(ua) == "" {
		ua = account.FormatAntigravityUserAgent(account.DefaultAntigravityCliVersion)
	}

	results := make([]AccountModelCapabilityInfo, len(candidates))
	allPoolModelsSet := make(map[string]bool)
	var poolMu sync.Mutex

	// 逐个账号探测（使用随机抖动控制并发，消除等间隔扫描脉冲指纹，各账号走专属连接池）
	for i, acc := range candidates {
		if i > 0 && geminiInternalTransport == nil {
			time.Sleep(time.Duration(140+rand.Intn(260)) * time.Millisecond)
		}

		models, err := probeSingleAccountWithRetry(acc, ua, getStoredProject, refreshToken)
		info := AccountModelCapabilityInfo{
			ID:             acc.ID,
			Email:          acc.Email,
			Tier:           acc.Tier,
			Provider:       acc.Provider,
			Enabled:        acc.Enabled,
			HasArgonBucket: acc.HasArgonBucket,
		}

		if err != nil {
			info.Error = err.Error()
			// 若探测遇到临时错误但此前已持久化过 SupportedModels，使用历史缓存兜底
			if len(acc.SupportedModels) > 0 {
				info.SupportedModels = acc.SupportedModels
				info.SupportedCount = len(acc.SupportedModels)
			}
		} else {
			info.SupportedModels = models
			info.SupportedCount = len(models)
			accountMgr.SetAccountSupportedModels(acc.ID, models)
		}

		poolMu.Lock()
		for _, m := range info.SupportedModels {
			allPoolModelsSet[m] = true
		}
		poolMu.Unlock()

		results[i] = info
	}

	// 确定本号池所发现的全部模型全集与受限模型全集
	var allPoolModels []string
	for m := range allPoolModelsSet {
		allPoolModels = append(allPoolModels, m)
	}
	sort.Strings(allPoolModels)

	// 计算各账号缺失的核心模型 (MissingModels)
	argonSupportedTotal := 0
	opus55SupportedTotal := 0

	for i := range results {
		accInfo := &results[i]
		supSet := make(map[string]bool, len(accInfo.SupportedModels))
		for _, m := range accInfo.SupportedModels {
			supSet[m] = true
		}

		hasArgon := false
		hasOpus55 := false
		var missing []string

		for _, core := range DefaultCoreGatedModels {
			// 若账号配额桶明确无 Argon，则 Argon 系列模型必属缺失
			if strings.Contains(core, "argon") && accInfo.HasArgonBucket != nil && !*accInfo.HasArgonBucket {
				missing = append(missing, core)
				continue
			}

			// 检查是否支持
			if supSet[core] {
				if strings.Contains(core, "argon") {
					hasArgon = true
				}
				if strings.Contains(core, "opus-5-5") {
					hasOpus55 = true
				}
				continue
			}

			// 若号池中有任何账号具备该模型，而当前账号缺失，明确列入 MissingModels
			if allPoolModelsSet[core] || isCoreModelExpected(core) {
				missing = append(missing, core)
			}
		}

		if hasArgon {
			argonSupportedTotal++
		}
		if hasOpus55 {
			opus55SupportedTotal++
		}
		sort.Strings(missing)
		accInfo.MissingModels = missing
	}

	return &AccountProbeResponse{
		Success: true,
		Summary: ProbeSummary{
			TotalAccounts:        len(candidates),
			ProbedAccounts:       len(candidates),
			ArgonSupportedCount:  argonSupportedTotal,
			Opus55SupportedCount: opus55SupportedTotal,
			CoreGatedModels:      DefaultCoreGatedModels,
			AllPoolModels:        allPoolModels,
		},
		Results: results,
	}
}

func isCoreModelExpected(m string) bool {
	norm := strings.ToLower(strings.TrimSpace(m))
	return strings.Contains(norm, "argon") || strings.Contains(norm, "opus-5-5") || strings.Contains(norm, "sonnet-5-5")
}

func probeSingleAccountWithRetry(
	acc *account.Account,
	ua string,
	getStoredProject func(string) string,
	refreshToken func(*account.Account) (string, error),
) ([]string, error) {
	if acc == nil {
		return nil, fmt.Errorf("账号为 nil")
	}

	// 优先复用未过期 Token，若为空则刷新
	token := acc.GetAccessToken()
	if token == "" && refreshToken != nil {
		if newTok, err := refreshToken(acc); err == nil && newTok != "" {
			token = newTok
			acc.SetAccessToken(newTok)
		}
	}
	if token == "" {
		return nil, fmt.Errorf("账号 AccessToken 为空")
	}

	projectID := acc.ProjectID
	if projectID == "" && getStoredProject != nil {
		projectID = getStoredProject(acc.Email)
	}
	if projectID == "" {
		projectID = "favorable-synapse-ttvcb"
	}

	reqBody, _ := json.Marshal(map[string]string{
		"project": projectID,
	})

	doReq := func(tok string) (int, []byte, error) {
		req, err := http.NewRequestWithContext(context.Background(), "POST", "https://daily-cloudcode-pa.googleapis.com/v1internal:fetchAvailableModels", bytes.NewBuffer(reqBody))
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/json")
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
			return 0, nil, err
		}
		defer resp.Body.Close()
		b, rErr := io.ReadAll(resp.Body)
		return resp.StatusCode, b, rErr
	}

	status, body, err := doReq(token)
	if err != nil {
		return nil, err
	}

	// 遇 401 自动刷新 Token 重试一次
	if status == http.StatusUnauthorized && refreshToken != nil {
		if newTok, rErr := refreshToken(acc); rErr == nil && newTok != "" {
			token = newTok
			acc.SetAccessToken(newTok)
			status, body, err = doReq(token)
			if err != nil {
				return nil, err
			}
		}
	}

	if status != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", status, string(body))
	}

	var parsed struct {
		Models map[string]struct {
			DisplayName string `json:"displayName"`
			Disabled    bool   `json:"disabled"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
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
