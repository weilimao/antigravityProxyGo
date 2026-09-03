<template>
  <BaseModal
    id="benchmarkConfigModal"
    containerId="benchmarkConfigModalContainer"
    closeBtnId="btnBenchmarkConfigClose"
    title="测速配置"
    titleI18n="benchmarkConfigTitle"
    icon="speed"
    iconClass="text-amber-500 text-[20px]"
    maxWidth="w-[620px] max-w-[92vw]"
    maxHeight="max-h-[88vh]"
    bodyClass="p-5 flex flex-col gap-3 overflow-y-auto"
  >
    <!-- 测速模型多选: 搜索 + 复选框清单(候选来自中继模型映射, 与现有模型选择组件同口径) -->
    <div class="flex flex-col gap-1.5">
      <div class="flex items-center justify-between">
        <label class="text-[11px] font-bold text-outline dark:text-outline-variant uppercase tracking-wider" data-i18n="benchmarkModelsLabel">测速模型</label>
        <div class="flex items-center gap-2 text-[10px]">
          <button type="button" id="btnBenchmarkSelectAllModels" class="text-primary dark:text-primary-fixed-dim hover:underline font-medium cursor-pointer" data-i18n="btnSelectAll">全选</button>
          <span class="text-outline/30">|</span>
          <button type="button" id="btnBenchmarkClearAllModels" class="text-outline hover:text-primary hover:underline font-medium cursor-pointer" data-i18n="btnClearAll">清空</button>
          <span class="text-outline/30">|</span>
          <span class="text-outline dark:text-outline-variant/70"><span id="lblBenchmarkSelectedCount">0</span> <span data-i18n="benchmarkModelsUnit">个</span></span>
        </div>
      </div>
      <div class="flex items-center gap-2">
        <div class="relative flex-1">
          <span class="material-symbols-outlined absolute left-2 top-1/2 -translate-y-1/2 text-[14px] text-outline pointer-events-none">search</span>
          <input type="text" id="benchmarkModelSearch" data-i18n-placeholder="benchmarkModelsSearchPlaceholder" placeholder="搜索模型 id..."
            class="w-full pl-7 pr-2 py-1.5 bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[12px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all" />
        </div>
        <button type="button" id="btnBenchmarkLoadModels" class="px-2.5 py-1.5 text-[11px] font-bold bg-primary/10 hover:bg-primary/20 text-primary dark:text-primary-fixed-dim border border-primary/30 rounded-lg transition-colors flex items-center gap-1 shrink-0 cursor-pointer">
          <span class="material-symbols-outlined text-[13px]">cloud_download</span>
          <span data-i18n="benchmarkLoadModels">载入中继映射模型</span>
        </button>
      </div>
      <div id="benchmarkModelsList" class="flex flex-col gap-0.5 p-2 bg-slate-50/60 dark:bg-slate-900/20 border border-outline-variant/30 rounded-lg max-h-52 overflow-y-auto text-[11.5px] text-on-surface dark:text-slate-200">
        <div class="text-center text-outline py-6 select-none" id="benchmarkModelsEmpty" data-i18n="benchmarkModelsEmpty">点击「载入中继映射模型」加载候选</div>
      </div>
      <span class="text-[10px] text-outline dark:text-outline-variant/60" id="benchmarkLoadHint"></span>
    </div>

    <!-- 间隔 + 超时 -->
    <div class="grid grid-cols-2 gap-3">
      <div class="flex flex-col gap-1.5">
        <label class="text-[11px] font-bold text-outline dark:text-outline-variant uppercase tracking-wider" data-i18n="benchmarkIntervalLabel">触发间隔</label>
        <select id="benchmarkIntervalSelect" class="px-3 py-1.5 text-[12px] bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-on-surface dark:text-white cursor-pointer focus:outline-none focus:border-primary transition-all">
          <option value="1" data-i18n="benchmarkInterval1">1 分钟</option>
          <option value="5" data-i18n="benchmarkInterval5">5 分钟</option>
          <option value="15" data-i18n="benchmarkInterval15">15 分钟</option>
          <option value="30" data-i18n="benchmarkInterval30">30 分钟</option>
          <option value="60" data-i18n="benchmarkInterval60">1 小时</option>
        </select>
      </div>
      <div class="flex flex-col gap-1.5">
        <label class="text-[11px] font-bold text-outline dark:text-outline-variant uppercase tracking-wider" data-i18n="benchmarkTimeoutLabel">单模型超时(毫秒)</label>
        <input type="number" min="5000" max="120000" step="1000" id="benchmarkTimeoutInput" value="30000"
          class="px-3 py-1.5 text-[12px] font-mono bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all" />
      </div>
    </div>

    <!-- 测试提示词 -->
    <div class="flex flex-col gap-1.5">
      <label class="text-[11px] font-bold text-outline dark:text-outline-variant uppercase tracking-wider" data-i18n="benchmarkPromptLabel">测试提示词</label>
      <input type="text" id="benchmarkPromptInput" value="Hi"
        class="px-3 py-1.5 text-[12px] bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all" />
    </div>

    <!-- 启用开关 -->
    <div class="flex flex-col gap-1 pt-1">
      <label class="flex items-center gap-2 text-[12px] font-medium text-on-surface dark:text-white cursor-pointer select-none">
        <input type="checkbox" id="benchmarkEnabledToggle" class="rounded border-outline-variant/40 text-primary focus:ring-primary cursor-pointer" />
        <span data-i18n="benchmarkEnabledLabel">启用定时测速</span>
      </label>
      <span class="text-[10.5px] text-outline/80 dark:text-outline-variant/70 pl-6 select-none" data-i18n="benchmarkEnabledHint">未勾选时仅在仪表盘手动测速，不执行后台定时探测</span>
    </div>

    <template #footer>
      <button class="px-4 py-1.5 text-[12px] font-medium text-on-surface dark:text-white hover:bg-slate-100 dark:hover:bg-white/5 rounded-lg border border-outline-variant/40 transition-colors cursor-pointer select-none" id="btnBenchmarkConfigCancel" data-i18n="benchmarkCancel">取消</button>
      <button class="px-4 py-1.5 text-[12px] font-bold text-white bg-amber-500 hover:bg-amber-600 rounded-lg shadow-sm transition-colors flex items-center gap-1 disabled:opacity-50 cursor-pointer select-none" id="btnBenchmarkConfigSave">
        <span class="material-symbols-outlined text-[14px]">bolt</span>
        <span data-i18n="benchmarkSave">保存并测速</span>
      </button>
    </template>
  </BaseModal>
</template>

<script setup lang="ts">
import BaseModal from './BaseModal.vue';
</script>
