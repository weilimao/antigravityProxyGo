package relay

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/settings"
)

// anthropic_count_tokens_test.go 锁定 NVIDIA 中继对 Anthropic 可选端点
// POST /nvidia/v1/messages/count_tokens 的纯本地估算行为:
//   - 端点路由:命中该路径 → 200 + {"input_tokens": N},不打上游、不消耗号池;
//   - 估算算法:中英混合按字符类别加权(CJK ≈1.5 字符/token,其余 ≈4);
//   - 边界:空请求保底 1、超大输入受硬顶钳制、非法 JSON 回 400;
//   - 形态兼容:system/content 的字符串与数组两种格式、tools、thinking、tool_result、tool_use、image 均纳入估算。

// ===== 算法纯函数单测(不依赖 AnthropicRequest 构造,直接断言加权逻辑) =====

func TestWeightedTokenEstimate_PureEnglish(t *testing.T) {
	// 40 个英文字符 → 40/4 = 10 token
	if got := weightedTokenEstimate(strings.Repeat("a", 40)); got != 10 {
		t.Errorf("pure english 40 chars = %d, want 10", got)
	}
}

func TestWeightedTokenEstimate_PureChinese(t *testing.T) {
	// 15 个中文字符 → ceil(15/1.5) = 10 token
	if got := weightedTokenEstimate(strings.Repeat("中", 15)); got != 10 {
		t.Errorf("pure chinese 15 chars = %d, want 10", got)
	}
}

func TestWeightedTokenEstimate_Mixed(t *testing.T) {
	// 6 中文 + 8 英文 → 6/1.5 + 8/4 = 4 + 2 = 6
	if got := weightedTokenEstimate("中文测试六个" + strings.Repeat("a", 8)); got != 6 {
		t.Errorf("mixed = %d, want 6", got)
	}
}

func TestWeightedTokenEstimate_Empty(t *testing.T) {
	if got := weightedTokenEstimate(""); got != 0 {
		t.Errorf("empty = %d, want 0", got)
	}
}

func TestWeightedTokenEstimate_CeilRounding(t *testing.T) {
	// 1 中文 → 1/1.5 = 0.667 → ceil = 1
	if got := weightedTokenEstimate("中"); got != 1 {
		t.Errorf("single cjk = %d, want 1 (ceil)", got)
	}
	// 5 中文字符数不足以整除:5/1.5 = 3.333 → ceil = 4
	if got := weightedTokenEstimate(strings.Repeat("中", 5)); got != 4 {
		t.Errorf("5 cjk = %d, want 4 (ceil 5/1.5=3.33)", got)
	}
}

func TestIsCJKRune_Coverage(t *testing.T) {
	cases := map[rune]bool{
		'中': true,  // CJK 统一表意
		'あ': true,  // 平假名
		'ア': true,  // 片假名
		'가': true,  // 谚文音节
		'。': true,  // CJK 标点(全角句号 U+3002)
		' ': false, // 半角空格
		'a': false, // 半角英文
		'1': false, // 半角数字
		'Ａ': true,  // 全角字母
	}
	for r, want := range cases {
		if got := isCJKRune(r); got != want {
			t.Errorf("isCJKRune(%q U+%X) = %v, want %v", r, r, got, want)
		}
	}
}

// ===== AnthropicRequest 端估算单测(覆盖各请求形态) =====

func TestEstimateInputTokens_EmptyRequest(t *testing.T) {
	// 空 messages([]) + 无 system/tools → 保底 1
	req := &AnthropicRequest{Model: "claude-sonnet-4-5", Messages: []AnthropicMessage{}}
	if got := estimateInputTokens(req); got != 1 {
		t.Errorf("empty request = %d, want 1 (floor)", got)
	}
}

func TestEstimateInputTokens_SystemString(t *testing.T) {
	// system 为字符串 "You are helpful."(20 字符,全英文)
	// 估算至少 20/4 = 5 token,且因 messages 空保底 1,实际取 system 估算值
	req := &AnthropicRequest{
		System:   "You are very helpful.",
		Messages: []AnthropicMessage{{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "hi"}}}},
	}
	got := estimateInputTokens(req)
	if got < 5 {
		t.Errorf("system string estimate = %d, want >= 5", got)
	}
}

