<template>
  <BaseModal
    id="otherAccountModal"
    containerId="otherAccountModalContainer"
    closeBtnId="btnOtherModalClose"
    title="添加 Other 号池账号"
    titleI18n="otherAddModalTitle"
    icon="hub"
    maxWidth="w-[560px] max-w-[95vw]"
    maxHeight="max-h-[85vh]"
    bodyClass="px-6 py-5 overflow-y-auto flex flex-col gap-4"
  >
    <div class="flex flex-col gap-1.5">
      <label class="text-[12px] font-medium text-on-surface dark:text-white" data-i18n="otherFieldGroupId">组 ID (英文标识)</label>
      <div class="flex items-center gap-2">
      <input type="text" id="inputOtherGroupId"
        placeholder="deepseek / openai / myrelay"
        data-i18n-placeholder="otherFieldGroupIdPlaceholder"
        class="flex-1 min-w-0 px-3 py-2 bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[13px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all font-mono" />
      <select id="selectOtherGroup"
        data-i18n-title="otherGroupSelectTitle"
        class="hidden w-40 shrink-0 px-2 py-2 bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[13px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all cursor-pointer">
        <option value="" data-i18n="otherGroupSelectPlaceholder">选择已有组...</option>
      </select>
    </div>
    <div class="text-[10px] text-outline mt-0.5">英文字母/数字/下划线,作为组唯一标识与模型前缀第二段(如 other/deepseek/...);不能与内置号池重名</div>
    </div>

    <div class="flex flex-col gap-1.5">
      <label class="text-[12px] font-medium text-on-surface dark:text-white" data-i18n="otherFieldGroupName">组名 (展示用)</label>
      <input type="text" id="inputOtherGroupName"
        placeholder="DeepSeek 上游"
        data-i18n-placeholder="otherFieldGroupNamePlaceholder"
        class="px-3 py-2 bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[13px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all" />
    </div>

    <div class="flex flex-col gap-1.5">
      <label class="text-[12px] font-medium text-on-surface dark:text-white" data-i18n="nvidiaFieldBaseUrl">Base URL (上游端点)</label>
      <input type="text" id="inputOtherBaseUrl"
        placeholder="https://api.deepseek.com/v1"
        data-i18n-placeholder="nvidiaFieldBaseUrlPlaceholder"
        class="px-3 py-2 bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[13px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all" />
    </div>

    <div class="flex flex-col gap-1.5">
      <label class="text-[12px] font-medium text-on-surface dark:text-white" data-i18n="nvidiaFieldApiKey">API Key</label>
      <PasswordInput
        inputId="inputOtherApiKey"
        placeholder="sk-..."
        dataI18nPlaceholder="nvidiaFieldApiKeyPlaceholder"
        revealProvider="other"
        inputClass="w-full px-3 py-2 pr-10 bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[13px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all"
      />
    </div>

    <div class="flex flex-col gap-1.5">
      <label class="text-[12px] font-medium text-on-surface dark:text-white" data-i18n="otherFieldFormats">兼容协议格式 (可多选)</label>
      <div class="flex flex-wrap gap-3 px-1">
        <label class="flex items-center gap-1.5 text-[13px] text-on-surface dark:text-white cursor-pointer select-none">
          <input type="checkbox" id="chkOtherFmtOpenai" class="w-4 h-4 rounded border-outline-variant/40 text-primary focus:ring-primary cursor-pointer" checked />
          <span data-i18n="otherFormatOpenai">OpenAI 格式</span>
        </label>
        <label class="flex items-center gap-1.5 text-[13px] text-on-surface dark:text-white cursor-pointer select-none">
          <input type="checkbox" id="chkOtherFmtAnthropic" class="w-4 h-4 rounded border-outline-variant/40 text-primary focus:ring-primary cursor-pointer" />
          <span data-i18n="otherFormatAnthropic">Anthropic 格式</span>
        </label>
      </div>
    </div>

    <div class="flex flex-col gap-1.5">
      <label class="text-[12px] font-medium text-on-surface dark:text-white" data-i18n="nvidiaFieldLabel">展示名 (可选)</label>
      <input type="text" id="inputOtherLabel"
        placeholder="DeepSeek 账号"
        data-i18n-placeholder="nvidiaFieldLabelPlaceholder"
        class="px-3 py-2 bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[13px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all" />
    </div>

    <div class="border-t border-outline-variant/20 pt-4">
      <div class="mb-2.5 flex items-center justify-between">
        <div>
          <div class="text-[12px] font-bold text-on-surface dark:text-white" data-i18n="nvidiaFieldDefaultModel">默认模型 (档位未命中时回退)</div>
          <div class="text-[11px] text-outline mt-0.5" data-i18n="otherItemDesc">勾选的格式决定探测端点,Anthropic-only 组无公开 /v1/models 时手填</div>
        </div>
        <button type="button" id="btnOtherFetchModels" class="px-2.5 py-1 text-[11px] font-bold text-primary bg-primary/10 hover:bg-primary/20 rounded-lg transition-colors flex items-center gap-1 shrink-0 cursor-pointer">
          <span class="material-symbols-outlined text-[14px]">download</span>
          <span data-i18n="otherFetchModels">获取模型</span>
        </button>
      </div>
      <div class="flex items-center gap-1.5">
        <input type="text" id="inputOtherModelDefault" placeholder="deepseek-chat"
          class="flex-1 min-w-0 px-3 py-1.5 bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[12px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all" />
        <select id="selectOtherModelDefault" class="hidden w-36 shrink-0 px-2 py-1.5 bg-slate-100 dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[11px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all cursor-pointer">
          <option value="">选择模型...</option>
        </select>
      </div>
    </div>

    <div id="otherModalError" class="hidden text-[12px] text-red-500 bg-red-500/10 border border-red-500/20 rounded-lg px-3 py-2"></div>

    <template #footer>
      <button class="px-4 py-2 text-[13px] font-medium text-on-surface dark:text-white hover:bg-slate-100 dark:hover:bg-white/5 rounded-lg transition-colors border border-outline-variant/30 cursor-pointer" id="btnOtherModalCancel" data-i18n="otherModalCancel">取消</button>
      <button class="px-4 py-2 text-[13px] font-bold text-white bg-primary hover:bg-primary/90 rounded-lg transition-colors shadow-sm disabled:opacity-50 disabled:pointer-events-none cursor-pointer" id="btnOtherModalSave" data-i18n="otherModalSave">添加账号</button>
    </template>
  </BaseModal>
</template>

<script setup lang="ts">
import BaseModal from './BaseModal.vue';
import PasswordInput from './PasswordInput.vue';
</script>
