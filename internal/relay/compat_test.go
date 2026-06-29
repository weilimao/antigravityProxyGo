package relay

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// TestModelMapping 验证模型名称映射是否正确
func TestModelMapping(t *testing.T) {
	tests := []struct {
		clientModel string
		expected    string
	}{
		{"claude-3-5-sonnet-20241022", "gemini-1.5-pro"},
		{"claude-3-opus-20240229", "gemini-1.5-pro"},
		{"claude-3-5-haiku-20241022", "gemini-1.5-flash"},
		{"gpt-4o-mini", "gemini-1.5-pro"},
		{"gpt-3.5-turbo", "gemini-1.5-flash"},
		{"o1-preview", "gemini-2.0-flash"},
		{"unknown-model", "gemini-1.5-pro"}, // Default fallback
	}

	for _, tt := range tests {
		actual := MapClientModelToGemini(tt.clientModel, nil)
		if actual != tt.expected {
			t.Errorf("MapClientModelToGemini(%q) = %q; expected %q", tt.clientModel, actual, tt.expected)
		}
	}
}

// TestTranslateOpenAIToGemini 验证 OpenAI -> Gemini 协议转换
func TestTranslateOpenAIToGemini(t *testing.T) {
	temp := 0.7
	maxTokens := 100
	req := &OpenAIRequest{
		Model: "gpt-4o",
		Messages: []OpenAIMessage{
			{Role: "system", Content: "You are a coding assistant."},
			{Role: "user", Content: "Hello!"},
			{Role: "assistant", Content: "Hi, how can I help you?"},
			{Role: "user", Content: "Write a Go function."},
		},
		Temperature: &temp,
		MaxTokens:   &maxTokens,
	}

	geminiReq := TranslateOpenAIToGemini(req)

	// 1. system instruction 应当被正确提取
	if geminiReq.SystemInstruction == nil {
		t.Fatal("expected SystemInstruction to be populated")
	}
	if len(geminiReq.SystemInstruction.Parts) != 1 || geminiReq.SystemInstruction.Parts[0].Text != "You are a coding assistant." {
		t.Errorf("unexpected system instruction: %+v", geminiReq.SystemInstruction)
	}

	// 2. 正常对话消息数应当为 3 条 (排除了 system)
	if len(geminiReq.Contents) != 3 {
		t.Fatalf("expected 3 content entries, got %d", len(geminiReq.Contents))
	}

	// 3. 角色映射转换
	if geminiReq.Contents[0].Role != "user" || geminiReq.Contents[0].Parts[0].Text != "Hello!" {
		t.Errorf("first message role/content mismatch: %+v", geminiReq.Contents[0])
	}
	if geminiReq.Contents[1].Role != "model" || geminiReq.Contents[1].Parts[0].Text != "Hi, how can I help you?" {
		t.Errorf("second message (assistant) role/content mismatch: %+v", geminiReq.Contents[1])
	}

	// 4. 配置映射
	if geminiReq.GenerationConfig == nil || *geminiReq.GenerationConfig.Temperature != 0.7 || *geminiReq.GenerationConfig.MaxOutputTokens != 100 {
		t.Errorf("generation config mapping mismatch: %+v", geminiReq.GenerationConfig)
	}
}

// TestTranslateAnthropicToGemini 验证 Anthropic -> Gemini 协议转换
func TestTranslateAnthropicToGemini(t *testing.T) {
	req := &AnthropicRequest{
		Model: "claude-3-5-sonnet",
		System: "You are a translator.",
		Messages: []AnthropicMessage{
			{
				Role: "user",
				Content: []AnthropicContent{{Type: "text", Text: "Hello, translate this."}},
			},
		},
	}

	geminiReq := TranslateAnthropicToGemini(req)

	if geminiReq.SystemInstruction == nil || geminiReq.SystemInstruction.Parts[0].Text != "You are a translator." {
		t.Errorf("system instruction mismatch: %+v", geminiReq.SystemInstruction)
	}

	if len(geminiReq.Contents) != 1 || geminiReq.Contents[0].Role != "user" || geminiReq.Contents[0].Parts[0].Text != "Hello, translate this." {
		t.Errorf("messages content translation mismatch: %+v", geminiReq.Contents)
	}
}

