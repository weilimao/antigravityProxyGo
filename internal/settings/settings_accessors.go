package settings

import "strings"

// settings_accessors.go 集中所有 Config 字段级 Get/Set 访问器。
// 拆分自原 settings.go,用 settings_generic.go 的泛型 getSetting/setSetting/
// setSettingWithPost 收口样板,保证与原实现逐段等价的锁语义:
//   - getSetting:         RLock → 读 → RUnlock
//   - setSetting:         Lock → 改 → SaveConfig → Unlock(落盘持锁,原 simple setter 语义)
//   - setSettingWithPost: Lock → 改 → SaveConfig → Unlock → post(原代理类 Setter 语义)
// 含 clamp/decrypt/trim/dedup 等非纯字段逻辑的访问器,把特化逻辑放进 set/get 回调,
// 仍在同一锁临界区内执行,语义与原手写实现一致。
//
// 注意:SetEnableDebuggerMode/SetDebuggerLogPath 原实现是「Lock→改→Unlock→SaveConfig」
// (落盘前释放锁),与 setSetting 的「落盘持锁」语义不同,故保留手写,不放泛型。

// ============ 路径/目录(读非 config 字段,手写) ============

func (m *Manager) GetActiveDataDirectory() string {
	m.RLock()
	defer m.RUnlock()
	return m.activeDataDirectory
}

func (m *Manager) GetDefaultUserDataPath() string {
	m.RLock()
	defer m.RUnlock()
	return m.defaultUserDataPath
}

// ============ 基础开关(bool 纯字段,泛型收口) ============

func (m *Manager) GetEnableSystemLog() bool {
	return getSetting(m, func(c *Config) bool { return c.EnableSystemLog })
}

func (m *Manager) SetEnableSystemLog(enable bool) error {
	return setSetting(m, func(c *Config, v bool) { c.EnableSystemLog = v }, enable)
}

func (m *Manager) GetIsInterceptMode() bool {
	return getSetting(m, func(c *Config) bool { return c.IsInterceptMode })
}

func (m *Manager) SetIsInterceptMode(mode bool) error {
	return setSetting(m, func(c *Config, v bool) { c.IsInterceptMode = v }, mode)
}

func (m *Manager) GetAutoStart() bool {
	return getSetting(m, func(c *Config) bool { return c.AutoStart })
}

// SetAutoStart 落盘后还要同步 OS 自启项(setOSAutoStart 无需持锁;但原实现是持锁落盘后、
// 仍持锁调用 setOSAutoStart,最后统一 Unlock)。这里保留原序:Lock→改→SaveConfig→setOSAutoStart→Unlock。
func (m *Manager) SetAutoStart(enabled bool) error {
	m.Lock()
	defer m.Unlock()
	m.config.AutoStart = enabled
	if err := m.SaveConfig(); err != nil {
		return err
	}
	return setOSAutoStart(enabled)
}

func (m *Manager) GetSilentStart() bool {
	return getSetting(m, func(c *Config) bool { return c.SilentStart })
}

func (m *Manager) SetSilentStart(enabled bool) error {
	return setSetting(m, func(c *Config, v bool) { c.SilentStart = v }, enabled)
}

func (m *Manager) GetRelayEnabled() bool {
	return getSetting(m, func(c *Config) bool { return c.RelayEnabled })
}

func (m *Manager) SetRelayEnabled(enabled bool) error {
	return setSetting(m, func(c *Config, v bool) { c.RelayEnabled = v }, enabled)
}

// ============ 中继/远端连接(字符串纯字段,泛型收口) ============

func (m *Manager) GetRelayPort() string {
	return getSetting(m, func(c *Config) string { return c.RelayPort })
}

func (m *Manager) SetRelayPort(port string) error {
	return setSetting(m, func(c *Config, v string) { c.RelayPort = v }, port)
}

func (m *Manager) GetRemoteHost() string {
	return getSetting(m, func(c *Config) string { return c.RemoteHost })
}

func (m *Manager) SetRemoteHost(host string) error {
	return setSetting(m, func(c *Config, v string) { c.RemoteHost = v }, host)
}

func (m *Manager) GetRemotePath() string {
	return getSetting(m, func(c *Config) string { return c.RemotePath })
}

func (m *Manager) SetRemotePath(path string) error {
	return setSetting(m, func(c *Config, v string) { c.RemotePath = v }, path)
}

