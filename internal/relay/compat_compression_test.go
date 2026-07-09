package relay

import (
	"bytes"
	"database/sql"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"antigravity-proxy/internal/db"
	"antigravity-proxy/internal/settings"

	_ "modernc.org/sqlite"
)

// MockRoundTripper 用于 Mock http.Client 的请求响应
type MockRoundTripper struct {
	roundTripFn func(req *http.Request) (*http.Response, error)
}

func (m *MockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.roundTripFn(req)
}

// MockSettingsManager 用于 mock 配置项
type MockSettingsManager struct {
	settings.ManagerInterface
	cfg settings.SessionOptimizationConfig
}

func (m *MockSettingsManager) GetSessionOptimization() settings.SessionOptimizationConfig {
	return m.cfg
}

func (m *MockSettingsManager) GetRelayModelMapping() []settings.ModelMappingEntry {
	return []settings.ModelMappingEntry{
		{ClientModel: "gemini-2.5-pro", TargetModel: "gemini-2.5-pro", Expose: true},
		{ClientModel: "gemini-2.5-flash-lite", TargetModel: "gemini-2.5-flash-lite", Expose: true},
	}
}

func TestSessionCompression(t *testing.T) {
	// 1. 初始化内存 SQLite 数据库
	sqliteDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open memory db: %v", err)
	}
	defer sqliteDB.Close()

	// 创建 request_logs 表
	_, err = sqliteDB.Exec(`
		CREATE TABLE request_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			server_log_id INTEGER NOT NULL DEFAULT 0,
			req_id TEXT NOT NULL,
			timestamp TEXT NOT NULL,
			mode TEXT NOT NULL,
			user_id TEXT,
			model_name TEXT NOT NULL,
			in_tokens INTEGER NOT NULL DEFAULT 0,
			out_tokens INTEGER NOT NULL DEFAULT 0,
			cached_tokens INTEGER NOT NULL DEFAULT 0,
			cost REAL NOT NULL DEFAULT 0.0,
			input_cost REAL NOT NULL DEFAULT 0.0,
			output_cost REAL NOT NULL DEFAULT 0.0,
			cached_cost REAL NOT NULL DEFAULT 0.0,
			duration_ms INTEGER NOT NULL DEFAULT 0,
			status_code INTEGER NOT NULL DEFAULT 200,
			method TEXT NOT NULL DEFAULT '',
			host TEXT NOT NULL DEFAULT '',
			path TEXT NOT NULL DEFAULT '',
			session_id TEXT NOT NULL DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
	`)
	if err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}

	// 插入一条 105,000 token 的成功记录作为会话历史特征
	sessionKey := "auth:acc:testsession"
	_, err = sqliteDB.Exec(`
		INSERT INTO request_logs (
			req_id, timestamp, mode, model_name, in_tokens, status_code, session_id
		) VALUES (
			'test_req_1', ?, 'relay', 'gemini-2.5-pro', 105000, 200, ?
		)
	`, time.Now().Format(time.RFC3339), sessionKey)
	if err != nil {
		t.Fatalf("failed to insert mock log: %v", err)
	}

	// 绑定 GlobalDB
	oldDB := db.GlobalDB
	db.GlobalDB = sqliteDB
	defer func() { db.GlobalDB = oldDB }()

	// 2. 构造 Mock 配置管理器
	mockSettings := &MockSettingsManager{
		cfg: settings.SessionOptimizationConfig{
			EnableCustomCompression: true,
			MaxTokensThreshold:      100000,
			CompressionStrategy:     "summarize",
			SummaryModel:            "gemini-2.5-flash-lite",
			KeepRecentTurns:         2, // 保留 2 轮 (即 4 条 contents)
		},
	}

	// 3. 构造 Mock http.Client
	mockClient := &http.Client{
		Transport: &MockRoundTripper{
			roundTripFn: func(req *http.Request) (*http.Response, error) {
				// 期望代理向本地 18443 请求生成摘要
				if strings.Contains(req.URL.Path, "gemini-2.5-flash-lite:generateContent") {
					respBody := `{
						"candidates": [{
							"content": {
								"parts": [{"text": "MOCK SUMMARY CONTENT"}]
							}
						}]
					}`
					return &http.Response{
						StatusCode: 200,
						Body:       io.NopCloser(bytes.NewReader([]byte(respBody))),
						Header:     make(http.Header),
					}, nil
				}
				return &http.Response{
					StatusCode: 500,
					Body:       io.NopCloser(bytes.NewReader([]byte(`{"error":"unexpected request"}`))),
				}, nil
			},
		},
	}

	// 4. 构造原始的聊天请求 (超过 2 轮，含 6 条 contents)
	geminiReq := &GeminiRequest{
		Contents: []GeminiContent{
			{Role: "user", Parts: []GeminiPart{{Text: "Hello 1"}}},
			{Role: "model", Parts: []GeminiPart{{Text: "Hi 1"}}},
			{Role: "user", Parts: []GeminiPart{{Text: "Hello 2"}}},
			{Role: "model", Parts: []GeminiPart{{Text: "Hi 2"}}},
			{Role: "user", Parts: []GeminiPart{{Text: "Hello 3"}}},
			{Role: "model", Parts: []GeminiPart{{Text: "Hi 3"}}},
		},
	}

	req, _ := http.NewRequest("POST", "http://localhost/v1/chat/completions", nil)

	// 5. 执行检查和优化
	finalModel, compressed := CheckAndOptimizeSession(
		req,
		geminiReq,
		"gemini-2.5-pro",
		sessionKey,
		"user_key",
		"user_id",
		"apikey_id",
		mockClient,
		mockSettings,
		func(msg string) {},
	)

	// 6. 断言结果
	if !compressed {
		t.Errorf("expected session to be compressed")
	}
	if finalModel != "gemini-2.5-pro" {
		t.Errorf("expected targetModel to remain gemini-2.5-pro for masking, got %s", finalModel)
	}

	// 校验 Contents 是否被压缩重组
	// 保留 KeepRecentTurns*2 = 4 条，加上 2 条摘要背景，一共 6 条消息
	if len(geminiReq.Contents) != 6 {
		t.Errorf("expected 6 messages after compression, got %d", len(geminiReq.Contents))
	}

	// 首条应当是背景总结说明
	firstMsg := geminiReq.Contents[0].Parts[0].Text
	if !strings.Contains(firstMsg, "MOCK SUMMARY CONTENT") {
		t.Errorf("expected summary content to be present in first message, got: %s", firstMsg)
	}
}

