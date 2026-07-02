<template>
<div class="fixed inset-0 bg-slate-950/75 z-50 flex items-center justify-center opacity-0 pointer-events-none transition-opacity duration-200" id="exportPacketsModal">
<div class="bg-white dark:bg-[#1e2538] w-[420px] max-w-[90vw] rounded-2xl border border-outline-variant/60 shadow-2xl flex flex-col transform scale-95 transition-transform duration-200" id="exportPacketsModalContainer">
<!-- Modal 头部 -->
<div class="px-6 py-4 border-b border-outline-variant/30 flex justify-between items-center bg-slate-50/50 dark:bg-white/5 rounded-t-2xl">
<span class="text-sm font-bold text-on-surface dark:text-white flex items-center gap-1.5">
<span class="material-symbols-outlined text-[18px] text-primary">description</span>
<span data-i18n="exportPacketsTitle">导出接口日志</span>
</span>
<button class="text-outline hover:text-primary transition-colors flex items-center justify-center p-1 rounded-full hover:bg-slate-100 dark:hover:bg-white/5" id="exportPacketsModalCloseBtn">
<span class="material-symbols-outlined text-[18px]">close</span>
</button>
</div>
<!-- Modal 主体内容 -->
<div class="p-6 flex flex-col gap-4">
<div class="text-[12px] text-outline leading-relaxed" data-i18n="exportPacketsTip">
                    请选择需要导出的接口数据包分类。我们将为您生成一份包含请求和响应细节（包含 Method、Host、Path、Headers 以及 Request/Response Body）的完整 Markdown 文档。
                </div>
<!-- 类型选择 -->
<div class="flex flex-col gap-2">
<label class="text-[13px] font-medium text-slate-700 dark:text-slate-200" for="exportPacketsTypeSelect" data-i18n="exportPacketsSelectLabel">选择接口分类:</label>
<select class="bg-white dark:bg-[#1a1f30] border border-outline-variant/50 rounded-md px-3 py-1.5 text-[13px] font-medium text-slate-700 dark:text-slate-200 focus:outline-none focus:border-primary w-full" id="exportPacketsTypeSelect">
<option value="ALL" data-i18n="packetFilterAll">全部</option>
<option value="CLI">CLI</option>
<option value="IDE">IDE</option>
<option value="Agent">Agent</option>
<option value="UNKNOWN" data-i18n="packetFilterUnknown">未知</option>
</select>
</div>
</div>
<!-- Modal 底部 -->
<div class="px-6 py-4 border-t border-outline-variant/30 bg-slate-50/50 dark:bg-white/5 flex justify-end gap-3 rounded-b-2xl">
<button class="px-4 py-2 text-[12px] font-medium bg-slate-100 hover:bg-slate-200 dark:bg-white/5 dark:hover:bg-white/10 text-on-surface dark:text-white rounded-lg transition-colors border border-outline-variant/40 cursor-pointer" id="btnExportPacketsCancel" data-i18n="btnCancel">取消</button>
<button class="px-4 py-2 text-[12px] font-bold bg-primary text-white hover:bg-primary/90 rounded-lg transition-colors shadow-sm cursor-pointer" id="btnExportPacketsConfirm" data-i18n="btnExportPacketsConfirm">确认导出</button>
</div>
</div>
</div>
</template>

<script setup lang="ts">
import { onMounted } from 'vue';

onMounted(() => {
});
</script>
