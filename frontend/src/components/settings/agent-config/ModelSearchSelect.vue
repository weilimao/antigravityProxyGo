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
      
      <!-- 多选模式: chips 常驻回显 + 搜索输入框并排(下拉开合均可见) -->
      <template v-if="multiple && selectedArray.length > 0">
        <div class="flex flex-wrap items-center gap-1 flex-1 min-w-0 py-0.5">
          <span
            v-for="m in selectedArray.slice(0, maxChips)"
            :key="m"
            class="inline-flex items-center gap-0.5 px-1.5 py-0.5 rounded bg-primary/10 text-primary dark:bg-primary/20 dark:text-primary-fixed-dim text-[11px] font-mono max-w-[180px]"
            :title="m"
          >
            <span class="truncate">{{ m }}</span>
            <button
              type="button"
              class="shrink-0 hover:text-error cursor-pointer"
              @click.stop="removeChip(m)"
            >
              <span class="material-symbols-outlined text-[12px]">close</span>
            </button>
          </span>
          <span v-if="selectedArray.length > maxChips" class="text-[11px] text-outline shrink-0">
            +{{ selectedArray.length - maxChips }}
          </span>
          <input
            ref="inputRef"
            type="text"
            :value="isOpen ? searchQuery : ''"
            :placeholder="isOpen ? (placeholder || '搜索并勾选...') : `已选 ${selectedArray.length} 个`"
            :disabled="disabled"
            @input="onInput"
            @focus="onFocus"
            @keydown="onKeyDown"
            class="flex-1 min-w-[100px] bg-transparent focus:outline-none text-on-surface dark:text-white font-medium text-[12px] placeholder:text-slate-400 dark:placeholder:text-slate-500"
          />
        </div>
      </template>
      <input
        v-else
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
        v-if="hasSelection || searchQuery"
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

    <!-- 下拉浮层 (Teleport 到 body 避免被父级 modal/容器 overflow 裁剪，置为最高层级) -->
    <Teleport to="body">
      <div
        v-if="isOpen"
        ref="dropdownRef"
        :style="dropdownStyle"
        class="fixed z-[100000] bg-white dark:bg-[#1a1f30] border border-outline-variant/40 dark:border-white/10 rounded-lg shadow-2xl overflow-hidden flex flex-col animate-in fade-in zoom-in-95 duration-100"
      >
        <!-- 浮层顶部统计信息 -->
        <div class="flex items-center justify-between px-3 py-1.5 bg-slate-50 dark:bg-white/5 border-b border-outline-variant/30 text-[11px] text-slate-500 dark:text-slate-400 font-medium shrink-0">
          <span>
            {{ searchQuery ? `找到 ${filteredOptions.length} 个匹配` : (options.length === 0 ? '暂无可用模型' : `共 ${options.length} 个模型可用`) }}
          </span>
          <!-- 多选模式: 全选当前过滤结果 / 清空 -->
          <div v-if="multiple" class="flex items-center gap-2 shrink-0">
            <button
              type="button"
              class="text-primary hover:underline cursor-pointer"
              @click.stop="selectAllFiltered"
            >全选{{ searchQuery ? '匹配项' : '' }}</button>
            <span v-if="selectedArray.length > 0" class="text-outline">|</span>
            <button
              v-if="selectedArray.length > 0"
              type="button"
              class="text-error hover:underline cursor-pointer"
              @click.stop="clearSelection"
            >清空({{ selectedArray.length }})</button>
          </div>
          <span v-else-if="searchQuery" class="text-primary font-mono text-[10px]">搜索中: "{{ searchQuery }}"</span>
        </div>

        <!-- 选项列表 -->
        <div class="overflow-y-auto overflow-x-hidden flex-1 py-1 divide-y divide-transparent">
          <!-- 未设置选项 (单选模式) -->
          <div
            v-if="!multiple"
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

          <!-- 匹配的模型项列表(仅渲染前 MAX_RENDER_OPTIONS 条,超大号池全量平铺会卡) -->
          <div
            v-for="(item, idx) in visibleOptions"
            :key="item"
            class="flex items-center justify-between px-3 py-2 text-[12px] cursor-pointer transition-colors font-mono"
            :class="[
              isSelected(item) ? 'bg-primary/10 text-primary font-bold dark:bg-primary/20' : 'text-on-surface dark:text-slate-200 hover:bg-slate-100 dark:hover:bg-white/5',
              highlightedIndex === idx ? 'bg-slate-100 dark:bg-white/10' : ''
            ]"
            @click="onItemClick(item)"
            @mouseenter="highlightedIndex = idx"
          >
            <div class="flex items-center gap-2 truncate min-w-0 pr-2">
              <!-- 多选模式: 勾选框 -->
              <span
                v-if="multiple"
                class="material-symbols-outlined text-[16px] shrink-0"
                :class="isSelected(item) ? 'text-primary' : 'text-slate-300 dark:text-slate-600'"
              >{{ isSelected(item) ? 'check_box' : 'check_box_outline_blank' }}</span>
              <span v-else class="material-symbols-outlined text-[15px] text-primary/70 shrink-0">smart_toy</span>
              <span class="truncate" v-html="highlightMatch(item, searchQuery)"></span>
            </div>
            <span v-if="!multiple && item === modelValue" class="material-symbols-outlined text-[16px] text-primary shrink-0">check</span>
          </div>

          <!-- 截断提示:过滤结果超出渲染上限时引导输入关键词缩小范围 -->
          <div
            v-if="hasMoreOptions"
            class="px-3 py-1.5 text-[11px] text-slate-400 dark:text-slate-500 italic border-t border-outline-variant/20"
          >
            仅渲染前 {{ visibleOptions.length }} 项(共 {{ filteredOptions.length }} 个匹配)，输入关键词可缩小范围
          </div>

          <!-- 自定义输入选项（当搜索词不在选项列表中时） -->
          <div
            v-if="allowCustom !== false && isCustomOptionAvailable"
            class="flex items-center justify-between px-3 py-2 text-[12px] cursor-pointer transition-colors border-t border-dashed border-amber-500/30"
            :class="highlightedIndex === visibleOptions.length ? 'bg-amber-500/20' : ''"
            @click="selectOption(searchQuery.trim())"
            @mouseenter="highlightedIndex = visibleOptions.length"
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
            {{ (!options || options.length === 0) && !searchQuery ? '暂无可用模型（号池无账号或未同步模型）' : '无匹配的模型' }}
          </div>
        </div>
      </div>
    </Teleport>
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
    /** 多选模式: true 时 modelValue 为数组语义, 以 chip 展示已选项, 点击项切换勾选 */
    multiple?: boolean;
    /** 多选模式的选中数组 (v-model:modelIds) */
    modelIds?: string[];
    /** 多选模式下 chip 最多直显个数, 超出折叠为 +N */
    maxChips?: number;
  }>(),
  {
    modelValue: '',
    options: () => [],
    placeholder: '搜索或选择模型...',
    disabled: false,
    allowCustom: true,
    refreshOnOpen: false,
    multiple: false,
    modelIds: () => [],
    maxChips: 6,
  }
);