func (m *Manager) GetRemotePort() string {
	return getSetting(m, func(c *Config) string { return c.RemotePort })
}

func (m *Manager) SetRemotePort(port string) error {
	return setSetting(m, func(c *Config, v string) { c.RemotePort = v }, port)
}

func (m *Manager) GetRemoteKey() string {
	return getSetting(m, func(c *Config) string { return c.RemoteKey })
}

func (m *Manager) SetRemoteKey(key string) error {
	return setSetting(m, func(c *Config, v string) { c.RemoteKey = v }, key)
}

// GetRemotePassword 读出后解密;解密失败回退原文(兼容旧明文配置),与原实现一致。
func (m *Manager) GetRemotePassword() string {
	return getSetting(m, func(c *Config) string {
		decrypted, err := DecryptCredential(c.RemotePassword)
		if err != nil {
			return c.RemotePassword
		}
		return decrypted
	})
}

// SetRemotePassword 写入前加密;加密失败回退明文(与原实现一致),整段在写锁内完成。
func (m *Manager) SetRemotePassword(pwd string) error {
	return setSetting(m, func(c *Config, v string) {
		if v == "" {
			c.RemotePassword = ""
			return
		}
		encrypted, err := EncryptCredential(v)
		if err != nil {
			c.RemotePassword = v
		} else {
			c.RemotePassword = encrypted
		}
	}, pwd)
}

func (m *Manager) GetRemoteEnabled() bool {
	return getSetting(m, func(c *Config) bool { return c.RemoteEnabled })
}

func (m *Manager) SetRemoteEnabled(enabled bool) error {
	return setSetting(m, func(c *Config, v bool) { c.RemoteEnabled = v }, enabled)
}

// ============ 安全/过滤(bool 纯字段 + 切片,泛型收口) ============

func (m *Manager) GetRelaySSRFBlock() bool {
	return getSetting(m, func(c *Config) bool { return c.RelaySSRFBlock })
}

func (m *Manager) SetRelaySSRFBlock(val bool) error {
	return setSetting(m, func(c *Config, v bool) { c.RelaySSRFBlock = v }, val)
}

func (m *Manager) GetRelayPortBlock() bool {
	return getSetting(m, func(c *Config) bool { return c.RelayPortBlock })
}

func (m *Manager) SetRelayPortBlock(val bool) error {
	return setSetting(m, func(c *Config, v bool) { c.RelayPortBlock = v }, val)
}

func (m *Manager) GetRelayDomainFilter() bool {
	return getSetting(m, func(c *Config) bool { return c.RelayDomainFilter })
}

func (m *Manager) SetRelayDomainFilter(val bool) error {
	return setSetting(m, func(c *Config, v bool) { c.RelayDomainFilter = v }, val)
}

// GetRelayDomainWhitelist nil 兜底空切片,避免前端拿到 null。
func (m *Manager) GetRelayDomainWhitelist() []string {
	return getSetting(m, func(c *Config) []string {
		if c.RelayDomainWhitelist == nil {
			return []string{}
		}
		return c.RelayDomainWhitelist
	})
}

func (m *Manager) SetRelayDomainWhitelist(val []string) error {
	return setSetting(m, func(c *Config, v []string) { c.RelayDomainWhitelist = v }, val)
}

// ============ 模型映射(复杂逻辑,set 回调内计算 deleted) ============

// GetRelayModelMapping 空且无删除记录时返回默认映射,与原实现一致。
func (m *Manager) GetRelayModelMapping() []ModelMappingEntry {
	m.RLock()
	defer m.RUnlock()
	if len(m.config.RelayModelMapping) == 0 && len(m.config.DeletedModelMappings) == 0 {
		return GetDefaultModelMappings()
	}
	return m.config.RelayModelMapping
}

// SetRelayModelMapping 在写锁内计算被删除的默认映射,再落盘,逻辑与原实现一致。
func (m *Manager) SetRelayModelMapping(val []ModelMappingEntry) error {
	return setSetting(m, func(c *Config, v []ModelMappingEntry) {
		defaults := GetDefaultModelMappings()
		existingInVal := make(map[string]bool)
		for _, entry := range v {
			existingInVal[entry.ClientModel] = true
		}
		var deleted []string
		for _, def := range defaults {
			if !existingInVal[def.ClientModel] {
				deleted = append(deleted, def.ClientModel)
			}
		}
		c.DeletedModelMappings = deleted
		c.RelayModelMapping = v
	}, val)
}

