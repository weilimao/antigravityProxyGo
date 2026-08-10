package relay

// passthrough_body.go: 透传上游请求体构造(buildUpstreamBody 逐入站/上游协议形态归一化 +
// buildPassthroughUpstreamReq 入站 body→OpenAIChatRequest + normalizeOpenAIChatRoles
// developer→system role 折叠 + ensureIncludeUsage 流式 usage 注入),从 passthrough_forwarder.go
// 按职责物理拆分,同 package relay 跨文件符号自动解析,逐字等价零回归。

import (
	"encoding/json"
	"fmt"
)

// buildUpstreamBody 按入站协议与上游协议形态构造发往上游的请求体。
// allowOCR 为 false 时跳过 image 降级(避免换号时重复降级,外部已降级后调用)。
// 返回的 body 直接作为上游请求体 marshal 透传。
func (pf *passthroughForward) buildUpstreamBody(bodyBytes []byte, upstreamModel string, isStreaming bool,
	isChat, isResponses, isMessages bool, upstreamFormat string, userSession *RelaySession, allowOCR bool,
) ([]byte, error) {
	// 上游 OpenAI 兼容端点:入站 OpenAI Chat / Responses → OpenAIChatRequest(Responses 转换);入站 Anthropic → AnthropicToOpenAIChat(含 image 降级)。
	if upstreamFormat == "openai" {
		if isMessages {
			var anthReq AnthropicRequest
			if err := json.Unmarshal(bodyBytes, &anthReq); err != nil {
				return nil, fmt.Errorf("invalid anthropic request: %w", err)
			}
			anthReq.Model = upstreamModel
			// 本地图片路径自愈(L2.5 预处理):Claude Code 等客户端对未识别模型会剔除 image 块,
			// 本地截图路径作为纯 text 块发来。allowOCR=true 时先扫 text 块裸路径读图 OCR 注入,
			// 再交下方 DowngradeAnthropicImagesToText 做结构化 image 块降级。静默 miss 不报错。
			// allowOCR=false(换号重试或 ocrSelf 自递归)时跳过,避免重复注入。
			if allowOCR {
				if enriched := pf.h.ocr.EnrichLocalImagePathsInAnthropic(&anthReq, userSession); enriched > 0 {
					pf.h.log("✅ [路由转发] Anthropic 检测到 %d 个本地图片路径,已读图 OCR 注入 text 块(会话 %s)", enriched, ocrSessionDisplay(userSession))
				}
			}
			// 多模态判据:上游模型原生支持视觉时跳过 image 降级(图块原样透传,保留原生视觉理解)。
			// 与外层 attempt==0 的 OpenAI Chat 分支用同一份 pf.h.ocr.modelSupportsImage 判据,
			// 覆盖 DeepSeek-VL / Qwen-VL / Kimi-K2 等挂在 OpenAI 兼容端点上的多模态上游。
			if allowOCR && pf.h.ocr != nil && !pf.h.ocr.modelSupportsImage(upstreamModel) {
				if replaced, errDown, _, _, _ := pf.h.ocr.DowngradeAnthropicImagesToText(&anthReq, userSession); errDown == nil && replaced > 0 {
					pf.h.log("✅ [路由转发] Anthropic image 降级 %d 块 → OpenAI Chat(会话 %s)", replaced, ocrSessionDisplay(userSession))
				}
			}
			mappings := pf.h.getRelayModelMappingSafe()
			// 多模态上游保图:与上方降级闸同一判据,上游原生支持视觉时让翻译层把 image 块转译为
			// OpenAI Chat Vision 数组形态 content 原样透传(否则旧字符串路径静默丢图)。
			preserveImages := allowOCR && pf.h.ocr != nil && pf.h.ocr.modelSupportsImage(upstreamModel)
			u, err := AnthropicToOpenAIChatPreservingImages(&anthReq, preserveImages, mappings)
			if err != nil {
				return nil, fmt.Errorf("anthropic->openai transform failed: %w", err)
			}
			if isStreaming {
				ensureIncludeUsage(u)
			}
			return json.Marshal(u)
		}
		upstreamReq, err := buildPassthroughUpstreamReq(bodyBytes, upstreamModel, isStreaming)
		if err != nil {
			return nil, err
		}
		return json.Marshal(upstreamReq)
	}

	// 上游 Anthropic 原生端点 /v1/messages。
	// 入站 Anthropic → 原样透传(仅 model 改写);入站 OpenAI Chat / Responses → OpenAIToAnthropicMessages 转译。
	if isMessages {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(bodyBytes, &obj); err != nil {
			return nil, fmt.Errorf("invalid anthropic request: %w", err)
		}
		mb, _ := json.Marshal(upstreamModel)
		obj["model"] = mb
		return json.Marshal(obj)
	}
	// 入站 OpenAI Chat / Responses → Anthropic Messages 请求体。
	anthReq, err := OpenAIToAnthropicMessages(bodyBytes, upstreamModel, isResponses)
	if err != nil {
		return nil, fmt.Errorf("openai->anthropic transform failed: %w", err)
	}
	return json.Marshal(anthReq)
}

