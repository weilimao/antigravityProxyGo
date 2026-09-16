<template>
  <div class="glass-card p-6">
    <!-- 标题与终端系统切换选项卡 -->
    <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3 mb-4 pb-3 border-b border-slate-800/80">
      <div class="flex items-center gap-3">
        <h3 class="text-sm font-bold text-white flex items-center gap-2">
          <span class="material-symbols-outlined text-indigo-400 text-18px">terminal</span>
          <span>快速接入与客户端配置</span>
        </h3>
        <!-- 复制状态轻量提示条 -->
        <transition name="fade">
          <div v-if="copyToast" class="flex items-center gap-1.5 px-3 py-1 rounded-full bg-emerald-500/20 text-emerald-300 border border-emerald-500/30 text-11px font-medium shadow-sm">
            <span class="material-symbols-outlined text-14px">check_circle</span>
            <span>{{ copyToast }}</span>
          </div>
        </transition>
      </div>

      <!-- 终端环境选择切换器 (Linux/macOS / Windows PowerShell / Windows CMD) -->
      <div class="flex items-center bg-slate-950/80 p-1 rounded-lg border border-slate-800 self-start sm:self-auto">
        <button
          v-for="item in shellOptions"
          :key="item.id"
          type="button"
          class="px-2.5 py-1 rounded-md text-11px font-medium transition-all flex items-center gap-1.5 cursor-pointer select-none"
          :class="[
            activeShell === item.id
              ? 'bg-indigo-600 text-white shadow-sm'
              : 'text-slate-400 hover:text-slate-200 hover:bg-slate-800/60'
          ]"
          @click="selectShell(item.id)"
          :title="item.title"
        >
          <span class="material-symbols-outlined text-13px">{{ item.icon }}</span>
          <span>{{ item.label }}</span>
          <span v-if="item.badge" class="text-9px px-1 py-0.2 rounded bg-indigo-500/30 text-indigo-200 ml-0.5">
            {{ item.badge }}
          </span>
        </button>
      </div>
    </div>

    <!-- 左右协议卡片区域 (OpenAI 协议 与 Anthropic 协议) -->
    <div class="grid grid-cols-1 lg:grid-cols-2 gap-5 text-xs">
      <!-- 1. OpenAI 兼容协议接入卡片 -->
      <div class="p-4 bg-slate-900/60 rounded-xl border border-slate-800 hover:border-slate-700/80 transition-all flex flex-col justify-between gap-4">
        <div>
          <!-- 头部协议标识 -->
          <div class="flex items-center justify-between mb-3 pb-2.5 border-b border-slate-800/60">
            <div class="flex items-center gap-2">
              <span class="w-7 h-7 rounded-lg bg-teal-500/15 text-teal-400 border border-teal-500/25 flex items-center justify-center">
                <span class="material-symbols-outlined text-16px">smart_toy</span>
              </span>
              <div>
                <h4 class="text-xs font-bold text-white flex items-center gap-1.5">
                  <span>OpenAI 兼容协议</span>
                  <span class="text-10px px-1.5 py-0.5 rounded bg-teal-500/20 text-teal-300 font-mono">OpenAI Compatible</span>
                </h4>
                <p class="text-10px text-slate-400 mt-0.5">NextChat · Cherry Studio · Cursor · Python SDK · 各种兼容客户端</p>
              </div>
            </div>
          </div>

          <!-- 参数区域 -->
          <div class="flex flex-col gap-2.5 font-mono text-11px">
            <div>
              <span class="text-slate-400 text-10px block mb-1 font-sans font-semibold">API 接口地址 (Base URL)</span>
              <div class="flex items-center justify-between bg-black/40 px-2.5 py-2 rounded-lg border border-slate-800 text-indigo-300 break-all group">
                <span class="truncate mr-2">{{ openaiBaseUrl }}</span>
                <button
                  type="button"
                  class="text-slate-400 hover:text-white shrink-0 cursor-pointer p-1 rounded hover:bg-slate-800 transition-colors"
                  @click="copyText(openaiBaseUrl, 'OpenAI Base URL')"
                  title="点击复制 Base URL"
                >
                  <span class="material-symbols-outlined text-14px">content_copy</span>
                </button>
              </div>
            </div>

            <div>
              <div class="flex items-center justify-between mb-1 font-sans">
                <span class="text-slate-400 text-10px font-semibold">调用密钥 (API Key)</span>
                <span v-if="!hasKeys" class="text-10px text-amber-400">尚未建 Key，点击右上角新建</span>
              </div>
              <div class="flex items-center justify-between bg-black/40 px-2.5 py-2 rounded-lg border border-slate-800 text-slate-300 break-all">
                <span class="truncate mr-2 font-mono text-11px">{{ apiKey }}</span>
                <button
                  type="button"
                  :disabled="!hasKeys"
                  class="text-slate-400 hover:text-white shrink-0 cursor-pointer p-1 rounded hover:bg-slate-800 transition-colors disabled:opacity-40"
                  @click="copyText(apiKey, 'API Key')"
                  title="点击复制 API Key"
                >
                  <span class="material-symbols-outlined text-14px">content_copy</span>
                </button>
              </div>
            </div>

            <div>
              <div class="flex items-center justify-between mb-1 font-sans">
                <span class="text-slate-400 text-10px font-semibold">
                  终端环境变量一键配置 ({{ currentShellName }})
                </span>
                <span class="text-9px text-indigo-400/80 font-mono">{{ shellLabelTip }}</span>
              </div>
              <pre class="bg-slate-950/80 p-2.5 rounded-lg border border-slate-800/80 text-11px text-slate-300 leading-relaxed overflow-x-auto whitespace-pre-wrap select-all font-mono">{{ openaiCommand }}</pre>
            </div>
          </div>
        </div>

        <div class="pt-2 border-t border-slate-800/60 flex items-center justify-between">
          <span class="text-10px text-slate-500 font-sans">模型填写建议：<code class="text-indigo-400">auto</code> (并发竞速) 或套餐授权模型</span>
          <button
            type="button"
            class="btn-secondary text-10px py-1 px-2.5 flex items-center gap-1 shrink-0 cursor-pointer"
            @click="copyOpenAIEnv"
          >
            <span class="material-symbols-outlined text-12px">terminal</span>
            <span>复制 {{ currentShellShortName }} 环境变量</span>
          </button>
        </div>
      </div>

      <!-- 2. Anthropic 兼容协议接入卡片 -->
      <div class="p-4 bg-slate-900/60 rounded-xl border border-slate-800 hover:border-slate-700/80 transition-all flex flex-col justify-between gap-4">
        <div>
          <!-- 头部协议标识 -->
          <div class="flex items-center justify-between mb-3 pb-2.5 border-b border-slate-800/60">
            <div class="flex items-center gap-2">
              <span class="w-7 h-7 rounded-lg bg-amber-500/15 text-amber-400 border border-amber-500/25 flex items-center justify-center">
                <span class="material-symbols-outlined text-16px">psychology</span>
              </span>
              <div>
                <h4 class="text-xs font-bold text-white flex items-center gap-1.5">
                  <span>Anthropic 兼容协议</span>
                  <span class="text-10px px-1.5 py-0.5 rounded bg-amber-500/20 text-amber-300 font-mono">Anthropic Compatible</span>
                </h4>
                <p class="text-10px text-slate-400 mt-0.5">Claude Code · Claude Desktop · Cline · Roo Code · 官方 SDK</p>
              </div>
            </div>
          </div>

          <!-- 参数区域 -->
          <div class="flex flex-col gap-2.5 font-mono text-11px">
            <div>
              <span class="text-slate-400 text-10px block mb-1 font-sans font-semibold">API 接口地址 (Base URL)</span>
              <div class="flex items-center justify-between bg-black/40 px-2.5 py-2 rounded-lg border border-slate-800 text-amber-300 break-all group">
                <span class="truncate mr-2">{{ anthropicBaseUrl }}</span>
                <button
                  type="button"
                  class="text-slate-400 hover:text-white shrink-0 cursor-pointer p-1 rounded hover:bg-slate-800 transition-colors"
                  @click="copyText(anthropicBaseUrl, 'Anthropic Base URL')"
                  title="点击复制 Base URL"
                >
                  <span class="material-symbols-outlined text-14px">content_copy</span>
                </button>
              </div>
            </div>

            <div>
              <div class="flex items-center justify-between mb-1 font-sans">
                <span class="text-slate-400 text-10px font-semibold">调用密钥 (API Key)</span>
                <span v-if="!hasKeys" class="text-10px text-amber-400">尚未建 Key，点击右上角新建</span>
              </div>
              <div class="flex items-center justify-between bg-black/40 px-2.5 py-2 rounded-lg border border-slate-800 text-slate-300 break-all">
                <span class="truncate mr-2 font-mono text-11px">{{ apiKey }}</span>
                <button
                  type="button"
                  :disabled="!hasKeys"
                  class="text-slate-400 hover:text-white shrink-0 cursor-pointer p-1 rounded hover:bg-slate-800 transition-colors disabled:opacity-40"
                  @click="copyText(apiKey, 'API Key')"
                  title="点击复制 API Key"
                >
                  <span class="material-symbols-outlined text-14px">content_copy</span>
                </button>
              </div>
            </div>

            <div>
              <div class="flex items-center justify-between mb-1 font-sans">
                <span class="text-slate-400 text-10px font-semibold">
                  Claude Code 终端直连 ({{ currentShellName }})
                </span>
                <span class="text-9px text-amber-400/80 font-mono">{{ shellLabelTip }}</span>
              </div>
              <pre class="bg-slate-950/80 p-2.5 rounded-lg border border-slate-800/80 text-11px text-slate-300 leading-relaxed overflow-x-auto whitespace-pre-wrap select-all font-mono">{{ anthropicCommand }}</pre>
            </div>
          </div>
        </div>

        <div class="pt-2 border-t border-slate-800/60 flex items-center justify-between">
          <span class="text-10px text-slate-500 font-sans">
            终端执行上述命令后，直接输入 <code class="text-amber-400">claude</code> 即可启动
          </span>
          <button
            type="button"
            class="btn-secondary text-10px py-1 px-2.5 flex items-center gap-1 shrink-0 cursor-pointer"
            @click="copyAnthropicEnv"
          >
            <span class="material-symbols-outlined text-12px">terminal</span>
            <span>复制 {{ currentShellShortName }} 命令</span>
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'

