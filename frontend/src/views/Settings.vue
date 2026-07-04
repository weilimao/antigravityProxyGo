<template>
<div class="flex flex-col gap-5 w-full" id="view-settings">
<div class="flex flex-wrap items-center justify-between gap-4 border-b border-outline-variant/20 pb-4">
<div>
<h1 class="text-2xl font-bold text-on-surface dark:text-white" data-i18n="settingsTitle">系统设置</h1>
<p class="text-xs text-outline dark:text-outline-variant" data-i18n="settingsDesc">配置代理软件的底层行为与本地数据存储路径</p>
</div>
<!-- Sub Tab Menu -->
<div class="flex gap-1 bg-slate-100 dark:bg-white/5 p-1 rounded-lg text-[12px]">
<button class="px-4 py-1.5 text-[12px] bg-white dark:bg-[#1a1f30] text-primary dark:text-primary-fixed-dim rounded-md shadow-sm font-bold cursor-pointer transition-all duration-200" id="btnSettingsTabGeneral" data-i18n="settingsTabGeneral">参数配置</button>
<button class="px-4 py-1.5 text-[12px] text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200 rounded-md font-medium cursor-pointer transition-all duration-200" data-i18n="settingsTabRelay" id="btnSettingsTabRelay">中继服务器</button>
<button class="px-4 py-1.5 text-[12px] text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200 rounded-md font-medium cursor-pointer transition-all duration-200" id="btnSettingsTabNetwork" data-i18n="settingsTabNetwork">网络监控</button>
<button class="px-4 py-1.5 text-[12px] text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200 rounded-md font-medium cursor-pointer transition-all duration-200" id="btnSettingsTabAbout" data-i18n="settingsTabAbout">关于</button>
</div>
</div>
<!-- 参数配置面板 -->
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
<input class="flex-grow px-3 py-2 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:outline-none text-on-surface dark:text-white" id="txtDataDir" readonly type="text">
<button class="px-4 py-2 bg-primary text-white hover:bg-primary/90 rounded-md text-[13px] font-bold transition-colors shadow-sm flex items-center gap-1.5 cursor-pointer" id="btnBrowseDir">
<span class="material-symbols-outlined text-[16px]">folder</span>
<span data-i18n="btnChangeDir">更改位置</span>
</button>
</input></div>
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
<input class="px-3 py-2 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:outline-none text-on-surface dark:text-white" id="txtCustomSocks5Password" placeholder="无" data-i18n-placeholder="optionalPlaceholder" type="password"/>
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
<!-- End settings-panel-general -->

