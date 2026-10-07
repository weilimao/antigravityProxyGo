package session

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"
)

func authHashHex(t *testing.T, token string) string {
	t.Helper()
	hasher := sha256.New()
	hasher.Write([]byte(token))
	return hex.EncodeToString(hasher.Sum(nil))[:16]
}

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

// TestExtractClientSessionHeader 锁定客户端原生会话头的识别优先级与四态:
//   - X-Claude-Code-Session-Id 优先 → "claude:<UUID>"
//   - 缺 Claude 头时 Session-Id 次优先 → "codex:<UUID>"
//   - 缺 Session-Id 时 Thread-Id 兜底 → "codex:<UUID>"(与 Session-Id 等值口径)
//   - 三头全缺 → 空串(调用方据此回退 ExtractSessionKey)
//   - 纯空白头等同未携带
//   - nil req 防御返回空串
func TestExtractClientSessionHeader(t *testing.T) {
	router := NewRouter()

	const (
		claudeID    = "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
		opencodeSID = "ses_ffad672d8ffenm0wrlAwIM85IH"
		codexSID    = "019fea2a-ebe0-7693-a323-14df0c53786c"
		codexTID    = "019fea2a-ebe0-7693-a323-14df0c53786c"
	)

	tests := []struct {
		name    string
		headers map[string]string
		req     *http.Request
		want    string
	}{
		{
			name:    "Claude Code 头 优先",
			headers: map[string]string{"X-Claude-Code-Session-Id": claudeID, "X-Session-Id": opencodeSID, "Session-Id": codexSID, "Thread-Id": codexTID},
			want:    "claude:" + claudeID,
		},
		{
			name:    "OpenCode X-Session-Id 优先于 Codex",
			headers: map[string]string{"X-Session-Id": opencodeSID, "Session-Id": codexSID},
			want:    "opencode:" + opencodeSID,
		},
		{
			name:    "仅 OpenCode X-Session-Id",
			headers: map[string]string{"X-Session-Id": opencodeSID},
			want:    "opencode:" + opencodeSID,
		},
		{
			name:    "仅 OpenCode X-Session-Affinity 兜底",
			headers: map[string]string{"X-Session-Affinity": opencodeSID},
			want:    "opencode:" + opencodeSID,
		},
		{
			name:    "ZCode UA 下 X-Session-Id 标 zcode 前缀",
			headers: map[string]string{"User-Agent": "ZCode/3.10.1 ai-sdk/provider-utils/4.0.27 runtime/node.js/24", "X-Session-Id": opencodeSID},
			want:    "zcode:" + opencodeSID,
		},
		{
			name:    "ZCode UA 下 X-Session-Affinity 亦标 zcode 前缀",
			headers: map[string]string{"User-Agent": "ZCode/3.10.1", "X-Session-Affinity": opencodeSID},
			want:    "zcode:" + opencodeSID,
		},
		{
			name:    "UA 含 opencode 保持 opencode 前缀",
			headers: map[string]string{"User-Agent": "opencode/1.18.18", "X-Session-Id": opencodeSID},
			want:    "opencode:" + opencodeSID,
		},
		{
			name:    "OpenCode X-Session-Id 优先于 X-Session-Affinity",
			headers: map[string]string{"X-Session-Id": opencodeSID, "X-Session-Affinity": "other-affinity"},
			want:    "opencode:" + opencodeSID,
		},
		{
			name:    "仅 Codex Session-Id",
			headers: map[string]string{"Session-Id": codexSID},
			want:    "codex:" + codexSID,
		},
		{
			name:    "仅 Codex Thread-Id 兜底",
			headers: map[string]string{"Thread-Id": codexTID},
			want:    "codex:" + codexTID,
		},
		{
			name:    "多头全缺 返回空串",
			headers: map[string]string{},
			want:    "",
		},
		{
			name:    "Claude 与 OpenCode 头纯空白视同未携带 回退 Codex",
			headers: map[string]string{"X-Claude-Code-Session-Id": "   ", "X-Session-Id": "\t", "Session-Id": codexSID},
			want:    "codex:" + codexSID,
		},
		{
			name:    "全头皆空白 返回空串",
			headers: map[string]string{"X-Session-Id": " ", "Session-Id": "\t", "Thread-Id": " "},
			want:    "",
		},
		{
			name: "nil req 防御",
			req:  nil,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req *http.Request
			if tt.req == nil {
				req, _ = http.NewRequest("POST", "http://example.com", nil)
				for k, v := range tt.headers {
					req.Header.Set(k, v)
				}
			} else {
				req = tt.req
			}
			got := router.ExtractClientSessionHeader(req)
			if got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

// TestExtractSessionKey_CredentialHeaders 锁定 ExtractSessionKey baseKey 对承载接入凭证的
// 请求头识别(与 extractToken 同口径但不含 URL ?key=):X-Api-Key / X-Goog-Api-Key(大小写两态由
// http.Header.Get 自动归一)/ ANTHROPIC_API_KEY / API_KEY 任一非空且 trim 后长度>10 即走
// "auth:<SHA256[:16hex]>" 分支,与 Authorization: Bearer 路径产物同形态;Authorization 存在时
// 优先级压过其他凭证头;全部凭证全空/过短回退既有 "sock:<host>" 兜底。
//
// 业务背景:Anthropic SDK / Cherry Studio 等 X-Api-Key 鉴权的直连客户端,此前因 baseKey 计算只
// 认 Authorization Bearer 而被降级到 "sock:<IP>",致使同一来源 IP 下不同 API Key/用户全部 sticky
// 到同一上游账号(就是请求日志里看到的 "sock:acc:172.16.10.123" 错误聚合)。
func TestExtractSessionKey_CredentialHeaders(t *testing.T) {
	r := NewRouter()

	const (
		xapiKey     = "sk-ant-apikey-test1"
		googKeyU    = "goog-key-12345-aaa" // X-Goog-Api-Key(大写键)
		googKeyL    = "goog-key-12345-aaa" // x-goog-api-key(小写键同值,验证大小写归一)
		anthKey     = "ant-key-test-12345" // ANTHROPIC_API_KEY
		apiKey      = "apik-key-test-1234" // API_KEY
		bearerToken = "bearer-prio-token12"
		xapiIgnored = "sk-ant-ignored-xx"   // 与 bearerToken 同时存在时应被 Authorization 压过
		shortCred   = "sk-ab"              // len 5 ≤10,过短噪声,不应入键
	)

	wantXapi := "auth:" + authHashHex(t, xapiKey)
	wantGoog := "auth:" + authHashHex(t, googKeyU) // googKeyL 同值
	wantAnth := "auth:" + authHashHex(t, anthKey)
	wantAPI := "auth:" + authHashHex(t, apiKey)
	wantBearer := "auth:" + authHashHex(t, bearerToken)

	mkReq := func(headers map[string]string, remoteAddr string) *http.Request {
		req, _ := http.NewRequest("POST", "http://example.com", nil)
		req.RemoteAddr = remoteAddr
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		return req
	}

	tests := []struct {
		name     string
		headers  map[string]string
		remote   string
		expected string
	}{
		{
			name:     "X-Api-Key 单独 → auth:<hash>(Anthropic SDK 直连修复场景)",
			headers:  map[string]string{"X-Api-Key": xapiKey},
			remote:   "172.16.10.123:50000",
			expected: wantXapi,
		},
		{
			name:     "X-Goog-Api-Key(大写键)→ auth:<hash>(Google 风格)",
			headers:  map[string]string{"X-Goog-Api-Key": googKeyU},
			remote:   "172.16.10.123:50000",
			expected: wantGoog,
		},
		{
			name:     "x-goog-api-key(小写键)→ 与大写键同 hash(大小写归一)",
			headers:  map[string]string{"x-goog-api-key": googKeyL},
			remote:   "172.16.10.123:50000",
			expected: wantGoog,
		},
		{
			name:     "ANTHROPIC_API_KEY(分发客户端自定义头)→ auth:<hash>",
			headers:  map[string]string{"ANTHROPIC_API_KEY": anthKey},
			remote:   "172.16.10.123:50000",
			expected: wantAnth,
		},
		{
			name:     "API_KEY(分发客户端自定义头)→ auth:<hash>",
			headers:  map[string]string{"API_KEY": apiKey},
			remote:   "172.16.10.123:50000",
			expected: wantAPI,
		},
		{
			name:     "Authorization Bearer + X-Api-Key 同存 → Authorization 优先级压过 X-Api-Key",
			headers:  map[string]string{"Authorization": "Bearer " + bearerToken, "X-Api-Key": xapiIgnored},
			remote:   "172.16.10.123:50000",
			expected: wantBearer,
		},
		{
			name:     "凭证头过短(len≤10)→ 不入键,回退 sock:<host>",
			headers:  map[string]string{"X-Api-Key": shortCred},
			remote:   "172.16.10.123:50000",
			expected: "sock:172.16.10.123",
		},
		{
			name:     "全部凭证全空 → 既有 sock 兜底零回归",
			headers:  map[string]string{},
			remote:   "172.16.10.123:50000",
			expected: "sock:172.16.10.123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := mkReq(tt.headers, tt.remote)
			got := r.ExtractSessionKey(req, nil)
			if got != tt.expected {
				t.Fatalf("expected %q, got %q", tt.expected, got)
			}
			// 额外断言:走 auth 分支的键必须是 "auth:<16hex>" 形态,杜绝短凭证噪声串入。
			if strings.HasPrefix(got, "auth:") {
				if len(got) != len("auth:")+16 {
					t.Errorf("auth 前缀键长度异常: got %q (want auth:+16hex)", got)
				}
			}
		})
	}
}

func TestExtractSessionKey_AntigravityAndFilter(t *testing.T) {
	r := NewRouter()
	const bearer = "bearer-test-token-123456"
	wantBase := "auth:" + authHashHex(t, bearer)

	mkReq := func() *http.Request {
		req, _ := http.NewRequest("POST", "http://example.com/v1internal:streamGenerateContent", nil)
		req.Header.Set("Authorization", "Bearer "+bearer)
		return req
	}

	t.Run("Antigravity原生请求: 同时含静态长整型sessionId与真实agent/uuid, 优先提取agent/uuid", func(t *testing.T) {
		reqBody := []byte(`{
			"model": "gemini-3.8-flash-high",
			"project": "favorite-synapse-37587653",
			"sessionId": "-3750763034362895579",
			"requestId": "agent/1c4fb43a-7893-4ded-9360-e3c559eb66a2/1791304784640/cbc95a4f-8f0a-4596-9772-ac8eaf84dc1c/154"
		}`)
		got := r.ExtractSessionKey(mkReq(), reqBody)
		want := wantBase + ":1c4fb43a"
		if got != want {
			t.Fatalf("expected %q, got %q (不能被 -37507630 截胡)", want, got)
		}
	})

	t.Run("本地Brain工作区路径: 缺少requestId但请求体包含brain/uuid, 精确提取会话UUID", func(t *testing.T) {
		reqBody := []byte(`{
			"model": "gemini-3.8-flash-high",
			"systemInstruction": "Workspace path: C:/Users/user/.gemini/antigravity/brain/44e60633-1a26-4685-8581-e4fd22efed1d/scratch"
		}`)
		got := r.ExtractSessionKey(mkReq(), reqBody)
		want := wantBase + ":44e60633"
		if got != want {
			t.Fatalf("expected %q, got %q", want, got)
		}
	})

	t.Run("纯数字静态遥测sessionId: 过滤忽略, 回退baseKey, 避免误聚合全局会话", func(t *testing.T) {
		reqBody := []byte(`{
			"sessionId": "-3750763034362895579"
		}`)
		got := r.ExtractSessionKey(mkReq(), reqBody)
		if got != wantBase {
			t.Fatalf("expected base key %q, got %q", wantBase, got)
		}
	})

	t.Run("第三方合法UUID格式sessionId: 正常提取前8位", func(t *testing.T) {
		reqBody := []byte(`{
			"sessionId": "b47ac10b-58cc-4372-a567-0e02b2c3d479"
		}`)
		got := r.ExtractSessionKey(mkReq(), reqBody)
		want := wantBase + ":b47ac10b"
		if got != want {
			t.Fatalf("expected %q, got %q", want, got)
		}
	})
}

