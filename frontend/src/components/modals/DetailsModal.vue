<template>
  <BaseModal
    id="detailsModal"
    containerId="modalContainer"
    closeBtnId="modalCloseBtn"
    title="请求参数详情"
    titleI18n="reqDetailsTitle"
    icon="terminal"
    maxWidth="w-[900px] max-w-[92vw]"
    maxHeight="max-h-[90vh]"
    bodyClass="px-6 py-5 overflow-y-auto flex flex-col gap-4"
  >
    <!-- 基本信息卡片 -->
    <div class="grid grid-cols-1 sm:grid-cols-2 gap-4 text-[12px] p-4 bg-slate-50 dark:bg-slate-900/30 rounded-xl border border-outline-variant/20">
      <div class="flex flex-col gap-1">
        <span class="text-outline" data-i18n="reqTime">请求时间</span>
        <span class="font-data-mono text-on-surface dark:text-white font-semibold" id="modalTime">-</span>
      </div>
      <div class="flex flex-col gap-1">
        <span class="text-outline" data-i18n="reqSessionId">会话 ID</span>
        <span class="font-data-mono text-on-surface dark:text-white font-semibold break-all" id="modalSession">-</span>
      </div>
      <div class="flex flex-col gap-1">
        <span class="text-outline" data-i18n="reqModel">所用模型</span>
        <span class="font-sans text-on-surface dark:text-white font-semibold" id="modalModel">-</span>
      </div>
      <div class="flex flex-col gap-1">
        <span class="text-outline" data-i18n="colResponseTime">响应时间</span>
        <span class="font-data-mono text-on-surface dark:text-white font-semibold" id="modalFirstByte">-</span>
      </div>
      <div class="flex flex-col gap-1">
        <span class="text-outline" data-i18n="colDuration">请求耗时</span>
        <span class="font-data-mono text-on-surface dark:text-white font-semibold" id="modalDuration">-</span>
      </div>
      <div class="flex flex-col gap-1 col-span-2">
        <span class="text-outline" data-i18n="reqApi">API 接口</span>
        <span class="font-data-mono text-primary dark:text-primary-fixed-dim font-bold break-all" id="modalPath">-</span>
      </div>
      <div class="flex flex-col gap-1">
        <span class="text-outline" data-i18n="reqTokens">Token 消耗 (In / Out / Cache)</span>
        <span class="font-data-mono text-on-surface dark:text-white font-semibold" id="modalTokens">-</span>
      </div>
      <div class="flex flex-col gap-1">
        <span class="text-outline" data-i18n="reqCacheStatus">缓存状态 / HTTP 状态码</span>
        <span class="font-sans font-semibold" id="modalStatus">-</span>
      </div>
      <div class="flex flex-col gap-1">
        <span class="text-outline" data-i18n="colPrice">价格</span>
        <span class="font-data-mono text-emerald-600 dark:text-emerald-400 font-bold" id="modalCost">-</span>
      </div>
      <div class="flex flex-col gap-1 hidden" id="modalAccountWrapper">
        <span class="text-outline" data-i18n="reqAccount">分发账号</span>
        <span class="font-data-mono text-on-surface dark:text-white font-semibold" id="modalAccount">-</span>
      </div>
    </div>
    <!-- JSON 双栏区：左=请求头 右=请求体 -->
    <div class="grid grid-cols-1 md:grid-cols-2 gap-3 flex-1 min-h-0">
      <!-- 请求头 -->
      <div class="flex flex-col gap-1.5 min-h-0">
        <div class="flex justify-between items-center">
          <span class="text-[12px] font-bold text-outline" data-i18n="reqHeaders">请求头 (Request Headers)</span>
          <button class="px-2.5 py-1 text-[11px] bg-slate-100 hover:bg-slate-200 dark:bg-white/5 dark:hover:bg-white/10 text-on-surface dark:text-white rounded-md flex items-center gap-1 transition-all border border-outline-variant/30 cursor-pointer" id="modalCopyHeadersBtn">
            <span class="material-symbols-outlined text-[14px]">content_copy</span>
            <span data-i18n="btnCopy">复制</span>
          </button>
        </div>
        <div class="relative flex-1 min-h-0">
          <pre class="p-4 bg-slate-950 text-sky-400 dark:text-sky-300 rounded-xl overflow-x-auto text-[11px] font-data-mono border border-slate-800 h-[300px] overflow-y-auto select-text leading-relaxed whitespace-pre-wrap break-all" id="modalHeaderArea"></pre>
        </div>
      </div>
      <!-- 请求体 -->
      <div class="flex flex-col gap-1.5 min-h-0">
        <div class="flex justify-between items-center">
          <span class="text-[12px] font-bold text-outline" data-i18n="reqBody">请求参数 (Request Body)</span>
          <button class="px-2.5 py-1 text-[11px] bg-slate-100 hover:bg-slate-200 dark:bg-white/5 dark:hover:bg-white/10 text-on-surface dark:text-white rounded-md flex items-center gap-1 transition-all border border-outline-variant/30 cursor-pointer" id="modalCopyBtn">
            <span class="material-symbols-outlined text-[14px]">content_copy</span>
            <span data-i18n="btnCopyJson">复制 JSON</span>
          </button>
        </div>
        <div class="relative flex-1 min-h-0">
          <pre class="p-4 bg-slate-950 text-emerald-400 dark:text-emerald-300 rounded-xl overflow-x-auto text-[11px] font-data-mono border border-slate-800 h-[300px] overflow-y-auto select-text leading-relaxed whitespace-pre-wrap break-all" id="modalJsonArea"></pre>
        </div>
      </div>
    </div>

    <template #footer>
      <button class="px-4 py-1.5 text-[12px] font-bold bg-slate-100 hover:bg-slate-200 dark:bg-white/5 dark:hover:bg-white/10 text-on-surface dark:text-white rounded-lg transition-colors border border-outline-variant/40 cursor-pointer" id="modalCloseBtnSecondary" data-i18n="close">关闭</button>
    </template>
  </BaseModal>
</template>

<script setup lang="ts">
import BaseModal from './BaseModal.vue';
</script>

