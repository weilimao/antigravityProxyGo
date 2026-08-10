<template>
<div class="flex flex-col gap-6 w-full" id="settings-panel-general">
<!-- 数据存储路径卡片 -->
<div class="glass-card rounded-xl p-6 flex flex-col gap-4">
<h2 class="text-[15px] font-bold text-on-surface dark:text-white flex items-center gap-2">
<span class="material-symbols-outlined text-primary text-[20px]">folder_open</span>
<span data-i18n="dataDirLabel">数据存储位置</span>
</h2>
<p class="text-xs text-outline leading-relaxed" data-i18n="dataDirTip">
                所有核心数据（账号凭证、流量统计数据、计费配置、以及局域网 CA 证书）均保存在此目录中。更改此路径后，系统会自动将您之前存储的数据完整迁移至新位置。
            </p>
<div class="flex flex-col gap-2 mt-2">
<label class="text-[12px] font-bold text-outline" data-i18n="currentDirLabel">当前存储路径</label>
<div class="flex gap-2">
<input class="flex-grow px-3 py-2 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:outline-none text-on-surface dark:text-white" id="txtDataDir" readonly type="text" />
<button class="px-4 py-2 bg-primary text-white hover:bg-primary/90 rounded-md text-[13px] font-bold transition-colors shadow-sm flex items-center gap-1.5 cursor-pointer" id="btnBrowseDir">
<span class="material-symbols-outlined text-[16px]">folder</span>
<span data-i18n="btnChangeDir">更改位置</span>
</button>
</div>
</div>
<!-- 状态提示 -->
<div class="hidden text-[12px] p-3 rounded-lg border flex flex-col gap-1" id="migrationStatus">
<div class="font-bold flex items-center gap-1.5 text-on-surface dark:text-white">
<span class="material-symbols-outlined text-[16px] text-primary">info</span>
<span data-i18n="migrationStatusTitle">数据迁移状态</span>
</div>
<div class="text-[12px] text-outline mt-1 font-medium" id="migrationStatusMsg"></div>
</div>
</div>
<!-- 控制台日志设置卡片 -->
<div class="glass-card rounded-xl p-6 flex flex-col gap-4">
<h2 class="text-[15px] font-bold text-on-surface dark:text-white flex items-center gap-2">
<span class="material-symbols-outlined text-primary text-[20px]">terminal</span>
<span data-i18n="logSettingTitle">控制台日志设置</span>
</h2>
<p class="text-xs text-outline leading-relaxed" data-i18n="logSettingTip">
                启用或禁用底部控制台系统日志的实时输出。禁用此功能可减少日志输出和渲染，从而显著节省内存并提升系统运行性能。
            </p>
<div class="flex items-center justify-between border-t border-outline-variant/20 pt-4 mt-2">
<div class="flex flex-col gap-0.5">
<span class="text-[13px] font-bold text-on-surface dark:text-white" data-i18n="enableLogLabel">启用控制台系统日志</span>
<span class="text-[11px] text-outline text-wrap max-w-[80%]" data-i18n="enableLogDesc">关闭后将不再输出和记录新的系统日志，并隐藏底部的系统日志抽屉。</span>
</div>
<!-- Toggle Switch -->
<label class="relative inline-flex items-center cursor-pointer">
<input class="sr-only peer" id="chkEnableSystemLog" type="checkbox"/>
<div class="w-11 h-6 bg-slate-200 dark:bg-white/10 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full rtl:peer-checked:after:-translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:start-[2px] after:bg-white after:border-gray-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all dark:border-gray-600 peer-checked:bg-primary"></div>
</label>
</div>
</div>

<!-- 思维链与思考模式设置卡片 (与全站标准卡片一致) -->
<div class="glass-card rounded-xl p-6 flex flex-col gap-4">
<h2 class="text-[15px] font-bold text-on-surface dark:text-white flex items-center gap-2">
<span class="material-symbols-outlined text-primary text-[20px]">psychology</span>
<span>思维链与思考模式设置</span>
</h2>
<p class="text-xs text-outline leading-relaxed" data-i18n="thinkingModeCardTip">
全局总开关：控制 NVIDIA NIM 与 Gemini 转译链路是否向请求注入思考参数（让上游输出明文思维链）。关闭后代理透明剥离所有思考配置，模型直出正文。Claude 协议直通不经此开关。NVIDIA 链路专属的「思考过程直吐正文」展现项已移至「NVIDIA设置」tab。
</p>

