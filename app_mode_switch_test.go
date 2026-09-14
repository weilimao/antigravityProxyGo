package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/proxy"
	"antigravity-proxy/internal/relay"
	"antigravity-proxy/internal/settings"
)

func TestApp_ModeSwitch_LosslessIsolation(t *testing.T) {
	// 1. 创建本地测试数据目录
	localDir := t.TempDir()

	// 准备本地账号文件 (包含 2 个本地 Antigravity 官方账号)
	localAntigravityAccounts := `{"accounts":[
		{"id":"acc-local-1","email":"local1@gmail.com","provider":"antigravity","enabled":true},
		{"id":"acc-local-2","email":"local2@gmail.com","provider":"antigravity","enabled":true}
	]}`
	_ = os.WriteFile(filepath.Join(localDir, "accounts_antigravity.json"), []byte(localAntigravityAccounts), 0644)
	_ = os.WriteFile(filepath.Join(localDir, "accounts_pool.json"), []byte("{}"), 0644)

	// 准备本地用户文件 (包含本地用户 weilimao)
	localUsers := `[
		{"id":"u-local-1","key":"weilimao","role":"user","enabled":true}
	]`
	_ = os.WriteFile(filepath.Join(localDir, "relay_users.json"), []byte(localUsers), 0644)

	// 准备本地配置文件
	localConfig := `{"language":"zh","nvidiaPreferredModels":["deepseek-v3"]}`
	_ = os.WriteFile(filepath.Join(localDir, "config.json"), []byte(localConfig), 0644)

	// 2. 模拟远端服务器提供的数据 (包含 1 个远端 NVIDIA 账号, 1 个远端管理员 admin)
	remoteNvidiaAccounts := `{"accounts":[
		{"id":"acc-remote-nv-1","email":"remote-nv@nvidia.com","provider":"nvidia","enabled":true}
	]}`
	remoteUsers := `[
		{"id":"u-remote-admin","key":"admin","role":"admin","isAdmin":true,"enabled":true}
	]`
	remoteConfig := `{"language":"zh","nvidiaPreferredModels":["deepseek-ai/deepseek-v4-flash-0731"]}`

	// 构造测试 HTTP 服务模拟云端中继 API
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sync/full" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"files": map[string]string{
					"accounts_nvidia.json": remoteNvidiaAccounts,
					"relay_users.json":     remoteUsers,
					"config.json":          remoteConfig,
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(mockServer.Close)

	serverURL, _ := url.Parse(mockServer.URL)
	serverHost := serverURL.Hostname()
	serverPort := serverURL.Port()

	// 3. 初始化 App 组件
	app := &App{
		ctx:         nil,
		settingsMgr: settings.NewManager(),
		accountMgr:  account.NewManager(),
		relayUserMgr: relay.NewUserManager(),
	}

	app.settingsMgr.Init(localDir)
	app.accountMgr.Init(localDir)
	app.relayUserMgr.Init(localDir)

	// 校验初始本地状态
	if len(app.accountMgr.GetAccounts()) != 2 {
		t.Fatalf("expected 2 local accounts, got %d", len(app.accountMgr.GetAccounts()))
	}
	if len(app.relayUserMgr.GetUsers()) != 1 || app.relayUserMgr.GetUsers()[0].Key != "weilimao" {
		t.Fatalf("expected local user 'weilimao'")
	}
	if len(app.settingsMgr.GetNvidiaPreferredModels()) != 1 || app.settingsMgr.GetNvidiaPreferredModels()[0] != "deepseek-v3" {
		t.Fatalf("expected local nvidia preferred model 'deepseek-v3'")
	}

	// 4. 模拟建立远端中继连接并切换到服务器模式
	app.remoteRelay = proxy.NewRemoteRelay(nil)
	app.remoteRelay.SetConfigForTest(proxy.RemoteConfig{
		Connected: true,
		Host:      serverHost,
		Port:      serverPort,
		Token:     "test-token",
		UserKey:   "admin",
	})

	err := app.switchToServerMode()
	if err != nil {
		t.Fatalf("switchToServerMode failed: %v", err)
	}

	// 5. 验证服务器模式下的数据:
	// 5.1 账号池应该只展示远端的 NVIDIA 账号, 本地的 2 个 Antigravity 官方账号绝不能出现!
	remoteAccounts := app.accountMgr.GetAccounts()
	if len(remoteAccounts) != 1 {
		t.Fatalf("server mode expected 1 remote account, got %d", len(remoteAccounts))
	}
	if remoteAccounts[0].Provider != "nvidia" || remoteAccounts[0].Email != "remote-nv@nvidia.com" {
		t.Fatalf("unexpected account in server mode: %+v", remoteAccounts[0])
	}

	// 5.2 中继用户应该只展示云端的 admin
	usersInServerMode := app.relayUserMgr.GetUsers()
	if len(usersInServerMode) != 1 || usersInServerMode[0].Key != "admin" {
		t.Fatalf("expected server user 'admin', got %+v", usersInServerMode)
	}

	// 5.3 NVIDIA 专属模型应该为云端的配置
	serverModels := app.settingsMgr.GetNvidiaPreferredModels()
	if len(serverModels) != 1 || serverModels[0] != "deepseek-ai/deepseek-v4-flash-0731" {
		t.Fatalf("expected server nvidia preferred model, got %+v", serverModels)
	}

	// 6. 执行断开切回本地模式
	app.switchToLocalMode()

	// 7. 验证本地模式下的数据完好无损地完全恢复:
	restoredAccounts := app.accountMgr.GetAccounts()
	if len(restoredAccounts) != 2 {
		t.Fatalf("expected 2 restored local accounts, got %d", len(restoredAccounts))
	}

	restoredUsers := app.relayUserMgr.GetUsers()
	if len(restoredUsers) != 1 || restoredUsers[0].Key != "weilimao" {
		t.Fatalf("expected restored local user 'weilimao', got %+v", restoredUsers)
	}

	restoredModels := app.settingsMgr.GetNvidiaPreferredModels()
	if len(restoredModels) != 1 || restoredModels[0] != "deepseek-v3" {
		t.Fatalf("expected restored local nvidia model 'deepseek-v3', got %+v", restoredModels)
	}

	// 8. 验证本地磁盘文件未被覆盖或修改
	localAntigravityAfter, _ := os.ReadFile(filepath.Join(localDir, "accounts_antigravity.json"))
	if string(localAntigravityAfter) != localAntigravityAccounts {
		t.Fatalf("local accounts_antigravity.json was modified! Content: %s", string(localAntigravityAfter))
	}

	localConfigAfter, _ := os.ReadFile(filepath.Join(localDir, "config.json"))
	var cfgMap map[string]interface{}
	_ = json.Unmarshal(localConfigAfter, &cfgMap)
	modelsAfter, _ := cfgMap["nvidiaPreferredModels"].([]interface{})
	if len(modelsAfter) != 1 || modelsAfter[0] != "deepseek-v3" {
		t.Fatalf("local config.json was modified! Content: %s", string(localConfigAfter))
	}
}