func TestEstimateInputTokens_SystemArrayFormat(t *testing.T) {
	// system 为数组格式 [{"type":"text","text":"You are helpful."}] —— AnthropicRequest.UnmarshalJSON 归一
	body := []byte(`{"model":"claude-sonnet-4-5","system":[{"type":"text","text":"You are very helpful."}],"messages":[{"role":"user","content":"hi"}]}`)
	var req AnthropicRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if req.System != "You are very helpful." {
		t.Fatalf("system not normalized: %q", req.System)
	}
	if got := estimateInputTokens(&req); got < 5 {
		t.Errorf("system array estimate = %d, want >= 5", got)
	}
}

func TestEstimateInputTokens_ContentStringFormat(t *testing.T) {
	// message.content 为纯字符串 "你是什么模型"(6 中文字符) —— AnthropicMessage.UnmarshalJSON 归一
	body := []byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"你是什么模型"}]}`)
	var req AnthropicRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if len(req.Messages) != 1 || len(req.Messages[0].Content) != 1 || req.Messages[0].Content[0].Text != "你是什么模型" {
		t.Fatalf("content string not normalized: %+v", req.Messages)
	}
	// 6 中文 + 1 空格 → ceil(6/1.5) + ceil(1/4) = 4 + 1 = 5 token,>1 不触发保底
	if got := estimateInputTokens(&req); got != 5 {
		t.Errorf("content string estimate = %d, want 5", got)
	}
}

func TestEstimateInputTokens_WithTools(t *testing.T) {
	// tools 的 name + description + input_schema 应计入
	req := &AnthropicRequest{
		Model:    "claude-sonnet-4-5",
		Messages: []AnthropicMessage{{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "use the tool"}}}},
		Tools: []AnthropicTool{{
			Name:        "get_weather",
			Description: "Get current weather for a city",
			InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"city": map[string]interface{}{"type": "string"}}},
		}},
	}
	got := estimateInputTokens(req)
	// 工具声明体量明显 > 单条 "use the tool"(12 字符),估算应比纯消息高
	baseline := estimateInputTokens(&AnthropicRequest{
		Model: "claude-sonnet-4-5", Messages: req.Messages,
	})
	if got <= baseline {
		t.Errorf("with tools = %d, should exceed baseline %d (tool schema not counted)", got, baseline)
	}
}

func TestEstimateInputTokens_WithThinkingAndToolResult(t *testing.T) {
	// 官方 count_tokens 口径:前序 assistant 轮的 thinking 块不计 input_tokens,
	// 仅当前轮(末条且 role==assistant 的 prefill)的 thinking 才计。
	// 本用例 assistant 的 thinking 处于前序(末条是 user.tool_result),故应被剔除,
	// 估算仅剩 tool_result 的 "result data here"(18 字符 ≈ 5 token)。
	const prevThinking = "Let me reason about this carefully"
	req := &AnthropicRequest{
		Model: "claude-sonnet-4-5",
		Messages: []AnthropicMessage{
			{Role: "assistant", Content: []AnthropicContent{
				{Type: "thinking", Thinking: prevThinking},
			}},
			{Role: "user", Content: []AnthropicContent{
				{Type: "tool_result", ToolUseID: "toolu_1", ToolResultContent: json.RawMessage(`"result data here"`)},
			}},
		},
	}
	got := estimateInputTokens(req)
	// 对照:完全不含该 thinking 块的请求(前序 thinking 被剔除后应与之等价)。
	baseline := estimateInputTokens(&AnthropicRequest{
		Model: "claude-sonnet-4-5",
		Messages: []AnthropicMessage{
			{Role: "user", Content: []AnthropicContent{
				{Type: "tool_result", ToolUseID: "toolu_1", ToolResultContent: json.RawMessage(`"result data here"`)},
			}},
		},
	})
	if got != baseline {
		t.Errorf("previous-turn thinking should be excluded: got %d, want baseline %d (thinking+tool_result)", got, baseline)
	}
	if got < 5 {
		t.Errorf("tool_result-only estimate = %d, want >= 5", got)
	}
	// 对照:把同一 thinking 挪到末条 assistant(prefill)应被计入,估算值严格高于剔除口径。
	prefill := estimateInputTokens(&AnthropicRequest{
		Model: "claude-sonnet-4-5",
		Messages: []AnthropicMessage{
			{Role: "user", Content: []AnthropicContent{{Type: "tool_result", ToolUseID: "toolu_1", ToolResultContent: json.RawMessage(`"result data here"`)}}},
			{Role: "assistant", Content: []AnthropicContent{
				{Type: "thinking", Thinking: prevThinking},
				{Type: "text", Text: "ok"},
			}},
		},
	})
	if prefill <= baseline {
		t.Errorf("current-turn(prefill) thinking should be counted: prefill %d <= baseline %d", prefill, baseline)
	}
}

func TestEstimateInputTokens_ToolResultArrayContent(t *testing.T) {
	// tool_result.content 为数组形态 [{"type":"text","text":"..."}] —— flattenToolResultContent 展开
	body := []byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"text","text":"the tool returned this long output"}]}]}]}`)
	var req AnthropicRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	got := estimateInputTokens(&req)
	if got < 5 {
		t.Errorf("tool_result array content estimate = %d, want >= 5", got)
	}
}

