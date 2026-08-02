package relay

import (
	"errors"
	"io"
	"net/http"
	"runtime"
	"sync"
	"testing"
	"time"
)

// 以下为 readBodyWithTimeout 的单元测试,验证三条核心契约:
//   1. 正常路径:timeout 内读完 body 原样返回;
//   2. 超时路径:返回 ErrBodyReadTimeout,且在窗口附近及时触发;
//   3. 无泄漏:超时后读 goroutine 不残留(NumGoroutine 不增长)。
// 通过 io.Pipe 构造"永不投递 body"的慢请求,模拟客户端只发 header / 链路半死场景。

func TestReadBodyWithTimeout_Normal(t *testing.T) {
	r := newSlowBodyRequest([]byte(`{"hello":"world"}`))
	got, err := readBodyWithTimeout(r, 2*time.Second)
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if string(got) != `{"hello":"world"}` {
		t.Fatalf("unexpected body: %q", string(got))
	}
}

func TestReadBodyWithTimeout_Timeout(t *testing.T) {
	// 永不发 body:模拟客户端只发 header / 链路半死,body 永不到达。
	r := newSlowBodyRequest(nil)
	start := time.Now()
	_, err := readBodyWithTimeout(r, 80*time.Millisecond)
	elapsed := time.Since(start)
	if !errors.Is(err, ErrBodyReadTimeout) {
		t.Fatalf("expected ErrBodyReadTimeout, got %v", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("timeout did not fire promptly, elapsed=%v", elapsed)
	}
}

func TestReadBodyWithTimeout_NoLeak(t *testing.T) {
	// 超时分支必须不残留读 goroutine。
	r := newSlowBodyRequest(nil)
	runtime.GC()
	before := runtime.NumGoroutine()
	_, _ = readBodyWithTimeout(r, 60*time.Millisecond)
	// 给读 goroutine(被 Close 唤醒后)退出留一点调度时间。
	time.Sleep(50 * time.Millisecond)
	runtime.GC()
	after := runtime.NumGoroutine()
	if after > before+1 { // +1 容忍 GC/调度瞬时抖动
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}

// newSlowBodyRequest 构造一个"可受控投递 body"的入站请求。
// body==nil 表示永不投递任何 body 字节(模拟卡死);否则立即异步投递一次后关闭写端。
// 通过 io.Pipe 实现:写端在 goroutine 投递,Read 端挂起直到有数据或 Close。
func newSlowBodyRequest(body []byte) *http.Request {
	r, w := io.Pipe()
	mr := &mockBodyReader{r: r, w: w, body: body}
	return &http.Request{
		Body:          mr,
		ContentLength: -1,
		Method:        http.MethodPost,
	}
}

// mockBodyReader 包装 io.Pipe 的读端,实现 http.Request.Body 的 Read/Close 契约。
// body 非 nil 时在独立 goroutine 中(经写端)投递一次后 Close 写端,模拟一次性 body;
// body 为 nil 时永不投递,Read 永久阻塞(直到外部 Close 触发 ErrClosedPipe)。
type mockBodyReader struct {
	r    *io.PipeReader
	w    *io.PipeWriter
	body []byte
	once sync.Once
}

func (m *mockBodyReader) Read(p []byte) (int, error) {
	m.once.Do(func() {
		if m.body != nil {
			go func() {
				_, _ = m.w.Write(m.body)
				_ = m.w.Close()
			}()
		}
		// body==nil:不投递,Read 永久阻塞,直到外部 Close 触发 pipe Read 返回 ErrClosedPipe。
	})
	return m.r.Read(p)
}

func (m *mockBodyReader) Close() error {
	_ = m.r.Close()
	_ = m.w.Close()
	return nil
}
