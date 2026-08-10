package main

import (
	"encoding/json"
)

// app_ipc_invoke_pricing.go: IPCInvoke 的计费通道 —— pricing:ai-generate。
// 从 app_ipc.go IPCInvoke 分发链抽离为独立子处理器,与 handlePacketInvokeIPC 等
// 同构: 返回 (string, bool, error), 3-tuple marshalResponse, 未命中 fall-through。
//
// pricing:ai-generate: 前端把"模型统计里出现、但尚未登记单价的候选模型基名[]"连同
// 用户选定 Antigravity 账号 id 传过来, 调 a.aiPricingGen.Generate 直连 daily-cloudcode-pa
// 经 gemini-2.5-flash-lite 生成建议单价, 返回 {模型名: {input,output,cached}} 供前端
// 在确认弹窗里微调。前端确认后再走 update-pricing-batch(单向 send) 批量落盘。
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
		rates, err := a.aiPricingGen.Generate(models, accId)
		if err != nil {
			return marshalResponse(map[string]interface{}{"error": err.Error()})
		}
		return marshalResponse(rates)
	}

	return "", false, nil
}
