package session

import (
	"net/http"
	"testing"
)

func TestExtractSessionKey_StripPort(t *testing.T) {
	r := NewRouter()

	tests := []struct {
		name       string
		remoteAddr string
		expected   string
	}{
		{"IPv4 with port", "192.168.1.100:12345", "sock:192.168.1.100"},
		{"IPv6 with port", "[2001:db8::1]:12345", "sock:2001:db8::1"},
		{"IP without port", "192.168.1.100", "sock:192.168.1.100"},
		{"empty addr", "", "sock:unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("POST", "http://example.com", nil)
			req.RemoteAddr = tt.remoteAddr

			key := r.ExtractSessionKey(req, nil)
			if key != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, key)
			}
		})
	}
}
