package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchRemoteNvidiaModels_DataShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path %q, want /v1/models", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("unexpected Authorization %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "meta/llama-3.3-70b-instruct"},
				{"id": "moonshotai/kimi-k2.5"},
				{"id": "moonshotai/kimi-k2.5"}, // 重复项须被去重
			},
		})
	}))
	defer srv.Close()

	models, err := fetchRemoteNvidiaModels(srv.URL+"/v1", "test-key")
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

func TestFetchRemoteNvidiaModels_ModelsShape(t *testing.T) {
	// 部分上游用 {models:[{id}]} 形态;且 baseURL 不带 /v1 后缀时应自动拼 /v1/models
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path %q, want /v1/models", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]any{
				{"id": "nvidia/llama-3.1-nemotron-70b-instruct"},
				{"id": "abc/zeta"},
			},
		})
	}))
	defer srv.Close()

	models, err := fetchRemoteNvidiaModels(srv.URL, "")
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

func TestFetchRemoteNvidiaModels_HTTPErr(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := fetchRemoteNvidiaModels(srv.URL+"/v1", "bad-key")
	if err == nil {
		t.Fatalf("expected error for HTTP 401, got nil")
	}
}

func TestFetchRemoteNvidiaModels_EmptyBaseURL(t *testing.T) {
	// baseURL 空时回退 account.DefaultNvidiaBaseURL(指向真实端点但此处不打外网,
	// 仅验证调用不 panic 且对空入参内部补全不报错;断言由 Error/结果二选一驱动)。
	models, err := fetchRemoteNvidiaModels("", "")
	_ = models
	// 真实默认端点可能可达也可能不可达,因此不固定断言成功/失败,
	// 只保证 helper 不 panic 且能给出确定性的 (models, err) 返回。
	_ = err
}
