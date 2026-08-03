package relay

// compat_translate.go: client-protocol (OpenAI/Anthropic) <-> Gemini single-direction
// translate types and functions. As the single file reached 1093 lines (over the project
// red line), it has been split by responsibility into the satellites below. Logic is
// line-for-line equivalent; in-package symbols are shared unchanged.
//
//   compat_translate_types_openai.go     OpenAI Chat type family (Message/ToolCall/Request/Response/Stream/Delta)
//   compat_translate_types_anthropic.go   Anthropic Messages type family + Gemini ThinkingConfig/Config/Request carriers
//   compat_translate_types_gemini.go      Gemini generateContent type family (Blob/Part/Content/Candidate/Response)
//   compat_translate_translate.go         core translate functions (MapClientModelToGemini/ParseUnifiedOpenAIRequest/Translate*)
//   compat_translate_helpers.go           translate helpers (budget/findToolName/extractToken/parseToolCallArgs/truncate)
//
// This file is kept as an index header; it no longer holds entity logic.
