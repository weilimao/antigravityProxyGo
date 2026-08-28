package main

import (
	"fmt"
	"math"
	"time"

	"antigravity-proxy/internal/db"
	"antigravity-proxy/internal/relay"
	"antigravity-proxy/internal/stats"
)

// app_lifecycle_relayhooks.go: 从 startup 的 proxy.NewProxyHandler 构造中提取出的两个
// relay 用量/授权回调 method。原为内联闭包(合计约 180 行),是 startup 复杂度最大来源。
// 二者均为纯业务逻辑,仅依赖 a.* 字段与包级函数,不引用 startup 局部变量,提取为 method
// 后签名逐字对齐、行为零回归,同时显著缩短 startup 主体。

// relayRecordUsage 请求完成回调:将远程中继的用量明细同时写入 SQLite(db.RequestLog)、
// 内存统计(stats.RequestLog)与 relay 运行时统计(relay.RelaySample),并按 API Key 记账
// 各模型族的 Token 消耗。仅当 relay 组件已装配时生效。
func (a *App) relayRecordUsage(allocatedAccount, userID, apiKeyID, modelName string, inTokens, outTokens, cachedTokens int, method, host, path, sessionID string, durationMs, firstByteMs int64, statusCode int, reqID string) {
	if a.relayStatsMgr != nil {
		rate := a.statsTracker.GetPricingMgr().GetPricingForModel(modelName)
		nonCachedIn := inTokens - cachedTokens
		if nonCachedIn < 0 {
			nonCachedIn = 0
		}
		inputCost := math.Round((float64(nonCachedIn)*rate.Input/1000000.0)*1000000.0) / 1000000.0
		outputCost := math.Round((float64(outTokens)*rate.Output/1000000.0)*1000000.0) / 1000000.0
		cachedCost := math.Round((float64(cachedTokens)*rate.Cached/1000000.0)*1000000.0) / 1000000.0
		totalCost := inputCost + outputCost + cachedCost

		dbItem := &db.RequestLog{
			ReqID:        reqID,
			Timestamp:    time.Now().Format(time.RFC3339),
			Mode:         "remote_relay",
			UserID:       userID,
			ModelName:    modelName,
			InTokens:     inTokens,
			OutTokens:    outTokens,
			CachedTokens: cachedTokens,
			Cost:         totalCost,
			InputCost:    inputCost,
			OutputCost:   outputCost,
			CachedCost:   cachedCost,
			DurationMs:   durationMs,
			FirstByteMs:  firstByteMs,
			StatusCode:   statusCode,
			Method:       method,
			Host:         host,
			Path:         path,
			SessionID:    sessionID,
		}
		_ = db.InsertRequestLog(dbItem)

		a.statsTracker.AddRequestLogInMemoryOnly(&stats.RequestLog{
			ID:           reqID,
			Timestamp:    time.Now().Format("01/02 15:04:05"),
			Method:       method,
			Host:         host,
			Path:         path,
			Model:        modelName,
			Account:      allocatedAccount,
			InTokens:     inTokens,
			OutTokens:    outTokens,
			CachedTokens: cachedTokens,
			Cost:         totalCost,
			StatusCode:   statusCode,
			SessionID:    sessionID,
			DurationMs:   durationMs,
			FirstByteMs:  firstByteMs,
		})

		a.relayStatsMgr.RecordUsage(relay.RelaySample{
			ReqID:        reqID,
			UserID:       userID,
			UserKey:      apiKeyID,
			ModelName:    modelName,
			InTokens:     inTokens,
			OutTokens:    outTokens,
			CachedTokens: cachedTokens,
			Method:       method,
			Host:         host,
			Path:         path,
			SessionID:    sessionID,
			DurationMs:   durationMs,
			FirstByteMs:  firstByteMs,
			StatusCode:   statusCode,
		})
	}
	if a.relayUserMgr != nil && apiKeyID != "" {
		family := relay.DetectAPIKeyFamily(modelName)
		totalTokens := int64(inTokens + outTokens)
		a.relayUserMgr.RecordAPIKeyUsage(userID, apiKeyID, family == relay.FamilyClaude, totalTokens)
	}
}

