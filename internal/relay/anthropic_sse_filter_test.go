package relay

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPatchAnthropicMessageStart_PreservesExistingNonZeroInputTokens(t *testing.T) {
	rawSSE := []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"usage\":{\"input_tokens\":2500,\"output_tokens\":1}}}\n\n")
	reqBody := []byte(`{"messages":[{"role":"user","content":"hello"}]}`)

	res := PatchAnthropicMessageStart(rawSSE, reqBody)
	if string(res) != string(rawSSE) {
		t.Fatalf("Expected raw SSE to be preserved when input_tokens > 0, got: %s", string(res))
	}
}

func TestPatchAnthropicMessageStart_PatchesZeroOrMissingInputTokens(t *testing.T) {
	rawSSE := []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_2\",\"usage\":{\"input_tokens\":0,\"output_tokens\":1}}}\n\n")
	reqBody := []byte(`{"messages":[{"role":"user","content":"hello world, this is a test prompt to estimate tokens"}]}`)

	res := PatchAnthropicMessageStart(rawSSE, reqBody)
	resStr := string(res)

	if strings.Contains(resStr, `"input_tokens":0`) {
		t.Fatalf("Expected input_tokens to be patched from 0 to estimated value, got: %s", resStr)
	}

	// 提取并断言 input_tokens > 0
	dataIdx := strings.Index(resStr, "data: ")
	if dataIdx < 0 {
		t.Fatalf("Missing data: prefix in patched SSE: %s", resStr)
	}
	jsonStr := strings.TrimSpace(resStr[dataIdx+6:])

	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &obj); err != nil {
		t.Fatalf("Failed to parse patched JSON: %v", err)
	}

	msg := obj["message"].(map[string]interface{})
	usage := msg["usage"].(map[string]interface{})
	inTokens := int(usage["input_tokens"].(float64))

	if inTokens <= 0 {
		t.Errorf("Expected inTokens > 0, got %d", inTokens)
	}
}
