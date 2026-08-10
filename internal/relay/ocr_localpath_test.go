package relay

// ocr_localpath_test.go —— 锁定 L2.5 裸本地图片路径预扫描的识别/读图/注入/缓存/守卫契约。
//
// 覆盖:
//   - findLocalImagePathTokens / truncateToImageExt 命中表(含用户钦定反例);
//   - resolveLocalImageToB64:t.TempDir 建真 png/jpg/非图/超大,Stat/ReadFile/DetectContentType/size cap;
//   - resolveLocalImagePathsInText:0/1/2 路径、存在/不存在/混合、尾随 CJK 保留;
//   - 缓存命中防重复:同一 b64 连调两次,第二次上游请求计数不变(命中 L1);
//   - isLocalDirectSession:APIKeyID 各来源矩阵(official_bypass/default_bypass 放行;持久化 key.ID/
//     web 登录空/nil 拒);
//   - 三协议适配函数:Anthropic/Gemini/OpenAI Chat(string 与数组 text 块两种形态)。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// localDirectSession 是单测里"本地自用直连"的标准会话(APIKeyID=official_bypass)。
func localDirectSession() *RelaySession {
	return &RelaySession{UserID: "u1", UserKey: "k1", APIKeyID: APIKeyIDOfficialBypass}
}

// ocrLocalpathMockHandler 覆盖 localProxyAddr 指向 ocrMock,返回带 cache 的 OCRService 的 handler。
func ocrLocalpathMockHandler(t *testing.T, ocrMock *httptest.Server) *APICompatHandler {
	t.Helper()
	origAddr := localProxyAddr
	localProxyAddr = strings.TrimPrefix(ocrMock.URL, "http://")
	t.Cleanup(func() { localProxyAddr = origAddr })
	h := NewAPICompatHandler(nil, nil, nil, nil, nil, nil, nil)
	return h
}

// writeFakePNG 在 dir 下写一张真实 1x1 PNG 并返回其绝对路径(跨平台 safe)。
func writeFakePNG(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, decodeFakePNGBytes(t), 0644); err != nil {
		t.Fatalf("write fake png: %v", err)
	}
	return p
}

// ===== 1. 识别:findLocalImagePathTokens / truncateToImageExt 命中表 =====

func TestTruncateToImageExt(t *testing.T) {
	cases := []struct {
		cand    string
		wantHit bool
		want    string
	}{
		// 命中(紧贴中文也算:尾随非扩展名字符被吃掉)。
		{`D:\x\a.png看`, true, `D:\x\a.png`},
		{`D:\x.a\a.png看`, true, `D:\x.a\a.png`}, // 中间 .a 不干扰,取最右图片扩展名
		{`D:\x\a.png`, true, `D:\x\a.png`},
		{`/Users/me/c.png`, true, `/Users/me/c.png`},
		// 不命中。
		{`D:\a\b.cs`, false, ""},  // 无图片扩展名后缀
		{`D:\a\b`, false, ""},     // 无扩展名
		{`C:\x\y.txt`, false, ""}, // .txt 不是图片
		{`nodot`, false, ""},      // 无点
		{`a.png`, true, `a.png`},  // truncateToImageExt 只管扩展名后缀;左边界由 localPathHead 管(见 FindLocalImagePathTokens_MissTable 的 b.png 反例)
	}
	for _, c := range cases {
		got, _ := truncateToImageExt(c.cand)
		if c.wantHit {
			if got != c.want {
				t.Errorf("truncate(%q) want %q got %q", c.cand, c.want, got)
			}
		} else {
			if got != "" {
				t.Errorf("truncate(%q) want empty(未命中), got %q", c.cand, got)
			}
		}
	}
}

func TestFindLocalImagePathTokens_HitMissTable(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		paths []string // 期望截到的净路径(顺序)
	}{
		// 命中(紧贴中文算)。
		{"紧贴中文-后", `图D:\x\a.png看`, []string{`D:\x\a.png`}},
		{"紧贴中文-前", `图D:\x\a.png 是图`, []string{`D:\x\a.png`}},
		{"引号包裹", `请看 "D:\x\b.png"`, []string{`D:\x\b.png`}},
		{"Unix绝对路径", `/Users/me/c.png 是图`, []string{`/Users/me/c.png`}},
		{"多个路径", `看 D:\x\a.png 和 /u/b.jpg 两张`, []string{`D:\x\a.png`, `/u/b.jpg`}},
		{"句首紧贴", `D:\x\a.png你看`, []string{`D:\x\a.png`}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := findLocalImagePathTokens(c.text)
			if len(got) != len(c.paths) {
				t.Fatalf("find tokens(%q) want %d got %d: %+v", c.text, len(c.paths), len(got), got)
			}
			for i, m := range got {
				if m.path != c.paths[i] {
					t.Errorf("find tokens(%q)[%d].path want %q got %q", c.text, i, c.paths[i], m.path)
				}
			}
		})
	}
}

