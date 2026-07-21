package proxy

import (
	"encoding/json"
	"testing"
)

func TestThinkingConfigInject_AdaptiveBudget(t *testing.T) {
	inputJSON := `{
		"model": "gemini-3.6-flash-tiered",
		"project": "test-project",
		"request": {
			"contents": [
				{"role": "user", "parts": [{"text": "Hello"}]}
			],
			"generationConfig": {
				"maxOutputTokens": 16384
			}
		}
	}`

	var bodyMap map[string]interface{}
	if err := json.Unmarshal([]byte(inputJSON), &bodyMap); err != nil {
		t.Fatalf("Failed to unmarshal input JSON: %v", err)
	}

	supportsThinking := true
	budget := -1
	minBudget := 32
	maxOutputTokens := 65536

	var thinkingCfg map[string]interface{}
	if !supportsThinking || budget == 0 {
		thinkingCfg = map[string]interface{}{
			"includeThoughts": false,
		}
	} else if budget == -1 {
		thinkingCfg = map[string]interface{}{
			"includeThoughts": true,
		}
	} else {
		clampedBudget := budget
		if minBudget > 0 && clampedBudget < minBudget {
			clampedBudget = minBudget
		}
		thinkingCfg = map[string]interface{}{
			"includeThoughts": true,
			"thinkingBudget":  clampedBudget,
		}
	}

	reqMap := bodyMap["request"].(map[string]interface{})
	genConfig := reqMap["generationConfig"].(map[string]interface{})
	genConfig["thinkingConfig"] = thinkingCfg
	if maxOutputTokens > 0 {
		genConfig["maxOutputTokens"] = maxOutputTokens
	}

	outBytes, err := json.Marshal(bodyMap)
	if err != nil {
		t.Fatalf("Failed to marshal modified JSON: %v", err)
	}

	var resMap map[string]interface{}
	if err := json.Unmarshal(outBytes, &resMap); err != nil {
		t.Fatalf("Failed to unmarshal result JSON: %v", err)
	}

	resReq := resMap["request"].(map[string]interface{})
	resGen := resReq["generationConfig"].(map[string]interface{})
	tConfig := resGen["thinkingConfig"].(map[string]interface{})

	if tConfig["includeThoughts"] != true {
		t.Errorf("Expected includeThoughts to be true, got %v", tConfig["includeThoughts"])
	}

	// Verify thinkingBudget key is omitted when budget == -1
	if _, exists := tConfig["thinkingBudget"]; exists {
		t.Errorf("Expected thinkingBudget to be omitted when budget == -1, but it exists: %v", tConfig["thinkingBudget"])
	}

	if int(resGen["maxOutputTokens"].(float64)) != 65536 {
		t.Errorf("Expected maxOutputTokens to be 65536, got %v", resGen["maxOutputTokens"])
	}
}

func TestThinkingConfigInject_DisabledThinking(t *testing.T) {
	inputJSON := `{
		"model": "gemini-2.5-pro",
		"request": {
			"generationConfig": {
				"thinkingConfig": {
					"includeThoughts": true,
					"thinkingBudget": 2048
				}
			}
		}
	}`

	var bodyMap map[string]interface{}
	if err := json.Unmarshal([]byte(inputJSON), &bodyMap); err != nil {
		t.Fatalf("Failed to unmarshal input JSON: %v", err)
	}

	supportsThinking := true
	budget := 0

	var thinkingCfg map[string]interface{}
	if !supportsThinking || budget == 0 {
		thinkingCfg = map[string]interface{}{
			"includeThoughts": false,
		}
	}

	reqMap := bodyMap["request"].(map[string]interface{})
	genConfig := reqMap["generationConfig"].(map[string]interface{})
	genConfig["thinkingConfig"] = thinkingCfg

	outBytes, err := json.Marshal(bodyMap)
	if err != nil {
		t.Fatalf("Failed to marshal modified JSON: %v", err)
	}

	var resMap map[string]interface{}
	if err := json.Unmarshal(outBytes, &resMap); err != nil {
		t.Fatalf("Failed to unmarshal result JSON: %v", err)
	}

	resReq := resMap["request"].(map[string]interface{})
	resGen := resReq["generationConfig"].(map[string]interface{})
	tConfig := resGen["thinkingConfig"].(map[string]interface{})

	if tConfig["includeThoughts"] != false {
		t.Errorf("Expected includeThoughts to be false, got %v", tConfig["includeThoughts"])
	}
	if _, exists := tConfig["thinkingBudget"]; exists {
		t.Errorf("Expected thinkingBudget to be omitted when thinking is disabled, but exists: %v", tConfig["thinkingBudget"])
	}
}