func TestEstimateInputTokens_ToolUseBlock(t *testing.T) {
	// tool_use 块的 name + input 应计入
	req := &AnthropicRequest{
		Model: "claude-sonnet-4-5",
		Messages: []AnthropicMessage{
			{Role: "assistant", Content: []AnthropicContent{
				{Type: "tool_use", ID: "toolu_1", Name: "get_weather", Input: map[string]interface{}{"city": "Beijing"}},
			}},
		},
	}
	got := estimateInputTokens(req)
	if got < 3 {
		t.Errorf("tool_use estimate = %d, want >= 3", got)
	}
}

func TestEstimateInputTokens_NilSafe(t *testing.T) {
	// nil 请求不 panic,保底 1
	if got := estimateInputTokens(nil); got != 1 {
		t.Errorf("nil request = %d, want 1", got)
	}
}

func TestEstimateInputTokens_HardCap(t *testing.T) {
	// 构造超大输入(远超 countTokensHardCap),断言被钳制到硬顶,不崩、不溢出
	huge := strings.Repeat("中", 10_000_000) // 1000 万中文字符 → 估算 ≈ 666 万 > 硬顶 100 万
	req := &AnthropicRequest{
		Model:    "claude-sonnet-4-5",
		Messages: []AnthropicMessage{{Role: "user", Content: []AnthropicContent{{Type: "text", Text: huge}}}},
	}
	got := estimateInputTokens(req)
	if got != countTokensHardCap {
		t.Errorf("huge input = %d, want hard cap %d", got, countTokensHardCap)
	}
}

// ===== thinking 计入口径对齐官方(仅当前轮计入,前序剔除) =====

// TestEstimate_Thinking_PreviousTurnSkipped:[user, assistant(thinking+text), user]
// 末条非 assistant,前序 assistant 的 thinking 应被剔除。断言:带 thinking 的估算 == 去掉
// thinking 的同请求估算(证明前序 thinking 零增量)。
func TestEstimate_Thinking_PreviousTurnSkipped(t *testing.T) {
	const thinking = "I should reason step by step about this number theory problem before answering"
	withThinking := &AnthropicRequest{
		Model: "claude-sonnet-4-5",
		Messages: []AnthropicMessage{
			{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "Are there infinitely many primes p with p mod 4 == 3?"}}},
			{Role: "assistant", Content: []AnthropicContent{
				{Type: "thinking", Thinking: thinking},
				{Type: "text", Text: "Yes, there are infinitely many such primes."},
			}},
			{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "Can you write a formal proof?"}}},
		},
	}
	// 对照:把同一 assistant 轮的 thinking 块置空(其余完全一致)。
	withoutThinking := &AnthropicRequest{
		Model: "claude-sonnet-4-5",
		Messages: []AnthropicMessage{
			{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "Are there infinitely many primes p with p mod 4 == 3?"}}},
			{Role: "assistant", Content: []AnthropicContent{
				{Type: "text", Text: "Yes, there are infinitely many such primes."},
			}},
			{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "Can you write a formal proof?"}}},
		},
	}
	got := estimateInputTokens(withThinking)
	want := estimateInputTokens(withoutThinking)
	if got != want {
		t.Errorf("previous-turn thinking must be excluded: with=%d, without=%d (should match)", got, want)
	}
}

