package relay

// nvidia_image_test.go —— 锁定 NVIDIA 入站 Anthropic image block 的本地 OCR 自愈降级。
//
// 背景:NVIDIA 上游(glm-5.2 等 NIM 模型)不支持多模态,Claude Code 入站的 Anthropic
// image content block(base64)曾因 anthropicUserToChat 的 default 分支被吞成空串、
// 上游模型完全感知不到图而拒识澄清。方案 A' 在 handleNvidia 转 OpenAI 前先用本地
// Gemini(gemini-2.5-flash)把每张图 OCR 成纯文本,把 image 块原地改写为 text 块,
// 上游段永远只见 text、永远零负担。本文件测试覆盖:
//   - AnthropicContent 能否正确反序列化 image block 的 source 字段(单元)
//   - downgradeAnthropicImagesToText 在 OCR 成功/失败/无图/多图混合 各场景的行为(单元)
//   - handleNvidia 端到端:入站含 image 的 Anthropic 请求被降级后,送上游的 OpenAI Chat
//     请求 messages[].content 全是 string、无 image_url、含 OCR 识别文本(端到端)
//   - handleNvidia 在 OCR 服务不可达时仍把图改写成占位文本、不阻断主请求(端到端韧性)

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/settings"
)

// fakeNvidiaImageB64 是一张 1x1 PNG 的 base64,仅用于占位触发降级分支,OCR mock 不关心内容。
const fakeNvidiaImageB64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII="

