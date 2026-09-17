<template>
  <div class="flex flex-col gap-6">
    <div class="flex items-center justify-between">
      <div>
        <h3 class="text-base font-bold text-white">交易订单与切单对账流水</h3>
        <p class="text-xs text-slate-400">实时展示来自用户选购及极客工坊切单收银台回调的订单状态</p>
      </div>
      <button type="button" :disabled="loading" class="btn-secondary text-xs flex items-center gap-1.5" @click="fetchOrders">
        <span class="material-symbols-outlined text-16px" :class="{ 'animate-spin': loading }">refresh</span>
        <span>{{ loading ? '拉取中...' : '刷新流水' }}</span>
      </button>
    </div>

    <!-- 订单表格 -->
    <div class="glass-card overflow-hidden">
      <div v-if="loading" class="p-6">
        <LoadingSpinner text="正在拉取交易订单与流水..." />
      </div>
      <div v-else-if="orders.length === 0" class="p-12 text-center text-slate-400 text-xs">
        <span class="material-symbols-outlined text-32px text-slate-500 mb-2 block">receipt_long</span>
        暂无交易订单记录
      </div>
      <div v-else class="overflow-x-auto">
        <table class="table-dark">
          <thead>
            <tr>
              <th>内部订单号 (Order No)</th>
              <th>用户账号</th>
              <th>订阅套餐</th>
              <th>交易金额 (元)</th>
              <th>订单状态</th>
              <th>极客工坊切单号</th>
              <th>支付时间</th>
              <th class="text-right">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="order in orders" :key="order.id">
              <td class="font-mono text-xs text-slate-300">{{ order.orderNo }}</td>
              <td class="font-medium text-white">{{ order.user?.username || order.userId }}</td>
              <td class="text-xs">
                <div class="flex items-center gap-1.5">
                  <span>{{ order.plan?.name || order.planId }}</span>
                  <span :class="order.plan?.type === 'addon' ? 'badge badge-amber text-[10px] py-0 px-1' : 'badge badge-indigo text-[10px] py-0 px-1'">
                    {{ order.plan?.type === 'addon' ? '加油包' : '订阅' }}
                  </span>
                </div>
              </td>
              <td>
                <div class="flex flex-col items-start">
                  <span class="font-mono font-bold text-emerald-400">¥{{ (order.amountCents / 100).toFixed(2) }}</span>
                  <span v-if="order.discountCents > 0" class="text-[10px] text-purple-400 font-mono" :title="`原价 ¥${(order.originalAmountCents / 100).toFixed(2)}，立减 ¥${(order.discountCents / 100).toFixed(2)}`">
                    减 ¥{{ (order.discountCents / 100).toFixed(2) }}
                  </span>
                </div>
              </td>
              <td>
                <span
                  class="badge"
                  :class="{
                    'badge-emerald': order.status === 'paid',
                    'badge-amber': order.status === 'pending',
                    'badge-cyan': order.status === 'cancelled' || order.status === 'expired',
                  }"
                >
                  {{ getAdminOrderStatusText(order.status) }}
                </span>
              </td>
              <td class="font-mono text-xs text-slate-400">{{ order.relayOrderNo || '-' }}</td>
              <td class="text-xs text-slate-400">{{ order.paidAt ? formatTime(order.paidAt) : '-' }}</td>
              <td class="text-right">
                <button
                  v-if="order.status !== 'paid'"
                  type="button"
                  :disabled="fulfillingNo === order.orderNo"
                  class="btn-primary text-xs py-1 px-2.5 cursor-pointer inline-flex items-center gap-1 disabled:opacity-60"
                  title="为未收到回调但已转账的用户手动激活"
                  @click="handleManualFulfill(order.orderNo)"
                >
                  <span v-if="fulfillingNo === order.orderNo" class="material-symbols-outlined text-12px animate-spin">progress_activity</span>
                  <span>{{ fulfillingNo === order.orderNo ? '补单中...' : '手动核销补单' }}</span>
                </button>
                <span v-else class="text-xs text-slate-500">已完结</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { adminApi } from '../../../api/client'
import LoadingSpinner from '../../../components/common/LoadingSpinner.vue'

const orders = ref<any[]>([])
const loading = ref(false)
const fulfillingNo = ref<string | null>(null)

async function fetchOrders() {
  loading.value = true
  try {
    const res = await adminApi.listOrders(1, 50)
    orders.value = res.list || []
  } catch (err) {
    console.error(err)
  } finally {
    loading.value = false
  }
}

async function handleManualFulfill(orderNo: string) {
  if (!confirm(`确定要为订单 ${orderNo} 执行手动补单核销吗？系统将立即为该用户激活对应套餐。`)) return
  fulfillingNo.value = orderNo
  try {
    await adminApi.fulfillOrder(orderNo)
    alert('已成功补单并激活套餐！')
    await fetchOrders()
  } catch (err: any) {
    alert(err.message || '补单失败')
  } finally {
    fulfillingNo.value = null
  }
}

function formatTime(t: string | number) {
  if (!t) return '-'
  return new Date(t).toLocaleString()
}

function getAdminOrderStatusText(status: string): string {
  switch (status) {
    case 'paid': return '已支付履约'
    case 'pending': return '待付款'
    case 'cancelled': return '已取消'
    case 'expired': return '已超时'
    default: return status || '待付款'
  }
}

onMounted(() => {
  fetchOrders()
})
</script>
