package stats

import (
	"math"
	"path/filepath"
	"sync"
	"time"

	"antigravity-proxy/internal/pricing"
)

type ModelStats struct {
	Reqs         int     `json:"reqs"`
	InTokens     int     `json:"inTokens"`
	OutTokens    int     `json:"outTokens"`
	CachedTokens int     `json:"cachedTokens"`
	Cost         float64 `json:"cost"`
}

type GlobalStats struct {
	TotalRequests     int `json:"totalRequests"`
	TotalInputTokens  int `json:"totalInputTokens"`
	TotalOutputTokens int `json:"totalOutputTokens"`
	TotalCachedTokens int `json:"totalCachedTokens"`
	// TotalCacheEligibleInputTokens 是“缓存命中率”分母专用累加器: 仅在 TrackRequest
	// (gemini/claude 直连链路, 上游响应携带真实 cachedTokens) 时累加 inputTokens,
	// 刻意不含 TrackRequestForModel 走的 NVIDIA 号池链路，且刻意不含 TAB 代码补全模型 (IsTabModel)。
	TotalCacheEligibleInputTokens int                    `json:"totalCacheEligibleInputTokens"`
	TabExcludedFromEligible       bool                   `json:"tabExcludedFromEligible,omitempty"`
	// BackfillForcedDone 是「Pools["antigravity"] 强制全量回填」一次性迁移标志。
	// 修正 8/8 上线后老 stats.json 已带零散 Pools["antigravity"](reqs=121 而非 Models 全量
	// 几千 M cached)的历史脏态: 首次 LoadFromDisk 检测到该标志缺失时, 调
	// BackfillPoolFromModelsForce 用 Models 表 Google 族全量覆盖式重算桶标量, 置 true 落盘,
	// 再次启动即跳过该段重算(避免每次启动都从 Models 重算覆盖正在增长的真实增量)。
	BackfillForcedDone            bool                   `json:"backfillForcedDone,omitempty"`
	// NvidiaUsageBackfillDone 是「NVIDIA 号池历史差量自愈合并」一次性迁移标志。
	// recordNvidiaUsage 的第 4 落点(TrackRequestForModel)上线晚于 usage.json 记账,
	// 历史 NVIDIA 请求量只躺在 usage.json 未进 stats.Models 模型表。首次 LoadFromDisk
	// 检测到该标志缺失时, 从 usage.json 聚合 nvidia 账号差量合并进 Models + 全局标量 +
	// Pools["nvidia"], 置 true 落盘, 再次启动即跳过。详见 stats_migrate_nvidia.go。
	NvidiaUsageBackfillDone       bool                   `json:"nvidiaUsageBackfillDone,omitempty"`
	TotalCost                     float64                `json:"totalCost"`
	TotalRetries                  int                    `json:"totalRetries"`
	TotalErrors                   int                    `json:"totalErrors"`
	Models                        map[string]*ModelStats `json:"models"`
	// Pools 是「按号池/按组」维度的命中率子聚合(前端缓存命中率卡片号池筛选数据源)。
	// key: "antigravity" / "nvidia" / "other:<groupId>"(缺失兜底 "other:__unknown__")。
	// 由 TrackRequestForPool 累加, 与全局标量(TotalCachedTokens 等)两套独立口径并行:
	// 全局口径含 NVIDIA/Other 合流, 池口径各自只取所属池/组, 互不串扰。旧口径不变式零回归。
	// 远端中继模式(app_monitor.go 远端分支)不填本字段 → 前端兜底回退旧三档全量口径。
	Pools map[string]*PoolStats `json:"pools,omitempty"`
}

type HourlyTrend struct {
	Time       string  `json:"time"` // "MM/DD HH:00"
	Input      int     `json:"input"`
	Output     int     `json:"output"`
	Cached     int     `json:"cached"`
	Requests   int     `json:"requests"`
	Cost       float64 `json:"cost"`
	InputCost  float64 `json:"inputCost"`
	OutputCost float64 `json:"outputCost"`
	CachedCost float64 `json:"cachedCost"`
}

