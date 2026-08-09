package modelfetch

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- 候选生成:纯逻辑单测(无需联网) ---

func TestBuildCandidates_PlainRoot(t *testing.T) {
	c, err := BuildCandidates("https://api.siliconflow.cn")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	assertSliceEq(t, "plain root", c, []string{"https://api.siliconflow.cn/v1/models"})
}

func TestBuildCandidates_TrailingSlash(t *testing.T) {
	c, err := BuildCandidates("https://api.example.com/")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	assertSliceEq(t, "trailing slash", c, []string{"https://api.example.com/v1/models"})
}

func TestBuildCandidates_WithV1(t *testing.T) {
	// /v1 版本段结尾:模型端点 = {base}/models,恰好等价 {base}/v1/models。
	c, err := BuildCandidates("https://api.example.com/v1")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	assertSliceEq(t, "/v1 suffix", c, []string{"https://api.example.com/v1/models"})
}

func TestBuildCandidates_ZhipuV4(t *testing.T) {
	// 智谱 Coding Plan 以 /v4 结尾:正确端点 {base}/models 必须排在 /v1/models(404)之前。
	c, err := BuildCandidates("https://open.bigmodel.cn/api/coding/paas/v4")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	assertSliceEq(t, "zhipu v4", c, []string{
		"https://open.bigmodel.cn/api/coding/paas/v4/models",
		"https://open.bigmodel.cn/api/coding/paas/v4/v1/models",
	})
}

func TestBuildCandidates_DeepSeekStripAnthropic(t *testing.T) {
	// 用户反馈场景:DeepSeek anthropic 格式 baseURL 取模型应兜底剥到 OpenAI 根域。
	c, err := BuildCandidates("https://api.deepseek.com/anthropic")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	assertSliceEq(t, "deepseek /anthropic", c, []string{
		"https://api.deepseek.com/anthropic/v1/models",
		"https://api.deepseek.com/v1/models",
		"https://api.deepseek.com/models",
	})
}

func TestBuildCandidates_LongerSuffixWins(t *testing.T) {
	// /api/anthropic 必须整段剥离,而非只剥掉 /anthropic 留下残缺的 https://.../api。
	c, err := BuildCandidates("https://api.z.ai/api/anthropic")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	assertSliceEq(t, "longer suffix", c, []string{
		"https://api.z.ai/api/anthropic/v1/models",
		"https://api.z.ai/v1/models",
		"https://api.z.ai/models",
	})
}

func TestBuildCandidates_NoSuffixNoStrip(t *testing.T) {
	// /api 非已知兼容子路径,不触发剥离,仅单候选。
	c, err := BuildCandidates("https://openrouter.ai/api")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	assertSliceEq(t, "no suffix", c, []string{"https://openrouter.ai/api/v1/models"})
}

func TestBuildCandidates_Deduplicate(t *testing.T) {
	// 裸 scheme://host 无可剥离后缀,应只有 1 个候选(主候选)。
	c, err := BuildCandidates("https://host.example.com")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(c) != 1 {
		t.Fatalf("expected 1 candidate, got %d: %v", len(c), c)
	}
}

func TestBuildCandidates_Empty(t *testing.T) {
	if _, err := BuildCandidates(""); err == nil {
		t.Fatal("expected error for empty baseURL, got nil")
	}
	if _, err := BuildCandidates("   "); err == nil {
		t.Fatal("expected error for whitespace baseURL, got nil")
	}
}

func TestBuildCandidates_StepfunStripStepPlan(t *testing.T) {
	c, err := BuildCandidates("https://api.stepfun.com/step_plan")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	assertSliceEq(t, "stepfun", c, []string{
		"https://api.stepfun.com/step_plan/v1/models",
		"https://api.stepfun.com/v1/models",
		"https://api.stepfun.com/models",
	})
}

func TestBuildCandidates_DoubaoStripApiCoding(t *testing.T) {
	c, err := BuildCandidates("https://ark.cn-beijing.volces.com/api/coding")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	assertSliceEq(t, "doubao", c, []string{
		"https://ark.cn-beijing.volces.com/api/coding/v1/models",
		"https://ark.cn-beijing.volces.com/v1/models",
		"https://ark.cn-beijing.volces.com/models",
	})
}

func TestBuildCandidates_RightCodeStripClaude(t *testing.T) {
	c, err := BuildCandidates("https://www.right.codes/claude")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	assertSliceEq(t, "rightcode", c, []string{
		"https://www.right.codes/claude/v1/models",
		"https://www.right.codes/v1/models",
		"https://www.right.codes/models",
	})
}

func TestEndsWithVersionSegment(t *testing.T) {
	cases := map[string]bool{
		"https://x.com/v1":                            true,
		"https://open.bigmodel.cn/api/coding/paas/v4": true,
		"https://x.com/v10":                           true,
		"https://x.com/api":                           false,
		"https://x.com/vX":                            false,
		"https://x.com/models":                        false,
		"https://api.siliconflow.cn":                  false,
	}
	for url, want := range cases {
		if got := endsWithVersionSegment(url); got != want {
			t.Errorf("endsWithVersionSegment(%q) = %v, want %v", url, got, want)
		}
	}
}

// --- 取模型:httptest 端到端(本地复现,不联外网) ---

