package db

import (
	"database/sql"
	"fmt"
)

// repo_benchmark.go: 模型测速(首帧/耗时)最新结果持久化。
//
// 表 benchmark_results 每模型一行(PRIMARY KEY=model), 存最新一轮测速结果 + 上一轮值(prev)。
// UpsertBenchmarkResult 先读旧 current(ttft/total) 作为 prev, 再覆盖写入新值,
// 使前端「趋势」箭头有上一轮基准可对比, 无需额外历史表。
//
// 与 repo_ocr.go 同款 INSERT ... ON CONFLICT DO UPDATE upsert, dbMutex 串行化读写。
// 由 internal/benchmark 调度器与 app_benchmark_ipc.go 调用。

// BenchmarkResult 是单模型一次测速的结果记录。
type BenchmarkResult struct {
	Model       string `json:"model"`
	TTFTMs      int64  `json:"ttftMs"`      // 首字响应延迟(毫秒); 0=未采集/失败
	TotalMs     int64  `json:"totalMs"`     // 端到端总耗时(毫秒)
	PrevTTFTMs  int64  `json:"prevTtftMs"`  // 上一轮首字延迟(供趋势对比)
	PrevTotalMs int64  `json:"prevTotalMs"` // 上一轮总耗时
	Status      string `json:"status"`      // ok | warning | error
	Error       string `json:"error"`       // 失败原因(空=成功)
	TestedAt    string `json:"testedAt"`     // 本轮测试时刻(RFC3339)
}

// UpsertBenchmarkResult 写入/更新单模型测速结果。旧 current(ttft/total)平移为 prev 后覆盖。
func UpsertBenchmarkResult(r *BenchmarkResult) error {
	dbMutex.Lock()
	defer dbMutex.Unlock()

	if GlobalDB == nil {
		return fmt.Errorf("database not initialized")
	}
	if r == nil || r.Model == "" {
		return nil
	}

	// 读旧 current 作为新 prev(无旧记录则 prev=0)。
	var oldTTFT, oldTotal int64
	_ = GlobalDB.QueryRow("SELECT ttft_ms, total_ms FROM benchmark_results WHERE model = ?", r.Model).Scan(&oldTTFT, &oldTotal)

	query := `INSERT INTO benchmark_results (model, ttft_ms, total_ms, prev_ttft_ms, prev_total_ms, status, error, tested_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(model) DO UPDATE SET
			ttft_ms = excluded.ttft_ms,
			total_ms = excluded.total_ms,
			prev_ttft_ms = excluded.prev_ttft_ms,
			prev_total_ms = excluded.prev_total_ms,
			status = excluded.status,
			error = excluded.error,
			tested_at = excluded.tested_at;`
	_, err := GlobalDB.Exec(query, r.Model, r.TTFTMs, r.TotalMs, oldTTFT, oldTotal, r.Status, r.Error, r.TestedAt)
	return err
}

// ListBenchmarkResults 返回全部模型的最新测速结果(按 model 升序)。
func ListBenchmarkResults() ([]BenchmarkResult, error) {
	dbMutex.Lock()
	defer dbMutex.Unlock()

	if GlobalDB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	rows, err := GlobalDB.Query("SELECT model, ttft_ms, total_ms, prev_ttft_ms, prev_total_ms, status, error, tested_at FROM benchmark_results ORDER BY model")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []BenchmarkResult{}
	for rows.Next() {
		var r BenchmarkResult
		var testedAt sql.NullString
		if err := rows.Scan(&r.Model, &r.TTFTMs, &r.TotalMs, &r.PrevTTFTMs, &r.PrevTotalMs, &r.Status, &r.Error, &testedAt); err != nil {
			return nil, err
		}
		r.TestedAt = testedAt.String
		out = append(out, r)
	}
	return out, nil
}

// ClearBenchmarkResults 清空全部测速结果(配置清空模型时调用, 使卡片回到空态)。
func ClearBenchmarkResults() error {
	dbMutex.Lock()
	defer dbMutex.Unlock()
	if GlobalDB == nil {
		return fmt.Errorf("database not initialized")
	}
	_, err := GlobalDB.Exec("DELETE FROM benchmark_results")
	return err
}
