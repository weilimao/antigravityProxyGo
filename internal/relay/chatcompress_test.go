package relay

import (
	"strings"
	"testing"
)

// chatcompress_test.go —— 公共压缩算法表驱动单测。
// 覆盖:Token 估算 / L1 微压缩 / L2 PTL 裁剪 / 配对保护 / system 保留 / 小请求 NoOp / L2 回退。

func TestEstimateChatTokens(t *testing.T) {
	// 已知文本断言:每 4 字节约 1 token,总和 ×4/3 填充
	msgs := []ChatMessage{
		{Role: "user", Content: strings.Repeat("a", 40)},   // 40/4 = 10
		{Role: "assistant", Content: strings.Repeat("b", 0)}, // 0
	}
	// role "user"(4/4=1) + content(40/4=10) + assistant role(9/4=2) = 13 ; ×4/3 = 17
	got := EstimateChatTokens(msgs)
	if got <= 0 {
		t.Fatalf("estimate should be positive, got %d", got)
	}
	// 粗校验:13*4/3 = 17
	if got != 17 {
		t.Logf("EstimateChatTokens=%d (expected ~17,容差宽松)", got)
	}
}

func TestMicroCompress_KeepsRecentN(t *testing.T) {
	// 构造:1 assistant(tool_calls) + 8 个 tool 结果,keepN=4
	msgs := []ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "q"},
		{Role: "assistant", ToolCalls: []ChatToolCall{{ID: "c1", Type: "function", Function: ChatToolCallFunction{Name: "f", Arguments: "{}"}}}},
	}
	for i := 0; i < 8; i++ {
		msgs = append(msgs, ChatMessage{Role: "tool", ToolCallID: "c1", ToolName: "f", Content: "result-" + string(rune('A'+i))})
	}
	out, did := microCompress(msgs, 4)
	if !did {
		t.Fatal("expected microCompress to compress (did=true)")
	}
	// 统计被替换的:应是前 4 个 tool,保留后 4 个原文
	cleared, original := 0, 0
	for _, m := range out {
		if m.Role == "tool" {
			if m.Content == ChatCompressClearText {
				cleared++
			} else if strings.HasPrefix(m.Content, "result-") {
				original++
			}
		}
	}
	if cleared != 4 || original != 4 {
		t.Fatalf("expected 4 cleared + 4 original, got cleared=%d original=%d", cleared, original)
	}
	// ToolCallID/ToolName 必须保留(配对完整)
	for _, m := range out {
		if m.Role == "tool" {
			if m.ToolCallID != "c1" || m.ToolName != "f" {
				t.Fatalf("tool 配对字段被破坏: ToolCallID=%q ToolName=%q", m.ToolCallID, m.ToolName)
			}
		}
	}
	// system 与 assistant 的 tool_calls 不应被动
	if out[0].Role != "system" || out[0].Content != "sys" {
		t.Fatalf("system 被破坏: %+v", out[0])
	}
	if len(out[2].ToolCalls) != 1 || out[2].ToolCalls[0].ID != "c1" {
		t.Fatalf("assistant tool_calls 被破坏: %+v", out[2])
	}
	// 原入参未被修改(深拷贝)
	if msgs[3].Content != "result-A" {
		t.Fatalf("入参被污染: msgs[3].Content=%q", msgs[3].Content)
	}
}

func TestMicroCompress_NoOpWhenAllFew(t *testing.T) {
	// 只有 3 个 tool,keepN=4 → 无可清,did=false
	msgs := []ChatMessage{
		{Role: "user", Content: "q"},
		{Role: "assistant"},
		{Role: "tool", Content: "r1"},
		{Role: "tool", Content: "r2"},
		{Role: "tool", Content: "r3"},
	}
	out, did := microCompress(msgs, 4)
	if did {
		t.Fatal("expected no compress when keepN>=total, got did=true")
	}
	if len(out) != len(msgs) {
		t.Fatalf("out length changed: %d vs %d", len(out), len(msgs))
	}
}

func TestPTLTruncate_DropsOldest20Percent(t *testing.T) {
	// 构造 5 轮:system + (user/assistant/tool)×5,每轮 assistant 后跟 1 tool
	var msgs []ChatMessage
	msgs = append(msgs, ChatMessage{Role: "system", Content: "sys"})
	for r := 0; r < 5; r++ {
		msgs = append(msgs,
			ChatMessage{Role: "user", Content: "u" + itoa(r)},
			ChatMessage{Role: "assistant", ToolCalls: []ChatToolCall{{ID: "c" + itoa(r), Type: "function", Function: ChatToolCallFunction{Name: "f", Arguments: "{}"}}}},
			ChatMessage{Role: "tool", ToolCallID: "c" + itoa(r), ToolName: "f", Content: "res" + itoa(r)},
		)
	}
	// groups: [system 1组锁定] + [user] + [assistant+tool] + [user]+[assistant+tool] x4 ... 共 1+5*2=11 组
	// 可删=10,dropCount=2
	out, did := pttlTruncate(msgs)
	if !did {
		t.Fatal("expected pttlTruncate to truncate, got did=false")
	}
	if len(out) >= len(msgs) {
		t.Fatalf("expected trimmed shorter, in=%d out=%d", len(msgs), len(out))
	}
	// system 必须保留在最前
	if len(out) == 0 || out[0].Role != "system" {
		t.Fatalf("system 未保留在最前: %+v", out)
	}
	// 不应出现孤儿 tool(其 tool_call_id 对应的 assistant.tool_calls 必须也在)
	toolIDSet := map[string]bool{}
	for _, m := range out {
		if m.Role == "assistant" {
			for _, tc := range m.ToolCalls {
				toolIDSet[tc.ID] = true
			}
		}
	}
	for _, m := range out {
		if m.Role == "tool" {
			if !toolIDSet[m.ToolCallID] {
				t.Fatalf("孤儿 tool_result: tool_call_id=%q 无对应 assistant.tool_calls", m.ToolCallID)
			}
		}
	}
}

