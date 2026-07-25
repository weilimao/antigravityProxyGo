package relay

// nvidia_quota.go 提供 NVIDIA 号池的配额预扣额校验。
// 独立于 gemini/claude family，基于 UserQuotas.Nvidia 配置，复用 GetActiveWindow 窗口机制。

import (
	"fmt"
	"strings"

	"antigravity-proxy/internal/db"
)

type APIKeyFamily string

const (
	FamilyGemini APIKeyFamily = "gemini"
	FamilyClaude APIKeyFamily = "claude"
	FamilyNvidia APIKeyFamily = "nvidia"
)

func QuotaTypeHourly(family APIKeyFamily) string {
	return string(family) + "_hourly"
}

func QuotaTypeDaily(family APIKeyFamily) string {
	return string(family) + "_daily"
}

// DetectAPIKeyFamily 根据模型 ID 判定所属 API Key 家族。
func DetectAPIKeyFamily(model string) APIKeyFamily {
	m := strings.ToLower(strings.TrimSpace(model))
	if strings.HasPrefix(m, "claude") {
		return FamilyClaude
	}
	if strings.HasPrefix(m, "nvidia/") || strings.Contains(m, "nvidia/") {
		return FamilyNvidia
	}
	return FamilyGemini
}

// MatchModelFamily 校验 model 是否匹配给定的 APIKeyFamily。
func MatchModelFamily(model string, family APIKeyFamily) bool {
	return DetectAPIKeyFamily(model) == family
}

// NvidiaQuotaFamily 是 request_logs 中 NVIDIA 族 model_name 的统一前缀，
// recordNvidiaUsage 在落库时给 model_name 加上此前缀，使 LIKE "nvidia/" 能命中整族。
const NvidiaQuotaFamily = "nvidia/"

// nvidiaQuotaFamily 保留为内部别名（向后兼容旧引用），与 NvidiaQuotaFamily 同值。
const nvidiaQuotaFamily = NvidiaQuotaFamily

// nvidiaQuotaCheck 校验用户在 NVIDIA 配额窗口（小时级/天级）内是否已超额。
// 返回非 nil error 表示应拒绝(429)。窗口逻辑与现有 gemini/claude 一致。
func nvidiaQuotaCheck(userID string, q ModelQuota) error {
	if !q.EnableHourly && !q.EnableDaily {
		return nil // 未启用限额，放行
	}
	if q.EnableHourly && q.HourlyTokens > 0 && q.HourlyHours > 0 {
		used, _, err := GetActiveWindow(userID, nvidiaQuotaFamily, "nvidia_hourly", q.HourlyHours, false)
		if err == nil && used >= q.HourlyTokens {
			return fmt.Errorf("nvidia hourly token quota exhausted (limit=%d, used=%d)", q.HourlyTokens, used)
		}
	}
	if q.EnableDaily && q.DailyTokens > 0 && q.DailyDays > 0 {
		used, _, err := GetActiveWindow(userID, nvidiaQuotaFamily, "nvidia_daily", q.DailyDays*24, false)
		if err == nil && used >= q.DailyTokens {
			return fmt.Errorf("nvidia daily token quota exhausted (limit=%d, used=%d)", q.DailyTokens, used)
		}
	}
	return nil
}

// GetTokensForUserNvidiaSince 返回用户某时间点之后累计的 NVIDIA 族用量（供统计/API 查询）。
func GetTokensForUserNvidiaSince(userID, sinceIso string) (int64, error) {
	return db.GetTokensForUserModelFamilySince(userID, nvidiaQuotaFamily, sinceIso)
}
