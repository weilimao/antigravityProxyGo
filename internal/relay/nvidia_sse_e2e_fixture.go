package relay

// nvidia_sse_e2e_fixture.go: 从日志 20260828_191140_nv_1787915500740684000.log 提取的
// 真实上游(Kimi-K3/NIM)SSE 字节,逐字节封板(0 手改),用于 E2E 测试。
// 覆盖形态: reasoning_content x20 + 1 帧完整 tool_calls + finish=tool_calls + usage + [DONE]。

const realUpstreamKimiToolCallSSE = `data: {"id":"chatcmpl-bc6fa9cc-2acb-4955-b032-930d2bd6033b","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"Let"},"finish_reason":null,"logprobs":null}],"created":1787915500,"model":"moonshotai/kimi-k3","service_tier":null,"system_fingerprint":null,"object":"chat.completion.chunk","usage":null}

data: {"id":"chatcmpl-bc6fa9cc-2acb-4955-b032-930d2bd6033b","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":" me"},"finish_reason":null,"logprobs":null}],"created":1787915500,"model":"moonshotai/kimi-k3","service_tier":null,"system_fingerprint":null,"object":"chat.completion.chunk","usage":null}

data: {"id":"chatcmpl-bc6fa9cc-2acb-4955-b032-930d2bd6033b","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":" try"},"finish_reason":null,"logprobs":null}],"created":1787915500,"model":"moonshotai/kimi-k3","service_tier":null,"system_fingerprint":null,"object":"chat.completion.chunk","usage":null}

data: {"id":"chatcmpl-bc6fa9cc-2acb-4955-b032-930d2bd6033b","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":" making"},"finish_reason":null,"logprobs":null}],"created":1787915500,"model":"moonshotai/kimi-k3","service_tier":null,"system_fingerprint":null,"object":"chat.completion.chunk","usage":null}

data: {"id":"chatcmpl-bc6fa9cc-2acb-4955-b032-930d2bd6033b","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":" the"},"finish_reason":null,"logprobs":null}],"created":1787915500,"model":"moonshotai/kimi-k3","service_tier":null,"system_fingerprint":null,"object":"chat.completion.chunk","usage":null}

data: {"id":"chatcmpl-bc6fa9cc-2acb-4955-b032-930d2bd6033b","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":" tool"},"finish_reason":null,"logprobs":null}],"created":1787915500,"model":"moonshotai/kimi-k3","service_tier":null,"system_fingerprint":null,"object":"chat.completion.chunk","usage":null}

data: {"id":"chatcmpl-bc6fa9cc-2acb-4955-b032-930d2bd6033b","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":" call"},"finish_reason":null,"logprobs":null}],"created":1787915500,"model":"moonshotai/kimi-k3","service_tier":null,"system_fingerprint":null,"object":"chat.completion.chunk","usage":null}

data: {"id":"chatcmpl-bc6fa9cc-2acb-4955-b032-930d2bd6033b","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":" now"},"finish_reason":null,"logprobs":null}],"created":1787915500,"model":"moonshotai/kimi-k3","service_tier":null,"system_fingerprint":null,"object":"chat.completion.chunk","usage":null}

data: {"id":"chatcmpl-bc6fa9cc-2acb-4955-b032-930d2bd6033b","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":" —"},"finish_reason":null,"logprobs":null}],"created":1787915500,"model":"moonshotai/kimi-k3","service_tier":null,"system_fingerprint":null,"object":"chat.completion.chunk","usage":null}

data: {"id":"chatcmpl-bc6fa9cc-2acb-4955-b032-930d2bd6033b","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":" a"},"finish_reason":null,"logprobs":null}],"created":1787915500,"model":"moonshotai/kimi-k3","service_tier":null,"system_fingerprint":null,"object":"chat.completion.chunk","usage":null}

data: {"id":"chatcmpl-bc6fa9cc-2acb-4955-b032-930d2bd6033b","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":" single"},"finish_reason":null,"logprobs":null}],"created":1787915500,"model":"moonshotai/kimi-k3","service_tier":null,"system_fingerprint":null,"object":"chat.completion.chunk","usage":null}

data: {"id":"chatcmpl-bc6fa9cc-2acb-4955-b032-930d2bd6033b","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":" Bash"},"finish_reason":null,"logprobs":null}],"created":1787915500,"model":"moonshotai/kimi-k3","service_tier":null,"system_fingerprint":null,"object":"chat.completion.chunk","usage":null}

data: {"id":"chatcmpl-bc6fa9cc-2acb-4955-b032-930d2bd6033b","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":" call"},"finish_reason":null,"logprobs":null}],"created":1787915500,"model":"moonshotai/kimi-k3","service_tier":null,"system_fingerprint":null,"object":"chat.completion.chunk","usage":null}

data: {"id":"chatcmpl-bc6fa9cc-2acb-4955-b032-930d2bd6033b","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":" to"},"finish_reason":null,"logprobs":null}],"created":1787915500,"model":"moonshotai/kimi-k3","service_tier":null,"system_fingerprint":null,"object":"chat.completion.chunk","usage":null}

data: {"id":"chatcmpl-bc6fa9cc-2acb-4955-b032-930d2bd6033b","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":" test"},"finish_reason":null,"logprobs":null}],"created":1787915500,"model":"moonshotai/kimi-k3","service_tier":null,"system_fingerprint":null,"object":"chat.completion.chunk","usage":null}

data: {"id":"chatcmpl-bc6fa9cc-2acb-4955-b032-930d2bd6033b","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":" whether"},"finish_reason":null,"logprobs":null}],"created":1787915500,"model":"moonshotai/kimi-k3","service_tier":null,"system_fingerprint":null,"object":"chat.completion.chunk","usage":null}

data: {"id":"chatcmpl-bc6fa9cc-2acb-4955-b032-930d2bd6033b","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":" tool"},"finish_reason":null,"logprobs":null}],"created":1787915500,"model":"moonshotai/kimi-k3","service_tier":null,"system_fingerprint":null,"object":"chat.completion.chunk","usage":null}

data: {"id":"chatcmpl-bc6fa9cc-2acb-4955-b032-930d2bd6033b","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":" execution"},"finish_reason":null,"logprobs":null}],"created":1787915500,"model":"moonshotai/kimi-k3","service_tier":null,"system_fingerprint":null,"object":"chat.completion.chunk","usage":null}

data: {"id":"chatcmpl-bc6fa9cc-2acb-4955-b032-930d2bd6033b","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":" works"},"finish_reason":null,"logprobs":null}],"created":1787915500,"model":"moonshotai/kimi-k3","service_tier":null,"system_fingerprint":null,"object":"chat.completion.chunk","usage":null}

data: {"id":"chatcmpl-bc6fa9cc-2acb-4955-b032-930d2bd6033b","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"."},"finish_reason":null,"logprobs":null}],"created":1787915500,"model":"moonshotai/kimi-k3","service_tier":null,"system_fingerprint":null,"object":"chat.completion.chunk","usage":null}

data: {"id":"chatcmpl-bc6fa9cc-2acb-4955-b032-930d2bd6033b","choices":[{"index":0,"delta":{"content":"","tool_calls":[{"index":0,"id":"Bash:0","type":"function","function":{"name":"Bash","arguments":"{\"command\":\"echo tool-check-ok\",\"description\":\"验证工具调用通道可用\"}"}}],"role":"assistant"},"finish_reason":null,"logprobs":null}],"created":1787915500,"model":"moonshotai/kimi-k3","service_tier":null,"system_fingerprint":null,"object":"chat.completion.chunk","usage":null}

data: {"id":"chatcmpl-bc6fa9cc-2acb-4955-b032-930d2bd6033b","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls","logprobs":null}],"created":1787915500,"model":"moonshotai/kimi-k3","service_tier":null,"system_fingerprint":null,"object":"chat.completion.chunk","usage":null}

data: {"id":"chatcmpl-bc6fa9cc-2acb-4955-b032-930d2bd6033b","choices":[],"created":1787915500,"model":"moonshotai/kimi-k3","service_tier":null,"system_fingerprint":null,"object":"chat.completion.chunk","usage":{"prompt_tokens":31522,"completion_tokens":92,"total_tokens":31614,"prompt_tokens_details":{"audio_tokens":null,"cached_tokens":7680}}}

data: [DONE]

`
