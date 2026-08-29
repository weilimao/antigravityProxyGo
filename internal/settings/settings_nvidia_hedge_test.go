package settings

import (
	"os"
	"testing"
)

// TestSettings_NvidiaHedge 锁定对冲开关/延迟的默认值、归一化(0/负→10000,钳位
// [2000,60000])与落盘往返。对仗 TestSettings_GrokWorkerProxy 的 Manager 真实 Init 口径。
func TestSettings_NvidiaHedge(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "settings_nvidia_hedge_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr := NewManager()
	mgr.Init(tempDir)

	// 初始状态:默认关闭,延迟取默认 10000(未配置 0 值归一化)。
	if mgr.IsNvidiaHedgeEnabled() {
		t.Errorf("Expected IsNvidiaHedgeEnabled == false initially")
	}
	if got := mgr.GetNvidiaHedgeDelayMs(); got != defaultNvidiaHedgeDelayMs {
		t.Errorf("Expected default delay %d, got %d", defaultNvidiaHedgeDelayMs, got)
	}

	// 开关往返。
	if err := mgr.SetNvidiaHedgeEnabled(true); err != nil {
		t.Fatalf("SetNvidiaHedgeEnabled failed: %v", err)
	}
	if !mgr.IsNvidiaHedgeEnabled() {
		t.Errorf("Expected IsNvidiaHedgeEnabled == true after set")
	}

	// 延迟归一化:常规值原样;0/负 → 默认;越界钳位。
	cases := []struct {
		in   int
		want int
	}{
		{15000, 15000},
		{0, defaultNvidiaHedgeDelayMs},
		{-5, defaultNvidiaHedgeDelayMs},
		{500, minNvidiaHedgeDelayMs},
		{999999, maxNvidiaHedgeDelayMs},
		{2000, minNvidiaHedgeDelayMs},
		{60000, maxNvidiaHedgeDelayMs},
	}
	for _, c := range cases {
		if err := mgr.SetNvidiaHedgeDelayMs(c.in); err != nil {
			t.Fatalf("SetNvidiaHedgeDelayMs(%d) failed: %v", c.in, err)
		}
		if got := mgr.GetNvidiaHedgeDelayMs(); got != c.want {
			t.Errorf("SetNvidiaHedgeDelayMs(%d) → Get 期望 %d,实际 %d", c.in, c.want, got)
		}
	}

	// 并发数:默认 2(零值归一化,向后兼容);settings 层钳位 [2,512](产品级上限
	// 由 IPC 写入处按启用号数动态钳,此处仅锁存储层防脏值保险丝)。
	if got := mgr.GetNvidiaHedgeMaxParallel(); got != defaultNvidiaHedgeMaxParallel {
		t.Errorf("Expected default maxParallel %d, got %d", defaultNvidiaHedgeMaxParallel, got)
	}
	parallelCases := []struct {
		in   int
		want int
	}{
		{3, 3},
		{5, 5},
		{117, 117},
		{0, defaultNvidiaHedgeMaxParallel},
		{-1, defaultNvidiaHedgeMaxParallel},
		{1, minNvidiaHedgeMaxParallel},
		{512, maxNvidiaHedgeMaxParallel},
		{999, maxNvidiaHedgeMaxParallel},
	}
	for _, c := range parallelCases {
		if err := mgr.SetNvidiaHedgeMaxParallel(c.in); err != nil {
			t.Fatalf("SetNvidiaHedgeMaxParallel(%d) failed: %v", c.in, err)
		}
		if got := mgr.GetNvidiaHedgeMaxParallel(); got != c.want {
			t.Errorf("SetNvidiaHedgeMaxParallel(%d) → Get 期望 %d,实际 %d", c.in, c.want, got)
		}
	}

	// 落盘往返:开关、延迟(60000)与并发(117,池级上限内的池规模值)重启后保持一致。
	if err := mgr.SetNvidiaHedgeMaxParallel(117); err != nil {
		t.Fatalf("SetNvidiaHedgeMaxParallel(117) failed: %v", err)
	}
	// 即刻竞赛开关:默认 false(延迟对冲模式) → set true → 重启后仍 true。
	if mgr.IsNvidiaHedgeImmediate() {
		t.Errorf("Expected IsNvidiaHedgeImmediate == false initially")
	}
	if err := mgr.SetNvidiaHedgeImmediate(true); err != nil {
		t.Fatalf("SetNvidiaHedgeImmediate failed: %v", err)
	}
	if !mgr.IsNvidiaHedgeImmediate() {
		t.Errorf("Expected IsNvidiaHedgeImmediate == true after set")
	}
	mgr2 := NewManager()
	mgr2.Init(tempDir)
	if !mgr2.IsNvidiaHedgeEnabled() {
		t.Errorf("Expected reloaded IsNvidiaHedgeEnabled == true")
	}
	if got := mgr2.GetNvidiaHedgeDelayMs(); got != maxNvidiaHedgeDelayMs {
		t.Errorf("Expected reloaded delay %d, got %d", maxNvidiaHedgeDelayMs, got)
	}
	if got := mgr2.GetNvidiaHedgeMaxParallel(); got != 117 {
		t.Errorf("Expected reloaded maxParallel 117, got %d", got)
	}
	if !mgr2.IsNvidiaHedgeImmediate() {
		t.Errorf("Expected reloaded IsNvidiaHedgeImmediate == true")
	}
}