type RequestLog struct {
	ID             string      `json:"id"`
	Timestamp      string      `json:"timestamp"` // "MM/DD HH:MM:SS"
	Method         string      `json:"method"`
	Host           string      `json:"host"`
	Path           string      `json:"path"`
	Model          string      `json:"model"`
	InTokens       int         `json:"inTokens"`
	OutTokens      int         `json:"outTokens"`
	CachedTokens   int         `json:"cachedTokens"`
	CacheStatus    string      `json:"cacheStatus"`
	StatusCode     int         `json:"statusCode"`
	Cost           float64     `json:"cost"`
	Account        string      `json:"account"`
	RequestBody    interface{} `json:"requestBody"`
	RequestHeaders interface{} `json:"requestHeaders"`
	SessionID      string      `json:"sessionId"`
	DurationMs     int64       `json:"durationMs"`
	FirstByteMs    int64       `json:"firstByteMs"`
	// Family 标记本次请求所属的协议族,用于与「NVIDIA 号池」等专属链路做逻辑隔离。
	// gemini/claude 直连链路默认 ""(空),NVIDIA 号池链路记 "nvidia"。
	// 前端可据此为 NVIDIA 行渲染专属 badge/筛选,既可合并入主列表又便于按族区分,
	// 不污染 NVIDIA 专用趋势桶(nvidiaTrends)与综合趋势桶(trends)的物理隔离语义。
	Family string `json:"family"`
}

// RequestLogLite is the scalar-only projection of RequestLog sent on the
// stats-updated hot path. The logs table only renders these fields; the
// (potentially large) requestBody / requestHeaders are fetched on demand via
// GetRequestDetails when a user opens the details modal. This keeps the
// per-tick IPC payload and V8 JSON-parse allocations small, which is the
// single biggest lever for keeping the WebView renderer memory bounded under
// heavy traffic.
type RequestLogLite struct {
	ID           string  `json:"id"`
	Timestamp    string  `json:"timestamp"`
	Method       string  `json:"method"`
	Host         string  `json:"host"`
	Path         string  `json:"path"`
	Model        string  `json:"model"`
	InTokens     int     `json:"inTokens"`
	OutTokens    int     `json:"outTokens"`
	CachedTokens int     `json:"cachedTokens"`
	CacheStatus  string  `json:"cacheStatus"`
	StatusCode   int     `json:"statusCode"`
	Cost         float64 `json:"cost"`
	Account      string  `json:"account"`
	SessionID    string  `json:"sessionId"`
	DurationMs   int64   `json:"durationMs"`
	// FirstByteMs 为首字响应延迟(TTFT, 毫秒), 随轻量投影下行到 IPC 热路径,
	// 供前端展示首字延迟指标。未触发 MarkFirstByte() 时由 durationMs 兜底, 详见 ttft.go。
	FirstByteMs int64 `json:"firstByteMs"`
	// Family 与 RequestLog.Family 同义, 轻量投影随之下行到 IPC 热路径,
	// 供前端按族渲染 badge/筛选, 不携带 requestBody/requestHeaders(按需经 GetRequestDetails 拉取)。
	Family string `json:"family"`
}

func toRequestLogLite(r *RequestLog) RequestLogLite {
	return RequestLogLite{
		ID:           r.ID,
		Timestamp:    r.Timestamp,
		Method:       r.Method,
		Host:         r.Host,
		Path:         r.Path,
		Model:        r.Model,
		InTokens:     r.InTokens,
		OutTokens:    r.OutTokens,
		CachedTokens: r.CachedTokens,
		CacheStatus:  r.CacheStatus,
		StatusCode:   r.StatusCode,
		Cost:         r.Cost,
		Account:      r.Account,
		SessionID:    r.SessionID,
		DurationMs:   r.DurationMs,
		FirstByteMs:  r.FirstByteMs,
		Family:       r.Family,
	}
}

type StatsData struct {
	Stats        GlobalStats    `json:"stats"`
	Trends       []*HourlyTrend `json:"trends"`
	NvidiaTrends []*HourlyTrend `json:"nvidiaTrends,omitempty"`
	Requests     []*RequestLog  `json:"requests"`
}

type Tracker struct {
	sync.RWMutex
	persistPath string
	stats       GlobalStats
	trends      []*HourlyTrend
	// nvidiaTrends 是英伟达号池专用趋势桶, 与 trends (综合/全局桶) 完全隔离:
	// 由 TrackNvidiaRequest 累加, 不进 trends, 反之亦然。前端「使用趋势」的
	// 「NVIDIA」Tab 消费此序列, 「综合趋势」Tab 仍消费 trends, 两者互不污染。
	nvidiaTrends    []*HourlyTrend
	requests        []*RequestLog
	saveTimeout     *time.Timer
	saveTimeoutLock sync.Mutex
	pricingMgr      *pricing.Manager
	onPayloadUpdate func()
}

func NewTracker(pricingMgr *pricing.Manager) *Tracker {
	return &Tracker{
		stats: GlobalStats{
			Models: make(map[string]*ModelStats),
			Pools:  make(map[string]*PoolStats),
		},
		trends:       make([]*HourlyTrend, 0),
		nvidiaTrends: make([]*HourlyTrend, 0),
		requests:     make([]*RequestLog, 0),
		pricingMgr:   pricingMgr,
	}
}

