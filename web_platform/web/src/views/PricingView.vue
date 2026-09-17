<template>
  <div class="max-w-[1680px] mx-auto px-4 sm:px-6 lg:px-8 pt-4 pb-12">
    <!-- 顶部横向流线型工具栏 (左翼：标识/主副标题，右翼：一体化分类筛选胶囊) -->
    <div class="flex flex-col lg:flex-row lg:items-center justify-between gap-3 mb-5 pb-3.5 border-b border-slate-800">
      <div class="flex items-center gap-3">
        <div class="w-10 h-10 rounded-xl bg-indigo-600/20 border border-indigo-500/30 text-indigo-400 flex items-center justify-center shrink-0">
          <span class="material-symbols-outlined text-22px">workspace_premium</span>
        </div>
        <div>
          <div class="flex items-center gap-2.5">
            <h2 class="text-xl sm:text-2xl font-extrabold text-white tracking-tight">精选会员套餐方案</h2>
            <span class="px-2.5 py-0.5 rounded-full bg-indigo-500/10 border border-indigo-500/20 text-indigo-400 text-[11px] font-medium hidden sm:inline-block">商业化订阅中心</span>
          </div>
          <p class="text-xs text-slate-400 mt-0.5">
            按需选择专属大模型算力包，畅享高性能并发与首字极速响应能力
          </p>
        </div>
      </div>

      <!-- 右侧分类筛选导航胶囊 (Segmented Pills) -->
      <div class="flex items-center gap-1.5 p-1 rounded-xl bg-slate-900/90 border border-slate-800 shrink-0 self-start lg:self-auto backdrop-blur shadow-sm">
        <button
          type="button"
          class="px-3.5 py-1.5 rounded-lg text-xs font-semibold cursor-pointer transition-all flex items-center gap-1.5"
          :class="activeTab === 'all' ? 'bg-indigo-600 text-white shadow-sm' : 'text-slate-400 hover:text-white hover:bg-slate-800/60'"
          @click="activeTab = 'all'"
        >
          <span>全部方案</span>
          <span class="text-[10px] px-1.5 py-0.2 rounded-full font-mono" :class="activeTab === 'all' ? 'bg-white/20 text-white' : 'bg-slate-800 text-slate-400'">
            {{ plans.length }}
          </span>
        </button>

        <button
          type="button"
          class="px-3.5 py-1.5 rounded-lg text-xs font-semibold cursor-pointer transition-all flex items-center gap-1.5"
          :class="activeTab === 'subscription' ? 'bg-indigo-600 text-white shadow-sm' : 'text-slate-400 hover:text-white hover:bg-slate-800/60'"
          @click="activeTab = 'subscription'"
        >
          <span class="material-symbols-outlined text-15px">verified</span>
          <span>会员订阅</span>
          <span class="text-[10px] px-1.5 py-0.2 rounded-full font-mono" :class="activeTab === 'subscription' ? 'bg-white/20 text-white' : 'bg-slate-800 text-slate-400'">
            {{ subscriptionPlans.length }}
          </span>
        </button>

        <button
          type="button"
          class="px-3.5 py-1.5 rounded-lg text-xs font-semibold cursor-pointer transition-all flex items-center gap-1.5"
          :class="activeTab === 'addon' ? 'bg-amber-600 text-white shadow-sm' : 'text-slate-400 hover:text-white hover:bg-slate-800/60'"
          @click="activeTab = 'addon'"
        >
          <span class="material-symbols-outlined text-15px">bolt</span>
          <span>Token 加油包</span>
          <span class="text-[10px] px-1.5 py-0.2 rounded-full font-mono" :class="activeTab === 'addon' ? 'bg-white/20 text-white' : 'bg-slate-800 text-slate-400'">
            {{ addonPlans.length }}
          </span>
        </button>
      </div>
    </div>

    <!-- 错误信息提示 -->
    <div v-if="errorMsg" class="max-w-md mx-auto mb-4 p-3 rounded-lg bg-rose-500/10 border border-rose-500/30 text-rose-400 text-xs flex items-center gap-2">
      <span class="material-symbols-outlined text-16px">error</span>
      <span>{{ errorMsg }}</span>
    </div>

    <!-- 加载中 -->
    <LoadingSpinner v-if="loading" text="正在加载精选会员套餐方案..." />

    <!-- 套餐网格卡片 (Linear Style Solid Dark Cards) -->
    <div v-else-if="filteredPlans.length > 0" class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
      <div
        v-for="plan in filteredPlans"
        :key="plan.id"
        class="glass-card p-6 flex flex-col justify-between relative overflow-hidden transition-all hover:-translate-y-1"
        :class="[
          isPlanCurrentActive(plan) ? '!border-emerald-500/80 ring-1 ring-emerald-500/40' :
          plan.type === 'addon' ? 'hover:!border-amber-500/50' :
          'hover:!border-indigo-500/50'
        ]"
      >
        <!-- 当前生效角标 -->
        <div v-if="isPlanCurrentActive(plan)" class="absolute top-0 right-0 bg-emerald-600 text-white text-[10px] font-bold px-3 py-1 rounded-bl-xl shadow flex items-center gap-1 z-10 font-mono">
          <span class="material-symbols-outlined text-[12px]">check_circle</span>
          <span>当前订阅</span>
        </div>

        <div>
          <!-- 头部名称与描述 -->
          <div class="flex items-start justify-between gap-2 mb-1.5" :class="{ 'pr-20': isPlanCurrentActive(plan) }">
            <h3 class="text-base font-bold text-white flex items-center gap-2 flex-wrap">
              <!-- 左侧等级徽章：订阅方案显示专属 Pro / MAX / MAX+ / MAX++ 胶囊徽章 -->
              <span
                v-if="!plan.type || plan.type === 'subscription'"
                :class="getTierBadgeClass(plan.tier)"
                class="px-2 py-0.5 rounded text-[11px] font-mono font-extrabold tracking-wide uppercase shadow-sm shrink-0"
              >
                {{ formatTierName(plan.tier) }}
              </span>
              <!-- 加油包显示黄色加油包标识 -->
              <span
                v-else-if="plan.type === 'addon'"
                class="px-2 py-0.5 rounded text-[11px] font-bold bg-amber-500/15 text-amber-300 border border-amber-500/30 tracking-wide shrink-0"
              >
                加油包
              </span>
              <span>{{ plan.name }}</span>
            </h3>

            <!-- 右侧业务状态角标与周期天数 -->
            <div class="flex items-center gap-1.5 flex-wrap justify-end shrink-0">
              <span v-if="!isPlanCurrentActive(plan) && hasActiveSubscription && (!plan.type || plan.type === 'subscription') && canUpgradeToPlan(plan)" class="badge bg-purple-500/20 text-purple-300 border border-purple-500/30 text-[10px] font-bold">
                ⚡ 支持折算升级
              </span>
              <span v-else-if="!isPlanCurrentActive(plan) && hasActiveSubscription && (!plan.type || plan.type === 'subscription') && !canUpgradeToPlan(plan)" class="badge bg-slate-800 text-slate-400 border border-slate-700 text-[10px]">
                不可降级
              </span>
              <span :class="plan.type === 'addon' ? 'badge badge-amber text-[11px]' : 'badge badge-indigo text-[11px]'">
                {{ plan.durationDays === 0 ? '永久有效' : `${plan.durationDays} 天有效` }}
              </span>
            </div>
          </div>
          <p class="text-xs text-slate-400 mb-3.5 min-h-[18px] line-clamp-1" :title="plan.description">
            {{ plan.description || (plan.type === 'addon' ? '一次性充值叠加 Token 算力额度，到期仍可使用至加油包结束' : '解锁高可用模型调用权限与专属算力配额') }}
          </p>

          <!-- 价格栏 -->
          <div class="flex items-baseline gap-1 mb-4 pb-3.5 border-b border-slate-800">
            <span class="text-xs text-slate-400 font-mono">¥</span>
            <span class="text-3xl font-extrabold text-white tracking-tight font-mono">
              {{ (plan.priceCents / 100).toFixed(2) }}
            </span>
            <span class="text-xs text-slate-400 font-mono ml-1">
              / {{ plan.type === 'addon' ? (plan.durationDays === 0 ? '份 (永久有效)' : `份 (${plan.durationDays}天有效)`) : (plan.durationDays === 0 ? '永久' : `${plan.durationDays}天`) }}
            </span>
          </div>

          <!-- 核心权益亮点 -->
          <div class="flex flex-col gap-2.5 mb-4">
            <!-- 额度限制亮点 -->
            <div class="flex items-center gap-2 text-xs text-slate-300">
              <span class="material-symbols-outlined text-16px text-amber-400">toll</span>
              <span>{{ plan.type === 'addon' ? '包含 Token 额度' : '额度限制' }}: <strong class="text-white font-mono font-bold">{{ formatTokenLimit(plan.tokenLimit) }}</strong></span>
            </div>

            <div v-if="plan.type !== 'addon' || plan.rateLimit" class="flex items-center gap-2 text-xs text-slate-300">
              <span class="material-symbols-outlined text-emerald-400 text-16px">speed</span>
              <span>速率限制: <strong class="text-white font-mono font-semibold">{{ plan.rateLimit || 30 }}</strong> 次/分钟 (RPM)</span>
            </div>

            <!-- 视觉分析多模态能力亮点 -->
            <div class="flex items-center gap-2 text-xs text-cyan-300 font-medium">
              <span class="material-symbols-outlined text-cyan-400 text-16px">visibility</span>
              <span>视觉能力: <strong class="text-cyan-200">全系列模型支持多模态视觉分析（智能后台调度）</strong></span>
            </div>

            <!-- 当套餐包含 auto 时，在速率限制下方新增独立亮点行 -->
            <div v-if="(plan.allowedModels || []).includes('auto')" class="flex items-center gap-2 text-xs text-amber-300 font-medium">
              <span class="material-symbols-outlined text-amber-400 text-16px">bolt</span>
              <span>核心特性: <strong class="text-amber-200">支持 Auto 智能调度模型</strong></span>
            </div>

            <!-- 包含模型清单展示 -->
            <div class="mt-1">
              <div class="text-xs font-semibold text-slate-300 mb-1.5 flex items-center gap-1.5">
                <span class="material-symbols-outlined text-indigo-400 text-15px">hub</span>
                <span>{{ plan.type === 'addon' ? '加油包适用模型:' : '包含并授权的模型清单:' }}</span>
              </div>
              <div class="flex flex-wrap gap-1.5 max-h-[72px] overflow-y-auto pr-1">
                <span
                  v-for="model in (plan.allowedModels || [])"
                  :key="model"
                  class="badge text-[10px] font-mono py-0.5 px-2"
                  :class="model === 'auto' ? 'badge-amber font-bold' : 'badge-cyan'"
                >
                  {{ model === 'auto' ? '⚡ 支持 auto 模型' : model }}
                </span>
                <span v-if="!plan.allowedModels || plan.allowedModels.length === 0" class="text-11px text-slate-500">
                  全量公共模型通用
                </span>
              </div>
            </div>

            <!-- Auto 模型专属包含模型说明板块 -->
            <div
              v-if="(plan.allowedModels || []).includes('auto') && plan.autoModels && plan.autoModels.length > 0"
              class="p-2.5 rounded-xl bg-slate-900/90 border border-amber-500/30 shadow-sm mt-1"
            >
              <div class="text-xs font-bold text-amber-300 flex items-center gap-1.5 mb-1.5">
                <span class="material-symbols-outlined text-15px text-amber-400">bolt</span>
                <span>Auto 智能竞速实际包含模型 ({{ plan.autoModels.length }} 款):</span>
              </div>
              <div class="flex flex-wrap gap-1 max-h-[58px] overflow-y-auto pr-0.5">
                <span
                  v-for="am in plan.autoModels"
                  :key="am"
                  class="px-1.5 py-0.5 rounded text-[10px] font-mono font-medium bg-black/50 text-amber-200 border border-amber-500/30 flex items-center gap-1"
                >
                  <span class="material-symbols-outlined text-[9px] text-amber-400">check_circle</span>
                  <span>{{ am }}</span>
                </span>
              </div>
              <p class="text-[10px] text-amber-200/70 mt-1">
                * 调用 auto 模型时将根据号池健康度与响应速度在上述模型池中自动择优调度
              </p>
            </div>
          </div>
        </div>

        <!-- 底部购买/订阅操作按钮区 -->
        <div class="mt-auto pt-3.5 border-t border-slate-800 flex flex-col gap-1.5">
          <!-- 情况 A: Token 加油包 (限持有有效会员订阅的用户购买) -->
          <template v-if="plan.type === 'addon'">
            <!-- 用户未登录 或 已有生效会员订阅：允许点击进入 -->
            <button
              v-if="!currentUser || hasActiveSubscription"
              type="button"
              class="btn-amber-tech w-full py-2.5 text-xs font-semibold rounded-xl flex items-center justify-center gap-1.5"
              @click="handleSubscribe(plan)"
            >
              <span class="material-symbols-outlined text-16px">shopping_bag</span>
              <span>立即购买 →</span>
            </button>

            <!-- 用户已登录但没有有效会员订阅：置灰引导开通会员订阅 -->
            <button
              v-else
              type="button"
              class="w-full py-2.5 text-xs font-bold rounded-xl cursor-pointer flex items-center justify-center gap-1.5 transition-all bg-amber-500/10 border border-amber-500/30 text-amber-300 hover:bg-amber-500/20"
              @click="activeTab = 'subscription'"
              title="加油包仅限已开通会员订阅的账户购买，点击切换至会员订阅方案"
            >
              <span class="material-symbols-outlined text-16px">lock</span>
              <span>需先开通会员订阅 →</span>
            </button>
            <p class="text-[10px] text-center text-slate-400">
              {{ hasActiveSubscription ? '⚡ 额度即刻到账 · 订阅失效亦可使用至加油包结束' : '💡 加油包限会员购买，购买后可使用至加油包结束' }}
            </p>
          </template>

          <!-- 情况 B: 订阅型套餐且为用户当前生效的订阅 (禁用，提示当前订阅生效中) -->
          <template v-else-if="isPlanCurrentActive(plan)">
            <button
              type="button"
              disabled
              class="w-full py-2.5 text-xs font-bold rounded-xl flex items-center justify-center gap-1.5 cursor-not-allowed bg-emerald-950/60 border border-emerald-500/40 text-emerald-300 shadow-none"
            >
              <span class="material-symbols-outlined text-16px text-emerald-400">task_alt</span>
              <span>当前订阅生效中</span>
            </button>
            <p class="text-[10px] text-center text-emerald-400/90 font-medium font-mono">
              有效期至: {{ formatExpireText(currentUser?.planExpireAt) }} (到期后方可续费)
            </p>
          </template>

          <!-- 情况 C: 订阅型套餐，用户当前已有其他生效中的订阅 -->
          <template v-else-if="hasActiveSubscription">
            <!-- 子情况 C-1: 目标等级严格高于当前等级（允许升级） -->
            <template v-if="canUpgradeToPlan(plan)">
              <button
                type="button"
                class="w-full py-2.5 text-xs font-bold rounded-xl cursor-pointer flex items-center justify-center gap-1.5 transition-all bg-gradient-to-r from-indigo-600 to-indigo-700 hover:from-indigo-500 hover:to-indigo-600 text-white shadow-md shadow-indigo-600/30"
                @click="handleSubscribe(plan)"
              >
                <span class="material-symbols-outlined text-16px">upgrade</span>
                <span>立即升级方案 →</span>
              </button>
              <p class="text-[10px] text-center text-purple-300/90 font-medium">
                ⚡ 支持按当前剩余 Token 额度折算立减抵扣
              </p>
            </template>
            <!-- 子情况 C-2: 目标等级低于或等于当前等级（严禁降级） -->
            <template v-else>
              <button
                type="button"
                disabled
                class="w-full py-2.5 text-xs font-bold rounded-xl flex items-center justify-center gap-1.5 cursor-not-allowed bg-slate-800/60 border border-slate-700/60 text-slate-400 opacity-60 shadow-none"
              >
                <span class="material-symbols-outlined text-16px text-slate-400">block</span>
                <span>不可降级 (当前方案级别更高)</span>
              </button>
              <p class="text-[10px] text-center text-slate-500">
                当前订阅方案层级高于或同等于此方案，仅支持向上升级
              </p>
            </template>
          </template>

          <!-- 情况 D: 用户未订阅或当前订阅已到期 (允许购买/续费) -->
          <template v-else>
            <button
              type="button"
              class="btn-primary w-full py-2.5 text-xs cursor-pointer flex items-center justify-center gap-1.5"
              @click="handleSubscribe(plan)"
            >
              <span class="material-symbols-outlined text-16px">workspace_premium</span>
              <span>{{ isPlanPreviouslyOwned(plan) ? '立即续费 →' : '立即订阅 →' }}</span>
            </button>
            <p class="text-[10px] text-center text-slate-400">
              🚀 支付成功后秒级开通服务与模型授权
            </p>
          </template>
        </div>
      </div>
    </div>

    <!-- 空数据提示 -->
    <div v-else class="glass-card p-12 text-center text-slate-400 text-xs">
      <span class="material-symbols-outlined text-36px text-slate-500 mb-2 block">layers_clear</span>
      <span>当前分类下暂无套餐方案</span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { planApi, getToken } from '../api/client'
