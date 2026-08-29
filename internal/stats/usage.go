package stats

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"antigravity-proxy/internal/fileutil"
	"antigravity-proxy/internal/pricing"
)

type TokenStats struct {
	RequestCount     int     `json:"requestCount"`
	InputTokens      int     `json:"inputTokens"`
	OutputTokens     int     `json:"outputTokens"`
	CachedTokens     int     `json:"cachedTokens"`
	CacheHitRequests int     `json:"cacheHitRequests"`
	InputCost        float64 `json:"inputCost"`
	OutputCost       float64 `json:"outputCost"`
	CachedCost       float64 `json:"cachedCost"`
	TotalCost        float64 `json:"totalCost"`
}

type ModelUsage struct {
	Model string `json:"model"`
	TokenStats
	LastUsedAt string `json:"lastUsedAt"`
}

type AccountUsage struct {
	AccountID string `json:"accountId"`
	Email     string `json:"email"`
	Provider  string `json:"provider"`
	ProjectID string `json:"projectId"`
	ScopeType string `json:"scopeType"`
	TokenStats
	LastUsedAt string                 `json:"lastUsedAt"`
	Models     map[string]*ModelUsage `json:"models"`
}

type UsageState struct {
	UpdatedAt string                  `json:"updatedAt"`
	Totals    TokenStats              `json:"totals"`
	Accounts  map[string]*AccountUsage `json:"accounts"`
}

type UsageData struct {
	Usage UsageState `json:"usage"`
}

type UsageTracker struct {
	sync.RWMutex
	persistPath     string
	state           UsageState
	saveTimeout     *time.Timer
	saveTimeoutLock sync.Mutex
	pricingMgr      *pricing.Manager
	onPayloadUpdate  func()
}

func NewUsageTracker(pricingMgr *pricing.Manager) *UsageTracker {
	return &UsageTracker{
		state: UsageState{
			Accounts: make(map[string]*AccountUsage),
		},
		pricingMgr: pricingMgr,
	}
}

func (u *UsageTracker) Init(userDataPath string) {
	u.Lock()
	u.persistPath = filepath.Join(userDataPath, "usage.json")
	u.Unlock()

	u.LoadFromDisk()
}

func (u *UsageTracker) UpdatePath(newPath string) {
	u.Lock()
	if u.saveTimeout != nil {
		u.saveTimeout.Stop()
		u.saveTimeout = nil
	}
	u.Unlock()

	u.SaveToDisk()

	u.Lock()
	u.persistPath = filepath.Join(newPath, "usage.json")
	u.Unlock()

	u.LoadFromDisk()
}

func (u *UsageTracker) SetOnPayloadUpdate(fn func()) {
	u.Lock()
	defer u.Unlock()
	u.onPayloadUpdate = fn
}

type UsageSample struct {
	ModelName    string
	InTokens     int
	OutTokens    int
	CachedTokens int
	Timestamp    string
	Account      *AccountMeta
}

type AccountMeta struct {
	ID        string
	Email     string
	Provider  string
	ProjectID string
	ScopeType string
}

func getAccountKey(acc *AccountMeta) string {
	if acc == nil {
		return "direct"
	}
	id := strings.TrimSpace(acc.ID)
	if id != "" {
		return id
	}
	email := strings.TrimSpace(acc.Email)
	if email != "" {
		provider := "unknown"
		if acc.Provider != "" {
			provider = acc.Provider
		}
		return email + ":" + provider
	}
	return "direct"
}

func getAccountLabel(acc *AccountMeta) string {
	if acc == nil {
		return "Direct"
	}
	email := strings.TrimSpace(acc.Email)
	if email != "" {
		return email
	}
	if acc.ID != "" {
		return acc.ID
	}
	return "Direct"
}