// TestHandleModelsEndpoint 验证 models 模型拉取端点的输出格式自适应
func TestHandleModelsEndpoint(t *testing.T) {
	handler := NewAPICompatHandler(nil, nil, nil, nil, nil, nil)

	// 1. 模拟 OpenAI 客户端拉取模型列表（不带 anthropic-version 头）
	reqOpenAI := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rrOpenAI := httptest.NewRecorder()

	handler.handleModels(rrOpenAI, reqOpenAI)

	if rrOpenAI.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rrOpenAI.Code)
	}

	var openResp map[string]interface{}
	if err := json.Unmarshal(rrOpenAI.Body.Bytes(), &openResp); err != nil {
		t.Fatalf("failed to parse OpenAI response: %v", err)
	}

	if openResp["object"] != "list" {
		t.Errorf("expected object type to be 'list', got %v", openResp["object"])
	}
	dataArr, ok := openResp["data"].([]interface{})
	if !ok || len(dataArr) == 0 {
		t.Fatal("expected OpenAI data array list to be populated")
	}

	// 2. 模拟 Anthropic / Claude Code 客户端拉取模型列表 (带 anthropic-version 头)
	reqAnth := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	reqAnth.Header.Set("anthropic-version", "2023-06-01")
	rrAnth := httptest.NewRecorder()

	handler.handleModels(rrAnth, reqAnth)

	if rrAnth.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rrAnth.Code)
	}

	var anthResp map[string]interface{}
	if err := json.Unmarshal(rrAnth.Body.Bytes(), &anthResp); err != nil {
		t.Fatalf("failed to parse Anthropic response: %v", err)
	}

	if _, exists := anthResp["object"]; exists {
		t.Error("Anthropic response should not contain 'object' field")
	}
	anthData, ok := anthResp["data"].([]interface{})
	if !ok || len(anthData) == 0 {
		t.Fatal("expected Anthropic data array list to be populated")
	}

	firstModel, ok := anthData[0].(map[string]interface{})
	if !ok || firstModel["type"] != "model" || firstModel["display_name"] == "" {
		t.Errorf("Anthropic model block structure invalid: %+v", firstModel)
	}
}

// TestAnthropicMessageJSONUnmarshal 验证 AnthropicMessage.UnmarshalJSON 兼容 string 和 array
func TestAnthropicMessageJSONUnmarshal(t *testing.T) {
	// 1. 测试内容为 array of blocks 的标准格式
	jsonArray := []byte(`{"role": "user", "content": [{"type": "text", "text": "Hello, array!"}]}`)
	var msg1 AnthropicMessage
	if err := json.Unmarshal(jsonArray, &msg1); err != nil {
		t.Fatalf("failed to unmarshal array format: %v", err)
	}
	if msg1.Role != "user" || len(msg1.Content) != 1 || msg1.Content[0].Text != "Hello, array!" {
		t.Errorf("unexpected msg1 structure: %+v", msg1)
	}

	// 2. 测试内容为纯 string 的简写格式（如 Claude Code 默认发送）
	jsonString := []byte(`{"role": "user", "content": "Hello, string!"}`)
	var msg2 AnthropicMessage
	if err := json.Unmarshal(jsonString, &msg2); err != nil {
		t.Fatalf("failed to unmarshal string format: %v", err)
	}
	if msg2.Role != "user" || len(msg2.Content) != 1 || msg2.Content[0].Text != "Hello, string!" || msg2.Content[0].Type != "text" {
		t.Errorf("unexpected msg2 structure: %+v", msg2)
	}
}

// TestOfficialKeyBypass 验证 authManager 识别官方 Key 前缀并自动映射至本地首个启用账户的放行逻辑
func TestOfficialKeyBypass(t *testing.T) {
	// 1. 初始化临时的 UserManager 并增加一个 Enabled 用户
	userMgr := NewUserManager()
	// 设置测试持久化路径，确保测试不干扰真实用户文件
	userMgr.persistPath = "relay_users_test.json"
	defer os.Remove(userMgr.persistPath)

	user, err := userMgr.AddUser("test_user", "password123", "Unit Test User")
	if err != nil {
		t.Fatalf("failed to add user: %v", err)
	}

	// 2. 初始化 AuthManager
	authMgr := NewAuthManager(userMgr)

	// 3. 验证传入官方前缀 sk-ant- Key 时被自动放通，且会话 UserID 被智能重定向至我们添加的本地真实 test_user ID
	session, err := authMgr.ValidateToken("sk-ant-api03-uRmS5MQ2UACPR0d6aUpzF1K1sSKJEQv-YlvMFD-Vs8ee")
	if err != nil {
		t.Fatalf("ValidateToken should dynamically bypass official key, got err: %v", err)
	}
	if session == nil {
		t.Fatal("expected returned RelaySession to be non-nil")
	}
	if session.UserID != user.ID {
		t.Errorf("expected session.UserID to be mapped to real user ID %q, got %q", user.ID, session.UserID)
	}

	// 4. 验证传入普通非法令牌时，依旧被拒返回 error
	_, err = authMgr.ValidateToken("arbitrary-bad-token")
	if err == nil {
		t.Fatal("expected ValidateToken to reject arbitrary bad token, but it passed")
	}
}

