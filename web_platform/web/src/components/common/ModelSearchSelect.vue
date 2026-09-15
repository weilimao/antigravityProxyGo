<template>
  <div class="relative w-full select-none" ref="containerRef">
    <!-- 输入/触发框 -->
    <div
      class="flex items-center gap-1.5 px-3 py-1.5 text-[12px] bg-slate-900/80 border rounded-lg transition-all cursor-text"
      :class="[
        isOpen ? 'border-indigo-500 ring-1 ring-indigo-500/30 shadow-sm' : 'border-slate-700/70 hover:border-slate-600',
        disabled ? 'opacity-50 cursor-not-allowed' : ''
      ]"
      @click="onBoxClick"
    >
      <span class="material-symbols-outlined text-[16px] text-slate-400 shrink-0">search</span>
      
      <!-- 多选模式: chips 常驻回显 + 搜索输入框并排(下拉开合均可见) -->
      <template v-if="multiple && selectedArray.length > 0">
        <div class="flex flex-wrap items-center gap-1 flex-1 min-w-0 py-0.5">
          <span
            v-for="m in selectedArray.slice(0, maxChips)"
            :key="m"
            class="inline-flex items-center gap-0.5 px-1.5 py-0.5 rounded bg-indigo-500/20 text-indigo-300 border border-indigo-500/30 text-[11px] font-mono max-w-[200px]"
            :title="m"
          >
            <span class="truncate">{{ m }}</span>
            <button
              type="button"
              class="shrink-0 hover:text-rose-400 cursor-pointer ml-0.5"
              @click.stop="removeChip(m)"
            >
              <span class="material-symbols-outlined text-[12px]">close</span>
            </button>
          </span>
          <span v-if="selectedArray.length > maxChips" class="text-[11px] text-slate-400 shrink-0">
            +{{ selectedArray.length - maxChips }}
          </span>
          <input
            ref="inputRef"
            type="text"
            :value="isOpen ? searchQuery : ''"
            :placeholder="isOpen ? (placeholder || '搜索并勾选...') : `已选 ${selectedArray.length} 个模型`"
            :disabled="disabled"
            @input="onInput"
            @focus="onFocus"
            @keydown="onKeyDown"
            class="flex-1 min-w-[120px] bg-transparent focus:outline-none text-white font-medium text-[12px] placeholder:text-slate-500"
          />
        </div>
      </template>
      <input
        v-else
        ref="inputRef"
        type="text"
        :value="inputValue"
        :placeholder="placeholder || '搜索或选择大模型...'"
        :disabled="disabled"
        @input="onInput"
        @focus="onFocus"
        @keydown="onKeyDown"
        class="w-full bg-transparent focus:outline-none text-white font-medium text-[12px] placeholder:text-slate-500 min-w-0"
      />

      <!-- 清空按钮 -->
      <button
        v-if="hasSelection || searchQuery"
        type="button"
        @click.stop="onClear"
        class="p-0.5 text-slate-400 hover:text-white transition-colors shrink-0 cursor-pointer rounded"
        title="清空"
      >
        <span class="material-symbols-outlined text-[14px]">close</span>
      </button>

      <!-- 下拉展开图标 -->
      <button
        type="button"
        @click.stop="toggleOpen"
        class="p-0.5 text-slate-400 hover:text-white transition-transform shrink-0 cursor-pointer"
        :class="isOpen ? 'rotate-180 text-indigo-400' : ''"
      >
        <span class="material-symbols-outlined text-[16px]">expand_more</span>
      </button>
    </div>

    <!-- 下拉浮层 (Teleport 到 body 避免被父级 modal/容器 overflow 裁剪) -->
    <Teleport to="body">
      <div
        v-if="isOpen"
        ref="dropdownRef"
        :style="dropdownStyle"
        class="fixed z-[100000] bg-[#0f172a] border border-slate-700/80 rounded-lg shadow-2xl overflow-hidden flex flex-col backdrop-blur-md animate-in fade-in zoom-in-95 duration-100"
      >
        <!-- 浮层顶部统计信息 -->
        <div class="flex items-center justify-between px-3 py-1.5 bg-slate-900/90 border-b border-slate-800 text-[11px] text-slate-400 font-medium shrink-0">
          <span>
            {{ searchQuery ? `找到 ${filteredOptions.length} 个匹配` : (options.length === 0 ? '暂无可用模型' : `共 ${options.length} 个模型可用`) }}
          </span>
          <!-- 多选模式: 全选当前过滤结果 / 清空 -->
          <div v-if="multiple" class="flex items-center gap-2 shrink-0">
            <button
              type="button"
              class="text-indigo-400 hover:underline cursor-pointer"
              @click.stop="selectAllFiltered"
            >全选{{ searchQuery ? '匹配项' : '' }}</button>
            <span v-if="selectedArray.length > 0" class="text-slate-600">|</span>
            <button
              v-if="selectedArray.length > 0"
              type="button"
              class="text-rose-400 hover:underline cursor-pointer"
              @click.stop="clearSelection"
            >清空({{ selectedArray.length }})</button>
          </div>
          <span v-else-if="searchQuery" class="text-indigo-400 font-mono text-[10px]">搜索中: "{{ searchQuery }}"</span>
        </div>

        <!-- 选项列表 -->
        <div class="overflow-y-auto overflow-x-hidden flex-1 py-1 divide-y divide-transparent max-h-[240px]">
          <!-- 未设置选项 (单选模式) -->
          <div
            v-if="!multiple"
            class="flex items-center justify-between px-3 py-2 text-[12px] cursor-pointer transition-colors"
            :class="[
              !modelValue ? 'bg-indigo-600/20 text-indigo-300 font-bold' : 'text-slate-400 hover:bg-slate-800',
              highlightedIndex === -1 ? 'bg-slate-800 text-white' : ''
            ]"
            @click="selectOption('')"
          >
            <div class="flex items-center gap-2">
              <span class="material-symbols-outlined text-[15px] text-slate-400">block</span>
              <span>未设置 (清空)</span>
            </div>
            <span v-if="!modelValue" class="material-symbols-outlined text-[16px] text-indigo-400">check</span>
          </div>

          <!-- 匹配的模型项列表 -->
          <div
            v-for="(item, idx) in visibleOptions"
            :key="item"
            class="flex items-center justify-between px-3 py-2 text-[12px] cursor-pointer transition-colors font-mono"
            :class="[
              isSelected(item) ? 'bg-indigo-600/20 text-indigo-300 font-bold border-l-2 border-indigo-500' : 'text-slate-200 hover:bg-slate-800/80',
              highlightedIndex === idx ? 'bg-slate-800 text-white' : ''
            ]"
            @click="onItemClick(item)"
            @mouseenter="highlightedIndex = idx"
          >
            <div class="flex items-center gap-2 truncate min-w-0 pr-2">
              <!-- 多选模式: 勾选框 -->
              <span
                v-if="multiple"
                class="material-symbols-outlined text-[16px] shrink-0"
                :class="isSelected(item) ? 'text-indigo-400' : 'text-slate-500'"
              >{{ isSelected(item) ? 'check_box' : 'check_box_outline_blank' }}</span>
              <span v-else class="material-symbols-outlined text-[15px] text-indigo-400/80 shrink-0">smart_toy</span>
              <span class="truncate" v-html="highlightMatch(item, searchQuery)"></span>
            </div>
            <span v-if="!multiple && item === modelValue" class="material-symbols-outlined text-[16px] text-indigo-400 shrink-0">check</span>
          </div>

          <!-- 截断提示: 过滤结果超出渲染上限时引导输入关键词缩小范围 -->
          <div
            v-if="hasMoreOptions"
            class="px-3 py-1.5 text-[11px] text-slate-500 italic border-t border-slate-800"
          >
            仅渲染前 {{ visibleOptions.length }} 项(共 {{ filteredOptions.length }} 个匹配)，输入关键词可快速缩小范围
          </div>

          <!-- 自定义输入选项（当搜索词不在选项列表中时） -->
          <div
            v-if="allowCustom !== false && isCustomOptionAvailable"
            class="flex items-center justify-between px-3 py-2 text-[12px] cursor-pointer transition-colors border-t border-dashed border-amber-500/30 bg-amber-500/5 hover:bg-amber-500/15 text-amber-300"
            :class="highlightedIndex === visibleOptions.length ? 'bg-amber-500/20' : ''"
            @click="selectOption(searchQuery.trim())"
            @mouseenter="highlightedIndex = visibleOptions.length"
          >
            <div class="flex items-center gap-2 truncate min-w-0">
              <span class="material-symbols-outlined text-[15px] text-amber-400 shrink-0">edit_note</span>
              <span class="truncate">使用自定义模型: <strong class="font-bold underline">{{ searchQuery.trim() }}</strong></span>
            </div>
            <span class="text-[10px] px-1.5 py-0.5 rounded bg-amber-500/20 text-amber-200 border border-amber-500/40 shrink-0">按回车选用</span>
          </div>

          <!-- 无匹配且不允许自定义 -->
          <div
            v-if="filteredOptions.length === 0 && (!isCustomOptionAvailable || allowCustom === false)"
            class="px-3 py-4 text-center text-[12px] text-slate-500 italic"
          >
            {{ (!options || options.length === 0) && !searchQuery ? '暂无可用模型（未连接网关或无模型）' : '无匹配的模型' }}
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'

