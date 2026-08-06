<template>
  <BaseModal
    id="remoteModal"
    hideType="hidden"
    zIndex="z-[9999]"
    title="远程连接"
    titleI18n="remoteModalTitle"
    icon="cloud"
    maxWidth="w-[540px] max-w-[90vw]"
  >
    <div class="space-y-3">
      <div class="flex gap-3">
        <input class="flex-1 px-4 py-2 text-[14px] rounded-lg border border-outline-variant/30 bg-white dark:bg-[#1a1f30] text-on-surface dark:text-white focus:border-primary focus:ring-1 focus:ring-primary/30 outline-none" data-i18n-placeholder="remoteHostPlaceholder" id="remoteHostInput" placeholder="IP 地址 / 域名" type="text" />
        <input class="w-28 px-4 py-2 text-[14px] rounded-lg border border-outline-variant/30 bg-white dark:bg-[#1a1f30] text-on-surface dark:text-white text-center focus:border-primary focus:ring-1 focus:ring-primary/30 outline-none" id="remotePortInput" type="text" />
      </div>
      <input class="w-full px-4 py-2 text-[14px] rounded-lg border border-outline-variant/30 bg-white dark:bg-[#1a1f30] text-on-surface dark:text-white focus:border-primary focus:ring-1 focus:ring-primary/30 outline-none" data-i18n-placeholder="remotePathPlaceholder" id="remotePathInput" placeholder="路径前缀 (如 /v1，可选)" type="text" />
      <input class="w-full px-4 py-2 text-[14px] rounded-lg border border-outline-variant/30 bg-white dark:bg-[#1a1f30] text-on-surface dark:text-white focus:border-primary focus:ring-1 focus:ring-primary/30 outline-none" data-i18n-placeholder="remoteKeyPlaceholder" id="remoteKeyInput" placeholder="Key (用户标识)" type="text" />
      <PasswordInput
        inputId="remotePasswordInput"
        placeholder="密码"
        dataI18nPlaceholder="remotePasswordPlaceholder"
        inputClass="w-full px-4 py-2 pr-10 text-[14px] rounded-lg border border-outline-variant/30 bg-white dark:bg-[#1a1f30] text-on-surface dark:text-white focus:border-primary focus:ring-1 focus:ring-primary/30 outline-none"
      />
    </div>
    <div class="hidden mt-3 text-[12px] px-3 py-2 rounded-lg" id="remoteLoginResult"></div>

    <template #footer>
      <button class="px-5 py-2.5 text-[13px] font-medium text-outline hover:text-primary border border-outline-variant/30 rounded-lg transition-colors cursor-pointer" data-i18n="remoteBtnTest" id="btnRemoteTest">测试连接</button>
      <button class="px-5 py-2.5 text-[13px] font-medium text-white bg-primary hover:bg-primary/90 rounded-lg transition-colors cursor-pointer" data-i18n="remoteBtnLogin" id="btnRemoteLogin">登录</button>
      <button class="px-5 py-2.5 text-[13px] font-medium text-outline hover:text-on-surface border border-outline-variant/30 rounded-lg transition-colors cursor-pointer" data-i18n="remoteBtnCancel" id="btnRemoteCancel">取消</button>
    </template>
  </BaseModal>
</template>

<script setup lang="ts">
import BaseModal from './BaseModal.vue';
import PasswordInput from './PasswordInput.vue';
</script>

