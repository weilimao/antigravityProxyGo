<template>
  <div class="max-w-6xl mx-auto px-4 py-6 flex flex-col gap-6">
    <!-- 用户概览与订阅状态卡片 -->
    <div class="glass-card-glow p-6 flex flex-wrap items-center justify-between gap-4">
      <div class="flex items-center gap-4">
        <div class="w-14 h-14 rounded-2xl bg-gradient-to-tr from-indigo-600 to-pink-500 flex items-center justify-center text-white shadow-md">
          <span class="material-symbols-outlined text-32px">account_circle</span>
        </div>
        <div>
          <div class="flex items-center gap-2">
            <h2 class="text-xl font-bold text-white">{{ user?.username || '加载中...' }}</h2>
            <span v-if="user?.role === 'admin'" class="badge badge-amber">管理员</span>
            <span v-if="user?.isActive" class="badge badge-emerald">已激活订阅</span>
            <span v-else class="badge badge-rose">无有效订阅</span>
          </div>
          <p class="text-xs text-slate-400 mt-1">
            当前套餐: <strong class="text-white">{{ user?.plan?.name || '无激活套餐' }}</strong>
            <span v-if="user?.planExpireAt" class="ml-2">
              (有效期至: {{ formatTime(user?.planExpireAt) }})
            </span>
          </p>
        </div>
      </div>

      <div class="flex items-center gap-3">
        <router-link to="/pricing" class="btn-primary text-xs">
          <span class="material-symbols-outlined text-16px">upgrade</span>
          <span>{{ user?.isActive ? '续费或升配套餐' : '立即选购套餐' }}</span>
        </router-link>
      </div>
    </div>


    <!-- API Key 凭证管理卡片 -->
    <div class="glass-card p-6">
      <div class="flex items-center justify-between mb-4 pb-3 border-b border-slate-800">
        <div class="flex items-center gap-2">
          <span class="material-symbols-outlined text-indigo-400 text-20px">key</span>
          <h3 class="text-sm font-bold text-white">API 调用密钥 (API Keys)</h3>
        </div>
        <button type="button" class="btn-primary text-xs cursor-pointer" @click="showCreateKeyModal = true">
          <span class="material-symbols-outlined text-16px">add</span>
          <span>新建 API Key</span>
        </button>
      </div>

      <!-- Key 列表表格 -->
      <div class="overflow-x-auto">
        <table class="table-dark">
          <thead>
            <tr>
              <th>密钥名称</th>
              <th>密钥内容 (Key)</th>
              <th>授权模型范围</th>
              <th>限频 (RPM)</th>
              <th>创建时间</th>
              <th class="text-right">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="k in keys" :key="k.id">
              <td class="font-medium text-white">{{ k.name }}</td>
              <td class="font-mono text-xs text-indigo-300">
                <span>{{ k.key.slice(0, 10) }}...{{ k.key.slice(-6) }}</span>
                <button
                  type="button"
                  class="ml-2 text-slate-400 hover:text-white cursor-pointer inline-flex items-center"
                  title="复制完整密钥"
                  @click="copyToClipboard(k.key)"
                >
                  <span class="material-symbols-outlined text-14px">content_copy</span>
                </button>
              </td>
              <td>
                <div class="flex flex-wrap gap-1 max-w-xs">
                  <span
                    v-for="m in (k.allowedModels || [])"
                    :key="m"
                    class="badge text-10px font-mono"
                    :class="m === 'auto' ? 'badge-amber font-bold' : 'badge-cyan'"
                  >
                    {{ m === 'auto' ? '⚡ auto' : m }}
                  </span>
                </div>
              </td>
              <td><span class="text-xs font-mono text-slate-300">{{ k.rateLimit }}</span></td>
              <td class="text-xs text-slate-400">{{ formatTime(k.createdAt) }}</td>
              <td class="text-right">
                <button
                  type="button"
                  class="btn-danger cursor-pointer"
                  @click="handleDeleteKey(k.id)"
                >
                  <span class="material-symbols-outlined text-14px">delete</span>
                  <span>删除</span>
                </button>
              </td>
            </tr>
            <tr v-if="keys.length === 0">
              <td colspan="6" class="text-center py-8 text-slate-500 text-xs">
                暂未创建 API 密钥，请点击右上角新建以开始调用。
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- 快速接入客户端指导卡片 -->
    <div class="glass-card p-6">
      <h3 class="text-sm font-bold text-white mb-3 flex items-center gap-2">
        <span class="material-symbols-outlined text-indigo-400 text-18px">terminal</span>
        <span>快速接入与客户端配置</span>
      </h3>
      <div class="grid grid-cols-1 md:grid-cols-2 gap-4 text-xs">
        <div class="p-3 bg-slate-900/60 rounded-lg border border-slate-800">
          <strong class="text-slate-200 block mb-1">OpenAI 格式接入 (NextChat / Cherry Studio / Cursor)</strong>
          <p class="text-slate-400 mb-1">API Base URL:</p>
          <code class="p-1.5 block bg-black/40 rounded text-indigo-300 font-mono text-11px break-all">
            http://&lt;服务器IP&gt;:18444/v1
          </code>
        </div>
        <div class="p-3 bg-slate-900/60 rounded-lg border border-slate-800">
          <strong class="text-slate-200 block mb-1">Claude Code 环境变量接入</strong>
          <code class="p-1.5 block bg-black/40 rounded text-slate-300 font-mono text-11px break-all mt-1">
            export ANTHROPIC_BASE_URL="http://&lt;服务器IP&gt;:18444/v1"<br/>
            export ANTHROPIC_API_KEY="sk-ant-xxx"
          </code>
        </div>
      </div>
    </div>

    <!-- 新建 Key Modal 弹窗 -->
    <div v-if="showCreateKeyModal" class="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/70 backdrop-blur-sm">
      <div class="glass-card-glow w-full max-w-md p-6">
        <div class="flex items-center justify-between mb-4">
          <h3 class="text-base font-bold text-white">新建 API 调用密钥</h3>
          <span class="material-symbols-outlined text-slate-400 hover:text-white cursor-pointer" @click="showCreateKeyModal = false">close</span>
        </div>
        <div class="flex flex-col gap-4">
          <div>
            <label class="block text-xs text-slate-300 mb-1">密钥描述名称</label>
            <input v-model="newKeyForm.name" type="text" class="input-dark w-full" placeholder="如: OpenCode 专用" />
          </div>
          <p class="text-11px text-slate-400 leading-relaxed">
            该密钥由中继服务实时签发与绑定，授权调用范围为 <code class="text-amber-400 font-mono">auto</code> (并发竞速) 以及管理员在后台配置的当前套餐模型。非授权模型将被中继网关自动拦截。
          </p>
          <div class="flex items-center justify-end gap-3 mt-2">
            <button type="button" class="btn-secondary text-xs" @click="showCreateKeyModal = false">取消</button>
            <button type="button" class="btn-primary text-xs" @click="handleCreateKey">确认生成</button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { authApi, keyApi } from '../api/client'

