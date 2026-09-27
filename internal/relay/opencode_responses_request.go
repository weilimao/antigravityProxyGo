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
func chatToResponsesBody(body []byte, model string) ([]byte, bool) {
	var in map[string]json.RawMessage
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, false
	}

	out := make(map[string]interface{}, 8)
	out["model"] = model

	// messages → input, 同时抽出 system 作为 instructions
	if rawMsgs, ok := in["messages"]; ok {
		var msgs []map[string]interface{}
		if err := json.Unmarshal(rawMsgs, &msgs); err == nil {
			inputs := make([]map[string]interface{}, 0, len(msgs))
			var systemParts []string
			for _, m := range msgs {
				role, _ := m["role"].(string)
				if role == "system" || role == "developer" {
					if s, ok := m["content"].(string); ok && s != "" {
						systemParts = append(systemParts, s)
					}
					continue
				}
				item := map[string]interface{}{}
				if role != "" {
					item["role"] = role
				}
				if c, ok := m["content"]; ok {
					item["content"] = c
				}
				// tool 结果消息: Responses 用 function_call_output 形态, 这里保守处理为
				// 带 tool_call_id 的普通输入项, 保留信息不丢失。
				if id, ok := m["tool_call_id"].(string); ok && id != "" {
					item["tool_call_id"] = id
				}
				if name, ok := m["name"].(string); ok && name != "" {
					item["name"] = name
				}
				if tc, ok := m["tool_calls"]; ok {
					item["tool_calls"] = tc
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
	if raw, ok := in["prompt_cache_key"]; ok {
		out["prompt_cache_key"] = raw
	}

	converted, err := json.Marshal(out)
	if err != nil {
		return nil, false
	}
	return converted, true
}
