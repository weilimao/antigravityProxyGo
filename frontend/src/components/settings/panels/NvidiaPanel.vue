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
</div>
</template>

<script setup lang="ts">
// NvidiaPanel: 从 Settings.vue 提取的纯展示面板。
// 保留所有 id / data-i18n / onclick / class 属性，
// 使 settingsController / relayController 的 getElementById 与 classList 操作零改动。
</script>
