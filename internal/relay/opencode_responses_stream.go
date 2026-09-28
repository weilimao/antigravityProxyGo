package relay

import (
	"bufio"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// opencode_responses_stream.go: Responses SSE → Chat SSE 实时逐帧转换。
//
// 设计动机(2026-09-27 实测):
//   此前 Responses-only 模型(如 muse-spark-*)的流式请求走「先聚合完整响应, 再重建
//   SSE 一次性发出」的路径。实测一个长输出请求(约 22K 字符)出现 172 秒完全无输出、
//   随后全部内容瞬间涌出 —— 客户端侧表现为「卡死」; 若中途连接抖动或上游流被截断,
//   则表现为「输出一半就停住」。根因是聚合必须先读完整个流, 牺牲了实时性。
//
//   本文件改为**逐帧实时转换**: 读到上游一帧 Responses SSE 就立即产出对应 Chat SSE
//   帧并 flush, 使客户端看到逐字输出, 且上游中断时已产出的内容不会丢失。
//
// 事件映射(Responses → Chat):
//   response.created / response.in_progress → 首帧 role=assistant(仅一次)
//   response.output_text.delta              → choices[0].delta.content
//   response.reasoning_*.delta              → choices[0].delta.reasoning_content
//   response.output_item.done(function_call) → choices[0].delta.tool_calls(整块)
//   response.completed                      → 末帧 finish_reason + usage
//   response.failed / error                 → 以错误帧结束
//
// 与 aggregateOpenAIResponsesSSE 的分工:
//   - 客户端要流式 → 本文件(实时转换, 保持低延迟);
//   - 客户端要非流式 → opencode_responses_aggregate.go(聚合后一次返回)。

// responsesSSEToChatSSE 读取上游 Responses SSE, 实时转换为 Chat SSE 写入 writer。
//
// 返回 (inputTokens, outputTokens, cachedTokens, err)。
// 转换过程中每产出一帧都会调用 flush(若非 nil), 保证客户端立即收到。
func responsesSSEToChatSSE(
	reader io.Reader,
	writer io.Writer,
	model string,
	flush func(),
) (input, output, cached int, err error) {
	scanner := bufio.NewScanner(reader)
	// 单帧可能很大(response.created 携带完整 tools 定义), 放宽至 8MB。
	scanner.Buffer(make([]byte, 0, 256*1024), 8*1024*1024)

	var (
		respID      string
		createdAt   int64
		modelOut    string
		finishRsn   string
		hasUsage    bool
		sawAnyFrame bool
	)

	emit := func(delta map[string]interface{}, finish interface{}, usage map[string]interface{}) {
		frame := map[string]interface{}{
			"id":      respID,
			"object":  "chat.completion.chunk",
			"created": createdAt,
			"model":   modelOut,
			"choices": []map[string]interface{}{
				{"index": 0, "delta": delta, "finish_reason": finish},
			},
		}
		if usage != nil {
			frame["usage"] = usage
		}
		b, mErr := json.Marshal(frame)
		if mErr != nil {
			return
		}
		if _, wErr := writer.Write([]byte("data: ")); wErr != nil {
			diagLastWriteErr = wErr.Error()
			return
		}
		if _, wErr := writer.Write(b); wErr != nil {
			diagLastWriteErr = wErr.Error()
			return
		}
		if _, wErr := writer.Write([]byte("\n\n")); wErr != nil {
			diagLastWriteErr = wErr.Error()
			return
		}
		if flush != nil {
			flush()
		}
	}

	// 立即发出首帧(role), 不等上游首个事件。
	//
	// 必要性(2026-09-27 实测): reasoning 模型(如 muse-spark-*)在长任务下
	// 思考阶段可达 30-60 秒才开始输出文本。若首帧也等到上游有数据才发,
	// 客户端在这段时间内收不到任何字节, 会判定为"流未建立/超时"而主动断开
	// (实测 curl 报 context canceled, opencode CLI 报 InvalidHTTPResponse)。
	// 提前发出 role 帧使 SSE 流立即建立, 客户端保持等待。
	respID = "chatcmpl-opencode-stream"
	modelOut = model
	emit(map[string]interface{}{"role": "assistant", "content": ""}, nil, nil)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}

		var raw map[string]interface{}
		if jErr := json.Unmarshal([]byte(payload), &raw); jErr != nil {
			// 畸形帧跳过, 不中断流。
			continue
		}

		evType, _ := raw["type"].(string)
		if evType == "" {
			continue
		}

		// 错误帧: 立即以错误结束(已产出的内容保留在客户端)。
		if evType == "error" {
			msg, typ := extractResponsesError(raw)
			return input, output, cached, &openCodeUpstreamError{Type: typ, Message: msg}
		}
		if evType == "response.failed" {
			msg, typ := extractResponsesErrorFromResponse(raw)
			return input, output, cached, &openCodeUpstreamError{Type: typ, Message: msg}
		}

		sawAnyFrame = true

		// 元信息 + 首帧 role
		if resp, ok := raw["response"].(map[string]interface{}); ok {
			if v, ok := resp["id"].(string); ok && v != "" && respID == "" {
				respID = v
			}
			if v, ok := resp["model"].(string); ok && v != "" {
				modelOut = v
			}
			if v, ok := resp["created_at"].(float64); ok && createdAt == 0 {
				createdAt = int64(v)
			}
		}
		if respID == "" {
			respID = "chatcmpl-opencode-stream"
		}
		if modelOut == "" {
			modelOut = model
		}

		switch evType {
		case "response.output_text.delta":
			if s, ok := raw["delta"].(string); ok && s != "" {
				emit(map[string]interface{}{"content": s}, nil, nil)
			}

		case "response.reasoning_text.delta", "response.reasoning_summary_text.delta":
			if s, ok := raw["delta"].(string); ok && s != "" {
				emit(map[string]interface{}{"reasoning_content": s}, nil, nil)
			}

		case "response.output_item.done":
			// function_call 项在 done 事件携带完整 arguments → 整块作为 tool_calls 增量。
			if item, ok := raw["item"].(map[string]interface{}); ok {
				if t, _ := item["type"].(string); t == "function_call" {
					name, _ := item["name"].(string)
					args, _ := item["arguments"].(string)
					callID, _ := item["call_id"].(string)
					if callID == "" {
						callID, _ = item["id"].(string)
					}
					if name != "" {
						emit(map[string]interface{}{
							"tool_calls": []map[string]interface{}{
								{
									"index": 0,
									"id":    callID,
									"type":  "function",
									"function": map[string]interface{}{
										"name":      name,
										"arguments": args,
									},
								},
							},
						}, nil, nil)
						finishRsn = "tool_calls"
					}
				}
			}

		case "response.completed":
			if resp, ok := raw["response"].(map[string]interface{}); ok {
				if u, ok := resp["usage"].(map[string]interface{}); ok && len(u) > 0 {
					cu := parseResponsesUsage(u)
					input, output, cached = cu.PromptTokens, cu.CompletionTokens, cu.CachedTokens()
					hasUsage = true
				}
			}
			if finishRsn == "" {
				finishRsn = "stop"
			}
		}
	}

	if sErr := scanner.Err(); sErr != nil {
		// 诊断: 记录扫描中断时的已产出规模, 用于区分"上游断流"与"本地管道问题"。
		diagLastScanErr = sErr.Error()
		return input, output, cached, sErr
	}
	if !sawAnyFrame {
		return input, output, cached, &openCodeUpstreamError{
			Type: "empty_stream", Message: "upstream returned no usable Responses SSE frame"}
	}

	// 末帧: finish_reason + usage
	if finishRsn == "" {
		finishRsn = "stop"
	}
	var usage map[string]interface{}
	if hasUsage {
		usage = map[string]interface{}{
			"prompt_tokens":     input,
			"completion_tokens": output,
			"total_tokens":      input + output,
		}
	}
	emit(map[string]interface{}{}, finishRsn, usage)
	_, _ = writer.Write([]byte("data: [DONE]\n\n"))
	if flush != nil {
		flush()
	}
	return input, output, cached, nil
}

