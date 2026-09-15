<template>
  <div class="w-full flex flex-col gap-6">
    <div class="glass-card p-6">
      <div class="flex items-center justify-between mb-4 pb-3 border-b border-slate-800">
        <div class="flex items-center gap-3">
          <span class="w-10 h-10 rounded-xl bg-amber-500/10 text-amber-400 flex items-center justify-center border border-amber-500/20">
            <span class="material-symbols-outlined text-24px">bolt</span>
          </span>
          <div>
            <h3 class="text-base font-bold text-white flex items-center gap-2">
              <span>全局 Auto 并发竞速默认规则</span>
              <span class="badge badge-amber text-[10px] font-mono">跨号池并发首字抢占</span>
            </h3>
            <p class="text-xs text-slate-400 mt-0.5">
              当客户端请求名为 <code class="font-mono text-indigo-400">auto</code> 的虚拟竞速模型时，多模型同时并发请求，最先响应（首字）胜出回传，其余自动中断。
            </p>
          </div>
        </div>

        <!-- 从服务端拉取最新配置按钮 -->
        <button
          type="button"
          :disabled="pulling"
          class="btn-secondary text-xs flex items-center gap-1.5 cursor-pointer"
          @click="pullFromGateway"
          title="直接从 Linux 服务端 (18444 网关) 重新拉取当前生效的真实 Auto 模型与规则"
        >
          <span class="material-symbols-outlined text-14px" :class="pulling ? 'animate-spin' : ''">sync</span>
          <span>{{ pulling ? '拉取中...' : '从服务端同步' }}</span>
        </button>
      </div>

      <div class="flex flex-col gap-5 text-xs">
        <div class="flex items-center justify-between p-3 bg-slate-900/50 rounded-lg border border-slate-800">
          <div>
            <label class="font-semibold text-slate-200 block">默认启用 Auto 并发竞速</label>
            <span class="text-[11px] text-slate-500">开启后，客户端传入 auto 模型时自动触发首字极速并发竞速</span>
          </div>
          <input
            type="checkbox"
            v-model="autoConfig.enabled"
            class="w-4 h-4 text-indigo-600 rounded bg-slate-900 border-slate-700 cursor-pointer"
          />
        </div>

        <!-- 候选模型选择器（采用公共 ModelSearchSelect 组件） -->
        <div class="flex flex-col gap-2">
          <div class="flex items-center justify-between">
            <label class="font-semibold text-slate-200 flex items-center gap-1.5">
              <span class="material-symbols-outlined text-amber-400 text-16px">playlist_add_check</span>
              <span>默认候选参赛模型池 (Candidate Models，已选 {{ autoConfig.candidateModels.length }} 个)</span>
            </label>
            <span class="text-[11px] text-slate-500">
              当前竞速候选: {{ autoConfig.candidateModels.length }} 个模型 (支持手动输入添加)
            </span>
          </div>

          <!-- 通用模型搜索多选组件 -->
          <ModelSearchSelect
            :multiple="true"
            :model-ids="autoConfig.candidateModels"
            :options="availableModels"
            :allow-custom="true"
            placeholder="搜索并多选参赛模型，或键盘输入自定义模型按回车添加..."
            @update:model-ids="(val) => autoConfig.candidateModels = val"
          />

          <!-- 快捷候选模型推荐点击池 -->
          <div v-if="quickModels.length > 0" class="mt-1 flex flex-wrap items-center gap-1.5">
            <span class="text-[11px] text-slate-500 mr-1">快捷添加:</span>
            <button
              v-for="qm in quickModels"
              :key="qm"
              type="button"
              class="px-2 py-0.5 rounded text-[11px] font-mono transition-all border cursor-pointer flex items-center gap-0.5"
              :class="autoConfig.candidateModels.includes(qm)
                ? 'bg-amber-500/20 text-amber-300 border-amber-500/40'
                : 'bg-slate-800/80 text-slate-400 border-slate-700 hover:text-white hover:border-slate-600'"
              @click="toggleQuickModel(qm)"
            >
              <span class="material-symbols-outlined text-[12px]">{{ autoConfig.candidateModels.includes(qm) ? 'check' : 'add' }}</span>
              <span>{{ qm }}</span>
            </button>
          </div>
        </div>

        <div class="flex items-center justify-between p-3 bg-slate-900/50 rounded-lg border border-slate-800">
          <div>
            <span class="font-semibold text-slate-200 block">默认联动控制台测速池 (Use Benchmark Pool)</span>
            <p class="text-[11px] text-slate-500">自动将响应耗时最低且健康的模型动态融入并发抢占队列，实现零配置免维护自愈</p>
          </div>
          <input
            type="checkbox"
            v-model="autoConfig.useBenchmarkPool"
            class="w-4 h-4 text-indigo-600 rounded bg-slate-900 border-slate-700 cursor-pointer"
          />
        </div>

        <div class="pt-4 border-t border-slate-800 flex items-center justify-between">
          <span class="text-[11px] text-slate-500">
            💡 保存后将即刻落库并自动向 Linux 服务端 (18444) 下发热生效
          </span>
          <button
            type="button"
            :disabled="saving"
            class="btn-primary"
            @click="saveConfig"
          >
            <span class="material-symbols-outlined text-16px">{{ saving ? 'sync' : 'save' }}</span>
            <span>{{ saving ? '保存并同步中...' : '保存全局 Auto 规则' }}</span>
          </button>
        </div>
      </div>
    </div>

    <!-- 测速卡片 -->
    <BenchmarkCard />
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import { autoApi, mappingApi } from '../../../api/client'
import BenchmarkCard from './BenchmarkCard.vue'
import ModelSearchSelect from '../../../components/common/ModelSearchSelect.vue'