const emit = defineEmits<{
  'update:modelValue': [val: string];
  'change': [val: string];
  /** 下拉打开时触发（仅当 refreshOnOpen=true），由父级用于重新拉取数据并更新 options */
  'refresh': [];
  /** 多选模式专用: 选中数组整体变化 */
  'update:modelIds': [val: string[]];
  'changeMultiple': [val: string[]];
  /** 下拉未打开且有值时按 Enter 触发快捷提交 */
  'submit': [val: string];
}>();

const containerRef = ref<HTMLElement | null>(null);
const dropdownRef = ref<HTMLElement | null>(null);
const inputRef = ref<HTMLInputElement | null>(null);
const isOpen = ref(false);
const searchQuery = ref('');
const highlightedIndex = ref(-1);
const dropdownStyle = ref<Record<string, string>>({});

// ===== 多选模式内部状态 =====
// 选中集合以 prop.modelIds 为准(v-model:modelIds), 内部不持有副本, 直接透传父级。
const selectedArray = computed<string[]>(() => {
  if (!props.multiple) return [];
  return Array.isArray(props.modelIds) ? props.modelIds : [];
});
const hasSelection = computed(() => (props.multiple ? selectedArray.value.length > 0 : !!props.modelValue));

function isSelected(item: string): boolean {
  if (props.multiple) return selectedArray.value.includes(item);
  return item === props.modelValue;
}

/** 多选: 切换某项勾选状态(不关闭下拉) */
function toggleItem(item: string) {
  const cur = [...selectedArray.value];
  const idx = cur.indexOf(item);
  if (idx >= 0) {
    cur.splice(idx, 1);
  } else {
    cur.push(item);
  }
  emit('update:modelIds', cur);
  emit('changeMultiple', cur);
}

/** 多选: 全选当前过滤结果 */
function selectAllFiltered() {
  const cur = [...selectedArray.value];
  for (const item of filteredOptions.value) {
    if (!cur.includes(item)) cur.push(item);
  }
  emit('update:modelIds', cur);
  emit('changeMultiple', cur);
}