// pipeReadCloser 包装 io.Pipe 的读写两端, 使 Close 时同时关闭两端。
//
// 必要性: 下游 SSE 转换器(OpenAIChatSSEToAnthropicSSE 等)在客户端断开时
// 会调用传入的 ReadCloser.Close() 以唤醒阻塞的读。若只包 NopCloser(pr),
// 关闭 pr 不会解除生产端 pw.Write 的阻塞, 导致 goroutine 泄漏与整个管道死锁
// (实测表现为客户端收到 InvalidHTTPResponse、服务端 handler 永久挂起)。
// 同时关闭 pw 可立即让生产端的 Write 返回 ErrClosedPipe 并退出。
// diagLastScanErr / diagLastWriteErr 记录最近一次扫描与写入错误, 仅供诊断日志使用。
var diagLastScanErr string
var diagLastWriteErr string

type pipeReadCloser struct {
	pr       *io.PipeReader
	pw       *io.PipeWriter
	upstream io.Closer // 上游响应体; Close 时一并关闭以解除读上游的阻塞
	once     sync.Once
}

func (p *pipeReadCloser) Read(b []byte) (int, error) { return p.pr.Read(b) }

func (p *pipeReadCloser) Close() error {
	var err error
	p.once.Do(func() {
		if p.upstream != nil {
			_ = p.upstream.Close()
		}
		_ = p.pr.Close()
		err = p.pw.Close()
	})
	return err
}

// opencodeDirectClient 是 opencode 上游专用的直连 HTTP client(禁用代理)。
//
// 与 h.streamClient 的差异: 后者的 transport 走 netutil.NewTransport(), 其
// GetSystemProxy 在系统代理关闭时会降级使用"探测到的本地 VPN 代理"。实测长流
// (muse-spark-* 输出数万字符)经该代理转发时会在 30-70 秒后被切断, 报
// "http2: response body closed", 导致下游缺少收尾事件、客户端报错。
//
// 本 client 使用干净的 Transport(无 Proxy 字段 → 直连), 并显式关闭 HTTP/2,
// 规避 HTTP/2 流复用与代理交互带来的提前关闭问题。
var opencodeDirectClient = &http.Client{
	Transport: &http.Transport{
		Proxy:                 nil, // 显式直连, 不走任何代理
		MaxIdleConns:          50,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     false, // 用 HTTP/1.1, 长流更稳定
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
	},
}

// lockedResponseWriter 串行化对底层 ResponseWriter 的写入。
//
// 必要性(2026-09-27 实测): 保活 goroutine 与下游 SSE 转换器会并发写同一个 w。
// 转换器的 writeSSEFrame 分两次 WriteString("event: ...") 与 WriteString("data: ..."),
// 若保活 ping 恰好插在两者之间, 客户端会读到半帧并报
// "chunk hex-length char not a hex digit"(HTTP chunked 解析失败)。
// 故用本包装器把所有写入串行化, 保证每帧原子落盘。
type lockedResponseWriter struct {
	w  http.ResponseWriter
	mu *sync.Mutex
}

func (l *lockedResponseWriter) Header() http.Header { return l.w.Header() }

func (l *lockedResponseWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func (l *lockedResponseWriter) WriteHeader(code int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.w.WriteHeader(code)
}

func (l *lockedResponseWriter) Flush() {
	if f, ok := l.w.(http.Flusher); ok {
		f.Flush()
	}
}