import { useRouter } from 'vue-router'
import LoadingSpinner from '../components/common/LoadingSpinner.vue'
import { useUserStore } from '../stores'

const router = useRouter()
const userStore = useUserStore()
const plans = ref<any[]>([])
const loading = ref(true)
const errorMsg = ref('')
const currentUser = computed(() => userStore.user)
const activeTab = ref<'all' | 'subscription' | 'addon'>('all')

const nowTimestamp = ref<number>(Math.floor(Date.now() / 1000))

// 过滤会员订阅型套餐
const subscriptionPlans = computed(() => {
  return plans.value.filter(p => !p.type || p.type === 'subscription')
})

// 过滤增值 Token 包套餐
const addonPlans = computed(() => {
  return plans.value.filter(p => p.type === 'addon')
})

// 根据当前 Tab 过滤展示的套餐列表
const filteredPlans = computed(() => {
  if (activeTab.value === 'subscription') return subscriptionPlans.value
  if (activeTab.value === 'addon') return addonPlans.value
  return plans.value
})

// 用户当前是否拥有尚未过期的有效基础订阅方案
const hasActiveSubscription = computed(() => {
  if (!currentUser.value) return false
  const pId = currentUser.value.planId
  const expRaw = currentUser.value.planExpireAt
  if (!pId || pId <= 0) return false
  const exp = typeof expRaw === 'number' ? expRaw : (Number(expRaw) || 0)
  // 永久有效 (0) 或 当前未过期
  return exp === 0 || exp > nowTimestamp.value
})

