package settings

// settings.go(精简后): 仅保留配置模型定义、默认模型映射与本包级常量。
// Manager 结构体 / 加载保存 / 迁移 / 访问器 / 泛型收口 / NVIDIA / 接口 断言 已按职责拆分到:
//   - settings_manager.go   : Manager 结构、Init、loadConfig、SaveConfig、updateNetutilConfig、MigrateData
//   - settings_accessors.go  : 字段级 Get/Set(走泛型 getSetting/setSetting/setSettingWithPost)
//   - settings_extras.go     : debugger/OCR/SessionOptimization 等特化访问器
//   - settings_nvidia.go     : NVIDIA 专属模型清单 + ManagerInterface 断言
//   - settings_generic.go    : 复用点1 泛型读写封装(getSetting/setSetting/setSettingWithPost)

const configFileName = "config.json"

var dataFiles = []string{
	"accounts.json",
	"stats.json",
	"usage.json",
	"pricing.json",
	"captured_packets.json",
}

var dataDirs = []string{
	"certs",
}

type ModelMappingEntry struct {
	ClientModel string `json:"clientModel"`
	TargetModel string `json:"targetModel"`
	Expose      bool   `json:"expose"`
	// InjectChatTemplateKwargs 是否向 NVIDIA 等上游注入 chat_template_kwargs (思考等级参数)。
	// 指针类型: nil 视作默认 true(开启); 显式配置为 false 时关闭注入。
	InjectChatTemplateKwargs *bool `json:"injectChatTemplateKwargs,omitempty"`
	// OwnedBy 是该模型在 /v1/models 列表里 owned_by 字段的归属(号池/Provider, 如 "google", "nvidia", "deepseek")。
	// 留空时由 relay.inferOwnedBy 按模型名前缀兜底推断。
	OwnedBy string `json:"ownedBy,omitempty"`
	// TargetProvider 是该模型映射自由绑定的目标路由账号池 Channel ID(如 "nvidia", "deepseek", "google", "gcp" 等)。
	// 若配置了 TargetProvider, /route/* 路由入口会直接分发至此号池。
	TargetProvider string `json:"targetProvider,omitempty"`
}

// ShouldInjectChatTemplateKwargs 返回该映射项是否允许注入 chat_template_kwargs。
// 缺省/未配置(nil)时默认返回 true。
func (m ModelMappingEntry) ShouldInjectChatTemplateKwargs() bool {
	if m.InjectChatTemplateKwargs == nil {
		return true
	}
	return *m.InjectChatTemplateKwargs
}

// ModelRouteRule 是「按模型路由到号池」的单条规则。
// 入站请求(model=ClientModel) 命中 Pattern 后,转发到 TargetProvider 号池,
// 并把请求体里的 model 字段改写为 TargetModel(空则原样透传)。
// Pattern 支持三种写法:精确匹配("deepseek-chat")、前缀通配("deepseek-*")、
// 正则("regexp:^ds-.*")。多条规则按 Priority 降序匹配,首个命中即止。
type ModelRouteRule struct {
	Pattern        string `json:"pattern"`
	TargetProvider string `json:"targetProvider"` // 号池 Provider,如 "nvidia"/"deepseek";对应 Account.Provider
	TargetModel    string `json:"targetModel,omitempty"`
	Priority       int    `json:"priority,omitempty"`
	Enabled        bool   `json:"enabled"`
}