// LookupModelMultimodalFlag 按入站 ClientModel 名在 RelayModelMapping 中查映射项的 Multimodal 声明位。
// 返回 (declared *bool, found bool)——用 *bool 表达三态,与 OCR 降级闸的"配置优先"语义对齐:
//   - found=false:未命中任何映射项(或模型名/表为空)。调用方走启发式兜底。
//   - found=true, declared=nil:命中映射项但用户未显式声明 Multimodal(默认项即此态)。
//     调用方走启发式兜底(保持旧行为,避免升级后突然把图直送给原本配好的非多模态上游)。
//   - found=true, declared=&true:用户显式声明多模态,跳过 OCR 降级,图块原样透传。
//   - found=true, declared=&false:用户显式声明非多模态,即使名字命中启发式白名单仍强制降级。
//
// 匹配顺序与 MapClientModelToGemini 一致:精确 → 大小写不敏感(经 GetRelayModelMapping 落盘的项原样,
// 不做归一化,故大小写不敏感是查名字时的兼容兜底)。空配置无删除记录时 GetRelayModelMapping
// 返回默认映射,故对默认 gemini-*/gpt-* 等也能命中(默认项未显式设 Multimodal → 同样走 nil 兜底)。
func LookupModelMultimodalFlag(mappings []ModelMappingEntry, clientModel string) (declared *bool, found bool) {
	name := strings.TrimSpace(clientModel)
	if name == "" || len(mappings) == 0 {
		return nil, false
	}
	// 精确匹配。
	for _, e := range mappings {
		if e.ClientModel == name {
			return e.Multimodal, true
		}
	}
	// 大小写不敏感。
	lower := strings.ToLower(name)
	for _, e := range mappings {
		if strings.ToLower(e.ClientModel) == lower {
			return e.Multimodal, true
		}
	}
	return nil, false
}

// ============ 抓包/重试(含 clamp,泛型 + clamp 回调) ============

func (m *Manager) GetEnablePacketCapture() bool {
	return getSetting(m, func(c *Config) bool { return c.EnablePacketCapture })
}

func (m *Manager) SetEnablePacketCapture(enable bool) error {
	return setSetting(m, func(c *Config, v bool) { c.EnablePacketCapture = v }, enable)
}

func (m *Manager) GetMaxRetries() int {
	return getSetting(m, func(c *Config) int {
		if c.MaxRetries <= 0 {
			return 20
		}
		return c.MaxRetries
	})
}

func (m *Manager) SetMaxRetries(retries int) error {
	return setSetting(m, func(c *Config, v int) {
		if v <= 0 {
			v = 20
		}
		c.MaxRetries = v
	}, retries)
}

func (m *Manager) GetMaxRetryDelay() int {
	return getSetting(m, func(c *Config) int {
		if c.MaxRetryDelay <= 0 {
			return 10
		}
		return c.MaxRetryDelay
	})
}

func (m *Manager) SetMaxRetryDelay(delay int) error {
	return setSetting(m, func(c *Config, v int) {
		if v <= 0 {
			v = 10
		}
		c.MaxRetryDelay = v
	}, delay)
}

// ============ 出站代理类(落盘后热下推 netutil,走 setSettingWithPost) ============
//
// 以下 8 个 Setter 原实现均为「Lock→改→SaveConfig→Unlock→(err==nil)updateNetutilConfig」,
// 与 setSettingWithPost 完全一致;post 在解锁后执行,避免 updateNetutilConfig 内部 RLock 自死锁。

func (m *Manager) GetFallbackProxyPorts() string {
	return getSetting(m, func(c *Config) string { return c.FallbackProxyPorts })
}

func (m *Manager) SetFallbackProxyPorts(val string) error {
	return setSettingWithPost(m, func(c *Config, v string) { c.FallbackProxyPorts = v }, val, m.updateNetutilConfig)
}

func (m *Manager) GetCustomSocks5Address() string {
	return getSetting(m, func(c *Config) string { return c.CustomSocks5Address })
}

func (m *Manager) SetCustomSocks5Address(val string) error {
	return setSettingWithPost(m, func(c *Config, v string) { c.CustomSocks5Address = v }, val, m.updateNetutilConfig)
}

func (m *Manager) GetCustomSocks5Enabled() bool {
	return getSetting(m, func(c *Config) bool { return c.CustomSocks5Enabled })
}

