package relay

// ocr_capability_test.go —— 锁定「多模态模型能力感知」OCR 降级闸的契约。
//
// 覆盖 ocr_capability.go 的三条判据优先级:
//  1. 配置优先:RelayModelMapping 的 Multimodal 显式声明(&true 跳过降级 / &false 强制降级);
//  2. 启发式兜底:配置 nil 或未命中时按模型名前缀白名单判定;
//  3. 默认安全:缺省(nil)保持旧行为(该降仍降,不致突然把图直送非多模态上游触发 400)。
//
// 并锁定三处号池入口(compat_dispatch Gemini / nvidia / passthrough_forwarder)在"目标模型
// 判定为多模态"时跳过 image→文本降级、图块原样透传的端到端契约。

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/settings"
)

// ===== 1. 启发式白名单(纯函数,不依赖映射表) =====

func TestHeuristicModelSupportsImage_GeminiFamily(t *testing.T) {
	// Gemini 全系(1.5/2.0/2.5/3.x pro/flash)均原生多模态。
	for _, m := range []string{
		"gemini-2.5-flash", "gemini-2.5-pro", "gemini-1.5-pro", "gemini-3-flash",
		"gemini-2.0-flash-thinking-exp", "GEMINI-3.5-FLASH", // 大小写不敏感
		"gemini-2.5-flash[1M]", // 剥离 [1M] 上下文窗口后缀后命中
	} {
		if !heuristicModelSupportsImage(m) {
			t.Errorf("heuristicModelSupportsImage(%q) want true, got false", m)
		}
	}
}

func TestHeuristicModelSupportsImage_VisionFamilies(t *testing.T) {
	// 业界高置信度多模态模型族前缀:OpenAI gpt-4o / 通义 qwen-vl / 智谱 glm-4v / Anthropic claude-vision 系等。
	for _, m := range []string{
		"gpt-4o", "gpt-4o-mini", "gpt-4.1", "gpt-4-turbo",
		"qwen-vl-plus", "qwen-vl-max", "qwen2-vl-7b", "qwen2.5-vl-72b",
		"glm-4v", "glm-4.5v",
		"internvl2-26b",
		"llama-3.2-vision-11b",
		"minicpm-v-2.6",
		"gemma-3-27b",
		"claude-3-5-sonnet", "claude-3.5-sonnet", "claude-3.7-sonnet",
		"claude-sonnet-4-5", "claude-opus-4-1",
		"claude-3-opus",
		"kimi-k2-0905",
		"step-1v", "step-1.5v-32b",
		"yi-vl-plus",
		"deepseek-vl2",
		"o4-mini",
	} {
		if !heuristicModelSupportsImage(m) {
			t.Errorf("heuristicModelSupportsImage(%q) want true (vision family), got false", m)
		}
	}
}

func TestHeuristicModelSupportsImage_NonVisionTextModels(t *testing.T) {
	// 纯文本族 / 推理族:严禁命中启发式白名单(避免把图直送给非多模态上游触发 400)。
	for _, m := range []string{
		"deepseek-chat", "deepseek-reasoner", "deepseek-v3",
		"gpt-3.5-turbo", "gpt-4", // 注意:gpt-4(无 turbo)非 vision
		"o1-mini", "o1-preview",
		"qwen-plus", "qwen-max", // qwen-max 非视觉系(不带 -vl)
		"glm-4-plus", "glm-4-air", // glm-4 系非视觉变体
		"llama-3.3-70b-instruct", // 无 -vision 后缀
		"claude-3-haiku", "claude-3-5-haiku", // haiku 系非 vision
		"unknown-random-model",
	} {
		if heuristicModelSupportsImage(m) {
			t.Errorf("heuristicModelSupportsImage(%q) want false (non-vision), got true", m)
		}
	}
}