type ShellType = 'bash' | 'powershell' | 'cmd'

interface Props {
  openaiBaseUrl: string
  anthropicBaseUrl: string
  apiKey: string
  hasKeys: boolean
}

const props = defineProps<Props>()

const copyToast = ref('')
let toastTimer: any = null

const shellOptions = [
  {
    id: 'bash' as ShellType,
    label: 'Linux / macOS',
    icon: 'terminal',
    badge: '',
    title: '适用 Bash, Zsh, Git Bash, WSL 等类 Unix 终端',
  },
  {
    id: 'powershell' as ShellType,
    label: 'PowerShell',
    icon: 'code',
    badge: 'Win推荐',
    title: '适用 Windows PowerShell 与 PowerShell 7',
  },
  {
    id: 'cmd' as ShellType,
    label: 'Windows CMD',
    icon: 'reorder',
    badge: '',
    title: '适用 Windows 经典命令提示符',
  },
]

// 默认根据当前操作系统类型自动匹配偏好，并支持持久化记忆
const activeShell = ref<ShellType>('bash')

onMounted(() => {
  const saved = localStorage.getItem('max_api_preferred_shell') as ShellType
  if (saved && ['bash', 'powershell', 'cmd'].includes(saved)) {
    activeShell.value = saved
  } else {
    // 自动检测是否为 Windows 环境
    const isWindows = typeof navigator !== 'undefined' && navigator.userAgent.toLowerCase().includes('windows')
    activeShell.value = isWindows ? 'powershell' : 'bash'
  }
})

