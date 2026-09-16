<template>
  <div class="glass-card p-6 mt-6 relative overflow-hidden">
    <!-- Header -->
    <div class="flex flex-col sm:flex-row sm:items-center justify-between mb-4 pb-3 border-b border-slate-800 gap-4">
      <div class="flex items-center gap-3">
        <span class="w-10 h-10 rounded-xl bg-indigo-500/10 text-indigo-400 flex items-center justify-center border border-indigo-500/20">
          <span class="material-symbols-outlined text-24px">speed</span>
        </span>
        <div>
          <h3 class="text-base font-bold text-white flex items-center gap-2">
            <span>网关模型响应测速</span>
            <span v-if="running" class="badge badge-indigo text-[10px] animate-pulse">测速中...</span>
            <span v-else class="badge bg-slate-800 text-slate-300 text-[10px]">空闲</span>
          </h3>
          <p class="text-xs text-slate-400 mt-0.5">
            定时通过 127.0.0.1 回环探测各模型真实端到端延迟，并供竞速池参考。
          </p>
        </div>
      </div>
      <div class="flex items-center gap-2">
        <button class="btn-primary text-xs flex items-center gap-1.5" @click="runBenchmark" :disabled="running">
          <span class="material-symbols-outlined text-14px">play_arrow</span>
          立即测速
        </button>
        <button class="btn-secondary text-xs flex items-center gap-1.5" @click="showConfig = true">
          <span class="material-symbols-outlined text-14px">settings</span>
          配置
        </button>
      </div>
    </div>

    <!-- Results Grid -->
    <div v-if="results && results.length > 0" class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3">
      <div v-for="(res, index) in sortedResults" :key="res.model" 
           class="bg-slate-800/50 rounded-xl p-3 border transition-colors relative"
           :class="[isPending(res.model) ? 'border-indigo-500/50' : (res.status === 'error' ? 'border-red-500/30' : (res.status === 'warning' ? 'border-amber-500/30' : 'border-emerald-500/30'))]">
        
        <!-- Rank Badge -->
        <div v-if="!isPending(res.model) && res.status !== 'error' && index < 3" 
             class="absolute -top-2 -right-2 w-6 h-6 rounded-full flex items-center justify-center text-[10px] font-bold shadow-lg"
             :class="[index === 0 ? 'bg-yellow-500 text-yellow-950' : (index === 1 ? 'bg-slate-300 text-slate-800' : 'bg-amber-700 text-amber-100')]">
          {{ index + 1 }}
        </div>

        <div class="flex items-center justify-between mb-2">
          <div class="flex items-center gap-2 truncate pr-4">
            <span class="relative flex h-2.5 w-2.5 shrink-0">
              <span v-if="isPending(res.model)" class="animate-ping absolute inline-flex h-full w-full rounded-full bg-indigo-400 opacity-75"></span>
              <span class="relative inline-flex rounded-full h-2.5 w-2.5" 
                    :class="[isPending(res.model) ? 'bg-indigo-500' : (res.status === 'error' ? 'bg-red-500' : (res.status === 'warning' ? 'bg-amber-500' : 'bg-emerald-500'))]"></span>
            </span>
            <span class="text-sm font-semibold text-slate-200 truncate" :title="res.model">{{ res.model }}</span>
          </div>
          <button @click="runSingleModel(res.model)" :disabled="isPending(res.model)" class="text-slate-500 hover:text-white transition-colors disabled:opacity-50" title="重测此模型">
            <span class="material-symbols-outlined text-[14px]" :class="isPending(res.model) ? 'animate-spin' : ''">refresh</span>
          </button>
        </div>

        <div v-if="res.status === 'error' && !isPending(res.model)" class="text-xs text-red-400 mt-2 truncate" :title="res.error">
          {{ res.error || 'Request failed' }}
        </div>
        <div v-else-if="!isPending(res.model)" class="flex items-end justify-between mt-2">
          <div>
            <div class="text-[10px] text-slate-500">首帧 (TTFT)</div>
            <div class="flex items-center gap-1.5">
              <span class="text-base font-mono font-bold" :class="getTtft(res) > 2000 ? 'text-amber-400' : 'text-emerald-400'">{{ getTtft(res) }}<span class="text-[10px] ml-0.5 opacity-70">ms</span></span>
              <span v-if="getPrevTtft(res) > 0" class="text-[10px] font-bold" :class="getTrendColor(getTtft(res), getPrevTtft(res))">
                {{ getTrendIcon(getTtft(res), getPrevTtft(res)) }}
              </span>
            </div>
          </div>
          <div class="text-right">
            <div class="text-[10px] text-slate-500">总耗时 (Total)</div>
            <div class="flex items-center gap-1.5 justify-end">
              <span class="text-base font-mono font-bold text-slate-300">{{ getTotal(res) }}<span class="text-[10px] ml-0.5 opacity-70">ms</span></span>
              <span v-if="getPrevTotal(res) > 0" class="text-[10px] font-bold" :class="getTrendColor(getTotal(res), getPrevTotal(res))">
                {{ getTrendIcon(getTotal(res), getPrevTotal(res)) }}
              </span>
            </div>
          </div>
        </div>
        <div v-else class="text-xs text-indigo-400 mt-2 flex items-center gap-1 font-mono">
          <span class="material-symbols-outlined text-[14px] animate-spin">autorenew</span>
          Testing...
        </div>
      </div>
    </div>
    
    <div v-else class="py-10 text-center flex flex-col items-center justify-center border border-dashed border-slate-700 rounded-xl bg-slate-800/30">
      <span class="material-symbols-outlined text-4xl text-slate-600 mb-2">speed</span>
      <p class="text-slate-400 text-sm">暂无测速数据</p>
      <button class="text-primary hover:underline text-xs mt-2" @click="showConfig = true">配置并测速</button>
    </div>

    <div class="mt-4 pt-3 border-t border-slate-800 flex justify-between items-center text-xs text-slate-500">
      <div class="flex items-center gap-2">
        <span class="material-symbols-outlined text-14px">schedule</span>
        上次测速: {{ formatTime(lastRun) }}
      </div>
      <div>
        自动间隔: {{ config?.intervalMinutes || 5 }} 分钟
      </div>
    </div>

    <BenchmarkConfigModal v-if="showConfig" :initialConfig="config" @close="showConfig = false" @saved="onConfigSaved" />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { benchmarkApi } from '@/api/client'