func TestClientRequestDowngrade(t *testing.T) {
	// 测试客户端自发的压缩请求如何触发模型降级路由
	mockSettings := &MockSettingsManager{
		cfg: settings.SessionOptimizationConfig{
			EnableCustomCompression: true,
			SummaryModel:            "gemini-2.5-flash-lite",
		},
	}

	// 构造自发的压缩请求
	geminiReq := &GeminiRequest{
		SystemInstruction: &GeminiInstruction{
			Parts: []GeminiPart{{Text: "Please summarize the chat context"}},
		},
		Contents: []GeminiContent{
			{Role: "user", Parts: []GeminiPart{{Text: "Summarize this"}}},
		},
	}

	req, _ := http.NewRequest("POST", "http://localhost/v1/chat/completions", nil)

	finalModel, compressed := CheckAndOptimizeSession(
		req,
		geminiReq,
		"gemini-2.5-pro",
		"session_key",
		"user_key",
		"user_id",
		"apikey_id",
		nil,
		mockSettings,
		func(msg string) {},
	)

	if !compressed {
		t.Errorf("expected compression to be flagged as true (hijacked)")
	}
	if finalModel != "gemini-2.5-flash-lite" {
		t.Errorf("expected model to be routed to gemini-2.5-flash-lite, got %s", finalModel)
	}
}

