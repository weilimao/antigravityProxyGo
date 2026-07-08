package relay

import (
	"os"
	"testing"
	"time"

	"antigravity-proxy/internal/db"
)

func TestUpdateUserQuota_ResetLimit(t *testing.T) {
	// 1. 初始化临时 SQLite 数据库
	tempDir, err := os.MkdirTemp("", "relay-quota-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	err = db.InitDB(tempDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.CloseDB()

	// 2. 初始化 UserManager
	userMgr := NewUserManager()
	userMgr.persistPath = tempDir + "/relay_users_test.json"

	user, err := userMgr.AddUser("test_reset_user", "password123", "Unit Test User")
	if err != nil {
		t.Fatalf("failed to add user: %v", err)
	}

	// 3. 不带 resetLimit = false 更新限额
	quotas := UserQuotas{
		Gemini: ModelQuota{
			EnableFixed:  true,
			FixedTokens:  100000,
			EnableHourly: true,
			HourlyHours:  5,
			HourlyTokens: 10000,
		},
		Claude: ModelQuota{
			EnableFixed: true,
			FixedTokens: 200000,
		},
	}

	err = userMgr.UpdateUserQuota(user.ID, quotas, false)
	if err != nil {
		t.Fatalf("failed to update quota: %v", err)
	}

	updatedUser := userMgr.GetUserByID(user.ID)
	if updatedUser.Quotas.Gemini.ResetAt != "" || updatedUser.Quotas.Claude.ResetAt != "" {
		t.Errorf("expected ResetAt to be empty, got Gemini: %q, Claude: %q", updatedUser.Quotas.Gemini.ResetAt, updatedUser.Quotas.Claude.ResetAt)
	}

	// 验证 SQLite 中的滚动周期窗口起始时间未设置
	winStart, err := db.GetQuotaWindowStart(user.ID, "gemini_hourly")
	if err != nil {
		t.Fatalf("failed to get window start: %v", err)
	}
	if winStart != "" {
		t.Errorf("expected window start to be empty, got %q", winStart)
	}

	// 4. 传入 resetLimit = true 进行限额重置
	timeBeforeReset := time.Now().Add(-2 * time.Second)
	err = userMgr.UpdateUserQuota(user.ID, quotas, true)
	if err != nil {
		t.Fatalf("failed to update quota with reset: %v", err)
	}
	timeAfterReset := time.Now().Add(2 * time.Second)

	resetUser := userMgr.GetUserByID(user.ID)
	geminiResetAt := resetUser.Quotas.Gemini.ResetAt
	claudeResetAt := resetUser.Quotas.Claude.ResetAt

	if geminiResetAt == "" || claudeResetAt == "" {
		t.Fatal("expected ResetAt to be set, but got empty")
	}
	if geminiResetAt != claudeResetAt {
		t.Errorf("expected Gemini and Claude reset times to match, got %q and %q", geminiResetAt, claudeResetAt)
	}

	parsedReset, err := time.Parse(time.RFC3339, geminiResetAt)
	if err != nil {
		t.Fatalf("failed to parse reset time: %v", err)
	}
	if parsedReset.Before(timeBeforeReset) || parsedReset.After(timeAfterReset) {
		t.Errorf("expected reset time to be close to now, got %v", parsedReset)
	}

	// 验证 SQLite 中 `gemini_hourly` 滚动窗口开始时间已被设置为重置时间
	dbWinStart, err := db.GetQuotaWindowStart(user.ID, "gemini_hourly")
	if err != nil {
		t.Fatalf("failed to get window start from DB: %v", err)
	}
	if dbWinStart != geminiResetAt {
		t.Errorf("expected DB window start to be %q, got %q", geminiResetAt, dbWinStart)
	}

	// 验证 SQLite 中 `gemini_daily` 滚动窗口开始时间也被设置为重置时间
	dbDailyWinStart, err := db.GetQuotaWindowStart(user.ID, "gemini_daily")
	if err != nil {
		t.Fatalf("failed to get daily window start from DB: %v", err)
	}
	if dbDailyWinStart != geminiResetAt {
		t.Errorf("expected DB daily window start to be %q, got %q", geminiResetAt, dbDailyWinStart)
	}
}