const props = withDefaults(
  defineProps<{
    modelValue?: string
    options?: string[]
    placeholder?: string
    disabled?: boolean
    allowCustom?: boolean
    refreshOnOpen?: boolean
    multiple?: boolean
    modelIds?: string[]
    maxChips?: number
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
    maxChips: 8,
  }
)

const emit = defineEmits<{
  'update:modelValue': [val: string]
  'change': [val: string]
  'refresh': []
  'update:modelIds': [val: string[]]
  'changeMultiple': [val: string[]]
  'submit': [val: string]
}>()

const containerRef = ref<HTMLElement | null>(null)
const dropdownRef = ref<HTMLElement | null>(null)
const inputRef = ref<HTMLInputElement | null>(null)
const isOpen = ref(false)
const searchQuery = ref('')
const highlightedIndex = ref(-1)
const dropdownStyle = ref<Record<string, string>>({})

const selectedArray = computed<string[]>(() => {
  if (!props.multiple) return []
  return Array.isArray(props.modelIds) ? props.modelIds : []
})
const hasSelection = computed(() => (props.multiple ? selectedArray.value.length > 0 : !!props.modelValue))

function isSelected(item: string): boolean {
  if (props.multiple) return selectedArray.value.includes(item)
  return item === props.modelValue
}

