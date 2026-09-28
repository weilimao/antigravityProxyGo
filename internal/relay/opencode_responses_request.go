package relay

import (
	"encoding/json"
	"strings"
)

// opencode_responses_request.go: OpenAI Chat 请求体 → Responses API 请求体转换。
//
// 设计动机:
//   OpenCode Zen 上部分模型(如 muse-spark-*)仅支持 Responses API。当客户端以
//   Chat 格式请求这类模型时, 中继需把入站 Chat body 转成 Responses 形态再发往
//   /responses, 否则上游报 "unknown parameter `max_tokens`" 之类的字段错误。
//
// 字段映射(实测 Zen /responses 接受的字段集):
//   Chat                       → Responses
//   ---------------------------------------------------------------
//   messages[{role,content}]   → input[{role,content}]
//   messages[role=system]      → instructions (字符串, 多条以换行拼接)
//   max_tokens                 → max_output_tokens
//   tools[{type,function{...}}] → tools[{type:"function",name,description,parameters}]
//   tool_choice                → tool_choice (字符串形态一致)
//   stream                     → stream
//   temperature/top_p          → 同名保留
//
// 不支持映射的 Chat 专有字段(如 stream_options / frequency_penalty 等)直接丢弃,
// 避免上游因未知参数报错。
//
// 返回值: (转换后 body, 是否成功)。body 无法解析为 JSON 对象时返回 (nil, false)。

