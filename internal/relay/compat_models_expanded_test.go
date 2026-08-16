package relay

import (
	"encoding/json"
	"testing"

	"antigravity-proxy/internal/settings"
)

// compat_models_expanded_test.go: 验证 ModelMappingEntry.VariantEfforts 字段驱动的
// /v1/models 虚项展开与 router 层变体后缀感知 + effort 兜底注入行为。
//
// 覆盖三组行为:
// 1) buildExposedModelMap 按 VariantEfforts 对每个 mapping 展开 {ClientModel}-{effort} 虚项;
// 2) resolveVariantEffort 剥离变体后缀并校验 baseModel/effort 均在 mapping 表内;
// 3) maybeInjectEffortFallback 在客户端未显式带思考信号时把 effort 写回请求体,
//    覆盖 anthropic/openai-chat/openai-responses 三种入站协议形态。

// --- 1) /v1/models 虚项展开 ---

func TestBuildExposedModelMap_VariantEffortsExpansion(t *testing.T) {
	maxTokens := int64(200000)
	h := &APICompatHandler{
		settingsMgr: &stubExposedModelsSettings{
			mappings: []settings.ModelMappingEntry{
				{
					ClientModel:    "glm-5.2",
					TargetModel:    "z-ai/glm-5.2",
					Expose:         true,
					OwnedBy:        "nvidia",
					MaxInputTokens: &maxTokens,
					VariantEfforts: []string{"high", "max"},
				},
				{
					ClientModel:    "grok/grok-4.6",
					TargetModel:    "grok/grok-4.6",
					Expose:         true,
					OwnedBy:        "grok",
					VariantEfforts: []string{"high", "max", "low"},
				},
				{
					// 无 VariantEfforts → 仅暴露裸 ClientModel 一项(原有行为)。
					ClientModel: "gemini-2.5-pro",
					TargetModel: "gemini-2.5-pro",
					Expose:      true,
					OwnedBy:     "google",
				},
				{
					// 不暴露 → 裸名 + 变体虚项都应被过滤。
					ClientModel:    "hidden-model",
					TargetModel:    "hidden-model",
					Expose:         false,
					VariantEfforts: []string{"high"},
				},
			},
		},
	}

	ids := collectExposedIDs(h.buildExposedModelMap(true))

	expectContain(t, ids, "glm-5.2")
	expectContain(t, ids, "glm-5.2-high")
	expectContain(t, ids, "glm-5.2-max")
	expectContain(t, ids, "grok/grok-4.6")
	expectContain(t, ids, "grok/grok-4.6-high")
	expectContain(t, ids, "grok/grok-4.6-max")
	expectContain(t, ids, "grok/grok-4.6-low")
	expectContain(t, ids, "gemini-2.5-pro")
	if containsID(ids, "hidden-model") || containsID(ids, "hidden-model-high") {
		t.Errorf("Expose=false 条目连同其变体虚项都应被过滤, got ids=%v", ids)
	}

	// 同一 mapping 的虚项与主条目共享 OwnedBy/MaxInputTokens,这里抽查 glm-5.2-max 的 max_input_tokens 是否对齐。
	for _, m := range h.buildExposedModelMap(true) {
		if m.ID == "glm-5.2" || m.ID == "glm-5.2-high" || m.ID == "glm-5.2-max" {
			if m.OwnedBy != "nvidia" {
				t.Errorf("variant %s OwnerBy expected=nvidia, got=%q", m.ID, m.OwnedBy)
			}
			if m.MaxInputTokens != maxTokens {
				t.Errorf("variant %s MaxInputTokens expected=%d, got=%d", m.ID, maxTokens, m.MaxInputTokens)
			}
		}
	}
}

// --- 2) resolveVariantEffort 剥离 ---