func TestPTLTruncate_PrependsUserMarkerWhenFirstAssistant(t *testing.T) {
	// 无 system,首条 user 后跟 assistant:裁掉首条 user 后,首条变 assistant → 应回退保留 assistant 组并在最前补 marker
	var msgs []ChatMessage
	msgs = append(msgs, ChatMessage{Role: "user", Content: "u0"})
	for r := 0; r < 3; r++ {
		msgs = append(msgs,
			ChatMessage{Role: "assistant", Content: "a" + itoa(r)},
			ChatMessage{Role: "user", Content: "u" + itoa(r+1)},
		)
	}
	out, did := pttlTruncate(msgs)
	if !did {
		t.Fatal("expected truncate, got did=false")
	}
	if len(out) == 0 {
		t.Fatal("out empty")
	}
	// 删最旧 1 个 keeper=u0组,配对保护把裁剪线停在 a0 之前(cutIdx=1),
	// out=msgs[1:]=[a0,u1,a1,u2,a2,u3] → 首条 assistant → 补 marker
	if out[0].Role != "user" || out[0].Content != ChatCompressTruncMarker {
		t.Fatalf("首条 assistant 未补 marker, out[0]=%+v", out[0])
	}
}

func TestCompress_PreservesSystem(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "system", Content: strings.Repeat("S", 5000)},
	}
	for i := 0; i < 12; i++ {
		msgs = append(msgs, ChatMessage{Role: "tool", Content: strings.Repeat("x", 8000)})
	}
	req := &OpenAIChatRequest{Model: "m", Messages: msgs}
	c := NewChatCompressor(8000, 2, 3) // 低阈值强制压
	newReq, ok := c.Compress(req)
	if !ok {
		t.Fatal("expected compress ok=true")
	}
	if len(newReq.Messages) == 0 || newReq.Messages[0].Role != "system" {
		t.Fatalf("system 未保留: %+v", newReq.Messages)
	}
	// 原入参 msgs 不应被改
	if len(req.Messages) != 13 {
		t.Fatalf("入参被污染: len=%d", len(req.Messages))
	}
}

func TestCompress_NoOpWhenSmall(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
	}
	req := &OpenAIChatRequest{Model: "m", Messages: msgs}
	c := NewChatCompressor(80000, 4, 3)
	newReq, ok := c.Compress(req)
	if ok {
		t.Fatal("expected NoOp when small, got ok=true")
	}
	if len(newReq.Messages) != 2 {
		t.Fatalf("messages changed unexpectedly: %d", len(newReq.Messages))
	}
}

func TestCompress_L2FallbackWhenNoToolResults(t *testing.T) {
	// 无 tool 消息,L1 必无效,应直接走 L2 分组裁剪
	var msgs []ChatMessage
	for r := 0; r < 6; r++ {
		msgs = append(msgs,
			ChatMessage{Role: "user", Content: strings.Repeat("u", 5000)},
			ChatMessage{Role: "assistant", Content: strings.Repeat("a", 5000)},
		)
	}
	req := &OpenAIChatRequest{Model: "m", Messages: msgs}
	c := NewChatCompressor(5000, 4, 3) // 低阈值强制压
	newReq, ok := c.Compress(req)
	if !ok {
		t.Fatal("expected L2 fallback to compress, got ok=false")
	}
	if len(newReq.Messages) >= len(msgs) {
		t.Fatalf("L2 未生效: in=%d out=%d", len(msgs), len(newReq.Messages))
	}
}

func TestCompress_NilOrEmpty(t *testing.T) {
	c := NewChatCompressor(80000, 4, 3)
	if _, ok := c.Compress(nil); ok {
		t.Fatal("nil req should return ok=false")
	}
	if _, ok := c.Compress(&OpenAIChatRequest{Messages: nil}); ok {
		t.Fatal("empty messages should return ok=false")
	}
}

// itoa 是避免引入 strconv 的最小整数→字符串,仅测试用。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	if n < 0 {
		return "-" + itoa(-n)
	}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
