<template>
  <div class="flex flex-col gap-0.5 w-full">
    <!-- Label row -->
    <div class="flex items-center gap-2">
      <label v-if="field.toggleable" class="flex items-center gap-1.5 cursor-pointer">
        <input
          type="checkbox"
          :checked="isEnabled"
          @change="onToggleEnable"
          class="w-3.5 h-3.5 accent-primary"
        />
        <span class="text-[12px] font-bold text-on-surface dark:text-white">{{ field.label }}</span>
      </label>
      <label v-else class="text-[12px] font-bold text-on-surface dark:text-white">{{ field.label }}</label>
    </div>
    <p v-if="field.description && field.type !== 'object'" class="text-[11px] text-outline leading-relaxed">{{ field.description }}</p>

    <!-- Input row -->
    <div v-if="!field.toggleable || isEnabled">
      <!-- string -->
      <div v-if="field.type === 'string'" :class="field.secret ? 'relative' : 'contents'">
        <input
          :type="field.secret ? (revealSecret ? 'text' : 'password') : 'text'"
          :value="displayValue"
          :placeholder="field.placeholder || ''"
          @input="emitValue(($event.target as HTMLInputElement).value)"
          :class="['px-3 py-2 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:outline-none text-on-surface dark:text-white w-full', field.secret ? 'pr-10' : '']"
        />
        <button
          v-if="field.secret"
          type="button"
          @click="revealSecret = !revealSecret"
          class="absolute inset-y-0 right-0 flex items-center pr-3 text-slate-400 hover:text-on-surface dark:hover:text-white cursor-pointer"
          :title="revealSecret ? '隐藏' : '显示'"
        >
          <span class="material-symbols-outlined text-[16px]">{{ revealSecret ? 'visibility_off' : 'visibility' }}</span>
        </button>
      </div>

      <!-- number -->
      <input
        v-else-if="field.type === 'number'"
        type="number"
        :value="displayValue"
        :placeholder="field.placeholder || ''"
        @input="emitValue(Number(($event.target as HTMLInputElement).value))"
        class="px-3 py-2 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:outline-none text-on-surface dark:text-white w-full"
      />

      <!-- boolean -->
      <label v-else-if="field.type === 'boolean'" class="relative inline-flex items-center cursor-pointer">
        <input type="checkbox" :checked="Boolean(displayValue)" @change="emitValue(($event.target as HTMLInputElement).checked)" class="sr-only peer" />
        <div class="w-11 h-6 bg-slate-200 dark:bg-white/10 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full rtl:peer-checked:after:-translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:start-[2px] after:bg-white after:border-gray-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all dark:border-gray-600 peer-checked:bg-primary"></div>
      </label>

      <!-- select (with optional custom input mode) -->
      <div v-else-if="field.type === 'select'" class="flex items-center gap-2 w-full">
        <!-- 自定义输入模式 -->
        <template v-if="isCustomInputMode">
          <input
            type="text"
            :value="displayValue"
            :placeholder="field.placeholder || '输入自定义值 (如 32768, max 等)'"
            @input="emitValue(($event.target as HTMLInputElement).value)"
            class="px-3 py-2 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:border-primary focus:outline-none text-on-surface dark:text-white flex-1 font-mono min-w-0"
          />
          <button
            v-if="field.allowCustom || isValueNotInOptions"
            type="button"
            @click="switchToSelectMode"
            class="flex items-center gap-1 px-2.5 py-2 text-[11px] bg-slate-100 dark:bg-white/5 hover:bg-slate-200 dark:hover:bg-white/10 border border-outline-variant/40 rounded-md cursor-pointer text-slate-600 dark:text-slate-300 transition-colors shrink-0 select-none font-medium"
            title="切换回预设选项列表"
          >
            <span class="material-symbols-outlined text-[15px] text-primary">list</span>
            <span>预设选项</span>
          </button>
        </template>

        <!-- 预设下拉选择模式 -->
        <template v-else>
          <select
            :value="selectDropdownValue"
            @change="onSelectChange(($event.target as HTMLSelectElement).value)"
            class="px-3 py-2 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:border-primary focus:outline-none text-on-surface dark:text-white flex-1 font-medium min-w-0"
          >
            <option value="">未设置</option>
            <option v-for="opt in field.options" :key="opt" :value="opt">{{ opt }}</option>
            <option v-if="field.allowCustom" value="__custom__">✏️ 自定义输入...</option>
          </select>
          <button
            v-if="field.allowCustom"
            type="button"
            @click="switchToCustomMode"
            class="flex items-center gap-1 px-2.5 py-2 text-[11px] bg-slate-100 dark:bg-white/5 hover:bg-slate-200 dark:hover:bg-white/10 border border-outline-variant/40 rounded-md cursor-pointer text-slate-600 dark:text-slate-300 transition-colors shrink-0 select-none font-medium"
            title="切换为自定义文本输入"
          >
            <span class="material-symbols-outlined text-[15px] text-primary">edit</span>
            <span>自定义</span>
          </button>
        </template>
      </div>

      <!-- model-select (dynamically populated from relay model mapping, with optional 1M switch) -->
      <div v-else-if="field.type === 'model-select'" class="flex items-center gap-2 w-full">
        <ModelSearchSelect
          :model-value="baseModelValue"
          :options="modelOptions"
          :placeholder="field.placeholder || '搜索或选择模型...'"
          :refresh-on-open="true"
          @update:model-value="onModelSelect"
          @refresh="emit('refresh-models')"
          class="flex-1 min-w-0"
        />
        <label
          v-if="field.with1mSuffix !== false"
          class="flex items-center gap-1.5 px-2.5 py-2 bg-slate-100 dark:bg-white/5 border border-outline-variant/40 rounded-md cursor-pointer hover:bg-slate-200 dark:hover:bg-white/10 transition-colors shrink-0 select-none"
          title="勾选后自动为模型 ID 追加 [1M] 后缀以启用 1M 上下文模式"
        >
          <input
            type="checkbox"
            :checked="is1mChecked"
            @change="onToggle1m(($event.target as HTMLInputElement).checked)"
            class="w-3.5 h-3.5 accent-primary rounded cursor-pointer"
          />
          <span class="text-[11px] font-bold" :class="is1mChecked ? 'text-primary' : 'text-slate-500 dark:text-slate-400'">1M</span>
        </label>
      </div>

      <!-- array (textarea, newline-separated, supports objects as JSON per line) -->
      <textarea
        v-else-if="field.type === 'array'"
        :value="arrayDisplayValue"
        :placeholder="field.description || '每行一个'"
        @input="onArrayInput(($event.target as HTMLTextAreaElement).value)"
        class="px-3 py-2 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:outline-none text-on-surface dark:text-white w-full font-mono resize-y"
        style="min-height: 80px;"
      ></textarea>

      <!-- kv-list (textarea, KEY=VALUE per line) -->
      <textarea
        v-else-if="field.type === 'kv-list'"
        :value="displayValue"
        :placeholder="'KEY=VALUE\n每行一个'"
        @input="emitValue(($event.target as HTMLTextAreaElement).value)"
        class="px-3 py-2 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:outline-none text-on-surface dark:text-white w-full font-mono resize-y"
        style="min-height: 60px;"
      ></textarea>

      <!-- object (info text only, full editing via JSON editor) -->
      <div v-else-if="field.type === 'object'" class="text-[11px] text-outline italic">
        {{ field.description || '请在右侧 JSON 编辑器中直接配置' }}
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue';
import { ConfigField } from './types';
import ModelSearchSelect from './ModelSearchSelect.vue';

