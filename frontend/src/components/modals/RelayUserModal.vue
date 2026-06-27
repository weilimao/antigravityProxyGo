<template>
<div class="hidden fixed inset-0 bg-black/50 z-[9999] flex items-center justify-center" id="relayUserModal">
<div class="bg-white dark:bg-[#1e2538] rounded-xl shadow-2xl w-[400px] p-6 border border-outline-variant/20">
<div class="flex items-center gap-2 mb-5">
<span class="material-symbols-outlined text-[22px] text-primary">person_add</span>
<h3 class="text-[16px] font-bold text-on-surface dark:text-white" data-i18n="relayAddUserTitle">添加中继用户</h3>
</div>
<div class="space-y-3">
<input class="w-full px-3 py-2 text-[13px] rounded-lg border border-outline-variant/30 bg-white dark:bg-[#1a1f30] text-on-surface dark:text-white focus:border-primary focus:ring-1 focus:ring-primary/30 outline-none" data-i18n-placeholder="relayUserKeyPlaceholder" id="relayUserKeyInput" placeholder="用户 Key" type="text">
<input class="w-full px-3 py-2 text-[13px] rounded-lg border border-outline-variant/30 bg-white dark:bg-[#1a1f30] text-on-surface dark:text-white focus:border-primary focus:ring-1 focus:ring-primary/30 outline-none" data-i18n-placeholder="relayUserPasswordPlaceholder" id="relayUserPasswordInput" placeholder="密码" type="password">
<input class="w-full px-3 py-2 text-[13px] rounded-lg border border-outline-variant/30 bg-white dark:bg-[#1a1f30] text-on-surface dark:text-white focus:border-primary focus:ring-1 focus:ring-primary/30 outline-none" data-i18n-placeholder="relayUserRemarkPlaceholder" id="relayUserRemarkInput" placeholder="备注 (可选)" type="text"/>
</input></input></div>
<div class="flex gap-2 mt-5 justify-end">
<button class="px-4 py-2 text-[12px] font-medium text-white bg-primary hover:bg-primary/90 rounded-lg transition-colors" data-i18n="relayBtnConfirm" id="btnRelayUserConfirm">添加</button>
<button class="px-4 py-2 text-[12px] font-medium text-outline hover:text-on-surface border border-outline-variant/30 rounded-lg transition-colors" data-i18n="relayBtnCancel" id="btnRelayUserCancel">取消</button>
</div>
</div>
</div>
</template>

<script setup lang="ts">
import { onMounted } from 'vue';

onMounted(() => {
});
</script>