func (u *UsageTracker) RecordUsage(sample UsageSample) {
	modelName := strings.TrimSpace(sample.ModelName)
	if modelName == "" || modelName == "unknown" {
		return
	}

	u.Lock()
	// defer 包裹保 panic 安全:同 stats.Tracker 同构,critical section 任意 panic 均保证
	// 释放锁再 notify(notify 拿 RLock 依赖 Unlock 先行,顺序敏感)。
	defer func() {
		u.Unlock()
		u.notifyPayloadUpdate()
	}()

	inTokens := sample.InTokens
	outTokens := sample.OutTokens
	cachedTokens := sample.CachedTokens
	if inTokens < 0 {
		inTokens = 0
	}
	if outTokens < 0 {
		outTokens = 0
	}
	if cachedTokens < 0 {
		cachedTokens = 0
	}

	accKey := getAccountKey(sample.Account)
	accLabel := getAccountLabel(sample.Account)

	accBucket, exists := u.state.Accounts[accKey]
	if !exists {
		provider := "direct"
		var projectId, scopeType string
		if sample.Account != nil {
			if sample.Account.Provider != "" {
				provider = sample.Account.Provider
			}
			projectId = sample.Account.ProjectID
			scopeType = sample.Account.ScopeType
		}
		accBucket = &AccountUsage{
			AccountID: accKey,
			Email:     accLabel,
			Provider:  provider,
			ProjectID: projectId,
			ScopeType: scopeType,
			Models:    make(map[string]*ModelUsage),
		}
		u.state.Accounts[accKey] = accBucket
	} else if sample.Account != nil {
		accBucket.Email = accLabel
		if sample.Account.Provider != "" {
			accBucket.Provider = sample.Account.Provider
		}
		if sample.Account.ProjectID != "" {
			accBucket.ProjectID = sample.Account.ProjectID
		}
		if sample.Account.ScopeType != "" {
			accBucket.ScopeType = sample.Account.ScopeType
		}
	}

	modelKey := modelName
	modelBucket, exists := accBucket.Models[modelKey]
	if !exists {
		modelBucket = &ModelUsage{
			Model: modelKey,
		}
		accBucket.Models[modelKey] = modelBucket
	}

	breakdown := u.pricingMgr.CalculateCostBreakdown(modelName, inTokens, outTokens, cachedTokens)
	cacheHit := 0
	if cachedTokens > 0 {
		cacheHit = 1
	}

	timestamp := sample.Timestamp
	if timestamp == "" {
		timestamp = time.Now().Format(time.RFC3339)
	}

	// Helper function to update stats block
	updateStats := func(s *TokenStats, bd pricing.CostBreakdown) {
		s.RequestCount += 1
		s.InputTokens += inTokens
		s.OutputTokens += outTokens
		s.CachedTokens += cachedTokens
		s.CacheHitRequests += cacheHit
		s.InputCost = math.Round((s.InputCost+bd.InputCost)*1000000.0) / 1000000.0
		s.OutputCost = math.Round((s.OutputCost+bd.OutputCost)*1000000.0) / 1000000.0
		s.CachedCost = math.Round((s.CachedCost+bd.CachedCost)*1000000.0) / 1000000.0
		s.TotalCost = math.Round((s.TotalCost+bd.TotalCost)*1000000.0) / 1000000.0
	}

	updateStats(&accBucket.TokenStats, breakdown)
	accBucket.LastUsedAt = timestamp

	updateStats(&modelBucket.TokenStats, breakdown)
	modelBucket.LastUsedAt = timestamp

	updateStats(&u.state.Totals, breakdown)
	u.state.UpdatedAt = timestamp

	u.scheduleSave()
}

// notifyPayloadUpdate 请求级 UI 通知:与 scheduleSave(磁盘落盘节拍)解耦。
// 与 stats.Tracker.notifyPayloadUpdate 同构,Unlock 后调用,死锁风险为零。
func (u *UsageTracker) notifyPayloadUpdate() {
	u.RLock()
	cb := u.onPayloadUpdate
	u.RUnlock()
	if cb != nil {
		cb()
	}
}

