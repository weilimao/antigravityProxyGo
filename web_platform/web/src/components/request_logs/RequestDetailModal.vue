<template>
  <div v-if="visible" class="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/70 backdrop-blur-sm animate-fade-in">
    <div class="bg-slate-900 border border-slate-700/80 rounded-2xl w-full max-w-4xl max-h-[90vh] flex flex-col shadow-2xl overflow-hidden">
      <!-- 弹窗 Header -->
      <div class="flex items-center justify-between px-6 py-4 border-b border-slate-800 bg-slate-950/40">
        <div class="flex items-center gap-3">
          <span class="w-8 h-8 rounded-lg bg-indigo-500/20 text-indigo-400 border border-indigo-500/30 flex items-center justify-center">
            <span class="material-symbols-outlined text-18px">receipt_long</span>
          </span>
          <div>
            <h3 class="text-base font-bold text-white flex items-center gap-2">
              <span>请求报文详情</span>
              <span class="text-xs font-mono px-2 py-0.5 rounded-full" :class="item?.statusCode === 200 ? 'bg-emerald-500/20 text-emerald-300 border border-emerald-500/30' : 'bg-rose-500/20 text-rose-300 border border-rose-500/30'">
                HTTP {{ item?.statusCode || 200 }}
              </span>
            </h3>
            <p class="text-11px text-slate-400 font-mono mt-0.5">{{ item?.reqId }}</p>
          </div>
        </div>

        <button
          type="button"
          class="text-slate-400 hover:text-white p-1 rounded-lg hover:bg-slate-800 transition-colors cursor-pointer"
          @click="emit('close')"
        >
          <span class="material-symbols-outlined text-20px">close</span>
        </button>
      </div>

      <!-- 弹窗 Body (可滚动) -->
      <div class="p-6 overflow-y-auto space-y-5 text-xs text-slate-300">
        <!-- 基础元数据网格 -->
        <div class="grid grid-cols-2 sm:grid-cols-4 gap-3 bg-slate-950/50 p-3.5 rounded-xl border border-slate-800/80">
          <div>
            <span class="text-slate-500 text-10px block">调用账号</span>
            <span class="text-white font-medium font-mono">{{ item?.account || '-' }}</span>
          </div>
          <div>
            <span class="text-slate-500 text-10px block">命中模型</span>
            <span class="text-indigo-300 font-mono font-bold">{{ item?.model || '-' }}</span>
          </div>
          <div>
            <span class="text-slate-500 text-10px block">响应耗时 / TTFT</span>
            <span class="text-white font-mono">{{ formatDuration(item?.durationMs) }} / {{ formatDuration(item?.firstByteMs) }}</span>
          </div>
          <div>
            <span class="text-slate-500 text-10px block">总 Tokens / 成本</span>
            <span class="text-emerald-400 font-mono font-semibold">
              {{ (item?.inTokens || 0) + (item?.outTokens || 0) }} / ${{ Number(item?.cost || 0).toFixed(4) }}
            </span>
          </div>
        </div>

        <!-- Tab 切换查看不同报文段 -->
        <div class="flex items-center gap-2 border-b border-slate-800 pb-2 text-xs">
          <button
            type="button"
            class="px-3 py-1.5 rounded-lg font-medium transition-colors cursor-pointer"
            :class="activeSection === 'request' ? 'bg-indigo-600 text-white font-semibold' : 'text-slate-400 hover:text-white hover:bg-slate-800'"
            @click="activeSection = 'request'"
          >
            请求报文 (Request)
          </button>
          <button
            type="button"
            class="px-3 py-1.5 rounded-lg font-medium transition-colors cursor-pointer"
            :class="activeSection === 'response' ? 'bg-indigo-600 text-white font-semibold' : 'text-slate-400 hover:text-white hover:bg-slate-800'"
            @click="activeSection = 'response'"
          >
            响应报文 (Response)
          </button>
          <button
            type="button"
            class="px-3 py-1.5 rounded-lg font-medium transition-colors cursor-pointer"
            :class="activeSection === 'headers' ? 'bg-indigo-600 text-white font-semibold' : 'text-slate-400 hover:text-white hover:bg-slate-800'"
            @click="activeSection = 'headers'"
          >
            协议 Headers
          </button>
        </div>

        <!-- 1. 请求报文 -->
        <div v-if="activeSection === 'request'" class="space-y-2">
          <div class="flex items-center justify-between">
            <span class="text-slate-400 text-11px font-mono">Payload Body</span>
            <button
              v-if="item?.requestBody"
              type="button"
              class="btn-secondary text-[10px] py-1 px-2.5 flex items-center gap-1 cursor-pointer"
              @click="copyContent(formatJson(item?.requestBody))"
            >
              <span class="material-symbols-outlined text-12px">content_copy</span>
              <span>复制请求 Body</span>
            </button>
          </div>
          <pre v-if="item?.requestBody" class="bg-slate-950 p-4 rounded-xl border border-slate-800 text-slate-300 font-mono text-11px leading-relaxed overflow-x-auto max-h-96 whitespace-pre-wrap select-all">{{ formatJson(item?.requestBody) }}</pre>
          <div v-else class="p-6 rounded-xl bg-slate-950/60 border border-slate-800/80 text-center text-slate-400 flex flex-col items-center justify-center gap-2">
            <span class="material-symbols-outlined text-28px text-slate-500">inventory_2</span>
            <p class="text-xs">该请求未存储请求体报文</p>
            <p class="text-[11px] text-slate-500">（MySQL 模式/生产环境已开启极简元数据存储，不保留请求与响应体原文以节约磁盘空间）</p>
          </div>
        </div>

        <!-- 2. 响应报文 -->
        <div v-else-if="activeSection === 'response'" class="space-y-2">
          <div class="flex items-center justify-between">
            <span class="text-slate-400 text-11px font-mono">Response Body</span>
            <button
              v-if="item?.responseBody"
              type="button"
              class="btn-secondary text-[10px] py-1 px-2.5 flex items-center gap-1 cursor-pointer"
              @click="copyContent(formatJson(item?.responseBody))"
            >
              <span class="material-symbols-outlined text-12px">content_copy</span>
              <span>复制响应 Body</span>
            </button>
          </div>
          <pre v-if="item?.responseBody" class="bg-slate-950 p-4 rounded-xl border border-slate-800 text-slate-300 font-mono text-11px leading-relaxed overflow-x-auto max-h-96 whitespace-pre-wrap select-all">{{ formatJson(item?.responseBody) }}</pre>
          <div v-else class="p-6 rounded-xl bg-slate-950/60 border border-slate-800/80 text-center text-slate-400 flex flex-col items-center justify-center gap-2">
            <span class="material-symbols-outlined text-28px text-slate-500">inventory_2</span>
            <p class="text-xs">该请求未存储响应体报文</p>
            <p class="text-[11px] text-slate-500">（MySQL 模式/生产环境已开启极简元数据存储，不保留请求与响应体原文以节约磁盘空间）</p>
          </div>
        </div>

        <!-- 3. 协议 Headers -->
        <div v-else-if="activeSection === 'headers'" class="space-y-4">
          <div>
            <div class="flex items-center justify-between mb-1">
              <span class="text-slate-400 text-11px">Request Headers</span>
              <button
                type="button"
                class="btn-secondary text-[10px] py-1 px-2 flex items-center gap-1 cursor-pointer"
                @click="copyContent(formatJson(item?.requestHeaders))"
              >
                <span class="material-symbols-outlined text-12px">content_copy</span>
                <span>复制</span>
              </button>
            </div>
            <pre class="bg-slate-950 p-3.5 rounded-xl border border-slate-800 text-slate-300 font-mono text-11px leading-relaxed overflow-x-auto max-h-48 select-all">{{ formatJson(item?.requestHeaders) }}</pre>
          </div>

          <div>
            <div class="flex items-center justify-between mb-1">
              <span class="text-slate-400 text-11px">Response Headers</span>
              <button
                type="button"
                class="btn-secondary text-[10px] py-1 px-2 flex items-center gap-1 cursor-pointer"
                @click="copyContent(formatJson(item?.responseHeaders))"
              >
                <span class="material-symbols-outlined text-12px">content_copy</span>
                <span>复制</span>
              </button>
            </div>
            <pre class="bg-slate-950 p-3.5 rounded-xl border border-slate-800 text-slate-300 font-mono text-11px leading-relaxed overflow-x-auto max-h-48 select-all">{{ formatJson(item?.responseHeaders) }}</pre>
          </div>
        </div>

        <!-- 复制成功反馈 Toast -->
        <div v-if="copiedMsg" class="text-center text-xs text-emerald-400 bg-emerald-500/10 border border-emerald-500/20 py-1.5 rounded-lg animate-fade-in">
          {{ copiedMsg }}
        </div>
      </div>

      <!-- 弹窗 Footer -->
      <div class="flex items-center justify-end px-6 py-3 border-t border-slate-800 bg-slate-950/40">
        <button
          type="button"
          class="btn-secondary text-xs"
          @click="emit('close')"
        >
          关闭
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import type { LogItem } from '../../api/client'

const props = defineProps<{
  visible: boolean
  item: LogItem | null
}>()

const emit = defineEmits<{
  (e: 'close'): void
}>()

const activeSection = ref<'request' | 'response' | 'headers'>('request')
const copiedMsg = ref('')
let timer: any = null

function formatJson(raw?: string): string {
  if (!raw || !raw.trim()) return '(空)'
  try {
    const parsed = JSON.parse(raw)
    return JSON.stringify(parsed, null, 2)
  } catch {
    return raw
  }
}

function copyContent(text: string) {
  if (!text || text === '(空)') return
  navigator.clipboard.writeText(text)
  copiedMsg.value = '内容已复制到剪贴板'
  if (timer) clearTimeout(timer)
  timer = setTimeout(() => {
    copiedMsg.value = ''
  }, 2000)
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
</script>
