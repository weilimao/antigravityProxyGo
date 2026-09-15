<template>
  <div class="flex flex-col gap-6">
    <div class="flex items-center justify-between">
      <div>
        <h3 class="text-base font-bold text-white flex items-center gap-2">
          <span>中继模型映射与多号池路由配置</span>
          <span class="badge badge-indigo">对标桌面端核心</span>
        </h3>
        <p class="text-xs text-slate-400">
          配置入站客户端请求模型名（ClientModel）到上游真实模型名（TargetModel）的转译映射、号池分流与原生多模态控制
        </p>
      </div>

      <div class="flex items-center gap-3">
        <button
          type="button"
          :disabled="pulling"
          class="btn-secondary text-xs flex items-center gap-1.5"
          @click="pullMappings"
          title="从远端 18444 服务端网关拉取最新模型映射配置"
        >
          <span class="material-symbols-outlined text-16px" :class="{ 'animate-spin': pulling }">cloud_download</span>
          <span>{{ pulling ? '同步中...' : '从服务端同步' }}</span>
        </button>
        <button type="button" class="btn-secondary text-xs" @click="addRow">
          <span class="material-symbols-outlined text-16px">add</span>
          <span>添加映射行</span>
        </button>
        <button type="button" :disabled="saving" class="btn-primary text-xs" @click="saveMappings">
          <span class="material-symbols-outlined text-16px" :class="{ 'animate-spin': saving }">save</span>
          <span>{{ saving ? '保存中...' : '保存并下发网关' }}</span>
        </button>
      </div>
    </div>

    <!-- 同步反馈提示条 -->
    <div v-if="syncMsg" class="p-3 rounded-lg flex items-center justify-between text-xs" :class="syncSuccess ? 'bg-emerald-500/10 text-emerald-300 border border-emerald-500/30' : 'bg-rose-500/10 text-rose-300 border border-rose-500/30'">
      <div class="flex items-center gap-2">
        <span class="material-symbols-outlined text-16px">{{ syncSuccess ? 'check_circle' : 'error' }}</span>
        <span>{{ syncMsg }}</span>
      </div>
      <button type="button" @click="syncMsg = ''" class="hover:opacity-80">
        <span class="material-symbols-outlined text-14px">close</span>
      </button>
    </div>

    <!-- 模型映射表格 -->
    <div class="glass-card overflow-hidden">
      <div class="overflow-x-auto">
        <table class="table-dark">
          <thead>
            <tr>
              <th>入站模型名 (Client Model)</th>
              <th>目标上游模型名 (Target Model)</th>
              <th>路由号池 (Target Provider)</th>
              <th>多模态模式 (Multimodal)</th>
              <th>注入思考参数</th>
              <th>对外暴露 (Expose)</th>
              <th class="text-right">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(item, idx) in mappings" :key="idx">
              <td>
                <input
                  v-model="item.clientModel"
                  type="text"
                  placeholder="如: claude-3-7-sonnet"
                  class="input-dark text-xs w-44 font-mono"
                />
              </td>
              <td>
                <input
                  v-model="item.targetModel"
                  type="text"
                  placeholder="如: deepseek-ai/deepseek-v3"
                  class="input-dark text-xs w-48 font-mono"
                />
              </td>
              <td>
                <select v-model="item.targetProvider" class="input-dark text-xs cursor-pointer font-mono">
                  <option value="google">google (Gemini)</option>
                  <option value="claude">claude (Anthropic)</option>
                  <option value="nvidia">nvidia (NVIDIA NIM)</option>
                  <option value="grok">grok (xAI Grok)</option>
                  <option value="other">other (第三方聚合)</option>
                  <option value="auto">auto (并发竞速池)</option>
                </select>
              </td>
              <td>
                <select
                  :value="item.multimodal === null || item.multimodal === undefined ? '__auto__' : item.multimodal ? 'true' : 'false'"
                  @change="(e: any) => onMultimodalChange(item, e.target.value)"
                  class="input-dark text-xs cursor-pointer"
                >
                  <option value="__auto__">自动判定 (启发式)</option>
                  <option value="true">强制多模态 (直送)</option>
                  <option value="false">非多模态 (走OCR降级)</option>
                </select>
              </td>
              <td class="text-center">
                <input
                  type="checkbox"
                  :checked="item.injectChatTemplateKwargs !== false"
                  @change="(e: any) => item.injectChatTemplateKwargs = e.target.checked"
                  class="w-4 h-4 text-indigo-600 rounded bg-slate-900 border-slate-700 cursor-pointer"
                />
              </td>
              <td class="text-center">
                <input
                  type="checkbox"
                  v-model="item.expose"
                  class="w-4 h-4 text-indigo-600 rounded bg-slate-900 border-slate-700 cursor-pointer"
                />
              </td>
              <td class="text-right">
                <button type="button" class="btn-danger text-xs py-1 px-2 cursor-pointer" @click="deleteRow(idx)">
                  <span class="material-symbols-outlined text-14px">delete</span>
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { mappingApi } from '../../../api/client'

const mappings = ref<any[]>([])
const saving = ref(false)
const pulling = ref(false)
const syncMsg = ref('')
const syncSuccess = ref(true)

async function fetchMappings() {
  try {
    const list = await mappingApi.getMappings()
    mappings.value = Array.isArray(list) ? list : []
  } catch (err: any) {
    console.error('加载模型映射失败:', err)
  }
}

async function pullMappings() {
  pulling.value = true
  syncMsg.value = ''
  try {
    const res = await mappingApi.pullMappings()
    const pulled = res.mappings || res
    mappings.value = Array.isArray(pulled) ? pulled : []
    syncSuccess.value = true
    syncMsg.value = `已成功从远端 18444 服务端网关拉取最新模型映射配置（共 ${mappings.value.length} 条）！`
  } catch (err: any) {
    syncSuccess.value = false
    syncMsg.value = `从远端网关同步失败: ${err.message || err}`
  } finally {
    pulling.value = false
  }
}

function addRow() {
  mappings.value.push({
    clientModel: '',
    targetModel: '',
    targetProvider: 'google',
    expose: true,
    injectChatTemplateKwargs: true,
    multimodal: null,
  })
}

function deleteRow(idx: number) {
  mappings.value.splice(idx, 1)
}

function onMultimodalChange(item: any, val: string) {
  if (val === '__auto__') {
    item.multimodal = null
  } else {
    item.multimodal = val === 'true'
  }
}

async function saveMappings() {
  saving.value = true
  syncMsg.value = ''
  try {
    await mappingApi.setMappings(mappings.value)
    syncSuccess.value = true
    syncMsg.value = `模型映射配置（${mappings.value.length} 条规则）已持久化保存，并向现网 18444 Go Relay 网关热同步生效！`
  } catch (err: any) {
    syncSuccess.value = false
    syncMsg.value = err.message || '保存失败'
  } finally {
    saving.value = false
  }
}

onMounted(() => {
  fetchMappings()
})
</script>