import BenchmarkConfigModal from './BenchmarkConfigModal.vue'

const config = ref<any>(null)
const results = ref<any[]>([])
const running = ref(false)
const lastRun = ref('')
const pendingModels = ref<string[]>([])
const showConfig = ref(false)

let pollTimer: number | null = null
let fastPollCount = 0

const loadData = async () => {
  try {
    const res = await benchmarkApi.get()
    if (res.success) {
      config.value = res.config
      results.value = res.results || []
      running.value = res.running
      lastRun.value = res.lastRun
      pendingModels.value = res.pendingModels || []
      
      if (running.value || pendingModels.value.length > 0) {
        fastPollCount = 12 // Keep fast polling while running
      }
    }
  } catch (error: any) {
    console.error('Failed to load benchmark data:', error)
  }
}

const runBenchmark = async () => {
  try {
    const res = await benchmarkApi.run()
    if (res && (res.success || res.status === 'ok')) {
      alert('已触发模型响应测速，正在后台并发探测各模型延迟...')
      running.value = true
      fastPollCount = 12 // Fast poll 5s * 12 = 1 min
      await loadData()
    } else {
      alert(res?.error || '触发测速失败: 网关未返回成功状态')
    }
  } catch (error: any) {
    alert(error.message || '触发测速失败')
  }
}

const runSingleModel = async (model: string) => {
  try {
    const res = await benchmarkApi.runModel(model)
    if (res && (res.success || res.status === 'ok')) {
      alert(`已触发 ${model} 测速`)
      if (!pendingModels.value.includes(model)) {
        pendingModels.value.push(model)
      }
      fastPollCount = 12
      await loadData()
    } else {
      alert(res?.error || `触发 ${model} 测速失败`)
    }
  } catch (error: any) {
    alert(error.message || '触发测速失败')
  }
}

const onConfigSaved = () => {
  showConfig.value = false
  fastPollCount = 12
  loadData()
}

const isPending = (model: string) => {
  return pendingModels.value.includes(model)
}

const getTtft = (r: any) => r?.ttftMs ?? r?.ttft_ms ?? 0
const getPrevTtft = (r: any) => r?.prevTtftMs ?? r?.prev_ttft_ms ?? 0
const getTotal = (r: any) => r?.totalMs ?? r?.total_ms ?? 0
const getPrevTotal = (r: any) => r?.prevTotalMs ?? r?.prev_total_ms ?? 0

const sortedResults = computed(() => {
  if (!results.value) return []
  return [...results.value].sort((a, b) => {
    // Error models to the bottom
    if (a.status === 'error' && b.status !== 'error') return 1
    if (a.status !== 'error' && b.status === 'error') return -1
    // Sort by TTFT
    const aTtft = getTtft(a) || 999999
    const bTtft = getTtft(b) || 999999
    return aTtft - bTtft
  })
})

const getTrendIcon = (current: number, prev: number) => {
  if (!prev) return ''
  const diff = current - prev
  if (Math.abs(diff) <= 20) return '≈' // within 20ms is neutral
  return diff > 0 ? '↑' : '↓'
}

const getTrendColor = (current: number, prev: number) => {
  if (!prev) return ''
  const diff = current - prev
  if (Math.abs(diff) <= 20) return 'text-slate-500'
  return diff > 0 ? 'text-red-400' : 'text-emerald-400' // Up means slower (bad), Down means faster (good)
}

const formatTime = (isoString: string) => {
  if (!isoString || isoString.startsWith('0001')) return '从未'
  const d = new Date(isoString)
  if (isNaN(d.getTime())) return '从未'
  return d.toLocaleString()
}

const startPolling = () => {
  pollTimer = window.setInterval(() => {
    loadData()
    if (fastPollCount > 0) {
      fastPollCount--
      // Fast poll is 5s, normal is 30s. Since setInterval is fixed, we just skip some intervals if not fast polling.
      // Actually a dynamic timer is better.
    }
  }, 5000) as unknown as number
}

// Custom adaptive polling
const adaptivePoll = async () => {
  await loadData()
  let delay = 30000
  if (fastPollCount > 0 || running.value || pendingModels.value.length > 0) {
    delay = 5000
    if (fastPollCount > 0) fastPollCount--
  }
  pollTimer = window.setTimeout(adaptivePoll, delay) as unknown as number
}

onMounted(() => {
  loadData().then(() => {
    adaptivePoll()
  })
})

onUnmounted(() => {
  if (pollTimer) clearTimeout(pollTimer)
})
</script>
