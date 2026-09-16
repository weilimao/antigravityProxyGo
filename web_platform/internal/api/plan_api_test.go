package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"antigravity-web-platform/internal/model"
	"antigravity-web-platform/internal/pkg/jwt"
)

func TestAdminPlanAutoModelsAPICycle(t *testing.T) {
	teardown := setupAPITestEnvironment(t)
	defer teardown()

	router := SetupRouter()

	// 1. 生成管理员 JWT Token
	adminToken, err := jwt.GenerateToken(9999, "admin_test", "admin", "test-api-jwt-secret-999", 24)
	if err != nil {
		t.Fatalf("failed to generate admin token: %v", err)
	}

	// 2. 管理员通过 POST /api/v1/admin/plans 创建包含 AutoModels 的套餐
	createPlanPayload := map[string]interface{}{
		"name":          "Auto智能竞速旗舰版",
		"description":   "端到端验证 autoModels 字段在 API 与数据库间往返",
		"priceCents":    8800,
		"durationDays":  30,
		"allowedModels": []string{"auto", "claude-3-7-sonnet"},
		"autoModels":    []string{"claude-3-7-sonnet", "gemini-2.5-flash", "deepseek-chat"},
		"rateLimit":     60,
		"status":        "active",
	}
	createBytes, _ := json.Marshal(createPlanPayload)
	reqCreate, _ := http.NewRequest(http.MethodPost, "/api/v1/admin/plans", bytes.NewReader(createBytes))
	reqCreate.Header.Set("Content-Type", "application/json")
	reqCreate.Header.Set("Authorization", "Bearer "+adminToken)
	wCreate := httptest.NewRecorder()
	router.ServeHTTP(wCreate, reqCreate)

	if wCreate.Code != http.StatusOK {
		t.Fatalf("expected 200 on POST /api/v1/admin/plans, got %d: %s", wCreate.Code, wCreate.Body.String())
	}

	var createResp struct {
		Code int        `json:"code"`
		Data model.Plan `json:"data"`
	}
	if err := json.Unmarshal(wCreate.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("failed to unmarshal create plan response: %v", err)
	}
	createdPlan := createResp.Data
	if createdPlan.ID == 0 {
		t.Fatalf("expected created plan to have valid ID")
	}

	// 注册 Teardown 钩子确保数据完全清理
	t.Cleanup(func() {
		delReq, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/admin/plans/%d", createdPlan.ID), nil)
		delReq.Header.Set("Authorization", "Bearer "+adminToken)
		delW := httptest.NewRecorder()
		router.ServeHTTP(delW, delReq)
	})

	if len(createdPlan.AutoModels) != 3 {
		t.Fatalf("expected 3 autoModels in created plan, got %d", len(createdPlan.AutoModels))
	}

	// 3. 普通用户公开端点 GET /api/v1/plans 获取已上架套餐列表
	reqList, _ := http.NewRequest(http.MethodGet, "/api/v1/plans", nil)
	wList := httptest.NewRecorder()
	router.ServeHTTP(wList, reqList)
	if wList.Code != http.StatusOK {
		t.Fatalf("expected 200 on GET /api/v1/plans, got %d: %s", wList.Code, wList.Body.String())
	}

	var listResp struct {
		Code int          `json:"code"`
		Data []model.Plan `json:"data"`
	}
	if err := json.Unmarshal(wList.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("failed to unmarshal list plans response: %v", err)
	}

	var foundPlan *model.Plan
	for _, p := range listResp.Data {
		if p.ID == createdPlan.ID {
			foundPlan = &p
			break
		}
	}
	if foundPlan == nil {
		t.Fatalf("expected newly created plan in active plans list, but not found")
	}
	if len(foundPlan.AutoModels) != 3 || foundPlan.AutoModels[0] != "claude-3-7-sonnet" {
		t.Fatalf("expected autoModels to be returned in public API, got %v", foundPlan.AutoModels)
	}

	// 4. 管理员通过 PUT /api/v1/admin/plans/:id 更新 autoModels
	updatePlanPayload := map[string]interface{}{
		"name":          "Auto智能竞速旗舰版-已更新",
		"description":   "更新后的描述",
		"priceCents":    8800,
		"durationDays":  30,
		"allowedModels": []string{"auto"},
		"autoModels":    []string{"claude-3-7-sonnet", "gpt-4o"},
		"rateLimit":     60,
		"status":        "active",
	}
	updateBytes, _ := json.Marshal(updatePlanPayload)
	reqUpdate, _ := http.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/admin/plans/%d", createdPlan.ID), bytes.NewReader(updateBytes))
	reqUpdate.Header.Set("Content-Type", "application/json")
	reqUpdate.Header.Set("Authorization", "Bearer "+adminToken)
	wUpdate := httptest.NewRecorder()
	router.ServeHTTP(wUpdate, reqUpdate)

	if wUpdate.Code != http.StatusOK {
		t.Fatalf("expected 200 on PUT /api/v1/admin/plans/:id, got %d: %s", wUpdate.Code, wUpdate.Body.String())
	}

	var updateResp struct {
		Code int        `json:"code"`
		Data model.Plan `json:"data"`
	}
	if err := json.Unmarshal(wUpdate.Body.Bytes(), &updateResp); err != nil {
		t.Fatalf("failed to unmarshal update response: %v", err)
	}
	if len(updateResp.Data.AutoModels) != 2 || updateResp.Data.AutoModels[1] != "gpt-4o" {
		t.Fatalf("expected updated autoModels [claude-3-7-sonnet, gpt-4o], got %v", updateResp.Data.AutoModels)
	}
}

