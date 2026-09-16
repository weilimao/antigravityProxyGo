<template>
  <div
    class="bg-slate-900/80 border rounded-2xl p-4 flex flex-col justify-between transition-all hover:border-slate-700 shadow-lg relative group"
    :class="[
      selected ? 'border-indigo-500/70 bg-indigo-950/20' : 'border-slate-800/80',
      !acc.enabled ? 'opacity-60' : ''
    ]"
  >
    <!-- 顶部信息行: 复选框 + 邮箱 + 状态 -->
    <div>
      <div class="flex items-start justify-between gap-2 mb-2">
        <div class="flex items-start gap-2 overflow-hidden">
          <input
            type="checkbox"
            :value="acc.id"
            :checked="selected"
            class="mt-1 rounded bg-slate-800 border-slate-700 text-indigo-600 focus:ring-0 cursor-pointer"
            @change="$emit('toggleSelect', acc.id)"
          />
          <div class="overflow-hidden">
            <div class="font-bold text-white text-xs truncate" :title="acc.email">
              {{ acc.email || acc.id }}
            </div>
            <div class="flex items-center gap-1.5 mt-1">
              <span class="px-1.5 py-0.2 rounded text-[10px] font-bold bg-amber-500/10 text-amber-400 border border-amber-500/20">
                {{ acc.groupName || acc.provider.toUpperCase() }}
              </span>
              <span v-if="acc.addedAt" class="text-[10px] text-slate-500 truncate">
                {{ formatAddedAt(acc.addedAt) }}
              </span>
            </div>
          </div>
        </div>

        <!-- 状态 Badge -->
        <span
          v-if="acc.cooldownUntil && acc.cooldownUntil > Date.now()"
          class="px-1.5 py-0.5 rounded text-[10px] font-semibold bg-sky-500/10 text-sky-400 border border-sky-500/20 whitespace-nowrap"
        >
          冷静中
        </span>
        <span
          v-else-if="acc.enabled"
          class="px-1.5 py-0.5 rounded text-[10px] font-semibold bg-emerald-500/10 text-emerald-400 border border-emerald-500/20 whitespace-nowrap"
        >
          活跃
        </span>
        <span
          v-else
          class="px-1.5 py-0.5 rounded text-[10px] font-semibold bg-slate-800 text-slate-500 whitespace-nowrap"
        >
          已停用
        </span>
      </div>

      <!-- 配额/模型说明栏 -->
      <div class="bg-slate-800/40 border border-slate-800 rounded-xl p-2.5 mt-2.5 text-[11px] space-y-1">
        <div class="text-slate-400 flex items-center justify-between">
          <span>配额与凭据:</span>
          <span class="font-mono text-slate-300">{{ acc.maskedKey || '免 Key / 内部凭证' }}</span>
        </div>
        <div v-if="acc.defaultModel || acc.tier" class="text-slate-400 flex items-center justify-between">
          <span>关联模型/Tier:</span>
          <span class="text-indigo-300 font-medium truncate max-w-[150px]">{{ acc.defaultModel || acc.tier }}</span>
        </div>
      </div>
    </div>

    <!-- 底部操作与开关行 -->
    <div class="flex items-center justify-between pt-3 mt-3 border-t border-slate-800/80 text-xs">
      <!-- Toggle 启停 -->
      <div
        class="flex items-center gap-2 cursor-pointer select-none group/toggle"
        @click="$emit('toggleStatus', acc)"
      >
        <div
          class="w-9 h-5 rounded-full transition-colors duration-200 relative flex items-center px-0.5"
          :class="acc.enabled ? 'bg-indigo-600' : 'bg-slate-700'"
        >
          <div
            class="w-4 h-4 bg-white rounded-full shadow-md transform transition-transform duration-200 ease-in-out"
            :class="acc.enabled ? 'translate-x-4' : 'translate-x-0'"
          ></div>
        </div>
        <span
          class="text-[11px] font-bold tracking-wide transition-colors"
          :class="acc.enabled ? 'text-emerald-400' : 'text-slate-500'"
        >
          {{ acc.enabled ? '已启用' : '已停用' }}
        </span>
      </div>

      <!-- 操作按钮组 -->
      <div class="flex items-center gap-1">
        <button
          type="button"
          class="p-1.5 rounded-lg text-slate-400 hover:text-indigo-400 hover:bg-slate-800 transition-colors"
          title="导出此账号"
          @click="$emit('export', acc)"
        >
          <span class="material-symbols-outlined text-16px">download</span>
        </button>
        <button
          type="button"
          class="p-1.5 rounded-lg text-slate-400 hover:text-indigo-400 hover:bg-slate-800 transition-colors"
          title="编辑账号"
          @click="$emit('edit', acc)"
        >
          <span class="material-symbols-outlined text-16px">edit</span>
        </button>
        <button
          type="button"
          class="p-1.5 rounded-lg text-slate-400 hover:text-rose-400 hover:bg-slate-800 transition-colors"
          title="删除账号"
          @click="$emit('delete', acc)"
        >
          <span class="material-symbols-outlined text-16px">delete</span>
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import type { AccountItem } from '../../../api/client'

defineProps<{
  acc: AccountItem
  selected: boolean
}>()

defineEmits<{
  (e: 'toggleSelect', id: string): void
  (e: 'toggleStatus', acc: AccountItem): void
  (e: 'export', acc: AccountItem): void
  (e: 'edit', acc: AccountItem): void
  (e: 'delete', acc: AccountItem): void
}>()

function formatAddedAt(str: string): string {
  if (!str) return ''
  try {
    const d = new Date(str)
    return `${d.getFullYear()}/${d.getMonth() + 1}/${d.getDate()} ${d.getHours().toString().padStart(2, '0')}:${d.getMinutes().toString().padStart(2, '0')}`
  } catch {
    return str
  }
}
</script>