type Config struct {
	DataDirectory   string `json:"dataDirectory"`
	EnableSystemLog bool   `json:"enableSystemLog"`
	IsInterceptMode bool   `json:"isInterceptMode"`
	AutoStart       bool   `json:"autoStart"`
	SilentStart     bool   `json:"silentStart"`
	MaxRetries      int    `json:"maxRetries"`
	MaxRetryDelay   int    `json:"maxRetryDelay"`
	RelayEnabled    bool   `json:"relayEnabled"`
	RelayPort       string `json:"relayPort"`
	RemoteHost      string `json:"remoteHost"`
	RemotePort      string `json:"remotePort"`
	RemotePath      string `json:"remotePath"`
	RemoteKey       string `json:"remoteKey"`
	RemotePassword       string   `json:"remotePassword"`
	RemoteEnabled        bool     `json:"remoteEnabled"`
	RelaySSRFBlock       bool     `json:"relaySSRFBlock"`
	RelayPortBlock       bool     `json:"relayPortBlock"`
	RelayDomainFilter    bool     `json:"relayDomainFilter"`
	RelayDomainWhitelist []string `json:"relayDomainWhitelist"`
	RelayModelMapping    []ModelMappingEntry `json:"relayModelMapping"`
	DeletedModelMappings []string            `json:"deletedModelMappings"`
	// RelayModelRoutes 是「按模型路由到号池」的规则表,供 /route/* 专属入口按入站 model
	// 分发到对应 Provider 号池。空表则 /route/* 退化为「交给 nvidia 号池兜底」(向后兼容)。
	RelayModelRoutes []ModelRouteRule `json:"relayModelRoutes,omitempty"`
	EnablePacketCapture  bool   `json:"enablePacketCapture"`
	FallbackProxyPorts   string `json:"fallbackProxyPorts"`
	CustomSocks5Address  string `json:"customSocks5Address"`
	CustomSocks5Enabled  bool   `json:"customSocks5Enabled"`
	CustomSocks5Username string `json:"customSocks5Username"`
	CustomSocks5Password string `json:"customSocks5Password"`
	// FallbackProxy* 是"NVIDIA 上游蓄流重试耗尽后的兜底出站代理",独立于上方 CustomSocks5(专属全局代理)。
	// 两者语义界限:CustomSocks5 开启后覆盖一切出站链(系统 IE 代理 + 本地端口探测全绕过);
	// FallbackProxy 仅在 NVIDIA 链路、直连 5s×5 重试全部耗尽后,切此代理再试 1 轮(单次请求级,不记忆状态)。
	// 字段为单 URL 区分协议:填 "socks5://host:port" 或 "http://host:port",http/socks5 二选一由 URL scheme 区分,
	// Username/Password 仅 socks5 或需要鉴权的 http 代理才用。
	FallbackProxyAddress  string `json:"fallbackProxyAddress"`
	FallbackProxyEnabled  bool   `json:"fallbackProxyEnabled"`
	FallbackProxyUsername string `json:"fallbackProxyUsername"`
	FallbackProxyPassword string `json:"fallbackProxyPassword"`
	Language             string `json:"language"`
	MaxRequestBodyMB     int    `json:"maxRequestBodyMB"`
	RequestTimeout       int    `json:"requestTimeout"`
	EnableCustomCompression bool   `json:"enableCustomCompression"`
	MaxTokensThreshold      int    `json:"maxTokensThreshold"`
	CompressionStrategy     string `json:"compressionStrategy"`
	SummaryModel            string `json:"summaryModel"`
	KeepRecentTurns         int    `json:"keepRecentTurns"`
	// OcrModel 是入站 image 自愈降级时调用的本地 Gemini OCR 模型名。
	// 默认 gemini-2.5-flash。前端下拉默认显示中继模型映射列表 + 兜底,可改任意 Gemini 系模型。
	// 影响:NVIDIA/Gemini 入站 image 降级链路(URL)与 descHeader 文案。
	// 空字符串走默认(见 GetOcrModel),不阻断主请求。
	OcrModel string `json:"ocrModel"`
	// NVIDIA 号池 ResourceExhausted 时的服务端就地压缩参数（公共 chatcompress 引擎）。
	NvidiaCompressEnabled        bool `json:"nvidiaCompressEnabled"`
	NvidiaCompressThresholdTokens int  `json:"nvidiaCompressThresholdTokens"`
	NvidiaCompressKeepToolResults int  `json:"nvidiaCompressKeepToolResults"`
	PromptPrefix            string `json:"promptPrefix"`
	CustomModelOverrideEnabled    bool   `json:"customModelOverrideEnabled"`
	CustomModelOverrideID         string `json:"customModelOverrideID"`
	// BypassOverridePrefixes 是全局模型覆写的"按前缀绕过"名单:客户端原始模型名
	// (去 "models/" 前缀、小写化后)若以其中任一前缀开头,则跳过 GlobalModelOverride,
	// 原样透传。默认 ["tab"] —— Tab 补全模型(tab_flash_lite_preview 等)本属代码补全通道,
	// 走推理上游会触发 400 INVALID_ARGUMENT,故默认放行。
	// 与思考链覆写的 isTabModel(handler_attempt_routing.go:171) 同源思路,但更通用可配。
	BypassOverridePrefixes []string `json:"bypassOverridePrefixes"`
	CustomThinkingOverrideEnabled bool   `json:"customThinkingOverrideEnabled"`
	CustomThinkingSupports        bool   `json:"customThinkingSupports"`
	CustomThinkingBudget          int    `json:"customThinkingBudget"`
	CustomThinkingMinBudget       int    `json:"customThinkingMinBudget"`
	CustomMaxOutputTokens         int    `json:"customMaxOutputTokens"`
	ReasoningAsText               bool   `json:"reasoningAsText"`
	EnableThinkingMode            bool   `json:"enableThinkingMode"`
	EnableDebuggerMode            bool   `json:"enableDebuggerMode"`
	DebuggerLogPath               string `json:"debuggerLogPath"`
	// NvidiaPreferredModels 是全局级"NVIDIA 专属模型清单",所有 NVIDIA 账号共用。
	// 配置后,前端"获取模型"直接返回该清单(不请求远端);为空时才请求远端 /v1/models。
	NvidiaPreferredModels []string `json:"nvidiaPreferredModels"`
}

