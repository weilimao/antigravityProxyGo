<template>
  <tr class="border-b border-outline-variant/15 hover:bg-slate-50 dark:hover:bg-white/5">
    <td class="py-2 px-1">
      <input
        type="text"
        class="w-full px-2 py-1 text-[12px] rounded border border-outline-variant/30 bg-transparent text-on-surface dark:text-white focus:outline-none focus:border-primary"
        :value="item.clientModel"
        @input="onClientModelInput"
        placeholder="例如: gpt-4o"
      />
    </td>
    <td class="py-2 px-1">
      <div class="flex items-center gap-1.5">
        <ModelSearchSelect
          :model-value="item.targetModel"
          :options="rowModels"
          :allow-custom="true"
          placeholder="搜索或输入目标模型..."
          @update:model-value="onTargetModelSelect"
          class="w-full min-w-0"
        />
        <span v-if="isNew" class="shrink-0 px-1.5 py-0.5 rounded text-[10px] font-bold bg-green-500/15 text-green-600 dark:text-green-400 border border-green-500/30">新增</span>
        <span v-if="isStale" class="shrink-0 px-1.5 py-0.5 rounded text-[10px] font-bold bg-red-500/15 text-red-500 dark:text-red-400 border border-red-500/30">远端已删除</span>
      </div>
    </td>
    <td v-if="showInjectKwargs" class="py-2 text-center inject-kwargs-cell">
      <input
        type="checkbox"
        class="text-primary focus:ring-primary rounded"
        :checked="item.injectChatTemplateKwargs !== false"
        @change="onInjectKwargsChange"
        title="是否向 NVIDIA 等上游注入 chat_template_kwargs (默认勾选)"
      />
    </td>
    <td class="py-2 text-center px-1">
      <select
        class="px-2 py-1 text-[11px] rounded border border-outline-variant/30 bg-slate-50 dark:bg-[#1a1f30] text-on-surface dark:text-white cursor-pointer w-28 text-center focus:outline-none focus:border-primary"
        :value="multimodalValue"
        @change="onMultimodalChange"
        title="多模态模式：自动判定(启发式) / 强制多模态(直送) / 强制非多模态(OCR)"
      >
        <option value="auto">自动判定</option>
        <option value="true">强制多模态</option>
        <option value="false">强制非多模态</option>
      </select>
    </td>
    <td class="py-2 text-center">
      <input
        type="checkbox"
        class="text-primary focus:ring-primary rounded"
        :checked="item.expose"
        @change="onExposeChange"
      />
    </td>
    <td class="py-2 text-center">
      <button
        class="text-red-500 hover:text-red-700 transition-colors flex items-center justify-center mx-auto cursor-pointer"
        @click="$emit('delete')"
      >
        <span class="material-symbols-outlined text-[18px]">delete</span>
      </button>
    </td>
  </tr>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import ModelSearchSelect from '../agent-config/ModelSearchSelect.vue';
import type { ModelMappingEntry } from './types';

const props = defineProps<{
  item: ModelMappingEntry;
  rowModels: string[];
  showInjectKwargs: boolean;
  isStale: boolean;
  isNew: boolean;
}>();

const emit = defineEmits<{
  'update:clientModel': [val: string];
  'update:targetModel': [val: string];
  'update:expose': [val: boolean];
  'update:injectKwargs': [val: boolean];
  'update:multimodal': [val: boolean | null];
  'delete': [];
}>();

const multimodalValue = computed(() => {
  if (props.item.multimodal === true) return 'true';
  if (props.item.multimodal === false) return 'false';
  return 'auto';
});

function onClientModelInput(e: Event) {
  const val = (e.target as HTMLInputElement).value.trim();
  emit('update:clientModel', val);
}

function onTargetModelSelect(val: string) {
  emit('update:targetModel', val);
}

function onExposeChange(e: Event) {
  emit('update:expose', (e.target as HTMLInputElement).checked);
}

function onInjectKwargsChange(e: Event) {
  emit('update:injectKwargs', (e.target as HTMLInputElement).checked);
}

function onMultimodalChange(e: Event) {
  const val = (e.target as HTMLSelectElement).value;
  if (val === 'true') emit('update:multimodal', true);
  else if (val === 'false') emit('update:multimodal', false);
  else emit('update:multimodal', null);
}
</script>
