package relay

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

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
