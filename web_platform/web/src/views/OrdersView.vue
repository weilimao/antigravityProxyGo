<template>
  <div class="max-w-[1680px] mx-auto px-4 sm:px-6 lg:px-8 py-8">
    <!-- 头部引导与操作 -->
    <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-4 mb-8">
      <div>
        <div class="flex items-center gap-2 mb-1">
          <span class="material-symbols-outlined text-indigo-400 text-24px">receipt_long</span>
          <h1 class="text-2xl font-extrabold text-white tracking-tight">我的订单管理</h1>
        </div>
        <p class="text-xs text-slate-400">查看所有订阅套餐的支付历史、履约状态追踪与订单取消/继续支付操作</p>
      </div>

      <div class="flex items-center gap-2">
        <button
          type="button"
          :disabled="loading"
          class="btn-secondary text-xs flex items-center gap-1.5 cursor-pointer"
          @click="fetchOrders(true)"
        >
          <span class="material-symbols-outlined text-16px" :class="{ 'animate-spin': loading }">refresh</span>
          <span>{{ loading ? '刷新中...' : '刷新订单' }}</span>
        </button>

        <router-link to="/pricing" class="btn-primary text-xs py-2 px-3 flex items-center gap-1.5">
          <span class="material-symbols-outlined text-16px">add_shopping_cart</span>
          <span>选购新套餐</span>
        </router-link>
      </div>
    </div>

    <!-- 顶部数据概览胶囊 -->
    <div class="grid grid-cols-2 sm:grid-cols-4 gap-3 mb-6">
      <div class="glass-card p-4 flex items-center justify-between">
        <div>
          <span class="text-xs text-slate-400 block mb-0.5">全部订单</span>
          <span class="text-xl font-extrabold text-white">{{ orders.length }}</span>
        </div>
        <span class="material-symbols-outlined text-slate-500 text-24px">receipt</span>
      </div>

      <div class="glass-card p-4 flex items-center justify-between border-amber-500/20">
        <div>
          <span class="text-xs text-amber-400 block mb-0.5">待支付</span>
          <span class="text-xl font-extrabold text-amber-300">{{ pendingCount }}</span>
        </div>
        <span class="material-symbols-outlined text-amber-400 text-24px">pending_actions</span>
      </div>

      <div class="glass-card p-4 flex items-center justify-between border-emerald-500/20">
        <div>
          <span class="text-xs text-emerald-400 block mb-0.5">已生效 (已付款)</span>
          <span class="text-xl font-extrabold text-emerald-300">{{ paidCount }}</span>
        </div>
        <span class="material-symbols-outlined text-emerald-400 text-24px">check_circle</span>
      </div>

      <div class="glass-card p-4 flex items-center justify-between border-slate-700/40">
        <div>
          <span class="text-xs text-slate-400 block mb-0.5">已取消 / 已失效</span>
          <span class="text-xl font-extrabold text-slate-400">{{ cancelledCount }}</span>
        </div>
        <span class="material-symbols-outlined text-slate-500 text-24px">cancel</span>
      </div>
    </div>

    <!-- 订单列表卡片 -->
    <div class="glass-card overflow-hidden">
      <!-- 加载中 -->
      <div v-if="loading && orders.length === 0" class="p-12">
        <LoadingSpinner text="正在加载订单记录..." />
      </div>

      <!-- 空状态 -->
      <div v-else-if="orders.length === 0" class="p-16 text-center text-slate-400">
        <div class="w-14 h-14 rounded-2xl bg-indigo-600/10 border border-indigo-500/20 text-indigo-400 mx-auto flex items-center justify-center mb-3">
          <span class="material-symbols-outlined text-28px">receipt_long</span>
        </div>
        <h3 class="text-base font-bold text-white mb-1">暂无任何订单记录</h3>
        <p class="text-xs text-slate-400 mb-6">您目前尚未发起任何套餐订购，可前往套餐列表挑选专属算力包</p>
        <router-link to="/pricing" class="btn-primary text-xs py-2 px-4 inline-flex items-center gap-1.5">
          <span class="material-symbols-outlined text-16px">workspace_premium</span>
          <span>前往选购套餐</span>
        </router-link>
      </div>

      <!-- 表格内容 -->
      <div v-else class="overflow-x-auto">
        <table class="table-dark">
          <thead>
            <tr>
              <th>订单号 (Order No)</th>
              <th>订阅套餐</th>
              <th>订单金额</th>
              <th>订单状态与时效</th>
              <th>创建时间</th>
              <th class="text-right">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="order in orders" :key="order.id">
              <!-- 订单号 -->
              <td>
                <div class="flex items-center gap-1.5">
                  <span class="font-mono text-xs text-slate-300 font-semibold">{{ order.orderNo }}</span>
                  <button
                    type="button"
                    class="text-indigo-400 hover:text-indigo-300 transition-colors p-1 cursor-pointer"
                    title="复制订单号"
                    @click="copyOrderNo(order.orderNo)"
                  >
                    <span class="material-symbols-outlined text-14px">content_copy</span>
                  </button>
                </div>
              </td>

              <!-- 订阅套餐 -->
              <td>
                <div>
                  <div class="flex items-center gap-1.5 mb-0.5 flex-wrap">
                    <span
                      v-if="order.plan && (!order.plan.type || order.plan.type === 'subscription')"
                      :class="getTierBadgeClass(order.plan.tier)"
                      class="px-1.5 py-0.2 rounded text-[10px] font-mono font-extrabold uppercase shadow-sm shrink-0"
                    >
                      {{ formatTierName(order.plan.tier) }}
                    </span>
                    <span class="font-medium text-white text-xs">{{ order.planName || (order.plan && order.plan.name) || '会员套餐' }}</span>
                    <span :class="order.plan?.type === 'addon' ? 'badge badge-amber text-[10px] py-0 px-1' : 'badge badge-indigo text-[10px] py-0 px-1'">
                      {{ order.plan?.type === 'addon' ? '加油包' : '订阅方案' }}
                    </span>
                  </div>
                  <span class="text-[11px] text-slate-500">
                    {{ order.plan?.type === 'addon' ? '一次性永久叠加额度' : (order.durationDays === 0 ? '永久周期' : `${order.durationDays} 天周期`) }}
                  </span>
                </div>
              </td>

              <!-- 订单金额 -->
              <td>
                <div class="flex flex-col items-start">
                  <span class="font-mono font-bold text-sm text-emerald-400">
                    ¥{{ (order.amountCents / 100).toFixed(2) }}
                  </span>
                  <span v-if="order.discountCents > 0" class="text-[10px] text-purple-300 font-mono" :title="`原价 ¥${(order.originalAmountCents / 100).toFixed(2)}，折算立减 ¥${(order.discountCents / 100).toFixed(2)}`">
                    立减 -¥{{ (order.discountCents / 100).toFixed(2) }}
                  </span>
                </div>
              </td>

              <!-- 状态与倒计时 -->
              <td>
                <div class="flex flex-col items-start gap-1">
                  <div class="flex items-center gap-1.5">
                    <span
                      class="badge"
                      :class="{
                        'badge-amber': order.status === 'pending',
                        'badge-emerald': order.status === 'paid',
                        'badge-cyan': order.status === 'cancelled' || order.status === 'expired',
                      }"
                    >
                      {{ getStatusText(order.status) }}
                    </span>
                  </div>

                  <!-- 待支付状态下的 15 分钟倒计时 -->
                  <div
                    v-if="order.status === 'pending' && (order.remainSeconds ?? 0) > 0"
                    class="inline-flex items-center gap-1 px-2 py-0.5 rounded text-[10px] font-mono font-medium"
                    :class="(order.remainSeconds ?? 0) <= 180 ? 'bg-rose-500/15 text-rose-300 border border-rose-500/30 animate-pulse' : 'bg-amber-500/15 text-amber-300 border border-amber-500/30'"
                  >
                    <span class="material-symbols-outlined text-[12px]">schedule</span>
                    <span>剩余 {{ formatCountdown(order.remainSeconds) }} 自动取消</span>
                  </div>
                </div>
              </td>

              <!-- 创建时间 -->
              <td class="text-xs text-slate-400 font-mono">
                {{ formatTime(order.createdAt) }}
              </td>

              <!-- 操作栏 -->
              <td class="text-right">
                <div class="flex items-center justify-end gap-2">
                  <!-- 待支付操作 -->
                  <template v-if="order.status === 'pending'">
                    <button
                      type="button"
                      :disabled="payingNo === order.orderNo"
                      class="btn-primary text-xs py-1 px-2.5 cursor-pointer inline-flex items-center gap-1 disabled:opacity-60"
                      @click="handleGoPay(order)"
                    >
                      <span v-if="payingNo === order.orderNo" class="material-symbols-outlined text-12px animate-spin">progress_activity</span>
                      <span v-else class="material-symbols-outlined text-14px">payment</span>
                      <span>{{ payingNo === order.orderNo ? '唤起中...' : '去支付' }}</span>
                    </button>

                    <button
                      type="button"
                      :disabled="cancellingNo === order.orderNo"
                      class="btn-secondary text-xs py-1 px-2 text-rose-400 hover:text-rose-300 hover:border-rose-500/40 cursor-pointer inline-flex items-center gap-1 disabled:opacity-60"
                      @click="handleCancelOrder(order)"
                    >
                      <span v-if="cancellingNo === order.orderNo" class="material-symbols-outlined text-12px animate-spin">progress_activity</span>
                      <span>{{ cancellingNo === order.orderNo ? '取消中...' : '取消订单' }}</span>
                    </button>
                  </template>

                  <!-- 已支付 -->
                  <template v-else-if="order.status === 'paid'">
                    <span class="text-xs text-emerald-400 font-medium flex items-center gap-1 justify-end">
                      <span class="material-symbols-outlined text-14px">verified</span>
                      <span>已履约生效</span>
                    </span>
                  </template>

                  <!-- 已取消 -->
                  <template v-else>
                    <router-link
                      to="/pricing"
                      class="text-xs text-slate-400 hover:text-indigo-400 transition-colors inline-flex items-center gap-1"
                    >
                      <span class="material-symbols-outlined text-14px">refresh</span>
                      <span>重新选购</span>
                    </router-link>
                  </template>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { orderApi, getToken } from '../api/client'
