<template>
  <BaseModal
    id="updateModal"
    containerId="updateModalContainer"
    closeBtnId="updateModalCloseBtn"
    title="软件更新"
    titleI18n="updateTitle"
    icon="system_update"
    maxWidth="w-[540px] max-w-[90vw]"
  >
    <div class="flex flex-col items-center gap-4 text-center max-h-[65vh] overflow-y-auto">
      <!-- 状态图标 -->
      <div class="w-16 h-16 rounded-2xl bg-primary/10 flex items-center justify-center shadow-inner" id="updateModalIconContainer">
        <span class="material-symbols-outlined text-[36px] text-primary" id="updateModalIcon">sync</span>
      </div>
      <h3 class="text-base font-bold text-on-surface dark:text-white" id="updateModalStatusTitle" data-i18n="checkingUpdates">正在检查更新...</h3>
      <p class="text-[12px] text-outline leading-relaxed break-all max-w-md" id="updateModalStatusMsg"></p>
      <!-- 新版本日志 (只在有更新时显示) -->
      <div class="hidden w-full flex flex-col gap-2 text-left" id="updateModalChangelogContainer">
        <div class="text-[11px] font-bold text-outline uppercase tracking-wider" data-i18n="releaseNotesLabel">更新日志:</div>
        <div class="p-3.5 bg-slate-50 dark:bg-slate-900/60 rounded-xl text-[12px] text-on-surface dark:text-slate-300 border border-outline-variant/20 max-h-[220px] overflow-y-auto whitespace-pre-wrap leading-relaxed select-text" id="updateModalChangelog"></div>
      </div>
      <!-- 进度条 -->
      <div class="hidden w-full bg-slate-100 dark:bg-white/5 rounded-full h-2.5 mt-2 overflow-hidden border border-outline-variant/10" id="updateModalProgressContainer">
        <div class="bg-primary h-full rounded-full transition-all duration-150" id="updateModalProgressBarFill" style="width: 0%"></div>
      </div>
    </div>

    <template #footer>
      <div class="flex justify-end gap-3 w-full" id="updateModalActions">
        <button class="px-4 py-2 text-[12px] font-medium bg-slate-100 hover:bg-slate-200 dark:bg-white/5 dark:hover:bg-white/10 text-on-surface dark:text-white rounded-lg transition-colors border border-outline-variant/40 cursor-pointer" data-i18n="btnUpdateLater" id="btnUpdateModalCancel">暂不更新</button>
        <button class="px-4 py-2 text-[12px] font-bold bg-primary text-white hover:bg-primary/90 rounded-lg transition-colors shadow-sm cursor-pointer" data-i18n="btnUpdateNow" id="btnUpdateModalConfirm">在应用内更新</button>
      </div>
    </template>
  </BaseModal>
</template>

<script setup lang="ts">
import BaseModal from './BaseModal.vue';
</script>

