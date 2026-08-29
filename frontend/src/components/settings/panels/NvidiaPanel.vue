<template>
<div class="hidden flex-col gap-6 w-full" id="settings-panel-nvidia">
<!-- NVIDIA 流式思考回译设置卡片 -->
<div class="glass-card rounded-xl p-6 flex flex-col gap-4">
<h2 class="text-[15px] font-bold text-on-surface dark:text-white flex items-center gap-2">
<span class="material-symbols-outlined text-primary text-[20px]">psychology</span>
<span data-i18n="nvidiaReasoningAsTextTitle">NVIDIA 流式思考回译</span>
</h2>
<p class="text-xs text-outline leading-relaxed" data-i18n="nvidiaReasoningAsTextTip">
仅作用于 NVIDIA 上游链路的流式回译：开启后，将上游思维链 (Reasoning Content) 直接作为文本流逐字打屏输出，避免 CLI 客户端界面默认收起折叠。
</p>
<div class="flex items-center justify-between border-t border-outline-variant/20 pt-4 mt-2">
<div class="flex flex-col gap-0.5">
<span class="text-[13px] font-bold text-on-surface dark:text-white" data-i18n="reasoningAsTextLabel">思考过程直吐正文 (打字机模式)</span>
<span class="text-[11px] text-outline text-wrap max-w-[80%]" data-i18n="reasoningAsTextDesc">开启后，将上游思维链 (Reasoning Content) 直接作为文本流逐字打屏输出，避免 CLI 客户端界面默认收起折叠。</span>
</div>
<!-- Toggle Switch -->
<label class="relative inline-flex items-center cursor-pointer">
<input class="sr-only peer" id="chkReasoningAsText" type="checkbox"/>
<div class="w-11 h-6 bg-slate-200 dark:bg-white/10 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full rtl:peer-checked:after:-translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:start-[2px] after:bg-white after:border-gray-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all dark:border-gray-600 peer-checked:bg-primary"></div>
</label>
</div>
</div>

<!-- Debugger 调试模式与日志落盘卡片 (NVIDIA 入站专属落盘) -->
<div class="glass-card rounded-xl p-6 flex flex-col gap-4">
<h2 class="text-[15px] font-bold text-on-surface dark:text-white flex items-center gap-2">
<span class="material-symbols-outlined text-primary text-[20px]">bug_report</span>
<span data-i18n="debuggerModeCardTitle">Debugger 调试模式与全量请求日志落盘</span>
</h2>
<p class="text-xs text-outline leading-relaxed" data-i18n="debuggerModeCardTip">
开启后，系统将把每一笔中继请求的 Headers、Body、上游地址、状态码以及原始 SSE 流逐帧毫秒级实时记录存储到指定日志文件中，便于精准诊断断流与异常排查。
</p>
<div class="flex items-center justify-between border-t border-outline-variant/20 pt-4 mt-2">
<div class="flex flex-col gap-0.5">
<span class="text-[13px] font-bold text-on-surface dark:text-white" data-i18n="enableDebuggerModeLabel">启用 Debugger 调试模式</span>
<span class="text-[11px] text-outline text-wrap max-w-[80%]" data-i18n="enableDebuggerModeDesc">开启后实时将中继与上游交互的全量原始字节与 SSE 帧落盘到指定的调试日志文件夹中（默认关闭）。</span>
</div>
<!-- Toggle Switch -->
<label class="relative inline-flex items-center cursor-pointer">
<input class="sr-only peer" id="chkEnableDebuggerMode" type="checkbox"/>
<div class="w-11 h-6 bg-slate-200 dark:bg-white/10 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full rtl:peer-checked:after:-translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:start-[2px] after:bg-white after:border-gray-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all dark:border-gray-600 peer-checked:bg-primary"></div>
</label>
</div>
<div class="flex flex-col gap-2 mt-2 pt-3 border-t border-outline-variant/10">
<label class="text-[12px] font-bold text-outline" data-i18n="debuggerLogPathLabel">调试日志保存目录</label>
<div class="flex gap-2">
<input class="flex-grow px-3 py-2 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:outline-none text-on-surface dark:text-white" id="txtDebuggerLogPath" type="text" placeholder="logs/debugger" data-i18n-placeholder="debuggerLogPathPlaceholder">
<button class="px-4 py-2 bg-primary text-white hover:bg-primary/90 rounded-md text-[13px] font-bold transition-colors shadow-sm flex items-center gap-1.5 cursor-pointer" id="btnBrowseDebuggerDir">
<span class="material-symbols-outlined text-[16px]">folder</span>
<span data-i18n="btnBrowseDebuggerDir">更改目录</span>
</button>
</div>
</div>
</div>

