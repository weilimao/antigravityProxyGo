package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSettings_RequestTimeout(t *testing.T) {
	// 创建临时工作目录
	tempDir, err := os.MkdirTemp("", "antigravity-settings-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr := NewManager()
	mgr.Init(tempDir)

	// 1. 测试默认超时时间是否为 300 秒
	if timeout := mgr.GetRequestTimeout(); timeout != 300 {
		t.Errorf("Expected default RequestTimeout to be 300, got %d", timeout)
	}

	// 2. 测试 Setter/Getter 方法
	if err := mgr.SetRequestTimeout(120); err != nil {
		t.Fatalf("Failed to set request timeout: %v", err)
	}
	if timeout := mgr.GetRequestTimeout(); timeout != 120 {
		t.Errorf("Expected RequestTimeout to be 120, got %d", timeout)
	}

	// 3. 校验配置文件是否已落盘且值正确
	configPath := filepath.Join(tempDir, "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("Failed to read config file: %v", err)
	}

	// 初始化一个新 Manager 加载它
	newMgr := NewManager()
	newMgr.Init(tempDir)
	if timeout := newMgr.GetRequestTimeout(); timeout != 120 {
		t.Errorf("Expected reloaded RequestTimeout to be 120, got %d (raw config: %s)", timeout, string(data))
	}

	// 4. 测试边界防御防呆逻辑（设为负数或 0 是否回弹为 300）
	if err := mgr.SetRequestTimeout(-10); err != nil {
		t.Fatalf("Failed to set invalid timeout: %v", err)
	}
	if timeout := mgr.GetRequestTimeout(); timeout != 300 {
		t.Errorf("Expected invalid timeout to fallback to 300, got %d", timeout)
	}
}

func TestSettings_RelayModelMappingRetention(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "antigravity-settings-mapping-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr := NewManager()
	mgr.Init(tempDir)

	// 1. Initially, we should have all default model mappings
	initialMappings := mgr.GetRelayModelMapping()
	if len(initialMappings) == 0 {
		t.Fatalf("Expected default model mappings to be loaded, got 0")
	}

	// Record original count of mappings
	origCount := len(initialMappings)
	targetToDelete := initialMappings[0].ClientModel

	// 2. Delete the first mapping
	var newMappings []ModelMappingEntry
	for _, entry := range initialMappings {
		if entry.ClientModel != targetToDelete {
			newMappings = append(newMappings, entry)
		}
	}
	if err := mgr.SetRelayModelMapping(newMappings); err != nil {
		t.Fatalf("Failed to set model mappings: %v", err)
	}

	// 3. Re-initialize a new manager to simulate app restart
	newMgr := NewManager()
	newMgr.Init(tempDir)

	reloadedMappings := newMgr.GetRelayModelMapping()
	if len(reloadedMappings) != origCount-1 {
		t.Errorf("Expected reloaded mappings count to be %d, got %d", origCount-1, len(reloadedMappings))
	}

	foundDeleted := false
	for _, entry := range reloadedMappings {
		if entry.ClientModel == targetToDelete {
			foundDeleted = true
			break
		}
	}
	if foundDeleted {
		t.Errorf("Expected model mapping for %q to remain deleted, but it was restored", targetToDelete)
	}

	// 4. Test deleting ALL mappings
	if err := newMgr.SetRelayModelMapping([]ModelMappingEntry{}); err != nil {
		t.Fatalf("Failed to clear all model mappings: %v", err)
	}

	// Re-initialize manager again
	finalMgr := NewManager()
	finalMgr.Init(tempDir)

	finalMappings := finalMgr.GetRelayModelMapping()
	if len(finalMappings) != 0 {
		t.Errorf("Expected model mappings to remain empty, but got %d entries", len(finalMappings))
	}
}

func TestSettings_PromptPrefix(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "antigravity-settings-prompt-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr := NewManager()
	mgr.Init(tempDir)

	// 1. 默认值校验
	if prefix := mgr.GetPromptPrefix(); prefix != "" {
		t.Errorf("Expected default PromptPrefix to be empty, got %q", prefix)
	}

	// 2. 设置并校验
	testPrefix := "[Please reply in English] "
	if err := mgr.SetPromptPrefix(testPrefix); err != nil {
		t.Fatalf("Failed to set prompt prefix: %v", err)
	}
	if prefix := mgr.GetPromptPrefix(); prefix != testPrefix {
		t.Errorf("Expected PromptPrefix to be %q, got %q", testPrefix, prefix)
	}

	// 3. 重启加载校验
	newMgr := NewManager()
	newMgr.Init(tempDir)
	if prefix := newMgr.GetPromptPrefix(); prefix != testPrefix {
		t.Errorf("Expected reloaded PromptPrefix to be %q, got %q", testPrefix, prefix)
	}
}

func TestSettings_DefaultAllOff(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "antigravity-settings-alloff-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr := NewManager()
	mgr.Init(tempDir)

	if mgr.GetCustomModelOverrideEnabled() != false {
		t.Errorf("Expected CustomModelOverrideEnabled to default to false")
	}
	if mgr.GetCustomThinkingOverrideEnabled() != false {
		t.Errorf("Expected CustomThinkingOverrideEnabled to default to false")
	}
	if mgr.GetCustomThinkingSupports() != false {
		t.Errorf("Expected CustomThinkingSupports to default to false")
	}
	if mgr.GetCustomThinkingBudget() != 0 {
		t.Errorf("Expected CustomThinkingBudget to default to 0, got %d", mgr.GetCustomThinkingBudget())
	}

	// BypassOverridePrefixes 默认 ["tab"] —— Tab 补全模型走代码补全通道,
	// 被全局覆写改向推理上游会触发 400 INVALID_ARGUMENT(见 handler.go 全局覆写日志)。
	bypass := mgr.GetBypassOverridePrefixes()
	if len(bypass) != 1 || bypass[0] != "tab" {
		t.Errorf("Expected BypassOverridePrefixes to default to [tab], got %v", bypass)
	}
}

// TestNvidiaPreferredModels 覆盖全局级 NVIDIA 专属模型清单的默认值、Set/Get 往返、
// 去空去重、落盘与重载一致性、返回切片的内部隔离性。
func TestNvidiaPreferredModels(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "antigravity-nvidia-preferred-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr := NewManager()
	mgr.Init(tempDir)

	// 1. 默认应为空切片(非 nil),避免调用方判空歧义
	got := mgr.GetNvidiaPreferredModels()
	if len(got) != 0 {
		t.Errorf("Expected empty preferred models by default, got %v", got)
	}

	// 2. Set 后 Get 返回一致;去空去重
	want := []string{"moonshotai/kimi-k2.5", "  ", "nvidia/llama-3.1-nemotron-70b-instruct", "", "meta/llama-3.3-70b-instruct", "moonshotai/kimi-k2.5"}
	if err := mgr.SetNvidiaPreferredModels(want); err != nil {
		t.Fatalf("SetNvidiaPreferredModels failed: %v", err)
	}
	got = mgr.GetNvidiaPreferredModels()
	expected := []string{"moonshotai/kimi-k2.5", "nvidia/llama-3.1-nemotron-70b-instruct", "meta/llama-3.3-70b-instruct"}
	if len(got) != len(expected) {
		t.Fatalf("Expected %d deduped models, got %d: %v", len(expected), len(got), got)
	}
	for i, m := range expected {
		if got[i] != m {
			t.Errorf("Expected models[%d]=%q, got %q", i, m, got[i])
		}
	}

	// 3. 返回的是副本,外部修改不影响内存态
	got[0] = "tampered"
	again := mgr.GetNvidiaPreferredModels()
	if again[0] == "tampered" {
		t.Errorf("GetNvidiaPreferredModels should return a defensive copy, but mutation leaked")
	}

	// 4. 配置落盘,新 Manager 加载该清单
	configPath := filepath.Join(tempDir, "config.json")
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("Expected config.json persisted, stat err: %v", err)
	}
	mgr2 := NewManager()
	mgr2.Init(tempDir)
	reloaded := mgr2.GetNvidiaPreferredModels()
	if len(reloaded) != len(expected) {
		t.Fatalf("Reloaded preferred models mismatch, got %v", reloaded)
	}
	for i, m := range expected {
		if reloaded[i] != m {
			t.Errorf("Reloaded models[%d]=%q, expected %q", i, reloaded[i], m)
		}
	}

	// 5. 清空清单
	if err := mgr2.SetNvidiaPreferredModels([]string{}); err != nil {
		t.Fatalf("Clear preferred models failed: %v", err)
	}
	if len(mgr2.GetNvidiaPreferredModels()) != 0 {
		t.Errorf("Expected empty after clear, got %v", mgr2.GetNvidiaPreferredModels())
	}
}

