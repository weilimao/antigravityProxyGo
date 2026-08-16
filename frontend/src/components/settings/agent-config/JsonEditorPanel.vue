<template>
  <div class="flex flex-col h-full">
    <div class="flex items-center justify-between mb-2">
      <div class="flex items-center gap-2">
        <span class="material-symbols-outlined text-primary text-[18px]">data_object</span>
        <span class="text-[13px] font-bold text-on-surface dark:text-white">JSON 预览与编辑</span>
      </div>
      <span v-if="parseError" class="text-[11px] text-error font-medium flex items-center gap-1">
        <span class="material-symbols-outlined text-[14px]">error</span>
        {{ parseError }}
      </span>
      <span v-else class="text-[11px] text-success font-medium flex items-center gap-1">
        <span class="material-symbols-outlined text-[14px]">check_circle</span>
        JSON 有效
      </span>
    </div>
    <textarea
      ref="editorRef"
      v-model="jsonText"
      @input="onInput"
      class="w-full px-3 py-2 text-[12px] bg-slate-50 dark:bg-[#0d1117] border rounded-lg focus:outline-none font-mono resize-none leading-relaxed"
      :class="parseError ? 'border-error/50' : 'border-outline-variant/60 dark:border-white/10'"
      style="flex: 1 1 0%; min-height: 0;"
      spellcheck="false"
    ></textarea>
  </div>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue';

const props = defineProps<{
  modelValue: string;
}>();

const emit = defineEmits<{
  'update:modelValue': [value: string];
  'parse-error': [error: string | null];
}>();

const editorRef = ref<HTMLTextAreaElement | null>(null);
const jsonText = ref(props.modelValue);
const parseError = ref<string | null>(null);

watch(() => props.modelValue, (newVal) => {
  if (newVal !== jsonText.value) {
    jsonText.value = newVal;
    validateJSON(newVal);
  }
});

function onInput() {
  emit('update:modelValue', jsonText.value);
  validateJSON(jsonText.value);
}

function validateJSON(text: string) {
  if (text.trim() === '') {
    parseError.value = null;
    emit('parse-error', null);
    return;
  }
  try {
    JSON.parse(text);
    parseError.value = null;
    emit('parse-error', null);
  } catch (err: any) {
    parseError.value = err.message;
    emit('parse-error', err.message);
  }
}

validateJSON(jsonText.value);
</script>