func TestHeuristicModelSupportsImage_EmptyAndBracketSuffix(t *testing.T) {
	if heuristicModelSupportsImage("") {
		t.Error("empty model should be non-multimodal")
	}
	if heuristicModelSupportsImage("   ") {
		t.Error("whitespace model should be non-multimodal")
	}
	// [1M] 后缀剥离后才是真名:gpt-3.5[1M] 仍应判非多模态(后缀剥离不改变底层能力)。
	if heuristicModelSupportsImage("gpt-3.5[1M]") {
		t.Error("gpt-3.5 with [1M] suffix should still be non-multimodal after stripping suffix")
	}
}

// ===== 2. modelSupportsImage 配置优先(配置位 vs 启发式) =====

// boolPtr 包装一个 bool 指针,供构造 ModelMappingEntry.Multimodal。
func boolPtr(v bool) *bool { return &v }

// newCapabilityOCRService 构造一个注入 mappingResolver 的 OCRService(resolver 闭包把
// 模型名查给定 mappings,复用 settings.LookupModelMultimodalFlag 的三态语义)。
// 与 WireOcrRouteResolver 的生产闭包同构:getRelayModelMappingSafe → LookupModelMultimodalFlag。
func newCapabilityOCRService(t *testing.T, mappings []settings.ModelMappingEntry) *OCRService {
	t.Helper()
	svc := NewOCRService(nil, nil, func(string) {})
	svc.SetMappingResolver(func(model string) (*bool, bool) {
		return settings.LookupModelMultimodalFlag(mappings, model)
	})
	return svc
}

func TestModelSupportsImage_ConfigExplicitTrueOverridesHeuristic(t *testing.T) {
	// 名字命中启发式白名单(claude-3-5-sonnet)但配置显式 false → 强制降级(否决启发式误判)。
	// 反之亦然:名字不命中白名单(deepseek-test-vl-tone)但配置显式 true → 跳过降级。
	mappings := []settings.ModelMappingEntry{
		{ClientModel: "claude-3-5-sonnet", TargetModel: "x", Multimodal: boolPtr(false)}, // 强制非多模态
		{ClientModel: "custom-text-model", TargetModel: "y", Multimodal: boolPtr(true)},  // 强制多模态
	}
	svc := newCapabilityOCRService(t, mappings)

	if svc.modelSupportsImage("claude-3-5-sonnet") {
		t.Error("explicit Multimodal=false must override heuristic true → should NOT support image")
	}
	if !svc.modelSupportsImage("custom-text-model") {
		t.Error("explicit Multimodal=true must override heuristic false → should support image")
	}
}

func TestModelSupportsImage_ConfigNilFallsBackToHeuristic(t *testing.T) {
	// 映射项命中但未显式声明 Multimodal(nil)→ 走启发式兜底(默认项即此态)。
	// 取默认映射里裸名 gemini-2.5-flash(默认项未设 Multimodal)+ chat 类 deepseek-chat 对照。
	mappings := []settings.ModelMappingEntry{
		{ClientModel: "gemini-2.5-flash", TargetModel: "gemini-2.5-flash"}, // Multimodal nil
		{ClientModel: "deepseek-chat", TargetModel: "deepseek-chat"},       // Multimodal nil
	}
	svc := newCapabilityOCRService(t, mappings)

	if !svc.modelSupportsImage("gemini-2.5-flash") {
		t.Error("nil Multimodal + heuristic-hit gemini should support image")
	}
	if svc.modelSupportsImage("deepseek-chat") {
		t.Error("nil Multimodal + heuristic-miss deepseek-chat should NOT support image")
	}
}

func TestModelSupportsImage_NoResolverFallsBackToHeuristic(t *testing.T) {
	// 未注入 mappingResolver(relay 单测不调 WireOcrRouteResolver 的场景)→ 纯启发式,保持旧行为兼容。
	svc := NewOCRService(nil, nil, func(string) {}) // 不调 SetMappingResolver
	if !svc.modelSupportsImage("gemini-2.5-flash") {
		t.Error("no resolver + gemini should support image (heuristic fallback)")
	}
	if svc.modelSupportsImage("deepseek-chat") {
		t.Error("no resolver + deepseek-chat should NOT support image")
	}
}

