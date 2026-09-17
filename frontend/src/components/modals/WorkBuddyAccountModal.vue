<template>
  <BaseModal
    id="workbuddyAccountModal"
    containerId="workbuddyAccountModalContainer"
    closeBtnId="btnWorkBuddyModalClose"
    title="添加 WorkBuddy 号池账号"
    titleI18n="workbuddyAddModalTitle"
    icon="cloud_sync"
    maxWidth="w-[560px] max-w-[95vw]"
    maxHeight="max-h-[85vh]"
    bodyClass="px-6 py-5 overflow-y-auto flex flex-col gap-4"
    @close="closeWorkBuddyAccountModal"
  >
    <!-- 官方网页授权登录卡片 (1:1 官方体验) -->
    <div id="workbuddyWebLoginSection"
      class="p-5 rounded-xl bg-gradient-to-br from-blue-50/80 via-indigo-50/50 to-slate-50/90 dark:from-[#131b2e] dark:via-[#161f36] dark:to-[#101624] border border-blue-200/70 dark:border-blue-500/30 shadow-sm flex flex-col items-center text-center relative overflow-hidden transition-all">
      <!-- 官方品牌 Logo 与标题 -->
      <div class="flex items-center gap-2.5 mb-1.5 z-10">
        <div class="w-8 h-8 rounded-lg bg-blue-600 text-white flex items-center justify-center font-bold shadow-md shadow-blue-500/20">
          <span class="material-symbols-outlined text-[20px]">smart_toy</span>
        </div>
        <span class="text-[17px] font-bold text-slate-800 dark:text-white tracking-wide">WorkBuddy，我帮你</span>
      </div>
      <p class="text-[12px] text-slate-500 dark:text-slate-400 max-w-[400px] mb-4 z-10 leading-relaxed">
        官方网页授权登录，支持在浏览器中通过 Google / GitHub / X 账号一键登录并自动入库
      </p>

      <!-- 状态 1: 就绪态 (idle) -->
      <div id="wbOAuthStateIdle" class="w-full max-w-[420px] flex items-center justify-center gap-2.5 z-10">
        <button type="button" id="btnWorkBuddyWebLogin"
          class="flex-1 py-2.5 px-4 rounded-lg text-[13px] font-bold text-white bg-blue-600 hover:bg-blue-700 active:scale-[0.99] transition-all shadow-md shadow-blue-500/25 flex items-center justify-center gap-1.5 cursor-pointer whitespace-nowrap">
          <span class="material-symbols-outlined text-[18px]">open_in_browser</span>
          <span>官方网页一键授权</span>
        </button>
        <button type="button" id="btnWorkBuddyIdleCopyUrl"
          class="py-2.5 px-4 rounded-lg text-[13px] font-semibold text-slate-700 dark:text-slate-200 bg-white dark:bg-[#1f293d] hover:bg-slate-50 dark:hover:bg-[#27334d] border border-slate-300 dark:border-slate-700 shadow-sm active:scale-[0.99] transition-all flex items-center justify-center gap-1.5 cursor-pointer whitespace-nowrap">
          <span class="material-symbols-outlined text-[17px]">content_copy</span>
          <span id="btnWorkBuddyIdleCopyUrlText">复制授权链接</span>
        </button>
      </div>

      <!-- 状态 2: 登录中 (logging_in) -->
      <div id="wbOAuthStateLogging" class="hidden w-full max-w-[440px] flex flex-col items-center gap-3 z-10 py-1">
        <div class="flex items-center gap-2.5 text-blue-600 dark:text-blue-400 font-bold text-[14px]">
          <span class="material-symbols-outlined animate-spin text-[20px]">progress_activity</span>
          <span>登录中... 正在等待浏览器完成授权</span>
        </div>
        <p id="wbOAuthLoggingTip" class="text-[11px] text-slate-500 dark:text-slate-400 leading-normal">
          已自动为你唤起系统默认浏览器。若浏览器未自动打开，请复制链接在浏览器中完成登录：
        </p>
        <div class="flex items-center gap-2 w-full justify-center">
          <button type="button" id="btnWorkBuddyCopyLoginUrl"
            class="px-3.5 py-1.5 rounded-lg text-[12px] font-semibold text-slate-700 dark:text-slate-200 bg-white dark:bg-[#1f293d] hover:bg-slate-50 dark:hover:bg-[#27334d] border border-slate-300 dark:border-slate-700 shadow-sm transition-all flex items-center gap-1.5 cursor-pointer">
            <span class="material-symbols-outlined text-[15px]">content_copy</span>
            <span id="btnWorkBuddyCopyLoginUrlText">复制登录链接</span>
          </button>
          <button type="button" id="btnWorkBuddyCancelLogin"
            class="px-3.5 py-1.5 rounded-lg text-[12px] font-medium text-slate-500 dark:text-slate-400 hover:bg-slate-200/50 dark:hover:bg-white/5 transition-all cursor-pointer">
            取消
          </button>
        </div>
      </div>

      <!-- 状态 3: 授权成功 (success) -->
      <div id="wbOAuthStateSuccess" class="hidden w-full max-w-[360px] flex flex-col items-center gap-1.5 z-10 py-2">
        <div class="flex items-center gap-2 text-emerald-600 dark:text-emerald-400 font-bold text-[14px]">
          <span class="material-symbols-outlined text-[20px]">check_circle</span>
          <span id="wbOAuthSuccessName">授权成功，账号已入库！</span>
        </div>
        <span class="text-[11px] text-slate-500 dark:text-slate-400">正在为您刷新号池列表...</span>
      </div>
    </div>

    <!-- 备用导入方式分割线 -->
    <div class="relative flex py-1 items-center">
      <div class="flex-grow border-t border-outline-variant/20"></div>
      <span class="flex-shrink mx-3 text-[11px] text-outline">或选择其他录入方式</span>
      <div class="flex-grow border-t border-outline-variant/20"></div>
    </div>

    <div class="flex flex-col gap-2">
      <!-- 一键导入本机当前已登录的 WorkBuddy 客户端凭证 -->
      <button type="button" id="btnWorkBuddyImportLocal"
        class="w-full px-4 py-2.5 text-[13px] font-semibold text-teal-700 dark:text-teal-300 bg-teal-50 hover:bg-teal-100 dark:bg-teal-950/40 dark:hover:bg-teal-900/50 border border-teal-200/70 dark:border-teal-700/40 rounded-lg shadow-sm transition-colors flex items-center justify-center gap-2 cursor-pointer disabled:opacity-50 disabled:pointer-events-none">
        <span class="material-symbols-outlined text-[18px]">download_for_offline</span>
        <span data-i18n="workbuddyImportLocalBtn">一键读取本机 WorkBuddy 客户端凭证</span>
      </button>
      <div id="workbuddyImportAlert" class="hidden p-3 rounded-lg text-[12px] bg-teal-500/10 border border-teal-500/30 text-teal-700 dark:text-teal-300"></div>
    </div>

    <div class="border-t border-outline-variant/20 pt-3">
      <div class="text-[12px] font-bold text-on-surface dark:text-white mb-3" data-i18n="workbuddyManualSection">手动填写凭证</div>
      <div class="flex flex-col gap-3">
        <div class="flex flex-col gap-1.5">
          <label class="text-[12px] font-medium text-on-surface dark:text-white" data-i18n="workbuddyFieldBaseUrl">Base URL (上游端点)</label>
          <input type="text" id="inputWorkBuddyBaseUrl"
            placeholder="https://www.codebuddy.ai"
            value="https://www.codebuddy.ai"
            class="px-3 py-2 bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[13px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all" />
        </div>

        <div class="flex flex-col gap-1.5">
          <label class="text-[12px] font-medium text-on-surface dark:text-white" data-i18n="workbuddyFieldToken">Access Token (JWT)</label>
          <PasswordInput
            inputId="inputWorkBuddyToken"
            placeholder="eyJhbGciOi..."
            revealProvider="workbuddy"
            inputClass="w-full px-3 py-2 pr-10 bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[13px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all"
          />
        </div>

        <div class="flex flex-col gap-1.5">
          <label class="text-[12px] font-medium text-on-surface dark:text-white" data-i18n="workbuddyFieldLabel">展示名 / 备注 (可选)</label>
          <input type="text" id="inputWorkBuddyLabel"
            placeholder="WorkBuddy 账号"
            class="px-3 py-2 bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[13px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all" />
        </div>
      </div>
    </div>

    <div id="workbuddyModalError" class="hidden p-3 rounded-lg text-[12px] bg-rose-500/10 border border-rose-500/30 text-rose-600 dark:text-rose-400"></div>

    <div class="flex items-center justify-end gap-2 pt-2 border-t border-outline-variant/20">
      <button type="button" id="btnWorkBuddyCancel"
        class="px-4 py-2 text-[13px] font-medium text-on-surface dark:text-white hover:bg-slate-100 dark:hover:bg-white/5 rounded-lg transition-colors cursor-pointer"
        data-i18n="btnCancel">取消</button>
      <button type="button" id="btnWorkBuddySave"
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
import { closeWorkBuddyAccountModal } from '../../ui/workbuddyAccountModal'
</script>
