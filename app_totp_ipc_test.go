package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"antigravity-proxy/internal/account"
)

func newTotpTestApp(t *testing.T) *App {
	t.Helper()
	tempDir := t.TempDir()
	app := &App{
		ctx:        context.Background(),
		accountMgr: account.NewManager(),
	}
	app.accountMgr.Init(tempDir)
	return app
}

func TestHandleTotpIPC_Export_TXT(t *testing.T) {
	app := newTotpTestApp(t)
	app.accountMgr.AddTwoFAAccount("test1@gmail.com", "JBSWY3DPEHPK3PXP")
	app.accountMgr.AddTwoFAAccount("test2@gmail.com", "MZXW6YTBOI======")

	tempDir := t.TempDir()
	exportPath := filepath.Join(tempDir, "exported_2fa.txt")
	t.Cleanup(func() {
		_ = os.Remove(exportPath)
	})

	app.dialogSvc = &mockDialogService{
		savePath: exportPath,
		saveOk:   true,
	}

	res, handled, err := app.handleTotpIPC("totp:export", nil)
	if err != nil {
		t.Fatalf("handleTotpIPC failed: %v", err)
	}
	if !handled {
		t.Fatalf("expected channel totp:export to be handled")
	}
	if res != "true" {
		t.Fatalf("expected true, got %s", res)
	}

	data, err := os.ReadFile(exportPath)
	if err != nil {
		t.Fatalf("read export file failed: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "test1@gmail.com----JBSWY3DPEHPK3PXP") {
		t.Errorf("expected content to contain test1, got: %s", content)
	}
	if !strings.Contains(content, "test2@gmail.com----MZXW6YTBOI======") {
		t.Errorf("expected content to contain test2, got: %s", content)
	}
}

func TestHandleTotpIPC_Export_CSV(t *testing.T) {
	app := newTotpTestApp(t)
	app.accountMgr.AddTwoFAAccount("csv_user@gmail.com", "JBSWY3DPEHPK3PXP")

	tempDir := t.TempDir()
	exportPath := filepath.Join(tempDir, "exported_2fa.csv")
	t.Cleanup(func() {
		_ = os.Remove(exportPath)
	})

	app.dialogSvc = &mockDialogService{
		savePath: exportPath,
		saveOk:   true,
	}

	res, handled, err := app.handleTotpIPC("totp:export", nil)
	if err != nil {
		t.Fatalf("handleTotpIPC failed: %v", err)
	}
	if !handled {
		t.Fatalf("expected handled to be true")
	}
	if res != "true" {
		t.Fatalf("expected true, got %s", res)
	}

	data, err := os.ReadFile(exportPath)
	if err != nil {
		t.Fatalf("read export file failed: %v", err)
	}
	content := string(data)
	expectedHeader := "\uFEFF邮箱,2FA密钥\n"
	if !strings.HasPrefix(content, expectedHeader) {
		t.Errorf("CSV header mismatch, got: %q", content)
	}
	if !strings.Contains(content, `"csv_user@gmail.com","JBSWY3DPEHPK3PXP"`) {
		t.Errorf("CSV row mismatch, got: %s", content)
	}
}

func TestHandleTotpIPC_Export_JSON(t *testing.T) {
	app := newTotpTestApp(t)
	app.accountMgr.AddTwoFAAccount("json_user@gmail.com", "JBSWY3DPEHPK3PXP")

	tempDir := t.TempDir()
	exportPath := filepath.Join(tempDir, "exported_2fa.json")
	t.Cleanup(func() {
		_ = os.Remove(exportPath)
	})

	app.dialogSvc = &mockDialogService{
		savePath: exportPath,
		saveOk:   true,
	}

	res, handled, err := app.handleTotpIPC("totp:export", nil)
	if err != nil {
		t.Fatalf("handleTotpIPC failed: %v", err)
	}
	if !handled {
		t.Fatalf("expected handled to be true")
	}
	if res != "true" {
		t.Fatalf("expected true, got %s", res)
	}

	data, err := os.ReadFile(exportPath)
	if err != nil {
		t.Fatalf("read export file failed: %v", err)
	}

	type ExportItem struct {
		Email  string `json:"email"`
		Secret string `json:"secret"`
	}
	var items []ExportItem
	if err := json.Unmarshal(data, &items); err != nil {
		t.Fatalf("unmarshal exported JSON failed: %v, raw: %s", err, string(data))
	}
	if len(items) != 1 || items[0].Email != "json_user@gmail.com" || items[0].Secret != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("unexpected items: %+v", items)
	}
}

func TestHandleTotpIPC_Export_UserCancel(t *testing.T) {
	app := newTotpTestApp(t)
	app.accountMgr.AddTwoFAAccount("cancel_user@gmail.com", "JBSWY3DPEHPK3PXP")

	app.dialogSvc = &mockDialogService{
		savePath: "",
		saveOk:   false, // 用户取消保存
	}

	res, handled, err := app.handleTotpIPC("totp:export", nil)
	if err != nil {
		t.Fatalf("handleTotpIPC failed: %v", err)
	}
	if !handled {
		t.Fatalf("expected handled to be true")
	}
	if res != "false" {
		t.Fatalf("expected false on user cancel, got %s", res)
	}
}

func TestHandleTotpIPC_Export_Empty(t *testing.T) {
	app := newTotpTestApp(t)
	// 未添加任何 2FA 账号

	app.dialogSvc = &mockDialogService{
		savePath: "some/path.txt",
		saveOk:   true,
	}

	res, handled, err := app.handleTotpIPC("totp:export", nil)
	if err != nil {
		t.Fatalf("handleTotpIPC failed: %v", err)
	}
	if !handled {
		t.Fatalf("expected handled to be true")
	}
	if res != "false" {
		t.Fatalf("expected false on empty accounts, got %s", res)
	}
}
