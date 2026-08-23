<template>
  <div class="relative w-full select-none" ref="containerRef">
    <!-- 输入/触发框 -->
    <div
      class="flex items-center gap-1.5 px-3 py-1.5 text-[12px] bg-slate-50 dark:bg-white/5 border rounded-md transition-all cursor-text"
      :class="[
        isOpen ? 'border-primary ring-1 ring-primary/30 shadow-sm' : 'border-outline-variant/60 hover:border-outline-variant',
        disabled ? 'opacity-50 cursor-not-allowed' : ''
      ]"
      @click="onBoxClick"
    >
      <span class="material-symbols-outlined text-[16px] text-slate-400 dark:text-slate-500 shrink-0">search</span>
      
      <input
        ref="inputRef"
        type="text"
        :value="inputValue"
        :placeholder="placeholder || '搜索或选择模型...'"
        :disabled="disabled"
        @input="onInput"
        @focus="onFocus"
        @keydown="onKeyDown"
        class="w-full bg-transparent focus:outline-none text-on-surface dark:text-white font-medium text-[12px] placeholder:text-slate-400 dark:placeholder:text-slate-500 min-w-0"
      />

      <!-- 清空按钮 -->
      <button
        v-if="modelValue || searchQuery"
        type="button"
        @click.stop="onClear"
        class="p-0.5 text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 transition-colors shrink-0 cursor-pointer rounded"
        title="清空"
      >
        <span class="material-symbols-outlined text-[14px]">close</span>
      </button>

      <!-- 下拉展开图标 -->
      <button
        type="button"
        @click.stop="toggleOpen"
        class="p-0.5 text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 transition-transform shrink-0 cursor-pointer"
        :class="isOpen ? 'rotate-180 text-primary' : ''"
      >
        <span class="material-symbols-outlined text-[16px]">expand_more</span>
      </button>
    </div>

    <!-- 下拉浮层 -->
    <div
      v-if="isOpen"
      class="absolute left-0 right-0 top-full mt-1.5 z-50 bg-white dark:bg-[#1a1f30] border border-outline-variant/40 dark:border-white/10 rounded-lg shadow-xl overflow-hidden flex flex-col max-h-64 animate-in fade-in zoom-in-95 duration-100"
    >
      <!-- 浮层顶部统计信息 -->
      <div class="flex items-center justify-between px-3 py-1.5 bg-slate-50 dark:bg-white/5 border-b border-outline-variant/30 text-[11px] text-slate-500 dark:text-slate-400 font-medium">
        <span>
          {{ searchQuery ? `找到 ${filteredOptions.length} 个匹配` : `共 ${options.length} 个模型可用` }}
        </span>
        <span v-if="searchQuery" class="text-primary font-mono text-[10px]">搜索中: "{{ searchQuery }}"</span>
      </div>

      <!-- 选项列表 -->
      <div class="overflow-y-auto overflow-x-hidden flex-1 py-1 divide-y divide-transparent">
        <!-- 未设置选项 -->
        <div
          class="flex items-center justify-between px-3 py-2 text-[12px] cursor-pointer transition-colors"
          :class="[
            !modelValue ? 'bg-primary/10 text-primary font-bold dark:bg-primary/20' : 'text-slate-500 dark:text-slate-400 hover:bg-slate-100 dark:hover:bg-white/5',
            highlightedIndex === -1 ? 'bg-slate-100 dark:bg-white/10' : ''
          ]"
          @click="selectOption('')"
        >
          <div class="flex items-center gap-2">
            <span class="material-symbols-outlined text-[15px] text-slate-400">block</span>
            <span>未设置 (清空)</span>
          </div>
          <span v-if="!modelValue" class="material-symbols-outlined text-[16px] text-primary">check</span>
        </div>

        <!-- 匹配的模型项列表 -->
        <div
          v-for="(item, idx) in filteredOptions"
          :key="item"
          class="flex items-center justify-between px-3 py-2 text-[12px] cursor-pointer transition-colors font-mono"
          :class="[
            item === modelValue ? 'bg-primary/10 text-primary font-bold dark:bg-primary/20' : 'text-on-surface dark:text-slate-200 hover:bg-slate-100 dark:hover:bg-white/5',
            highlightedIndex === idx ? 'bg-slate-100 dark:bg-white/10' : ''
          ]"
          @click="selectOption(item)"
          @mouseenter="highlightedIndex = idx"
        >
          <div class="flex items-center gap-2 truncate min-w-0 pr-2">
            <span class="material-symbols-outlined text-[15px] text-primary/70 shrink-0">smart_toy</span>
            <span class="truncate" v-html="highlightMatch(item, searchQuery)"></span>
          </div>
          <span v-if="item === modelValue" class="material-symbols-outlined text-[16px] text-primary shrink-0">check</span>
        </div>

        <!-- 自定义输入选项（当搜索词不在选项列表中时） -->
        <div
          v-if="allowCustom !== false && isCustomOptionAvailable"
          class="flex items-center justify-between px-3 py-2 text-[12px] text-amber-600 dark:text-amber-400 bg-amber-500/5 hover:bg-amber-500/10 cursor-pointer transition-colors border-t border-dashed border-amber-500/30"
          :class="highlightedIndex === filteredOptions.length ? 'bg-amber-500/20' : ''"
          @click="selectOption(searchQuery.trim())"
          @mouseenter="highlightedIndex = filteredOptions.length"
        >
          <div class="flex items-center gap-2 truncate min-w-0">
            <span class="material-symbols-outlined text-[15px] text-amber-500 shrink-0">edit_note</span>
            <span class="truncate">使用自定义模型: <strong class="font-bold underline">{{ searchQuery.trim() }}</strong></span>
          </div>
          <span class="text-[10px] px-1.5 py-0.5 rounded bg-amber-500/20 text-amber-600 dark:text-amber-300 shrink-0">回车选用</span>
        </div>

        <!-- 无匹配且不允许自定义 -->
        <div
          v-if="filteredOptions.length === 0 && (!isCustomOptionAvailable || allowCustom === false)"
          class="px-3 py-4 text-center text-[12px] text-slate-400 dark:text-slate-500 italic"
        >
          无匹配的模型
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue';

