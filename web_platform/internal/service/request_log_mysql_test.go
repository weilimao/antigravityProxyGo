package service

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"antigravity-web-platform/internal/model"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestRequestLogModel_NoBodyFields 严格断言 model.RequestLog 结构体中严禁定义任何请求体或响应体字段
func TestRequestLogModel_NoBodyFields(t *testing.T) {
	val := reflect.TypeOf(model.RequestLog{})
	forbiddenSubstrings := []string{"requestbody", "responsebody", "body", "payload", "rawcontent", "content"}

	for i := 0; i < val.NumField(); i++ {
		field := val.Field(i)
		lowerName := strings.ToLower(field.Name)
		tag := strings.ToLower(string(field.Tag))

		for _, forbidden := range forbiddenSubstrings {
			if strings.Contains(lowerName, forbidden) {
				t.Fatalf("CRITICAL: model.RequestLog must NOT contain body field, found: %s", field.Name)
			}
			if strings.Contains(tag, forbidden) {
				t.Fatalf("CRITICAL: model.RequestLog field %s has forbidden tag: %s", field.Name, field.Tag)
			}
		}
	}
}

// TestRequestLogModel_MigrationAndCRUD 验证轻量模型的数据库建表、插入与标量查询闭环，执行严格 Teardown
func TestRequestLogModel_MigrationAndCRUD(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_req_log.db")

	testDB, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("Failed to open test db: %v", err)
	}

	// 强制 Teardown
	defer func() {
		_ = testDB.Migrator().DropTable(&model.RequestLog{})
		sqlDB, err := testDB.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	}()

	// 自动建表
	if err := testDB.AutoMigrate(&model.RequestLog{}); err != nil {
		t.Fatalf("AutoMigrate failed: %v", err)
	}

	// 验证表中不存在 request_body 或 response_body 字段
	if testDB.Migrator().HasColumn(&model.RequestLog{}, "request_body") {
		t.Fatal("Database table request_logs should NOT have column request_body")
	}
	if testDB.Migrator().HasColumn(&model.RequestLog{}, "response_body") {
		t.Fatal("Database table request_logs should NOT have column response_body")
	}

	// 写入轻量日志
	now := time.Now()
	sample := &model.RequestLog{
		ReqID:        "test-req-001",
		UserID:       "alice",
		Account:      "alice-account",
		ModelName:    "deepseek-ai/DeepSeek-V4-Flash",
		InTokens:     1024,
		OutTokens:    256,
		CachedTokens: 512,
		Cost:         0.0025,
		DurationMs:   350,
		FirstByteMs:  120,
		StatusCode:   200,
		Method:       "POST",
		Path:         "/route/v1/messages",
		CacheStatus:  "HIT",
		CreatedAt:    now,
	}

	if err := testDB.Create(sample).Error; err != nil {
		t.Fatalf("Create request log failed: %v", err)
	}

	// 查询验证
	var queried model.RequestLog
	if err := testDB.Where("req_id = ?", "test-req-001").First(&queried).Error; err != nil {
		t.Fatalf("Query request log failed: %v", err)
	}

	if queried.UserID != "alice" || queried.InTokens != 1024 || queried.CacheStatus != "HIT" {
		t.Errorf("Queried log data mismatch: %+v", queried)
	}
}
