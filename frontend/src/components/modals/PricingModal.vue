<template>
  <BaseModal
    id="pricingModal"
    containerId="pricingModalContainer"
    closeBtnId="pricingModalCloseBtn"
    icon="payments"
    maxWidth="w-[450px] max-w-[90vw]"
  >
    <template #header-title>
      <span id="pricingModalTitle" class="text-sm font-bold text-on-surface dark:text-white" data-i18n="pricingModalTitle">编辑模型计费</span>
    </template>

    <form class="flex flex-col gap-4" id="pricingForm">
      <input id="pricingOrigKey" type="hidden"/>
      <!-- 模型匹配名称 -->
      <div class="flex flex-col gap-1.5">
        <label class="text-[11px] text-outline font-bold uppercase" for="pricingModelName" data-i18n="pricingModelPatternLabel">模型匹配名称 (如 deepseek-chat)</label>
        <input class="px-3 py-1.5 text-[13px] bg-white dark:bg-[#1a1f30] border border-outline-variant/60 rounded-md focus:border-primary focus:outline-none transition-shadow text-on-surface dark:text-white font-data-mono" id="pricingModelName" required type="text"/>
      </div>
      <!-- 输入价格 -->
      <div class="flex flex-col gap-1.5">
        <label class="text-[11px] text-outline font-bold uppercase" for="pricingInputVal" data-i18n="pricingInputValLabel">输入 Token 单价 (USD / 每百万 Tokens)</label>
        <input class="px-3 py-1.5 text-[13px] bg-white dark:bg-[#1a1f30] border border-outline-variant/60 rounded-md focus:border-primary focus:outline-none transition-shadow text-on-surface dark:text-white font-data-mono" id="pricingInputVal" min="0" required step="0.000001" type="number"/>
      </div>
      <!-- 输出价格 -->
      <div class="flex flex-col gap-1.5">
        <label class="text-[11px] text-outline font-bold uppercase" for="pricingOutputVal" data-i18n="pricingOutputValLabel">输出 Token 单价 (USD / 每百万 Tokens)</label>
        <input class="px-3 py-1.5 text-[13px] bg-white dark:bg-[#1a1f30] border border-outline-variant/60 rounded-md focus:border-primary focus:outline-none transition-shadow text-on-surface dark:text-white font-data-mono" id="pricingOutputVal" min="0" required step="0.000001" type="number"/>
      </div>
      <!-- 缓存价格 -->
      <div class="flex flex-col gap-1.5">
        <label class="text-[11px] text-outline font-bold uppercase" for="pricingCachedVal" data-i18n="pricingCachedValLabel">缓存 Token 单价 (USD / 每百万 Tokens)</label>
        <input class="px-3 py-1.5 text-[13px] bg-white dark:bg-[#1a1f30] border border-outline-variant/60 rounded-md focus:border-primary focus:outline-none transition-shadow text-on-surface dark:text-white font-data-mono" id="pricingCachedVal" min="0" required step="0.000001" type="number"/>
      </div>
    </form>

    <template #footer>
      <button class="px-4 py-1.5 text-[12px] font-medium bg-slate-100 hover:bg-slate-200 dark:bg-white/5 dark:hover:bg-white/10 text-on-surface dark:text-white rounded-lg transition-colors border border-outline-variant/40 cursor-pointer" id="pricingModalCancelBtn" data-i18n="btnCancel">取消</button>
      <button class="px-4 py-1.5 text-[12px] font-bold bg-primary text-white hover:bg-primary/90 rounded-lg transition-colors shadow-sm cursor-pointer" id="pricingModalSaveBtn" data-i18n="btnSave">保存</button>
    </template>
  </BaseModal>
</template>

<script setup lang="ts">
import BaseModal from './BaseModal.vue';
</script>