<!-- 网络监控面板 -->
<div class="hidden flex-col gap-6 w-full" id="settings-panel-network">
    <!-- 实时网络状态卡片 -->
    <div class="glass-card rounded-xl p-6 flex flex-col gap-4">
        <h2 class="text-[15px] font-bold text-on-surface dark:text-white flex items-center gap-2">
            <span class="material-symbols-outlined text-primary text-[20px]">lan</span>
            <span data-i18n="netStatusTitle">当前本地代理状态</span>
        </h2>
        <div class="grid grid-cols-1 md:grid-cols-3 gap-4 mt-2">
            <div class="bg-slate-50 dark:bg-white/5 p-4 rounded-lg border border-outline-variant/20 flex flex-col gap-1">
                <span class="text-[11px] text-outline font-bold" data-i18n="netStatusFallbackLabel">Fallback 探测代理</span>
                <span class="text-[13px] font-mono font-bold text-on-surface dark:text-white" id="lblNetStatusFallback" data-i18n="netStatusDetecting">正在检测...</span>
            </div>
            <div class="bg-slate-50 dark:bg-white/5 p-4 rounded-lg border border-outline-variant/20 flex flex-col gap-1">
                <span class="text-[11px] text-outline font-bold" data-i18n="netStatusCustomSocksLabel">专属 SOCKS5 代理</span>
                <span class="text-[13px] font-mono font-bold text-on-surface dark:text-white" id="lblNetStatusCustomSocks" data-i18n="netStatusDisabled">未启用</span>
            </div>
            <div class="bg-slate-50 dark:bg-white/5 p-4 rounded-lg border border-outline-variant/20 flex flex-col gap-1">
                <span class="text-[11px] text-outline font-bold" data-i18n="netStatusPeriodLabel">后台探测周期</span>
                <span class="text-[13px] font-medium text-on-surface dark:text-white" data-i18n="netStatusPeriodValue">每 15 秒轮询</span>
            </div>
        </div>
    </div>

    <!-- 滚动出站网络日志卡片 -->
    <div class="glass-card rounded-xl p-6 flex flex-col gap-4">
        <div class="flex items-center justify-between">
            <h2 class="text-[15px] font-bold text-on-surface dark:text-white flex items-center gap-2">
                <span class="material-symbols-outlined text-primary text-[20px]">list_alt</span>
                <span data-i18n="netLogsTitle">出站路由跟踪日志 (最近 100 条)</span>
            </h2>
            <button class="px-3 py-1 bg-primary/10 hover:bg-primary/20 text-primary border border-primary/20 rounded-lg text-[11px] font-semibold transition-all cursor-pointer flex items-center gap-1" id="btnRefreshNetLogs">
                <span class="material-symbols-outlined text-[14px]">refresh</span>
                <span data-i18n="btnManualRefresh">手动刷新</span>
            </button>
        </div>
        <p class="text-xs text-outline leading-relaxed" data-i18n="netLogsDesc">
            实时捕获本客户端发往谷歌等上游服务的每一次底层 TCP/HTTPS 拨号细节，包括实际选用的代理端口、网络建连耗时以及建连结果状态。
        </p>
        <div class="overflow-x-auto w-full border border-outline-variant/20 rounded-lg max-h-[360px] overflow-y-auto">
            <table class="w-full text-left text-[11px] font-medium">
                <thead class="bg-slate-50 dark:bg-[#1a1f30] text-outline/80 border-b border-outline-variant/20 font-bold sticky top-0 z-10">
                    <tr>
                        <th class="py-2.5 px-3" data-i18n="netLogTime">时间</th>
                        <th class="py-2.5 px-3" data-i18n="netLogTarget">拨号目标 (Target)</th>
                        <th class="py-2.5 px-3" data-i18n="netLogRoute">出站路由 (Via Proxy)</th>
                        <th class="py-2.5 px-3 text-center" data-i18n="netLogDuration">建连耗时</th>
                        <th class="py-2.5 px-3" data-i18n="netLogStatus">状态 (Status)</th>
                    </tr>
                </thead>
                <tbody id="tblNetworkLogsBody" class="font-data-mono">
                    <tr>
                        <td colspan="5" class="py-6 text-center text-outline/60" data-i18n="netLogsEmpty">暂无连接记录，正在等待出站网络活动...</td>
                    </tr>
                </tbody>
            </table>
        </div>
    </div>
</div>

