// Package pricing: aigen_envelope.go —— AI 计费生成的 v1internal 信封构造与错误分类。
//
// 从 aigen.go 抽离的纯/轻函数,职责单一:
//   - buildAIRequestEnvelope:构造与 internal/stats/packet.go 同构的 v1internal 信封,
//     wantTools=true 时注入 google_search 顶层工具做联网检索;
//   - isAuthError / isGroundingRejection:错误分类,上层 Generate 据此决定重试策略;
//   - truncateBody:错误响应体截断,避免污染日志。
//
// isGroundingRejection 关键词收窄(相对旧版):
//   - 旧版 strings.Contains(msg,"tool") / "search" 单字过宽,易把"tool array too long"
//     等无关 400 误判为工具拒收而触发降级,导致联网路径被频繁误关闭;
//   - 本版改用具区分度的 "grounding tool" / "google_search" / "grounding" 等短语,
//     仅在错误真正指向 grounding 工具被拒收时才降级为纯知识库生成。
package pricing

import (
	"fmt"
	"strings"
	"time"
)

// buildAIRequestEnvelope 构造 v1internal 信封,与 packet.go:527-554 逐段对齐。
// wantTools=true 时在内层 request tools 里加一个 google_search 顶层工具做联网检索。
func buildAIRequestEnvelope(projectID, prompt, model string, wantTools bool) map[string]interface{} {
	requestInner := map[string]interface{}{
		"contents": []interface{}{
			map[string]interface{}{
				"role": "user",
				"parts": []interface{}{
					map[string]interface{}{"text": prompt},
				},
			},
		},
		"generationConfig": map[string]interface{}{
			"maxOutputTokens": AIMaxOutputTokens,
			"thinkingConfig": map[string]interface{}{
				"includeThoughts": false,
				"thinkingBudget":  0,
			},
		},
	}
	if wantTools {
		requestInner["tools"] = []interface{}{
			map[string]interface{}{"google_search": map[string]interface{}{}},
		}
	}
	return map[string]interface{}{
		"project":   projectID,
		"requestId": fmt.Sprintf("aiprice/%d", time.Now().UnixNano()),
		"request":   requestInner,
		"model":     model,
		"userAgent": "antigravity",
		// requestType 与 enabledCreditTypes 与 AnalyzePackets 对齐,
		// 让上游把这次调用计入 GOOGLE_ONE_AI 计费档(与抓包分析路径同信任域)。
		"requestType":        "chat",
		"enabledCreditTypes": []string{"GOOGLE_ONE_AI"},
	}
}

// isAuthError 判断错误是否为鉴权失败,上层据此触发 refreshAccount 重试。
func isAuthError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "HTTP 401")
}

// isGroundingRejection 判断错误是否疑似上游拒收 googleSearch 工具,
// 上层据此决定是否去掉 tools 降级为纯知识库生成。
//
// 收窄命中条件:HTTP 400 且错误文本含具区分度的 grounding 拒收关键词。
// 不再使用 "tool" / "search" 单字——它们极易误命中"tool array too long"
// "search context exceeded" 等与工具拒收无关的 400,导致联网路径被误关闭。
func isGroundingRejection(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "http 400") {
		return false
	}
	return strings.Contains(msg, "grounding") ||
		strings.Contains(msg, "google_search") ||
		strings.Contains(msg, "googlesearch") ||
		strings.Contains(msg, "search_entry_point")
}

// truncateBody 把过长的错误响应体截断,避免污染日志与错误串(与 packet.go 路径同款)。
func truncateBody(b []byte) string {
	if len(b) > 300 {
		return string(b[:300])
	}
	return string(b)
}