// relayAuthorize 请求授权回调:按用户 API Key 与固定/小时/日配额三级校验,超限返回错误。
// 与 relayRecordUsage 同为原 startup 内联闭包提取,依赖 a.relayUserMgr / a.relayStatsMgr 与
// db/relay 包级函数,不依赖 startup 局部变量。
func (a *App) relayAuthorize(userID, apiKeyID, modelName string) error {
	if a.relayUserMgr == nil || a.relayStatsMgr == nil {
		return nil
	}
	user := a.relayUserMgr.GetUserByID(userID)
	if user == nil {
		return fmt.Errorf("user not found")
	}
	if user.Quotas.ExpireAt > 0 && time.Now().Unix() > user.Quotas.ExpireAt {
		return fmt.Errorf("account expired")
	}

	if apiKeyID != "" {
		for _, key := range user.APIKeys {
			if key.ID == apiKeyID {
				family := relay.DetectAPIKeyFamily(modelName)
				switch family {
				case relay.FamilyClaude:
					if key.LimitClaudeTokens > 0 && key.UsedClaudeTokens >= key.LimitClaudeTokens {
						return fmt.Errorf("API Key Claude token limit exceeded (%d / %d)", key.UsedClaudeTokens, key.LimitClaudeTokens)
					}
				case relay.FamilyNvidia:
					if key.LimitNvidiaTokens > 0 && key.UsedNvidiaTokens >= key.LimitNvidiaTokens {
						return fmt.Errorf("API Key NVIDIA token limit exceeded (%d / %d)", key.UsedNvidiaTokens, key.LimitNvidiaTokens)
					}
				case relay.FamilyGrok:
					if key.LimitGrokTokens > 0 && key.UsedGrokTokens >= key.LimitGrokTokens {
						return fmt.Errorf("API Key Grok token limit exceeded (%d / %d)", key.UsedGrokTokens, key.LimitGrokTokens)
					}
				default:
					if key.LimitGeminiTokens > 0 && key.UsedGeminiTokens >= key.LimitGeminiTokens {
						return fmt.Errorf("API Key Gemini token limit exceeded (%d / %d)", key.UsedGeminiTokens, key.LimitGeminiTokens)
					}
				}
				break
			}
		}
	}

	family := relay.DetectAPIKeyFamily(modelName)
	var quota relay.ModelQuota
	var familyKeyword string
	switch family {
	case relay.FamilyClaude:
		quota = user.Quotas.Claude
		familyKeyword = "claude"
	case relay.FamilyNvidia:
		quota = user.Quotas.Nvidia
		familyKeyword = relay.NvidiaQuotaFamily
	case relay.FamilyGrok:
		quota = user.Quotas.Grok
		familyKeyword = relay.GrokQuotaFamily
	default:
		quota = user.Quotas.Gemini
		familyKeyword = "gemini"
	}

	if !quota.EnableFixed && !quota.EnableHourly && !quota.EnableDaily {
		return fmt.Errorf("model series unauthorized")
	}

	stats := a.relayStatsMgr.GetUserStats(userID)

	if quota.EnableFixed {
		var usedTokens int64
		if quota.ResetAt != "" {
			var err error
			usedTokens, err = db.GetTokensForUserModelFamilySince(userID, familyKeyword, quota.ResetAt)
			if err != nil {
				return fmt.Errorf("failed to check fixed quota")
			}
		} else {
			if stats != nil {
				for mName, mStats := range stats.Models {
					if relay.MatchModelFamily(mName, family) {
						usedTokens += int64(mStats.InputTokens + mStats.OutputTokens)
					}
				}
			}
		}
		if usedTokens >= quota.FixedTokens {
			return fmt.Errorf("fixed token limit exceeded (%d / %d)", usedTokens, quota.FixedTokens)
		}
	}

	if quota.EnableHourly && quota.HourlyHours > 0 {
		usedTokens, _, err := relay.GetActiveWindow(userID, familyKeyword, relay.QuotaTypeHourly(family), quota.HourlyHours, true)
		if err != nil {
			return fmt.Errorf("failed to check hourly quota")
		}
		if usedTokens >= quota.HourlyTokens {
			return fmt.Errorf("hourly token limit exceeded (%d / %d)", usedTokens, quota.HourlyTokens)
		}
	}

	if quota.EnableDaily && quota.DailyDays > 0 {
		usedTokens, _, err := relay.GetActiveWindow(userID, familyKeyword, relay.QuotaTypeDaily(family), quota.DailyDays*24, true)
		if err != nil {
			return fmt.Errorf("failed to check daily quota")
		}
		if usedTokens >= quota.DailyTokens {
			return fmt.Errorf("daily token limit exceeded (%d / %d)", usedTokens, quota.DailyTokens)
		}
	}

	return nil
}
