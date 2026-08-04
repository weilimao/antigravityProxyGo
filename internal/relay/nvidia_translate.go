package relay

// nvidia_translate.go 实现 Anthropic Messages <-> OpenAI Chat Completions 的双向协议转换。
// 这是给 NVIDIA NIM(及任意 OpenAI Chat 兼容上游)专用的转换层,
// 与现有 compat_translate.go 的「客户端协议 -> Gemini」单向转换并存但不耦合。
//
// 参考实现:cc-switch 的 transform.rs::anthropic_to_openai_with_reasoning_content /
// openai_to_anthropic 及 streaming.rs::create_anthropic_sse_stream。
//
// 本文件逻辑已按职责拆分到:
//   nvidia_translate_request.go   请求方向转换
//   nvidia_translate_response.go  非流式响应方向转换
//   nvidia_translate_sse.go       流式转译主循环 + sseBlockStates 状态机 + watchCancel
//   nvidia_translate_buffer.go    sseEventSink + flushWriter/replayWriter/teeSink/resumeSink + 帧扫描/改写辅助
//   nvidia_translate_payload.go   Anthropic SSE payload 构造 + 全局思考/reasoning 开关 + mapNvidiaModel
//   nvidia_translate_types.go     OpenAI Chat 兼容类型定义
//   nvidia_translate_ocr.go       已迁出至 ocr_downgrade_anthropic.go + ocr_engine.go(本文件保留空锚点)

