<template>
<div class="hidden flex-col gap-6 w-full" id="settings-panel-network">
    <!-- 实时网络状态卡片 -->
    <div class="glass-card rounded-xl p-6 flex flex-col gap-4">
        <h2 class="text-[15px] font-bold text-on-surface dark:text-white flex items-center gap-2">
            <span class="material-symbols-outlined text-primary text-[20px]">lan</span>
            <span data-i18n="netStatusTitle">当前本地代理状态</span>
        </h2>
        <div class="grid grid-cols-1 md:grid-cols-4 gap-4 mt-2">
            <div class="bg-slate-50 dark:bg-white/5 p-4 rounded-lg border border-outline-variant/20 flex flex-col gap-1">
                <span class="text-[11px] text-outline font-bold" data-i18n="netStatusFallbackLabel">Fallback 探测代理</span>
                <span class="text-[13px] font-mono font-bold text-on-surface dark:text-white" id="lblNetStatusFallback" data-i18n="netStatusDetecting">正在检测...</span>
            </div>
            <div class="bg-slate-50 dark:bg-white/5 p-4 rounded-lg border border-outline-variant/20 flex flex-col gap-1">
                <span class="text-[11px] text-outline font-bold" data-i18n="netStatusCustomSocksLabel">专属 SOCKS5 代理</span>
                <span class="text-[13px] font-mono font-bold text-on-surface dark:text-white" id="lblNetStatusCustomSocks" data-i18n="netStatusDisabled">未启用</span>
            </div>
            <div class="bg-slate-50 dark:bg-white/5 p-4 rounded-lg border border-outline-variant/20 flex flex-col gap-1">
                <span class="text-[11px] text-outline font-bold" data-i18n="netStatusFallbackProxyLabel">NVIDIA 兜底代理</span>
                <span class="text-[13px] font-mono font-bold text-on-surface dark:text-white" id="lblNetStatusFallbackProxy" data-i18n="netStatusDisabled">未启用</span>
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
</template>

<script setup lang="ts">
// NetworkPanel: 从 Settings.vue 提取的纯展示面板。
// 保留所有 id / data-i18n / onclick / class 属性，
// 使 settingsController / relayController 的 getElementById 与 classList 操作零改动。
</script>
