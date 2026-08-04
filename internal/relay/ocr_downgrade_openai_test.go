package relay

// ocr_downgrade_openai_test.go —— 锁定 OpenAI Chat (image_url) 入站图片本地 OCR 降级契约。
//
// 覆盖 P3 新增能力:
//   - DowngradeOpenAIChatImagesToText: 无图原样返回 / string content 不动 / 数组无图不动 /
//     数组含 image_url(base64 / Data URL / 网络 URL 三态)→ content 重写为纯字符串含 OCR 文本;
//   - 窗口语义:窗内 cache miss 真打上游,窗外仅查缓存未命中走占位;
//   - 端到端 passthroughForward:含 image_url 的入站经降级后送上游的 body content 全为 string、无 image_url。

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/settings"
)

// newOpenAIDowngradeHandler 复用 newImageTestHandler 的 localProxyAddr 覆盖法,
// 构造 client 路由到 ocrMock 的 handler,供 OpenAI 降级单测使用。
func newOpenAIDowngradeHandler(t *testing.T, ocrMock *httptest.Server) *APICompatHandler {
	return newImageTestHandler(t, ocrMock)
}

// ocrBase64Block 构造一个 OpenAI Chat image_url 块(base64 直存)。
func ocrBase64Block(b64 string) map[string]interface{} {
	return map[string]interface{}{
		"type": "image_url",
		"image_url": map[string]interface{}{
			"url": "data:image/png;base64," + b64,
		},
	}
}

// ocrHTTPURLBlock 构造一个 OpenAI Chat image_url 块(网络 URL)。
func ocrHTTPURLBlock(u string) map[string]interface{} {
	return map[string]interface{}{
		"type": "image_url",
		"image_url": map[string]interface{}{
			"url": u,
		},
	}
}

// ocrTextBlock 构造一个 OpenAI Chat text 块。
func ocrTextBlock(text string) map[string]interface{} {
	return map[string]interface{}{"type": "text", "text": text}
}

// buildOpenAIChatBody 构造一个 OpenAI Chat 请求体(messages[].content 为数组形态)。
func buildOpenAIChatBody(model string, contentArrays ...[]map[string]interface{}) []byte {
	msgs := make([]map[string]interface{}, 0, len(contentArrays))
	for i, parts := range contentArrays {
		msgs = append(msgs, map[string]interface{}{
			"role":    pickRole(i),
			"content": parts,
		})
	}
	obj := map[string]interface{}{"model": model, "messages": msgs}
	b, _ := json.Marshal(obj)
	return b
}

// pickRole 给第 i 条消息一个交替的 role(user/assistant),只是让 messages 多样化。
func pickRole(i int) string {
	if i%2 == 0 {
		return "user"
	}
	return "assistant"
}

// settingsAdapterWithOcr 包一层 stubPassThroughSettings 并补上 GetOcrModel,
// 让 OCRService.getOcrModel 走默认模型兜底链路(pkg relay 内 OCR 单测常规口径)。
type settingsAdapterWithOcr struct {
	*stubPassThroughSettings
}

func (s *settingsAdapterWithOcr) GetOcrModel() string { return "" }

// firstUserContentStringFromOpenAI 取降级后 body 里第一条 user content 的字符串值。
func firstUserContentStringFromOpenAI(t *testing.T, body []byte) string {
	t.Helper()
	var obj map[string]interface{}
	if err := json.Unmarshal(body, &obj); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, string(body))
	}
	msgs, ok := obj["messages"].([]interface{})
	if !ok || len(msgs) == 0 {
		t.Fatalf("no messages: %s", string(body))
	}
	for _, m := range msgs {
		mm := m.(map[string]interface{})
		if mm["role"] != "user" {
			continue
		}
		if s, ok := mm["content"].(string); ok {
			return s
		}
		t.Fatalf("user content not string after downgrade: %T -> %s", mm["content"], string(body))
	}
	t.Fatalf("no user message: %s", string(body))
	return ""
}