func TestFetchModels_DataShape(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path %q, want /v1/models", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("unexpected Authorization %q, want Bearer test-key", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "meta/llama-3.3-70b-instruct"},
				{"id": "moonshotai/kimi-k2.5"},
				{"id": "moonshotai/kimi-k2.5"}, // 重复项须被去重
			},
		})
	})
	defer srv.Close()

	// /v1 版本段 → 单候选 {base}/models,即命中 /v1/models。
	models, err := FetchModels(srv.URL+"/v1", "test-key")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	want := []string{"meta/llama-3.3-70b-instruct", "moonshotai/kimi-k2.5"}
	if len(models) != len(want) {
		t.Fatalf("expected %d deduped sorted models, got %d: %v", len(want), len(models), models)
	}
	for i, m := range want {
		if models[i] != m {
			t.Errorf("models[%d]=%q, want %q", i, models[i], m)
		}
	}
}

func TestFetchModels_ModelsShape(t *testing.T) {
	// 部分上游用 {models:[{id}]} 形态;裸根域 baseURL 应自动拼 /v1/models。
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path %q, want /v1/models", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]any{
				{"id": "nvidia/llama-3.1-nemotron-70b-instruct"},
				{"id": "abc/zeta"},
			},
		})
	})
	defer srv.Close()

	models, err := FetchModels(srv.URL, "")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	want := []string{"abc/zeta", "nvidia/llama-3.1-nemotron-70b-instruct"}
	if len(models) != len(want) {
		t.Fatalf("expected %d sorted models, got %d: %v", len(want), len(models), models)
	}
	for i, m := range want {
		if models[i] != m {
			t.Errorf("models[%d]=%q, want %q", i, models[i], m)
		}
	}
}

// TestFetchModels_CandidateFallback 关键集成:复现 DeepSeek /anthropic 场景。
// 第 1 候选 /anthropic/v1/models → 404 续试;第 2 候选 /v1/models → 200 命中。
// 断言:最终成功返回模型列表,且 /anthropic/v1/models 被实际请求过(404 已消费)。
func TestFetchModels_CandidateFallback(t *testing.T) {
	var hitAnthropic bool
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/anthropic/v1/models":
			hitAnthropic = true
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"deepseek-chat"},{"id":"deepseek-reasoner"}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	defer srv.Close()

	models, err := FetchModels(srv.URL+"/anthropic", "sk-test")
	if err != nil {
		t.Fatalf("expected fallback success via 2nd candidate, got error: %v", err)
	}
	if !hitAnthropic {
		t.Fatal("expected 1st candidate /anthropic/v1/models to be tried (and 404), but it was not hit")
	}
	want := []string{"deepseek-chat", "deepseek-reasoner"}
	if len(models) != len(want) {
		t.Fatalf("expected %d models, got %d: %v", len(want), len(models), models)
	}
	for i, m := range want {
		if models[i] != m {
			t.Errorf("models[%d]=%q, want %q", i, models[i], m)
		}
	}
}

// TestFetchModels_AllCandidatesFailed 所有候选(含剥离兜底)均 404 → 汇总错误,
// 错误文案须带 "All candidates failed" 前缀,供前端识别为「端点不存在」而非其他故障。
func TestFetchModels_AllCandidatesFailed(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not found"}`))
	})
	defer srv.Close()

	_, err := FetchModels(srv.URL+"/anthropic", "sk-test")
	if err == nil {
		t.Fatal("expected All candidates failed error, got nil")
	}
	if !strings.HasPrefix(err.Error(), "All candidates failed") {
		t.Fatalf("expected error prefixed with 'All candidates failed', got %q", err.Error())
	}
}

// TestFetchModels_FailFast401 首候选返回 401 → 立即返回该错误,不续试后续候选
// (鉴权失败对同 host 多候选同源,续试无意义且会无谓消耗请求)。
func TestFetchModels_FailFast401(t *testing.T) {
	var hitSecond bool
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/anthropic/v1/models":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid api key"}`))
		case "/v1/models":
			hitSecond = true
			_, _ = w.Write([]byte(`{"data":[{"id":"should-not-reach"}]}`))
		default:
			w.WriteHeader(http.StatusOK)
		}
	})
	defer srv.Close()

	_, err := FetchModels(srv.URL+"/anthropic", "sk-bad")
	if err == nil {
		t.Fatal("expected HTTP 401 error, got nil")
	}
	if !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("expected error to mention HTTP 401, got %q", err.Error())
	}
	if hitSecond {
		t.Fatal("must NOT try 2nd candidate on 401 (fail-fast), but /v1/models was hit")
	}
}

// TestFetchModels_NetworkErrFailFast 候选连接失败 → 立即返回网络错误,不续试
// 后续同源候选(同 host 多候选同源失败,续试无价值;且 httptest 关停后任一候选必连不上)。
func TestFetchModels_NetworkErrFailFast(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"x"}]}`))
	})
	baseURL := srv.URL + "/anthropic"
	srv.Close() // 立即关停,所有候选必连不上。

	_, err := FetchModels(baseURL, "sk-test")
	if err == nil {
		t.Fatal("expected network error, got nil")
	}
	// 网络错误文案形态多样(connect refused / EOF / ...),只断言非 "All candidates failed"
	// (后者是续试全部耗尽后才出现的,网络错误应 fail-fast 不会走到那)。
	if strings.HasPrefix(err.Error(), "All candidates failed") {
		t.Fatalf("network error must fail-fast, not reach 'All candidates failed': %q", err.Error())
	}
}

// --- 测试辅助 ---

func newServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	return httptest.NewServer(h)
}

func assertSliceEq(t *testing.T, label string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: expected %d candidates %v, got %d: %v", label, len(want), want, len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s: candidates[%d]=%q, want %q", label, i, got[i], want[i])
		}
	}
}
