package relay

import (
	"testing"
)

// TestNormalizePartOrderForClaudeVertex 锁定 Anthropic 典范顺序规整纯函数:
//   - model 消息必须规整为 [Text..., FunctionCall...](tool_use 在助手回合末尾)
//   - user 消息必须规整为 [FunctionResponse..., Text/InlineData...](tool_result 排在 text 前)
// 各分区保持原相对顺序;不动 id/不删 part;非 model/user 与 ≤1 part 的消息原样深拷贝。
//
// 背景: Codex → Vertex Anthropic(daily-cloudcode-pa 重译)线上 400:
//   "tool_use ids were found without tool_result blocks immediately after: call_..."
// 根因是 mergeConsecutiveRoles 把 [FC] + [T] 合并成 model[FC, T], Text 失序导致 tool_use 不再处于
// 助手回合末尾。本规整在合并之后把 parts 拉回典范顺序,消除紧邻违例。
func TestNormalizePartOrderForClaudeVertex(t *testing.T) {
	mkText := func(s string) GeminiPart { return GeminiPart{Text: s} }
	mkFC := func(name, id string) GeminiPart {
		return GeminiPart{FunctionCall: &GeminiFunctionCall{Name: name, Args: map[string]interface{}{}, ID: id}}
	}
	mkFR := func(name, id string) GeminiPart {
		return GeminiPart{FunctionResponse: &GeminiFunctionResponse{Name: name, ID: id, Response: map[string]interface{}{"result": "ok"}}}
	}
	mkImg := func() GeminiPart { return GeminiPart{InlineData: &GeminiBlob{MimeType: "image/png", Data: "x"}} }

	cases := []struct {
		name   string
		input  []GeminiContent
		expect []GeminiContent
		desc   string
	}{
		{
			name: "model_fc_then_text_normalized",
			desc: "Codex interleaved_text 场景: model[FC,T] 规整为 model[T,FC],tool_use 末尾化",
			input:  []GeminiContent{{Role: "model", Parts: []GeminiPart{mkFC("shell_command", "call_a_1"), mkText("我来执行一下")}}},
			expect: []GeminiContent{{Role: "model", Parts: []GeminiPart{mkText("我来执行一下"), mkFC("shell_command", "call_a_1")}}},
		},
		{
			name: "model_text_then_fc_already_canonical_noop",
			desc: "已是典范 model[T,FC]: 顺序不动",
			input:  []GeminiContent{{Role: "model", Parts: []GeminiPart{mkText("let me check"), mkFC("shell_command", "call_a_1")}}},
			expect: []GeminiContent{{Role: "model", Parts: []GeminiPart{mkText("let me check"), mkFC("shell_command", "call_a_1")}}},
		},
		{
			name: "model_multi_text_multi_fc_stable_within_partition",
			desc: "model[T2,FC2,T1,FC1] → [T2,T1,FC2,FC1]: 各分区保持原相对顺序(稳定分区)",
			input:  []GeminiContent{{Role: "model", Parts: []GeminiPart{mkText("T2"), mkFC("fn", "id2"), mkText("T1"), mkFC("fn", "id1")}}},
			expect: []GeminiContent{{Role: "model", Parts: []GeminiPart{mkText("T2"), mkText("T1"), mkFC("fn", "id2"), mkFC("fn", "id1")}}},
		},
		{
			name: "user_text_then_fr_normalized",
			desc: "user[T,FR] 规整为 user[FR,T],tool_result 前移",
			input:  []GeminiContent{{Role: "user", Parts: []GeminiPart{mkText("继续分析"), mkFR("shell_command", "call_a_1")}}},
			expect: []GeminiContent{{Role: "user", Parts: []GeminiPart{mkFR("shell_command", "call_a_1"), mkText("继续分析")}}},
		},
		{
			name: "user_fr_then_text_already_canonical_noop",
			desc: "已是典范 user[FR,T]: 顺序不动",
			input:  []GeminiContent{{Role: "user", Parts: []GeminiPart{mkFR("shell_command", "call_a_1"), mkText("看完")}}},
			expect: []GeminiContent{{Role: "user", Parts: []GeminiPart{mkFR("shell_command", "call_a_1"), mkText("看完")}}},
		},
		{
			name: "user_text_image_then_fr_normalized_keeps_image_after",
			desc: "user[T,Img,FR] 规整为 user[FR,T,Img],图片作为 textLike 跟在 FR 之后",
			input:  []GeminiContent{{Role: "user", Parts: []GeminiPart{mkText("看图"), mkImg(), mkFR("shell_command", "call_a_1")}}},
			expect: []GeminiContent{{Role: "user", Parts: []GeminiPart{mkFR("shell_command", "call_a_1"), mkText("看图"), mkImg()}}},
		},
		{
			name: "single_part_noop_model",
			desc: "单 part 的 model 消息无需分区,原样",
			input:  []GeminiContent{{Role: "model", Parts: []GeminiPart{mkFC("shell_command", "call_a_1")}}},
			expect: []GeminiContent{{Role: "model", Parts: []GeminiPart{mkFC("shell_command", "call_a_1")}}},
		},
		{
			name: "empty_parts_noop",
			desc: "空 parts 原样返回 nil",
			input:  []GeminiContent{{Role: "user", Parts: nil}},
			expect: []GeminiContent{{Role: "user", Parts: nil}},
		},
		{
			name: "non_target_role_noop",
			desc: "system 等非 model/user 角色原样深拷贝(不规整)",
			input:  []GeminiContent{{Role: "system", Parts: []GeminiPart{mkFC("fn", "id1"), mkText("说明")}}},
			expect: []GeminiContent{{Role: "system", Parts: []GeminiPart{mkFC("fn", "id1"), mkText("说明")}}},
		},
		{
			name: "empty_slice",
			desc: "空切片原样返回",
			input:  []GeminiContent{},
			expect: []GeminiContent{},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := NormalizePartOrderForClaudeVertex(c.input)
			if len(got) != len(c.expect) {
				t.Fatalf("len = %d, want %d | %s", len(got), len(c.expect), c.desc)
			}
			for i := range got {
				if !geminiContentEqual(got[i], c.expect[i]) {
					t.Errorf("[%d] mismatch | %s\n got=%s\nwant=%s", i, c.desc, describeContent(got[i]), describeContent(c.expect[i]))
				}
			}
		})
	}
}

