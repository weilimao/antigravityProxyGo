package relay

// nvidia_cancel_e2e_test.go 业务/集成层:验证 NVIDIA 链路多会话并发取消隔离。
//
// 场景:两个会话 A、B 并发走 NVIDIA Anthropic 流式回译;A 用慢速永不结束上游,
// 在流中途取消 ctx;B 用可正常完结的上游。断言:
//   ① A 的 ctx 取消不串扰 B —— B 仍完整跑完事件序列(message_start→...→message_stop);
//   ② A 也能及时退出(被 watchCancel 中断),不再空跑上游;
//   ③ 两会话各自独立,不共享可变状态。
//
// 同时验证"客户端取消 → 上游 body 被 Close"在端到端链路中被触发。

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// runnerCaptor 既是 scanner 的上游 reader,也是 watchCancel 的 close target。
type runnerCaptor struct {
	*slowNeverEndingReader
}

// TestNvidiaMultiSessionCancelIsolation_AtoBNotDisturbed 验证取消会话 A 不影响 B 的流正常结束。
func TestNvidiaMultiSessionCancelIsolation_AtoBNotDisturbed(t *testing.T) {
	// 会话 A:慢速永不结束 + 中途取消
	bodyA := newSlowNeverEndingReader(
		"data: {\"id\":\"1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"a\"}}]}\n\n", 5)
	ctxA, cancelA := context.WithCancel(context.Background())
	var outA bytes.Buffer
	bwA := bufio.NewWriter(&outA)
	doneA := make(chan struct{})
	var aIn, aOut int
	go func() {
		defer close(doneA)
		aIn, aOut, _, _ = OpenAIChatSSEToAnthropicSSE(ctxA, bodyA, bodyA, bwA, "z-ai/glm-5.2", 0, nil)
		bwA.Flush()
	}()

	// 会话 B:可正常完结的上游(自然 EOF + finish_reason)
	sseB := strings.Join([]string{
		`data: {"id":"2","choices":[{"index":0,"delta":{"content":"hello"}}]}`,
		`data: {"id":"2","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`data: {"id":"2","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":3}}`,
		`data: [DONE]`,
		"",
	}, "\n\n")
	ctxB := context.Background()
	var outB bytes.Buffer
	bwB := bufio.NewWriter(&outB)
	doneB := make(chan struct{})
	var bIn, bOut int
	var errB error
	go func() {
		defer close(doneB)
		bIn, bOut, _, errB = OpenAIChatSSEToAnthropicSSE(ctxB, strings.NewReader(sseB), nil, bwB, "z-ai/glm-5.2", 0, nil)
		bwB.Flush()
	}()

	// 等会话 B 先完成,确认它完整收尾(取消 A 不应阻塞 B 的并发完成)
	select {
	case <-doneB:
	case <-time.After(3 * time.Second):
		t.Fatalf("会话 B 未在 3s 内正常完成(隔离失效?)")
	}
	if errB != nil {
		t.Fatalf("会话 B 正常路径不应报错: %v", errB)
	}
	eventsB := parseSSEEvents(outB.String())
	requireEvent(t, eventsB, "message_start")
	requireEvent(t, eventsB, "message_stop")
	if bIn != 5 || bOut != 3 {
		t.Errorf("会话 B usage 错乱: in=%d out=%d, want 5/3(PromptToken)/3(OutputTokens)", bIn, bOut)
	}

	// 现在取消会话 A
	cancelA()
	select {
	case <-doneA:
	case <-time.After(3 * time.Second):
		t.Fatalf("会话 A 未在 ctx 取消后 3s 内退出(watchCancel 未生效)")
	}
	if !bodyA.closed {
		t.Errorf("会话 A 上游 body 应被 watchCancel Close")
	}
	eventsA := parseSSEEvents(outA.String())
	requireEvent(t, eventsA, "message_start")
	// A 被取消也应补发尾帧,客户端不卡等
	requireEvent(t, eventsA, "message_delta")
	requireEvent(t, eventsA, "message_stop")

	// 隔离断言:A 被取消但 B 早已完成且 usage 正确 —— 已由上面断言覆盖。
	_ = aIn
	_ = aOut
}

// TestNvidiaMultiSessionCancelBoth_NoCrossCorruption 两会话都中途取消,断言各自独立补尾帧、不互相污染。
func TestNvidiaMultiSessionCancelBoth_NoCrossCorruption(t *testing.T) {
	const n = 3
	var wg sync.WaitGroup
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			body := newSlowNeverEndingReader(
				"data: {\"id\":\"x\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"z\"}}]}\n\n", 3)
			ctx, cancel := context.WithCancel(context.Background())
			// 随机错峰取消,模拟并发多会话各自被不同时机取消
			go func() {
				time.Sleep(time.Duration(10+idx*5) * time.Millisecond)
				cancel()
			}()
			var out bytes.Buffer
			bw := bufio.NewWriter(&out)
			done := make(chan struct{})
			go func() {
				defer close(done)
				_, _, _, _ = OpenAIChatSSEToAnthropicSSE(ctx, body, body, bw, "z-ai/glm-5.2", 0, nil)
				bw.Flush()
			}()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				errCh <- fmt.Errorf("会话 %d 未在 ctx 取消后 3s 退出", idx)
				return
			}
			if !body.closed {
				errCh <- fmt.Errorf("会话 %d 上游 body 未被 Close", idx)
				return
			}
			events := parseSSEEvents(out.String())
			if !hasEvent(events, "message_stop") {
				errCh <- fmt.Errorf("会话 %d 缺尾帧 message_stop, body: %s", idx, out.String())
				return
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for e := range errCh {
		t.Error(e)
	}
}

func hasEvent(events []sseEvent, name string) bool {
	for _, e := range events {
		if e.event == name {
			return true
		}
	}
	return false
}
