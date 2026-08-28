package relay

import (
	"encoding/json"
	"strings"
	"testing"
)

// nvidia_translate_request_test.go
//
// 锁定 NVIDIA Anthropic→OpenAI Chat 请求方向转换的关键行为,聚焦于
// anthropicAssistantToChat 对 assistant 消息 content 各块类型的翻译正确性。
//
// 历史沉淀的核心 bug(20260828 修复):
//   旧版 anthropicAssistantToChat 的 switch 只 case "text"/"tool_use",对 Claude Code
//   开启 interleaved-thinking 后回传的 {"type":"thinking","thinking":"...","signature":""} 块
//   静默丢弃 → 上游模型(Kimi-K2/K3, DeepSeek 等推理模型)看到的 assistant 历史被"阉割",
//   无法延续前轮推理轨迹,表现为客户端陷入"思考→夭折→自动追问(no content)"死循环。
//
// 修复方案: thinking 块按原始顺序合并到 ChatMessage.ReasoningContent(OpenAI 协议
// reasoning_content 字段),让上游识别该字段的推理模型能吸收前轮思考脉络。
//
// 本测试文件的锁定目标:
//   1) thinking 块不丢失,翻译到 ReasoningContent
//   2) 多个 thinking 块按原始顺序合并(\n 拼接)
//   3) 空 thinking 块被跳过(不产生空串占位)
//   4) text/tool_use 与 thinking 共存时,各领域互不污染
//   5) thinking-only 的 assistant 消息(content 无 text/tool_use)能正确产出
//      content="" + reasoning_content=<thinking>,而非落入"good,我继续"占位分支
//   6) 序列化产物符合 OpenAI Chat Completions 上游协议(reasoning_content 字段外显)
//   7) 不含 thinking 的旧形态(纯 text 或 text+tool_use)行为零回归

// TestAnthropicAssistantToChat_ThinkingTranslated 锁定:thinking 块被翻译到 ReasoningContent。
func TestAnthropicAssistantToChat_ThinkingTranslated(t *testing.T) {
	msg := AnthropicMessage{
		Role: "assistant",
		Content: []AnthropicContent{
			{Type: "thinking", Thinking: "用户问的是性能瓶颈,先看 pprof 输出", Signature: ""},
			{Type: "text", Text: "我来看下 pprof"},
		},
	}
	got := anthropicAssistantToChat(msg)
	if got.ReasoningContent != "用户问的是性能瓶颈,先看 pprof 输出" {
		t.Errorf("ReasoningContent 期望值 mismatch, got=%q", got.ReasoningContent)
	}
	if got.Content != "我来看下 pprof" {
		t.Errorf("Content 期望值 mismatch, got=%q", got.Content)
	}
	if len(got.ToolCalls) != 0 {
		t.Errorf("ToolCalls 应为空, got=%v", got.ToolCalls)
	}
}

// TestAnthropicAssistantToChat_MultipleThinkingBlocks 锁定:多个 thinking 块按序合并。
func TestAnthropicAssistantToChat_MultipleThinkingBlocks(t *testing.T) {
	msg := AnthropicMessage{
		Role: "assistant",
		Content: []AnthropicContent{
			{Type: "thinking", Thinking: "step1: 看 git status"},
			{Type: "thinking", Thinking: "step2: 看 diff"},
			{Type: "text", Text: "继续"},
		},
	}
	got := anthropicAssistantToChat(msg)
	want := "step1: 看 git status\nstep2: 看 diff"
	if got.ReasoningContent != want {
		t.Errorf("ReasoningContent 应为 %q, got=%q", want, got.ReasoningContent)
	}
}

// TestAnthropicAssistantToChat_EmptyThinkingSkipped 锁定:空 thinking 不写入 ReasoningContent。
func TestAnthropicAssistantToChat_EmptyThinkingSkipped(t *testing.T) {
	msg := AnthropicMessage{
		Role: "assistant",
		Content: []AnthropicContent{
			{Type: "thinking", Thinking: ""},
			{Type: "thinking", Thinking: "   \n\t  "},
			{Type: "text", Text: "response"},
		},
	}
	got := anthropicAssistantToChat(msg)
	if got.ReasoningContent != "" {
		t.Errorf("空 thinking 应被跳过, ReasoningContent 应为空串, got=%q", got.ReasoningContent)
	}
	if got.Content != "response" {
		t.Errorf("Content 应为 %q, got=%q", "response", got.Content)
	}
}

// TestAnthropicAssistantToChat_ThinkingOnly 锁定:thinking-only 消息(content 无 text/tool_use)
// 应产出 content="" + reasoning_content=<thinking>,而非被填入"好的,我继续执行任务。"占位符。
//
// 这是 20260828 修复的核心回归:旧版遇到这种消息会走到 "content==\"\" && len(toolCalls)==0"
// 分支填入占位符,严重污染上游上下文(让模型误以为助手无缘无故补了一句无关文本,助长复读)。
func TestAnthropicAssistantToChat_ThinkingOnly(t *testing.T) {
	msg := AnthropicMessage{
		Role: "assistant",
		Content: []AnthropicContent{
			{Type: "thinking", Thinking: "纯思考回合,无文本输出"},
		},
	}
	got := anthropicAssistantToChat(msg)
	if got.ReasoningContent != "纯思考回合,无文本输出" {
		t.Errorf("ReasoningContent mismatch, got=%q", got.ReasoningContent)
	}
	if got.Content != "" {
		t.Errorf("Content 应为空串(thinking-only 消息),不应被占位符污染, got=%q", got.Content)
	}
	if strings.Contains(got.Content, "好的，我继续") {
		t.Errorf("Content 不应包含占位符「好的，我继续」, got=%q", got.Content)
	}
}

