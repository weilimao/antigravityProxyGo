<template>
  <div class="space-y-4">
    <!-- 顶部控制条与标题 -->
    <div class="flex flex-wrap items-center justify-between gap-3 pb-2 border-b border-slate-800">
      <div class="flex items-center gap-2">
        <span class="w-8 h-8 rounded-lg bg-indigo-500/20 text-indigo-400 border border-indigo-500/30 flex items-center justify-center">
          <span class="material-symbols-outlined text-18px">fact_check</span>
        </span>
        <div>
          <h3 class="text-sm font-bold text-white flex items-center gap-2">
            <span>请求命中模型日志</span>
            <span class="text-[11px] px-2 py-0.5 rounded-full font-medium bg-slate-800 text-slate-300 border border-slate-700">
              {{ total }} 条记录
            </span>
          </h3>
          <p class="text-[11px] text-slate-400 mt-0.5">
            {{ mode === 'admin' ? '全域实时网关调用流水、上游真实模型命中与耗时监控' : '您当前账号的实时请求调用、模型命中与响应耗时监控' }}
          </p>
        </div>
      </div>

      <div class="flex items-center gap-2 text-xs">
        <!-- 自动刷新切换 -->
        <label class="flex items-center gap-1.5 text-slate-400 hover:text-white cursor-pointer select-none bg-slate-900/80 px-2.5 py-1.5 rounded-lg border border-slate-800">
          <input v-model="autoRefresh" type="checkbox" class="accent-indigo-600 rounded" />
          <span>自动刷新 (10s)</span>
        </label>

        <button
          type="button"
          class="btn-secondary text-xs py-1.5 px-3 flex items-center gap-1 cursor-pointer"
          :disabled="loading"
          @click="loadLogs"
        >
          <span class="material-symbols-outlined text-14px" :class="{ 'animate-spin': loading }">refresh</span>
          <span>刷新</span>
        </button>
      </div>
    </div>

    <!-- 顶部四块核心指标卡片 -->
    <div class="grid grid-cols-2 lg:grid-cols-4 gap-3">
      <!-- 1. 24h 总请求数 -->
      <div class="p-3.5 rounded-xl bg-slate-950/60 border border-slate-800/80 flex flex-col justify-between">
        <div class="flex items-center justify-between text-slate-400 text-11px mb-1">
          <span>总请求次数</span>
          <span class="material-symbols-outlined text-indigo-400 text-16px">sync_alt</span>
        </div>
        <div class="flex items-baseline gap-1.5">
          <span class="text-xl font-mono font-extrabold text-white">{{ summary.totalRequests.toLocaleString() }}</span>
          <span class="text-10px text-slate-400 font-sans">次</span>
        </div>
        <div class="text-[10px] text-slate-500 mt-1">最近 24 小时活跃调用量</div>
      </div>

      <!-- 2. 消耗成本 -->
      <div class="p-3.5 rounded-xl bg-slate-950/60 border border-slate-800/80 flex flex-col justify-between">
        <div class="flex items-center justify-between text-slate-400 text-11px mb-1">
          <span>累计成本预估</span>
          <span class="material-symbols-outlined text-emerald-400 text-16px">payments</span>
        </div>
        <div class="flex items-baseline gap-1.5">
          <span class="text-xl font-mono font-extrabold text-emerald-400">${{ summary.totalCost.toFixed(4) }}</span>
          <span class="text-10px text-slate-400 font-sans">USD</span>
        </div>
        <div class="text-[10px] text-slate-500 mt-1">输入 ${{ summary.inputCost.toFixed(4) }} · 输出 ${{ summary.outputCost.toFixed(4) }}</div>
      </div>

      <!-- 3. Tokens 统计 -->
      <div class="p-3.5 rounded-xl bg-slate-950/60 border border-slate-800/80 flex flex-col justify-between">
        <div class="flex items-center justify-between text-slate-400 text-11px mb-1">
          <span>总吞吐 Tokens</span>
          <span class="material-symbols-outlined text-amber-400 text-16px">data_usage</span>
        </div>
        <div class="flex items-baseline gap-1.5">
          <span class="text-xl font-mono font-extrabold text-amber-300">
            {{ formatTokenNumber(totalThroughputTokens) }}
          </span>
          <span class="text-10px text-slate-400 font-sans">Tokens</span>
        </div>
        <div class="text-[10px] text-slate-500 mt-1">入: {{ formatTokenNumber(inTokens) }} · 出: {{ formatTokenNumber(outTokens) }}</div>
      </div>

      <!-- 4. 缓存命中率 -->
      <div class="p-3.5 rounded-xl bg-slate-950/60 border border-slate-800/80 flex flex-col justify-between">
        <div class="flex items-center justify-between text-slate-400 text-11px mb-1">
          <span>Prompt 缓存命中率</span>
          <span class="material-symbols-outlined text-cyan-400 text-16px">bolt</span>
        </div>
        <div class="flex items-baseline gap-1.5">
          <span class="text-xl font-mono font-extrabold text-cyan-300">{{ (summary.cacheHitRate || 0).toFixed(1) }}%</span>
          <span class="text-10px text-slate-400 font-sans">Hit Rate</span>
        </div>
        <div class="text-[10px] text-slate-500 mt-1">节省 Token: {{ formatTokenNumber(cachedTokens) }}</div>
      </div>
    </div>

    <!-- 模型性能分布条 (有数据时展示) -->
    <div v-if="modelPerf.length > 0" class="p-3 bg-slate-950/40 rounded-xl border border-slate-800/80 flex flex-wrap items-center gap-4 text-xs">
      <span class="text-slate-400 font-medium text-11px flex items-center gap-1">
        <span class="material-symbols-outlined text-14px text-indigo-400">speed</span>
        <span>模型延迟分析:</span>
      </span>
      <div v-for="m in modelPerf" :key="m.model" class="flex items-center gap-2 bg-slate-900/80 px-2.5 py-1 rounded-lg border border-slate-800">
        <span class="font-mono text-white font-medium">{{ m.model }}</span>
        <span class="text-[10px] text-slate-500">({{ m.count }}次)</span>
        <span class="text-[10px] font-mono text-emerald-400">均延 {{ formatDuration(m.avgDurationMs) }}</span>
        <span class="text-[10px] font-mono text-amber-300">TTFT {{ formatDuration(m.avgTtftMs) }}</span>
      </div>
    </div>

    <!-- 筛选工具栏 -->
    <div class="flex flex-wrap items-center justify-between gap-3 bg-slate-900/60 p-2.5 rounded-xl border border-slate-800">
      <div class="flex flex-wrap items-center gap-2 text-xs">
        <!-- 账号选择 (全部账号/上游号池账号) -->
        <div class="flex items-center gap-1.5">
          <span class="text-slate-400 text-11px font-medium">账号:</span>
          <select
            v-model="selectedAccount"
            class="bg-slate-800 text-white font-medium border border-slate-700 rounded-lg px-2.5 py-1 text-xs focus:outline-none focus:border-indigo-500 cursor-pointer max-w-[200px]"
            @change="onFilterChange"
          >
            <option value="all">全部账号 (All Accounts)</option>
            <option v-for="acc in accounts" :key="acc" :value="acc">{{ acc }}</option>
          </select>
        </div>

        <!-- 状态胶囊筛选 -->
        <div class="flex items-center bg-slate-950/80 p-0.5 rounded-lg border border-slate-800 text-[11px]">
          <button
            v-for="st in statusOptions"
            :key="st.value"
            type="button"
            class="px-2.5 py-1 rounded-md font-medium transition-all cursor-pointer"
            :class="selectedStatus === st.value ? 'bg-indigo-600 text-white font-semibold shadow-sm' : 'text-slate-400 hover:text-white'"
            @click="selectStatus(st.value)"
          >
            {{ st.label }}
          </button>
        </div>
      </div>

      <!-- 关键词搜索 -->
      <div class="flex items-center gap-2">
        <div class="relative w-48 sm:w-64">
          <span class="material-symbols-outlined absolute left-2.5 top-1/2 -translate-y-1/2 text-slate-400 text-14px">search</span>
          <input
            v-model="search"
            type="text"
            placeholder="搜索请求 ID / 模型..."
            class="bg-slate-950 text-white text-xs pl-8 pr-3 py-1.5 rounded-lg border border-slate-800 focus:outline-none focus:border-indigo-500 w-full"
            @keyup.enter="onFilterChange"
            @blur="onFilterChange"
          />
        </div>
      </div>
    </div>

    <!-- 请求日志表格 -->
    <div class="overflow-x-auto rounded-xl border border-slate-800/80 bg-slate-950/40">
      <table class="table-dark text-xs">
        <thead>
          <tr>
            <th class="w-36">请求时间</th>
            <th class="w-32">账号</th>
            <th>命中模型 (Model)</th>
            <th class="w-20">状态</th>
            <th class="w-20">缓存</th>
            <th class="w-28">首字 / 总耗时</th>
            <th class="w-28">Tokens (入/出)</th>
            <th class="w-24">预估成本</th>
            <th class="w-16 text-right">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-if="loading && logs.length === 0">
            <td :colspan="9" class="text-center py-8 text-slate-400">
              <div class="flex items-center justify-center gap-2">
                <span class="material-symbols-outlined text-18px animate-spin">refresh</span>
                <span>加载日志数据中...</span>
              </div>
            </td>
          </tr>
          <tr v-else-if="logs.length === 0">
            <td :colspan="9" class="text-center py-8 text-slate-500">
              暂无匹配的请求命中记录
            </td>
          </tr>
          <tr v-for="item in logs" :key="item.id || item.reqId" class="hover:bg-slate-900/50 transition-colors">
            <!-- 请求时间 -->
            <td class="font-mono text-slate-400 text-[11px] whitespace-nowrap">
              {{ formatLogTime(item.timestamp) }}
            </td>

            <!-- 账号 -->
            <td class="font-mono text-[11px] text-slate-300">
              <span class="px-1.5 py-0.5 rounded bg-slate-800 text-slate-300 border border-slate-700/60 truncate block max-w-[130px]" :title="item.account">
                {{ item.account || '-' }}
              </span>
            </td>

            <!-- 命中模型 -->
            <td>
              <div class="flex flex-col gap-0.5">
                <span class="font-mono font-medium text-white">{{ item.model }}</span>
                <span v-if="item.path" class="text-[10px] font-mono text-slate-500 truncate max-w-xs">{{ item.path }}</span>
              </div>
            </td>

            <!-- 状态 -->
            <td>
              <span
                class="px-1.5 py-0.5 rounded font-mono text-[10px] font-bold inline-block"
                :class="item.statusCode >= 200 && item.statusCode < 300 ? 'bg-emerald-500/20 text-emerald-300 border border-emerald-500/30' : 'bg-rose-500/20 text-rose-300 border border-rose-500/30'"
              >
                {{ item.statusCode || 200 }}
              </span>
            </td>

            <!-- 缓存状态 -->
            <td>
              <span
                v-if="item.cacheStatus === 'hit' || item.cachedTokens > 0"
                class="inline-flex items-center gap-0.5 text-emerald-400 font-medium text-[10px] bg-emerald-500/10 px-1.5 py-0.5 rounded border border-emerald-500/20"
                title="命中 Prompt 缓存"
              >
                <span class="material-symbols-outlined text-[12px]">bolt</span>
                <span>Hit</span>
              </span>
              <span v-else class="text-slate-500 text-[10px] font-mono">Miss</span>
            </td>

            <!-- 首字 / 总耗时 -->
            <td class="font-mono text-[11px] text-slate-300 whitespace-nowrap">
              <span class="text-amber-300">{{ formatDuration(item.firstByteMs) }}</span>
              <span class="text-slate-500 mx-1">/</span>
              <span>{{ formatDuration(item.durationMs) }}</span>
            </td>

            <!-- Tokens (入 / 出) -->
            <td class="font-mono text-[11px] text-slate-300 whitespace-nowrap">
              <span class="text-indigo-300">{{ formatTokenNumber(item.inTokens) }}</span>
              <span class="text-slate-500 mx-1">/</span>
              <span class="text-emerald-300">{{ formatTokenNumber(item.outTokens) }}</span>
            </td>

            <!-- 预估成本 -->
            <td class="font-mono text-[11px] text-emerald-400 whitespace-nowrap">
              ${{ Number(item.cost || 0).toFixed(4) }}
            </td>

            <!-- 操作 -->
            <td class="text-right">
              <button
                type="button"
                class="text-indigo-400 hover:text-indigo-300 p-1 rounded hover:bg-indigo-500/10 transition-colors cursor-pointer"
                title="查看报文详情"
                @click="openDetail(item)"
              >
                <span class="material-symbols-outlined text-16px">info</span>
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- 分页控制栏 -->
    <div class="flex items-center justify-between text-xs text-slate-400 pt-2">
      <div>
        <span>共 {{ total }} 条记录，第 {{ page }} / {{ totalPages }} 页</span>
      </div>

      <div class="flex items-center gap-2">
        <button
          type="button"
          class="btn-secondary text-xs py-1 px-2.5 cursor-pointer disabled:opacity-40"
          :disabled="page <= 1"
          @click="changePage(page - 1)"
        >
          上一页
        </button>
        <button
          type="button"
          class="btn-secondary text-xs py-1 px-2.5 cursor-pointer disabled:opacity-40"
          :disabled="page >= totalPages"
          @click="changePage(page + 1)"
        >
          下一页
        </button>
      </div>
    </div>

    <!-- 详情弹窗 -->
    <RequestDetailModal
      :visible="detailModalVisible"
      :item="selectedItem"
      @close="detailModalVisible = false"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import {
  logsApi,
  type LogItem,
  type LogSummary,
  type ModelPerfStat,
} from '../../api/client'
import RequestDetailModal from './RequestDetailModal.vue'

