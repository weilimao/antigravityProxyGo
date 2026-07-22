package proxy

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestStripThoughtSignature_BasicRemoval 验证 thoughtSignature 字段被递归删除
func TestStripThoughtSignature_BasicRemoval(t *testing.T) {
	inputJSON := `{
		"contents": [
			{
				"role": "model",
				"parts": [
					{
						"functionCall": {"name": "shell_command", "args": {"command": "ls"}},
						"thoughtSignature": "skip_thought_signature_validator"
					},
					{
						"text": "hello",
						"thoughtSignature": "EpAECo0..."
					}
				]
			}
		]
	}`

	var doc map[string]interface{}
	if err := json.Unmarshal([]byte(inputJSON), &doc); err != nil {
		t.Fatalf("Failed to unmarshal input: %v", err)
	}

	stripThoughtSignature(doc)

	out, _ := json.Marshal(doc)
	outStr := string(out)
	if strings.Contains(outStr, "thoughtSignature") {
		t.Errorf("thoughtSignature field still present after stripping: %s", outStr)
	}

	// functionCall 和 text 字段应保留
	contents := doc["contents"].([]interface{})
	parts := contents[0].(map[string]interface{})["parts"].([]interface{})
	part0 := parts[0].(map[string]interface{})
	if _, ok := part0["functionCall"]; !ok {
		t.Errorf("functionCall field was incorrectly removed")
	}
	part1 := parts[1].(map[string]interface{})
	if part1["text"] != "hello" {
		t.Errorf("text field was incorrectly modified: %v", part1["text"])
	}
}

// TestStripThoughtSignature_NestedRequest 验证 v1internal 的 request.contents 结构被正确处理
func TestStripThoughtSignature_NestedRequest(t *testing.T) {
	inputJSON := `{
		"request": {
			"contents": [
				{
					"role": "model",
					"parts": [
						{
							"functionCall": {"name": "shell_command"},
							"thoughtSignature": "sig123"
						}
					]
				}
			],
			"tools": [{"functionDeclarations": [{"name": "shell_command"}]}]
		}
	}`

	var doc map[string]interface{}
	if err := json.Unmarshal([]byte(inputJSON), &doc); err != nil {
		t.Fatalf("Failed to unmarshal input: %v", err)
	}

	stripThoughtSignature(doc)

	out, _ := json.Marshal(doc)
	outStr := string(out)
	if strings.Contains(outStr, "thoughtSignature") {
		t.Errorf("thoughtSignature field still present in nested request: %s", outStr)
	}

	// tools 应保留
	req := doc["request"].(map[string]interface{})
	if _, ok := req["tools"]; !ok {
		t.Errorf("tools field was incorrectly removed")
	}
}

// TestStripThoughtSignature_NoSignature 验证无 thoughtSignature 的请求体不受影响
func TestStripThoughtSignature_NoSignature(t *testing.T) {
	inputJSON := `{
		"contents": [
			{"role": "user", "parts": [{"text": "Hello"}]}
		]
	}`

	var doc map[string]interface{}
	if err := json.Unmarshal([]byte(inputJSON), &doc); err != nil {
		t.Fatalf("Failed to unmarshal input: %v", err)
	}

	stripThoughtSignature(doc)

	contents := doc["contents"].([]interface{})
	parts := contents[0].(map[string]interface{})["parts"].([]interface{})
	text := parts[0].(map[string]interface{})["text"]
	if text != "Hello" {
		t.Errorf("text field should be unchanged, got: %v", text)
	}
}
