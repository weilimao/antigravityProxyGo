package relay

import (
	"context"
	"strings"
	"testing"
)

// nvidia_toolcall_id_test.go 锁定 OpenAI→Anthropic 转换时 tool_use id 的全局唯一性重写策略
// (20260828 修复:上游每轮重发 "Bash:0" 类自增短 id,与客户端历史已执行调用冲突,
// 被 Claude Code SDK 判为重复调用丢弃 → 工具调用死循环)。
//
// 核心不变式:
//   1) 非 toolu_ 前缀的上游 id(Bash:0/Read:0/call_xxx/空)一律重写为 toolu_nv_* 全局唯一 id
//   2) 两次调用(模拟跨轮新请求)产出 id 必不相同(客户端按 id 区分新旧调用)
//   3) 已 toolu_ 前缀(官方格式)原样透传(防御)
//   4) 断流重试轮经 pinnedToolIDs 复用首轮 id,客户端不会见同块两 id
//   5) 端到端(流式):同一上游响应跑两次独立转换,产出 tool_use id 必须不同
//   6) 非流式路径与非流式 Other 池路径同样重写(两处同源调用点)

// TestRewriteToolCallID_RewritesNonToolu 锁定非 toolu_ 前缀 id 一律重写。
func TestRewriteToolCallID_RewritesNonToolu(t *testing.T) {
	cases := []string{"Bash:0", "Bash_0", "read:1", "call_abc123", "", "  "}
	for _, in := range cases {
		got := rewriteToolCallID(in, 0)
		if !strings.HasPrefix(got, "toolu_nv_") {
			t.Errorf("rewriteToolCallID(%q) = %q, 应带 toolu_nv_ 前缀", in, got)
		}
		if strings.Contains(got, ":") {
			t.Errorf("rewriteToolCallID(%q) = %q, 不应含冒号", in, got)
		}
	}
}

// TestRewriteToolCallID_UniqueAcrossCalls 锁定跨轮唯一性(核心修复目标):
// 同一上游 id(如每轮都重置的 Bash:0)在两次调用中必须产出不同的 Anthropic id,
// 否则 Claude Code SDK 会把第二轮新调用判为历史重复而忽略。
func TestRewriteToolCallID_UniqueAcrossCalls(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		id := rewriteToolCallID("Bash:0", 0)
		if seen[id] {
			t.Fatalf("第 %d 次调用产出重复 id %q —— 会触发客户端判重丢调用死循环", i, id)
		}
		seen[id] = true
	}
}

// TestRewriteToolCallID_PreservesToolu 锁定官方格式透传(防御)。
func TestRewriteToolCallID_PreservesToolu(t *testing.T) {
	in := "toolu_01XFDUDYJgAACzvnptvVoYEL"
	if got := rewriteToolCallID(in, 3); got != in {
		t.Errorf("toolu_ 前缀 id 应原样透传, got %q", got)
	}
}

// TestEmitToolCallDelta_PinnedIDRoundTrip 锁定断流重试的 ID 一致性:
// 首轮生成重写 id 入 emittedToolIDs,重试轮经 pinnedToolIDs 复用同一 id。
func TestEmitToolCallDelta_PinnedIDRoundTrip(t *testing.T) {
	// 首轮:无 pin
	s := &sseBlockStates{
		blocks:         map[int]*sseBlock{},
		emittedToolIDs: map[int]string{},
	}
	sink := &captureSink{}
	s.emitToolCallDelta(ChatToolCall{
		Index:    0,
		ID:       "Bash:0",
		Type:     "function",
		Function: ChatToolCallFunction{Name: "Bash", Arguments: `{"command":"git status"}`},
	}, sink)
	firstID := s.emittedToolIDs[0]
	if !strings.HasPrefix(firstID, "toolu_nv_") {
		t.Fatalf("首轮 id 应为 toolu_nv_* 格式, got %q", firstID)
	}

	// 重试轮:pin 首轮 id
	s2 := &sseBlockStates{
		blocks:         map[int]*sseBlock{},
		pinnedToolIDs:  map[int]string{0: firstID},
		emittedToolIDs: map[int]string{},
	}
	s2.emitToolCallDelta(ChatToolCall{
		Index:    0,
		ID:       "Bash:0",
		Type:     "function",
		Function: ChatToolCallFunction{Name: "Bash", Arguments: `{"command":"git status"}`},
	}, sink)
	if got := s2.emittedToolIDs[0]; got != firstID {
		t.Fatalf("重试轮 id 应复用首轮 pin id %q, 实际 %q —— 客户端会见同块两 id", firstID, got)
	}
}

