package stats

import (
	"os"
	"testing"
)

func TestPacketCapturer_EnablePacketCaptureToggle(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "packet_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	captureEnabled := true
	pc := NewPacketCapturer(
		nil,
		nil,
		func() bool {
			return captureEnabled
		},
	)
	pc.Init(tempDir)

	// 1. Test when capture is enabled
	pkg1 := pc.SavePacket("POST", "test.com", "/v1/test", nil, []byte(`{"req":"val"}`), nil, []byte(`{"res":"val"}`), 200)
	if pkg1 == nil {
		t.Fatal("expected packet to be captured when toggle is ON")
	}

	packets := pc.GetPackets()
	if len(packets) != 1 {
		t.Fatalf("expected 1 captured packet, got %d", len(packets))
	}

	// 2. Test when capture is disabled
	captureEnabled = false
	pkg2 := pc.SavePacket("POST", "test.com", "/v1/test2", nil, []byte(`{"req":"val"}`), nil, []byte(`{"res":"val"}`), 200)
	if pkg2 != nil {
		t.Fatal("expected packet to NOT be captured when toggle is OFF")
	}

	// The count of packets should still be 1 (the one from step 1)
	packets = pc.GetPackets()
	if len(packets) != 1 {
		t.Fatalf("expected packets count to remain 1, got %d", len(packets))
	}
}

func TestResolvePacketSource(t *testing.T) {
	tests := []struct {
		name     string
		headers  interface{}
		expected string
	}{
		{
			name: "Antigravity IDE with vscode_client",
			headers: map[string][]string{
				"User-Agent": {"antigravity/ide/2.5.7 (vscode_client; os_type:windows; arch:amd64)"},
			},
			expected: "IDE",
		},
		{
			name: "Antigravity IDE case insensitive header key",
			headers: map[string]string{
				"user-Agent": "antigravity/ide/2.1.1 windows/amd64",
			},
			expected: "IDE",
		},
		{
			name: "CloudAI Companion IDE",
			headers: map[string]interface{}{
				"user-agent": "cloudaicompanion/1.0.0",
			},
			expected: "IDE",
		},
		{
			name: "Google API Nodejs Client IDE",
			headers: map[string]string{
				"User-Agent": "google-api-nodejs-client/1.0.0",
			},
			expected: "IDE",
		},
		{
			name: "Go HTTP Client (IDE proxy stream)",
			headers: map[string][]string{
				"User-Agent": {"Go-http-client/1.1"},
			},
			expected: "IDE",
		},
		{
			name: "Antigravity Hub Agent with aidev_client suffix (MUST be Agent, not CLI)",
			headers: map[string][]string{
				"User-Agent": {"antigravity/hub/2.3.1 (aidev_client; os_type=windows; arch=amd64)"},
			},
			expected: "Agent",
		},
		{
			name: "Antigravityproxy prefix Agent",
			headers: map[string]string{
				"User-Agent": "antigravityproxy-desktop/1.0.0",
			},
			expected: "Agent",
		},
		{
			name: "Antigravity CLI client",
			headers: map[string][]string{
				"User-Agent": {"antigravity/cli/1.2.0 darwin/arm64"},
			},
			expected: "CLI",
		},
		{
			name:     "Nil headers",
			headers:  nil,
			expected: "未知",
		},
		{
			name: "Empty User-Agent",
			headers: map[string][]string{
				"Content-Type": {"application/json"},
			},
			expected: "未知",
		},
		{
			name: "Unknown Client User-Agent",
			headers: map[string]string{
				"User-Agent": "curl/7.68.0",
			},
			expected: "未知",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolvePacketSource(tt.headers)
			if got != tt.expected {
				t.Errorf("ResolvePacketSource() = %v, want %v", got, tt.expected)
			}
		})
	}
}