/** 多选: 清空全部选中 */
function clearSelection() {
  emit('update:modelIds', []);
  emit('changeMultiple', []);
}

/** 多选: 删除一个 chip */
function removeChip(item: string) {
  toggleItem(item);
}

/** 条目点击: 多选=切换勾选(保留下拉), 单选=原行为 */
function onItemClick(item: string) {
  if (props.multiple) {
    toggleItem(item);
    return;
  }
  selectOption(item);
}

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

// 实际渲染的选项上限:几百个模型的全量平铺(每项含正则高亮)会让下拉打开/输入明显卡顿。
const MAX_RENDER_OPTIONS = 100;
const visibleOptions = computed(() => filteredOptions.value.slice(0, MAX_RENDER_OPTIONS));
const hasMoreOptions = computed(() => filteredOptions.value.length > visibleOptions.value.length);

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

function updatePosition() {
  if (!containerRef.value || !isOpen.value) return;
  const rect = containerRef.value.getBoundingClientRect();

  // 如果触发框脱离渲染或不可见（如父级弹窗隐藏）
  if (rect.width === 0 && rect.height === 0) {
    closeDropdown();
    return;
  }

  const dropdownMaxHeight = 256;
  const spaceBelow = window.innerHeight - rect.bottom;
  const spaceAbove = rect.top;
  const gap = 4;

  // 判断方向：当下方空间少于 220px 且上方空间大于下方空间时，向上展开
  const placeAbove = spaceBelow < 220 && spaceAbove > spaceBelow;

  const style: Record<string, string> = {
    left: `${Math.max(8, rect.left)}px`,
    width: `${rect.width}px`,
  };

  if (placeAbove) {
    const availableHeight = Math.min(dropdownMaxHeight, Math.max(100, spaceAbove - gap - 16));
    style.bottom = `${window.innerHeight - rect.top + gap}px`;
    style.maxHeight = `${availableHeight}px`;
  } else {
    const availableHeight = Math.min(dropdownMaxHeight, Math.max(100, spaceBelow - gap - 16));
    style.top = `${rect.bottom + gap}px`;
    style.maxHeight = `${availableHeight}px`;
  }

  dropdownStyle.value = style;
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
  updatePosition();
  // 打开时按需通知父级刷新模型列表（如中继映射可能已在其他面板更新）
  if (props.refreshOnOpen) {
    emit('refresh');
  }
  nextTick(() => {
    updatePosition();
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
  nextTick(() => {
    updatePosition();
  });
}

function onClear() {
  if (props.multiple) {
    clearSelection();
    return;
  }
  selectOption('');
}

function selectOption(val: string) {
  emit('update:modelValue', val);
  emit('change', val);
  closeDropdown();
}

function onKeyDown(e: KeyboardEvent) {
  if (!isOpen.value) {
    if (e.key === 'Enter' && props.modelValue) {
      e.preventDefault();
      emit('submit', props.modelValue);
      return;
    }
    if (e.key === 'ArrowDown' || e.key === 'Enter') {
      e.preventDefault();
      openDropdown();
    }
    return;
  }

  // 键盘导航以实际渲染的 visibleOptions 为准(截断时下标与 DOM 一一对应)。
  const listLength = visibleOptions.value.length;
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
    if (props.multiple) {
      // 多选: Enter = 切换当前高亮项(保持下拉打开), 与点击行为一致
      if (highlightedIndex.value >= 0 && highlightedIndex.value < listLength) {
        toggleItem(visibleOptions.value[highlightedIndex.value]);
      }
      return;
    }
    if (highlightedIndex.value === -1) {
      selectOption('');
    } else if (highlightedIndex.value >= 0 && highlightedIndex.value < listLength) {
      selectOption(visibleOptions.value[highlightedIndex.value]);
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
  const target = event.target as Node;
  const inContainer = containerRef.value?.contains(target);
  const inDropdown = dropdownRef.value?.contains(target);
  if (!inContainer && !inDropdown) {
    closeDropdown();
  }
}

function handleScrollOrResize() {
  if (isOpen.value) {
    updatePosition();
  }
}

// 监听 options 变化自适应刷新位置
watch(() => props.options, () => {
  if (isOpen.value) {
    nextTick(updatePosition);
  }
});

onMounted(() => {
  document.addEventListener('click', handleClickOutside);
  window.addEventListener('resize', handleScrollOrResize);
  window.addEventListener('scroll', handleScrollOrResize, true);
});

onUnmounted(() => {
  document.removeEventListener('click', handleClickOutside);
  window.removeEventListener('resize', handleScrollOrResize);
  window.removeEventListener('scroll', handleScrollOrResize, true);
});
</script>
