<template>
<div class="flex flex-col gap-5 w-full" id="view-otp">
<div class="flex flex-col lg:flex-row lg:justify-between lg:items-end gap-4">
<div>
<h1 class="text-2xl font-bold text-on-surface dark:text-white" data-i18n="otpTitle">2FA 验证码管理</h1>
<p class="text-xs text-outline dark:text-outline-variant" data-i18n="otpDesc">为账号配置谷歌两步验证 (2FA/TOTP) 密钥，实时生成并自动刷新动态验证码</p>
</div>
<div class="flex flex-wrap items-center gap-3 w-full lg:w-auto">
<button class="flex items-center gap-1.5 px-4 py-1.5 bg-primary text-white hover:bg-primary/90 rounded-md text-[13px] font-bold transition-colors shadow-sm cursor-pointer whitespace-nowrap flex-shrink-0" id="btnAddNewOtp">
<span class="material-symbols-outlined text-[16px]">add_circle</span>
                    <span data-i18n="btnAddOtpAccount">新增 2FA 账号</span>
                </button>
<div class="flex items-center gap-3 bg-slate-50/50 dark:bg-white/5 px-3.5 py-1.5 rounded-lg border border-outline-variant/30 text-[13px] text-on-surface dark:text-white whitespace-nowrap flex-shrink-0">
<span class="material-symbols-outlined text-[16px] text-primary dark:text-primary-fixed-dim animate-spin hidden" id="otpRefreshSpinner">sync</span>
<span class="font-medium" data-i18n="otpCountdownLabel">验证码更新倒计时:</span>
<span class="font-bold text-primary dark:text-primary-fixed-dim w-6 text-center" id="otpCountdown">-</span><span data-i18n="otpSeconds">秒</span>
                </div>