import LoadingSpinner from '../components/common/LoadingSpinner.vue'

interface OrderItem {
  id: number
  orderNo: string
  userId: number
  planId: number
  planName: string
  durationDays: number
  amountCents: number
  originalAmountCents?: number
  discountCents?: number
  upgradeFromPlanId?: number
  upgradeFromPlan?: any
  status: string
  payUrl: string
  paidAt?: string
  createdAt: string
  expireAt?: string
  expireSeconds?: number
  remainSeconds?: number
  plan?: any
}

const router = useRouter()
const orders = ref<OrderItem[]>([])
const loading = ref(true)
const payingNo = ref<string | null>(null)
const cancellingNo = ref<string | null>(null)
let countdownTimer: ReturnType<typeof setInterval> | null = null

const pendingCount = computed(() => orders.value.filter(o => o.status === 'pending').length)
const paidCount = computed(() => orders.value.filter(o => o.status === 'paid').length)
const cancelledCount = computed(() => orders.value.filter(o => o.status === 'cancelled' || o.status === 'expired').length)

function getStatusText(status: string): string {
  switch (status) {
    case 'pending': return '待支付'
    case 'paid': return '已生效'
    case 'cancelled': return '已取消'
    case 'expired': return '已超时'
    default: return status
  }
}

function formatCountdown(sec?: number): string {
  if (!sec || sec <= 0) return '00:00'
  const m = Math.floor(sec / 60).toString().padStart(2, '0')
  const s = (sec % 60).toString().padStart(2, '0')
  return `${m}:${s}`
}