func TestModelSupportsImage_MappingNotFoundFallsBackToHeuristic(t *testing.T) {
	// 模型名未在映射表中命中(found=false)→ 走启发式兜底(与默认映射表外的冷门模型一致)。
	mappings := []settings.ModelMappingEntry{
		{ClientModel: "deepseek-chat", TargetModel: "deepseek-chat"},
	}
	svc := newCapabilityOCRService(t, mappings)

	if !svc.modelSupportsImage("qwen-vl-plus") { // 未配映射,启发式命中 -vl
		t.Error("unmapped qwen-vl-plus should support image via heuristic")
	}
	if svc.modelSupportsImage("random-unknown-text-model") { // 未配映射,启发式 miss
		t.Error("unmapped random text model should NOT support image")
	}
}

func TestModelSupportsImage_CaseInsensitiveMapping(t *testing.T) {
	// 大小写不敏感匹配(与 MapClientModelToGemini 同款兜底)。
	mappings := []settings.ModelMappingEntry{
		{ClientModel: "Other/OpenAI/GPT-4o", TargetModel: "gpt-4o", Multimodal: boolPtr(true)},
	}
	svc := newCapabilityOCRService(t, mappings)
	if !svc.modelSupportsImage("other/openai/gpt-4o") {
		t.Error("case-insensitive match should hit mapping and respect Multimodal=true")
	}
}

// ===== 3. settings.LookupModelMultimodalFlag 三态契约 =====

func TestLookupModelMultimodalFlag_ThreeStates(t *testing.T) {
	mappings := []settings.ModelMappingEntry{
		{ClientModel: "a-multimodal", Multimodal: boolPtr(true)},
		{ClientModel: "a-nonmultimodal", Multimodal: boolPtr(false)},
		{ClientModel: "a-unset"}, // nil
	}

	// 显式 true。
	d, found := settings.LookupModelMultimodalFlag(mappings, "a-multimodal")
	if !found || d == nil || !*d {
		t.Errorf("explicit true: want found=true, &true; got found=%v d=%v", found, d)
	}
	// 显式 false。
	d, found = settings.LookupModelMultimodalFlag(mappings, "a-nonmultimodal")
	if !found || d == nil || *d {
		t.Errorf("explicit false: want found=true, &false; got found=%v d=%v", found, d)
	}
	// nil 未声明(命中但未配置)。
	d, found = settings.LookupModelMultimodalFlag(mappings, "a-unset")
	if !found || d != nil {
		t.Errorf("unset: want found=true, d=nil; got found=%v d=%v", found, d)
	}
	// 未命中。
	d, found = settings.LookupModelMultimodalFlag(mappings, "not-in-table")
	if found || d != nil {
		t.Errorf("miss: want found=false, d=nil; got found=%v d=%v", found, d)
	}
	// 空表 / 空名。
	if _, found := settings.LookupModelMultimodalFlag(nil, "x"); found {
		t.Error("nil mappings should be found=false")
	}
	if _, found := settings.LookupModelMultimodalFlag(mappings, "  "); found {
		t.Error("empty/whitespace name should be found=false")
	}
}

// ===== 4. 端到端:NVIDIA 池入口,上游为多模态模型时跳过降级、图块原样透传 =====

