package relay

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestResponsesToolAdjacency_CodexRepro 锁定 Codex /v1/responses 走 antigravity 号池 claude 路径的
// tool_use / tool_result 邻接问题根因(线上 400:
//   messages.2: `tool_use` ids were found without `tool_result` blocks immediately after:
//   call_1785597050575167700_144
// )。
//
// 线上日志角色序列: [user[TTTT] model[FC(shell_command)T] user[FR(shell_command)]] (3 条)。
// messages[1]=model 含 FunctionCall + Text;messages[2]=user 含 FunctionResponse。
// Vertex Anthropic 重译侧要求每个 tool_use 在「紧接着的下一条消息」里有同 id 的 tool_result。
//
// 本测试复刻 parseResponsesInput → TranslateOpenAIToGemini → mergeConsecutiveRoles 全链,
// 打印最终 Gemini Contents 结构(id / parts 顺序 / 角色序列),定位是否存在:
//   (a) FunctionCall.ID ≠ FunctionResponse.ID 的 id 失配;
//   (b) mergeConsecutiveRoles 把 user[FR] 合入前一条 user 破坏紧邻;
//   (c) 占位 model "OK." 插在 FR 之后导致 model[FC] 与 user[FR] 不再紧邻。
func TestResponsesToolAdjacency_CodexRepro(t *testing.T) {
	SetGlobalEnableThinkingMode(true)
	defer SetGlobalEnableThinkingMode(true)

	// Codex /v1/responses input 结构(对齐线上日志):
	//   message(user, "TTTT")
	//   function_call(call_id, shell_command, args...)
	//   function_call_output(call_id, output)
	//   message(user, "看完报告") 若有后续 user prompt 则再跟一条
	// 线上当轮 tool 调用日志的 mdl 是 model[FC(shell_command)T],说明 assistant 消息里
	// 同时含 FC 与 T(text)。Codex Response 输入不会直接发 assistant 文本 message,
	// 故 model 的 Text 来自哪一段需在本链路里被推导确认。
	cmdJSON := `{
		"model": "claude-opus-4-6-thinking",
		"stream": true,
		"input": [
			{"type":"message","role":"user","content":"TTTT"},
			{"type":"function_call","call_id":"call_1785597050575167700_144","name":"shell_command","arguments":"{\"command\":\"ls\"}"},
			{"type":"function_call_output","call_id":"call_1785597050575167700_144","output":"file1\nfile2"}
		]
	}`

	openReq, err := ParseUnifiedOpenAIRequest([]byte(cmdJSON))
	if err != nil {
		t.Fatalf("ParseUnifiedOpenAIRequest: %v", err)
	}

	// 1. 先看 parseResponsesInput 产出的 OpenAIMessage 序列(角色 / ToolCalls / ToolCallID)。
	t.Logf("parseResponsesInput 产出 %d 条 OpenAIMessage:", len(openReq.Messages))
	for i, m := range openReq.Messages {
		ids := ""
		if m.ToolCallID != "" {
			ids = " toolCallID=" + m.ToolCallID
		}
		tcIDs := ""
		for _, tc := range m.ToolCalls {
			tcIDs += " tc.id=" + tc.ID + " tc.name=" + tc.Name
		}
		t.Logf("  [%d] role=%s content=%q%s%s", i, m.Role, m.Content, ids, tcIDs)
	}

	gem := TranslateOpenAIToGemini(openReq)

	// 2. 看最终 Gemini Contents(mergeConsecutiveRoles 已在 TranslateOpenAIToGemini 末尾调用)。
	t.Logf("TranslateOpenAIToGemini 产出 %d 条 GeminiContent:", len(gem.Contents))
	for i, c := range gem.Contents {
		var desc []string
		for _, p := range c.Parts {
			switch {
			case p.FunctionCall != nil:
				desc = append(desc, "FC(name="+p.FunctionCall.Name+",id="+p.FunctionCall.ID+")")
			case p.FunctionResponse != nil:
				desc = append(desc, "FR(name="+p.FunctionResponse.Name+",id="+p.FunctionResponse.ID+")")
			case p.Text != "":
				desc = append(desc, "T("+truncLog(p.Text, 24)+")")
			}
		}
		t.Logf("  [%d] role=%s parts=[%s]", i, c.Role, joinLog(desc, " "))
	}

	// 3. 关键不变式:每个 FunctionCall 的 id 必须在「紧接的下一条消息」里有同 id 的 FunctionResponse。
	// 这是 Vertex Anthropic(daily-cloudcode-pa 重译)的硬性约束。本断言锁定该不变式。
	if err := assertToolAdiacency(gem.Contents); err != nil {
		t.Fatalf("邻接不变式违反: %v", err)
	}
	t.Logf("✓ 邻接不变式通过:每个 tool_use 都有紧邻同 id 的 tool_result")

	// 4. 构造「Codex 把 assistant 文本插在 function_call 与 output 之间」的失序场景,
	// 在 TranslateOpenAIToGemini 末尾 NormalizePartOrderForClaudeVertex 介入之后,
	// model 必须把 Text 排在 FunctionCall 之前(Anthropic 典范),证明修复在线上路径命中。
	interleavedJSON := `{
		"model":"claude-opus-4-6-thinking","stream":true,
		"input":[
			{"type":"message","role":"user","content":"TTTT"},
			{"type":"function_call","call_id":"call_z_1","name":"shell_command","arguments":"{\"command\":\"ls\"}"},
			{"type":"message","role":"assistant","content":"我来执行一下"},
			{"type":"function_call_output","call_id":"call_z_1","output":"file1"}
		]
	}`
	ir, err := ParseUnifiedOpenAIRequest([]byte(interleavedJSON))
	if err != nil {
		t.Fatalf("ParseUnifiedOpenAIRequest interleaved: %v", err)
	}
	igem := TranslateOpenAIToGemini(ir)
	// 找到含 call_z_1 的 model 消息,断言其 parts 顺序为 [Text, FunctionCall]。
	for i, c := range igem.Contents {
		if c.Role != "model" {
			continue
		}
		var fcIdx, textIdx, hasFC, hasText = -1, -1, false, false
		for j, p := range c.Parts {
			if p.FunctionCall != nil && p.FunctionCall.ID == "call_z_1" {
				fcIdx, hasFC = j, true
			}
			if p.Text != "" {
				textIdx, hasText = j, true
			}
		}
		if !hasFC || !hasText {
			t.Fatalf("interleaved 场景: model 消息[%d] 未同时含 FC(call_z_1) 与 Text", i)
		}
		if textIdx > fcIdx {
			t.Fatalf("interleaved 场景修复失败: model[%d] Text(idx=%d) 排在 FunctionCall(idx=%d) 之后,应反之 | got parts=%s",
				i, textIdx, fcIdx, describeContentForLog(c))
		}
		t.Logf("✓ interleaved 场景: model[%d] parts=[Text(idx=%d), FunctionCall(idx=%d)] —— tool_use 已末尾化", i, textIdx, fcIdx)
		break
	}

	// 5. 旁证:若对「未经 NormalizePartOrderForClaudeVertex」的 mergeConsecutiveRoles 产物直接断言,
	// 该 interleaved 场景本应是 model[FC, T](Text 在后)从而违反典范顺序。这里手工模拟"不规整"路径,
	// 用反证确认 NormalizePartOrderForClaudeVertex 的介入不可或缺。
	rawWithoutNorm := mergeConsecutiveRoles(interleaveRawOpenAIMessages(ir))
	for i, c := range rawWithoutNorm {
		if c.Role != "model" {
			continue
		}
		fcIdx, textIdx := -1, -1
		for j, p := range c.Parts {
			if p.FunctionCall != nil {
				fcIdx = j
			}
			if p.Text != "" {
				textIdx = j
			}
		}
		if fcIdx >= 0 && textIdx >= 0 && fcIdx < textIdx {
			t.Logf("✓ 反证成立: 未规整时 model[%d] parts=[FunctionCall(idx=%d), Text(idx=%d)] 即 model[FC, T] 失序,会被 daily-cloudcode-pa 判 400", i, fcIdx, textIdx)
		}
		break
	}
}