func (m *Manager) SetCustomSocks5Enabled(val bool) error {
	return setSettingWithPost(m, func(c *Config, v bool) { c.CustomSocks5Enabled = v }, val, m.updateNetutilConfig)
}

func (m *Manager) GetCustomSocks5Username() string {
	return getSetting(m, func(c *Config) string { return c.CustomSocks5Username })
}

func (m *Manager) SetCustomSocks5Username(val string) error {
	return setSettingWithPost(m, func(c *Config, v string) { c.CustomSocks5Username = v }, val, m.updateNetutilConfig)
}

func (m *Manager) GetCustomSocks5Password() string {
	return getSetting(m, func(c *Config) string { return c.CustomSocks5Password })
}

func (m *Manager) SetCustomSocks5Password(val string) error {
	return setSettingWithPost(m, func(c *Config, v string) { c.CustomSocks5Password = v }, val, m.updateNetutilConfig)
}

// FallbackProxy* 的 Getter/Setter:与 CustomSocks5* 同款(SaveConfig + updateNetutilConfig 热下推),
// 但语义独立——仅 NVIDIA 上游蓄流重试耗尽后的兜底出站代理,不参与全局 GetSystemProxy 三级链。
func (m *Manager) GetFallbackProxyAddress() string {
	return getSetting(m, func(c *Config) string { return c.FallbackProxyAddress })
}

func (m *Manager) SetFallbackProxyAddress(val string) error {
	return setSettingWithPost(m, func(c *Config, v string) { c.FallbackProxyAddress = v }, val, m.updateNetutilConfig)
}

func (m *Manager) GetFallbackProxyEnabled() bool {
	return getSetting(m, func(c *Config) bool { return c.FallbackProxyEnabled })
}

func (m *Manager) SetFallbackProxyEnabled(val bool) error {
	return setSettingWithPost(m, func(c *Config, v bool) { c.FallbackProxyEnabled = v }, val, m.updateNetutilConfig)
}

func (m *Manager) GetFallbackProxyUsername() string {
	return getSetting(m, func(c *Config) string { return c.FallbackProxyUsername })
}

func (m *Manager) SetFallbackProxyUsername(val string) error {
	return setSettingWithPost(m, func(c *Config, v string) { c.FallbackProxyUsername = v }, val, m.updateNetutilConfig)
}

func (m *Manager) GetFallbackProxyPassword() string {
	return getSetting(m, func(c *Config) string { return c.FallbackProxyPassword })
}

func (m *Manager) SetFallbackProxyPassword(val string) error {
	return setSettingWithPost(m, func(c *Config, v string) { c.FallbackProxyPassword = v }, val, m.updateNetutilConfig)
}

// ============ 语言/请求体/超时(含 clamp,泛型收口) ============

func (m *Manager) GetLanguage() string {
	return getSetting(m, func(c *Config) string {
		if c.Language == "" {
			return "zh"
		}
		return c.Language
	})
}

func (m *Manager) SetLanguage(lang string) error {
	return setSetting(m, func(c *Config, v string) { c.Language = v }, lang)
}

// GetMaxRequestBodyMB 返回请求体大小限制（MB），默认 50MB
func (m *Manager) GetMaxRequestBodyMB() int {
	return getSetting(m, func(c *Config) int {
		if c.MaxRequestBodyMB <= 0 {
			return 50
		}
		return c.MaxRequestBodyMB
	})
}

func (m *Manager) SetMaxRequestBodyMB(mb int) error {
	return setSetting(m, func(c *Config, v int) {
		if v <= 0 {
			v = 50
		}
		c.MaxRequestBodyMB = v
	}, mb)
}

func (m *Manager) GetRequestTimeout() int {
	return getSetting(m, func(c *Config) int {
		if c.RequestTimeout <= 0 {
			return 300
		}
		return c.RequestTimeout
	})
}

func (m *Manager) SetRequestTimeout(timeout int) error {
	return setSetting(m, func(c *Config, v int) {
		if v <= 0 {
			v = 300
		}
		c.RequestTimeout = v
	}, timeout)
}

// ============ Prompt 前缀 & 自定义模型/思考覆盖 ============

func (m *Manager) GetPromptPrefix() string {
	return getSetting(m, func(c *Config) string { return c.PromptPrefix })
}

func (m *Manager) SetPromptPrefix(val string) error {
	return setSetting(m, func(c *Config, v string) { c.PromptPrefix = v }, val)
}

