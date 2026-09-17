<template>
  <div class="max-w-[1680px] mx-auto px-4 sm:px-6 lg:px-8 py-6 flex flex-col gap-6">
    <!-- 用户概览与订阅状态卡片 -->
    <div class="glass-card-glow p-6 flex flex-wrap items-center justify-between gap-4">
      <div class="flex items-center gap-4">
        <div class="w-14 h-14 rounded-2xl bg-gradient-to-tr from-indigo-600 to-pink-500 flex items-center justify-center text-white shadow-md">
          <span class="material-symbols-outlined text-32px">account_circle</span>
        </div>
        <div>
          <div class="flex items-center gap-2 flex-wrap">
            <h2 class="text-xl font-bold text-white">{{ user?.username || '加载中...' }}</h2>
            <span v-if="user?.role === 'admin'" class="badge badge-amber">管理员</span>
            <span v-if="user?.isActive" class="badge badge-emerald">已激活订阅</span>
            <span v-else class="badge badge-rose">无有效订阅</span>
            <span
              v-if="user?.plan && (!user.plan.type || user.plan.type === 'subscription')"
              :class="getTierBadgeClass(user.plan.tier)"
              class="badge text-[10px] font-mono font-bold uppercase"
            >
              {{ formatTierName(user.plan.tier) }} 会员
            </span>
          </div>
          <p class="text-xs text-slate-400 mt-1 flex items-center flex-wrap gap-1.5">
            <span>当前套餐:</span>
            <span
              v-if="user?.plan && (!user.plan.type || user.plan.type === 'subscription')"
              :class="getTierBadgeClass(user.plan.tier)"
              class="px-2 py-0.5 rounded text-[11px] font-mono font-extrabold uppercase shadow-sm shrink-0"
            >
              {{ formatTierName(user.plan.tier) }}
            </span>
            <span
              v-else-if="user?.plan?.type === 'addon'"
              class="px-2 py-0.5 rounded text-[11px] font-bold bg-amber-500/15 text-amber-300 border border-amber-500/30 shrink-0"
            >
              加油包
            </span>
            <strong class="text-white">{{ user?.plan?.name || '无激活套餐' }}</strong>
            <span v-if="user?.planExpireAt" class="text-slate-400">
              (有效期至: {{ formatTime(user?.planExpireAt) }})
            </span>
            <span v-if="user?.plan" class="text-slate-300">
              · 额度限制: <strong class="text-indigo-300">{{ formatTokenDisplay(user?.plan?.tokenLimit, true) }}</strong>
            </span>
          </p>
          <!-- 若套餐配置了 Auto 包含模型说明，在控制台顶部清晰回显 -->
          <div v-if="user?.plan?.autoModels && user.plan.autoModels.length > 0" class="flex flex-wrap items-center gap-1 mt-1.5 text-[11px] text-amber-300/90">
            <span class="material-symbols-outlined text-[13px] text-amber-400">bolt</span>
            <span>Auto包含:</span>
            <span v-for="am in user.plan.autoModels" :key="am" class="px-1.5 py-0.2 rounded text-[10px] font-mono bg-amber-500/15 text-amber-200 border border-amber-500/30">
              {{ am }}
            </span>
          </div>
        </div>
      </div>

      <div class="flex items-center gap-2.5">
        <router-link to="/orders" class="btn-secondary text-xs flex items-center gap-1.5">
          <span class="material-symbols-outlined text-16px">receipt_long</span>
          <span>我的订单</span>
        </router-link>
        <router-link to="/pricing" class="btn-primary text-xs flex items-center gap-1.5">
          <span class="material-symbols-outlined text-16px">upgrade</span>
          <span>{{ user?.isActive ? '续费或升配套餐' : '立即选购套餐' }}</span>
        </router-link>
      </div>
    </div>

    <!-- 套餐使用量监控面板 (Package Token Usage & Quota Bar) -->
    <div class="glass-card p-6 flex flex-col gap-4 border-indigo-500/20 bg-slate-900/50 relative overflow-hidden">
      <!-- 装饰背景光晕 -->
      <div class="absolute -right-20 -top-20 w-60 h-60 rounded-full bg-indigo-500/10 blur-3xl pointer-events-none"></div>

      <div class="flex items-center justify-between flex-wrap gap-2">
        <div class="flex items-center gap-2">
          <span class="w-8 h-8 rounded-lg bg-indigo-500/15 text-indigo-400 border border-indigo-500/25 flex items-center justify-center">
            <span class="material-symbols-outlined text-18px">data_usage</span>
          </span>
          <div>
            <h3 class="text-sm font-bold text-white flex items-center gap-2">
              <span>套餐算力使用量</span>
              <span class="text-11px px-2 py-0.5 rounded-full font-medium" :class="usageBadgeClass">
                {{ usageBadgeText }}
              </span>
            </h3>
            <p class="text-11px text-slate-400 mt-0.5">一眼看清当前套餐总额度消耗与各 API Key 算力调用进度</p>
          </div>
        </div>

        <!-- 刷新与操作 -->
        <div class="flex items-center gap-2">
          <button
            type="button"
            class="btn-secondary text-11px py-1 px-2.5 flex items-center gap-1 cursor-pointer"
            :disabled="refreshingUsage"
            @click="refreshAllUsage"
            title="刷新最新使用量统计"
          >
            <span class="material-symbols-outlined text-14px" :class="{ 'animate-spin': refreshingUsage }">refresh</span>
            <span>刷新用量</span>
          </button>
          <router-link to="/pricing" class="btn-primary text-11px py-1 px-3 flex items-center gap-1">
            <span class="material-symbols-outlined text-14px">upgrade</span>
            <span>升级/增配</span>
          </router-link>
        </div>
      </div>

      <!-- 核心指标统计卡片 (4列自适应) -->
      <div class="grid grid-cols-2 lg:grid-cols-4 gap-3.5">
        <!-- 0. 请求次数 -->
        <div class="p-3.5 rounded-xl bg-slate-950/60 border border-slate-800/80 flex flex-col justify-between">
          <div class="flex items-center justify-between text-slate-400 text-11px mb-1.5">
            <span>请求次数</span>
            <span class="material-symbols-outlined text-cyan-400 text-16px">query_stats</span>
          </div>
          <div class="flex items-baseline gap-1.5">
            <span class="text-xl font-mono font-extrabold text-white">{{ totalRequests.toLocaleString() }}</span>
            <span class="text-10px text-slate-400 font-sans">次</span>
          </div>
          <div class="text-[10px] text-slate-400 mt-1">
            累计调用 API 请求总量
          </div>
        </div>

        <!-- 1. 已消耗 Tokens -->
        <div class="p-3.5 rounded-xl bg-slate-950/60 border border-slate-800/80 flex flex-col justify-between">
          <div class="flex items-center justify-between text-slate-400 text-11px mb-1.5">
            <span>已消耗 Token</span>
            <span class="material-symbols-outlined text-amber-400 text-16px">trending_up</span>
          </div>
          <div class="flex items-baseline gap-1.5">
            <span class="text-xl font-mono font-extrabold text-white">{{ formatTokenNumber(totalUsedTokens) }}</span>
            <span class="text-10px text-slate-400 font-sans">Tokens</span>
          </div>
          <div class="text-[10px] text-slate-400 mt-1">
            约 {{ formatTokenDisplay(totalUsedTokens, false) }}
          </div>
        </div>

        <!-- 2. 剩余可用 Tokens -->
        <div class="p-3.5 rounded-xl bg-slate-950/60 border border-slate-800/80 flex flex-col justify-between">
          <div class="flex items-center justify-between text-slate-400 text-11px mb-1.5">
            <span>剩余可用 Token</span>
            <span class="material-symbols-outlined text-emerald-400 text-16px">check_circle</span>
          </div>
          <div class="flex items-baseline gap-1.5">
            <template v-if="!hasActivePlan">
              <span class="text-xl font-mono font-extrabold text-slate-400">0</span>
              <span class="text-10px text-slate-400 font-sans">Tokens</span>
            </template>
            <template v-else-if="isUnlimitedPlan">
              <span class="text-xl font-mono font-extrabold text-emerald-400">不限额度</span>
            </template>
            <template v-else>
              <span class="text-xl font-mono font-extrabold" :class="remainingTokensColor">
                {{ formatTokenNumber(remainingTokens) }}
              </span>
              <span class="text-10px text-slate-400 font-sans">Tokens</span>
            </template>
          </div>
          <div class="text-[10px] text-slate-400 mt-1">
            <template v-if="!hasActivePlan">未激活套餐，暂无可用算力</template>
            <template v-else-if="isUnlimitedPlan">无限配额，畅享全系模型</template>
            <template v-else>约 {{ formatTokenDisplay(remainingTokens, false) }}</template>
          </div>
        </div>

        <!-- 3. 套餐总配额上限 -->
        <div class="p-3.5 rounded-xl bg-slate-950/60 border border-slate-800/80 flex flex-col justify-between">
          <div class="flex items-center justify-between text-slate-400 text-11px mb-1.5">
            <span>套餐总额度</span>
            <span class="material-symbols-outlined text-indigo-400 text-16px">toll</span>
          </div>
          <div class="flex items-baseline gap-1.5">
            <template v-if="!hasActivePlan">
              <span class="text-xl font-mono font-extrabold text-slate-400">0</span>
              <span class="text-10px text-slate-400 font-sans">Tokens</span>
            </template>
            <template v-else-if="isUnlimitedPlan">
              <span class="text-xl font-mono font-extrabold text-indigo-300">无上限</span>
            </template>
            <template v-else>
              <span class="text-xl font-mono font-extrabold text-indigo-300">
                {{ formatTokenNumber(planTokenLimit) }}
              </span>
              <span class="text-10px text-slate-400 font-sans">Tokens</span>
            </template>
          </div>
          <div class="text-[10px] text-slate-400 mt-1">
            <template v-if="!hasActivePlan && extraTokens <= 0">暂无激活套餐</template>
            <template v-else-if="isUnlimitedPlan">不设配额上限</template>
            <template v-else>
              <span>约 {{ formatTokenDisplay(planTokenLimit, true) }}</span>
              <span v-if="extraTokens > 0" class="text-amber-300 ml-1">(含加油包: {{ formatTokenDisplay(extraTokens, false) }})</span>
            </template>
          </div>
        </div>
      </div>

      <!-- 动态图形化进度条 -->
      <div class="p-3.5 bg-slate-950/40 rounded-xl border border-slate-800/70 flex flex-col gap-2">
        <div class="flex items-center justify-between text-xs font-semibold">
          <span class="text-slate-300 flex items-center gap-1.5">
            <span class="material-symbols-outlined text-14px text-indigo-400">speed</span>
            <span>算力额度消耗进度</span>
          </span>
          <span class="font-mono" :class="usagePercentColor">
            <template v-if="!hasActivePlan">未激活</template>
            <template v-else-if="isUnlimitedPlan">无限制畅用</template>
            <template v-else>{{ usagePercent.toFixed(1) }}%</template>
          </span>
        </div>

        <!-- 进度条背景槽 -->
        <div class="w-full h-3 bg-slate-800/80 rounded-full overflow-hidden p-0.5 border border-slate-700/50">
          <div
            v-if="!hasActivePlan"
            class="h-full rounded-full bg-slate-700/40"
            style="width: 0%"
          ></div>
          <div
            v-else-if="isUnlimitedPlan"
            class="h-full rounded-full bg-gradient-to-r from-emerald-500 via-teal-400 to-cyan-500 w-full animate-pulse"
          ></div>
          <div
            v-else
            class="h-full rounded-full transition-all duration-500 shadow-sm"
            :class="progressBarClass"
            :style="{ width: `${Math.min(100, Math.max(0, usagePercent))}%` }"
          ></div>
        </div>

        <div class="flex items-center justify-between text-[10px] text-slate-400 font-mono gap-2 flex-wrap">
          <span>已消耗: {{ formatTokenNumber(totalUsedTokens) }} Tokens (约 {{ formatTokenDisplay(totalUsedTokens, false) }})</span>
          <span v-if="!hasActivePlan">未激活</span>
          <span v-else-if="isUnlimitedPlan">无限制</span>
          <span v-else>总限额: {{ formatTokenDisplay(planTokenLimit, true) }}</span>
        </div>
      </div>
    </div>


    <!-- API Key 凭证管理卡片 -->
    <div class="glass-card p-6">
      <div class="flex items-center justify-between mb-4 pb-3 border-b border-slate-800">
        <div class="flex items-center gap-3">
          <div class="flex items-center gap-2">
            <span class="material-symbols-outlined text-indigo-400 text-20px">key</span>
            <h3 class="text-sm font-bold text-white">API 调用密钥 (API Keys)</h3>
          </div>
          <!-- 复制状态轻量提示条 -->
          <transition name="fade">
            <div v-if="copyToast" class="flex items-center gap-1.5 px-3 py-1 rounded-full bg-emerald-500/20 text-emerald-300 border border-emerald-500/30 text-11px font-medium shadow-sm">
              <span class="material-symbols-outlined text-14px">check_circle</span>
              <span>{{ copyToast }}</span>
            </div>
          </transition>
        </div>
        <button type="button" class="btn-primary text-xs cursor-pointer" @click="openCreateKeyModal">
          <span class="material-symbols-outlined text-16px">add</span>
          <span>新建 API Key</span>
        </button>
      </div>

      <!-- Key 列表表格 -->
      <div v-if="loadingKeys" class="p-6">
        <LoadingSpinner text="正在加载 API 密钥列表..." />
      </div>
      <div v-else-if="keys.length === 0" class="p-12 text-center text-slate-400 text-xs">
        <span class="material-symbols-outlined text-32px text-slate-500 mb-2 block">vpn_key_off</span>
        暂无活跃 API 密钥，请点击右上角【新建 API Key】生成
      </div>
      <div v-else class="overflow-x-auto">
        <table class="table-dark">
          <thead>
            <tr>
              <th>密钥名称</th>
              <th>密钥内容 (Key)</th>
              <th>授权模型范围</th>
              <th>Token 限额</th>
              <th>已用量 (Used)</th>
              <th>限频 (RPM)</th>
              <th>创建时间</th>
              <th class="text-right whitespace-nowrap w-24">操作</th>
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
                    :title="m === 'auto' && user?.plan?.autoModels?.length ? `Auto实际包含: ${user.plan.autoModels.join(', ')}` : m"
                  >
                    {{ m === 'auto' ? '⚡ 支持 auto 模型' : m }}
                  </span>
                </div>
              </td>
              <td class="font-mono text-xs">
                <span v-if="!k.limitTokens || k.limitTokens <= 0" class="badge badge-emerald">不限额度</span>
                <span v-else class="text-indigo-300">{{ formatTokenDisplay(k.limitTokens) }}</span>
              </td>
              <td class="font-mono text-xs">
                <div class="flex flex-col gap-1">
                  <div class="flex items-center gap-1.5">
                    <span class="text-white font-semibold">{{ formatTokenNumber(k.usedTokens || 0) }}</span>
                    <span v-if="k.limitTokens > 0" class="text-[10px] text-slate-400">
                      ({{ (((k.usedTokens || 0) / k.limitTokens) * 100).toFixed(1) }}%)
                    </span>
                  </div>
                  <!-- 微型进度条 -->
                  <div v-if="k.limitTokens > 0" class="w-20 h-1.5 bg-slate-800 rounded-full overflow-hidden">
                    <div
                      class="h-full rounded-full transition-all duration-300"
                      :class="((k.usedTokens || 0) / k.limitTokens) >= 0.9 ? 'bg-rose-500' : ((k.usedTokens || 0) / k.limitTokens) >= 0.7 ? 'bg-amber-400' : 'bg-indigo-400'"
                      :style="{ width: `${Math.min(100, ((k.usedTokens || 0) / k.limitTokens) * 100)}%` }"
                    ></div>
                  </div>
                </div>
              </td>
              <td><span class="text-xs font-mono text-slate-300">{{ k.rateLimit }}</span></td>
              <td class="text-xs text-slate-400">{{ formatTime(k.createdAt) }}</td>
              <td class="text-right whitespace-nowrap">
                <button
                  type="button"
                  :disabled="deletingKeyId === k.id"
                  class="btn-danger cursor-pointer inline-flex items-center gap-1 whitespace-nowrap shrink-0 disabled:opacity-50 text-xs py-1 px-2.5"
                  @click="handleDeleteKey(k.id)"
                >
                  <span v-if="deletingKeyId === k.id" class="material-symbols-outlined text-14px animate-spin">progress_activity</span>
                  <span v-else class="material-symbols-outlined text-14px">delete</span>
                  <span>{{ deletingKeyId === k.id ? '删除中...' : '删除' }}</span>
                </button>
              </td>
            </tr>
            <tr v-if="keys.length === 0">
              <td colspan="8" class="text-center py-8 text-slate-500 text-xs">
                暂未创建 API 密钥，请点击右上角新建以开始调用。
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- 快速接入客户端指导卡片 (支持 Linux/macOS, Windows PowerShell, Windows CMD 终端环境切换) -->
    <QuickAccessPanel
      :openai-base-url="openaiBaseUrl"
      :anthropic-base-url="anthropicBaseUrl"
      :api-key="activeApiKey"
      :has-keys="keys.length > 0"
    />

    <!-- 请求命中模型日志监控卡片 (仅管理员角色可见) -->
    <div v-if="isAdmin" class="glass-card p-6 flex flex-col gap-4">
      <RequestLogsPanel mode="user" />
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

          <div>
            <div class="flex items-center justify-between mb-1.5">
              <label class="block text-xs text-slate-300 font-medium">授权模型范围 (Allowed Models)</label>
              <div class="flex items-center gap-2 text-[11px]">
                <button
                  type="button"
                  class="text-indigo-400 hover:text-indigo-300 cursor-pointer"
                  @click="selectAllModels"
                >
                  全选
                </button>
                <span class="text-slate-600">|</span>
                <button
                  type="button"
                  class="text-slate-400 hover:text-slate-300 cursor-pointer"
                  @click="clearAllModels"
                >
                  清空
                </button>
              </div>
            </div>
            <ModelSearchSelect
              :multiple="true"
              :model-ids="newKeyForm.allowedModels"
              :options="selectableModels"
              :allow-custom="isAdmin"
              placeholder="搜索或勾选授权模型..."
              @update:model-ids="(val) => newKeyForm.allowedModels = val"
            />
            <p class="text-[10px] text-slate-500 mt-1">
              客户端使用此 Key 获取模型列表时，将仅显示所选授权模型
            </p>
          </div>
          <div class="flex items-center justify-end gap-3 mt-2">
            <button type="button" class="btn-secondary text-xs" :disabled="creatingKey" @click="showCreateKeyModal = false">取消</button>
            <button type="button" :disabled="creatingKey" class="btn-primary text-xs flex items-center gap-1.5 disabled:opacity-60" @click="handleCreateKey">
              <span v-if="creatingKey" class="material-symbols-outlined text-14px animate-spin">progress_activity</span>
              <span>{{ creatingKey ? '正在生成...' : '确认生成' }}</span>
            </button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import LoadingSpinner from '../components/common/LoadingSpinner.vue'