func TestApp_ModeSwitch_CustomDataDirectory_LosslessIsolation(t *testing.T) {
	configDir := t.TempDir()
	customDataDir := t.TempDir()

	// 准备自定义数据目录下的本地账号
	localAntigravityAccounts := `{"accounts":[
		{"id":"acc-custom-1","email":"custom1@gmail.com","provider":"antigravity","enabled":true},
		{"id":"acc-custom-2","email":"custom2@gmail.com","provider":"antigravity","enabled":true}
	]}`
	_ = os.WriteFile(filepath.Join(customDataDir, "accounts_antigravity.json"), []byte(localAntigravityAccounts), 0644)
	_ = os.WriteFile(filepath.Join(customDataDir, "accounts_pool.json"), []byte("{}"), 0644)

	// 基础配置目录下放置指定 dataDirectory 的 config.json
	cfgData, _ := json.Marshal(map[string]interface{}{
		"dataDirectory": customDataDir,
		"language":      "zh",
	})
	_ = os.WriteFile(filepath.Join(configDir, "config.json"), cfgData, 0644)

	// 模拟远端服务器提供的数据 (只有 1 个远端账号)
	remoteAccounts := `{"accounts":[
		{"id":"acc-remote-1","email":"remote@nvidia.com","provider":"nvidia","enabled":true}
	]}`
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sync/full" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"files": map[string]string{
					"accounts_nvidia.json": remoteAccounts,
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(func() {
		mockServer.Close()
		_ = os.RemoveAll(configDir)
		_ = os.RemoveAll(customDataDir)
	})

	serverURL, _ := url.Parse(mockServer.URL)

	app := &App{
		settingsMgr: settings.NewManager(),
		accountMgr:  account.NewManager(),
	}

	app.settingsMgr.Init(configDir)
	if app.settingsMgr.GetActiveDataDirectory() != customDataDir {
		t.Fatalf("expected activeDataDirectory to be %q, got %q", customDataDir, app.settingsMgr.GetActiveDataDirectory())
	}
	app.accountMgr.Init(customDataDir)

	if len(app.accountMgr.GetAccounts()) != 2 {
		t.Fatalf("expected 2 accounts in customDataDir, got %d", len(app.accountMgr.GetAccounts()))
	}

	// 切换至服务器模式
	app.remoteRelay = proxy.NewRemoteRelay(nil)
	app.remoteRelay.SetConfigForTest(proxy.RemoteConfig{
		Connected: true,
		Host:      serverURL.Hostname(),
		Port:      serverURL.Port(),
		Token:     "test-token",
		UserKey:   "admin",
	})

	if err := app.switchToServerMode(); err != nil {
		t.Fatalf("switchToServerMode failed: %v", err)
	}

	// 验证服务器模式：本地账号被隔离，只展示远端账号
	if len(app.accountMgr.GetAccounts()) != 1 || app.accountMgr.GetAccounts()[0].Email != "remote@nvidia.com" {
		t.Fatalf("expected 1 remote account in server mode, got %+v", app.accountMgr.GetAccounts())
	}

	// 切换回本地模式
	app.switchToLocalMode()

	// 核心断言：切回本地模式后，必须完好恢复自定义数据目录下的 2 个官方账号，绝对不可为 0！
	restoredAccounts := app.accountMgr.GetAccounts()
	if len(restoredAccounts) != 2 {
		t.Fatalf("expected 2 restored accounts from customDataDir, got %d (directory mismatch bug!)", len(restoredAccounts))
	}
	if restoredAccounts[0].Email != "custom1@gmail.com" || restoredAccounts[1].Email != "custom2@gmail.com" {
		t.Fatalf("restored accounts unexpected: %+v", restoredAccounts)
	}
	if app.settingsMgr.GetActiveDataDirectory() != customDataDir {
		t.Fatalf("expected activeDataDirectory to remain %q, got %q", customDataDir, app.settingsMgr.GetActiveDataDirectory())
	}
}

