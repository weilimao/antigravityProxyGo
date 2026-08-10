package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"antigravity-proxy/internal/db"
	"antigravity-proxy/internal/relay"
)

// handleRelayUsersIPC 处理 relay 用户/包/统计 IPC 通道。
// 从 app_relay.go handleRelayIPC 抽离:relay:get-users/add-user/remove-user/toggle-user/
// update-user-quota/get-packages/save-package/delete-package/get-user-stats。自包含本地闭包,
// 未命中返回 ("", false, nil) 由 handleRelayIPC fall-through。matchUserPackage/checkQuotaEqual
// 自由函数随簇迁出(同 package main 跨文件解析)。逻辑逐行等价。
func (a *App) handleRelayUsersIPC(channel string, args []interface{}) (string, bool, error) {
	getStringArg := func(idx int) string {
		if idx < len(args) {
			if s, ok := args[idx].(string); ok {
				return s
			}
		}
		return ""
	}

	getBoolArg := func(idx int) bool {
		if idx < len(args) {
			if b, ok := args[idx].(bool); ok {
				return b
			}
		}
		return false
	}

	marshalResponse := func(val interface{}) (string, bool, error) {
		b, err := json.Marshal(val)
		if err != nil {
			return `{"success":false,"error":"JSON serialization error"}`, true, nil
		}
		return string(b), true, nil
	}

	switch channel {
	case "relay:get-users":
		a.ensureRelayInitialized()
		if a.relayUserMgr == nil {
			return marshalResponse(map[string]interface{}{
				"users": []interface{}{},
				"total": 0,
				"page":  1,
			})
		}

		var req struct {
			Page       int    `json:"page"`
			PageSize   int    `json:"pageSize"`
			Search     string `json:"search"`
			PackageTag string `json:"packageTag"`
		}
		if len(args) > 0 {
			b, _ := json.Marshal(args[0])
			_ = json.Unmarshal(b, &req)
		}

		if req.Page <= 0 {
			req.Page = 1
		}
		if req.PageSize <= 0 {
			req.PageSize = 10
		}

		allUsers := a.relayUserMgr.GetUsers()
		var pkgs []*relay.RelayPackageTemplate
		if a.relayPackageMgr != nil {
			pkgs = a.relayPackageMgr.GetPackages()
		}

		var filtered []*relay.RelayUser
		for _, u := range allUsers {
			// 1. Search by account name (case-insensitive)
			if req.Search != "" {
				if !strings.Contains(strings.ToLower(u.Key), strings.ToLower(req.Search)) {
					continue
				}
			}

			// 2. Filter by package type
			if req.PackageTag != "" && req.PackageTag != "all" {
				pkgName := matchUserPackage(u.Quotas, pkgs)
				if req.PackageTag == "custom" {
					if pkgName != "custom" {
						continue
					}
				} else if req.PackageTag == "unlimited" {
					if pkgName != "unlimited" {
						continue
					}
				} else {
					if pkgName != req.PackageTag {
						continue
					}
				}
			}

			filtered = append(filtered, u)
		}

		total := len(filtered)
		start := (req.Page - 1) * req.PageSize
		end := start + req.PageSize
		if start > total {
			start = total
		}
		if end > total {
			end = total
		}

		paginatedUsers := filtered[start:end]
		return marshalResponse(map[string]interface{}{
			"users": paginatedUsers,
			"total": total,
			"page":  req.Page,
		})

	case "relay:add-user":
		key := getStringArg(0)
		password := getStringArg(1)
		remark := getStringArg(2)

		if a.relayUserMgr == nil {
			return marshalResponse(map[string]interface{}{"success": false, "error": "relay not initialized"})
		}

		user, err := a.relayUserMgr.AddUser(key, password, remark)
		if err != nil {
			return marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
		}
		a.AddLog(fmt.Sprintf("🔑 Relay user added: %s", key))
		return marshalResponse(map[string]interface{}{"success": true, "user": user})

	case "relay:remove-user":
		userId := getStringArg(0)
		if a.relayUserMgr == nil {
			return marshalResponse(map[string]interface{}{"success": false, "error": "relay not initialized"})
		}

		if err := a.relayUserMgr.RemoveUser(userId); err != nil {
			return marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
		}
		a.AddLog(fmt.Sprintf("🗑️ Relay user removed: %s", userId))
		return marshalResponse(map[string]interface{}{"success": true})

	case "relay:toggle-user":
		userId := getStringArg(0)
		enabled := getBoolArg(1)
		if a.relayUserMgr == nil {
			return marshalResponse(map[string]interface{}{"success": false, "error": "relay not initialized"})
		}

		a.relayUserMgr.UpdateUserEnabled(userId, enabled)
		return marshalResponse(map[string]interface{}{"success": true})

	case "relay:update-user-quota":
		userId := getStringArg(0)
		if a.relayUserMgr == nil {
			return marshalResponse(map[string]interface{}{"success": false, "error": "relay not initialized"})
		}
		var quotas relay.UserQuotas
		if len(args) > 1 {
			b, _ := json.Marshal(args[1])
			_ = json.Unmarshal(b, &quotas)
		}
		resetLimit := getBoolArg(2)
		err := a.relayUserMgr.UpdateUserQuota(userId, quotas, resetLimit)
		if err != nil {
			return marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
		}
		return marshalResponse(map[string]interface{}{"success": true})

	case "relay:get-packages":
		a.ensureRelayInitialized()
		if a.relayPackageMgr == nil {
			return marshalResponse([]interface{}{})
		}
		return marshalResponse(a.relayPackageMgr.GetPackages())

	case "relay:save-package":
		if a.relayPackageMgr == nil {
			return marshalResponse(map[string]interface{}{"success": false, "error": "not initialized"})
		}
		var pkg relay.RelayPackageTemplate
		if len(args) > 0 {
			b, _ := json.Marshal(args[0])
			_ = json.Unmarshal(b, &pkg)
		}
		if pkg.ID == "" {
			_, err := a.relayPackageMgr.AddPackage(pkg.Name, pkg.Quotas)
			if err != nil {
				return marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
			}
		} else {
			err := a.relayPackageMgr.UpdatePackage(pkg.ID, pkg.Name, pkg.Quotas)
			if err != nil {
				return marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
			}
		}
		return marshalResponse(map[string]interface{}{"success": true})

	case "relay:delete-package":
		if a.relayPackageMgr == nil {
			return marshalResponse(map[string]interface{}{"success": false, "error": "not initialized"})
		}
		id := getStringArg(0)
		err := a.relayPackageMgr.DeletePackage(id)
		if err != nil {
			return marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
		}
		return marshalResponse(map[string]interface{}{"success": true})

	case "relay:get-user-stats":
		userId := getStringArg(0)
		a.ensureRelayInitialized()
		if a.relayStatsMgr == nil {
			return marshalResponse(nil)
		}
		stats := a.relayStatsMgr.GetUserStats(userId)
		var geminiLifetime, claudeLifetime, nvidiaLifetime, grokLifetime int64
		user := a.relayUserMgr.GetUserByID(userId)
		if user != nil {
			if user.Quotas.Gemini.ResetAt != "" {
				geminiLifetime, _ = db.GetTokensForUserModelFamilySince(userId, "gemini", user.Quotas.Gemini.ResetAt)
			} else if stats != nil {
				for mName, mStats := range stats.Models {
					if relay.MatchModelFamily(mName, relay.FamilyGemini) {
						geminiLifetime += int64(mStats.InputTokens + mStats.OutputTokens)
					}
				}
			}

			if user.Quotas.Claude.ResetAt != "" {
				claudeLifetime, _ = db.GetTokensForUserModelFamilySince(userId, "claude", user.Quotas.Claude.ResetAt)
			} else if stats != nil {
				for mName, mStats := range stats.Models {
					if relay.MatchModelFamily(mName, relay.FamilyClaude) {
						claudeLifetime += int64(mStats.InputTokens + mStats.OutputTokens)
					}
				}
			}

			if user.Quotas.Nvidia.ResetAt != "" {
				nvidiaLifetime, _ = db.GetTokensForUserModelFamilySince(userId, relay.NvidiaQuotaFamily, user.Quotas.Nvidia.ResetAt)
			} else if stats != nil {
				for mName, mStats := range stats.Models {
					if relay.MatchModelFamily(mName, relay.FamilyNvidia) {
						nvidiaLifetime += int64(mStats.InputTokens + mStats.OutputTokens)
					}
				}
			}

			if user.Quotas.Grok.ResetAt != "" {
				grokLifetime, _ = db.GetTokensForUserModelFamilySince(userId, relay.GrokQuotaFamily, user.Quotas.Grok.ResetAt)
			} else if stats != nil {
				for mName, mStats := range stats.Models {
					if relay.MatchModelFamily(mName, relay.FamilyGrok) {
						grokLifetime += int64(mStats.InputTokens + mStats.OutputTokens)
					}
				}
			}
		} else if stats != nil {
			for mName, mStats := range stats.Models {
				switch {
				case relay.MatchModelFamily(mName, relay.FamilyNvidia):
					nvidiaLifetime += int64(mStats.InputTokens + mStats.OutputTokens)
				case relay.MatchModelFamily(mName, relay.FamilyClaude):
					claudeLifetime += int64(mStats.InputTokens + mStats.OutputTokens)
				case relay.MatchModelFamily(mName, relay.FamilyGrok):
					grokLifetime += int64(mStats.InputTokens + mStats.OutputTokens)
				default:
					geminiLifetime += int64(mStats.InputTokens + mStats.OutputTokens)
				}
			}
		}

		var geminiHourlyUsed, geminiDailyUsed int64
		var claudeHourlyUsed, claudeDailyUsed int64
		var nvidiaHourlyUsed, nvidiaDailyUsed int64
		var grokHourlyUsed, grokDailyUsed int64
		var geminiHourlyResetAt, claudeHourlyResetAt, nvidiaHourlyResetAt, grokHourlyResetAt string
		var geminiDailyResetAt, claudeDailyResetAt, nvidiaDailyResetAt, grokDailyResetAt string
		if user != nil {
			if user.Quotas.Gemini.EnableHourly && user.Quotas.Gemini.HourlyHours > 0 {
				var resetStr string
				geminiHourlyUsed, resetStr, _ = relay.GetActiveWindow(userId, "gemini", "gemini_hourly", user.Quotas.Gemini.HourlyHours, false)
				if resetStr != "" {
					geminiHourlyResetAt = resetStr
				}
			}
			if user.Quotas.Gemini.EnableDaily && user.Quotas.Gemini.DailyDays > 0 {
				var resetStr string
				geminiDailyUsed, resetStr, _ = relay.GetActiveWindow(userId, "gemini", "gemini_daily", user.Quotas.Gemini.DailyDays*24, false)
				if resetStr != "" {
					geminiDailyResetAt = resetStr
				}
			}

			if user.Quotas.Claude.EnableHourly && user.Quotas.Claude.HourlyHours > 0 {
				var resetStr string
				claudeHourlyUsed, resetStr, _ = relay.GetActiveWindow(userId, "claude", "claude_hourly", user.Quotas.Claude.HourlyHours, false)
				if resetStr != "" {
					claudeHourlyResetAt = resetStr
				}
			}
			if user.Quotas.Claude.EnableDaily && user.Quotas.Claude.DailyDays > 0 {
				var resetStr string
				claudeDailyUsed, resetStr, _ = relay.GetActiveWindow(userId, "claude", "claude_daily", user.Quotas.Claude.DailyDays*24, false)
				if resetStr != "" {
					claudeDailyResetAt = resetStr
				}
			}

			if user.Quotas.Nvidia.EnableHourly && user.Quotas.Nvidia.HourlyHours > 0 {
				var resetStr string
				nvidiaHourlyUsed, resetStr, _ = relay.GetActiveWindow(userId, relay.NvidiaQuotaFamily, "nvidia_hourly", user.Quotas.Nvidia.HourlyHours, false)
				if resetStr != "" {
					nvidiaHourlyResetAt = resetStr
				}
			}
			if user.Quotas.Nvidia.EnableDaily && user.Quotas.Nvidia.DailyDays > 0 {
				var resetStr string
				nvidiaDailyUsed, resetStr, _ = relay.GetActiveWindow(userId, relay.NvidiaQuotaFamily, "nvidia_daily", user.Quotas.Nvidia.DailyDays*24, false)
				if resetStr != "" {
					nvidiaDailyResetAt = resetStr
				}
			}

			if user.Quotas.Grok.EnableHourly && user.Quotas.Grok.HourlyHours > 0 {
				var resetStr string
				grokHourlyUsed, resetStr, _ = relay.GetActiveWindow(userId, relay.GrokQuotaFamily, "grok_hourly", user.Quotas.Grok.HourlyHours, false)
				if resetStr != "" {
					grokHourlyResetAt = resetStr
				}
			}
			if user.Quotas.Grok.EnableDaily && user.Quotas.Grok.DailyDays > 0 {
				var resetStr string
				grokDailyUsed, resetStr, _ = relay.GetActiveWindow(userId, relay.GrokQuotaFamily, "grok_daily", user.Quotas.Grok.DailyDays*24, false)
				if resetStr != "" {
					grokDailyResetAt = resetStr
				}
			}
		}

		return marshalResponse(map[string]interface{}{
			"stats":               stats,
			"user":                user,
			"geminiLifetime":      geminiLifetime,
			"geminiHourlyUsed":    geminiHourlyUsed,
			"geminiDailyUsed":     geminiDailyUsed,
			"claudeLifetime":      claudeLifetime,
			"claudeHourlyUsed":    claudeHourlyUsed,
			"claudeDailyUsed":     claudeDailyUsed,
			"nvidiaLifetime":      nvidiaLifetime,
			"nvidiaHourlyUsed":    nvidiaHourlyUsed,
			"nvidiaDailyUsed":     nvidiaDailyUsed,
			"grokLifetime":        grokLifetime,
			"grokHourlyUsed":      grokHourlyUsed,
			"grokDailyUsed":       grokDailyUsed,
			"geminiHourlyResetAt": geminiHourlyResetAt,
			"claudeHourlyResetAt": claudeHourlyResetAt,
			"nvidiaHourlyResetAt": nvidiaHourlyResetAt,
			"grokHourlyResetAt":   grokHourlyResetAt,
			"geminiDailyResetAt":  geminiDailyResetAt,
			"claudeDailyResetAt":  claudeDailyResetAt,
			"nvidiaDailyResetAt":  nvidiaDailyResetAt,
			"grokDailyResetAt":    grokDailyResetAt,
		})
	}

	return "", false, nil
}

