package relay

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

// opencode_responses_stream_test.go: Responses SSE → Chat SSE 实时转换用例。
//
// 背景(2026-09-27 实测):
//   此前流式路径先聚合完整响应再一次性发出, 实测长输出请求出现 172 秒零输出、
//   随后内容瞬间涌出(客户端表现为卡死); 上游中断时表现为"输出一半停住"。
//   本组用例锁定逐帧实时转换行为。

// TestResponsesSSEToChatSSE_Basic 验证基础文本流的逐帧转换。
func TestResponsesSSEToChatSSE_Basic(t *testing.T) {
	sse := "event: response.created\n" +
		"data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\",\"model\":\"muse-spark-1.3\",\"created_at\":1700000000}}\n\n" +
		"event: response.output_text.delta\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"Hello\"}\n\n" +
		"event: response.output_text.delta\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\" world\"}\n\n" +
		"event: response.completed\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"usage\":{\"input_tokens\":7,\"output_tokens\":2,\"total_tokens\":9}}}\n\n"

	var buf bytes.Buffer
	in, out, cached, err := responsesSSEToChatSSE(strings.NewReader(sse), &buf, "fb", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if in != 7 || out != 2 {
		t.Errorf("usage = %d/%d, want 7/2", in, out)
	}
	_ = cached

	s := buf.String()
	// 应含逐帧 content delta
	if !strings.Contains(s, `"content":"Hello"`) {
		t.Errorf("缺少 Hello 帧: %s", s)
	}
	if !strings.Contains(s, `"content":" world"`) {
		t.Errorf("缺少 ' world' 帧: %s", s)
	}
	// 应含 role 首帧
	if !strings.Contains(s, `"role":"assistant"`) {
		t.Errorf("缺少 role 首帧: %s", s)
	}
	// 应含末帧 finish_reason
	if !strings.Contains(s, `"finish_reason":"stop"`) {
		t.Errorf("缺少 finish_reason: %s", s)
	}
	// 应以 [DONE] 结束
	if !strings.HasSuffix(s, "data: [DONE]\n\n") {
		t.Errorf("未以 [DONE] 结束: %q", s[len(s)-40:])
	}
}

// TestResponsesSSEToChatSSE_RealtimeFlush 验证逐帧 flush 而非缓冲到结束。
//
// 这是本次修复的核心: 若实现改为聚合后再发, 则 flush 次数会远小于 delta 帧数。
func TestResponsesSSEToChatSSE_RealtimeFlush(t *testing.T) {
	var frames []string
	for i := 0; i < 20; i++ {
		frames = append(frames,
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"x\"}\n\n")
	}
	frames = append(frames,
		"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n")
	sse := strings.Join(frames, "")

	flushCount := 0
	var buf bytes.Buffer
	_, _, _, err := responsesSSEToChatSSE(strings.NewReader(sse), &buf, "m", func() {
		flushCount++
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 20 个 delta + role 首帧 + 末帧 = 至少 22 次 flush
	if flushCount < 20 {
		t.Errorf("flush 次数 = %d, 应 >= 20 (证明逐帧实时而非聚合后一次发出)", flushCount)
	}
	t.Logf("flush 次数 = %d (delta=20)", flushCount)
}

// TestResponsesSSEToChatSSE_ToolCall 验证 function_call 转为 tool_calls 增量。
func TestResponsesSSEToChatSSE_ToolCall(t *testing.T) {
	sse := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"r2\",\"model\":\"m\"}}\n\n" +
		"data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"call_id\":\"call_x\",\"name\":\"bash\",\"arguments\":\"{\\\"command\\\":\\\"ls\\\"}\"}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r2\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n"

	var buf bytes.Buffer
	_, _, _, err := responsesSSEToChatSSE(strings.NewReader(sse), &buf, "m", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := buf.String()
	if !strings.Contains(s, `"name":"bash"`) {
		t.Errorf("缺少 tool_calls name: %s", s)
	}
	if !strings.Contains(s, `"id":"call_x"`) {
		t.Errorf("缺少 tool_calls id: %s", s)
	}
	if !strings.Contains(s, `"finish_reason":"tool_calls"`) {
		t.Errorf("tool_calls 场景 finish_reason 应为 tool_calls: %s", s)
	}
}

// TestResponsesSSEToChatSSE_UpstreamError 验证错误帧被识别。
func TestResponsesSSEToChatSSE_UpstreamError(t *testing.T) {
	sse := "data: {\"type\":\"error\",\"error\":{\"type\":\"FreeTierError\",\"message\":\"restricted\"}}\n\n"

	var buf bytes.Buffer
	_, _, _, err := responsesSSEToChatSSE(strings.NewReader(sse), &buf, "m", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	uerr, ok := err.(*openCodeUpstreamError)
	if !ok {
		t.Fatalf("error type = %T, want *openCodeUpstreamError", err)
	}
	if uerr.Type != "FreeTierError" {
		t.Errorf("type = %q, want FreeTierError", uerr.Type)
	}
}

// TestResponsesSSEToChatSSE_EmptyStream 验证空流报错。
func TestResponsesSSEToChatSSE_EmptyStream(t *testing.T) {
	var buf bytes.Buffer
	_, _, _, err := responsesSSEToChatSSE(strings.NewReader(""), &buf, "m", nil)
	if err == nil {
		t.Fatal("expected error for empty stream")
	}
}

// TestResponsesSSEToChatSSE_MalformedSkipped 验证畸形帧跳过。
func TestResponsesSSEToChatSSE_MalformedSkipped(t *testing.T) {
	sse := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"A\"}\n\n" +
		"data: {broken\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"B\"}\n\n"

	var buf bytes.Buffer
	_, _, _, err := responsesSSEToChatSSE(strings.NewReader(sse), &buf, "m", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := buf.String()
	if !strings.Contains(s, `"content":"A"`) || !strings.Contains(s, `"content":"B"`) {
		t.Errorf("畸形帧应被跳过且 A/B 均保留: %s", s)
	}
}

// TestResponsesSSEToChatSSE_ReasoningDelta 验证思考增量单独映射。
func TestResponsesSSEToChatSSE_ReasoningDelta(t *testing.T) {
	sse := "data: {\"type\":\"response.reasoning_text.delta\",\"delta\":\"think\"}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"answer\"}\n\n"

	var buf bytes.Buffer
	_, _, _, err := responsesSSEToChatSSE(strings.NewReader(sse), &buf, "m", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := buf.String()
	if !strings.Contains(s, `"reasoning_content":"think"`) {
		t.Errorf("缺少 reasoning_content: %s", s)
	}
	if !strings.Contains(s, `"content":"answer"`) {
		t.Errorf("缺少 content: %s", s)
	}
}

// TestResponsesSSEToChatSSE_SlowUpstreamStillStreams 验证上游慢速分帧时
// 转换器仍逐帧产出(而非等全部收完), 这是"不卡死"的关键性质。
func TestResponsesSSEToChatSSE_SlowUpstreamStillStreams(t *testing.T) {
	pr, pwRaw := io.Pipe()
	pw := &slowWriter{pw: pwRaw, delay: 3 * time.Millisecond}
	go func() {
		defer pw.Close()
		for i := 0; i < 5; i++ {
			_, _ = pw.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"chunk\"}\n\n"))
		}
		_, _ = pw.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n"))
	}()

	var buf bytes.Buffer
	firstFlush := make(chan time.Duration, 1)
	t0 := time.Now()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _, _, _ = responsesSSEToChatSSE(pr, &buf, "m", func() {
			select {
			case firstFlush <- time.Since(t0):
			default:
			}
		})
	}()

	select {
	case d := <-firstFlush:
		// 首次 flush 应在上游全部写完之前发生
		if d > 500*time.Millisecond {
			t.Errorf("首次 flush 延迟 %v, 应在上游写完前发生(逐帧实时)", d)
		}
		t.Logf("首次 flush 延迟 = %v", d)
	case <-time.After(3 * time.Second):
		t.Fatal("3 秒内未收到任何 flush —— 说明是聚合后一次发出, 非实时")
	}
	<-done
}

// slowWriter 包装 io.PipeWriter, 每次写入前延时, 模拟慢速上游分帧到达。
type slowWriter struct {
	pw    *io.PipeWriter
	delay time.Duration
}

func (s *slowWriter) Write(b []byte) (int, error) {
	time.Sleep(s.delay)
	return s.pw.Write(b)
}

func (s *slowWriter) Close() error { return s.pw.Close() }

// TestRebuildChatSSEFromAggregated_StillWorks 验证非流式路径的重建函数未被破坏。
func TestRebuildChatSSEFromAggregated_StillWorks(t *testing.T) {
	resp := &OpenAIChatResponse{
		ID: "agg1", Object: "chat.completion", Created: 1700000000, Model: "m",
		Choices: []OpenAIChatChoice{{
			Index:        0,
			Message:      ChatMessage{Role: "assistant", Content: "hi"},
			FinishReason: "stop",
		}},
	}
	s := rebuildChatSSEString(resp)
	if !strings.Contains(s, `"content":"hi"`) {
		t.Errorf("重建 SSE 缺少内容: %s", s)
	}
	if countSSEFrames(s) < 3 {
		t.Errorf("重建 SSE 帧数不足: %d", countSSEFrames(s))
	}
	// 应可被 JSON 解析
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, "data: ") && !strings.Contains(line, "[DONE]") {
			var v map[string]interface{}
			if err := json.Unmarshal([]byte(line[6:]), &v); err != nil {
				t.Errorf("重建帧非法 JSON: %v | %s", err, line[:80])
			}
		}
	}
}

// TestPipeReadCloser_CloseUnblocksWriter 验证管道包装器关闭时能解除生产端阻塞。
//
// 背景(2026-09-27): 曾用 io.NopCloser(pr) 作为下游转换器的 body 参数, 导致
// 客户端断开时 watchCancel 关闭无效, 生产端 goroutine 永久阻塞在 pw.Write,
// 整个管道死锁 —— 实测客户端收到 InvalidHTTPResponse、handler 永久挂起。
// 本用例锁定 pipeReadCloser.Close() 能真正解除写端阻塞。
func TestPipeReadCloser_CloseUnblocksWriter(t *testing.T) {
	pr, pw := io.Pipe()
	body := &pipeReadCloser{pr: pr, pw: pw}

	writeDone := make(chan error, 1)
	go func() {
		// 无读者时该写入会阻塞, 直到 pw 被关闭。
		_, err := pw.Write([]byte("blocked"))
		writeDone <- err
	}()

	// 确保写端已进入阻塞状态。
	time.Sleep(30 * time.Millisecond)

	select {
	case <-writeDone:
		t.Fatal("写端不应在 Close 前完成")
	default:
	}

	if err := body.Close(); err != nil {
		t.Logf("Close 返回: %v (非致命)", err)
	}

	select {
	case err := <-writeDone:
		if err == nil {
			t.Error("Close 后写入应返回错误(ErrClosedPipe), 实际 nil")
		} else {
			t.Logf("写端已解除阻塞, err = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close 未能解除写端阻塞 —— 管道会死锁")
	}
}

// TestPipeReadCloser_CloseIdempotent 验证重复 Close 安全(once 保护)。
func TestPipeReadCloser_CloseIdempotent(t *testing.T) {
	pr, pw := io.Pipe()
	body := &pipeReadCloser{pr: pr, pw: pw}
	_ = body.Close()
	_ = body.Close()
	_ = body.Close()
}

// TestResponsesSSEToChatSSE_LargeStreamNoDeadlock 验证大流量下逐帧转换不死锁。
//
// 模拟长输出(500 个 delta 帧)经管道实时转换, 若存在缓冲/关闭缺陷会在此超时。
func TestResponsesSSEToChatSSE_LargeStreamNoDeadlock(t *testing.T) {
	const nFrames = 500
	pr, pw := io.Pipe()
	body := &pipeReadCloser{pr: pr, pw: pw}

	go func() {
		for i := 0; i < nFrames; i++ {
			_, _ = pw.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"abcdefghij\"}\n\n"))
		}
		_, _ = pw.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":500,\"total_tokens\":510}}}\n\n"))
		_ = pw.Close()
	}()

	done := make(chan int, 1)
	go func() {
		var buf bytes.Buffer
		_, _, _, _ = responsesSSEToChatSSE(pr, &buf, "m", nil)
		done <- buf.Len()
	}()

	select {
	case n := <-done:
		if n == 0 {
			t.Error("转换输出为空")
		}
		t.Logf("大流量转换完成, 输出 %d 字节 (%d 帧)", n, nFrames)
	case <-time.After(10 * time.Second):
		t.Fatal("大流量转换超时 —— 存在死锁")
	}
	_ = body
}
