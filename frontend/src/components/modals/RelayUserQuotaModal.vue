<template>
<div class="hidden fixed inset-0 bg-black/50 z-[9999] flex items-center justify-center" id="relayUserQuotaModal">
<div class="bg-white dark:bg-[#1e2538] rounded-xl shadow-2xl w-[480px] p-6 border border-outline-variant/20">
<div class="flex items-center justify-between mb-5">
<div class="flex items-center gap-2">
<span class="material-symbols-outlined text-[22px] text-primary">settings</span>
<h3 class="text-[16px] font-bold text-on-surface dark:text-white" id="relayQuotaModalTitle" data-i18n="relayQuotaModalTitleUser">限额配置</h3>
</div>
<button class="text-outline hover:text-on-surface transition-colors" onclick="document.getElementById('relayUserQuotaModal').classList.add('hidden')">
<span class="material-symbols-outlined text-[20px]">close</span>
</button>
</div>
<input id="quotaUserId" type="hidden"/>
<input id="quotaPackageId" type="hidden"/>
<div class="hidden mb-4" id="quotaPackageNameContainer">
<label class="block text-[12px] font-medium text-outline mb-1" data-i18n="relayQuotaPackageNameLabel">套餐名称</label>
<input class="w-full px-3 py-2 text-[13px] rounded-lg border border-outline-variant/30 bg-transparent text-on-surface dark:text-white focus:outline-none focus:border-primary" id="quotaPackageName" placeholder="输入套餐名称" data-i18n-placeholder="relayQuotaPackageNamePlaceholder" type="text"/>
</div>
<div class="mb-4">
<label class="block text-[12px] font-medium text-outline mb-1 flex items-center gap-1"><span class="material-symbols-outlined text-[14px]">event</span> <span data-i18n="relayQuotaValidLabel">账号/套餐有效期</span></label>
<div class="flex gap-2">
<input class="flex-1 px-3 py-2 text-[13px] rounded-lg border border-outline-variant/30 bg-transparent text-on-surface dark:text-white focus:outline-none focus:border-primary" id="quotaValidDuration" placeholder="有效期时长，0表示永久" data-i18n-placeholder="relayQuotaValidPlaceholder" type="number"/>
<select class="w-24 px-3 py-2 text-[13px] rounded-lg border border-outline-variant/30 bg-transparent text-on-surface dark:text-white focus:outline-none focus:border-primary" id="quotaValidUnit">
<option value="days" data-i18n="relayDay">天</option>
<option value="months" data-i18n="relayMonth">个月</option>
<option value="years" data-i18n="relayYear">年</option>
</select>
</div>
</div>
<div class="mb-4">
<label class="block text-[12px] font-medium text-outline mb-1 flex items-center gap-1">
<span class="material-symbols-outlined text-[14px]">speed</span>
<span data-i18n="relayQuotaRateLimitLabel">请求速率限制 (次/分钟)</span>
</label>
<input class="w-full px-3 py-2 text-[13px] rounded-lg border border-outline-variant/30 bg-transparent text-on-surface dark:text-white focus:outline-none focus:border-primary" id="quotaRateLimit" placeholder="默认每分钟 30 次" data-i18n-placeholder="relayQuotaRateLimitPlaceholder" type="number"/>
</div>
<div class="mb-4 bg-outline-variant/5 p-3 rounded-lg border border-outline-variant/20" id="quotaPresetsContainer">
<div class="text-[12px] font-medium text-outline mb-2" data-i18n="relayQuotaQuickSetupLabel">快速设置套餐</div>
<div class="flex gap-2 flex-wrap" id="dynamicQuotaPresets"></div>
</div>
<div class="space-y-6 max-h-[50vh] overflow-y-auto">
<!-- Gemini Quota -->
<div>
<h4 class="text-[13px] font-bold text-primary mb-2 flex items-center gap-1"><span class="material-symbols-outlined text-[16px]">psychology</span> <span data-i18n="relayQuotaGeminiTitle">Gemini 系列限额</span></h4>
<div class="space-y-3 text-[13px]">
<!-- Fixed -->
<div>
<label class="flex items-center gap-2 cursor-pointer mb-1">
<input class="text-primary focus:ring-primary rounded" id="geminiEnableFixed" type="checkbox"/> <span data-i18n="relayQuotaFixedTokensLabel">纯固定总量限额</span>
                            </label>
<div class="pl-6 flex gap-2">
<input class="w-full px-2 py-1 text-[12px] rounded border border-outline-variant/30 bg-transparent text-on-surface dark:text-white" id="geminiFixedTokens" placeholder="总计 Token 上限" data-i18n-placeholder="relayQuotaFixedTokensPlaceholder" type="number"/>
</div>
</div>
<!-- Hourly -->
<div>
<label class="flex items-center gap-2 cursor-pointer mb-1">
<input class="text-primary focus:ring-primary rounded" id="geminiEnableHourly" type="checkbox"/> <span data-i18n="relayQuotaHourlyTokensLabel">短期滚动周期限额 (小时级)</span>
                            </label>
