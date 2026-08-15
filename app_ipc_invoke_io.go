package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"antigravity-proxy/internal/db"
	"antigravity-proxy/internal/dialogs"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// handleIOInvokeIPC 处理 IPCInvoke 的日志/详情通道:request:get-details/
// retry-error-logs:get/clear/export/request-logs:export。从 app_ipc.go IPCInvoke 尾部
// switch 抽离(段A request:get-details + 段BC retry-error-logs 串 + request-logs:export),
// 2-tuple marshalResponse 改写为 3-tuple(子处理器返 (string, bool, error)),未命中 fall-through。
// 逻辑逐行等价;sibling 内 case 重排,channel 互不重叠 first-match 无功能差异。
// request-logs:export 经 db.QueryAllRequestLogs 查询数据库,CSV/JSON 双格式导出。
func (a *App) handleIOInvokeIPC(channel string, args []interface{}) (string, bool, error) {
	marshalResponse := func(val interface{}) (string, bool, error) {
		b, err := json.Marshal(val)
		if err != nil {
			return `{"success":false,"error":"JSON serialization error"}`, true, nil
		}
		return string(b), true, nil
	}

	getStringArg := func(idx int) string {
		if idx < len(args) {
			if s, ok := args[idx].(string); ok {
				return s
			}
		}
		return ""
	}

	switch channel {
	case "request:get-details":
		// On-demand fetch of the (truncated) requestBody / requestHeaders for
		// the details modal. The hot-path stats-updated payload only carries
		// scalar request metadata, so heavy bodies are pulled here when the
		// user actually opens a log entry. Returns nil when the id has aged
		// out of the 50-entry recent window (or in remote mode).
		reqID := getStringArg(0)
		body, headers := a.statsTracker.GetRequestDetails(reqID)
		return marshalResponse(map[string]interface{}{
			"requestBody":    body,
			"requestHeaders": headers,
		})

	case "retry-error-logs:get":
		return marshalResponse(a.errLogger.GetLogs())

	case "retry-error-logs:clear":
		logType := getStringArg(0)
		a.errLogger.ClearLogs(logType)
		a.statsTracker.ClearRetriesOrErrors(logType)
		wailsRuntime.EventsEmit(a.ctx, "stats-updated", a.getStatsPayload(false))
		return marshalResponse(true)

	case "retry-error-logs:export":
		logs := a.errLogger.GetLogs()
		filePath, ok, _ := a.dialogSvc.Save(a.ctx, dialogs.SaveRequest{
			Title:       "导出重试与报错日志",
			DefaultName: fmt.Sprintf("antigravity_retry_error_logs_%d.json", time.Now().Unix()),
			Filters: []dialogs.FileFilter{
				{DisplayName: "JSON Files", Pattern: "*.json"},
				{DisplayName: "CSV Files", Pattern: "*.csv"},
			},
		})
		if !ok {
			return marshalResponse(false)
		}

		var content []byte
		if strings.HasSuffix(filePath, ".csv") {
			var csv strings.Builder
			csv.WriteString("\uFEFF时间,类型,尝试/状态,账号,目标模型,接口路径,错误/异常详情\n")
			for _, log := range logs {
				logType := "最终失败"
				if log.Type == "RETRY" {
					logType = fmt.Sprintf("第 %d 次", log.Attempt)
				}
				csv.WriteString(fmt.Sprintf("\"%s\",\"%s\",\"%s\",\"%s\",\"%s\",\"%s\",\"%s\"\n",
					log.Timestamp, log.Type, logType, log.Account, log.Model, log.Path, strings.ReplaceAll(log.Error, "\"", "\"\"")))
			}
			content = []byte(csv.String())
		} else {
			content, _ = json.MarshalIndent(logs, "", "  ")
		}

		_ = os.WriteFile(filePath, content, 0644)
		a.dialogSvc.RevealFile(filePath)
		return marshalResponse(true)

	case "request-logs:export":
		logs, err := db.QueryAllRequestLogs()
		if err != nil {
			a.AddLog(fmt.Sprintf("❌ [请求日志导出] 查询数据库失败: %v", err))
			return marshalResponse(false)
		}
		filePath, ok, _ := a.dialogSvc.Save(a.ctx, dialogs.SaveRequest{
			Title:       "导出请求日志",
			DefaultName: fmt.Sprintf("antigravity_request_logs_%d.json", time.Now().Unix()),
			Filters: []dialogs.FileFilter{
				{DisplayName: "JSON Files", Pattern: "*.json"},
				{DisplayName: "CSV Files", Pattern: "*.csv"},
			},
		})
		if !ok {
			return marshalResponse(false)
		}

		var content []byte
		if strings.HasSuffix(filePath, ".csv") {
			var csv strings.Builder
			csv.WriteString("\uFEFF时间,模式,账号/用户,请求方式,域名,路径,模型,输入Token,输出Token,缓存Token,总成本,首帧响应时间(ms),耗时(ms),状态码,会话ID\n")
			for _, log := range logs {
				formattedTime := log.Timestamp
				if t, err := time.Parse(time.RFC3339, log.Timestamp); err == nil {
					formattedTime = t.Local().Format("2006-01-02 15:04:05")
				}
				csv.WriteString(fmt.Sprintf("\"%s\",\"%s\",\"%s\",\"%s\",\"%s\",\"%s\",\"%s\",%d,%d,%d,%f,%d,%d,%d,\"%s\"\n",
					formattedTime, log.Mode, log.UserID, log.Method, log.Host, log.Path, log.ModelName,
					log.InTokens, log.OutTokens, log.CachedTokens, log.Cost, log.FirstByteMs, log.DurationMs, log.StatusCode, log.SessionID))
			}
			content = []byte(csv.String())
		} else {
			content, _ = json.MarshalIndent(logs, "", "  ")
		}

		if err := os.WriteFile(filePath, content, 0644); err != nil {
			a.AddLog(fmt.Sprintf("❌ [请求日志导出] 写盘失败: %v", err))
			return marshalResponse(false)
		}
		a.AddLog(fmt.Sprintf("📥 [请求日志导出] 成功导出请求日志到: %s", filePath))
		a.dialogSvc.RevealFile(filePath)
		return marshalResponse(true)
	}

	return "", false, nil
}
