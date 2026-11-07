package proxy

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// cloudaicompanionProjectID 从 loadCodeAssist 响应里取出账号专属项目。
// 上游可能返回字符串，也可能返回带 id 的对象。
func cloudaicompanionProjectID(raw []byte) string {
	var resp struct {
		Project interface{} `json:"cloudaicompanionProject"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &resp) != nil || resp.Project == nil {
		return ""
	}
	switch v := resp.Project.(type) {
	case string:
		return strings.TrimSpace(v)
	case map[string]interface{}:
		if id, ok := v["id"].(string); ok {
			return strings.TrimSpace(id)
		}
	}
	return ""
}

// replaceTopLevelJSONString 只替换 JSON 对象顶层字符串字段的值。
// 字段已存在时，其余字节保持原样；字段缺失时，插在开头的 '{' 之后。
// 顶层值不是字符串，或正文不是 JSON 对象时返回 false。
func replaceTopLevelJSONString(body []byte, field, value string) ([]byte, bool) {
	i := skipJSONWS(body, 0)
	if i >= len(body) || body[i] != '{' {
		return nil, false
	}
	i++
	for {
		i = skipJSONWS(body, i)
		if i >= len(body) {
			return nil, false
		}
		if body[i] == '}' {
			return insertTopLevelJSONString(body, field, value), true
		}
		key, next, ok := scanJSONString(body, i)
		if !ok {
			return nil, false
		}
		next = skipJSONWS(body, next)
		if next >= len(body) || body[next] != ':' {
			return nil, false
		}
		next = skipJSONWS(body, next+1)
		if key == field {
			if next >= len(body) || body[next] != '"' {
				return nil, false
			}
			_, valEnd, ok := scanJSONString(body, next)
			if !ok {
				return nil, false
			}
			quoted := []byte(strconv.Quote(value))
			out := make([]byte, 0, len(body)-(valEnd-next)+len(quoted))
			out = append(out, body[:next]...)
			out = append(out, quoted...)
			out = append(out, body[valEnd:]...)
			return out, true
		}
		next, ok = skipJSONValue(body, next)
		if !ok {
			return nil, false
		}
		next = skipJSONWS(body, next)
		if next < len(body) && body[next] == ',' {
			i = next + 1
			continue
		}
		if next < len(body) && body[next] == '}' {
			return insertTopLevelJSONString(body, field, value), true
		}
		return nil, false
	}
}

func insertTopLevelJSONString(body []byte, field, value string) []byte {
	i := skipJSONWS(body, 0)
	rest := skipJSONWS(body, i+1)
	piece := `"` + field + `":` + strconv.Quote(value)
	if rest < len(body) && body[rest] != '}' {
		piece += ","
	}
	out := make([]byte, 0, len(body)+len(piece))
	out = append(out, body[:i+1]...)
	out = append(out, piece...)
	out = append(out, body[i+1:]...)
	return out
}

func skipJSONWS(b []byte, i int) int {
	for i < len(b) {
		switch b[i] {
		case ' ', '\n', '\r', '\t':
			i++
		default:
			return i
		}
	}
	return i
}

func scanJSONString(b []byte, i int) (string, int, bool) {
	if i >= len(b) || b[i] != '"' {
		return "", i, false
	}
	j := i + 1
	for j < len(b) {
		if b[j] == '\\' {
			if j+1 >= len(b) {
				return "", i, false
			}
			j += 2
			continue
		}
		if b[j] == '"' {
			j++
			var s string
			if err := json.Unmarshal(b[i:j], &s); err != nil {
				return "", i, false
			}
			return s, j, true
		}
		j++
	}
	return "", i, false
}

func skipJSONValue(b []byte, i int) (int, bool) {
	i = skipJSONWS(b, i)
	if i >= len(b) {
		return i, false
	}
	switch b[i] {
	case '"':
		_, end, ok := scanJSONString(b, i)
		return end, ok
	case '{', '[':
		return skipJSONContainer(b, i)
	case 't':
		if bytes.HasPrefix(b[i:], []byte("true")) {
			return i + 4, true
		}
	case 'f':
		if bytes.HasPrefix(b[i:], []byte("false")) {
			return i + 5, true
		}
	case 'n':
		if bytes.HasPrefix(b[i:], []byte("null")) {
			return i + 4, true
		}
	default:
		j := i
		if b[j] == '-' {
			j++
		}
		start := j
		for j < len(b) {
			c := b[j]
			if (c >= '0' && c <= '9') || c == '+' || c == '-' || c == '.' || c == 'e' || c == 'E' {
				j++
				continue
			}
			break
		}
		if j > start {
			return j, true
		}
	}
	return i, false
}

func skipJSONContainer(b []byte, i int) (int, bool) {
	stack := []byte{b[i]}
	i++
	for i < len(b) && len(stack) > 0 {
		c := b[i]
		if c == '"' {
			_, end, ok := scanJSONString(b, i)
			if !ok {
				return i, false
			}
			i = end
			continue
		}
		switch c {
		case '{', '[':
			stack = append(stack, c)
		case '}':
			if stack[len(stack)-1] != '{' {
				return i, false
			}
			stack = stack[:len(stack)-1]
		case ']':
			if stack[len(stack)-1] != '[' {
				return i, false
			}
			stack = stack[:len(stack)-1]
		}
		i++
	}
	return i, len(stack) == 0
}