// nvidiaChatUpstreamAssertingImageURL 构造一个 mock NVIDIA(OpenAI Chat 兼容)上游,
// 断言其收到的 messages 里**必须**出现 image_url 块(证明多模态上游图块原样透传、未被降级)。
// 返回的服务与捕获体由调用方 defer / 校验。
func nvidiaChatUpstreamAssertingImageURL(t *testing.T, captured *map[string]interface{}) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		*captured = body
		// 回一个收敛的 Chat 响应,让 handleNvidia 正常回译 Anthropic。
		resp := map[string]interface{}{
			"id": "chatcmpl-vis", "object": "chat.completion", "model": "qwen-vl-plus",
			"choices": []map[string]interface{}{{
				"index": 0,
				"message": map[string]interface{}{"role": "assistant", "content": "已看到截图"},
				"finish_reason": "stop",
			}},
			"usage": map[string]interface{}{"prompt_tokens": 5, "completion_tokens": 2, "total_tokens": 7},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	return srv
}

// anyImageURLContent 检查 OpenAI Chat messages 里是否出现数组形态 content 且含 image_url 块。
func anyImageURLContent(msgs []interface{}) bool {
	for _, m := range msgs {
		mm, ok := m.(map[string]interface{})
		if !ok {
			continue
		}
		parts, ok := mm["content"].([]interface{})
		if !ok {
			continue
		}
		for _, p := range parts {
			pp, ok := p.(map[string]interface{})
			if !ok {
				continue
			}
			if pp["type"] == "image_url" {
				return true
			}
		}
	}
	return false
}

// TestHandleNvidia_MultimodalUpstream_SkipsDowngrade_ImagePreserved:
// NIM 上游模型名(qwen-vl-plus)命中启发式白名单 → modelSupportsImage=true → 跳过 image 降级,
// 入站 Anthropic 的 image 块经 AnthropicToOpenAIChat 原样转译为 image_url 数组形态直送上游。
func TestHandleNvidia_MultimodalUpstream_SkipsDowngrade_ImagePreserved(t *testing.T) {
	// OCR mock:若误降级会触达此服务;本测试断言它**不被调用**(图未降级)。
	ocr := ocrFlashServer(t, "should-not-be-called-on-vision-upstream", http.StatusOK)
	defer ocr.Close()
	origAddr := localProxyAddr
	localProxyAddr = strings.TrimPrefix(ocr.URL, "http://")
	t.Cleanup(func() { localProxyAddr = origAddr })

	var captured map[string]interface{}
	upstream := nvidiaChatUpstreamAssertingImageURL(t, &captured)
	defer upstream.Close()

	// 账号上游模型配为 qwen-vl-plus(命中 -vl 启发式),降级闸应跳过。
	acc := mkNvidiaAccount("nv-vis", "nv-vis@x.cloud", "k", upstream.URL, "qwen-vl-plus")
	handler, _, _, _ := newNvidiaTestHandler(t, []*account.Account{acc})
	// 纯启发式判定(qwen-vl-plus 命中 -vl 白名单)即可跳过降级,不调 WireOcrRouteResolver
	// (调用会注入 routeResolver 把 OCR 出站改路由到其它号池,破坏本地 Gemini mock)。

	anthReq := &AnthropicRequest{
		Model:    "claude-sonnet-4-5",
		MaxTokens: func() *int { v := 200; return &v }(),
		Messages: []AnthropicMessage{{
			Role: "user",
			Content: []AnthropicContent{
				{Type: "image", Source: &AnthropicImageSource{Type: "base64", MediaType: "image/png", Data: fakeNvidiaImageB64}},
				{Type: "text", Text: "看看这张图"},
			},
		}},
	}
	body, _ := json.Marshal(anthReq)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u-vis", UserKey: "k1"})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	msgs, _ := captured["messages"].([]interface{})
	if !anyImageURLContent(msgs) {
		t.Errorf("multimodal upstream MUST receive image_url (preserved): %s", mustJSON(captured))
	}
	// 文本块亦保留(原样透传不丢字段)。
	concat := concatUserText(msgs)
	if !strings.Contains(concat, "看看这张图") {
		t.Errorf("original user text lost: %s", concat)
	}
}