// matchUserPackage returns the package name if user quotas match a template,
// or "custom" if the quota has any enabled limit, or "unlimited" otherwise.
func matchUserPackage(q relay.UserQuotas, pkgs []*relay.RelayPackageTemplate) string {
	for _, pkg := range pkgs {
		if checkQuotaEqual(q.Gemini, pkg.Quotas.Gemini) &&
			checkQuotaEqual(q.Claude, pkg.Quotas.Claude) &&
			q.ValidDuration == pkg.Quotas.ValidDuration &&
			q.ValidUnit == pkg.Quotas.ValidUnit {
			return pkg.Name
		}
	}
	if q.Gemini.EnableFixed || q.Gemini.EnableHourly || q.Gemini.EnableDaily ||
		q.Claude.EnableFixed || q.Claude.EnableHourly || q.Claude.EnableDaily {
		return "custom"
	}
	return "unlimited"
}

// checkQuotaEqual performs a field-by-field comparison of two ModelQuota values.
func checkQuotaEqual(q1, q2 relay.ModelQuota) bool {
	return q1.EnableFixed == q2.EnableFixed &&
		q1.FixedTokens == q2.FixedTokens &&
		q1.EnableHourly == q2.EnableHourly &&
		q1.HourlyHours == q2.HourlyHours &&
		q1.HourlyTokens == q2.HourlyTokens &&
		q1.EnableDaily == q2.EnableDaily &&
		q1.DailyDays == q2.DailyDays &&
		q1.DailyTokens == q2.DailyTokens
}
