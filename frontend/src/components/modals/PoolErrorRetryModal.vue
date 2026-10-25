<template>
  <BaseModal
    id="poolErrorRetryModal"
    @close="closePoolErrorRetryModal"
    containerId="poolErrorRetryModalContainer"
    closeBtnId="btnPoolErrorRetryModalClose"
    title="报错重试与切号配置"
    titleI18n="poolErrorRetryModalTitle"
    icon="tune"
    maxWidth="w-[520px] max-w-[95vw]"
    maxHeight="max-h-[85vh]"
    bodyClass="px-6 py-5 overflow-y-auto flex flex-col gap-4"
  >
    <!-- 启用开关 -->
    <div class="flex items-center justify-between gap-3 bg-slate-50/50 dark:bg-white/5 border border-outline-variant/30 rounded-lg px-3 py-2.5">
      <div>
        <div class="text-[13px] font-bold text-on-surface dark:text-white" data-i18n="poolErrorRetryEnabledLabel">启用自定义重试与切号策略</div>
        <div class="text-[11px] text-outline mt-0.5" data-i18n="poolErrorRetryEnabledTip">开启后上游返回指定报错状态码时将自动重试或切换下一个号；关闭时使用默认策略</div>
      </div>
      <label class="relative inline-flex items-center cursor-pointer shrink-0">
        <input type="checkbox" id="poolErrorRetryEnabledInput" class="sr-only peer" v-model="enabled" />
        <span class="text-[11px] font-medium text-outline mr-2 peer-checked:text-primary dark:peer-checked:text-primary-fixed-dim">{{ enabled ? '已启用' : '已停用' }}</span>
        <span class="relative inline-block w-7 h-3.5 bg-slate-300 dark:bg-white/10 rounded-full peer-checked:bg-primary transition-colors peer-checked:after:translate-x-3 after:content-[''] after:absolute after:top-0.5 after:left-0.5 after:w-3 after:h-3 after:bg-white dark:after:bg-slate-200 after:rounded-full after:shadow-sm after:transition-transform"></span>
      </label>
    </div>

    <!-- 切换下一个号状态码 -->
    <div class="flex flex-col gap-1.5">
      <label class="text-[12px] font-medium text-on-surface dark:text-white flex items-center gap-1.5" data-i18n="poolErrorRetrySwitchCodesLabel">
        <span>🔀 切换下一个号的状态码</span>
      </label>
      <input type="text" id="poolErrorRetrySwitchCodesInput"
        v-model="switchCodesText"
        placeholder="例如 429, 403, 503, 5xx"
        data-i18n-placeholder="poolErrorRetrySwitchCodesPlaceholder"
        class="w-full px-3 py-2 bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[13px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all font-mono" />
      <div class="text-[11px] text-outline mt-0.5" data-i18n="poolErrorRetrySwitchCodesTip">遇到这些报错状态码时，立即冻结当前号并切换号池下一个可用账号；支持逗号分隔与 5xx 范围简写</div>
    </div>

    <!-- 切号冷静期 -->
    <div class="flex flex-col gap-1.5">
      <label class="text-[12px] font-medium text-on-surface dark:text-white" data-i18n="poolErrorRetryCooldownSecsLabel">切号冷静期 (秒)</label>
      <div class="flex items-center gap-2">
        <input type="number" id="poolErrorRetryCooldownSecsInput" min="0" max="604800"
          v-model.number="cooldownSecs"
          class="w-32 px-3 py-2 bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[13px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all text-center" />
        <span class="text-[12px] text-outline">秒 (0 表示不进入长期冷静期，仅本次单次请求内切号)</span>
      </div>
      <div class="text-[11px] text-outline mt-0.5" data-i18n="poolErrorRetryCooldownSecsTip">切换账号时对原账号设置的冷却秒数 (0~86400，默认 60 秒，填 0 仅单次请求切号)</div>
    </div>

    <!-- 当前号直接重试状态码 -->
    <div class="flex flex-col gap-1.5">
      <label class="text-[12px] font-medium text-on-surface dark:text-white flex items-center gap-1.5" data-i18n="poolErrorRetryRetryCodesLabel">
        <span>🔁 当前号直接重试的状态码</span>
      </label>
      <input type="text" id="poolErrorRetryRetryCodesInput"
        v-model="retryCodesText"
        placeholder="例如 500, 502, 504"
        data-i18n-placeholder="poolErrorRetryRetryCodesPlaceholder"
        class="w-full px-3 py-2 bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[13px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all font-mono" />
      <div class="text-[11px] text-outline mt-0.5" data-i18n="poolErrorRetryRetryCodesTip">遇到这些报错状态码时，不更换账号，直接在当前账号上重试；支持逗号分隔与 5xx 简写</div>
    </div>

    <!-- 最大重试次数 -->
    <div class="flex flex-col gap-1.5">
      <label class="text-[12px] font-medium text-on-surface dark:text-white" data-i18n="poolErrorRetryMaxRetriesLabel">最大重试次数</label>
      <div class="flex items-center gap-2">
        <input type="number" id="poolErrorRetryMaxRetriesInput" min="1" max="10"
          v-model.number="maxRetries"
          class="w-32 px-3 py-2 bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[13px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all text-center" />
        <span class="text-[12px] text-outline">次 (建议 1~5 次)</span>
      </div>
      <div class="text-[11px] text-outline mt-0.5" data-i18n="poolErrorRetryMaxRetriesTip">单次请求遇到报错时的最大尝试次数 (1~10，默认 3 次)</div>
    </div>

    <template #footer>
      <button class="px-4 py-2 text-[13px] font-medium text-on-surface dark:text-white hover:bg-slate-100 dark:hover:bg-white/5 rounded-lg transition-colors border border-outline-variant/30 cursor-pointer" id="btnPoolErrorRetryModalCancel">取消</button>
      <button class="px-4 py-2 text-[13px] font-bold text-white bg-primary hover:bg-primary/90 rounded-lg transition-colors shadow-sm cursor-pointer" id="btnPoolErrorRetryModalSave">保存配置</button>
    </template>
  </BaseModal>
</template>

<script setup lang="ts">
import BaseModal from './BaseModal.vue';
import {
  closePoolErrorRetryModal,
  poolErrorRetryEnabled,
  poolErrorRetrySwitchCodesText,
  poolErrorRetryRetryCodesText,
  poolErrorRetryCooldownSecs,
  poolErrorRetryMaxRetries,
} from '../../ui/poolErrorRetryModal';

const enabled = poolErrorRetryEnabled;
const switchCodesText = poolErrorRetrySwitchCodesText;
const retryCodesText = poolErrorRetryRetryCodesText;
const cooldownSecs = poolErrorRetryCooldownSecs;
const maxRetries = poolErrorRetryMaxRetries;
</script>