const props = defineProps<{
  field: ConfigField;
  value?: any;
  nameResolver?: string;
  availableModels?: string[];
}>();

const emit = defineEmits<{
  'update:value': [value: any];
  /** 下拉打开时请求父级刷新模型列表（中继映射可能已更新） */
  'refresh-models': [];
}>();

const enabledSet = ref<Set<string>>(new Set());
const revealSecret = ref(false);
const enabledKey = computed(() => {
  const namePart = props.nameResolver ? `_${props.nameResolver}` : '';
  return `${props.field.key}${namePart}_enabled`;
});

const isEnabled = computed(() => {
  if (enabledSet.value.has(enabledKey.value)) return true;
  return props.value !== undefined && props.value !== null && props.value !== '';
});

const fieldDefault = computed(() => {
  return props.field.default !== undefined ? props.field.default : '';
});

const displayValue = computed(() => {
  return props.value !== undefined ? props.value : fieldDefault.value;
});

const baseModelValue = computed(() => {
  const str = String(displayValue.value || '');
  if (str.endsWith('[1M]') || str.endsWith('[1m]')) {
    return str.slice(0, -4);
  }
  return str;
});

const is1mChecked = computed(() => {
  const str = String(displayValue.value || '');
  return str.endsWith('[1M]') || str.endsWith('[1m]');
});

