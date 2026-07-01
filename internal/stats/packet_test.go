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
