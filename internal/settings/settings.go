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
	// 账号数据已从单一 accounts.json 拆分为按 provider 分区存储(见 internal/account/account_storage.go):
	//   accounts_antigravity.json / accounts_project.json / accounts_nvidia.json /
	//   accounts_grok.json / accounts_other.json / accounts_2fa.json / accounts_pool.json
	// 旧 accounts.json 仍保留在清单尾部,供历史数据目录迁移时一并拷贝(首次启动由 Manager 做一次性拆分)。
	"accounts_antigravity.json",
	"accounts_project.json",
	"accounts_nvidia.json",
	"accounts_grok.json",
	"accounts_other.json",
	"accounts_2fa.json",
	"accounts_pool.json",
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
	// TargetGroupID 是 Other 号池组内细分标识(如 "openai"/"deepseek")。仅当 TargetProvider=="other" 时生效,
	// 配合 TargetProvider="other" 把入站 model 路由到 Other 号池的某个具体上游组。非 other 号池留空。
	TargetGroupID string `json:"targetGroupId,omitempty"`
	// TargetFormat 是该映射期望上游使用的原生协议("openai"/"anthropic"),仅 Other 号池组需要显式指定,
	// 供中继转发层决定上游端点与转译方向。留空时由组 Formats 决定(见 account.GetOtherGroupFormats)。
	TargetFormat string `json:"targetFormat,omitempty"`
	// Multimodal 显式声明该映射对应的上游模型是否支持多模态(原生视觉/图片理解),供 OCR 自愈降级闸
	// (relay.OCRService.modelSupportsImage)决定是否跳过 image→文本降级:
	//   - nil(缺省):交由启发式按模型名前缀判定(gemini/gpt-4o/qwen-vl 等判 true,其余 false),
	//     保持旧行为兼容,避免升级后突然把图直送给原本配好的非多模态上游触发 400;
	//   - 显式 true:强制视为多模态,跳过 OCR 降级,图块原样透传给上游(用户已确知上游支持视觉);
	//   - 显式 false:强制视为非多模态,即使名字命中启发式白名单也仍走 OCR 降级(否决冷门误判)。
	// 指针类型与 InjectChatTemplateKwargs 同款,nil 视作"未配置",非 nil 视作"已声明"。
	Multimodal *bool `json:"multimodal,omitempty"`
	// MaxInputTokens 是该模型的真实上下文窗口(input 上限, token 数),如 128000 / 262144 / 200000。
	// 仅用作 /v1/models(含 /nvidia/v1/models)Anthropic 形态响应的 max_input_tokens 字段声明,
	// 供客户端模型列表按官方 Models API schema 对齐;零值(缺省)表示不声明,沿用 Anthropic 官方
	// 对未知模型名的默认窗口认知。指针类型与 Multimodal 同款,nil/0 视作"未配置"。
	MaxInputTokens *int64 `json:"maxInputTokens,omitempty"`
	// VariantEfforts 是该模型对客户端暴露的「可选思考等级变体清单」,如 ["high","max"]。
	// /v1/models 端点会按此清单在裸 ClientModel 之外额外列出 {ClientModel}-{effort} 形式的虚项,
	// 让客户端(OpenCode / Claude Code 等)的模型选择菜单直接呈现这些带后缀的思考等级选项。
	// 客户端选这些虚项后请求时,按惯例在请求体里带 output_config.effort=<对应等级> 或
	// thinking.type=adaptive,enabling 中继现有的 thinkingRequested / normalizeEffort 思考注入链路。
	// 同时 router 缺省命中失败时,会尝试剥离 "-{effort}" 后缀再查本表并回填 effort,
	// 供转发层在客户端未显式带 output_config.effort 时兜底注入到上游。
	// 不配置或空则不展开,保持原有仅暴露裸 ClientModel 一项的行为。
	VariantEfforts []string `json:"variantEfforts,omitempty"`
	// CandidateModels 是 auto 竞速模式下配置的自定义候选模型清单。
	// 当客户端请求该映射模型时, 多个候选模型将并发发送请求, 谁先成功响应就使用谁。
	// 默认严格为空(nil/空切片), 由用户在配置面板按需添加。
	CandidateModels []string `json:"candidateModels,omitempty"`
	// UseBenchmarkPool 是否将控制台「模型响应测速」配置的模型池(BenchmarkModels)动态加入并发竞速候选。
	// 指针类型: nil/false 为不加入, 显式为 true 时动态融合测速池中的有效模型。
	UseBenchmarkPool *bool `json:"useBenchmarkPool,omitempty"`
}

