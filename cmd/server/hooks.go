package main

import (
	"fmt"
	"math"
	"time"

	"antigravity-proxy/internal/db"
	"antigravity-proxy/internal/relay"
	"antigravity-proxy/internal/stats"
)

// relayRecordUsage 用量记录闭包回调：将中继请求用量同步写入 SQLite、内存统计与 relay 状态
func (s *ServerInstance) relayRecordUsage(allocatedAccount, userID, apiKeyID, modelName string, inTokens, outTokens, cachedTokens int, method, host, path, sessionID string, durationMs, firstByteMs int64, statusCode int, reqID string) {
	if s.RelayStatsMgr != nil {
		rate := s.StatsTracker.GetPricingMgr().GetPricingForModel(modelName)
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

		if s.StatsTracker != nil {
			s.StatsTracker.TrackRequestForModel(modelName, inTokens, outTokens, cachedTokens)
		}

		s.StatsTracker.AddRequestLogInMemoryOnly(&stats.RequestLog{
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

		s.RelayStatsMgr.RecordUsage(relay.RelaySample{
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
	if s.RelayUserMgr != nil && apiKeyID != "" {
		family := relay.DetectAPIKeyFamily(modelName)
		totalTokens := int64(inTokens + outTokens)
		s.RelayUserMgr.RecordAPIKeyUsage(userID, apiKeyID, family == relay.FamilyClaude, totalTokens)
	}
}

// relayAuthorize 授权鉴权与配额检查回调
func (s *ServerInstance) relayAuthorize(userID, apiKeyID, modelName string) error {
	if s.RelayUserMgr == nil || s.RelayStatsMgr == nil {
		return nil
	}
	user := s.RelayUserMgr.GetUserByID(userID)
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

	stats := s.RelayStatsMgr.GetUserStats(userID)

	if quota.EnableFixed {
		var usedTokens int64
		if quota.ResetAt != "" {
			var err error
			usedTokens, err = db.GetTokensForUserModelFamilySince(userID, familyKeyword, quota.ResetAt)
			if err != nil {
				return fmt.Errorf("failed to check fixed quota: %w", err)
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
			return fmt.Errorf("failed to check hourly quota: %w", err)
		}
		if usedTokens >= quota.HourlyTokens {
			return fmt.Errorf("hourly token limit exceeded (%d / %d)", usedTokens, quota.HourlyTokens)
		}
	}

	if quota.EnableDaily && quota.DailyDays > 0 {
		usedTokens, _, err := relay.GetActiveWindow(userID, familyKeyword, relay.QuotaTypeDaily(family), quota.DailyDays*24, true)
		if err != nil {
			return fmt.Errorf("failed to check daily quota: %w", err)
		}
		if usedTokens >= quota.DailyTokens {
			return fmt.Errorf("daily token limit exceeded (%d / %d)", usedTokens, quota.DailyTokens)
		}
	}

	return nil
}
