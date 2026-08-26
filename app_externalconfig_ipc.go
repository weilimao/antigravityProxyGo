package main

import (
	"encoding/json"
	"fmt"

	"antigravity-proxy/internal/externalconfig"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// app_externalconfig_ipc.go: 外部 Agent 配置管理的 IPC 处理器。
// 通道前缀 externalconfig:*，后端只做文件 I/O，不感知业务结构。
//
// IPCSend(单向事件下发):
//   externalconfig:get-agents    → emit externalconfig:agents-res (Agent 列表)
//   externalconfig:get-config    → emit externalconfig:config-res  (JSON 字符串)
//
// IPCInvoke(双向调用):
//   externalconfig:save-config         → {success, error?}
//   externalconfig:reload-config       → {success, jsonStr?, error?}
//   externalconfig:restore-backup      → {success, error?}
//   externalconfig:get-model-catalog   → {success, models: [{slug, display_name}], error?}
//   externalconfig:get-catalog-full    → {success, jsonStr, error?}
//   externalconfig:save-catalog-full   → {success, error?}

// handleExternalConfigIPCSend 处理单向 IPC 事件。
func (a *App) handleExternalConfigIPCSend(channel string, args []interface{}) bool {
	getStringArg := func(idx int) string {
		if idx < len(args) {
			if s, ok := args[idx].(string); ok {
				return s
			}
		}
		return ""
	}

	switch channel {
	case "externalconfig:get-agents":
		agents := a.externalConfigMgr.ListAgents()
		wailsRuntime.EventsEmit(a.ctx, "externalconfig:agents-res", agents)
		return true

	case "externalconfig:get-config":
		agentID := getStringArg(0)
		jsonStr, err := a.externalConfigMgr.ReadConfig(agentID)
		if err != nil {
			a.AddLog(fmt.Sprintf("❌ [Agent配置] 读取 %s 配置失败: %v", agentID, err))
			wailsRuntime.EventsEmit(a.ctx, "externalconfig:config-res", map[string]interface{}{
				"agentId": agentID,
				"success": false,
				"error":   err.Error(),
				"jsonStr": "{}",
			})
			return true
		}
		wailsRuntime.EventsEmit(a.ctx, "externalconfig:config-res", map[string]interface{}{
			"agentId": agentID,
			"success": true,
			"jsonStr": jsonStr,
		})
		return true
	}
	return false
}

// handleExternalConfigInvokeIPC 处理双向 IPC 调用。
func (a *App) handleExternalConfigInvokeIPC(channel string, args []interface{}) (string, bool, error) {
	marshalResponse := func(val interface{}) (string, bool, error) {
		b, err := json.Marshal(val)
		if err != nil {
			return `{"success":false,"error":"JSON serialization error"}`, true, nil
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
	case "externalconfig:save-config":
		agentID := getStringArg(0)
		jsonStr := getStringArg(1)
		if agentID == "" {
			return marshalResponse(map[string]interface{}{"success": false, "error": "agentId is required"})
		}
		if err := a.externalConfigMgr.WriteConfig(agentID, jsonStr); err != nil {
			a.AddLog(fmt.Sprintf("❌ [Agent配置] 保存 %s 配置失败: %v", agentID, err))
			return marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
		}
		a.AddLog(fmt.Sprintf("✅ [Agent配置] %s 配置已保存", agentID))
		return marshalResponse(map[string]interface{}{"success": true})

	case "externalconfig:reload-config":
		agentID := getStringArg(0)
		jsonStr, err := a.externalConfigMgr.ReloadConfig(agentID)
		if err != nil {
			return marshalResponse(map[string]interface{}{
				"success": false,
				"error":   err.Error(),
			})
		}
		return marshalResponse(map[string]interface{}{
			"success": true,
			"jsonStr": jsonStr,
		})

	case "externalconfig:restore-backup":
		agentID := getStringArg(0)
		if err := a.externalConfigMgr.RestoreBackup(agentID); err != nil {
			a.AddLog(fmt.Sprintf("❌ [Agent配置] 恢复 %s 备份失败: %v", agentID, err))
			return marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
		}
		a.AddLog(fmt.Sprintf("✅ [Agent配置] %s 配置已从备份恢复", agentID))
		return marshalResponse(map[string]interface{}{"success": true})

	case "externalconfig:get-model-catalog":
		agentID := getStringArg(0)
		catalog, err := a.externalConfigMgr.ReadModelCatalog(agentID)
		if err != nil {
			return marshalResponse(map[string]interface{}{"success": false, "models": []interface{}{}, "error": err.Error()})
		}
		return marshalResponse(map[string]interface{}{"success": true, "models": catalog})

	case "externalconfig:get-catalog-full":
		agentID := getStringArg(0)
		jsonStr, err := a.externalConfigMgr.ReadModelCatalogFull(agentID)
		if err != nil {
			return marshalResponse(map[string]interface{}{"success": false, "jsonStr": "{}", "error": err.Error()})
		}
		return marshalResponse(map[string]interface{}{"success": true, "jsonStr": jsonStr})

	case "externalconfig:save-catalog-full":
		agentID := getStringArg(0)
		jsonStr := getStringArg(1)
		if agentID == "" {
			return marshalResponse(map[string]interface{}{"success": false, "error": "agentId is required"})
		}
		if err := a.externalConfigMgr.WriteModelCatalogFull(agentID, jsonStr); err != nil {
			a.AddLog(fmt.Sprintf("❌ [Agent配置] 保存 %s 模型 catalog 失败: %v", agentID, err))
			return marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
		}
		a.AddLog(fmt.Sprintf("✅ [Agent配置] %s 模型 catalog 已保存", agentID))
		return marshalResponse(map[string]interface{}{"success": true})

	case "externalconfig:ai-generate-provider":
		// OpenCode provider AI 一键生成: 入参 (model, systemPrompt, userInput, models?),
		// 调本地中继 /v1/chat/completions 让 AI 输出 provider.{name} JSON 片段。
		// models(可选)为用户在前端多选的真实模型列表, 注入 user prompt 强约束 AI 只为
		// 这些模型生成条目, 杜绝 AI 编造已下架/不存在的模型 id。
		// 实现见 app_opencode_aigen.go, 此处只做参数提取与结果包装。
		model := getStringArg(0)
		systemPrompt := getStringArg(1)
		userInput := getStringArg(2)
		selectedModels := getStringSliceArg(3)
		content, err := a.generateOpenCodeProvider(model, systemPrompt, userInput, selectedModels)
		if err != nil {
			a.AddLog(fmt.Sprintf("❌ [Agent配置] AI 生成 provider 失败: %v", err))
			return marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
		}
		return marshalResponse(map[string]interface{}{"success": true, "content": content})

	case "externalconfig:get-top-models":
		// 中继调用量 Top 模型统计(跨用户聚合, 按请求次数降序),
		// 供 AI 生成弹窗默认预选调用量前十的模型。
		return marshalResponse(map[string]interface{}{"success": true, "models": a.getTopRelayModels()})
	}

	return "", false, nil
}

// ensure externalconfig.Manager is referenced for compile-time type check
var _ *externalconfig.Manager = nil