const saving = ref(false)
const pulling = ref(false)

const autoConfig = reactive({
  enabled: true,
  candidateModels: [] as string[],
  useBenchmarkPool: true,
})

const availableModels = ref<string[]>([])

const quickModels = computed(() => {
  return autoConfig.candidateModels.filter(m => m !== 'auto').slice(0, 12)
})

async function fetchConfig() {
  try {
    const res = await autoApi.getGlobalConfig()
    if (res) {
      autoConfig.enabled = res.enabled ?? true
      autoConfig.candidateModels = Array.isArray(res.candidateModels) ? res.candidateModels : []
      autoConfig.useBenchmarkPool = res.useBenchmarkPool ?? true
    }
  } catch (err) {
    console.error('加载 Auto 配置失败:', err)
  }
}

async function fetchAvailableModels() {
  try {
    const res = await mappingApi.getAvailableModels()
    if (res && Array.isArray(res)) {
      availableModels.value = res
    }
  } catch (err) {
    console.error('加载可用模型列表失败:', err)
  }
}

function toggleQuickModel(m: string) {
  const idx = autoConfig.candidateModels.indexOf(m)
  if (idx >= 0) {
    autoConfig.candidateModels.splice(idx, 1)
  } else {
    autoConfig.candidateModels.push(m)
  }
}

async function pullFromGateway() {
  pulling.value = true
  try {
    const res = await autoApi.pullGlobalConfig()
    if (res) {
      autoConfig.enabled = res.enabled ?? true
      autoConfig.candidateModels = Array.isArray(res.candidateModels) ? res.candidateModels : []
      autoConfig.useBenchmarkPool = res.useBenchmarkPool ?? true
    }
    alert(`已成功从 Go Relay 服务端同步 Auto 竞速规则！(候选模型: ${autoConfig.candidateModels.length} 个)`)
  } catch (err: any) {
    alert(err.message || '从服务端网关拉取失败')
  } finally {
    pulling.value = false
  }
}

async function saveConfig() {
  saving.value = true
  try {
    await autoApi.setGlobalConfig(autoConfig)
    alert('全局 Auto 竞速规则已保存并同步至服务端！')
  } catch (err: any) {
    alert(err.message || '保存失败')
  } finally {
    saving.value = false
  }
}

onMounted(async () => {
  await Promise.all([
    fetchConfig(),
    fetchAvailableModels()
  ])
  // 本地无候选模型时，自动从网关拉取服务端真实配置
  if (autoConfig.candidateModels.length === 0) {
    try {
      const res = await autoApi.pullGlobalConfig()
      if (res) {
        autoConfig.enabled = res.enabled ?? true
        autoConfig.candidateModels = Array.isArray(res.candidateModels) ? res.candidateModels : []
        autoConfig.useBenchmarkPool = res.useBenchmarkPool ?? true
      }
    } catch (err) {
      console.warn('自动从网关拉取 Auto 配置失败，可手动点击"从服务端同步":', err)
    }
  }
})
</script>
