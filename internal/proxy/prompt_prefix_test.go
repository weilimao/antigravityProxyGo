package proxy

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestPromptPrefix_Standard(t *testing.T) {
	inputJSON := `{
		"contents": [
			{"role": "user", "parts": [{"text": "Hello, how are you?"}]}
		]
	}`
	prefix := "[Chinese Answer] "

	output := injectPromptPrefix([]byte(inputJSON), prefix)

	var doc map[string]interface{}
	if err := json.Unmarshal(output, &doc); err != nil {
		t.Fatalf("Failed to unmarshal output: %v", err)
	}

	contents, ok := doc["contents"].([]interface{})
	if !ok || len(contents) == 0 {
		t.Fatalf("Contents not found or empty")
	}

	firstContent, ok := contents[0].(map[string]interface{})
	if !ok {
		t.Fatalf("Invalid content element structure")
	}

	parts, ok := firstContent["parts"].([]interface{})
	if !ok || len(parts) == 0 {
		t.Fatalf("Parts not found or empty")
	}

	firstPart, ok := parts[0].(map[string]interface{})
	if !ok {
		t.Fatalf("Invalid part structure")
	}

	text, _ := firstPart["text"].(string)
	expected := "[Chinese Answer] Hello, how are you?"
	if text != expected {
		t.Errorf("Expected text %q, got %q", expected, text)
	}
}

func TestPromptPrefix_V1Internal(t *testing.T) {
	inputJSON := `{
		"project": "my-project",
		"request": {
			"contents": [
				{"role": "user", "parts": [{"text": "Explain quantum computing."}]}
			]
		}
	}`
	prefix := "[Simple Explanation] "

	output := injectPromptPrefix([]byte(inputJSON), prefix)

	var doc map[string]interface{}
	if err := json.Unmarshal(output, &doc); err != nil {
		t.Fatalf("Failed to unmarshal output: %v", err)
	}

	request, ok := doc["request"].(map[string]interface{})
	if !ok {
		t.Fatalf("Request object not found")
	}

	contents, ok := request["contents"].([]interface{})
	if !ok || len(contents) == 0 {
		t.Fatalf("Contents not found or empty")
	}

	firstContent, ok := contents[0].(map[string]interface{})
	if !ok {
		t.Fatalf("Invalid content structure")
	}

	parts, ok := firstContent["parts"].([]interface{})
	if !ok || len(parts) == 0 {
		t.Fatalf("Parts not found or empty")
	}

	firstPart, ok := parts[0].(map[string]interface{})
	if !ok {
		t.Fatalf("Invalid part structure")
	}

	text, _ := firstPart["text"].(string)
	expected := "[Simple Explanation] Explain quantum computing."
	if text != expected {
		t.Errorf("Expected text %q, got %q", expected, text)
	}
}

func TestPromptPrefix_Robustness(t *testing.T) {
	// 1. 空前缀不处理
	raw := []byte(`{"contents": [{"parts": [{"text": "no change"}]}]}`)
	out1 := injectPromptPrefix(raw, "")
	if !bytes.Equal(raw, out1) {
		t.Errorf("Expected no change for empty prefix")
	}

	// 2. 非 JSON 不崩溃并直接返回
	invalidJSON := []byte(`not-a-json-payload`)
	out2 := injectPromptPrefix(invalidJSON, "[Prefix] ")
	if !bytes.Equal(invalidJSON, out2) {
		t.Errorf("Expected no change for invalid JSON payload")
	}
}