<!-- 关于面板 -->
<div class="hidden flex-col gap-6 w-full" id="settings-panel-about">
<div class="glass-card rounded-xl p-8 flex flex-col items-center text-center gap-4 max-w-2xl mx-auto w-full">
<!-- App Logo -->
<img class="w-16 h-16 rounded-2xl shadow-lg object-cover shadow-primary/20" src="/src/assets/appicon.png"/>
<h2 class="text-xl font-bold text-on-surface dark:text-white">Antigravity Proxy</h2>
<!-- Pill Buttons Row -->
<div class="flex flex-wrap justify-center gap-2 mt-1">
<span class="px-2.5 py-0.5 bg-slate-100 dark:bg-white/5 border border-outline-variant/30 text-outline rounded-full text-[11px] font-semibold font-data-mono flex items-center" id="lblCurrentVersion">v1.0.0</span>
<button class="px-2.5 py-0.5 bg-primary/10 hover:bg-primary/20 text-primary border border-primary/20 rounded-full text-[11px] font-semibold flex items-center gap-1 transition-all cursor-pointer" id="btnCheckUpdate">
<span class="material-symbols-outlined text-[12px] animate-none" id="iconCheckUpdate">sync</span>
<span data-i18n="btnCheckUpdate" id="lblBtnCheckUpdate">检查更新</span>
</button>
<button class="px-2.5 py-0.5 bg-slate-100 hover:bg-slate-200 dark:bg-white/5 dark:hover:bg-white/10 text-on-surface dark:text-white border border-outline-variant/30 rounded-full text-[11px] font-medium flex items-center gap-1 transition-all cursor-pointer" id="btnAboutChangelog">
<span class="material-symbols-outlined text-[12px]">description</span>
<span data-i18n="btnReleaseNotes">更新记录</span>
</button>
</div>
<p class="text-[12px] text-outline max-w-md mt-1" data-i18n="aboutDesc">基于大模型的智能代理与多账号调度流量管理解决方案</p>
<!-- 四宫格卡片布局 -->
<div class="grid grid-cols-1 sm:grid-cols-2 gap-4 w-full mt-4 border-t border-outline-variant/20 pt-6">
<!-- Card 1: 主作者 -->
<div class="glass-card hover:border-primary/30 hover:bg-primary/5 transition-all duration-300 rounded-xl p-4 flex flex-col items-center justify-center text-center gap-1.5 border border-outline-variant/20">
<span class="material-symbols-outlined text-primary/80 text-[20px]">person</span>
<span class="text-[12px] font-bold text-on-surface dark:text-white" data-i18n="aboutAuthorLabel">主作者</span>
<span class="text-[11px] text-outline font-data-mono">weilimao</span>
</div>
<!-- Card 2: 开源仓库 -->
<button class="glass-card hover:border-primary/30 hover:bg-primary/5 transition-all duration-300 rounded-xl p-4 flex flex-col items-center justify-center text-center gap-1.5 border border-outline-variant/20 cursor-pointer" id="btnAboutRepo">
<svg class="w-5 h-5 text-primary/80 fill-current" viewBox="0 0 24 24">
<path d="M12 0c-6.626 0-12 5.373-12 12 0 5.302 3.438 9.8 8.207 11.387.599.111.793-.261.793-.577v-2.234c-3.338.726-4.033-1.416-4.033-1.416-.546-1.387-1.333-1.756-1.333-1.756-1.089-.745.083-.729.083-.729 1.205.084 1.839 1.237 1.839 1.237 1.07 1.834 2.807 1.304 3.492.997.107-.775.418-1.305.762-1.604-2.665-.305-5.467-1.334-5.467-5.931 0-1.311.469-2.381 1.236-3.221-.124-.303-.535-1.524.117-3.176 0 0 1.008-.322 3.301 1.23.957-.266 1.983-.399 3.003-.404 1.02.005 2.047.138 3.006.404 2.291-1.552 3.297-1.23 3.297-1.23.653 1.653.242 2.874.118 3.176.77.84 1.235 1.911 1.235 3.221 0 4.609-2.807 5.624-5.479 5.921.43.372.823 1.102.823 2.222v3.293c0 .319.192.694.801.576 4.765-1.589 8.199-6.086 8.199-11.386 0-6.627-5.373-12-12-12z"></path>
</svg>
<span class="text-[12px] font-bold text-on-surface dark:text-white" data-i18n="aboutRepoLabel">开源仓库</span>
<span class="text-[11px] text-outline font-data-mono">antigravityProxyGo</span>
</button>
<!-- Card 3: 赞助支持 -->
<div class="glass-card hover:border-primary/30 hover:bg-primary/5 transition-all duration-300 rounded-xl p-4 flex flex-col items-center justify-center text-center gap-1.5 border border-outline-variant/20">
<span class="material-symbols-outlined text-primary/80 text-[20px]">favorite</span>
<span class="text-[12px] font-bold text-on-surface dark:text-white" data-i18n="aboutSponsorLabel">赞助支持</span>
<span class="text-[11px] text-outline" data-i18n="aboutSponsorDesc">支持项目持续开发</span>
</div>
<!-- Card 4: 意见反馈 -->
<button class="glass-card hover:border-primary/30 hover:bg-primary/5 transition-all duration-300 rounded-xl p-4 flex flex-col items-center justify-center text-center gap-1.5 border border-outline-variant/20 cursor-pointer" id="btnAboutFeedback">
<span class="material-symbols-outlined text-primary/80 text-[20px]">sms</span>
<span class="text-[12px] font-bold text-on-surface dark:text-white" data-i18n="aboutFeedbackLabel">意见反馈</span>
<span class="text-[11px] text-outline" data-i18n="aboutFeedbackDesc">报告问题或提交建议</span>
</button>
</div>
<!-- 版权声明 -->
<div class="text-[11px] text-slate-400/80 dark:text-slate-500/80 mt-6 font-medium select-none text-center">
                    Copyright © 2026 weilimao. All rights reserved.
                </div>