// RenameAccountByID 就地更新某账号在用量聚合里缓存的展示名(Email),
// 供账号池「编辑改名」即时同步到「使用详情」页,无需等该账号再次产生流量。
//
// 背景:账号池改名只改 accounts.json;usage.json 的 Accounts 桶按账号 ID 归集,
// 每个桶持有一份独立的 Email 展示名副本,常态由 RecordUsage 在每次请求时
// 覆写(accBucket.Email = accLabel)。故改名后若该账号不再出请求,
// 使用详情页会一直停留在旧名(如「账号1」)。
//
// 契约:
//   - 仅改展示名副本,绝不触碰任何 Token/成本数值,历史用量数字零漂移;
//   - accountID 指向的桶不存在(该账号从未产生用量)时静默 no-op;
//   - accountID 或 newLabel 为空时不做变更(调用方须传入 host 回退后的权威最终名)。
func (u *UsageTracker) RenameAccountByID(accountID, newLabel string) {
	accountID = strings.TrimSpace(accountID)
	newLabel = strings.TrimSpace(newLabel)
	if accountID == "" || newLabel == "" {
		return
	}

	changed := false
	u.Lock()
	for key, bucket := range u.state.Accounts {
		if key == accountID {
			if bucket != nil && bucket.Email != newLabel {
				bucket.Email = newLabel
				changed = true
			}
			break
		}
	}
	u.Unlock()

	if changed {
		u.scheduleSave()
	}
}

// GetNvidiaModelAggregates 聚合全部 provider=="nvidia" 账号的 per-model 累计,
// 返回去重无损的合计列表(跨账号同模型合并 sum)。供 NVIDIA 历史差量自愈合并
// (stats_migrate_nvidia.go)在 LoadFromDisk 编排时作为权威全量数据源读用;
// usage.json 自 recordNvidiaUsage 上线即连续记录, 是 NVIDIA 历史全量可信来源。
// 非 nvidia 账号、无 Models 的账号一律排除。
func (u *UsageTracker) GetNvidiaModelAggregates() []ModelAggregate {
	u.RLock()
	defer u.RUnlock()

	agg := make(map[string]*ModelAggregate)
	for _, acc := range u.state.Accounts {
		if acc == nil || !strings.EqualFold(strings.TrimSpace(acc.Provider), "nvidia") {
			continue
		}
		for mk, mu := range acc.Models {
			if mu == nil || (mu.RequestCount <= 0 && mu.InputTokens <= 0) {
				continue
			}
			key := strings.TrimSpace(mk)
			if key == "" || key == "unknown" {
				continue
			}
			b, exists := agg[key]
			if !exists {
				b = &ModelAggregate{Model: key}
				agg[key] = b
			}
			b.Reqs += mu.RequestCount
			b.InTokens += mu.InputTokens
			b.OutTokens += mu.OutputTokens
			b.CachedTokens += mu.CachedTokens
			b.Cost = math.Round((b.Cost+mu.TotalCost)*1000000.0) / 1000000.0
		}
	}

	out := make([]ModelAggregate, 0, len(agg))
	for _, b := range agg {
		out = append(out, *b)
	}
	return out
}