<!-- 开关 1：启用思考模式 -->
<div class="flex items-center justify-between border-t border-outline-variant/20 pt-4 mt-2">
<div class="flex flex-col gap-0.5">
<span class="text-[13px] font-bold text-on-surface dark:text-white" data-i18n="enableThinkingModeLabel">开启思考模式 (Enable Thinking Mode)</span>
<span class="text-[11px] text-outline text-wrap max-w-[80%]" data-i18n="enableThinkingModeDesc">默认开启。作用于 NVIDIA NIM 与 Gemini 转译链路：关闭后代理将透明剥离请求 Payload 中的思考参数，让上游模型直出正文回答，降低响应开销。</span>
</div>
<!-- Toggle Switch -->
<label class="relative inline-flex items-center cursor-pointer">
<input class="sr-only peer" id="chkEnableThinkingMode" type="checkbox" checked />
<div class="w-11 h-6 bg-slate-200 dark:bg-white/10 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full rtl:peer-checked:after:-translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:start-[2px] after:bg-white after:border-gray-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all dark:border-gray-600 peer-checked:bg-primary"></div>
</label>
</div>
</div>
<!-- 抓包分析设置卡片 -->
<div class="glass-card rounded-xl p-6 flex flex-col gap-4">
<h2 class="text-[15px] font-bold text-on-surface dark:text-white flex items-center gap-2">
<span class="material-symbols-outlined text-primary text-[20px]">analytics</span>
<span data-i18n="packetSettingTitle">抓包分析设置</span>
</h2>
<p class="text-xs text-outline leading-relaxed" data-i18n="packetSettingTip">
                配置是否在顶部菜单栏显示“抓包分析”功能。关闭后将隐藏“抓包分析”菜单，同时停止在本地抓取并存储所有的接口数据包。
            </p>
<div class="flex items-center justify-between border-t border-outline-variant/20 pt-4 mt-2">
<div class="flex flex-col gap-0.5">
<span class="text-[13px] font-bold text-on-surface dark:text-white" data-i18n="enablePacketCaptureLabel">显示抓包分析并进行抓包</span>
<span class="text-[11px] text-outline text-wrap max-w-[80%]" data-i18n="enablePacketCaptureDesc">关闭后隐藏菜单栏的抓包分析选项，并不再记录和持久化保存任何接口的请求与响应数据。</span>
</div>
<!-- Toggle Switch -->
<label class="relative inline-flex items-center cursor-pointer">
<input class="sr-only peer" id="chkEnablePacketCapture" type="checkbox"/>
<div class="w-11 h-6 bg-slate-200 dark:bg-white/10 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full rtl:peer-checked:after:-translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:start-[2px] after:bg-white after:border-gray-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all dark:border-gray-600 peer-checked:bg-primary"></div>
</label>
</div>
</div>
<!-- 代理参数设置卡片 -->
<div class="glass-card rounded-xl p-6 flex flex-col gap-4">
<h2 class="text-[15px] font-bold text-on-surface dark:text-white flex items-center gap-2">
<span class="material-symbols-outlined text-primary text-[20px]">refresh</span>
<span data-i18n="proxySettingTitle">代理重试设置</span>
</h2>
<p class="text-xs text-outline leading-relaxed" data-i18n="proxySettingTip">
                配置代理在遭遇服务器临时算力不足等错误时的最大重试次数。
            </p>