</div>
</div>
<!-- 中继服务器管理面板 -->
<div class="hidden flex-col gap-5 w-full" id="settings-panel-relay">
<!-- 启用中继服务器 -->
<div class="bg-white dark:bg-[#1e2538] rounded-xl border border-outline-variant/20 p-5">
<h3 class="text-[14px] font-bold text-on-surface dark:text-white mb-4 flex items-center gap-2">
<span class="material-symbols-outlined text-[18px] text-primary">dns</span>
<span data-i18n="relayServerTitle">中继服务器</span>
</h3>
<div class="flex items-center justify-between mb-4">
<div>
<div class="text-[13px] font-medium text-on-surface dark:text-white" data-i18n="relayEnableLabel">启用中继服务器</div>
<div class="text-[11px] text-outline/60" data-i18n="relayEnableDesc">开放端口供其他客户端远程连接使用</div>
</div>
<div class="flex items-center gap-3">
<input class="w-20 px-2 py-1 text-[12px] rounded-md border border-outline-variant/30 bg-white dark:bg-[#1a1f30] text-on-surface dark:text-white text-center" id="relayPortInput" placeholder="18444" type="text" value="18444">
<label class="relative inline-block w-10 h-5 cursor-pointer">
<input class="sr-only peer" id="chkRelayEnabled" type="checkbox">
<div class="w-10 h-5 bg-slate-300 dark:bg-slate-600 rounded-full peer-checked:bg-primary transition-colors"></div>
<div class="absolute left-0.5 top-0.5 w-4 h-4 bg-white rounded-full transition-transform peer-checked:translate-x-5 shadow-sm"></div>
</input></label>
</input></div>
</div>

<!-- 新增子 Tab 切换栏 -->
<div class="flex items-center gap-2 border-t border-outline-variant/10 pt-4 mt-4">
  <button id="btnRelaySubTabUsers" class="px-4 py-1.5 text-[12px] font-bold bg-primary/10 text-primary dark:bg-primary/20 rounded-lg cursor-pointer transition-all duration-200" data-i18n="relaySubTabUsers">中继用户</button>
  <button id="btnRelaySubTabPackages" class="px-4 py-1.5 text-[12px] font-medium text-slate-500 hover:text-slate-800 dark:text-slate-400 dark:hover:text-slate-200 rounded-lg cursor-pointer transition-all duration-200" data-i18n="relaySubTabPackages">限额套餐</button>
  <button id="btnRelaySubTabSecurity" class="px-4 py-1.5 text-[12px] font-medium text-slate-500 hover:text-slate-800 dark:text-slate-400 dark:hover:text-slate-200 rounded-lg cursor-pointer transition-all duration-200" data-i18n="relaySubTabSecurity">中继配置</button>
  <button id="btnRelaySubTabModelMapping" class="px-4 py-1.5 text-[12px] font-medium text-slate-500 hover:text-slate-800 dark:text-slate-400 dark:hover:text-slate-200 rounded-lg cursor-pointer transition-all duration-200" data-i18n="relaySubTabModelMapping">模型映射</button>
</div>
</div>

