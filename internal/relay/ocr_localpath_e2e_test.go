package relay

// ocr_localpath_e2e_test.go —— L2.5 裸本地图片路径自愈的端到端集成测试。
//
// 覆盖三条入站链路的端到端契约:
//  1. Anthropic 入站 → handleNvidia(#1 接入点):text 块裸路径经预扫读图 OCR 注入,
//     送 NVIDIA 上游的 OpenAI Chat body content 全为 string、含 OCR 文本、无 image_url;
//  2. OpenAI Chat 入站 → handleRoutedForward(#3 接入点):string 形态 content 的裸路径经预扫注入,
//     送上游 body content 含 OCR 文本;
//  3. 远程会话守卫:#1 入站换用持久化 API Key 会话(APIKeyID=真实 key.ID),即便 text 含本机
//     真实图片路径也不读图(上游 body 原样保留路径串,无 OCR 文本),证明 LFI 守卫生效。
//
// 复用 nvidia_image_test.go 的 ocrFlashServer / newNvidiaTestHandler / nvidiaChatUpstreamWithImageAssertion /
// mkNvidiaAccount / bytesReader / parseOpenAIMessages / anyImageURL / firstUserContentString 脚手架,
// 与现有 image 块降级 E2E 同款口径,保证两条链路行为对齐。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/settings"
)

// localpathE2EImage 在测试临时目录写一张真实 1x1 PNG,返回绝对路径。
func localpathE2EImage(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "e2e_shot.png")
	if err := os.WriteFile(p, decodeFakePNGBytes(t), 0644); err != nil {
		t.Fatalf("write e2e image: %v", err)
	}
	return p
}

// localpathNvidiaUpstream 构造一个 mock NVIDIA 上游 /v1/chat/completions,捕获入站 body,
// 回包最小合法 OpenAI Chat 非流式响应。供 handleNvidia E2E 用。
func localpathNvidiaUpstream(t *testing.T, captured *[]byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := readAllSafe(r)
		*captured = body
		resp := &OpenAIChatResponse{
			ID: "chatcmpl-lp", Model: "z-ai/glm-5.2",
			Choices: []OpenAIChatChoice{{Index: 0, Message: ChatMessage{Role: "assistant", Content: "已看到截图"}, FinishReason: "stop"}},
			Usage:   OpenAIChatUsage{PromptTokens: 10, CompletionTokens: 3, TotalTokens: 13},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

// readAllSafe 安全读取 r.Body(避免在 e2e helper 直接引 io 包名冲突,统一封装)。
func readAllSafe(r *http.Request) ([]byte, error) {
	return readBodyAll(r.Body)
}

// TestE2E_LocalPath_AnthropicInbound_Nvidia 锁定 #1 接入点(Anthropic→NVIDIA):
// 入站 text 块含本机真实图片绝对路径 + 紧贴中文,Claude Code 客户端因 model 不可识别已剔除
// image 块(此处直接发 text 块)。handleNvidia 入口的 EnrichLocalImagePathsInAnthropic 先扫 text
// 裸路径读图 OCR 注入 descHeader,再交 DowngradeAnthropicImagesToText(无 image 块,零动作)与
// AnthropicToOpenAIChat。上游 NVIDIA 收到的 OpenAI Chat body:user content 全为 string、含 OCR
// 文本、无 image_url;尾随 CJK 保留。
func TestE2E_LocalPath_AnthropicInbound_Nvidia(t *testing.T) {
	ocr := ocrFlashServer(t, "E2E-ANTH-OCR:报错信息 ERROR 404", http.StatusOK)
	defer ocr.Close()
	origAddr := localProxyAddr
	localProxyAddr = strings.TrimPrefix(ocr.URL, "http://")
	t.Cleanup(func() { localProxyAddr = origAddr })

	var upstreamBody []byte
	upstream := localpathNvidiaUpstream(t, &upstreamBody)
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-lp1", "nv-lp1@x.cloud", "k", upstream.URL, "z-ai/glm-5.2")
	handler, _, _, _ := newNvidiaTestHandler(t, []*account.Account{acc})

	imgPath := localpathE2EImage(t)
	// 紧贴中文:前缀 '图' + 后缀 '你看',prove 紧贴命中 + 尾随 CJK 保留。
	anthReq := &AnthropicRequest{
		Model:     "claude-sonnet-4-5",
		MaxTokens: func() *int { v := 200; return &v }(),
		Messages: []AnthropicMessage{{
			Role: "user",
			Content: []AnthropicContent{
				{Type: "text", Text: "图" + imgPath + "你看"},
			},
		}},
	}
	body, _ := json.Marshal(anthReq)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytesReader(body))
	rr := httptest.NewRecorder()
	// 本地自用直连会话(APIKeyID=official_bypass),放行本功能。
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u-lp1", UserKey: "k1", APIKeyID: APIKeyIDOfficialBypass})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if len(upstreamBody) == 0 {
		t.Fatal("upstream never called")
	}
	msgs := parseOpenAIMessages(t, upstreamBody)
	if anyImageURL(msgs) {
		t.Errorf("upstream MUST NOT contain image_url: %s", string(upstreamBody))
	}
	// content 全 string(降级后无数组型 content)。
	for _, m := range msgs {
		if _, ok := m["content"].(string); !ok {
			t.Errorf("upstream content not string: %v", m["content"])
		}
	}
	concat := ""
	for _, m := range msgs {
		if m["role"] == "user" {
			if s, ok := m["content"].(string); ok {
				concat += s
			}
		}
	}
	if !strings.Contains(concat, "本地中继服务已自动调用") || !strings.Contains(concat, "ERROR 404") {
		t.Errorf("OCR text not in upstream user content: %s", concat)
	}
	// 尾随 CJK '你看' 应保留。
	if !strings.Contains(concat, "你看") {
		t.Errorf("trailing CJK 你看 should be preserved in upstream content: %s", concat)
	}
	// 前缀 '图' 也应保留。
	if !strings.Contains(concat, "图") {
		t.Errorf("leading 图 should be preserved in upstream content: %s", concat)
	}
}

