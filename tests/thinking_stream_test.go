package tests

import (
	"encoding/json"
	"testing"

	"antigravity-proxy/internal/relay"
)

func TestGeminiPartThoughtFlag(t *testing.T) {
	rawJSON := `{
		"candidates": [
			{
				"content": {
					"role": "model",
					"parts": [
						{
							"text": "Thinking step 1...",
							"thought": true
						},
						{
							"text": "Final answer text."
						}
					]
				}
			}
		]
	}`

	var resp relay.GeminiResponse
	if err := json.Unmarshal([]byte(rawJSON), &resp); err != nil {
		t.Fatalf("Failed to unmarshal GeminiResponse: %v", err)
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) != 2 {
		t.Fatalf("Expected 2 parts in candidate")
	}

	p1 := resp.Candidates[0].Content.Parts[0]
	p2 := resp.Candidates[0].Content.Parts[1]

	if !p1.Thought {
		t.Errorf("Expected p1.Thought to be true, got false")
	}
	if p2.Thought {
		t.Errorf("Expected p2.Thought to be false, got true")
	}
}