</div>
</div>
    <!-- 2FA 密钥即时查询 (不保存) -->
    <div class="glass-card rounded-xl p-4 flex flex-col gap-3">
        <div class="flex items-center gap-2 text-primary dark:text-primary-fixed-dim">
            <span class="material-symbols-outlined text-[18px]">bolt</span>
            <span class="text-[13px] font-bold text-outline dark:text-outline-variant" data-i18n="otpInstantQuery">2FA 密钥即时查询 (不保存)</span>
        </div>
        <div class="flex flex-col md:flex-row gap-3 items-stretch md:items-center">
            <div class="flex-1 relative">
                <input type="text" id="instantSecretInput" data-i18n-placeholder="otpInstantPlaceholder" placeholder="直接在此输入32位(或其它位数)2FA密钥，即时计算验证码..." class="w-full pl-9 pr-4 py-2.5 text-[13px] bg-slate-50 dark:bg-white/5 border border-outline-variant/30 rounded-xl focus:outline-none focus:border-primary focus:ring-1 focus:ring-primary text-on-surface dark:text-white placeholder-outline/40 font-mono" />
                <span class="material-symbols-outlined absolute left-3 top-3 text-[16px] text-outline/50">vpn_key</span>
                <span id="instantSecretStatus" class="absolute right-3 top-3 text-[12px] font-bold text-red-500 hidden" data-i18n="otpInvalidKey">密钥格式无效</span>
            </div>
            <div id="instantOtpResultContainer" class="hidden items-center gap-3 bg-emerald-500/10 dark:bg-[#10b981]/10 border border-emerald-500/20 dark:border-[#10b981]/20 rounded-xl px-4 py-2 select-none">
                <span id="instantOtpCode" class="text-[18px] font-bold tracking-widest font-mono text-emerald-600 dark:text-emerald-400">------</span>
                <span id="instantOtpCountdown" class="text-[11px] text-emerald-600/70 dark:text-emerald-400/70 font-mono">(0s)</span>
                <button id="btnCopyInstantOtp" class="material-symbols-outlined text-[16px] text-emerald-600 hover:text-emerald-700 dark:text-emerald-400 dark:hover:text-emerald-300 cursor-pointer transition-colors" data-i18n-title="otpCopyTip" title="复制验证码">content_copy</button>
            </div>
            <div id="instantOtpError" class="hidden text-[12px] text-red-500 bg-red-500/10 px-4 py-2.5 rounded-xl border border-red-500/20" data-i18n="otpInvalidKey">
                密钥格式无效
            </div>
        </div>
    </div>

    <div class="glass-card rounded-xl flex flex-col flex-1 min-h-[300px]">
        <div class="p-4 border-b border-outline-variant/30 flex flex-col sm:flex-row justify-between items-start sm:items-center gap-3 bg-slate-50/50 dark:bg-white/5 rounded-t-xl">
            <div class="flex items-center gap-3 w-full sm:w-auto">
                <span class="text-[13px] font-bold text-outline dark:text-outline-variant whitespace-nowrap" data-i18n="otpListHeader">账号 2FA 列表</span>
                <span class="text-[12px] font-medium text-primary dark:text-primary-fixed-dim bg-primary/10 px-2 py-0.5 rounded-md" id="otpCountBadge">共 0 个账号</span>
            </div>
            <div class="flex items-center gap-2 w-full sm:w-[260px] relative">
                <input type="text" id="otpSearchInput" data-i18n-placeholder="otpSearchPlaceholder" placeholder="按邮箱筛选..." class="w-full pl-8 pr-3 py-1.5 bg-slate-100 dark:bg-white/5 border border-outline-variant/30 rounded-lg focus:outline-none focus:border-primary focus:ring-1 focus:ring-primary text-[12px] text-on-surface dark:text-white placeholder-outline/50" />
                <span class="material-symbols-outlined absolute left-2.5 top-2 text-[14px] text-outline/50">search</span>
            </div>
        </div>
        <div class="flex-grow overflow-y-auto overflow-x-auto">
            <table class="w-full text-left border-collapse text-[13px] min-w-[600px]" id="otpTable">
                <thead>
                    <tr class="border-b border-outline-variant/50 bg-slate-50/50 dark:bg-white/5 text-outline select-none">
                        <th class="p-4 w-1/3 text-[11px] font-bold uppercase tracking-wider" data-i18n="otpColEmail">账号邮箱</th>
                        <th class="p-4 w-1/4 text-[11px] font-bold uppercase tracking-wider" data-i18n="otpColStatus">2FA 状态 / 密钥</th>
                        <th class="p-4 w-1/5 text-[11px] font-bold uppercase tracking-wider text-center" data-i18n="otpColCode">动态验证码</th>
                        <th class="p-4 w-1/5 text-[11px] font-bold uppercase tracking-wider text-right" data-i18n="otpColAction">操作</th>
                    </tr>
                </thead>
                <tbody class="text-on-surface dark:text-white divide-y divide-outline-variant/20" id="otpTableBody">
                    <!-- 动态生成 2FA 行 -->
                </tbody>
            </table>
            <div class="hidden flex-col items-center justify-center py-12 text-outline/50" id="otpEmptyState">
                <span class="material-symbols-outlined text-[48px] mb-2">vpn_key_off</span>
                <span class="text-[13px]" data-i18n="otpEmpty">暂无账号数据</span>
            </div>
        </div>
        <!-- 分页控制栏 -->
        <div class="p-3 border-t border-outline-variant/30 flex justify-between items-center bg-slate-50/20 dark:bg-white/[0.02] rounded-b-xl text-[12px] text-outline dark:text-outline-variant" id="otpPaginationContainer">
            <div id="otpPaginationInfo">显示第 1-10 条，共 0 条</div>
            <div class="flex items-center gap-1.5">
                <button id="btnOtpPrevPage" class="px-2.5 py-1 bg-slate-100 hover:bg-slate-200 dark:bg-white/5 dark:hover:bg-white/10 text-on-surface dark:text-white rounded border border-outline-variant/30 cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed transition-colors flex items-center gap-0.5" disabled>
                    <span class="material-symbols-outlined text-[14px]">chevron_left</span><span data-i18n="btnPrevPage">上一页</span>
                </button>
                <span class="mx-1.5 font-medium text-slate-700 dark:text-slate-300" id="otpPageIndicator">1 / 1</span>
                <button id="btnOtpNextPage" class="px-2.5 py-1 bg-slate-100 hover:bg-slate-200 dark:bg-white/5 dark:hover:bg-white/10 text-on-surface dark:text-white rounded border border-outline-variant/30 cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed transition-colors flex items-center gap-0.5" disabled>
                    <span data-i18n="btnNextPage">下一页</span><span class="material-symbols-outlined text-[14px]">chevron_right</span>
                </button>
            </div>
        </div>
    </div>
</div>
</template>

<script setup lang="ts">
import { onMounted } from 'vue';

onMounted(() => {
  // Logic from otp controller will go here
});
</script>
