package relay

import (
	"bytes"
	"encoding/json"
)

// PatchAnthropicMessageStart 嗅探并修饰 Anthropic 原生 SSE 响应流首帧 message_start:
// 当上游(第三方 Anthropic 代理/镜像)返回的 message_start 事件中 input_tokens 缺失或为 0 时,
// 提取入站请求体 reqBody 调用 EnsureInputTokens 进行本地字符估算并补齐, 确保 Claude Code CLI
// 在流首进行中即可展示非零 ↑(上传 Token)。若上游已返回非零 input_tokens, 则保持原样不变。
func PatchAnthropicMessageStart(sseChunk []byte, reqBody []byte) []byte {
	if len(sseChunk) == 0 {
		return sseChunk
	}

	// 快速判断: 是否包含 "message_start" 关键字
	if !bytes.Contains(sseChunk, []byte("message_start")) {
		return sseChunk
	}

	// 查找 data: 行
	lines := bytes.Split(sseChunk, []byte("\n"))
	var patchedLines [][]byte
	modified := false

	for _, line := range lines {
		trimmed := bytes.TrimSpace(line)
		if bytes.HasPrefix(trimmed, []byte("data:")) {
			dataJSON := bytes.TrimSpace(trimmed[5:])
			if len(dataJSON) > 0 && dataJSON[0] == '{' {
				var msgObj map[string]interface{}
				if err := json.Unmarshal(dataJSON, &msgObj); err == nil {
					if msgType, ok := msgObj["type"].(string); ok && msgType == "message_start" {
						if message, ok := msgObj["message"].(map[string]interface{}); ok {
							usage, _ := message["usage"].(map[string]interface{})
							if usage == nil {
								usage = make(map[string]interface{})
							}

							inputTokens := 0
							if v, exists := usage["input_tokens"]; exists {
								if num, ok := v.(float64); ok {
									inputTokens = int(num)
								}
							}

							// 如果 input_tokens <= 0, 则估算并补全
							if inputTokens <= 0 {
								estimated := EnsureInputTokens(0, reqBody)
								usage["input_tokens"] = estimated

								if _, ok := usage["output_tokens"]; !ok {
									usage["output_tokens"] = 1
								}
								if _, ok := usage["cache_creation_input_tokens"]; !ok {
									usage["cache_creation_input_tokens"] = 0
								}
								if _, ok := usage["cache_read_input_tokens"]; !ok {
									usage["cache_read_input_tokens"] = 0
								}

								message["usage"] = usage
								msgObj["message"] = message

								if newJSON, err := json.Marshal(msgObj); err == nil {
									patchedLine := append([]byte("data: "), newJSON...)
									patchedLines = append(patchedLines, patchedLine)
									modified = true
									continue
								}
							}
						}
					}
				}
			}
		}
		patchedLines = append(patchedLines, line)
	}

	if modified {
		return bytes.Join(patchedLines, []byte("\n"))
	}
	return sseChunk
}
