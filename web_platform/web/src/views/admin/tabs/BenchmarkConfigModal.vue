<template>
  <Teleport to="body">
    <div class="fixed inset-0 z-[100] flex items-center justify-center p-4">
    <!-- Backdrop -->
    <div class="absolute inset-0 bg-black/60 backdrop-blur-sm transition-opacity" @click="$emit('close')"></div>
    
    <!-- Modal Panel -->
    <div class="relative bg-slate-900 border border-slate-700 rounded-2xl shadow-2xl w-full max-w-lg overflow-hidden flex flex-col max-h-[90vh]">
      <!-- Header -->
      <div class="px-6 py-4 border-b border-slate-800 flex justify-between items-center bg-slate-800/30">
        <h3 class="text-lg font-bold text-white flex items-center gap-2">
          <span class="material-symbols-outlined text-indigo-400">settings</span>
          网关模型测速配置
        </h3>
        <button class="text-slate-400 hover:text-white transition-colors" @click="$emit('close')">
          <span class="material-symbols-outlined">close</span>
        </button>
      </div>

      <!-- Body -->
      <div class="p-6 overflow-y-auto flex-1 custom-scrollbar">
        <div class="flex flex-col gap-5">
          
          <!-- Enable Toggle -->
          <div class="flex items-center justify-between p-4 rounded-xl bg-slate-800/50 border border-slate-700/50">
            <div>
              <div class="text-sm font-semibold text-slate-200">启用定时自动测速</div>
              <div class="text-xs text-slate-500 mt-1">关闭后仍可手动触发测速</div>
            </div>
            <input type="checkbox" v-model="form.enabled" class="w-5 h-5 text-indigo-600 rounded bg-slate-900 border-slate-700 cursor-pointer" />
          </div>

          <!-- Models Multi-Select -->
          <div class="flex flex-col gap-2 relative" ref="selectContainerRef">
            <div class="flex items-center justify-between">
              <label class="text-sm font-semibold text-slate-300">
                测试模型列表 <span class="text-red-400">*</span>
              </label>
              <div class="flex items-center gap-3 text-xs">
                <span class="text-slate-400">已选 <strong class="text-indigo-400 font-bold">{{ form.models.length }}</strong> 个模型</span>
                <button
                  v-if="form.models.length > 0"
                  type="button"
                  class="text-rose-400 hover:text-rose-300 transition-colors font-medium cursor-pointer"
                  @click.stop="form.models = []"
                >
                  清空已选
                </button>
              </div>
            </div>

            <!-- Multi-Select Box -->
            <div
              class="w-full bg-slate-950 border rounded-xl p-2 min-h-[46px] flex flex-wrap items-center gap-1.5 cursor-text transition-all"
              :class="dropdownOpen ? 'border-indigo-500 ring-1 ring-indigo-500/30' : 'border-slate-700 hover:border-slate-600'"
              @click="focusInputAndOpen"
            >
              <!-- Selected Tags -->
              <span
                v-for="m in form.models"
                :key="m"
                class="inline-flex items-center gap-1 px-2.5 py-1 rounded-lg text-xs bg-indigo-500/20 text-indigo-300 border border-indigo-500/30 max-w-[260px] truncate"
              >
                <span class="truncate" :title="m">{{ m }}</span>
                <button
                  type="button"
                  class="text-indigo-400 hover:text-white p-0.5 rounded transition-colors flex-shrink-0"
                  @click.stop="removeModel(m)"
                >
                  <span class="material-symbols-outlined text-[13px]">close</span>
                </button>
              </span>

              <!-- Embedded Input -->
              <input
                ref="searchInputRef"
                v-model="searchQuery"
                type="text"
                class="bg-transparent text-xs text-white focus:outline-none flex-1 min-w-[150px] py-1 placeholder-slate-500"
                :placeholder="form.models.length === 0 ? '点击选择或输入模型名称回车添加...' : '继续搜索或回车添加...'"
                @focus="dropdownOpen = true"
                @keydown.enter.prevent="addCustomModel"
                @keydown.backspace="onBackspace"
              />

              <!-- Arrow -->
              <button
                type="button"
                class="text-slate-400 hover:text-white p-1 ml-auto flex items-center transition-transform duration-200"
                :class="dropdownOpen ? 'rotate-180' : ''"
                @click.stop="dropdownOpen = !dropdownOpen"
              >
                <span class="material-symbols-outlined text-18px">expand_more</span>
              </button>
            </div>

            <!-- Dropdown Menu -->
            <div
              v-if="dropdownOpen"
              class="absolute left-0 right-0 top-full mt-1.5 bg-slate-900 border border-slate-700/80 rounded-xl shadow-2xl z-50 overflow-hidden flex flex-col text-xs max-h-60 animate-fade-in"
            >
              <!-- Dropdown Header -->
              <div class="px-3 py-2 border-b border-slate-800 bg-slate-800/50 flex items-center justify-between text-slate-400">
                <span>可选模型 (共 {{ filteredModels.length }} 个)</span>
                <div class="flex items-center gap-2">
                  <button
                    v-if="filteredModels.length > 0"
                    type="button"
                    class="text-indigo-400 hover:text-indigo-300 font-semibold cursor-pointer"
                    @click.stop="selectAllFiltered"
                  >
                    全选当前
                  </button>
                </div>
              </div>

              <!-- Options List -->
              <div class="overflow-y-auto flex-1 p-1.5 space-y-0.5 custom-scrollbar max-h-48">
                <!-- Add Custom Model Prompt -->
                <div
                  v-if="trimmedSearch && !availableModels.includes(trimmedSearch) && !form.models.includes(trimmedSearch)"
                  class="px-2.5 py-2 rounded-lg text-emerald-300 hover:bg-emerald-500/10 cursor-pointer flex items-center gap-2 border border-emerald-500/20 mb-1"
                  @click.stop="addCustomModel"
                >
                  <span class="material-symbols-outlined text-16px text-emerald-400">add_circle</span>
                  <span>添加自定义测试模型: <strong>{{ trimmedSearch }}</strong> (按回车)</span>
                </div>

                <div
                  v-if="filteredModels.length === 0 && (!trimmedSearch || availableModels.includes(trimmedSearch) || form.models.includes(trimmedSearch))"
                  class="py-6 text-center text-slate-500"
                >
                  无匹配可选模型
                </div>

                <!-- Model Rows -->
                <div
                  v-for="m in filteredModels"
                  :key="m"
                  class="px-2.5 py-1.5 rounded-lg flex items-center justify-between cursor-pointer transition-colors"
                  :class="form.models.includes(m) ? 'bg-indigo-500/15 text-white font-medium' : 'text-slate-300 hover:bg-slate-800'"
                  @click.stop="toggleModel(m)"
                >
                  <div class="flex items-center gap-2 overflow-hidden">
                    <input
                      type="checkbox"
                      :checked="form.models.includes(m)"
                      class="rounded bg-slate-800 border-slate-700 text-indigo-600 focus:ring-0 cursor-pointer"
                      @click.stop
                      @change="toggleModel(m)"
                    />
                    <span class="truncate" :title="m">{{ m }}</span>
                  </div>
                  <span v-if="form.models.includes(m)" class="material-symbols-outlined text-16px text-indigo-400 flex-shrink-0">
                    check
                  </span>
                </div>
              </div>
            </div>
          </div>

          <div class="grid grid-cols-2 gap-4">
            <!-- Interval -->
            <div class="flex flex-col gap-2">
              <label class="text-sm font-semibold text-slate-300">测速间隔 (分钟)</label>
              <input type="number" v-model.number="form.intervalMinutes" min="1" max="1440" class="w-full bg-slate-950 border border-slate-700 rounded-lg p-2 text-sm text-slate-300 focus:outline-none focus:border-indigo-500" />
            </div>
            
            <!-- Timeout -->
            <div class="flex flex-col gap-2">
              <label class="text-sm font-semibold text-slate-300">超时时间 (毫秒)</label>
              <input type="number" v-model.number="form.timeoutMs" min="1000" max="120000" step="1000" class="w-full bg-slate-950 border border-slate-700 rounded-lg p-2 text-sm text-slate-300 focus:outline-none focus:border-indigo-500" />
            </div>
          </div>

          <!-- Prompt -->
          <div class="flex flex-col gap-2">
            <label class="text-sm font-semibold text-slate-300">测试 Prompt</label>
            <div class="text-xs text-slate-500 mb-1">发送给模型的最小探测文本，建议尽可能简短</div>
            <input type="text" v-model="form.prompt" class="w-full bg-slate-950 border border-slate-700 rounded-lg p-2 text-sm text-slate-300 focus:outline-none focus:border-indigo-500" placeholder="Hi" />
          </div>

        </div>
      </div>

      <!-- Footer -->
      <div class="px-6 py-4 border-t border-slate-800 bg-slate-900 flex justify-end gap-3">
        <button class="btn-secondary" :disabled="saving" @click="$emit('close')">取消</button>
        <button class="btn-primary flex items-center gap-1.5 disabled:opacity-60 shadow-md shadow-indigo-600/30" @click="handleSave" :disabled="saving">
          <span class="material-symbols-outlined text-[16px]" :class="saving ? 'animate-spin' : ''">
            {{ saving ? 'progress_activity' : 'save' }}
          </span>
          <span>{{ saving ? '保存并应用中...' : '保存并应用' }}</span>
        </button>
      </div>
    </div>
  </div>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted, computed } from 'vue'