const user = ref<any>(null)
const keys = ref<any[]>([])
const savingAuto = ref(false)
const showCreateKeyModal = ref(false)

const newKeyForm = reactive({
  name: '',
})

async function fetchUserData() {
  try {
    user.value = await authApi.getMe()
  } catch (err) {
    console.error(err)
  }
}

async function fetchKeys() {
  try {
    keys.value = await keyApi.list()
  } catch (err) {
    console.error(err)
  }
}


async function handleCreateKey() {
  try {
    await keyApi.create({
      name: newKeyForm.name || '默认密钥',
    })
    showCreateKeyModal.value = false
    newKeyForm.name = ''
    fetchKeys()
  } catch (err: any) {
    alert(err.message || '创建密钥失败')
  }
}

async function handleDeleteKey(id: number) {
  if (!confirm('确定要删除此 API Key 吗？相关客户端将无法继续调用。')) return
  try {
    await keyApi.delete(id)
    fetchKeys()
  } catch (err: any) {
    alert(err.message || '删除失败')
  }
}

function copyToClipboard(text: string) {
  navigator.clipboard.writeText(text)
  alert('密钥已复制到剪贴板！')
}

function formatTime(timestamp: number | string): string {
  if (!timestamp) return '未设置'
  let d: Date
  if (typeof timestamp === 'number') {
    d = new Date(timestamp * 1000)
  } else {
    d = new Date(timestamp)
  }
  return d.toLocaleString()
}

onMounted(() => {
  fetchUserData()
  fetchKeys()
})
</script>
