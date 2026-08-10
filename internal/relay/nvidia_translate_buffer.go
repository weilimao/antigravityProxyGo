package relay

import (
	"context"
	"io"
)

// nvidia_translate_buffer.go: NVIDIA 流式链路的 sseEventSink 抽象内核。
//
// 仅保留「取消即断」helper(watchCancel)与 SSE 事件写入目标接口(sseEventSink)两件所有 sink
// 实现共享的基础设施,其余按 sink 类型物理拆分到同目录 5 个兄弟文件:
//   - nvidia_flush_writer.go   : flushWriter  实时逐帧 flush 写客户端 socket(含 deferred 延迟 WriteHeader)
//   - nvidia_replay_writer.go  : replayWriter 蓄流回内存 buffer + replayBodyInto/replayFollowingInto 回放
//   - nvidia_sse_frame_scan.go : anthropicSSEFrame 帧扫描 + content_block JSON 解析/改写辅助
//   - nvidia_tee_sink.go       : teeSink      混合模式双写(思考/正文实时推 live + 蓄流兜底)
//   - nvidia_resume_sink.go    : resumeSink   重试轮续传(补闭合 + index 重映射,不重发草稿)
//
// 本文件从原 955 行单体抽离,仅作物理搬移,逻辑与原文件逐行等价。
// watchCancel 监听 ctx 取消：一旦 ctx 被撤销(客户端主动断开 / 请求超时),
// 立即 Close 上游 resp.Body,使阻塞在 bufio.Scanner.Scan() 上的读循环以
// "read on closed body" 错误立即返回,从而跳出逐帧回写的主循环。
//
// 这是 NVIDIA 流式链路"取消即断"的唯一可靠触发点 —— 不依赖下游写错检测
// (存在竞态:客户端断开时若 scanner 正好在两帧之间阻塞读,写错不会触发)。
// ctx.Done() 由 net/http 在客户端 TCP 半关闭时确定性触发,无竞态。
//
// 对齐谷歌链路 handler.go:1131-1140 的 cancelChan 监听协程,但抽成可复用 helper,
// 供 NVIDIA 三条流式回写路径(Anthropic / Responses / OpenAI 透传)统一接入。
//
// 返回 stop 函数:defer 调用以释放监听 goroutine,避免泄漏。
//   body 为上游响应体;调用方负责保证只在 ctx 取消时由本 helper 触发 Close,
//   正常流式读完时 scanner 先返回 EOF,主循环退出后 defer stop() 释放 goroutine,
//   body 的最终 Close 仍由各路径既有 defer resp.Body.Close() 负责。
func watchCancel(ctx context.Context, body io.ReadCloser) (stop func()) {
	stopped := make(chan struct{})
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			// 客户端已断开:主动切断上游连接,让阻塞的 Scan() 立即返回
			_ = body.Close()
		case <-stopped:
			// 正常收尾:主循环已退出,无需 Close(由既有的 defer resp.Body.Close() 兜底)
		}
		close(done)
	}()
	return func() {
		select {
		case <-stopped:
		default:
			close(stopped)
		}
		<-done
	}
}

// sseEventSink 抽象 SSE 事件写入目标。两类实现:
//   - flushWriter:边读上游边把 Anthropic SSE 事件逐帧 flush 到客户端 TCP socket(实时流式);
//   - replayWriter:把整条转译结果蓄流进内存 bytes.Buffer,供上游断流重试场景攒全量再回放。
//
// 抽象该接口使 OpenAIChatSSEToAnthropicSSE 的转译逻辑与"写往哪里"解耦:
// 蓄流回放链路(writeNvidiaAnthropicStream)先用 replayWriter 在内存攒出完整 Anthropic SSE,
// 断流可丢弃本次 buffer 原账号重拉上游(≤5×5s),整条 ready 后再把 buffer 逐帧 flush 给客户端,
// 客户端在重试期间未收到任何字节,不会出现"半截内容冲突"。
type sseEventSink interface {
	writeEvent(event, data string)
	writeRaw(s string)
	flush()
}