<!-- 中继用户面板 (默认显示) -->
<div id="relay-sub-panel-users" class="flex flex-col gap-6 w-full">
    <!-- 中继用户列表 -->
    <div class="bg-white dark:bg-[#1e2538] rounded-xl border border-outline-variant/20 p-5">
    <div class="flex items-center justify-between mb-4">
    <h3 class="text-[14px] font-bold text-on-surface dark:text-white flex items-center gap-2">
    <span class="material-symbols-outlined text-[18px] text-primary">group</span>
    <span data-i18n="relayUsersTitle">中继用户</span>
    </h3>
    <button class="flex items-center gap-1 text-[12px] font-medium text-primary hover:text-primary/80 transition-colors" id="btnAddRelayUser">
    <span class="material-symbols-outlined text-[16px]">person_add</span>
    <span data-i18n="relayAddUser">添加用户</span>
    </button>
    </div>
    <!-- 筛选与搜索条件 -->
    <div class="flex flex-wrap items-center gap-3 mb-4">
        <!-- 账户名搜索 -->
        <div class="relative flex-1 min-w-[200px]">
            <span class="absolute inset-y-0 left-0 pl-3 flex items-center pointer-events-none text-outline/50">
                <span class="material-symbols-outlined text-[18px]">search</span>
            </span>
            <input type="text" id="relayUserSearchInput" placeholder="按账户名搜索..." data-i18n-placeholder="relayUserSearchPlaceholder"
                class="w-full pl-9 pr-3 py-1.5 text-[12px] rounded-lg border border-outline-variant/30 bg-slate-50 dark:bg-white/5 text-on-surface dark:text-white focus:outline-none focus:border-primary/60" />
        </div>
        <!-- 套餐类型筛选 -->
        <div class="relative w-[180px]">
            <select id="relayUserPackageFilter" 
                class="w-full px-3 py-1.5 text-[12px] rounded-lg border border-outline-variant/30 bg-slate-50 dark:bg-white/5 text-on-surface dark:text-white focus:outline-none focus:border-primary/60 appearance-none cursor-pointer">
                <option value="all" data-i18n="relayUserFilterAll">所有套餐类型</option>
                <option value="unlimited" data-i18n="relayUserFilterUnlimited">无限制</option>
                <option value="custom" data-i18n="relayUserFilterCustom">自定义限额</option>
            </select>
            <span class="absolute inset-y-0 right-0 pr-3 flex items-center pointer-events-none text-outline/50">
                <span class="material-symbols-outlined text-[16px]">keyboard_arrow_down</span>
            </span>
        </div>
    </div>
    <div id="relayUsersList"></div>
    <!-- 分页控件 -->
    <div class="flex items-center justify-between border-t border-outline-variant/20 pt-4 mt-3">
        <span class="text-[11px] text-outline" id="relayUserPaginationInfo">显示第 0 - 0 个用户，共 0 个</span>
        <div class="flex items-center gap-1">
            <button id="btnRelayUserPrevPage" class="px-2.5 py-1 text-[11px] font-medium border border-outline-variant/30 rounded-md hover:bg-slate-50 dark:hover:bg-white/5 text-on-surface dark:text-white disabled:opacity-50 disabled:pointer-events-none flex items-center gap-0.5 cursor-pointer">
                <span class="material-symbols-outlined text-[14px]">chevron_left</span>
                <span data-i18n="btnPrevPage">上一页</span>
            </button>
            <span class="text-[11px] px-2 text-on-surface dark:text-white font-bold" id="relayUserCurrentPage">1</span>
            <button id="btnRelayUserNextPage" class="px-2.5 py-1 text-[11px] font-medium border border-outline-variant/30 rounded-md hover:bg-slate-50 dark:hover:bg-white/5 text-on-surface dark:text-white disabled:opacity-50 disabled:pointer-events-none flex items-center gap-0.5 cursor-pointer">
                <span data-i18n="btnNextPage">下一页</span>
                <span class="material-symbols-outlined text-[14px]">chevron_right</span>
            </button>
        </div>
    </div>
    </div>
</div>

