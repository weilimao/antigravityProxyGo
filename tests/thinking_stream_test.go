package tests

import (
	"encoding/json"
	"testing"
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

	var parsed struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text    string `json:"text"`
					Thought bool   `json:"thought"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}

	if err := json.Unmarshal([]byte(rawJSON), &parsed); err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	if len(parsed.Candidates) == 0 || len(parsed.Candidates[0].Content.Parts) != 2 {
		t.Fatalf("Unexpected structure length")
	}

	p1 := parsed.Candidates[0].Content.Parts[0]
	if !p1.Thought || p1.Text != "Thinking step 1..." {
		t.Errorf("Part 1 failed thought check")
	}

	p2 := parsed.Candidates[0].Content.Parts[1]
	if p2.Thought {
		t.Errorf("Part 2 should not be marked as thought")
	}
}
