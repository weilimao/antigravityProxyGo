<template>
  <BaseModal
    id="aiPricingModal"
    containerId="aiPricingModalContainer"
    closeBtnId="btnAiPricingModalClose"
    icon="auto_awesome"
    maxWidth="w-[680px] max-w-[95vw]"
    bodyClass="p-5"
  >
    <template #header-title>
      <span class="text-sm font-bold text-on-surface dark:text-white" data-i18n="aiPricingModalTitle">AI 一键生成计费配置</span>
    </template>

    <!-- 账号选择 + 说明 -->
    <div id="aiPricingPreSection" class="flex flex-col gap-4">
      <div class="flex flex-col gap-1.5">
        <label class="text-[11px] text-outline font-bold uppercase" data-i18n="aiPricingAccountLabel">选择用于生成的 Antigravity 账号</label>
        <select id="aiPricingAccountSelect" class="w-full px-3 py-1.5 text-[13px] bg-white dark:bg-[#1a1f30] border border-outline-variant/60 rounded-md focus:border-primary focus:outline-none transition-shadow text-on-surface dark:text-white">
          <!-- JS 动态填充 -->
        </select>
        <p class="text-[11px] text-outline leading-relaxed" data-i18n="aiPricingDesc">点击开始生成后,将调用选定账号的 Gemini 模型为「模型统计」中尚未登记单价的模型生成建议定价(USD/每百万 Tokens),生成完毕后可在表格中微调后确认添加。</p>
      </div>
      <button id="btnAiPricingGen" class="flex items-center justify-center gap-1.5 px-4 py-2 bg-primary text-white hover:bg-primary/90 rounded-lg text-[13px] font-bold transition-colors shadow-sm disabled:opacity-50 disabled:cursor-not-allowed">
        <span class="material-symbols-outlined text-[16px]">auto_awesome</span>
        <span data-i18n="aiPricingGenBtn">开始生成</span>
      </button>
    </div>

    <!-- 生成中态 -->
    <div id="aiPricingLoading" class="hidden flex flex-col items-center justify-center py-10 gap-3">
      <div class="w-9 h-9 border-3 border-primary/30 border-t-primary rounded-full animate-spin"></div>
      <p class="text-[12px] text-outline" data-i18n="aiPricingGenFetching">正在调用 Gemini 生成定价...</p>
    </div>

    <!-- 结果可编辑表 -->
    <div id="aiPricingResultSection" class="hidden flex flex-col gap-3">
      <div class="flex items-center justify-between">
        <span class="text-[11px] font-bold text-outline" data-i18n="aiPricingReviewTip">以下为 AI 生成的建议价格,请核对后确认添加(可直接编辑数值):</span>
      </div>
      <div class="overflow-x-auto rounded-xl border border-outline-variant/30 max-h-[46vh] overflow-y-auto">
        <table class="w-full text-left border-collapse text-[12px]">
          <thead>
            <tr class="bg-slate-50 dark:bg-[#1a1f30] text-outline border-b border-outline-variant/30 sticky top-0 z-10">
              <th class="p-2.5 font-bold" data-i18n="aiPricingColModel">模型名</th>
              <th class="p-2.5 font-bold text-right" data-i18n="aiPricingColInput">输入 (/1M)</th>
              <th class="p-2.5 font-bold text-right" data-i18n="aiPricingColOutput">输出 (/1M)</th>
              <th class="p-2.5 font-bold text-right" data-i18n="aiPricingColCached">缓存 (/1M)</th>
              <th class="p-2.5 font-bold text-center w-[10%]"></th>
            </tr>
          </thead>
          <tbody id="aiPricingTableBody" class="divide-y divide-outline-variant/10 text-on-surface dark:text-white">
            <!-- JS 动态填充 -->
          </tbody>
        </table>
      </div>
    </div>

    <template #footer>
      <button class="px-4 py-1.5 text-[12px] font-medium bg-slate-100 hover:bg-slate-200 dark:bg-white/5 dark:hover:bg-white/10 text-on-surface dark:text-white rounded-lg transition-colors border border-outline-variant/40 cursor-pointer" id="btnAiPricingCancel" data-i18n="btnCancel">取消</button>
      <button class="px-4 py-1.5 text-[12px] font-bold bg-primary text-white hover:bg-primary/90 rounded-lg transition-colors shadow-sm cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed" id="btnAiPricingConfirm" disabled>
        <span data-i18n="aiPricingConfirm">确认添加</span>
      </button>
    </template>
  </BaseModal>
</template>

<script setup lang="ts">
import BaseModal from './BaseModal.vue';
</script>
