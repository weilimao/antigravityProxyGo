package stats

import (
	"math"
	"path/filepath"
	"sync"
	"time"

	"antigravity-proxy/internal/pricing"
)

// MaxRequestLogs 定义内存与轻量投影中请求日志保留的最大条数。
const MaxRequestLogs = 150

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
	TotalCacheEligibleInputTokens int  `json:"totalCacheEligibleInputTokens"`
	TabExcludedFromEligible       bool `json:"tabExcludedFromEligible,omitempty"`
	// BackfillForcedDone 是「Pools["antigravity"] 强制全量回填」一次性迁移标志。
	// 修正 8/8 上线后老 stats.json 已带零散 Pools["antigravity"](reqs=121 而非 Models 全量
	// 几千 M cached)的历史脏态: 首次 LoadFromDisk 检测到该标志缺失时, 调
	// BackfillPoolFromModelsForce 用 Models 表 Google 族全量覆盖式重算桶标量, 置 true 落盘,
	// 再次启动即跳过该段重算(避免每次启动都从 Models 重算覆盖正在增长的真实增量)。
	BackfillForcedDone bool `json:"backfillForcedDone,omitempty"`
	// NvidiaUsageBackfillDone 是「NVIDIA 号池历史差量自愈合并」一次性迁移标志。
	// recordNvidiaUsage 的第 4 落点(TrackRequestForModel)上线晚于 usage.json 记账,
	// 历史 NVIDIA 请求量只躺在 usage.json 未进 stats.Models 模型表。首次 LoadFromDisk
	// 检测到该标志缺失时, 从 usage.json 聚合 nvidia 账号差量合并进 Models + 全局标量 +
	// Pools["nvidia"], 置 true 落盘, 再次启动即跳过。详见 stats_migrate_nvidia.go。
	NvidiaUsageBackfillDone bool `json:"nvidiaUsageBackfillDone,omitempty"`
	// RelayTrendsBackfillDone 是「远程中继历史用量回填趋势桶」一次性迁移标志。
	// 将 SQLite 中已有 mode='remote_relay' 的请求按小时桶回填进 trends 桶，使折线图与模型表口径对齐。
	RelayTrendsBackfillDone bool `json:"relayTrendsBackfillDone,omitempty"`
	TotalCost               float64                `json:"totalCost"`
	TotalRetries            int                    `json:"totalRetries"`
	TotalErrors             int                    `json:"totalErrors"`
	Models                  map[string]*ModelStats `json:"models"`
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
	UserID         string      `json:"userId,omitempty"`
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
	// ReasoningEffort 记录本次请求「命中上游」的思考等级(low/medium/high/max/none 等)。
	// 取映射折叠后真正发给上游的值(非客户端原始意图档),故:
	//   - NVIDIA-NIM deepseek 模式 low/medium 折叠成 high → 此处记 "high";max → "max";
	//   - Grok off→"none"、on→grokMapEffort 后档、unspecified→"";
	//   - Other 走 mapToOfficialOpenAIEffort(max 1:1 透传);
	//   - Anthropic 原生端点/gemini/claude 直连无该概念 → ""。
	// 由各池 record*Usage 从 upstreamReq(构造完成后)提取, 经 logCtx 透传落库;
	// 空串表示客户端未开思考或上游无 reasoning_effort 概念, 前端不渲染后缀。
	ReasoningEffort string `json:"reasoningEffort"`
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
	UserID       string  `json:"userId,omitempty"`
	SessionID    string  `json:"sessionId"`
	DurationMs   int64   `json:"durationMs"`
	// FirstByteMs 为首字响应延迟(TTFT, 毫秒), 随轻量投影下行到 IPC 热路径,
	// 供前端展示首字延迟指标。未触发 MarkFirstByte() 时由 durationMs 兜底, 详见 ttft.go。
	FirstByteMs int64 `json:"firstByteMs"`
	// Family 与 RequestLog.Family 同义, 轻量投影随之下行到 IPC 热路径,
	// 供前端按族渲染 badge/筛选, 不携带 requestBody/requestHeaders(按需经 GetRequestDetails 拉取)。
	Family string `json:"family"`
	// ReasoningEffort 与 RequestLog.ReasoningEffort 同义, 轻量投影随之下行到 IPC 热路径,
	// 供前端在请求日志「模型」列追加 (档) 后缀展示命中思考等级。空串 → 不渲染后缀。
	ReasoningEffort string `json:"reasoningEffort"`
}

func toRequestLogLite(r *RequestLog) RequestLogLite {
	return RequestLogLite{
		ID:              r.ID,
		Timestamp:       r.Timestamp,
		Method:          r.Method,
		Host:            r.Host,
		Path:            r.Path,
		Model:           r.Model,
		InTokens:        r.InTokens,
		OutTokens:       r.OutTokens,
		CachedTokens:    r.CachedTokens,
		CacheStatus:     r.CacheStatus,
		StatusCode:      r.StatusCode,
		Cost:            r.Cost,
		Account:         r.Account,
		UserID:          r.UserID,
		SessionID:       r.SessionID,
		DurationMs:      r.DurationMs,
		FirstByteMs:     r.FirstByteMs,
		Family:          r.Family,
		ReasoningEffort: r.ReasoningEffort,
	}
}

