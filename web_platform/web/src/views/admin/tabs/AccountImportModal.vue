<template>
  <Teleport to="body">
    <div
      v-if="show"
      class="fixed inset-0 z-[999] flex items-center justify-center p-4 bg-black/75 backdrop-blur-sm animate-fade-in"
      @click.self="$emit('close')"
    >
      <div class="bg-slate-900 border border-slate-700/80 rounded-2xl w-full max-w-lg shadow-2xl overflow-hidden flex flex-col max-h-[90vh]">
        <!-- 弹窗 Header -->
        <div class="px-6 py-4 border-b border-slate-800 flex items-center justify-between bg-slate-900/60">
          <div class="flex items-center gap-2.5">
            <span class="material-symbols-outlined text-indigo-400">upload_file</span>
            <h3 class="text-base font-bold text-white">批量导入账号配置</h3>
          </div>
          <button
            type="button"
            class="text-slate-400 hover:text-white p-1 rounded-lg hover:bg-slate-800 transition-colors"
            @click="$emit('close')"
          >
            <span class="material-symbols-outlined text-18px">close</span>
          </button>
        </div>

        <!-- 错误提示 -->
        <div v-if="errorMessage" class="mx-6 mt-4 p-3 rounded-lg bg-rose-500/10 border border-rose-500/30 text-rose-300 text-xs flex items-center gap-2">
          <span class="material-symbols-outlined text-16px text-rose-400">error</span>
          <span>{{ errorMessage }}</span>
        </div>

        <!-- 内容区域 -->
        <div class="p-6 space-y-4 overflow-y-auto flex-1 text-xs">
          <!-- 上传 JSON 文件区域 -->
          <div>
            <label class="block text-slate-400 mb-2 font-medium">方式一：选择 JSON 配置文件</label>
            <label class="flex flex-col items-center justify-center border-2 border-dashed border-slate-700 hover:border-indigo-500 rounded-xl p-5 cursor-pointer bg-slate-800/40 hover:bg-slate-800/80 transition-all">
              <span class="material-symbols-outlined text-32px text-indigo-400 mb-1">attach_file</span>
              <span class="text-slate-300 font-medium">点击选择本地 .json 文件</span>
              <span class="text-slate-500 text-[11px] mt-0.5">支持桌面端导出的 accounts_*.json 文件</span>
              <input type="file" accept=".json" class="hidden" @change="handleFileUpload" />
            </label>
          </div>

          <!-- 方式二：直接粘贴 JSON -->
          <div>
            <div class="flex items-center justify-between mb-1.5">
              <label class="text-slate-400 font-medium">方式二：直接粘贴 JSON 文本</label>
              <span v-if="parsedCount > 0" class="text-emerald-400 font-bold">已识别 {{ parsedCount }} 个账号</span>
            </div>
            <textarea
              v-model="jsonText"
              rows="6"
              placeholder='[ { "email": "user@domain.com", "access_token": "nvapi-...", "provider": "nvidia" } ] 或 { "accounts": [...] }'
              class="w-full px-3 py-2 bg-slate-800 border border-slate-700 rounded-lg text-white font-mono text-[11px] focus:outline-none focus:border-indigo-500"
              @input="onJsonInput"
            ></textarea>
          </div>

          <!-- 默认归属通道 -->
          <div>
            <label class="block text-slate-400 mb-1.5 font-medium">未声明 Provider 时默认归属</label>
            <select
              v-model="defaultProvider"
              class="w-full px-3 py-2 bg-slate-800 border border-slate-700 rounded-lg text-white focus:outline-none focus:border-indigo-500"
            >
              <option value="nvidia">NVIDIA 号池 (nvidia)</option>
              <option value="other">Other 自定义号池 (other)</option>
              <option value="grok">Grok 号池 (grok)</option>
              <option value="workbuddy">WorkBuddy 号池 (workbuddy)</option>
              <option value="antigravity">Antigravity 官方账号</option>
              <option value="project">谷歌云项目 API</option>
            </select>
          </div>
        </div>

        <!-- 弹窗 Footer -->
        <div class="px-6 py-4 border-t border-slate-800 flex items-center justify-end gap-3 bg-slate-900/60">
          <button
            type="button"
            class="px-4 py-2 rounded-lg text-xs font-semibold text-slate-300 hover:text-white hover:bg-slate-800 transition-colors"
            @click="$emit('close')"
          >
            取消
          </button>
          <button
            type="button"
            :disabled="importing || parsedCount === 0"
            class="px-5 py-2 rounded-lg text-xs font-semibold bg-indigo-600 hover:bg-indigo-500 text-white transition-all shadow-md shadow-indigo-600/30 flex items-center gap-2 disabled:opacity-50"
            @click="handleImport"
          >
            <span v-if="importing" class="material-symbols-outlined text-16px animate-spin">progress_activity</span>
            <span>确认导入 ({{ parsedCount }} 个账号)</span>
          </button>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { accountApi } from '../../../api/client'

const props = defineProps<{
  show: boolean
  currentChannel: string
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'imported', count: number): void
}>()

const importing = ref(false)
const errorMessage = ref('')
const jsonText = ref('')
const parsedCount = ref(0)
const parsedAccounts = ref<any[]>([])
const defaultProvider = ref('nvidia')

watch(
  () => props.show,
  (val) => {
    if (val) {
      errorMessage.value = ''
      jsonText.value = ''
      parsedCount.value = 0
      parsedAccounts.value = []
      defaultProvider.value = props.currentChannel || 'nvidia'
    }
  },
  { immediate: true }
)

function handleFileUpload(e: Event) {
  const target = e.target as HTMLInputElement
  if (!target.files || target.files.length === 0) return
  const file = target.files[0]
  const reader = new FileReader()
  reader.onload = (event) => {
    jsonText.value = String(event.target?.result || '')
    onJsonInput()
  }
  reader.readAsText(file)
}

function onJsonInput() {
  errorMessage.value = ''
  parsedCount.value = 0
  parsedAccounts.value = []
  const text = jsonText.value.trim()
  if (!text) return

  try {
    const data = JSON.parse(text)
    let list: any[] = []
    if (Array.isArray(data)) {
      list = data
    } else if (data && Array.isArray(data.accounts)) {
      list = data.accounts
    } else if (data && typeof data === 'object') {
      list = [data]
    }

    parsedAccounts.value = list.map((item) => ({
      ...item,
      provider: item.provider || defaultProvider.value,
    }))
    parsedCount.value = parsedAccounts.value.length
  } catch (err: any) {
    errorMessage.value = 'JSON 解析失败: ' + err.message
  }
}

async function handleImport() {
  if (parsedCount.value === 0) return
  importing.value = true
  errorMessage.value = ''
  try {
    const res = await accountApi.importAccounts(parsedAccounts.value)
    emit('imported', res?.imported || parsedCount.value)
    emit('close')
  } catch (err: any) {
    errorMessage.value = err?.message || '导入失败'
  } finally {
    importing.value = false
  }
}
</script>