// TestAnthropicRequestSystemJSONUnmarshal 验证 AnthropicRequest.UnmarshalJSON 兼容 string 和 array 格式的 system 字段
func TestAnthropicRequestSystemJSONUnmarshal(t *testing.T) {
	// 1. 测试 system 为纯字符串
	jsonStr := []byte(`{
		"model": "claude-3-5-sonnet",
		"messages": [],
		"system": "You are a helpful assistant."
	}`)
	var req1 AnthropicRequest
	if err := json.Unmarshal(jsonStr, &req1); err != nil {
		t.Fatalf("failed to unmarshal string system: %v", err)
	}
	if req1.System != "You are a helpful assistant." {
		t.Errorf("expected system string to be 'You are a helpful assistant.', got %q", req1.System)
	}

	// 2. 测试 system 为数组（Claude Code 常用格式）
	jsonArr := []byte(`{
		"model": "claude-3-5-sonnet",
		"messages": [],
		"system": [
			{"type": "text", "text": "Part1 "},
			{"type": "text", "text": "Part2"}
		]
	}`)
	var req2 AnthropicRequest
	if err := json.Unmarshal(jsonArr, &req2); err != nil {
		t.Fatalf("failed to unmarshal array system: %v", err)
	}
	if req2.System != "Part1 Part2" {
		t.Errorf("expected system array to be joined to 'Part1 Part2', got %q", req2.System)
	}
}

// TestParseUnifiedOpenAIRequest 验证 ParseUnifiedOpenAIRequest 在解析传统与新版 Responses 协议时的行为
func TestParseUnifiedOpenAIRequest(t *testing.T) {
	// 1. 传统格式
	traditionalJSON := []byte(`{
		"model": "gpt-4o",
		"messages": [
			{"role": "user", "content": "hello"}
		]
	}`)
	req, err := ParseUnifiedOpenAIRequest(traditionalJSON)
	if err != nil {
		t.Fatalf("failed to parse traditional request: %v", err)
	}
	if len(req.Messages) != 1 || req.Messages[0].Content != "hello" {
		t.Errorf("unexpected parsed messages for traditional: %+v", req.Messages)
	}

	// 2. Responses 格式 (带 instructions 和 input block 数组)
	responsesJSON := []byte(`{
		"model": "gpt-4o",
		"instructions": "You are a robot.",
		"input": [
			{
				"role": "user",
				"content": [
					{"type": "input_text", "text": "how are you?"}
				]
			}
		]
	}`)
	req2, err := ParseUnifiedOpenAIRequest(responsesJSON)
	if err != nil {
		t.Fatalf("failed to parse responses request: %v", err)
	}
	if len(req2.Messages) != 2 {
		t.Fatalf("expected 2 messages (system + user), got %d", len(req2.Messages))
	}
	if req2.Messages[0].Role != "system" || req2.Messages[0].Content != "You are a robot." {
		t.Errorf("unexpected system instruction: %+v", req2.Messages[0])
	}
	if req2.Messages[1].Role != "user" || req2.Messages[1].Content != "how are you?" {
		t.Errorf("unexpected user message: %+v", req2.Messages[1])
	}
}

// TestTranslateOpenAIToGeminiRoleMerging 验证 TranslateOpenAIToGemini 自动合并连续相同角色消息以符合 Gemini API 规范
func TestTranslateOpenAIToGeminiRoleMerging(t *testing.T) {
	req := &OpenAIRequest{
		Model: "gpt-4o",
		Messages: []OpenAIMessage{
			{Role: "user", Content: "Hello part 1"},
			{Role: "user", Content: "Hello part 2"},
			{Role: "assistant", Content: "Hi, I am model"},
			{Role: "user", Content: "Ok, bye"},
		},
	}

	geminiReq := TranslateOpenAIToGemini(req)

	// 验证合并后的消息数应当为 3 条 (user merged, model, user)
	if len(geminiReq.Contents) != 3 {
		t.Fatalf("expected 3 content entries after role merging, got %d", len(geminiReq.Contents))
	}

	if geminiReq.Contents[0].Role != "user" {
		t.Errorf("expected role 'user', got %q", geminiReq.Contents[0].Role)
	}
	if len(geminiReq.Contents[0].Parts) != 2 {
		t.Errorf("expected 2 parts for merged user role, got %d", len(geminiReq.Contents[0].Parts))
	}
	if geminiReq.Contents[0].Parts[0].Text != "Hello part 1" || geminiReq.Contents[0].Parts[1].Text != "Hello part 2" {
		t.Errorf("unexpected contents for merged user role: %+v", geminiReq.Contents[0].Parts)
	}

	if geminiReq.Contents[1].Role != "model" || geminiReq.Contents[1].Parts[0].Text != "Hi, I am model" {
		t.Errorf("unexpected content for model role: %+v", geminiReq.Contents[1])
	}
	if geminiReq.Contents[2].Role != "user" || geminiReq.Contents[2].Parts[0].Text != "Ok, bye" {
		t.Errorf("unexpected content for final user role: %+v", geminiReq.Contents[2])
	}
}