// DefaultOcrModel 是入站 image 自愈降级时调用的本地 Gemini OCR 模型默认值。
// 与 relay.defaultOcrModel 同值,供 GetOcrModel 空值兜底与 EnsureConfigExists 默认注入。
const DefaultOcrModel = "gemini-2.5-flash"

func GetDefaultModelMappings() []ModelMappingEntry {
	return []ModelMappingEntry{
		{ClientModel: "gemini-3-flash-agent", TargetModel: "gemini-3-flash-agent", Expose: true},
		{ClientModel: "gemini-2.5-flash-thinking", TargetModel: "gemini-2.5-flash-thinking", Expose: true},
		{ClientModel: "gemini-2.5-pro", TargetModel: "gemini-2.5-pro", Expose: true},
		{ClientModel: "gemini-2.0-flash-thinking-exp-01-21", TargetModel: "gemini-2.0-flash-thinking-exp-01-21", Expose: true},
		{ClientModel: "gemini-2.0-flash-lite-preview-02-05", TargetModel: "gemini-2.0-flash-lite-preview-02-05", Expose: true},
		{ClientModel: "gemini-2.0-pro-exp-02-05", TargetModel: "gemini-2.0-pro-exp-02-05", Expose: true},
		{ClientModel: "gemini-2.0-flash-thinking-exp", TargetModel: "gemini-2.0-flash-thinking-exp", Expose: true},
		{ClientModel: "gemini-2.0-flash-exp", TargetModel: "gemini-2.0-flash-exp", Expose: true},
		{ClientModel: "gemini-1.5-pro-latest", TargetModel: "gemini-1.5-pro", Expose: true},
		{ClientModel: "gemini-1.5-flash-latest", TargetModel: "gemini-1.5-flash", Expose: true},
		{ClientModel: "gemini-1.5-pro-exp-0827", TargetModel: "gemini-1.5-pro-exp-0827", Expose: true},

		{ClientModel: "gemini-2.0-flash-thinking-exp-1219", TargetModel: "gemini-2.0-flash-thinking-exp-1219", Expose: true},
		{ClientModel: "gemini-exp-1206", TargetModel: "gemini-exp-1206", Expose: true},
		{ClientModel: "gemini-exp-1121", TargetModel: "gemini-exp-1121", Expose: true},
		{ClientModel: "gemini-exp-1114", TargetModel: "gemini-exp-1114", Expose: true},
		{ClientModel: "gemini-1.5-pro-exp-0801", TargetModel: "gemini-1.5-pro-exp-0801", Expose: true},
		{ClientModel: "gemini-1.5-pro-002", TargetModel: "gemini-1.5-pro-002", Expose: true},
		{ClientModel: "gemini-1.5-pro-001", TargetModel: "gemini-1.5-pro-001", Expose: true},
		{ClientModel: "gemini-1.5-flash-002", TargetModel: "gemini-1.5-flash-002", Expose: true},
		{ClientModel: "gemini-1.5-flash-001", TargetModel: "gemini-1.5-flash-001", Expose: true},
		{ClientModel: "gemini-1.5-flash-8b", TargetModel: "gemini-1.5-flash-8b", Expose: true},
		{ClientModel: "text-embedding-004", TargetModel: "text-embedding-004", Expose: true},
		{ClientModel: "text-embedding-003", TargetModel: "text-embedding-003", Expose: true},

		{ClientModel: "gemini-1.5-flash-exp-0827", TargetModel: "gemini-1.5-flash-exp-0827", Expose: true},
		{ClientModel: "gemini-1.5-flash-8b-exp-0827", TargetModel: "gemini-1.5-flash-8b-exp-0827", Expose: true},
		{ClientModel: "learnlm-1.5-pro-experimental", TargetModel: "learnlm-1.5-pro-experimental", Expose: true},
		{ClientModel: "gemini-1.0-pro", TargetModel: "gemini-1.0-pro", Expose: true},
		{ClientModel: "aqa", TargetModel: "aqa", Expose: true},
		{ClientModel: "gemini-3.5-flash-low", TargetModel: "gemini-3.5-flash-low", Expose: true},
		{ClientModel: "gemini-pro-agent", TargetModel: "gemini-pro-agent", Expose: true},
		{ClientModel: "claude-sonnet-4-6", TargetModel: "claude-sonnet-4-6", Expose: true},
		{ClientModel: "claude-opus-4-6-thinking", TargetModel: "claude-opus-4-6-thinking", Expose: true},
		{ClientModel: "gemini-3-flash", TargetModel: "gemini-3-flash", Expose: true},
		{ClientModel: "tab_flash_lite_preview", TargetModel: "tab_flash_lite_preview", Expose: true},
		{ClientModel: "gemini-3.5-flash-extra-low", TargetModel: "gemini-3.5-flash-extra-low", Expose: true},
		{ClientModel: "tab_jump_flash_lite_preview", TargetModel: "tab_jump_flash_lite_preview", Expose: true},
		{ClientModel: "gemini-3.1-flash-lite", TargetModel: "gemini-3.1-flash-lite", Expose: true},
		{ClientModel: "gemini-3.1-pro-low", TargetModel: "gemini-3.1-pro-low", Expose: true},
		{ClientModel: "gemini-2.5-flash", TargetModel: "gemini-2.5-flash", Expose: true},
		{ClientModel: "gemini-2.5-flash-lite", TargetModel: "gemini-2.5-flash-lite", Expose: true},
		{ClientModel: "gemini-3.5-flash", TargetModel: "gemini-3.5-flash", Expose: true},
		{ClientModel: "gemini-3.1-pro-preview", TargetModel: "gemini-3.1-pro-preview", Expose: true},
		{ClientModel: "gemini-3-flash-preview", TargetModel: "gemini-3-flash-preview", Expose: true},
		{ClientModel: "gpt-cos-120b-medium", TargetModel: "gpt-cos-120b-medium", Expose: true},
		{ClientModel: "gemini-1.5-pro", TargetModel: "gemini-1.5-pro", Expose: true},
		{ClientModel: "gemini-1.5-flash", TargetModel: "gemini-1.5-flash", Expose: true},
		{ClientModel: "gemini-2.0-flash", TargetModel: "gemini-2.0-flash", Expose: true},
		{ClientModel: "gemini-2.0-pro-exp-02-05", TargetModel: "gemini-2.0-pro-exp-02-05", Expose: true},

		{ClientModel: "claude-3-5-sonnet", TargetModel: "gemini-1.5-pro", Expose: false},
		{ClientModel: "claude-3-opus", TargetModel: "gemini-1.5-pro", Expose: false},
		{ClientModel: "claude-3-haiku", TargetModel: "gemini-1.5-flash", Expose: false},
		{ClientModel: "claude-3-5-haiku", TargetModel: "gemini-1.5-flash", Expose: false},
		{ClientModel: "gpt-4o", TargetModel: "gemini-1.5-pro", Expose: false},
		{ClientModel: "gpt-4-turbo", TargetModel: "gemini-1.5-pro", Expose: false},
		{ClientModel: "gpt-4", TargetModel: "gemini-1.5-pro", Expose: false},
		{ClientModel: "gpt-3.5", TargetModel: "gemini-1.5-flash", Expose: false},
		{ClientModel: "o1-mini", TargetModel: "gemini-1.5-flash", Expose: false},
		{ClientModel: "o1-pro", TargetModel: "gemini-2.0-flash", Expose: false},
		{ClientModel: "o1-preview", TargetModel: "gemini-2.0-flash", Expose: false},
	}
}