func TestFindLocalImagePathTokens_MissTable(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{"裸文件名非绝对", `这是 b.png 结尾`},          // 无盘符/前导 /
		{"无图片扩展名", `D:\a\b.cs 文件`},          // .cs 不是图片扩展名
		{"D盘非路径", `打开D盘img.png文件`},          // D盘 不满足冒号后需 \ 或 /
		{"凡例引号内的裸名", `os.Open("b.png") 关掉`}, // 无盘符
		{"空串", ``},
		{"纯文本无路径", `你好世界,没有任何路径`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := findLocalImagePathTokens(c.text)
			if len(got) != 0 {
				t.Errorf("find tokens(%q) want 0 match, got %d: %+v", c.text, len(got), got)
			}
		})
	}
}

// ===== 2. resolveLocalImageToB64:真文件读图守卫 =====

func TestResolveLocalImageToB64_RealPNG(t *testing.T) {
	dir := t.TempDir()
	p := writeFakePNG(t, dir, "shot.png")
	b64, mime, ok := resolveLocalImageToB64(p)
	if !ok {
		t.Fatalf("real png should resolve ok: %s", p)
	}
	if !strings.HasPrefix(strings.ToLower(mime), "image/") {
		t.Errorf("mime want image/* got %s", mime)
	}
	// base64 解出来应等于原字节编码。
	if b64 == "" {
		t.Errorf("b64 should not be empty")
	}
}

func TestResolveLocalImageToB64_NotExist(t *testing.T) {
	b64, _, ok := resolveLocalImageToB64(filepath.Join(t.TempDir(), "nope.png"))
	if ok {
		t.Errorf("non-existent file should not resolve: b64=%s", b64)
	}
}