const props = withDefaults(
  defineProps<{
    mode?: 'user' | 'admin'
  }>(),
  {
    mode: 'user',
  }
)

const loading = ref(false)
const autoRefresh = ref(false)
let refreshInterval: any = null

const logs = ref<LogItem[]>([])
const accounts = ref<string[]>([])
const selectedAccount = ref('all')
const selectedStatus = ref('all')
const search = ref('')
const page = ref(1)
const pageSize = ref(15)
const total = ref(0)

const summary = ref<LogSummary>({
  totalRequests: 0,
  totalCost: 0,
  inputTokens: 0,
  outputTokens: 0,
  cachedTokens: 0,
  inputCost: 0,
  outputCost: 0,
  cachedCost: 0,
  cacheHitRate: 0,
})

const inTokens = computed(() => Number(summary.value.totalInputTokens ?? summary.value.inputTokens ?? 0))
const outTokens = computed(() => Number(summary.value.totalOutputTokens ?? summary.value.outputTokens ?? 0))
const cachedTokens = computed(() => Number(summary.value.totalCachedTokens ?? summary.value.cachedTokens ?? 0))
const totalThroughputTokens = computed(() => inTokens.value + outTokens.value + cachedTokens.value)

const modelPerf = ref<ModelPerfStat[]>([])

