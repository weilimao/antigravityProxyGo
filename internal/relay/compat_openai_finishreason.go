package relay

// compat_openai_finishreason.go: Gemini finishReason -> OpenAI finish_reason 映射。
// 上游 Gemini 的 candidates[].finishReason 必须 1:1 翻译到 OpenAI Chat Completions
// 的 finish_reason,否则客户端无法识别"触达 max_tokens 截断(length)"/"被安全过滤
// (content_filter)"等终端状态,把截断当正常 stop,内容静默丢失且不自知。
//
// Gemini finishReason 取值见 generativelanguage v1beta 候选终止枚举:
//   STOP / MAX_TOKENS / SAFETY / RECITATION / BLOCKLIST / PROHIBITED_CONTENT / SPII / OTHER
// (LANGUAGE 为较新可选值,本表一并并入 content_filter,保守不影响。)
//
// OpenAI finish_reason 官方取值: stop / length / tool_calls / content_filter。
// 当响应同时携带工具调用(hasToolCall)时,OpenAI 官方一律为 "tool_calls",即便上游
// finishReason 为 STOP(工具调用场景 Gemini 多以 STOP 收尾),故 hasToolCall 优先级最高。

// mapGeminiFinishToOpenAI 把 Gemini candidate 的 finishReason 翻译为 OpenAI finish_reason。
//
// 优先级:
//  1. hasToolCall==true → "tool_calls"(OpenAI 工具调用场景唯一终止态,保持与既有行为一致);
//  2. reason=="" 视作未携带,按正常完成 → "stop"(老上游/异常空响应兼容);
//  3. MAX_TOKENS → "length"(触达长度上限被截断);
//  4. SAFETY/RECITATION/BLOCKLIST/PROHIBITED_CONTENT/SPII/OTHER/LANGUAGE → "content_filter"
//     (被安全/引用/黑名单/禁言/隐私等规则拦截,无正常内容产出);
//  5. STOP(含大小写变体)及其余未知值 → "stop"(保守正常完成)。
func mapGeminiFinishToOpenAI(reason string, hasToolCall bool) string {
	// 1. 工具调用一律为 "tool_calls"
	if hasToolCall {
		return "tool_calls"
	}

	// 2. 归一化:Gemini 官方为大写枚举,容错大小写,trim 空白
	r := normalizeFinishReason(reason)

	// 3. 未携带 / 正常停止
	if r == "" || r == "STOP" {
		return "stop"
	}

	// 4. 长度截断
	if r == "MAX_TOKENS" {
		return "length"
	}

	// 5. 内容过滤类(被规则拦截)
	switch r {
	case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "OTHER", "LANGUAGE":
		return "content_filter"
	}

	// 6. 未知新枚举保守归入正常停止,避免向客户端暴露未知字符串导致其解析报错
	return "stop"
}

// normalizeFinishReason 容错处理 Gemini finishReason 的大小写与首尾空白。
// Gemini 官方枚举为全大写,但部分兼容上游(如 daily-cloudcode-pa 重译)可能返回小写或混合,
// 统一转大写后匹配,空串原样返回。
func normalizeFinishReason(reason string) string {
	s := reason
	// trim 前后空白(手写避免本文件 import strings 仅为一处复用而扩散依赖面)
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\n' || s[0] == '\r') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

// openAIToolChoiceToGemini 把入站 OpenAI Chat Completions 的 tool_choice 翻译为 Gemini toolConfig。
//
// OpenAI tool_choice 官方形态:
//   - 字符串: "auto"(默认,模型自行决定是否调用)/ "none"(禁调)/ "required"(必须调至少一个)
//   - 对象:   {"type":"function","function":{"name":"<name>"}}(强制调用指定工具)
//
// 与 Anthropic 链 translateToolChoiceToGemini 对齐的 Gemini 侧取值:
//   - none      → Mode=NONE
//   - auto/""   → Mode=VALIDATED(模型自行调,但保持 Gemini 校验链)
//   - required  → Mode=VALIDATED(强制至少一个,等价 Gemini 上游约束)
//   - 指定函数  → Mode=VALIDATED + AllowedFunctionNames=[name](Gemini 靠白名单独调)
//
// 注意:Gemini 没有 "required" 等价字面枚举,VALIDATED 是其函数调用校验链;
// 既不返回 nil 以保留"强制参与"语义,也不在 tools 为空时调用(由调用方保证)。
// 返回的 *GeminiToolConfig 会被 merge 进 gemReq.ToolConfig(见 TranslateOpenAIToGemini)。
func openAIToolChoiceToGemini(choice interface{}) *GeminiToolConfig {
	if choice == nil {
		return nil
	}

	config := &GeminiToolConfig{
		FunctionCallingConfig: &GeminiFCConfig{},
	}

	switch v := choice.(type) {
	case string:
		switch v {
		case "none":
			config.FunctionCallingConfig.Mode = "NONE"
		case "auto", "":
			config.FunctionCallingConfig.Mode = "VALIDATED"
		case "required":
			config.FunctionCallingConfig.Mode = "VALIDATED"
		default:
			// 未知字符串保守按 auto 处理,避免阻塞上游
			config.FunctionCallingConfig.Mode = "VALIDATED"
		}
	case map[string]interface{}:
		t, _ := v["type"].(string)
		if t == "function" {
			if fn, ok := v["function"].(map[string]interface{}); ok {
				if name, ok := fn["name"].(string); ok && name != "" {
					config.FunctionCallingConfig.Mode = "VALIDATED"
					config.FunctionCallingConfig.AllowedFunctionNames = []string{name}
					return config
				}
			}
			// {"type":"function"} 但缺 function.name → 按 required
			config.FunctionCallingConfig.Mode = "VALIDATED"
			return config
		}
		// 非 function 类型对象保守按 auto
		config.FunctionCallingConfig.Mode = "VALIDATED"
	default:
		// 未知类型(nil 已在入口拦截,此处防御)保守按 auto
		return nil
	}

	return config
}

// buildOpenAIUsage 构造 OpenAI Chat Completions 非流式 usage 与流式末尾 usage chunk 共用的
// 用量对象。思考 token(thoughtTokens>0)经 completion_tokens_details.reasoning_tokens 明细分项透出,
// 对齐 OpenAI 官方推理模型 usage 口径;completion_tokens 仍取 candidatesTokenCount(正文产出),
// 不把思考 token 加回 total,避免与 Gemini totalTokenCount 口径冲突。
//
// 当 thoughtTokens==0 时 completion_tokens_details 省略(指针 nil,omitempty 生效),
// 保持与非推理模型既有响应零回归(无多余 details 字段)。
func buildOpenAIUsage(inTokens, outTokens, thoughtTokens int) OpenAIResponseUsage {
	usage := OpenAIResponseUsage{
		PromptTokens:     inTokens,
		CompletionTokens: outTokens,
		TotalTokens:      inTokens + outTokens,
	}
	if thoughtTokens > 0 {
		usage.CompletionTokensDetails = &OpenAITokensDetails{ReasoningTokens: thoughtTokens}
	}
	return usage
}

// buildOpenAIUsagePtr 与 buildOpenAIUsage 同语义但返回指针,供 OpenAIStreamChunk.Usage(指针字段)
// 使用:仅在 include_usage 末尾 chunk 携带,其余 chunk 不引用故为零值 nil(字段 omitempty 省略)。
func buildOpenAIUsagePtr(inTokens, outTokens, thoughtTokens int) *OpenAIResponseUsage {
	u := buildOpenAIUsage(inTokens, outTokens, thoughtTokens)
	return &u
}