// TestE2E_LocalPath_AnthropicInbound_NonLocalSessionBlocked 锁定远程守卫:
// 同样入站(Anthropic text 块含本机真实图片路径),会话换成持久化 API Key 来源
// (APIKeyID=真实 key.ID),handleNvidia 入口的 isLocalDirectSession 闸短路 → 不读图,
// 上游 NVIDIA 收到的 body 原样保留路径串、无 OCR 文本(LFI 堵死)。
func TestE2E_LocalPath_AnthropicInbound_NonLocalSessionBlocked(t *testing.T) {
	ocr := ocrFlashServer(t, "不应被打到", http.StatusOK)
	defer ocr.Close()
	origAddr := localProxyAddr
	localProxyAddr = strings.TrimPrefix(ocr.URL, "http://")
	t.Cleanup(func() { localProxyAddr = origAddr })

	var upstreamBody []byte
	upstream := localpathNvidiaUpstream(t, &upstreamBody)
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-lp2", "nv-lp2@x.cloud", "k", upstream.URL, "z-ai/glm-5.2")
	handler, _, _, _ := newNvidiaTestHandler(t, []*account.Account{acc})

	imgPath := localpathE2EImage(t)
	anthReq := &AnthropicRequest{
		Model:     "claude-sonnet-4-5",
		MaxTokens: func() *int { v := 200; return &v }(),
		Messages: []AnthropicMessage{{
			Role: "user",
			Content: []AnthropicContent{
				{Type: "text", Text: "图" + imgPath + "你看"},
			},
		}},
	}
	body, _ := json.Marshal(anthReq)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytesReader(body))
	rr := httptest.NewRecorder()
	// 远程外部中继用户:后台持久化 API Key 来源。OCR mock 的鉴权头断言(Bearer k1)在此会话下
	// 仍为 k1,但 ocrSelf 闸外层短路不会真打 OCR;即便误打也会因本闸而 None。
	handler.handleNvidia(rr, req, &RelaySession{UserID: "remote-u", UserKey: "k1", APIKeyID: "real-key-id-xyz"})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	concat := ""
	msgs := parseOpenAIMessages(t, upstreamBody)
	for _, m := range msgs {
		if m["role"] == "user" {
			if s, ok := m["content"].(string); ok {
				concat += s
			}
		}
	}
	// 不读图:OCR 文本不应出现。
	if strings.Contains(concat, "本地中继服务已自动调用") || strings.Contains(concat, "不应被打到") {
		t.Errorf("non-local session must NOT trigger local path OCR: %s", concat)
	}
	// 原路径串应原样保留(未被替换)。
	if !strings.Contains(concat, filepath.Base(imgPath)) {
		t.Errorf("non-local session should keep raw path string in content: %s", concat)
	}
}

