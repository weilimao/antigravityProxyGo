package relay

// ocr_crosspool_test.go —— 锁定 OCR 引擎跨号池改造(L1)的契约:
//   - resolveOcrTarget:Google 族前缀 → isGoogle=true(走 18443);非 Google 前缀 → isGoogle=false(走 18444 /route)
//   - ocrImageUncachedViaRoute:Gemini→OpenAI 转译(含 image_url data URL)、请求头自递归守卫、响应解析
//   - 未注入 routeResolver 时回退 Google 族旧行为(零回归)

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 构造一个注入 routeResolver 的 OCRService,routeResolver 解析逻辑与真实 resolveRoutedTarget 一致:
// 精确匹配 ClientModel → 返回 TargetProvider/TargetGroupID/TargetModel。
func newCrossPoolOCRService(t *testing.T, routeFn func(model string) (string, string, string, bool)) *APICompatHandler {
	t.Helper()
	h := &APICompatHandler{
		settingsMgr: &settingsAdapterWithOcr{stubPassThroughSettings: &stubPassThroughSettings{}},
		logFn:       func(string) {},
		client:      &http.Client{},
	}
	h.ocr = NewOCRService(h.settingsMgr, h.client, func(s string) { h.log("%s", s) })
	if routeFn != nil {
		h.ocr.SetRouteResolver(routeFn)
	}
	return h
}

// 模拟 18444 /route 入口:记录请求体与守卫头,回一个 OpenAI Chat 响应。
func mockRouteOCRServer(t *testing.T, wantSelfHeader string) (*httptest.Server, *map[string]interface{}) {
	t.Helper()
	captured := map[string]interface{}{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured["path"] = r.URL.Path
		captured["selfHeader"] = r.Header.Get("X-Antigravity-OCR-Self")
		captured["auth"] = r.Header.Get("Authorization")
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		captured["body"] = body
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"OCR_TEXT_FROM_ROUTE"}}]}`))
	}))
	return srv, &captured
}

func TestResolveOcrTarget_GoogleRay(t *testing.T) {
	h := newCrossPoolOCRService(t, func(model string) (string, string, string, bool) {
		// 精确匹配 Google 族前缀
		if model == "google/gemini-2.5-flash" {
			return "google", "", "gemini-2.5-flash", true
		}
		return "", "", "", false
	})

	// Google 族 → isGoogle=true,上游模型名被改写为裸名
	isGoogle, provider, _, upstream := h.ocr.resolveOcrTarget("google/gemini-2.5-flash")
	if !isGoogle {
		t.Fatalf("google prefix should be isGoogle=true, got provider=%s", provider)
	}
	if upstream != "gemini-2.5-flash" {
		t.Errorf("upstream want gemini-2.5-flash got %s", upstream)
	}

	// 未命中 → 回退 Google 族
	isGoogle, _, _, upstream = h.ocr.resolveOcrTarget("unknown-model")
	if !isGoogle {
		t.Error("unmatched model should fallback to google renders")
	}
	if upstream != "unknown-model" {
		t.Errorf("unmatched upstream should passthrough model, got %s", upstream)
	}
}

func TestResolveOcrTarget_NonGoogle(t *testing.T) {
	h := newCrossPoolOCRService(t, func(model string) (string, string, string, bool) {
		switch model {
		case "nvidia/gpt-4o":
			return "nvidia", "", "gpt-4o", true
		case "other/openai/gpt-4o":
			return "other", "openai", "gpt-4o", true
		}
		return "", "", "", false
	})

	// NVIDIA 前缀 → isGoogle=false
	isGoogle, provider, _, _ := h.ocr.resolveOcrTarget("nvidia/gpt-4o")
	if isGoogle {
		t.Fatal("nvidia should be non-google")
	}
	if provider != "nvidia" {
		t.Errorf("provider want nvidia got %s", provider)
	}

	// Other 前缀 → isGoogle=false + groupID
	isGoogle, provider, groupID, _ := h.ocr.resolveOcrTarget("other/openai/gpt-4o")
	if isGoogle || provider != "other" || groupID != "openai" {
		t.Errorf("other want non-google/other/openai, got isGoogle=%v provider=%s group=%s", isGoogle, provider, groupID)
	}
}

func TestResolveOcrTarget_NoResolver_fallback(t *testing.T) {
	// 未注入 routeResolver → 一律回退 Google 族(旧行为)
	h := newCrossPoolOCRService(t, nil)
	isGoogle, _, _, upstream := h.ocr.resolveOcrTarget("nvidia/gpt-4o")
	if !isGoogle {
		t.Error("no resolver should fallback to google")
	}
	if upstream != "nvidia/gpt-4o" {
		t.Errorf("no resolver upstream should be original model, got %s", upstream)
	}
}

func TestOcrImageUncachedViaRoute_TranslateAndGuard(t *testing.T) {
	srv, captured := mockRouteOCRServer(t, "1")
	defer srv.Close()

	// 把 localRelayAddr 指向 mock server(包级 var 可临时改写,测试后恢复)。
	orig := localRelayAddr
	localRelayAddr = strings.TrimPrefix(srv.URL, "http://")
	defer func() { localRelayAddr = orig }()

	h := newCrossPoolOCRService(t, nil)
	text, err := h.ocr.ocrImageUncachedViaRoute(
		&RelaySession{UserKey: "k1", UserID: "u1"},
		"describe this", "image/png", "QUJDREVG", "nvidia/gpt-4o", "gpt-4o",
	)
	if err != nil {
		t.Fatalf("ocrImageUncachedViaRoute error: %v", err)
	}
	if text != "OCR_TEXT_FROM_ROUTE" {
		t.Errorf("text want OCR_TEXT_FROM_ROUTE got %q", text)
	}

	// 守卫头
	if (*captured)["selfHeader"] != "1" {
		t.Errorf("expected X-Antigravity-OCR-Self=1, got %v", (*captured)["selfHeader"])
	}
	// 认证复用 UserKey
	if (*captured)["auth"] != "Bearer k1" {
		t.Errorf("auth want Bearer k1 got %v", (*captured)["auth"])
	}
	// 路径
	if (*captured)["path"] != "/route/v1/chat/completions" {
		t.Errorf("path want /route/v1/chat/completions got %v", (*captured)["path"])
	}
	// 请求体:model 保留前缀 + image_url data URL
	body := (*captured)["body"].(map[string]interface{})
	if body["model"] != "nvidia/gpt-4o" {
		t.Errorf("body.model want nvidia/gpt-4o got %v", body["model"])
	}
	msgs := body["messages"].([]interface{})
	content := msgs[0].(map[string]interface{})["content"].([]interface{})
	imgPart := content[1].(map[string]interface{})
	imgURL := imgPart["image_url"].(map[string]interface{})["url"]
	if want := "data:image/png;base64,QUJDREVG"; imgURL != want {
		t.Errorf("image_url want %s got %v", want, imgURL)
	}
}

func TestContentToString_Shapes(t *testing.T) {
	if got := contentToString("plain"); got != "plain" {
		t.Errorf("string want plain got %s", got)
	}
	if got := contentToString([]string{"a", "b"}); got != "ab" {
		t.Errorf("[]string want ab got %s", got)
	}
	if got := contentToString([]interface{}{"x", map[string]interface{}{"text": "y"}}); got != "xy" {
		t.Errorf("[]interface want xy got %s", got)
	}
	if got := contentToString(nil); got != "" {
		t.Errorf("nil want empty got %s", got)
	}
}