// TestEstimate_Thinking_CurrentTurnPrefillCounted:末条为 assistant prefill 且带 thinking,
// 该 thinking 属"当前轮"应计入。断言:估算值严格高于去掉该 thinking 的同请求。
func TestEstimate_Thinking_CurrentTurnPrefillCounted(t *testing.T) {
	const thinking = "Let me carefully check the base case of this induction argument before continuing"
	withThinking := &AnthropicRequest{
		Model: "claude-sonnet-4-5",
		Messages: []AnthropicMessage{
			{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "Prove by induction that sum 1..n = n(n+1)/2"}}},
			{Role: "assistant", Content: []AnthropicContent{
				{Type: "thinking", Thinking: thinking},
				{Type: "text", Text: "Base case"},
			}},
		},
	}
	withoutThinking := &AnthropicRequest{
		Model: "claude-sonnet-4-5",
		Messages: []AnthropicMessage{
			{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "Prove by induction that sum 1..n = n(n+1)/2"}}},
			{Role: "assistant", Content: []AnthropicContent{
				{Type: "text", Text: "Base case"},
			}},
		},
	}
	got := estimateInputTokens(withThinking)
	baseline := estimateInputTokens(withoutThinking)
	if got <= baseline {
		t.Errorf("current-turn(prefill) thinking must be counted: with=%d, without=%d (should be strictly greater)", got, baseline)
	}
}

// ===== image 计入口径修正(patch 固定估值替代 base64 字符法) =====

// TestEstimate_Image_FlatCost:1 个 base64 image 块(Data 长 1.2M 字符)+ 一句 text。
// 旧实现把 1.2M base64 字符按 4 字符/token 计会估成 ~30 万 token;新实现每个 image 块按
// imageBlockTokenCost 固定计入,合计应 < 3000(真实约 1.6K 图 + 少量文本)。锁定量级归正。
func TestEstimate_Image_FlatCost(t *testing.T) {
	bigBase64 := strings.Repeat("A", 1_200_000) // 1.2M base64 字符模拟一张图
	req := &AnthropicRequest{
		Model: "claude-sonnet-4-5",
		Messages: []AnthropicMessage{{Role: "user", Content: []AnthropicContent{
			{Type: "image", Source: &AnthropicImageSource{Type: "base64", MediaType: "image/png", Data: bigBase64}},
			{Type: "text", Text: "Describe this image concisely"},
		}}},
	}
	got := estimateInputTokens(req)
	if got >= 3000 {
		t.Errorf("image flat-cost regression: got %d, want < 3000 (base64 char-counting leaked back: imageBlockTokenCost=%d)", got, imageBlockTokenCost)
	}
	if got < imageBlockTokenCost {
		t.Errorf("image flat-cost too low: got %d, want >= imageBlockTokenCost %d", got, imageBlockTokenCost)
	}
	// 对照:去掉 image 块后(仅文本)估算应严格更低,且两者之差恰好等于 imageBlockTokenCost。
	textOnly := &AnthropicRequest{
		Model: "claude-sonnet-4-5",
		Messages: []AnthropicMessage{{Role: "user", Content: []AnthropicContent{
			{Type: "text", Text: "Describe this image concisely"},
		}}},
	}
	textOnlyEst := estimateInputTokens(textOnly)
	if got-textOnlyEst != imageBlockTokenCost {
		t.Errorf("image block delta = %d, want exactly imageBlockTokenCost %d", got-textOnlyEst, imageBlockTokenCost)
	}
}

// TestEstimate_ImageURL_FlatCost:URL 型 image(source.type=url)官方同样按 patch 计,应按固定估值计入。
func TestEstimate_ImageURL_FlatCost(t *testing.T) {
	req := &AnthropicRequest{
		Model: "claude-sonnet-4-5",
		Messages: []AnthropicMessage{{Role: "user", Content: []AnthropicContent{
			{Type: "image", Source: &AnthropicImageSource{Type: "url", MediaType: "image/jpeg", Url: "https://example.com/big.jpg"}},
			{Type: "text", Text: "What is in this picture"},
		}}},
	}
	got := estimateInputTokens(req)
	textOnly := estimateInputTokens(&AnthropicRequest{
		Model:    "claude-sonnet-4-5",
		Messages: []AnthropicMessage{{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "What is in this picture"}}}},
	})
	if got-textOnly != imageBlockTokenCost {
		t.Errorf("url image block delta = %d, want exactly imageBlockTokenCost %d", got-textOnly, imageBlockTokenCost)
	}
}

