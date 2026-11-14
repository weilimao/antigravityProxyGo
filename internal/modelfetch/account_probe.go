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

// DefaultCoreGatedModels 保留作为向后兼容字段及无差异模型时的基准参考集合。
// 实际探测缺失列表（MissingModels）与差异化模型统计（DifferentiatedModels）已改为全池动态并集/差集计算。
var DefaultCoreGatedModels = []string{
	"gemini-4-argon-high",
	"gemini-4-argon-medium",
	"gemini-4-argon-low",
	"claude-opus-5-5-high",
	"claude-opus-5-5-medium",
	"claude-opus-5-5-low",
	"claude-sonnet-5-5-high",
}

// ModelCoverageItem 描述号池中单个模型的账号覆盖分布情况
type ModelCoverageItem struct {
	Model            string `json:"model"`
	SupportedCount   int    `json:"supportedCount"`
	TotalProbed      int    `json:"totalProbed"`
	IsDifferentiated bool   `json:"isDifferentiated"`
}

// ModelFamilyCoverage 描述按模型家族（动态剥离档位后缀）聚合后的号池覆盖情况
type ModelFamilyCoverage struct {
	Family           string   `json:"family"`
	Models           []string `json:"models"`
	SupportedCount   int      `json:"supportedCount"`
	TotalProbed      int      `json:"totalProbed"`
	IsDifferentiated bool     `json:"isDifferentiated"`
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
	TotalAccounts        int                   `json:"totalAccounts"`
	ProbedAccounts       int                   `json:"probedAccounts"`
	ValidProbedCount     int                   `json:"validProbedCount"`
	ArgonSupportedCount  int                   `json:"argonSupportedCount"`
	Opus55SupportedCount int                   `json:"opus55SupportedCount"`
	CoreGatedModels      []string              `json:"coreGatedModels"`
	AllPoolModels        []string              `json:"allPoolModels"`
	CommonModels         []string              `json:"commonModels"`
	DifferentiatedModels []string              `json:"differentiatedModels"`
	ModelCoverage        []ModelCoverageItem   `json:"modelCoverage"`
	FamilyCoverage       []ModelFamilyCoverage `json:"familyCoverage"`
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
			norm := normalizeProbeModelID(m)
			if norm != "" {
				allPoolModelsSet[norm] = true
			}
		}
		poolMu.Unlock()

		results[i] = info
	}

	// 确定本号池所发现的全部模型全集
	allPoolModels := make([]string, 0, len(allPoolModelsSet))
	for m := range allPoolModelsSet {
		allPoolModels = append(allPoolModels, m)
	}
	sort.Strings(allPoolModels)

	// 动态统计各模型与各模型家族在号池中的覆盖情况，并识别差异受限模型
	validProbedCount, commonModels, diffModels, modelCov, familyCov := buildPoolModelCoverage(results, allPoolModels)

	// 动态计算各账号缺失的全池模型 (MissingModels = allPoolModels \ acc.SupportedModels)
	argonSupportedTotal := 0
	opus55SupportedTotal := 0

	for i := range results {
		accInfo := &results[i]
		supSet := buildSupportedModelSet(accInfo)

		hasArgon := false
		hasOpus55 := false
		for m := range supSet {
			if strings.Contains(m, "argon") {
				hasArgon = true
			}
			if strings.Contains(m, "opus-5-5") {
				hasOpus55 = true
			}
		}
		if hasArgon {
			argonSupportedTotal++
		}
		if hasOpus55 {
			opus55SupportedTotal++
		}

		// 若本次探测报错且无历史缓存，不编造缺失列表
		if accInfo.Error != "" && len(accInfo.SupportedModels) == 0 {
			continue
		}

		var missing []string
		for _, poolModel := range allPoolModels {
			if !supSet[poolModel] {
				missing = append(missing, poolModel)
			}
		}
		// 若全池未探测到 argon 模型但账号明确无 Argon 桶，也兼容标注默认 argon 基线
		if accInfo.HasArgonBucket != nil && !*accInfo.HasArgonBucket {
			missingSet := make(map[string]bool, len(missing))
			for _, m := range missing {
				missingSet[m] = true
			}
			for _, core := range DefaultCoreGatedModels {
				if strings.Contains(core, "argon") && !missingSet[core] {
					missing = append(missing, core)
				}
			}
		}
		sort.Strings(missing)
		accInfo.MissingModels = missing
	}

	return &AccountProbeResponse{
		Success: true,
		Summary: ProbeSummary{
			TotalAccounts:        len(candidates),
			ProbedAccounts:       len(candidates),
			ValidProbedCount:     validProbedCount,
			ArgonSupportedCount:  argonSupportedTotal,
			Opus55SupportedCount: opus55SupportedTotal,
			CoreGatedModels:      DefaultCoreGatedModels,
			AllPoolModels:        allPoolModels,
			CommonModels:         commonModels,
			DifferentiatedModels: diffModels,
			ModelCoverage:        modelCov,
			FamilyCoverage:       familyCov,
		},
		Results: results,
	}
}