// interleaveRawOpenAIMessages 从已解析 OpenAIRequest 取出 Messages 字段,供手工复跑"未经规整"的链路。
// 与 TranslateOpenAIToGemini 内部先 parseResponsesInput→slice 再 mergeConsecutiveRoles 的中间态对齐。
func interleaveRawOpenAIMessages(r *OpenAIRequest) []GeminiContent {
	// 复刻 TranslateOpenAIToGemini 中 user/assistant/tool 三个分支对 r.Messages 的转换产出,
	// 但跳过末尾的 NormalizePartOrderForClaudeVertex,只跑 mergeConsecutiveRoles,便于反证。
	out := make([]GeminiContent, 0, len(r.Messages))
	for _, msg := range r.Messages {
		switch strings.ToLower(msg.Role) {
		case "assistant":
			parts := GeminiPart_fromOpenAIContent(msg)
			for _, tc := range msg.ToolCalls {
				args := parseToolCallArgs(tc.Arguments)
				parts = append(parts, GeminiPart{
					FunctionCall:     &GeminiFunctionCall{Name: tc.Name, Args: args, ID: tc.ID},
					ThoughtSignature: "skip_thought_signature_validator",
				})
			}
			if len(parts) > 0 {
				out = append(out, GeminiContent{Role: "model", Parts: parts})
			}
		case "tool":
			toolName := findOpenAIToolNameByID(r.Messages, msg.ToolCallID)
			out = append(out, GeminiContent{Role: "user", Parts: []GeminiPart{{
				FunctionResponse: &GeminiFunctionResponse{
					Name:     toolName,
					Response: map[string]interface{}{"result": msg.Content},
					ID:       msg.ToolCallID,
				},
			}}})
		case "user":
			cp := GeminiPart_fromOpenAIContent(msg)
			if len(cp) > 0 {
				out = append(out, GeminiContent{Role: "user", Parts: cp})
			}
		}
	}
	return out
}

