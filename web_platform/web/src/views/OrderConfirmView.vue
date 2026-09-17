<template>
  <div class="max-w-6xl mx-auto px-4 py-8">
    <!-- 顶部导航与安全标头 -->
    <div class="flex items-center justify-between mb-8 pb-4 border-b border-slate-800/80">
      <button
        type="button"
        class="inline-flex items-center gap-1.5 text-xs text-slate-400 hover:text-indigo-400 transition-colors cursor-pointer bg-slate-800/40 hover:bg-slate-800 px-3 py-1.5 rounded-lg border border-slate-700/50"
        @click="goBack"
      >
        <span class="material-symbols-outlined text-16px">arrow_back</span>
        <span>返回修改方案</span>
      </button>

      <div class="flex items-center gap-2 px-3 py-1.5 rounded-full bg-emerald-500/10 border border-emerald-500/20 text-emerald-400 text-xs font-medium">
        <span class="material-symbols-outlined text-16px">verified_user</span>
        <span>{{ plan?.type === 'addon' ? '官方算力加油包 · 即充即用 · 15分钟有效' : '官方安全收银 · 即开即用 · 15分钟有效' }}</span>
      </div>
    </div>

    <!-- 加载中 -->
    <LoadingSpinner v-if="loading" text="正在加载订单套餐确认详情..." />

    <!-- 错误提示 -->
    <div v-else-if="errorMsg" class="max-w-lg mx-auto p-6 text-center glass-card border-rose-500/30">
      <span class="material-symbols-outlined text-40px text-rose-400 mb-2 block">error</span>
      <h3 class="text-base font-bold text-white mb-2">获取订单详情失败</h3>
      <p class="text-xs text-rose-300 mb-6">{{ errorMsg }}</p>
      <button type="button" class="btn-primary text-xs py-2 px-4 cursor-pointer" @click="goBack">
        返回套餐选购列表
      </button>
    </div>

    <!-- 降级拦截警告 -->
    <div v-else-if="isDowngradeBlocked" class="max-w-lg mx-auto p-6 text-center glass-card border-rose-500/40 my-8">
      <span class="material-symbols-outlined text-48px text-rose-400 mb-2 block">block</span>
      <h3 class="text-base font-bold text-white mb-2">不支持降级订购方案</h3>
      <p class="text-xs text-slate-300 mb-4 leading-relaxed">
        {{ quote?.cannotUpgradeReason || '您当前已在更高或同等层级方案生效期内，系统暂不支持降级订购。' }}
      </p>
      <p class="text-xs text-rose-300 mb-6 font-medium">
        当前生效方案：【{{ quote?.currentPlanName || (currentUser as any)?.plan?.name || '当前订阅' }}】({{ quote?.currentPlanTier || '高级别' }})。<br />
        如需更改为更低等级方案，请等待当前方案到期后再行订购，或选择升级至更高级别方案。
      </p>
      <div class="flex items-center justify-center gap-3">
        <button type="button" class="btn-secondary text-xs py-2 px-4 cursor-pointer" @click="goBack">
          返回选择更高级别方案
        </button>
        <router-link to="/dashboard" class="btn-primary text-xs py-2 px-4">
          前往用户控制台
        </router-link>
      </div>
    </div>

    <!-- 同一订阅未到期防重复订购警告 -->
    <div v-else-if="isSameSubscriptionConflict" class="max-w-lg mx-auto p-6 text-center glass-card border-amber-500/40 my-8">
      <span class="material-symbols-outlined text-48px text-amber-400 mb-2 block">lock_clock</span>
      <h3 class="text-base font-bold text-white mb-2">当前方案正在生效中</h3>
      <p class="text-xs text-slate-300 mb-4 leading-relaxed">
        您当前账户已绑定并正在使用此会员订阅方案。按照会员规则，同方案生效期内无需重复购买。
      </p>
      <p class="text-xs text-amber-300 mb-6 font-medium">
        有效期至: {{ formatExpireText(currentUser?.planExpireAt) }}。如需续费请到期后再行操作，或返回方案列表升级其他方案。
      </p>
      <div class="flex items-center justify-center gap-3">
        <button type="button" class="btn-secondary text-xs py-2 px-4 cursor-pointer" @click="goBack">
          选择其他方案升级
        </button>
        <router-link to="/dashboard" class="btn-primary text-xs py-2 px-4">
          前往用户控制台
        </router-link>
      </div>
    </div>

    <!-- 加油包限会员购买拦截警告 -->
    <div v-else-if="isAddonWithoutSubscription" class="max-w-lg mx-auto p-6 text-center glass-card border-amber-500/40 my-8">
      <span class="material-symbols-outlined text-48px text-amber-400 mb-2 block">lock</span>
      <h3 class="text-base font-bold text-white mb-2">需先开通会员订阅</h3>
      <p class="text-xs text-slate-300 mb-4 leading-relaxed">
        Token 加油包属于会员专属补充算力，仅限持有有效会员订阅的账户购买。您当前账户尚未开通会员或订阅已到期。
      </p>
      <p class="text-xs text-amber-300 mb-6 font-medium">
        请先开通会员订阅，即可随时按需选购 Token 加油包叠加算力。
      </p>
      <div class="flex items-center justify-center gap-3">
        <button type="button" class="btn-secondary text-xs py-2 px-4 cursor-pointer" @click="goBack">
          返回方案列表
        </button>
        <router-link to="/pricing" class="btn-primary text-xs py-2 px-4">
          选购会员订阅方案
        </router-link>
      </div>
    </div>

    <!-- 主体双列布局 -->
    <div v-else-if="plan" class="grid grid-cols-1 lg:grid-cols-12 gap-8 items-start">
      <!-- 左侧：方案详情与规格卡片 (7-8 列) -->
      <div class="lg:col-span-8 flex flex-col gap-6">
        <!-- 1. 套餐基本信息与英雄卡 -->
        <div class="glass-card p-6 relative overflow-hidden">
          <div class="flex items-center justify-between mb-4">
            <div class="flex items-center gap-2">
              <span class="material-symbols-outlined text-indigo-400 text-20px">inventory_2</span>
              <h3 class="text-base font-bold text-white">{{ plan.type === 'addon' ? '已选算力加油包' : '已选订阅方案' }}</h3>
            </div>
            <div class="flex items-center gap-2">
              <span v-if="plan.type !== 'addon' && plan.tier" class="badge badge-purple uppercase font-bold text-[10px]">
                {{ plan.tier }} 级方案
              </span>
              <span :class="plan.type === 'addon' ? 'badge badge-amber' : 'badge badge-indigo'">
                {{ plan.type === 'addon' ? '⚡ Token 加油包' : '官方精选方案' }}
              </span>
            </div>
          </div>

          <!-- 套餐升级专属折算抵扣提示卡片 -->
          <div v-if="quote?.isUpgrade" class="p-4 rounded-xl bg-gradient-to-r from-purple-950/60 via-indigo-950/40 to-slate-900/60 border border-purple-500/40 mb-5 flex items-start gap-3.5 shadow-lg shadow-purple-500/5">
            <div class="w-8 h-8 rounded-lg bg-purple-500/20 border border-purple-500/40 flex items-center justify-center shrink-0 text-purple-300 mt-0.5">
              <span class="material-symbols-outlined text-18px">auto_mode</span>
            </div>
            <div>
              <div class="flex items-center gap-2 mb-1">
                <h4 class="text-xs font-bold text-white">已开启「按当前剩余 Token 折算抵扣」升级通道</h4>
                <span class="badge bg-purple-500/20 text-purple-300 border border-purple-500/30 text-[10px]">立减 ¥{{ (quote.discountCents / 100).toFixed(2) }}</span>
              </div>
              <p class="text-[11px] text-purple-200/80 leading-relaxed">
                您当前生效中的套餐为 <strong class="text-white">【{{ quote.currentPlanName }}】</strong>，剩余可用 Token 额度为 <strong class="text-purple-300">{{ formatTokenLimit(quote.currentRemainingTokens) }}</strong>。
                系统已按剩余额度精确折算剩余价值 <strong class="text-emerald-400">¥{{ (quote.remainingFeeCents / 100).toFixed(2) }}</strong>，已自动抵扣升级费用（升级实付保底最低 ¥1.00）。
              </p>
            </div>
          </div>

          <div class="p-4 rounded-xl bg-gradient-to-r from-indigo-900/30 via-slate-900/40 to-slate-950/60 border border-indigo-500/20 mb-6">
            <h2 class="text-2xl font-extrabold text-white tracking-tight mb-1 flex items-center gap-2.5 flex-wrap">
              <span
                v-if="!plan.type || plan.type === 'subscription'"
                class="px-2.5 py-0.5 rounded-lg text-xs font-mono font-extrabold uppercase shadow-sm"
                :class="plan.tier === 'max++' ? 'bg-amber-500/20 text-amber-300 border border-amber-500/40' : (plan.tier === 'max+' ? 'bg-purple-500/20 text-purple-300 border border-purple-500/40' : (plan.tier === 'max' ? 'bg-indigo-500/20 text-indigo-300 border border-indigo-500/40' : 'bg-blue-500/20 text-blue-300 border border-blue-500/40'))"
              >
                {{ (plan.tier || 'pro').toUpperCase() }}
              </span>
              <span>{{ plan.name }}</span>
            </h2>
            <p class="text-xs text-slate-400 leading-relaxed">{{ plan.description || (plan.type === 'addon' ? '一次性充值叠加专属大模型算力，不冲掉基础会员订阅' : '高可用企业级大模型算力包，畅享极速响应与专属调用额度') }}</p>
          </div>

          <!-- 4 列核心规格参数卡片 -->
          <div class="grid grid-cols-2 sm:grid-cols-4 gap-3 mb-6">
            <div class="p-3 rounded-lg bg-slate-900/60 border border-slate-800 flex flex-col gap-1">
              <span class="text-[11px] text-slate-400 flex items-center gap-1">
                <span class="material-symbols-outlined text-14px text-indigo-400">calendar_month</span>
                有效周期
              </span>
              <span class="text-sm font-bold text-white">
                {{ plan.durationDays === 0 ? '永久有效' : `${plan.durationDays} 天有效` }}
              </span>
            </div>

            <div class="p-3 rounded-lg bg-slate-900/60 border border-slate-800 flex flex-col gap-1">
              <span class="text-[11px] text-slate-400 flex items-center gap-1">
                <span class="material-symbols-outlined text-14px text-amber-400">toll</span>
                算力配额
              </span>
              <span class="text-sm font-bold text-amber-300">
                {{ formatTokenLimit(plan.tokenLimit) }}
              </span>
            </div>

            <div class="p-3 rounded-lg bg-slate-900/60 border border-slate-800 flex flex-col gap-1">
              <span class="text-[11px] text-slate-400 flex items-center gap-1">
                <span class="material-symbols-outlined text-14px text-emerald-400">speed</span>
                调用速率
              </span>
              <span class="text-sm font-bold text-emerald-300">
                {{ plan.rateLimit || 30 }} RPM
              </span>
            </div>

            <div class="p-3 rounded-lg bg-slate-900/60 border border-slate-800 flex flex-col gap-1">
              <span class="text-[11px] text-slate-400 flex items-center gap-1">
                <span class="material-symbols-outlined text-14px text-cyan-400">visibility</span>
                多模态分析
              </span>
              <span class="text-sm font-bold text-cyan-300">全面支持</span>
            </div>
          </div>

          <!-- 授权模型清单 -->
          <div class="mb-6">
            <div class="text-xs font-semibold text-slate-300 mb-2.5 flex items-center gap-1.5">
              <span class="material-symbols-outlined text-indigo-400 text-16px">hub</span>
              <span>套餐授权开放模型列表 ({{ (plan.allowedModels || []).length }} 款):</span>
            </div>
            <div class="flex flex-wrap gap-1.5 max-h-120px overflow-y-auto pr-1">
              <span
                v-for="m in (plan.allowedModels || [])"
                :key="m"
                class="badge text-10px font-mono"
                :class="m === 'auto' ? 'badge-amber font-bold shadow-sm' : 'badge-cyan'"
              >
                {{ m === 'auto' ? '⚡ 支持 auto 调度' : m }}
              </span>
              <span v-if="!plan.allowedModels || plan.allowedModels.length === 0" class="text-11px text-slate-500">
                全部公共模型开放
              </span>
            </div>
          </div>

          <!-- Auto 模型池补充说明 -->
          <div
            v-if="(plan.allowedModels || []).includes('auto') && plan.autoModels && plan.autoModels.length > 0"
            class="p-3.5 rounded-xl bg-gradient-to-br from-amber-500/15 via-amber-500/10 to-amber-600/5 border border-amber-500/30 mb-6"
          >
            <div class="text-xs font-bold text-amber-300 flex items-center gap-1.5 mb-2">
              <span class="material-symbols-outlined text-16px text-amber-400">bolt</span>
              <span>Auto 智能竞速实际调度池 (共 {{ plan.autoModels.length }} 款模型):</span>
            </div>
            <div class="flex flex-wrap gap-1.5 max-h-90px overflow-y-auto pr-0.5">
              <span
                v-for="am in plan.autoModels"
                :key="am"
                class="px-2 py-0.5 rounded text-[10px] font-mono font-medium bg-black/40 text-amber-200 border border-amber-500/30 flex items-center gap-1"
              >
                <span class="material-symbols-outlined text-[10px] text-amber-400">check_circle</span>
                <span>{{ am }}</span>
              </span>
            </div>
          </div>

          <!-- 权益保障列表 -->
          <div class="p-3.5 rounded-xl bg-slate-900/50 border border-slate-800 flex flex-col gap-2">
            <div class="flex items-center gap-2 text-xs text-slate-300">
              <span class="material-symbols-outlined text-emerald-400 text-16px">check_circle</span>
              <span>秒级自动开通：支付成功后额度与白名单即刻自动生效并顺延有效期</span>
            </div>
            <div class="flex items-center gap-2 text-xs text-slate-300">
              <span class="material-symbols-outlined text-emerald-400 text-16px">check_circle</span>
              <span>高可用集群保障：支持多模型智能热备与动态负载均衡</span>
            </div>
            <div class="flex items-center gap-2 text-xs text-slate-300">
              <span class="material-symbols-outlined text-emerald-400 text-16px">check_circle</span>
              <span>标准协议兼容：支持通过自定义 API Key 直连任意 OpenAI / Claude 生态客户端</span>
            </div>
          </div>
        </div>

        <!-- 2. 安全与时效提示卡片 -->
        <div class="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <div class="glass-card p-4 flex items-start gap-3">
            <div class="w-8 h-8 rounded-lg bg-amber-500/15 border border-amber-500/30 flex items-center justify-center shrink-0 text-amber-400">
              <span class="material-symbols-outlined text-18px">timer</span>
            </div>
            <div>
              <h4 class="text-xs font-bold text-white mb-1">15 分钟订单锁定期</h4>
              <p class="text-[11px] text-slate-400 leading-normal">
                提交订单后将保留 15 分钟支付窗口，超时未支付系统将自动取消订单，请及时完成付款。
              </p>
            </div>
          </div>

          <div class="glass-card p-4 flex items-start gap-3">
            <div class="w-8 h-8 rounded-lg bg-indigo-500/15 border border-indigo-500/30 flex items-center justify-center shrink-0 text-indigo-400">
              <span class="material-symbols-outlined text-18px">encrypted</span>
            </div>
            <div>
              <h4 class="text-xs font-bold text-white mb-1">加密安全支付通道</h4>
              <p class="text-[11px] text-slate-400 leading-normal">
                采用端到端签名防篡改验证，支持支付宝及微信直连收银，资金安全保障。
              </p>
            </div>
          </div>
        </div>
      </div>

      <!-- 右侧：费用结算卡片 (4-5 列) -->
      <div class="lg:col-span-4 sticky top-24">
        <div class="glass-card p-6 border-indigo-500/30 shadow-xl shadow-indigo-500/5">
          <div class="flex items-center justify-between mb-6 pb-4 border-b border-slate-800">
            <h3 class="text-base font-bold text-white">费用结算汇总</h3>
            <span class="badge badge-emerald">CNY 人民币</span>
          </div>

          <!-- 结算明细列表 -->
          <div class="flex flex-col gap-3 mb-6">
            <div class="flex items-center justify-between text-xs text-slate-400">
              <span>方案标准原价</span>
              <span class="font-mono text-slate-200">¥{{ ((quote?.priceCents || plan.priceCents) / 100).toFixed(2) }}</span>
            </div>

            <div v-if="quote?.isUpgrade && quote.discountCents > 0" class="flex items-center justify-between text-xs text-slate-400">
              <span class="flex items-center gap-1 text-purple-300">
                <span class="material-symbols-outlined text-14px">savings</span>
                <span>原订阅剩余 Token 折算抵扣</span>
              </span>
              <span class="font-mono text-emerald-400 font-bold">-¥{{ (quote.discountCents / 100).toFixed(2) }}</span>
            </div>

            <div class="pt-3 border-t border-slate-800 flex items-baseline justify-between">
              <div>
                <span class="text-xs font-bold text-white block">实际应付总额</span>
                <span class="text-[10px] text-slate-500">{{ quote?.isUpgrade ? '已扣减旧方案剩余价值（保底实付 ¥1.00）' : '已含专属算力保障服务' }}</span>
              </div>
              <div class="flex items-baseline gap-0.5 text-indigo-400">
                <span class="text-sm font-semibold">¥</span>
                <span class="text-3xl font-extrabold tracking-tight">{{ (finalPayAmountCents / 100).toFixed(2) }}</span>
              </div>
            </div>
          </div>

          <!-- 15 分钟有效提醒框 -->
          <div class="mb-6 p-3 rounded-lg bg-amber-500/10 border border-amber-500/20 text-amber-300 text-xs flex items-center gap-2">
            <span class="material-symbols-outlined text-16px shrink-0">hourglass_top</span>
            <span class="text-[11px] leading-relaxed">
              订单创建后请在 15 分钟内完成支付，超时未支付系统将自动取消。
            </span>
          </div>

          <!-- 提交支付按钮 -->
          <button
            type="button"
            :disabled="submitting || isSameSubscriptionConflict || isDowngradeBlocked"
            class="w-full py-3 text-xs font-bold rounded-xl cursor-pointer flex items-center justify-center gap-2 disabled:opacity-60 shadow-lg transition-all"
            :class="isDowngradeBlocked ? 'bg-slate-800 text-slate-500 cursor-not-allowed border border-slate-700' : (finalPayAmountCents === 0 ? 'bg-gradient-to-r from-emerald-600 to-teal-600 hover:from-emerald-500 hover:to-teal-500 text-white shadow-emerald-600/30' : 'btn-primary shadow-indigo-600/30')"
            @click="handleConfirmAndPay"
          >
            <span v-if="submitting" class="material-symbols-outlined text-16px animate-spin">progress_activity</span>
            <span v-else-if="isDowngradeBlocked" class="material-symbols-outlined text-16px text-rose-400">block</span>
            <span v-else class="material-symbols-outlined text-16px">{{ finalPayAmountCents === 0 ? 'verified' : 'payments' }}</span>
            <span>{{ submitButtonText }}</span>
          </button>

          <!-- 支持的支付方式图标 -->
          <div class="mt-5 pt-4 border-t border-slate-800/80 flex items-center justify-center gap-3 text-[11px] text-slate-500">
            <span class="flex items-center gap-1">
              <span class="material-symbols-outlined text-14px text-blue-400">check</span>
              支付宝
            </span>
            <span class="text-slate-700">|</span>
            <span class="flex items-center gap-1">
              <span class="material-symbols-outlined text-14px text-emerald-400">check</span>
              微信支付
            </span>
            <span class="text-slate-700">|</span>
            <span class="flex items-center gap-1">
              <span class="material-symbols-outlined text-14px text-indigo-400">check</span>
              极速开通
            </span>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { planApi, checkoutApi, getToken } from '../api/client'