// TestEstimate_Image_MultiBlockAccumulates:多个 image 块应累加多次固定估值。
func TestEstimate_Image_MultiBlockAccumulates(t *testing.T) {
	req := &AnthropicRequest{
		Model: "claude-sonnet-4-5",
		Messages: []AnthropicMessage{{Role: "user", Content: []AnthropicContent{
			{Type: "image", Source: &AnthropicImageSource{Type: "base64", MediaType: "image/png", Data: strings.Repeat("B", 50_000)}},
			{Type: "image", Source: &AnthropicImageSource{Type: "base64", MediaType: "image/jpeg", Data: strings.Repeat("C", 30_000)}},
			{Type: "text", Text: "Compare these two"},
		}}},
	}
	got := estimateInputTokens(req)
	// 2 个 image 块应贡献 2*imageBlockTokenCost,加上一句短文本。
	if got < 2*imageBlockTokenCost {
		t.Errorf("multi-image should accumulate twice imageBlockTokenCost: got %d, want >= %d", got, 2*imageBlockTokenCost)
	}
	if got >= 3*imageBlockTokenCost {
		t.Errorf("multi-image over-charged: got %d, want < 3*imageBlockTokenCost %d", got, 3*imageBlockTokenCost)
	}
}

// ===== 端点路由 + 不打上游 业务测试 =====

// runCountTokensRequest 经 handleNvidia 完整入口打 /nvidia/v1/messages/count_tokens,
// 返回 (statusCode, body)。配合一个 mock 上游 server —— 若端点正确走本地估算,
// 上游 server 不应被命中(hitCount 保持 0)。
func runCountTokensRequest(t *testing.T, upstreamHit *int, body string) (int, string) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		(*upstreamHit)++
	}))
	defer upstream.Close()
	acc := mkNvidiaAccount("nv-ct", "nvidia-ct", "k", upstream.URL, "moonshotai/kimi-k2.5")
	handler, _, _, _ := newNvidiaTestHandler(t, []*account.Account{acc})
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages/count_tokens", bytes.NewReader([]byte(body)))
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "uct", UserKey: "kct"})
	return rr.Code, rr.Body.String()
}

func TestCountTokens_EndpointRouting_NoUpstream(t *testing.T) {
	var upstreamHit int
	code, body := runCountTokensRequest(t, &upstreamHit, `{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hello world"}]}`)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", code, body)
	}
	if upstreamHit != 0 {
		t.Fatalf("upstream should NOT be hit for count_tokens, got hits=%d", upstreamHit)
	}
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("invalid response json: %v body=%s", err, body)
	}
	tokens, ok := out["input_tokens"].(float64)
	if !ok {
		t.Fatalf("missing/non-numeric input_tokens: %+v", out)
	}
	if tokens < 1 {
		t.Errorf("input_tokens = %v, want >= 1", tokens)
	}
}

func TestCountTokens_EndpointRouting_TrailingSlash(t *testing.T) {
	// 尾带斜杠的路径也应识别(handleNvidia 入口已经 TrimRight 尾斜杠归一)
	var upstreamHit int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHit++
	}))
	defer upstream.Close()
	acc := mkNvidiaAccount("nv-ct2", "nvidia-ct2", "k", upstream.URL, "moonshotai/kimi-k2.5")
	handler, _, _, _ := newNvidiaTestHandler(t, []*account.Account{acc})
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/messages/count_tokens/", bytes.NewReader([]byte(`{"model":"x","messages":[{"role":"user","content":"hi"}]}`)))
	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u", UserKey: "k"})
	if rr.Code != http.StatusOK {
		t.Errorf("trailing slash route = %d, want 200 body=%s", rr.Code, rr.Body.String())
	}
	if upstreamHit != 0 {
		t.Errorf("upstream hit on trailing-slash route: %d", upstreamHit)
	}
}