<div class="flex items-center justify-between border-t border-outline-variant/20 pt-4 mt-2">
<div class="flex flex-col gap-0.5">
<span class="text-[13px] font-bold text-on-surface dark:text-white" data-i18n="maxRetriesLabel">最大重试次数</span>
<span class="text-[11px] text-outline text-wrap max-w-[80%]" data-i18n="maxRetriesDesc">请求失败时的重试上限（默认 20 次）。</span>
</div>
<div class="flex items-center gap-2">
<input class="w-20 px-3 py-1 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:outline-none text-on-surface dark:text-white font-bold text-center" id="numMaxRetries" max="100" min="1" type="number">
</input></div>
</div>
<div class="flex items-center justify-between border-t border-outline-variant/20 pt-4 mt-2">
<div class="flex flex-col gap-0.5">
<span class="text-[13px] font-bold text-on-surface dark:text-white" data-i18n="maxRetryDelayLabel">最大重试延迟上限 (秒)</span>
<span class="text-[11px] text-outline text-wrap max-w-[80%]" data-i18n="maxRetryDelayDesc">连续多次重试时的最大等待退避时间（默认 10 秒）。</span>
</div>
<div class="flex items-center gap-2">
<input class="w-20 px-3 py-1 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:outline-none text-on-surface dark:text-white font-bold text-center" id="numMaxRetryDelay" max="300" min="1" type="number">
</input></div>
</div>
<div class="flex items-center justify-between border-t border-outline-variant/20 pt-4 mt-2">
<div class="flex flex-col gap-0.5">
<span class="text-[13px] font-bold text-on-surface dark:text-white" data-i18n="maxRequestBodyLabel">请求体大小限制 (MB)</span>
<span class="text-[11px] text-outline text-wrap max-w-[80%]" data-i18n="maxRequestBodyDesc">单次请求体的最大字节数（默认 50 MB）。设为 0 表示不限制。</span>
</div>
<div class="flex items-center gap-2">
<input class="w-20 px-3 py-1 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:outline-none text-on-surface dark:text-white font-bold text-center" id="numMaxRequestBodyMB" max="500" min="1" type="number">
</input></div>
</div>
<div class="flex items-center justify-between border-t border-outline-variant/20 pt-4 mt-2">
<div class="flex flex-col gap-0.5">
<span class="text-[13px] font-bold text-on-surface dark:text-white" data-i18n="requestTimeoutLabel">请求超时时间 (秒)</span>
<span class="text-[11px] text-outline text-wrap max-w-[80%]" data-i18n="requestTimeoutDesc">代理处理单次请求的最长等待时间（默认 300 秒）。</span>
</div>
<div class="flex items-center gap-2">
<input class="w-20 px-3 py-1 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:outline-none text-on-surface dark:text-white font-bold text-center" id="numRequestTimeout" max="1800" min="1" type="number">
</input></div>
</div>
</div>