func normalizeProbeModelID(raw string) string {
	m := strings.ToLower(strings.TrimSpace(raw))
	return strings.TrimPrefix(m, "models/")
}

func buildSupportedModelSet(accInfo *AccountModelCapabilityInfo) map[string]bool {
	supSet := make(map[string]bool, len(accInfo.SupportedModels))
	for _, m := range accInfo.SupportedModels {
		norm := normalizeProbeModelID(m)
		if norm == "" {
			continue
		}
		if strings.Contains(norm, "argon") && accInfo.HasArgonBucket != nil && !*accInfo.HasArgonBucket {
			continue
		}
		supSet[norm] = true
	}
	return supSet
}

func extractModelFamily(modelID string) string {
	norm := normalizeProbeModelID(modelID)
	for _, suffix := range []string{"-xhigh", "-high", "-medium", "-low", "-tiered"} {
		if strings.HasSuffix(norm, suffix) && len(norm) > len(suffix) {
			return strings.TrimSuffix(norm, suffix)
		}
	}
	return norm
}

func buildPoolModelCoverage(results []AccountModelCapabilityInfo, allPoolModels []string) (
	validProbed int,
	commonModels []string,
	diffModels []string,
	modelCov []ModelCoverageItem,
	familyCov []ModelFamilyCoverage,
) {
	validSets := make([]map[string]bool, 0, len(results))
	for i := range results {
		acc := &results[i]
		if acc.Error != "" && len(acc.SupportedModels) == 0 {
			continue
		}
		validSets = append(validSets, buildSupportedModelSet(acc))
	}
	validProbed = len(validSets)

	familyOrder := make([]string, 0)
	familyModelsMap := make(map[string][]string)

	for _, m := range allPoolModels {
		cnt := 0
		for _, s := range validSets {
			if s[m] {
				cnt++
			}
		}
		isDiff := validProbed > 1 && cnt > 0 && cnt < validProbed
		if isDiff {
			diffModels = append(diffModels, m)
		} else if validProbed > 0 && cnt == validProbed {
			commonModels = append(commonModels, m)
		}
		modelCov = append(modelCov, ModelCoverageItem{
			Model:            m,
			SupportedCount:   cnt,
			TotalProbed:      validProbed,
			IsDifferentiated: isDiff,
		})

		fam := extractModelFamily(m)
		if _, exists := familyModelsMap[fam]; !exists {
			familyOrder = append(familyOrder, fam)
		}
		familyModelsMap[fam] = append(familyModelsMap[fam], m)
	}

	sort.SliceStable(modelCov, func(i, j int) bool {
		if modelCov[i].IsDifferentiated != modelCov[j].IsDifferentiated {
			return modelCov[i].IsDifferentiated
		}
		return modelCov[i].Model < modelCov[j].Model
	})

	for _, fam := range familyOrder {
		fModels := familyModelsMap[fam]
		cnt := 0
		for _, s := range validSets {
			supportedAny := false
			for _, m := range fModels {
				if s[m] {
					supportedAny = true
					break
				}
			}
			if supportedAny {
				cnt++
			}
		}
		isDiff := validProbed > 1 && cnt > 0 && cnt < validProbed
		familyCov = append(familyCov, ModelFamilyCoverage{
			Family:           fam,
			Models:           fModels,
			SupportedCount:   cnt,
			TotalProbed:      validProbed,
			IsDifferentiated: isDiff,
		})
	}

	sort.SliceStable(familyCov, func(i, j int) bool {
		if familyCov[i].IsDifferentiated != familyCov[j].IsDifferentiated {
			return familyCov[i].IsDifferentiated
		}
		return familyCov[i].Family < familyCov[j].Family
	})

	return validProbed, commonModels, diffModels, modelCov, familyCov
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

	seen := make(map[string]bool, len(parsed.Models))
	list := make([]string, 0, len(parsed.Models))
	for k, v := range parsed.Models {
		if v.Disabled || strings.HasSuffix(strings.ToLower(k), "-disabled") {
			continue
		}
		norm := normalizeProbeModelID(k)
		if norm == "" || seen[norm] {
			continue
		}
		seen[norm] = true
		list = append(list, norm)
	}
	sort.Strings(list)
	return list, nil
}
