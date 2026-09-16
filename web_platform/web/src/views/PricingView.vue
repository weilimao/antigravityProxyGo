<template>
  <div class="max-w-6xl mx-auto px-4 py-8">
    <div class="text-center mb-10">
      <div class="inline-flex items-center gap-2 px-3 py-1 rounded-full bg-indigo-500/10 border border-indigo-500/20 text-indigo-400 text-xs font-semibold mb-3">
        <span class="material-symbols-outlined text-14px">workspace_premium</span>
        <span>商业化订阅中心</span>
      </div>
      <h2 class="text-3xl font-extrabold text-white tracking-tight">精选会员套餐方案</h2>
      <p class="text-sm text-slate-400 mt-2 max-w-xl mx-auto">
        按需选择专属大模型算力包，畅享高性能并发与首字极速响应能力。
      </p>
    </div>

    <!-- 错误信息提示 -->
    <div v-if="errorMsg" class="max-w-md mx-auto mb-6 p-3 rounded-lg bg-rose-500/10 border border-rose-500/30 text-rose-400 text-xs flex items-center gap-2">
      <span class="material-symbols-outlined text-16px">error</span>
      <span>{{ errorMsg }}</span>
    </div>

    <!-- 加载中 -->
    <LoadingSpinner v-if="loading" text="正在加载精选会员套餐方案..." />

    <!-- 套餐网格卡片 -->
    <div v-else class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
      <div
        v-for="plan in plans"
        :key="plan.id"
        class="glass-card p-6 flex flex-col justify-between relative overflow-hidden transition-all hover:-translate-y-1 hover:border-indigo-500/40 hover:shadow-lg hover:shadow-indigo-500/10"
      >
        <div>
          <!-- 头部名称与描述 -->
          <div class="flex items-center justify-between mb-3">
            <h3 class="text-lg font-bold text-white">{{ plan.name }}</h3>
            <span class="badge badge-indigo">
              {{ plan.durationDays === 0 ? '永久有效' : `${plan.durationDays} 天周期` }}
            </span>
          </div>
          <p class="text-xs text-slate-400 mb-6 min-h-36px">{{ plan.description || '解锁高可用模型调用权限与专属算力配额' }}</p>

          <!-- 价格栏 -->
          <div class="flex items-baseline gap-1 mb-6 pb-6 border-b border-slate-800">
            <span class="text-xs text-slate-400">¥</span>
            <span class="text-4xl font-extrabold text-white tracking-tight">{{ (plan.priceCents / 100).toFixed(2) }}</span>
            <span class="text-xs text-slate-400 ml-1">/ {{ plan.durationDays === 0 ? '永久' : `${plan.durationDays}天` }}</span>
          </div>

          <!-- 核心权益亮点 -->
          <div class="flex flex-col gap-3 mb-6">
            <!-- 额度限制亮点 -->
            <div class="flex items-center gap-2 text-xs text-slate-300">
              <span class="material-symbols-outlined text-amber-400 text-16px">toll</span>
              <span>额度限制: <strong class="text-white">{{ formatTokenLimit(plan.tokenLimit) }}</strong></span>
            </div>

            <div class="flex items-center gap-2 text-xs text-slate-300">
              <span class="material-symbols-outlined text-emerald-400 text-16px">speed</span>
              <span>速率限制: <strong class="text-white">{{ plan.rateLimit || 30 }}</strong> 次/分钟 (RPM)</span>
            </div>

            <!-- 视觉分析多模态能力亮点 -->
            <div class="flex items-center gap-2 text-xs text-cyan-300 font-medium">
              <span class="material-symbols-outlined text-cyan-400 text-16px">visibility</span>
              <span>视觉能力: <strong class="text-cyan-200">支持多模态视觉分析 (截屏/图片解析)</strong></span>
            </div>

            <!-- 当套餐包含 auto 时，在速率限制下方新增独立亮点行 -->
            <div v-if="(plan.allowedModels || []).includes('auto')" class="flex items-center gap-2 text-xs text-amber-300 font-medium">
              <span class="material-symbols-outlined text-amber-400 text-16px">bolt</span>
              <span>核心特性: <strong class="text-amber-200">支持 Auto 智能调度模型</strong></span>
            </div>

            <!-- 包含模型清单展示 -->
            <div>
              <div class="text-xs font-semibold text-slate-300 mb-2 flex items-center gap-1.5">
                <span class="material-symbols-outlined text-indigo-400 text-16px">hub</span>
                <span>包含并授权的模型清单:</span>
              </div>
              <div class="flex flex-wrap gap-1.5 max-h-120px overflow-y-auto pr-1">
                <span
                  v-for="model in (plan.allowedModels || [])"
                  :key="model"
                  class="badge text-10px font-mono"
                  :class="model === 'auto' ? 'badge-amber font-bold shadow-sm' : 'badge-cyan'"
                >
                  {{ model === 'auto' ? '⚡ 支持 auto 模型' : model }}
                </span>
                <span v-if="!plan.allowedModels || plan.allowedModels.length === 0" class="text-11px text-slate-500">
                  全部公共模型开放
                </span>
              </div>
            </div>

            <!-- Auto 模型专属包含模型说明板块 -->
            <div
              v-if="(plan.allowedModels || []).includes('auto') && plan.autoModels && plan.autoModels.length > 0"
              class="p-3 rounded-xl bg-gradient-to-br from-amber-500/15 via-amber-500/10 to-amber-600/5 border border-amber-500/30 shadow-sm"
            >
              <div class="text-xs font-bold text-amber-300 flex items-center gap-1.5 mb-2">
                <span class="material-symbols-outlined text-16px text-amber-400">bolt</span>
                <span>Auto 智能竞速实际包含模型 ({{ plan.autoModels.length }} 款):</span>
              </div>
              <div class="flex flex-wrap gap-1.5 max-h-100px overflow-y-auto pr-0.5">
                <span
                  v-for="am in plan.autoModels"
                  :key="am"
                  class="px-2 py-0.5 rounded text-[10px] font-mono font-medium bg-black/40 text-amber-200 border border-amber-500/30 flex items-center gap-1"
                >
                  <span class="material-symbols-outlined text-[10px] text-amber-400">check_circle</span>
                  <span>{{ am }}</span>
                </span>
              </div>
              <p class="text-[10px] text-amber-200/70 mt-2">
                * 调用 auto 模型时将根据号池健康度与响应速度在上述模型池中自动择优调度
              </p>
            </div>
          </div>
        </div>

        <!-- 购买确认按钮 -->
        <button
          type="button"
          class="btn-primary w-full py-2.5 mt-4 text-xs cursor-pointer flex items-center justify-center gap-1.5 shadow-md shadow-indigo-600/30 hover:shadow-indigo-500/40"
          @click="handleSubscribe(plan)"
        >
          <span class="material-symbols-outlined text-16px">shopping_cart_checkout</span>
          <span>立即选购并确认订单 →</span>
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { planApi, getToken } from '../api/client'
import { useRouter } from 'vue-router'
import LoadingSpinner from '../components/common/LoadingSpinner.vue'

const router = useRouter()
const plans = ref<any[]>([])
const loading = ref(true)
const errorMsg = ref('')

function formatTokenLimit(val?: number): string {
  if (!val || val <= 0) return '不限额度'
  if (val >= 100000000) return (val / 100000000).toFixed(val % 100000000 === 0 ? 0 : 2) + ' 亿 Tokens'
  if (val >= 10000) return (val / 10000).toFixed(val % 10000 === 0 ? 0 : 1) + ' 万 Tokens'
  if (val >= 1000000) return (val / 1000000).toFixed(1) + 'M Tokens'
  if (val >= 1000) return (val / 1000).toFixed(0) + 'K Tokens'
  return `${val.toLocaleString()} Tokens`
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

function handleSubscribe(plan: any) {
  if (!getToken()) {
    router.push(`/login?redirect=${encodeURIComponent(`/checkout/confirm?plan_id=${plan.id}`)}`)
    return
  }
  // 对标 ProxySubForClash：进入订单确认页核对后再前往支付
  router.push(`/checkout/confirm?plan_id=${plan.id}`)
}

onMounted(() => {
  fetchPlans()
})
</script>
