package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"antigravity-proxy/internal/db"
	"antigravity-proxy/internal/dialogs"
)

// mockDialogService 模拟对话框文件保存
type mockDialogService struct {
	savePath string
	saveOk   bool
}

func (m *mockDialogService) Open(ctx context.Context, req dialogs.OpenRequest) (string, bool, error) {
	return "", false, nil
}

func (m *mockDialogService) Save(ctx context.Context, req dialogs.SaveRequest) (string, bool, error) {
	return m.savePath, m.saveOk, nil
}

func (m *mockDialogService) OpenDir(ctx context.Context, req dialogs.DirRequest) (string, bool, error) {
	return "", false, nil
}

func (m *mockDialogService) RevealFile(path string) {
}

func (m *mockDialogService) MemoizeDir(dir string) {
}

// TestIOInvokeIPC_RequestLogsExportCSV 验证请求日志导出 CSV 时包含「首帧响应时间(ms)」列头
func TestIOInvokeIPC_RequestLogsExportCSV(t *testing.T) {
	tempDir := t.TempDir()
	if err := db.InitDB(tempDir); err != nil {
		t.Fatalf("初始化测试数据库失败: %v", err)
	}
	defer db.CloseDB()

	// 插入一条测试请求日志
	testLog := &db.RequestLog{
		Timestamp:    time.Now().Format(time.RFC3339),
		Mode:         "NVIDIA",
		UserID:       "user-test-01",
		Method:       "POST",
		Host:         "integrate.api.nvidia.com",
		Path:         "/v1/chat/completions",
		ModelName:    "meta/llama-3.1-70b-instruct",
		InTokens:     100,
		OutTokens:    50,
		CachedTokens: 20,
		Cost:         0.0012,
		FirstByteMs:  450,
		DurationMs:   1200,
		StatusCode:   200,
		SessionID:    "sess-test-12345",
	}
	if err := db.InsertRequestLog(testLog); err != nil {
		t.Fatalf("插入测试日志失败: %v", err)
	}

	exportCSVFile := filepath.Join(tempDir, "exported_request_logs.csv")
	app := &App{
		ctx: context.Background(),
		dialogSvc: &mockDialogService{
			savePath: exportCSVFile,
			saveOk:   true,
		},
	}

	res, handled, err := app.handleIOInvokeIPC("request-logs:export", nil)
	if err != nil {
		t.Fatalf("handleIOInvokeIPC 报错: %v", err)
	}
	if !handled {
		t.Fatalf("request-logs:export 未被处理")
	}
	if res != "true" {
		t.Fatalf("期望导出返回 true, 实际返回: %s", res)
	}

	// 读取导出的 CSV 内容
	contentBytes, err := os.ReadFile(exportCSVFile)
	if err != nil {
		t.Fatalf("读取导出的 CSV 文件失败: %v", err)
	}
	csvContent := string(contentBytes)

	// 验证包含新的首帧响应时间列头
	expectedHeader := "\uFEFF时间,模式,账号/用户,请求方式,域名,路径,模型,输入Token,输出Token,缓存Token,总成本,首帧响应时间(ms),耗时(ms),状态码,会话ID"
	if !strings.HasPrefix(csvContent, expectedHeader) {
		t.Errorf("CSV 表头不匹配!\n期望开头包含: %s\n实际文件内容:\n%s", expectedHeader, csvContent)
	}

	// 验证包含首帧响应时间 450 与 耗时 1200
	if !strings.Contains(csvContent, ",450,1200,200,\"sess-test-12345\"") {
		t.Errorf("CSV 记录数据不匹配! 未找到包含 450ms 首帧响应时间的记录, 文件内容:\n%s", csvContent)
	}
}
