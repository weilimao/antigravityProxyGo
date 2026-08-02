package relay

import "strings"

// NormalizePartOrderForClaudeVertex 把单条 GeminiContent 的 parts 规整为 Anthropic 典范顺序,
// 仅作用于 model / user 角色消息,确保被 daily-cloudcode-pa 重译为 Vertex Anthropic messages 时
// 不触发 "tool_use ids were found without tool_result blocks immediately after" 400。
//
// 背景(线上 400 实测根因):
//   Codex /v1/responses 把 assistant 文本 message 插在 function_call 与 function_call_output 之间时,
//   parseResponsesInput 先 flush 出只含 ToolCalls 的 assistant(FC)、再发只含 text 的 assistant(T),
//   两条连续 model 消息被 mergeConsecutiveRoles 合并成 model[FC, T] —— Text 排到了 functionCall 之后。
//   daily-cloudcode-pa 重译为 Vertex Anthropic 时,tool_use 不再位于助手回合末尾,其「紧接的下一条消息」
//   不再是只含 tool_result 的 user 消息,触发 tool_use/tool_result 紧邻 400。
//
// Anthropic 官方约束(https://platform.claude.com/docs/en/agents-and-tools/tool-use/handle-tool-calls):
//   - Tool result blocks must immediately follow their corresponding tool use blocks in the message history.
//     工具结果必须紧随其工具调用,中间不得插入任何消息。
//   - assistant 回合的典范形态:[text, ..., tool_use](text 在前,tool_use 在末尾)。
//   - user 回合承载 tool_result 时:tool_result 必须排在 content 数组前面,text 排在所有 tool_result 之后。
//
// 本函数职责(纯函数,不动 id/不删 part/不跨消息移动):
//   - model 消息:稳定分区为 [Text..., FunctionCall...](各分区保持原相对顺序)。
//     → Anthropic 典范:tool_use 处于助手回合末尾,下一条 user 即其 tool_result,紧邻成立。
//   - user 消息:稳定分区为 [FunctionResponse..., Text/InlineData...](各分区保持原相对顺序)。
//     → Anthropic 典范:tool_result 排在 user 回合前面,任何 text 都紧跟其后,不破坏"先 tool_result 后 text"。
//   - 其他角色(system 等)与空 parts 原样返回(深拷贝防止复用底层数组产生别名)。
//
// 稳定性保证:用 append 顺序遍历两档收集,天然保留原相对顺序;平行工具调用多个 FC 的相对顺序不变,
// 与各自 tool_result 的 id 配对不受影响。
//
// 在 TranslateOpenAIToGemini / TranslateAnthropicToGemini 的 mergeConsecutiveRoles 之后调用:
// 合并才会把 [FC] + [T] 拼成 [FC, T],合并后再规整才能命中目标顺序。
func NormalizePartOrderForClaudeVertex(in []GeminiContent) []GeminiContent {
	if len(in) == 0 {
		return in
	}
	out := make([]GeminiContent, len(in))
	for i, c := range in {
		role := strings.ToLower(c.Role)
		if role != "model" && role != "user" {
			// 非目标角色:深拷贝 parts 以避免调用方共享底层数组的别名风险。
			out[i] = GeminiContent{Role: c.Role, Parts: copyParts(c.Parts)}
			continue
		}
		if len(c.Parts) <= 1 {
			out[i] = GeminiContent{Role: c.Role, Parts: copyParts(c.Parts)}
			continue
		}
		outParts := make([]GeminiPart, 0, len(c.Parts))

		if role == "model" {
			// model 典范:[Text..., FunctionCall...]。先收 text(含纯 Text 与 ThoughtSignature/Thought
			// 等挂在独立 part 上的思维块),再收 functionCall,其余兜底按原序保留在对应分区之后。
			var textLike, fcLike, other []GeminiPart
			for _, p := range c.Parts {
				switch {
				case p.FunctionCall != nil:
					fcLike = append(fcLike, p)
				case p.Text != "" || p.Thought:
					textLike = append(textLike, p)
				default:
					other = append(other, p)
				}
			}
			outParts = append(outParts, textLike...)
			outParts = append(outParts, fcLike...)
			outParts = append(outParts, other...)
		} else { // role == "user"
			// user 典范:[FunctionResponse..., Text/InlineData...]。先收 functionResponse,
			// 再收 text/inlineData,其余兜底按原序保留在对应分区之后。
			var frLike, textLike, other []GeminiPart
			for _, p := range c.Parts {
				switch {
				case p.FunctionResponse != nil:
					frLike = append(frLike, p)
				case p.Text != "" || p.InlineData != nil:
					textLike = append(textLike, p)
				default:
					other = append(other, p)
				}
			}
			outParts = append(outParts, frLike...)
			outParts = append(outParts, textLike...)
			outParts = append(outParts, other...)
		}

		out[i] = GeminiContent{Role: c.Role, Parts: outParts}
	}
	return out
}

// copyParts 复制 GeminiPart 切片(浅拷贝 part 值,不动其指针字段指向的结构体),
// 确保调用方对 out[i].Parts 的 append/重排不会回流到入参 c.Parts 的底层数组。
func copyParts(parts []GeminiPart) []GeminiPart {
	if len(parts) == 0 {
		return nil
	}
	cp := make([]GeminiPart, len(parts))
	copy(cp, parts)
	return cp
}