// TestBypassOverridePrefixes 覆盖"按前缀绕过"名单的默认值、Set/Get 往返、
// 去空去重(大小写不敏感)、落盘与重载一致性、返回切片的内部隔离性。
func TestBypassOverridePrefixes(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "antigravity-bypass-prefixes-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr := NewManager()
	mgr.Init(tempDir)

	// 1. 默认应为 ["tab"](放行 Tab 补全模型),非空非 nil
	got := mgr.GetBypassOverridePrefixes()
	if len(got) != 1 || got[0] != "tab" {
		t.Errorf("Expected default [\"tab\"], got %v", got)
	}

	// 2. Set 后 Get 返回一致;去空去重(大小写不敏感,保留首次出现的大小写)
	want := []string{"tab", "  ", "Tab", "", "claude-", "tab"}
	if err := mgr.SetBypassOverridePrefixes(want); err != nil {
		t.Fatalf("SetBypassOverridePrefixes failed: %v", err)
	}
	got = mgr.GetBypassOverridePrefixes()
	expected := []string{"tab", "claude-"}
	if len(got) != len(expected) {
		t.Fatalf("Expected %d deduped prefixes, got %d: %v", len(expected), len(got), got)
	}
	for i, p := range expected {
		if got[i] != p {
			t.Errorf("Expected prefixes[%d]=%q, got %q", i, p, got[i])
		}
	}

	// 3. 返回的是副本,外部修改不影响内存态
	got[0] = "tampered"
	again := mgr.GetBypassOverridePrefixes()
	if again[0] == "tampered" {
		t.Errorf("GetBypassOverridePrefixes should return a defensive copy, but mutation leaked")
	}

	// 4. 配置落盘,新 Manager 加载该清单
	configPath := filepath.Join(tempDir, "config.json")
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("Expected config.json persisted, stat err: %v", err)
	}
	mgr2 := NewManager()
	mgr2.Init(tempDir)
	reloaded := mgr2.GetBypassOverridePrefixes()
	if len(reloaded) != len(expected) {
		t.Fatalf("Reloaded bypass prefixes mismatch, got %v", reloaded)
	}
	for i, p := range expected {
		if reloaded[i] != p {
			t.Errorf("Reloaded prefixes[%d]=%q, expected %q", i, reloaded[i], p)
		}
	}

	// 5. 清空清单(nil/空数组语义一致)
	if err := mgr2.SetBypassOverridePrefixes([]string{}); err != nil {
		t.Fatalf("Clear bypass prefixes failed: %v", err)
	}
	if len(mgr2.GetBypassOverridePrefixes()) != 0 {
		t.Errorf("Expected empty after clear, got %v", mgr2.GetBypassOverridePrefixes())
	}
}

