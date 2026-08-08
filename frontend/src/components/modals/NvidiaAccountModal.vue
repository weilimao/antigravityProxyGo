<template>
  <BaseModal
    id="nvidiaAccountModal"
    containerId="nvidiaAccountModalContainer"
    closeBtnId="btnNvidiaModalClose"
    title="添加 NVIDIA 号池账号"
    titleI18n="nvidiaAddModalTitle"
    icon="bolt"
    maxWidth="w-[560px] max-w-[95vw]"
    maxHeight="max-h-[85vh]"
    bodyClass="px-6 py-5 overflow-y-auto flex flex-col gap-4"
  >
    <div class="flex flex-col gap-1.5">
      <label class="text-[12px] font-medium text-on-surface dark:text-white" data-i18n="nvidiaFieldBaseUrl">Base URL (上游端点)</label>
      <input type="text" id="inputNvidiaBaseUrl"
        placeholder="https://integrate.api.nvidia.com/v1"
        data-i18n-placeholder="nvidiaFieldBaseUrlPlaceholder"
        class="px-3 py-2 bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[13px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all" />
    </div>

    <div class="flex flex-col gap-1.5">
      <label class="text-[12px] font-medium text-on-surface dark:text-white" data-i18n="nvidiaFieldApiKey">API Key</label>
      <PasswordInput
        inputId="inputNvidiaApiKey"
        placeholder="nvapi-..."
        dataI18nPlaceholder="nvidiaFieldApiKeyPlaceholder"
        revealProvider="nvidia"
        inputClass="w-full px-3 py-2 pr-10 bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[13px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all"
      />
    </div>

    <div class="flex flex-col gap-1.5">
      <label class="text-[12px] font-medium text-on-surface dark:text-white" data-i18n="nvidiaFieldLabel">展示名 (可选)</label>
      <input type="text" id="inputNvidiaLabel"
        placeholder="NVIDIA 账号"
        data-i18n-placeholder="nvidiaFieldLabelPlaceholder"
        class="px-3 py-2 bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[13px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all" />
    </div>

    <div class="border-t border-outline-variant/20 pt-4">
      <div class="mb-2.5 flex items-center justify-between">
        <div>
          <div class="text-[12px] font-bold text-on-surface dark:text-white" data-i18n="nvidiaModelMappingTitle">模型档位映射 (可选，留空用默认)</div>
          <div class="text-[11px] text-outline mt-0.5" data-i18n="nvidiaModelMappingDesc">按 Claude Code 调用的档位名映射到上游 NVIDIA 模型 id</div>
        </div>
        <button type="button" id="btnNvidiaFetchModels" class="px-2.5 py-1 text-[11px] font-bold text-primary bg-primary/10 hover:bg-primary/20 rounded-lg transition-colors flex items-center gap-1 shrink-0 cursor-pointer">
          <span class="material-symbols-outlined text-[14px]">download</span>
          <span>获取模型</span>
        </button>
      </div>
      <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
        <div class="flex flex-col gap-1">
          <label class="text-[11px] font-medium text-outline">Sonnet</label>
          <div class="flex items-center gap-1.5">
            <input type="text" id="inputNvidiaModelSonnet" placeholder="moonshotai/kimi-k2.5"
              class="flex-1 min-w-0 px-3 py-1.5 bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[12px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all" />
            <select id="selectNvidiaModelSonnet" class="hidden w-28 shrink-0 px-2 py-1.5 bg-slate-100 dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[11px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all cursor-pointer">
              <option value="">选择模型...</option>
            </select>
          </div>
        </div>
        <div class="flex flex-col gap-1">
          <label class="text-[11px] font-medium text-outline">Opus</label>
          <div class="flex items-center gap-1.5">
            <input type="text" id="inputNvidiaModelOpus" placeholder="moonshotai/kimi-k2.5"
              class="flex-1 min-w-0 px-3 py-1.5 bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[12px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all" />
            <select id="selectNvidiaModelOpus" class="hidden w-28 shrink-0 px-2 py-1.5 bg-slate-100 dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[11px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all cursor-pointer">
              <option value="">选择模型...</option>
            </select>
          </div>
        </div>
        <div class="flex flex-col gap-1">
          <label class="text-[11px] font-medium text-outline">Haiku</label>
          <div class="flex items-center gap-1.5">
            <input type="text" id="inputNvidiaModelHaiku" placeholder="meta/llama-3.3-70b-instruct"
              class="flex-1 min-w-0 px-3 py-1.5 bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[12px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all" />
            <select id="selectNvidiaModelHaiku" class="hidden w-28 shrink-0 px-2 py-1.5 bg-slate-100 dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[11px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all cursor-pointer">
              <option value="">选择模型...</option>
            </select>
          </div>
        </div>
        <div class="flex flex-col gap-1">
          <label class="text-[11px] font-medium text-outline">Fable</label>
          <div class="flex items-center gap-1.5">
            <input type="text" id="inputNvidiaModelFable" placeholder="nvidia/llama-3.1-nemotron-70b-instruct"
              class="flex-1 min-w-0 px-3 py-1.5 bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[12px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all" />
            <select id="selectNvidiaModelFable" class="hidden w-28 shrink-0 px-2 py-1.5 bg-slate-100 dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[11px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all cursor-pointer">
              <option value="">选择模型...</option>
            </select>
          </div>
        </div>
        <div class="flex flex-col gap-1 sm:col-span-2">
          <label class="text-[11px] font-medium text-outline" data-i18n="nvidiaFieldDefaultModel">默认模型 (档位未命中时回退)</label>
          <div class="flex items-center gap-1.5">
            <input type="text" id="inputNvidiaModelDefault" placeholder="moonshotai/kimi-k2.5"
              class="flex-1 min-w-0 px-3 py-1.5 bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[12px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all" />
            <select id="selectNvidiaModelDefault" class="hidden w-36 shrink-0 px-2 py-1.5 bg-slate-100 dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[11px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all cursor-pointer">
              <option value="">选择模型...</option>
            </select>
          </div>
        </div>
      </div>
    </div>

    <div id="nvidiaModalError" class="hidden text-[12px] text-red-500 bg-red-500/10 border border-red-500/20 rounded-lg px-3 py-2"></div>

    <template #footer>
      <button class="px-4 py-2 text-[13px] font-medium text-on-surface dark:text-white hover:bg-slate-100 dark:hover:bg-white/5 rounded-lg transition-colors border border-outline-variant/30 cursor-pointer" id="btnNvidiaModalCancel" data-i18n="nvidiaModalCancel">取消</button>
      <button class="px-4 py-2 text-[13px] font-bold text-white bg-primary hover:bg-primary/90 rounded-lg transition-colors shadow-sm disabled:opacity-50 disabled:pointer-events-none cursor-pointer" id="btnNvidiaModalSave" data-i18n="nvidiaModalSave">添加账号</button>
    </template>
  </BaseModal>
</template>

<script setup lang="ts">
import BaseModal from './BaseModal.vue';
import PasswordInput from './PasswordInput.vue';
</script>

