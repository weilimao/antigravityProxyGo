package main

import (
	"encoding/json"
	"fmt"

	"antigravity-proxy/internal/pricing"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// app_ipc_invoke_pricing.go: IPCInvoke 的计费通道 —— pricing:ai-generate。
// 从 app_ipc.go IPCInvoke 分发链抽离为独立子处理器,与 handlePacketInvokeIPC 等
// 同构: 返回 (string, bool, error), 3-tuple marshalResponse, 未命中 fall-through。
//
// pricing:ai-generate 支持两种生成模式:
//  1. "model": 选定中继服务暴露的模型名, 经本地中继 /v1/chat/completions (sk-local-aigen 回环) 生成建议定价
//  2. "account": 选定 Antigravity 账号 id, 调 a.aiPricingGen.Generate 直连 daily-cloudcode-pa 生成建议定价
// 返回 {模型名: AIPriceResult{rate,grounded,estimated,anchorConflict,sources}} 供前端在确认弹窗里微调。
// 前端确认后再走 update-pricing-batch(单向 send) 批量落盘。
//
// 进度推送:驻 pricing:ai-progress channel, 套用 settings:migration-progress 模板
// (app_ipc_invoke_settings.go:165/173/202 + migrationController.ts:60-84)。
// progressFn 在阶段边界被调用, 经 EventsEmit 推送 {step, status} payload,
// 前端 aiPricingController 监听后渲染动态进度文案。
func (a *App) handlePricingInvokeIPC(channel string, args []interface{}) (string, bool, error) {
	marshalResponse := func(val interface{}) (string, bool, error) {
		b, err := json.Marshal(val)
		if err != nil {
			return `{"error":"JSON serialization error"}`, true, nil
		}
		return string(b), true, nil
	}

	getStringArg := func(idx int) string {
		if idx < len(args) {
			if s, ok := args[idx].(string); ok {
				return s
			}
		}
		return ""
	}

	getStringSliceArg := func(idx int) []string {
		if idx >= len(args) {
			return nil
		}
		arr, ok := args[idx].([]interface{})
		if !ok {
			return nil
		}
		out := make([]string, 0, len(arr))
		for _, v := range arr {
			if s, ok := v.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	}

	switch channel {
	case "pricing:ai-generate", "pricing:ai-generate-by-model":
		progressFn := func(step, status string) {
			wailsRuntime.EventsEmit(a.ctx, "pricing:ai-progress", map[string]string{
				"step":   step,
				"status": status,
			})
		}

		// 检查首个参数是否为选项字典 (新协议: { mode, model, accountId, candidates })
		if len(args) > 0 {
			if optMap, ok := args[0].(map[string]interface{}); ok {
				mode, _ := optMap["mode"].(string)
				var models []string
				if cands, ok := optMap["candidates"].([]interface{}); ok {
					for _, c := range cands {
						if s, ok := c.(string); ok && s != "" {
							models = append(models, s)
						}
					}
				}
				if len(models) == 0 {
					return marshalResponse(map[string]interface{}{"error": "候选模型列表为空"})
				}

				if mode == "model" {
					modelName, _ := optMap["model"].(string)
					if modelName == "" {
						return marshalResponse(map[string]interface{}{"error": "未选择生成用的模型"})
					}
					port := "18444"
					if a.settingsMgr != nil && a.settingsMgr.GetRelayPort() != "" {
						port = a.settingsMgr.GetRelayPort()
					}
					endpointPath := "/route/v1/chat/completions"
					if a.relayCompatAPIMgr != nil {
						endpointPath = a.relayCompatAPIMgr.ResolveBenchmarkEntryPath(modelName)
					}
					endpointURL := fmt.Sprintf("http://127.0.0.1:%s%s", port, endpointPath)
					rates, err := pricing.GenerateByModel(a.ctx, localRelayClient, endpointURL, modelName, models, progressFn)
					if err != nil {
						return marshalResponse(map[string]interface{}{"error": err.Error()})
					}
					return marshalResponse(rates)
				}

				// 默认为按账号生成
				accId, _ := optMap["accountId"].(string)
				if accId == "" {
					return marshalResponse(map[string]interface{}{"error": "未选择账号"})
				}
				if a.aiPricingGen == nil {
					return marshalResponse(map[string]interface{}{"error": "AI 定价生成器未就绪"})
				}
				accountModel, _ := optMap["model"].(string)
				rates, err := a.aiPricingGen.GenerateWithModel(models, accId, accountModel, progressFn)
				if err != nil {
					return marshalResponse(map[string]interface{}{"error": err.Error()})
				}
				return marshalResponse(rates)
			}
		}

		// 兼容独立专用通道 pricing:ai-generate-by-model (modelName, candidates)
		if channel == "pricing:ai-generate-by-model" {
			modelName := getStringArg(0)
			models := getStringSliceArg(1)
			if modelName == "" {
				return marshalResponse(map[string]interface{}{"error": "未选择生成用的模型"})
			}
			if len(models) == 0 {
				return marshalResponse(map[string]interface{}{"error": "候选模型列表为空"})
			}
			port := "18444"
			if a.settingsMgr != nil && a.settingsMgr.GetRelayPort() != "" {
				port = a.settingsMgr.GetRelayPort()
			}
			endpointPath := "/route/v1/chat/completions"
			if a.relayCompatAPIMgr != nil {
				endpointPath = a.relayCompatAPIMgr.ResolveBenchmarkEntryPath(modelName)
			}
			endpointURL := fmt.Sprintf("http://127.0.0.1:%s%s", port, endpointPath)
			rates, err := pricing.GenerateByModel(a.ctx, localRelayClient, endpointURL, modelName, models, progressFn)
			if err != nil {
				return marshalResponse(map[string]interface{}{"error": err.Error()})
			}
			return marshalResponse(rates)
		}

		// 兼容原有通道 pricing:ai-generate (accId, candidates)
		accId := getStringArg(0)
		models := getStringSliceArg(1)
		if accId == "" {
			return marshalResponse(map[string]interface{}{"error": "未选择账号"})
		}
		if len(models) == 0 {
			return marshalResponse(map[string]interface{}{"error": "候选模型列表为空"})
		}
		if a.aiPricingGen == nil {
			return marshalResponse(map[string]interface{}{"error": "AI 定价生成器未就绪"})
		}
		rates, err := a.aiPricingGen.Generate(models, accId, progressFn)
		if err != nil {
			return marshalResponse(map[string]interface{}{"error": err.Error()})
		}
		return marshalResponse(rates)
	}

	return "", false, nil
}
