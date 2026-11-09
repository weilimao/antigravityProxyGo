package proxy

import (
	"antigravity-proxy/internal/account"
	"bytes"
	"net/http/httptest"
	"testing"
)

// handler_sniffer_test.go: 验证官方请求版本号与 CL 构建号的动态嗅探逻辑。

func TestSniffAntigravityVersion(t *testing.T) {
	tests := []struct {
		name       string
		ua         string
		wantVer    string
		wantCL     string
		wantOK     bool
	}{
		{
			name:    "官方标准 Hub 请求 (带完整 CL 构建号)",
			ua:      "antigravity/hub/2.29.1 (aidev_client; os_type=windows; arch=amd64; cl=1015941558)",
			wantVer: "2.29.1",
			wantCL:  "1015941558",
			wantOK:  true,
		},
		{
			name:    "官方 Hub 请求 (仅版本无 CL)",
			ua:      "antigravity/hub/2.29.0",
			wantVer: "2.29.0",
			wantCL:  "",
			wantOK:  true,
		},
		{
			name:    "官方白色桌面端请求",
			ua:      "antigravity/2.29.1 windows/amd64 google-api-nodejs-client/10.3.0",
			wantVer: "2.29.1",
			wantCL:  "",
			wantOK:  true,
		},
		{
			name:    "官方 IDE 请求",
			ua:      "antigravity/ide/2.29.1 (vscode_client; os_type=windows; arch=amd64; cl=1015941558)",
			wantVer: "2.29.1",
			wantCL:  "1015941558",
			wantOK:  true,
		},
		{
			name:    "第三方 Cursor 客户端伪装排除",
			ua:      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Cursor/0.45.0",
			wantVer: "",
			wantCL:  "",
			wantOK:  false,
		},
		{
			name:    "第三方插件带有 plugin 关键字排除",
			ua:      "antigravity/hub/2.29.1 (aidev_client; plugin=1)",
			wantVer: "",
			wantCL:  "",
			wantOK:  false,
		},
		{
			name:    "空 User-Agent",
			ua:      "",
			wantVer: "",
			wantCL:  "",
			wantOK:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ver, cl, ok := SniffAntigravityVersion(tt.ua)
			if ok != tt.wantOK {
				t.Fatalf("SniffAntigravityVersion() ok = %v, want %v", ok, tt.wantOK)
			}
			if ok {
				if ver != tt.wantVer {
					t.Errorf("ver = %q, want %q", ver, tt.wantVer)
				}
				if cl != tt.wantCL {
					t.Errorf("cl = %q, want %q", cl, tt.wantCL)
				}
			}
		})
	}
}

func TestProxyHandler_SniffAndAutoUpdate(t *testing.T) {
	tempDir := t.TempDir()
	accMgr := account.NewManager()
	accMgr.Init(tempDir)
	accMgr.SetAntigravityCliVersion("2.29.0")
	accMgr.SetAntigravityBuildCL("1013060670")

	var logMsg string
	h := &ProxyHandler{
		accountMgr: accMgr,
		logFn: func(s string) {
			logMsg = s
		},
	}

	// 模拟官方原生 2.29.1 请求进站
	req := httptest.NewRequest("POST", "/v1internal/test", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("User-Agent", "antigravity/hub/2.29.1 (aidev_client; os_type=windows; arch=amd64; cl=1015941558)")

	// 嗅探并触发自动更新
	inboundUA := req.Header.Get("User-Agent")
	if ver, cl, ok := SniffAntigravityVersion(inboundUA); ok && h.accountMgr != nil {
		if h.accountMgr.UpdateAntigravityVersionAndCL(ver, cl) && h.logFn != nil {
			h.logFn("updated: " + ver)
		}
	}

	if accMgr.GetAntigravityCliVersion() != "2.29.1" {
		t.Errorf("accountMgr version want 2.29.1, got %s", accMgr.GetAntigravityCliVersion())
	}
	if accMgr.GetAntigravityBuildCL() != "1015941558" {
		t.Errorf("accountMgr CL want 1015941558, got %s", accMgr.GetAntigravityBuildCL())
	}
	if logMsg != "updated: 2.29.1" {
		t.Errorf("expected logMsg 'updated: 2.29.1', got %q", logMsg)
	}
}
