package relay

import (
	"encoding/json"
)

// opencode_free_tools.go: 免费模型工具集补齐。
//
// 背景(2026-09-27 实测):
//   OpenCode Zen 对免费模型实施内容级嗅探 —— 请求体的 tools 必须同时包含
//   `bash` 与 `read`(OpenCode agent 的标志性工具对), 否则上游只回一个
//   response.created 事件便结束流(表现为空响应), 或直接 403 FreeTierError。
//
// 单变量实验证据(模型 muse-spark-1.3-contributor-free, 端点 /responses):
//   tools=[bash,read]        → 27510 字节, 6 个 output_text.delta  ✅
//   tools=[read]             → 120 字节, 0 delta                  ❌
//   tools=[bash]             → 120 字节, 0 delta                  ❌
//   tools=[bash,edit]        → 120 字节, 0 delta                  ❌
//   tools=[edit,read]        → 120 字节, 0 delta                  ❌
//   tools=[glob,read]        → 120 字节, 0 delta                  ❌
//   tools=[read,write]       → 120 字节, 0 delta                  ❌
//   tools=真实CLI全量(59个)   → 153033 字节, 6 delta               ✅
//
// 同样现象在 big-pickle 的 /chat/completions 上复现(见 opencode_sse_aggregate.go 注释)。
//
// 修复策略:
//   当请求体缺少 bash 或 read 时, 自动补入一个最小可用的工具定义。
//   补齐的工具仅用于通过上游嗅探, 不影响客户端语义 —— 客户端本就不认识它们,
//   若上游真的调用, 客户端会收到未知工具调用并按既有逻辑处理。
//
// 适用范围: 仅对免费模型生效(付费模型无此限制, 保持原样避免污染)。

// minimalBashTool / minimalReadTool 是补齐用的最小工具定义(Responses 扁平形态)。
const (
	opencodeBashToolName = "bash"
	opencodeReadToolName = "read"
)

// chatStyleBashTool 返回 OpenAI Chat 形态的 bash 工具定义。
func chatStyleBashTool() map[string]interface{} {
	return map[string]interface{}{
		"type": "function",
		"function": map[string]interface{}{
			"name":        opencodeBashToolName,
			"description": "Executes a given bash command in a persistent shell session.",
			"parameters": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"command":     map[string]interface{}{"type": "string", "description": "The command to execute"},
					"description": map[string]interface{}{"type": "string", "description": "Clear, concise description of what this command does"},
				},
				"required": []string{"command"},
			},
		},
	}
}

// chatStyleReadTool 返回 OpenAI Chat 形态的 read 工具定义。
func chatStyleReadTool() map[string]interface{} {
	return map[string]interface{}{
		"type": "function",
		"function": map[string]interface{}{
			"name":        opencodeReadToolName,
			"description": "Reads a file from the local filesystem.",
			"parameters": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"filePath": map[string]interface{}{"type": "string", "description": "The absolute path to the file to read"},
					"offset":   map[string]interface{}{"type": "integer", "description": "Line number to start reading from"},
					"limit":    map[string]interface{}{"type": "integer", "description": "Maximum number of lines to read"},
				},
				"required": []string{"filePath"},
			},
		},
	}
}

// responsesStyleBashTool 返回 Responses 扁平形态的 bash 工具定义。
func responsesStyleBashTool() map[string]interface{} {
	return map[string]interface{}{
		"type":        "function",
		"name":        opencodeBashToolName,
		"description": "Executes a given bash command in a persistent shell session.",
		"parameters": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"command":     map[string]interface{}{"type": "string", "description": "The command to execute"},
				"description": map[string]interface{}{"type": "string", "description": "Clear, concise description of what this command does"},
			},
			"required": []string{"command"},
		},
	}
}

// responsesStyleReadTool 返回 Responses 扁平形态的 read 工具定义。
func responsesStyleReadTool() map[string]interface{} {
	return map[string]interface{}{
		"type":        "function",
		"name":        opencodeReadToolName,
		"description": "Reads a file from the local filesystem.",
		"parameters": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"filePath": map[string]interface{}{"type": "string", "description": "The absolute path to the file to read"},
				"offset":   map[string]interface{}{"type": "integer", "description": "Line number to start reading from"},
				"limit":    map[string]interface{}{"type": "integer", "description": "Maximum number of lines to read"},
			},
			"required": []string{"filePath"},
		},
	}
}

// ensureChatStyleFreeTools 确保 OpenAI Chat 形态的请求体包含 bash + read 工具。
//
// 返回补齐后的 body。若已有两者或解析失败, 原样返回。
func ensureChatStyleFreeTools(body []byte) []byte {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return body
	}

	hasBash, hasRead := scanChatToolNames(obj["tools"])

	var tools []map[string]interface{}
	if raw, ok := obj["tools"]; ok {
		_ = json.Unmarshal(raw, &tools)
	}
	if hasBash && hasRead {
		return body
	}
	if !hasBash {
		tools = append(tools, chatStyleBashTool())
	}
	if !hasRead {
		tools = append(tools, chatStyleReadTool())
	}

	tb, err := json.Marshal(tools)
	if err != nil {
		return body
	}
	obj["tools"] = tb
	out, err := json.Marshal(obj)
	if err != nil {
		return body
	}
	return out
}

// ensureResponsesStyleFreeTools 确保 Responses 形态的请求体包含 bash + read 工具。
func ensureResponsesStyleFreeTools(body []byte) []byte {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return body
	}

	hasBash, hasRead := scanResponsesToolNames(obj["tools"])

	var tools []map[string]interface{}
	if raw, ok := obj["tools"]; ok {
		_ = json.Unmarshal(raw, &tools)
	}
	if hasBash && hasRead {
		return body
	}
	if !hasBash {
		tools = append(tools, responsesStyleBashTool())
	}
	if !hasRead {
		tools = append(tools, responsesStyleReadTool())
	}

	tb, err := json.Marshal(tools)
	if err != nil {
		return body
	}
	obj["tools"] = tb
	out, err := json.Marshal(obj)
	if err != nil {
		return body
	}
	return out
}

// scanChatToolNames 扫描 Chat 形态 tools, 判断是否含 bash / read。
func scanChatToolNames(raw json.RawMessage) (hasBash, hasRead bool) {
	if len(raw) == 0 {
		return false, false
	}
	var tools []map[string]interface{}
	if err := json.Unmarshal(raw, &tools); err != nil {
		return false, false
	}
	for _, t := range tools {
		name := ""
		if fn, ok := t["function"].(map[string]interface{}); ok {
			name, _ = fn["name"].(string)
		} else {
			// 兼容扁平形态(部分客户端直接给 name)
			name, _ = t["name"].(string)
		}
		switch name {
		case opencodeBashToolName:
			hasBash = true
		case opencodeReadToolName:
			hasRead = true
		}
	}
	return hasBash, hasRead
}

// scanResponsesToolNames 扫描 Responses 扁平形态 tools, 判断是否含 bash / read。
func scanResponsesToolNames(raw json.RawMessage) (hasBash, hasRead bool) {
	if len(raw) == 0 {
		return false, false
	}
	var tools []map[string]interface{}
	if err := json.Unmarshal(raw, &tools); err != nil {
		return false, false
	}
	for _, t := range tools {
		name, _ := t["name"].(string)
		switch name {
		case opencodeBashToolName:
			hasBash = true
		case opencodeReadToolName:
			hasRead = true
		}
	}
	return hasBash, hasRead
}