type SessionOptimizationConfig struct {
	EnableCustomCompression bool   `json:"enableCustomCompression"`
	MaxTokensThreshold      int    `json:"maxTokensThreshold"`
	CompressionStrategy     string `json:"compressionStrategy"`
	SummaryModel            string `json:"summaryModel"`
	KeepRecentTurns         int    `json:"keepRecentTurns"`
	// NVIDIA 号池服务端就地压缩参数（公共 chatcompress 引擎）。
	NvidiaCompressEnabled        bool `json:"nvidiaCompressEnabled"`
	NvidiaCompressThresholdTokens int  `json:"nvidiaCompressThresholdTokens"`
	NvidiaCompressKeepToolResults int  `json:"nvidiaCompressKeepToolResults"`
}

// ChatCompressDefaults 集中暴露 chatcompress 引擎的默认值,供 relay 包 settings 缺字段时兜底。
const (
	ChatCompressDefaultEnabled    = true
	ChatCompressDefaultThreshold  = 80000
	ChatCompressDefaultKeepN      = 4
)

type ManagerInterface interface {
	Init(defaultPath string)
	GetSessionOptimization() SessionOptimizationConfig
	SetSessionOptimization(cfg SessionOptimizationConfig) error
	GetActiveDataDirectory() string
	GetDefaultUserDataPath() string
	GetEnableSystemLog() bool
	SetEnableSystemLog(enable bool) error
	GetIsInterceptMode() bool
	SetIsInterceptMode(mode bool) error
	GetAutoStart() bool
	SetAutoStart(enabled bool) error
	GetSilentStart() bool
	SetSilentStart(enabled bool) error
	GetMaxRetries() int
	SetMaxRetries(retries int) error
	GetMaxRetryDelay() int
	SetMaxRetryDelay(delay int) error
	GetRelayEnabled() bool
	SetRelayEnabled(enabled bool) error
	GetRelayPort() string
	SetRelayPort(port string) error
	GetRemoteHost() string
	SetRemoteHost(host string) error
	GetRemotePath() string
	SetRemotePath(path string) error
	GetRemotePort() string
	SetRemotePort(port string) error
	GetRemoteKey() string
	SetRemoteKey(key string) error
	GetRemotePassword() string
	SetRemotePassword(pwd string) error
	GetRemoteEnabled() bool
	SetRemoteEnabled(enabled bool) error
	GetRelaySSRFBlock() bool
	SetRelaySSRFBlock(val bool) error
	GetRelayPortBlock() bool
	SetRelayPortBlock(val bool) error
	GetRelayDomainFilter() bool
	SetRelayDomainFilter(val bool) error
	GetRelayDomainWhitelist() []string
	SetRelayDomainWhitelist(val []string) error
	GetRelayModelMapping() []ModelMappingEntry
	SetRelayModelMapping(val []ModelMappingEntry) error
	GetEnablePacketCapture() bool
	SetEnablePacketCapture(enable bool) error
	GetFallbackProxyPorts() string
	SetFallbackProxyPorts(val string) error
	GetCustomSocks5Address() string
	SetCustomSocks5Address(val string) error
	GetCustomSocks5Enabled() bool
	SetCustomSocks5Enabled(val bool) error
	GetCustomSocks5Username() string
	SetCustomSocks5Username(val string) error
	GetCustomSocks5Password() string
	SetCustomSocks5Password(val string) error
	// FallbackProxy: NVIDIA 上游蓄流重试耗尽后的兜底出站代理(独立于 CustomSocks5)。
	GetFallbackProxyAddress() string
	SetFallbackProxyAddress(val string) error
	GetFallbackProxyEnabled() bool
	SetFallbackProxyEnabled(val bool) error
	GetFallbackProxyUsername() string
	SetFallbackProxyUsername(val string) error
	GetFallbackProxyPassword() string
	SetFallbackProxyPassword(val string) error
	GetLanguage() string
	SetLanguage(lang string) error
	GetRequestTimeout() int
	SetRequestTimeout(timeout int) error
	GetPromptPrefix() string
	SetPromptPrefix(val string) error
	GetCustomModelOverrideEnabled() bool
	SetCustomModelOverrideEnabled(val bool) error
	GetCustomModelOverrideID() string
	SetCustomModelOverrideID(val string) error
	GetBypassOverridePrefixes() []string
	SetBypassOverridePrefixes(val []string) error
	GetCustomThinkingOverrideEnabled() bool
	SetCustomThinkingOverrideEnabled(val bool) error
	GetCustomThinkingSupports() bool
	SetCustomThinkingSupports(val bool) error
	GetCustomThinkingBudget() int
	SetCustomThinkingBudget(val int) error
	GetCustomThinkingMinBudget() int
	SetCustomThinkingMinBudget(val int) error
	GetCustomMaxOutputTokens() int
	SetCustomMaxOutputTokens(val int) error
	GetReasoningAsText() bool
	SetReasoningAsText(val bool) error
	GetEnableThinkingMode() bool
	SetEnableThinkingMode(val bool) error
	// GetOcrModel/SetOcrModel: 入站 image 自愈降级使用的本地 Gemini OCR 模型,前端可配置。
	GetOcrModel() string
	SetOcrModel(val string) error
	GetEnableDebuggerMode() bool
	SetEnableDebuggerMode(enable bool) error
	GetDebuggerLogPath() string
	SetDebuggerLogPath(val string) error
	GetResolvedDebuggerLogPath() string
	GetNvidiaPreferredModels() []string
	SetNvidiaPreferredModels(val []string) error
	// GetRelayModelRoutes/SetRelayModelRoutes:「按模型路由到号池」规则表。
	// /route/* 专属入口按入站 model 命中规则,分发到 TargetProvider 号池。
	GetRelayModelRoutes() []ModelRouteRule
	SetRelayModelRoutes(val []ModelRouteRule) error
	SaveConfig() error
	MigrateData(
		targetPath string,
		progressCallback func(step string, status string),
		stopProxy func(),
		restartProxy func(),
		patchAll func(string) error,
		redirectPaths func(string),
	) error
}