// TestDowngradeOpenAIChatImagesToText_NoMessagesBody 无 messages 字段的原样返回。
func TestDowngradeOpenAIChatImagesToText_NoMessagesBody(t *testing.T) {
	h := NewAPICompatHandler(nil, nil, nil, nil, nil, nil, nil)
	in := []byte(`{"model":"deepseek-chat","input":[]}`)
	out, replaced, _, _, _, _ := h.ocr.DowngradeOpenAIChatImagesToText(in, &RelaySession{UserID: "u1", UserKey: "k1"})
	if string(out) != string(in) {
		t.Errorf("expected passthrough for body without messages")
	}
	if replaced != 0 {
		t.Errorf("replaced want 0 got %d", replaced)
	}
}

// TestDowngradeOpenAIChatImagesToText_StringContentUntouched content 为 string 的消息原样不动。
func TestDowngradeOpenAIChatImagesToText_StringContentUntouched(t *testing.T) {
	h := NewAPICompatHandler(nil, nil, nil, nil, nil, nil, nil)
	in := []byte(`{"model":"deepseek-chat","messages":[{"role":"user","content":"hi there"}]}`)
	out, replaced, _, _, _, _ := h.ocr.DowngradeOpenAIChatImagesToText(in, &RelaySession{UserID: "u1", UserKey: "k1"})
	if string(out) != string(in) {
		t.Errorf("string content should be untouched: %s", string(out))
	}
	if replaced != 0 {
		t.Errorf("replaced want 0 got %d", replaced)
	}
}

// TestDowngradeOpenAIChatImagesToText_ArrayNoImageUntouched 数组形态但无图片块的原样不动。
func TestDowngradeOpenAIChatImagesToText_ArrayNoImageUntouched(t *testing.T) {
	h := NewAPICompatHandler(nil, nil, nil, nil, nil, nil, nil)
	in := buildOpenAIChatBody("deepseek-chat", []map[string]interface{}{ocrTextBlock("a"), ocrTextBlock("b")})
	out, replaced, _, _, _, _ := h.ocr.DowngradeOpenAIChatImagesToText(in, &RelaySession{UserID: "u1", UserKey: "k1"})
	if string(out) != string(in) {
		t.Errorf("array-without-image should be untouched:\nin=%s\nout=%s", in, out)
	}
	if replaced != 0 {
		t.Errorf("replaced want 0 got %d", replaced)
	}
}