const selectedItem = ref<LogItem | null>(null)
const detailModalVisible = ref(false)

const statusOptions = [
  { label: '全部', value: 'all' },
  { label: '成功', value: 'success' },
  { label: '异常', value: 'error' },
  { label: '命中缓存', value: 'hit' },
  { label: '未命中', value: 'miss' },
]

const totalPages = computed(() => {
  return Math.max(1, Math.ceil(total.value / pageSize.value))
})

async function loadLogs() {
  loading.value = true
  try {
    const params: any = {
      status: selectedStatus.value,
      search: search.value.trim(),
      page: page.value,
      pageSize: pageSize.value,
    }

    let res
    if (selectedAccount.value !== 'all') {
      params.account = selectedAccount.value
    }
    if (props.mode === 'admin') {
      res = await logsApi.getAdminLogs(params)
    } else {
      res = await logsApi.getUserLogs(params)
    }

    if (res) {
      logs.value = res.list || []
      let t = res.total || 0
      if (props.mode === 'user' || (selectedAccount.value && selectedAccount.value !== 'all')) {
        if (t > 150) t = 150
      }
      total.value = t
      if (res.summary) summary.value = res.summary
      if (res.modelPerf) modelPerf.value = res.modelPerf
      if (res.accounts && res.accounts.length > 0) {
        accounts.value = res.accounts
      }
    }
  } catch (err) {
    console.error('加载日志失败:', err)
  } finally {
    loading.value = false
  }
}

