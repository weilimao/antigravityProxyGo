<template>
  <BaseModal
    id="nvidiaBatchAssignIPModal"
    containerId="nvidiaBatchAssignIPModalContainer"
    closeBtnId="btnCloseNvidiaBatchAssignIPModal"
    maxWidth="w-[860px] max-w-[95vw]"
    maxHeight="max-h-[90vh]"
    bodyClass="p-6 overflow-y-auto flex flex-col gap-5 select-none"
  >
    <template #header-icon>
      <span class="material-symbols-outlined text-amber-500 text-[22px] shrink-0">public</span>
    </template>

    <template #header-title>
      <div class="flex flex-col min-w-0">
        <span class="text-[15px] font-bold text-on-surface dark:text-white truncate" data-i18n="batchAssignIPModalTitle">批量分配独立住宅 IP</span>
        <span class="text-[11px] text-outline truncate" data-i18n="batchAssignIPModalDesc">选择目标运营商住宅网段，系统将随机生成互不重复的公网住宅 IP 分发给号池账号。</span>
      </div>
    </template>

    <!-- 网段选择与工具条 -->
    <div class="flex flex-col gap-2.5">
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

      <!-- 网段卡片列表 (开启硬件加速与 overscroll-contain，消除嵌套滚动冲突与卡顿) -->
      <div
        id="subnetListContainer"
        class="grid grid-cols-1 sm:grid-cols-2 gap-2.5 max-h-[340px] overflow-y-auto p-2 border border-outline-variant/20 rounded-xl bg-slate-50/50 dark:bg-black/20 overscroll-contain will-change-scroll"
      >
        <!-- 动态渲染网段选项 -->
      </div>
    </div>

    <!-- 分配策略 -->
    <div class="flex flex-col gap-2 border-t border-outline-variant/15 pt-4">
      <span class="text-[13px] font-bold text-on-surface dark:text-white" data-i18n="batchAssignStrategyTitle">分配策略</span>
      <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
        <label class="flex items-start gap-2.5 p-3 rounded-xl border border-outline-variant/30 hover:border-primary/50 cursor-pointer transition-all bg-white dark:bg-white/5">
          <input type="radio" name="assignStrategy" value="overwrite" checked class="mt-0.5 text-primary focus:ring-primary cursor-pointer" id="radStrategyOverwrite" />
          <div class="flex flex-col">
            <span class="text-[12px] font-bold text-on-surface dark:text-white" data-i18n="batchAssignOverwriteAll">覆盖全部账号（重新打散分配）</span>
            <span class="text-[10px] text-outline leading-relaxed mt-0.5">所有 NVIDIA 账号均重新分配一个新的独立住宅 IP，确保全球分布均匀。</span>
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
