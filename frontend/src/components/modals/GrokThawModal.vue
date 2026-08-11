<template>
  <BaseModal
    id="grokThawModal"
    containerId="grokThawModalContainer"
    closeBtnId="grokThawCloseBtn"
    icon="ac_unit"
    iconClass="text-sky-500 text-[20px]"
    maxWidth="w-[520px] max-w-[90vw]"
    maxHeight="max-h-[85vh]"
  >
    <template #header-title>
      <span class="text-sm font-bold text-on-surface dark:text-white" data-i18n="grokThawModalTitle">一键解冻冷却账号</span>
      <span class="text-[11px] font-medium text-sky-600 dark:text-sky-400 bg-sky-500/10 px-1.5 py-0.5 rounded-md" id="grokThawCountBadge">共 0 个冷却账号</span>
    </template>

    <div class="text-[11px] text-outline dark:text-outline-variant mb-3 bg-slate-50 dark:bg-slate-900/30 p-3 rounded-xl border border-outline-variant/20 leading-relaxed flex items-start gap-2">
      <span class="material-symbols-outlined text-sky-500 text-[16px]">info</span>
      <span data-i18n="grokThawModalDesc">勾选要解除冷却的 Grok 账号,点击确认后立即恢复可承接请求。</span>
    </div>

    <!-- 全选行 -->
    <div class="flex items-center gap-2 px-2 pb-2 mb-2 border-b border-outline-variant/20">
      <input type="checkbox" id="grokThawSelectAll" class="w-4 h-4 rounded border-outline-variant/40 dark:border-white/20 text-sky-500 focus:ring-sky-500 cursor-pointer" />
      <label for="grokThawSelectAll" id="grokThawSelectAllLabel" class="text-[12px] font-bold text-on-surface dark:text-white cursor-pointer select-none" data-i18n="grokThawSelectAll">全选</label>
    </div>

    <!-- 冷却账号列表 -->
    <div class="overflow-y-auto flex-grow max-h-[45vh] flex flex-col gap-1" id="grokThawListBody">
      <!-- 由 grokThawModal.ts 动态渲染 -->
    </div>

    <template #footer>
      <div class="flex justify-between items-center w-full gap-2">
        <button class="px-4 py-1.5 text-[12px] font-bold bg-slate-100 hover:bg-slate-200 dark:bg-white/5 dark:hover:bg-white/10 text-on-surface dark:text-white rounded-lg transition-colors border border-outline-variant/40 cursor-pointer" id="grokThawCancelBtn" data-i18n="grokThawCancel">取消</button>
        <button class="px-4 py-1.5 text-[12px] font-bold bg-sky-500 hover:bg-sky-600 text-white rounded-lg transition-colors border border-sky-500/40 cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed flex items-center gap-1.5" id="grokThawConfirmBtn">
          <span class="material-symbols-outlined text-[15px]" id="grokThawConfirmIcon">ac_unit</span>
          <span id="grokThawConfirmText" data-i18n="grokThawConfirm">解冻选中账号</span>
        </button>
      </div>
    </template>
  </BaseModal>
</template>

<script setup lang="ts">
import BaseModal from './BaseModal.vue';
</script>