// IsUseBenchmarkPool 返回该映射项是否启用了控制台测速池联动。
func (m ModelMappingEntry) IsUseBenchmarkPool() bool {
	if m.UseBenchmarkPool == nil {
		return false
	}
	return *m.UseBenchmarkPool
}

// IsMultimodal 返回该映射项是否声明为多模态模型。
// 缺省/未配置(nil)时返回 false(交由 relay 层启发式兜底判定),与 OCR 降级闸的"配置优先"语义一致。
func (m ModelMappingEntry) IsMultimodal() bool {
	if m.Multimodal == nil {
		return false
	}
	return *m.Multimodal
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
	DataDirectory        string              `json:"dataDirectory"`
	EnableSystemLog      bool                `json:"enableSystemLog"`
	IsInterceptMode      bool                `json:"isInterceptMode"`
	AutoStart            bool                `json:"autoStart"`
	SilentStart          bool                `json:"silentStart"`
	MaxRetries           int                 `json:"maxRetries"`
	MaxRetryDelay        int                 `json:"maxRetryDelay"`
	RelayEnabled         bool                `json:"relayEnabled"`
	RelayPort            string              `json:"relayPort"`
	RemoteHost           string              `json:"remoteHost"`
	RemotePort           string              `json:"remotePort"`
	RemotePath           string              `json:"remotePath"`
	RemoteKey            string              `json:"remoteKey"`
	RemotePassword       string              `json:"remotePassword"`
	RemoteEnabled        bool                `json:"remoteEnabled"`
	RelaySSRFBlock       bool                `json:"relaySSRFBlock"`
	RelayPortBlock       bool                `json:"relayPortBlock"`
	RelayDomainFilter    bool                `json:"relayDomainFilter"`
	RelayDomainWhitelist []string            `json:"relayDomainWhitelist"`
	RelayModelMapping    []ModelMappingEntry `json:"relayModelMapping"`
	DeletedModelMappings []string            `json:"deletedModelMappings"`
	// RelayModelRoutes 是「按模型路由到号池」的规则表,供 /route/* 专属入口按入站 model
	// 分发到对应 Provider 号池。空表则 /route/* 退化为「交给 nvidia 号池兜底」(向后兼容)。
	RelayModelRoutes     []ModelRouteRule `json:"relayModelRoutes,omitempty"`
	EnablePacketCapture  bool             `json:"enablePacketCapture"`
	FallbackProxyPorts   string           `json:"fallbackProxyPorts"`
	CustomSocks5Address  string           `json:"customSocks5Address"`
	CustomSocks5Enabled  bool             `json:"customSocks5Enabled"`
	CustomSocks5Username string           `json:"customSocks5Username"`
	CustomSocks5Password string           `json:"customSocks5Password"`
	// FallbackProxy* 是"NVIDIA 上游蓄流重试耗尽后的兜底出站代理",独立于上方 CustomSocks5(专属全局代理)。
	// 两者语义界限:CustomSocks5 开启后覆盖一切出站链(系统 IE 代理 + 本地端口探测全绕过);
	// FallbackProxy 仅在 NVIDIA 链路、直连 5s×5 重试全部耗尽后,切此代理再试 1 轮(单次请求级,不记忆状态)。
	// 字段为单 URL 区分协议:填 "socks5://host:port" 或 "http://host:port",http/socks5 二选一由 URL scheme 区分,
	// Username/Password 仅 socks5 或需要鉴权的 http 代理才用。
	FallbackProxyAddress    string `json:"fallbackProxyAddress"`
	FallbackProxyEnabled    bool   `json:"fallbackProxyEnabled"`
	FallbackProxyUsername   string `json:"fallbackProxyUsername"`
	FallbackProxyPassword   string `json:"fallbackProxyPassword"`
	Language                string `json:"language"`
	MaxRequestBodyMB        int    `json:"maxRequestBodyMB"`
	RequestTimeout          int    `json:"requestTimeout"`
	EnableCustomCompression bool   `json:"enableCustomCompression"`
	MaxTokensThreshold      int    `json:"maxTokensThreshold"`
	CompressionStrategy     string `json:"compressionStrategy"`
	SummaryModel            string `json:"summaryModel"`
	KeepRecentTurns         int    `json:"keepRecentTurns"`
	// OcrModel 是入站 image 自愈降级时调用的本地 Gemini OCR 模型名（单模型兼容字段）。
	// 默认 gemini-2.5-flash。前端下拉默认显示中继模型映射列表 + 兜底,可改任意 Gemini 系模型。
	// 影响:NVIDIA/Gemini 入站 image 降级链路(URL)与 descHeader 文案。
	// 空字符串走默认(见 GetOcrModel),不阻断主请求。
	OcrModel string `json:"ocrModel"`
	// OcrModels 是入站 image 自愈降级时并发竞速调用的 OCR 候选模型池。
	// 若配置多个模型，将开启并发抢跑模式，首个成功识别文本的模型胜出并秒级 Cancel 其余候选请求。
	OcrModels []string `json:"ocrModels,omitempty"`
	// NVIDIA 号池 ResourceExhausted 时的服务端就地压缩参数（公共 chatcompress 引擎）。
	NvidiaCompressEnabled         bool   `json:"nvidiaCompressEnabled"`
	NvidiaCompressThresholdTokens int    `json:"nvidiaCompressThresholdTokens"`
	NvidiaCompressKeepToolResults int    `json:"nvidiaCompressKeepToolResults"`
	// NvidiaWorkerProxyURL 是 NVIDIA 号池专用的 Cloudflare Worker 出口代理 URL (如 https://my-nvidia.workers.dev)
	NvidiaWorkerProxyURL          string `json:"nvidiaWorkerProxyUrl,omitempty"`
	NvidiaWorkerProxyEnabled      bool   `json:"nvidiaWorkerProxyEnabled"`
	// GrokWorkerProxyURL 是 Grok 号池专用的 Cloudflare Worker 出口代理 URL(号池单值,对仗 NvidiaWorkerProxyURL)。
	// 启用后 relay 把上游 baseURL 改写为该 Worker 原始上游经 X-Target-Upstream 头透传(与 NVIDIA 同口径)。
	GrokWorkerProxyURL     string `json:"grokWorkerProxyUrl,omitempty"`
	GrokWorkerProxyEnabled bool   `json:"grokWorkerProxyEnabled,omitempty"`
	// AntigravityWorkerProxyURL 是 Antigravity 官方号池专用的 Cloudflare Worker 出口代理 URL。
	// 启用后 finalRequester 把 https://{host}/v1beta/... 的 host 改写为 Worker host,
	// 原始完整 URL 经 X-Target-Upstream 头透传(与 NVIDIA 链路口径一致)。
	AntigravityWorkerProxyURL     string `json:"antigravityWorkerProxyUrl,omitempty"`
	AntigravityWorkerProxyEnabled bool   `json:"antigravityWorkerProxyEnabled,omitempty"`
	// NvidiaDedicatedProxy* 是 NVIDIA 号池专用的出站代理 (支持 SOCKS5/HTTP)
	NvidiaDedicatedProxyAddress  string `json:"nvidiaDedicatedProxyAddress,omitempty"`
	NvidiaDedicatedProxyEnabled  bool   `json:"nvidiaDedicatedProxyEnabled"`
	NvidiaDedicatedProxyUsername string `json:"nvidiaDedicatedProxyUsername,omitempty"`
	NvidiaDedicatedProxyPassword string `json:"nvidiaDedicatedProxyPassword,omitempty"`
	// NvidiaHedgeEnabled/NvidiaHedgeDelayMs 是 NVIDIA 号池「对冲请求」(hedged request) 开关与触发阈值。
	// 语义:每账号轮换的首次上游 Do 发起后,若在 DelayMs 内未收到响应头,立即用号池内另一账号
	// (并发槽成对占/释)并发补发一份完全相同的请求,谁先回响应头用谁;败方取消,不记故障、不冷却。
	// 默认关闭(零回归);代价是败方的上游预填算力浪费(0% 缓存场景上游计费可能翻倍),前端文案已明示。
	NvidiaHedgeEnabled bool `json:"nvidiaHedgeEnabled"`
	NvidiaHedgeDelayMs int  `json:"nvidiaHedgeDelayMs,omitempty"`
	// NvidiaHedgeMaxParallel 是对冲「总参赛请求数(含主请求)」,同时轰出形态:
	// 主请求 DelayMs 内未回响应头时,一次性并发补发 MaxParallel-1 份(各用不同账号)。
	// settings 层钳位 [2,512](防脏值保险丝);产品级上限由 IPC 写入处按当前启用
	// NVIDIA 账号数动态钳位,默认 2(一主一备,向后兼容旧配置零变化)。
	// 最坏情况上游计费 = MaxParallel 倍(全部败方预填算力浪费),前端文案同步警示。
	NvidiaHedgeMaxParallel int `json:"nvidiaHedgeMaxParallel,omitempty"`
	// NvidiaHedgeImmediate 是「即刻竞赛」开关:开启后不再等待 DelayMs,主请求与全部
	// 对冲在 t=0 同刻发出竞赛(每次请求上游计费恒为 MaxParallel 倍)。默认关闭。
	// 胜负/取消/差错纪律与延迟模式完全一致,仅触发时机不同。
	NvidiaHedgeImmediate bool `json:"nvidiaHedgeImmediate,omitempty"`
	PromptPrefix                  string `json:"promptPrefix"`
	CustomModelOverrideEnabled    bool   `json:"customModelOverrideEnabled"`
	CustomModelOverrideID         string `json:"customModelOverrideID"`
	// BypassOverridePrefixes 是全局模型覆写的"按前缀绕过"名单:客户端原始模型名
	// (去 "models/" 前缀、小写化后)若以其中任一前缀开头,则跳过 GlobalModelOverride,
	// 原样透传。默认 ["tab"] —— Tab 补全模型(tab_flash_lite_preview 等)本属代码补全通道,
	// 走推理上游会触发 400 INVALID_ARGUMENT,故默认放行。
	// 与思考链覆写的 isTabModel(handler_attempt_routing.go:171) 同源思路,但更通用可配。
	BypassOverridePrefixes        []string `json:"bypassOverridePrefixes"`
	CustomThinkingOverrideEnabled bool     `json:"customThinkingOverrideEnabled"`
	CustomThinkingSupports        bool     `json:"customThinkingSupports"`
	CustomThinkingBudget          int      `json:"customThinkingBudget"`
	CustomThinkingMinBudget       int      `json:"customThinkingMinBudget"`
	CustomMaxOutputTokens         int      `json:"customMaxOutputTokens"`
	ReasoningAsText               bool     `json:"reasoningAsText"`
	EnableThinkingMode            bool     `json:"enableThinkingMode"`
	EnableDebuggerMode            bool     `json:"enableDebuggerMode"`
	DebuggerLogPath               string   `json:"debuggerLogPath"`
	// NvidiaPreferredModels 是全局级"NVIDIA 专属模型清单",所有 NVIDIA 账号共用。
	// 配置后,前端"获取模型"直接返回该清单(不请求远端);为空时才请求远端 /v1/models。
	NvidiaPreferredModels []string `json:"nvidiaPreferredModels"`
	// NvidiaPreferredModelsSnapshot 记录「上次成功拉取到的 NVIDIA 上游模型全集」,
	// 供前端「获取上游模型」后与本次全集做 diff,提示本轮新增模型。omitempty 容旧配置零迁移。
	// 拉取成功即整体覆盖落盘(见 app_ipc_invoke_settings.go),非累积语义。
	NvidiaPreferredModelsSnapshot []string `json:"nvidiaPreferredModelsSnapshot,omitempty"`
	// RelayChannelModelsSnapshot 按 channel 维度记录「上次成功拉取到的上游模型全集」,
	// 供中继「模型映射」面板「获取号池模型」后与本次全集做 diff,提示本轮新增模型。
	// 键为 lowercase channel(如 "nvidia"/"google"/"deepseek"),值为去重规整后的模型 id 切片。
	// omitempty 容旧配置零迁移。某 channel 拉取成功即整体覆盖该键的值(非累积)。
	RelayChannelModelsSnapshot map[string][]string `json:"relayChannelModelsSnapshot,omitempty"`
	// AccountLayout/AccountGridColumns 是号池网格视图的纯 UI 偏好(grid|list 布局 + 3|4|5 列数)。
	// 落 config.json 而非前端 localStorage,规避 WebView2 localStorage 按 exe 构建隔离导致的重启回退。
	AccountLayout      string `json:"accountLayout"`
	AccountGridColumns int    `json:"accountGridColumns"`
	// Benchmark 模型测速(首帧/耗时)配置: 定时向所选模型发送最小流式请求测量 TTFT 与总耗时,
	// 结果落 SQLite(benchmark_results)并经 benchmark-updated 事件推送前端仪表盘卡片。
	// 测速请求走中继回环(127.0.0.1 专用监听), 复用全部路由/转译链路得真实端到端延迟,
	// 经 RelaySession.IsBenchmark 标记跳过 stats 落库, 不污染仪表盘的请求/成功率/Token 统计。
	BenchmarkEnabled         bool     `json:"benchmarkEnabled,omitempty"`
	BenchmarkModels          []string `json:"benchmarkModels,omitempty"`
	BenchmarkIntervalMinutes int      `json:"benchmarkIntervalMinutes,omitempty"`
	BenchmarkPrompt          string   `json:"benchmarkPrompt,omitempty"`
	BenchmarkTimeoutMs       int      `json:"benchmarkTimeoutMs,omitempty"`
}