// TestBypassOverridePrefixes_NullAndMissingFallback 锁定 loadConfig 兜底行为:
// 当 config.json 中 bypassOverridePrefixes 字段缺失(旧版本升级)或被显式写成 null
// 时,加载后应回落到默认 ["tab"],避免 Tab 补全模型被全局覆写改向推理上游触发 400。
// 显式 [] (空数组) 不受兜底干预,仍表达"全部覆写"的用户意图。
func TestBypassOverridePrefixes_NullAndMissingFallback(t *testing.T) {
	cases := []struct {
		name    string
		jsonStr string
		want    []string
	}{
		{
			name:    "字段缺失 (旧版本配置)",
			jsonStr: `{"customModelOverrideEnabled": true}`,
			want:    []string{"tab"},
		},
		{
			name:    "字段显式 null",
			jsonStr: `{"customModelOverrideEnabled": true, "bypassOverridePrefixes": null}`,
			want:    []string{"tab"},
		},
		{
			name:    "字段显式空数组 (用户主动全部覆写,不兜底)",
			jsonStr: `{"customModelOverrideEnabled": true, "bypassOverridePrefixes": []}`,
			want:    []string{},
		},
		{
			name:    "字段含自定义前缀 (正常透传用户配置)",
			jsonStr: `{"customModelOverrideEnabled": true, "bypassOverridePrefixes": ["tab", "claude-"]}`,
			want:    []string{"tab", "claude-"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tempDir, err := os.MkdirTemp("", "antigravity-bypass-null-test")
			if err != nil {
				t.Fatalf("Failed to create temp dir: %v", err)
			}
			defer os.RemoveAll(tempDir)

			configPath := filepath.Join(tempDir, configFileName)
			if err := os.WriteFile(configPath, []byte(c.jsonStr), 0o644); err != nil {
				t.Fatalf("Failed to write config: %v", err)
			}

			mgr := NewManager()
			mgr.Init(tempDir)
			got := mgr.GetBypassOverridePrefixes()
			if len(got) != len(c.want) {
				t.Fatalf("Got %v, want %v", got, c.want)
			}
			for i, p := range c.want {
				if got[i] != p {
					t.Errorf("Got[%d]=%q, want %q", i, got[i], p)
				}
			}
		})
	}
}

