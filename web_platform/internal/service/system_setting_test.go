package service

import (
	"os"
	"path/filepath"
	"testing"

	"antigravity-web-platform/internal/config"
	"antigravity-web-platform/internal/database"
	"antigravity-web-platform/internal/model"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupSystemTestEnvironment(t *testing.T) func() {
	tempDir, err := os.MkdirTemp("", "antigravity_system_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	dbPath := filepath.Join(tempDir, "test.db")

	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}

	_ = db.AutoMigrate(&model.Setting{})
	database.DB = db

	config.GlobalConfig = &config.Config{
		Gateway: config.GoRelayGatewayConfig{
			GatewayURL: "http://192.168.1.100:18444",
		},
	}

	teardown := func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
		_ = os.RemoveAll(tempDir)
	}

	return teardown
}

func TestSettingService_SystemConfig_Default(t *testing.T) {
	teardown := setupSystemTestEnvironment(t)
	defer teardown()

	settingSvc := NewSettingService()

	// 1. 在数据库没有任何配置时获取
	cfg, err := settingSvc.GetSystemConfig()
	if err != nil {
		t.Fatalf("GetSystemConfig failed: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil SystemConfig")
	}
	// 应当回退到 GatewayURL
	if cfg.APIBaseURL != "http://192.168.1.100:18444" {
		t.Fatalf("expected fallback APIBaseURL 'http://192.168.1.100:18444', got '%s'", cfg.APIBaseURL)
	}
	if cfg.SiteName != "MAX API" {
		t.Errorf("expected default SiteName 'MAX API', got '%s'", cfg.SiteName)
	}
}

func TestSettingService_SystemConfig_SaveAndGet(t *testing.T) {
	teardown := setupSystemTestEnvironment(t)
	defer teardown()

	settingSvc := NewSettingService()

	// 2. 保存自定义配置（测试末尾斜杠修剪与空格去除）
	newCfg := model.SystemConfig{
		APIBaseURL:   "  https://api.mydomain.com:8443/   ",
		SiteName:     "  我的专属 AI 中继站  ",
		Announcement: "欢迎使用全新中继平台",
	}

	if err := settingSvc.SetSystemConfig(&newCfg); err != nil {
		t.Fatalf("SetSystemConfig failed: %v", err)
	}

	// 再次读取校验
	saved, err := settingSvc.GetSystemConfig()
	if err != nil {
		t.Fatalf("GetSystemConfig after save failed: %v", err)
	}
	if saved.APIBaseURL != "https://api.mydomain.com:8443" {
		t.Fatalf("expected trimmed APIBaseURL 'https://api.mydomain.com:8443', got '%s'", saved.APIBaseURL)
	}
	if saved.SiteName != "我的专属 AI 中继站" {
		t.Fatalf("expected trimmed SiteName, got '%s'", saved.SiteName)
	}
	if saved.Announcement != "欢迎使用全新中继平台" {
		t.Fatalf("expected Announcement '欢迎使用全新中继平台', got '%s'", saved.Announcement)
	}
}

func TestSettingService_SystemConfig_NilValidation(t *testing.T) {
	teardown := setupSystemTestEnvironment(t)
	defer teardown()

	settingSvc := NewSettingService()

	// 3. 边界测试：传入 nil 应当报错
	if err := settingSvc.SetSystemConfig(nil); err == nil {
		t.Fatal("expected error when setting nil config, got nil")
	}
}