import LoadingSpinner from '../components/common/LoadingSpinner.vue'
import { useUserStore } from '../stores'

const route = useRoute()
const router = useRouter()
const userStore = useUserStore()

const loading = ref(true)
const submitting = ref(false)
const plan = ref<any>(null)
const quote = ref<any>(null)
const errorMsg = ref('')
const currentUser = computed(() => userStore.user)

// 校验是否与用户已有完全相同的生效中订阅冲突（同方案生效期内无需重复购买）
const isSameSubscriptionConflict = computed(() => {
  if (!plan.value || !currentUser.value) return false
  if (plan.value.type === 'addon') return false // 加油包不冲突
  const pId = currentUser.value.planId
  if (!pId || pId !== plan.value.id) return false
  const expRaw = currentUser.value.planExpireAt
  const exp = typeof expRaw === 'number' ? expRaw : (Number(expRaw) || 0)
  const now = Math.floor(Date.now() / 1000)
  return exp === 0 || exp > now
})

// 校验加油包是否要求必须持有有效会员订阅
const isAddonWithoutSubscription = computed(() => {
  if (!plan.value || !currentUser.value) return false
  if (plan.value.type !== 'addon') return false
  const pId = currentUser.value.planId
  const expRaw = currentUser.value.planExpireAt
  if (!pId || pId <= 0) return true
  const exp = typeof expRaw === 'number' ? expRaw : (Number(expRaw) || 0)
  const now = Math.floor(Date.now() / 1000)
  return exp !== 0 && exp <= now
})

