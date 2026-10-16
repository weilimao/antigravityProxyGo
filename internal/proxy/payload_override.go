package proxy

import (
	"encoding/json"
	"strings"
)

// replaceModelString 保证替换前后 "models/" 前缀形态一致性
func replaceModelString(oldVal, targetModel string) string {
	cleanTarget := strings.TrimPrefix(targetModel, "models/")
	if strings.HasPrefix(oldVal, "models/") {
		return "models/" + cleanTarget
	}
	return cleanTarget
}

// RewriteModelInPayload 检查并改写请求体 JSON 中的 model 字段。
// 无论请求是根层级的标准格式还是嵌套在 request 中的包装格式，
// 均能在保留其他全部业务参数的前提下安全替换模型名。
// 若输入为空、非 JSON 格式或无需改动，则安全返回原始切片与 false。
func RewriteModelInPayload(bodyBytes []byte, targetModel string) ([]byte, bool) {
	if len(bodyBytes) == 0 {
		return bodyBytes, false
	}
	targetModel = strings.TrimSpace(targetModel)
	if targetModel == "" || targetModel == "unknown" {
		return bodyBytes, false
	}

	var bodyMap map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &bodyMap); err != nil {
		return bodyBytes, false
	}

	modified := false

	// 1. 检查根节点的 model 字段 (如 v1internal 包装格式或标准 Gemini 格式)
	if modelVal, exists := bodyMap["model"].(string); exists {
		newModelVal := replaceModelString(modelVal, targetModel)
		if newModelVal != modelVal {
			bodyMap["model"] = newModelVal
			modified = true
		}
	}

	// 2. 检查嵌套在 request 内部的 model 字段
	if reqMap, ok := bodyMap["request"].(map[string]interface{}); ok {
		if innerModelVal, exists := reqMap["model"].(string); exists {
			newModelVal := replaceModelString(innerModelVal, targetModel)
			if newModelVal != innerModelVal {
				reqMap["model"] = newModelVal
				modified = true
			}
		}
	}

	if !modified {
		return bodyBytes, false
	}

	newBytes, err := json.Marshal(bodyMap)
	if err != nil {
		return bodyBytes, false
	}
	return newBytes, true
}