// TestAnthropicAssistantToChat_WithToolUseAndThinking 锁定:thinking/tool_use/text 三态共存。
func TestAnthropicAssistantToChat_WithToolUseAndThinking(t *testing.T) {
	msg := AnthropicMessage{
		Role: "assistant",
		Content: []AnthropicContent{
			{Type: "thinking", Thinking: "需要先查 git log"},
			{Type: "text", Text: "先查 git log"},
			{Type: "tool_use", ID: "call_1", Name: "Bash", Input: map[string]interface{}{"command": "git log"}},
		},
	}
	got := anthropicAssistantToChat(msg)
	if got.ReasoningContent != "需要先查 git log" {
		t.Errorf("ReasoningContent mismatch, got=%q", got.ReasoningContent)
	}
	if got.Content != "先查 git log" {
		t.Errorf("Content mismatch, got=%q", got.Content)
	}
	if len(got.ToolCalls) != 1 || got.ToolCalls[0].Function.Name != "Bash" {
		t.Errorf("ToolCalls 应有 1 个 Bash 调用, got=%+v", got.ToolCalls)
	}
}

// TestAnthropicAssistantToChat_NoThinking_ZeroRegression 锁定:无 thinking 的旧形态消息行为零回归。
// 重要:历史 Anthropic 客户端可能不带 thinking 块,本函数对它们应保持零变更。
func TestAnthropicAssistantToChat_NoThinking_ZeroRegression(t *testing.T) {
	// 纯文本
	msg := AnthropicMessage{
		Role:    "assistant",
		Content: []AnthropicContent{{Type: "text", Text: "回答"}},
	}
	got := anthropicAssistantToChat(msg)
	if got.ReasoningContent != "" {
		t.Errorf("无 thinking 时 ReasoningContent 应为空, got=%q", got.ReasoningContent)
	}
	if got.Content != "回答" {
		t.Errorf("Content mismatch, got=%q", got.Content)
	}

	// 空消息(兜底占位仍生效)
	emptyMsg := AnthropicMessage{Role: "assistant", Content: []AnthropicContent{}}
	got = anthropicAssistantToChat(emptyMsg)
	if got.ReasoningContent != "" {
		t.Errorf("无任何块时 ReasoningContent 应为空, got=%q", got.ReasoningContent)
	}
	if got.Content != "好的，我继续执行任务。" {
		t.Errorf("空消息兜底占位逻辑应保留, got=%q", got.Content)
	}
}

// TestAnthropicAssistantToChat_MarshalIncludesReasoningContent 锁定:序列化产物中 reasoning_content 字段外显。
// 这是给 OpenAI Chat 上游的关键协议字段:若 OpenAIChatRequest.MarshalJSON 漏出该字段,上游模型
// 拿到的 message 就只有 content,我们写的 ReasoningContent 接口形同虚设。
func TestAnthropicAssistantToChat_MarshalIncludesReasoningContent(t *testing.T) {
	msg := AnthropicMessage{
		Role: "assistant",
		Content: []AnthropicContent{
			{Type: "thinking", Thinking: "推理过程"},
			{Type: "text", Text: "回答"},
		},
	}
	got := anthropicAssistantToChat(msg)
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal 失败: %v", err)
	}
	// 断言序列化产物中含 "reasoning_content" 键
	if !strings.Contains(string(raw), `"reasoning_content"`) {
		t.Errorf("序列化产物应包含 reasoning_content 字段, got=%s", string(raw))
	}
	if !strings.Contains(string(raw), `"推理过程"`) {
		t.Errorf("序列化产物应包含 thinking 内容, got=%s", string(raw))
	}
	// 同时确认 content 字段存在(OpenAI 协议要求必填)
	if !strings.Contains(string(raw), `"content"`) {
		t.Errorf("序列化产物应包含 content 字段, got=%s", string(raw))
	}
}

// TestAnthropicToOpenAIChat_PreservesThinkingInAssistantHistory 端到端集成测试:
// 含 thinking 块的 assistant 历史消息经 AnthropicToOpenAIChat 翻译后,
// OpenAIChatRequest.Messages 中的对应项 ReasoningContent 应为 thinking 合并值。
func TestAnthropicToOpenAIChat_PreservesThinkingInAssistantHistory(t *testing.T) {
	req := &AnthropicRequest{
		Model: "kimi-k2",
		Messages: []AnthropicMessage{
			{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "给 hello world"}}},
			{
				Role: "assistant",
				Content: []AnthropicContent{
					{Type: "thinking", Thinking: "用户要 hello world"},
					{Type: "text", Text: "print('hello world')"},
				},
			},
			{Role: "user", Content: []AnthropicContent{{Type: "text", Text: "改成 go"}}},
		},
	}
	out, err := AnthropicToOpenAIChat(req)
	if err != nil {
		t.Fatalf("AnthropicToOpenAIChat 失败: %v", err)
	}
	if len(out.Messages) != 3 {
		t.Fatalf("期望 3 条消息, got=%d", len(out.Messages))
	}
	asst := out.Messages[1]
	if asst.ReasoningContent != "用户要 hello world" {
		t.Errorf("assistant 历史 ReasoningContent mismatch, got=%q", asst.ReasoningContent)
	}
	if asst.Content != "print('hello world')" {
		t.Errorf("assistant 历史 Content mismatch, got=%q", asst.Content)
	}
}