func TestCompressionFailureReturnsFalse(t *testing.T) {
	// 测试：当摘要模型调用失败时，executeActiveCompression 应返回 false，
	// CheckAndOptimizeSession 也应返回 compressed=false，不破坏原始请求体
	sqliteDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open memory db: %v", err)
	}
	defer sqliteDB.Close()

	_, err = sqliteDB.Exec(`
		CREATE TABLE request_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			server_log_id INTEGER NOT NULL DEFAULT 0,
			req_id TEXT NOT NULL,
			timestamp TEXT NOT NULL,
			mode TEXT NOT NULL,
			user_id TEXT,
			model_name TEXT NOT NULL,
			in_tokens INTEGER NOT NULL DEFAULT 0,
			out_tokens INTEGER NOT NULL DEFAULT 0,
			cached_tokens INTEGER NOT NULL DEFAULT 0,
			cost REAL NOT NULL DEFAULT 0.0,
			input_cost REAL NOT NULL DEFAULT 0.0,
			output_cost REAL NOT NULL DEFAULT 0.0,
			cached_cost REAL NOT NULL DEFAULT 0.0,
			duration_ms INTEGER NOT NULL DEFAULT 0,
			status_code INTEGER NOT NULL DEFAULT 200,
			method TEXT NOT NULL DEFAULT '',
			host TEXT NOT NULL DEFAULT '',
			path TEXT NOT NULL DEFAULT '',
			session_id TEXT NOT NULL DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
	`)
	if err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}

	sessionKey := "auth:acc:fail_test"
	_, err = sqliteDB.Exec(`
		INSERT INTO request_logs (
			req_id, timestamp, mode, model_name, in_tokens, status_code, session_id
		) VALUES (
			'test_req_fail', ?, 'relay', 'gemini-2.5-pro', 160000, 200, ?
		)
	`, time.Now().Format(time.RFC3339), sessionKey)
	if err != nil {
		t.Fatalf("failed to insert mock log: %v", err)
	}

	oldDB := db.GlobalDB
	db.GlobalDB = sqliteDB
	defer func() { db.GlobalDB = oldDB }()

	mockSettings := &MockSettingsManager{
		cfg: settings.SessionOptimizationConfig{
			EnableCustomCompression: true,
			MaxTokensThreshold:      150000,
			CompressionStrategy:     "summarize",
			SummaryModel:            "gemini-2.5-flash-lite",
			KeepRecentTurns:         2,
		},
	}

	// Mock 摘要模型返回 500 错误，模拟摘要调用失败
	mockClient := &http.Client{
		Transport: &MockRoundTripper{
			roundTripFn: func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: 500,
					Body:       io.NopCloser(bytes.NewReader([]byte(`{"error":"internal server error"}`))),
					Header:     make(http.Header),
				}, nil
			},
		},
	}

	originalContents := []GeminiContent{
		{Role: "user", Parts: []GeminiPart{{Text: "Hello 1"}}},
		{Role: "model", Parts: []GeminiPart{{Text: "Hi 1"}}},
		{Role: "user", Parts: []GeminiPart{{Text: "Hello 2"}}},
		{Role: "model", Parts: []GeminiPart{{Text: "Hi 2"}}},
		{Role: "user", Parts: []GeminiPart{{Text: "Hello 3"}}},
		{Role: "model", Parts: []GeminiPart{{Text: "Hi 3"}}},
	}

	geminiReq := &GeminiRequest{
		Contents: originalContents,
	}

	req, _ := http.NewRequest("POST", "http://localhost/v1/chat/completions", nil)

	finalModel, compressed := CheckAndOptimizeSession(
		req,
		geminiReq,
		"gemini-2.5-pro",
		sessionKey,
		"user_key",
		"user_id",
		"apikey_id",
		mockClient,
		mockSettings,
		func(msg string) {},
	)

	// 关键断言：压缩失败时 compressed 必须为 false
	if compressed {
		t.Errorf("expected compressed=false when summary model fails, got true")
	}
	// 模型不应被改变
	if finalModel != "gemini-2.5-pro" {
		t.Errorf("expected model to remain gemini-2.5-pro, got %s", finalModel)
	}
	// Contents 不应被修改
	if len(geminiReq.Contents) != len(originalContents) {
		t.Errorf("expected contents to remain unchanged (%d items), got %d", len(originalContents), len(geminiReq.Contents))
	}
}