<!-- 限额套餐面板 (默认隐藏) -->
<div id="relay-sub-panel-packages" class="flex flex-col gap-6 w-full hidden">
    <!-- 套餐模板管理 -->
    <div class="bg-white dark:bg-[#1e2538] rounded-xl border border-outline-variant/20 p-5">
    <div class="flex items-center justify-between mb-4">
    <h3 class="text-[14px] font-bold text-on-surface dark:text-white flex items-center gap-2">
    <span class="material-symbols-outlined text-[18px] text-primary">view_quilt</span>
    <span data-i18n="relayPackagesTitle">限额套餐模板</span>
    </h3>
    <button class="flex items-center gap-1 text-[12px] font-medium text-primary hover:text-primary/80 transition-colors" onclick="window._relayOpenPackageSettings('')">
    <span class="material-symbols-outlined text-[16px]">add</span>
    <span data-i18n="relayNewPackage">新建套餐</span>
    </button>
    </div>
    <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3" id="relayPackagesList"></div>
    </div>
</div>

<!-- 中继配置面板 (默认隐藏) -->
<div id="relay-sub-panel-security" class="flex flex-col gap-6 w-full hidden">
    <div class="bg-white dark:bg-[#1e2538] rounded-xl border border-outline-variant/20 p-5">
        <h3 class="text-[14px] font-bold text-on-surface dark:text-white mb-5 flex items-center gap-2">
            <span class="material-symbols-outlined text-[18px] text-primary">security</span>
            <span data-i18n="relaySecurityTitle">中继安全与防攻击配置</span>
        </h3>
        
        <div class="space-y-5">
            <!-- SSRF Switch -->
            <div class="flex items-center justify-between">
                <div>
                    <div class="text-[13px] font-medium text-on-surface dark:text-white" data-i18n="relaySSRFLabel">启用 SSRF 安全防护 (推荐)</div>
                    <div class="text-[11px] text-outline/60" data-i18n="relaySSRFDesc">拦截发往 127.0.0.1、localhost 以及局域网私有网段的恶意拨号穿透</div>
                </div>
                <label class="relative inline-block w-10 h-5 cursor-pointer">
                    <input class="sr-only peer" id="chkRelaySSRFBlock" type="checkbox">
                    <div class="w-10 h-5 bg-slate-300 dark:bg-slate-600 rounded-full peer-checked:bg-primary transition-colors"></div>
                    <div class="absolute left-0.5 top-0.5 w-4 h-4 bg-white rounded-full transition-transform peer-checked:translate-x-5 shadow-sm"></div>
                </label>
            </div>

            <!-- Port Block Switch -->
            <div class="flex items-center justify-between border-t border-outline-variant/10 pt-4">
                <div>
                    <div class="text-[13px] font-medium text-on-surface dark:text-white" data-i18n="relayPortBlockLabel">限制代理端口</div>
                    <div class="text-[11px] text-outline/60" data-i18n="relayPortBlockDesc">仅放行 80 (HTTP) 和 443 (HTTPS) 常用端口，防止将中继作为其他服务代理</div>
                </div>
                <label class="relative inline-block w-10 h-5 cursor-pointer">
                    <input class="sr-only peer" id="chkRelayPortBlock" type="checkbox">
                    <div class="w-10 h-5 bg-slate-300 dark:bg-slate-600 rounded-full peer-checked:bg-primary transition-colors"></div>
                    <div class="absolute left-0.5 top-0.5 w-4 h-4 bg-white rounded-full transition-transform peer-checked:translate-x-5 shadow-sm"></div>
                </label>
            </div>

            <!-- Domain Whitelist Switch -->
            <div class="flex items-center justify-between border-t border-outline-variant/10 pt-4">
                <div>
                    <div class="text-[13px] font-medium text-on-surface dark:text-white" data-i18n="relayDomainFilterLabel">启用目标域名白名单过滤</div>
                    <div class="text-[11px] text-outline/60" data-i18n="relayDomainFilterDesc">开启后中继服务器只代理列表内的域名流量，拦截并丢弃其余外部网站访问</div>
                </div>
                <label class="relative inline-block w-10 h-5 cursor-pointer">
                    <input class="sr-only peer" id="chkRelayDomainFilter" type="checkbox">
                    <div class="w-10 h-5 bg-slate-300 dark:bg-slate-600 rounded-full peer-checked:bg-primary transition-colors"></div>
                    <div class="absolute left-0.5 top-0.5 w-4 h-4 bg-white rounded-full transition-transform peer-checked:translate-x-5 shadow-sm"></div>
                </label>
            </div>

            <!-- Whitelist List Textarea -->
            <div class="border-t border-outline-variant/10 pt-4 flex flex-col gap-2">
                <div class="text-[13px] font-medium text-on-surface dark:text-white" data-i18n="relayDomainWhitelistLabel">代理域名白名单 (每行一个，支持通配符如 *.google.com)</div>
                <textarea id="txtRelayDomainWhitelist" rows="6" 
                    class="w-full p-3 text-[12px] font-mono rounded-lg border border-outline-variant/30 bg-slate-50 dark:bg-white/5 text-on-surface dark:text-white focus:outline-none focus:border-primary/60 placeholder-slate-400"
                    placeholder="输入允许的域名列表，例如：&#10;*.googleapis.com&#10;*.google.com&#10;*.anthropic.com" data-i18n-placeholder="relayDomainWhitelistPlaceholder"></textarea>
                <div class="flex justify-end mt-1">
                    <button id="btnSaveRelaySecurity" class="px-4 py-1.5 text-[12px] font-medium bg-primary text-white hover:bg-primary/90 rounded-lg cursor-pointer transition-colors flex items-center gap-1.5 shadow-sm">
                        <span class="material-symbols-outlined text-[16px]">save</span>
                        <span data-i18n="relayBtnSaveSecurity">保存配置</span>
                    </button>
                </div>
            </div>
        </div>
    </div>
