<template>
  <BaseModal
    id="triggerTestModal"
    containerId="triggerTestModalContainer"
    closeBtnId="btnTriggerModalClose"
    icon="bolt"
    maxWidth="w-[720px] max-w-[95vw]"
    maxHeight="max-h-[90vh]"
    bodyClass="p-6 overflow-y-auto flex-grow space-y-4 max-h-[70vh]"
  >
    <template #header-title>
      <span class="text-sm font-bold text-on-surface dark:text-white" data-i18n="triggerTestModalTitle">触发配额刷新测试</span>
      <span class="text-[11px] font-medium text-primary dark:text-primary-fixed-dim bg-primary/10 px-1.5 py-0.5 rounded-md" id="triggerModalAccountCount">已选择 0 个账号</span>
    </template>

    <!-- 配置表单区域 -->
    <div id="triggerConfigSection" class="space-y-4">
      <div>
        <label class="block text-[12px] font-bold text-outline dark:text-outline-variant mb-1.5" data-i18n="labelTriggerPrompt">1. 测试触发词 (Prompt)</label>
        <input type="text" id="inputTriggerPrompt" value="ok" class="w-full px-3 py-1.5 bg-slate-50 dark:bg-[#1a1f30] border border-outline-variant/40 rounded-lg text-[12px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all" placeholder="生成内容请求所使用的 Prompt，默认为 ok" data-i18n-placeholder="placeholderTriggerPrompt" />
      </div>

      <div>
        <div class="flex items-center justify-between mb-1.5">
          <label class="block text-[12px] font-bold text-outline dark:text-outline-variant" data-i18n="labelTriggerModels">2. 选择测试模型</label>
          <div class="flex items-center gap-2 text-[11px]">
            <button type="button" id="btnTriggerFetchModels" class="flex items-center gap-0.5 text-primary hover:underline font-medium cursor-pointer" title="点击调用接口获取最新可用模型">
              <span class="material-symbols-outlined text-[13px]" id="iconTriggerFetchModels">sync</span>
              <span id="textTriggerFetchModels">获取最新模型</span>
            </button>
            <span class="text-outline/30">|</span>
            <button type="button" id="btnTriggerModalSelectAll" class="text-primary dark:text-primary-fixed-dim hover:underline font-medium cursor-pointer" data-i18n="btnSelectAll">全选</button>
            <span class="text-outline/30">|</span>
            <button type="button" id="btnTriggerModalClearAll" class="text-outline hover:text-primary hover:underline font-medium cursor-pointer" data-i18n="btnClearAll">清空</button>
          </div>
        </div>
        
        <!-- 模型选择网格 -->
        <div class="grid grid-cols-3 gap-4 p-3 bg-slate-50/50 dark:bg-slate-900/20 border border-outline-variant/30 rounded-xl max-h-48 overflow-y-auto text-[11.5px] text-on-surface dark:text-slate-200">
          <!-- Gemini Models -->
          <div class="space-y-1">
            <div class="font-bold text-[10.5px] text-outline uppercase tracking-wider pb-1 border-b border-outline-variant/10">Gemini Models</div>
            <div class="space-y-0.5 mt-1" id="triggerModelsGemini"></div>
          </div>

          <!-- Claude Models -->
          <div class="space-y-1">
            <div class="font-bold text-[10.5px] text-outline uppercase tracking-wider pb-1 border-b border-outline-variant/10">Claude Models</div>
            <div class="space-y-0.5 mt-1" id="triggerModelsClaude"></div>
          </div>

          <!-- Others -->
          <div class="space-y-1">
            <div class="font-bold text-[10.5px] text-outline uppercase tracking-wider pb-1 border-b border-outline-variant/10">Others</div>
            <div class="space-y-0.5 mt-1" id="triggerModelsOthers"></div>
          </div>
        </div>
        <div id="triggerModelsStatusMsg" class="text-[10.5px] text-outline dark:text-outline-variant mt-1.5 hidden flex items-center gap-1"></div>
      </div>
    </div>

    <!-- 实时日志区域 -->
    <div>
      <label class="block text-[12px] font-bold text-outline dark:text-outline-variant mb-1.5" data-i18n="labelLiveLogs">进程实时日志</label>
      <div id="triggerLogsArea" class="bg-slate-900 dark:bg-slate-950 text-slate-300 font-mono text-[11px] p-3.5 rounded-xl h-44 overflow-y-auto border border-outline-variant/15 leading-relaxed selection:bg-primary/30">
        <div class="text-outline dark:text-outline-variant italic" data-i18n="waitingConfigTrigger">等待配置并开始触发...</div>
      </div>
    </div>

    <!-- 汇总表格区域 -->
    <div id="triggerResultsContainer" class="hidden animate-fadeIn space-y-2">
      <label class="block text-[12px] font-bold text-outline dark:text-outline-variant" data-i18n="labelTriggerResults">触发结果汇总</label>
      <div class="overflow-x-auto rounded-xl border border-outline-variant/30 max-h-56 overflow-y-auto bg-slate-50/20 dark:bg-slate-950/10">
        <table class="w-full text-left border-collapse table-fixed text-[11.5px]">
          <thead>
            <tr class="bg-slate-50 dark:bg-[#1a1f30] text-outline border-b border-outline-variant/30 sticky top-0 z-10">
              <th class="p-2.5 font-bold w-[35%]" data-i18n="thResultAccount">账号</th>
              <th class="p-2.5 font-bold w-[25%]" data-i18n="thResultModel">所试模型</th>
              <th class="p-2.5 font-bold text-center w-[15%]" data-i18n="thResultStatus">状态</th>
              <th class="p-2.5 font-bold w-[25%]" data-i18n="thResultDetail">详情/错误</th>
            </tr>
          </thead>
          <tbody id="triggerResultsTableBody" class="divide-y divide-outline-variant/10 text-on-surface dark:text-slate-200">
            <!-- JS 动态填充 -->
          </tbody>
        </table>
      </div>
    </div>

    <template #footer>
      <button class="px-4 py-1.5 text-[12px] font-bold bg-slate-100 hover:bg-slate-200 dark:bg-white/5 dark:hover:bg-white/10 text-on-surface dark:text-white rounded-lg transition-colors border border-outline-variant/40 cursor-pointer select-none" id="btnTriggerModalCancel" data-i18n="btnCancel">取消</button>
      <button class="flex items-center gap-1.5 px-4 py-1.5 bg-primary text-white hover:bg-primary/90 disabled:opacity-60 disabled:cursor-not-allowed rounded-lg text-[12px] font-bold transition-all shadow-sm cursor-pointer select-none" id="btnStartTriggerTest">
        <span class="material-symbols-outlined text-[15px]" id="btnStartTriggerIcon">play_arrow</span>
        <span data-i18n="btnStartTrigger">开始触发</span>
      </button>
    </template>
  </BaseModal>
</template>

<script setup lang="ts">
import BaseModal from './BaseModal.vue';
</script>

<style scoped>
@keyframes fadeIn {
    from { opacity: 0; transform: translateY(4px); }
    to { opacity: 1; transform: translateY(0); }
}
.animate-fadeIn {
    animation: fadeIn 0.25s ease-out forwards;
}
</style>

