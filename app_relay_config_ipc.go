package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/netutil"
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
		return marshalResponse(a.settingsMgr.GetRelayModelMapping())

	case "relay:set-model-mapping":
		var mapping []settings.ModelMappingEntry
		if len(args) > 0 {
			b, _ := json.Marshal(args[0])
			_ = json.Unmarshal(b, &mapping)
		}
		err := a.settingsMgr.SetRelayModelMapping(mapping)
		if err != nil {
			return marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
		}
		a.AddLog("🔄 中继大模型映射配置已保存")
		return marshalResponse(map[string]interface{}{"success": true})

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
		models, err := a.fetchChannelAvailableModels(channel)
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

func (a *App) fetchChannelAvailableModels(channel string) ([]string, error) {
	ch := strings.ToLower(strings.TrimSpace(channel))
	if ch == "" {
		ch = "google"
	}

	// 查找账号池中属于该 Channel/Provider 的账号 (使用 GetRawAccountsByProvider 获取未掩码的真实 Token)
	var activeAcc *account.Account
	if a.accountMgr != nil {
		rawAccounts := a.accountMgr.GetRawAccountsByProvider("all")
		for _, acc := range rawAccounts {
			if acc != nil && acc.Enabled {
				accProv := strings.ToLower(strings.TrimSpace(acc.Provider))
				if isGoogleChannel(ch) {
					// 只要账号属于 Google 族 (antigravity, project, google, gcp, gemini-cli 或空) 且 token 不为空
					if (accProv == "" || isGoogleChannel(accProv)) && accProv != "2fa" && acc.GetAccessToken() != "" {
						activeAcc = acc
						break
					}
				} else {
					if accProv == ch {
						activeAcc = acc
						break
					}
				}
			}
		}

		// 兜底：若寻找指定 Google 族标签未命中，回退查寻任意未冷却、具备 AccessToken 的 Antigravity/Google 账号
		if activeAcc == nil && isGoogleChannel(ch) {
			for _, acc := range rawAccounts {
				if acc != nil && acc.Enabled && acc.GetAccessToken() != "" && acc.Provider != "nvidia" && acc.Provider != "2fa" {
					activeAcc = acc
					break
				}
			}
		}
	}

	if activeAcc == nil {
		return nil, fmt.Errorf("号池 [%s] 下暂无已启用的有效账号，请先在【账号池】中添加该号池账号", channel)
	}

	// 1. 如果是 OpenAI 兼容第三方号池 (NVIDIA, DeepSeek, Qwen, Anthropic, Moonshot, Other 自定义组等)
	// Other 号池的 Anthropic 格式组也统一打上游 /v1/models;上游不支持时返回错误,前端手填兜底(无预置清单)。
	if ch == "nvidia" || ch == "deepseek" || ch == "qwen" || ch == "anthropic" || ch == "moonshot" || ch == "other" || ch == "grok" || activeAcc.Provider == "nvidia" || activeAcc.Provider == "grok" {
		baseURL := activeAcc.BaseURL
		apiKey := activeAcc.GetAccessToken()
		if baseURL == "" && ch == "nvidia" {
			baseURL = account.DefaultNvidiaBaseURL
		}
		if baseURL == "" {
			return nil, fmt.Errorf("账号 %s 未配置 BaseURL，无法打上游获取模型", activeAcc.Email)
		}

		models, err := fetchRemoteNvidiaModels(baseURL, apiKey)
		if err != nil {
			return nil, fmt.Errorf("打上游 [%s] 获取模型失败: %w", baseURL, err)
		}
		if len(models) == 0 {
			return nil, fmt.Errorf("上游 [%s] 返回的模型列表为空", baseURL)
		}
		return models, nil
	}

	// 2. 对于 Google / Antigravity / GCP 号池，真正发起 v1internal:fetchAvailableModels 请求
	models, err := fetchGeminiInternalModels(activeAcc)
	if err != nil {
		return nil, fmt.Errorf("打 Google 上游 v1internal:fetchAvailableModels 失败 (账号 %s): %w", activeAcc.Email, err)
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("Google 上游返回的模型列表为空")
	}
	return models, nil
}

func fetchGeminiInternalModels(acc *account.Account) ([]string, error) {
	token := acc.GetAccessToken()
	if token == "" {
		return nil, fmt.Errorf("账号 AccessToken 为空")
	}

	projectID := acc.ProjectID
	if projectID == "" {
		projectID = "favorable-synapse-ttvcb"
	}

	reqBody, _ := json.Marshal(map[string]string{
		"project": projectID,
	})

	targetURL := "https://daily-cloudcode-pa.googleapis.com/v1internal:fetchAvailableModels"
	req, err := http.NewRequestWithContext(context.Background(), "POST", targetURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "antigravity/ide/2.1.1 windows/amd64")

	// 复用项目 netutil 系统代理链(IE 注册表 / 自定义 SOCKS5 / 本地 VPN 端口探测三级回退)。
	// 原裸 http.Client{Timeout} 在 Transport=nil 时只读 HTTPS_PROXY 环境变量，不走 Windows
	// IE 系统代理与本地 VPN 端口探测，导致防火墙环境 console “Google 获取模型必报 context deadline”。
	// Timeout 由 15s 提到 30s：代理握手 + Google 内部接口延迟抖动，15s 偏紧。
	client := &http.Client{Timeout: 30 * time.Second, Transport: netutil.NewTransport()}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("网络连接失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}

	var parsed struct {
		Models map[string]interface{} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("解析上游响应 JSON 失败: %w", err)
	}

	var list []string
	for k := range parsed.Models {
		list = append(list, k)
	}
	sort.Strings(list)
	return list, nil
}