func TestResolveVariantEffort(t *testing.T) {
	h := &APICompatHandler{
		settingsMgr: &stubMappingSettings{
			mappings: []settings.ModelMappingEntry{
				{ClientModel: "glm-5.2", VariantEfforts: []string{"high", "max"}},
				{ClientModel: "grok/grok-4.6", VariantEfforts: []string{"high", "max", "low"}},
				// 无 VariantEfforts 的 mapping:客户端 model 名带其后缀(StringSuffix)也不应命中。
				{ClientModel: "plain-model"},
			},
		},
	}

	cases := []struct {
		name          string
		inModel       string
		wantBase      string
		wantEffort    string
		wantMatched   bool
	}{
		{"命中 high 后缀", "glm-5.2-high", "glm-5.2", "high", true},
		{"命中 max 后缀", "glm-5.2-max", "glm-5.2", "max", true},
		{"命中含斜杠模型名 + low 后缀", "grok/grok-4.6-low", "grok/grok-4.6", "low", true},
		// 未带变体后缀(裸 ClientModel)→ 不命中, 让上游调用方按原 inModel 路由。
		{"裸 ClientModel 不触发剥离", "glm-5.2", "", "", false},
		// effort 已知 但 baseModel 不在 mapping 表。
		{"baseModel 不存在不剥离", "unknown-model-max", "", "", false},
		// baseModel 存在 但 effort 不在 VariantEfforts 集合(即使看上去像 -后的串符)。
		{"unknown effort 不剥离", "glm-5.2-xhigh", "", "", false},
		// plain-model 未配置 VariantEfforts,即便 model 名末段字面像 effort 也不应剥离。
		{"mapping 未配 VariantEfforts 不剥离", "plain-model-high", "", "", false},
		// 模型名本身含多个 "-"(如 other/sensenova/glm-5.2): 仅最末段视为 candidateEffort,
		// 命中 high 为已知 effort 且 baseModel=other/sensenova/glm-5.2 不在 knownClients(本测试场景)
		// → 不剥离。若 baseModel 在 knownClients 且 effort 命中 → 应命中。
		{"长前缀 + 已知 effort 且 base 在表", "grok/grok-4.6-max", "grok/grok-4.6", "max", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			base, effort, matched := h.resolveVariantEffort(c.inModel)
			if matched != c.wantMatched || base != c.wantBase || effort != c.wantEffort {
				t.Fatalf("resolveVariantEffort(%q) = (base=%q effort=%q matched=%v), want (base=%q effort=%q matched=%v)",
					c.inModel, base, effort, matched, c.wantBase, c.wantEffort, c.wantMatched)
			}
		})
	}
}

// --- 3) maybeInjectEffortFallback ---

func TestMaybeInjectEffortFallback_Anthropic(t *testing.T) {
	// 客户端未带任何思考信号 → 兜底注入 thinking.type=adaptive + output_config.effort=high。
	body := []byte(`{"model":"glm-5.2-high","messages":[{"role":"user","content":"hi"}],"max_tokens":1000}`)
	out := maybeInjectEffortFallback(body, "high")
	var parsed map[string]interface{}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if th, ok := parsed["thinking"].(map[string]interface{}); !ok || th["type"] != "adaptive" {
		t.Errorf("thinking.type expected=adaptive, got=%v", parsed["thinking"])
	}
	if oc, ok := parsed["output_config"].(map[string]interface{}); !ok || oc["effort"] != "high" {
		t.Errorf("output_config.effort expected=high, got=%v", parsed["output_config"])
	}
}

func TestMaybeInjectEffortFallback_Anthropic_ClientAlreadyHasEffort(t *testing.T) {
	// 客户端已带 output_config.effort=max → 尊重不动,不覆盖为剥离出的 high。
	body := []byte(`{"model":"glm-5.2-high","output_config":{"effort":"max"},"thinking":{"type":"adaptive"},"max_tokens":1000}`)
	out := maybeInjectEffortFallback(body, "high")
	var parsed map[string]interface{}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if oc, ok := parsed["output_config"].(map[string]interface{}); !ok || oc["effort"] != "max" {
		t.Errorf("客户端已显式 max, 应尊重不动, got=%v", parsed["output_config"])
	}
}

func TestMaybeInjectEffortFallback_OpenAIChat(t *testing.T) {
	body := []byte(`{"model":"glm-5.2-high","messages":[{"role":"user","content":"hi"}]}`)
	out := maybeInjectEffortFallback(body, "high")
	var parsed map[string]interface{}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if parsed["reasoning_effort"] != "high" {
		t.Errorf("reasoning_effort expected=high, got=%v", parsed["reasoning_effort"])
	}
}

func TestMaybeInjectEffortFallback_OpenAIResponses(t *testing.T) {
	// Responses API 入站: 顶层 reasoning.effort 未带 → 兜底写 high。
	body := []byte(`{"model":"glm-5.2-high","input":[{"role":"user","content":"hi"}]}`)
	out := maybeInjectEffortFallback(body, "high")
	var parsed map[string]interface{}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if reasoning, ok := parsed["reasoning"].(map[string]interface{}); !ok || reasoning["effort"] != "high" {
		t.Errorf("reasoning.effort expected=high, got=%v", parsed["reasoning"])
	}
}

func TestMaybeInjectEffortFallback_EmptyEffort_NoOp(t *testing.T) {
	body := []byte(`{"model":"glm-5.2"}`)
	out := maybeInjectEffortFallback(body, "")
	if string(out) != string(body) {
		t.Errorf("strippedEffort 空时应原样返回 body, got diff (orig=%q new=%q)", body, out)
	}
}

// expectContain 断言 ids 列表包含 want。
func expectContain(t *testing.T, ids []string, want string) {
	t.Helper()
	if !containsID(ids, want) {
		t.Errorf("expected ids to contain %q, got ids=%v", want, ids)
	}
}
