package relay

import (
	"encoding/json"
	"strings"
	"testing"
)

// opencode_free_tools_test.go: 免费模型工具集补齐用例。
//
// 背景(2026-09-27 实测):
//   Zen 对免费模型做内容级嗅探, tools 必须同时含 bash 与 read, 否则只回
//   response.created 便结束流(空响应)。单变量实验证明:
//     [bash,read] → 正常;  [read]/[bash]/[bash,edit]/[glob,read]/[read,write] → 空流。

// TestEnsureChatStyleFreeTools 验证 Chat 形态的工具补齐。
func TestEnsureChatStyleFreeTools(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantBash   bool
		wantRead   bool
		wantInject bool
	}{
		{
			name:       "无 tools 字段",
			body:       `{"model":"m","messages":[]}`,
			wantBash:   true,
			wantRead:   true,
			wantInject: true,
		},
		{
			name:       "空 tools",
			body:       `{"model":"m","tools":[]}`,
			wantBash:   true,
			wantRead:   true,
			wantInject: true,
		},
		{
			name:       "只有 read",
			body:       `{"model":"m","tools":[{"type":"function","function":{"name":"read","parameters":{}}}]}`,
			wantBash:   true,
			wantRead:   true,
			wantInject: true,
		},
		{
			name:       "只有 bash",
			body:       `{"model":"m","tools":[{"type":"function","function":{"name":"bash","parameters":{}}}]}`,
			wantBash:   true,
			wantRead:   true,
			wantInject: true,
		},
		{
			name:       "已有 bash+read（不应重复注入）",
			body:       `{"model":"m","tools":[{"type":"function","function":{"name":"bash","parameters":{}}},{"type":"function","function":{"name":"read","parameters":{}}}]}`,
			wantBash:   true,
			wantRead:   true,
			wantInject: false,
		},
		{
			name:       "有其他工具但无 bash/read",
			body:       `{"model":"m","tools":[{"type":"function","function":{"name":"glob","parameters":{}}},{"type":"function","function":{"name":"edit","parameters":{}}}]}`,
			wantBash:   true,
			wantRead:   true,
			wantInject: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := ensureChatStyleFreeTools([]byte(c.body))
			var obj struct {
				Tools []struct {
					Function struct {
						Name string `json:"name"`
					} `json:"function"`
				} `json:"tools"`
			}
			if err := json.Unmarshal(out, &obj); err != nil {
				t.Fatalf("输出非法 JSON: %v", err)
			}
			var gotBash, gotRead bool
			for _, tl := range obj.Tools {
				switch tl.Function.Name {
				case "bash":
					gotBash = true
				case "read":
					gotRead = true
				}
			}
			if gotBash != c.wantBash {
				t.Errorf("bash 存在=%v, want %v", gotBash, c.wantBash)
			}
			if gotRead != c.wantRead {
				t.Errorf("read 存在=%v, want %v", gotRead, c.wantRead)
			}
			// 验证核心工具集全覆盖, 且已有的不重复注入
			if !c.wantInject {
				var names []string
				seen := map[string]int{}
				for _, tl := range obj.Tools {
					names = append(names, tl.Function.Name)
					seen[tl.Function.Name]++
				}
				for _, want := range opencodeCoreToolNames {
					if seen[want] == 0 {
						t.Errorf("核心工具 %s 缺失, 实际 tools=%v", want, names)
					}
					if seen[want] > 1 {
						t.Errorf("核心工具 %s 重复注入 %d 次", want, seen[want])
					}
				}
			}
		})
	}
}

// TestEnsureResponsesStyleFreeTools 验证 Responses 扁平形态的工具补齐。
func TestEnsureResponsesStyleFreeTools(t *testing.T) {
	body := []byte(`{"model":"m","input":[{"role":"user","content":"hi"}],"tools":[{"type":"function","name":"glob","parameters":{}}]}`)
	out := ensureResponsesStyleFreeTools(body)

	var obj struct {
		Tools []struct {
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatalf("输出非法 JSON: %v", err)
	}
	var gotBash, gotRead, gotGlob bool
	for _, tl := range obj.Tools {
		switch tl.Name {
		case "bash":
			gotBash = true
			if tl.Type != "function" {
				t.Errorf("bash type = %q, want function", tl.Type)
			}
		case "read":
			gotRead = true
		case "glob":
			gotGlob = true
		}
	}
	if !gotBash || !gotRead {
		t.Errorf("应补齐 bash+read, 实际 bash=%v read=%v", gotBash, gotRead)
	}
	if !gotGlob {
		t.Error("原有 glob 工具应保留")
	}
	// Responses 形态必须是扁平结构（无嵌套 function 字段）
	if strings.Contains(string(out), `"function":{`) {
		t.Errorf("Responses 形态不应含嵌套 function 字段: %s", out)
	}
}

// TestEnsureResponsesStyleFreeTools_AlreadyHas 验证核心工具集已全覆盖时不改动。
func TestEnsureResponsesStyleFreeTools_AlreadyHas(t *testing.T) {
	// 构造一个已含全部核心工具的请求体
	var parts []string
	for _, n := range opencodeCoreToolNames {
		parts = append(parts, `{"type":"function","name":"`+n+`","parameters":{}}`)
	}
	body := []byte(`{"model":"m","tools":[` + strings.Join(parts, ",") + `]}`)

	out := ensureResponsesStyleFreeTools(body)

	var obj struct {
		Tools []map[string]interface{} `json:"tools"`
	}
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatalf("非法 JSON: %v", err)
	}
	if len(obj.Tools) != len(opencodeCoreToolNames) {
		t.Errorf("核心工具集已全覆盖时不应追加, want %d got %d", len(opencodeCoreToolNames), len(obj.Tools))
	}
}

// TestEnsureFreeTools_InvalidJSON 验证非法 JSON 原样返回。
func TestEnsureFreeTools_InvalidJSON(t *testing.T) {
	bad := []byte(`not json`)
	if got := ensureChatStyleFreeTools(bad); string(got) != string(bad) {
		t.Errorf("非法 JSON 应原样返回, got %s", got)
	}
	if got := ensureResponsesStyleFreeTools(bad); string(got) != string(bad) {
		t.Errorf("非法 JSON 应原样返回, got %s", got)
	}
}

// TestEnsureChatStyleFreeTools_PreservesOtherFields 验证补齐不破坏其他字段。
func TestEnsureChatStyleFreeTools_PreservesOtherFields(t *testing.T) {
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"max_tokens":100,"stream":true,"temperature":0.7}`)
	out := ensureChatStyleFreeTools(body)

	var obj map[string]json.RawMessage
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatalf("非法 JSON: %v", err)
	}
	for _, k := range []string{"model", "messages", "max_tokens", "stream", "temperature", "tools"} {
		if _, ok := obj[k]; !ok {
			t.Errorf("字段 %s 丢失", k)
		}
	}
}
