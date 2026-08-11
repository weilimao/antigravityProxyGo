package pricing

import (
	"errors"
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

// sseBodyWithGrounding 拼一段带 groundingMetadata 的 SSE 响应,
// 模拟 google_search 联网检索成功并返回引用来源的真实形态。
func sseBodyWithGrounding(text, uri, title string) string {
	return fmt.Sprintf(
		"data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":%q}]},\"groundingMetadata\":{\"groundingChunks\":[{\"web\":{\"uri\":%q,\"title\":%q}}]}}]}}\n\n",
		text, uri, title,
	)
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
	// 去幻觉重写:必须含 google_search 工具检索指令与 estimated 字段说明。
	if !strings.Contains(p, "google_search") {
		t.Error("prompt must drive model to call google_search tool")
	}
	if !strings.Contains(p, "estimated") {
		t.Error("prompt must instruct estimated flag for uncertain entries")
	}
	// 厂商官方定价页示例应被列出,驱使 AI 联网检索真实价页。
	if !strings.Contains(p, "anthropic.com/pricing") {
		t.Error("prompt must point to real vendor pricing pages")
	}
}

func TestExtractPricingJSON_BareArray(t *testing.T) {
	text := `[{"name":"deepseek-v4-flash","input":0.15,"output":0.3,"cached":0.037}]`
	m := extractPricingJSON(text)
	r, ok := m["deepseek-v4-flash"]
	if !ok {
		t.Fatalf("expected key deepseek-v4-flash, got %v", m)
	}
	if r.Rate.Input != 0.15 || r.Rate.Output != 0.3 || r.Rate.Cached != 0.037 {
		t.Errorf("unexpected rate: %+v", r.Rate)
	}
}

func TestExtractPricingJSON_Fenced(t *testing.T) {
	text := "这是结果:\n```json\n[{\"name\":\"glm-5.2\",\"input\":1.4,\"output\":4.4,\"cached\":0.26}]\n```\n以上。"
	m := extractPricingJSON(text)
	r, ok := m["glm-5.2"]
	if !ok {
		t.Fatalf("expected key glm-5.2, got %v", m)
	}
	if r.Rate.Input != 1.4 || r.Rate.Output != 4.4 {
		t.Errorf("unexpected rate: %+v", r.Rate)
	}
}

func TestExtractPricingJSON_WithProse(t *testing.T) {
	text := "好的,以下是为您查询的定价:\n[{\"name\":\"X\",\"input\":2,\"output\":8,\"cached\":0.5}]\n注意仅供参考。"
	m := extractPricingJSON(text)
	r, ok := m["x"]
	if !ok {
		t.Fatalf("expected lower-cased key x, got %v", m)
	}
	if r.Rate.Input != 2 {
		t.Errorf("unexpected input: %v", r.Rate.Input)
	}
}