func TestEnableThinkingMode_AutoPersistToDisk(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "settings_test_persist_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	configPath := filepath.Join(tempDir, "config.json")
	// 模拟旧版的 config.json(缺少 enableThinkingMode 属性)
	oldConfigJSON := []byte(`{
		"relayPort": "18444",
		"requestTimeout": 300
	}`)
	if err := os.WriteFile(configPath, oldConfigJSON, 0644); err != nil {
		t.Fatalf("Failed to write mock old config: %v", err)
	}

	// 实例化 Manager 并初始化(触发 loadConfig 自动补全与 SaveConfig 落盘)
	mgr := NewManager()
	mgr.Init(tempDir)

	if !mgr.GetEnableThinkingMode() {
		t.Errorf("Expected EnableThinkingMode == true in memory after loadConfig")
	}

	// 读取落盘后的 config.json 验证磁盘文件中已经真实持久化写入了 "enableThinkingMode": true
	diskData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("Failed to read back config.json from disk: %v", err)
	}
	diskStr := string(diskData)
	if !strings.Contains(diskStr, `"enableThinkingMode": true`) {
		t.Errorf("Expected disk config.json to contain \"enableThinkingMode\": true, got:\n%s", diskStr)
	}
}

func TestSettings_NvidiaWorkerProxy(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "settings_worker_proxy_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr := NewManager()
	mgr.Init(tempDir)

	// 初始状态：未配置 URL，应返回 disabled
	if mgr.IsNvidiaWorkerProxyEnabled() {
		t.Errorf("Expected IsNvidiaWorkerProxyEnabled == false initially")
	}

	// 配置 URL 与启用
	testURL := "https://my-nvidia.workers.dev"
	if err := mgr.SetNvidiaWorkerProxyURL(testURL); err != nil {
		t.Fatalf("SetNvidiaWorkerProxyURL failed: %v", err)
	}
	if err := mgr.SetNvidiaWorkerProxyEnabled(true); err != nil {
		t.Fatalf("SetNvidiaWorkerProxyEnabled failed: %v", err)
	}

	if mgr.GetNvidiaWorkerProxyURL() != testURL {
		t.Errorf("Expected %s, got %s", testURL, mgr.GetNvidiaWorkerProxyURL())
	}
	if !mgr.IsNvidiaWorkerProxyEnabled() {
		t.Errorf("Expected IsNvidiaWorkerProxyEnabled == true")
	}

	// 重新加载验证落盘
	mgr2 := NewManager()
	mgr2.Init(tempDir)
	if mgr2.GetNvidiaWorkerProxyURL() != testURL {
		t.Errorf("Expected reloaded URL %s, got %s", testURL, mgr2.GetNvidiaWorkerProxyURL())
	}
	if !mgr2.IsNvidiaWorkerProxyEnabled() {
		t.Errorf("Expected reloaded IsNvidiaWorkerProxyEnabled == true")
	}
}


