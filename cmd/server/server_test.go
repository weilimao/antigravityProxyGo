package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"antigravity-proxy/internal/db"
)

// getFreePort 动态获取一个随机空闲端口，避免测试中的端口占用冲突
func getFreePort(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	defer l.Close()
	return fmt.Sprintf("%d", l.Addr().(*net.TCPAddr).Port)
}

func TestResolveDefaultDataDir(t *testing.T) {
	dir := ResolveDefaultDataDir()
	if dir == "" {
		t.Fatalf("ResolveDefaultDataDir returned empty string")
	}
}

func TestServerInstance_LifecycleAndEndpoints(t *testing.T) {
	// 严格沙箱隔离：使用临时目录
	tempDir := t.TempDir()
	freePort := getFreePort(t)

	cfg := ServerConfig{
		Port:        freePort,
		DataDir:     tempDir,
		EnableProxy: false,
	}

	var logs []string
	testLogFn := func(msg string) {
		logs = append(logs, msg)
	}

	inst, err := NewServerInstance(cfg, testLogFn)
	if err != nil {
		t.Fatalf("NewServerInstance failed: %v", err)
	}

	// 注册环境清理钩子（Teardown & Clean-up）
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = inst.Shutdown(shutdownCtx)
		db.CloseDB()
		_ = os.RemoveAll(tempDir)
	})

	if err := inst.Start(); err != nil {
		t.Fatalf("inst.Start() failed: %v", err)
	}

	// 稍作休眠等待 Listener 就绪
	time.Sleep(100 * time.Millisecond)

	// 1. 验证配置文件与 SQLite 文件均在独立沙箱目录正常生成
	configFile := filepath.Join(tempDir, "config.json")
	if _, err := os.Stat(configFile); os.IsNotExist(err) {
		t.Errorf("expected config.json to exist in %s", tempDir)
	}

	dbFile := filepath.Join(tempDir, "antigravity.db")
	if _, err := os.Stat(dbFile); os.IsNotExist(err) {
		t.Errorf("expected antigravity.db to exist in %s", tempDir)
	}

	client := &http.Client{Timeout: 3 * time.Second}

	// 2. 验证 Health 端点 (/api/health)
	healthURL := fmt.Sprintf("http://127.0.0.1:%s/api/health", freePort)
	resp, err := client.Get(healthURL)
	if err != nil {
		t.Fatalf("GET /api/health request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200 from /api/health, got %d", resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}

	var healthResp map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &healthResp); err != nil {
		t.Fatalf("failed to unmarshal health response: %v", err)
	}
	if healthResp["status"] != "ok" {
		t.Errorf("expected status 'ok', got %v", healthResp["status"])
	}

	// 3. 验证 Models 端点 (/v1/models)
	modelsURL := fmt.Sprintf("http://127.0.0.1:%s/v1/models", freePort)
	modelsResp, err := client.Get(modelsURL)
	if err != nil {
		t.Fatalf("GET /v1/models request failed: %v", err)
	}
	defer modelsResp.Body.Close()

	if modelsResp.StatusCode != http.StatusOK && modelsResp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected status 200 or 401 from /v1/models, got %d", modelsResp.StatusCode)
	}

	// 4. 验证 /api/models/mapping 远程管理与鉴权
	mappingURL := fmt.Sprintf("http://127.0.0.1:%s/api/models/mapping", freePort)
	// 4a. 无 Token 应被拦截 401
	unauthMappingResp, err := client.Get(mappingURL)
	if err != nil {
		t.Fatalf("GET /api/models/mapping failed: %v", err)
	}
	defer unauthMappingResp.Body.Close()
	if unauthMappingResp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 from /api/models/mapping without token, got %d", unauthMappingResp.StatusCode)
	}

	// 4b. 创建管理员用户并登录获取 Token
	_, _ = inst.RelayUserMgr.EnsureAdminUser("server-admin", "admin-pass-123", "Admin")
	loginSession, err := inst.RelayAuthMgr.Login("server-admin", "admin-pass-123")
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}

	// 4c. 携带 Token 获取模型映射
	authGetReq, _ := http.NewRequest(http.MethodGet, mappingURL, nil)
	authGetReq.Header.Set("Authorization", "Bearer "+loginSession.Token)
	authGetResp, err := client.Do(authGetReq)
	if err != nil {
		t.Fatalf("GET /api/models/mapping with token failed: %v", err)
	}
	defer authGetResp.Body.Close()
	if authGetResp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 from /api/models/mapping with token, got %d", authGetResp.StatusCode)
	}

	// 5. 验证优雅退出
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutdownCancel()

	if err := inst.Shutdown(shutdownCtx); err != nil {
		t.Errorf("Shutdown returned error: %v", err)
	}

	// 再次请求应拒绝连接
	_, err = client.Get(healthURL)
	if err == nil {
		t.Errorf("expected connection error after shutdown, but request succeeded")
	}
}