function formatTime(t?: string | number): string {
  if (!t) return '-'
  return new Date(t).toLocaleString()
}

async function copyOrderNo(no: string) {
  try {
    await navigator.clipboard.writeText(no)
    alert(`订单号 ${no} 已复制到剪贴板`)
  } catch {
    alert(`复制失败，订单号为: ${no}`)
  }
}

function initCountdowns() {
  let hasPending = false
  const nowMs = Date.now()

  for (const o of orders.value) {
    if (o.status === 'pending') {
      if (o.expireSeconds !== undefined && o.expireSeconds !== null) {
        o.remainSeconds = Math.max(0, o.expireSeconds)
      } else if (o.expireAt) {
        const expMs = new Date(o.expireAt).getTime()
        o.remainSeconds = Math.max(0, Math.floor((expMs - nowMs) / 1000))
      } else {
        const createMs = new Date(o.createdAt).getTime()
        const expMs = createMs + 15 * 60 * 1000
        o.remainSeconds = Math.max(0, Math.floor((expMs - nowMs) / 1000))
      }

      if (o.remainSeconds > 0) {
        hasPending = true
      } else {
        o.status = 'cancelled'
      }
    }
  }

  if (hasPending) {
    startTimer()
  } else {
    stopTimer()
  }
}

function startTimer() {
  if (countdownTimer) return
  countdownTimer = setInterval(() => {
    let hasActivePending = false
    let needRefresh = false

    for (const o of orders.value) {
      if (o.status === 'pending') {
        if (o.remainSeconds && o.remainSeconds > 0) {
          o.remainSeconds -= 1
          if (o.remainSeconds > 0) {
            hasActivePending = true
          } else {
            o.status = 'cancelled'
            needRefresh = true
          }
        } else {
          o.status = 'cancelled'
          needRefresh = true
        }
      }
    }

    if (needRefresh) {
      fetchOrders(false)
    }

    if (!hasActivePending) {
      stopTimer()
    }
  }, 1000)
}