function selectShell(type: ShellType) {
  activeShell.value = type
  try {
    localStorage.setItem('max_api_preferred_shell', type)
  } catch (e) {
    // ignore
  }
}

const currentShellName = computed(() => {
  switch (activeShell.value) {
    case 'powershell':
      return 'Windows PowerShell'
    case 'cmd':
      return 'Windows 命令提示符 CMD'
    case 'bash':
    default:
      return 'Linux / macOS Shell'
  }
})

const currentShellShortName = computed(() => {
  switch (activeShell.value) {
    case 'powershell':
      return 'PowerShell'
    case 'cmd':
      return 'CMD'
    case 'bash':
    default:
      return 'Shell'
  }
})

const shellLabelTip = computed(() => {
  switch (activeShell.value) {
    case 'powershell':
      return '$env:VAR="VAL"'
    case 'cmd':
      return 'set VAR=VAL'
    case 'bash':
    default:
      return 'export VAR="VAL"'
  }
})

const openaiCommand = computed(() => {
  const base = props.openaiBaseUrl
  const key = props.apiKey
  if (activeShell.value === 'powershell') {
    return `$env:OPENAI_BASE_URL="${base}"\n$env:OPENAI_API_KEY="${key}"`
  }
  if (activeShell.value === 'cmd') {
    return `set OPENAI_BASE_URL=${base}\nset OPENAI_API_KEY=${key}`
  }
  return `export OPENAI_BASE_URL="${base}"\nexport OPENAI_API_KEY="${key}"`
})

const anthropicCommand = computed(() => {
  const base = props.anthropicBaseUrl
  const key = props.apiKey
  if (activeShell.value === 'powershell') {
    return `$env:ANTHROPIC_BASE_URL="${base}"\n$env:ANTHROPIC_API_KEY="${key}"`
  }
  if (activeShell.value === 'cmd') {
    return `set ANTHROPIC_BASE_URL=${base}\nset ANTHROPIC_API_KEY=${key}`
  }
  return `export ANTHROPIC_BASE_URL="${base}"\nexport ANTHROPIC_API_KEY="${key}"`
})

function copyText(val: string, label: string) {
  if (!val) return
  navigator.clipboard.writeText(val)
  copyToast.value = `${label} 已复制`
  if (toastTimer) clearTimeout(toastTimer)
  toastTimer = setTimeout(() => {
    copyToast.value = ''
  }, 2500)
}

function copyOpenAIEnv() {
  copyText(openaiCommand.value, `OpenAI ${currentShellShortName.value} 环境变量`)
}

function copyAnthropicEnv() {
  copyText(anthropicCommand.value, `Claude Code ${currentShellShortName.value} 命令`)
}
</script>

<style scoped>
.fade-enter-active,
.fade-leave-active {
  transition: opacity 0.2s ease;
}
.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}
</style>
