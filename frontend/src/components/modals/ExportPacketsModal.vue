<template>
  <BaseModal
    id="exportPacketsModal"
    containerId="exportPacketsModalContainer"
    closeBtnId="exportPacketsModalCloseBtn"
    title="导出接口日志"
    titleI18n="exportPacketsTitle"
    icon="description"
    maxWidth="w-[420px] max-w-[90vw]"
  >
    <div class="flex flex-col gap-4">
      <div class="text-[12px] text-outline leading-relaxed" data-i18n="exportPacketsTip">
        请选择需要导出的接口数据包分类。我们将为您生成一份包含请求和响应细节（包含 Method、Host、Path、Headers 以及 Request/Response Body）的完整 Markdown 文档。
      </div>
      <!-- 类型选择 -->
      <div class="flex flex-col gap-2">
        <label class="text-[13px] font-medium text-slate-700 dark:text-slate-200" for="exportPacketsTypeSelect" data-i18n="exportPacketsSelectLabel">选择接口分类:</label>
        <select class="bg-white dark:bg-[#1a1f30] border border-outline-variant/50 rounded-md px-3 py-1.5 text-[13px] font-medium text-slate-700 dark:text-slate-200 focus:outline-none focus:border-primary w-full" id="exportPacketsTypeSelect">
          <option value="ALL" data-i18n="packetFilterAll">全部</option>
          <option value="CLI">CLI</option>
          <option value="IDE">IDE</option>
          <option value="Agent">Agent</option>
          <option value="UNKNOWN" data-i18n="packetFilterUnknown">未知</option>
        </select>
      </div>
    </div>

    <template #footer>
      <button class="px-4 py-2 text-[12px] font-medium bg-slate-100 hover:bg-slate-200 dark:bg-white/5 dark:hover:bg-white/10 text-on-surface dark:text-white rounded-lg transition-colors border border-outline-variant/40 cursor-pointer" id="btnExportPacketsCancel" data-i18n="btnCancel">取消</button>
      <button class="px-4 py-2 text-[12px] font-bold bg-primary text-white hover:bg-primary/90 rounded-lg transition-colors shadow-sm cursor-pointer" id="btnExportPacketsConfirm" data-i18n="btnExportPacketsConfirm">确认导出</button>
    </template>
  </BaseModal>
</template>

<script setup lang="ts">
import BaseModal from './BaseModal.vue';
</script>

