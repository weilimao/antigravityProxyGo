<template>
  <div class="relative w-full">
    <input
      :id="inputId"
      :type="show ? 'text' : 'password'"
      :placeholder="placeholder"
      :data-i18n-placeholder="dataI18nPlaceholder"
      :class="inputClass"
    />
    <button
      type="button"
      :aria-label="show ? '隐藏' : '显示'"
      :title="show ? '隐藏' : '显示'"
      class="absolute right-2 top-1/2 -translate-y-1/2 flex items-center justify-center w-6 h-6 text-outline hover:text-on-surface dark:hover:text-white transition-colors cursor-pointer"
      @click="toggle"
    >
      <span class="material-symbols-outlined text-[18px] block leading-none">{{ show ? 'visibility_off' : 'visibility' }}</span>
    </button>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue';

const props = defineProps<{
  inputId: string;
  placeholder?: string;
  dataI18nPlaceholder?: string;
  inputClass?: string;
}>();

const show = ref(false);
const toggle = () => {
  show.value = !show.value;
};
</script>