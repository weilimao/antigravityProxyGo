package relay

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// nvidiaInboundReadTimeout 是 NVIDIA 中继入站请求体的最大读取等待时长。
// 客户端(Claude Code 等)正常情况下会在数百毫秒内发完整请求体;
// 超过该窗口仍未读完整 body,判定为客户端"发了 header 不发 body"或入站链路半死
// (TCP keep-alive 最快 2 小时才探测到死连接,期间 Read 无限挂起),强制中断读取并
// 回写 408,把"handler 永久卡死在 io.ReadAll"退化为"快速失败 + 客户端自动换连接重试"。
// 60s 足以覆盖跨网/弱网客户端的 body 上传延迟,远小于"永久挂起"的不可接受代价。
const nvidiaInboundReadTimeout = 60 * time.Second

// ErrBodyReadTimeout 是 readBodyWithTimeout 在超时分支返回的哨兵错误。
// 调用方用 errors.Is 判定后回写 408 Request Timeout(区别于普通读失败的 400)。
var ErrBodyReadTimeout = errors.New("inbound request body read timeout")

// readBodyWithTimeout 在 timeout 内读取完整入站请求体。
//
// 设计动机:net/http 的 http.Server 在零超时配置下对 r.Body.Read() 不做时间兜底,
// 底层 net.Conn.Read 是阻塞系统调用——socket 无数据且连接未 RST 时,goroutine 被
// runtime park 永久挂起(系统级 TCP keep-alive 最快 7200s 才探测,应用层无法及时感知)。
// 此前 handleNvidia 入口 io.ReadAll(r.Body) 一旦撞上此类半死连接,handler 即被钉死,
// 后续所有上游日志(🟢)与响应写出全部停摆,表现为"请求进不去终极服务"。
//
// 实现:独立 goroutine 读 body,主流程 select 监听「读结果」与「超时」:
//   - 正常读完 → 返回 (body, nil);
//   - 超时 → r.Body.Close() 唤醒底层 Read(立即返回错误),读 goroutine 收到 error 后
//     向 buffered chan 投递结果即退出,主流程返回 ErrBodyReadTimeout。
//
// 无 goroutine 泄漏保证:
//   - ch 为 buffered(cap=1),超时分支主流程虽不再读 ch,但读 goroutine 的投递永不阻塞,
//     退出干净;TestReadBodyWithTimeout_NoLeak 用 runtime.NumGoroutine 显式断言不增长。
//   - 回收兜底:就算极端情况下 Read goroutine 的 io.ReadAll 因 Close 时序刚好读到数据并
//     提前成功返回,buffered chan 也能吞下该结果,goroutine 依旧退出。两条路径都收敛。
//
// 仅作用于入站 body 读取阶段;不触碰出站流式(NVIDIA SSE 长生成)写出,与 streamClient
// 的无超时行为互不影响。
func readBodyWithTimeout(r *http.Request, timeout time.Duration) ([]byte, error) {
	if timeout <= 0 {
		// 零/负超时退化为不可超时读取,保留对未显式配置调用方的兼容。
		return io.ReadAll(r.Body)
	}

	type result struct {
		b   []byte
		err error
	}
	ch := make(chan result, 1)

	go func() {
		b, err := io.ReadAll(r.Body)
		ch <- result{b: b, err: err}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-timer.C:
		// 超时:主动 Close body,唤醒挂在 net.Conn.Read 上的读 goroutine,
		// 使其 io.ReadAll 立即收到 error 并退出,杜绝泄漏。
		_ = r.Body.Close()
		return nil, ErrBodyReadTimeout
	case res := <-ch:
		return res.b, res.err
	}
}

// inboundKindOfPath 根据入站路径推断 NVIDIA 入站协议类型,供超时/错误回写时
// 选择与"正常成功路径"一致的错误结构(inboundKind)。在 body 读取阶段(协议解析
// 尚未完成)即需回写超时错误时使用,确保 408 错误结构与客户端预期协议对齐,
// Claude Code 等 Anthropic 客户端能识别并自动换连接重试。
// 返回 "anthropic" | "responses" | "openai_chat",无法判定时回退 "openai_chat"。
func inboundKindOfPath(path string) string {
	switch {
	case strings.HasSuffix(path, "/v1/messages"):
		return "anthropic"
	case strings.HasSuffix(path, "/v1/responses"):
		return "responses"
	default:
		return "openai_chat"
	}
}

// writeNvidiaInboundTimeout 把入站 body 读取超时回写为客户端能识别的标准错误。
// Anthropic 入站按 {"type":"error","error":{"type":"request_timeout","message":...}}
// 结构回写(Claude Code 据此识别后自动重开连接重试,而非卡死);Responses/OpenAI Chat
// 入站回写各自标准错误结构。状态码统一 408 Request Timeout。
func writeNvidiaInboundTimeout(w http.ResponseWriter, inboundKind string) {
	const msg = "request body read timed out waiting for client; please retry"

	switch inboundKind {
	case "anthropic":
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusRequestTimeout)
		payload, _ := json.Marshal(map[string]interface{}{
			"type": "error",
			"error": map[string]interface{}{
				"type":    "request_timeout",
				"message": msg,
			},
		})
		_, _ = w.Write(payload)
	case "responses":
		// Responses API(codex /v1/responses)沿用 OpenAI 错误结构,
		// Codex CLI 同样按 error.message 识别失败并重试。
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusRequestTimeout)
		payload, _ := json.Marshal(map[string]interface{}{
			"error": map[string]interface{}{
				"message": msg,
				"type":    "request_timeout",
				"code":    "request_timeout",
			},
		})
		_, _ = w.Write(payload)
	default:
		// openai_chat
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusRequestTimeout)
		payload, _ := json.Marshal(map[string]interface{}{
			"error": map[string]interface{}{
				"message": msg,
				"type":    "request_timeout",
				"code":    "request_timeout",
			},
		})
		_, _ = w.Write(payload)
	}
}
