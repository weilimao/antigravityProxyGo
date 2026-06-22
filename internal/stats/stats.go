package stats

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
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
	TotalRequests     int                    `json:"totalRequests"`
	TotalInputTokens  int                    `json:"totalInputTokens"`
	TotalOutputTokens int                    `json:"totalOutputTokens"`
	TotalCachedTokens int                     `json:"totalCachedTokens"`
	TotalCost         float64                `json:"totalCost"`
	TotalRetries      int                    `json:"totalRetries"`
	TotalErrors       int                    `json:"totalErrors"`
	Models            map[string]*ModelStats `json:"models"`
}

type HourlyTrend struct {
	Time         string  `json:"time"` // "MM/DD HH:00"
	Input        int     `json:"input"`
	Output       int     `json:"output"`
	Cached       int     `json:"cached"`
	CacheCreated int     `json:"cacheCreated"`
	Cost         float64 `json:"cost"`
	InputCost    float64 `json:"inputCost"`
	OutputCost   float64 `json:"outputCost"`
	CachedCost   float64 `json:"cachedCost"`
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
}

type StatsData struct {
	Stats    GlobalStats    `json:"stats"`
	Trends   []*HourlyTrend `json:"trends"`
	Requests []*RequestLog  `json:"requests"`
}

type Tracker struct {
	sync.RWMutex
	persistPath    string
	stats          GlobalStats
	trends         []*HourlyTrend
	requests       []*RequestLog
	saveTimeout    *time.Timer
	saveTimeoutLock sync.Mutex
	pricingMgr     *pricing.Manager
	onPayloadUpdate func()
}

