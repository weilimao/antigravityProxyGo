<template>
<div class="hidden fixed inset-0 bg-black/50 z-[9999] flex items-center justify-center modal-backdrop-animate" id="relayUserStatsModal">
<div class="bg-white dark:bg-[#1e2538] rounded-xl shadow-2xl w-[90%] max-w-[700px] p-6 border border-outline-variant/20 modal-content-animate">
<div class="flex items-center justify-between mb-5">
<div class="flex items-center gap-2">
<span class="material-symbols-outlined text-[22px] text-primary">bar_chart</span>
<h3 class="text-[16px] font-bold text-on-surface dark:text-white" data-i18n="userStatsTitle">用户数据统计</h3>
</div>
<button class="text-outline hover:text-on-surface transition-colors" onclick="window._relayCloseModal('relayUserStatsModal')">
<span class="material-symbols-outlined text-[20px]">close</span>
</button>
</div>
<div class="space-y-4 max-h-[60vh] overflow-y-auto" id="relayUserStatsContent">
<!-- Data will be populated here -->
</div>
<div class="flex gap-2 mt-5 justify-end">
<button class="px-4 py-2 text-[12px] font-medium text-outline hover:text-on-surface border border-outline-variant/30 rounded-lg transition-colors" onclick="window._relayCloseModal('relayUserStatsModal')" data-i18n="close">关闭</button>
</div>
</div>
</div>
</template>

<script setup lang="ts">
import { onMounted } from 'vue';

onMounted(() => {
});
</script>