// GeminiPart_fromOpenAIContent 把 OpenAIMessage.Content 字符串解析为 Gemini text part 切片,
// 复刻 TranslateOpenAIToGemini 中对 assistant/user 纯文本分支的处理(parseOpenAIContentString)。
func GeminiPart_fromOpenAIContent(msg OpenAIMessage) []GeminiPart {
	if msg.Content == "" {
		return nil
	}
	return []GeminiPart{{Text: msg.Content}}
}

// describeContentForLog 用于失败信息中打印 parts。
func describeContentForLog(c GeminiContent) string {
	var parts []string
	for _, p := range c.Parts {
		switch {
		case p.FunctionCall != nil:
			parts = append(parts, "FC(id="+p.FunctionCall.ID+")")
		case p.FunctionResponse != nil:
			parts = append(parts, "FR(id="+p.FunctionResponse.ID+")")
		case p.Text != "":
			parts = append(parts, "T("+truncLog(p.Text, 24)+")")
		}
	}
	joined := ""
	for i, p := range parts {
		if i > 0 {
			joined += ", "
		}
		joined += p
	}
	return "{" + c.Role + ": [" + joined + "]}"
}

// TestResponsesToolAdjacency_CodexMultiTurnRepro 复刻「回答了一会就报错」场景:
// 多轮 tool 调用历史累加,Codex 把历轮 function_call / function_call_output 顺序回传。
// 触发 400 的 messages[2] 正是第 3 条(0 基下标 2),与线上日志「messages.2」对齐。
//
// 关键变量:本轮与上一轮是否各含 FC/FR,FR 之间是否会被 mergeConsecutiveRoles
// 合并,以及 Text 与 FC 在同一 model 消息里的相对顺序。
func TestResponsesToolAdjacency_CodexMultiTurnRepro(t *testing.T) {
	SetGlobalEnableThinkingMode(true)
	defer SetGlobalEnableThinkingMode(true)

	cases := []struct {
		name string
		json string
		desc string
	}{
		{
			name: "single_turn",
			desc: "单轮 tool 调用(对照基线,应通过)",
			json: `{
				"model":"claude-opus-4-6-thinking","stream":true,
				"input":[
					{"type":"message","role":"user","content":"TTTT"},
					{"type":"function_call","call_id":"call_a_1","name":"shell_command","arguments":"{\"command\":\"ls\"}"},
					{"type":"function_call_output","call_id":"call_a_1","output":"file1"}
				]
			}`,
		},
		{
			name: "two_turns",
			desc: "两轮 tool 调用:第 1 轮 call_a_1 已有 FR,第 2 轮 call_b_1 的 FC 后紧跟其 FR",
			json: `{
				"model":"claude-opus-4-6-thinking","stream":true,
				"input":[
					{"type":"message","role":"user","content":"TTTT"},
					{"type":"function_call","call_id":"call_a_1","name":"shell_command","arguments":"{\"command\":\"ls\"}"},
					{"type":"function_call_output","call_id":"call_a_1","output":"file1"},
					{"type":"message","role":"user","content":"继续分析"},
					{"type":"function_call","call_id":"call_b_1","name":"shell_command","arguments":"{\"command\":\"pwd\"}"},
					{"type":"function_call_output","call_id":"call_b_1","output":"/tmp"}
				]
			}`,
		},
		{
			name: "two_turns_interleaved_text",
			desc: "两轮 + Codex 把 assistant 文本 message 插在 function_call 与 output 之间",
			json: `{
				"model":"claude-opus-4-6-thinking","stream":true,
				"input":[
					{"type":"message","role":"user","content":"TTTT"},
					{"type":"function_call","call_id":"call_a_1","name":"shell_command","arguments":"{\"command\":\"ls\"}"},
					{"type":"message","role":"assistant","content":"我来执行一下"},
					{"type":"function_call_output","call_id":"call_a_1","output":"file1"}
				]
			}`,
		},
		{
			name: "two_turns_text_before_fc",
			desc: "Codex 在 function_call 前发 assistant 文本",
			json: `{
				"model":"claude-opus-4-6-thinking","stream":true,
				"input":[
					{"type":"message","role":"user","content":"TTTT"},
					{"type":"message","role":"assistant","content":"let me check"},
					{"type":"function_call","call_id":"call_a_1","name":"shell_command","arguments":"{\"command\":\"ls\"}"},
					{"type":"function_call_output","call_id":"call_a_1","output":"file1"}
				]
			}`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			openReq, err := ParseUnifiedOpenAIRequest([]byte(c.json))
			if err != nil {
				t.Fatalf("%s: ParseUnifiedOpenAIRequest: %v | %s", c.name, err, c.desc)
			}
			gem := TranslateOpenAIToGemini(openReq)

			t.Logf("[%s] %s", c.name, c.desc)
			t.Logf("  最终 %d 条 GeminiContent:", len(gem.Contents))
			for i, ct := range gem.Contents {
				var desc []string
				for _, p := range ct.Parts {
					switch {
					case p.FunctionCall != nil:
						desc = append(desc, "FC(name="+p.FunctionCall.Name+",id="+p.FunctionCall.ID+")")
					case p.FunctionResponse != nil:
						desc = append(desc, "FR(name="+p.FunctionResponse.Name+",id="+p.FunctionResponse.ID+")")
					case p.Text != "":
						desc = append(desc, "T("+truncLog(p.Text, 24)+")")
					}
				}
				t.Logf("    [%d] role=%s parts=[%s]", i, ct.Role, joinLog(desc, " "))
			}

			// 邻接不变式:每个 tool_use 必须在紧接的下一条消息里有同 id 的 tool_result。
			if err := assertToolAdiacency(gem.Contents); err != nil {
				t.Errorf("[%s] 邻接不变式违反: %v | %s", c.name, err, c.desc)
			} else {
				t.Logf("[%s] ✓ 邻接通过", c.name)
			}

			// Anthropic 典范顺序不变式(防 daily-cloudcode-pa 重译踩 400):
			//   - model 消息中 FunctionCall 必须排在所有 Text 之后(tool_use 处于助手回合末尾);
			//   - user 消息中 FunctionResponse 必须排在所有 Text/InlineData 之前(tool_result 前置)。
			if bad := assertAnthropicCanonicalPartOrder(gem.Contents); bad != "" {
				t.Errorf("[%s] 典范顺序违反: %s | %s", c.name, bad, c.desc)
			} else {
				t.Logf("[%s] ✓ 典范顺序通过", c.name)
			}
		})
	}
}