type StatsData struct {
	Stats        GlobalStats    `json:"stats"`
	Trends       []*HourlyTrend `json:"trends"`
	NvidiaTrends []*HourlyTrend `json:"nvidiaTrends,omitempty"`
	// Requests 仅为向后兼容保留: 旧版 stats.json 曾在本字段持久化 150 条完整请求日志
	// (含报文, 致文件膨胀到 12MB+)。现持久化职责已迁移至 SQLite request_logs
	// (SaveToDisk 停写; LoadFromDisk 读到本字段时一次性迁入 DB), 写出恒为空。
	Requests []*RequestLog `json:"requests,omitempty"`
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

	// 请求日志报文列的低频剪枝:原挂在每次 SaveToDisk(3s 防抖)热路径,现已解耦为
	// 独立 goroutine(5min),让热路径不留每 3s 一次的全表 UPDATE 抖动。
	startRequestBodyPruner()
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
	// defer 包裹保 panic 安全:critical section 任何 panic 都保证锁释放;
	// Unlock 在前、notify 在后,顺序保证 notify 时锁已释放(notifyPayloadUpdate 拿 RLock)。
	defer func() {
		t.Unlock()
		t.notifyPayloadUpdate()
	}()

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
	// 命中率分母: 仅 gemini/claude 直连链路(本方法)、命中缓存(cachedTokens > 0)且非 TAB 补全模型累加,
	// 0 缓存未命中请求不计入分母以防稀释缓存命中率, NVIDIA 经 TrackRequestForModel
	// 走专属方法不触达此行, TAB 经 IsTabModel 过滤不触达此行。
	if cachedTokens > 0 && !IsTabModel(modelName) {
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

// notifyPayloadUpdate 请求级 UI 通知:与 scheduleSave(磁盘落盘节拍)解耦。
// 各 Track* 方法在写锁释放后直接触发,由 triggerStatsUpdate(1s 节流)安插
// stats-updated。这样指标卡/日志刷新跟手(秒级),而落盘仍是独立低频节拍,
// 拉长落盘间隔(10s)不会让仪表盘变慢。callback 判空后调用,实际发射频率 ≤ 1/s。
func (t *Tracker) notifyPayloadUpdate() {
	t.RLock()
	cb := t.onPayloadUpdate
	t.RUnlock()
	if cb != nil {
		cb()
	}
}

// TrackRequestForModel 将一次请求计入全局综合统计
//
// 设计目的: 纳入 NVIDIA 号池链路的用量到「模型统计」Tab / 顶部指标卡 / 「综合趋势」曲线, 使其与
// gemini/claude 直连链路口径一致。本方法刻意不含 family 参数——family 仅是 RequestLog 的展示标记
// (落点5 用 AddRequestLogForFamily 写库), 不应进入统计累加签名, 避免误以为按族分流(最小惊讶原则)。
// 若未来确需按 family 分桶, 应在该处新增独立方法, 而非给本方法加被忽略的参数。
// cachedTokens 对 NVIDIA 上游(OpenAI Chat 协议)固定为 0。
func (t *Tracker) TrackRequestForModel(modelName string, inTokens, outTokens, cachedTokens int) {
	t.Lock()
	defer func() {
		t.Unlock()
		t.notifyPayloadUpdate()
	}()

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
	defer func() {
		t.Unlock()
		t.notifyPayloadUpdate()
	}()

	cost := t.pricingMgr.CalculateCost(modelName, inTokens, outTokens, cachedTokens)
	rate := t.pricingMgr.GetPricingForModel(modelName)

	// inputCost 必须基于 nonCachedIn(扣除缓存命中部分), 与 TrackRequest(241行) /
	// TrackRequestForModel(305行) 同构。缓存命中那部分成本归 CachedCost(走 rate.Cached),
	// 不得在 InputCost 里按 rate.Input 重算, 否则违反「Cost = InputCost + OutputCost +
	// CachedCost」恒等式, 出现「输入总成本 > 总成本」的反向数值(前端 NVIDIA Tab 四卡自洽)。
	nonCachedIn := inTokens - cachedTokens
	if nonCachedIn < 0 {
		nonCachedIn = 0
	}

	inputCost := math.Round((float64(nonCachedIn)*rate.Input/1000000.0)*1000000.0) / 1000000.0
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
	t.notifyPayloadUpdate()
}

func (t *Tracker) TrackError(count int) {
	t.Lock()
	t.stats.TotalErrors += count
	t.Unlock()

	t.scheduleSave()
	t.notifyPayloadUpdate()
}