async function loadAccounts() {
  try {
    let list
    if (props.mode === 'admin') {
      list = await logsApi.getLogAccounts()
    } else {
      list = await logsApi.getUserLogAccounts()
    }
    if (list && list.length > 0) {
      accounts.value = list
    }
  } catch (err) {
    console.warn('获取日志账号列表失败:', err)
  }
}

function selectStatus(st: string) {
  selectedStatus.value = st
  page.value = 1
  loadLogs()
}

function onFilterChange() {
  page.value = 1
  loadLogs()
}

function changePage(p: number) {
  if (p < 1 || p > totalPages.value) return
  page.value = p
  loadLogs()
}

async function openDetail(item: LogItem) {
  selectedItem.value = item
  detailModalVisible.value = true
  // 拉取完整带有 requestBody/responseBody 的详情
  try {
    let detail
    if (props.mode === 'admin') {
      detail = await logsApi.getAdminLogDetail(item.reqId, item.id)
    } else {
      detail = await logsApi.getUserLogDetail(item.reqId, item.id)
    }
    if (detail) {
      selectedItem.value = { ...item, ...detail }
    }
  } catch (err) {
    console.warn('加载日志报文详情失败:', err)
  }
}

function formatDuration(ms?: number): string {
  if (ms === undefined || ms === null || isNaN(ms)) return '0ms'
  const val = Number(ms)
  if (val < 1000) {
    return `${Math.round(val)}ms`
  }
  const sec = (val / 1000).toFixed(2)
  const formatted = parseFloat(sec).toString()
  return `${formatted}s`
}