function toggleItem(item: string) {
  const cur = [...selectedArray.value]
  const idx = cur.indexOf(item)
  if (idx >= 0) {
    cur.splice(idx, 1)
  } else {
    cur.push(item)
  }
  emit('update:modelIds', cur)
  emit('changeMultiple', cur)
}

function selectAllFiltered() {
  const cur = [...selectedArray.value]
  for (const item of filteredOptions.value) {
    if (!cur.includes(item)) cur.push(item)
  }
  emit('update:modelIds', cur)
  emit('changeMultiple', cur)
}

function clearSelection() {
  emit('update:modelIds', [])
  emit('changeMultiple', [])
}

function removeChip(item: string) {
  toggleItem(item)
}

function onItemClick(item: string) {
  if (props.multiple) {
    toggleItem(item)
    return
  }
  selectOption(item)
}

const inputValue = computed(() => {
  if (isOpen.value) {
    return searchQuery.value
  }
  return props.modelValue || ''
})

const filteredOptions = computed(() => {
  const q = searchQuery.value.trim().toLowerCase()
  const list = props.options || []
  if (!q) {
    return list
  }
  return list.filter(item => item.toLowerCase().includes(q))
})

const MAX_RENDER_OPTIONS = 100
const visibleOptions = computed(() => filteredOptions.value.slice(0, MAX_RENDER_OPTIONS))
const hasMoreOptions = computed(() => filteredOptions.value.length > visibleOptions.value.length)

const isCustomOptionAvailable = computed(() => {
  const q = searchQuery.value.trim()
  if (!q) return false
  const list = props.options || []
  return !list.includes(q)
})

function highlightMatch(text: string, query: string): string {
  const q = query.trim()
  if (!q) return escapeHtml(text)
  const regex = new RegExp(`(${escapeRegex(q)})`, 'gi')
  return escapeHtml(text).replace(regex, '<span class="text-indigo-400 font-bold underline bg-indigo-500/20 px-0.5 rounded">$1</span>')
}

function escapeHtml(str: string): string {
  return str
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#039;')
}

