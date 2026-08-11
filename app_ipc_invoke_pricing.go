package main

import (
	"encoding/json"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// app_ipc_invoke_pricing.go: IPCInvoke 的计费通道 —— pricing:ai-generate。
// 从 app_ipc.go IPCInvoke 分发链抽离为独立子处理器,与 handlePacketInvokeIPC 等
// 同构: 返回 (string, bool, error), 3-tuple marshalResponse, 未命中 fall-through。
//
// pricing:ai-generate: 前端把"模型统计里出现、但尚未登记单价的候选模型基名[]"连同
// 用户选定 Antigravity 账号 id 传过来, 调 a.aiPricingGen.Generate 直连 daily-cloudcode-pa
// 经 gemini-2.5-flash-lite 生成建议单价, 返回 {模型名: AIPriceResult{rate,grounded,
// estimated,anchorConflict,sources}} 供前端在确认弹窗里微调(并标估算/未联网/锚点冲突
// 警示 + 来源 URL)。前端确认后再走 update-pricing-batch(单向 send) 批量落盘。
//
// 进度推送:驻 pricing:ai-progress channel, 套用 settings:migration-progress 模板
// (app_ipc_invoke_settings.go:165/173/202 + migrationController.ts:60-84)。
// progressFn 在 Generate 的 6 阶段边界被调用(fetch-token / grounding-search /
// grounding-degraded / token-refresh / parse-result / done / error), 经 EventsEmit
// 推送 {step, status} payload, 前端 aiPricingController 监听后渲染动态进度文案。
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
	case "pricing:ai-generate":
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
		// 进度闭包:把 Generate 内部 6 个阶段边界桥接到前端 pricing:ai-progress channel。
		// 套用 settings:migration-progress 同款 {step, status} payload 协议,前端
		// aiPricingController 按 step 键映射 i18n 文案渲染动态进度。
		progressFn := func(step, status string) {
			wailsRuntime.EventsEmit(a.ctx, "pricing:ai-progress", map[string]string{
				"step":   step,
				"status": status,
			})
		}
		rates, err := a.aiPricingGen.Generate(models, accId, progressFn)
		if err != nil {
			return marshalResponse(map[string]interface{}{"error": err.Error()})
		}
		return marshalResponse(rates)
	}

	return "", false, nil
}
