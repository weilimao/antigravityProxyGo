package settings

import (
	"path/filepath"
	"testing"
)

func TestPoolErrorRetry_Normalize(t *testing.T) {
	input := PoolErrorRetryConfig{
		Enabled:            true,
		SwitchCodes:        []int{503, 429, 99, 600, 429, 403},
		RetryCodes:         []int{500, 502, 503, 504, 500}, // 503 与 SwitchCodes 冲突，应被移除
		SwitchCooldownSecs: -10,                             // 小于 0 应该被设为 0
		MaxRetries:         20,                              // 大于 10 应该钳位为 10
	}

	normalized := NormalizePoolErrorRetry(input)

	// SwitchCodes 应该是 [403, 429, 503]
	expectedSwitch := []int{403, 429, 503}
	if len(normalized.SwitchCodes) != len(expectedSwitch) {
		t.Fatalf("expected SwitchCodes len %d, got %d", len(expectedSwitch), len(normalized.SwitchCodes))
	}
	for i, v := range expectedSwitch {
		if normalized.SwitchCodes[i] != v {
			t.Errorf("expected SwitchCodes[%d] = %d, got %d", i, v, normalized.SwitchCodes[i])
		}
	}

	// RetryCodes 应该排除 503 并去重排序为 [500, 502, 504]
	expectedRetry := []int{500, 502, 504}
	if len(normalized.RetryCodes) != len(expectedRetry) {
		t.Fatalf("expected RetryCodes len %d, got %d", len(expectedRetry), len(normalized.RetryCodes))
	}
	for i, v := range expectedRetry {
		if normalized.RetryCodes[i] != v {
			t.Errorf("expected RetryCodes[%d] = %d, got %d", i, v, normalized.RetryCodes[i])
		}
	}

	if normalized.SwitchCooldownSecs != 0 {
		t.Errorf("expected SwitchCooldownSecs 0, got %d", normalized.SwitchCooldownSecs)
	}

	if normalized.MaxRetries != 10 {
		t.Errorf("expected MaxRetries 10, got %d", normalized.MaxRetries)
	}
}

func TestPoolErrorRetry_MatchAction(t *testing.T) {
	cfg := &PoolErrorRetryConfig{
		Enabled:     true,
		SwitchCodes: []int{429, 503},
		RetryCodes:  []int{500, 502},
	}

	if act := cfg.MatchAction(429); act != "switch" {
		t.Errorf("expected 'switch' for 429, got %q", act)
	}
	if act := cfg.MatchAction(503); act != "switch" {
		t.Errorf("expected 'switch' for 503, got %q", act)
	}
	if act := cfg.MatchAction(500); act != "retry" {
		t.Errorf("expected 'retry' for 500, got %q", act)
	}
	if act := cfg.MatchAction(502); act != "retry" {
		t.Errorf("expected 'retry' for 502, got %q", act)
	}
	if act := cfg.MatchAction(404); act != "" {
		t.Errorf("expected '' for 404, got %q", act)
	}

	// 当未启用时，即使状态码匹配也应该返回 ""
	cfg.Enabled = false
	if act := cfg.MatchAction(429); act != "" {
		t.Errorf("expected '' when disabled, got %q", act)
	}
}

func TestManager_PoolErrorRetry_Persistence(t *testing.T) {
	// 隔离环境，自动清理
	tempDir := t.TempDir()
	mgr := NewManager()
	mgr.Init(tempDir)

	// 初始默认应为未启用或零值
	initial := mgr.GetPoolErrorRetry()
	if initial.Enabled {
		t.Errorf("expected initial Enabled to be false")
	}

	toSet := PoolErrorRetryConfig{
		Enabled:            true,
		SwitchCodes:        []int{429, 403},
		RetryCodes:         []int{500, 502},
		SwitchCooldownSecs: 30,
		MaxRetries:         4,
	}

	if err := mgr.SetPoolErrorRetry(toSet); err != nil {
		t.Fatalf("SetPoolErrorRetry failed: %v", err)
	}

	got := mgr.GetPoolErrorRetry()
	if !got.Enabled {
		t.Errorf("expected Enabled true, got false")
	}
	if got.SwitchCooldownSecs != 30 {
		t.Errorf("expected SwitchCooldownSecs 30, got %d", got.SwitchCooldownSecs)
	}
	if got.MaxRetries != 4 {
		t.Errorf("expected MaxRetries 4, got %d", got.MaxRetries)
	}

	// 重新初始化，验证磁盘加载
	mgr2 := NewManager()
	mgr2.Init(tempDir)
	got2 := mgr2.GetPoolErrorRetry()
	if !got2.Enabled {
		t.Errorf("expected reloaded Enabled true, got false")
	}
	if len(got2.SwitchCodes) != 2 || got2.SwitchCodes[0] != 403 || got2.SwitchCodes[1] != 429 {
		t.Errorf("unexpected reloaded SwitchCodes: %v", got2.SwitchCodes)
	}

	// 清理保证：由 t.TempDir() 自动并在测试结束时回收
	_ = filepath.Join(tempDir, "config.json")
}