import { benchmarkApi } from '@/api/client'

const props = defineProps<{
  initialConfig: any
}>()

const emit = defineEmits(['close', 'saved'])

const saving = ref(false)
const availableModels = ref<string[]>([])
const selectContainerRef = ref<HTMLElement | null>(null)
const searchInputRef = ref<HTMLInputElement | null>(null)
const searchQuery = ref('')
const dropdownOpen = ref(false)

const form = ref({
  enabled: true,
  models: [] as string[],
  intervalMinutes: 5,
  timeoutMs: 30000,
  prompt: 'Hi'
})

if (props.initialConfig) {
  form.value = {
    enabled: props.initialConfig.enabled ?? true,
    models: [...(props.initialConfig.models || [])],
    intervalMinutes: props.initialConfig.intervalMinutes || 5,
    timeoutMs: props.initialConfig.timeoutMs || 30000,
    prompt: props.initialConfig.prompt || 'Hi'
  }
}

const trimmedSearch = computed(() => searchQuery.value.trim())

const filteredModels = computed(() => {
  const q = trimmedSearch.value.toLowerCase()
  if (!q) return availableModels.value
  return availableModels.value.filter((m) => m.toLowerCase().includes(q))
})

function focusInputAndOpen() {
  dropdownOpen.value = true
  searchInputRef.value?.focus()
}