// 校验是否属于被禁止的同级或降级订购（当前用户生效订阅的等级 >= 目标套餐等级）
const isDowngradeBlocked = computed(() => {
  if (!plan.value || plan.value.type === 'addon') return false
  if (quote.value && quote.value.canUpgrade === false) {
    return true
  }
  return false
})

// 计算最终需要支付的金额（分）
const finalPayAmountCents = computed(() => {
  if (quote.value && typeof quote.value.finalAmountCents === 'number') {
    return quote.value.finalAmountCents
  }
  return plan.value ? plan.value.priceCents : 0
})

// 提交支付按钮文案
const submitButtonText = computed(() => {
  if (submitting.value) return '正在创建订单并连接收银台...'
  if (isDowngradeBlocked.value) return '无法降级订购'
  if (quote.value?.isUpgrade) return `立即支付升级 ¥${(finalPayAmountCents.value / 100).toFixed(2)} →`
  if (plan.value?.type === 'addon') return `立即支付购买 ¥${(finalPayAmountCents.value / 100).toFixed(2)} →`
  return `立即支付 ¥${(finalPayAmountCents.value / 100).toFixed(2)} →`
})

function formatExpireText(timestamp?: number | string): string {
  if (!timestamp || timestamp === 0 || timestamp === '0') return '永久有效'
  const ts = typeof timestamp === 'string' ? (isNaN(Number(timestamp)) ? Date.parse(timestamp) / 1000 : Number(timestamp)) : timestamp
  const d = new Date(ts * 1000)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')} ${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
}

