<template>
  <BaseModal
    id="relayUserStatsModal"
    hideType="hidden"
    zIndex="z-[9999]"
    title="用户数据统计"
    titleI18n="userStatsTitle"
    icon="bar_chart"
    maxWidth="w-[90%] max-w-[700px]"
  >
    <div class="space-y-4 max-h-[60vh] overflow-y-auto" id="relayUserStatsContent">
      <!-- Data will be populated here -->
    </div>

    <template #footer>
      <button class="px-4 py-2 text-[12px] font-medium text-outline hover:text-on-surface border border-outline-variant/30 rounded-lg transition-colors cursor-pointer" onclick="window._relayCloseModal('relayUserStatsModal')" data-i18n="close">关闭</button>
    </template>
  </BaseModal>
</template>

<script setup lang="ts">
import BaseModal from './BaseModal.vue';
</script>