func (m *Manager) GetCustomModelOverrideEnabled() bool {
	return getSetting(m, func(c *Config) bool { return c.CustomModelOverrideEnabled })
}

func (m *Manager) SetCustomModelOverrideEnabled(val bool) error {
	return setSetting(m, func(c *Config, v bool) { c.CustomModelOverrideEnabled = v }, val)
}

func (m *Manager) GetCustomModelOverrideID() string {
	return getSetting(m, func(c *Config) string { return c.CustomModelOverrideID })
}

func (m *Manager) SetCustomModelOverrideID(val string) error {
	return setSetting(m, func(c *Config, v string) { c.CustomModelOverrideID = v }, val)
}

// GetBypassOverridePrefixes 返回全局模型覆写的"按前缀绕过"名单。
// nil 兜底为空切片,与 GetNvidiaPreferredModels 一致,避免调用方判空歧义;
// 返回副本防止外部误改内存态。
func (m *Manager) GetBypassOverridePrefixes() []string {
	return getSetting(m, func(c *Config) []string {
		if c.BypassOverridePrefixes == nil {
			return []string{}
		}
		out := make([]string, len(c.BypassOverridePrefixes))
		copy(out, c.BypassOverridePrefixes)
		return out
	})
}

// SetBypassOverridePrefixes 去空白去重(大小写不敏感)后存入并持久化。
func (m *Manager) SetBypassOverridePrefixes(val []string) error {
	return setSetting(m, func(c *Config, v []string) {
		seen := make(map[string]bool)
		cleaned := make([]string, 0, len(v))
		for _, item := range v {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			key := strings.ToLower(item)
			if seen[key] {
				continue
			}
			seen[key] = true
			cleaned = append(cleaned, item)
		}
		c.BypassOverridePrefixes = cleaned
	}, val)
}

func (m *Manager) GetCustomThinkingOverrideEnabled() bool {
	return getSetting(m, func(c *Config) bool { return c.CustomThinkingOverrideEnabled })
}

func (m *Manager) SetCustomThinkingOverrideEnabled(val bool) error {
	return setSetting(m, func(c *Config, v bool) { c.CustomThinkingOverrideEnabled = v }, val)
}

func (m *Manager) GetCustomThinkingBudget() int {
	return getSetting(m, func(c *Config) int { return c.CustomThinkingBudget })
}

func (m *Manager) SetCustomThinkingBudget(val int) error {
	return setSetting(m, func(c *Config, v int) { c.CustomThinkingBudget = v }, val)
}

func (m *Manager) GetCustomThinkingSupports() bool {
	return getSetting(m, func(c *Config) bool { return c.CustomThinkingSupports })
}

func (m *Manager) SetCustomThinkingSupports(val bool) error {
	return setSetting(m, func(c *Config, v bool) { c.CustomThinkingSupports = v }, val)
}

func (m *Manager) GetCustomThinkingMinBudget() int {
	return getSetting(m, func(c *Config) int { return c.CustomThinkingMinBudget })
}

func (m *Manager) SetCustomThinkingMinBudget(val int) error {
	return setSetting(m, func(c *Config, v int) { c.CustomThinkingMinBudget = v }, val)
}

func (m *Manager) GetCustomMaxOutputTokens() int {
	return getSetting(m, func(c *Config) int { return c.CustomMaxOutputTokens })
}

func (m *Manager) SetCustomMaxOutputTokens(val int) error {
	return setSetting(m, func(c *Config, v int) { c.CustomMaxOutputTokens = v }, val)
}

func (m *Manager) GetReasoningAsText() bool {
	return getSetting(m, func(c *Config) bool { return c.ReasoningAsText })
}

func (m *Manager) SetReasoningAsText(val bool) error {
	return setSetting(m, func(c *Config, v bool) { c.ReasoningAsText = v }, val)
}

func (m *Manager) GetEnableThinkingMode() bool {
	return getSetting(m, func(c *Config) bool { return c.EnableThinkingMode })
}

func (m *Manager) SetEnableThinkingMode(val bool) error {
	return setSetting(m, func(c *Config, v bool) { c.EnableThinkingMode = v }, val)
}

// Debugger / OCR / SessionOptimization / NVIDIA 等含特化逻辑的访问器
// 已迁移至 settings_extras.go 与 settings_nvidia.go(需 filepath/strings 等额外 import)。
