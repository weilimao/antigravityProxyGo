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

	"antigravity-proxy/internal/db"
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
	// 刻意不含 TrackRequestForModel 走的 NVIDIA 号池链路——NVIDIA 上游(OpenAI Chat 协议)
	// 无 cache 概念, cachedTokens 恒为 0, 若其 input 计入分母会永久稀释命中率。
	// TotalInputTokens(口径不变, 含 NVIDIA) 仍用于总 Token / 成本 / 模型表 / 综合趋势,
	// 二者各司其职: 前者是命中率分母, 后者是“含 NVIDIA 的全部输入”分母。
	TotalCacheEligibleInputTokens int                    `json:"totalCacheEligibleInputTokens"`
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
	// 命中率分母: 仅 gemini/claude 直连链路(本方法)累加, NVIDIA 经 TrackRequestForModel
	// 走专属方法不触达此行, 故其 input 不会稀释缓存命中率(详见 TotalCacheEligibleInputTokens 注释)。
	t.stats.TotalCacheEligibleInputTokens += inTokens
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
// modelName 应为去前缀后的上游展示名(如 "z-ai/glm-5.2"); 成本按 NVIDIA 价计算,
// cache 概念在 NVIDIA 上游(OpenAI Chat 协议)不存在, cachedTokens 固定为 0。
// 调用方应先判 (input==0 && output==0) 跳过, 避免制造空桶。
func (t *Tracker) TrackNvidiaRequest(modelName string, inTokens, outTokens int) {
	t.Lock()
	defer t.Unlock()

	cost := t.pricingMgr.CalculateCost(modelName, inTokens, outTokens, 0)
	rate := t.pricingMgr.GetPricingForModel(modelName)

	inputCost := math.Round((float64(inTokens)*rate.Input/1000000.0)*1000000.0) / 1000000.0
	outputCost := math.Round((float64(outTokens)*rate.Output/1000000.0)*1000000.0) / 1000000.0

	t.updateNvidiaTrends(inTokens, outTokens, cost, inputCost, outputCost)

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

// GetTotalRetries 轻量级读取，避免 GetPayload 的全量深拷贝
func (t *Tracker) GetTotalRetries() int {
	t.RLock()
	defer t.RUnlock()
	return t.stats.TotalRetries
}

// GetTotalRequests 轻量级读取全局总请求数, 供外部(含单测)不做全量 payload 深拷贝即能校验
// 落点4(TrackRequestForFamily)是否被触发。与 GetTotalRetries 同口径, 读锁内返回标量。
func (t *Tracker) GetTotalRequests() int {
	t.RLock()
	defer t.RUnlock()
	return t.stats.TotalRequests
}

// GetTotalCachedTokens 轻量级读取全局累计缓存命中 token(TotalCachedTokens), 供单测断言
// TrackRequestForModel 的 cached 透传口径(缓存命中率分子)真实写入而非恒 0。
func (t *Tracker) GetTotalCachedTokens() int {
	t.RLock()
	defer t.RUnlock()
	return t.stats.TotalCachedTokens
}

// GetRequestLogCount 轻量级读取内存请求日志条数, 供外部(含单测)校验落点5
// (AddRequestLogForFamily)是否把日志写入了内存 requests 快照。读锁内返回长度, 不回切片别名。
func (t *Tracker) GetRequestLogCount() int {
	t.RLock()
	defer t.RUnlock()
	return len(t.requests)
}

// GetRecentRequestFirstByteMs 轻量级读取最近一条内存请求日志的 FirstByteMs, 供单测端到端
// 断言 TTFT 打点链路(FirstByteRecorder → RequestLog.FirstByteMs)真实闭环而非恒 0。
// 无日志时返回 -1。读锁内取值, 不回切片别名。
func (t *Tracker) GetRecentRequestFirstByteMs() int64 {
	t.RLock()
	defer t.RUnlock()
	if len(t.requests) == 0 {
		return -1
	}
	return t.requests[len(t.requests)-1].FirstByteMs
}

// GetRecentRequestCacheStatus 轻量级读取最近一条内存请求日志的 CacheStatus, 供单测端到端
// 断言缓存命中链路(record*Usage 的 cached>0 → CacheStatus="HIT")真实闭环而非恒 "NONE"。
// 无日志时返回 ""。读锁内取值, 不回切片别名。
func (t *Tracker) GetRecentRequestCacheStatus() string {
	t.RLock()
	defer t.RUnlock()
	if len(t.requests) == 0 {
		return ""
	}
	return t.requests[len(t.requests)-1].CacheStatus
}

// GetNvidiaTrends 轻量级读取 NVIDIA 号池专用趋势桶的深拷贝, 供 app.go 远程中继分支
// 在手工组装 stats-updated payload 时携带本地 nvidiaTrends (该分支走 remote query 不
// 调 GetPayload, 故需单独取)。线程安全: 读锁内值拷贝每个 HourlyTrend, 与 GetPayload 的
// trendsCopy 同口径, 避免返回内部切片别名导致的并发写竞争。
func (t *Tracker) GetNvidiaTrends() []*HourlyTrend {
	t.RLock()
	defer t.RUnlock()
	copyOut := make([]*HourlyTrend, len(t.nvidiaTrends))
	for i, tr := range t.nvidiaTrends {
		copyOut[i] = &HourlyTrend{
			Time:       tr.Time,
			Input:      tr.Input,
			Output:     tr.Output,
			Cached:     tr.Cached,
			Requests:   tr.Requests,
			Cost:       tr.Cost,
			InputCost:  tr.InputCost,
			OutputCost: tr.OutputCost,
			CachedCost: tr.CachedCost,
		}
	}
	return copyOut
}

func (t *Tracker) updateTrends(inTokens, outTokens, cachedTokens int, cost, inputCost, outputCost, cachedCost float64) {
	t.appendTrendBucket(&t.trends, inTokens, outTokens, cachedTokens, cost, inputCost, outputCost, cachedCost)
}

// updateNvidiaTrends 把一次 NVIDIA 号池请求的 Token/成本累加到 nvidiaTrends 桶。
// 与 updateTrends 逻辑同构, 但目标桶是 nvidiaTrends, 与综合全局桶 trends 物理隔离,
// 保证「NVIDIA」Tab 的曲线只反映英伟达号池用量, 不会混入 Gemini/claude 直连请求,
// 反之「综合趋势」Tab 也不会被 NVIDIA 用量污染。
func (t *Tracker) updateNvidiaTrends(inTokens, outTokens int, cost, inputCost, outputCost float64) {
	t.appendTrendBucket(&t.nvidiaTrends, inTokens, outTokens, 0, cost, inputCost, outputCost, 0)
}

// appendTrendBucket 是按小时桶累加趋势的通用内核, 由 updateTrends(综合桶) 与
// updateNvidiaTrends(NVIDIA 桶) 共用。target 为桶切片指针, 调用方负责并发安全
// (二者均在 Tracker.Lock 持有区内调用)。每桶最多保留 720 点(30 天小时级)。
func (t *Tracker) appendTrendBucket(target *[]*HourlyTrend, inTokens, outTokens, cachedTokens int, cost, inputCost, outputCost, cachedCost float64) {
	now := time.Now()
	hourLabel := fmt.Sprintf("%02d:00", now.Hour())
	dateLabel := fmt.Sprintf("%02d/%02d", now.Month(), now.Day())
	timeKey := dateLabel + " " + hourLabel

	var currentBin *HourlyTrend
	for _, bin := range *target {
		if bin.Time == timeKey {
			currentBin = bin
			break
		}
	}

	if currentBin == nil {
		currentBin = &HourlyTrend{
			Time: timeKey,
		}
		*target = append(*target, currentBin)
		// Limit to last 720 data points (30 days of hourly bins)
		if len(*target) > 720 {
			*target = (*target)[1:]
		}
	}

	currentBin.Input += inTokens
	currentBin.Output += outTokens
	currentBin.Cached += cachedTokens
	currentBin.Requests++
	currentBin.Cost = math.Round((currentBin.Cost+cost)*1000000.0) / 1000000.0
	currentBin.InputCost = math.Round((currentBin.InputCost+inputCost)*1000000.0) / 1000000.0
	currentBin.OutputCost = math.Round((currentBin.OutputCost+outputCost)*1000000.0) / 1000000.0
	currentBin.CachedCost = math.Round((currentBin.CachedCost+cachedCost)*1000000.0) / 1000000.0
}

func (t *Tracker) AddRequestLog(reqLog *RequestLog) {
	// 只保留真正的模型对话/发送请求（即包含 generatecontent 或 predict 的 API 调用）
	p := strings.ToLower(reqLog.Path)
	isRealModel := strings.Contains(p, "generatecontent") || strings.Contains(p, "predict")
	if !isRealModel {
		return
	}

	if reqLog.Model == "" || reqLog.Model == "unknown" {
		return
	}

	t.Lock()
	reqLog.Cost = t.pricingMgr.CalculateCost(reqLog.Model, reqLog.InTokens, reqLog.OutTokens, reqLog.CachedTokens)
	reqLog.RequestBody = TruncateRequestBody(reqLog.RequestBody)

	t.requests = append([]*RequestLog{reqLog}, t.requests...)
	if len(t.requests) > 50 {
		t.requests = t.requests[:50]
	}
	t.Unlock()

	go func(rl *RequestLog, prMgr *pricing.Manager) {
		timestamp := time.Now().Format(time.RFC3339)
		rate := prMgr.GetPricingForModel(rl.Model)
		nonCachedIn := rl.InTokens - rl.CachedTokens
		if nonCachedIn < 0 {
			nonCachedIn = 0
		}
		inputCost := math.Round((float64(nonCachedIn)*rate.Input/1000000.0)*1000000.0) / 1000000.0
		outputCost := math.Round((float64(rl.OutTokens)*rate.Output/1000000.0)*1000000.0) / 1000000.0
		cachedCost := math.Round((float64(rl.CachedTokens)*rate.Cached/1000000.0)*1000000.0) / 1000000.0

		dbItem := &db.RequestLog{
			ReqID:        rl.ID,
			Timestamp:    timestamp,
			Mode:         "local",
			UserID:       rl.Account,
			ModelName:    rl.Model,
			InTokens:     rl.InTokens,
			OutTokens:    rl.OutTokens,
			CachedTokens: rl.CachedTokens,
			Cost:         rl.Cost,
			InputCost:    inputCost,
			OutputCost:   outputCost,
			CachedCost:   cachedCost,
			DurationMs:   rl.DurationMs,
			StatusCode:   rl.StatusCode,
			Method:       rl.Method,
			Host:         rl.Host,
			Path:         rl.Path,
			SessionID:    rl.SessionID,
		}
		_ = db.InsertRequestLog(dbItem)
	}(reqLog, t.pricingMgr)

	t.scheduleSave()
}

// AddRequestLogForFamily 与 AddRequestLog 同构, 但跳过 isRealModel 过滤: NVIDIA 上游走 OpenAI Chat
// 协议, 入站 Path 形如 /nvidia/v1/chat/completions, 不含 gemini 链路的 generatecontent/predict 关键词,
// 既有 AddRequestLog 的过滤会把 NVIDIA 请求全丢弃(漏计根因之一)。本方法以显式 family 入库,
// 供 NVIDIA 链路把成功请求写入「请求日志」列表, 与 gemini/claude 口径一致。
//
// 与 AddRequestLog 的其余差异: 仅保留 Model==""||"unknown" 跳过与 TruncateRequestBody 截断;
// Cost 仍复用 pricingMgr.CalculateCost 重算; cachedTokens 由调用方填(NVIDIA 固定 0, CacheStatus="NONE")。
// 落库 db.RequestLog 时写入 family 列, 使远程聚合查询可按族过滤。
func (t *Tracker) AddRequestLogForFamily(reqLog *RequestLog) {
	if reqLog.Model == "" || reqLog.Model == "unknown" {
		return
	}

	t.Lock()
	reqLog.Cost = t.pricingMgr.CalculateCost(reqLog.Model, reqLog.InTokens, reqLog.OutTokens, reqLog.CachedTokens)
	reqLog.RequestBody = TruncateRequestBody(reqLog.RequestBody)

	t.requests = append([]*RequestLog{reqLog}, t.requests...)
	if len(t.requests) > 50 {
		t.requests = t.requests[:50]
	}
	t.Unlock()

	go func(rl *RequestLog, prMgr *pricing.Manager) {
		timestamp := time.Now().Format(time.RFC3339)
		rate := prMgr.GetPricingForModel(rl.Model)
		nonCachedIn := rl.InTokens - rl.CachedTokens
		if nonCachedIn < 0 {
			nonCachedIn = 0
		}
		inputCost := math.Round((float64(nonCachedIn)*rate.Input/1000000.0)*1000000.0) / 1000000.0
		outputCost := math.Round((float64(rl.OutTokens)*rate.Output/1000000.0)*1000000.0) / 1000000.0
		cachedCost := math.Round((float64(rl.CachedTokens)*rate.Cached/1000000.0)*1000000.0) / 1000000.0

		dbItem := &db.RequestLog{
			ReqID:        rl.ID,
			Timestamp:    timestamp,
			Mode:         "local",
			UserID:       rl.Account,
			ModelName:    rl.Model,
			InTokens:     rl.InTokens,
			OutTokens:    rl.OutTokens,
			CachedTokens: rl.CachedTokens,
			Cost:         rl.Cost,
			InputCost:    inputCost,
			OutputCost:   outputCost,
			CachedCost:   cachedCost,
			DurationMs:   rl.DurationMs,
			StatusCode:   rl.StatusCode,
			Method:       rl.Method,
			Host:         rl.Host,
			Path:         rl.Path,
			SessionID:    rl.SessionID,
			Family:       rl.Family,
		}
		_ = db.InsertRequestLog(dbItem)
	}(reqLog, t.pricingMgr)

	t.scheduleSave()
}

func (t *Tracker) AddRequestLogInMemoryOnly(reqLog *RequestLog) {
	// 只保留真正的模型对话/发送请求（即包含 generatecontent 或 predict 的 API 调用）
	p := strings.ToLower(reqLog.Path)
	isRealModel := strings.Contains(p, "generatecontent") || strings.Contains(p, "predict")
	if !isRealModel {
		return
	}

	if reqLog.Model == "" || reqLog.Model == "unknown" {
		return
	}

	t.Lock()
	reqLog.Cost = t.pricingMgr.CalculateCost(reqLog.Model, reqLog.InTokens, reqLog.OutTokens, reqLog.CachedTokens)
	reqLog.RequestBody = TruncateRequestBody(reqLog.RequestBody)

	t.requests = append([]*RequestLog{reqLog}, t.requests...)
	if len(t.requests) > 50 {
		t.requests = t.requests[:50]
	}
	t.Unlock()

	t.scheduleSave()
}

func (t *Tracker) ClearRetriesOrErrors(logType string) {
	t.Lock()
	if logType == "RETRY" || logType == "ALL" {
		t.stats.TotalRetries = 0
	}
	if logType == "ERROR" || logType == "ALL" {
		t.stats.TotalErrors = 0
	}
	t.Unlock()

	t.SaveToDisk()
}

func (t *Tracker) GetPayload(usagePayload interface{}) map[string]interface{} {
	t.RLock()
	defer t.RUnlock()

	// deep copy map/arrays for thread safety when returning payload
	modelsCopy := make(map[string]*ModelStats)
	for k, v := range t.stats.Models {
		modelsCopy[k] = &ModelStats{
			Reqs:         v.Reqs,
			InTokens:     v.InTokens,
			OutTokens:    v.OutTokens,
			CachedTokens: v.CachedTokens,
			Cost:         v.Cost,
		}
	}

	statsCopy := GlobalStats{
		TotalRequests:                 t.stats.TotalRequests,
		TotalInputTokens:              t.stats.TotalInputTokens,
		TotalOutputTokens:             t.stats.TotalOutputTokens,
		TotalCachedTokens:             t.stats.TotalCachedTokens,
		TotalCacheEligibleInputTokens: t.stats.TotalCacheEligibleInputTokens,
		TotalCost:                     t.stats.TotalCost,
		TotalRetries:                  t.stats.TotalRetries,
		TotalErrors:                   t.stats.TotalErrors,
		Models:                        modelsCopy,
		Pools:                         copyPools(t.stats.Pools),
	}

	trendsCopy := make([]*HourlyTrend, len(t.trends))
	for i, trend := range t.trends {
		trendsCopy[i] = &HourlyTrend{
			Time:       trend.Time,
			Input:      trend.Input,
			Output:     trend.Output,
			Cached:     trend.Cached,
			Requests:   trend.Requests,
			Cost:       trend.Cost,
			InputCost:  trend.InputCost,
			OutputCost: trend.OutputCost,
			CachedCost: trend.CachedCost,
		}
	}

	// nvidiaTrendsCopy: 英伟达号池专用趋势桶深拷贝, 供前端「NVIDIA」Tab 消费;
	// 与综合趋势 trendsCopy 物理隔离, 二者在前端按 scope 切换, 互不污染。
	nvidiaTrendsCopy := make([]*HourlyTrend, len(t.nvidiaTrends))
	for i, trend := range t.nvidiaTrends {
		nvidiaTrendsCopy[i] = &HourlyTrend{
			Time:       trend.Time,
			Input:      trend.Input,
			Output:     trend.Output,
			Cached:     trend.Cached,
			Requests:   trend.Requests,
			Cost:       trend.Cost,
			InputCost:  trend.InputCost,
			OutputCost: trend.OutputCost,
			CachedCost: trend.CachedCost,
		}
	}

	// Lite projection: only scalar fields. requestBody / requestHeaders stay
	// in t.requests and are fetched on demand via GetRequestDetails.
	requestsCopy := make([]RequestLogLite, len(t.requests))
	for i, req := range t.requests {
		requestsCopy[i] = toRequestLogLite(req)
	}

	return map[string]interface{}{
		"stats":        statsCopy,
		"trends":       trendsCopy,
		"nvidiaTrends": nvidiaTrendsCopy,
		"requests":     requestsCopy,
		"usage":        usagePayload,
	}
}

func (t *Tracker) GetPayloadSimplified(usagePayload interface{}) map[string]interface{} {
	t.RLock()
	defer t.RUnlock()

	// deep copy map/arrays for thread safety when returning payload
	modelsCopy := make(map[string]*ModelStats)
	for k, v := range t.stats.Models {
		modelsCopy[k] = &ModelStats{
			Reqs:         v.Reqs,
			InTokens:     v.InTokens,
			OutTokens:    v.OutTokens,
			CachedTokens: v.CachedTokens,
			Cost:         v.Cost,
		}
	}

	statsCopy := GlobalStats{
		TotalRequests:                 t.stats.TotalRequests,
		TotalInputTokens:              t.stats.TotalInputTokens,
		TotalOutputTokens:             t.stats.TotalOutputTokens,
		TotalCachedTokens:             t.stats.TotalCachedTokens,
		TotalCacheEligibleInputTokens: t.stats.TotalCacheEligibleInputTokens,
		TotalCost:                     t.stats.TotalCost,
		TotalRetries:                  t.stats.TotalRetries,
		TotalErrors:                   t.stats.TotalErrors,
		Models:                        modelsCopy,
		// Pools 随 stats-updated 一并下发供前端命中率卡片按池筛选;
		// 与 GetPayload 同口径深拷贝, 避免返回内部 map 别名导致的并发写竞争。
		Pools: copyPools(t.stats.Pools),
	}

	requestsCopy := make([]RequestLogLite, len(t.requests))
	for i, req := range t.requests {
		requestsCopy[i] = toRequestLogLite(req)
	}

	return map[string]interface{}{
		"stats":    statsCopy,
		"trends":   nil, // Omit trends to optimize memory/IPC overhead
		"requests": requestsCopy,
		"usage":    usagePayload,
	}
}

// GetRequestDetails returns the (truncated) requestBody and requestHeaders for
// a given request ID from the in-memory recent log buffer. Used by the frontend
// details modal to fetch heavy payload on demand instead of carrying it on
// every stats-updated tick. Returns nil/nil when the id is no longer in the
// 50-entry recent window (or in remote mode, where bodies aren't stored).
func (t *Tracker) GetRequestDetails(id string) (interface{}, interface{}) {
	t.RLock()
	defer t.RUnlock()
	for _, r := range t.requests {
		if r.ID == id {
			return r.RequestBody, r.RequestHeaders
		}
	}
	return nil, nil
}

func (t *Tracker) scheduleSave() {
	t.saveTimeoutLock.Lock()
	defer t.saveTimeoutLock.Unlock()

	if t.saveTimeout != nil {
		return
	}

	t.saveTimeout = time.AfterFunc(3*time.Second, func() {
		t.SaveToDisk()
		t.saveTimeoutLock.Lock()
		t.saveTimeout = nil
		t.saveTimeoutLock.Unlock()

		t.RLock()
		callback := t.onPayloadUpdate
		t.RUnlock()
		if callback != nil {
			callback()
		}
	})
}

func (t *Tracker) SaveToDisk() {
	t.RLock()
	path := t.persistPath
	if path == "" {
		t.RUnlock()
		return
	}

	// Deep-copy all mutable slices while holding the read lock so that
	// json.Marshal (which uses reflection) never races with concurrent writes.
	statsCopy := GlobalStats{
		TotalRequests:                 t.stats.TotalRequests,
		TotalInputTokens:              t.stats.TotalInputTokens,
		TotalOutputTokens:             t.stats.TotalOutputTokens,
		TotalCachedTokens:             t.stats.TotalCachedTokens,
		TotalCacheEligibleInputTokens: t.stats.TotalCacheEligibleInputTokens,
		TotalCost:                     t.stats.TotalCost,
		TotalRetries:                  t.stats.TotalRetries,
		TotalErrors:                   t.stats.TotalErrors,
		Models:                        make(map[string]*ModelStats, len(t.stats.Models)),
		Pools:                         copyPools(t.stats.Pools),
	}
	for k, v := range t.stats.Models {
		ms := *v // value copy, not pointer
		statsCopy.Models[k] = &ms
	}

	trendsCopy := make([]*HourlyTrend, len(t.trends))
	for i, tr := range t.trends {
		cp := *tr // value copy
		trendsCopy[i] = &cp
	}

	// nvidiaTrendsCopy: 英伟达号池专用趋势桶序列化拷贝, 与 trends 同样做值拷贝避免
	// json.Marshal 反射与并发写竞争; 落盘进 stats.json 的 nvidiaTrends 字段, 重启回填。
	nvidiaTrendsCopy := make([]*HourlyTrend, len(t.nvidiaTrends))
	for i, tr := range t.nvidiaTrends {
		cp := *tr
		nvidiaTrendsCopy[i] = &cp
	}

	reqsCopy := make([]*RequestLog, len(t.requests))
	for i, req := range t.requests {
		cp := *req // value copy
		reqsCopy[i] = &cp
	}
	t.RUnlock()

	// Marshal from fully-owned copies – no shared pointers, no data race.
	data := StatsData{
		Stats:        statsCopy,
		Trends:       trendsCopy,
		NvidiaTrends: nvidiaTrendsCopy,
		Requests:     reqsCopy,
	}

	bytesData, err := json.Marshal(data)
	if err != nil {
		fmt.Printf("[StatsTracker] Failed to marshal stats: %v\n", err)
		return
	}

	err = os.WriteFile(path, bytesData, 0644)
	if err != nil {
		fmt.Printf("[StatsTracker] Failed to write stats: %v\n", err)
	}
}

func (t *Tracker) LoadFromDisk() {
	t.Lock()
	defer t.Unlock()

	if t.persistPath == "" {
		t.seedEmptyTrends()
		return
	}

	if _, err := os.Stat(t.persistPath); os.IsNotExist(err) {
		t.seedEmptyTrends()
		return
	}

	data, err := os.ReadFile(t.persistPath)
	if err != nil {
		t.seedEmptyTrends()
		return
	}

	var parsed StatsData
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.seedEmptyTrends()
		return
	}

	t.stats = parsed.Stats
	if t.stats.Models == nil {
		t.stats.Models = make(map[string]*ModelStats)
	}
	// Pools 回填: 旧 stats.json 无本字段时为 nil, 兜底为空 map 保持 NewTracker 口径一致
	// (与 Models 兜底同构), 避免 TrackRequestForPool 的 getOrCreatePoolLocked 对 nil map 写入 panic。
	// 历史 Pools 缺失 → 重启后"按池/组口径"从 0 累加(全局口径从旧标量正常读回, 不受影响)。
	if t.stats.Pools == nil {
		t.stats.Pools = make(map[string]*PoolStats)
	}
	t.trends = parsed.Trends
	// nvidiaTrends 回填: 老 stats.json 无此字段时为 nil, 兜底为空切片保持 NewTracker 口径一致,
	// 避免 appendTrendBucket 对 nil 切片 append 时虽合法但与「始终非 nil」的约定不符。
	t.nvidiaTrends = parsed.NvidiaTrends
	if t.nvidiaTrends == nil {
		t.nvidiaTrends = make([]*HourlyTrend, 0)
	}
	t.requests = parsed.Requests

	for _, req := range t.requests {
		req.RequestBody = TruncateRequestBody(req.RequestBody)
	}

	if len(t.trends) <= 6 {
		t.seedEmptyTrends()
	}
}