// TestDowngradeOpenAIChatImagesToText_Base64DataURL base64 图 block → OCR 成功 → content 变纯字符串含 OCR 文本、无 image_url。
func TestDowngradeOpenAIChatImagesToText_Base64DataURL(t *testing.T) {
	ocr := ocrFlashServer(t, "图识别:OPENAI-B64-OK", http.StatusOK)
	defer ocr.Close()
	h := newOpenAIDowngradeHandler(t, ocr)

	in := buildOpenAIChatBody("deepseek-chat", []map[string]interface{}{
		ocrTextBlock("[Image #1] 看这个报错"),
		ocrBase64Block(fakeNvidiaImageB64),
	})
	out, replaced, err, _, _, _ := h.ocr.DowngradeOpenAIChatImagesToText(in, &RelaySession{UserID: "u1", UserKey: "k1"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if replaced != 1 {
		t.Fatalf("replaced want 1 got %d", replaced)
	}
	// content 必须已是 string 形态(不再是数组),且含 OCR 文本、不含 image_url / data: 残留。
	c := firstUserContentStringFromOpenAI(t, out)
	if !strings.Contains(c, "OPENAI-B64-OK") {
		t.Errorf("OCR text missing in content: %s", c)
	}
	if strings.Contains(c, "image_url") {
		t.Errorf("image_url leaked into content: %s", c)
	}
	if strings.Contains(c, "[Image #1]") {
		// 原文文本应被合并保留
	}
	if !strings.Contains(c, "[Image #1]") {
		t.Errorf("original text block should be merged: %s", c)
	}
	// 上游可消化性校验:降级后 body 能被 OpenAIChatRequest 的 Unmarshal 成功吃下(content 为 string)。
	var chatReq OpenAIChatRequest
	if err := json.Unmarshal(out, &chatReq); err != nil {
		t.Fatalf("downgraded body must unmarshal into OpenAIChatRequest: %v", err)
	}
}

// TestDowngradeOpenAIChatImagesToText_OCRFail_Placeholder OCR 不可用 → 占位文本,不阻断。
func TestDowngradeOpenAIChatImagesToText_OCRFail_Placeholder(t *testing.T) {
	ocr := ocrFlashServer(t, "", http.StatusServiceUnavailable)
	defer ocr.Close()
	h := newOpenAIDowngradeHandler(t, ocr)

	in := buildOpenAIChatBody("deepseek-chat", []map[string]interface{}{
		ocrTextBlock("看图"),
		ocrBase64Block(fakeNvidiaImageB64),
	})
	out, replaced, _, _, _, _ := h.ocr.DowngradeOpenAIChatImagesToText(in, &RelaySession{UserID: "u1", UserKey: "k1"})
	if replaced != 0 {
		t.Errorf("replaced want 0 on OCR failure got %d", replaced)
	}
	c := firstUserContentStringFromOpenAI(t, out)
	if !strings.Contains(c, imageNotExtractablePlaceholder) {
		t.Errorf("placeholder missing: %s", c)
	}
	if !strings.Contains(c, "看图") {
		t.Errorf("original text must be preserved: %s", c)
	}
	// 上游可消化性校验。
	var chatReq OpenAIChatRequest
	if err := json.Unmarshal(out, &chatReq); err != nil {
		t.Fatalf("downgraded body must unmarshal: %v", err)
	}
}

// TestDowngradeOpenAIChatImagesToText_HTTPURLAndCacheReuse 网络 URL 图下载→OCR→第二轮命中缓存免下载。
func TestDowngradeOpenAIChatImagesToText_HTTPURLAndCacheReuse(t *testing.T) {
	enableSSRFLoopbackForTest(t)
	pngBytes := decodeFakePNGBytes(t)
	// 下载命中计数:统计图床被请求的次数。第二轮应命中 urlCache + ocrCache,不再触达图床。
	fetchHits := 0
	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetchHits++
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngBytes)
	}))
	defer imgSrv.Close()

	ocr := ocrFlashServer(t, "图中识别:OPENAI-URL-OK", http.StatusOK)
	defer ocr.Close()
	h := newOpenAIDowngradeHandler(t, ocr)

	in := buildOpenAIChatBody("deepseek-chat", []map[string]interface{}{
		ocrHTTPURLBlock(imgSrv.URL),
	})
	out, replaced, err, _, _, _ := h.ocr.DowngradeOpenAIChatImagesToText(in, &RelaySession{UserID: "u1", UserKey: "k1"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if replaced != 1 {
		t.Fatalf("replaced want 1 got %d", replaced)
	}
	c := firstUserContentStringFromOpenAI(t, out)
	if !strings.Contains(c, "OPENAI-URL-OK") {
		t.Errorf("OCR text missing: %s", c)
	}
	if fetchHits != 1 {
		t.Errorf("first round should fetch image exactly once, got %d", fetchHits)
	}

	// 第二轮同 URL 同会话:命中 urlCache + ocrCache,图床零触达、OCR 上游零触达。
	in2 := buildOpenAIChatBody("deepseek-chat", []map[string]interface{}{
		ocrHTTPURLBlock(imgSrv.URL),
	})
	out2, replaced2, err2, _, _, _ := h.ocr.DowngradeOpenAIChatImagesToText(in2, &RelaySession{UserID: "u1", UserKey: "k1"})
	if err2 != nil {
		t.Fatalf("unexpected err round2: %v", err2)
	}
	if replaced2 != 1 {
		t.Fatalf("round2 replaced want 1 got %d", replaced2)
	}
	c2 := firstUserContentStringFromOpenAI(t, out2)
	if !strings.Contains(c2, "OPENAI-URL-OK") {
		t.Errorf("round2 OCR text missing: %s", c2)
	}
	if fetchHits != 1 {
		t.Errorf("round2 should reuse cache, fetch should stay 1, got %d", fetchHits)
	}
}

// TestDowngradeOpenAIChatImagesToText_OutOfWindow_PlaceholderOnly 窗外历史 URL 图仅查缓存,
// 未命中 → 占位文本兜底,绝不真打上游(省配额)。
func TestDowngradeOpenAIChatImagesToText_OutOfWindow_PlaceholderOnly(t *testing.T) {
	enableSSRFLoopbackForTest(t)
	pngBytes := decodeFakePNGBytes(t)
	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("out-of-window image must NOT be downloaded")
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngBytes)
	}))
	defer imgSrv.Close()

	// OCR 上游若被触达则报错(窗外不应真打)。
	ocr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("out-of-window image must NOT trigger OCR upstream")
		w.WriteHeader(http.StatusOK)
	}))
	defer ocr.Close()
	h := newOpenAIDowngradeHandler(t, ocr)

	// 构造 > ocrRecentWindowMessages 条消息,末条 user 含 URL 图(落在窗外)。
	// windowStart = msgCount - ocrRecentWindowMessages。让含图消息 index < windowStart。
	const extras = ocrRecentWindowMessages + 1
	contentArrays := make([][]map[string]interface{}, 0, extras)
	// 前 (extras-1) 条纯文本(占位以填充窗口外)。
	for i := 0; i < extras-1; i++ {
		contentArrays = append(contentArrays, []map[string]interface{}{ocrTextBlock("filler " + pickRole(i))})
	}
	// 最后一条?不——要让目标图在窗外,需把图放在开头。结构:首条含图,后跟 fillers 使窗口覆盖尾部,
	// 即 fillers 在窗口内、图在窗口外。
	// 重新构造:第 0 条含图,后续 extras 条纯文本 → msgCount = extras+1, windowStart = 1, 图 index=0 在窗外。
	contentArrays = nil
	contentArrays = append(contentArrays, []map[string]interface{}{ocrTextBlock("早期图前文本"), ocrHTTPURLBlock(imgSrv.URL)})
	for i := 0; i < extras; i++ {
		contentArrays = append(contentArrays, []map[string]interface{}{ocrTextBlock("filler " + pickRole(i))})
	}

	in := buildOpenAIChatBody("deepseek-chat", contentArrays...)
	out, replaced, err, _, skipped, _ := h.ocr.DowngradeOpenAIChatImagesToText(in, &RelaySession{UserID: "u1", UserKey: "k1"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if replaced != 0 {
		t.Errorf("out-of-window URL image should not be replaced (placeholder only), got replaced=%d", replaced)
	}
	// 与 Anthropic 链路一致:窗外 URL 未在 urlCache 中 → resolveB64NoFetch 返回 ok=false →
	// 走占位但不计 ocrSkipped(ocrSkipped 仅在 b64 可得但 OCR 缓存未命中时自增)。
	if skipped != 0 {
		t.Errorf("out-of-window URL not in urlCache should be skipped=0 (placeholder only), got %d", skipped)
	}
	// 第一条消息(含图)的 content 应已变成纯字符串,含占位文本 + 早期图前文本。
	var obj map[string]interface{}
	_ = json.Unmarshal(out, &obj)
	msgs := obj["messages"].([]interface{})
	first := msgs[0].(map[string]interface{})
	c, ok := first["content"].(string)
	if !ok {
		t.Fatalf("first message content should be string, got %T", first["content"])
	}
	if !strings.Contains(c, imageNotExtractablePlaceholder) {
		t.Errorf("placeholder missing: %s", c)
	}
	if !strings.Contains(c, "早期图前文本") {
		t.Errorf("original text must be preserved: %s", c)
	}
}

// TestDowngradeOpenAIChatImagesToText_InvalidJSONInvalidBody body 非 JSON 对象时原样返回,不阻断。
func TestDowngradeOpenAIChatImagesToText_InvalidJSONInvalidBody(t *testing.T) {
	h := NewAPICompatHandler(nil, nil, nil, nil, nil, nil, nil)
	in := []byte(`not-json`)
	out, replaced, _, _, _, _ := h.ocr.DowngradeOpenAIChatImagesToText(in, &RelaySession{UserID: "u1", UserKey: "k1"})
	if string(out) != string(in) {
		t.Errorf("invalid body should be passed through untouched")
	}
	if replaced != 0 {
		t.Errorf("replaced want 0 got %d", replaced)
	}
}

// TestDowngradeOpenAIChatImagesToText_PassthroughForwardE2E 端到端:含 image_url 的入站经 handleRoutedForward
// 降级后送上游的 body content 全为 string、无 image_url、含 OCR 文本。
func TestDowngradeOpenAIChatImagesToText_PassthroughForwardE2E(t *testing.T) {
	enableSSRFLoopbackForTest(t)
	pngBytes := decodeFakePNGBytes(t)

	// 上游(模拟 DeepSeek)收到请求后解析 messages,断言 content 全为 string、无 image_url、含 OCR 文本。
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		msgs := parseOpenAIMessages(t, body)
		if anyImageURL(msgs) {
			t.Errorf("upstream MUST NOT receive image_url: %s", string(body))
		}
		// 第一条 user content 应是 string 且含 OCR 文本。
		c, ok := firstUserContentString(msgs)
		if !ok {
			t.Fatalf("upstream user content not string: %s", string(body))
		}
		if !strings.Contains(c, "PS-OCR-OK") {
			t.Errorf("upstream user content missing OCR text: %s", c)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"chat.completion","choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer upstream.Close()

	// 图床(被下载一次)。
	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngBytes)
	}))
	defer imgSrv.Close()

	// OCR 上游。
	ocr := ocrFlashServer(t, "PS-OCR-OK", http.StatusOK)
	defer ocr.Close()
	origAddr := localProxyAddr
	localProxyAddr = strings.TrimPrefix(ocr.URL, "http://")
	t.Cleanup(func() { localProxyAddr = origAddr })

	mgr := account.NewManager()
	mgr.AddAccount(&account.Account{
		ID:          "ds-1", Email: "ds-1@pool", Provider: "deepseek",
		AccessToken: "test-key-1", BaseURL: upstream.URL,
		Enabled: true, Cooldowns: map[string]int64{},
	})

	h := &APICompatHandler{
		accountMgr:  mgr,
		settingsMgr: &settingsAdapterWithOcr{stubPassThroughSettings: &stubPassThroughSettings{routes: []settings.ModelRouteRule{{Pattern: "deepseek-*", TargetProvider: "deepseek", Priority: 100, Enabled: true}}}},
		logFn:       func(string) {},
		client:      &http.Client{Timeout: 5 * 1000 * 1000 * 1000},
	}
	h.streamClient = h.client
	// 注入与 NewOCRService 等价的 OCR 服务(logFn 适配签名差异)。
	h.ocr = NewOCRService(h.settingsMgr, h.client, func(s string) { h.log("%s", s) })

	in := buildOpenAIChatBody("deepseek-chat", []map[string]interface{}{
		ocrTextBlock("这是个报错截图"),
		ocrHTTPURLBlock(imgSrv.URL),
	})
	req := httptest.NewRequest(http.MethodPost, "/route/v1/chat/completions", strings.NewReader(string(in)))
	w := httptest.NewRecorder()
	h.handleRoutedForward(w, req, &RelaySession{UserKey: "k1", UserID: "u1"})

	resp := w.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, w.Body.String())
	}
}