func TestAdminPlanTokenLimitAPICycle(t *testing.T) {
	teardown := setupAPITestEnvironment(t)
	defer teardown()

	router := SetupRouter()

	// 1. 生成管理员 JWT Token
	adminToken, err := jwt.GenerateToken(9998, "admin_token_limit_test", "admin", "test-api-jwt-secret-999", 24)
	if err != nil {
		t.Fatalf("failed to generate admin token: %v", err)
	}

	// 2. 管理员通过 POST /api/v1/admin/plans 创建包含 TokenLimit 的套餐
	createPlanPayload := map[string]interface{}{
		"name":          "TokenLimit API周期测试套餐",
		"description":   "端到端验证 tokenLimit 字段在 API 往返与公开回显",
		"priceCents":    6800,
		"durationDays":  30,
		"allowedModels": []string{"auto", "claude-3-7-sonnet"},
		"tokenLimit":    5000000,
		"rateLimit":     60,
		"status":        "active",
	}
	createBytes, _ := json.Marshal(createPlanPayload)
	reqCreate, _ := http.NewRequest(http.MethodPost, "/api/v1/admin/plans", bytes.NewReader(createBytes))
	reqCreate.Header.Set("Content-Type", "application/json")
	reqCreate.Header.Set("Authorization", "Bearer "+adminToken)
	wCreate := httptest.NewRecorder()
	router.ServeHTTP(wCreate, reqCreate)

	if wCreate.Code != http.StatusOK {
		t.Fatalf("expected 200 on POST /api/v1/admin/plans, got %d: %s", wCreate.Code, wCreate.Body.String())
	}

	var createResp struct {
		Code int        `json:"code"`
		Data model.Plan `json:"data"`
	}
	if err := json.Unmarshal(wCreate.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("failed to unmarshal create plan response: %v", err)
	}
	createdPlan := createResp.Data
	if createdPlan.ID == 0 {
		t.Fatalf("expected created plan to have valid ID")
	}

	// 注册 Teardown 钩子确保数据完全清理
	t.Cleanup(func() {
		delReq, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/admin/plans/%d", createdPlan.ID), nil)
		delReq.Header.Set("Authorization", "Bearer "+adminToken)
		delW := httptest.NewRecorder()
		router.ServeHTTP(delW, delReq)
	})

	if createdPlan.TokenLimit != 5000000 {
		t.Fatalf("expected TokenLimit 5000000 in created plan, got %d", createdPlan.TokenLimit)
	}

	// 3. 普通用户公开端点 GET /api/v1/plans 获取已上架套餐列表，校验 tokenLimit 回显
	reqList, _ := http.NewRequest(http.MethodGet, "/api/v1/plans", nil)
	wList := httptest.NewRecorder()
	router.ServeHTTP(wList, reqList)
	if wList.Code != http.StatusOK {
		t.Fatalf("expected 200 on GET /api/v1/plans, got %d: %s", wList.Code, wList.Body.String())
	}

	var listResp struct {
		Code int          `json:"code"`
		Data []model.Plan `json:"data"`
	}
	if err := json.Unmarshal(wList.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("failed to unmarshal list plans response: %v", err)
	}

	var foundPlan *model.Plan
	for _, p := range listResp.Data {
		if p.ID == createdPlan.ID {
			foundPlan = &p
			break
		}
	}
	if foundPlan == nil {
		t.Fatalf("expected newly created plan in active plans list, but not found")
	}
	if foundPlan.TokenLimit != 5000000 {
		t.Fatalf("expected tokenLimit 5000000 in public API, got %d", foundPlan.TokenLimit)
	}

	// 4. 管理员通过 PUT /api/v1/admin/plans/:id 更新 tokenLimit
	updatePlanPayload := map[string]interface{}{
		"name":          "TokenLimit API周期测试套餐-已更新",
		"description":   "更新为不限额度",
		"priceCents":    6800,
		"durationDays":  30,
		"allowedModels": []string{"auto"},
		"tokenLimit":    0,
		"rateLimit":     60,
		"status":        "active",
	}
	updateBytes, _ := json.Marshal(updatePlanPayload)
	reqUpdate, _ := http.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/admin/plans/%d", createdPlan.ID), bytes.NewReader(updateBytes))
	reqUpdate.Header.Set("Content-Type", "application/json")
	reqUpdate.Header.Set("Authorization", "Bearer "+adminToken)
	wUpdate := httptest.NewRecorder()
	router.ServeHTTP(wUpdate, reqUpdate)

	if wUpdate.Code != http.StatusOK {
		t.Fatalf("expected 200 on PUT /api/v1/admin/plans/:id, got %d: %s", wUpdate.Code, wUpdate.Body.String())
	}

	var updateResp struct {
		Code int        `json:"code"`
		Data model.Plan `json:"data"`
	}
	if err := json.Unmarshal(wUpdate.Body.Bytes(), &updateResp); err != nil {
		t.Fatalf("failed to unmarshal update response: %v", err)
	}
	if updateResp.Data.TokenLimit != 0 {
		t.Fatalf("expected updated tokenLimit 0, got %d", updateResp.Data.TokenLimit)
	}
}
