<template>
<div class="fixed inset-0 bg-slate-950/75 z-50 flex items-center justify-center opacity-0 pointer-events-none transition-opacity duration-200" id="pricingModal">
<div class="bg-white dark:bg-[#1e2538] w-[450px] max-w-[90vw] rounded-2xl border border-outline-variant/60 shadow-2xl flex flex-col transform scale-95 transition-transform duration-200" id="pricingModalContainer">
<!-- Modal 头部 -->
<div class="px-6 py-4 border-b border-outline-variant/30 flex justify-between items-center bg-slate-50/50 dark:bg-white/5 rounded-t-2xl">
<h3 class="text-sm font-bold text-on-surface dark:text-white flex items-center gap-2">
<span class="material-symbols-outlined text-primary text-[18px]">payments</span>
<span id="pricingModalTitle">编辑模型计费</span>
</h3>
<button class="text-outline hover:text-primary transition-colors flex items-center justify-center p-1 rounded-full hover:bg-slate-100 dark:hover:bg-white/5" id="pricingModalCloseBtn">
<span class="material-symbols-outlined text-[20px]">close</span>
</button>
</div>
<!-- Modal 主体内容 -->
<form class="px-6 py-5 flex flex-col gap-4" id="pricingForm">
<input id="pricingOrigKey" type="hidden"/>
<!-- 模型匹配名称 -->
<div class="flex flex-col gap-1.5">
<label class="text-[11px] text-outline font-bold uppercase" for="pricingModelName">模型匹配名称 (如 deepseek-chat)</label>
<input class="px-3 py-1.5 text-[13px] bg-white dark:bg-[#1a1f30] border border-outline-variant/60 rounded-md focus:border-primary focus:outline-none transition-shadow text-on-surface dark:text-white font-data-mono" id="pricingModelName" required type="text"/>
</div>
<!-- 输入价格 -->
<div class="flex flex-col gap-1.5">
<label class="text-[11px] text-outline font-bold uppercase" for="pricingInputVal">输入 Token 单价 (USD / 每百万 Tokens)</label>
<input class="px-3 py-1.5 text-[13px] bg-white dark:bg-[#1a1f30] border border-outline-variant/60 rounded-md focus:border-primary focus:outline-none transition-shadow text-on-surface dark:text-white font-data-mono" id="pricingInputVal" min="0" required step="0.000001" type="number"/>
</div>
<!-- 输出价格 -->
<div class="flex flex-col gap-1.5">
<label class="text-[11px] text-outline font-bold uppercase" for="pricingOutputVal">输出 Token 单价 (USD / 每百万 Tokens)</label>
<input class="px-3 py-1.5 text-[13px] bg-white dark:bg-[#1a1f30] border border-outline-variant/60 rounded-md focus:border-primary focus:outline-none transition-shadow text-on-surface dark:text-white font-data-mono" id="pricingOutputVal" min="0" required step="0.000001" type="number"/>
</div>
<!-- 缓存价格 -->
<div class="flex flex-col gap-1.5">
<label class="text-[11px] text-outline font-bold uppercase" for="pricingCachedVal">缓存 Token 单价 (USD / 每百万 Tokens)</label>
<input class="px-3 py-1.5 text-[13px] bg-white dark:bg-[#1a1f30] border border-outline-variant/60 rounded-md focus:border-primary focus:outline-none transition-shadow text-on-surface dark:text-white font-data-mono" id="pricingCachedVal" min="0" required step="0.000001" type="number"/>
</div>
</form>
<!-- Modal 底部 -->
<div class="px-6 py-3.5 border-t border-outline-variant/30 bg-slate-50/50 dark:bg-white/5 flex justify-end gap-3 rounded-b-2xl">
<button class="px-4 py-1.5 text-[12px] font-medium bg-slate-100 hover:bg-slate-200 dark:bg-white/5 dark:hover:bg-white/10 text-on-surface dark:text-white rounded-lg transition-colors border border-outline-variant/40" id="pricingModalCancelBtn">取消</button>
<button class="px-4 py-1.5 text-[12px] font-bold bg-primary text-white hover:bg-primary/90 rounded-lg transition-colors shadow-sm" id="pricingModalSaveBtn">保存</button>
</div>
</div>
</div>
</template>

<script setup lang="ts">
import { onMounted } from 'vue';

onMounted(() => {
});
</script>