<div class="pl-6 flex gap-2">
<input class="w-1/3 px-2 py-1 text-[12px] rounded border border-outline-variant/30 bg-transparent text-on-surface dark:text-white" id="geminiHourlyHours" placeholder="小时数" data-i18n-placeholder="relayQuotaHourlyHoursPlaceholder" type="number" step="any"/>
<input class="w-2/3 px-2 py-1 text-[12px] rounded border border-outline-variant/30 bg-transparent text-on-surface dark:text-white" id="geminiHourlyTokens" placeholder="该时段 Token 上限" data-i18n-placeholder="relayQuotaHourlyTokensPlaceholder" type="number"/>
</div>
</div>
<!-- Daily -->
<div>
<label class="flex items-center gap-2 cursor-pointer mb-1">
<input class="text-primary focus:ring-primary rounded" id="geminiEnableDaily" type="checkbox"/> <span data-i18n="relayQuotaDailyTokensLabel">长期滚动周期限额 (天级)</span>
                            </label>
<div class="pl-6 flex gap-2">
<input class="w-1/3 px-2 py-1 text-[12px] rounded border border-outline-variant/30 bg-transparent text-on-surface dark:text-white" id="geminiDailyDays" placeholder="天数" data-i18n-placeholder="relayQuotaDailyDaysPlaceholder" type="number" step="any"/>
<input class="w-2/3 px-2 py-1 text-[12px] rounded border border-outline-variant/30 bg-transparent text-on-surface dark:text-white" id="geminiDailyTokens" placeholder="该时段 Token 上限" data-i18n-placeholder="relayQuotaDailyTokensPlaceholder" type="number"/>
</div>
</div>
</div>
</div>
<!-- Claude Quota -->
<div>
<h4 class="text-[13px] font-bold text-primary mb-2 flex items-center gap-1"><span class="material-symbols-outlined text-[16px]">smart_toy</span> <span data-i18n="relayQuotaClaudeTitle">Claude 系列限额</span></h4>
<div class="space-y-3 text-[13px]">
<!-- Fixed -->
<div>
<label class="flex items-center gap-2 cursor-pointer mb-1">
<input class="text-primary focus:ring-primary rounded" id="claudeEnableFixed" type="checkbox"/> <span data-i18n="relayQuotaFixedTokensLabel">纯固定总量限额</span>
                            </label>
<div class="pl-6 flex gap-2">
<input class="w-full px-2 py-1 text-[12px] rounded border border-outline-variant/30 bg-transparent text-on-surface dark:text-white" id="claudeFixedTokens" placeholder="总计 Token 上限" data-i18n-placeholder="relayQuotaFixedTokensPlaceholder" type="number"/>
</div>
</div>
<!-- Hourly -->
<div>
<label class="flex items-center gap-2 cursor-pointer mb-1">
<input class="text-primary focus:ring-primary rounded" id="claudeEnableHourly" type="checkbox"/> <span data-i18n="relayQuotaHourlyTokensLabel">短期滚动周期限额 (小时级)</span>
                            </label>
<div class="pl-6 flex gap-2">
<input class="w-1/3 px-2 py-1 text-[12px] rounded border border-outline-variant/30 bg-transparent text-on-surface dark:text-white" id="claudeHourlyHours" placeholder="小时数" data-i18n-placeholder="relayQuotaHourlyHoursPlaceholder" type="number" step="any"/>
<input class="w-2/3 px-2 py-1 text-[12px] rounded border border-outline-variant/30 bg-transparent text-on-surface dark:text-white" id="claudeHourlyTokens" placeholder="该时段 Token 上限" data-i18n-placeholder="relayQuotaHourlyTokensPlaceholder" type="number"/>
</div>
</div>
<!-- Daily -->
<div>
<label class="flex items-center gap-2 cursor-pointer mb-1">
<input class="text-primary focus:ring-primary rounded" id="claudeEnableDaily" type="checkbox"/> <span data-i18n="relayQuotaDailyTokensLabel">长期滚动周期限额 (天级)</span>
                            </label>
<div class="pl-6 flex gap-2">
<input class="w-1/3 px-2 py-1 text-[12px] rounded border border-outline-variant/30 bg-transparent text-on-surface dark:text-white" id="claudeDailyDays" placeholder="天数" data-i18n-placeholder="relayQuotaDailyDaysPlaceholder" type="number" step="any"/>
<input class="w-2/3 px-2 py-1 text-[12px] rounded border border-outline-variant/30 bg-transparent text-on-surface dark:text-white" id="claudeDailyTokens" placeholder="该时段 Token 上限" data-i18n-placeholder="relayQuotaDailyTokensPlaceholder" type="number"/>
</div>
</div>
</div>
</div>
</div>
<div class="flex gap-2 mt-5 justify-end">
<button class="px-4 py-2 text-[12px] font-medium text-white bg-primary hover:bg-primary/90 rounded-lg transition-colors" onclick="window._relaySaveQuota()" data-i18n="btnConfirmText">保存</button>
<button class="px-4 py-2 text-[12px] font-medium text-outline hover:text-on-surface border border-outline-variant/30 rounded-lg transition-colors" onclick="document.getElementById('relayUserQuotaModal').classList.add('hidden')" data-i18n="btnCancel">取消</button>
</div>
</div>
</div>
</template>

<script setup lang="ts">
import { onMounted } from 'vue';

onMounted(() => {
});
</script>
