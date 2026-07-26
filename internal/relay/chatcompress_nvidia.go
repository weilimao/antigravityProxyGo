package relay

import (
	"net/http"
	"strings"

	"antigravity-proxy/internal/settings"
)

// chatcompress_nvidia.go —— NVIDIA 链路对公共 chatcompress 引擎的接入辅助。
// 与 chatcompress.go(纯算法)分离,这里只做"取配置 + 调引擎 + 回写客户端 400 治本"三件事,
// 避免 nvidia.go(已 >900 行)继续膨胀,符合单文件模块化红线。

// tryCompressNvidiaRequest 读取 session 压缩配置,若已开启则用 ChatCompressor 对当前请求体就地压缩。
// 返回:(压缩后的新请求体, 是否真压缩, 是否启用压缩)。
// compEnabled=false 时调用方应完全跳过压缩分支(向后兼容,配置未开/缺省时行为同改动前)。
func (h *APICompatHandler) tryCompressNvidiaRequest(req *OpenAIChatRequest) (newReq *OpenAIChatRequest, ok bool, compEnabled bool) {
	if h == nil || h.settingsMgr == nil {
		return req, false, false
	}
	cfg := h.settingsMgr.GetSessionOptimization()
	if !cfg.NvidiaCompressEnabled {
		return req, false, false
	}
	threshold := cfg.NvidiaCompressThresholdTokens
	if threshold <= 0 {
		threshold = settings.ChatCompressDefaultThreshold
	}
	keepN := cfg.NvidiaCompressKeepToolResults
	if keepN <= 0 {
		keepN = settings.ChatCompressDefaultKeepN
	}
	comp := NewChatCompressor(threshold, keepN, ChatCompressDefaultMaxRetry)
	out, did := comp.Compress(req)
	return out, did, true
}

// looksLikeContextTooLong 判定上游首帧 error 文本是否含"上下文超模型窗口"语义。
// 命中这些文案才走"回写 Anthropic 标准 400 引导客户端本地 /compact 自压"治本路径;
// 纯 worker 限流(ResourceExhausted 而无超窗文案)不命中,保留原冷冻换号。
func looksLikeContextTooLong(frame string) bool {
	if frame == "" {
		return false
	}
	lower := strings.ToLower(frame)
	cues := []string{
		"maximum context length",
		"context length is",
		"prompt is too long",
		"reduce the length of the messages",
		"too many tokens",
		"request too large",
		"input length",
	}
	for _, c := range cues {
		if strings.Contains(lower, c) {
			return true
		}
	}
	return false
}

// replyAnthropicContextTooLong 回写 Anthropic 官方 invalid_request_error 400。
// 客户端(Claude Code)的 5 层错误恢复机制第 2 层(Reactive Compact)识别此结构与文案后,
// 会在本地自动 fork 摘要并替换 messages 数组(本地 /compact),从源头把后续轮次请求体减小。
// inboundAnthropic=true 时回 Anthropic Messages 错误体;否则回写与入站 OpenAI Chat 兼容的简单 error 体。
// writeJSON 内部已设 Content-Type 与 WriteHeader,此处不重复设置以免 superfluous WriteHeader 警告。
func (h *APICompatHandler) replyAnthropicContextTooLong(w http.ResponseWriter, inboundAnthropic bool, model string) {
	if inboundAnthropic {
		// Anthropic Messages 标准错误结构:{"type":"error","error":{"type":"invalid_request_error","message":"..."}}
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"type": "error",
			"error": map[string]interface{}{
				"type":    "invalid_request_error",
				"message": "This model's maximum context length has been exceeded. Please reduce the length of the messages.",
			},
		})
		return
	}
	// OpenAI Chat 兼容简单 error 体
	writeJSON(w, http.StatusBadRequest, map[string]interface{}{
		"error": map[string]interface{}{
			"type":    "invalid_request_error",
			"message": "context length exceeded; please reduce request size",
			"model":   model,
		},
	})
}
