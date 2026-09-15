package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"antigravity-proxy/internal/modelfetch"
	"antigravity-proxy/internal/proxy"
	"antigravity-proxy/internal/settings"
)

// handleRelayConfigIPC 处理 relay 服务配置 IPC 通道。
// 从 app_relay.go handleRelayIPC 抽离:relay:get-security-config/set-security-config/
// get-model-mapping/set-model-mapping/get-account-channels/fetch-channel-models/
// get-model-routes/set-model-routes/get-config/set-config。自包含本地 marshalResponse(3-tuple),
// 未命中返回 ("", false, nil) 由 handleRelayIPC fall-through。isGoogleChannel/
// fetchChannelAvailableModels(*App)/fetchGeminiInternalModels 随簇迁出。逻辑逐行等价。
func (a *App) handleRelayConfigIPC(channel string, args []interface{}) (string, bool, error) {
	marshalResponse := func(val interface{}) (string, bool, error) {
		b, err := json.Marshal(val)
		if err != nil {
			return `{"success":false,"error":"JSON serialization error"}`, true, nil
		}
		return string(b), true, nil
	}

	switch channel {
	// ========== Relay Server Management ==========

	case "relay:get-security-config":
		return marshalResponse(map[string]interface{}{
			"relaySSRFBlock":       a.settingsMgr.GetRelaySSRFBlock(),
			"relayPortBlock":       a.settingsMgr.GetRelayPortBlock(),
			"relayDomainFilter":    a.settingsMgr.GetRelayDomainFilter(),
			"relayDomainWhitelist": a.settingsMgr.GetRelayDomainWhitelist(),
		})

	case "relay:set-security-config":
		var config struct {
			SSRFBlock       bool     `json:"relaySSRFBlock"`
			PortBlock       bool     `json:"relayPortBlock"`
			DomainFilter    bool     `json:"relayDomainFilter"`
			DomainWhitelist []string `json:"relayDomainWhitelist"`
		}
		if len(args) > 0 {
			b, _ := json.Marshal(args[0])
			_ = json.Unmarshal(b, &config)
		}

		_ = a.settingsMgr.SetRelaySSRFBlock(config.SSRFBlock)
		_ = a.settingsMgr.SetRelayPortBlock(config.PortBlock)
		_ = a.settingsMgr.SetRelayDomainFilter(config.DomainFilter)
		_ = a.settingsMgr.SetRelayDomainWhitelist(config.DomainWhitelist)

		a.proxyEngine.UpdateSecurityRules(
			config.SSRFBlock,
			config.PortBlock,
			config.DomainFilter,
			config.DomainWhitelist,
		)

		a.AddLog("🛡️ 中继服务网络安全规则已保存并热加载")
		return marshalResponse(map[string]interface{}{"success": true})

	case "relay:get-model-mapping":
		// 若当前启用了远程连接且处于已连接状态（服务器模式），优先从远端服务器拉取模型映射与当前账号专属 Auto 配置
		if a.settingsMgr != nil && a.settingsMgr.GetRemoteEnabled() && a.remoteRelay != nil && a.remoteRelay.IsConnected() {
			remoteMappings, err := a.remoteRelay.FetchRemoteModelMappings()
			if err == nil {
				// 进一步拉取当前账号在远端的专属 Auto 配置并融合回显
				if userAuto, isUser, autoErr := a.remoteRelay.FetchRemoteUserAutoConfig(); autoErr == nil && userAuto != nil && isUser {
					hasAuto := false
					for i, m := range remoteMappings {
						if strings.EqualFold(strings.TrimSpace(m.ClientModel), "auto") {
							remoteMappings[i].Expose = userAuto.Enabled
							remoteMappings[i].CandidateModels = userAuto.CandidateModels
							useBench := userAuto.UseBenchmarkPool
							remoteMappings[i].UseBenchmarkPool = &useBench
							hasAuto = true
							break
						}
					}
					if !hasAuto {
						useBench := userAuto.UseBenchmarkPool
						remoteMappings = append([]settings.ModelMappingEntry{{
							ClientModel:      "auto",
							TargetModel:      "auto",
							Expose:           userAuto.Enabled,
							CandidateModels:  userAuto.CandidateModels,
							UseBenchmarkPool: &useBench,
						}}, remoteMappings...)
					}
				}
				_ = a.settingsMgr.SetRelayModelMapping(remoteMappings)
				return marshalResponse(remoteMappings)
			}
			a.AddLog(fmt.Sprintf("⚠️ 从远程服务器拉取模型映射失败 (%v)，回退读取沙箱配置", err))
		}

		if !a.hasAccountsInPool() {
			return marshalResponse([]settings.ModelMappingEntry{})
		}
		return marshalResponse(a.settingsMgr.GetRelayModelMapping())

	case "relay:set-model-mapping":
		var mapping []settings.ModelMappingEntry
		if len(args) > 0 {
			b, _ := json.Marshal(args[0])
			_ = json.Unmarshal(b, &mapping)
		}

		// 若当前启用了远程连接且处于已连接状态（服务器模式），将映射配置同步保存到远程服务器
		if a.settingsMgr != nil && a.settingsMgr.GetRemoteEnabled() && a.remoteRelay != nil && a.remoteRelay.IsConnected() {
			// 1. 同步保存当前登录账号在远端的专属 Auto 竞速配置
			for _, m := range mapping {
				if strings.EqualFold(strings.TrimSpace(m.ClientModel), "auto") {
					autoCfg := proxy.RemoteUserAutoConfig{
						Enabled:          m.Expose,
						CandidateModels:  m.CandidateModels,
						UseBenchmarkPool: m.IsUseBenchmarkPool(),
					}
					if autoErr := a.remoteRelay.SaveRemoteUserAutoConfig(autoCfg); autoErr != nil {
						a.AddLog(fmt.Sprintf("⚠️ 同步专属 Auto 配置至远程服务器失败: %v", autoErr))
					} else {
						a.AddLog(fmt.Sprintf("✅ 已将当前账号专属 Auto 竞速配置同步至远端服务器 (候选数: %d)", len(m.CandidateModels)))
					}
					break
				}
			}

			// 2. 尝试同步全局映射表(若具备管理员权限)
			remoteErr := a.remoteRelay.SaveRemoteModelMappings(mapping)
			if remoteErr != nil {
				// 若因非管理员而拒绝，记录提示但不阻断（因为个人专属 auto 配置已成功保存）
				a.AddLog(fmt.Sprintf("ℹ️ 远程全局模型映射未更新: %v (已保留个人专属 Auto 配置)", remoteErr))
			} else {
				a.AddLog(fmt.Sprintf("🌐 [服务器模式] 中继大模型映射配置已成功同步至远程服务器 (共 %d 项)", len(mapping)))
			}

			// 3. 在当前沙箱的 settingsMgr 中也落盘同步
			_ = a.settingsMgr.SetRelayModelMapping(mapping)
			return marshalResponse(map[string]interface{}{"success": true, "isRemote": true})
		}

		err := a.settingsMgr.SetRelayModelMapping(mapping)
		if err != nil {
			return marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
		}
		a.AddLog("🔄 中继大模型映射配置已保存 (本地模式)")
		return marshalResponse(map[string]interface{}{"success": true, "isRemote": false})

	case "relay:get-auto-config":
		isRemote := a.isServerMode() && a.remoteRelay != nil && a.remoteRelay.IsConnected()
		remoteUserKey := ""
		if isRemote {
			cfg := a.remoteRelay.GetConfig()
			remoteUserKey = cfg.UserKey
			if remoteAuto, isUser, err := a.remoteRelay.FetchRemoteUserAutoConfig(); err == nil && remoteAuto != nil {
				return marshalResponse(map[string]interface{}{
					"success":          true,
					"isRemote":         true,
					"isUser":           isUser,
					"userKey":          remoteUserKey,
					"enabled":          remoteAuto.Enabled,
					"candidateModels":  remoteAuto.CandidateModels,
					"useBenchmarkPool": remoteAuto.UseBenchmarkPool,
				})
			}
		}

		// 本地模式或远端拉取失败兜底：从本地/沙箱 settingsMgr 中读取 auto entry
		mappings := a.settingsMgr.GetRelayModelMapping()
		for _, m := range mappings {
			if strings.EqualFold(strings.TrimSpace(m.ClientModel), "auto") {
				return marshalResponse(map[string]interface{}{
					"success":          true,
					"isRemote":         isRemote,
					"isUser":           false,
					"userKey":          remoteUserKey,
					"enabled":          m.Expose,
					"candidateModels":  m.CandidateModels,
					"useBenchmarkPool": m.IsUseBenchmarkPool(),
				})
			}
		}

		return marshalResponse(map[string]interface{}{
			"success":          true,
			"isRemote":         isRemote,
			"isUser":           false,
			"userKey":          remoteUserKey,
			"enabled":          false,
			"candidateModels":  []string{},
			"useBenchmarkPool": false,
		})

	case "relay:save-auto-config":
		var req struct {
			Enabled          bool     `json:"enabled"`
			CandidateModels  []string `json:"candidateModels"`
			UseBenchmarkPool bool     `json:"useBenchmarkPool"`
		}
		if len(args) > 0 {
			b, _ := json.Marshal(args[0])
			_ = json.Unmarshal(b, &req)
		}

		isRemote := a.isServerMode() && a.remoteRelay != nil && a.remoteRelay.IsConnected()
		if isRemote {
			autoCfg := proxy.RemoteUserAutoConfig{
				Enabled:          req.Enabled,
				CandidateModels:  req.CandidateModels,
				UseBenchmarkPool: req.UseBenchmarkPool,
			}
			if err := a.remoteRelay.SaveRemoteUserAutoConfig(autoCfg); err != nil {
				return marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
			}
			a.AddLog(fmt.Sprintf("🌐 [服务器模式] 已同步当前账号专属 Auto 竞速配置至远端服务器 (候选数: %d)", len(req.CandidateModels)))
		}

		// 同步更新本地/沙箱 settingsMgr
		mappings := a.settingsMgr.GetRelayModelMapping()
		found := false
		useBench := req.UseBenchmarkPool
		for i, m := range mappings {
			if strings.EqualFold(strings.TrimSpace(m.ClientModel), "auto") {
				mappings[i].Expose = req.Enabled
				mappings[i].CandidateModels = req.CandidateModels
				mappings[i].UseBenchmarkPool = &useBench
				found = true
				break
			}
		}
		if !found {
			mappings = append([]settings.ModelMappingEntry{{
				ClientModel:      "auto",
				TargetModel:      "auto",
				Expose:           req.Enabled,
				CandidateModels:  req.CandidateModels,
				UseBenchmarkPool: &useBench,
			}}, mappings...)
		}
		_ = a.settingsMgr.SetRelayModelMapping(mappings)
		if !isRemote {
			a.AddLog("🔄 已保存 Auto 竞速模型配置 (本地模式)")
		}

		return marshalResponse(map[string]interface{}{
			"success":  true,
			"isRemote": isRemote,
		})

	case "relay:get-account-channels":
		if a.accountMgr != nil {
			return marshalResponse(a.accountMgr.GetAllChannels())
		}
		return marshalResponse([]string{"antigravity", "google", "gcp", "nvidia"})

	case "relay:fetch-channel-models":
		channel := ""
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				channel = strings.TrimSpace(s)
			}
		}
		models, err := modelfetch.FetchChannelAvailableModels(a.accountMgr, channel)
		if err != nil {
			return marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
		}
		// diff:本次远端全集 − 该 channel 上次快照 = 本轮新增。新增即整体覆盖该 channel 快照。
		// 快照缺失(首次/旧配置)时返回空切片,前端当无新增处理;本次全集则整体落盘为下次基准。
		oldSnapByCh := a.settingsMgr.GetRelayChannelModelsSnapshot()
		oldSnap := oldSnapByCh[strings.ToLower(channel)]
		added := diffRemoteAdded(models, oldSnap)
		if err := a.settingsMgr.SetRelayChannelModelsSnapshot(channel, models); err != nil {
			a.AddLog(fmt.Sprintf("⚠️ [中继模型映射] %s 快照落盘失败(不影响本次返回): %v", channel, err))
		}
		return marshalResponse(map[string]interface{}{
			"success":  true,
			"models":   models,
			"snapshot": oldSnap,
			"added":    added,
		})

	case "relay:get-model-routes":
		// 「按模型路由到号池」规则表(/route/* 入口按入站 model 分发到对应 Provider 号池)。
		return marshalResponse(a.settingsMgr.GetRelayModelRoutes())

	case "relay:set-model-routes":
		var routes []settings.ModelRouteRule
		if len(args) > 0 {
			b, _ := json.Marshal(args[0])
			_ = json.Unmarshal(b, &routes)
		}
		err := a.settingsMgr.SetRelayModelRoutes(routes)
		if err != nil {
			return marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
		}
		a.AddLog("🔀 模型路由规则表已保存并热加载")
		return marshalResponse(map[string]interface{}{"success": true})

	case "relay:get-config":
		return marshalResponse(map[string]interface{}{
			"enabled": a.settingsMgr.GetRelayEnabled(),
			"port":    a.settingsMgr.GetRelayPort(),
		})

	case "relay:set-config":
		var config struct {
			Enabled bool   `json:"enabled"`
			Port    string `json:"port"`
		}
		if len(args) > 0 {
			b, _ := json.Marshal(args[0])
			_ = json.Unmarshal(b, &config)
		}

		if config.Port == "" {
			config.Port = "18444"
		}
		_ = a.settingsMgr.SetRelayEnabled(config.Enabled)
		_ = a.settingsMgr.SetRelayPort(config.Port)

		if config.Enabled {
			if err := a.startRelayServer(config.Port); err != nil {
				a.AddLog(fmt.Sprintf("❌ Failed to start relay server: %v", err))
				return marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
			}
		} else {
			a.stopRelayServer()
		}
		return marshalResponse(map[string]interface{}{"success": true})
	}

	return "", false, nil
}

func isGoogleChannel(ch string) bool {
	c := strings.ToLower(strings.TrimSpace(ch))
	return c == "google" || c == "antigravity" || c == "gcp" || c == "project" || c == "gemini-cli" || c == ""
}