function formatTokenLimit(val?: number): string {
  if (!val || val <= 0) return '不限额度'
  if (val >= 100000000) return (val / 100000000).toFixed(val % 100000000 === 0 ? 0 : 2) + ' 亿 Tokens'
  if (val >= 10000) return (val / 10000).toFixed(val % 10000 === 0 ? 0 : 1) + ' 万 Tokens'
  if (val >= 1000000) return (val / 1000000).toFixed(1) + 'M Tokens'
  if (val >= 1000) return (val / 1000).toFixed(0) + 'K Tokens'
  return `${val.toLocaleString()} Tokens`
}

function goBack() {
  router.push('/pricing')
}

async function fetchPlanDetail() {
  const planId = Number(route.query.plan_id)
  if (!planId || isNaN(planId)) {
    errorMsg.value = '缺少套餐信息，请返回套餐列表重新选择'
    loading.value = false
    return
  }

  loading.value = true
  errorMsg.value = ''
  try {
    const res = await planApi.getDetail(planId)
    plan.value = res
    // 若用户已登录，并发尝试获取升级报价与抵扣详情
    if (getToken()) {
      try {
        const q = await checkoutApi.getQuote(planId)
        quote.value = q
      } catch (e) {
        console.warn('获取套餐升级折算报价失败:', e)
      }
    }
  } catch (err: any) {
    errorMsg.value = err.message || '获取套餐详情失败'
  } finally {
    loading.value = false
  }
}

async function fetchUser() {
  if (!userStore.token) return
  try {
    await userStore.fetchUserProfile()
  } catch (err) {
    console.warn('获取用户信息失败:', err)
  }
}

async function handleConfirmAndPay() {
  if (!plan.value || submitting.value || isSameSubscriptionConflict.value || isDowngradeBlocked.value) return

  if (!getToken()) {
    router.push(`/login?redirect=${encodeURIComponent(route.fullPath)}`)
    return
  }

  submitting.value = true
  try {
    const res = await checkoutApi.createOrder(plan.value.id)
    if (res && res.payUrl) {
      // 成功获得支付链接或完成免单核销，跳转收银台或控制台
      window.location.href = res.payUrl
    } else {
      alert('创建订单失败：未返回有效的支付链接')
    }
  } catch (err: any) {
    alert(err.message || '创建订单失败，请稍后重试')
  } finally {
    submitting.value = false
  }
}

onMounted(async () => {
  if (!getToken()) {
    router.push(`/login?redirect=${encodeURIComponent(route.fullPath)}`)
    return
  }
  await Promise.all([fetchUser(), fetchPlanDetail()])
})
</script>
