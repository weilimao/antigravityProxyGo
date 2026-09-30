package stats

import (
	"encoding/json"
	"fmt"
)

// TruncateRequestBody structure and string truncation to keep IPC payloads bounded and prevent OOM.

const (
	maxRawStringUnmarshalLen = 32 * 1024 // 超过 32KB 的原始字符串不执行完整 JSON 反序列化，直接做轻量首尾截断
	maxStringFieldLen        = 500       // 单个字符串字段保留上限
	maxArrayItems            = 8         // 数组最多保留项数（防长对话历史膨胀）
	maxObjectDepth           = 4         // 最大递归深度，防深度嵌套对象栈溢出与深克隆
)

// TruncateRequestBody structure and string truncation to prevent OOM
func TruncateRequestBody(body interface{}) interface{} {
	if body == nil {
		return nil
	}

	switch val := body.(type) {
	case string:
		if len(val) > maxRawStringUnmarshalLen {
			// 大报文避免反序列化为巨大对象树造成大量 GC 堆内存分配
			return val[:1500] + fmt.Sprintf("\n... [大报文已截断，原字符数: %d] ...\n", len(val)) + val[len(val)-500:]
		}

		var parsed interface{}
		if err := json.Unmarshal([]byte(val), &parsed); err == nil {
			return processObject(parsed, 0)
		}
		if len(val) > 1000 {
			return val[:400] + fmt.Sprintf("\n... [已截断，原字符数: %d] ...\n", len(val)) + val[len(val)-200:]
		}
		return val
	default:
		return processObject(body, 0)
	}
}

func processObject(item interface{}, depth int) interface{} {
	if item == nil {
		return nil
	}
	if depth > maxObjectDepth {
		return "... [层级过深，已截断] ..."
	}

	switch v := item.(type) {
	case map[string]interface{}:
		newMap := make(map[string]interface{})
		count := 0
		for k, val := range v {
			count++
			if count > 30 {
				newMap["_more_fields"] = fmt.Sprintf("... [其余 %d 个字段已省略] ...", len(v)-30)
				break
			}
			if str, ok := val.(string); ok {
				if len(str) > maxStringFieldLen {
					newMap[k] = str[:250] + fmt.Sprintf("... [已截断，原长度: %d 字符] ...", len(str)) + str[len(str)-100:]
				} else {
					newMap[k] = str
				}
			} else {
				newMap[k] = processObject(val, depth+1)
			}
		}
		return newMap
	case []interface{}:
		if len(v) > maxArrayItems {
			// 长消息历史数组截断：保留前 3 条 + 省略提示 + 后 3 条
			newSlice := make([]interface{}, 0, 7)
			for i := 0; i < 3; i++ {
				newSlice = append(newSlice, processObject(v[i], depth+1))
			}
			newSlice = append(newSlice, fmt.Sprintf("... [已省略中间 %d 项] ...", len(v)-6))
			for i := len(v) - 3; i < len(v); i++ {
				newSlice = append(newSlice, processObject(v[i], depth+1))
			}
			return newSlice
		}
		newSlice := make([]interface{}, len(v))
		for i, val := range v {
			newSlice[i] = processObject(val, depth+1)
		}
		return newSlice
	case string:
		if len(v) > maxStringFieldLen {
			return v[:250] + fmt.Sprintf("... [已截断，原长度: %d 字符] ...", len(v)) + v[len(v)-100:]
		}
		return v
	default:
		return v
	}
}
