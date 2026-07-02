<template>
<div class="fixed inset-0 bg-slate-950/75 z-50 flex items-center justify-center opacity-0 pointer-events-none transition-opacity duration-200" id="sessionBindingsModal">
<div class="bg-white dark:bg-[#1e2538] w-[850px] max-w-[90vw] rounded-2xl border border-outline-variant/60 shadow-2xl flex flex-col max-h-[85vh] transform scale-95 transition-transform duration-200" id="sessionBindingsModalContainer">
<!-- Modal 头部 -->
<div class="px-6 py-4 border-b border-outline-variant/30 flex justify-between items-center bg-slate-50/50 dark:bg-white/5 rounded-t-2xl">
<div class="flex items-center gap-2">
<span class="material-symbols-outlined text-primary text-[20px]">hub</span>
<span class="text-sm font-bold text-on-surface dark:text-white" data-i18n="sessionBindingsTitle">当前会话绑定关系</span>
<span class="text-[11px] font-medium text-primary dark:text-primary-fixed-dim bg-primary/10 px-1.5 py-0.5 rounded-md" id="sessionBindingsCount">共 0 条记录</span>
</div>
<button class="text-outline hover:text-primary transition-colors flex items-center justify-center p-1 rounded-full hover:bg-slate-100 dark:hover:bg-white/5" id="sessionBindingsModalCloseBtn">
<span class="material-symbols-outlined text-[18px]">close</span>
</button>
</div>
<!-- Modal 主体内容 -->
<div class="p-6 overflow-y-auto flex-grow max-h-[65vh]">
<!-- 顶部说明 -->
<div class="text-[11px] text-outline dark:text-outline-variant mb-4 bg-slate-50 dark:bg-slate-900/30 p-3 rounded-xl border border-outline-variant/20 leading-relaxed flex items-start gap-2">
<span class="material-symbols-outlined text-amber-500 text-[16px]">info</span>
<div>
<span class="font-bold" data-i18n="tipLabel">提示：</span><span data-i18n="sessionBindingsTip">系统根据客户端请求特征（例如 Authorization 凭据、网卡 TCP 套接字源端口等）对账号池中的账号进行粘性映射分配（默认有效期为 30 分钟），以此来实现会话一致性。</span>
                    </div>
</div>
<div class="overflow-x-auto rounded-xl border border-outline-variant/30 bg-slate-50/30 dark:bg-slate-950/20 max-h-[420px] overflow-y-auto">
<table class="w-full text-left border-collapse table-fixed">
<thead>
<tr class="bg-slate-50 dark:bg-[#1a1f30] text-outline dark:text-outline-variant text-[11px] border-b border-outline-variant/30 sticky top-0 z-10">
<th class="p-3 font-bold w-[40%]" data-i18n="colSessionKey">会话标识 (SessionKey)</th>
<th class="p-3 font-bold w-[35%]" data-i18n="colBoundEmail">绑定的账号邮箱</th>
<th class="p-3 font-bold w-[25%]" data-i18n="colLastActive">最后活跃时间</th>
<th class="p-3 font-bold text-center w-[12%]" data-i18n="colAction">操作</th>
</tr>
</thead>
<tbody class="text-[12px] text-on-surface dark:text-slate-200 divide-y divide-outline-variant/10" id="sessionBindingsTableBody">
<!-- 由 JS 动态渲染 -->
<tr>
<td class="p-8 text-center text-outline dark:text-outline-variant italic" colspan="4" data-i18n="sessionBindingsLoading">正在加载会话绑定数据...</td>
</tr>
</tbody>
</table>
</div>
</div>
<!-- Modal 底部 -->
<div class="px-6 py-4 border-t border-outline-variant/30 bg-slate-50/50 dark:bg-white/5 flex justify-between items-center rounded-b-2xl">
<button class="px-3.5 py-1.5 text-[12px] font-bold text-red-500 hover:text-white hover:bg-red-500 bg-red-500/10 border border-red-500/20 rounded-lg transition-all flex items-center gap-1 select-none cursor-pointer" id="sessionBindingsModalClearAllBtn">
<span class="material-symbols-outlined text-[15px]">cleaning_services</span>
<span data-i18n="btnClearAllBindings">清空所有绑定</span>
</button>
<button class="px-4 py-1.5 text-[12px] font-bold bg-slate-100 hover:bg-slate-200 dark:bg-white/5 dark:hover:bg-white/10 text-on-surface dark:text-white rounded-lg transition-colors border border-outline-variant/40 cursor-pointer" id="sessionBindingsModalCloseBtnSecondary" data-i18n="btnClose">关闭</button>
</div>
</div>
</div>
</template>

<script setup lang="ts">
import { onMounted } from 'vue';

onMounted(() => {
});
</script>
