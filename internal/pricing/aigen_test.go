package pricing

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeTokenProvider 是注入 AIPriceGenerator 的 token 解析/刷新闭包的轻量桩。
// access/refresh/project 可在测试中改写以模拟 401、刷新等场景。
type fakeTokenProvider struct {
	access   string
	refresh  string
	project  string
	refreshs int32 // refreshAccount 被调用次数
	newToken string // refreshAccount 返回的新 token
	failErr  error
}

func (f *fakeTokenProvider) getFn(id string) (string, string, string, error) {
	if f.failErr != nil {
		return "", "", "", f.failErr
	}
	return f.access, f.refresh, f.project, nil
}

func (f *fakeTokenProvider) refreshFn(id string) (string, error) {
	atomic.AddInt32(&f.refreshs, 1)
	if f.newToken != "" {
		f.access = f.newToken
	}
	return f.newToken, nil
}

// sseBody 拼一段 daily-cloudcode-pa 风格的 SSE 响应,把 text 作为 candidates[0] 的文本。
func sseBody(text string) string {
	return fmt.Sprintf("data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":%q}]}}]}}\n\n", text)
}

func TestBuildPricingPrompt(t *testing.T) {
	p := buildPricingPrompt([]string{"deepseek-v4-flash", "glm-5.2"})
	if !strings.Contains(p, "deepseek-v4-flash") {
		t.Error("prompt missing model name deepseek-v4-flash")
	}
	if !strings.Contains(p, "glm-5.2") {
		t.Error("prompt missing model name glm-5.2")
	}
	if !strings.Contains(p, "JSON") {
		t.Error("prompt must instruct JSON output")
	}
	if !strings.Contains(strings.ToLower(p), "美元") && !strings.Contains(p, "USD") {
		t.Error("prompt must state USD unit")
	}
	// 第一个字符前必须出现"第一个字符必须是 [" 的明文指令。
	if !strings.Contains(p, "[") {
		t.Error("prompt must reference array start '['")
	}
}

func TestExtractPricingJSON_BareArray(t *testing.T) {
	text := `[{"name":"deepseek-v4-flash","input":0.15,"output":0.3,"cached":0.037}]`
	m := extractPricingJSON(text)
	r, ok := m["deepseek-v4-flash"]
	if !ok {
		t.Fatalf("expected key deepseek-v4-flash, got %v", m)
	}
	if r.Input != 0.15 || r.Output != 0.3 || r.Cached != 0.037 {
		t.Errorf("unexpected rate: %+v", r)
	}
}

func TestExtractPricingJSON_Fenced(t *testing.T) {
	text := "这是结果:\n```json\n[{\"name\":\"glm-5.2\",\"input\":1.4,\"output\":4.4,\"cached\":0.26}]\n```\n以上。"
	m := extractPricingJSON(text)
	r, ok := m["glm-5.2"]
	if !ok {
		t.Fatalf("expected key glm-5.2, got %v", m)
	}
	if r.Input != 1.4 || r.Output != 4.4 {
		t.Errorf("unexpected rate: %+v", r)
	}
}

func TestExtractPricingJSON_WithProse(t *testing.T) {
	text := "好的,以下是为您查询的定价:\n[{\"name\":\"X\",\"input\":2,\"output\":8,\"cached\":0.5}]\n注意仅供参考。"
	m := extractPricingJSON(text)
	r, ok := m["x"]
	if !ok {
		t.Fatalf("expected lower-cased key x, got %v", m)
	}
	if r.Input != 2 {
		t.Errorf("unexpected input: %v", r.Input)
	}
}

func TestExtractPricingJSON_Malformed(t *testing.T) {
	m := extractPricingJSON("抱歉不是一个数组")
	if len(m) != 0 {
		t.Errorf("malformed text should yield empty map, got %v", m)
	}
}

