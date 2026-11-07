<template>
  <BaseModal
    id="accountModelsModal"
    containerId="accountModelsModalContainer"
    closeBtnId="btnAccountModelsModalClose"
    icon="fact_check"
    maxWidth="w-[880px] max-w-[95vw]"
    maxHeight="max-h-[90vh]"
    bodyClass="p-5 overflow-y-auto flex-grow space-y-4 max-h-[70vh]"
  >
    <template #header-title>
      <span class="text-sm font-bold text-on-surface dark:text-white" data-i18n="accountModelsModalTitle">账号可用模型检测结果</span>
      <span class="text-[11px] font-medium text-primary dark:text-primary-fixed-dim bg-primary/10 px-1.5 py-0.5 rounded-md" id="accountModelsSummaryCount">共 0 个账号</span>
    </template>

    <!-- 顶部状态与自动跳过生效说明横幅 -->
    <div class="bg-primary/5 dark:bg-primary/10 p-3.5 rounded-xl border border-primary/20 flex items-start gap-2.5 text-[12px] leading-relaxed text-on-surface dark:text-slate-200">
      <span class="material-symbols-outlined text-primary text-[18px] flex-shrink-0 mt-0.5">verified_user</span>
      <div class="space-y-1">
        <div class="font-bold text-primary dark:text-primary-fixed-dim">
          模型权限已识别 · 自动跳过机制已实时生效
        </div>
        <div class="text-[11.5px] text-outline dark:text-slate-300">
          系统已精准画像各个账号的可用模型列表。后续向上游请求指定模型时（例如 <span class="font-mono text-purple-600 dark:text-purple-400 font-semibold">Gemini 4 Argon</span> 或 <span class="font-mono text-amber-600 dark:text-amber-400 font-semibold">Claude Opus 5.5</span> 系列），负载均衡与中继将<strong class="text-emerald-600 dark:text-emerald-400">自动跳过不支持该模型的账号</strong>，直接分配至可用账号，杜绝 404 错误与频繁换号风控。
        </div>
      </div>
    </div>

    <!-- 核心模型权限概览徽章卡片 -->
    <div class="grid grid-cols-1 sm:grid-cols-3 gap-3 text-[12px]">
      <div class="bg-slate-50 dark:bg-slate-900/30 p-3 rounded-xl border border-outline-variant/20 flex flex-col justify-between gap-1">
        <span class="text-outline dark:text-outline-variant text-[11px] font-medium">检测账号总数</span>
        <div class="text-lg font-bold text-on-surface dark:text-white font-mono" id="statProbeTotalAccounts">0</div>
      </div>
      <div class="bg-slate-50 dark:bg-slate-900/30 p-3 rounded-xl border border-outline-variant/20 flex flex-col justify-between gap-1">
        <span class="text-outline dark:text-outline-variant text-[11px] font-medium">Gemini 4 Argon 可用账号</span>
        <div class="text-lg font-bold text-purple-600 dark:text-purple-400 font-mono" id="statProbeArgonCount">0</div>
      </div>
      <div class="bg-slate-50 dark:bg-slate-900/30 p-3 rounded-xl border border-outline-variant/20 flex flex-col justify-between gap-1">
        <span class="text-outline dark:text-outline-variant text-[11px] font-medium">Claude Opus 5.5 可用账号</span>
        <div class="text-lg font-bold text-amber-600 dark:text-amber-400 font-mono" id="statProbeOpus55Count">0</div>
      </div>
    </div>

    <!-- 检测进度加载或错误信息 -->
    <div id="accountModelsLoadingBar" class="hidden flex items-center justify-center p-6 text-[12px] text-outline">
      <span class="inline-block animate-spin mr-2">⏳</span>
      <span>正在逐个探测账号真实模型权限并隔离连接，请稍候...</span>
    </div>

    <!-- 账号检测明细列表 -->
    <div class="space-y-2.5" id="accountModelsListContainer">
      <div class="overflow-x-auto rounded-xl border border-outline-variant/30 bg-slate-50/20 dark:bg-slate-950/10">
        <table class="w-full text-left border-collapse table-fixed text-[12px]">
          <thead>
            <tr class="bg-slate-50 dark:bg-[#1a1f30] text-outline dark:text-outline-variant text-[11px] border-b border-outline-variant/30 sticky top-0 z-10">
              <th class="p-3 font-bold w-[30%]">账号邮箱 / 层级</th>
              <th class="p-3 font-bold w-[46%]">不可用模型 (请求时自动跳过)</th>
              <th class="p-3 font-bold text-center w-[24%]">可用模型详情</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-outline-variant/10 text-on-surface dark:text-slate-200" id="accountModelsTableBody">
            <!-- 由 accountModelsModal.ts 动态填充 -->
            <tr>
              <td colspan="3" class="p-8 text-center text-outline italic">
                点击下方【重新检测】开始探测各账号模型权限
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <template #footer>
      <div class="flex justify-between items-center w-full">
        <button
          type="button"
          id="btnAccountModelsReProbe"
          class="px-3.5 py-1.5 text-[12px] font-bold text-primary dark:text-primary-fixed-dim bg-primary/10 hover:bg-primary/20 border border-primary/30 rounded-lg transition-all flex items-center gap-1.5 select-none cursor-pointer"
        >
          <span class="material-symbols-outlined text-[15px]" id="iconAccountModelsReProbe">refresh</span>
          <span>重新检测模型</span>
        </button>
        <button
          type="button"
          id="btnAccountModelsCloseSecondary"
          class="px-4 py-1.5 text-[12px] font-bold bg-slate-100 hover:bg-slate-200 dark:bg-white/5 dark:hover:bg-white/10 text-on-surface dark:text-white rounded-lg transition-colors border border-outline-variant/40 cursor-pointer"
        >
          关闭
        </button>
      </div>
    </template>
  </BaseModal>
</template>

<script setup lang="ts">
import BaseModal from './BaseModal.vue';
</script>
