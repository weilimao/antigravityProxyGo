package relay

import (
	"testing"
)

func TestParseSummaryResponse_NormalJSON(t *testing.T) {
	normalJSON := `{
		"candidates": [
			{
				"content": {
					"parts": [
						{"text": "This is a normal summary."}
					]
				}
			}
		]
	}`

	text, err := parseSummaryResponse([]byte(normalJSON))
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	expected := "This is a normal summary."
	if text != expected {
		t.Errorf("Expected %q, got %q", expected, text)
	}
}

func TestParseSummaryResponse_SSEStream(t *testing.T) {
	sseData := `data: {"candidates":[{"content":{"parts":[{"text":"Part 1 "}]}}]}

data: {"candidates":[{"content":{"parts":[{"text":"Part 2"}]}}]}

data: [DONE]
`

	text, err := parseSummaryResponse([]byte(sseData))
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	expected := "Part 1 Part 2"
	if text != expected {
		t.Errorf("Expected %q, got %q", expected, text)
	}
}

func TestParseSummaryResponse_Invalid(t *testing.T) {
	invalidJSON := `invalid json`
	_, err := parseSummaryResponse([]byte(invalidJSON))
	if err == nil {
		t.Fatal("Expected error for invalid JSON, got nil")
	}
}