<!-- NVIDIA 断流兜底出站代理卡片 (仅 NVIDIA Anthropic 流式重试耗尽后切此代理再试 1 轮) -->
<div class="glass-card rounded-xl p-6 flex flex-col gap-4">
<h2 class="text-[15px] font-bold text-on-surface dark:text-white flex items-center gap-2">
<span class="material-symbols-outlined text-primary text-[20px]">dns</span>
<span data-i18n="nvidiaFallbackProxyTitle">NVIDIA 断流兜底出站代理</span>
</h2>
<p class="text-xs text-outline leading-relaxed" data-i18n="nvidiaFallbackProxyTip">
仅 NVIDIA 上游链路：直连 5s×5 蓄流重试全部耗尽后，换此代理再试 1 轮（单次请求级，不记忆，不换号）。系统代理与虚拟网卡优先，仅当它们都不通才走此兜底。配置独立于「参数配置」中的专属 SOCKS5。
</p>
<div class="flex flex-col gap-3 border-t border-outline-variant/20 pt-4 mt-2">
<div class="flex items-center justify-between">
<div class="flex flex-col gap-0.5">
<label class="text-[13px] font-bold text-on-surface dark:text-white" data-i18n="fallbackProxyEnabledLabel">启用 NVIDIA 断流兜底出站代理</label>
<span class="text-[11px] text-outline text-wrap max-w-[80%]" data-i18n="fallbackProxyEnabledDesc">仅 NVIDIA 上游链路：直连 5s×5 蓄流重试全部耗尽后，换此代理再试 1 轮（单次请求级，不记忆，不换号）。系统代理与虚拟网卡优先，仅当它们都不通才走此兜底。配置独立于上方专属 SOCKS5。</span>
</div>
<label class="relative inline-flex items-center cursor-pointer">
<input class="sr-only peer" id="chkFallbackProxyEnabled" type="checkbox"/>
<div class="w-11 h-6 bg-slate-200 dark:bg-white/10 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full rtl:peer-checked:after:-translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:start-[2px] after:bg-white after:border-gray-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all dark:border-gray-600 peer-checked:bg-primary"></div>
</label>
</div>
<div class="flex flex-col gap-3 mt-1" id="divFallbackProxyAddress">
<div class="flex flex-col gap-1.5">
<label class="text-[12px] font-bold text-outline" data-i18n="fallbackProxyAddressLabel">兜底代理地址 (协议必须显式配置，如 http:// 或 socks5://)</label>
<input class="px-3 py-2 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:outline-none text-on-surface dark:text-white" id="txtFallbackProxyAddress" placeholder="例如：http://127.0.0.1:8080 或 socks5://127.0.0.1:1080" data-i18n-placeholder="fallbackProxyAddressPlaceholder" type="text"/>
</div>
<div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
<div class="flex flex-col gap-1.5">
<label class="text-[12px] font-bold text-outline" data-i18n="fallbackProxyUsernameLabel">兜底代理用户名 (可选)</label>
<input class="px-3 py-2 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:outline-none text-on-surface dark:text-white" id="txtFallbackProxyUsername" placeholder="无" data-i18n-placeholder="optionalPlaceholder" type="text"/>
</div>
<div class="flex flex-col gap-1.5">
<label class="text-[12px] font-bold text-outline" data-i18n="fallbackProxyPasswordLabel">兜底代理密码 (可选)</label>
<PasswordInput inputId="txtFallbackProxyPassword" placeholder="无" dataI18nPlaceholder="optionalPlaceholder" inputClass="w-full px-3 py-2 pr-9 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:outline-none text-on-surface dark:text-white"/>
</div>
</div>
</div>
</div>
</div>

<!-- NVIDIA 对冲请求卡片 (可选,默认关闭;首帧排队尾部延迟的对冲杠杆,败方取消不记故障) -->
<div class="glass-card rounded-xl p-6 flex flex-col gap-4">
<h2 class="text-[15px] font-bold text-on-surface dark:text-white flex items-center gap-2">
<span class="material-symbols-outlined text-primary text-[20px]">bolt</span>
<span data-i18n="nvidiaHedgeTitle">NVIDIA 对冲请求 (首帧加速)</span>
</h2>
<p class="text-xs text-outline leading-relaxed" data-i18n="nvidiaHedgeTip">
仅作用于 NVIDIA 上游链路：首个上游请求发出后，若在设定毫秒数内未收到响应头，立即用号池中另一账号并发补发一份相同请求，谁先回响应头用谁，败方自动取消（不记故障、不冷却账号）。可显著压缩偶发的几十秒级首帧排队，但败方的上游预填充算力会浪费——缓存未命中场景上游计费可能翻倍，请按需开启。
</p>
<div class="flex flex-col gap-3 border-t border-outline-variant/20 pt-4 mt-2">
<div class="flex items-center justify-between">
<div class="flex flex-col gap-0.5">
<label class="text-[13px] font-bold text-on-surface dark:text-white" data-i18n="nvidiaHedgeEnableLabel">启用 NVIDIA 对冲请求</label>
<span class="text-[11px] text-outline text-wrap max-w-[80%]" data-i18n="nvidiaHedgeEnableDesc">默认关闭。开启后主请求超过下方毫秒数未回响应头时，以另一账号并发补发对冲请求（仅每账号首个请求生效，重试轮不放大）。</span>
</div>
<label class="relative inline-flex items-center cursor-pointer">
<input class="sr-only peer" id="chkNvidiaHedgeEnabled" type="checkbox"/>
<div class="w-11 h-6 bg-slate-200 dark:bg-white/10 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full rtl:peer-checked:after:-translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:start-[2px] after:bg-white after:border-gray-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all dark:border-gray-600 peer-checked:bg-primary"></div>
</label>
</div>
<div class="flex flex-col gap-1.5 mt-1" id="divNvidiaHedgeDelay" style="display: none;">
<label class="text-[12px] font-bold text-outline" data-i18n="nvidiaHedgeDelayLabel">对冲触发延迟 (毫秒，范围 2000-60000，默认 10000)</label>
<input class="px-3 py-2 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:outline-none text-on-surface dark:text-white font-mono" id="txtNvidiaHedgeDelayMs" type="number" min="2000" max="60000" step="500" placeholder="10000"/>
<span class="text-[11px] text-outline" data-i18n="nvidiaHedgeDelayTip">越小对冲越激进（更费上游算力），越大越保守（尾部延迟省得少）。建议 8000-15000。</span>
<label class="text-[12px] font-bold text-outline mt-1" data-i18n="nvidiaHedgeMaxParallelLabel">对冲并发请求总数 (含主请求，默认 2，上限跟随号池规模)</label>
<input class="px-3 py-2 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:outline-none text-on-surface dark:text-white font-mono" id="txtNvidiaHedgeMaxParallel" type="number" min="2" max="2" step="1" placeholder="2"/>
<span class="text-[11px] text-outline"><span data-i18n="nvidiaHedgeMaxParallelTip">到达阈值后同时用不同账号补发「并发数-1」份相同请求。最坏情况上游计费 ≈ 并发数 × 单请求预填成本；号池可用账号不足时自动按实际数降级。当前号池可并发上限：</span><b id="lblNvidiaHedgeMaxPool" class="text-primary">-</b></span>
<div class="flex items-center justify-between mt-2">
<div class="flex flex-col gap-0.5 max-w-[80%]">
<label class="text-[12px] font-bold text-outline" data-i18n="nvidiaHedgeImmediateLabel">同时轰出竞赛 (不等延迟，全部请求同刻出发)</label>
<span class="text-[11px] text-outline" data-i18n="nvidiaHedgeImmediateDesc">勾选后不再等待上方触发延迟：主请求与全部对冲同刻发出竞赛，先回响应头者胜，败方立即取消。注意：每次请求上游计费恒 ≈ 并发数 × 单请求预填成本（如并发 5 即单请求的 5 倍），仅在追求极致首帧时开启。</span>
</div>
<label class="relative inline-flex items-center cursor-pointer shrink-0">
<input class="sr-only peer" id="chkNvidiaHedgeImmediate" type="checkbox"/>
<div class="w-11 h-6 bg-slate-200 dark:bg-white/10 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full rtl:peer-checked:after:-translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:start-[2px] after:bg-white after:border-gray-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all dark:border-gray-600 peer-checked:bg-primary"></div>
</label>
</div>
</div>
</div>
</div>

<!-- NVIDIA 专属出站代理 (支持 SOCKS5/HTTP，可指定本地 Clash 独立端口实现单号池极速分流) -->
<div class="glass-card rounded-xl p-6 flex flex-col gap-4">
<h2 class="text-[15px] font-bold text-on-surface dark:text-white flex items-center gap-2">
<span class="material-symbols-outlined text-primary text-[20px]">lan</span>
<span data-i18n="nvidiaDedicatedProxyTitle">NVIDIA 专属出站代理 (支持 SOCKS5/HTTP)</span>
</h2>
<p class="text-xs text-outline leading-relaxed" data-i18n="nvidiaDedicatedProxyTip">
为 NVIDIA 号池单独配置独立的出站代理（如 Clash 独立端口 socks5://127.0.0.1:7891 或专属 VPS 代理），直接走机场专线享受 1.1s 极速响应，而其他号池继续走默认住宅 IP，实现号池级精准物理分流。
</p>
<div class="flex flex-col gap-3 border-t border-outline-variant/20 pt-4 mt-2">
<div class="flex items-center justify-between">
<div class="flex flex-col gap-0.5">
<label class="text-[13px] font-bold text-on-surface dark:text-white" data-i18n="nvidiaDedicatedProxyEnableLabel">启用 NVIDIA 专属出站代理</label>
<span class="text-[11px] text-outline text-wrap max-w-[80%]" data-i18n="nvidiaDedicatedProxyEnableDesc">开启后 NVIDIA 请求将强行且仅通过此代理访问，不走全局系统代理与 TUN 虚拟网卡。</span>
</div>
<label class="relative inline-flex items-center cursor-pointer">
<input class="sr-only peer" id="chkNvidiaDedicatedProxyEnabled" type="checkbox"/>
<div class="w-11 h-6 bg-slate-200 dark:bg-white/10 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full rtl:peer-checked:after:-translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:start-[2px] after:bg-white after:border-gray-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all dark:border-gray-600 peer-checked:bg-primary"></div>
</label>
</div>
<div class="flex flex-col gap-3 mt-1" id="divNvidiaDedicatedProxyAddress" style="display: none;">
<div class="flex flex-col gap-1.5">
<label class="text-[12px] font-bold text-outline" data-i18n="nvidiaDedicatedProxyAddressLabel">专属代理地址 (协议必须显式配置，如 socks5:// 或 http://)</label>
<input class="px-3 py-2 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:outline-none text-on-surface dark:text-white font-mono" id="txtNvidiaDedicatedProxyAddress" placeholder="例如：socks5://127.0.0.1:7891 或 http://127.0.0.1:7890" data-i18n-placeholder="nvidiaDedicatedProxyAddressPlaceholder" type="text"/>
</div>
<div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
<div class="flex flex-col gap-1.5">
<label class="text-[12px] font-bold text-outline" data-i18n="nvidiaDedicatedProxyUsernameLabel">专属代理用户名 (可选)</label>
<input class="px-3 py-2 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:outline-none text-on-surface dark:text-white" id="txtNvidiaDedicatedProxyUsername" placeholder="无" data-i18n-placeholder="optionalPlaceholder" type="text"/>
</div>
<div class="flex flex-col gap-1.5">
<label class="text-[12px] font-bold text-outline" data-i18n="nvidiaDedicatedProxyPasswordLabel">专属代理密码 (可选)</label>
<PasswordInput inputId="txtNvidiaDedicatedProxyPassword" placeholder="无" dataI18nPlaceholder="optionalPlaceholder" inputClass="w-full px-3 py-2 pr-9 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:outline-none text-on-surface dark:text-white"/>
</div>
</div>
</div>
</div>
</div>

<!-- NVIDIA Cloudflare 代理出口 Worker 卡片 (通过 Anycast 边缘节点打散出口，避免单 IP 触发 429) -->
<div class="glass-card rounded-xl p-6 flex flex-col gap-4">
<h2 class="text-[15px] font-bold text-on-surface dark:text-white flex items-center gap-2">
<span class="material-symbols-outlined text-amber-500 text-[20px]">cloud</span>
<span data-i18n="nvidiaWorkerProxyTitle">Cloudflare 通用代理出口 Worker</span>
</h2>
<p class="text-xs text-outline leading-relaxed" data-i18n="nvidiaWorkerProxyEnableDesc">
将请求转发至 Cloudflare Worker，通过 Anycast 边缘 IP 轮换打散出口，避免单 IP 突发流量触发 429 拦截。通用 Worker 脚本适用于所有号池（NVIDIA / Other 组），无需为不同上游分别部署。
</p>
<div class="flex flex-col gap-3 border-t border-outline-variant/20 pt-4 mt-2">
<div class="flex items-center justify-between">
<div class="flex flex-col gap-0.5">
<label class="text-[13px] font-bold text-on-surface dark:text-white" data-i18n="nvidiaWorkerProxyEnableLabel">启用 Cloudflare 边缘代理出口</label>
<span class="text-[11px] text-outline text-wrap max-w-[80%]" data-i18n="nvidiaWorkerProxyEnableDesc">将 NVIDIA 请求转发至 Cloudflare Worker，通过 Anycast 边缘 IP 轮换打散出口，避免触发单 IP 429 限流</span>
</div>
<label class="relative inline-flex items-center cursor-pointer">
<input class="sr-only peer" id="chkNvidiaWorkerProxyEnabled" type="checkbox"/>
<div class="w-11 h-6 bg-slate-200 dark:bg-white/10 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full rtl:peer-checked:after:-translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:start-[2px] after:bg-white after:border-gray-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all dark:border-gray-600 peer-checked:bg-primary"></div>
</label>
</div>
<div class="flex flex-col gap-2 mt-1" id="divNvidiaWorkerProxyUrl" style="display: none;">
<label class="text-[12px] font-bold text-outline" data-i18n="nvidiaWorkerProxyUrlLabel">Cloudflare Worker 地址 (URL)</label>
<input class="px-3 py-2 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:outline-none text-on-surface dark:text-white font-mono" id="txtNvidiaWorkerProxyUrl" placeholder="https://your-worker.workers.dev" data-i18n-placeholder="nvidiaWorkerProxyUrlPlaceholder" type="text"/>
<span class="text-[11px] text-outline" data-i18n="nvidiaWorkerProxyUrlTip">填写部署好的 Cloudflare Worker 地址。若账号配置了专属出口 IP，将通过 X-Egress-IP 请求头一并透传给 Worker。</span>
</div>

<!-- 部署教程与 Worker 脚本展示 -->
<div class="border-t border-outline-variant/10 pt-4 mt-2 flex flex-col gap-3">
<div class="flex items-center justify-between">
<div class="flex items-center gap-1.5">
<span class="material-symbols-outlined text-[18px] text-primary">menu_book</span>
<span class="text-[13px] font-bold text-on-surface dark:text-white">Cloudflare Worker 部署指南与脚本</span>
</div>
</div>

<p class="text-[12px] text-outline leading-relaxed">
当您使用多个账号高频请求上游时，官方网关可能会因<b>单 IP 短期内吞吐过大</b>触发 429 拦截。通过部署 Cloudflare Worker 作为代理出口，可利用 Cloudflare 全球 Anycast 边缘出站 IP 自动打散流量，并支持账号级独立伪装住宅 IP。<b>此通用 Worker 脚本适用于所有号池</b>——NVIDIA 号池在上方开启即可，Other 号池在组 Tab 内开启即可，无需为不同上游分别部署。
</p>

<!-- Worker 脚本代码编辑器 (带语法着色、行号、原位编辑与一键复制) -->
<div class="flex flex-col gap-1.5">
<div class="text-[11px] font-bold text-on-surface dark:text-white flex items-center gap-1.5">
  <span class="material-symbols-outlined text-[16px] text-amber-500">code</span>
  <span>Worker 脚本代码（通用版，支持任意号池上游、流式 SSE、双工零缓冲边收边发与 X-Egress-IP 伪装，可直接编辑与一键复制）：</span>
</div>
<CodeEditor
  v-model="workerScript"
  :default-code="defaultWorkerScriptCode"
  file-name="universal-worker-egress.js"
  language="JavaScript"
/>
</div>

<div class="text-[12px] text-outline leading-relaxed flex flex-col gap-1">
<div class="font-bold text-on-surface dark:text-white">使用步骤：</div>
<div>1. 登录 <a href="https://dash.cloudflare.com" target="_blank" class="text-primary hover:underline">Cloudflare Dashboard</a> &rarr; <b>Workers &amp; Pages</b> &rarr; 点击 <b>Create application</b> &rarr; 创建 Worker。</div>
<div>2. 点击 <b>Quick edit</b>，粘贴上方一键复制的代码并点击 <b>Save and deploy</b>。</div>
<div>3. 复制生成的 Worker 地址（如 <code>https://your-worker.workers.dev</code>），粘贴到上方「Cloudflare Worker 地址」输入框并开启。</div>
<div>4. NVIDIA 号池：在此面板开启即可。Other 号池：进入「账号池 &rarr; Other」选中具体组 Tab，在工具栏开启「Worker 出口」并填写同一 Worker 地址。</div>
<div>5. （可选）在「账号池 &rarr; NVIDIA」点击账号编辑，即可查看到已为各账号分配的专属住宅伪装 IP。</div>
</div>
</div>

</div>
</div>
</div>
</template>

<script setup lang="ts">
import { ref } from 'vue';
import CodeEditor from '../../common/CodeEditor.vue';
import PasswordInput from '../../modals/PasswordInput.vue';

const defaultWorkerScriptCode = `export default {
  async fetch(request, env, ctx) {
    const url = new URL(request.url);

    // 1. 健康检查与 CORS 预检
    if (url.pathname === "/health") {
      return new Response(JSON.stringify({ status: "ok", edge: "cloudflare-worker" }), {
        headers: { "content-type": "application/json" }
      });
    }
    if (request.method === "OPTIONS") {
      return new Response(null, {
        status: 204,
        headers: {
          "Access-Control-Allow-Origin": "*",
          "Access-Control-Allow-Methods": "GET, POST, PUT, DELETE, OPTIONS",
          "Access-Control-Allow-Headers": "*"
        }
      });
    }

    // 2. 从 X-Target-Upstream 头获取真正的上游地址（通用化核心）
    //    号池中继层在启用 Worker 代理出口时注入此头，Worker 据此转发到真正上游。
    //    未携带此头的请求回退到 path 推断（兼容旧客户端直连 Worker 的场景）。
    const targetUpstream = request.headers.get("x-target-upstream");
    let targetOrigin;
    if (targetUpstream && targetUpstream.trim() !== "") {
      // 显式上游：拼接 X-Target-Upstream + 原始 path + query
      const upstream = new URL(targetUpstream.trim());
      targetOrigin = upstream.origin;
    } else {
      // 无显式上游头时回退：直接用请求 path 作为上游（兼容直连场景）
      targetOrigin = url.origin;
    }
    const targetUrl = targetOrigin + url.pathname + url.search;

    // 3. 构造出站 Headers（修正 Host 并注入专属伪装 IP，剔除逐跳头）
    const newHeaders = new Headers(request.headers);
    const targetHost = new URL(targetOrigin).host;
    newHeaders.set("host", targetHost);

    const egressIP = request.headers.get("x-egress-ip");
    if (egressIP && egressIP.trim() !== "") {
      const cleanIP = egressIP.trim();
      newHeaders.set("cf-connecting-ip", cleanIP);
      newHeaders.set("x-real-ip", cleanIP);
      newHeaders.set("x-forwarded-for", cleanIP);
    } else {
      newHeaders.delete("cf-connecting-ip");
      newHeaders.delete("x-real-ip");
      newHeaders.delete("x-forwarded-for");
    }
    // 剔除自定义控制头，不透传给上游
    newHeaders.delete("x-egress-ip");
    newHeaders.delete("x-target-upstream");

    // 4. 双工流零拷贝转发 (Zero-Buffer Duplex Pipeline) 与异步惰性重试
    let primaryBody = null;
    let retryBufferPromise = null;

    if (request.body && request.method !== "GET" && request.method !== "HEAD") {
      const [stream1, stream2] = request.body.tee();
      primaryBody = stream1;
      // 备用流异步克隆至内存备用，完全不阻塞主流向上游边推边发
      retryBufferPromise = new Response(stream2).arrayBuffer();
    }

    // 极速首发直通通道 (Fast-Path)
    let response;
    try {
      response = await fetch(targetUrl, {
        method: request.method,
        headers: newHeaders,
        body: primaryBody,
        duplex: "half",
        keepalive: true,
        cf: {
          cacheEverything: false,
          cacheTtl: 0
        }
      });
    } catch (e) {
      response = null;
    }

    // 仅在首发遇到 429 或 5xx 异常时，才提取已就绪的 retryBuffer 进行快速重试
    if (!response || [429, 500, 502, 503, 504].includes(response.status)) {
      const fallbackBytes = retryBufferPromise ? await retryBufferPromise : null;
      for (let i = 0; i < 2; i++) {
        await new Promise(r => setTimeout(r, 100 * (i + 1)));
        try {
          response = await fetch(targetUrl, {
            method: request.method,
            headers: newHeaders,
            body: fallbackBytes,
            redirect: "follow",
            keepalive: true,
            cf: {
              cacheEverything: false,
              cacheTtl: 0
            }
          });
          if (![429, 500, 502, 503, 504].includes(response.status)) {
            break;
          }
        } catch (err) {}
      }
    }

    if (!response) {
      return new Response(JSON.stringify({ error: "Upstream connection failed" }), {
        status: 502,
        headers: { "content-type": "application/json", "access-control-allow-origin": "*" }
      });
    }

    // 5. 构造下游响应（透传 SSE 流式传输，禁用 Cloudflare 边缘缓存与压缩缓冲）
    const respHeaders = new Headers(response.headers);
    respHeaders.set("access-control-allow-origin", "*");
    respHeaders.set("cache-control", "no-cache, no-transform");
    respHeaders.set("x-accel-buffering", "no");
    respHeaders.delete("content-length");

    return new Response(response.body, {
      status: response.status,
      headers: respHeaders
    });
  }
};`;

const workerScript = ref(defaultWorkerScriptCode);
</script>
