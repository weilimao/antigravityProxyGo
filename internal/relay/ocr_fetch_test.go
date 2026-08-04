package relay

// ocr_fetch_test.go —— 锁定 URL 图片抓取与 SSRF 防护、Data URL 解析、URL 二级缓存的契约。
//
// 覆盖 P2 新增能力:
//   - parseDataURL: 标准与缺 mime、非 base64 data URL、非数据 URLs
//   - isPublicIP: 回环/私网/链路本地/保留段拒绝,公网通过
//   - fetchImageAsBase64: 成功下载 / SSRF 拒绝(私网+回环+169.254)/ 非 image Content-Type / 超限
//   - resolveB64WithFetch / resolveB64NoFetch: base64/Data URL/URL 三态, urlCache 命中免下载
//   - DowngradeAnthropicImagesToText URL 图端到端:下载→OCR→改写 text 块;SSRF URL 走占位

import (
	"encoding/base64"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseDataURL_Standard(t *testing.T) {
	// 1x1 PNG base64.
	const pngB64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII="
	raw := "data:image/png;base64," + pngB64
	b, m, ok := parseDataURL(raw)
	if !ok {
		t.Fatal("expected ok")
	}
	if m != "image/png" {
		t.Errorf("mime want image/png got %s", m)
	}
	// 解码后应当等于原字节,确认标准化正确。
	dec, _ := base64.StdEncoding.DecodeString(b)
	if string(dec) != func() string {
		d, _ := base64.StdEncoding.DecodeString(pngB64)
		return string(d)
	}() {
		t.Errorf("normalized b64 roundtrip mismatch")
	}
}

func TestParseDataURL_NonBase64(t *testing.T) {
	if _, _, ok := parseDataURL("data:image/svg+xml,<svg/>"); ok {
		t.Error("non-base64 data url should fail")
	}
	if _, _, ok := parseDataURL("data:image/png;utf8,abc"); ok {
		t.Error("non-base64 encoding should fail")
	}
}

func TestParseDataURL_NotDataURL(t *testing.T) {
	if _, _, ok := parseDataURL("https://x/a.png"); ok {
		t.Error("http url should not be parsed as data url")
	}
	if _, _, ok := parseDataURL("ABC"); ok {
		t.Error("plain base64 should not be parsed as data url")
	}
}

func TestParseDataURL_MissingMime(t *testing.T) {
	if _, _, ok := parseDataURL("data:;base64,AAAA"); ok {
		t.Error("missing image/* mime should fail")
	}
	if _, _, ok := parseDataURL("data:text/plain;base64,AAAA"); ok {
		t.Error("text/* mime should fail")
	}
}

func TestIsPublicIP(t *testing.T) {
	reject := []string{"127.0.0.1", "10.0.0.1", "192.168.1.1", "172.16.0.1", "169.254.169.254",
		"0.0.0.0", "224.0.0.1", "::1", "fc00::1", "fd00::1", "fe80::1"}
	for _, s := range reject {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Errorf("unparseable %s", s)
			continue
		}
		if isPublicIP(ip) {
			t.Errorf("expected %s to be rejected (non-public)", s)
		}
	}
	// 公网正向。
	if !isPublicIP(net.ParseIP("8.8.8.8")) {
		t.Error("8.8.8.8 should be public")
	}
	if !isPublicIP(net.ParseIP("1.1.1.1")) {
		t.Error("1.1.1.1 should be public")
	}
	if !isPublicIP(net.ParseIP("2606:4700:4700::1111")) {
		t.Error("cloudflare ipv6 should be public")
	}
}

// enableSSRFLoopbackForTest 临时放开 SSRF 回环守卫,供 httptest 图床(绑 127.0.0.1)
// 走真实下载链路。返回需在 t.Cleanup 调用的还原函数。生产代码绝不调用此函数。
func enableSSRFLoopbackForTest(t *testing.T) {
	t.Helper()
	ssrfLoopbackAllowed.Store(true)
	t.Cleanup(func() { ssrfLoopbackAllowed.Store(false) })
}

func TestFetchImageAsBase64_OK(t *testing.T) {
	enableSSRFLoopbackForTest(t)
	// 真实 1x1 PNG 字节。
	pngBytes := decodeFakePNGBytes(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(pngBytes)
	}))
	defer srv.Close()

	b, m, err := fetchImageAsBase64(srv.URL)
	if err != nil {
		t.Fatalf("fetch err: %v", err)
	}
	if m != "image/png" {
		t.Errorf("mime want image/png got %s", m)
	}
	if b != base64.StdEncoding.EncodeToString(pngBytes) {
		t.Errorf("b64 mismatch")
	}
}

func TestFetchImageAsBase64_SSRF_Rejected(t *testing.T) {
	// 不放过 loopback 守卫:即便拨到回环也应被 SSRF 守卫拒绝。但 127.0.0.1:1 直接拨号失败
	// 也属"未触达内网"——为确定性覆盖 SSRF 路径,改用本机 httptest server + 关闭 loopback 开关:
	// SSRF 守卫会在 DialContext 阶段拒绝 127.0.0.1,命中 errSSRFRejected。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("downstream should never be reached under SSRF guard")
	}))
	defer srv.Close()
	if _, _, err := fetchImageAsBase64(srv.URL); err == nil {
		t.Error("expected SSRF rejection for httptest loopback url")
	}
	// 私网/元数据字面量(scheme 层 url.Parse 通过,但拨号阶段被拒)。
	for _, u := range []string{
		"http://10.0.0.1/x.png",
		"http://169.254.169.254/latest/meta-data/",
		"http://192.168.1.1/x.png",
	} {
		if _, _, err := fetchImageAsBase64(u); err == nil {
			t.Errorf("expected SSRF rejection for %s, got nil", u)
		}
	}
}

