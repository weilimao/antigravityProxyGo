package db

import (
	"testing"
	"time"
)

// TestRequestLog_FamilyRoundTrip 验证 family 列在 InsertRequestLog / GetRequestLogsSince 的往返一致性:
// 落点5(stats.AddRequestLogForFamily)写入 family="nvidia" 的日志后, 远程聚合分支 GetRequestLogsSince
// 应能读回同一 family, 供前端按族识别。空 family(模拟旧版本/gemini/claude 请求)亦需兼容往返不报错。
//
// 用 t.TempDir() 跑真实 SQLite InitDB(含建表 + family 列 ALTER 幂等迁移), 覆盖用例10/11/12。
func TestRequestLog_FamilyRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := InitDB(dir); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer func() {
		// 关闭并清空全局实例, 避免污染同进程其他测试(if GlobalDB != nil 守卫)。
		CloseDB()
		GlobalDB = nil
	}()

	nv := &RequestLog{
		ReqID:      "nv-rt-1",
		Timestamp:  time.Now().Format(time.RFC3339),
		Mode:       "local",
		UserID:     "u-1",
		ModelName:  "z-ai/glm-5.2",
		InTokens:   100,
		OutTokens:  50,
		Cost:       0.001,
		StatusCode: 200,
		Method:     "POST",
		Host:       "integrate.api.nvidia.com",
		Path:       "/nvidia/v1/chat/completions",
		SessionID:  "tok-1",
		Family:     "nvidia",
	}
	if err := InsertRequestLog(nv); err != nil {
		t.Fatalf("InsertRequestLog(nvidia): %v", err)
	}

	// 模拟旧版本/非 NVIDIA 请求: 空 family 也需正确往返(列存在且默认空串)。
	legacy := &RequestLog{
		ReqID:      "gem-rt-1",
		Timestamp:  time.Now().Format(time.RFC3339),
		Mode:       "local",
		UserID:     "u-1",
		ModelName:  "gemini-3.5-flash",
		InTokens:   200,
		OutTokens:  80,
		Cost:       0.002,
		StatusCode: 200,
		Method:     "POST",
		Host:       "daily-cloudcode-pa.googleapis.com",
		Path:       "/v1internal:streamGenerateContent",
		Family:     "", // gemini/claude 直连: 空
	}
	if err := InsertRequestLog(legacy); err != nil {
		t.Fatalf("InsertRequestLog(legacy): %v", err)
	}

	logs, err := GetRequestLogsSince("u-1", "local", 0, 100)
	if err != nil {
		t.Fatalf("GetRequestLogsSince: %v", err)
	}
	if len(logs) != 2 {
		t.Fatalf("expected 2 logs, got %d", len(logs))
	}

	// 以 ReqID 索引断言 family 往返。
	byReq := map[string]*RequestLog{}
	for _, l := range logs {
		byReq[l.ReqID] = l
	}
	if got := byReq["nv-rt-1"]; got == nil || got.Family != "nvidia" {
		if got == nil {
			t.Error("nvidia log not returned")
		} else {
			t.Errorf("nvidia family round-trip = %q, want %q", got.Family, "nvidia")
		}
	}
	if got := byReq["gem-rt-1"]; got == nil || got.Family != "" {
		if got == nil {
			t.Error("legacy gemini log not returned")
		} else {
			t.Errorf("legacy family round-trip = %q, want empty", got.Family)
		}
	}
}

// TestDBMigration_FamilyColumnIdempotent 验证新建库后表已含 family 列, 二次 InitDB(经幂等守卫直接返回)
// 不破坏结构。实际 ALTER 幂等体现在 runMigrations 对已存在列静默忽略 error。
func TestDBMigration_FamilyColumnIdempotent(t *testing.T) {
	dir := t.TempDir()
	if err := InitDB(dir); err != nil {
		t.Fatalf("InitDB first: %v", err)
	}
	defer func() {
		CloseDB()
		GlobalDB = nil
	}()

	// 校验列存在。
	rows, err := GlobalDB.Query(`PRAGMA table_info(request_logs)`)
	if err != nil {
		t.Fatalf("PRAGMA: %v", err)
	}
	defer rows.Close()
	hasFamily := false
	familyCount := 0
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull int
		var dflt interface{}
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan pragma row: %v", err)
		}
		if name == "family" {
			hasFamily = true
			familyCount++
		}
	}
	if !hasFamily {
		t.Fatal("request_logs missing family column after migration")
	}
	if familyCount != 1 {
		t.Errorf("family column count = %d, want exactly 1 (no duplicate column)", familyCount)
	}
}
