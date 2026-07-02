<template>
<div class="flex flex-col gap-5 w-full" id="view-usage-details">
    <div class="flex flex-col lg:flex-row lg:justify-between lg:items-end gap-4">
        <div>
            <h1 class="text-2xl font-bold text-on-surface dark:text-white" data-i18n="usage_title">账号用量详情</h1>
            <p class="text-xs text-outline dark:text-outline-variant" data-i18n="usage_desc">按账号与模型展开统计输入、输出、缓存 Token 与成本支出</p>
        </div>
    </div>

    <!-- 用量统计与分页列表挂载节点 -->
    <div id="usageStatsPanel" class="w-full"></div>
</div>
</template>

<script setup lang="ts">
import { onMounted } from 'vue';

onMounted(() => {
    // 页面挂载完成
});
</script>
