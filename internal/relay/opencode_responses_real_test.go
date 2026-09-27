package relay

import (
	"os"
	"testing"
)

// TestAggregateResponses_RealCapture 用真实抓取到的 Zen SSE 数据验证聚合器。
//
// 数据来源: 2026-09-27 直连 https://opencode.ai/zen/v1/responses 抓取的
// muse-spark-1.3-contributor-free 完整响应流(139KB, 含 59 个 tools 的 created 事件)。
// 该用例锁定聚合器对真实数据的解析能力, 避免仅凭合成数据通过而实际失效。
func TestAggregateResponses_RealCapture(t *testing.T) {
	const path = `C:\Temp\ms_events.txt`
	f, err := os.Open(path)
	if err != nil {
		t.Skipf("真实抓包文件不存在, 跳过: %v", err)
	}
	defer f.Close()

	resp, err := aggregateOpenAIResponsesSSE(f, "fallback")
	if err != nil {
		t.Fatalf("聚合失败: %v", err)
	}
	t.Logf("聚合结果: id=%s model=%s content=%q usage=%+v",
		resp.ID, resp.Model, resp.Choices[0].Message.Content, resp.Usage)

	if resp.Choices[0].Message.Content == "" {
		t.Error("content 为空 —— 聚合器未能从真实 SSE 提取文本")
	}
	if resp.Usage.PromptTokens == 0 && resp.Usage.CompletionTokens == 0 {
		t.Error("usage 全 0 —— 聚合器未解析 completed 事件的 usage")
	}
}
