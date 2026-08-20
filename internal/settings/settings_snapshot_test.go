package settings

import (
	"os"
	"reflect"
	"testing"
)

// settings_snapshot_test.go: 「上次远端拉取到的上游模型全集」快照读写往返与隔离性测试。

// TestNvidiaPreferredModelsSnapshot 覆盖 NVIDIA 快照:默认空切片、Set/Get 往返、去空去重落盘、副本隔离、跨实例持久化。
func TestNvidiaPreferredModelsSnapshot(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "antigravity-nv-snap-test")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr := NewManager()
	mgr.Init(tempDir)

	// 1. 默认空切片(非 nil),避免调用方判空歧义
	got := mgr.GetNvidiaPreferredModelsSnapshot()
	if len(got) != 0 {
		t.Errorf("default snapshot should be empty, got %v", got)
	}

	// 2. Set 去 trim + 去重落盘;Get 返回一致
	want := []string{"z-ai/glm-5.2", "  ", "moonshotai/kimi-k2.5", "", "z-ai/glm-5.2", "nvidia/nemotron-3-ultra"}
	if err := mgr.SetNvidiaPreferredModelsSnapshot(want); err != nil {
		t.Fatalf("SetNvidiaPreferredModelsSnapshot failed: %v", err)
	}
	got = mgr.GetNvidiaPreferredModelsSnapshot()
	expected := []string{"z-ai/glm-5.2", "moonshotai/kimi-k2.5", "nvidia/nemotron-3-ultra"}
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("after dedup/trim got %v, want %v", got, expected)
	}

	// 3. 副本隔离:改外部切片不得影响内存态
	got[0] = "tampered"
	got2 := mgr.GetNvidiaPreferredModelsSnapshot()
	if got2[0] != "z-ai/glm-5.2" {
		t.Errorf("snapshot not isolated from caller mutation: got %q, want %q", got2[0], "z-ai/glm-5.2")
	}

	// 4. 跨实例持久化:新 Manager 从磁盘加载应读到上次落盘值
	mgr2 := NewManager()
	mgr2.Init(tempDir)
	got3 := mgr2.GetNvidiaPreferredModelsSnapshot()
	if !reflect.DeepEqual(got3, expected) {
		t.Fatalf("reload from disk got %v, want %v", got3, expected)
	}

	// 5. 整体覆盖语义(非累积):再 Set 一个全量新值应完全替换旧快照
	if err := mgr.SetNvidiaPreferredModelsSnapshot([]string{"deepseek-ai/deepseek-v3", "deepseek-ai/deepseek-v3"}); err != nil {
		t.Fatalf("second Set failed: %v", err)
	}
	got4 := mgr.GetNvidiaPreferredModelsSnapshot()
	if !reflect.DeepEqual(got4, []string{"deepseek-ai/deepseek-v3"}) {
		t.Fatalf("overwrite semantics got %v, want single deepseek entry", got4)
	}
}

// TestRelayChannelModelsSnapshot 覆盖中继 channel 快照:默认空 map、多 channel 各自独立、
// 键小写规范化、单 channel 整体覆盖不影响其它 channel、深拷贝隔离、空 channel no-op、跨实例持久化。
func TestRelayChannelModelsSnapshot(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "antigravity-relay-snap-test")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr := NewManager()
	mgr.Init(tempDir)

	// 1. 默认空 map(非 nil)
	m := mgr.GetRelayChannelModelsSnapshot()
	if len(m) != 0 {
		t.Errorf("default channel snapshot map should be empty, got %v", m)
	}

	// 2. 写入 nvidia channel(含脏数据 trim/dedup)
	if err := mgr.SetRelayChannelModelsSnapshot("NVIDIA", []string{"z-ai/glm-5.2", "", "z-ai/glm-5.2", "nvidia/nemotron"}); err != nil {
		t.Fatalf("set nvidia failed: %v", err)
	}
	// 3. 写入 google channel(channel 键应小写规范化)
	if err := mgr.SetRelayChannelModelsSnapshot("Google", []string{"gemini-2.5-pro", "gemini-2.5-flash"}); err != nil {
		t.Fatalf("set google failed: %v", err)
	}

	m = mgr.GetRelayChannelModelsSnapshot()
	if len(m) != 2 {
		t.Fatalf("expected 2 channels, got %d: %v", len(m), m)
	}
	if nv, ok := m["nvidia"]; !ok || !reflect.DeepEqual(nv, []string{"z-ai/glm-5.2", "nvidia/nemotron"}) {
		t.Errorf("nvidia chan got %v (ok=%v), want trimmed/deduped", nv, ok)
	}
	if !reflect.DeepEqual(m["google"], []string{"gemini-2.5-pro", "gemini-2.5-flash"}) {
		t.Errorf("google chan got %v", m["google"])
	}

	// 4. 单 channel 整体覆盖不影响其它 channel
	if err := mgr.SetRelayChannelModelsSnapshot("nvidia", []string{"deepseek-ai/deepseek-v3"}); err != nil {
		t.Fatalf("overwrite nvidia failed: %v", err)
	}
	m = mgr.GetRelayChannelModelsSnapshot()
	if !reflect.DeepEqual(m["nvidia"], []string{"deepseek-ai/deepseek-v3"}) {
		t.Fatalf("nvidia overwrite got %v", m["nvidia"])
	}
	if !reflect.DeepEqual(m["google"], []string{"gemini-2.5-pro", "gemini-2.5-flash"}) {
		t.Errorf("google chan should remain after nvidia overwrite, got %v", m["google"])
	}

	// 5. 深拷贝隔离:改外部 map/切片不得影响内存态
	m["nvidia"][0] = "tampered"
	m["rogue"] = []string{"x"}
	m2 := mgr.GetRelayChannelModelsSnapshot()
	if m2["nvidia"][0] != "deepseek-ai/deepseek-v3" {
		t.Errorf("channel snapshot not isolated: nvidia[0]=%q", m2["nvidia"][0])
	}
	if _, rogue := m2["rogue"]; rogue {
		t.Errorf("caller-added key leaked into in-memory snapshot: %v", m2["rogue"])
	}

	// 6. 空 channel no-op:不应写入空键
	if err := mgr.SetRelayChannelModelsSnapshot("   ", []string{"should-not-persist"}); err != nil {
		t.Fatalf("blank channel set failed: %v", err)
	}
	m3 := mgr.GetRelayChannelModelsSnapshot()
	if _, empty := m3[""]; empty {
		t.Errorf("blank channel key should not be persisted, got %v", m3[""])
	}
	if len(m3) != 2 {
		t.Errorf("blank channel should not add entry, got %d channels", len(m3))
	}

	// 7. 跨实例持久化
	mgr2 := NewManager()
	mgr2.Init(tempDir)
	m4 := mgr2.GetRelayChannelModelsSnapshot()
	if len(m4) != 2 {
		t.Fatalf("reload got %d channels, want 2", len(m4))
	}
	if !reflect.DeepEqual(m4["nvidia"], []string{"deepseek-ai/deepseek-v3"}) {
		t.Errorf("reload nvidia got %v", m4["nvidia"])
	}
	if !reflect.DeepEqual(m4["google"], []string{"gemini-2.5-pro", "gemini-2.5-flash"}) {
		t.Errorf("reload google got %v", m4["google"])
	}
}