import ModelSearchSelect from '../components/common/ModelSearchSelect.vue'
import QuickAccessPanel from '../components/dashboard/QuickAccessPanel.vue'
import RequestLogsPanel from '../components/request_logs/RequestLogsPanel.vue'
import { useUserStore, useSystemStore } from '../stores'

const userStore = useUserStore()
const systemStore = useSystemStore()

const user = computed(() => userStore.user)
const isAdmin = computed(() => userStore.isAdmin)
const keys = computed(() => userStore.keys)
const loadingKeys = computed(() => userStore.loadingKeys)
const totalRequests = computed(() => userStore.totalRequests)
const systemConfig = computed(() => systemStore.config)
const creatingKey = ref(false)
const deletingKeyId = ref<number | null>(null)
const copyToast = ref('')
let toastTimer: any = null

const savingAuto = ref(false)
const showCreateKeyModal = ref(false)

const newKeyForm = reactive({
  name: '',
  allowedModels: [] as string[],
})

const selectableModels = computed(() => userStore.selectableModels)

function openCreateKeyModal() {
  newKeyForm.name = ''
  newKeyForm.allowedModels = [...selectableModels.value]
  showCreateKeyModal.value = true
}

function selectAllModels() {
  newKeyForm.allowedModels = [...selectableModels.value]
}

