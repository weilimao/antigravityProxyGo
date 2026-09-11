package stats

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"antigravity-proxy/internal/db"
)

// BackfillRelayTrendsFromDBLocked 从 SQLite request_logs 回填历史 remote_relay 请求用量。
//
// 背景: 过去远程中继(remote_relay)仅写入 SQLite request_logs, 未接入 TrackRequestForModel,
// 导致 trends 小时桶与全局标量中缺少中继流量, 使主仪表盘折线图与底部「模型统计」表出现数字割裂。
// 本函数将过去 30 天内(720 小时窗口)的 remote_relay 请求按真实时间小时桶聚合回填入 t.trends,
// 同时补齐 t.stats 全局标量与 Models 模型表。
//
// 幂等守卫: 由 t.stats.RelayTrendsBackfillDone 标志守卫, 首次执行完成后落盘, 后续启动直接跳过, 日常零开销。
// 调用方必须持有 t.Lock() 写锁。
func (t *Tracker) BackfillRelayTrendsFromDBLocked() {
	if t.stats.RelayTrendsBackfillDone {
		return
	}
	if db.GlobalDB == nil {
		return
	}

	// 仅拉取近 30 天(720 小时窗口)内的 remote_relay 记录
	sinceTime := time.Now().Add(-30 * 24 * time.Hour)
	sinceISO := sinceTime.Format(time.RFC3339)

	query := `
		SELECT model_name, timestamp, in_tokens, out_tokens, cached_tokens, cost, input_cost, output_cost, cached_cost
		FROM request_logs
		WHERE mode = 'remote_relay' AND timestamp >= ?
		ORDER BY timestamp ASC
	`

	rows, err := db.GlobalDB.Query(query, sinceISO)
	if err != nil {
		fmt.Printf("[StatsTracker] Relay trends backfill query error: %v\n", err)
		return
	}
	defer rows.Close()

	// 快速索引已有的 trends 桶
	trendsMap := make(map[string]*HourlyTrend, len(t.trends))
	for _, tr := range t.trends {
		if tr != nil && tr.Time != "" {
			trendsMap[tr.Time] = tr
		}
	}

	if t.stats.Models == nil {
		t.stats.Models = make(map[string]*ModelStats)
	}

	backfilledCount := 0
	for rows.Next() {
		var model, tsStr string
		var inT, outT, cachedT int
		var cost, inCost, outCost, cachedCost float64

		if err := rows.Scan(&model, &tsStr, &inT, &outT, &cachedT, &cost, &inCost, &outCost, &cachedCost); err != nil {
			continue
		}

		parsedTime, err := parseTimestampToLocal(tsStr)
		if err != nil {
			continue
		}

		timeKey := fmt.Sprintf("%02d/%02d %02d:00", parsedTime.Month(), parsedTime.Day(), parsedTime.Hour())

		// 1. 回填 trends 小时桶
		bin, exists := trendsMap[timeKey]
		if !exists {
			bin = &HourlyTrend{
				Time: timeKey,
			}
			trendsMap[timeKey] = bin
			t.trends = append(t.trends, bin)
		}

		bin.Input += inT
		bin.Output += outT
		bin.Cached += cachedT
		bin.Requests++
		bin.Cost = math.Round((bin.Cost+cost)*1000000.0) / 1000000.0
		bin.InputCost = math.Round((bin.InputCost+inCost)*1000000.0) / 1000000.0
		bin.OutputCost = math.Round((bin.OutputCost+outCost)*1000000.0) / 1000000.0
		bin.CachedCost = math.Round((bin.CachedCost+cachedCost)*1000000.0) / 1000000.0

		// 2. 回填全局标量
		t.stats.TotalRequests++
		t.stats.TotalInputTokens += inT
		t.stats.TotalOutputTokens += outT
		t.stats.TotalCachedTokens += cachedT
		t.stats.TotalCost = math.Round((t.stats.TotalCost+cost)*1000000.0) / 1000000.0

		// 3. 回填 Models 模型表
		mKey := "unknown"
		if strings.TrimSpace(model) != "" {
			mKey = strings.TrimSpace(model)
		}
		ms, mExists := t.stats.Models[mKey]
		if !mExists {
			ms = &ModelStats{}
			t.stats.Models[mKey] = ms
		}
		ms.Reqs++
		ms.InTokens += inT
		ms.OutTokens += outT
		ms.CachedTokens += cachedT
		ms.Cost = math.Round((ms.Cost+cost)*1000000.0) / 1000000.0

		backfilledCount++
	}

	// 保持 t.trends 按时间升序排序
	sort.SliceStable(t.trends, func(i, j int) bool {
		return compareTrendTime(t.trends[i].Time, t.trends[j].Time)
	})

	// 维持最多 720 个小时桶
	if len(t.trends) > 720 {
		t.trends = t.trends[len(t.trends)-720:]
	}

	t.stats.RelayTrendsBackfillDone = true
	t.scheduleSave()
	if backfilledCount > 0 {
		fmt.Printf("[StatsTracker] ✅ 成功回填 %d 条历史远程中继请求至趋势桶\n", backfilledCount)
	}
}

// parseTimestampToLocal 解析 RFC3339 字符串并转换为本地时区
func parseTimestampToLocal(ts string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, ts)
	if err == nil {
		return t.Local(), nil
	}
	// 兜底支持可能存在的未带时区的旧格式
	t, err = time.ParseInLocation("2006-01-02 15:04:05", ts, time.Local)
	if err == nil {
		return t, nil
	}
	return time.Time{}, err
}

// compareTrendTime 比较两个 "MM/DD HH:00" 格式的时间标签先后
func compareTrendTime(a, b string) bool {
	return a < b
}
