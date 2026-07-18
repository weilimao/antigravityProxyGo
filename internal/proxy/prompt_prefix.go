package proxy

import (
	"encoding/json"
)

// injectPromptPrefix 负责解析请求载荷，并将自定义提示词前缀插入到最新一条 user 消息的前端
func injectPromptPrefix(bodyBytes []byte, prefix string) []byte {
	if prefix == "" || len(bodyBytes) == 0 {
		return bodyBytes
	}

	var doc map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &doc); err != nil {
		return bodyBytes // 非 JSON 或者解析失败，原样返回（保障鲁棒性）
	}

	var contentsObj interface{}
	var hasContents bool

	// 1. 尝试解析嵌套 4 层的 v1internal 结构 (request.contents)
	if reqObj, ok := doc["request"].(map[string]interface{}); ok {
		if contentsObj, hasContents = reqObj["contents"]; hasContents {
			modifiedContents := modifyContentsArray(contentsObj, prefix)
			if modifiedContents != nil {
				reqObj["contents"] = modifiedContents
				doc["request"] = reqObj
				if newBytes, err := json.Marshal(doc); err == nil {
					return newBytes
				}
			}
		}
	} else if contentsObj, hasContents = doc["contents"]; hasContents {
		// 2. 尝试解析嵌套 3 层的官方标准结构 (contents)
		modifiedContents := modifyContentsArray(contentsObj, prefix)
		if modifiedContents != nil {
			doc["contents"] = modifiedContents
			if newBytes, err := json.Marshal(doc); err == nil {
				return newBytes
			}
		}
	}

	return bodyBytes
}

// modifyContentsArray 迭代 contents 数组，定位最后一个 user 的 text part 并执行前缀拼接
func modifyContentsArray(contentsObj interface{}, prefix string) []interface{} {
	contentsSlice, ok := contentsObj.([]interface{})
	if !ok || len(contentsSlice) == 0 {
		return nil
	}

	// 从后往前寻找第一个 role 为 "user" 的元素，或者没有 role 字段的元素（默认也是 user 消息）
	var targetIdx = -1
	for i := len(contentsSlice) - 1; i >= 0; i-- {
		cMap, ok := contentsSlice[i].(map[string]interface{})
		if !ok {
			continue
		}
		role, _ := cMap["role"].(string)
		if role == "" || role == "user" {
			targetIdx = i
			break
		}
	}

	// 如果没有找到 user 消息，默认使用最后一个元素
	if targetIdx == -1 {
		targetIdx = len(contentsSlice) - 1
	}

	cMap, ok := contentsSlice[targetIdx].(map[string]interface{})
	if !ok {
		return nil
	}

	partsObj, hasParts := cMap["parts"]
	if !hasParts {
		return nil
	}

	partsSlice, ok := partsObj.([]interface{})
	if !ok || len(partsSlice) == 0 {
		return nil
	}

	// 找到 parts 里的第一个含有 text 属性的 part，并拼接前缀
	var modified = false
	for i := 0; i < len(partsSlice); i++ {
		pMap, ok := partsSlice[i].(map[string]interface{})
		if !ok {
			continue
		}
		
		if textVal, ok := pMap["text"].(string); ok {
			pMap["text"] = prefix + textVal
			partsSlice[i] = pMap
			modified = true
			break
		}
	}

	// 如果 parts 里全不是 text 类型，我们在 parts 的最前面插入一个含有 text 的 part
	if !modified {
		newPart := map[string]interface{}{
			"text": prefix,
		}
		partsSlice = append([]interface{}{newPart}, partsSlice...)
	}

	cMap["parts"] = partsSlice
	contentsSlice[targetIdx] = cMap
	return contentsSlice
}
