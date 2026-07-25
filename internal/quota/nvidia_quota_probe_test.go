package quota

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"antigravity-proxy/internal/account"
)

// TestFetchNvidiaQuota_OK 验证上游正常返回模型清单时，FetchQuota 走 nvidia 分支
// 真实探活并返回语义 bucket（含"可用模型数 N 个"）以及 credits=模型数。
func TestFetchNvidiaQuota_OK(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer nvapi-test-key" {
			t.Errorf("expected Bearer auth, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		// 模拟 NVIDIA 上游 /v1/models 标准结构
		_, _ = w.Write([]byte(`{"data":[{"id":"meta/llama-3.3-70b-instruct"},{"id":"moonshotai/kimi-k2.5"},{"id":"z-ai/glm-5.2"}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	acc := &account.Account{
		ID:          "nv-1",
		Email:       "test-nvidia",
		Provider:    "nvidia",
		BaseURL:     srv.URL, // 已含 http://127.0.0.1:port，无 /v1 后缀
		AccessToken: "nvapi-test-key",
		Enabled:     true,
	}

	q := NewQuotaService()
	q.Init(t.TempDir())

	res, err := q.FetchQuota(acc, nil, nil)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if res.Tier != "NVIDIA" {
		t.Errorf("expected tier NVIDIA, got %q", res.Tier)
	}
	if res.Credits == nil || int(*res.Credits) != 3 {
		t.Errorf("expected credits=3, got %v", res.Credits)
	}
	if len(res.Buckets) != 1 {
		t.Fatalf("expected 1 bucket, got %d", len(res.Buckets))
	}
	b := res.Buckets[0]
	if !strings.Contains(b.ModelID, "3 个") && !strings.Contains(b.ModelID, "3 models") {
		t.Errorf("expected modelId to mention 3 models, got %q", b.ModelID)
	}
	if b.Group != "NVIDIA 第三方 API Key" {
		t.Errorf("expected GROUP=NVIDIA 第三方 API Key, got %q", b.Group)
	}
	if b.RemainPercent != 100 {
		t.Errorf("expected RemainPercent=100, got %d", b.RemainPercent)
	}
}

// TestFetchNvidiaQuota_BaseURLWithV1Suffix 验证 BaseURL 以 /v1 结尾时
// 端点拼接命中 /v1/models 而非 /models（更不会出现 /v1/v1/models 双段错误）。
func TestFetchNvidiaQuota_BaseURLWithV1Suffix(t *testing.T) {
	mux := http.NewServeMux()
	// /v1 后缀时 endpoint=baseURL+/models => 命中 /v1/models，返回模型清单
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"a"},{"id":"b"}]}`))
	})
	// 显式 404 /models：证明不会把 /v1 后缀的 baseURL 错误拼成裸 /models，
	// 也证明不会拼出 /v1/v1/models（ServeMux 未注册则 404）。
	mux.HandleFunc("/models", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	acc := &account.Account{
		ID:          "nv-2",
		Email:       "test-nvidia-v1",
		Provider:    "nvidia",
		BaseURL:     srv.URL + "/v1", // 显式带 /v1 后缀
		AccessToken: "nvapi-test-key",
		Enabled:     true,
	}

	q := NewQuotaService()
	q.Init(t.TempDir())

	res, err := q.FetchQuota(acc, nil, nil)
	if err != nil {
		t.Fatalf("expected success on /v1/models, got error: %v", err)
	}
	if res.Credits == nil || int(*res.Credits) != 2 {
		t.Errorf("expected 2 models, got %v", res.Credits)
	}
}

// TestFetchNvidiaQuota_Unauthorized 验证上游返回 401 时，
// FetchQuota 返回带"配额请求失败"前缀的 error，而非伪成功。
func TestFetchNvidiaQuota_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid api key"}`))
	}))
	defer srv.Close()

	acc := &account.Account{
		ID:          "nv-3",
		Email:       "test-nvidia-badkey",
		Provider:    "nvidia",
		BaseURL:     srv.URL,
		AccessToken: "nvapi-wrong-key",
		Enabled:     true,
	}

	q := NewQuotaService()
	q.Init(t.TempDir())

	res, err := q.FetchQuota(acc, nil, nil)
	if err == nil {
		t.Fatalf("expected error for 401 upstream, got result=%+v", res)
	}
	if !strings.HasPrefix(err.Error(), "配额请求失败") {
		t.Errorf("expected error prefixed with 配额请求失败, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "HTTP 401") {
		t.Errorf("expected error to mention HTTP 401, got %q", err.Error())
	}
}

// TestFetchNvidiaQuota_EmptyBaseURL 验证账号未配置 Base URL 时返回可读错误。
func TestFetchNvidiaQuota_EmptyBaseURL(t *testing.T) {
	acc := &account.Account{
		ID:          "nv-4",
		Email:       "test-nvidia-nobase",
		Provider:    "nvidia",
		BaseURL:     "",
		AccessToken: "nvapi-test-key",
		Enabled:     true,
	}
	q := NewQuotaService()
	q.Init(t.TempDir())

	if _, err := q.FetchQuota(acc, nil, nil); err == nil {
		t.Fatal("expected error for empty BaseURL, got nil")
	}
}