// TestE2E_LocalPath_OpenAIChatInbound_Route 锁定 #3 接入点(OpenAI Chat→/route):
// 入站 OpenAI Chat 的 string 形态 content 里含本机真实图片路径,handleRoutedForward 入口的
// passthroughForward 调 EnrichLocalImagePathsInOpenAIChat 先扫注入,再交 DowngradeOpenAIChatImagesToText
// (无 image 块零动作)与上游。上游 body:user content 含 OCR 文本、无 image_url、尾随 CJK 保留。
func TestE2E_LocalPath_OpenAIChatInbound_Route(t *testing.T) {
	ocr := ocrFlashServer(t, "E2E-ROUTE-OCR:截图说明", http.StatusOK)
	defer ocr.Close()
	origAddr := localProxyAddr
	localProxyAddr = strings.TrimPrefix(ocr.URL, "http://")
	t.Cleanup(func() { localProxyAddr = origAddr })

	var upstreamBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := readAllSafe(r)
		upstreamBody = body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"chat.completion","choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer upstream.Close()

	mgr := account.NewManager()
	mgr.AddAccount(&account.Account{
		ID: "ds-lp", Email: "ds-lp@pool", Provider: "deepseek",
		AccessToken: "test-key-1", BaseURL: upstream.URL, Enabled: true, Cooldowns: map[string]int64{},
	})

	h := &APICompatHandler{
		accountMgr:  mgr,
		settingsMgr: &settingsAdapterWithOcr{stubPassThroughSettings: &stubPassThroughSettings{routes: []settings.ModelRouteRule{{Pattern: "deepseek-*", TargetProvider: "deepseek", Priority: 100, Enabled: true}}}},
		logFn:       func(string) {},
		client:      &http.Client{Timeout: 5 * 1000 * 1000 * 1000},
	}
	h.streamClient = h.client
	h.ocr = NewOCRService(h.settingsMgr, h.client, func(s string) { h.log("%s", s) })

	imgPath := localpathE2EImage(t)
	escPath := strings.ReplaceAll(imgPath, `\`, `\\`)
	// string 形态 content + 紧贴中文。
	in := []byte(`{"model":"deepseek-chat","messages":[{"role":"user","content":"看这张 ` + escPath + ` 图"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/route/v1/chat/completions", strings.NewReader(string(in)))
	rr := httptest.NewRecorder()
	// 本地自用直连会话,放行。
	h.handleRoutedForward(rr, req, &RelaySession{UserKey: "k1", UserID: "u1", APIKeyID: APIKeyIDOfficialBypass})

	resp := rr.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, rr.Body.String())
	}
	if len(upstreamBody) == 0 {
		t.Fatal("upstream never called")
	}
	msgs := parseOpenAIMessages(t, upstreamBody)
	if anyImageURL(msgs) {
		t.Errorf("upstream MUST NOT contain image_url: %s", string(upstreamBody))
	}
	concat := ""
	for _, m := range msgs {
		if m["role"] == "user" {
			if s, ok := m["content"].(string); ok {
				concat += s
			}
		}
	}
	if !strings.Contains(concat, "本地中继服务已自动调用") || !strings.Contains(concat, "截图说明") {
		t.Errorf("OCR text not in upstream content: %s", concat)
	}
	if !strings.Contains(concat, "图") {
		t.Errorf("trailing CJK should be preserved: %s", concat)
	}
}

// readBodyAll 复用 relay 包内已有的 body 读取工具(若不存在则退化到 io.ReadAll)。
// 定义在 e2e 测试文件本地,避免跨文件符号依赖。
func readBodyAll(body interface{ Read(p []byte) (int, error) }) ([]byte, error) {
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, err := body.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			if err.Error() == "EOF" {
				return buf, nil
			}
			return buf, err
		}
	}
}