func TestApp_ModeSwitch_DisableKeepsCredentialsAndEnablesToggle(t *testing.T) {
	configDir := t.TempDir()

	localConfig := `{"language":"zh"}`
	_ = os.WriteFile(filepath.Join(configDir, "config.json"), []byte(localConfig), 0644)
	_ = os.WriteFile(filepath.Join(configDir, "accounts_antigravity.json"), []byte(`{"accounts":[]}`), 0644)
	_ = os.WriteFile(filepath.Join(configDir, "accounts_pool.json"), []byte("{}"), 0644)

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sync/full" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"files": map[string]string{
					"accounts_nvidia.json": `{"accounts":[]}`,
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(func() {
		mockServer.Close()
		_ = os.RemoveAll(configDir)
	})

	serverURL, _ := url.Parse(mockServer.URL)

	app := &App{
		settingsMgr: settings.NewManager(),
		accountMgr:  account.NewManager(),
	}
	app.settingsMgr.Init(configDir)
	app.accountMgr.Init(configDir)

	// 1. 模拟用户登录远程服务器（先写入本地配置）
	_ = app.settingsMgr.SetRemoteHost(serverURL.Hostname())
	_ = app.settingsMgr.SetRemotePort(serverURL.Port())
	_ = app.settingsMgr.SetRemoteKey("weilimao-key")
	_ = app.settingsMgr.SetRemoteEnabled(true)

	app.remoteRelay = proxy.NewRemoteRelay(nil)
	app.remoteRelay.SetConfigForTest(proxy.RemoteConfig{
		Connected: true,
		Host:      serverURL.Hostname(),
		Port:      serverURL.Port(),
		UserKey:   "weilimao-key",
		Token:     "test-token",
	})

	// 2. 进入服务器模式
	if err := app.switchToServerMode(); err != nil {
		t.Fatalf("switchToServerMode failed: %v", err)
	}

	if !app.isServerMode() {
		t.Fatalf("expected isServerMode to be true")
	}

	// 3. 模拟用户点击“停用”（切回本地模式）
	app.switchToLocalMode()

	if app.isServerMode() {
		t.Fatalf("expected isServerMode to be false after disable")
	}

	// 4. 关键验证：本地配置中的远程凭据必须完好保留，且 remoteEnabled 必须为 false
	if app.settingsMgr.GetRemoteHost() != serverURL.Hostname() {
		t.Fatalf("expected remoteHost %q preserved, got %q", serverURL.Hostname(), app.settingsMgr.GetRemoteHost())
	}
	if app.settingsMgr.GetRemoteKey() != "weilimao-key" {
		t.Fatalf("expected remoteKey 'weilimao-key' preserved, got %q", app.settingsMgr.GetRemoteKey())
	}
	if app.settingsMgr.GetRemoteEnabled() != false {
		t.Fatalf("expected remoteEnabled to be false after disable, got %v", app.settingsMgr.GetRemoteEnabled())
	}

	// 5. 验证状态 Payload：必须保持 hasSavedCredentials=true，使前端能正确显示黄色待命徽标与“启用”按钮
	payload := app.getRemoteStatusPayload()
	if payload["hasSavedCredentials"] != true {
		t.Fatalf("expected hasSavedCredentials=true, got %v", payload["hasSavedCredentials"])
	}
	if payload["remoteEnabled"] != false {
		t.Fatalf("expected remoteEnabled=false in payload, got %v", payload["remoteEnabled"])
	}
	if payload["savedHost"] != serverURL.Hostname() {
		t.Fatalf("expected savedHost=%q in payload, got %v", serverURL.Hostname(), payload["savedHost"])
	}
}

