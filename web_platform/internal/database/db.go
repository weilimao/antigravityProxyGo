package database

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"antigravity-web-platform/internal/config"
	"antigravity-web-platform/internal/model"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var DB *gorm.DB

func InitDB(cfg *config.Config) (*gorm.DB, error) {
	var dialector gorm.Dialector

	switch cfg.Database.Type {
	case "mysql":
		dialector = mysql.Open(cfg.Database.DSN)
	case "sqlite":
		dir := filepath.Dir(cfg.Database.DSN)
		if dir != "." && dir != "" {
			_ = os.MkdirAll(dir, 0755)
		}
		dialector = sqlite.Open(cfg.Database.DSN)
	default:
		// 默认兜底 sqlite
		_ = os.MkdirAll("data", 0755)
		dialector = sqlite.Open("data/antigravity_web.db")
	}

	db, err := gorm.Open(dialector, &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect database: %w", err)
	}

	// 自动迁移
	err = db.AutoMigrate(
		&model.User{},
		&model.Plan{},
		&model.Order{},
		&model.APIKey{},
		&model.Setting{},
		&model.RequestLog{},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to auto migrate tables: %w", err)
	}

	DB = db

	// 初始化种子数据
	seedData(db)

	return db, nil
}

func seedData(db *gorm.DB) {
	// 1. 初始化管理员
	var adminCount int64
	db.Model(&model.User{}).Where("role = ?", "admin").Count(&adminCount)
	if adminCount == 0 {
		admin := model.User{
			Username: "admin",
			Email:    "admin@antigravity.local",
			Role:     "admin",
			Status:   "active",
		}
		_ = admin.SetPassword("admin123")
		db.Create(&admin)
	}

	// 2. 初始化套餐
	var planCount int64
	db.Model(&model.Plan{}).Count(&planCount)
	if planCount == 0 {
		defaultPlans := []model.Plan{
			{
				Name:         "开发者基础版 (Pro)",
				Description:  "适合个人日常开发，畅享主流全系模型",
				PriceCents:   3900, // 39.00 元
				DurationDays: 30,
				AllowedModels: []string{},
				AutoModels:    []string{"claude-3-7-sonnet", "gemini-2.5-flash", "deepseek-chat"},
				RateLimit: 30,
				SortOrder: 1,
				Status:    "active",
				Quotas: model.PlanQuotas{
					Gemini: model.QuotaBucket{
						EnableHourly: true, HourlyHours: 5, HourlyTokens: 50000,
						EnableDaily: true, DailyDays: 7, DailyTokens: 500000,
					},
					Claude: model.QuotaBucket{
						EnableHourly: true, HourlyHours: 5, HourlyTokens: 50000,
						EnableDaily: true, DailyDays: 7, DailyTokens: 500000,
					},
					Nvidia: model.QuotaBucket{
						EnableHourly: true, HourlyHours: 5, HourlyTokens: 100000,
						EnableDaily: true, DailyDays: 7, DailyTokens: 1000000,
					},
					Grok: model.QuotaBucket{
						EnableHourly: true, HourlyHours: 5, HourlyTokens: 50000,
						EnableDaily: true, DailyDays: 7, DailyTokens: 500000,
					},
					RateLimit: 30,
				},
			},
			{
				Name:         "团队进阶版 (Pro 5x)",
				Description:  "5倍高配额，高频编程辅助，支持全模态与竞速",
				PriceCents:   9900, // 99.00 元
				DurationDays: 30,
				AllowedModels: []string{},
				AutoModels:    []string{"claude-3-7-sonnet", "gemini-2.5-flash", "deepseek-chat", "gpt-4o"},
				RateLimit: 60,
				SortOrder: 2,
				Status:    "active",
				Quotas: model.PlanQuotas{
					Gemini: model.QuotaBucket{
						EnableHourly: true, HourlyHours: 5, HourlyTokens: 250000,
						EnableDaily: true, DailyDays: 7, DailyTokens: 2500000,
					},
					Claude: model.QuotaBucket{
						EnableHourly: true, HourlyHours: 5, HourlyTokens: 250000,
						EnableDaily: true, DailyDays: 7, DailyTokens: 2500000,
					},
					Nvidia: model.QuotaBucket{
						EnableHourly: true, HourlyHours: 5, HourlyTokens: 500000,
						EnableDaily: true, DailyDays: 7, DailyTokens: 5000000,
					},
					Grok: model.QuotaBucket{
						EnableHourly: true, HourlyHours: 5, HourlyTokens: 250000,
						EnableDaily: true, DailyDays: 7, DailyTokens: 2500000,
					},
					RateLimit: 60,
				},
			},
		}

		for _, p := range defaultPlans {
			db.Create(&p)
		}
	}

	// 3. 初始化全局设置 (默认不预置任何模型，由管理员在平台显式配置)
	initSetting(db, "ocr_model", "", "全局 OCR 入站图片自愈降级模型")
	initSetting(db, "model_mappings", "[]", "中继模型映射与号池路由规则")

	defaultAuto := model.AutoRacingConfig{
		Enabled:          false,
		CandidateModels:  []string{},
		UseBenchmarkPool: false,
	}
	autoJSON, _ := json.Marshal(defaultAuto)
	initSetting(db, "auto_racing_config", string(autoJSON), "全局 Auto 并发竞速默认规则")
}

func initSetting(db *gorm.DB, key, val, desc string) {
	var count int64
	db.Model(&model.Setting{}).Where("`key` = ?", key).Count(&count)
	if count == 0 {
		db.Create(&model.Setting{
			Key:         key,
			Value:       val,
			Description: desc,
			UpdatedAt:   time.Now(),
		})
	}
}