// ocrFlashServer 构造一个 mock 本地 Gemini(gemini-2.5-flash:generateContent)服务,
// 收到任意请求回包 candidates[0].content.parts[0].text = ocrText;若 ocrText 为空则回空 candidates。
func ocrFlashServer(t *testing.T, ocrText string, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "generateContent") {
			t.Errorf("ocr mock unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer k1" {
			t.Errorf("ocr mock missing auth: %s", r.Header.Get("Authorization"))
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			w.Write([]byte(`{"error":"ocr unavailable"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		var resp string
		if ocrText != "" {
			resp = `{"candidates":[{"content":{"parts":[{"text":` + jsonString(ocrText) + `}]}}]}`
		} else {
			resp = `{"candidates":[]}`
		}
		w.Write([]byte(resp))
	}))
}

// jsonString 复用 nvidia_test.go 同名 helper(把字符串编为 JSON 字面量含引号,供手工拼 JSON)。
// var 占位仅为注释锚点;实际函数定义在 nvidia_test.go:1344。

// trim images from a marshaled request: helper to assert downstream payload.
// parseOpenAIMessages 把上游收到的请求体解包成 messages 数组(供断言 content 形态)。
func parseOpenAIMessages(t *testing.T, body []byte) []map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("unmarshal upstream body: %v body=%s", err, string(body))
	}
	raw, ok := m["messages"]
	if !ok {
		t.Fatalf("no messages in upstream body: %s", string(body))
	}
	arr, ok := raw.([]interface{})
	if !ok {
		t.Fatalf("messages not array: %T", raw)
	}
	out := make([]map[string]interface{}, 0, len(arr))
	for _, it := range arr {
		if mm, ok := it.(map[string]interface{}); ok {
			out = append(out, mm)
		}
	}
	return out
}

// anyImageURL 检查上游 messages 里是否出现任何 image_url 块(content 为数组且含 type=image_url)。
// 降级成功后绝不应出现。content 为 string 时直接 false。
func anyImageURL(messages []map[string]interface{}) bool {
	for _, mm := range messages {
		c, ok := mm["content"]
		if !ok {
			continue
		}
		arr, ok := c.([]interface{})
		if !ok {
			continue
		}
		for _, p := range arr {
			if pp, ok := p.(map[string]interface{}); ok {
				if pp["type"] == "image_url" {
					return true
				}
			}
		}
	}
	return false
}

// firstUserContentString 取上游 messages 里第一条 role=user 的 content(string 形态)。
func firstUserContentString(messages []map[string]interface{}) (string, bool) {
	for _, mm := range messages {
		if mm["role"] != "user" {
			continue
		}
		if s, ok := mm["content"].(string); ok {
			return s, true
		}
	}
	return "", false
}

// ===== 1. 单元:AnthropicContent 反序列化 image block =====

func TestAnthropicContent_UnmarshalImageBlock(t *testing.T) {
	raw := `{"type":"image","source":{"type":"base64","media_type":"image/png","data":"ABC"}}`
	var c AnthropicContent
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("unmarshal image block: %v", err)
	}
	if c.Type != "image" {
		t.Errorf("type want image got %s", c.Type)
	}
	if c.Source == nil {
		t.Fatalf("source nil")
	}
	if c.Source.Type != "base64" {
		t.Errorf("source.type want base64 got %s", c.Source.Type)
	}
	if c.Source.MediaType != "image/png" {
		t.Errorf("source.media_type want image/png got %s", c.Source.MediaType)
	}
	if c.Source.Data != "ABC" {
		t.Errorf("source.data want ABC got %s", c.Source.Data)
	}
}

func TestAnthropicContent_UnmarshalTextBlockKeepsSourceNil(t *testing.T) {
	raw := `{"type":"text","text":"hi"}`
	var c AnthropicContent
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("unmarshal text block: %v", err)
	}
	if c.Type != "text" || c.Text != "hi" {
		t.Errorf("text block wrong: %+v", c)
	}
	if c.Source != nil {
		t.Errorf("text block source should be nil, got %+v", c.Source)
	}
}

// ===== 2. 单元:downgradeAnthropicImagesToText =====

// newImageTestHandler 构造一个 handler,其 h.client 会路由到 ocrMock(因为 localProxyAddr 被覆盖)。
// 账号池为空不影响单测(单测直接调 h.downgradeAnthropicImagesToText,不走 handleNvidia 选号)。
func newImageTestHandler(t *testing.T, ocrMock *httptest.Server) *APICompatHandler {
	t.Helper()
	origAddr := localProxyAddr
	localProxyAddr = strings.TrimPrefix(ocrMock.URL, "http://")
	t.Cleanup(func() { localProxyAddr = origAddr })
	return NewAPICompatHandler(nil, nil, nil, nil, nil, nil, nil)
}

func TestDowngradeAnthropicImagesToText_OCROk(t *testing.T) {
	ocr := ocrFlashServer(t, "图中文字:报错信息 ERROR 404", http.StatusOK)
	defer ocr.Close()
	h := newImageTestHandler(t, ocr)

	req := &AnthropicRequest{
		Messages: []AnthropicMessage{{
			Role: "user",
			Content: []AnthropicContent{
				{Type: "text", Text: "[Image #1] 看看这个报错"},
				{Type: "image", Source: &AnthropicImageSource{Type: "base64", MediaType: "image/png", Data: fakeNvidiaImageB64}},
			},
		}},
	}
	replaced, err, _, _, _ := h.ocr.DowngradeAnthropicImagesToText(req, &RelaySession{UserID: "u1", UserKey: "k1"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if replaced != 1 {
		t.Fatalf("replaced want 1 got %d", replaced)
	}
	blocks := req.Messages[0].Content
	if len(blocks) != 2 {
		t.Fatalf("block count changed: %d", len(blocks))
	}
	if blocks[0].Type != "text" || blocks[0].Text != "[Image #1] 看看这个报错" {
		t.Errorf("text block corrupted: %+v", blocks[0])
	}
	if blocks[1].Type != "text" {
		t.Errorf("image block should become text, got type=%s", blocks[1].Type)
	}
	if blocks[1].Source != nil {
		t.Errorf("source should be cleared, got %+v", blocks[1].Source)
	}
	if !strings.Contains(blocks[1].Text, "本地中继服务已自动调用") || !strings.Contains(blocks[1].Text, "ERROR 404") {
		t.Errorf("desc text wrong: %s", blocks[1].Text)
	}
}

func TestDowngradeAnthropicImagesToText_OCRFail(t *testing.T) {
	ocr := ocrFlashServer(t, "", http.StatusServiceUnavailable)
	defer ocr.Close()
	h := newImageTestHandler(t, ocr)

	req := &AnthropicRequest{
		Messages: []AnthropicMessage{{
			Role: "user",
			Content: []AnthropicContent{
				{Type: "image", Source: &AnthropicImageSource{Type: "base64", MediaType: "image/png", Data: fakeNvidiaImageB64}},
			},
		}},
	}
	replaced, err, _, _, _ := h.ocr.DowngradeAnthropicImagesToText(req, &RelaySession{UserID: "u1", UserKey: "k1"})
	if err == nil {
		t.Fatalf("expected err on OCR failure, got nil")
	}
	if replaced != 0 {
		t.Errorf("replaced want 0 on OCR fail, got %d", replaced)
	}
	b := req.Messages[0].Content[0]
	if b.Type != "text" || b.Source != nil {
		t.Errorf("block should be placeholder text: %+v", b)
	}
	if b.Text != imageNotExtractablePlaceholder {
		t.Errorf("placeholder text wrong: %s", b.Text)
	}
}

func TestDowngradeAnthropicImagesToText_OCREmptyCandidates(t *testing.T) {
	ocr := ocrFlashServer(t, "", http.StatusOK) // ocrText=="" → 候选为空
	defer ocr.Close()
	h := newImageTestHandler(t, ocr)

	req := &AnthropicRequest{
		Messages: []AnthropicMessage{{
			Role: "user",
			Content: []AnthropicContent{
				{Type: "image", Source: &AnthropicImageSource{Type: "base64", MediaType: "image/png", Data: fakeNvidiaImageB64}},
			},
		}},
	}
	replaced, _, _, _, _ := h.ocr.DowngradeAnthropicImagesToText(req, &RelaySession{UserID: "u1", UserKey: "k1"})
	if replaced != 0 {
		t.Errorf("replaced want 0 on empty ocr, got %d", replaced)
	}
	if req.Messages[0].Content[0].Type != "text" || req.Messages[0].Content[0].Text != imageNotExtractablePlaceholder {
		t.Errorf("placeholder fallback wrong: %+v", req.Messages[0].Content[0])
	}
}

func TestDowngradeAnthropicImagesToText_NoImage(t *testing.T) {
	ocr := ocrFlashServer(t, "x", http.StatusOK)
	defer ocr.Close()
	h := newImageTestHandler(t, ocr)

	req := &AnthropicRequest{
		Messages: []AnthropicMessage{{
			Role: "user",
			Content: []AnthropicContent{
				{Type: "text", Text: "纯文本消息没图"},
			},
		}},
	}
	replaced, err, _, _, _ := h.ocr.DowngradeAnthropicImagesToText(req, &RelaySession{UserID: "u1", UserKey: "k1"})
	if err != nil || replaced != 0 {
		t.Fatalf("no-image case: replaced=%d err=%v", replaced, err)
	}
	if req.Messages[0].Content[0].Text != "纯文本消息没图" {
		t.Errorf("text corrupted: %+v", req.Messages[0].Content[0])
	}
}

func TestDowngradeAnthropicImagesToText_MultipleImages(t *testing.T) {
	ocr := ocrFlashServer(t, "图N识别结果", http.StatusOK)
	defer ocr.Close()
	h := newImageTestHandler(t, ocr)

	req := &AnthropicRequest{
		Messages: []AnthropicMessage{{
			Role: "user",
			Content: []AnthropicContent{
				{Type: "text", Text: "起点"},
				{Type: "image", Source: &AnthropicImageSource{Type: "base64", MediaType: "image/png", Data: fakeNvidiaImageB64}},
				{Type: "text", Text: "中间文本"},
				{Type: "image", Source: &AnthropicImageSource{Type: "base64", MediaType: "image/jpeg", Data: fakeNvidiaImageB64}},
				{Type: "text", Text: "终点"},
			},
		}},
	}
	replaced, err, _, _, _ := h.ocr.DowngradeAnthropicImagesToText(req, &RelaySession{UserID: "u1", UserKey: "k1"})
	if err != nil || replaced != 2 {
		t.Fatalf("replaced want 2 got %d err=%v", replaced, err)
	}
	if len(req.Messages[0].Content) != 5 {
		t.Fatalf("block order/count changed: %d", len(req.Messages[0].Content))
	}
	// 块顺序保持:image 已变 text,但仍在原位(1、3)
	for i, b := range req.Messages[0].Content {
		if b.Type != "text" {
			t.Errorf("block %d should be text, got %s", i, b.Type)
		}
	}
	if req.Messages[0].Content[0].Text != "起点" || req.Messages[0].Content[2].Text != "中间文本" || req.Messages[0].Content[4].Text != "终点" {
		t.Errorf("original text corrupted in mix case")
	}
	if !strings.Contains(req.Messages[0].Content[1].Text, "图N识别结果") || !strings.Contains(req.Messages[0].Content[3].Text, "图N识别结果") {
		t.Errorf("image blocks not OCR-replaced")
	}
}

func TestDowngradeAnthropicImagesToText_UrlSourcePlaceholder(t *testing.T) {
	// 窗内 url 类型 source 且 urlCache 未命中时,应尝试下载;但本用例 URL 指向一个不可达地址,
	// fetchImageAsBase64 必然失败 → 走占位文本兜底,不调 OCR 服务。
	// (P2 前:此用例锁定"url 一律占位";P2 后:URL 图改为下载尝试,失败仍兜底,行为等价:
	//  即不真调 OCR 上游、块为占位文本。故断言 replaced==0/OCR 上游零触达/占位文本保持不变。)
	ocr := ocrFlashServer(t, "should-not-be-called", http.StatusOK)
	defer ocr.Close()
	h := newImageTestHandler(t, ocr)

	req := &AnthropicRequest{
		Messages: []AnthropicMessage{{
			Role: "user",
			Content: []AnthropicContent{
				{Type: "image", Source: &AnthropicImageSource{Type: "url", MediaType: "image/png", Url: "http://127.0.0.1:1/unreachable.png"}},
			},
		}},
	}
	replaced, _, _, _, _ := h.ocr.DowngradeAnthropicImagesToText(req, &RelaySession{UserID: "u1", UserKey: "k1"})
	if replaced != 0 {
		t.Errorf("url source(failed download) should not count as replaced, got %d", replaced)
	}
	if req.Messages[0].Content[0].Text != imageNotExtractablePlaceholder {
		t.Errorf("url placeholder wrong: %s", req.Messages[0].Content[0].Text)
	}
}

// ===== 3. 端到端:handleNvidia =====

// nvidiaChatUpstreamWithImageAssertion 构造一个 mock NVIDIA 上游 /v1/chat/completions,
// 收到请求后把 body 落盘供断言,并回包最小合法 OpenAI Chat 非流式响应。
func nvidiaChatUpstreamWithImageAssertion(t *testing.T, captured *[]byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/v1/chat/completions") {
			t.Errorf("upstream unexpected path: %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		*captured = body
		resp := &OpenAIChatResponse{
			ID: "chatcmpl-img", Model: "z-ai/glm-5.2",
			Choices: []OpenAIChatChoice{{
				Index: 0, Message: ChatMessage{Role: "assistant", Content: "已看到截图内容"}, FinishReason: "stop",
			}},
			Usage: OpenAIChatUsage{PromptTokens: 10, CompletionTokens: 3, TotalTokens: 13},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func TestHandleNvidia_AnthropicImage_DowngradesToText(t *testing.T) {
	ocr := ocrFlashServer(t, "截图里的报错是: API Error NVIDIA upstream stream interrupted", http.StatusOK)
	defer ocr.Close()
	origAddr := localProxyAddr
	localProxyAddr = strings.TrimPrefix(ocr.URL, "http://")
	t.Cleanup(func() { localProxyAddr = origAddr })

	var upstreamBody []byte
	upstream := nvidiaChatUpstreamWithImageAssertion(t, &upstreamBody)
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-img", "nv-img@x.cloud", "k", upstream.URL, "z-ai/glm-5.2")
	handler, _, _, _ := newNvidiaTestHandler(t, []*account.Account{acc})

	// 入站 Anthropic 含 image block,模拟 Claude Code VSCode 粘图后附一句模糊指代(无 [Image #N] 芯片)。
	anthReq := &AnthropicRequest{
		Model:    "claude-sonnet-4-5",
		MaxTokens: func() *int { v := 200; return &v }(),
		Messages: []AnthropicMessage{{
			Role: "user",
			Content: []AnthropicContent{
				{Type: "image", Source: &AnthropicImageSource{Type: "base64", MediaType: "image/png", Data: fakeNvidiaImageB64}},
				{Type: "text", Text: "怎么回事啊"},
			},
		}},
	}
	body, _ := json.Marshal(anthReq)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytesReader(body))
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u-img", UserKey: "k1"})

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
	// content 应全是 string 形态(降级后无数组型 content)
	for _, m := range msgs {
		if _, ok := m["content"].(string); !ok {
			t.Errorf("upstream content not string: %v", m["content"])
		}
	}
	// user 文本里应含 OCR 识别结果(证明图被降级成文字而非丢失)
	concat := ""
	for _, m := range msgs {
		if m["role"] == "user" {
			if s, ok := m["content"].(string); ok {
				concat += s
			}
		}
	}
	if !strings.Contains(concat, "本地中继服务已自动调用") || !strings.Contains(concat, "NVIDIA upstream stream interrupted") {
		t.Errorf("OCR text not in upstream user content: %s", concat)
	}
	if !strings.Contains(concat, "怎么回事啊") {
		t.Errorf("original text lost after downgrade: %s", concat)
	}

	// 客户端回包应正常回译 Anthropic(无 overloaded_error)
	var out AnthropicResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("invalid anthropic response: %v body=%s", err, rr.Body.String())
	}
	if len(out.Content) == 0 || out.Content[0].Text != "已看到截图内容" {
		t.Errorf("assistant content wrong: %+v", out.Content)
	}
}

func TestHandleNvidia_AnthropicImage_OCRUnavailable_StillSendsText(t *testing.T) {
	// OCR 服务返回 503,降级应不阻断:上游仍收到含占位文本的纯文本请求,不报错。
	ocr := ocrFlashServer(t, "", http.StatusServiceUnavailable)
	defer ocr.Close()
	origAddr := localProxyAddr
	localProxyAddr = strings.TrimPrefix(ocr.URL, "http://")
	t.Cleanup(func() { localProxyAddr = origAddr })

	var upstreamBody []byte
	upstream := nvidiaChatUpstreamWithImageAssertion(t, &upstreamBody)
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-img2", "nv-img2@x.cloud", "k", upstream.URL, "z-ai/glm-5.2")
	handler, _, _, _ := newNvidiaTestHandler(t, []*account.Account{acc})

	anthReq := &AnthropicRequest{
		Model:    "claude-sonnet-4-5",
		MaxTokens: func() *int { v := 200; return &v }(),
		Messages: []AnthropicMessage{{
			Role: "user",
			Content: []AnthropicContent{
				{Type: "image", Source: &AnthropicImageSource{Type: "base64", MediaType: "image/png", Data: fakeNvidiaImageB64}},
			},
		}},
	}
	body, _ := json.Marshal(anthReq)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytesReader(body))
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u-img2", UserKey: "k1"})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 even when OCR down, got %d body=%s", rr.Code, rr.Body.String())
	}
	if len(upstreamBody) == 0 {
		t.Fatal("upstream never called")
	}
	msgs := parseOpenAIMessages(t, upstreamBody)
	if anyImageURL(msgs) {
		t.Errorf("must not leak image_url even if OCR fails")
	}
	concat := ""
	for _, m := range msgs {
		if m["role"] == "user" {
			if s, ok := m["content"].(string); ok {
				concat += s
			}
		}
	}
	if !strings.Contains(concat, imageNotExtractablePlaceholder) {
		t.Errorf("placeholder text missing after OCR unavailable: %s", concat)
	}
}

// 确保 bytes 包被使用(部分断言路径用 bytes.NewReader 在其它测试;此处保险引用避免未用告警)。
var _ = bytes.NewReader

// stubOcrSettings 是模型可配置化测试专用的最小 settings mock,
// 仅实现 settings.ManagerInterface 中 OCR 测试路径会触达的 GetOcrModel。
// 其余方法走嵌入接口(不被调用不会走进去,无 nil deref 风险),与 preferredSettings 同款范式。
type stubOcrSettings struct {
	settings.ManagerInterface
	ocrModel string
}

func (m *stubOcrSettings) GetOcrModel() string { return m.ocrModel }

// ===== 4. 缓存与模型可配置化契约 =====

// ocrFlashCountingServerV2 与 ocrFlashServer 一致,但额外维护收到请求的路径计数,
// 供校验"OCR 调用走的是哪个模型 URL"以及"是否真触达上游"。
// 返回 (server, *pathHits map),路径计数由 server 内部对每个 model path 自增。
func ocrFlashCountingServerV2(t *testing.T, ocrText string, pathHits *map[string]int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if pathHits != nil {
			(*pathHits)[r.URL.Path]++
		}
		w.Header().Set("Content-Type", "application/json")
		var resp string
		if ocrText != "" {
			resp = `{"candidates":[{"content":{"parts":[{"text":` + jsonString(ocrText) + `}]}}]}`
		} else {
			resp = `{"candidates":[]}`
		}
		w.Write([]byte(resp))
	}))
}

// TestHandleNvidia_HistoricImageNotReOCROnSecondTurn 锁定缓存契约:同一 handler、
// 同一会话、同一张图在第二轮 handleNvidia 降级时,命中 OCR 缓存,不再触达 OCR 上游。
// 断言:OCR mock 收到的请求数为 1(仅首轮真打);两轮送 NVIDIA 上游的 body 都含完整 OCR 文本、无 image_url。
func TestHandleNvidia_HistoricImageNotReOCROnSecondTurn(t *testing.T) {
	ocrPathHits := map[string]int{}
	ocr := ocrFlashCountingServerV2(t, "历史图 OCR 结果 ERROR 999", &ocrPathHits)
	defer ocr.Close()
	origAddr := localProxyAddr
	localProxyAddr = strings.TrimPrefix(ocr.URL, "http://")
	t.Cleanup(func() { localProxyAddr = origAddr })

	var upstreamBodies [][]byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		upstreamBodies = append(upstreamBodies, body)
		resp := &OpenAIChatResponse{
			ID: "chatcmpl-cache", Model: "z-ai/glm-5.2",
			Choices: []OpenAIChatChoice{{Index: 0, Message: ChatMessage{Role: "assistant", Content: "ok"}, FinishReason: "stop"}},
			Usage:   OpenAIChatUsage{PromptTokens: 5, CompletionTokens: 1, TotalTokens: 6},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-c1", "nv-c1@x.cloud", "k", upstream.URL, "z-ai/glm-5.2")
	handler, _, _, _ := newNvidiaTestHandler(t, []*account.Account{acc})

	// 入站 Anthropic 含一张图 + 模糊指代。
	anthReq := &AnthropicRequest{
		Model:    "claude-sonnet-4-5",
		MaxTokens: func() *int { v := 200; return &v }(),
		Messages: []AnthropicMessage{{
			Role: "user",
			Content: []AnthropicContent{
				{Type: "image", Source: &AnthropicImageSource{Type: "base64", MediaType: "image/png", Data: fakeNvidiaImageB64}},
				{Type: "text", Text: "看这张历史报错图"},
			},
		}},
	}
	body, _ := json.Marshal(anthReq)

	// 第 1 轮:miss,真打 OCR 上游一次。
	rr1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytesReader(body))
	handler.handleNvidia(rr1, req1, &RelaySession{UserID: "u-cache", UserKey: "k1"})
	if rr1.Code != http.StatusOK {
		t.Fatalf("turn1 expected 200, got %d body=%s", rr1.Code, rr1.Body.String())
	}
	totalOCR := 0
	for _, n := range ocrPathHits {
		totalOCR += n
	}
	if totalOCR != 1 {
		t.Fatalf("turn1 OCR upstream should be hit once, got %d (pathMap=%v)", totalOCR, ocrPathHits)
	}

	// 第 2 轮:同 handler 同会话同图 → 命中缓存,不再触达 OCR 上游。
	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytesReader(body))
	handler.handleNvidia(rr2, req2, &RelaySession{UserID: "u-cache", UserKey: "k1"})
	if rr2.Code != http.StatusOK {
		t.Fatalf("turn2 expected 200, got %d body=%s", rr2.Code, rr2.Body.String())
	}
	totalOCR2 := 0
	for _, n := range ocrPathHits {
		totalOCR2 += n
	}
	if totalOCR2 != 1 {
		t.Errorf("turn2 cache hit should skip OCR upstream, still got %d (pathMap=%v)", totalOCR2, ocrPathHits)
	}

	if len(upstreamBodies) != 2 {
		t.Fatalf("NVIDIA upstream should be called twice, got %d", len(upstreamBodies))
	}
	// 两轮送 NVIDIA 上游的 body 都应含 OCR 文本、无 image_url。
	for i, b := range upstreamBodies {
		msgs := parseOpenAIMessages(t, b)
		if anyImageURL(msgs) {
			t.Errorf("turn %d upstream leaked image_url", i+1)
		}
		concat := ""
		for _, m := range msgs {
			if m["role"] == "user" {
				if s, ok := m["content"].(string); ok {
					concat += s
				}
			}
		}
		if !strings.Contains(concat, "历史图 OCR 结果 ERROR 999") {
			t.Errorf("turn %d upstream missing OCR text: %s", i+1, concat)
		}
	}
}

// TestOCR_ModelSwitchReOCRs 锁定模型可配置化契约:同一张图先按 gemini-2.5-flash 缓存,
// 切换 settings.OcrModel 到 gemini-2.5-pro 后,缓存键变化(模型维度隔离)→ 重新 OCR 一次,
// 且文案里展示真实使用的模型名。
// 注:这里用 settingsMgr=nil 的 handler,通过环境变量或直接覆写 defaultOcrModel 不现实;
//   故走具体路径:用两次 NewAPICompatHandler,各持不同 settingsMgr mock 返回不同 OcrModel,
//   共享同一 OCR mock(计数),验证模型切换会再次触达上游。
func TestOCR_ModelSwitchReOCRs(t *testing.T) {
	var ocrHits atomic.Int64
	ocr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ocrHits.Add(1)
		// 区分两次模型:回包里带上路径里的模型名,供断言文案/URL 差异。
		modelInPath := "gemini-2.5-flash"
		if strings.Contains(r.URL.Path, "gemini-2.5-pro") {
			modelInPath = "gemini-2.5-pro"
		}
		w.Header().Set("Content-Type", "application/json")
		resp := `{"candidates":[{"content":{"parts":[{"text":"OCR-BY-` + modelInPath + `"}]}}]}`
		w.Write([]byte(resp))
	}))
	defer ocr.Close()

	origAddr := localProxyAddr
	localProxyAddr = strings.TrimPrefix(ocr.URL, "http://")
	t.Cleanup(func() { localProxyAddr = origAddr })

	// 两个 stub settingsMgr,各返回不同 OcrModel。
	flashSettings := &stubOcrSettings{ocrModel: "gemini-2.5-flash"}
	proSettings := &stubOcrSettings{ocrModel: "gemini-2.5-pro"}

	hFlash := NewAPICompatHandler(nil, nil, nil, nil, nil, flashSettings, nil)
	hPro := NewAPICompatHandler(nil, nil, nil, nil, nil, proSettings, nil)

	sess := &RelaySession{UserID: "u-switch", UserKey: "k1"}

	// 第 1 次:flash handler,缓存 gemini-2.5-flash 的结果。
	t1, err1, _ := hFlash.ocr.OcrImage(sess, fakeNvidiaImageB64, "image/png")
	if err1 != nil {
		t.Fatalf("flash ocr err: %v", err1)
	}
	if !strings.Contains(t1, "OCR-BY-gemini-2.5-flash") {
		t.Errorf("flash result wrong: %s", t1)
	}
	if ocrHits.Load() != 1 {
		t.Fatalf("first call should hit upstream once, got %d", ocrHits.Load())
	}

	// 第 2 次:同 flash handler 同图 → 命中缓存,不触达上游。
	t2, err2, _ := hFlash.ocr.OcrImage(sess, fakeNvidiaImageB64, "image/png")
	if err2 != nil || t2 != t1 {
		t.Fatalf("flash cache hit should equal first result, got %q err=%v", t2, err2)
	}
	if ocrHits.Load() != 1 {
		t.Fatalf("flash cache hit should not hit upstream, got %d", ocrHits.Load())
	}

	// 第 3 次:切到 pro handler,键变化 → 重新 OCR 一次(不同模型)。
	t3, err3, _ := hPro.ocr.OcrImage(sess, fakeNvidiaImageB64, "image/png")
	if err3 != nil {
		t.Fatalf("pro ocr err: %v", err3)
	}
	if !strings.Contains(t3, "OCR-BY-gemini-2.5-pro") {
		t.Errorf("pro result wrong: %s", t3)
	}
	if ocrHits.Load() != 2 {
		t.Fatalf("model switch should re-hit upstream (want 2), got %d", ocrHits.Load())
	}

	// 第 4 次:pro handler 同图 → 命中 pro 缓存,不触达上游。
	t4, err4, _ := hPro.ocr.OcrImage(sess, fakeNvidiaImageB64, "image/png")
	if err4 != nil || t4 != t3 {
		t.Fatalf("pro cache hit should equal third result, got %q err=%v", t4, err4)
	}
	if ocrHits.Load() != 2 {
		t.Fatalf("pro cache hit should not hit upstream, got %d", ocrHits.Load())
	}
}

// ===== 5. 窗口与按会话隔离契约(末尾 10 条窗口 + 窗外只查缓存/占位) =====

// ocrFlashCountingServerV3 与 ocrFlashServer 一致,但每收一个请求把 hitUpstream 原子计数 +1,
// 用于精确度量"窗外是否真触达上游""窗口内 miss 是否真打了一次"。
func ocrFlashCountingServerV3(t *testing.T, ocrText string, hitUpstream *atomic.Int64) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitUpstream.Add(1)
		w.Header().Set("Content-Type", "application/json")
		resp := fmt.Sprintf(`{"candidates":[{"content":{"parts":[{"text":%s}]}}]}`, jsonString(ocrText))
		w.Write([]byte(resp))
	}))
}

// TestOCR_DowngradeWindow_OutOfRangeMiss_Placeholder 锁定窗口契约:消息数 > 10 条时,
// 头部窗口以外(第 1 条)的图在缓存未命中场景下,绝真打 gemini 上游,直接走占位文本兜底。
// 断言:ocrSkipped==1、replaced==0、OCR 上游零触达、该块为 imageNotExtractablePlaceholder。
func TestOCR_DowngradeWindow_OutOfRangeMiss_Placeholder(t *testing.T) {
	var ocrHits atomic.Int64
	ocr := ocrFlashCountingServerV3(t, "窗内不该被调用", &ocrHits)
	defer ocr.Close()
	origAddr := localProxyAddr
	localProxyAddr = strings.TrimPrefix(ocr.URL, "http://")
	t.Cleanup(func() { localProxyAddr = origAddr })

	h := NewAPICompatHandler(nil, nil, nil, nil, nil, nil, nil)
	// 显式启用缓存(默认参数),让"窗外只查缓存"路径真正命中缓存查找分支而非零缓存降级。
	h.ocr.cache = newOcrLRUCache(0, 0, 0)
	sess := &RelaySession{UserID: "u-win1", UserKey: "k1"}

	// 构造 11 条消息:第 1 条含图(窗外),其余 10 条纯文本塞满窗口。
	msgs := make([]AnthropicMessage, 0, 11)
	msgs = append(msgs, AnthropicMessage{
		Role: "user",
		Content: []AnthropicContent{
			{Type: "text", Text: "这是很早之前的消息"},
			{Type: "image", Source: &AnthropicImageSource{Type: "base64", MediaType: "image/png", Data: fakeNvidiaImageB64}},
		},
	})
	for i := 0; i < 10; i++ {
		msgs = append(msgs, AnthropicMessage{
			Role:    "user",
			Content: []AnthropicContent{{Type: "text", Text: fmt.Sprintf("后续追问 %d", i)}},
		})
	}
	req := &AnthropicRequest{Messages: msgs}

	replaced, _, _, _, ocrSkipped := h.ocr.DowngradeAnthropicImagesToText(req, sess)
	if replaced != 0 {
		t.Fatalf("out-of-window miss should not count as replaced, got %d", replaced)
	}
	if ocrSkipped != 1 {
		t.Fatalf("out-of-window miss should be skipped once, got %d", ocrSkipped)
	}
	if ocrHits.Load() != 0 {
		t.Fatalf("out-of-window miss must NEVER hit OCR upstream, got %d", ocrHits.Load())
	}
	got := req.Messages[0].Content[1]
	if got.Type != "text" || got.Text != imageNotExtractablePlaceholder {
		t.Errorf("out-of-window miss should become placeholder, got %+v", got)
	}
}

// TestOCR_DowngradeWindow_OutOfRangeHit_Reused 锁定窗口契约:同一张图先前在窗口内被 OCR 过
// (缓存命中写入),随后被推出窗口外,再降级时应复用历史 OCR 文本而非重打上游,且计入 ocrHits。
// 断言:两轮总触达上游 1 次(仅首轮);第二轮 replaced==1 且块含 OCR 文本;第二轮 ocrHits==1。
//
// 关键前提:Claude Code 客户端无状态,每轮逐字重发完整历史——老消息的伴随文本在两轮间字节级
// 一致,故 promptCtx 维度稳定,缓存键不变,窗外能命中。若人为改动老消息文本(promptCtx 变),
// 则属"换了提问靶向"场景,按设计应 miss 走占位,不在本契约覆盖范围。
func TestOCR_DowngradeWindow_OutOfRangeHit_Reused(t *testing.T) {
	var ocrHits atomic.Int64
	ocr := ocrFlashCountingServerV3(t, "历史图 OCR 复用文本 XY", &ocrHits)
	defer ocr.Close()
	origAddr := localProxyAddr
	localProxyAddr = strings.TrimPrefix(ocr.URL, "http://")
	t.Cleanup(func() { localProxyAddr = origAddr })

	h := NewAPICompatHandler(nil, nil, nil, nil, nil, nil, nil)
	h.ocr.cache = newOcrLRUCache(0, 0, 0)
	sess := &RelaySession{UserID: "u-win2", UserKey: "k1"}

	// 老消息的伴随文本:两轮完全一致(模拟 Claude Code 逐字重发历史),保证 promptCtx 维度稳定。
	const oldMsgText = "看这张很早之前的报错图"

	// 第 1 轮:仅 1 条消息含图 → 在窗口内 → miss 真打上游一次,缓存写入(promptCtx=oldMsgText)。
	req1 := &AnthropicRequest{Messages: []AnthropicMessage{{
		Role: "user",
		Content: []AnthropicContent{
			{Type: "image", Source: &AnthropicImageSource{Type: "base64", MediaType: "image/png", Data: fakeNvidiaImageB64}},
			{Type: "text", Text: oldMsgText},
		},
	}}}
	r1, _, _, _, _ := h.ocr.DowngradeAnthropicImagesToText(req1, sess)
	if r1 != 1 || ocrHits.Load() != 1 {
		t.Fatalf("turn1 want replaced=1 hits=1, got replaced=%d hits=%d", r1, ocrHits.Load())
	}

	// 第 2 轮:同图仍在第 1 条(伴随文本逐字不变),后面塞 10 条纯文本占满窗口 → 第 1 条被推出窗外。
	msgs := make([]AnthropicMessage, 0, 11)
	msgs = append(msgs, AnthropicMessage{
		Role: "user",
		Content: []AnthropicContent{
			{Type: "image", Source: &AnthropicImageSource{Type: "base64", MediaType: "image/png", Data: fakeNvidiaImageB64}},
			{Type: "text", Text: oldMsgText},
		},
	})
	for i := 0; i < 10; i++ {
		msgs = append(msgs, AnthropicMessage{
			Role:    "user",
			Content: []AnthropicContent{{Type: "text", Text: fmt.Sprintf("新的追问 %d", i)}},
		})
	}
	req2 := &AnthropicRequest{Messages: msgs}

	r2, _, ocrHits2, _, ocrSkipped2 := h.ocr.DowngradeAnthropicImagesToText(req2, sess)
	if r2 != 1 {
		t.Fatalf("turn2 out-of-window cache hit should reuse text (replaced=1), got %d", r2)
	}
	if ocrSkipped2 != 0 {
		t.Errorf("turn2 cache hit should not skip, got ocrSkipped=%d", ocrSkipped2)
	}
	if ocrHits2 != 1 {
		t.Errorf("turn2 out-of-window cache hit should count ocrHits=1, got %d", ocrHits2)
	}
	if ocrHits.Load() != 1 {
		t.Fatalf("turn2 must NOT hit OCR upstream (cache reuse), got %d", ocrHits.Load())
	}
	got := req2.Messages[0].Content[0]
	if got.Type != "text" || !strings.Contains(got.Text, "历史图 OCR 复用文本 XY") {
		t.Errorf("turn2 out-of-window hit should reuse OCR text, got %+v", got)
	}
}

// TestOCR_DowngradeWindow_InRange_ReOCRsOnMiss 锁定窗口契约:消息数 <= 10 条时全部消息
// 在窗口内,图在 cache miss 时真打 gemini 上游一次(不被窗口逻辑短路)。
// 断言:replaced==1、ocrMisses==1、OCR 上游触达 1 次、块含 OCR 文本。
func TestOCR_DowngradeWindow_InRange_ReOCRsOnMiss(t *testing.T) {
	var ocrHits atomic.Int64
	ocr := ocrFlashCountingServerV3(t, "窗内图 OCR 结果 Z", &ocrHits)
	defer ocr.Close()
	origAddr := localProxyAddr
	localProxyAddr = strings.TrimPrefix(ocr.URL, "http://")
	t.Cleanup(func() { localProxyAddr = origAddr })

	h := NewAPICompatHandler(nil, nil, nil, nil, nil, nil, nil)
	h.ocr.cache = newOcrLRUCache(0, 0, 0)
	sess := &RelaySession{UserID: "u-win3", UserKey: "k1"}

	// 5 条消息,均在窗口内;第 3 条含图。
	msgs := make([]AnthropicMessage, 0, 5)
	for i := 0; i < 5; i++ {
		if i == 2 {
			msgs = append(msgs, AnthropicMessage{
				Role: "user",
				Content: []AnthropicContent{
					{Type: "text", Text: "看这张报错图"},
					{Type: "image", Source: &AnthropicImageSource{Type: "base64", MediaType: "image/png", Data: fakeNvidiaImageB64}},
				},
			})
			continue
		}
		msgs = append(msgs, AnthropicMessage{
			Role:    "user",
			Content: []AnthropicContent{{Type: "text", Text: fmt.Sprintf("普通追问 %d", i)}},
		})
	}
	req := &AnthropicRequest{Messages: msgs}

	r, _, _, ocrMisses, ocrSkipped := h.ocr.DowngradeAnthropicImagesToText(req, sess)
	if r != 1 {
		t.Fatalf("in-window miss should replace 1, got %d", r)
	}
	if ocrMisses != 1 {
		t.Errorf("in-window miss should count ocrMisses=1, got %d", ocrMisses)
	}
	if ocrSkipped != 0 {
		t.Errorf("in-window should not skip, got ocrSkipped=%d", ocrSkipped)
	}
	if ocrHits.Load() != 1 {
		t.Fatalf("in-window miss should hit OCR upstream once, got %d", ocrHits.Load())
	}
	got := req.Messages[2].Content[1]
	if got.Type != "text" || !strings.Contains(got.Text, "窗内图 OCR 结果 Z") {
		t.Errorf("in-window miss block should hold OCR text, got %+v", got)
	}
}

// TestOCR_CacheKeyIsolatesBySessionKey 锁定按会话隔离契约:同一张图、同一 OCR 模型、
// 同一 UserKey(同用户),仅 SessionKey 不同 → 缓存键不同,两会话各自独立 OCR 一次
// (各打上游一次),不共享缓存槽。断言:两会话均触达上游,总命中 2 次;
// 若 SessionKey 未纳入缓存键(回退 UserKey),两会话会共享同一个缓存槽 → 第二次会命中缓存,
// 总命中仅 1 次,本断言即失败。故总命中 2 次是 SessionKey 参与隔离的必要证据。
func TestOCR_CacheKeyIsolatesBySessionKey(t *testing.T) {
	var ocrHits atomic.Int64
	ocr := ocrFlashCountingServerV3(t, "OCR-ISO-TEXT", &ocrHits)
	defer ocr.Close()
	origAddr := localProxyAddr
	localProxyAddr = strings.TrimPrefix(ocr.URL, "http://")
	t.Cleanup(func() { localProxyAddr = origAddr })

	h := NewAPICompatHandler(nil, nil, nil, nil, nil, nil, nil)
	h.ocr.cache = newOcrLRUCache(0, 0, 0)

	// 同 UserKey(同用户),仅 SessionKey 不同 → 缓存键首维 SessionKey 隔离。
	sessA := &RelaySession{UserID: "u-iso", UserKey: "k1", SessionKey: "auth:acc:sessionA"}
	sessB := &RelaySession{UserID: "u-iso", UserKey: "k1", SessionKey: "auth:acc:sessionB"}

	if _, errA, _ := h.ocr.OcrImage(sessA, fakeNvidiaImageB64, "image/png"); errA != nil {
		t.Fatalf("sessA ocr err: %v", errA)
	}
	if _, errB, _ := h.ocr.OcrImage(sessB, fakeNvidiaImageB64, "image/png"); errB != nil {
		t.Fatalf("sessB ocr err: %v", errB)
	}

	// 缓存键前缀应为各自 SessionKey,故两会话不共享缓存:各触达上游 1 次,总 2 次。
	if got := ocrHits.Load(); got != 2 {
		t.Fatalf("session isolation: each session should hit upstream once (total 2), got %d "+
			"(若=1 说明 SessionKey 未纳入缓存键,两会话共享了 UserKey 槽)", got)
	}
}

// TestHandleNvidia_ClaudeCodeSessionHeader_InjectsSessionKey 锁定:Claude Code 客户端用
// X-Api-Key 鉴权(不带 Authorization: Bearer),原 ExtractSessionKey 会兜底走 sock 分支 →
// 全部本地 Claude Code 会话共一个 "sock:acc:127.0.0.1",会话级隔离失效。修复后 handleNvidia
// 入口优先读 X-Claude-Code-Session-Id 头,以 "claude:<UUID>" 注入 userSession.SessionKey。
// 断言:入站带该头 → userSession.SessionKey == "claude:<UUID>";不带该头(空)→ 回退 ExtractSessionKey。
func TestHandleNvidia_ClaudeCodeSessionHeader_InjectsSessionKey(t *testing.T) {
	ocr := ocrFlashServer(t, "OCR-SESSION", http.StatusOK)
	defer ocr.Close()
	origAddr := localProxyAddr
	localProxyAddr = strings.TrimPrefix(ocr.URL, "http://")
	t.Cleanup(func() { localProxyAddr = origAddr })

	var upstreamBody []byte
	upstream := nvidiaChatUpstreamWithImageAssertion(t, &upstreamBody)
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-sess", "nv-sess@x.cloud", "k", upstream.URL, "z-ai/glm-5.2")
	handler, _, _, _ := newNvidiaTestHandler(t, []*account.Account{acc})

	anthReq := &AnthropicRequest{
		Model:     "claude-sonnet-4-5",
		MaxTokens: func() *int { v := 50; return &v }(),
		Messages: []AnthropicMessage{{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "hi"}}}},
	}
	body, _ := json.Marshal(anthReq)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytesReader(body))
	// 模拟 Claude Code:用 X-Api-Key 鉴权 + 带原生会话头。
	req.Header.Set("X-Api-Key", "sk-ant-testsession")
	req.Header.Set("X-Claude-Code-Session-Id", "a1b2c3d4-e5f6-7890-abcd-ef1234567890")

	sess := &RelaySession{UserID: "u-sess", UserKey: "k1"}
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, sess)

	if sess.SessionKey != "claude:a1b2c3d4-e5f6-7890-abcd-ef1234567890" {
		t.Fatalf("SessionKey should be claude:<UUID> when X-Claude-Code-Session-Id present, got %q", sess.SessionKey)
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
}

// TestHandleNvidia_NoClaudeSessionHeader_FallsBackToExtractSessionKey 锁定:无
// X-Claude-Code-Session-Id 头时(如直接调 /nvidia/* 的脚本/SDK),回退 ExtractSessionKey。
// 走 Authorization: Bearer 分支 → "auth:acc:<16hex>";走 sock 兜底 → "sock:acc:<host>"。
func TestHandleNvidia_NoClaudeSessionHeader_FallsBackToExtractSessionKey(t *testing.T) {
	ocr := ocrFlashServer(t, "OCR-NOSID", http.StatusOK)
	defer ocr.Close()
	origAddr := localProxyAddr
	localProxyAddr = strings.TrimPrefix(ocr.URL, "http://")
	t.Cleanup(func() { localProxyAddr = origAddr })

	var upstreamBody []byte
	upstream := nvidiaChatUpstreamWithImageAssertion(t, &upstreamBody)
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-nosid", "nv-nosid@x.cloud", "k", upstream.URL, "z-ai/glm-5.2")
	handler, _, router, _ := newNvidiaTestHandler(t, []*account.Account{acc})
	_ = router

	anthReq := &AnthropicRequest{
		Model:     "claude-sonnet-4-5",
		MaxTokens: func() *int { v := 50; return &v }(),
		Messages: []AnthropicMessage{{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "hi"}}}},
	}
	body, _ := json.Marshal(anthReq)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytesReader(body))
	// 用 Authorization: Bearer(非 Claude Code 路径),不带 X-Claude-Code-Session-Id。
	req.Header.Set("Authorization", "Bearer test-bearer-token-no-claude-sid")

	sess := &RelaySession{UserID: "u-nosid", UserKey: "k1"}
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, sess)

	// 应回退到 ExtractSessionKey 的 auth 分支:auth:acc:<16hex>(Bearer token SHA256 前 16 hex)。
	if !strings.HasPrefix(sess.SessionKey, "auth:acc:") {
		t.Fatalf("SessionKey should fall back to auth:acc:<hex> without X-Claude-Code-Session-Id, got %q", sess.SessionKey)
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
}


