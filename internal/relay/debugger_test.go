package relay

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDebuggerLogger(t *testing.T) {
	tempDir := t.TempDir()
	dbg := GetGlobalDebugger()
	dbg.Configure(true, tempDir)

	reqID := "test_req_001"
	dbg.LogClientRequest(reqID, "POST", "/nvidia/v1/messages", map[string][]string{"User-Agent": {"ClaudeCode"}}, []byte(`{"model":"z-ai/glm-5.2"}`))
	dbg.LogUpstreamRequest(reqID, "https://integrate.api.nvidia.com/v1/chat/completions", map[string][]string{"Authorization": {"Bearer nvapi-123"}}, []byte(`{"model":"z-ai/glm-5.2"}`))
	dbg.LogUpstreamResponse(reqID, 200, map[string][]string{"Content-Type": {"text/event-stream"}})
	dbg.LogUpstreamFrame(reqID, `data: {"choices":[{"delta":{"content":"hi"}}]}`)
	dbg.LogClientFrame(reqID, "content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`)
	dbg.CloseReq(reqID, "success")

	// 验证日志文件生成
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("failed to read temp dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 log file, got %d", len(entries))
	}

	content, err := os.ReadFile(filepath.Join(tempDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}
	logText := string(content)

	if !strings.Contains(logText, "==== 📥 客户端入站请求 ====") {
		t.Errorf("missing client request log header")
	}
	if !strings.Contains(logText, "==== 🟢 发往上游请求 ====") {
		t.Errorf("missing upstream request log header")
	}
	if !strings.Contains(logText, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}") {
		t.Errorf("missing raw upstream frame log")
	}
	if !strings.Contains(logText, "content_block_delta") {
		t.Errorf("missing translated client frame log")
	}
}