function stopTimer() {
  if (countdownTimer) {
    clearInterval(countdownTimer)
    countdownTimer = null
  }
}

async function fetchOrders(showLoading = true) {
  if (showLoading) loading.value = true
  try {
    const res = await orderApi.listMyOrders(1, 50)
    orders.value = (res.list || []) as OrderItem[]
    initCountdowns()
  } catch (err: any) {
    console.error('获取订单列表失败:', err)
  } finally {
    if (showLoading) loading.value = false
  }
}

async function handleGoPay(order: OrderItem) {
  payingNo.value = order.orderNo
  try {
    const res = await orderApi.getPayUrl(order.orderNo)
    if (res && res.payUrl) {
      window.location.href = res.payUrl
    } else if (order.payUrl) {
      window.location.href = order.payUrl
    } else {
      alert('未获取到支付地址，请重试或取消后重新订购')
    }
  } catch (err: any) {
    alert(err.message || '获取支付跳转地址失败')
    await fetchOrders(false)
  } finally {
    payingNo.value = null
  }
}

async function handleCancelOrder(order: OrderItem) {
  const confirmed = confirm(`确定要取消订单「${order.orderNo}」吗？取消后该订单将无法继续支付。`)
  if (!confirmed) return

  cancellingNo.value = order.orderNo
  try {
    await orderApi.cancelOrder(order.orderNo)
    alert('订单已成功取消')
    await fetchOrders(false)
  } catch (err: any) {
    alert(err.message || '取消订单失败')
  } finally {
    cancellingNo.value = null
  }
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

onMounted(() => {
  if (!getToken()) {
    router.push('/login?redirect=/orders')
    return
  }
  fetchOrders(true)
})

onUnmounted(() => {
  stopTimer()
})
</script>
