package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"antigravity-web-platform/internal/config"
	"antigravity-web-platform/internal/database"
	"antigravity-web-platform/internal/model"
	"antigravity-web-platform/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupBenchmarkTestEnv(t *testing.T) (*gorm.DB, func()) {
	gin.SetMode(gin.TestMode)
	tempDir, err := os.MkdirTemp("", "antigravity_bm_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	dbPath := filepath.Join(tempDir, "bm_test.db")

	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		_ = os.RemoveAll(tempDir)
		t.Fatalf("failed to open test db: %v", err)
	}

	_ = db.AutoMigrate(
		&model.User{},
		&model.Plan{},
		&model.Order{},
		&model.APIKey{},
		&model.Setting{},
	)

	origDB := database.DB
	origConfig := config.GlobalConfig

	database.DB = db
	config.GlobalConfig = &config.Config{
		Server: config.ServerConfig{
			JWTSecret:        "test-jwt-secret-bm",
			TokenExpireHours: 24,
		},
		Gateway: config.GoRelayGatewayConfig{
			GatewayURL:  "http://127.0.0.1:18444",
			AdminKey:    "sk-ant-admin",
			SyncEnabled: true,
		},
	}

	teardown := func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
		_ = os.RemoveAll(tempDir)
		database.DB = origDB
		config.GlobalConfig = origConfig
	}

	return db, teardown
}

func TestGetBenchmarkModels_AggregationAndDeduplication(t *testing.T) {
	_, teardown := setupBenchmarkTestEnv(t)
	defer teardown()

	// 1. 模拟网关返回 models
	gatewayServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/admin/benchmark/models" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"models": []string{
					"nvidia/moonshotai/kimi-k3",
					"nvidia/z-ai/glm-5.3-flash",
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer gatewayServer.Close()

	config.GlobalConfig.Gateway.GatewayURL = gatewayServer.URL

	// 2. 本地数据库插入模型映射，包含用户在第二张图中的 nvidia/z-ai/glm-5.3
	settingSvc := service.NewSettingService()
	localMappings := []model.ModelMappingEntry{
		{
			ClientModel: "nvidia/z-ai/glm-5.3",
			TargetModel: "z-ai/glm-5.3",
			Expose:      true,
		},
		{
			ClientModel: "nvidia/z-ai/glm-5.3-flash", // 与网关重复，测试去重
			TargetModel: "z-ai/glm-5.3-flash",
			Expose:      true,
		},
		{
			ClientModel: "nvidia/hidden-model",
			TargetModel: "hidden-model",
			Expose:      false, // expose=false，不应被选入候选池
		},
		{
			ClientModel: "auto", // 虚拟竞速入口，不应被选入候选池
			TargetModel: "auto",
			Expose:      true,
		},
	}
	if err := settingSvc.SetModelMappings(localMappings); err != nil {
		t.Fatalf("SetModelMappings failed: %v", err)
	}

	// 3. 调用 GetBenchmarkModels 接口
	handler := NewAdminBenchmarkHandler()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	handler.GetBenchmarkModels(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Code    int  `json:"code"`
		Success bool `json:"success"`
		Data    struct {
			Success bool     `json:"success"`
			Models  []string `json:"models"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !resp.Success || !resp.Data.Success {
		t.Fatalf("expected success, got: %+v", resp)
	}

	modelMap := make(map[string]bool)
	for _, m := range resp.Data.Models {
		modelMap[m] = true
	}

	// 验证：必须包含本地模型映射中的 nvidia/z-ai/glm-5.3
	if !modelMap["nvidia/z-ai/glm-5.3"] {
		t.Errorf("expected nvidia/z-ai/glm-5.3 to be present, but got: %v", resp.Data.Models)
	}
	// 验证：包含网关原有的模型
	if !modelMap["nvidia/moonshotai/kimi-k3"] {
		t.Errorf("expected nvidia/moonshotai/kimi-k3 to be present, but got: %v", resp.Data.Models)
	}
	if !modelMap["nvidia/z-ai/glm-5.3-flash"] {
		t.Errorf("expected nvidia/z-ai/glm-5.3-flash to be present, but got: %v", resp.Data.Models)
	}
	// 验证：未暴露模型被排除
	if modelMap["nvidia/hidden-model"] {
		t.Errorf("nvidia/hidden-model with expose=false should NOT be included")
	}
	// 验证：auto 虚拟模型被排除
	if modelMap["auto"] {
		t.Errorf("auto virtual model should NOT be included")
	}
}

func TestGetBenchmarkModels_GatewayFallback(t *testing.T) {
	_, teardown := setupBenchmarkTestEnv(t)
	defer teardown()

	// 模拟网关不可用（端口无效）
	config.GlobalConfig.Gateway.GatewayURL = "http://127.0.0.1:54321"

	// 仅配置本地数据库模型映射
	settingSvc := service.NewSettingService()
	localMappings := []model.ModelMappingEntry{
		{
			ClientModel: "nvidia/z-ai/glm-5.3",
			TargetModel: "z-ai/glm-5.3",
			Expose:      true,
		},
	}
	if err := settingSvc.SetModelMappings(localMappings); err != nil {
		t.Fatalf("SetModelMappings failed: %v", err)
	}

	handler := NewAdminBenchmarkHandler()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	handler.GetBenchmarkModels(c)

	// 网关不可达时，应优雅降级返回本地模型，返回 HTTP 200
	if w.Code != http.StatusOK {
		t.Fatalf("expected fallback 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Code int `json:"code"`
		Data struct {
			Success bool     `json:"success"`
			Models  []string `json:"models"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Data.Models) != 1 || resp.Data.Models[0] != "nvidia/z-ai/glm-5.3" {
		t.Errorf("expected fallback to return [nvidia/z-ai/glm-5.3], got: %v", resp.Data.Models)
	}
}