<!-- 本地代理与 Fallback 中转设置卡片 -->
<div class="glass-card rounded-xl p-6 flex flex-col gap-4">
<h2 class="text-[15px] font-bold text-on-surface dark:text-white flex items-center gap-2">
<span class="material-symbols-outlined text-primary text-[20px]">dns</span>
<span data-i18n="fallbackSettingTitle">本地代理与 Fallback 中转设置</span>
</h2>
<p class="text-xs text-outline leading-relaxed" data-i18n="fallbackSettingTip">
配置当系统代理为空（如仅开启 Clash TUN 虚拟网卡模式）时的本地 Fallback 代理探测，或指定全局专属 SOCKS5 代理。
</p>
<div class="flex flex-col gap-3 border-t border-outline-variant/20 pt-4 mt-2">
<div class="flex items-center justify-between">
<div class="flex flex-col gap-0.5">
<label class="text-[13px] font-bold text-on-surface dark:text-white" data-i18n="customSocks5EnabledLabel">启用专属出站代理 (支持 HTTP/SOCKS5)</label>
<span class="text-[11px] text-outline text-wrap max-w-[80%]" data-i18n="customSocks5EnabledDesc">开启后将强行且仅通过此代理访问外网，完全不走 Windows 系统代理。支持 HTTP 和 SOCKS5 协议。</span>
</div>
<label class="relative inline-flex items-center cursor-pointer">
<input class="sr-only peer" id="chkCustomSocks5Enabled" type="checkbox"/>
<div class="w-11 h-6 bg-slate-200 dark:bg-white/10 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full rtl:peer-checked:after:-translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:start-[2px] after:bg-white after:border-gray-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all dark:border-gray-600 peer-checked:bg-primary"></div>
</label>
</div>
<div class="flex flex-col gap-3 mt-1" id="divCustomSocks5Address">
<div class="flex flex-col gap-1.5">
<label class="text-[12px] font-bold text-outline" data-i18n="customSocks5AddressLabel">专属代理地址 (协议必须显式配置，如 http:// 或 socks5://)</label>
<input class="px-3 py-2 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:outline-none text-on-surface dark:text-white" id="txtCustomSocks5Address" placeholder="例如：http://127.0.0.1:8080 或 socks5://127.0.0.1:1080" data-i18n-placeholder="customSocks5AddressPlaceholder" type="text"/>
</div>
<div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
<div class="flex flex-col gap-1.5">
<label class="text-[12px] font-bold text-outline" data-i18n="customSocks5UsernameLabel">专属 SOCKS5 用户名 (可选)</label>
<input class="px-3 py-2 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:outline-none text-on-surface dark:text-white" id="txtCustomSocks5Username" placeholder="无" data-i18n-placeholder="optionalPlaceholder" type="text"/>
</div>
<div class="flex flex-col gap-1.5">
<label class="text-[12px] font-bold text-outline" data-i18n="customSocks5PasswordLabel">专属 SOCKS5 密码 (可选)</label>
<PasswordInput inputId="txtCustomSocks5Password" placeholder="无" dataI18nPlaceholder="optionalPlaceholder" inputClass="w-full px-3 py-2 pr-9 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:outline-none text-on-surface dark:text-white"/>
</div>
</div>
</div>
</div>
<div class="flex flex-col gap-2 border-t border-outline-variant/20 pt-4">
<div class="flex flex-col gap-0.5">
<span class="text-[13px] font-bold text-on-surface dark:text-white" data-i18n="fallbackPortsLabel">Fallback 自定义探测端口</span>
<span class="text-[11px] text-outline text-wrap" data-i18n="fallbackPortsDesc">当系统没有配置代理时（只开 TUN 模式），除了默认扫描常用端口（7890/7897等），还会扫描此处的端口做代理回退。多个端口用英文逗号分隔。</span>
</div>
<input class="px-3 py-2 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:outline-none text-on-surface dark:text-white mt-1" id="txtFallbackProxyPorts" placeholder="例如：8888, 9999" data-i18n-placeholder="fallbackPortsPlaceholder" type="text"/>
</div>
	</div>

	<!-- 自定义消息前缀设置卡片 -->
	<div class="glass-card rounded-xl p-6 flex flex-col gap-4">
		<h2 class="text-[15px] font-bold text-on-surface dark:text-white flex items-center gap-2">
			<span class="material-symbols-outlined text-primary text-[20px]">chat_bubble</span>
			<span data-i18n="promptPrefixTitle">自定义消息前缀</span>
		</h2>
		<p class="text-xs text-outline leading-relaxed" data-i18n="promptPrefixTip">
			在发送请求到上游之前，会自动在每条发送的内容消息主体最前端拼接上此处配置的文本，可用于对模型预设规则、系统提示词补充或限制回答格式等。该配置仅在流式聊天接口（如 Chat 窗口）生效，不干扰后台代码自动补全。留空则不拼接。
		</p>
		<div class="flex flex-col gap-1.5 border-t border-outline-variant/20 pt-4 mt-2">
			<label class="text-[12px] font-bold text-outline" data-i18n="promptPrefixLabel">消息前缀文本 (支持多行)</label>
			<textarea class="px-3 py-2 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:outline-none text-on-surface dark:text-white mt-1 h-20 resize-y" id="txtPromptPrefix" placeholder="例如：[请用中文回答] " data-i18n-placeholder="promptPrefixPlaceholder"></textarea>
		</div>
	</div>

	<!-- 全局自定义模型覆写卡片 -->
	<div class="glass-card rounded-xl p-6 flex flex-col gap-4">
		<h2 class="text-[15px] font-bold text-on-surface dark:text-white flex items-center gap-2">
			<span class="material-symbols-outlined text-primary text-[20px]">transform</span>
			<span data-i18n="customModelOverrideTitle">全局自定义模型覆写</span>
		</h2>
		<p class="text-xs text-outline leading-relaxed" data-i18n="customModelOverrideTip">
			配置强制拦截并修改客户端请求的原始模型。开启后，不论客户端请求任何模型（如 gemini-1.5-flash），代理都会在底层透明地将其路由到此处指定的模型，而对客户端保持无感。
		</p>
		<div class="flex items-center justify-between border-t border-outline-variant/20 pt-4 mt-2">
			<div class="flex flex-col gap-0.5">
				<span class="text-[13px] font-bold text-on-surface dark:text-white" data-i18n="enableCustomModelOverrideLabel">启用全局模型覆写</span>
				<span class="text-[11px] text-outline text-wrap max-w-[80%]" data-i18n="enableCustomModelOverrideDesc">强制接管并忽略客户端传递的模型标识。</span>
			</div>
			<!-- Toggle Switch -->
			<label class="relative inline-flex items-center cursor-pointer">
				<input class="sr-only peer" id="chkEnableCustomModelOverride" type="checkbox"/>
				<div class="w-11 h-6 bg-slate-200 dark:bg-white/10 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full rtl:peer-checked:after:-translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:start-[2px] after:bg-white after:border-gray-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all dark:border-gray-600 peer-checked:bg-primary"></div>
			</label>
		</div>
		<div class="flex flex-col gap-1.5 mt-1" id="divCustomModelOverrideOptions">
			<label class="text-[12px] font-bold text-outline" data-i18n="customModelOverrideIDLabel">目标模型 ID (被覆写的实际模型)</label>
			<input class="px-3 py-2 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:outline-none text-on-surface dark:text-white" id="txtCustomModelOverrideID" placeholder="例如：gemini-1.5-pro" data-i18n-placeholder="customModelOverrideIDPlaceholder" type="text" />
			<label class="text-[12px] font-bold text-outline mt-2" data-i18n="bypassOverridePrefixesLabel">覆写绕过模型前缀</label>
			<p class="text-[11px] text-outline text-wrap max-w-[80%] leading-relaxed" data-i18n="bypassOverridePrefixesTip">按前缀匹配的模型将跳过全局覆写、原样透传（例如 Tab 补全模型 tab_flash_lite_preview 不应被改向推理上游）。多个前缀用英文逗号分隔，留空表示不绕过任何模型。</p>
			<input class="px-3 py-2 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:outline-none text-on-surface dark:text-white" id="txtBypassOverridePrefixes" placeholder="例如：tab" data-i18n-placeholder="bypassOverridePrefixesPlaceholder" type="text" />
		</div>

		<!-- 全局思维链预算 (Thinking Budget) 覆写开关与参数 -->
		<div class="flex items-center justify-between border-t border-outline-variant/20 pt-4 mt-2">
			<div class="flex flex-col gap-0.5">
				<span class="text-[13px] font-bold text-on-surface dark:text-white" data-i18n="enableCustomThinkingOverrideLabel">启用思维链预算 (Thinking Budget) 覆写</span>
				<span class="text-[11px] text-outline text-wrap max-w-[80%]" data-i18n="enableCustomThinkingOverrideDesc">开启后，强制重写请求 Payload 中的 thinkingConfig 深度思考 Token 预算。</span>
			</div>
			<!-- Toggle Switch -->
			<label class="relative inline-flex items-center cursor-pointer">
				<input class="sr-only peer" id="chkEnableCustomThinkingOverride" type="checkbox"/>
				<div class="w-11 h-6 bg-slate-200 dark:bg-white/10 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full rtl:peer-checked:after:-translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:start-[2px] after:bg-white after:border-gray-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all dark:border-gray-600 peer-checked:bg-primary"></div>
			</label>
		</div>
		<div class="flex flex-col gap-3 mt-1" id="divCustomThinkingOverrideOptions">
			<div class="flex items-center justify-between">
				<label class="text-[12px] font-bold text-outline" data-i18n="customThinkingSupportsLabel">声明模型具备思维链能力 (supportsThinking)</label>
				<label class="relative inline-flex items-center cursor-pointer">
					<input class="sr-only peer" id="chkCustomThinkingSupports" type="checkbox"/>
					<div class="w-9 h-5 bg-slate-200 dark:bg-white/10 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full rtl:peer-checked:after:-translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:start-[2px] after:bg-white after:border-gray-300 after:border after:rounded-full after:h-4 after:w-4 after:transition-all dark:border-gray-600 peer-checked:bg-primary"></div>
				</label>
			</div>
			<div class="flex flex-col gap-1">
				<label class="text-[12px] font-bold text-outline" data-i18n="customThinkingBudgetLabel">默认思考 Token 预算 (thinkingBudget: -1 自适应, 0 关闭, >0 固定上限)</label>
				<input class="px-3 py-2 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:outline-none text-on-surface dark:text-white" id="txtCustomThinkingBudget" placeholder="-1" type="number" />
			</div>
			<div class="flex flex-col gap-1">
				<label class="text-[12px] font-bold text-outline" data-i18n="customThinkingMinBudgetLabel">最小思考 Token 限制 (minThinkingBudget)</label>
				<input class="px-3 py-2 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:outline-none text-on-surface dark:text-white" id="txtCustomThinkingMinBudget" placeholder="32" type="number" />
			</div>
			<div class="flex flex-col gap-1">
				<label class="text-[12px] font-bold text-outline" data-i18n="customMaxOutputTokensLabel">最大单次输出 Token 上限 (maxOutputTokens)</label>
				<input class="px-3 py-2 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:outline-none text-on-surface dark:text-white" id="txtCustomMaxOutputTokens" placeholder="65536" type="number" />
			</div>
		</div>
	</div>

	<!-- OCR 图片分析设置卡片 -->
	<div class="glass-card rounded-xl p-6 flex flex-col gap-4">
		<h2 class="text-[15px] font-bold text-on-surface dark:text-white flex items-center gap-2">
			<span class="material-symbols-outlined text-primary text-[20px]">image_search</span>
			<span data-i18n="ocrCardTitle">OCR 图片分析设置</span>
		</h2>
		<p class="text-xs text-outline leading-relaxed" data-i18n="ocrCardTip">
			当上游模型不支持多模态（如 NVIDIA 中继）时，代理会自动把入站的图片内容块 OCR 降级为纯文本后再送上游。此处选择用于执行 OCR 的本地 Gemini 系模型。
		</p>
		<div class="flex flex-col gap-1.5 border-t border-outline-variant/20 pt-4 mt-2">
			<label class="text-[12px] font-bold text-outline" data-i18n="ocrModelLabel">OCR 图片分析模型</label>
			<select class="px-3 py-2 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:outline-none text-on-surface dark:text-white font-medium" id="selOcrModel">
				<!-- 动态填充 -->
			</select>
			<span class="text-[11px] text-outline leading-relaxed" data-i18n="ocrModelDesc">入站图片自动 OCR 降级时调用的本地模型,默认 gemini-2.5-flash,可从中继模型映射列表任选 Gemini 系模型。</span>
		</div>
	</div>

	<!-- 会话优化与压缩设置卡片 -->
	<div class="glass-card rounded-xl p-6 flex flex-col gap-4">
		<h2 class="text-[15px] font-bold text-on-surface dark:text-white flex items-center gap-2">
			<span class="material-symbols-outlined text-primary text-[20px]">compress</span>
			<span data-i18n="sessionOptimizationTitle">会话优化与压缩设置</span>
		</h2>
		<p class="text-xs text-outline leading-relaxed" data-i18n="sessionOptimizationTip">
			配置代理在遭遇会话超长时的主动会话压缩和模型降级路由。开启后，将从已启用模型列表中选择低成本模型默默代为总结并节省 Token 支出。
		</p>
		<div class="flex items-center justify-between border-t border-outline-variant/20 pt-4 mt-2">
			<div class="flex flex-col gap-0.5">
				<span class="text-[13px] font-bold text-on-surface dark:text-white" data-i18n="enableCustomCompressionLabel">启用自定义会话压缩</span>
				<span class="text-[11px] text-outline text-wrap max-w-[80%]" data-i18n="enableCustomCompressionDesc">开启后，当上一轮请求输入超过设定的 Token 阈值时，自动在代理侧对历史做摘要压缩。</span>
			</div>
			<!-- Toggle Switch -->
			<label class="relative inline-flex items-center cursor-pointer">
				<input class="sr-only peer" id="chkEnableCustomCompression" type="checkbox"/>
				<div class="w-11 h-6 bg-slate-200 dark:bg-white/10 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full rtl:peer-checked:after:-translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:start-[2px] after:bg-white after:border-gray-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all dark:border-gray-600 peer-checked:bg-primary"></div>
			</label>
		</div>
		<div class="flex flex-col gap-3 mt-1" id="divSessionCompressionOptions">
			<div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
				<div class="flex flex-col gap-1.5">
					<label class="text-[12px] font-bold text-outline" data-i18n="maxTokensThresholdLabel">触发压缩的 Token 阈值</label>
					<input class="px-3 py-2 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:outline-none text-on-surface dark:text-white font-bold" id="numMaxTokensThreshold" min="1000" step="1000" type="number"/>
				</div>
				<div class="flex flex-col gap-1.5">
					<label class="text-[12px] font-bold text-outline" data-i18n="keepRecentTurnsLabel">压缩后保留的最近对话轮数</label>
					<input class="px-3 py-2 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:outline-none text-on-surface dark:text-white font-bold" id="numKeepRecentTurns" min="1" max="50" type="number"/>
				</div>
			</div>
			<div class="flex flex-col gap-1.5">
				<label class="text-[12px] font-bold text-outline" data-i18n="summaryModelLabel">用于生成摘要的低配模型</label>
				<select class="px-3 py-2 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:outline-none text-on-surface dark:text-white font-medium" id="selSummaryModel">
					<!-- 动态填充 -->
				</select>
			</div>
		</div>
	</div>

	<!-- 系统启动设置卡片 -->
	<div class="glass-card rounded-xl p-6 flex flex-col gap-4">
<h2 class="text-[15px] font-bold text-on-surface dark:text-white flex items-center gap-2">
<span class="material-symbols-outlined text-primary text-[20px]">settings_power</span>
<span data-i18n="startupSettingTitle">系统启动设置</span>
</h2>
<p class="text-xs text-outline leading-relaxed" data-i18n="startupSettingTip">
                配置开机自启动与启动时的显示方式。
            </p>
<div class="flex items-center justify-between border-t border-outline-variant/20 pt-4 mt-2">
<div class="flex flex-col gap-0.5">
<span class="text-[13px] font-bold text-on-surface dark:text-white" data-i18n="enableAutoStartLabel">开机自启动</span>
<span class="text-[11px] text-outline text-wrap max-w-[80%]" data-i18n="enableAutoStartDesc">在系统启动时自动运行 Antigravity Proxy。</span>
</div>
<!-- Toggle Switch -->
<label class="relative inline-flex items-center cursor-pointer">
<input class="sr-only peer" id="chkEnableAutoStart" type="checkbox"/>
<div class="w-11 h-6 bg-slate-200 dark:bg-white/10 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full rtl:peer-checked:after:-translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:start-[2px] after:bg-white after:border-gray-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all dark:border-gray-600 peer-checked:bg-primary"></div>
</label>
</div>
<div class="flex items-center justify-between border-t border-outline-variant/20 pt-4">
<div class="flex flex-col gap-0.5">
<span class="text-[13px] font-bold text-on-surface dark:text-white" data-i18n="enableSilentStartLabel">静默启动</span>
<span class="text-[11px] text-outline text-wrap max-w-[80%]" data-i18n="enableSilentStartDesc">自启动时保持在后台运行，只在托盘显示，不打开主界面。</span>
</div>
<!-- Toggle Switch -->
<label class="relative inline-flex items-center cursor-pointer">
<input class="sr-only peer" id="chkEnableSilentStart" type="checkbox"/>
<div class="w-11 h-6 bg-slate-200 dark:bg-white/10 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full rtl:peer-checked:after:-translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:start-[2px] after:bg-white after:border-gray-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all dark:border-gray-600 peer-checked:bg-primary"></div>
</label>
</div>
</div>
</div>
</template>

<script setup lang="ts">
// GeneralPanel: 从 Settings.vue 提取的纯展示面板。
// 保留所有 id / data-i18n / onclick / class 属性，
// 使 settingsController / relayController 的 getElementById 与 classList 操作零改动。
</script>
