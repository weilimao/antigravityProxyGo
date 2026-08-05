package relay

import (
	"antigravity-proxy/internal/settings"
	"strings"
	"testing"
)

// stubExposedModelsSettings 给 buildExposedModelMap 喂一个最小 settings.ManagerInterface 实现,
// 只返回测试自定义的 RelayModelMapping,其余走 nil/默认。
type stubExposedModelsSettings struct {
	settings.ManagerInterface
	mappings []settings.ModelMappingEntry
}

func (s *stubExposedModelsSettings) GetRelayModelMapping() []settings.ModelMappingEntry {
	out := make([]settings.ModelMappingEntry, len(s.mappings))
	copy(out, s.mappings)
	return out
}

// collectExposedIDs 提取 buildExposedModelMap 产出的 ClientModel ID 列表,便于断言。
func collectExposedIDs(models []exposedModel) []string {
	ids := make([]string, 0, len(models))
	for _, m := range models {
		ids = append(ids, m.ID)
	}
	return ids
}

func containsID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func TestBuildExposedModelMap_PrefixFiltering(t *testing.T) {
	h := &APICompatHandler{
		settingsMgr: &stubExposedModelsSettings{
			mappings: []settings.ModelMappingEntry{
				// 非 Google 族带 provider/ 前缀(/route 专属条目)。
				{ClientModel: "nvidia/deepseek-ai/deepseek-v4-pro", TargetModel: "deepseek-ai/deepseek-v4-pro", Expose: true, OwnedBy: "nvidia"},
				{ClientModel: "deepseek/deepseek-chat", TargetModel: "deepseek-chat", Expose: true, OwnedBy: "deepseek"},
				// Google 族裸名条目(应始终收录,供 /v1/* 裸名直连)。
				{ClientModel: "gemini-2.5-pro", TargetModel: "gemini-2.5-pro", Expose: true, OwnedBy: "google"},
				// Google 族双条目:裸名 + 带 google/ 前缀(供 /route 精准路由)。
				{ClientModel: "google/gemini-1.5-flash", TargetModel: "gemini-1.5-flash", Expose: true, OwnedBy: "google"},
				// 带 nvidia/ 前缀但 Expose=false(应被 Expose 过滤,与前缀过滤正交)。
				{ClientModel: "nvidia/hidden-model", TargetModel: "hidden-model", Expose: false, OwnedBy: "nvidia"},
			},
		},
	}

	// /v1/* 入口语义(includePrefixed=false):过滤掉非 Google 族带前缀条目。
	// Google 族裸名条目照常列出;Google 族带 google/ 前缀条目也列出(同属 Google 族,不走 /route 过滤)。
	ids := collectExposedIDs(h.buildExposedModelMap(false))
	if containsID(ids, "nvidia/deepseek-ai/deepseek-v4-pro") {
		t.Errorf("includePrefixed=false 应过滤 nvidia/ 前缀条目, got ids=%v", ids)
	}
	if containsID(ids, "deepseek/deepseek-chat") {
		t.Errorf("includePrefixed=false 应过滤 deepseek/ 前缀条目, got ids=%v", ids)
	}
	if !containsID(ids, "gemini-2.5-pro") {
		t.Errorf("includePrefixed=false 应收录 Google 族裸名条目 gemini-2.5-pro, got ids=%v", ids)
	}
	if !containsID(ids, "google/gemini-1.5-flash") {
		t.Errorf("includePrefixed=false 应收录 Google 族带 google/ 前缀条目, got ids=%v", ids)
	}
	if containsID(ids, "nvidia/hidden-model") {
		t.Errorf("Expose=false 条目应被过滤, got ids=%v", ids)
	}

	// /route 入口语义(includePrefixed=true):全部 Expose 条目(含带前缀名)收录。
	ids = collectExposedIDs(h.buildExposedModelMap(true))
	if !containsID(ids, "nvidia/deepseek-ai/deepseek-v4-pro") {
		t.Errorf("includePrefixed=true 应收录 nvidia/ 前缀条目, got ids=%v", ids)
	}
	if !containsID(ids, "deepseek/deepseek-chat") {
		t.Errorf("includePrefixed=true 应收录 deepseek/ 前缀条目, got ids=%v", ids)
	}
	if !containsID(ids, "gemini-2.5-pro") {
		t.Errorf("includePrefixed=true 应收录 Google 族裸名条目, got ids=%v", ids)
	}
	if !containsID(ids, "google/gemini-1.5-flash") {
		t.Errorf("includePrefixed=true 应收录 Google 族带 google/ 前缀条目, got ids=%v", ids)
	}
	if containsID(ids, "nvidia/hidden-model") {
		t.Errorf("Expose=false 条目应被过滤, got ids=%v", ids)
	}
}

func TestIsRoutedPrefixedModel(t *testing.T) {
	cases := []struct {
		clientModel string
		want        bool
	}{
		// 非 Google 族带 provider/ 前缀 → 命中。
		{"nvidia/deepseek-ai/deepseek-v4-pro", true},
		{"deepseek/deepseek-chat", true},
		{"qwen/qwen-max", true},
		{"moonshotai/kimi-k2.5", true},
		// Google 族前缀 → 不命中(Google 族走裸名直连,不被 /route 过滤)。
		{"google/gemini-1.5-flash", false},
		{"gcp/gemini-2.5-pro", false},
		{"antigravity/claude-sonnet-4-5", false},
		// 无斜杠前缀的裸名 → 不命中。
		{"gemini-2.5-pro", false},
		{"deepseek-chat", false},
		{"nvidia-llama", false},
		// 空串/仅前缀/空 provider → 不命中。
		{"", false},
		{"/something", false},
		{" /x", false},
	}
	for _, c := range cases {
		got := isRoutedPrefixedModel(c.clientModel)
		if got != c.want {
			t.Errorf("isRoutedPrefixedModel(%q) = %v, want %v", c.clientModel, got, c.want)
		}
	}
}

// 编译期保证 buildExposedModelMap 的签名收口为 (bool),防止后续误回退到无参版本。
var _ = func(h *APICompatHandler) {
	_ = h.buildExposedModelMap(false)
	// 防止未使用的 import 警告(若测试仅用到了 strings.TrimSpace 之外的内容)。
	_ = strings.TrimSpace
}