func TestCountTokens_InvalidJSON_400(t *testing.T) {
	var upstreamHit int
	code, body := runCountTokensRequest(t, &upstreamHit, `{not valid json`)
	if code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid json, got %d body=%s", code, body)
	}
	if !strings.Contains(body, "invalid_request_error") {
		t.Errorf("400 body should be anthropic error shape: %s", body)
	}
}

func TestCountTokens_EmptyMessages_MinOne(t *testing.T) {
	var upstreamHit int
	code, body := runCountTokensRequest(t, &upstreamHit, `{"model":"claude-sonnet-4-5","messages":[]}`)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	var out map[string]interface{}
	_ = json.Unmarshal([]byte(body), &out)
	if tokens, _ := out["input_tokens"].(float64); tokens != 1 {
		t.Errorf("empty messages input_tokens = %v, want 1 (floor)", tokens)
	}
}

func TestCountTokens_KnownEstimate(t *testing.T) {
	// 固定输入断言确定值:1 中文句 "你好世界"(4 CJK) + " hi"(3 other) → ceil(4/1.5) + ceil(3/4)
	// 4/1.5 = 2.667 → ceil 3; 3/4 = 0.75 → ceil 1; 合计 4。但加权是分别累加后再整体 ceil:
	// raw = 4/1.5 + 3/4 = 2.667 + 0.75 = 3.417 → ceil 4
	var upstreamHit int
	code, body := runCountTokensRequest(t, &upstreamHit, `{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"你好世界 hi"}]}`)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	tokens, _ := out["input_tokens"].(float64)
	// 允许 ±1 容差(text " hi" 含空格可能影响 otherCount 计数精度)
	if tokens < 3 || tokens > 5 {
		t.Errorf("known estimate input_tokens = %v, want 3..5", tokens)
	}
}

// ===== /route/v1/messages/count_tokens 端点拦截回归(消除计数回退小推理请求) =====
//
// 背景:Anthropic 官方 LLM Gateway Protocol 把 /v1/messages/count_tokens 标为 (optional),
// 缺失/失败不致命,Claude Code 自动回退到本地估算。但官方文档明确当网关不提供该端点时,
// CLI 会"回退成用 /v1/messages 推理端点计数",产生额外的小推理请求(号池消耗 + 日志噪声)。
// nvidia 池(/nvidia/v1/messages/count_tokens 与 /vc 别名)与 grok 池已分别在本池入口拦截,
// 但 /route/* 通用转发入口此前缺失该拦截 → Other/deepseek/阿里云等经 /route 进入的号池
// 仍会产生计数回退小推理请求。router_entry.go 现已在 /route 三生成端点 404 兜底之前补齐拦截,
// 复用 handleNvidiaCountTokens 纯本地估算(零上游、零号池、零计费)。以下用例锁定该路由回归。

// TestRouteCountTokens_NoUpstream_LocalEstimate 打 /route/v1/messages/count_tokens,
// 断言走纯本地估算:HTTP 200、input_tokens 非零正整数。
// 关键:不走 resolveRoutedTarget 选号,故即便没有任何账号/号池规则也必须 200。
func TestRouteCountTokens_NoUpstream_LocalEstimate(t *testing.T) {
	// handler 不注入任何号池账号,且 settingsMgr 故意返回不匹配入站 model 的空规则表
	// (证明 count_tokens 拦截发生在 resolveRoutedTarget 之前,不依赖任何号池规则)。
	h := &APICompatHandler{
		accountMgr:   account.NewManager(),
		settingsMgr:  &stubPassThroughSettings{routes: nil},
		logFn:        func(string) {},
		client:       &http.Client{Timeout: 5 * time.Second},
		streamClient: &http.Client{Timeout: 0},
	}

	body := `{"model":"other/aliyun/deepseek-v4-flash-0731","messages":[{"role":"user","content":"hello count tokens world"}]}`
	req := httptest.NewRequest(http.MethodPost, "/route/v1/messages/count_tokens", bytes.NewReader([]byte(body)))
	rr := httptest.NewRecorder()
	h.handleRoutedForward(rr, req, &RelaySession{UserID: "u", UserKey: "k"})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(rr.Body.String()), &out); err != nil {
		t.Fatalf("invalid response json: %v body=%s", err, rr.Body.String())
	}
	tokens, ok := out["input_tokens"].(float64)
	if !ok {
		t.Fatalf("missing/non-numeric input_tokens: %+v", out)
	}
	if tokens < 1 {
		t.Errorf("input_tokens = %v, want >= 1", tokens)
	}
}

