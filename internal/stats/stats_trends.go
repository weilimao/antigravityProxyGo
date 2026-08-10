package stats

import (
	"fmt"
	"math"
	"time"
)

// 趋势桶累加簇：综合 trends 与 NVIDIA nvidiaTrends 各自按小时桶累加，物理隔离。


func (t *Tracker) updateTrends(inTokens, outTokens, cachedTokens int, cost, inputCost, outputCost, cachedCost float64) {
	t.appendTrendBucket(&t.trends, inTokens, outTokens, cachedTokens, cost, inputCost, outputCost, cachedCost)
}

// updateNvidiaTrends 把一次 NVIDIA 号池请求的 Token/成本累加到 nvidiaTrends 桶。
// 与 updateTrends 逻辑同构, 但目标桶是 nvidiaTrends, 与综合全局桶 trends 物理隔离,
// 保证「NVIDIA」Tab 的曲线只反映英伟达号池用量, 不会混入 Gemini/claude 直连请求,
// 反之「综合趋势」Tab 也不会被 NVIDIA 用量污染。
// cachedTokens/cachedCost 透传上游缓存命中口径, 与落点4 TrackRequestForModel 同源;
// 上游无 cache 时 cached==0, 与旧行为(硬编码 0)等价, 不造成回归。
func (t *Tracker) updateNvidiaTrends(inTokens, outTokens, cachedTokens int, cost, inputCost, outputCost, cachedCost float64) {
	t.appendTrendBucket(&t.nvidiaTrends, inTokens, outTokens, cachedTokens, cost, inputCost, outputCost, cachedCost)
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
