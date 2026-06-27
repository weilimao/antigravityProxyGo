<template>
<div class="fixed inset-0 bg-slate-950/75 z-50 flex items-center justify-center opacity-0 pointer-events-none transition-opacity duration-200" id="updateModal">
<div class="bg-white dark:bg-[#1e2538] w-[540px] max-w-[90vw] rounded-2xl border border-outline-variant/60 shadow-2xl flex flex-col transform scale-95 transition-transform duration-200" id="updateModalContainer">
<!-- Modal 头部 -->
<div class="px-6 py-4 border-b border-outline-variant/30 flex justify-between items-center bg-slate-50/50 dark:bg-white/5 rounded-t-2xl">
<span class="text-sm font-bold text-on-surface dark:text-white flex items-center gap-1.5">
<span class="material-symbols-outlined text-[18px] text-primary">system_update</span>
<span data-i18n="updateTitle">软件更新</span>
</span>
<button class="text-outline hover:text-primary transition-colors flex items-center justify-center p-1 rounded-full hover:bg-slate-100 dark:hover:bg-white/5" id="updateModalCloseBtn">
<span class="material-symbols-outlined text-[18px]">close</span>
</button>
</div>
<!-- Modal 主体内容 -->
<div class="p-6 flex flex-col items-center gap-4 text-center max-h-[65vh] overflow-y-auto">
<!-- 状态图标 -->
<div class="w-16 h-16 rounded-2xl bg-primary/10 flex items-center justify-center shadow-inner" id="updateModalIconContainer">
<span class="material-symbols-outlined text-[36px] text-primary" id="updateModalIcon">sync</span>
</div>
<h3 class="text-base font-bold text-on-surface dark:text-white" id="updateModalStatusTitle">正在检查更新...</h3>
<p class="text-[12px] text-outline leading-relaxed break-all max-w-md" id="updateModalStatusMsg"></p>
<!-- 新版本日志 (只在有更新时显示) -->
<div class="hidden w-full flex flex-col gap-2 text-left" id="updateModalChangelogContainer">
<div class="text-[11px] font-bold text-outline uppercase tracking-wider">更新日志:</div>
<div class="p-3.5 bg-slate-50 dark:bg-slate-900/60 rounded-xl text-[12px] text-on-surface dark:text-slate-300 border border-outline-variant/20 max-h-[220px] overflow-y-auto whitespace-pre-wrap leading-relaxed select-text" id="updateModalChangelog"></div>
</div>
<!-- 进度条 -->
<div class="hidden w-full bg-slate-100 dark:bg-white/5 rounded-full h-2.5 mt-2 overflow-hidden border border-outline-variant/10" id="updateModalProgressContainer">
<div class="bg-primary h-full rounded-full transition-all duration-150" id="updateModalProgressBarFill" style="width: 0%"></div>
</div>
</div>
<!-- Modal 底部 -->
<div class="px-6 py-4 border-t border-outline-variant/30 bg-slate-50/50 dark:bg-white/5 flex justify-end gap-3 rounded-b-2xl" id="updateModalActions">
<button class="px-4 py-2 text-[12px] font-medium bg-slate-100 hover:bg-slate-200 dark:bg-white/5 dark:hover:bg-white/10 text-on-surface dark:text-white rounded-lg transition-colors border border-outline-variant/40 cursor-pointer" data-i18n="btnUpdateLater" id="btnUpdateModalCancel">暂不更新</button>
<button class="px-4 py-2 text-[12px] font-bold bg-primary text-white hover:bg-primary/90 rounded-lg transition-colors shadow-sm cursor-pointer" data-i18n="btnUpdateNow" id="btnUpdateModalConfirm">在应用内更新</button>
</div>
</div>
</div>
</template>

<script setup lang="ts">
import { onMounted } from 'vue';

onMounted(() => {
});
</script>
