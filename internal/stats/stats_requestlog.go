package stats

import (
	"encoding/json"
	"math"
	"strings"
	"time"

	"antigravity-proxy/internal/db"
	"antigravity-proxy/internal/pricing"
)

// 请求日志入库簇：AddRequestLog / AddRequestLogForFamily / AddRequestLogInMemoryOnly / ClearRetriesOrErrors。

// requestBodyToDBString 将(已截断的)报文序列化为 JSON 文本写入 DB TEXT 列; nil → ""。
func requestBodyToDBString(v interface{}) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	if b, err := json.Marshal(v); err == nil {
		return string(b)
	}
	return ""
}

// requestBodyFromDBString 将 DB TEXT 列反序列化回 interface{}(详情弹窗按对象美化展示);
// 空串 → nil, 解析失败(历史脏数据) → 原样按字符串兜底, 不丢失内容。
func requestBodyFromDBString(s string) interface{} {
	if s == "" {
		return nil
	}
	var v interface{}
	if err := json.Unmarshal([]byte(s), &v); err == nil {
		return v
	}
	return s
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
	if len(t.requests) > MaxRequestLogs {
		t.requests = t.requests[:MaxRequestLogs]
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

		userID := rl.Account
		if strings.TrimSpace(rl.UserID) != "" {
			userID = strings.TrimSpace(rl.UserID)
		}
		dbItem := &db.RequestLog{
			ReqID:        rl.ID,
			Timestamp:    timestamp,
			Mode:         "local",
			UserID:       userID,
			ModelName:    rl.Model,
			InTokens:     rl.InTokens,
			OutTokens:    rl.OutTokens,
			CachedTokens: rl.CachedTokens,
			Cost:         rl.Cost,
			InputCost:    inputCost,
			OutputCost:   outputCost,
			CachedCost:   cachedCost,
			DurationMs:   rl.DurationMs,
			// 修复历史遗漏: FirstByteMs 此前未写入 DB 列(request_logs.first_byte_ms 恒 0),
			// 现与内存环/IPC 热路径同口径写入, 供跨重启回填后首帧列有值。
			FirstByteMs:    rl.FirstByteMs,
			StatusCode:     rl.StatusCode,
			Method:         rl.Method,
			Host:           rl.Host,
			Path:           rl.Path,
			SessionID:      rl.SessionID,
			Family:         rl.Family,
			RequestBody:    requestBodyToDBString(rl.RequestBody),
			RequestHeaders: requestBodyToDBString(rl.RequestHeaders),
			CacheStatus:    rl.CacheStatus,
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
	if len(t.requests) > MaxRequestLogs {
		t.requests = t.requests[:MaxRequestLogs]
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

		userID := rl.Account
		if strings.TrimSpace(rl.UserID) != "" {
			userID = strings.TrimSpace(rl.UserID)
		}
		dbItem := &db.RequestLog{
			ReqID:           rl.ID,
			Timestamp:       timestamp,
			Mode:            "local",
			UserID:          userID,
			ModelName:       rl.Model,
			InTokens:        rl.InTokens,
			OutTokens:       rl.OutTokens,
			CachedTokens:    rl.CachedTokens,
			Cost:            rl.Cost,
			InputCost:       inputCost,
			OutputCost:      outputCost,
			CachedCost:      cachedCost,
			DurationMs:      rl.DurationMs,
			FirstByteMs:     rl.FirstByteMs,
			StatusCode:      rl.StatusCode,
			Method:          rl.Method,
			Host:            rl.Host,
			Path:            rl.Path,
			SessionID:       rl.SessionID,
			Family:          rl.Family,
			ReasoningEffort: rl.ReasoningEffort,
			RequestBody:     requestBodyToDBString(rl.RequestBody),
			RequestHeaders:  requestBodyToDBString(rl.RequestHeaders),
			CacheStatus:     rl.CacheStatus,
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
	if len(t.requests) > MaxRequestLogs {
		t.requests = t.requests[:MaxRequestLogs]
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
