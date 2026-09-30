package stats

import (
	"strings"
	"testing"
)

func TestTruncateRequestBody_Nil(t *testing.T) {
	if res := TruncateRequestBody(nil); res != nil {
		t.Fatalf("expected nil, got %v", res)
	}
}

func TestTruncateRequestBody_SmallJSON(t *testing.T) {
	raw := `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`
	res := TruncateRequestBody(raw)
	m, ok := res.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map[string]interface{}, got %T", res)
	}
	if m["model"] != "gpt-4o" {
		t.Fatalf("expected model=gpt-4o, got %v", m["model"])
	}
}

func TestTruncateRequestBody_LargeStringExceedsThreshold(t *testing.T) {
	// 构造超过 32KB 的大字符串
	largeStr := strings.Repeat("A", 40*1024)
	res := TruncateRequestBody(largeStr)
	resStr, ok := res.(string)
	if !ok {
		t.Fatalf("expected string, got %T", res)
	}
	if !strings.Contains(resStr, "大报文已截断") {
		t.Fatalf("expected truncation mark in result, got: %s", resStr[:100])
	}
	if len(resStr) > 3000 {
		t.Fatalf("expected truncated string length <= 3000, got %d", len(resStr))
	}
}

func TestTruncateRequestBody_LargeArrayTruncation(t *testing.T) {
	// 构造 30 条对话消息的数组
	messages := make([]interface{}, 30)
	for i := 0; i < 30; i++ {
		messages[i] = map[string]interface{}{
			"role":    "user",
			"content": "message",
		}
	}
	payload := map[string]interface{}{
		"messages": messages,
	}

	res := TruncateRequestBody(payload)
	m, ok := res.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map, got %T", res)
	}
	msgs, ok := m["messages"].([]interface{})
	if !ok {
		t.Fatalf("expected []interface{}, got %T", m["messages"])
	}
	// 期望保留 3 + 1(提示) + 3 = 7 项
	if len(msgs) != 7 {
		t.Fatalf("expected 7 items in truncated slice, got %d", len(msgs))
	}
	middleNote, ok := msgs[3].(string)
	if !ok || !strings.Contains(middleNote, "已省略中间") {
		t.Fatalf("expected middle omission notice, got: %v", msgs[3])
	}
}

func TestTruncateRequestBody_LongStringField(t *testing.T) {
	longText := strings.Repeat("X", 2000)
	payload := map[string]interface{}{
		"prompt": longText,
	}
	res := TruncateRequestBody(payload)
	m := res.(map[string]interface{})
	prompt := m["prompt"].(string)
	if !strings.Contains(prompt, "已截断") {
		t.Fatalf("expected prompt to be truncated, got %s", prompt)
	}
	if len(prompt) > 600 {
		t.Fatalf("expected prompt length <= 600, got %d", len(prompt))
	}
}

func TestTruncateRequestBody_DeepNesting(t *testing.T) {
	// 构造 10 层嵌套 map
	curr := make(map[string]interface{})
	root := curr
	for i := 0; i < 10; i++ {
		next := make(map[string]interface{})
		curr["child"] = next
		curr = next
	}
	res := TruncateRequestBody(root)
	if res == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestRequestBodyToDBString_HardBound(t *testing.T) {
	// 构造超大字符串测试入库防御
	hugeText := strings.Repeat("B", 50000)
	dbStr := requestBodyToDBString(hugeText)
	if len(dbStr) > 8192 {
		t.Fatalf("expected dbStr <= 8192, got %d", len(dbStr))
	}
	if !strings.Contains(dbStr, "报文过大已截断入库") {
		t.Fatalf("expected truncation mark in dbStr, got %s", dbStr[:100])
	}

	// 构造超大 Map 测试入库防御
	hugeMap := map[string]string{
		"huge": strings.Repeat("C", 50000),
	}
	dbMapStr := requestBodyToDBString(hugeMap)
	if len(dbMapStr) > 8192 {
		t.Fatalf("expected dbMapStr <= 8192, got %d", len(dbMapStr))
	}
	if !strings.Contains(dbMapStr, "报文过大已截断入库") {
		t.Fatalf("expected truncation mark in dbMapStr, got %s", dbMapStr[:100])
	}
}
