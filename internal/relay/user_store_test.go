package relay

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"antigravity-proxy/internal/platform/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestUserUsageStore_FileMode_Debounced 验证单机文件模式下的防抖批处理特性与资源清理
func TestUserUsageStore_FileMode_Debounced(t *testing.T) {
	var flushCount int64

	flushFn := func() {
		atomic.AddInt64(&flushCount, 1)
	}

	store := NewFileUserUsageStore(flushFn)
	t.Cleanup(func() {
		_ = store.Close()
	})

	if store.IsDatabaseMode() {
		t.Fatal("expected file mode, got db mode")
	}

	// 连续记录 50 次用量，不应立即触发 50 次 flush
	for i := 0; i < 50; i++ {
		store.RecordUsage(UsageRecord{
			UserID:   "user_1",
			APIKeyID: "key_1",
			Tokens:   100,
		})
	}

	// 立即检查，flushCount 应该为 0（处于防抖缓冲期）
	if atomic.LoadInt64(&flushCount) != 0 {
		t.Fatalf("expected 0 flushes immediately, got %d", atomic.LoadInt64(&flushCount))
	}

	// 手动触发 Flush 提交
	if err := store.Flush(); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	if atomic.LoadInt64(&flushCount) != 1 {
		t.Fatalf("expected 1 flush after manual Flush(), got %d", atomic.LoadInt64(&flushCount))
	}
}

// TestUserUsageStore_DBMode_AtomicUpdate 验证数据库模式下原子 SQL 更新与用量累加
func TestUserUsageStore_DBMode_AtomicUpdate(t *testing.T) {
	// 使用隔离的 SQLite 内存数据库模拟现网 MySQL
	testDB, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("failed to open memory sqlite: %v", err)
	}

	t.Cleanup(func() {
		sqlDB, err := testDB.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})

	// 自动迁移模型表
	if err := testDB.AutoMigrate(&model.User{}, &model.APIKey{}); err != nil {
		t.Fatalf("AutoMigrate failed: %v", err)
	}

	// 创建测试用户与测试 Key
	user := model.User{
		Username: "db_test_user",
		Role:     "user",
		Status:   "active",
	}
	testDB.Create(&user)

	apiKey := model.APIKey{
		Key:         "sk-ant-test-db-mode-key",
		UserID:      user.ID,
		Name:        "Test Key",
		LimitTokens: 1000000,
		UsedTokens:  0,
		Status:      "active",
	}
	testDB.Create(&apiKey)

	store := NewDBUserUsageStore(testDB)
	t.Cleanup(func() {
		_ = store.Close()
	})

	if !store.IsDatabaseMode() {
		t.Fatal("expected database mode")
	}

	// 记录 Gemini 用量 1000 Tokens
	store.RecordUsage(UsageRecord{
		UserID:    "db_test_user",
		APIKeyID:  apiKey.Key,
		KeyStr:    apiKey.Key,
		Family:    FamilyGemini,
		Tokens:    1000,
		Timestamp: time.Now(),
	})

	// 记录 Claude 用量 2500 Tokens
	store.RecordUsage(UsageRecord{
		UserID:    "db_test_user",
		APIKeyID:  apiKey.Key,
		KeyStr:    apiKey.Key,
		Family:    FamilyClaude,
		Tokens:    2500,
		Timestamp: time.Now(),
	})

	// 显式排空写入
	if err := store.Flush(); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	// 从数据库校验回查
	var checkKey model.APIKey
	if err := testDB.First(&checkKey, apiKey.ID).Error; err != nil {
		t.Fatalf("failed to find api key in db: %v", err)
	}

	if checkKey.UsedTokens != 3500 {
		t.Fatalf("expected used_tokens 3500, got %d", checkKey.UsedTokens)
	}
	if checkKey.UsedGeminiTokens != 1000 {
		t.Fatalf("expected used_gemini_tokens 1000, got %d", checkKey.UsedGeminiTokens)
	}
	if checkKey.UsedClaudeTokens != 2500 {
		t.Fatalf("expected used_claude_tokens 2500, got %d", checkKey.UsedClaudeTokens)
	}
	if checkKey.LastUsedAt == nil {
		t.Fatal("expected last_used_at to be populated")
	}
}

// TestUserManager_DBIntegration_And_Fallback 验证 UserManager 整合 DB 与回表发现能力
func TestUserManager_DBIntegration_And_Fallback(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "relay_mgr_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(tempDir)
	})

	testDB, err := gorm.Open(sqlite.Open(filepath.Join(tempDir, "test.db")), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := testDB.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	})

	_ = testDB.AutoMigrate(&model.User{}, &model.APIKey{})

	// 模拟 Web 平台在数据库中新增了一个 Key，此时 UserManager 内存中尚未存在
	testUser := model.User{
		Username: "web_admin",
		Role:     "admin",
		Status:   "active",
	}
	testDB.Create(&testUser)

	testKey := model.APIKey{
		Key:         "sk-ant-from-web-platform-001",
		UserID:      testUser.ID,
		Name:        "Web Key",
		LimitTokens: 500000,
		UsedTokens:  10000,
		Status:      "active",
	}
	testDB.Create(&testKey)

	mgr := NewUserManager()
	mgr.SetDB(testDB)
	mgr.Init(tempDir)
	t.Cleanup(func() {
		_ = mgr.Close()
	})

	// 校验通过 DB 回表自动识别与装载
	u, k, err := mgr.ValidateAPIKey("sk-ant-from-web-platform-001")
	if err != nil {
		t.Fatalf("ValidateAPIKey failed to find key from DB: %v", err)
	}
	if u.Key != "web_admin" {
		t.Fatalf("expected username web_admin, got %s", u.Key)
	}
	if k.Key != "sk-ant-from-web-platform-001" {
		t.Fatalf("expected key sk-ant-from-web-platform-001, got %s", k.Key)
	}

	// 记录用量并验证落库
	mgr.RecordAPIKeyUsageForFamily(u.ID, k.Key, FamilyGrok, 500)
	_ = mgr.usageStore.Flush()

	var updatedKey model.APIKey
	testDB.First(&updatedKey, testKey.ID)
	if updatedKey.UsedTokens != 10500 {
		t.Fatalf("expected updated UsedTokens 10500, got %d", updatedKey.UsedTokens)
	}
	if updatedKey.UsedGrokTokens != 500 {
		t.Fatalf("expected updated UsedGrokTokens 500, got %d", updatedKey.UsedGrokTokens)
	}
}