function formatTokenNumber(num: number): string {
  if (!num || isNaN(num)) return '0'
  if (num >= 100000000) return (num / 100000000).toFixed(2) + ' 亿'
  if (num >= 1000000) return (num / 1000000).toFixed(2) + 'M'
  if (num >= 1000) return (num / 1000).toFixed(1) + 'k'
  return num.toLocaleString()
}

function formatLogTime(t?: string): string {
  if (!t) return '-'
  try {
    let d = new Date(t)
    // 容错兜底：若传入旧版无年份格式导致 JS 引擎回退至 2001 年或非法 Date，按当前年份重构
    if (isNaN(d.getTime()) || d.getFullYear() <= 2010) {
      const match = t.match(/^(\d{1,2})[-/](\d{1,2})\s+(\d{1,2}:\d{2}(?::\d{2})?)/)
      if (match) {
        const currentYear = new Date().getFullYear()
        d = new Date(`${currentYear}/${match[1]}/${match[2]} ${match[3]}`)
      }
    }
    if (isNaN(d.getTime())) return t
    return d.toLocaleString('zh-CN', { hour12: false })
  } catch {
    return t
  }
}

onMounted(() => {
  loadLogs()
  loadAccounts()

  refreshInterval = setInterval(() => {
    if (autoRefresh.value && !loading.value) {
      loadLogs()
    }
  }, 10000)
})

onUnmounted(() => {
  if (refreshInterval) {
    clearInterval(refreshInterval)
  }
})
</script>
