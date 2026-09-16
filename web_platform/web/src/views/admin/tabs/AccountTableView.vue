<template>
  <div class="overflow-x-auto bg-slate-900/80 border border-slate-800/80 rounded-2xl shadow-xl">
    <table class="w-full text-left text-xs text-slate-300">
      <thead class="bg-slate-800/50 text-slate-400 font-semibold border-b border-slate-800">
        <tr>
          <th class="p-3 w-10">
            <input
              type="checkbox"
              :checked="allPageSelected"
              class="rounded bg-slate-800 border-slate-700 text-indigo-600 focus:ring-0 cursor-pointer"
              @change="$emit('toggleSelectAllPage')"
            />
          </th>
          <th class="p-3">账号标识 / 邮箱</th>
          <th class="p-3">通道 / 渠道组</th>
          <th class="p-3">API Key 凭证</th>
          <th class="p-3">默认模型 / 端点</th>
          <th class="p-3">状态</th>
          <th class="p-3">启停</th>
          <th class="p-3 text-right">操作</th>
        </tr>
      </thead>
      <tbody class="divide-y divide-slate-800">
        <tr
          v-for="acc in accounts"
          :key="acc.id"
          class="hover:bg-slate-800/40 transition-colors"
          :class="[
            selectedAccountIds.includes(acc.id) ? 'bg-indigo-950/20' : '',
            !acc.enabled ? 'opacity-60' : ''
          ]"
        >
          <td class="p-3">
            <input
              type="checkbox"
              :value="acc.id"
              :checked="selectedAccountIds.includes(acc.id)"
              class="rounded bg-slate-800 border-slate-700 text-indigo-600 focus:ring-0 cursor-pointer"
              @change="$emit('toggleSelectAccount', acc.id)"
            />
          </td>
          <td class="p-3 font-bold text-white">
            {{ acc.email || acc.id }}
            <div v-if="acc.addedAt" class="text-[10px] text-slate-500 font-normal">
              {{ formatAddedAt(acc.addedAt) }}
            </div>
          </td>
          <td class="p-3">
            <span class="px-2 py-0.5 rounded text-[11px] font-bold bg-slate-800 text-slate-300 border border-slate-700">
              {{ acc.groupName || acc.provider.toUpperCase() }}
            </span>
          </td>
          <td class="p-3 font-mono text-slate-400">
            {{ acc.maskedKey || '—' }}
          </td>
          <td class="p-3">
            <div class="text-indigo-300 font-medium truncate max-w-[200px]">{{ acc.defaultModel || '默认路由' }}</div>
            <div class="text-[10px] text-slate-500 font-mono truncate max-w-[200px]">{{ acc.baseUrl || '官方默认端点' }}</div>
          </td>
          <td class="p-3">
            <span
              v-if="acc.cooldownUntil && acc.cooldownUntil > Date.now()"
              class="px-2 py-0.5 rounded text-[10px] font-semibold bg-sky-500/10 text-sky-400 border border-sky-500/20"
            >
              冷静中
            </span>
            <span
              v-else-if="acc.enabled"
              class="px-2 py-0.5 rounded text-[10px] font-semibold bg-emerald-500/10 text-emerald-400 border border-emerald-500/20"
            >
              活跃
            </span>
            <span
              v-else
              class="px-2 py-0.5 rounded text-[10px] font-semibold bg-slate-800 text-slate-500"
            >
              已停用
            </span>
          </td>
          <td class="p-3">
            <div
              class="inline-flex items-center gap-2 cursor-pointer select-none"
              @click="$emit('toggleAccount', acc)"
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
              <span class="text-[11px] font-bold" :class="acc.enabled ? 'text-emerald-400' : 'text-slate-500'">
                {{ acc.enabled ? '已启用' : '已停用' }}
              </span>
            </div>
          </td>
          <td class="p-3 text-right">
            <div class="flex items-center justify-end gap-1.5">
              <button
                type="button"
                class="p-1 rounded text-slate-400 hover:text-indigo-400 transition-colors"
                title="导出此账号"
                @click="$emit('exportAccount', acc)"
              >
                <span class="material-symbols-outlined text-16px">download</span>
              </button>
              <button
                type="button"
                class="p-1 rounded text-slate-400 hover:text-indigo-400 transition-colors"
                title="编辑"
                @click="$emit('editAccount', acc)"
              >
                <span class="material-symbols-outlined text-16px">edit</span>
              </button>
              <button
                type="button"
                class="p-1 rounded text-slate-400 hover:text-rose-400 transition-colors"
                title="删除"
                @click="$emit('deleteAccount', acc)"
              >
                <span class="material-symbols-outlined text-16px">delete</span>
              </button>
            </div>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<script setup lang="ts">
import type { AccountItem } from '../../../api/client'

defineProps<{
  accounts: AccountItem[]
  selectedAccountIds: string[]
  allPageSelected: boolean
}>()

defineEmits<{
  (e: 'toggleSelectAllPage'): void
  (e: 'toggleSelectAccount', id: string): void
  (e: 'toggleAccount', acc: AccountItem): void
  (e: 'exportAccount', acc: AccountItem): void
  (e: 'editAccount', acc: AccountItem): void
  (e: 'deleteAccount', acc: AccountItem): void
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