// DefaultOcrModel 默认为空，由管理控制台平台配置驱动。
const DefaultOcrModel = ""

// GetDefaultModelMappings 返回默认模型映射列表。初始化严格为空，完全由平台控制台配置驱动。
func GetDefaultModelMappings() []ModelMappingEntry {
	return []ModelMappingEntry{}
}

type SessionOptimizationConfig struct {
	EnableCustomCompression bool   `json:"enableCustomCompression"`
	MaxTokensThreshold      int    `json:"maxTokensThreshold"`
	CompressionStrategy     string `json:"compressionStrategy"`
	SummaryModel            string `json:"summaryModel"`
	KeepRecentTurns         int    `json:"keepRecentTurns"`
	// NVIDIA 号池服务端就地压缩参数（公共 chatcompress 引擎）。
	NvidiaCompressEnabled         bool `json:"nvidiaCompressEnabled"`
	NvidiaCompressThresholdTokens int  `json:"nvidiaCompressThresholdTokens"`
	NvidiaCompressKeepToolResults int  `json:"nvidiaCompressKeepToolResults"`
}

// ChatCompressDefaults 集中暴露 chatcompress 引擎的默认值,供 relay 包 settings 缺字段时兜底。
const (
	ChatCompressDefaultEnabled   = true
	ChatCompressDefaultThreshold = 80000
	ChatCompressDefaultKeepN     = 4
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
	// GetOcrModels/SetOcrModels: 入站 image 自愈降级并发竞速候选模型池。
	GetOcrModels() []string
	SetOcrModels(val []string) error
	GetEnableDebuggerMode() bool
	SetEnableDebuggerMode(enable bool) error
	GetDebuggerLogPath() string
	SetDebuggerLogPath(val string) error
	GetResolvedDebuggerLogPath() string
	GetNvidiaPreferredModels() []string
	SetNvidiaPreferredModels(val []string) error
	// GetNvidiaPreferredModelsSnapshot/SetNvidiaPreferredModelsSnapshot: 上次成功拉取的 NVIDIA
	// 上游模型全集。供前端「获取上游模型」后与本次全集 diff 提示本轮新增(见 app_ipc_invoke_settings.go)。
	GetNvidiaPreferredModelsSnapshot() []string
	SetNvidiaPreferredModelsSnapshot(val []string) error
	// GetRelayChannelModelsSnapshot/SetRelayChannelModelsSnapshot: 按 channel 维度的上次上游模型全集快照。
	// 供中继「模型映射」面板「获取号池模型」后 diff 提示本轮新增。key=channel(lowercase)。
	GetRelayChannelModelsSnapshot() map[string][]string
	SetRelayChannelModelsSnapshot(channel string, val []string) error
	GetNvidiaWorkerProxyURL() string
	SetNvidiaWorkerProxyURL(val string) error
	IsNvidiaWorkerProxyEnabled() bool
	SetNvidiaWorkerProxyEnabled(val bool) error
	// Grok Worker 出口代理(号池单值,对仗 NVIDIA):URL + Enabled 成对,Enabled 需 URL 非空才视为激活。
	GetGrokWorkerProxyURL() string
	SetGrokWorkerProxyURL(val string) error
	IsGrokWorkerProxyEnabled() bool
	SetGrokWorkerProxyEnabled(val bool) error
	// Antigravity Worker 出口代理(号池单值,对仗 NVIDIA):URL + Enabled 成对,Enabled 需 URL 非空才视为激活。
	GetAntigravityWorkerProxyURL() string
	SetAntigravityWorkerProxyURL(val string) error
	IsAntigravityWorkerProxyEnabled() bool
	SetAntigravityWorkerProxyEnabled(val bool) error
	GetNvidiaDedicatedProxyAddress() string
	SetNvidiaDedicatedProxyAddress(val string) error
	GetNvidiaDedicatedProxyEnabled() bool
	SetNvidiaDedicatedProxyEnabled(val bool) error
	GetNvidiaDedicatedProxyUsername() string
	SetNvidiaDedicatedProxyUsername(val string) error
	GetNvidiaDedicatedProxyPassword() string
	SetNvidiaDedicatedProxyPassword(val string) error
	// IsNvidiaHedgeEnabled/SetNvidiaHedgeEnabled: NVIDIA 对冲请求开关(默认关,零回归)。
	IsNvidiaHedgeEnabled() bool
	SetNvidiaHedgeEnabled(val bool) error
	// GetNvidiaHedgeDelayMs/SetNvidiaHedgeDelayMs: 对冲触发延迟(毫秒)。读写两侧共用同一
	// 归一化(0/负 → 默认 10000,越界钳位 [2000,60000]),落盘值与生效值恒一致。
	GetNvidiaHedgeDelayMs() int
	SetNvidiaHedgeDelayMs(val int) error
	// GetNvidiaHedgeMaxParallel/SetNvidiaHedgeMaxParallel: 对冲总参赛请求数(含主请求),
	// 归一化钳位 [2,5],0/缺省 → 2(一主一备,向后兼容)。
	GetNvidiaHedgeMaxParallel() int
	SetNvidiaHedgeMaxParallel(val int) error
	// IsNvidiaHedgeImmediate/SetNvidiaHedgeImmediate: 即刻竞赛开关(默认关=延迟对冲模式)。
	IsNvidiaHedgeImmediate() bool
	SetNvidiaHedgeImmediate(val bool) error
	// GetAccountLayout/SetAccountLayout: 号池视图布局("grid"|"list"),纯 UI pref,落 config.json。
	GetAccountLayout() string
	SetAccountLayout(layout string) error
	// GetAccountGridColumns/SetAccountGridColumns: 号池网格列数(3|4|5),纯 UI pref,落 config.json。
	GetAccountGridColumns() int
	SetAccountGridColumns(cols int) error
	// GetMaxInputTokensByModel: 按「上游模型 id → 上下文窗口」解析模型列表 max_input_tokens
	// 声明的查询函数。allowlist 为空不过滤;fallback 为未显式配置时的兜底窗口(0=不声明)。
	GetMaxInputTokensByModel(allowlist []string, fallback int64) func(string) int64
	// GetRelayModelMappingSafe: GetRelayModelMapping 的 nil 安全版本(测试/未注入 Manager 时返回空)。
	GetRelayModelMappingSafe() []ModelMappingEntry
	// GetRelayModelRoutes/SetRelayModelRoutes:「按模型路由到号池」规则表。
	// /route/* 专属入口按入站 model 命中规则,分发到 TargetProvider 号池。
	GetRelayModelRoutes() []ModelRouteRule
	SetRelayModelRoutes(val []ModelRouteRule) error
	// GetBenchmarkConfig/SetBenchmarkConfig: 模型测速(首帧/耗时)配置聚合读写。
	// 聚合 Enabled/Models/IntervalMinutes/Prompt/TimeoutMs 五字段, 读写两侧共用归一化兜底。
	GetBenchmarkConfig() BenchmarkConfig
	SetBenchmarkConfig(cfg BenchmarkConfig) error
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