func TestExtractPricingJSON_EstimatedAndSource(t *testing.T) {
	text := `[{"name":"claude-opus-4-6-thinking","input":15,"output":75,"cached":3.75,"estimated":true,"source_url":"https://example.com/x"}]`
	m := extractPricingJSON(text)
	r, ok := m["claude-opus-4-6-thinking"]
	if !ok {
		t.Fatalf("expected key, got %v", m)
	}
	if !r.Estimated {
		t.Error("estimated flag should be parsed true")
	}
	if r.SourceURL != "https://example.com/x" {
		t.Errorf("source_url not parsed: %q", r.SourceURL)
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

	res, err := g.Generate([]string{"deepseek-v4-flash"}, "acc", nil)
	if err != nil {
		t.Fatalf("Generate err: %v", err)
	}
	r, ok := res["deepseek-v4-flash"]
	if !ok {
		t.Fatalf("missing key, got %v", res)
	}
	if r.Rate.Input != 0.15 {
		t.Errorf("input = %v want 0.15", r.Rate.Input)
	}
	// cached=0 且 input>0 时应回填 0.25×input,并标 Estimated。
	if r.Rate.Cached != 0.15*0.25 {
		t.Errorf("cached not backfilled to 0.25*input, got %v", r.Rate.Cached)
	}
	if !r.Estimated {
		t.Error("cached 0.25x backfill should mark Estimated true")
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

	res, err := g.Generate([]string{"m1"}, "acc", nil)
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

	res, err := g.Generate([]string{"m1"}, "acc", nil)
	if err != nil {
		t.Fatalf("Generate err: %v", err)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("expected 2 calls (grounding then fallback), got %d", calls)
	}
	r, ok := res["m1"]
	if !ok || r.Rate.Input != 3 {
		t.Fatalf("unexpected result: %+v", res)
	}
	// 降级路径无 grounding 来源 → Grounded 必须为 false,前端据此标「未联网」。
	if r.Grounded {
		t.Error("degraded path (no grounding chunks) should have Grounded=false")
	}
}

func TestGenerate_EmptyModels(t *testing.T) {
	g := NewAIPriceGenerator((&fakeTokenProvider{}).getFn, (&fakeTokenProvider{}).refreshFn, nil)
	res, err := g.Generate(nil, "acc", nil)
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

	res, err := g.Generate([]string{"m1", "m2"}, "acc", nil)
	if err != nil {
		t.Fatalf("Generate err: %v", err)
	}
	r2, ok := res["m2"]
	if !ok {
		t.Fatalf("missing backfilled key m2, got %v", res)
	}
	if r2.Rate.Input != 0 || r2.Rate.Output != 0 {
		t.Errorf("missing entry should be zero-filled, got %+v", r2.Rate)
	}
	// 缺席条目应标估算,提示用户手动补。
	if !r2.Estimated {
		t.Error("missing entry should be marked Estimated")
	}
	if res["m1"].Rate.Input != 1 {
		t.Errorf("m1 input wrong: %+v", res["m1"].Rate)
	}
}

// TestGenerate_GroundingMetadataParsed 验证带 groundingMetadata 的响应被正确解析,
// Sources 含引用 uri/title 且 Grounded==true——这是「判断 AI 是否真联网」的核心信号。
func TestGenerate_GroundingMetadataParsed(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1internal:streamGenerateContent", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseBodyWithGrounding(
			`[{"name":"gemini-2.5-flash","input":0.075,"output":0.3,"cached":0.01875,"estimated":false}]`,
			"https://ai.google.dev/gemini/pricing",
			"Gemini Pricing",
		))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	prov := &fakeTokenProvider{access: "tok", refresh: "rt", project: "proj"}
	g := NewAIPriceGenerator(prov.getFn, prov.refreshFn, nil,
		WithAIEndpoint(srv.URL+"/v1internal:streamGenerateContent"),
		WithAIClient(srv.Client()))

	res, err := g.Generate([]string{"gemini-2.5-flash"}, "acc", nil)
	if err != nil {
		t.Fatalf("Generate err: %v", err)
	}
	r, ok := res["gemini-2.5-flash"]
	if !ok {
		t.Fatalf("missing key, got %v", res)
	}
	if !r.Grounded {
		t.Error("Grounded should be true when groundingChunks present")
	}
	if len(r.Sources) == 0 || r.Sources[0].URI != "https://ai.google.dev/gemini/pricing" {
		t.Errorf("Sources not parsed: %+v", r.Sources)
	}
	if r.Sources[0].Title != "Gemini Pricing" {
		t.Errorf("source title not parsed: %q", r.Sources[0].Title)
	}
}

// TestGenerate_ProgressCallbackOrder 验证 progressFn 在各阶段按预期顺序触发,
// 前端据此渲染「取 token → 联网 → 解析 → 完成」动态进度文案。
func TestGenerate_ProgressCallbackOrder(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1internal:streamGenerateContent", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseBody(`[{"name":"m1","input":1,"output":2,"cached":0.25}]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	prov := &fakeTokenProvider{access: "tok", refresh: "rt", project: "proj"}
	g := NewAIPriceGenerator(prov.getFn, prov.refreshFn, nil,
		WithAIEndpoint(srv.URL+"/v1internal:streamGenerateContent"),
		WithAIClient(srv.Client()))

	var stages []string
	progressFn := func(stage, status string) { stages = append(stages, stage) }
	if _, err := g.Generate([]string{"m1"}, "acc", progressFn); err != nil {
		t.Fatalf("Generate err: %v", err)
	}
	want := []string{"fetch-token", "grounding-search", "parse-result", "done"}
	if len(stages) != len(want) {
		t.Fatalf("stage sequence length = %d, want %d, got %v", len(stages), len(want), stages)
	}
	for i, s := range stages {
		if s != want[i] {
			t.Errorf("stage[%d] = %q, want %q (full: %v)", i, s, want[i], stages)
		}
	}
}