function toggleModel(m: string) {
  if (form.value.models.includes(m)) {
    form.value.models = form.value.models.filter((x) => x !== m)
  } else {
    form.value.models.push(m)
  }
}

function removeModel(m: string) {
  form.value.models = form.value.models.filter((x) => x !== m)
}

function addCustomModel() {
  const val = trimmedSearch.value
  if (!val) return
  if (!form.value.models.includes(val)) {
    form.value.models.push(val)
  }
  searchQuery.value = ''
}

function selectAllFiltered() {
  for (const m of filteredModels.value) {
    if (!form.value.models.includes(m)) {
      form.value.models.push(m)
    }
  }
}

function onBackspace() {
  if (!searchQuery.value && form.value.models.length > 0) {
    form.value.models.pop()
  }
}

function handleClickOutside(e: MouseEvent) {
  if (selectContainerRef.value && !selectContainerRef.value.contains(e.target as Node)) {
    dropdownOpen.value = false
  }
}

onMounted(async () => {
  document.addEventListener('click', handleClickOutside)
  try {
    const res = await benchmarkApi.getModels()
    if (res.success && res.models) {
      availableModels.value = res.models
    }
  } catch (e) {
    console.error('Failed to load available models', e)
  }
})

onUnmounted(() => {
  document.removeEventListener('click', handleClickOutside)
})

const handleSave = async () => {
  if (!form.value.models || form.value.models.length === 0) {
    alert('请至少选择或输入一个测试模型')
    return
  }
  saving.value = true
  try {
    const res = await benchmarkApi.saveConfig({
      enabled: form.value.enabled,
      models: form.value.models,
      intervalMinutes: form.value.intervalMinutes,
      timeoutMs: form.value.timeoutMs,
      prompt: form.value.prompt
    })
    if (res.success) {
      alert('竞速测试配置已保存并生效')
      emit('saved')
    } else {
      alert(res.error || '保存失败')
    }
  } catch (error: any) {
    alert(error.message || '保存请求失败')
  } finally {
    saving.value = false
  }
}
</script>