function clearAllModels() {
  newKeyForm.allowedModels = []
}

const apiBaseUrl = computed(() => systemStore.apiBaseUrl)
const openaiBaseUrl = computed(() => systemStore.openaiBaseUrl)
const anthropicBaseUrl = computed(() => systemStore.anthropicBaseUrl)

const activeApiKey = computed(() => {
  if (keys.value && keys.value.length > 0) {
    return keys.value[0].key || 'sk-ant-xxxxxxxx'
  }
  return 'sk-ant-xxxxxxxx'
})

async function handleCreateKey() {
  if (newKeyForm.allowedModels.length === 0) {
    alert('请至少选择一个授权模型')
    return
  }
  creatingKey.value = true
  try {
    await userStore.createKey({
      name: newKeyForm.name || '默认密钥',
      allowedModels: newKeyForm.allowedModels,
    })
    showCreateKeyModal.value = false
    newKeyForm.name = ''
    newKeyForm.allowedModels = []
  } catch (err: any) {
    alert(err.message || '创建密钥失败')
  } finally {
    creatingKey.value = false
  }
}

async function handleDeleteKey(id: number) {
  if (!confirm('确定要删除此 API Key 吗？相关客户端将无法继续调用。')) return
  deletingKeyId.value = id
  try {
    await userStore.deleteKey(id)
  } catch (err: any) {
    alert(err.message || '删除失败')
  } finally {
    deletingKeyId.value = null
  }
}