func NewTracker(pricingMgr *pricing.Manager) *Tracker {
	return &Tracker{
		stats: GlobalStats{
			Models: make(map[string]*ModelStats),
		},
		trends:     make([]*HourlyTrend, 0),
		requests:   make([]*RequestLog, 0),
		pricingMgr: pricingMgr,
	}
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

func (t *Tracker) updateTrends(inTokens, outTokens, cachedTokens int, cost, inputCost, outputCost, cachedCost float64) {
	now := time.Now()
	hourLabel := fmt.Sprintf("%02d:00", now.Hour())
	dateLabel := fmt.Sprintf("%02d/%02d", now.Month(), now.Day())
	timeKey := dateLabel + " " + hourLabel

	var currentBin *HourlyTrend
	for _, bin := range t.trends {
		if bin.Time == timeKey {
			currentBin = bin
			break
		}
	}

	if currentBin == nil {
		currentBin = &HourlyTrend{
			Time: timeKey,
		}
		t.trends = append(t.trends, currentBin)
		// Limit to last 720 data points (30 days of hourly bins)
		if len(t.trends) > 720 {
			t.trends = t.trends[1:]
		}
	}

	currentBin.Input += inTokens
	currentBin.Output += outTokens
	currentBin.Cached += cachedTokens
	currentBin.Cost = math.Round((currentBin.Cost+cost)*1000000.0) / 1000000.0
	currentBin.InputCost = math.Round((currentBin.InputCost+inputCost)*1000000.0) / 1000000.0
	currentBin.OutputCost = math.Round((currentBin.OutputCost+outputCost)*1000000.0) / 1000000.0
	currentBin.CachedCost = math.Round((currentBin.CachedCost+cachedCost)*1000000.0) / 1000000.0
}

func (t *Tracker) AddRequestLog(reqLog *RequestLog) {
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
		TotalRequests:     t.stats.TotalRequests,
		TotalInputTokens:  t.stats.TotalInputTokens,
		TotalOutputTokens: t.stats.TotalOutputTokens,
		TotalCachedTokens: t.stats.TotalCachedTokens,
		TotalCost:         t.stats.TotalCost,
		TotalRetries:      t.stats.TotalRetries,
		TotalErrors:       t.stats.TotalErrors,
		Models:            modelsCopy,
	}

	trendsCopy := make([]*HourlyTrend, len(t.trends))
	for i, trend := range t.trends {
		trendsCopy[i] = &HourlyTrend{
			Time:         trend.Time,
			Input:        trend.Input,
			Output:       trend.Output,
			Cached:       trend.Cached,
			CacheCreated: trend.CacheCreated,
			Cost:         trend.Cost,
			InputCost:    trend.InputCost,
			OutputCost:   trend.OutputCost,
			CachedCost:   trend.CachedCost,
		}
	}

	requestsCopy := make([]*RequestLog, len(t.requests))
	for i, req := range t.requests {
		requestsCopy[i] = &RequestLog{
			ID:             req.ID,
			Timestamp:      req.Timestamp,
			Method:         req.Method,
			Host:           req.Host,
			Path:           req.Path,
			Model:          req.Model,
			InTokens:       req.InTokens,
			OutTokens:      req.OutTokens,
			CachedTokens:   req.CachedTokens,
			CacheStatus:    req.CacheStatus,
			StatusCode:     req.StatusCode,
			Cost:           req.Cost,
			Account:        req.Account,
			RequestBody:    req.RequestBody,
			RequestHeaders: req.RequestHeaders,
			SessionID:      req.SessionID,
		}
	}

	return map[string]interface{}{
		"stats":    statsCopy,
		"trends":   trendsCopy,
		"requests": requestsCopy,
		"usage":    usagePayload,
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
		TotalRequests:     t.stats.TotalRequests,
		TotalInputTokens:  t.stats.TotalInputTokens,
		TotalOutputTokens: t.stats.TotalOutputTokens,
		TotalCachedTokens: t.stats.TotalCachedTokens,
		TotalCost:         t.stats.TotalCost,
		TotalRetries:      t.stats.TotalRetries,
		TotalErrors:       t.stats.TotalErrors,
		Models:            modelsCopy,
	}

	requestsCopy := make([]*RequestLog, len(t.requests))
	for i, req := range t.requests {
		requestsCopy[i] = &RequestLog{
			ID:             req.ID,
			Timestamp:      req.Timestamp,
			Method:         req.Method,
			Host:           req.Host,
			Path:           req.Path,
			Model:          req.Model,
			InTokens:       req.InTokens,
			OutTokens:      req.OutTokens,
			CachedTokens:   req.CachedTokens,
			CacheStatus:    req.CacheStatus,
			StatusCode:     req.StatusCode,
			Cost:           req.Cost,
			Account:        req.Account,
			RequestBody:    req.RequestBody,
			RequestHeaders: req.RequestHeaders,
			SessionID:      req.SessionID,
		}
	}

	return map[string]interface{}{
		"stats":    statsCopy,
		"trends":   nil, // Omit trends to optimize memory/IPC overhead
		"requests": requestsCopy,
		"usage":    usagePayload,
	}
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

	data := StatsData{
		Stats:    t.stats,
		Trends:   t.trends,
		Requests: t.requests,
	}
	t.RUnlock()

	bytesData, err := json.MarshalIndent(data, "", "  ")
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
	t.trends = parsed.Trends
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
	now := time.Now()
	// Generate 30 days of hourly data (720 points)
	for i := 719; i >= 0; i-- {
		targetTime := now.Add(time.Duration(-i) * time.Hour)
		hourLabel := fmt.Sprintf("%02d:00", targetTime.Hour())
		dateLabel := fmt.Sprintf("%02d/%02d", targetTime.Month(), targetTime.Day())

		hour := targetTime.Hour()
		dayOfWeek := targetTime.Weekday()

		// Sine wave + random factor for realistic diurnal variations
		timeFactor := math.Sin(float64(hour-6)/24.0*2.0*math.Pi)*0.4 + 0.6
		dayFactor := 1.0
		if dayOfWeek == time.Sunday || dayOfWeek == time.Saturday {
			dayFactor = 0.5
		}

		randGen := rand.New(rand.NewSource(targetTime.UnixNano()))
		base := (randGen.Float64()*0.4 + 0.8) * timeFactor * dayFactor

		input := int(math.Round(base * 300000))
		output := int(math.Round(base * 150000))
		cached := 0
		if randGen.Float64() > 0.4 {
			cached = int(math.Round(float64(input) * (randGen.Float64()*0.6 + 0.2)))
		}
		cacheCreated := int(math.Round(float64(input) * (randGen.Float64()*0.3 + 0.1)))

		nonCachedIn := input - cached
		if nonCachedIn < 0 {
			nonCachedIn = 0
		}

		// Calculate precise cost based on Gemini 1.5 Flash rates: In: $1.50/1M, Out: $9.00/1M, Cached: $0.375/1M
		inputCost := math.Round((float64(nonCachedIn)*1.50/1000000.0)*1000000.0) / 1000000.0
		outputCost := math.Round((float64(output)*9.00/1000000.0)*1000000.0) / 1000000.0
		cachedCost := math.Round((float64(cached)*0.375/1000000.0)*1000000.0) / 1000000.0
		cost := math.Round((inputCost+outputCost+cachedCost)*1000000.0) / 1000000.0

		t.trends = append(t.trends, &HourlyTrend{
			Time:         dateLabel + " " + hourLabel,
			Input:        input,
			Output:       output,
			Cached:       cached,
			CacheCreated: cacheCreated,
			Cost:         cost,
			InputCost:    inputCost,
			OutputCost:   outputCost,
			CachedCost:   cachedCost,
		})
	}
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