// 获取用户当前生效中的套餐对象
const currentPlanObj = computed(() => {
  if (currentUser.value?.plan) return currentUser.value.plan
  if (currentUser.value?.planId) {
    return plans.value.find(p => p.id === currentUser.value?.planId) || null
  }
  return null
})

function getTierWeight(tier?: string): number {
  const t = (tier || 'pro').toLowerCase().trim()
  if (t === 'max++') return 4
  if (t === 'max+') return 3
  if (t === 'max') return 2
  return 1 // 'pro'
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

// 检查是否允许升级至目标套餐（仅当目标等级严格高于当前方案等级时为 true）
function canUpgradeToPlan(targetPlan: any): boolean {
  if (!hasActiveSubscription.value) return false
  if (targetPlan.type === 'addon') return true
  if (isPlanCurrentActive(targetPlan)) return false

  const currentPlan = currentPlanObj.value
  if (!currentPlan) return false

  const currentWeight = getTierWeight(currentPlan.tier)
  const targetWeight = getTierWeight(targetPlan.tier)

  return targetWeight > currentWeight
}

// 判断某一套餐是否为用户当前正在生效的订阅
function isPlanCurrentActive(plan: any): boolean {
  if (!hasActiveSubscription.value) return false
  if (plan.type === 'addon') return false
  return plan.id === currentUser.value?.planId
}

// 判断用户此前是否曾订购过该套餐（已过期）
function isPlanPreviouslyOwned(plan: any): boolean {
  if (!currentUser.value) return false
  return plan.id === currentUser.value.planId
}

function formatTokenLimit(val?: number): string {
  if (!val || val <= 0) return '不限额度'
  if (val >= 100000000) return (val / 100000000).toFixed(val % 100000000 === 0 ? 0 : 2) + ' 亿 Tokens'
  if (val >= 10000) return (val / 10000).toFixed(val % 10000 === 0 ? 0 : 1) + ' 万 Tokens'
  if (val >= 1000000) return (val / 1000000).toFixed(1) + 'M Tokens'
  if (val >= 1000) return (val / 1000).toFixed(0) + 'K Tokens'
  return `${val.toLocaleString()} Tokens`
}

function formatExpireText(timestamp?: number | string): string {
  if (!timestamp || timestamp === 0 || timestamp === '0') return '永久有效'
  const ts = typeof timestamp === 'string' ? (isNaN(Number(timestamp)) ? Date.parse(timestamp) / 1000 : Number(timestamp)) : timestamp
  const d = new Date(ts * 1000)
  const Y = d.getFullYear()
  const M = String(d.getMonth() + 1).padStart(2, '0')
  const D = String(d.getDate()).padStart(2, '0')
  const h = String(d.getHours()).padStart(2, '0')
  const m = String(d.getMinutes()).padStart(2, '0')
  return `${Y}-${M}-${D} ${h}:${m}`
}

async function fetchPlans() {
  loading.value = true
  errorMsg.value = ''
  try {
    plans.value = await planApi.listActive()
  } catch (err: any) {
    errorMsg.value = err.message || '获取套餐列表失败'
  } finally {
    loading.value = false
  }
}

async function fetchUserProfile() {
  if (!userStore.token) return
  try {
    await userStore.fetchUserProfile()
    nowTimestamp.value = Math.floor(Date.now() / 1000)
  } catch (err) {
    console.warn('获取当前登录用户订阅信息失败:', err)
  }
}

function handleSubscribe(plan: any) {
  if (!getToken()) {
    router.push(`/login?redirect=${encodeURIComponent(`/checkout/confirm?plan_id=${plan.id}`)}`)
    return
  }

  // 若为同一订阅方案且用户当前仍在生效期内，提示无需重复订购
  if (isPlanCurrentActive(plan)) {
    alert('您当前已有此订阅方案在生效期内，无需重复订购。如需续费请在到期后再行操作，或选择其他方案办理升级。')
    return
  }

  // 进入订单确认页核对后再前往支付
  router.push(`/checkout/confirm?plan_id=${plan.id}`)
}

onMounted(() => {
  fetchPlans()
  fetchUserProfile()
})
</script>