// TestHandleNvidia_NonMultimodalUpstream_DowngradesAsBefore:
// NIM 上游模型名(glm-5.2)命中启发式 miss → modelSupportsImage=false → 照旧降级,
// 上游只见 text、不含 image_url(回归基线,锁定旧行为不破)。
func TestHandleNvidia_NonMultimodalUpstream_DowngradesAsBefore(t *testing.T) {
	ocr := ocrFlashServer(t, "OCR-TEXT-FOR-NON-VISION", http.StatusOK)
	defer ocr.Close()
	origAddr := localProxyAddr
	localProxyAddr = strings.TrimPrefix(ocr.URL, "http://")
	t.Cleanup(func() { localProxyAddr = origAddr })

	var captured map[string]interface{}
	upstream := nvidiaChatUpstreamAssertingImageURL(t, &captured)
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-text", "nv-text@x.cloud", "k", upstream.URL, "z-ai/glm-5.2")
	handler, _, _, _ := newNvidiaTestHandler(t, []*account.Account{acc})
	// 不调 WireOcrRouteResolver:glm-5.2 纯启发式 miss → 走降级,且 OCR 出站保持本地 Gemini mock。

	anthReq := &AnthropicRequest{
		Model:    "claude-sonnet-4-5",
		MaxTokens: func() *int { v := 200; return &v }(),
		Messages: []AnthropicMessage{{
			Role: "user",
			Content: []AnthropicContent{
				{Type: "image", Source: &AnthropicImageSource{Type: "base64", MediaType: "image/png", Data: fakeNvidiaImageB64}},
				{Type: "text", Text: "看图"},
			},
		}},
	}
	body, _ := json.Marshal(anthReq)
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u-text", UserKey: "k1"})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	msgs, _ := captured["messages"].([]interface{})
	if anyImageURLContent(msgs) {
		t.Errorf("non-multimodal upstream MUST NOT receive image_url (downgraded): %s", mustJSON(captured))
	}
	concat := concatUserText(msgs)
	if !strings.Contains(concat, "OCR-TEXT-FOR-NON-VISION") {
		t.Errorf("OCR text missing in upstream user content: %s", concat)
	}
}

// ===== 5. 端到端:OpenAI Chat 入站 NIM 上游,多模态跳过降级 =====

// TestHandleNvidia_OpenAIChatInbound_MultimodalUpstream_PreservesImageURL:
// 入站 OpenAI Chat(Vision 数组形态 content)+ NIM 上游为多模态 → 跳过降级,image_url 数组原样透传。
func TestHandleNvidia_OpenAIChatInbound_MultimodalUpstream_PreservesImageURL(t *testing.T) {
	ocr := ocrFlashServer(t, "should-not-be-called", http.StatusOK)
	defer ocr.Close()
	origAddr := localProxyAddr
	localProxyAddr = strings.TrimPrefix(ocr.URL, "http://")
	t.Cleanup(func() { localProxyAddr = origAddr })

	var captured map[string]interface{}
	upstream := nvidiaChatUpstreamAssertingImageURL(t, &captured)
	defer upstream.Close()

	acc := mkNvidiaAccount("nv-vis2", "nv-vis2@x.cloud", "k", upstream.URL, "gpt-4o")
	handler, _, _, _ := newNvidiaTestHandler(t, []*account.Account{acc})
	handler.WireOcrRouteResolver()

	// 入站 OpenAI Chat:数组形态 content 含 text + image_url(data URL)。
	inBody := buildOpenAIChatBody("gpt-4o", []map[string]interface{}{
		ocrTextBlock("看这个报错截图"),
		ocrBase64Block(fakeNvidiaImageB64),
	})
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/chat/completions", bytes.NewReader(inBody))
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u-vis2", UserKey: "k1"})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	msgs, _ := captured["messages"].([]interface{})
	if !anyImageURLContent(msgs) {
		t.Errorf("multimodal upstream MUST receive image_url (OpenAI Chat inbound preserved): %s", mustJSON(captured))
	}
}

