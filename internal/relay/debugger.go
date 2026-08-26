package relay

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// debuggerReqIDKey 是往 context.Context 中携带「debugger 同请求关联 ID」的私有 key,
// 用于把 handleNvidia 入口分配的 reqID 一路透传到下游 writeNvidiaAnthropicStream /
// pullAnthropicStreamWithRetry / openAIChatSSEToAnthropicSSEIntoPinned 等无须修改签名
// 的位置。
//
// 设计取舍:之所以用 context value 而不是改函数签名,是因为 NVIDIA+Anthropic 流式链路
// 的中间函数(translate/tee/resume/sink)数量多、调用层级深,签名层层外扩会污染所有
// 调用点,测试代码与回归用例需要同步跟着改一处;用 context value 后,仅 handleNvidia
// 入口注入 + pullAnthropicStreamWithRetry 出口提取,中间链路整体零改动。
type debuggerReqIDKey struct{}

// WithDebuggerReqID 把 reqID 注入 context, 下游 ExtractDebuggerReqID 提取;若无注入则
// 返回空串(调用方 fallback 到自己生成的默认 reqID)。
func WithDebuggerReqID(ctx context.Context, reqID string) context.Context {
	if ctx == nil {
		return nil
	}
	if strings.TrimSpace(reqID) == "" {
		return ctx
	}
	return context.WithValue(ctx, debuggerReqIDKey{}, reqID)
}

// ExtractDebuggerReqID 从 context 提取 reqID;未注入或为空时返回空串,由调用方决定
// 是否需要 fallback 到自身生成的 reqID(通常 NV 路径用 fmt.Sprintf("msg_nvidia_%d", ...))。
func ExtractDebuggerReqID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(debuggerReqIDKey{}).(string); ok {
		return v
	}
	return ""
}

// DebuggerLogger 提供中继层的全量请求/响应/Raw SSE 逐帧抓包日志落盘服务。
type DebuggerLogger struct {
	mu          sync.Mutex
	enabled     bool
	logDir      string
	activeFiles map[string]*os.File
}

var (
	globalDebugger     *DebuggerLogger
	onceGlobalDebugger sync.Once
)

// GetGlobalDebugger 获取全局 DebuggerLogger 单例。
func GetGlobalDebugger() *DebuggerLogger {
	onceGlobalDebugger.Do(func() {
		globalDebugger = &DebuggerLogger{
			enabled:     true,
			logDir:      "logs/debugger",
			activeFiles: make(map[string]*os.File),
		}
	})
	return globalDebugger
}

// Configure 配置 DebuggerLogger 开启状态与输出目录。
func (d *DebuggerLogger) Configure(enabled bool, dir string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.enabled = enabled
	if dir != "" {
		d.logDir = dir
	}
}

func (d *DebuggerLogger) IsEnabled() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.enabled
}

// getFileForReq 获取或新建给定 reqID 的日志文件句柄。
func (d *DebuggerLogger) getFileForReq(reqID string) (*os.File, error) {
	if f, ok := d.activeFiles[reqID]; ok && f != nil {
		return f, nil
	}
	if err := os.MkdirAll(d.logDir, 0755); err != nil {
		return nil, err
	}
	filename := fmt.Sprintf("%s_%s.log", time.Now().Format("20060102_150405"), reqID)
	fullPath := filepath.Join(d.logDir, filename)
	f, err := os.OpenFile(fullPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}
	d.activeFiles[reqID] = f
	return f, nil
}

// writeLine 格式化写入单条数据到日志文件。
func (d *DebuggerLogger) writeLine(reqID, format string, args ...interface{}) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.enabled {
		return
	}
	f, err := d.getFileForReq(reqID)
	if err != nil {
		return
	}
	ts := time.Now().Format("15:04:05.000")
	msg := fmt.Sprintf("[%s] ", ts) + fmt.Sprintf(format, args...) + "\n"
	_, _ = f.WriteString(msg)
	_ = f.Sync()
}

// LogClientRequest 记录客户端入站请求。
func (d *DebuggerLogger) LogClientRequest(reqID, method, path string, headers map[string][]string, body []byte) {
	d.writeLine(reqID, "==== 📥 客户端入站请求 ====")
	d.writeLine(reqID, "Method: %s | Path: %s", method, path)
	d.writeLine(reqID, "Headers: %v", headers)
	if len(body) > 0 {
		d.writeLine(reqID, "Request Body:\n%s", string(body))
	} else {
		d.writeLine(reqID, "Request Body: (empty)")
	}
}

