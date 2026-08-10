package relay

// grok_quota.go 提供 Grok(x.ai) 号池的配额预扣额校验。
// 独立于 gemini/claude family，基于 UserQuotas.Grok 配置，复用 GetActiveWindow 窗口机制。
// 与 nvidia_quota.go(nvidiaQuotaCheck / NvidiaQuotaFamily)完全对偶，仅替换族标识为 "grok/"。

import (
	"fmt"

	"antigravity-proxy/internal/db"
)

// GrokQuotaFamily 是 request_logs 中 Grok 族 model_name 的统一前缀，
// recordGrokUsage 在落点1(relay_stats.json)落库时给 model_name 加 "grok/" 前缀
// (见 grok_usage.go:100-103)，使 DB 的 family LIKE 查询("grok/") 能命中整族。
// 与 NvidiaQuotaFamily="nvidia/" 同构，作为 GetActiveWindow 的 familyKeyword 透传到
// db.GetTokensForUserModelFamilySince → WHERE model_name LIKE '%grok/%'。
const GrokQuotaFamily = "grok/"

// grokQuotaFamily 保留为内部别名（向后兼容引用对称性），与 GrokQuotaFamily 同值。
const grokQuotaFamily = GrokQuotaFamily

// grokQuotaCheck 校验用户在 Grok 配额窗口（小时级/天级）内是否已超额。
// 返回非 nil error 表示应拒绝(429)。窗口逻辑与 nvidiaQuotaCheck / gemini / claude 完全一致：
//   - 未启用任何限额(EnableHourly==false && EnableDaily==false) → 放行(nil);
//   - 小时窗: familyKeyword="grok/"，quotaType="grok_hourly"，窗口小时数取 q.HourlyHours;
//   - 天窗:   familyKeyword="grok/"，quotaType="grok_daily"，窗口小时数取 q.DailyDays*24;
//   - 任一窗口 used >= 限额即返回超额 error(带 limit/used 便于前端/日志定位)。
//
// 与 nvidiaQuotaCheck 的关键等价点: familyKeyword 用带 "/" 的 "grok/" 而非裸 "grok",
// 因为 recordGrokUsage 落点1 写入的 model_name 带 "grok/" 前缀(如 "grok/grok-4.3"),
// LIKE '%grok/%' 才能精确命中整族而不误伤裸含 "grok" 子串的其它模型名。
// "grok_hourly" / "grok_daily" 作为 quota_windows 表的 quotaType 主键，与 nvidia_hourly/
// nvidia_daily 物理隔离，各自独立滚动窗口，互不串扰。
func grokQuotaCheck(userID string, q ModelQuota) error {
	if !q.EnableHourly && !q.EnableDaily {
		return nil // 未启用限额，放行
	}
	if q.EnableHourly && q.HourlyTokens > 0 && q.HourlyHours > 0 {
		used, _, err := GetActiveWindow(userID, grokQuotaFamily, "grok_hourly", q.HourlyHours, false)
		if err == nil && used >= q.HourlyTokens {
			return fmt.Errorf("grok hourly token quota exhausted (limit=%d, used=%d)", q.HourlyTokens, used)
		}
	}
	if q.EnableDaily && q.DailyTokens > 0 && q.DailyDays > 0 {
		used, _, err := GetActiveWindow(userID, grokQuotaFamily, "grok_daily", q.DailyDays*24, false)
		if err == nil && used >= q.DailyTokens {
			return fmt.Errorf("grok daily token quota exhausted (limit=%d, used=%d)", q.DailyTokens, used)
		}
	}
	return nil
}

// GetTokensForUserGrokSince 返回用户某时间点之后累计的 Grok 族用量（供统计/API 查询）。
// 与 GetTokensForUserNvidiaSince 对偶，供前端「使用统计」页查询用户 grok 用量。
func GetTokensForUserGrokSince(userID, sinceIso string) (int64, error) {
	return db.GetTokensForUserModelFamilySince(userID, grokQuotaFamily, sinceIso)
}