// TestUserManager_HighConcurrency_NoLag 验证 1,000 次高并发请求下的零锁等待与稳定性
func TestUserManager_HighConcurrency_NoLag(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "relay_concurrency_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(tempDir)
	})

	testDB, err := gorm.Open(sqlite.Open(filepath.Join(tempDir, "concurrency.db")), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := testDB.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	})

	_ = testDB.AutoMigrate(&model.User{}, &model.APIKey{})

	user := model.User{Username: "concurrency_user", Status: "active"}
	testDB.Create(&user)
	key := model.APIKey{Key: "sk-ant-concurrency-key", UserID: user.ID, Status: "active"}
	testDB.Create(&key)

	mgr := NewUserManager()
	mgr.SetDB(testDB)
	mgr.Init(tempDir)
	t.Cleanup(func() {
		_ = mgr.Close()
	})

	start := time.Now()
	var wg sync.WaitGroup
	concurrentWorkers := 20
	iterationsPerWorker := 50 // 总计 1,000 次用量扣减

	for i := 0; i < concurrentWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterationsPerWorker; j++ {
				mgr.RecordAPIKeyUsage("1", "sk-ant-concurrency-key", false, 10)
			}
		}()
	}

	wg.Wait()
	elapsed := time.Since(start)

	// 1,000 次并发写入应在 500ms 内完成，绝对不能卡死
	if elapsed > 2*time.Second {
		t.Fatalf("concurrency recording took too long: %v", elapsed)
	}

	// 排空提交并验证最终累加值
	_ = mgr.usageStore.Flush()

	var finalKey model.APIKey
	testDB.First(&finalKey, key.ID)
	expectedTokens := int64(concurrentWorkers * iterationsPerWorker * 10) // 10,000 Tokens
	if finalKey.UsedTokens != expectedTokens {
		t.Fatalf("expected final tokens %d, got %d", expectedTokens, finalKey.UsedTokens)
	}
}

// TestUserManager_RecordWorkbuddyAndNvidiaUsage 验证 WorkBuddy 与 NVIDIA 请求用量精准落入各自独立渠道桶，绝不误入 Gemini
func TestUserManager_RecordWorkbuddyAndNvidiaUsage(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "relay_workbuddy_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(tempDir)
	})

	testDB, err := gorm.Open(sqlite.Open(filepath.Join(tempDir, "wb_test.db")), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}

	if errMigrate := testDB.AutoMigrate(&model.User{}, &model.APIKey{}); errMigrate != nil {
		t.Fatalf("failed to migrate schema: %v", errMigrate)
	}

	testUser := model.User{
		Username: "wb_user",
		Role:     "user",
		Status:   "active",
	}
	testDB.Create(&testUser)

	testKey := model.APIKey{
		UserID:     testUser.ID,
		Name:       "WB Test Key",
		Key:        "sk-ant-wb-key-001",
		Status:     "active",
		UsedTokens: 0,
	}
	testDB.Create(&testKey)

	mgr := &UserManager{
		users:      make([]*RelayUser, 0),
		gormDB:     testDB,
		usageStore: NewDBUserUsageStore(testDB),
	}
	t.Cleanup(func() {
		_ = mgr.usageStore.Close()
	})

	u, k, err := mgr.ValidateAPIKey("sk-ant-wb-key-001")
	if err != nil {
		t.Fatalf("ValidateAPIKey failed: %v", err)
	}

	// 1. 模拟 WorkBuddy 请求 100,000 Tokens
	mgr.RecordAPIKeyUsageForFamily(u.ID, k.Key, FamilyWorkbuddy, 100000)

	// 2. 模拟 NVIDIA 请求 50,000 Tokens
	mgr.RecordAPIKeyUsageForFamily(u.ID, k.Key, FamilyNvidia, 50000)

	// 3. 强制 Flush
	if errFlush := mgr.usageStore.Flush(); errFlush != nil {
		t.Fatalf("Flush failed: %v", errFlush)
	}

	// 4. 从 DB 重新查询并断言
	var updatedKey model.APIKey
	if errFind := testDB.First(&updatedKey, testKey.ID).Error; errFind != nil {
		t.Fatalf("Failed to query updated key: %v", errFind)
	}

	if updatedKey.UsedTokens != 150000 {
		t.Errorf("expected UsedTokens 150000, got %d", updatedKey.UsedTokens)
	}
	if updatedKey.UsedWorkbuddyTokens != 100000 {
		t.Errorf("expected UsedWorkbuddyTokens 100000, got %d", updatedKey.UsedWorkbuddyTokens)
	}
	if updatedKey.UsedNvidiaTokens != 50000 {
		t.Errorf("expected UsedNvidiaTokens 50000, got %d", updatedKey.UsedNvidiaTokens)
	}
	if updatedKey.UsedGeminiTokens != 0 {
		t.Errorf("CRITICAL BUG: UsedGeminiTokens must be 0, but got %d", updatedKey.UsedGeminiTokens)
	}
}