func (u *UsageTracker) GetPayload() interface{} {

	mergeTokenStats := func(dest *TokenStats, src TokenStats) {
		dest.RequestCount += src.RequestCount
		dest.InputTokens += src.InputTokens
		dest.OutputTokens += src.OutputTokens
		dest.CachedTokens += src.CachedTokens
		dest.CacheHitRequests += src.CacheHitRequests
		dest.InputCost = math.Round((dest.InputCost+src.InputCost)*1000000.0) / 1000000.0
		dest.OutputCost = math.Round((dest.OutputCost+src.OutputCost)*1000000.0) / 1000000.0
		dest.CachedCost = math.Round((dest.CachedCost+src.CachedCost)*1000000.0) / 1000000.0
		dest.TotalCost = math.Round((dest.TotalCost+src.TotalCost)*1000000.0) / 1000000.0
	}

	getNewerTime := func(t1, t2 string) string {
		if t1 == "" {
			return t2
		}
		if t2 == "" {
			return t1
		}
		p1, err1 := time.Parse(time.RFC3339, t1)
		p2, err2 := time.Parse(time.RFC3339, t2)
		if err1 == nil && err2 == nil {
			if p1.After(p2) {
				return t1
			}
			return t2
		}
		if t1 > t2 {
			return t1
		}
		return t2
	}

	// deep copy and dynamically merge duplicate accounts by Email + Provider
	accountsCopy := make(map[string]*AccountUsage)
	for _, acc := range u.state.Accounts {
		email := strings.TrimSpace(acc.Email)
		provider := strings.TrimSpace(acc.Provider)
		if provider == "" {
			provider = "direct"
		}

		var mergeKey string
		if email != "" {
			mergeKey = strings.ToLower(email) + ":" + strings.ToLower(provider)
		} else {
			mergeKey = strings.ToLower(acc.AccountID) + ":" + strings.ToLower(provider)
		}

		existing, exists := accountsCopy[mergeKey]
		if !exists {
			modelsCopy := make(map[string]*ModelUsage)
			for mk, mu := range acc.Models {
				modelsCopy[mk] = &ModelUsage{
					Model: mu.Model,
					TokenStats: TokenStats{
						RequestCount:     mu.RequestCount,
						InputTokens:      mu.InputTokens,
						OutputTokens:     mu.OutputTokens,
						CachedTokens:     mu.CachedTokens,
						CacheHitRequests: mu.CacheHitRequests,
						InputCost:        mu.InputCost,
						OutputCost:       mu.OutputCost,
						CachedCost:       mu.CachedCost,
						TotalCost:        mu.TotalCost,
					},
					LastUsedAt: mu.LastUsedAt,
				}
			}
			accountsCopy[mergeKey] = &AccountUsage{
				AccountID: acc.AccountID,
				Email:     acc.Email,
				Provider:  acc.Provider,
				ProjectID: acc.ProjectID,
				ScopeType: acc.ScopeType,
				TokenStats: TokenStats{
					RequestCount:     acc.RequestCount,
					InputTokens:      acc.InputTokens,
					OutputTokens:     acc.OutputTokens,
					CachedTokens:     acc.CachedTokens,
					CacheHitRequests: acc.CacheHitRequests,
					InputCost:        acc.InputCost,
					OutputCost:       acc.OutputCost,
					CachedCost:       acc.CachedCost,
					TotalCost:        acc.TotalCost,
				},
				LastUsedAt: acc.LastUsedAt,
				Models:     modelsCopy,
			}
		} else {
			// Merge token stats
			mergeTokenStats(&existing.TokenStats, acc.TokenStats)
			existing.LastUsedAt = getNewerTime(existing.LastUsedAt, acc.LastUsedAt)

			// Merge models stats
			for mk, mu := range acc.Models {
				existingModel, modelExists := existing.Models[mk]
				if !modelExists {
					existing.Models[mk] = &ModelUsage{
						Model: mu.Model,
						TokenStats: TokenStats{
							RequestCount:     mu.RequestCount,
							InputTokens:      mu.InputTokens,
							OutputTokens:     mu.OutputTokens,
							CachedTokens:     mu.CachedTokens,
							CacheHitRequests: mu.CacheHitRequests,
							InputCost:        mu.InputCost,
							OutputCost:       mu.OutputCost,
							CachedCost:       mu.CachedCost,
							TotalCost:        mu.TotalCost,
						},
						LastUsedAt: mu.LastUsedAt,
					}
				} else {
					mergeTokenStats(&existingModel.TokenStats, mu.TokenStats)
					existingModel.LastUsedAt = getNewerTime(existingModel.LastUsedAt, mu.LastUsedAt)
				}
			}
		}
	}

	return UsageState{
		UpdatedAt: u.state.UpdatedAt,
		Totals: TokenStats{
			RequestCount:     u.state.Totals.RequestCount,
			InputTokens:      u.state.Totals.InputTokens,
			OutputTokens:     u.state.Totals.OutputTokens,
			CachedTokens:     u.state.Totals.CachedTokens,
			CacheHitRequests: u.state.Totals.CacheHitRequests,
			InputCost:        u.state.Totals.InputCost,
			OutputCost:       u.state.Totals.OutputCost,
			CachedCost:       u.state.Totals.CachedCost,
			TotalCost:        u.state.Totals.TotalCost,
		},
		Accounts: accountsCopy,
	}
}