const modelOptions = computed(() => {
  const list = [...(props.availableModels || [])];
  const current = baseModelValue.value;
  if (current && !list.includes(current)) {
    list.unshift(current);
  }
  return list;
});

// ===== select 字段自定义输入模式逻辑 =====
const customModeForced = ref(false);

const isValueNotInOptions = computed(() => {
  if (props.field.type !== 'select') return false;
  const val = displayValue.value;
  if (val === undefined || val === null || val === '') return false;
  const opts = props.field.options || [];
  return !opts.includes(String(val));
});

const isCustomInputMode = computed(() => {
  if (props.field.type !== 'select') return false;
  if (customModeForced.value) return true;
  return isValueNotInOptions.value;
});

const selectDropdownValue = computed(() => {
  if (isValueNotInOptions.value) return '__custom__';
  return displayValue.value;
});

function onSelectChange(val: string) {
  if (val === '__custom__') {
    customModeForced.value = true;
    return;
  }
  customModeForced.value = false;
  emitValue(val);
}

function switchToCustomMode() {
  customModeForced.value = true;
}

function switchToSelectMode() {
  customModeForced.value = false;
  const opts = props.field.options || [];
  if (isValueNotInOptions.value) {
    emitValue(opts.length > 0 ? opts[0] : '');
  }
}

function onModelSelect(val: string) {
  if (!val) {
    emitValue('');
    return;
  }
  const finalVal = (props.field.with1mSuffix !== false && is1mChecked.value) ? `${val}[1M]` : val;
  emitValue(finalVal);
}

function onToggle1m(checked: boolean) {
  const base = baseModelValue.value;
  if (!base) return;
  const finalVal = checked ? `${base}[1M]` : base;
  emitValue(finalVal);
}

function onToggleEnable(e: Event) {
  const checked = (e.target as HTMLInputElement).checked;
  if (checked) {
    enabledSet.value.add(enabledKey.value);
    if (props.value === undefined || props.value === null) {
      emit('update:value', fieldDefault.value);
    }
  } else {
    enabledSet.value.delete(enabledKey.value);
    emit('update:value', undefined);
  }
}

function emitValue(val: any) {
  emit('update:value', val);
}

// ===== array 字段双向解析与安全显示（防止对象数组渲染为 [object Object]） =====
const arrayDisplayValue = computed(() => {
  const val = props.value !== undefined ? props.value : fieldDefault.value;
  if (!Array.isArray(val)) {
    return typeof val === 'string' ? val : (val ? String(val) : '');
  }
  return val.map((item) => {
    if (typeof item === 'object' && item !== null) {
      return JSON.stringify(item);
    }
    return String(item);
  }).join('\n');
});

function onArrayInput(text: string) {
  const lines = text.split('\n').map((l) => l.trim()).filter((l) => l.length > 0);
  const parsed = lines.map((line) => {
    if ((line.startsWith('{') && line.endsWith('}')) || (line.startsWith('[') && line.endsWith(']'))) {
      try {
        return JSON.parse(line);
      } catch {
        return line;
      }
    }
    return line;
  });
  emitValue(parsed);
}
</script>