function copyToClipboard(text: string) {
  navigator.clipboard.writeText(text)
  copyToast.value = '密钥已复制到剪贴板'
  if (toastTimer) clearTimeout(toastTimer)
  toastTimer = setTimeout(() => {
    copyToast.value = ''
  }, 2500)
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

function formatTokenDisplay(val?: number, isLimit = false): string {
  if (!val || val <= 0) return isLimit ? '不限额度' : '0 Tokens'
  if (val >= 100000000) return (val / 100000000).toFixed(val % 100000000 === 0 ? 0 : 2) + ' 亿 Tokens'
  if (val >= 10000) return (val / 10000).toFixed(val % 10000 === 0 ? 0 : 1) + ' 万 Tokens'
  if (val >= 1000000) return (val / 1000000).toFixed(1) + 'M Tokens'
  if (val >= 1000) return (val / 1000).toFixed(0) + 'K Tokens'
  return `${val.toLocaleString()} Tokens`
}

function formatTierName(tier?: string): string {
  const t = (tier || 'pro').toLowerCase().trim()
  if (t === 'max++') return 'MAX++'
  if (t === 'max+') return 'MAX+'
  if (t === 'max') return 'MAX'
  return 'Pro'
}

function getTierBadgeClass(tier?: string): string {
  const t = (tier || 'pro').toLowerCase().trim()
  if (t === 'max++') return 'bg-amber-500/20 text-amber-300 border border-amber-500/40'
  if (t === 'max+') return 'bg-purple-500/20 text-purple-300 border border-purple-500/40'
  if (t === 'max') return 'bg-indigo-500/20 text-indigo-300 border border-indigo-500/40'
  return 'bg-blue-500/20 text-blue-300 border border-blue-500/40'
}

const refreshingUsage = ref(false)

const totalUsedTokens = computed(() => userStore.totalUsedTokens)
const extraTokens = computed(() => userStore.extraTokens)
const hasActivePlan = computed(() => userStore.hasActivePlan)
const planTokenLimit = computed(() => userStore.planTokenLimit)
const isUnlimitedPlan = computed(() => userStore.isUnlimitedPlan)
const remainingTokens = computed(() => userStore.remainingTokens)
const usagePercent = computed(() => userStore.usagePercent)
const usageBadgeText = computed(() => userStore.usageBadgeText)
const usageBadgeClass = computed(() => userStore.usageBadgeClass)
const remainingTokensColor = computed(() => userStore.remainingTokensColor)
const usagePercentColor = computed(() => userStore.usagePercentColor)
const progressBarClass = computed(() => userStore.progressBarClass)

function formatTokenNumber(val: number): string {
  if (!val || isNaN(val)) return '0'
  return Math.round(val).toLocaleString()
}

async function refreshAllUsage() {
  refreshingUsage.value = true
  try {
    await userStore.refreshAll()
  } finally {
    setTimeout(() => {
      refreshingUsage.value = false
    }, 400)
  }
}

onMounted(() => {
  userStore.fetchUserProfile()
  userStore.fetchKeys()
  userStore.fetchRequestCount()
  systemStore.fetchSystemConfig()
})
</script>
