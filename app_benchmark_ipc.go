package main

import (
	"encoding/json"
	"fmt"
	"time"

	"antigravity-proxy/internal/db"
	"antigravity-proxy/internal/settings"
)

// app_benchmark_ipc.go: 模型测速(首帧/耗时) IPC 呼叫处理。
//
// 通道:
//   benchmark:get       — 取配置 + 最新结果 + lastRun/running(供卡片初始装载)
//   benchmark:save      — 保存配置(Enabled/Models/IntervalMinutes/Prompt/TimeoutMs);
//                         启用时保存后立即触发一轮测速, 使配置后卡片即时出数据。
//   benchmark:run-now   — 手动触发一轮测速(异步, 完成经 benchmark-updated 推送)
//   benchmark:models    — 取候选模型清单(中继模型映射里 Expose 的 ClientModel, 去重)
//
// 测速请求经 internal/benchmark 调度器走中继回环, 不计入仪表盘统计(见 relay IsBenchmark)。

func (a *App) handleBenchmarkIPC(channel string, args []interface{}) (string, bool, error) {
	marshalResponse := func(val interface{}) (string, error) {
		b, err := json.Marshal(val)
		if err != nil {
			return `{"success":false,"error":"JSON serialization error"}`, nil
		}
		return string(b), nil
	}

	switch channel {
	case "benchmark:get":
		cfg := a.settingsMgr.GetBenchmarkConfig()
		results, _ := db.ListBenchmarkResults()
		lastRun := time.Time{}
		running := false
		if a.benchmarkScheduler != nil {
			lastRun = a.benchmarkScheduler.LastRun()
			running = a.benchmarkScheduler.IsRunning()
		}
		data, _ := marshalResponse(map[string]interface{}{
			"success": true,
			"config": map[string]interface{}{
				"enabled":         cfg.Enabled,
				"models":          cfg.Models,
				"intervalMinutes": cfg.IntervalMinutes,
				"prompt":          cfg.Prompt,
				"timeoutMs":       cfg.TimeoutMs,
			},
			"results": results,
			"lastRun": lastRun.Format(time.RFC3339),
			"running": running,
		})
		return data, true, nil

	case "benchmark:save":
		var payload settings.BenchmarkConfig
		if len(args) > 0 {
			bytesPayload, _ := json.Marshal(args[0])
			_ = json.Unmarshal(bytesPayload, &payload)
		}
		// models 允许来自前端 textarea(每行一个)拆分后的数组, 此处由 SetBenchmarkConfig 去空去重。
		if err := a.settingsMgr.SetBenchmarkConfig(payload); err != nil {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
			return data, true, nil
		}
		// 配置变更后清零 lastRun, 使下一节拍立即触发(而非等满旧间隔)。
		if a.benchmarkScheduler != nil {
			a.benchmarkScheduler.ResetLastRun()
		}
		cfg := a.settingsMgr.GetBenchmarkConfig()
		a.AddLog(fmt.Sprintf("⚡ [测速] 配置已保存: %d 个模型, 间隔 %d 分钟, 已%s",
			len(cfg.Models), cfg.IntervalMinutes, enabledText(cfg.Enabled)))
		// 按钮文案为「保存并测速」: 无论是否启用定时, 只要配了模型就立即跑一轮,
		// 让用户配置完即时看到数据。启用开关仅控制后续周期性定时触发(maybeRun 判 enabled)。
		if len(cfg.Models) > 0 && a.benchmarkScheduler != nil {
			go a.benchmarkScheduler.RunNow()
		} else if len(cfg.Models) == 0 {
			// 清空模型时同步清结果表, 卡片回到空态。
			_ = db.ClearBenchmarkResults()
			if a.benchmarkScheduler != nil {
				a.benchmarkScheduler.EmitResults()
			}
		}
		data, _ := marshalResponse(map[string]interface{}{
			"success": true,
			"config": map[string]interface{}{
				"enabled":         cfg.Enabled,
				"models":          cfg.Models,
				"intervalMinutes": cfg.IntervalMinutes,
				"prompt":          cfg.Prompt,
				"timeoutMs":       cfg.TimeoutMs,
			},
		})
		return data, true, nil

	case "benchmark:run-now":
		if a.benchmarkScheduler == nil {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "测速调度器未启动"})
			return data, true, nil
		}
		go a.benchmarkScheduler.RunNow()
		a.AddLog("⚡ [测速] 手动触发一轮模型测速")
		data, _ := marshalResponse(map[string]interface{}{"success": true})
		return data, true, nil

	case "benchmark:run-model":
		// 单模型重测: 仅探测指定模型并更新该行结果, 供卡片每行「▷」按钮调用。
		model := ""
		if len(args) > 0 {
			if ms, ok := args[0].(string); ok {
				model = ms
			}
		}
		if model == "" || a.benchmarkScheduler == nil {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "模型名为空或调度器未启动"})
			return data, true, nil
		}
		go a.benchmarkScheduler.RunModelNow(model)
		data, _ := marshalResponse(map[string]interface{}{"success": true})
		return data, true, nil

	case "benchmark:models":
		mapping := a.settingsMgr.GetRelayModelMapping()
		seen := make(map[string]bool)
		out := []string{}
		for _, e := range mapping {
			if !e.Expose {
				continue
			}
			m := e.ClientModel
			if m == "" || seen[m] {
				continue
			}
			seen[m] = true
			out = append(out, m)
		}
		data, _ := marshalResponse(map[string]interface{}{"success": true, "models": out})
		return data, true, nil
	}

	return "", false, nil
}

func enabledText(enabled bool) string {
	if enabled {
		return "启用"
	}
	return "停用"
}
