package stats

import (
	"encoding/json"
	"fmt"
)

// TruncateRequestBody structure and string truncation to keep IPC payloads bounded.


// TruncateRequestBody structure and string truncation to prevent OOM
func TruncateRequestBody(body interface{}) interface{} {
	if body == nil {
		return nil
	}

	switch val := body.(type) {
	case string:
		var parsed interface{}
		if err := json.Unmarshal([]byte(val), &parsed); err == nil {
			return processObject(parsed)
		}
		if len(val) > 1000 {
			return val[:400] + fmt.Sprintf("\n... [已截断，原字符数: %d] ...\n", len(val)) + val[len(val)-200:]
		}
		return val
	default:
		return processObject(body)
	}
}

func processObject(item interface{}) interface{} {
	if item == nil {
		return nil
	}

	switch v := item.(type) {
	case map[string]interface{}:
		newMap := make(map[string]interface{})
		for k, val := range v {
			if str, ok := val.(string); ok {
				if len(str) > 1000 {
					newMap[k] = str[:400] + fmt.Sprintf("... [已截断，原长度: %d 字符] ...", len(str)) + str[len(str)-100:]
				} else {
					newMap[k] = str
				}
			} else {
				newMap[k] = processObject(val)
			}
		}
		return newMap
	case []interface{}:
		newSlice := make([]interface{}, len(v))
		for i, val := range v {
			newSlice[i] = processObject(val)
		}
		return newSlice
	default:
		return v
	}
}
