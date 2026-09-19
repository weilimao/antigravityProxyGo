<template>
  <BaseModal
    id="opencodeAccountModal"
    containerId="opencodeAccountModalContainer"
    closeBtnId="btnOpenCodeModalClose"
    title="添加 OpenCode 号池账号"
    titleI18n="opencodeAddModalTitle"
    icon="terminal"
    maxWidth="w-[560px] max-w-[95vw]"
    maxHeight="max-h-[85vh]"
    bodyClass="px-6 py-5 overflow-y-auto flex flex-col gap-4"
    @close="closeOpenCodeAccountModal"
  >

    <!-- 录入模式切换 Tab (单个 / 批量) -->
    <div id="opencodeModalTabs" class="flex items-center justify-center gap-1 bg-slate-100 dark:bg-white/5 p-1 rounded-lg text-[12px]">
      <button type="button" id="tabOpenCodeSingle" class="px-3 py-1.5 rounded-lg text-[12px] font-bold bg-white dark:bg-[#1f293d] text-primary dark:text-primary-fixed-dim shadow-sm cursor-pointer">
        <span>单个录入 / 编辑</span>
      </button>
      <button type="button" id="tabOpenCodeBatch" class="px-3 py-1.5 rounded-lg text-[12px] font-medium text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-200 cursor-pointer">
        <span>批量导入 (多 Key)</span>
      </button>
    </div>

    <!-- 单个录入区域 -->
    <div id="sectionOpenCodeSingle" class="flex flex-col gap-3">
      <div class="flex flex-col gap-1.5">
        <label class="text-[12px] font-medium text-on-surface dark:text-white" data-i18n="opencodeFieldToken">API Key (OpenCode Zen sk-...)</label>
        <PasswordInput
          inputId="inputOpenCodeApiKey"
          placeholder="sk-..."
          revealProvider="opencode"
          inputClass="w-full px-3 py-2 pr-10 bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[13px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all font-mono"
        />
      </div>

      <div class="flex flex-col gap-1.5">
        <label class="text-[12px] font-medium text-on-surface dark:text-white" data-i18n="opencodeFieldLabel">展示名 / 备注 (可选)</label>
        <input type="text" id="inputOpenCodeLabel"
          placeholder="例如: 主力 Key 1"
          class="px-3 py-2 bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[13px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all" />
      </div>

    </div>

    <!-- 批量导入区域 -->
    <div id="sectionOpenCodeBatch" class="hidden flex flex-col gap-3">
      <div class="flex flex-col gap-1.5">
        <label class="text-[12px] font-medium text-on-surface dark:text-white">批量 API Key 列表 (每行一个 sk-...)</label>
        <textarea id="inputOpenCodeBatchKeys" rows="6"
          placeholder="sk-xxx1&#10;sk-xxx2&#10;sk-xxx3"
          class="w-full px-3 py-2 bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[12px] text-on-surface dark:text-white focus:outline-none focus:border-primary font-mono transition-all"></textarea>
      </div>
      <p class="text-[11px] text-slate-500 dark:text-slate-400">
        每行粘贴一个由 OpenCode Zen 签发的 API Key，系统将自动校验并并发入库。
      </p>
    </div>

    <!-- 错误信息提示框 -->
    <div id="opencodeModalError" class="hidden p-3 rounded-lg text-[12px] bg-rose-500/10 border border-rose-500/30 text-rose-600 dark:text-rose-400"></div>

    <!-- 底部操作按钮 -->
    <div class="flex items-center justify-end gap-2 pt-2 border-t border-outline-variant/20">
      <button type="button" id="btnOpenCodeCancel"
        class="px-4 py-2 text-[13px] font-medium text-on-surface dark:text-white hover:bg-slate-100 dark:hover:bg-white/5 rounded-lg transition-colors cursor-pointer"
        data-i18n="btnCancel">取消</button>
      <button type="button" id="btnOpenCodeSave"
        class="px-5 py-2 text-[13px] font-bold text-white bg-primary hover:bg-primary/90 rounded-lg shadow-sm transition-colors cursor-pointer flex items-center gap-1.5"
        data-i18n="btnSaveAccount">
        <span class="material-symbols-outlined text-[16px]">check</span>
        <span>保存账号</span>
      </button>
    </div>
  </BaseModal>
</template>

<script setup lang="ts">
import BaseModal from './BaseModal.vue'
import PasswordInput from './PasswordInput.vue'
import { closeOpenCodeAccountModal } from '../../ui/opencodeAccountModal'
</script>