function escapeRegex(str: string): string {
  return str.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

function updatePosition() {
  if (!containerRef.value || !isOpen.value) return
  const rect = containerRef.value.getBoundingClientRect()

  if (rect.width === 0 && rect.height === 0) {
    closeDropdown()
    return
  }

  const dropdownMaxHeight = 280
  const spaceBelow = window.innerHeight - rect.bottom
  const spaceAbove = rect.top
  const gap = 4

  const placeAbove = spaceBelow < 220 && spaceAbove > spaceBelow

  const style: Record<string, string> = {
    left: `${Math.max(8, rect.left)}px`,
    width: `${rect.width}px`,
  }

  if (placeAbove) {
    const availableHeight = Math.min(dropdownMaxHeight, Math.max(100, spaceAbove - gap - 16))
    style.bottom = `${window.innerHeight - rect.top + gap}px`
    style.maxHeight = `${availableHeight}px`
  } else {
    const availableHeight = Math.min(dropdownMaxHeight, Math.max(100, spaceBelow - gap - 16))
    style.top = `${rect.bottom + gap}px`
    style.maxHeight = `${availableHeight}px`
  }

  dropdownStyle.value = style
}

function onBoxClick() {
  if (props.disabled) return
  if (!isOpen.value) {
    openDropdown()
  }
}

function toggleOpen() {
  if (props.disabled) return
  if (isOpen.value) {
    closeDropdown()
  } else {
    openDropdown()
  }
}

function openDropdown() {
  isOpen.value = true
  searchQuery.value = ''
  highlightedIndex.value = -1
  updatePosition()
  if (props.refreshOnOpen) {
    emit('refresh')
  }
  nextTick(() => {
    updatePosition()
    inputRef.value?.focus()
    inputRef.value?.select()
  })
}

function closeDropdown() {
  isOpen.value = false
  searchQuery.value = ''
  highlightedIndex.value = -1
}

function onFocus() {
  if (props.disabled) return
  if (!isOpen.value) {
    openDropdown()
  }
}

function onInput(e: Event) {
  const val = (e.target as HTMLInputElement).value
  searchQuery.value = val
  if (!isOpen.value) {
    isOpen.value = true
  }
  highlightedIndex.value = 0
  nextTick(() => {
    updatePosition()
  })
}

function onClear() {
  if (props.multiple) {
    clearSelection()
    return
  }
  selectOption('')
}

function selectOption(val: string) {
  if (props.multiple) {
    toggleItem(val)
    searchQuery.value = ''
    return
  }
  emit('update:modelValue', val)
  emit('change', val)
  closeDropdown()
}

function onKeyDown(e: KeyboardEvent) {
  if (!isOpen.value) {
    if (e.key === 'Enter' && props.modelValue) {
      e.preventDefault()
      emit('submit', props.modelValue)
      return
    }
    if (e.key === 'ArrowDown' || e.key === 'Enter') {
      e.preventDefault()
      openDropdown()
    }
    return
  }

  const listLength = visibleOptions.value.length
  const maxIndex = isCustomOptionAvailable.value ? listLength : listLength - 1

  if (e.key === 'ArrowDown') {
    e.preventDefault()
    if (highlightedIndex.value < maxIndex) {
      highlightedIndex.value++
    } else {
      highlightedIndex.value = -1
    }
  } else if (e.key === 'ArrowUp') {
    e.preventDefault()
    if (highlightedIndex.value > -1) {
      highlightedIndex.value--
    } else {
      highlightedIndex.value = maxIndex
    }
  } else if (e.key === 'Enter') {
    e.preventDefault()
    if (props.multiple) {
      if (highlightedIndex.value >= 0 && highlightedIndex.value < listLength) {
        toggleItem(visibleOptions.value[highlightedIndex.value])
      } else if (highlightedIndex.value === listLength && isCustomOptionAvailable.value) {
        toggleItem(searchQuery.value.trim())
        searchQuery.value = ''
      } else if (searchQuery.value.trim() && props.allowCustom !== false) {
        toggleItem(searchQuery.value.trim())
        searchQuery.value = ''
      }
      return
    }
    if (highlightedIndex.value === -1) {
      selectOption('')
    } else if (highlightedIndex.value >= 0 && highlightedIndex.value < listLength) {
      selectOption(visibleOptions.value[highlightedIndex.value])
    } else if (highlightedIndex.value === listLength && isCustomOptionAvailable.value) {
      selectOption(searchQuery.value.trim())
    } else if (searchQuery.value.trim() && props.allowCustom !== false) {
      selectOption(searchQuery.value.trim())
    }
  } else if (e.key === 'Escape') {
    e.preventDefault()
    closeDropdown()
  }
}

function handleClickOutside(event: MouseEvent) {
  const target = event.target as Node
  const inContainer = containerRef.value?.contains(target)
  const inDropdown = dropdownRef.value?.contains(target)
  if (!inContainer && !inDropdown) {
    closeDropdown()
  }
}

function handleScrollOrResize() {
  if (isOpen.value) {
    updatePosition()
  }
}

watch(() => props.options, () => {
  if (isOpen.value) {
    nextTick(updatePosition)
  }
})

onMounted(() => {
  document.addEventListener('click', handleClickOutside)
  window.addEventListener('resize', handleScrollOrResize)
  window.addEventListener('scroll', handleScrollOrResize, true)
})

onUnmounted(() => {
  document.removeEventListener('click', handleClickOutside)
  window.removeEventListener('resize', handleScrollOrResize)
  window.removeEventListener('scroll', handleScrollOrResize, true)
})
</script>
