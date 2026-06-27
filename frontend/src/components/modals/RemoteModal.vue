<template>
<div class="hidden fixed inset-0 bg-black/50 z-[9999] flex items-center justify-center" id="remoteModal">
<div class="bg-white dark:bg-[#1e2538] rounded-xl shadow-2xl w-[440px] p-6 border border-outline-variant/20">
<div class="flex items-center gap-2 mb-5">
<span class="material-symbols-outlined text-[22px] text-primary">cloud</span>
<h3 class="text-[16px] font-bold text-on-surface dark:text-white" data-i18n="remoteModalTitle">远程连接</h3>
</div>
<div class="space-y-3">
<div class="flex gap-3">
<input class="flex-1 px-3 py-2 text-[13px] rounded-lg border border-outline-variant/30 bg-white dark:bg-[#1a1f30] text-on-surface dark:text-white focus:border-primary focus:ring-1 focus:ring-primary/30 outline-none" data-i18n-placeholder="remoteHostPlaceholder" id="remoteHostInput" placeholder="IP 地址 / 域名" type="text">
<input class="w-24 px-3 py-2 text-[13px] rounded-lg border border-outline-variant/30 bg-white dark:bg-[#1a1f30] text-on-surface dark:text-white text-center focus:border-primary focus:ring-1 focus:ring-primary/30 outline-none" id="remotePortInput" placeholder="18444" type="text">
</input></input></div>
<input class="w-full px-3 py-2 text-[13px] rounded-lg border border-outline-variant/30 bg-white dark:bg-[#1a1f30] text-on-surface dark:text-white focus:border-primary focus:ring-1 focus:ring-primary/30 outline-none" data-i18n-placeholder="remoteKeyPlaceholder" id="remoteKeyInput" placeholder="Key (用户标识)" type="text">
<input class="w-full px-3 py-2 text-[13px] rounded-lg border border-outline-variant/30 bg-white dark:bg-[#1a1f30] text-on-surface dark:text-white focus:border-primary focus:ring-1 focus:ring-primary/30 outline-none" data-i18n-placeholder="remotePasswordPlaceholder" id="remotePasswordInput" placeholder="密码" type="password">
</input></input></div>
<div class="hidden mt-3 text-[12px] px-3 py-2 rounded-lg" id="remoteLoginResult"></div>
<div class="flex gap-2 mt-5 justify-end">
<button class="px-4 py-2 text-[12px] font-medium text-outline hover:text-primary border border-outline-variant/30 rounded-lg transition-colors" data-i18n="remoteBtnTest" id="btnRemoteTest">测试连接</button>
<button class="px-4 py-2 text-[12px] font-medium text-white bg-primary hover:bg-primary/90 rounded-lg transition-colors" data-i18n="remoteBtnLogin" id="btnRemoteLogin">登录</button>
<button class="px-4 py-2 text-[12px] font-medium text-outline hover:text-on-surface border border-outline-variant/30 rounded-lg transition-colors" data-i18n="remoteBtnCancel" id="btnRemoteCancel">取消</button>
</div>
</div>
</div>
</template>

<script setup lang="ts">
import { onMounted } from 'vue';

onMounted(() => {
});
</script>