// chatToResponsesBody 把 OpenAI Chat 请求体转换为 Responses API 请求体。
func chatToResponsesBody(body []byte, model string, promptCacheKey string) ([]byte, bool) {
	var in map[string]json.RawMessage
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, false
	}

	out := make(map[string]interface{}, 8)
	out["model"] = model

	// messages → input, 同时抽出 system 作为 instructions
	//
	// 消息形态映射(实测 2026-09-27, 对 Zen /responses 逐个变体验证):
	//   role=system/developer  → instructions(字符串, 多条换行拼接), 不进 input
	//   role=user/assistant    → {role, content} 原样保留
	//   assistant + tool_calls → 追加 {type:"function_call", call_id, name, arguments} 项
	//   role=tool              → {type:"function_call_output", call_id, output}
	//
	// 关键: **不能**把 Chat 的 role=tool 直接塞进 input —— 实测该形态被上游拒绝,
	// 报 `input[N] did not match any supported type`(HTTP 400)。必须转为
	// function_call_output 类型项, 否则多轮工具调用场景必然失败。
	if rawMsgs, ok := in["messages"]; ok {
		var msgs []map[string]interface{}
		if err := json.Unmarshal(rawMsgs, &msgs); err == nil {
			inputs := make([]map[string]interface{}, 0, len(msgs))
			var systemParts []string
			for _, m := range msgs {
				role, _ := m["role"].(string)

				// system / developer → instructions
				if role == "system" || role == "developer" {
					if s, ok := m["content"].(string); ok && s != "" {
						systemParts = append(systemParts, s)
					}
					continue
				}

				// tool 结果 → function_call_output(Responses 专有类型)
				if role == "tool" {
					callID, _ := m["tool_call_id"].(string)
					out2 := map[string]interface{}{"type": "function_call_output"}
					if callID != "" {
						out2["call_id"] = callID
					}
					// output 必须是字符串; content 为数组时序列化保留信息。
					switch c := m["content"].(type) {
					case string:
						out2["output"] = c
					case nil:
						out2["output"] = ""
					default:
						b, err := json.Marshal(c)
						if err != nil {
							out2["output"] = ""
						} else {
							out2["output"] = string(b)
						}
					}
					inputs = append(inputs, out2)
					continue
				}

				// assistant 的 tool_calls → 展开为 function_call 项(置于该消息之前)
				if role == "assistant" {
					if tcs, ok := m["tool_calls"].([]interface{}); ok {
						for _, tci := range tcs {
							tcm, ok := tci.(map[string]interface{})
							if !ok {
								continue
							}
							callID, _ := tcm["id"].(string)
							fn, _ := tcm["function"].(map[string]interface{})
							name, _ := fn["name"].(string)
							args, _ := fn["arguments"].(string)
							if name == "" {
								continue
							}
							fc := map[string]interface{}{
								"type": "function_call",
								"name": name,
							}
							if callID != "" {
								fc["call_id"] = callID
							}
							if args != "" {
								fc["arguments"] = args
							} else {
								fc["arguments"] = "{}"
							}
							inputs = append(inputs, fc)
						}
					}
				}

				// 普通 user / assistant 消息
				//
				// 关键: assistant 仅携带 tool_calls 时 content 常为空串。此时若产出
				// {role:"assistant"} 这种**无 content 的空壳项**, 上游会判非法并报
				// `input[N] did not match any supported type`(实测 400)。而该消息的
				// tool_calls 已在上方展开为 function_call 项, 信息未丢失,
				// 故此处直接跳过空 content 的 assistant 消息。
				hasContent := false
				var contentVal interface{}
				if c, ok := m["content"]; ok {
					switch v := c.(type) {
					case string:
						if v != "" {
							hasContent = true
							contentVal = v
						}
					case nil:
						// 无内容
					default:
						hasContent = true
						contentVal = c
					}
				}
				if !hasContent {
					if role == "assistant" {
						// 纯 tool_calls 消息: 已展开, 不产生空壳项
						continue
					}
					// user 等角色无内容时给空串, 保持项存在(避免丢轮次)
					contentVal = ""
				}

				item := map[string]interface{}{}
				if role != "" {
					item["role"] = role
				}
				item["content"] = contentVal
				// 非 assistant 的 tool_calls(罕见) 原样保留
				if role != "assistant" {
					if tc, ok := m["tool_calls"]; ok {
						item["tool_calls"] = tc
					}
				}
				if name, ok := m["name"].(string); ok && name != "" {
					item["name"] = name
				}
				inputs = append(inputs, item)
			}
			out["input"] = inputs
			if len(systemParts) > 0 {
				out["instructions"] = strings.Join(systemParts, "\n\n")
			}
		}
	}

	// max_tokens / max_completion_tokens → max_output_tokens
	//
	// 下限保护(实测 2026-09-27): reasoning 类模型(如 muse-spark-*)在
	// max_output_tokens 过小时会因思考预算不足而**空流转结束** —— 上游只回一个
	// response.created 事件便关闭连接, 客户端收到空内容。实测数据:
	//   max_output_tokens=60/100/101 → 3526 字节, 0 个 output_text.delta(空响应)
	//   max_output_tokens>=200       → 8244 字节, 2 个 delta(正常)
	// 阈值表现为概率性(110 通过而 130 偶发失败), 故取一个足够大的安全下限,
	// 而非贴边阈值。默认 4096 足以容纳正常回答, 且不影响客户端对长度的语义期望
	// —— 若客户端显式给了更大值, 保留其原值。
	const minResponsesOutputTokens = 4096
	for _, k := range []string{"max_tokens", "max_completion_tokens"} {
		if raw, ok := in[k]; ok {
			var n int
			if err := json.Unmarshal(raw, &n); err == nil && n > 0 {
				if n < minResponsesOutputTokens {
					n = minResponsesOutputTokens
				}
				out["max_output_tokens"] = n
			} else {
				out["max_output_tokens"] = raw
			}
			break
		}
	}

	// tools: Chat 的 {type,function:{name,description,parameters}} → Responses 的
	// {type:"function",name,description,parameters}(扁平化)
	if raw, ok := in["tools"]; ok {
		var tools []map[string]interface{}
		if err := json.Unmarshal(raw, &tools); err == nil {
			flat := make([]map[string]interface{}, 0, len(tools))
			for _, t := range tools {
				ft, _ := t["type"].(string)
				if fn, ok := t["function"].(map[string]interface{}); ok {
					item := map[string]interface{}{"type": "function"}
					if ft != "" {
						item["type"] = ft
					}
					for _, k := range []string{"name", "description", "parameters", "strict"} {
						if v, ok := fn[k]; ok {
							item[k] = v
						}
					}
					flat = append(flat, item)
				} else if _, hasName := t["name"]; hasName {
					// 已是 Responses 扁平形态, 原样保留
					flat = append(flat, t)
				}
			}
			if len(flat) > 0 {
				out["tools"] = flat
			}
		}
	}

	// 直通字段
	for _, k := range []string{"tool_choice", "stream", "temperature", "top_p"} {
		if raw, ok := in[k]; ok {
			out[k] = raw
		}
	}

	// Responses 规范必填/强建议字段(实测真实 opencode CLI 均携带):
	//   include: ["reasoning.encrypted_content"] —— reasoning 模型必须声明, 否则流可能
	//            在首个事件后提前终止(实测 muse-spark-* 缺该字段时只回 response.created);
	//   store:   false —— 不持久化, 中继场景应显式关闭;
	//   prompt_cache_key: 会话键, 影响上游缓存命中与限流归属。
	// 若入站已是 Responses 格式则由调用方走 patchResponsesBodyModel 透传, 不经过此处。
	if _, ok := out["include"]; !ok {
		out["include"] = []string{"reasoning.encrypted_content"}
	}
	if _, ok := out["store"]; !ok {
		out["store"] = false
	}
	// prompt_cache_key: 优先透传入站已有值; 否则用 session 派生的稳定键。
	//
	// 必要性(2026-09-27 实测): 真实 opencode CLI 直连 Zen 时恒携带该字段。
	// 缺失会导致上游缓存归属不明, 长流在数十秒后被提前切断(实测反代在 39-54 秒
	// 中断, 直连可稳定跑满 123 秒并收到 response.completed)。
	if raw, ok := in["prompt_cache_key"]; ok {
		out["prompt_cache_key"] = raw
	} else if promptCacheKey != "" {
		out["prompt_cache_key"] = promptCacheKey
	}

	converted, err := json.Marshal(out)
	if err != nil {
		return nil, false
	}
	return converted, true
}