func TestDynamicSessionCompression(t *testing.T) {
	// 初始化内存 SQLite
	sqliteDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open memory db: %v", err)
	}
	defer sqliteDB.Close()

	_, err = sqliteDB.Exec(`
		CREATE TABLE request_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			server_log_id INTEGER NOT NULL DEFAULT 0,
			req_id TEXT NOT NULL,
			timestamp TEXT NOT NULL,
			mode TEXT NOT NULL,
			user_id TEXT,
			model_name TEXT NOT NULL,
			in_tokens INTEGER NOT NULL DEFAULT 0,
			out_tokens INTEGER NOT NULL DEFAULT 0,
			cached_tokens INTEGER NOT NULL DEFAULT 0,
			cost REAL NOT NULL DEFAULT 0.0,
			input_cost REAL NOT NULL DEFAULT 0.0,
			output_cost REAL NOT NULL DEFAULT 0.0,
			cached_cost REAL NOT NULL DEFAULT 0.0,
			duration_ms INTEGER NOT NULL DEFAULT 0,
			status_code INTEGER NOT NULL DEFAULT 200,
			method TEXT NOT NULL DEFAULT '',
			host TEXT NOT NULL DEFAULT '',
			path TEXT NOT NULL DEFAULT '',
			session_id TEXT NOT NULL DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
	`)
	if err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}

	sessionKey := "auth:acc:dynamicsession"
	_, err = sqliteDB.Exec(`
		INSERT INTO request_logs (
			req_id, timestamp, mode, model_name, in_tokens, status_code, session_id
		) VALUES (
			'test_req_dynamic', ?, 'relay', 'gemini-2.5-pro', 120000, 200, ?
		)
	`, time.Now().Format(time.RFC3339), sessionKey)
	if err != nil {
		t.Fatalf("failed to insert mock log: %v", err)
	}

	oldDB := db.GlobalDB
	db.GlobalDB = sqliteDB
	defer func() { db.GlobalDB = oldDB }()

	// KeepRecentTurns = 5 => keepRecentCount = 10.
	// But we only provide 3 messages in Contents (user1, model1, user2).
	mockSettings := &MockSettingsManager{
		cfg: settings.SessionOptimizationConfig{
			EnableCustomCompression: true,
			MaxTokensThreshold:      100000,
			CompressionStrategy:     "summarize",
			SummaryModel:            "gemini-2.5-flash-lite",
			KeepRecentTurns:         5,
		},
	}

	mockClient := &http.Client{
		Transport: &MockRoundTripper{
			roundTripFn: func(req *http.Request) (*http.Response, error) {
				if strings.Contains(req.URL.Path, "gemini-2.5-flash-lite:generateContent") {
					respBody := `{
						"candidates": [{
							"content": {
								"parts": [{"text": "DYNAMIC MOCK SUMMARY"}]
							}
						}]
					}`
					return &http.Response{
						StatusCode: 200,
						Body:       io.NopCloser(bytes.NewReader([]byte(respBody))),
						Header:     make(http.Header),
					}, nil
				}
				return &http.Response{
					StatusCode: 500,
					Body:       io.NopCloser(bytes.NewReader([]byte(`{"error":"unexpected request"}`))),
				}, nil
			},
		},
	}

	geminiReq := &GeminiRequest{
		Contents: []GeminiContent{
			{Role: "user", Parts: []GeminiPart{{Text: "Hello 1"}}},
			{Role: "model", Parts: []GeminiPart{{Text: "Hi 1"}}},
			{Role: "user", Parts: []GeminiPart{{Text: "Hello 2"}}},
		},
	}

	req, _ := http.NewRequest("POST", "http://localhost/v1/chat/completions", nil)

	finalModel, compressed := CheckAndOptimizeSession(
		req,
		geminiReq,
		"gemini-2.5-pro",
		sessionKey,
		"user_key",
		"user_id",
		"apikey_id",
		mockClient,
		mockSettings,
		func(msg string) {},
	)

	if !compressed {
		t.Errorf("expected session to be compressed via dynamic scaling")
	}
	if finalModel != "gemini-2.5-pro" {
		t.Errorf("expected targetModel to remain gemini-2.5-pro, got %s", finalModel)
	}

	// 1 message kept (user2), plus 2 summary messages (user, model) => 3 messages total
	if len(geminiReq.Contents) != 3 {
		t.Errorf("expected 3 messages after dynamic compression, got %d", len(geminiReq.Contents))
	}

	// Check if roles alternate: user, model, user
	if geminiReq.Contents[0].Role != "user" || geminiReq.Contents[1].Role != "model" || geminiReq.Contents[2].Role != "user" {
		t.Errorf("invalid alternating roles: %s, %s, %s", geminiReq.Contents[0].Role, geminiReq.Contents[1].Role, geminiReq.Contents[2].Role)
	}

	if !strings.Contains(geminiReq.Contents[0].Parts[0].Text, "DYNAMIC MOCK SUMMARY") {
		t.Errorf("expected dynamic summary content, got: %s", geminiReq.Contents[0].Parts[0].Text)
	}
}
