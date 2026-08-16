<template>
  <BaseModal
    id="nvidiaBatchAssignIPModal"
    containerId="nvidiaBatchAssignIPModalContainer"
    closeBtnId="btnCloseNvidiaBatchAssignIPModal"
    maxWidth="w-[880px] max-w-[95vw]"
    maxHeight="max-h-[92vh]"
    bodyClass="p-6 overflow-y-auto flex flex-col gap-4 select-none"
  >
    <template #header-icon>
      <span class="material-symbols-outlined text-amber-500 text-[22px] shrink-0">public</span>
    </template>

    <template #header-title>
      <div class="flex flex-col min-w-0">
        <span class="text-[15px] font-bold text-on-surface dark:text-white truncate" data-i18n="batchAssignIPModalTitle">批量分配出口住宅 IP</span>
        <span class="text-[11px] text-outline truncate" data-i18n="batchAssignIPModalDesc">支持多网段独立打散或全池统一分配同一个公网住宅/出口伪装 IP。</span>
      </div>
    </template>

    <!-- 分配模式选择 (卡片单选) -->
    <div class="flex flex-col gap-2">
      <span class="text-[13px] font-bold text-on-surface dark:text-white" data-i18n="batchAssignModeTitle">分配模式</span>
      <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
        <label id="lblModeCardUnique" class="flex items-start gap-2.5 p-3 rounded-xl border border-primary/40 bg-primary/5 dark:bg-primary/10 cursor-pointer transition-all">
          <input type="radio" name="assignMode" value="unique" checked class="mt-0.5 text-primary focus:ring-primary cursor-pointer" id="radAssignModeUnique" />
          <div class="flex flex-col">
            <span class="text-[12px] font-bold text-on-surface dark:text-white flex items-center gap-1">
              <span class="material-symbols-outlined text-[15px] text-primary">shuffle</span>
              <span data-i18n="batchAssignModeUnique">多网段独立打散 (每号独立 IP)</span>
            </span>
            <span class="text-[10.5px] text-outline leading-relaxed mt-0.5" data-i18n="batchAssignModeUniqueDesc">从所选网段中为每个账号随机生成互不重复的住宅 IP，确保全球分布均匀。</span>
          </div>
        </label>
        <label id="lblModeCardSingle" class="flex items-start gap-2.5 p-3 rounded-xl border border-outline-variant/30 hover:border-primary/50 cursor-pointer transition-all bg-white dark:bg-white/5">
          <input type="radio" name="assignMode" value="single" class="mt-0.5 text-primary focus:ring-primary cursor-pointer" id="radAssignModeSingle" />
          <div class="flex flex-col">
            <span class="text-[12px] font-bold text-on-surface dark:text-white flex items-center gap-1">
              <span class="material-symbols-outlined text-[15px] text-amber-500">pin</span>
              <span data-i18n="batchAssignModeSingle">统一分配单 IP (所有账号共用同一 IP)</span>
            </span>
            <span class="text-[10.5px] text-outline leading-relaxed mt-0.5" data-i18n="batchAssignModeSingleDesc">为号池目标账号配置完全相同的出口 IP，支持手动输入或选择网段一键随机生成。</span>
          </div>
        </label>
      </div>
    </div>

    <!-- 模式 A：多网段独立打散面板 -->
    <div id="modeUniqueContainer" class="flex flex-col gap-2.5">
      <div class="flex items-center justify-between">
        <span class="text-[13px] font-bold text-on-surface dark:text-white flex items-center gap-1">
          <span>住宅 ISP 网段</span>
          <span class="text-[11px] font-normal text-outline">（已选 <span id="lblSelectedSubnetCount" class="font-bold text-primary">0</span> 个）</span>
        </span>
        <div class="flex items-center gap-2">
          <button id="btnSelectAllSubnets" type="button" class="text-[11px] text-primary hover:underline cursor-pointer font-medium" data-i18n="batchAssignSelectAll">全选</button>
          <span class="text-outline/30">|</span>
          <button id="btnDeselectAllSubnets" type="button" class="text-[11px] text-outline hover:underline cursor-pointer font-medium" data-i18n="batchAssignDeselectAll">全不选</button>
        </div>
      </div>

      <!-- 网段卡片多选列表 -->
      <div
        id="subnetListContainer"
        class="grid grid-cols-1 sm:grid-cols-2 gap-2.5 max-h-[280px] overflow-y-auto p-2 border border-outline-variant/20 rounded-xl bg-slate-50/50 dark:bg-black/20 overscroll-contain will-change-scroll"
      >
        <!-- 动态渲染多选网段选项 -->
      </div>
    </div>

    <!-- 模式 B：统一分配单 IP 面板 (默认隐藏) -->
    <div id="modeSingleContainer" class="flex flex-col gap-3 hidden">
      <!-- 手动输入与快速生成栏 -->
      <div class="flex flex-col gap-1.5 p-3.5 rounded-xl border border-outline-variant/20 bg-slate-50/60 dark:bg-white/5">
        <label class="text-[12px] font-bold text-on-surface dark:text-white flex items-center justify-between">
          <span data-i18n="batchAssignSingleIpLabel">目标出口 IP 地址 (IPv4)</span>
          <span id="lblSingleIPValidationTip" class="text-[11px] font-normal text-outline"></span>
        </label>
        <div class="flex items-center gap-2">
          <div class="relative flex-1">
            <span class="material-symbols-outlined absolute left-3 top-1/2 -translate-y-1/2 text-outline text-[18px]">public</span>
            <input
              type="text"
              id="inputBatchAssignSingleIP"
              placeholder="例如 108.85.12.34 (可直接输入，或点击下方任一网段快速生成)"
              data-i18n-placeholder="batchAssignSingleIpPlaceholder"
              class="w-full pl-9 pr-3.5 py-2 bg-white dark:bg-black/30 border border-outline-variant/30 rounded-lg text-[12.5px] font-mono focus:outline-none focus:border-primary focus:ring-1 focus:ring-primary text-on-surface dark:text-white transition-all shadow-2xs"
            />
          </div>
          <button
            id="btnBatchAssignRandomSingleIP"
            type="button"
            class="px-3.5 py-2 bg-amber-500/10 hover:bg-amber-500/20 text-amber-600 dark:text-amber-400 border border-amber-500/30 rounded-lg text-[12px] font-bold flex items-center gap-1.5 transition-colors cursor-pointer shrink-0"
            title="从选定网段重新随机生成一个 IP"
          >
            <span class="material-symbols-outlined text-[16px]">casino</span>
            <span data-i18n="batchAssignRandomOneBtn">🎲 随机换一个</span>
          </button>
        </div>
      </div>

      <!-- 网段卡片单选列表 (点击即生成) -->
      <div class="flex flex-col gap-1.5">
        <span class="text-[12px] font-bold text-on-surface dark:text-white" data-i18n="batchAssignSubnetListSingleTitle">可选住宅网段 (点击快速生成并填入)</span>
        <div
          id="singleSubnetListContainer"
          class="grid grid-cols-1 sm:grid-cols-2 gap-2 max-h-[220px] overflow-y-auto p-2 border border-outline-variant/20 rounded-xl bg-slate-50/50 dark:bg-black/20 overscroll-contain will-change-scroll"
        >
          <!-- 动态渲染单选网段选项 -->
        </div>
      </div>
    </div>

    <!-- 分配策略 -->
    <div class="flex flex-col gap-2 border-t border-outline-variant/15 pt-3">
      <span class="text-[13px] font-bold text-on-surface dark:text-white" data-i18n="batchAssignStrategyTitle">分配策略</span>
      <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
        <label class="flex items-start gap-2.5 p-3 rounded-xl border border-outline-variant/30 hover:border-primary/50 cursor-pointer transition-all bg-white dark:bg-white/5">
          <input type="radio" name="assignStrategy" value="overwrite" checked class="mt-0.5 text-primary focus:ring-primary cursor-pointer" id="radStrategyOverwrite" />
          <div class="flex flex-col">
            <span class="text-[12px] font-bold text-on-surface dark:text-white" data-i18n="batchAssignOverwriteAll">覆盖全部账号（重新打散/统一分配）</span>
            <span class="text-[10px] text-outline leading-relaxed mt-0.5">所有 NVIDIA 账号均更新为新生成的出口住宅 IP。</span>
          </div>
        </label>
        <label class="flex items-start gap-2.5 p-3 rounded-xl border border-outline-variant/30 hover:border-primary/50 cursor-pointer transition-all bg-white dark:bg-white/5">
          <input type="radio" name="assignStrategy" value="blank_only" class="mt-0.5 text-primary focus:ring-primary cursor-pointer" id="radStrategyBlankOnly" />
          <div class="flex flex-col">
            <span class="text-[12px] font-bold text-on-surface dark:text-white" data-i18n="batchAssignOnlyBlank">仅为空白未配置 IP 的账号分配</span>
            <span class="text-[10px] text-outline leading-relaxed mt-0.5">保留已经配置好 IP 的账号，仅为当前未配置出口 IP 的账号补充生成。</span>
          </div>
        </label>
      </div>
    </div>

    <template #footer>
      <div class="flex items-center justify-between w-full">
        <span class="text-[11px] text-outline truncate" id="lblAssignAccountTargetInfo">号池共有 0 个 NVIDIA 账号</span>
        <div class="flex items-center gap-3 shrink-0">
          <button id="btnCancelNvidiaBatchAssignIP" type="button" class="px-4 py-2 text-[12px] font-medium text-slate-600 dark:text-slate-300 hover:bg-slate-100 dark:hover:bg-white/5 rounded-lg transition-colors border border-outline-variant/30 cursor-pointer">
            取消
          </button>
          <button id="btnConfirmNvidiaBatchAssignIP" type="button" class="px-5 py-2 text-[12px] font-bold text-white bg-primary hover:bg-primary/90 rounded-lg shadow-sm transition-all flex items-center gap-1.5 cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed">
            <span class="material-symbols-outlined text-[16px]">shuffle</span>
            <span data-i18n="batchAssignConfirm">确定分配</span>
          </button>
        </div>
      </div>
    </template>
  </BaseModal>
</template>

<script setup lang="ts">
import BaseModal from './BaseModal.vue';
</script>