// buildPassthroughUpstreamReq 把入站 body 归一化为 OpenAIChatRequest 并改写 model/stream_options。
// 入站可能是 OpenAI Chat(直解)或 Responses(ResponsesToOpenAIChat 转换);二者产出同构,
// 这里统一在产出后 set model,避免对两种入站各写一份改写逻辑。
func buildPassthroughUpstreamReq(bodyBytes []byte, upstreamModel string, isStreaming bool) (*OpenAIChatRequest, error) {
	// 先尝试 Chat 直解;失败再尝试 Responses。两路径产出 OpenAIChatRequest。
	var chatReq OpenAIChatRequest
	if err := json.Unmarshal(bodyBytes, &chatReq); err == nil && len(chatReq.Messages) > 0 {
		chatReq.Model = upstreamModel
		if isStreaming {
			ensureIncludeUsage(&chatReq)
		}
		normalizeOpenAIChatRoles(&chatReq)
		return &chatReq, nil
	}
	// Responses 形态(如 Codex /v1/responses):input[] 而非 messages[]。
	u, err := ResponsesToOpenAIChat(bodyBytes, upstreamModel)
	if err != nil {
		return nil, fmt.Errorf("unrecognized openai/responses request body: %w", err)
	}
	if isStreaming {
		ensureIncludeUsage(u)
	}
	normalizeOpenAIChatRoles(u)
	return u, nil
}

// normalizeOpenAIChatRoles 把 OpenAI Chat/Responses 入站消息的 role 归一化为第三方
// OpenAI 兼容端点能识别的集合。
//
// 背景:OpenAI Codex CLI 等新客户端会把系统提示写成新版规范要求的 "developer" 角色,
// 并原样经 /route/* 透传到 Other 号池(阿里云 deepseek / 商汤 sensenova 等)的
// /v1/chat/completions。这些第三方端点仅兼容旧规范,允许的 role 只有
// system/assistant/user/tool/function,收到 "developer" 会直接回 400
// (invalid_request_error: "developer is not one of [...]")。
// 这里在透传前统一把 developer 折叠为 system,语义等价(OpenAI 官方即把 developer 视作
// system 的命名升级),彻底消除该 400。其余 role 原样保留,零副作用。
func normalizeOpenAIChatRoles(req *OpenAIChatRequest) {
	if req == nil {
		return
	}
	for i := range req.Messages {
		if req.Messages[i].Role == "developer" {
			req.Messages[i].Role = "system"
		}
	}
}

// ensureIncludeUsage 注入 stream_options.include_usage,确保上游 SSE 末尾吐 usage,
// 与 handleNvidia 的流式行为一致,供统计/计费链路回收 token。
func ensureIncludeUsage(req *OpenAIChatRequest) {
	if req == nil {
		return
	}
	if req.StreamOptions == nil || !req.StreamOptions.IncludeUsage {
		req.StreamOptions = &ChatStreamOptions{IncludeUsage: true}
	}
}
