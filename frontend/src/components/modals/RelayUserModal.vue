<template>
  <BaseModal
    id="relayUserModal"
    hideType="hidden"
    zIndex="z-[9999]"
    title="添加中继用户"
    titleI18n="relayAddUserTitle"
    icon="person_add"
    maxWidth="w-[400px]"
  >
    <div class="space-y-3">
      <input class="w-full px-3 py-2 text-[13px] rounded-lg border border-outline-variant/30 bg-white dark:bg-[#1a1f30] text-on-surface dark:text-white focus:border-primary focus:ring-1 focus:ring-primary/30 outline-none" data-i18n-placeholder="relayUserKeyPlaceholder" id="relayUserKeyInput" placeholder="用户 Key" type="text" />
      <input class="w-full px-3 py-2 text-[13px] rounded-lg border border-outline-variant/30 bg-white dark:bg-[#1a1f30] text-on-surface dark:text-white focus:border-primary focus:ring-1 focus:ring-primary/30 outline-none" data-i18n-placeholder="relayUserPasswordPlaceholder" id="relayUserPasswordInput" placeholder="密码" type="password" />
      <input class="w-full px-3 py-2 text-[13px] rounded-lg border border-outline-variant/30 bg-white dark:bg-[#1a1f30] text-on-surface dark:text-white focus:border-primary focus:ring-1 focus:ring-primary/30 outline-none" data-i18n-placeholder="relayUserRemarkPlaceholder" id="relayUserRemarkInput" placeholder="备注 (可选)" type="text" />
    </div>

    <template #footer>
      <button class="px-4 py-2 text-[12px] font-medium text-outline hover:text-on-surface border border-outline-variant/30 rounded-lg transition-colors cursor-pointer" data-i18n="relayBtnCancel" id="btnRelayUserCancel">取消</button>
      <button class="px-4 py-2 text-[12px] font-medium text-white bg-primary hover:bg-primary/90 rounded-lg transition-colors cursor-pointer" data-i18n="relayBtnConfirm" id="btnRelayUserConfirm">添加</button>
    </template>
  </BaseModal>
</template>

<script setup lang="ts">
import BaseModal from './BaseModal.vue';
</script>