func TestGenerate_HTTPSuccess(t *testing.T) {
	var calls int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v1internal:streamGenerateContent", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		// 第一次带 googleSearch 工具的请求应被服务端接受并返回成功 SSE。
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseBody(`[{"name":"deepseek-v4-flash","input":0.15,"output":0.3,"cached":0}]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	prov := &fakeTokenProvider{access: "tok", refresh: "rt", project: "proj"}
	g := NewAIPriceGenerator(prov.getFn, prov.refreshFn, nil,
		WithAIEndpoint(srv.URL+"/v1internal:streamGenerateContent"),
		WithAIClient(srv.Client()))

	res, err := g.Generate([]string{"deepseek-v4-flash"}, "acc")
	if err != nil {
		t.Fatalf("Generate err: %v", err)
	}
	r, ok := res["deepseek-v4-flash"]
	if !ok {
		t.Fatalf("missing key, got %v", res)
	}
	if r.Input != 0.15 {
		t.Errorf("input = %v want 0.15", r.Input)
	}
	// cached=0 且 input>0 时应回填 0.25×input。
	if r.Cached != 0.15*0.25 {
		t.Errorf("cached not backfilled to 0.25*input, got %v", r.Cached)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("expected exactly 1 upstream call, got %d", calls)
	}
}

func TestGenerate_HTTP401RefreshRetry(t *testing.T) {
	var calls int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v1internal:streamGenerateContent", func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		auth := r.Header.Get("Authorization")
		if n == 1 && auth == "Bearer tok" {
			http.Error(w, `{"error":{"message":"unauthorized"}}`, http.StatusUnauthorized)
			return
		}
		if auth != "Bearer newtok" {
			http.Error(w, `{"error":{"message":"unauthorized"}}`, http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseBody(`[{"name":"m1","input":1,"output":2,"cached":0.25}]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	prov := &fakeTokenProvider{access: "tok", refresh: "rt", project: "proj", newToken: "newtok"}
	g := NewAIPriceGenerator(prov.getFn, prov.refreshFn, nil,
		WithAIEndpoint(srv.URL+"/v1internal:streamGenerateContent"),
		WithAIClient(srv.Client()))

	res, err := g.Generate([]string{"m1"}, "acc")
	if err != nil {
		t.Fatalf("Generate err: %v", err)
	}
	if atomic.LoadInt32(&prov.refreshs) != 1 {
		t.Errorf("expected refreshAccount called once, got %d", prov.refreshs)
	}
	if _, ok := res["m1"]; !ok {
		t.Fatalf("missing key m1, got %v", res)
	}
}

func TestGenerate_GroundingFallback(t *testing.T) {
	var calls int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v1internal:streamGenerateContent", func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		// 第一次(带 tools)返回 400 grounding 拒收。
		if n == 1 {
			http.Error(w, `{"error":{"message":"grounding tool not supported"}}`, http.StatusBadRequest)
			return
		}
		// 后续(去 tools)返回成功。
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseBody(`[{"name":"m1","input":3,"output":6,"cached":0}]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	prov := &fakeTokenProvider{access: "tok", refresh: "rt", project: "proj"}
	g := NewAIPriceGenerator(prov.getFn, prov.refreshFn, nil,
		WithAIEndpoint(srv.URL+"/v1internal:streamGenerateContent"),
		WithAIClient(srv.Client()))

	res, err := g.Generate([]string{"m1"}, "acc")
	if err != nil {
		t.Fatalf("Generate err: %v", err)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("expected 2 calls (grounding then fallback), got %d", calls)
	}
	r, ok := res["m1"]
	if !ok || r.Input != 3 {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestGenerate_EmptyModels(t *testing.T) {
	g := NewAIPriceGenerator((&fakeTokenProvider{}).getFn, (&fakeTokenProvider{}).refreshFn, nil)
	res, err := g.Generate(nil, "acc")
	if err != nil {
		t.Fatalf("empty models should not error, got %v", err)
	}
	if len(res) != 0 {
		t.Errorf("expected empty map, got %v", res)
	}
}

func TestGenerate_MissingEntriesBackfilled(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1internal:streamGenerateContent", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// 故意只返回 m1,m2 缺席。
		fmt.Fprint(w, sseBody(`[{"name":"m1","input":1,"output":2,"cached":0}]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	prov := &fakeTokenProvider{access: "tok", refresh: "rt", project: "proj"}
	g := NewAIPriceGenerator(prov.getFn, prov.refreshFn, nil,
		WithAIEndpoint(srv.URL+"/v1internal:streamGenerateContent"),
		WithAIClient(srv.Client()))

	res, err := g.Generate([]string{"m1", "m2"}, "acc")
	if err != nil {
		t.Fatalf("Generate err: %v", err)
	}
	r2, ok := res["m2"]
	if !ok {
		t.Fatalf("missing backfilled key m2, got %v", res)
	}
	if r2.Input != 0 || r2.Output != 0 {
		t.Errorf("missing entry should be zero-filled, got %+v", r2)
	}
	if res["m1"].Input != 1 {
		t.Errorf("m1 input wrong: %+v", res["m1"])
	}
}

func TestManager_UpdatePricingBatch(t *testing.T) {
	m := NewManager()
	m.customUserDataPath = t.TempDir()
	m.pricingFilePath = filepath.Join(m.customUserDataPath, "pricing.json")
	m.initialized = false
	m.copyDefaults()
	m.initialized = true

	rates := map[string]ModelRate{
		"deepseek-v4-flash": {Input: 0.15, Output: 0.3, Cached: 0.037},
		"GLM-5.2":           {Input: 1.4, Output: 4.4, Cached: 0.26},
	}
	if err := m.UpdatePricingBatch(rates); err != nil {
		t.Fatalf("UpdatePricingBatch err: %v", err)
	}
	// 批量写入应不破坏既有 defaultPricing。
	if m.GetPricingForModel("gpt-oss-120b-medium").Input != defaultPricing["gpt-oss 120b (medium)"].Input {
		t.Error("batch write corrupted default pricing")
	}
	// 键应大小写归一。
	if m.GetPricingForModel("deepseek-v4-flash").Input != 0.15 {
		t.Error("lower-case key not written")
	}
	if m.GetPricingForModel("glm-5.2").Input != 1.4 {
		t.Error("upper-case key not normalized")
	}
}
