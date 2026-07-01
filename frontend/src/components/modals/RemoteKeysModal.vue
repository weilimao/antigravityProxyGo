<template>
<div class="hidden fixed inset-0 bg-black/50 z-[9999] flex items-center justify-center" id="remoteKeysModal">
<div class="bg-white dark:bg-[#1e2538] rounded-xl shadow-2xl w-[640px] p-6 border border-outline-variant/20 flex flex-col max-h-[80vh]">
<div class="flex items-center gap-2 mb-5 flex-shrink-0">
<span class="material-symbols-outlined text-[22px] text-primary">key</span>
<h3 class="text-[16px] font-bold text-on-surface dark:text-white">管理 API Keys</h3>
<div class="flex-grow"></div>
<button id="btnRemoteKeysClose" class="text-outline hover:text-on-surface dark:hover:text-white transition-colors">
<span class="material-symbols-outlined text-[20px]">close</span>
</button>
</div>
<div class="flex gap-2 mb-4 flex-shrink-0">
<input class="flex-1 px-3 py-2 text-[13px] rounded-lg border border-outline-variant/30 bg-white dark:bg-[#1a1f30] text-on-surface dark:text-white focus:border-primary focus:ring-1 focus:ring-primary/30 outline-none" id="remoteNewKeyName" placeholder="新 Key 备注名称 (例如：Claude Code 测试端)" type="text">
<button class="px-4 py-2 text-[12px] font-medium text-white bg-primary hover:bg-primary/90 rounded-lg transition-colors flex items-center gap-1 shadow-sm whitespace-nowrap" id="btnRemoteCreateKey">
<span class="material-symbols-outlined text-[16px]">add</span> 创建 Key
</button>
</div>
<div class="flex-1 overflow-y-auto pr-1 min-h-[200px]">
<table class="w-full text-left text-[12px]">
    <thead class="sticky top-0 bg-white dark:bg-[#1e2538] z-10">
        <tr class="border-b border-outline-variant/25 text-outline/80">
            <th class="py-2.5 font-bold pl-2 w-[120px]">名称</th>
            <th class="py-2.5 font-bold">API Key (脱敏展示)</th>
            <th class="py-2.5 font-bold w-[130px]">创建时间</th>
            <th class="py-2.5 font-bold text-center w-[120px]">操作</th>
        </tr>
    </thead>
    <tbody id="remoteKeysTableBody">
        <!-- 动态渲染 -->
    </tbody>
</table>
</div>
</div>
</div>
</template>

<script setup lang="ts">
</script>