func (t *Tracker) GetPricingMgr() *pricing.Manager {
	return t.pricingMgr
}

func (t *Tracker) Init(userDataPath string) {
	t.Lock()
	t.persistPath = filepath.Join(userDataPath, "stats.json")
	t.Unlock()

	t.LoadFromDisk()
}

func (t *Tracker) UpdatePath(newPath string) {
	t.Lock()
	if t.saveTimeout != nil {
		t.saveTimeout.Stop()
		t.saveTimeout = nil
	}
	t.Unlock()

	t.SaveToDisk()

	t.Lock()
	t.persistPath = filepath.Join(newPath, "stats.json")
	t.Unlock()

	t.LoadFromDisk()
}

func (t *Tracker) SetOnPayloadUpdate(fn func()) {
	t.Lock()
	defer t.Unlock()
	t.onPayloadUpdate = fn
}

func (t *Tracker) TrackRequest(modelName string, inTokens, outTokens, cachedTokens int) {
	t.Lock()
	defer t.Unlock()

	cost := t.pricingMgr.CalculateCost(modelName, inTokens, outTokens, cachedTokens)
	rate := t.pricingMgr.GetPricingForModel(modelName)

	nonCachedIn := inTokens - cachedTokens
	if nonCachedIn < 0 {
		nonCachedIn = 0
	}

	inputCost := math.Round((float64(nonCachedIn)*rate.Input/1000000.0)*1000000.0) / 1000000.0
	outputCost := math.Round((float64(outTokens)*rate.Output/1000000.0)*1000000.0) / 1000000.0
	cachedCost := math.Round((float64(cachedTokens)*rate.Cached/1000000.0)*1000000.0) / 1000000.0

	// 1. Update overall stats
	t.stats.TotalRequests++
	t.stats.TotalInputTokens += inTokens
	t.stats.TotalOutputTokens += outTokens
	t.stats.TotalCachedTokens += cachedTokens
	// 命中率分母: 仅 gemini/claude 直连链路(本方法)且非 TAB 补全模型累加, NVIDIA 经 TrackRequestForModel
	// 走专属方法不触达此行, TAB 经 IsTabModel 过滤不触达此行, 均不会稀释缓存命中率。
	if !IsTabModel(modelName) {
		t.stats.TotalCacheEligibleInputTokens += inTokens
	}
	t.stats.TotalCost = math.Round((t.stats.TotalCost+cost)*1000000.0) / 1000000.0

	// 2. Update model specific stats
	modelKey := "unknown"
	if modelName != "" {
		modelKey = modelName
	}

	if t.stats.Models == nil {
		t.stats.Models = make(map[string]*ModelStats)
	}

	m, exists := t.stats.Models[modelKey]
	if !exists {
		m = &ModelStats{}
		t.stats.Models[modelKey] = m
	}
	m.Reqs++
	m.InTokens += inTokens
	m.OutTokens += outTokens
	m.CachedTokens += cachedTokens
	m.Cost = math.Round((m.Cost+cost)*1000000.0) / 1000000.0

	// 3. Update hourly trends
	t.updateTrends(inTokens, outTokens, cachedTokens, cost, inputCost, outputCost, cachedCost)

	// 4. Trigger async save
	t.scheduleSave()
}

