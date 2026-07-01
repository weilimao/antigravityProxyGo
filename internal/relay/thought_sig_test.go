package relay

import (
	"testing"
)

func TestSanitizeAllThoughtSignatures(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no signatures",
			input:    "Hello world! This is a clean text.",
			expected: "Hello world! This is a clean text.",
		},
		{
			name:     "single signature with leading newline",
			input:    "Here is the result.\n<!--thought_signature:EiYKJGUyNDgzMGE3LTVjZDYtNDJmZS05OThiLWVlNTM5ZTCyyJljMw==-->",
			expected: "Here is the result.",
		},
		{
			name:     "multiple signatures embedded",
			input:    "Step 1.\n<!--thought_signature:sig1-->\nStep 2.\n<!--thought_signature:sig2-->",
			expected: "Step 1.\nStep 2.",
		},
		{
			name:     "signature without newline",
			input:    "Text before<!--thought_signature:sig3-->Text after",
			expected: "Text beforeText after",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeAllThoughtSignatures(tc.input)
			if got != tc.expected {
				t.Errorf("SanitizeAllThoughtSignatures(%q) = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}
}

func TestEncodeThoughtSignatureDisabled(t *testing.T) {
	got := EncodeThoughtSignature("some-sig")
	if got != "" {
		t.Errorf("EncodeThoughtSignature should be disabled and return empty string, got %q", got)
	}
}
