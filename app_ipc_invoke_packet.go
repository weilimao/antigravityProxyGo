package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"antigravity-proxy/internal/dialogs"
)

// handlePacketInvokeIPC 处理 IPCInvoke 的抓包通道:packet:get-all/analyze/download/
// export-log/export-single。从 app_ipc.go IPCInvoke 尾部 switch 抽离(段A get-all/analyze +
// 段B download/export-log/export-single,中间 accounts:update-2fa 留 hub 待 step12)。2-tuple
// marshalResponse 改写为 3-tuple(子处理器返 (string, bool, error)),未命中 fall-through。
// 逻辑逐行等价;sibling 内 case 连续重排,channel 互不重叠 first-match 无功能差异。
func (a *App) handlePacketInvokeIPC(channel string, args []interface{}) (string, bool, error) {
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
	case "packet:get-all":
		return marshalResponse(a.packetCap.GetPackets())

	case "packet:analyze":
		accId := getStringArg(0)
		sourceType := getStringArg(1)
		if sourceType == "" {
			sourceType = "ALL"
		}
		markdown, err := a.packetCap.AnalyzePackets(accId, sourceType)
		if err != nil {
			return marshalResponse(map[string]interface{}{"error": err.Error()})
		}
		return marshalResponse(markdown)
	case "packet:download":
		markdown := getStringArg(0)
		filePath, ok, _ := a.dialogSvc.Save(a.ctx, dialogs.SaveRequest{
			Title:       "保存 API 接口文档说明",
			DefaultName: "api_documentation.md",
			Filters:     []dialogs.FileFilter{{DisplayName: "Markdown Files", Pattern: "*.md"}},
		})
		if !ok {
			return marshalResponse(false)
		}
		_ = os.WriteFile(filePath, []byte(markdown), 0644)
		a.dialogSvc.RevealFile(filePath)
		return marshalResponse(true)

	case "packet:export-log":
		markdown := getStringArg(0)
		exportType := getStringArg(1)
		defaultName := fmt.Sprintf("api_packets_log_%s.md", strings.ToLower(exportType))
		filePath, ok, _ := a.dialogSvc.Save(a.ctx, dialogs.SaveRequest{
			Title:       "保存接口抓包日志",
			DefaultName: defaultName,
			Filters:     []dialogs.FileFilter{{DisplayName: "Markdown Files", Pattern: "*.md"}},
		})
		if !ok {
			return marshalResponse(false)
		}
		_ = os.WriteFile(filePath, []byte(markdown), 0644)
		a.dialogSvc.RevealFile(filePath)
		return marshalResponse(true)

	case "packet:export-single":
		markdown := getStringArg(0)
		method := getStringArg(1)
		pathStr := getStringArg(2)

		// Clean up pathStr to be a safe filename
		safePath := pathStr
		invalidChars := []string{"/", "\\", ":", "*", "?", "\"", "<", ">", "|"}
		for _, char := range invalidChars {
			safePath = strings.ReplaceAll(safePath, char, "_")
		}
		safePath = strings.Trim(safePath, " _")
		if safePath == "" {
			safePath = "api"
		}

		defaultName := fmt.Sprintf("packet_%s_%s.md", strings.ToLower(method), safePath)
		filePath, ok, _ := a.dialogSvc.Save(a.ctx, dialogs.SaveRequest{
			Title:       "保存单条接口抓包日志",
			DefaultName: defaultName,
			Filters:     []dialogs.FileFilter{{DisplayName: "Markdown Files", Pattern: "*.md"}},
		})
		if !ok {
			return marshalResponse(false)
		}
		_ = os.WriteFile(filePath, []byte(markdown), 0644)
		a.dialogSvc.RevealFile(filePath)
		return marshalResponse(true)
	}

	return "", false, nil
}
