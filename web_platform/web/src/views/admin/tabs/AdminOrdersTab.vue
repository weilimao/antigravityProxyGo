<template>
  <div class="flex flex-col gap-6">
    <div class="flex items-center justify-between">
      <div>
        <h3 class="text-base font-bold text-white">交易订单与切单对账流水</h3>
        <p class="text-xs text-slate-400">实时展示来自用户选购及极客工坊切单收银台回调的订单状态</p>
      </div>
      <button type="button" class="btn-secondary text-xs" @click="fetchOrders">
        <span class="material-symbols-outlined text-16px">refresh</span>
        <span>刷新流水</span>
      </button>
    </div>

    <!-- 订单表格 -->
    <div class="glass-card overflow-hidden">
      <div class="overflow-x-auto">
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
              <td class="text-xs">{{ order.plan?.name || order.planId }}</td>
              <td class="font-mono font-bold text-emerald-400">¥{{ (order.amountCents / 100).toFixed(2) }}</td>
              <td>
                <span :class="order.status === 'paid' ? 'badge badge-emerald' : 'badge badge-amber'">
                  {{ order.status === 'paid' ? '已支付履约' : '待付款' }}
                </span>
              </td>
              <td class="font-mono text-xs text-slate-400">{{ order.relayOrderNo || '-' }}</td>
              <td class="text-xs text-slate-400">{{ order.paidAt ? formatTime(order.paidAt) : '-' }}</td>
              <td class="text-right">
                <button
                  v-if="order.status !== 'paid'"
                  type="button"
                  class="btn-primary text-xs py-1 px-2.5 cursor-pointer"
                  title="为未收到回调但已转账的用户手动激活"
                  @click="handleManualFulfill(order.orderNo)"
                >
                  手动核销补单
                </button>
                <span v-else class="text-xs text-slate-500">已完结</span>
              </td>
            </tr>
            <tr v-if="orders.length === 0">
              <td colspan="8" class="text-center py-8 text-slate-500 text-xs">暂无交易订单记录</td>
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

const orders = ref<any[]>([])

async function fetchOrders() {
  try {
    const res = await adminApi.listOrders(1, 50)
    orders.value = res.list || []
  } catch (err) {
    console.error(err)
  }
}

async function handleManualFulfill(orderNo: string) {
  if (!confirm(`确定要为订单 ${orderNo} 执行手动补单核销吗？系统将立即为该用户激活对应套餐。`)) return
  try {
    await adminApi.fulfillOrder(orderNo)
    alert('已成功补单并激活套餐！')
    fetchOrders()
  } catch (err: any) {
    alert(err.message || '补单失败')
  }
}

function formatTime(t: string | number) {
  if (!t) return '-'
  return new Date(t).toLocaleString()
}

onMounted(() => {
  fetchOrders()
})
</script>