func (t *Tracker) seedEmptyTrends() {
	t.trends = make([]*HourlyTrend, 0)
}

// TruncateRequestBody structure and string truncation to prevent OOM
func TruncateRequestBody(body interface{}) interface{} {
	if body == nil {
		return nil
	}

	switch val := body.(type) {
	case string:
		var parsed interface{}
		if err := json.Unmarshal([]byte(val), &parsed); err == nil {
			return processObject(parsed)
		}
		if len(val) > 1000 {
			return val[:400] + fmt.Sprintf("\n... [已截断，原字符数: %d] ...\n", len(val)) + val[len(val)-200:]
		}
		return val
	default:
		return processObject(body)
	}
}

func processObject(item interface{}) interface{} {
	if item == nil {
		return nil
	}

	switch v := item.(type) {
	case map[string]interface{}:
		newMap := make(map[string]interface{})
		for k, val := range v {
			if str, ok := val.(string); ok {
				if len(str) > 1000 {
					newMap[k] = str[:400] + fmt.Sprintf("... [已截断，原长度: %d 字符] ...", len(str)) + str[len(str)-100:]
				} else {
					newMap[k] = str
				}
			} else {
				newMap[k] = processObject(val)
			}
		}
		return newMap
	case []interface{}:
		newSlice := make([]interface{}, len(v))
		for i, val := range v {
			newSlice[i] = processObject(val)
		}
		return newSlice
	default:
		return v
	}
}