// TestLoadConfig_BakRecovery 业务回归(磁盘写满场景):settings 主配置被截断写坏时,
// 启动自动从 .bak 快照恢复,字段值以快照为准,且主文件不被旁移。
func TestLoadConfig_BakRecovery(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, configFileName)
	if err := os.WriteFile(configPath, []byte(`{"relayPort":"19999","language":"en","unfin`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath+".bak", []byte(`{"relayPort":"19988","language":"en"}`), 0644); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager()
	mgr.Init(tempDir)

	if got := mgr.GetRelayPort(); got != "19988" {
		t.Fatalf("应从 .bak 恢复 relayPort=19988,实际 %q", got)
	}
	if got := mgr.GetLanguage(); got != "en" {
		t.Fatalf("应从 .bak 恢复 language=en,实际 %q", got)
	}
	if _, err := os.Stat(configPath + ".corrupt"); !os.IsNotExist(err) {
		t.Fatal("恢复成功不应旁移主文件")
	}
}

// TestLoadConfig_BothCorrupt_Quarantine 业务回归:主与 .bak 双毁时,不加载脏数据、
// 主文件旁移为 .corrupt(字节保留),后续 SaveConfig 从干净起点正常重建。
func TestLoadConfig_BothCorrupt_Quarantine(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, configFileName)
	if err := os.WriteFile(configPath, []byte(`{"relayPort":"1`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath+".bak", []byte(`oops`), 0644); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager()
	mgr.Init(tempDir)

	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatal("双毁后主文件应被旁移")
	}
	if _, err := os.Stat(configPath + ".corrupt"); err != nil {
		t.Fatal(".corrupt 罪证应存在")
	}
	if err := mgr.SaveConfig(); err != nil {
		t.Fatalf("双毁旁移后保存失败: %v", err)
	}
	if _, err := os.Stat(configPath); err != nil {
		t.Fatal("保存后主文件应重建")
	}
}

// TestSettings_AutoModelMapping_DualPool 验证 Auto 模型双配置项(自定义模型池+测速池)的默认值与持久化
func TestSettings_AutoModelMapping_DualPool(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager()
	mgr.Init(tempDir)

	// 1. 默认 auto 映射存在且候选池严格为空
	defaults := mgr.GetRelayModelMapping()
	var autoEntry *ModelMappingEntry
	for _, m := range defaults {
		if m.ClientModel == "auto" {
			autoEntry = &m
			break
		}
	}
	if autoEntry == nil {
		t.Fatal("默认模型映射列表必须包含预置 auto 模型")
	}
	if len(autoEntry.CandidateModels) != 0 {
		t.Fatalf("默认 auto 模型的 CandidateModels 必须严格为空, 实际为: %v", autoEntry.CandidateModels)
	}
	if autoEntry.IsUseBenchmarkPool() {
		t.Fatal("默认 auto 模型的 UseBenchmarkPool 必须为 false")
	}

	// 2. 配置自定义池 + 启用测速池并保存
	useBenchmark := true
	customMappings := []ModelMappingEntry{
		{
			ClientModel:      "auto",
			TargetModel:      "auto",
			Expose:           true,
			CandidateModels:  []string{"gemini-2.5-flash", "deepseek-chat"},
			UseBenchmarkPool: &useBenchmark,
		},
	}
	if err := mgr.SetRelayModelMapping(customMappings); err != nil {
		t.Fatalf("SetRelayModelMapping failed: %v", err)
	}

	// 3. 重新初始化 Manager 并验证落盘持久化与恢复
	newMgr := NewManager()
	newMgr.Init(tempDir)
	reloaded := newMgr.GetRelayModelMapping()
	if len(reloaded) != 1 {
		t.Fatalf("期望恢复 1 个映射项, 实际得到 %d", len(reloaded))
	}
	reloadedAuto := reloaded[0]
	if len(reloadedAuto.CandidateModels) != 2 || reloadedAuto.CandidateModels[0] != "gemini-2.5-flash" || reloadedAuto.CandidateModels[1] != "deepseek-chat" {
		t.Fatalf("CandidateModels 恢复不匹配: %v", reloadedAuto.CandidateModels)
	}
	if !reloadedAuto.IsUseBenchmarkPool() {
		t.Fatal("UseBenchmarkPool 恢复应为 true")
	}
}