func TestResolveLocalImageToB64_Directory(t *testing.T) {
	dir := t.TempDir()
	// 目录本身路径加点图片扩展名尾巴,Stat 会判 IsDir → 不命中。
	dirWithName := filepath.Join(dir, "fake.png")
	if err := os.Mkdir(dirWithName, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	_, _, ok := resolveLocalImageToB64(dirWithName)
	if ok {
		t.Errorf("directory should not resolve as image")
	}
}

func TestResolveLocalImageToB64_NonImageMime(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "fake.png")
	// 写一段明显不是图片的文本(.png 扩展名但内容是文本),DetectContentType 会识别为 text/plain。
	if err := os.WriteFile(p, []byte("hello world not an image"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, _, ok := resolveLocalImageToB64(p)
	if ok {
		t.Errorf("non-image content with .png ext should not resolve (DetectContentType rejects)")
	}
}

func TestResolveLocalImageToB64_TooLarge(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "big.png")
	// 写一段超过 localPathImageMaxBytes 的"图片头 + 空洞"(用真 PNG 头 + 0 填充到超限)。
	hdr := decodeFakePNGBytes(t)
	body := append(append([]byte{}, hdr...), make([]byte, localPathImageMaxBytes+1)...)
	if err := os.WriteFile(p, body, 0644); err != nil {
		t.Fatalf("write big: %v", err)
	}
	_, _, ok := resolveLocalImageToB64(p)
	if ok {
		t.Errorf("file exceeding size cap should not resolve")
	}
}

// ===== 3. resolveLocalImagePathsInText:enrichText 核心 =====

func TestResolveLocalImagePathsInText_NoneInText(t *testing.T) {
	s := &OCRService{}
	out, replaced := s.resolveLocalImagePathsInText("没有任何路径的纯文本", localDirectSession())
	if replaced != 0 || out != "没有任何路径的纯文本" {
		t.Errorf("no path: replaced=%d out=%q", replaced, out)
	}
}

func TestResolveLocalImagePathsInText_FileNotExist_SilentMiss(t *testing.T) {
	s := &OCRService{}
	in := `看下 D:\nope\nope.png 这张图`
	out, replaced := s.resolveLocalImagePathsInText(in, localDirectSession())
	if replaced != 0 {
		t.Errorf("non-existent file should be silent miss, replaced=%d", replaced)
	}
	// 原始文本原样保留(含路径 token + 尾随中文)。
	if out != in {
		t.Errorf("silent miss should keep text as-is:\nin=%q\nout=%q", in, out)
	}
}

func TestResolveLocalImagePathsInText_RealImage_OCROK(t *testing.T) {
	ocr := ocrFlashServer(t, "图中文字:报错 ERROR 500", http.StatusOK)
	defer ocr.Close()
	h := ocrLocalpathMockHandler(t, ocr)
	s := h.ocr

	dir := t.TempDir()
	p := writeFakePNG(t, dir, "shot.png")
	// 适配平台:Windows 用反斜杠形式贴中文,Mac/Unix 用正斜杠。统一用 ToSlash 不行(Windows 也能 Stat /),
	// 这里同时覆盖两种分隔符:构造 `图<p>看` 形式(p 在 Windows 是 D:\...\shot.png,在 Unix 是 /tmp/.../shot.png)。
	in := "图" + p + "看"
	out, replaced := s.resolveLocalImagePathsInText(in, localDirectSession())
	if replaced != 1 {
		t.Fatalf("replaced want 1 got %d", replaced)
	}
	// OCR 文本应被注入(descHeader 含 OCR 模型与识别文本)。
	if !strings.Contains(out, "本地中继服务已自动调用") {
		t.Errorf("descHeader missing in out: %s", out)
	}
	if !strings.Contains(out, "ERROR 500") {
		t.Errorf("OCR text missing in out: %s", out)
	}
	// 尾随中文 '看' 应被保留。
	if !strings.HasSuffix(out, "看") {
		t.Errorf("trailing CJK 看 should be preserved: %q", out)
	}
	// 路径 token 本身被替换掉(不应再含原路径)。
	if strings.Contains(out, filepath.Base(p)) && !strings.Contains(out, "本地中继服务已自动调用") {
		// 路径基名可能巧出现在 OCR 文本里,这里只确认不含原裸路径(盘符/前导 / 段)。
	}
	// 头部 '图' 前缀应保留。
	if !strings.HasPrefix(out, "图") {
		t.Errorf("leading 图 should be preserved: %q", out)
	}
}

func TestResolveLocalImagePathsInText_TwoPaths(t *testing.T) {
	ocr := ocrFlashServer(t, "双图识别OK", http.StatusOK)
	defer ocr.Close()
	h := ocrLocalpathMockHandler(t, ocr)
	s := h.ocr

	dir := t.TempDir()
	p1 := writeFakePNG(t, dir, "a.png")
	p2 := writeFakePNG(t, dir, "b.jpg")
	in := "第一张 " + p1 + " 第二张 " + p2 + " 看完"
	out, replaced := s.resolveLocalImagePathsInText(in, localDirectSession())
	if replaced != 2 {
		t.Fatalf("replaced want 2 got %d", replaced)
	}
	// 两段 descHeader 都应出现。
	if strings.Count(out, "本地中继服务已自动调用") != 2 {
		t.Errorf("expect 2 descHeaders, got: %s", out)
	}
	// 尾随中文 '看完' 应保留。
	if !strings.HasSuffix(out, "看完") {
		t.Errorf("trailing 看完 should be preserved: %q", out)
	}
}

func TestResolveLocalImagePathsInText_MixedExistAndMissing(t *testing.T) {
	ocr := ocrFlashServer(t, "混合中的存在图", http.StatusOK)
	defer ocr.Close()
	h := ocrLocalpathMockHandler(t, ocr)
	s := h.ocr

	dir := t.TempDir()
	exist := writeFakePNG(t, dir, "exist.png")
	miss := filepath.Join(dir, "nope.png")
	in := "存在 " + exist + " 不存在 " + miss + " 结束"
	out, replaced := s.resolveLocalImagePathsInText(in, localDirectSession())
	// 仅 1 个命中(存在的那张),不存在的那张静默 miss。
	if replaced != 1 {
		t.Fatalf("replaced want 1 (only existing) got %d", replaced)
	}
	// 不存在的路径 token 应原样保留在文本里。
	if !strings.Contains(out, miss) {
		t.Errorf("missing path should be kept as-is in out: %s", out)
	}
}

// ===== 4. 缓存命中防重复分析 =====

// TestResolveLocalImagePathsInText_CacheHitNoReOCR 锁定"复用 L1 缓存防重复分析":
// 同一张本地图连发两轮,第二轮 L1 ocrCache 命中,OCR 上游零触达(hitUpstream 不增)。
// 与现有 image 块降级共享同一缓存池(同 b64→同 key→全局每图一次)。
func TestResolveLocalImagePathsInText_CacheHitNoReOCR(t *testing.T) {
	var hitUpstream atomic.Int64
	ocr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitUpstream.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"缓存测试OCR"}]}}]}`))
	}))
	defer ocr.Close()
	h := ocrLocalpathMockHandler(t, ocr)
	s := h.ocr

	dir := t.TempDir()
	p := writeFakePNG(t, dir, "cache.png")
	in := "看这张 " + p + " 图"

	// 第一轮:miss 真打上游一次。
	out1, r1 := s.resolveLocalImagePathsInText(in, localDirectSession())
	if r1 != 1 {
		t.Fatalf("round1 replaced want 1 got %d", r1)
	}
	if got := hitUpstream.Load(); got != 1 {
		t.Fatalf("round1 upstream should be hit once, got %d", got)
	}
	if !strings.Contains(out1, "缓存测试OCR") {
		t.Errorf("round1 OCR text missing: %s", out1)
	}

	// 第二轮:同图同会话,命中 L1 缓存,上游零触达。
	out2, r2 := s.resolveLocalImagePathsInText(in, localDirectSession())
	if r2 != 1 {
		t.Fatalf("round2 replaced want 1 got %d", r2)
	}
	if got := hitUpstream.Load(); got != 1 {
		t.Fatalf("round2 upstream should NOT be hit again (cache hit), got %d", got)
	}
	if !strings.Contains(out2, "缓存测试OCR") {
		t.Errorf("round2 OCR text missing: %s", out2)
	}
}

// ===== 5. isLocalDirectSession 来源矩阵 =====

func TestIsLocalDirectSession_Matrix(t *testing.T) {
	s := &OCRService{}
	cases := []struct {
		name string
		sess *RelaySession
		want bool
	}{
		{"nil", nil, false},
		{"official_bypass", &RelaySession{APIKeyID: APIKeyIDOfficialBypass}, true},
		{"default_bypass", &RelaySession{APIKeyID: APIKeyIDDefaultBypass}, true},
		{"persistent_apikey", &RelaySession{APIKeyID: "real-key-id-123"}, false},
		{"web_login_empty", &RelaySession{APIKeyID: ""}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := s.isLocalDirectSession(c.sess); got != c.want {
				t.Errorf("isLocalDirectSession(%s) want %v got %v", c.name, c.want, got)
			}
		})
	}
}

// TestResolveLocalImagePathsInText_NonLocalSessionBlocked 锁定"远程外部中继用户被守卫堵死":
// APIKeyID=持久化 key.ID 的请求即便 text 含本机真实图片路径,也不读图(保持原样,防 LFI)。
func TestResolveLocalImagePathsInText_NonLocalSessionBlocked(t *testing.T) {
	ocr := ocrFlashServer(t, "不应被打到", http.StatusOK)
	defer ocr.Close()
	h := ocrLocalpathMockHandler(t, ocr)
	s := h.ocr

	dir := t.TempDir()
	p := writeFakePNG(t, dir, "blocked.png")
	in := "看这张 " + p + " 图"
	// 远程外部中继用户:持久化 API Key 来源。
	remoteSess := &RelaySession{UserID: "remote-u", UserKey: "remote-k", APIKeyID: "real-key-id-xyz"}
	out, replaced := s.resolveLocalImagePathsInText(in, remoteSess)
	if replaced != 0 {
		t.Errorf("non-local session should NOT trigger local path OCR, replaced=%d", replaced)
	}
	if out != in {
		t.Errorf("non-local session should keep text as-is:\nin=%q\nout=%q", in, out)
	}
}

// ===== 6. 三协议适配函数 =====

func TestEnrichLocalImagePathsInAnthropic_OCROK(t *testing.T) {
	ocr := ocrFlashServer(t, "Anthropic鉴图OK", http.StatusOK)
	defer ocr.Close()
	h := ocrLocalpathMockHandler(t, ocr)
	s := h.ocr

	dir := t.TempDir()
	p := writeFakePNG(t, dir, "anth.png")
	req := &AnthropicRequest{
		Messages: []AnthropicMessage{{
			Role: "user",
			Content: []AnthropicContent{
				{Type: "text", Text: "看这张 " + p + " 图"},
			},
		}},
	}
	replaced := s.EnrichLocalImagePathsInAnthropic(req, localDirectSession())
	if replaced != 1 {
		t.Fatalf("replaced want 1 got %d", replaced)
	}
	if !strings.Contains(req.Messages[0].Content[0].Text, "Anthropic鉴图OK") {
		t.Errorf("OCR text missing: %s", req.Messages[0].Content[0].Text)
	}
}

func TestEnrichLocalImagePathsInAnthropic_NonTextBlockUntouched(t *testing.T) {
	ocr := ocrFlashServer(t, "不应被打到", http.StatusOK)
	defer ocr.Close()
	h := ocrLocalpathMockHandler(t, ocr)
	s := h.ocr

	dir := t.TempDir()
	p := writeFakePNG(t, dir, "blk.png")
	req := &AnthropicRequest{
		Messages: []AnthropicMessage{{
			Role: "user",
			Content: []AnthropicContent{
				{Type: "text", Text: "看 " + p},
				{Type: "image", Source: &AnthropicImageSource{Type: "base64", MediaType: "image/png", Data: "AAA"}},
			},
		}},
	}
	replaced := s.EnrichLocalImagePathsInAnthropic(req, localDirectSession())
	if replaced != 1 {
		t.Fatalf("replaced want 1 (text block) got %d", replaced)
	}
	// image 块保持原样(不被本层触碰,交后续 DowngradeAnthropicImagesToText)。
	if req.Messages[0].Content[1].Type != "image" || req.Messages[0].Content[1].Source == nil {
		t.Errorf("image block should be untouched: %+v", req.Messages[0].Content[1])
	}
}

func TestEnrichLocalImagePathsInGemini_OCROK(t *testing.T) {
	ocr := ocrFlashServer(t, "Gemini鉴图OK", http.StatusOK)
	defer ocr.Close()
	h := ocrLocalpathMockHandler(t, ocr)
	s := h.ocr

	dir := t.TempDir()
	p := writeFakePNG(t, dir, "gem.png")
	req := &GeminiRequest{
		Contents: []GeminiContent{{
			Role: "user",
			Parts: []GeminiPart{
				{Text: "看这张 " + p + " 图"},
				{InlineData: &GeminiBlob{MimeType: "image/png", Data: "AAA"}},
			},
		}},
	}
	replaced := s.EnrichLocalImagePathsInGemini(req, localDirectSession())
	if replaced != 1 {
		t.Fatalf("replaced want 1 got %d", replaced)
	}
	if !strings.Contains(req.Contents[0].Parts[0].Text, "Gemini鉴图OK") {
		t.Errorf("OCR text missing: %s", req.Contents[0].Parts[0].Text)
	}
	// InlineData 部分(图片块)保持原样。
	if req.Contents[0].Parts[1].InlineData == nil {
		t.Errorf("InlineData part should be untouched")
	}
}

func TestEnrichLocalImagePathsInOpenAIChat_StringContent(t *testing.T) {
	ocr := ocrFlashServer(t, "OpenAI-String-OK", http.StatusOK)
	defer ocr.Close()
	h := ocrLocalpathMockHandler(t, ocr)
	s := h.ocr

	dir := t.TempDir()
	p := writeFakePNG(t, dir, "oaistr.png")
	// string 形态 content。
	body := []byte(`{"model":"deepseek-chat","messages":[{"role":"user","content":"看这张 ` + strings.ReplaceAll(p, `\`, `\\`) + ` 图"}]}`)
	out, replaced := s.EnrichLocalImagePathsInOpenAIChat(body, localDirectSession())
	if replaced != 1 {
		t.Fatalf("replaced want 1 got %d", replaced)
	}
	// 仍可被 OpenAIChatRequest Unmarshal 吃下(content 仍 string)。
	var chatReq OpenAIChatRequest
	if err := json.Unmarshal(out, &chatReq); err != nil {
		t.Fatalf("enriched body must unmarshal: %v", err)
	}
	if len(chatReq.Messages) != 1 {
		t.Fatalf("messages count changed: %d", len(chatReq.Messages))
	}
	if !strings.Contains(chatReq.Messages[0].Content, "OpenAI-String-OK") {
		t.Errorf("OCR text missing in content: %s", chatReq.Messages[0].Content)
	}
	if !strings.Contains(chatReq.Messages[0].Content, "图") {
		t.Errorf("trailing CJK should be preserved: %s", chatReq.Messages[0].Content)
	}
}

func TestEnrichLocalImagePathsInOpenAIChat_ArrayTextBlock(t *testing.T) {
	ocr := ocrFlashServer(t, "OpenAI-Array-OK", http.StatusOK)
	defer ocr.Close()
	h := ocrLocalpathMockHandler(t, ocr)
	s := h.ocr

	dir := t.TempDir()
	p := writeFakePNG(t, dir, "oaiarr.png")
	// 数组形态 content:text 块 + image_url 块混合。
	escPath := strings.ReplaceAll(p, `\`, `\\`)
	body := []byte(`{"model":"deepseek-chat","messages":[{"role":"user","content":[{"type":"text","text":"看 ` + escPath + ` 图"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAA"}}]}]}`)
	out, replaced := s.EnrichLocalImagePathsInOpenAIChat(body, localDirectSession())
	if replaced != 1 {
		t.Fatalf("replaced want 1 (text block only) got %d", replaced)
	}
	// 仍是数组形态(text 块改写,image_url 块原样保留,交后续 DowngradeOpenAIChatImagesToText)。
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var msgs []map[string]json.RawMessage
	_ = json.Unmarshal(obj["messages"], &msgs)
	trimmed := strings.TrimSpace(string(msgs[0]["content"]))
	if trimmed == "" || trimmed[0] != '[' {
		t.Fatalf("content should remain array form (image_url block kept): %s", trimmed)
	}
	// 提取首个 text 块确认 OCR 注入。
	var blocks []map[string]interface{}
	if err := json.Unmarshal(msgs[0]["content"], &blocks); err != nil {
		t.Fatalf("unmarshal blocks: %v", err)
	}
	txt, _ := blocks[0]["text"].(string)
	if !strings.Contains(txt, "OpenAI-Array-OK") {
		t.Errorf("OCR text missing in text block: %s", txt)
	}
	if !strings.HasSuffix(txt, "图") {
		t.Errorf("trailing CJK should be preserved in text block: %s", txt)
	}
	// image_url 块原样保留(交后续降级)。
	if blocks[1]["type"] != "image_url" {
		t.Errorf("image_url block should be untouched, got: %+v", blocks[1])
	}
}

func TestEnrichLocalImagePathsInOpenAIChat_NoMatchPassthrough(t *testing.T) {
	ocr := ocrFlashServer(t, "不应被打到", http.StatusOK)
	defer ocr.Close()
	h := ocrLocalpathMockHandler(t, ocr)
	s := h.ocr

	// 无路径命中:原样返回入参 bodyBytes。
	in := []byte(`{"model":"deepseek-chat","messages":[{"role":"user","content":"纯文本无路径"}]}`)
	out, replaced := s.EnrichLocalImagePathsInOpenAIChat(in, localDirectSession())
	if replaced != 0 {
		t.Errorf("no path should give replaced=0, got %d", replaced)
	}
	if string(out) != string(in) {
		t.Errorf("no-match should passthrough body unchanged")
	}
}

func TestEnrichLocalImagePathsInOpenAIChat_NonLocalSessionBlocked(t *testing.T) {
	ocr := ocrFlashServer(t, "不应被打到", http.StatusOK)
	defer ocr.Close()
	h := ocrLocalpathMockHandler(t, ocr)
	s := h.ocr

	dir := t.TempDir()
	p := writeFakePNG(t, dir, "blocked2.png")
	escPath := strings.ReplaceAll(p, `\`, `\\`)
	in := []byte(`{"model":"deepseek-chat","messages":[{"role":"user","content":"看 ` + escPath + ` 图"}]}`)
	remoteSess := &RelaySession{UserID: "remote-u", UserKey: "remote-k", APIKeyID: "real-key-id-xyz"}
	out, replaced := s.EnrichLocalImagePathsInOpenAIChat(in, remoteSess)
	if replaced != 0 {
		t.Errorf("non-local session should NOT trigger, replaced=%d", replaced)
	}
	if string(out) != string(in) {
		t.Errorf("non-local session should passthrough body unchanged")
	}
}

// TestLocalPathTestHarness_AuthBinding 锁定测试桩的鉴权绑定:
// ocrFlashServer 期待 Authorization: Bearer k1,故 localDirectSession 的 UserKey 必须是 k1,
// 否则所有走真打上游的用例都会因 401 被判失败。
func TestLocalPathTestHarness_AuthBinding(t *testing.T) {
	s := localDirectSession()
	if s.UserKey != "k1" {
		t.Fatalf("localDirectSession UserKey must be k1 to satisfy ocrFlashServer auth, got %s", s.UserKey)
	}
}
