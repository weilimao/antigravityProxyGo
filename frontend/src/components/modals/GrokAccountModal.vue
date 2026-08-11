<template>
  <BaseModal
    id="grokAccountModal"
    containerId="grokAccountModalContainer"
    closeBtnId="btnGrokModalClose"
    title="添加 Grok 号池账号"
    titleI18n="grokAddModalTitle"
    icon="smart_toy"
    maxWidth="w-[560px] max-w-[95vw]"
    maxHeight="max-h-[85vh]"
    bodyClass="px-6 py-5 overflow-y-auto flex flex-col gap-4"
  >
    <div class="flex flex-col gap-2">
      <!-- Grok 授权登录并列双按钮(同级,各自独立起流):
           「打开浏览器登录」起流后调默认浏览器(authorization complete);
           「复制链接登录」起流后仅复制链接到剪贴板(不开默认浏览器,配合无痕/目标账号浏览器粘贴避免串号)。
           两者都须先触发后端 auth:xai-login 设备码起流一次,无法凭空复制已有链接。 -->
      <div class="flex gap-2">
        <button type="button" id="btnGrokOAuthOpenBrowser"
          class="flex-1 px-4 py-3 text-[14px] font-bold text-white bg-primary hover:bg-primary/90 rounded-lg shadow-sm transition-colors flex items-center justify-center gap-2 cursor-pointer disabled:opacity-50 disabled:pointer-events-none">
          <span class="material-symbols-outlined text-[18px]">open_in_new</span>
          <span data-i18n="grokOAuthOpenBrowser">打开浏览器登录</span>
        </button>
        <button type="button" id="btnGrokOAuthCopyLink"
          class="flex-1 px-4 py-3 text-[14px] font-bold text-primary bg-primary/10 hover:bg-primary/20 border border-primary/30 rounded-lg shadow-sm transition-colors flex items-center justify-center gap-2 cursor-pointer disabled:opacity-50 disabled:pointer-events-none">
          <span class="material-symbols-outlined text-[18px]">content_copy</span>
          <span data-i18n="grokOAuthCopyLogin">复制链接登录</span>
        </button>
      </div>
      <div class="text-[11px] text-outline" data-i18n="grokOAuthDesc">通过浏览器完成 xAI 账号授权，自动生成含刷新令牌的 OAuth 凭证</div>
    </div>

    <div id="grokOAuthStatus" class="hidden rounded-lg border border-primary/30 bg-primary/5 p-4 flex-col gap-3">
      <div class="flex items-center gap-2 text-[13px] font-bold text-on-surface dark:text-white">
        <span class="material-symbols-outlined text-primary animate-spin">autorenew</span>
        <span data-i18n="grokOAuthPolling">授权轮询中，请在浏览器完成授权…</span>
      </div>
      <div class="flex items-center gap-2">
        <span class="text-[12px] text-outline" data-i18n="grokOAuthUserCode">授权码</span>
        <span id="grokOAuthUserCodeValue" class="px-2 py-1 bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded text-[14px] font-mono font-bold tracking-widest text-on-surface dark:text-white"></span>
      </div>
      <!-- 链接只读输入框:剪贴板 API 失败时选中供手动 Ctrl+C 兜底;正常路径已自动复制,可手动复粘。 -->
      <div class="flex flex-col gap-1.5">
        <span class="text-[12px] text-outline" data-i18n="grokOAuthOpenLink">复制下方授权链接，粘贴到浏览器无痕窗口完成授权（避免自动登录当前账号）:</span>
        <input type="text" id="grokOAuthLinkInput" readonly
          class="flex-1 min-w-0 px-3 py-2 bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[11px] font-mono text-primary break-all focus:outline-none" />
      </div>
      <button type="button" id="btnGrokOAuthCancel"
        class="self-start px-3 py-1.5 text-[12px] font-medium text-on-surface dark:text-white hover:bg-slate-100 dark:hover:bg-white/5 rounded-lg transition-colors border border-outline-variant/30 cursor-pointer"
        data-i18n="grokOAuthCancel">取消授权</button>
    </div>

    <div class="border-t border-outline-variant/20 pt-4">
      <div class="text-[12px] font-bold text-on-surface dark:text-white mb-3" data-i18n="grokOAuthManualSection">手动填写 API Key</div>
      <div class="flex flex-col gap-3">
    <div class="flex flex-col gap-1.5">
      <label class="text-[12px] font-medium text-on-surface dark:text-white" data-i18n="grokFieldBaseUrl">Base URL (上游端点)</label>
      <input type="text" id="inputGrokBaseUrl"
        placeholder="https://api.x.ai/v1"
        data-i18n-placeholder="grokFieldBaseUrlPlaceholder"
        class="px-3 py-2 bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[13px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all" />
    </div>

    <div class="flex flex-col gap-1.5">
      <label class="text-[12px] font-medium text-on-surface dark:text-white" data-i18n="grokFieldApiKey">API Key</label>
      <PasswordInput
        inputId="inputGrokApiKey"
        placeholder="xai-..."
        dataI18nPlaceholder="grokFieldApiKeyPlaceholder"
        revealProvider="grok"
        inputClass="w-full px-3 py-2 pr-10 bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[13px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all"
      />
    </div>

    <div class="flex flex-col gap-1.5">
      <label class="text-[12px] font-medium text-on-surface dark:text-white" data-i18n="grokFieldLabel">展示名 (可选)</label>
      <input type="text" id="inputGrokLabel"
        placeholder="Grok 账号"
        data-i18n-placeholder="grokFieldLabelPlaceholder"
        class="px-3 py-2 bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[13px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all" />
    </div>

    <div class="border-t border-outline-variant/20 pt-4">
      <div class="mb-2.5 flex items-center justify-between">
        <div>
          <div class="text-[12px] font-bold text-on-surface dark:text-white" data-i18n="grokModelMappingTitle">模型档位映射 (可选，留空用默认)</div>
          <div class="text-[11px] text-outline mt-0.5" data-i18n="grokModelMappingDesc">按 Claude Code 调用的档位名映射到上游 Grok 模型 id</div>
        </div>
        <button type="button" id="btnGrokFetchModels" class="px-2.5 py-1 text-[11px] font-bold text-primary bg-primary/10 hover:bg-primary/20 rounded-lg transition-colors flex items-center gap-1 shrink-0 cursor-pointer">
          <span class="material-symbols-outlined text-[14px]">download</span>
          <span data-i18n="grokFieldFetchModels">获取模型</span>
        </button>
      </div>
      <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
        <div class="flex flex-col gap-1">
          <label class="text-[11px] font-medium text-outline">Sonnet</label>
          <div class="flex items-center gap-1.5">
            <input type="text" id="inputGrokModelSonnet" placeholder="grok-4"
              class="flex-1 min-w-0 px-3 py-1.5 bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[12px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all" />
            <select id="selectGrokModelSonnet" class="hidden w-28 shrink-0 px-2 py-1.5 bg-slate-100 dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[11px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all cursor-pointer">
              <option value="">选择模型...</option>
            </select>
          </div>
        </div>
        <div class="flex flex-col gap-1">
          <label class="text-[11px] font-medium text-outline">Opus</label>
          <div class="flex items-center gap-1.5">
            <input type="text" id="inputGrokModelOpus" placeholder="grok-4.3"
              class="flex-1 min-w-0 px-3 py-1.5 bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[12px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all" />
            <select id="selectGrokModelOpus" class="hidden w-28 shrink-0 px-2 py-1.5 bg-slate-100 dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[11px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all cursor-pointer">
              <option value="">选择模型...</option>
            </select>
          </div>
        </div>
        <div class="flex flex-col gap-1">
          <label class="text-[11px] font-medium text-outline">Haiku</label>
          <div class="flex items-center gap-1.5">
            <input type="text" id="inputGrokModelHaiku" placeholder="grok-4-fast"
              class="flex-1 min-w-0 px-3 py-1.5 bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[12px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all" />
            <select id="selectGrokModelHaiku" class="hidden w-28 shrink-0 px-2 py-1.5 bg-slate-100 dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[11px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all cursor-pointer">
              <option value="">选择模型...</option>
            </select>
          </div>
        </div>
        <div class="flex flex-col gap-1">
          <label class="text-[11px] font-medium text-outline">Fable</label>
          <div class="flex items-center gap-1.5">
            <input type="text" id="inputGrokModelFable" placeholder="grok-3"
              class="flex-1 min-w-0 px-3 py-1.5 bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[12px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all" />
            <select id="selectGrokModelFable" class="hidden w-28 shrink-0 px-2 py-1.5 bg-slate-100 dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[11px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all cursor-pointer">
              <option value="">选择模型...</option>
            </select>
          </div>
        </div>
        <div class="flex flex-col gap-1 sm:col-span-2">
          <label class="text-[11px] font-medium text-outline" data-i18n="grokFieldDefaultModel">默认模型 (档位未命中时回退)</label>
          <div class="flex items-center gap-1.5">
            <input type="text" id="inputGrokModelDefault" placeholder="grok-4.3"
              class="flex-1 min-w-0 px-3 py-1.5 bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[12px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all" />
            <select id="selectGrokModelDefault" class="hidden w-36 shrink-0 px-2 py-1.5 bg-slate-100 dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[11px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all cursor-pointer">
              <option value="">选择模型...</option>
            </select>
          </div>
        </div>
      </div>
    </div>
      </div>
    </div>

    <div id="grokModalError" class="hidden text-[12px] text-red-500 bg-red-500/10 border border-red-500/20 rounded-lg px-3 py-2"></div>

    <template #footer>
      <button class="px-4 py-2 text-[13px] font-medium text-on-surface dark:text-white hover:bg-slate-100 dark:hover:bg-white/5 rounded-lg transition-colors border border-outline-variant/30 cursor-pointer" id="btnGrokModalCancel" data-i18n="grokModalCancel">取消</button>
      <button class="px-4 py-2 text-[13px] font-bold text-white bg-primary hover:bg-primary/90 rounded-lg transition-colors shadow-sm disabled:opacity-50 disabled:pointer-events-none cursor-pointer" id="btnGrokModalSave" data-i18n="grokModalSave">添加账号</button>
    </template>
  </BaseModal>
</template>

<script setup lang="ts">
import BaseModal from './BaseModal.vue';
import PasswordInput from './PasswordInput.vue';
</script>