// LogUpstreamRequest 记录发往上游的物理请求。
func (d *DebuggerLogger) LogUpstreamRequest(reqID, targetURL string, headers map[string][]string, body []byte) {
	d.writeLine(reqID, "\n==== 🟢 发往上游请求 ====")
	d.writeLine(reqID, "Target URL: %s", targetURL)
	d.writeLine(reqID, "Headers: %v", headers)
	if len(body) > 0 {
		d.writeLine(reqID, "Upstream Body:\n%s", string(body))
	}
}

// LogUpstreamResponse 记录上游响应状态码及 Header。
func (d *DebuggerLogger) LogUpstreamResponse(reqID string, statusCode int, headers map[string][]string) {
	d.writeLine(reqID, "\n==== ⬅️ 上游响应 Header ====")
	d.writeLine(reqID, "Status Code: %d", statusCode)
	d.writeLine(reqID, "Headers: %v", headers)
}

// LogUpstreamFrame 逐帧记录上游返回的 Raw SSE 数据包。
func (d *DebuggerLogger) LogUpstreamFrame(reqID string, rawChunk string) {
	d.writeLine(reqID, "  [上游 Raw SSE 帧]: %s", rawChunk)
}

// LogClientFrame 逐帧记录发送给客户端的原始 SSE 数据包。
func (d *DebuggerLogger) LogClientFrame(reqID, eventType, data string) {
	d.writeLine(reqID, "  [客户端转译 SSE 帧 | event: %s]: %s", eventType, data)
}

// LogUpstreamResponseBody 记录上游响应的 body 原文(仅取一次,记录完整内容)。
// 用于 debugging Anthropic 路径上"上游到底回了什么"的实际形态 —— 例如排查
// kimi-k3 + Claude Code 工具调用中断时,需要看清上游在协议级是返回了 error 帧、
// 空 finish_reason、还是 truncated SSE。
//
// 调用方约定:
//   - 同一个 reqID 只应该调用一次(流式重试时,由调用方在循环外做"最终态"聚合后再调);
//   - body 为已读完的完整上游响应体(非流式)或聚合后的 raw SSE 字节流(流式);
//   - 严格一次写一行,内部使用 bufio 与既有 debugger 同文件,与 LogClientRequest 形成
//     📥入站 → 🟢出站上链 → 📥上游响应内容 完整闭环。
// 为在 SSE 流式链路下避免单行日志过长,body 超过 maxDebuggerBodyBytes 会被截断
// 并附 (truncated) 后缀;原始完整 body 永不形成日志丢失(其已流入Anthropic SSE
// 转换层,可通过 LogUpstreamRawFrame 逐行查看)。
const maxDebuggerBodyBytes = 1 << 20 // 1MB,超阈值截断保护(单个 debugger log 文件不会爆盘)

func (d *DebuggerLogger) LogUpstreamResponseBody(reqID string, body []byte) {
	if len(body) == 0 {
		d.writeLine(reqID, "\n==== 📥 上游响应 Body ====\n(empty)")
		return
	}
	display := string(body)
	truncated := false
	if len(body) > maxDebuggerBodyBytes {
		display = string(body[:maxDebuggerBodyBytes])
		truncated = true
	}
	if truncated {
		d.writeLine(reqID, "\n==== 📥 上游响应 Body ====(已截断,前 %d 字节)\n%s\n...(truncated)", maxDebuggerBodyBytes, display)
	} else {
		d.writeLine(reqID, "\n==== 📥 上游响应 Body ====\n%s", display)
	}
}

// LogUpstreamRawFrame 逐字节记录上游原始 SSE 帧(行级),作为 LogUpstreamFrame 的同义别名:
// 历史上 LogUpstreamFrame 在 NVIDIA 链路里没有被任何生产代码调用,存在误导性;
// 此处提供本函数的明确目的注释,NVIDIA+Anthropic 流式链路在 pullAnthropicStreamWithRetry
// 内通过 io.TeeReader 把上游字节流镜像一份到 debugger buffer 后由 LogUpstreamResponseBody
// 落盘,LogUpstreamFrame 仅作"SSE 帧级标记"(可省略,保持向后兼容)。
func (d *DebuggerLogger) LogUpstreamRawFrame(reqID string, rawChunk string) {
	d.LogUpstreamFrame(reqID, rawChunk)
}

// CloseReq 关闭指定 reqID 的日志文件句柄。
func (d *DebuggerLogger) CloseReq(reqID string, summary string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if f, ok := d.activeFiles[reqID]; ok && f != nil {
		ts := time.Now().Format("15:04:05.000")
		_, _ = f.WriteString(fmt.Sprintf("[%s] ==== 🏁 请求处理结束 (%s) ====\n", ts, summary))
		_ = f.Close()
		delete(d.activeFiles, reqID)
	}
}