func TestApp_ModeSwitch_AutoModelConfigIsolation(t *testing.T) {
	localDir := t.TempDir()

	// 1. 本地配置：配置本地 auto 模型候选池
	localConfig := `{"language":"zh","relayModelMapping":[{"clientModel":"auto","targetModel":"auto","expose":true,"candidateModels":["local-gemini","local-qwen"]}]}`
	_ = os.WriteFile(filepath.Join(localDir, "config.json"), []byte(localConfig), 0644)
	_ = os.WriteFile(filepath.Join(localDir, "accounts_antigravity.json"), []byte(`{"accounts":[]}`), 0644)
	_ = os.WriteFile(filepath.Join(localDir, "accounts_pool.json"), []byte("{}"), 0644)

	// 2. 远端服务器 Mock
	remoteAutoSaved := []string{"remote-llama", "remote-deepseek"}
	mockRemoteServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/auth/login":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"token":   "remote-test-token",
				"isAdmin": false,
			})
		case "/api/sync/full":
			remoteFiles := map[string]string{
				"config.json": `{"relayModelMapping":[{"clientModel":"auto","targetModel":"auto","expose":true}]}`,
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"files":   remoteFiles,
			})
		case "/api/models/auto-config":
			if r.Method == http.MethodGet {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"success": true,
					"isUser":  true,
					"config": map[string]interface{}{
						"enabled":          true,
						"candidateModels":  remoteAutoSaved,
						"useBenchmarkPool": false,
					},
					"userKey": "test-remote-user",
				})
			} else if r.Method == http.MethodPost {
				var req struct {
					Config struct {
						CandidateModels []string `json:"candidateModels"`
					} `json:"config"`
				}
				_ = json.NewDecoder(r.Body).Decode(&req)
				remoteAutoSaved = req.Config.CandidateModels
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"success": true,
				})
			}
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		}
	}))
	defer mockRemoteServer.Close()

	serverURL, _ := url.Parse(mockRemoteServer.URL)

	// 3. 初始化本地 App 实例
	app := &App{
		accountMgr:      account.NewManager(),
		settingsMgr:     settings.NewManager(),
		relayUserMgr:    relay.NewUserManager(),
		relayPackageMgr: relay.NewPackageManager(),
		proxyEngine:     proxy.NewProxyEngine(nil, nil, nil),
	}
	app.settingsMgr.Init(localDir)

	t.Cleanup(func() {
		app.switchToLocalMode()
		_ = os.RemoveAll(localDir)
	})

	// 验证初始状态：纯本地模式，读取本地 auto 候选池
	localMappings := app.settingsMgr.GetRelayModelMapping()
	if len(localMappings) == 0 || len(localMappings[0].CandidateModels) != 2 || localMappings[0].CandidateModels[0] != "local-gemini" {
		t.Fatalf("local auto candidate models unexpected: %+v", localMappings)
	}

	// 4. 连接远程服务器
	app.remoteRelay = proxy.NewRemoteRelay(nil)
	_ = app.remoteRelay.Login(serverURL.Hostname(), serverURL.Port(), "", "test-remote-user", "pwd123")
	if err := app.switchToServerMode(); err != nil {
		t.Fatalf("switchToServerMode failed: %v", err)
	}

	if !app.isServerMode() {
		t.Fatalf("expected isServerMode to be true")
	}

	// 5. 在服务器模式下通过 IPC 获取与保存 auto 配置
	var getRes map[string]interface{}
	rawGet, _, err := app.handleRelayConfigIPC("relay:get-auto-config", nil)
	if err != nil {
		t.Fatalf("relay:get-auto-config IPC failed: %v", err)
	}
	_ = json.Unmarshal([]byte(rawGet), &getRes)
	if getRes["isRemote"] != true || getRes["userKey"] != "test-remote-user" {
		t.Fatalf("expected remote user auto config, got: %+v", getRes)
	}

	// 在服务器模式下保存新的远程账号专属配置
	newRemoteCands := []string{"remote-gemini-pro", "remote-claude"}
	rawSave, _, err := app.handleRelayConfigIPC("relay:save-auto-config", []interface{}{map[string]interface{}{
		"enabled":          true,
		"candidateModels":  newRemoteCands,
		"useBenchmarkPool": false,
	}})
	if err != nil {
		t.Fatalf("relay:save-auto-config IPC failed: %v", err)
	}
	var saveRes map[string]interface{}
	_ = json.Unmarshal([]byte(rawSave), &saveRes)
	if saveRes["success"] != true {
		t.Fatalf("save auto config failed: %+v", saveRes)
	}
	if len(remoteAutoSaved) != 2 || remoteAutoSaved[0] != "remote-gemini-pro" {
		t.Fatalf("remoteAutoSaved on mock server not updated: %v", remoteAutoSaved)
	}

	// 6. 断开远程连接，无损切回本地模式
	app.switchToLocalMode()

	if app.isServerMode() {
		t.Fatalf("expected isServerMode to be false after disconnect")
	}

	// 7. 严格验证：本地模式下的 Auto 配置未受任何云端配置覆盖或污染，100% 恢复本地配置
	restoredMappings := app.settingsMgr.GetRelayModelMapping()
	if len(restoredMappings) == 0 {
		t.Fatalf("restored local mappings empty")
	}
	var localAuto *settings.ModelMappingEntry
	for _, m := range restoredMappings {
		if m.ClientModel == "auto" {
			localAuto = &m
			break
		}
	}
	if localAuto == nil || len(localAuto.CandidateModels) != 2 {
		t.Fatalf("restored local auto config not found or length wrong: %+v", localAuto)
	}
	if localAuto.CandidateModels[0] != "local-gemini" || localAuto.CandidateModels[1] != "local-qwen" {
		t.Fatalf("restored local candidates contaminated: %v", localAuto.CandidateModels)
	}
}