// TrackRequestForModel 将一次请求计入全局综合统计(顶部指标卡 + stats.Models 模型表 + trends
// 全局桶, 与 nvidiaTrends(NVIDIA 专用桶)物理隔离, 不会与 TrackNvidiaRequest 产生重复累加。
//
// 设计目的: 纳入 NVIDIA 号池链路的用量到「模型统计」Tab / 顶部指标卡 / 「综合趋势」曲线, 使其与
// gemini/claude 直连链路口径一致。本方法刻意不含 family 参数——family 仅是 RequestLog 的展示标记
// (落点5 用 AddRequestLogForFamily 写库), 不应进入统计累加签名, 避免误以为按族分流(最小惊讶原则)。
// 若未来确需按 family 分桶, 应在该处新增独立方法, 而非给本方法加被忽略的参数。
// cachedTokens 对 NVIDIA 上游(OpenAI Chat 协议)固定为 0。
func (t *Tracker) TrackRequestForModel(modelName string, inTokens, outTokens, cachedTokens int) {
	t.Lock()
	defer t.Unlock()

	cost := t.pricingMgr.CalculateCost(modelName, inTokens, outTokens, cachedTokens)
	rate := t.pricingMgr.GetPricingForModel(modelName)

	nonCachedIn := inTokens - cachedTokens
	if nonCachedIn < 0 {
		nonCachedIn = 0
	}

	inputCost := math.Round((float64(nonCachedIn)*rate.Input/1000000.0)*1000000.0) / 1000000.0
	outputCost := math.Round((float64(outTokens)*rate.Output/1000000.0)*1000000.0) / 1000000.0
	cachedCost := math.Round((float64(cachedTokens)*rate.Cached/1000000.0)*1000000.0) / 1000000.0

	// 1. Update overall stats
	t.stats.TotalRequests++
	t.stats.TotalInputTokens += inTokens
	t.stats.TotalOutputTokens += outTokens
	t.stats.TotalCachedTokens += cachedTokens
	// 注意: 本方法不累加 TotalCacheEligibleInputTokens。该字段是“缓存命中率”分母,
	// 仅 TrackRequest(gemini/claude 直连) 累加。本方法服务于 NVIDIA 号池链路(上游 OpenAI Chat
	// 协议无 cache, cachedTokens 恒 0), 若把其 input 计入命中率分母会永久稀释命中率——故刻意排除。
	// TotalInputTokens 仍累加, 保证“总 Token / 成本 / 模型表 / 综合趋势”口径含 NVIDIA 不变。
	t.stats.TotalCost = math.Round((t.stats.TotalCost+cost)*1000000.0) / 1000000.0

	// 2. Update model specific stats
	modelKey := "unknown"
	if modelName != "" {
		modelKey = modelName
	}

	if t.stats.Models == nil {
		t.stats.Models = make(map[string]*ModelStats)
	}

	m, exists := t.stats.Models[modelKey]
	if !exists {
		m = &ModelStats{}
		t.stats.Models[modelKey] = m
	}
	m.Reqs++
	m.InTokens += inTokens
	m.OutTokens += outTokens
	m.CachedTokens += cachedTokens
	m.Cost = math.Round((m.Cost+cost)*1000000.0) / 1000000.0

	// 3. Update hourly trends(综合趋势桶)
	t.updateTrends(inTokens, outTokens, cachedTokens, cost, inputCost, outputCost, cachedCost)

	// 4. Trigger async save
	t.scheduleSave()
}

// TrackNvidiaRequest 记录一次 NVIDIA 号池请求到 nvidiaTrends 专用桶。
// 与 TrackRequest 的关键区别: 不动 stats(全局统计) / 也不动 trends(综合趋势桶),
// 仅累加 nvidiaTrends, 供前端「使用趋势-NVIDIA」Tab 单独消费。
// modelName 应为去前缀后的上游展示名(如 "z-ai/glm-5.2"); 成本按 NVIDIA 价计算。
// cachedTokens 透传上游 OpenAI Chat usage 的缓存命中口径(prompt_cache_hit_tokens 或
// prompt_tokens_details.cached_tokens, 由 OpenAIChatUsage.CachedTokens() 解析后由
// recordNvidiaUsage 落点3 传入), 与落点4 TrackRequestForModel 同源。一旦上游/兼容端点
// 回报 cache 字段, 此处即如实累加到 nvidiaTrends 桶的 Cached/CachedCost, 使前端
// NVIDIA Tab 的「缓存命中」曲线与卡片出数。本方法仍刻意不动全局 stats(TotalCachedTokens
// 等由落点4 TrackRequestForModel 累加), 维持 nvidiaTrends 与综合桶的物理隔离语义不变。
// 调用方应先判 (input==0 && output==0) 跳过, 避免制造空桶。
func (t *Tracker) TrackNvidiaRequest(modelName string, inTokens, outTokens, cachedTokens int) {
	t.Lock()
	defer t.Unlock()

	cost := t.pricingMgr.CalculateCost(modelName, inTokens, outTokens, cachedTokens)
	rate := t.pricingMgr.GetPricingForModel(modelName)

	inputCost := math.Round((float64(inTokens)*rate.Input/1000000.0)*1000000.0) / 1000000.0
	outputCost := math.Round((float64(outTokens)*rate.Output/1000000.0)*1000000.0) / 1000000.0
	cachedCost := math.Round((float64(cachedTokens)*rate.Cached/1000000.0)*1000000.0) / 1000000.0

	t.updateNvidiaTrends(inTokens, outTokens, cachedTokens, cost, inputCost, outputCost, cachedCost)

	// Trigger async save — nvidiaTrends 同样落盘 stats.json, 重启可恢复。
	t.scheduleSave()
}

func (t *Tracker) TrackRetry(count int) {
	t.Lock()
	t.stats.TotalRetries += count
	t.Unlock()

	t.scheduleSave()
}

func (t *Tracker) TrackError(count int) {
	t.Lock()
	t.stats.TotalErrors += count
	t.Unlock()

	t.scheduleSave()
}