const props = withDefaults(
  defineProps<{
    modelValue?: string;
    options?: string[];
    placeholder?: string;
    disabled?: boolean;
    allowCustom?: boolean;
    /** 打开下拉时是否触发 refresh 事件（由父级注入刷新逻辑，如重新拉取中继映射） */
    refreshOnOpen?: boolean;
  }>(),
  {
    modelValue: '',
    options: () => [],
    placeholder: '搜索或选择模型...',
    disabled: false,
    allowCustom: true,
    refreshOnOpen: false,
  }
);

const emit = defineEmits<{
  'update:modelValue': [val: string];
  'change': [val: string];
  /** 下拉打开时触发（仅当 refreshOnOpen=true），由父级用于重新拉取数据并更新 options */
  'refresh': [];
}>();

const containerRef = ref<HTMLElement | null>(null);
const inputRef = ref<HTMLInputElement | null>(null);
const isOpen = ref(false);
const searchQuery = ref('');
const highlightedIndex = ref(-1);

// 输入框中显示的文本：打开浮层并正在搜索时显示 searchQuery，否则显示选中的 modelValue
const inputValue = computed(() => {
  if (isOpen.value) {
    return searchQuery.value;
  }
  return props.modelValue || '';
});

// 过滤模型列表（忽略大小写包含匹配）
const filteredOptions = computed(() => {
  const q = searchQuery.value.trim().toLowerCase();
  const list = props.options || [];
  if (!q) {
    return list;
  }
  return list.filter(item => item.toLowerCase().includes(q));
});

// 是否展示“使用自定义模型”
const isCustomOptionAvailable = computed(() => {
  const q = searchQuery.value.trim();
  if (!q) return false;
  const list = props.options || [];
  return !list.includes(q);
});

// 关键词高亮
function highlightMatch(text: string, query: string): string {
  const q = query.trim();
  if (!q) return escapeHtml(text);
  const regex = new RegExp(`(${escapeRegex(q)})`, 'gi');
  return escapeHtml(text).replace(regex, '<span class="text-primary font-bold underline bg-primary/10 px-0.5 rounded">$1</span>');
}

function escapeHtml(str: string): string {
  return str
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#039;');
}

function escapeRegex(str: string): string {
  return str.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

function onBoxClick() {
  if (props.disabled) return;
  if (!isOpen.value) {
    openDropdown();
  }
}

function toggleOpen() {
  if (props.disabled) return;
  if (isOpen.value) {
    closeDropdown();
  } else {
    openDropdown();
  }
}

function openDropdown() {
  isOpen.value = true;
  searchQuery.value = '';
  highlightedIndex.value = -1;
  // 打开时按需通知父级刷新模型列表（如中继映射可能已在其他面板更新）
  if (props.refreshOnOpen) {
    emit('refresh');
  }
  nextTick(() => {
    inputRef.value?.focus();
    inputRef.value?.select();
  });
}

function closeDropdown() {
  isOpen.value = false;
  searchQuery.value = '';
  highlightedIndex.value = -1;
}

function onFocus() {
  if (props.disabled) return;
  if (!isOpen.value) {
    openDropdown();
  }
}

function onInput(e: Event) {
  const val = (e.target as HTMLInputElement).value;
  searchQuery.value = val;
  if (!isOpen.value) {
    isOpen.value = true;
  }
  highlightedIndex.value = 0;
}

function onClear() {
  selectOption('');
}

function selectOption(val: string) {
  emit('update:modelValue', val);
  emit('change', val);
  closeDropdown();
}

function onKeyDown(e: KeyboardEvent) {
  if (!isOpen.value) {
    if (e.key === 'ArrowDown' || e.key === 'Enter') {
      e.preventDefault();
      openDropdown();
    }
    return;
  }

  const listLength = filteredOptions.value.length;
  const maxIndex = isCustomOptionAvailable.value ? listLength : listLength - 1;

  if (e.key === 'ArrowDown') {
    e.preventDefault();
    if (highlightedIndex.value < maxIndex) {
      highlightedIndex.value++;
    } else {
      highlightedIndex.value = -1; // 回到未设置
    }
  } else if (e.key === 'ArrowUp') {
    e.preventDefault();
    if (highlightedIndex.value > -1) {
      highlightedIndex.value--;
    } else {
      highlightedIndex.value = maxIndex;
    }
  } else if (e.key === 'Enter') {
    e.preventDefault();
    if (highlightedIndex.value === -1) {
      selectOption('');
    } else if (highlightedIndex.value >= 0 && highlightedIndex.value < listLength) {
      selectOption(filteredOptions.value[highlightedIndex.value]);
    } else if (highlightedIndex.value === listLength && isCustomOptionAvailable.value) {
      selectOption(searchQuery.value.trim());
    } else if (searchQuery.value.trim() && props.allowCustom !== false) {
      selectOption(searchQuery.value.trim());
    }
  } else if (e.key === 'Escape') {
    e.preventDefault();
    closeDropdown();
  }
}

function handleClickOutside(event: MouseEvent) {
  if (containerRef.value && !containerRef.value.contains(event.target as Node)) {
    closeDropdown();
  }
}

onMounted(() => {
  document.addEventListener('click', handleClickOutside);
});

onUnmounted(() => {
  document.removeEventListener('click', handleClickOutside);
});
</script>
