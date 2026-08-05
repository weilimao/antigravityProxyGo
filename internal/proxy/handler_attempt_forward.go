package proxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// handler_attempt_forward.go: forwardForAttempt —— 建请求 + client.Do + 流式/非流式读取 + thoughtSignature 提取。
// 从 ServeHTTP 闭包 attemptRequest 的 forward 段逐行搬移,逻辑零回归。
// 流式断流(客户端早断/流中途断/STREAM_INTERRUPTED/读失败/建请求失败)返回 finalized=true 直接交重试循环。
// 流式成功正常 EOF 与非流式读取完成返回 finalized=false,交 classify 继续。

func (sc *serveContext) forwardForAttempt(attemptIndex int, ro *routeOutcome) forwardOutcome {
	// Forward request
	targetUrl := "https://" + ro.targetHost + ro.targetPath

	// Clean up potential credential conflicts before sending to Google
	if ro.customHeaders.Get("Authorization") != "" {
		ro.customHeaders.Del("x-goog-api-key")
		ro.customHeaders.Del("X-Goog-Api-Key")
		if parsedUrl, err := url.Parse(targetUrl); err == nil {
			q := parsedUrl.Query()
			if q.Get("key") != "" {
				q.Del("key")
				parsedUrl.RawQuery = q.Encode()
				targetUrl = parsedUrl.String()
			}
		}
	}

	// 彻底剥离可能暴露中继分发的敏感内部请求头，防止被 Google 上游风控识别
	ro.customHeaders.Del("X-Relay-User-Id")
	ro.customHeaders.Del("X-Relay-Api-Key-Id")
	ro.customHeaders.Del("X-Antigravity-Original-Path")
	ro.customHeaders.Del("X-Antigravity-Original-Method")
	ro.customHeaders.Del("X-Antigravity-Req-ID")
	ro.customHeaders.Del("x-relay-user-id")
	ro.customHeaders.Del("x-relay-api-key-id")
	ro.customHeaders.Del("x-antigravity-original-path")
	ro.customHeaders.Del("x-antigravity-original-method")
	ro.customHeaders.Del("x-antigravity-req-id")

	// 剥离多余的 Content-Length 与 Host 标头，使 Header 集合与图一 100% 精准一致
	ro.customHeaders.Del("Content-Length")
	ro.customHeaders.Del("content-length")
	ro.customHeaders.Del("Host")
	ro.customHeaders.Del("host")

	// 统一伪装 User-Agent 为正规官方客户端格式 (与图一完全一致)
	ua := ro.customHeaders.Get("User-Agent")
	if ua == "" || strings.Contains(strings.ToLower(ua), "go-http-client") || strings.Contains(ua, "2.2.1") {
		ro.customHeaders.Set("User-Agent", "antigravity/hub/2.3.1 (aidev_client; os_type=windows; arch=amd64)")
	}

	timeoutSec := 300
	if sc.h.getRequestTimeout != nil {
		if val := sc.h.getRequestTimeout(); val > 0 {
			timeoutSec = val
		}
	}
	ctx, cancel := context.WithTimeout(sc.r.Context(), time.Duration(timeoutSec)*time.Second)
	defer cancel()
	proxyReq, errReq := http.NewRequestWithContext(ctx, sc.r.Method, targetUrl, bytes.NewReader(ro.finalReqBody))
	if errReq != nil {
		return forwardOutcome{status: 0, err: errReq, finalized: true}
	}
	proxyReq.Header = ro.customHeaders

	resp, errDo := sc.h.client.Do(proxyReq)
	if errDo != nil {
		// status=0: 重试循环兜底置 502 BadGateway(对应原 L1075/L1081,TestProxyHandler_Timeout 依赖)
		return forwardOutcome{status: 0, err: errDo, finalized: true}
	}
	defer resp.Body.Close()

	var respBodyBytes []byte
	var errRead error
	isStreaming := strings.Contains(ro.targetPath, "streamGenerateContent") || strings.Contains(ro.targetPath, "alt=sse")

	if isStreaming && resp.StatusCode == 200 {
		if !sc.headersSent {
			// Copy headers to writer
			for k, values := range resp.Header {
				for _, v := range values {
					sc.w.Header().Add(k, v)
				}
			}
			sc.w.Header().Del("Content-Length")
			sc.w.WriteHeader(resp.StatusCode)
			sc.headersSent = true
		}

		flusher, hasFlusher := sc.w.(http.Flusher)
		if hasFlusher {
			flusher.Flush()
		}

		buf := make([]byte, 4096)
		var clientDisconnected bool = false
		var streamErr error = nil
		var malformedFunctionCall bool = false
		var writeMu sync.Mutex
		var sentBytes []byte

		// 启动 SSE 心跳保活协程：如果长时间读取上游无响应/Prefill耗时极长，定期向下游写入标准 SSE 注释帧
		heartbeatStopChan := make(chan struct{})
		go func() {
			ticker := time.NewTicker(10 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					// SSE 注释帧，冒号开头，无害且持续重置客户端/网关 ReadTimeout
					writeMu.Lock()
					_, hbErr := sc.w.Write([]byte(": keep-alive (antigravity-proxy heartbeat)\n\n"))
					if hbErr == nil && hasFlusher {
						flusher.Flush()
					}
					writeMu.Unlock()
					if hbErr != nil {
						return
					}
				case <-heartbeatStopChan:
					return
				case <-ctx.Done():
					return
				}
			}
		}()
		defer close(heartbeatStopChan)

		// 监听 Context 取消事件，一旦取消立即关闭上游响应体，强行中断阻塞的 Read
		cancelChan := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				_ = resp.Body.Close()
			case <-cancelChan:
			}
		}()
		defer close(cancelChan)

		for {
			n, errR := resp.Body.Read(buf)
			if n > 0 {
				chunk := buf[:n]
				if len(chunk) > 0 {
					// 检测 MALFORMED_FUNCTION_CALL：Google 上游在流中返回此 finishReason 时，
					// 表示模型生成了格式错误的空函数调用，属于不可重试的终端错误
					if !malformedFunctionCall && bytes.Contains(chunk, []byte("MALFORMED_FUNCTION_CALL")) {
						malformedFunctionCall = true
						sc.h.logFn(fmt.Sprintf("%s ⚠️ 检测到上游 MALFORMED_FUNCTION_CALL（模型生成了格式错误的空函数调用），通常因 thoughtSignature 未剥离或 toolConfig 缺失导致", sc.logPrefix))
					}
					writeMu.Lock()
					_, writeErr := sc.w.Write(chunk)
					if writeErr == nil && hasFlusher {
						flusher.Flush()
					}
					writeMu.Unlock()
					if writeErr != nil {
						clientDisconnected = true
						break
					}
					// 真实业务首帧写回成功：触发 TTFT 打点(幂等 sync.Once, 首字节即记录,
					// 心跳帧不经过本分支故不污染)。与 helpers.go 落日志侧 FirstByteMs 对齐。
					sc.firstByteRec.MarkFirstByte()
					// 仅在未超出 1MB 上限时追踪 sentBytes，避免流式传输内存无限增长
					const maxSentBytesTrack = 1 * 1024 * 1024 // 1MB
					if len(sentBytes) < maxSentBytesTrack {
						remaining := maxSentBytesTrack - len(sentBytes)
						if len(chunk) <= remaining {
							sentBytes = append(sentBytes, chunk...)
						} else {
							sentBytes = append(sentBytes, chunk[:remaining]...)
						}
					}

					// 提取 thoughtSignature 并缓存，下次请求注入到 functionCall parts
					// 保证 v1internal API 的思考链连续性，防止模型丢失上下文后重复生成失败的工具调用
					if sc.rawSessionKey != "" {
						GetSignatureCache().ExtractAndCacheSignatures(chunk, sc.rawSessionKey)
					}
				}
			}
			if errR != nil {
				if errR != io.EOF {
					sc.h.logFn(fmt.Sprintf("%s ⚠️ Read error during streaming: %v", sc.logPrefix, errR))
					streamErr = errR
				}
				break
			}
		}

		if clientDisconnected {
			// 客户端主动断开，标记明确日志，避免外层抓包当成正常的空响应
			sc.h.logFn(fmt.Sprintf("%s ⚠️ 下游客户端主动中断或超时关闭了 Socket 连接 (Early Disconnect)", sc.logPrefix))
			if len(sentBytes) == 0 {
				sentBytes = []byte("[WARN: CLIENT_EARLY_DISCONNECT - Downstream closed socket before stream completion]")
			}
			return forwardOutcome{status: resp.StatusCode, headers: resp.Header, body: sentBytes, streamed: true, finalized: true}
		}
		if streamErr != nil {
			if len(sentBytes) > 0 {
				// 已发送业务数据，HTTP 响应已提交，重试会导致两次响应拼接损坏。
				// 保留已发送的部分流正常结束（客户端可检测到流不完整并自行重试）。
				sc.h.logFn(fmt.Sprintf("%s ⚠️ 流式响应中途中断，已发送 %d 字节业务数据。为避免响应拼接损坏不再重试，保留已发送内容。", sc.logPrefix, len(sentBytes)))
				return forwardOutcome{status: resp.StatusCode, headers: resp.Header, body: sentBytes, streamed: true, finalized: true}
			}
			// 未发送任何业务数据，HTTP 响应实质未提交，可安全重试
			sc.h.logFn(fmt.Sprintf("%s ⚠️ 流式响应在发送业务数据前中断（仅可能发了心跳），将重试。", sc.logPrefix))
			return forwardOutcome{status: resp.StatusCode, headers: resp.Header, body: sentBytes, streamed: true, err: errors.New("STREAM_INTERRUPTED"), finalized: true}
		}
		respBodyBytes = sentBytes

		// 检测真空响应：finishReason:STOP 且无工具调用且无非空文本。
		// 注意：模型返回 functionCall 时 text 常为空，这是正常工具调用响应，不是空响应。
		// 只有既无 functionCall 又无非空 text 才是模型真正放弃输出。
		if len(respBodyBytes) > 0 && bytes.Contains(respBodyBytes, []byte(`"finishReason": "STOP"`)) {
			hasFunctionCall := bytes.Contains(respBodyBytes, []byte(`"functionCall"`))
			hasNonEmptyText := reNonEmptyText.Match(respBodyBytes)
			if !hasFunctionCall && !hasNonEmptyText {
				sc.h.logFn(fmt.Sprintf("%s [空响应诊断] finishReason=STOP 且无工具调用、无文本内容 - 模型未输出任何内容。常见原因: 模型在工具调用失败后放弃输出 / 上下文过长截断 / 上游临时异常。", sc.logPrefix))
			}
		}
	} else {
		respBodyBytes, errRead = io.ReadAll(io.LimitReader(resp.Body, sc.maxBodyBytes))
		if errRead != nil {
			// 非流式读失败:finalized 直返(对应原 closure L1241-1243 的 errRead 早返回)。
			// 返回真实上游 status + nil headers/body,与原 classify 返回签名逐行一致;
			// 重试循环见 err 非 sentinel -> 直连兜底或 429,逐行等价原行为。
			return forwardOutcome{status: resp.StatusCode, headers: nil, body: nil, streamed: false, err: errRead, finalized: true}
		}
		// 非流式:读完即写回,整体落盘时刻即为首字时刻(无流式 SSE 分帧语义),触发 TTFT 打点。
		sc.firstByteRec.MarkFirstByte()
	}
	// 流式成功正常 EOF 或非流式读取完成:不 finalized,交 classify 继续(对应原 L1218 落条到 L1234)。
	return forwardOutcome{
		status:    resp.StatusCode,
		headers:   resp.Header,
		body:      respBodyBytes,
		streamed:  isStreaming && resp.StatusCode == 200,
		err:       nil,
		finalized: false,
	}
}