// TestGenerate_EstimatedFlagFromAI 验证 AI 自标 estimated:true 被透传到 AIPriceResult。
func TestGenerate_EstimatedFlagFromAI(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1internal:streamGenerateContent", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseBody(`[{"name":"claude-opus-4-6-thinking","input":15,"output":75,"cached":3.75,"estimated":true}]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	prov := &fakeTokenProvider{access: "tok", refresh: "rt", project: "proj"}
	g := NewAIPriceGenerator(prov.getFn, prov.refreshFn, nil,
		WithAIEndpoint(srv.URL+"/v1internal:streamGenerateContent"),
		WithAIClient(srv.Client()))

	res, err := g.Generate([]string{"claude-opus-4-6-thinking"}, "acc", nil)
	if err != nil {
		t.Fatalf("Generate err: %v", err)
	}
	r, ok := res["claude-opus-4-6-thinking"]
	if !ok {
		t.Fatalf("missing key, got %v", res)
	}
	if !r.Estimated {
		t.Error("Estimated should propagate AI's estimated:true flag")
	}
}

// TestGenerate_DegradedProgressFired 验证降级时 progress 触发 grounding-degraded 阶段,
// 前端据此提示用户本次未联网。
func TestGenerate_DegradedProgressFired(t *testing.T) {
	var calls int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v1internal:streamGenerateContent", func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			http.Error(w, `{"error":{"message":"grounding tool not supported"}}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseBody(`[{"name":"m1","input":3,"output":6,"cached":0}]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	prov := &fakeTokenProvider{access: "tok", refresh: "rt", project: "proj"}
	g := NewAIPriceGenerator(prov.getFn, prov.refreshFn, nil,
		WithAIEndpoint(srv.URL+"/v1internal:streamGenerateContent"),
		WithAIClient(srv.Client()))

	var sawDegraded bool
	progressFn := func(stage, status string) {
		if stage == "grounding-degraded" {
			sawDegraded = true
		}
	}
	if _, err := g.Generate([]string{"m1"}, "acc", progressFn); err != nil {
		t.Fatalf("Generate err: %v", err)
	}
	if !sawDegraded {
		t.Error("progress should fire grounding-degraded stage on fallback")
	}
}

// TestIsGroundingRejection_NarrowedNotLoose 验证收窄后 "tool array too long"
// 这类与工具拒收无关的 400 不再被误判为 grounding 拒收触发降级。
func TestIsGroundingRejection_NarrowedNotLoose(t *testing.T) {
	// 与工具拒收无关的 400:含 "tool" 单字但不该降级。
	loose := errors.New("HTTP 400: tool array too long")
	if isGroundingRejection(loose) {
		t.Error("narrowed isGroundingRejection must NOT match 'tool array too long'")
	}
	// 真正的 grounding 拒收:含 "grounding" 关键词。
	real := errors.New("HTTP 400: grounding not supported on this model")
	if !isGroundingRejection(real) {
		t.Error("isGroundingRejection should match real grounding rejection")
	}
	// google_search 显式拒收。
	gs := errors.New("HTTP 400: google_search tool unavailable")
	if !isGroundingRejection(gs) {
		t.Error("isGroundingRejection should match google_search rejection")
	}
	// 非 400 不判。
	non400 := errors.New("HTTP 500: grounding error")
	if isGroundingRejection(non400) {
		t.Error("non-400 errors must not be grounding rejection")
	}
}

// TestAnchorConflictForModel 验证锚点冲突比对纯函数。
func TestAnchorConflictForModel(t *testing.T) {
	// 命中锚点且偏差 > 2x(输入价 1.00 vs 锚点 0.075)→ 冲突。
	if !anchorConflictForModel("gemini-2.5-flash", ModelRate{Input: 1.00, Output: 4.0, Cached: 0.25}) {
		t.Error("input 1.00 vs anchor 0.075 (>2x) should conflict")
	}
	// 命中锚点但偏差在 2x 内(输入价 0.10 vs 锚点 0.075)→ 不冲突。
	if anchorConflictForModel("gemini-2.5-flash", ModelRate{Input: 0.10, Output: 0.4, Cached: 0.025}) {
		t.Error("input 0.10 vs anchor 0.075 (within 2x) should not conflict")
	}
	// 不在锚点表(虚构型号)→ 不判冲突(由 Estimated/Grounded 标记提示)。
	if anchorConflictForModel("claude-opus-4-6-thinking", ModelRate{Input: 999, Output: 1, Cached: 0}) {
		t.Error("unknown model should not trigger anchor conflict")
	}
	// 无锚点命中 → 永不冲突(防御除零也由此兜底)。
	if anchorConflictForModel("totally-unknown-model", ModelRate{Input: 1, Output: 1, Cached: 0}) {
		t.Error("no-anchor model should never conflict")
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
