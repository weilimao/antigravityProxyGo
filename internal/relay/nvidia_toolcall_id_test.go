package relay

import (
	"context"
	"strings"
	"testing"
)

// nvidia_toolcall_id_test.go 锁定 NVIDIA 链路 OpenAI→Anthropic 转换时 tool_use id 的
// 全局唯一性重写策略(20260828 修复:上游 NIM 每轮重发 "Bash:0" 类自增短 id,
// 与客户端历史已执行调用冲突,被 Claude Code SDK 判为重复调用丢弃 → 死循环)。
//
// 核心不变式:
//   1) 非 toolu_ 前缀的上游 id(Bash:0/Read:0/call_xxx/空)一律重写为 toolu_nv_* 全局唯一 id
//   2) 两次调用(模拟跨轮新请求)产出 id 必不相同(客户端按 id 分新旧调用)
//   3) 已 toolu_ 前缀(官方格式)原样透传(防御)
//   4) 断流重试轮经 pinnedToolIDs 复用首轮 id,客户端不会见同块两 id
//   5) 端到端(流式):同一上游响应跑两次独立转换,产出 tool_use id 必须不同

// TestRewriteUpstreamToolCallID_RewritesNonToolu 锁定非 toolu_ 前缀 id 一律重写。
func TestRewriteUpstreamToolCallID_RewritesNonToolu(t *testing.T) {
	cases := []string{"Bash:0", "Bash_0", "read:1", "call_abc123", "", "  "}
	for _, in := range cases {
		got := rewriteUpstreamToolCallID(in, 0)
		if !strings.HasPrefix(got, "toolu_nv_") {
			t.Errorf("rewriteUpstreamToolCallID(%q) = %q, 应带 toolu_nv_ 前缀", in, got)
		}
		if strings.Contains(got, ":") {
			t.Errorf("rewriteUpstreamToolCallID(%q) = %q, 不应含冒号", in, got)
		}
	}
}

// TestRewriteUpstreamToolCallID_UniqueAcrossCalls 锁定跨轮唯一性(核心修复目标):
// 同一上游 id(如每轮都重置的 Bash:0)在两次调用中必须产出不同的 Anthropic id,
// 否则 Claude Code SDK 会把第二轮新调用判为历史重复而忽略。
func TestRewriteUpstreamToolCallID_UniqueAcrossCalls(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		id := rewriteUpstreamToolCallID("Bash:0", 0)
		if seen[id] {
			t.Fatalf("第 %d 次调用产出重复 id %q —— 会触发客户端判重丢调用死循环", i, id)
		}
		seen[id] = true
	}
}

// TestRewriteUpstreamToolCallID_PreservesToolu 锁定官方格式透传(防御)。
func TestRewriteUpstreamToolCallID_PreservesToolu(t *testing.T) {
	in := "toolu_01XFDUDYJgAACzvnptvVoYEL"
	if got := rewriteUpstreamToolCallID(in, 3); got != in {
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
		ID:       "Bash:0", // 上游重试轮仍发同样的 id
		Type:     "function",
		Function: ChatToolCallFunction{Name: "Bash", Arguments: `{"command":"git status"}`},
	}, sink)
	if got := s2.emittedToolIDs[0]; got != firstID {
		t.Fatalf("重试轮 id 应复用首轮 pin id %q, 实际 %q —— 客户端会见同块两 id", firstID, got)
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
				// 粗提取 "id":"..."
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
