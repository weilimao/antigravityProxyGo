<template>
<div class="fixed inset-0 bg-slate-950/75 z-50 flex items-center justify-center opacity-0 pointer-events-none transition-opacity duration-200" id="retryErrorLogsModal">
<div class="bg-white dark:bg-[#1e2538] w-[950px] max-w-[95vw] rounded-2xl border border-outline-variant/60 shadow-2xl flex flex-col max-h-[85vh] transform scale-95 transition-transform duration-200" id="retryErrorLogsModalContainer">
<!-- Modal 头部 -->
<div class="px-6 py-4 border-b border-outline-variant/30 flex justify-between items-center bg-slate-50/50 dark:bg-white/5 rounded-t-2xl">
<div class="flex items-center gap-2">
<span class="text-primary dark:text-primary-fixed-dim text-lg font-bold" data-i18n="retryErrorLogsTitle">异常与重试日志详情</span>
<span class="text-xs bg-slate-100 dark:bg-white/10 px-2 py-0.5 rounded-full text-slate-500 dark:text-slate-400 font-medium" id="retryErrorLogsCount">0条记录</span>
</div>
<div class="flex items-center gap-3">
<!-- 筛选器 -->
<select class="text-[12px] bg-slate-100 hover:bg-slate-200 dark:bg-white/5 dark:hover:bg-white/10 text-on-surface dark:text-white rounded-lg px-2.5 py-1.5 border border-outline-variant/40 outline-none transition-all cursor-pointer" id="logTypeFilter">
<option data-i18n="allLogs" value="ALL">全部日志</option>
<option data-i18n="onlyRetries" value="RETRY">仅重试 (RETRY)</option>
<option data-i18n="onlyErrors" value="ERROR">仅报错 (ERROR)</option>
</select>
<button class="text-outline hover:text-primary transition-colors flex items-center justify-center p-1 rounded-full hover:bg-slate-100 dark:hover:bg-white/5" id="retryErrorLogsModalCloseBtn">
<svg class="w-5 h-5" fill="none" stroke="currentColor" viewbox="0 0 24 24"><path d="M6 18L18 6M6 6l12 12" stroke-linecap="round" stroke-linejoin="round" stroke-width="2"></path></svg>
</button>
</div>
</div>
<!-- Modal 主体内容 -->
<div class="p-6 overflow-y-auto flex-1 select-text">
<!-- 表格区域 -->
<div class="w-full overflow-x-auto border border-outline-variant/20 dark:border-white/5 rounded-xl">
<table class="w-full text-left border-collapse text-[12px]">
<thead>
<tr class="bg-slate-50 dark:bg-white/5 border-b border-outline-variant/20 dark:border-white/5 text-slate-500 dark:text-slate-400 uppercase tracking-wider font-semibold">
<th class="px-4 py-3 min-w-[110px]" data-i18n="colTime">时间</th>
<th class="px-4 py-3 min-w-[70px]" data-i18n="colType">类型</th>
<th class="px-4 py-3 min-w-[60px]" data-i18n="colAttempt">次数</th>
<th class="px-4 py-3 min-w-[120px]" data-i18n="colAccount">账号</th>
<th class="px-4 py-3 min-w-[140px]" data-i18n="colTargetModel">目标模型</th>
<th class="px-4 py-3 min-w-[120px]" data-i18n="colLogPath">请求路径</th>
<th class="px-4 py-3" data-i18n="colDetail">错误/异常详情</th>
</tr>
</thead>
<tbody class="divide-y divide-outline-variant/10 dark:divide-white/5 text-on-surface dark:text-slate-200" id="retryErrorLogsTableBody">
<!-- JS 动态填充 -->
</tbody>
</table>
</div>
<!-- 分页控制区 -->
<div class="flex justify-between items-center mt-4 text-[12px]" id="retryErrorLogsPaginationWrapper">
<span class="text-slate-500 dark:text-slate-400" id="valRetryErrorLogsShowingText"></span>
<div class="flex gap-1" id="retryErrorLogsPaginationControls"></div>
</div>
<!-- 空状态展示 -->
<div class="hidden py-16 flex flex-col items-center justify-center gap-3 text-slate-400 dark:text-slate-500" id="retryErrorLogsEmpty">
<svg class="w-12 h-12 stroke-current opacity-70" fill="none" stroke="currentColor" viewbox="0 0 24 24">
<path d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z" stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5"></path>
</svg>
<span class="text-sm" data-i18n="logEmptyText">暂无异常或重试日志，运行状况良好 ✨</span>
</div>
</div>
<!-- Modal 底部 -->
<div class="px-6 py-3.5 border-t border-outline-variant/30 bg-slate-50/50 dark:bg-white/5 flex justify-between items-center rounded-b-2xl">
<button class="px-4 py-1.5 text-[12px] font-medium bg-rose-50 text-rose-600 hover:bg-rose-100 dark:bg-rose-950/20 dark:text-rose-400 dark:hover:bg-rose-950/40 rounded-lg transition-colors border border-rose-200/40 dark:border-rose-900/40" data-i18n="btnClearLogs" id="btnClearRetryErrorLogs">清空日志</button>
<div class="flex gap-3">
<button class="px-4 py-1.5 text-[12px] font-bold bg-primary text-white hover:bg-primary/90 rounded-lg transition-colors shadow-sm flex items-center gap-1" data-i18n="btnExportLogs" id="btnExportRetryErrorLogs">
<svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewbox="0 0 24 24"><path d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-4l-4 4m0 0l-4-4m4 4V4" stroke-linecap="round" stroke-linejoin="round" stroke-width="2"></path></svg>
                        导出日志
                    </button>
<button class="px-4 py-1.5 text-[12px] font-medium bg-slate-100 hover:bg-slate-200 dark:bg-white/5 dark:hover:bg-white/10 text-on-surface dark:text-white rounded-lg transition-colors border border-outline-variant/40" data-i18n="btnClose" id="retryErrorLogsModalCloseBtnSecondary">关闭</button>
</div>
</div>
</div>
</div>
</template>

<script setup lang="ts">
import { onMounted } from 'vue';

onMounted(() => {
});
</script>
