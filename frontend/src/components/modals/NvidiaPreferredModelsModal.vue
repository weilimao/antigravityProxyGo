<template>
  <BaseModal
    id="nvidiaPreferredModelsModal"
    containerId="nvidiaPreferredModelsModalContainer"
    closeBtnId="btnNvidiaPreferredModalClose"
    title="NVIDIA 专属模型"
    titleI18n="nvidiaPreferredModelsTitle"
    icon="inventory"
    maxWidth="w-[860px] max-w-[95vw]"
    maxHeight="max-h-[85vh]"
    bodyClass="px-6 py-5 overflow-hidden flex flex-col gap-3"
  >
    <!-- 顶部状态行 -->
    <div class="flex items-center justify-between flex-wrap gap-2 shrink-0">
      <div class="text-[12px] text-on-surface dark:text-white">
        <span data-i18n="nvidiaPreferredModelsCountSaved">已保存</span>
        <span class="font-bold text-primary dark:text-primary-fixed-dim" id="lblNvidiaPreferredCount">0</span>
        <span data-i18n="nvidiaPreferredModelsUnit">个专属模型</span>
      </div>
      <div id="lblNvidiaPreferredSource" class="hidden text-[11px] px-2 py-0.5 rounded-md bg-primary/10 text-primary dark:text-primary-fixed-dim"></div>
    </div>

    <!-- 来源切换条 -->
    <div class="flex items-center gap-2 shrink-0">
      <button type="button" id="btnNvidiaPreferredSrcLocal"
        class="px-3 py-1.5 text-[12px] font-bold rounded-lg transition-colors flex items-center gap-1 cursor-pointer
               bg-primary text-white">
        <span class="material-symbols-outlined text-[14px]">inventory_2</span>
        <span data-i18n="nvidiaPreferredSrcLocal">本地清单</span>
      </button>
      <button type="button" id="btnNvidiaPreferredSrcRemote"
        class="px-3 py-1.5 text-[12px] font-bold rounded-lg transition-colors flex items-center gap-1 cursor-pointer
               text-primary bg-primary/10 hover:bg-primary/20">
        <span class="material-symbols-outlined text-[14px]">cloud</span>
        <span data-i18n="nvidiaPreferredSrcRemote">NVIDIA远端</span>
      </button>
      <span class="text-[10px] text-outline shrink-0" data-i18n="nvidiaPreferredCacheHint">本地清单若空将自动回退远端拉取</span>
    </div>

    <!-- 双列穿梭框 -->
    <div class="flex gap-2 flex-1 min-h-0">
      <!-- 左列:已选清单(给客户端用) -->
      <div class="flex-1 flex flex-col min-w-0 border border-outline-variant/30 rounded-lg overflow-hidden">
        <div class="flex items-center justify-between px-3 py-1.5 bg-slate-50/60 dark:bg-white/5 border-b border-outline-variant/20 shrink-0">
          <label class="flex items-center gap-1.5 text-[11px] font-medium text-on-surface dark:text-white cursor-pointer select-none">
            <input type="checkbox" id="chkNvidiaPreferredSelectAllLeft" class="w-3.5 h-3.5 rounded border-outline-variant/40 dark:border-white/20 text-primary focus:ring-primary cursor-pointer" />
            <span data-i18n="nvidiaPreferredColSelected">已选清单(给客户端用)</span>
          </label>
          <span class="text-[11px] text-outline" id="lblNvidiaPreferredVisibleLeft">- / -</span>
        </div>
        <div class="px-2 py-1.5 border-b border-outline-variant/20 shrink-0">
          <div class="relative">
            <span class="material-symbols-outlined absolute left-2 top-1/2 -translate-y-1/2 text-[14px] text-outline pointer-events-none">search</span>
            <input type="text" id="inputNvidiaPreferredSearchLeft"
              data-i18n-placeholder="nvidiaPreferredModelsSearchPlaceholder"
              placeholder="搜索模型 id..."
              class="w-full pl-7 pr-2 py-1 bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded text-[11px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all" />
          </div>
        </div>
        <div id="nvidiaPreferredModelsListLeft" class="flex-1 overflow-y-auto flex flex-col divide-y divide-outline-variant/10 min-h-0">
          <div id="nvidiaPreferredEmptyLeft" class="text-[12px] text-outline text-center py-8 select-none">
            <span data-i18n="nvidiaPreferredEmptyLeft">左列暂无已选模型，从右列移入</span>
          </div>
        </div>
      </div>

      <!-- 中间移入/移出按钮列 -->
      <div class="flex flex-col items-center justify-center gap-2 shrink-0">
        <button type="button" id="btnNvidiaPreferredMoveLeft" title="移入已选"
          class="px-2 py-1.5 text-[14px] text-primary bg-primary/10 hover:bg-primary/20 rounded-lg transition-colors cursor-pointer"
          data-i18n-title="nvidiaPreferredMoveLeft">◀</button>
        <button type="button" id="btnNvidiaPreferredMoveRight" title="移出已选"
          class="px-2 py-1.5 text-[14px] text-on-surface dark:text-white bg-slate-100 dark:bg-white/5 hover:bg-slate-200 dark:hover:bg-white/10 rounded-lg transition-colors cursor-pointer"
          data-i18n-title="nvidiaPreferredMoveRight">▶</button>
        <button type="button" id="btnNvidiaPreferredMoveAllLeft" title="全部移入"
          class="px-2 py-1.5 text-[14px] text-primary bg-primary/10 hover:bg-primary/20 rounded-lg transition-colors cursor-pointer"
          data-i18n-title="nvidiaPreferredMoveAllLeft">≪</button>
        <button type="button" id="btnNvidiaPreferredMoveAllRight" title="全部移出"
          class="px-2 py-1.5 text-[14px] text-on-surface dark:text-white bg-slate-100 dark:bg-white/5 hover:bg-slate-200 dark:hover:bg-white/10 rounded-lg transition-colors cursor-pointer"
          data-i18n-title="nvidiaPreferredMoveAllRight">≫</button>
      </div>

      <!-- 右列:上游候选 -->
      <div class="flex-1 flex flex-col min-w-0 border border-outline-variant/30 rounded-lg overflow-hidden">
        <div class="flex items-center justify-between px-3 py-1.5 bg-slate-50/60 dark:bg-white/5 border-b border-outline-variant/20 shrink-0">
          <label class="flex items-center gap-1.5 text-[11px] font-medium text-on-surface dark:text-white cursor-pointer select-none">
            <input type="checkbox" id="chkNvidiaPreferredSelectAllRight" class="w-3.5 h-3.5 rounded border-outline-variant/40 dark:border-white/20 text-primary focus:ring-primary cursor-pointer" />
            <span data-i18n="nvidiaPreferredColSource">上游候选</span>
          </label>
          <span class="text-[11px] text-outline" id="lblNvidiaPreferredVisibleRight">- / -</span>
        </div>
        <div class="px-2 py-1.5 border-b border-outline-variant/20 shrink-0 flex items-center gap-1.5">
          <button type="button" id="btnNvidiaPreferredFetch" class="px-2 py-1 text-[11px] font-bold text-white bg-primary hover:bg-primary/90 rounded transition-colors flex items-center gap-1 shrink-0 cursor-pointer disabled:opacity-50 disabled:pointer-events-none">
            <span class="material-symbols-outlined text-[12px]" id="iconNvidiaPreferredFetch">cloud_download</span>
            <span data-i18n="nvidiaPreferredFetchUpstream">获取上游模型</span>
          </button>
          <div class="relative flex-1 min-w-0">
            <span class="material-symbols-outlined absolute left-2 top-1/2 -translate-y-1/2 text-[14px] text-outline pointer-events-none">search</span>
            <input type="text" id="inputNvidiaPreferredSearchRight"
              data-i18n-placeholder="nvidiaPreferredModelsSearchPlaceholder"
              placeholder="搜索模型 id..."
              class="w-full pl-7 pr-2 py-1 bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded text-[11px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all" />
          </div>
        </div>
        <div id="nvidiaPreferredModelsListRight" class="flex-1 overflow-y-auto flex flex-col divide-y divide-outline-variant/10 min-h-0">
          <div id="nvidiaPreferredEmptyRight" class="text-[12px] text-outline text-center py-8 select-none">
            <span data-i18n="nvidiaPreferredEmptyRight">点击「获取上游模型」拉取 NVIDIA 线上候选</span>
          </div>
        </div>
      </div>
    </div>

    <div id="nvidiaPreferredError" class="hidden text-[12px] text-red-500 bg-red-500/10 border border-red-500/20 rounded-lg px-3 py-2 shrink-0"></div>

    <template #footer>
      <button class="px-4 py-2 text-[13px] font-medium text-on-surface dark:text-white hover:bg-slate-100 dark:hover:bg-white/5 rounded-lg transition-colors border border-outline-variant/30 cursor-pointer" id="btnNvidiaPreferredModalCancel" data-i18n="nvidiaPreferredModalCancel">取消</button>
      <button class="px-4 py-2 text-[13px] font-bold text-white bg-primary hover:bg-primary/90 rounded-lg transition-colors shadow-sm disabled:opacity-50 disabled:pointer-events-none cursor-pointer" id="btnNvidiaPreferredSave" data-i18n="nvidiaPreferredModelsSave">保存清单</button>
    </template>
  </BaseModal>
</template>

<script setup lang="ts">
import BaseModal from './BaseModal.vue';
</script>