</div>

<!-- 模型映射面板 (默认隐藏) -->
<div id="relay-sub-panel-modelmapping" class="flex flex-col gap-6 w-full hidden">
    <div class="bg-white dark:bg-[#1e2538] rounded-xl border border-outline-variant/20 p-5">
        <div class="flex items-center justify-between mb-5">
            <h3 class="text-[14px] font-bold text-on-surface dark:text-white flex items-center gap-2">
                <span class="material-symbols-outlined text-[18px] text-primary">alt_route</span>
                <span data-i18n="relayModelMappingTitle">自定义中继模型映射</span>
            </h3>
            <button class="flex items-center gap-1 text-[12px] font-medium text-primary hover:text-primary/80 transition-colors cursor-pointer" onclick="window._relayAddModelMapping()">
                <span class="material-symbols-outlined text-[16px]">add</span>
                <span data-i18n="relayAddMapping">添加映射</span>
            </button>
        </div>
        
        <div class="overflow-x-auto max-h-[400px] overflow-y-auto pr-1">
            <table class="w-full text-left text-[12px]">
                <thead>
                    <tr class="border-b border-outline-variant/25 text-outline/80">
                        <th class="py-2.5 font-bold pl-2" data-i18n="relayMappingClientModel">客户端请求模型 (Client Model)</th>
                        <th class="py-2.5 font-bold pl-2" data-i18n="relayMappingTargetModel">真实目标模型 (Target Model)</th>
                        <th class="py-2.5 font-bold text-center w-[120px]" data-i18n="relayMappingExpose">是否公开 (Expose)</th>
                        <th class="py-2.5 font-bold text-center w-[80px]" data-i18n="autoTriggerColAction">操作</th>
                    </tr>
                </thead>
                <tbody id="modelMappingTableBody">
                    <!-- 动态渲染映射行 -->
                </tbody>
            </table>
        </div>
        
        <div class="flex justify-end gap-3 mt-6 border-t border-outline-variant/20 pt-4">
            <button class="px-4 py-1.5 text-[12px] font-bold bg-primary text-white rounded-lg hover:bg-primary/90 transition-all duration-200 cursor-pointer shadow-md shadow-primary/20 flex items-center gap-1" onclick="window._relaySaveModelMapping()" id="btnSaveModelMapping">
                <span class="material-symbols-outlined text-[16px]">save</span>
                <span data-i18n="relaySaveMapping">保存映射配置</span>
            </button>
        </div>
    </div>
</div>
</div>
</div>
</template>

<script setup lang="ts">
import { onMounted } from 'vue';

onMounted(() => {
  // Logic from settings controller will go here
});
</script>