func (u *UsageTracker) scheduleSave() {
	u.saveTimeoutLock.Lock()
	defer u.saveTimeoutLock.Unlock()

	if u.saveTimeout != nil {
		return
	}

	u.saveTimeout = time.AfterFunc(10*time.Second, func() {
		u.SaveToDisk()
		u.saveTimeoutLock.Lock()
		u.saveTimeout = nil
		u.saveTimeoutLock.Unlock()

		// 落盘节拍已降频至 10s,UI 通知改由 RecordUsage 显式 Unlock 后立即触发,
		// 此处不再放行 callback ——保留它就是"落盘驱动 UI",即"拉长落盘节拍=让界面变卡"的回归。
	})
}

func (u *UsageTracker) SaveToDisk() {
	u.RLock()
	path := u.persistPath
	if path == "" {
		u.RUnlock()
		return
	}

	// 深拷贝:Accounts map 及其 Models 子 map 按值复制后释放读锁。
	// 只持锁到数据不再指向 u.state,避免锁内做磁盘 IO(同时彻底抹掉 recordUsage
	// 并发写期间 RLock→MarshalIndent 之间的深浅拷贝分界竞态)。
	accountsDeep := make(map[string]*AccountUsage, len(u.state.Accounts))
	for k, v := range u.state.Accounts {
		if v == nil {
			continue
		}
		modelsCopy := make(map[string]*ModelUsage, len(v.Models))
		for mk, mu := range v.Models {
			if mu == nil {
				continue
			}
			cp := *mu
			modelsCopy[mk] = &cp
		}
		accCp := *v
		accCp.Models = modelsCopy
		accountsDeep[k] = &accCp
	}
	data := UsageData{
		Usage: UsageState{
			UpdatedAt: u.state.UpdatedAt,
			Totals:    u.state.Totals,
			Accounts:  accountsDeep,
		},
	}
	u.RUnlock()

	// Marshal 而非 MarshalIndent:体积小、CPU 更省;配 fileutil.WriteFileAtomic
	// (tmp+fsync+rename),磁盘写满/掉电不再截断 usage.json。
	bytesData, err := json.Marshal(data)
	if err != nil {
		fmt.Printf("[UsageTracker] Failed to marshal usage: %v\n", err)
		return
	}

	if err := fileutil.WriteFileAtomic(path, bytesData, 0644); err != nil {
		fmt.Printf("[UsageTracker] Failed to write usage: %v\n", err)
	}
}

func (u *UsageTracker) LoadFromDisk() {
	u.Lock()
	defer u.Unlock()

	if u.persistPath == "" {
		u.state = UsageState{Accounts: make(map[string]*AccountUsage)}
		return
	}

	if _, err := os.Stat(u.persistPath); os.IsNotExist(err) {
		u.state = UsageState{Accounts: make(map[string]*AccountUsage)}
		return
	}

	data, err := os.ReadFile(u.persistPath)
	if err != nil {
		u.state = UsageState{Accounts: make(map[string]*AccountUsage)}
		return
	}

	// Standardize mapping structure
	var parsed struct {
		Usage *UsageState `json:"usage"`
	}
	
	if err := json.Unmarshal(data, &parsed); err == nil && parsed.Usage != nil {
		u.state = *parsed.Usage
	} else {
		// Fallback top level
		var rawState UsageState
		if err := json.Unmarshal(data, &rawState); err == nil {
			u.state = rawState
		} else {
			u.state = UsageState{Accounts: make(map[string]*AccountUsage)}
		}
	}

	if u.state.Accounts == nil {
		u.state.Accounts = make(map[string]*AccountUsage)
	}

	for _, acc := range u.state.Accounts {
		if acc.Models == nil {
			acc.Models = make(map[string]*ModelUsage)
		}
	}
}