// assertAnthropicCanonicalPartOrder 校验最终 Gemini Contents 满足 Anthropic 在
// 单条消息内的典范顺序(model:[text..., FC...] / user:[FR..., text/inlineData...])。
// 返回空串表示通过;非空串描述首个违例位置。仅检查 model/user 角色。
func assertAnthropicCanonicalPartOrder(contents []GeminiContent) string {
	for i, c := range contents {
		role := c.Role
		if role != "model" && role != "user" {
			continue
		}
		seenFC := false
		for _, p := range c.Parts {
			if p.FunctionCall != nil {
				seenFC = true
				continue
			}
			if seenFC && (p.Text != "" || p.Thought) && role == "model" {
				return "model message[" + itoaIdx(i) + "] 含 Text/Thought 出现在 FunctionCall 之后(tool_use 应末尾化)"
			}
		}
		seenFR := false
		for _, p := range c.Parts {
			if p.FunctionResponse != nil {
				seenFR = true
				continue
			}
			if !seenFR && (p.Text != "" || p.InlineData != nil) && role == "user" {
				// user 在含 FR 的情况下,Text/InlineData 应排在 FR 之后;但若本条 user 没有 FR,
				// 则不约束(纯文本 user 允许)。所以这里只在「同时含 FR」时判违例。
				if messageHasFR(c) {
					return "user message[" + itoaIdx(i) + "] 含 Text/InlineData 出现在 FunctionResponse 之前(tool_result 应前置)"
				}
			}
		}
	}
	return ""
}