// TestNonStreamResponse_ToolIDRewritten 直接锁定非流式响应路径
// (nvidia_translate_response.go openAIChoiceMessageToAnthropic)的 tool_use id 重写:
// 上游自增短 id 不透传,连续两次转换产出不同唯一 id。
func TestNonStreamResponse_ToolIDRewritten(t *testing.T) {
	resp := &OpenAIChatResponse{
		Choices: []OpenAIChatChoice{{
			Message: ChatMessage{
				Role: "assistant",
				ToolCalls: []ChatToolCall{{
					ID:       "Bash:0",
					Type:     "function",
					Function: ChatToolCallFunction{Name: "Bash", Arguments: `{"command":"ls"}`},
				}},
			},
			FinishReason: "tool_calls",
		}},
	}
	extractToolID := func() string {
		_, tools := openAIChoiceMessageToAnthropic(resp.Choices[0].Message)
		if len(tools) != 1 {
			t.Fatalf("tools 应有 1 个, got %d", len(tools))
		}
		return tools[0].ID
	}
	id1 := extractToolID()
	id2 := extractToolID()
	if !strings.HasPrefix(id1, "toolu_nv_") || !strings.HasPrefix(id2, "toolu_nv_") {
		t.Fatalf("非流式路径 tool_use id 应为 toolu_nv_* 重写格式, got %q / %q", id1, id2)
	}
	if id1 == id2 {
		t.Fatalf("非流式连续两次转换产出相同 id %q,客户端会判重丢调用", id1)
	}
}

// TestPassthroughAssistantToolIDRewritten 锁定 Other 池请求侧(OpenAI→Anthropic)透传行为:
// passthrough 链路将 Anthropic 上游响应转给 OpenAI 格式客户端(如 Codex/aider),
// 这些客户端不像 Claude Code 那样按 tool_use.id 判重,所以 id 应原样透传给上游,
// 避免上游转换丢失关联(也避免上游误将我们的 toolu_nv_* 格式当外来 id 拒收)。
// 该测试同时是 T1 的坚守——我曾误把 Other 池代码加进重写,正确修复范围仅在 NVIDIA 链路。
func TestPassthroughAssistantToolIDRewritten(t *testing.T) {
	m := ChatMessage{
		Role: "assistant",
		ToolCalls: []ChatToolCall{{
			ID:       "call_0",
			Type:     "function",
			Function: ChatToolCallFunction{Name: "get_weather", Arguments: `{}`},
		}},
	}
	got := openAIAssistantToAnthropic(m)
	if len(got.Content) != 1 || got.Content[0].Type != "tool_use" {
		t.Fatalf("应有 1 个 tool_use 块, got %+v", got.Content)
	}
	// 透传:上游发的 id(call_xxx / toolu_xxx)原样保留,不介入
	id := got.Content[0].ID
	if id != "call_0" {
		t.Fatalf("透传侧应原样保留上游 id %q, got %q", "call_0", id)
	}
}

// TestE2ETwoRoundsProduceDifferentToolIDs 端到端:同一段上游 SSE 字节跑两次独立转换
// (模拟客户端连续两轮请求,上游行为完全一致),两次产出的 tool_use id 必须不同。
// 这是修复前死循环的精确回归:若两轮 id 相同,Claude Code 第二轮起永远丢调用。
func TestE2ETwoRoundsProduceDifferentToolIDs(t *testing.T) {
	upstream := "data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"Bash:0\",\"type\":\"function\",\"function\":{\"name\":\"Bash\",\"arguments\":\"{\\\"command\\\":\\\"git status\\\"}\"}}]},\"finish_reason\":null}],\"created\":1,\"model\":\"k\",\"object\":\"chat.completion.chunk\",\"usage\":null}\n\n" +
		"data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}],\"created\":1,\"model\":\"k\",\"object\":\"chat.completion.chunk\",\"usage\":null}\n\n" +
		"data: [DONE]\n\n"

	extractID := func() string {
		sink := &captureSink{}
		_, _, _, fe, st, _, err := openAIChatSSEToAnthropicSSEIntoPinned(
			context.Background(), strings.NewReader(upstream), nil, sink, "msg_t", "k", 100, nil)
		if err != nil || !fe || !st {
			t.Fatalf("转换异常: err=%v finish=%v term=%v", err, fe, st)
		}
		for _, e := range sink.events {
			if strings.HasPrefix(e, "content_block_start|") && strings.Contains(e, `"tool_use"`) {
				const key = `"id":"`
				i := strings.Index(e, key)
				if i < 0 {
					continue
				}
				rest := e[i+len(key):]
				j := strings.Index(rest, `"`)
				if j > 0 {
					return rest[:j]
				}
			}
		}
		t.Fatalf("未找到 tool_use 块, events=%v", sink.events)
		return ""
	}

	id1 := extractID()
	id2 := extractID()
	if !strings.HasPrefix(id1, "toolu_nv_") || !strings.HasPrefix(id2, "toolu_nv_") {
		t.Fatalf("两轮 id 均应 toolu_nv_* 前缀, got %q / %q", id1, id2)
	}
	if id1 == id2 {
		t.Fatalf("两轮产出相同 id %q —— 修复目标未达成,客户端第二轮会丢弃该调用", id1)
	}
}
