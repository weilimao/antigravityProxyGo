package relay

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"antigravity-proxy/internal/settings"
)

// stubOwnedBySettings 用一份带 OwnedBy 的自定义映射喂给 handleModels,验证动态归类。
type stubOwnedBySettings struct {
	settings.ManagerInterface
	mappings []settings.ModelMappingEntry
}

func (s *stubOwnedBySettings) GetRelayModelMapping() []settings.ModelMappingEntry {
	out := make([]settings.ModelMappingEntry, len(s.mappings))
	copy(out, s.mappings)
	return out
}

// TestHandleModels_DynamicOwnedBy 锁定方案二核心契约:
//   - ModelMappingEntry.OwnedBy 非空时,直接作为 /v1/models 里 owned_by 的值;
//   - OwnedBy 留空时,由 inferOwnedBy 按模型名前缀兜底(gemini→google, deepseek→deepseek…);
//   - 不再硬编码全 "google"。
func TestHandleModels_DynamicOwnedBy(t *testing.T) {
	h := &APICompatHandler{
		settingsMgr: &stubOwnedBySettings{
			mappings: []settings.ModelMappingEntry{
				{ClientModel: "gemini-2.5-pro", TargetModel: "gemini-2.5-pro", Expose: true, OwnedBy: "google"},
				{ClientModel: "deepseek-chat", TargetModel: "deepseek-chat", Expose: true, OwnedBy: "deepseek"}, // 显式
				{ClientModel: "claude-3-5-sonnet", TargetModel: "claude-3-5-sonnet", Expose: true},              // 空 OwnedBy → 兜底 anthropic
				{ClientModel: "internal-x", TargetModel: "x", Expose: false},                                    // 不暴露,不应出现
			},
		},
		logFn: func(string) {},
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	w := httptest.NewRecorder()
	h.handleModels(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp struct {
		Data []struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}

	byID := map[string]string{}
	for _, m := range resp.Data {
		byID[m.ID] = m.OwnedBy
	}
	if byID["gemini-2.5-pro"] != "google" {
		t.Errorf("gemini owned_by = %q, want google", byID["gemini-2.5-pro"])
	}
	if byID["deepseek-chat"] != "deepseek" {
		t.Errorf("deepseek owned_by = %q, want deepseek", byID["deepseek-chat"])
	}
	if byID["claude-3-5-sonnet"] != "anthropic" {
		t.Errorf("claude owned_by (inferred) = %q, want anthropic", byID["claude-3-5-sonnet"])
	}
	if _, seen := byID["internal-x"]; seen {
		t.Errorf("Expose=false model must not appear in list, but it did")
	}
}

// TestHandleModels_GatewayAPIKey_StripOwnedBy 验证在商业化网关系统 API Key 调用下，
// 返回的模型列表 owned_by 自动抹除为空，不暴露内部渠道标记；原生调用保留。
func TestHandleModels_GatewayAPIKey_StripOwnedBy(t *testing.T) {
	h := &APICompatHandler{
		settingsMgr: &stubOwnedBySettings{
			mappings: []settings.ModelMappingEntry{
				{ClientModel: "glm-5.3-flash", TargetModel: "z-ai/glm-5.3-flash", Expose: true, OwnedBy: "nvidia"},
				{ClientModel: "deepseek-v4.1-flash", TargetModel: "deepseek-v4.1-flash", Expose: true, OwnedBy: "workbuddy"},
			},
		},
		logFn: func(string) {},
	}

	// 1. 携带网关系统 API Key Session 访问
	reqGateway := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	wGateway := httptest.NewRecorder()
	session := &RelaySession{UserID: "u1", APIKeyID: "key-123"}
	h.handleModels(wGateway, reqGateway, session)

	if wGateway.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", wGateway.Code)
	}

	var respGateway struct {
		Data []struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(wGateway.Body.Bytes(), &respGateway); err != nil {
		t.Fatalf("parse gateway response: %v", err)
	}

	for _, m := range respGateway.Data {
		if m.OwnedBy != "" {
			t.Errorf("网关API Key场景下模型 %s 的 owned_by 期望为空，得到 %q", m.ID, m.OwnedBy)
		}
	}

	// 2. 原生单机场景（未携带网关 API Key）访问，保留原有 owned_by
	reqNative := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	wNative := httptest.NewRecorder()
	h.handleModels(wNative, reqNative)

	var respNative struct {
		Data []struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(wNative.Body.Bytes(), &respNative); err != nil {
		t.Fatalf("parse native response: %v", err)
	}

	byID := map[string]string{}
	for _, m := range respNative.Data {
		byID[m.ID] = m.OwnedBy
	}
	if byID["glm-5.3-flash"] != "nvidia" {
		t.Errorf("原生调用期望 glm-5.3-flash 保持 nvidia，得到 %q", byID["glm-5.3-flash"])
	}
	if byID["deepseek-v4.1-flash"] != "workbuddy" {
		t.Errorf("原生调用期望 deepseek-v4.1-flash 保持 workbuddy，得到 %q", byID["deepseek-v4.1-flash"])
	}
}

