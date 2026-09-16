<template>
  <div class="w-full flex flex-col items-center justify-center py-12 gap-3 text-slate-400 select-none animate-fade-in">
    <span
      class="material-symbols-outlined text-indigo-400 animate-spin"
      :class="sizeClass"
    >
      progress_activity
    </span>
    <span class="text-xs tracking-wide text-slate-300">{{ text || '正在加载数据，请稍候...' }}</span>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(
  defineProps<{
    text?: string
    size?: 'sm' | 'md' | 'lg'
  }>(),
  {
    text: '正在加载数据，请稍候...',
    size: 'md',
  }
)

const sizeClass = computed(() => {
  switch (props.size) {
    case 'sm':
      return 'text-20px'
    case 'lg':
      return 'text-36px'
    case 'md':
    default:
      return 'text-28px'
  }
})
</script>

<style scoped>
.animate-fade-in {
  animation: fadeIn 0.25s cubic-bezier(0.16, 1, 0.3, 1);
}
@keyframes fadeIn {
  from {
    opacity: 0;
    transform: translateY(4px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}
</style>
