<template>
<div class="fixed inset-0 bg-slate-950/75 z-50 flex items-center justify-center opacity-0 pointer-events-none transition-opacity duration-200" id="detailsModal">
<div class="bg-white dark:bg-[#1e2538] w-[900px] max-w-[92vw] rounded-2xl border border-outline-variant/60 shadow-2xl flex flex-col max-h-[90vh] transform scale-95 transition-transform duration-200" id="modalContainer">
<!-- Modal 头部 -->
<div class="px-6 py-4 border-b border-outline-variant/30 flex justify-between items-center bg-slate-50/50 dark:bg-white/5 rounded-t-2xl">
<h3 class="text-sm font-bold text-on-surface dark:text-white flex items-center gap-2">
<span class="material-symbols-outlined text-primary text-[18px]">terminal</span>
<span>请求参数详情</span>
</h3>
<button class="text-outline hover:text-primary transition-colors flex items-center justify-center p-1 rounded-full hover:bg-slate-100 dark:hover:bg-white/5" id="modalCloseBtn">
<span class="material-symbols-outlined text-[20px]">close</span>
</button>
</div>
<!-- Modal 主体内容 -->
<div class="px-6 py-5 overflow-y-auto flex flex-col gap-4">
<!-- 基本信息卡片 -->
<div class="grid grid-cols-1 sm:grid-cols-2 gap-4 text-[12px] p-4 bg-slate-50 dark:bg-slate-900/30 rounded-xl border border-outline-variant/20">
<div class="flex flex-col gap-1">
<span class="text-outline">请求时间</span>
<span class="font-data-mono text-on-surface dark:text-white font-semibold" id="modalTime">-</span>
</div>
<div class="flex flex-col gap-1">
<span class="text-outline">会话 ID</span>
<span class="font-data-mono text-on-surface dark:text-white font-semibold break-all" id="modalSession">-</span>
</div>
<div class="flex flex-col gap-1">
<span class="text-outline">所用模型</span>
<span class="font-sans text-on-surface dark:text-white font-semibold" id="modalModel">-</span>
</div>
<div class="flex flex-col gap-1">
<span class="text-outline" data-i18n="colDuration">请求耗时</span>
<span class="font-data-mono text-on-surface dark:text-white font-semibold" id="modalDuration">-</span>
</div>
<div class="flex flex-col gap-1 col-span-2">
<span class="text-outline">API 接口</span>
<span class="font-data-mono text-primary dark:text-primary-fixed-dim font-bold break-all" id="modalPath">-</span>
</div>
<div class="flex flex-col gap-1">
<span class="text-outline">Token 消耗 (In / Out / Cache)</span>
<span class="font-data-mono text-on-surface dark:text-white font-semibold" id="modalTokens">-</span>
</div>
<div class="flex flex-col gap-1">
<span class="text-outline">缓存状态 / HTTP 状态码</span>
<span class="font-sans font-semibold" id="modalStatus">-</span>
</div>
<div class="flex flex-col gap-1">
<span class="text-outline" data-i18n="colPrice">价格</span>
<span class="font-data-mono text-emerald-600 dark:text-emerald-400 font-bold" id="modalCost">-</span>
</div>
<div class="flex flex-col gap-1 hidden" id="modalAccountWrapper">
<span class="text-outline">分发账号</span>
<span class="font-data-mono text-on-surface dark:text-white font-semibold" id="modalAccount">-</span>
</div>
</div>
<!-- JSON 双栏区：左=请求头 右=请求体 -->
<div class="grid grid-cols-1 md:grid-cols-2 gap-3 flex-1 min-h-0">
<!-- 请求头 -->
<div class="flex flex-col gap-1.5 min-h-0">
<div class="flex justify-between items-center">
<span class="text-[12px] font-bold text-outline">请求头 (Request Headers)</span>
<button class="px-2.5 py-1 text-[11px] bg-slate-100 hover:bg-slate-200 dark:bg-white/5 dark:hover:bg-white/10 text-on-surface dark:text-white rounded-md flex items-center gap-1 transition-all border border-outline-variant/30" id="modalCopyHeadersBtn">
<span class="material-symbols-outlined text-[14px]">content_copy</span>
<span>复制</span>
</button>
</div>
<div class="relative flex-1 min-h-0">
<pre class="p-4 bg-slate-950 text-sky-400 dark:text-sky-300 rounded-xl overflow-x-auto text-[11px] font-data-mono border border-slate-800 h-[300px] overflow-y-auto select-text leading-relaxed whitespace-pre-wrap break-all" id="modalHeaderArea"></pre>
</div>
</div>
<!-- 请求体 -->
<div class="flex flex-col gap-1.5 min-h-0">
<div class="flex justify-between items-center">
<span class="text-[12px] font-bold text-outline">请求参数 (Request Body)</span>
<button class="px-2.5 py-1 text-[11px] bg-slate-100 hover:bg-slate-200 dark:bg-white/5 dark:hover:bg-white/10 text-on-surface dark:text-white rounded-md flex items-center gap-1 transition-all border border-outline-variant/30" id="modalCopyBtn">
<span class="material-symbols-outlined text-[14px]">content_copy</span>
<span>复制 JSON</span>
</button>
</div>
<div class="relative flex-1 min-h-0">
<pre class="p-4 bg-slate-950 text-emerald-400 dark:text-emerald-300 rounded-xl overflow-x-auto text-[11px] font-data-mono border border-slate-800 h-[300px] overflow-y-auto select-text leading-relaxed whitespace-pre-wrap break-all" id="modalJsonArea"></pre>
</div>
</div>
</div>
</div>
<!-- Modal 底部 -->
<div class="px-6 py-3.5 border-t border-outline-variant/30 bg-slate-50/50 dark:bg-white/5 flex justify-end gap-3 rounded-b-2xl">
<button class="px-4 py-1.5 text-[12px] font-bold bg-slate-100 hover:bg-slate-200 dark:bg-white/5 dark:hover:bg-white/10 text-on-surface dark:text-white rounded-lg transition-colors border border-outline-variant/40" id="modalCloseBtnSecondary">关闭</button>
</div>
</div>
</div>
</template>

<script setup lang="ts">
import { onMounted } from 'vue';

onMounted(() => {
});
</script>