// ===== 6. 端到端:配置显式 false 否决启发式(冷门模型强制降级) =====

// TestModelSupportsImage_ConfigFalseForcesDowngrade_E2E:
// 即便模型名命中启发式白名单,配置显式 Multimodal=false 仍强制降级(否决冷门误判)。
// 用 DowngradeGeminiImagesToText 做闭环判据:配置 false → 返回 0 不降级判定不成立 → 实际进入降级分支。
func TestModelSupportsImage_ConfigFalseForcesDowngrade_E2E(t *testing.T) {
	ocr := ocrFlashServer(t, "OCR-FORCED-DESPITE-VL-NAME", http.StatusOK)
	defer ocr.Close()

	// 配置显式声明 qwen-vl-plus 为非多模态(冷门否决):期望即使名字含 -vl 也走降级。
	mappings := []settings.ModelMappingEntry{
		{ClientModel: "qwen-vl-plus", TargetModel: "qwen-vl-plus", Multimodal: boolPtr(false)},
	}
	svc := newCapabilityOCRService(t, mappings)
	// 接通 OCR 上游(本地 Gemini mock),让 DowngradeGeminiImagesToText 真打能命中 OCR 文本。
	svc.client = &http.Client{Timeout: 5 * time.Second}
	origAddr := localProxyAddr
	localProxyAddr = strings.TrimPrefix(ocr.URL, "http://")
	t.Cleanup(func() { localProxyAddr = origAddr })

	if svc.modelSupportsImage("qwen-vl-plus") {
		t.Fatal("config Multimodal=false must force downgrade even for -vl name")
	}
	// 闭环:DowngradeGeminiImagesToText 应进入降级分支(返回 downgradedCount>=1),证明配置 false 生效。
	gemReq := &GeminiRequest{
		Contents: []GeminiContent{{
			Role: "user",
			Parts: []GeminiPart{
				{Text: "看图"},
				{InlineData: &GeminiBlob{MimeType: "image/png", Data: fakeNvidiaImageB64}},
			},
		}},
	}
	downgraded, _, _, _ := svc.DowngradeGeminiImagesToText(gemReq, &RelaySession{UserID: "u", UserKey: "k1"}, "qwen-vl-plus")
	if downgraded < 1 {
		t.Fatalf("config Multimodal=false should drive downgrade (downgraded=%d)", downgraded)
	}
	// InlineData 被清,Text 拼接了 OCR 描述。
	if gemReq.Contents[0].Parts[1].InlineData != nil {
		t.Errorf("InlineData should be cleared after downgrade")
	}
	if !strings.Contains(gemReq.Contents[0].Parts[1].Text, "OCR-FORCED-DESPITE-VL-NAME") {
		t.Errorf("downgraded text missing OCR result: %s", gemReq.Contents[0].Parts[1].Text)
	}
}

// ===== helpers =====

func mustJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// concatUserText 拼接 OpenAI Chat messages 里所有 role=user 的可见文本。
// 兼容 string 形态(content 为字符串)与数组形态(content 为 text/image_url 块,多模态保图场景)。
func concatUserText(msgs []interface{}) string {
	var sb strings.Builder
	for _, m := range msgs {
		mm, ok := m.(map[string]interface{})
		if !ok {
			continue
		}
		if mm["role"] != "user" {
			continue
		}
		switch c := mm["content"].(type) {
		case string:
			sb.WriteString(c)
		case []interface{}:
			for _, p := range c {
				pp, ok := p.(map[string]interface{})
				if !ok {
					continue
				}
				if pp["type"] == "text" {
					if s, ok := pp["text"].(string); ok {
						sb.WriteString(s)
					}
				}
			}
		}
	}
	return sb.String()
}

// 占位 import:某些用例的 bytes/typing 需要时才引入,保留以确保编译。
var _ = io.EOF
