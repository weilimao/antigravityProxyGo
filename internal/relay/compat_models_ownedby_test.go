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
				{ClientModel: "claude-3-5-sonnet", TargetModel: "claude-3-5-sonnet", Expose: true}, // 空 OwnedBy → 兜底 anthropic
				{ClientModel: "internal-x", TargetModel: "x", Expose: false},                       // 不暴露,不应出现
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
