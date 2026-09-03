package db

import (
	"path/filepath"
	"testing"
)

func TestBenchmarkRepoOperations(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_bench.db")
	if err := InitDB(dbPath); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer CloseDB()

	// 1. 插入两条记录
	r1 := &BenchmarkResult{
		Model:    "model-a",
		TTFTMs:   100,
		TotalMs:  500,
		Status:   "ok",
		TestedAt: "2026-09-04T00:00:00Z",
	}
	r2 := &BenchmarkResult{
		Model:    "model-b",
		TTFTMs:   200,
		TotalMs:  800,
		Status:   "ok",
		TestedAt: "2026-09-04T00:00:00Z",
	}
	if err := UpsertBenchmarkResult(r1); err != nil {
		t.Fatalf("UpsertBenchmarkResult r1 failed: %v", err)
	}
	if err := UpsertBenchmarkResult(r2); err != nil {
		t.Fatalf("UpsertBenchmarkResult r2 failed: %v", err)
	}

	list, err := ListBenchmarkResults()
	if err != nil {
		t.Fatalf("ListBenchmarkResults failed: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 results, got %d", len(list))
	}

	// 2. 剪枝测试：只保留 model-b
	if err := PruneBenchmarkResults([]string{"model-b"}); err != nil {
		t.Fatalf("PruneBenchmarkResults failed: %v", err)
	}
	list, err = ListBenchmarkResults()
	if err != nil {
		t.Fatalf("ListBenchmarkResults after prune failed: %v", err)
	}
	if len(list) != 1 || list[0].Model != "model-b" {
		t.Fatalf("expected only model-b, got %+v", list)
	}

	// 3. 清空测试
	if err := ClearBenchmarkResults(); err != nil {
		t.Fatalf("ClearBenchmarkResults failed: %v", err)
	}
	list, err = ListBenchmarkResults()
	if err != nil {
		t.Fatalf("ListBenchmarkResults after clear failed: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected 0 results, got %d", len(list))
	}
}