// TestNormalizePartOrderForClaudeVertex_InputUntouched 校验规整不回流修改入参(原数组保持原序),
// 防御调用方对原 []GeminiContent 的别名依赖被破坏。
func TestNormalizePartOrderForClaudeVertex_InputUntouched(t *testing.T) {
	mkText := func(s string) GeminiPart { return GeminiPart{Text: s} }
	mkFC := func(name, id string) GeminiPart {
		return GeminiPart{FunctionCall: &GeminiFunctionCall{Name: name, ID: id}}
	}
	mkFR := func(name, id string) GeminiPart {
		return GeminiPart{FunctionResponse: &GeminiFunctionResponse{Name: name, ID: id}}
	}

	origIn := []GeminiContent{
		{Role: "model", Parts: []GeminiPart{mkFC("shell_command", "call_a_1"), mkText("我来执行一下")}},
		{Role: "user", Parts: []GeminiPart{mkText("继续"), mkFR("shell_command", "call_a_1")}},
	}
	backup := make([]GeminiContent, len(origIn))
	for i, c := range origIn {
		backup[i] = GeminiContent{Role: c.Role, Parts: make([]GeminiPart, len(c.Parts))}
		copy(backup[i].Parts, c.Parts)
	}

	_ = NormalizePartOrderForClaudeVertex(origIn)

	for i := range origIn {
		if len(origIn[i].Parts) != len(backup[i].Parts) {
			t.Fatalf("入参 [%d] parts 长度被改动 | %d -> %d", i, len(backup[i].Parts), len(origIn[i].Parts))
		}
		for j := range origIn[i].Parts {
			if !geminiPartEqual(origIn[i].Parts[j], backup[i].Parts[j]) {
				t.Errorf("入参 [%d].parts[%d] 被改动: got=%s want=%s",
					i, j, describePart(origIn[i].Parts[j]), describePart(backup[i].Parts[j]))
			}
		}
	}
}

// ===== 比较与描述辅助 =====
// 不用 reflect.DeepEqual 以回避指针字段 nil vs 空结构体的歧义,手写针对性比较。

func geminiContentEqual(a, b GeminiContent) bool {
	if !eqStr(a.Role, b.Role) {
		return false
	}
	if len(a.Parts) != len(b.Parts) {
		return false
	}
	for i := range a.Parts {
		if !geminiPartEqual(a.Parts[i], b.Parts[i]) {
			return false
		}
	}
	return true
}

func geminiPartEqual(a, b GeminiPart) bool {
	if !eqStr(a.Text, b.Text) {
		return false
	}
	if (a.Thought) != (b.Thought) {
		return false
	}
	if !eqStr(a.ThoughtSignature, b.ThoughtSignature) {
		return false
	}
	if (a.InlineData == nil) != (b.InlineData == nil) {
		return false
	}
	if a.InlineData != nil && b.InlineData != nil {
		if !eqStr(a.InlineData.MimeType, b.InlineData.MimeType) || !eqStr(a.InlineData.Data, b.InlineData.Data) {
			return false
		}
	}
	if (a.FunctionCall == nil) != (b.FunctionCall == nil) {
		return false
	}
	if a.FunctionCall != nil && b.FunctionCall != nil {
		if !eqStr(a.FunctionCall.Name, b.FunctionCall.Name) || !eqStr(a.FunctionCall.ID, b.FunctionCall.ID) {
			return false
		}
	}
	if (a.FunctionResponse == nil) != (b.FunctionResponse == nil) {
		return false
	}
	if a.FunctionResponse != nil && b.FunctionResponse != nil {
		if !eqStr(a.FunctionResponse.Name, b.FunctionResponse.Name) || !eqStr(a.FunctionResponse.ID, b.FunctionResponse.ID) {
			return false
		}
	}
	return true
}

func eqStr(a, b string) bool { return a == b }

func describeContent(c GeminiContent) string {
	s := "{" + c.Role + ": ["
	for i, p := range c.Parts {
		if i > 0 {
			s += ", "
		}
		s += describePart(p)
	}
	return s + "]}"
}

func describePart(p GeminiPart) string {
	switch {
	case p.FunctionCall != nil:
		return "FC(name=" + p.FunctionCall.Name + ",id=" + p.FunctionCall.ID + ")"
	case p.FunctionResponse != nil:
		return "FR(name=" + p.FunctionResponse.Name + ",id=" + p.FunctionResponse.ID + ")"
	case p.Text != "":
		return "T(" + p.Text + ")"
	case p.InlineData != nil:
		return "IMG(" + p.InlineData.MimeType + ")"
	default:
		return "?"
	}
}
