<template>
  <div class="w-full flex flex-col gap-6">
    <div class="glass-card p-6">
      <!-- 头部控制栏 -->
      <div class="flex items-center justify-between gap-4 mb-4 pb-3 border-b border-slate-800">
        <div class="flex items-center gap-3 min-w-0">
          <span class="w-10 h-10 rounded-xl bg-indigo-500/10 text-indigo-400 flex items-center justify-center border border-indigo-500/20 shrink-0">
            <span class="material-symbols-outlined text-24px">settings</span>
          </span>
          <div class="min-w-0">
            <h3 class="text-base font-bold text-white">系统全局与 API 服务配置 (System & Gateway Settings)</h3>
            <p class="text-xs text-slate-400 truncate sm:whitespace-normal">
              配置前台用户控制台展示的对外 API 服务地址，客户端（如 NextChat、Cursor、Claude Code 等）将依据此地址发起调用
            </p>
          </div>
        </div>
        <div class="flex items-center gap-2 shrink-0">
          <button
            type="button"
            :disabled="loading"
            class="btn-secondary text-xs flex items-center gap-1.5 whitespace-nowrap"
            @click="loadConfig"
            title="重新从数据库读取最新配置"
          >
            <span class="material-symbols-outlined text-16px" :class="{ 'animate-spin': loading }">refresh</span>
            <span>刷新</span>
          </button>
          <button
            type="button"
            :disabled="saving"
            class="btn-primary text-xs flex items-center gap-1.5 whitespace-nowrap"
            @click="saveConfig"
          >
            <span class="material-symbols-outlined text-16px" :class="{ 'animate-spin': saving }">save</span>
            <span>{{ saving ? '保存中...' : '保存系统配置' }}</span>
          </button>
        </div>
      </div>

      <!-- 反馈提示条 -->
      <div
        v-if="feedbackMsg"
        class="mb-4 p-3 rounded-lg flex items-center justify-between text-xs"
        :class="feedbackSuccess ? 'bg-emerald-500/10 text-emerald-300 border border-emerald-500/30' : 'bg-rose-500/10 text-rose-300 border border-rose-500/30'"
      >
        <div class="flex items-center gap-2">
          <span class="material-symbols-outlined text-16px">{{ feedbackSuccess ? 'check_circle' : 'error' }}</span>
          <span>{{ feedbackMsg }}</span>
        </div>
        <button type="button" @click="feedbackMsg = ''" class="hover:opacity-80">
          <span class="material-symbols-outlined text-14px">close</span>
        </button>
      </div>

      <!-- 配置主体 -->
      <div class="flex flex-col gap-6">
        <!-- 1. API 服务基准地址配置卡片 -->
        <div class="p-4 rounded-xl bg-slate-900/40 border border-slate-800 hover:border-slate-700 transition-all">
          <div class="flex items-center justify-between mb-3 pb-2 border-b border-slate-800/80">
            <div class="flex items-center gap-2">
              <span class="px-2 py-0.5 rounded text-10px font-mono font-bold bg-indigo-500/20 text-indigo-400 border border-indigo-500/30">核心配置</span>
              <span class="font-bold text-white text-xs flex items-center gap-1">
                <span class="material-symbols-outlined text-16px text-indigo-400">dns</span>
                <span>对外 API 服务基准地址 (API Base URL)</span>
              </span>
            </div>
            <span class="badge badge-indigo text-10px">决定前台接入指引</span>
          </div>

          <div class="flex flex-col gap-3 text-xs">
            <div>
              <label class="block text-slate-300 font-semibold mb-1">
                API Base URL (带协议与端口，末尾不含斜杠)
              </label>
              <div class="flex gap-1.5">
                <input
                  v-model="form.apiBaseUrl"
                  type="text"
                  placeholder="如: http://192.168.1.100:18444 或 https://api.yourdomain.com"
                  class="input-dark w-full text-xs font-mono"
                />
                <button
                  type="button"
                  class="btn-secondary px-2.5 text-xs shrink-0"
                  @click="copyText(form.apiBaseUrl, 'API 地址')"
                  title="复制 API 地址"
                >
                  <span class="material-symbols-outlined text-14px">content_copy</span>
                </button>
              </div>
              <div class="flex items-center gap-2 mt-2 flex-wrap">
                <span class="text-11px text-slate-500">快捷预设填充:</span>
                <button
                  type="button"
                  class="text-10px px-2 py-0.5 rounded bg-slate-800 hover:bg-slate-700 text-indigo-300 border border-slate-700 cursor-pointer"
                  @click="form.apiBaseUrl = 'http://127.0.0.1:18444'"
                >
                  💻 本地 (http://127.0.0.1:18444)
                </button>
                <button
                  type="button"
                  class="text-10px px-2 py-0.5 rounded bg-slate-800 hover:bg-slate-700 text-emerald-300 border border-slate-700 cursor-pointer"
                  @click="setHostPreset"
                >
                  🌐 当前主机 (http://{{ currentHostname }}:18444)
                </button>
                <button
                  type="button"
                  class="text-10px px-2 py-0.5 rounded bg-slate-800 hover:bg-slate-700 text-amber-300 border border-slate-700 cursor-pointer"
                  @click="setHttpsPreset"
                >
                  🔒 生产 HTTPS (https://{{ currentHostname }})
                </button>
              </div>
              <p class="text-11px text-slate-500 mt-1.5 leading-relaxed">
                说明：前台“用户控制台”的快速接入指南卡片中，OpenAI 格式将自动拼接为 <code class="text-slate-300 font-mono">{{ displayOpenAIUrl }}</code>，Anthropic 格式将展示为 <code class="text-slate-300 font-mono">{{ displayAnthropicUrl }}</code>。
              </p>
            </div>

            <!-- 实时客户端调用预览 -->
            <div class="p-3 rounded-lg bg-slate-950/60 border border-slate-800/80 mt-1">
              <span class="text-11px font-bold text-slate-300 block mb-2 flex items-center gap-1.5">
                <span class="material-symbols-outlined text-14px text-indigo-400">visibility</span>
                <span>前台客户端接入端点实时预览</span>
              </span>
              <div class="grid grid-cols-1 md:grid-cols-2 gap-3 text-11px font-mono">
                <div class="bg-slate-900/80 p-2.5 rounded border border-slate-800">
                  <span class="text-slate-400 block text-10px mb-1">OpenAI 格式接入 (NextChat / Cherry Studio / Cursor)</span>
                  <div class="text-indigo-300 break-all">{{ displayOpenAIUrl }}</div>
                </div>
                <div class="bg-slate-900/80 p-2.5 rounded border border-slate-800">
                  <span class="text-slate-400 block text-10px mb-1">Anthropic 格式接入 (Claude Code / Cline)</span>
                  <div class="text-emerald-300 break-all">{{ displayAnthropicUrl }}</div>
                </div>
              </div>
            </div>
          </div>
        </div>

        <!-- 2. 站点附加信息配置卡片 -->
        <div class="p-4 rounded-xl bg-slate-900/40 border border-slate-800 hover:border-slate-700 transition-all">
          <div class="flex items-center justify-between mb-3 pb-2 border-b border-slate-800/80">
            <div class="flex items-center gap-2">
              <span class="px-2 py-0.5 rounded text-10px font-mono font-bold bg-slate-700/50 text-slate-300 border border-slate-600/30">站点信息</span>
              <span class="font-bold text-white text-xs flex items-center gap-1">
                <span class="material-symbols-outlined text-16px text-slate-400">info</span>
                <span>平台品牌与公告信息</span>
              </span>
            </div>
            <span class="badge text-10px text-slate-400 bg-slate-800">可选</span>
          </div>

          <div class="grid grid-cols-1 md:grid-cols-2 gap-4 text-xs">
            <div>
              <label class="block text-slate-300 font-semibold mb-1">站点显示名称 (Site Name)</label>
              <input
                v-model="form.siteName"
                type="text"
                placeholder="如: MAX API"
                class="input-dark w-full text-xs font-mono"
              />
            </div>
            <div>
              <label class="block text-slate-300 font-semibold mb-1">系统全站公告 (Announcement)</label>
              <input
                v-model="form.announcement"
                type="text"
                placeholder="如: 系统稳定运行中，欢迎测试"
                class="input-dark w-full text-xs font-mono"
              />
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import { systemApi, type SystemConfig } from '../../../api/client'

const loading = ref(false)
const saving = ref(false)
const feedbackMsg = ref('')
const feedbackSuccess = ref(true)

const currentHostname = window.location.hostname || '127.0.0.1'

const form = reactive<SystemConfig>({
  apiBaseUrl: '',
  siteName: 'MAX API',
  announcement: '',
})

const cleanBaseUrl = computed(() => {
  return (form.apiBaseUrl || '').trim().replace(/\/+$/, '')
})

const displayOpenAIUrl = computed(() => {
  const base = cleanBaseUrl.value || `http://${currentHostname}:18444`
  return `${base}/v1`
})

const displayAnthropicUrl = computed(() => {
  const base = cleanBaseUrl.value || `http://${currentHostname}:18444`
  return base
})

function setHostPreset() {
  form.apiBaseUrl = `http://${currentHostname}:18444`
}

function setHttpsPreset() {
  form.apiBaseUrl = `https://${currentHostname}`
}

async function loadConfig() {
  loading.value = true
  feedbackMsg.value = ''
  try {
    const res = await systemApi.getAdminConfig()
    if (res) {
      form.apiBaseUrl = res.apiBaseUrl || ''
      form.siteName = (res.siteName && res.siteName !== 'Antigravity Web' && res.siteName !== 'open max api') ? res.siteName : 'MAX API'
      form.announcement = res.announcement || ''
    }
  } catch (err: any) {
    feedbackSuccess.value = false
    feedbackMsg.value = '读取系统配置失败: ' + (err.message || '未知错误')
  } finally {
    loading.value = false
  }
}

async function saveConfig() {
  saving.value = true
  feedbackMsg.value = ''
  try {
    await systemApi.saveAdminConfig({
      apiBaseUrl: cleanBaseUrl.value,
      siteName: form.siteName?.trim(),
      announcement: form.announcement?.trim(),
    })
    feedbackSuccess.value = true
    feedbackMsg.value = '系统与 API 服务配置已成功保存！前台用户控制台已即时更新。'
  } catch (err: any) {
    feedbackSuccess.value = false
    feedbackMsg.value = '保存失败: ' + (err.message || '网络异常')
  } finally {
    saving.value = false
  }
}

function copyText(val: string, label: string) {
  if (!val) return
  navigator.clipboard.writeText(val)
  feedbackSuccess.value = true
  feedbackMsg.value = `${label}已成功复制到剪贴板`
}

onMounted(() => {
  loadConfig()
})
</script>