func TestFetchImageAsBaseURL_SchemeRejected(t *testing.T) {
	if _, _, err := fetchImageAsBase64("file:///etc/passwd"); err == nil {
		t.Error("file:// scheme should be rejected")
	}
	if _, _, err := fetchImageAsBase64("gopher://x/"); err == nil {
		t.Error("gopher scheme should be rejected")
	}
}

func TestFetchImageAsBase64_NotImage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html>not image</html>"))
	}))
	defer srv.Close()
	if _, _, err := fetchImageAsBase64(srv.URL); err == nil {
		t.Error("non-image content-type should fail")
	}
}

// decodeFakePNGBytes 解出 fakeNvidiaImageB64 对应的 PNG 原始字节,供 fetch mock 回包。
func decodeFakePNGBytes(t *testing.T) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(fakeNvidiaImageB64)
	if err != nil {
		t.Fatalf("decode fake png: %v", err)
	}
	return b
}

func TestResolveB64WithFetch_UrlCacheHitSkipsDownload(t *testing.T) {
	// 构造一个会失败的下载目标(若被触达则报错),但预先把 URL 写入 urlCache,
	// 验证 resolveB64WithFetch 命中 urlCache 后绝不触达下载。
	handler := NewAPICompatHandler(nil, nil, nil, nil, nil, nil, nil)
	const urlStr = "http://8.8.8.8/cached.png"
	handler.ocr.urlCache.set(urlCacheKey(urlStr), fakeNvidiaImageB64, "image/png")
	b, m, err := handler.ocr.resolveB64WithFetch(&AnthropicImageSource{Type: "url", MediaType: "image/png", Url: urlStr})
	if err != nil {
		t.Fatalf("expected cache hit, got err %v", err)
	}
	if b != fakeNvidiaImageB64 || m != "image/png" {
		t.Errorf("unexpected b64/mime: %s/%s", b, m)
	}
}

func TestResolveB64NoFetch_UrlMissReturnsFalse(t *testing.T) {
	handler := NewAPICompatHandler(nil, nil, nil, nil, nil, nil, nil)
	_, _, ok := handler.ocr.resolveB64NoFetch(&AnthropicImageSource{Type: "url", Url: "http://8.8.8.8/some.png"})
	if ok {
		t.Error("unseen url should NOT be resolvable without fetch")
	}
}

func TestResolveB64_Base64Direct(t *testing.T) {
	handler := NewAPICompatHandler(nil, nil, nil, nil, nil, nil, nil)
	b, m, ok := handler.ocr.resolveB64NoFetch(&AnthropicImageSource{Type: "base64", MediaType: "image/png", Data: fakeNvidiaImageB64})
	if !ok || b != fakeNvidiaImageB64 || m != "image/png" {
		t.Errorf("base64 direct resolve wrong: b=%s m=%s ok=%v", b, m, ok)
	}
}

func TestDowngradeAnthropicImagesToText_UrlSourceDownloadOCR(t *testing.T) {
	enableSSRFLoopbackForTest(t)
	// 端到端:Anthropic image url block → httptest 图床 → OCR mock → 块变 text,含 OCR 文本,无 image_url 泄漏。
	pngBytes := decodeFakePNGBytes(t)
	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(pngBytes)
	}))
	defer imgSrv.Close()

	ocr := ocrFlashServer(t, "图中识别:URL-IMG-OK", http.StatusOK)
	defer ocr.Close()
	origAddr := localProxyAddr
	localProxyAddr = strings.TrimPrefix(ocr.URL, "http://")
	t.Cleanup(func() { localProxyAddr = origAddr })

	h := NewAPICompatHandler(nil, nil, nil, nil, nil, nil, nil)
	req := &AnthropicRequest{
		Messages: []AnthropicMessage{{
			Role: "user",
			Content: []AnthropicContent{
				{Type: "text", Text: "看这个截图"},
				{Type: "image", Source: &AnthropicImageSource{Type: "url", MediaType: "image/png", Url: imgSrv.URL}},
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
	b1 := req.Messages[0].Content[1]
	if b1.Type != "text" || b1.Source != nil {
		t.Fatalf("block not downgraded: %+v", b1)
	}
	if !strings.Contains(b1.Text, "URL-IMG-OK") {
		t.Errorf("OCR text missing: %s", b1.Text)
	}
	// 第二轮同 URL 直接命中 urlCache + ocrCache,绝不再触达图床与 OCR 上游。
	req2 := &AnthropicRequest{Messages: []AnthropicMessage{{
		Role: "user",
		Content: []AnthropicContent{
			{Type: "image", Source: &AnthropicImageSource{Type: "url", MediaType: "image/png", Url: imgSrv.URL}},
		},
	}}}
	r2, _, _, _, _ := h.ocr.DowngradeAnthropicImagesToText(req2, &RelaySession{UserID: "u1", UserKey: "k1"})
	if r2 != 1 {
		t.Fatalf("second round should reuse via cache, replaced want 1 got %d", r2)
	}
	if !strings.Contains(req2.Messages[0].Content[0].Text, "URL-IMG-OK") {
		t.Errorf("second round OCR text missing: %s", req2.Messages[0].Content[0].Text)
	}
}