// messageHasFR 判断单条 GeminiContent 是否含 FunctionResponse part。
func messageHasFR(c GeminiContent) bool {
	for _, p := range c.Parts {
		if p.FunctionResponse != nil {
			return true
		}
	}
	return false
}

// itoaIdx 简单整数转字符串,避免引入 strconv(本测试文件保持零额外依赖)。
func itoaIdx(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [12]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// truncLog 截断日志字符串便于打印。
func truncLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// joinLog 拼接日志片段。
func joinLog(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}

// assertToolAdiacency 校验 Gemini Contents 中每个 FunctionCall 的 id,
// 在紧接着的下一条消息里存在同 id 的 FunctionResponse。
// 注意:Anthropic 的紧邻约束是「下一条 message」里要有对应 tool_result,
// 因此这里跨消息边界(Contents[i] 的 FC → Contents[i+1] 中找 FR),
// 而不是在同一 message 内匹配。
func assertToolAdiacency(contents []GeminiContent) error {
	// 收集每条消息里出现的 FunctionCall id
	type fcRef struct {
		idx int
		id  string
	}
	var fcList []fcRef
	for i, c := range contents {
		for _, p := range c.Parts {
			if p.FunctionCall != nil {
				fcList = append(fcList, fcRef{i, p.FunctionCall.ID})
			}
		}
	}
	for _, fc := range fcList {
		// 必须在「紧接的下一条消息」(contents[fc.idx+1])里有同 id 的 FR。
		if fc.idx+1 >= len(contents) {
			return &adjacencyError{fcID: fc.id, reason: "tool_use 之后没有紧邻的下一条消息"}
		}
		next := contents[fc.idx+1]
		found := false
		for _, p := range next.Parts {
			if p.FunctionResponse != nil && p.FunctionResponse.ID == fc.id {
				found = true
				break
			}
		}
		if !found {
			return &adjacencyError{fcID: fc.id, reason: "下一条消息里没有同 id 的 tool_result"}
		}
	}
	return nil
}

type adjacencyError struct {
	fcID   string
	reason string
}

func (e *adjacencyError) Error() string {
	b, _ := json.Marshal(map[string]string{"fc_id": e.fcID, "reason": e.reason})
	return string(b)
}
