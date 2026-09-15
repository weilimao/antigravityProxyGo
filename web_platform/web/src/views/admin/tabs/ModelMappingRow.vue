<template>
  <tr class="border-b border-slate-800 hover:bg-slate-800/50 transition-colors">
    <td class="py-2.5 px-3">
      <input
        type="text"
        class="input-dark w-full text-xs"
        :value="item.clientModel"
        @input="onClientModelInput"
        placeholder="例如: gpt-4o"
      />
    </td>
    <td class="py-2.5 px-3">
      <div class="flex items-center gap-2">
        <input
          type="text"
          class="input-dark w-full text-xs"
          :value="item.targetModel"
          @input="onTargetModelInput"
          :list="datalistId"
          placeholder="输入或选择目标模型..."
        />
        <datalist :id="datalistId">
          <option v-for="mod in rowModels" :key="mod" :value="mod"></option>
        </datalist>
        <span v-if="isNew" class="shrink-0 px-1.5 py-0.5 rounded text-[10px] font-bold bg-emerald-500/15 text-emerald-400 border border-emerald-500/30">新增</span>
        <span v-if="isStale" class="shrink-0 px-1.5 py-0.5 rounded text-[10px] font-bold bg-rose-500/15 text-rose-400 border border-rose-500/30">已删除</span>
      </div>
    </td>
    <td v-if="showInjectKwargs" class="py-2.5 px-3 text-center">
      <input
        type="checkbox"
        class="accent-indigo-500 w-4 h-4 cursor-pointer"
        :checked="item.injectChatTemplateKwargs !== false"
        @change="onInjectKwargsChange"
        title="是否注入 chat_template_kwargs"
      />
    </td>
    <td class="py-2.5 px-3 text-center">
      <select
        class="input-dark py-1 px-2 text-xs cursor-pointer w-28 text-center"
        :value="multimodalValue"
        @change="onMultimodalChange"
      >
        <option value="auto">自动判定</option>
        <option value="true">强制多模态</option>
        <option value="false">强制非多模态</option>
      </select>
    </td>
    <td class="py-2.5 px-3 text-center">
      <input
        type="checkbox"
        class="accent-indigo-500 w-4 h-4 cursor-pointer"
        :checked="item.expose"
        @change="onExposeChange"
      />
    </td>
    <td class="py-2.5 px-3 text-right">
      <button
        class="text-slate-500 hover:text-rose-400 transition-colors inline-flex items-center justify-center p-1"
        @click="$emit('delete')"
        title="删除映射行"
      >
        <span class="material-symbols-outlined text-[18px]">delete</span>
      </button>
    </td>
  </tr>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import type { ModelMappingEntry } from '../../../composables/mappingTypes';

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

const datalistId = computed(() => 'dl_' + (props.item._rowKey || Math.random().toString(36).substring(2, 9)));

const multimodalValue = computed(() => {
  if (props.item.multimodal === true) return 'true';
  if (props.item.multimodal === false) return 'false';
  return 'auto';
});

function onClientModelInput(e: Event) {
  const val = (e.target as HTMLInputElement).value.trim();
  emit('update:clientModel', val);
}

function onTargetModelInput(e: Event) {
  const val = (e.target as HTMLInputElement).value.trim();
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
