package pricing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestGenerateByModel_Success 验证模型模式在标准 JSON 响应下的正常解析与结果映射。
func TestGenerateByModel_Success(t *testing.T) {
	mockContent := `[
		{"name":"test-gpt-4o","input":2.5,"output":10.0,"cached":1.25,"estimated":false,"source_url":"https://openai.com/api/pricing/"},
		{"name":"test-claude","input":3.0,"output":15.0,"cached":0,"estimated":false}
	]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-local-aigen" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var req modelChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Model != "gpt-4o-mini" {
			http.Error(w, "unexpected model", http.StatusBadRequest)
			return
		}

		resp := modelChatResponse{
			Choices: []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			}{
				{
					Message: struct {
						Content string `json:"content"`
					}{Content: mockContent},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	var stages []string
	progressFn := func(stage, status string) {
		stages = append(stages, stage)
	}

	candidates := []string{"test-gpt-4o", "test-claude"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	results, err := GenerateByModel(ctx, srv.Client(), srv.URL, "gpt-4o-mini", candidates, progressFn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	r1 := results["test-gpt-4o"]
	if r1.Rate.Input != 2.5 || r1.Rate.Output != 10.0 || r1.Rate.Cached != 1.25 {
		t.Errorf("unexpected rate for test-gpt-4o: %+v", r1.Rate)
	}
	if len(r1.Sources) != 1 || r1.Sources[0].URI != "https://openai.com/api/pricing/" {
		t.Errorf("unexpected sources for test-gpt-4o: %+v", r1.Sources)
	}

	// test-claude cached 为 0, 应触发 0.25x input 兜底估算并标 Estimated
	r2 := results["test-claude"]
	if r2.Rate.Input != 3.0 || r2.Rate.Cached != 0.75 {
		t.Errorf("expected cached fallback 0.75, got %+v", r2.Rate)
	}
	if !r2.Estimated {
		t.Errorf("expected Estimated=true for cached fallback, got false")
	}

	// 验证各进度阶段触发
	hasCalling := false
	hasParse := false
	hasDone := false
	for _, s := range stages {
		if s == "calling-model" {
			hasCalling = true
		}
		if s == "parse-result" {
			hasParse = true
		}
		if s == "done" {
			hasDone = true
		}
	}
	if !hasCalling || !hasParse || !hasDone {
		t.Errorf("missing stage in stages: %v", stages)
	}
}

// TestGenerateByModel_MarkdownCodeBlock 验证模型输出包含 ```json 代码块及前后说明文字时的容错解析。
func TestGenerateByModel_MarkdownCodeBlock(t *testing.T) {
	mockContent := "这是为您分析的最新定价：\n```json\n" +
		`[{"name":"deepseek-chat","input":0.14,"output":0.28,"cached":0.014,"estimated":false}]` +
		"\n```\n仅供参考。"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := modelChatResponse{
			Choices: []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			}{
				{
					Message: struct {
						Content string `json:"content"`
					}{Content: mockContent},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	results, err := GenerateByModel(ctx, srv.Client(), srv.URL, "deepseek-chat", []string{"deepseek-chat"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results["deepseek-chat"]
	if r.Rate.Input != 0.14 || r.Rate.Output != 0.28 {
		t.Errorf("unexpected rates: %+v", r.Rate)
	}
}

// TestGenerateByModel_PartialAndMissing 验证模型缺位模型项时自动补零并标记估算。
func TestGenerateByModel_PartialAndMissing(t *testing.T) {
	mockContent := `[{"name":"model-a","input":1.0,"output":2.0,"cached":0.5}]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := modelChatResponse{
			Choices: []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			}{
				{
					Message: struct {
						Content string `json:"content"`
					}{Content: mockContent},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	candidates := []string{"model-a", "model-missing"}
	results, err := GenerateByModel(ctx, srv.Client(), srv.URL, "gpt-4o", candidates, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	miss := results["model-missing"]
	if miss.Rate.Input != 0 || miss.Rate.Output != 0 || miss.Rate.Cached != 0 {
		t.Errorf("expected missing model rates to be 0, got %+v", miss.Rate)
	}
	if !miss.Estimated {
		t.Errorf("expected missing model Estimated=true, got false")
	}
}

// TestGenerateByModel_HttpError 验证中继返回 HTTP 错误时的异常捕获与解析。
func TestGenerateByModel_HttpError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"quota limit exceeded","type":"rate_limit"}}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := GenerateByModel(ctx, srv.Client(), srv.URL, "gpt-4o", []string{"any"}, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "模型生成失败 (HTTP 429): quota limit exceeded" {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestGenerateByModel_Validation 验证空入参边界。
func TestGenerateByModel_Validation(t *testing.T) {
	ctx := context.Background()

	// 空候选列表直接返回空
	res, err := GenerateByModel(ctx, nil, "http://localhost", "gpt-4o", nil, nil)
	if err != nil || len(res) != 0 {
		t.Fatalf("expected empty result without error, got res=%v, err=%v", res, err)
	}

	// 空模型名报错
	_, err = GenerateByModel(ctx, nil, "http://localhost", "", []string{"m1"}, nil)
	if err == nil {
		t.Fatal("expected error for empty model, got nil")
	}

	// 空端点报错
	_, err = GenerateByModel(ctx, nil, "", "gpt-4o", []string{"m1"}, nil)
	if err == nil {
		t.Fatal("expected error for empty endpoint, got nil")
	}
}