// TestRouteCountTokens_NonMessagesNotIntercepted 确保 /route 普通生成端点仍正常走号池
// 转发逻辑,不被 count_tokens 拦截误伤:打 /route/v1/messages(无 count_tokens 后缀),
// 在无匹配号池规则时应如旧回 404(与既有 TestRouteEndpoints_UnmatchedModel 行为一致),而非被
// count_tokens 分支吞成 200。防止"count_tokens 拦截范围过宽"的回归。
func TestRouteCountTokens_NonMessagesNotIntercepted(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = r.Body.Close() }))
	defer upstream.Close()

	// 空 routes → resolveRoutedTarget 走默认 nvidia 规则会兜底命中,故用「乱填无规则」表只匹配 x-*,
	// 使 /route/v1/messages 上不命中任何号池规则,走 404 分支(与既有非测同理)。
	h := &APICompatHandler{
		accountMgr:  account.NewManager(),
		settingsMgr: &stubPassThroughSettings{routes: []settings.ModelRouteRule{{Pattern: "x-*", TargetProvider: "x", Enabled: true}}},
		logFn:       func(string) {},
		client:      &http.Client{Timeout: 5 * time.Second},
		streamClient: &http.Client{Timeout: 0},
	}

	body := `{"model":"nomatch","messages":[]}`
	req := httptest.NewRequest(http.MethodPost, "/route/v1/messages", bytes.NewReader([]byte(body)))
	rr := httptest.NewRecorder()
	h.handleRoutedForward(rr, req, &RelaySession{UserID: "u", UserKey: "k"})

	if rr.Code != http.StatusNotFound {
		t.Fatalf("non-count_tokens /route/v1/messages must fall through to route resolution (404 when unmatched), got %d body=%s", rr.Code, rr.Body.String())
	}
}

// TestRouteCountTokens_TrailingSlash 尾带斜杠的 /route/v1/messages/count_tokens/ 也应识别:
// handleRoutedForward 在入口已 strings.TrimRight(path, "/") 归一,拦截判定基于归一后的 path。
func TestRouteCountTokens_TrailingSlash(t *testing.T) {
	h := &APICompatHandler{
		accountMgr:  account.NewManager(),
		settingsMgr: &stubPassThroughSettings{routes: nil},
		logFn:       func(string) {},
		client:      &http.Client{Timeout: 5 * time.Second},
		streamClient: &http.Client{Timeout: 0},
	}

	req := httptest.NewRequest(http.MethodPost, "/route/v1/messages/count_tokens/", bytes.NewReader([]byte(`{"model":"x","messages":[{"role":"user","content":"hi"}]}`)))
	rr := httptest.NewRecorder()
	h.handleRoutedForward(rr, req, &RelaySession{UserID: "u", UserKey: "k"})

	if rr.Code != http.StatusOK {
		t.Errorf("trailing slash route = %d, want 200 body=%s", rr.Code, rr.Body.String())
	}
}

// TestRouteCountTokens_InvalidJSON_400 非法 JSON 入站 count_tokens 应回 400(与 nvidia 链路口径一致)。
func TestRouteCountTokens_InvalidJSON_400(t *testing.T) {
	h := &APICompatHandler{
		accountMgr:  account.NewManager(),
		settingsMgr: &stubPassThroughSettings{routes: nil},
		logFn:       func(string) {},
		client:      &http.Client{Timeout: 5 * time.Second},
		streamClient: &http.Client{Timeout: 0},
	}

	req := httptest.NewRequest(http.MethodPost, "/route/v1/messages/count_tokens", bytes.NewReader([]byte(`{not valid json`)))
	rr := httptest.NewRecorder()
	h.handleRoutedForward(rr, req, &RelaySession{UserID: "u", UserKey: "k"})

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid json, got %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "invalid_request_error") {
		t.Errorf("400 body should be anthropic error shape: %s", rr.Body.String())
	}
}
