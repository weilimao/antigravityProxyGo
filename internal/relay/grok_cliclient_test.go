package relay

// grok_cliclient_test.go: Grok CLI 身份头注入(grok_cliclient.go)的单元测试。
//
// 覆盖 applyGrokCLIHeaders 的核心契约:
//   - 命中 cli-chat-proxy.grok.com 时注入三个身份头(X-XAI-Token-Auth / x-grok-client-version / User-Agent);
//   - 非 chat-proxy 端点(api.x.ai/v1 / 自建反代)不注入(不污染原生 API Key 路径);
//   - version 空串回退默认 DefaultGrokCliVersion("1.0.0"),确保 x-grok-client-version 永不空
//     (上游 "(none)" 报错正是此头漏值导致,这是本修复的红线);
//   - grokIsCLIChatProxyBaseURL host 判定:容 /v1 与无 /v1 写法、大小写、http/https、带 path。

import (
	"net/http"
	"strings"
	"testing"

	"antigravity-proxy/internal/account"
)

// applyGrokCLIHeaders 注入的头名(对齐 CLIProxyAPI2 xai_executor.go:65-69 头名常量)。
const (
	testXAITokenAuthHeader      = "X-XAI-Token-Auth"
	testGrokClientVersionHeader = "x-grok-client-version"
)

// TestGrokIsCLIChatProxyBaseURL 锁定 host 判定守卫:命中走注入,不命中走跳过。
func TestGrokIsCLIChatProxyBaseURL(t *testing.T) {
	cases := []struct {
		baseURL string
		want    bool
	}{
		{"https://cli-chat-proxy.grok.com/v1", true},
		{"https://cli-chat-proxy.grok.com", true},
		{"http://cli-chat-proxy.grok.com/v1/chat/completions", true},
		{"https://CLI-Chat-Proxy.Grok.COM/v1", true}, // 大小写不敏感
		{"https://api.x.ai/v1", false},                // 官方 API 端点不命中
		{"https://my-relay.example.com/v1", false},    // 自建反代不命中
		{"", false},                                   // 空串安全返回 false
		{"https://evil-cli-chat-proxy.grok.com/v1", false}, // 前缀仿冒不得命中
		{"https://cli-chat-proxy.grok.com.evil.com/v1", false},
	}
	for _, c := range cases {
		got := grokIsCLIChatProxyBaseURL(c.baseURL)
		if got != c.want {
			t.Errorf("grokIsCLIChatProxyBaseURL(%q) = %v, want %v", c.baseURL, got, c.want)
		}
	}
}

// TestApplyGrokCLIHeaders_ChatProxyHost 锁定核心修复:命中 chat-proxy 时三头齐注入。
// 版本号取显式入参 0.2.93(模拟号池配置后),三头取值应严格对齐 CLIProxyAPI2。
func TestApplyGrokCLIHeaders_ChatProxyHost(t *testing.T) {
	req, _ := http.NewRequest("POST", "https://cli-chat-proxy.grok.com/v1/chat/completions", nil)
	applyGrokCLIHeaders(req, "https://cli-chat-proxy.grok.com/v1", "0.2.93")

	if got := req.Header.Get(testXAITokenAuthHeader); got != "xai-grok-cli" {
		t.Errorf("X-XAI-Token-Auth = %q, want %q", got, "xai-grok-cli")
	}
	if got := req.Header.Get(testGrokClientVersionHeader); got != "0.2.93" {
		t.Errorf("x-grok-client-version = %q, want %q", got, "0.2.93")
	}
	if got := req.Header.Get("User-Agent"); got != "xai-grok-workspace/0.2.93" {
		t.Errorf("User-Agent = %q, want %q", got, "xai-grok-workspace/0.2.93")
	}
}

// TestApplyGrokCLIHeaders_OfficialAPI 锁定隔离安全:走 api.x.ai/v1 官方端点不注入身份头,
// 避免污染原生 API Key 路径(上游无版本闸门,注入反而多余)。
func TestApplyGrokCLIHeaders_OfficialAPI(t *testing.T) {
	req, _ := http.NewRequest("POST", "https://api.x.ai/v1/chat/completions", nil)
	applyGrokCLIHeaders(req, "https://api.x.ai/v1", "1.0.0")

	if got := req.Header.Get(testXAITokenAuthHeader); got != "" {
		t.Errorf("X-XAI-Token-Auth on api.x.ai = %q, want empty (no injection)", got)
	}
	if got := req.Header.Get(testGrokClientVersionHeader); got != "" {
		t.Errorf("x-grok-client-version on api.x.ai = %q, want empty (no injection)", got)
	}
	if got := req.Header.Get("User-Agent"); got != "" {
		t.Errorf("User-Agent on api.x.ai = %q, want empty (no injection)", got)
	}
}

// TestApplyGrokCLIHeaders_BlankVersionFallback 锁定红线:version 空串必须回退默认 DefaultGrokCliVersion,
// 绝不发出空 x-grok-client-version(上游 "(none)" → 426 报错正是此头漏值导致)。
func TestApplyGrokCLIHeaders_BlankVersionFallback(t *testing.T) {
	for _, blank := range []string{"", "   ", "\t"} {
		req, _ := http.NewRequest("POST", "https://cli-chat-proxy.grok.com/v1/chat/completions", nil)
		applyGrokCLIHeaders(req, "https://cli-chat-proxy.grok.com/v1", blank)

		want := account.DefaultGrokCliVersion
		if got := req.Header.Get(testGrokClientVersionHeader); got != want {
			t.Errorf("blank version %q → x-grok-client-version = %q, want %q (must fall back, never empty)",
				blank, got, want)
		}
		if got := req.Header.Get("User-Agent"); !strings.HasPrefix(got, "xai-grok-workspace/") || got == "xai-grok-workspace/" {
			t.Errorf("blank version %q → User-Agent = %q, want prefix xai-grok-workspace/<ver>", blank, got)
		}
	}
}

// TestApplyGrokCLIHeaders_NilRequestSafe 锁定防御:nil 请求不 panic(纯防御,正常链路不会传 nil)。
func TestApplyGrokCLIHeaders_NilRequestSafe(t *testing.T) {
	// 不应 panic。
	applyGrokCLIHeaders(nil, "https://cli-chat-proxy.grok.com/v1", "1.0.0")
}

// TestApplyGrokCLIHeaders_CustomBaseURL 锁定正交性:用户自建反代指向 cli-chat-proxy.grok.com 时
// 也应注入(host 判定只认 host,不认 scheme/path),保证代理链路同样带身份头。
func TestApplyGrokCLIHeaders_CustomBaseURLHitsChatProxy(t *testing.T) {
	req, _ := http.NewRequest("POST", "https://cli-chat-proxy.grok.com/v1/chat/completions", nil)
	// 用户在号池把 BaseURL 写成带冗余空格/无 /v1 的变体,host 判定仍应命中。
	applyGrokCLIHeaders(req, "https://cli-chat-proxy.grok.com/", "1.0.0")

	if got := req.Header.Get(testGrokClientVersionHeader); got != "1.0.0" {
		t.Errorf("custom baseURL (trailing slash) → x-grok-client-version = %q, want 1.0.0", got)
	}
}
