<template>
  <div class="flex flex-col gap-6">
    <div class="flex items-center justify-between">
      <div>
        <h3 class="text-base font-bold text-white">套餐订阅方案列表</h3>
        <p class="text-xs text-slate-400">配置商业化售卖套餐、订阅定价及各套餐内精确授权的模型白名单</p>
      </div>
      <button type="button" class="btn-primary text-xs cursor-pointer" @click="openCreateModal">
        <span class="material-symbols-outlined text-16px">add</span>
        <span>新增套餐</span>
      </button>
    </div>

    <!-- 套餐表格 -->
    <div class="glass-card overflow-hidden">
      <div v-if="loading" class="p-6">
        <LoadingSpinner text="正在加载套餐方案列表..." />
      </div>
      <div v-else-if="plans.length === 0" class="p-12 text-center text-slate-400 text-xs">
        <span class="material-symbols-outlined text-32px text-slate-500 mb-2 block">layers_clear</span>
        暂无套餐方案，点击右上角【新增套餐】创建
      </div>
      <div v-else class="overflow-x-auto">
        <table class="table-dark">
          <thead>
            <tr>
              <th>套餐名称</th>
              <th>类型</th>
              <th>售价 (元)</th>
              <th>有效时长</th>
              <th>包含模型范围 (Allowed Models)</th>
              <th>Token 限制</th>
              <th>限速 (RPM)</th>
              <th>状态</th>
              <th class="text-right">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="plan in plans" :key="plan.id">
              <td class="font-bold text-white">
                <div class="flex items-center gap-1.5 flex-wrap">
                  <span v-if="plan.type !== 'addon'" :class="getTierBadgeClass(plan.tier)" class="badge text-[10px] font-mono font-bold">
                    {{ formatTierName(plan.tier) }}
                  </span>
                  <span>{{ plan.name }}</span>
                </div>
                <div class="text-11px text-slate-400">{{ plan.description }}</div>
              </td>
              <td>
                <div class="flex flex-col gap-1 items-start">
                  <span :class="plan.type === 'addon' ? 'badge badge-amber' : 'badge badge-indigo'">
                    {{ plan.type === 'addon' ? '⚡ 加油包' : '👑 会员订阅' }}
                  </span>
                </div>
              </td>
              <td class="font-mono font-bold text-indigo-300">
                ¥{{ (plan.priceCents / 100).toFixed(2) }}
              </td>
              <td>
                <span class="badge badge-indigo">
                  {{ plan.durationDays === 0 ? '永久有效' : `${plan.durationDays} 天` }}
                </span>
              </td>
              <td>
                <div class="flex flex-wrap gap-1 max-w-sm">
                  <span
                    v-for="m in (plan.allowedModels || [])"
                    :key="m"
                    class="badge text-10px font-mono"
                    :class="m === 'auto' ? 'badge-amber font-bold' : 'badge-cyan'"
                  >
                    {{ m === 'auto' ? '⚡ 支持 auto 模型' : m }}
                  </span>
                  <span v-if="!plan.allowedModels || plan.allowedModels.length === 0" class="text-11px text-slate-500">
                    全量公共模型
                  </span>
                </div>
                <!-- 展示该套餐配置的 Auto 包含模型说明 -->
                <div v-if="(plan.allowedModels || []).includes('auto') && plan.autoModels && plan.autoModels.length > 0" class="mt-1.5 pt-1.5 border-t border-slate-800/80">
                  <div class="text-[10px] text-amber-300/90 font-semibold flex items-center gap-1 mb-1">
                    <span class="material-symbols-outlined text-[12px] text-amber-400">bolt</span>
                    <span>Auto 包含模型说明 ({{ plan.autoModels.length }}款):</span>
                  </div>
                  <div class="flex flex-wrap gap-1">
                    <span
                      v-for="am in plan.autoModels"
                      :key="am"
                      class="px-1.5 py-0.5 rounded text-[10px] font-mono bg-amber-500/15 text-amber-200 border border-amber-500/30"
                    >
                      {{ am }}
                    </span>
                  </div>
                </div>
              </td>
              <td class="font-mono text-xs">
                <span v-if="!plan.tokenLimit || plan.tokenLimit <= 0" class="badge badge-emerald">不限额度</span>
                <span v-else class="text-indigo-300 font-semibold">{{ formatTokenDisplay(plan.tokenLimit) }}</span>
              </td>
              <td class="font-mono text-xs">{{ plan.rateLimit }}</td>
              <td>
                <span :class="plan.status === 'active' ? 'badge badge-emerald' : 'badge badge-rose'">
                  {{ plan.status === 'active' ? '上架中' : '已下架' }}
                </span>
              </td>
              <td class="text-right whitespace-nowrap">
                <button type="button" class="btn-secondary text-xs mr-2 cursor-pointer" @click="openEditModal(plan)">
                  编辑
                </button>
                <button
                  type="button"
                  :disabled="deletingId === plan.id"
                  class="btn-danger cursor-pointer inline-flex items-center gap-1 disabled:opacity-50"
                  @click="handleDeletePlan(plan.id)"
                >
                  <span v-if="deletingId === plan.id" class="material-symbols-outlined text-12px animate-spin">progress_activity</span>
                  <span>{{ deletingId === plan.id ? '删除中...' : '删除' }}</span>
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- 新建/编辑套餐弹窗 -->
    <div v-if="showModal" class="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/70 backdrop-blur-sm overflow-y-auto">
      <div class="glass-card-glow w-full max-w-2xl p-6 max-h-[90vh] overflow-y-auto my-8">
        <div class="flex items-center justify-between mb-4 pb-3 border-b border-slate-800">
          <h3 class="text-base font-bold text-white">{{ isEditing ? '编辑套餐' : '新增套餐' }}</h3>
          <span class="material-symbols-outlined text-slate-400 hover:text-white cursor-pointer" @click="showModal = false">close</span>
        </div>

        <form @submit.prevent="savePlan" class="flex flex-col gap-4 text-xs">
          <!-- 套餐类型选择 -->
          <div class="p-3 bg-slate-900/80 rounded-xl border border-slate-800 flex flex-col gap-2">
            <label class="block text-slate-200 font-bold">套餐类型</label>
            <div class="grid grid-cols-2 gap-3">
              <label
                class="flex items-center gap-2 p-2.5 rounded-lg border cursor-pointer transition-all"
                :class="form.type === 'subscription' ? 'bg-indigo-600/20 border-indigo-500 text-white' : 'bg-slate-950/40 border-slate-800 text-slate-400 hover:border-slate-700'"
              >
                <input type="radio" value="subscription" v-model="form.type" class="hidden" />
                <span class="material-symbols-outlined text-18px" :class="form.type === 'subscription' ? 'text-indigo-400' : 'text-slate-500'">verified</span>
                <div>
                  <div class="font-bold text-xs">👑 会员订阅套餐</div>
                  <div class="text-[10px] text-slate-400">按周期生效（一月只能买一次，到期才能续费）</div>
                </div>
              </label>

              <label
                class="flex items-center gap-2 p-2.5 rounded-lg border cursor-pointer transition-all"
                :class="form.type === 'addon' ? 'bg-amber-600/20 border-amber-500 text-white' : 'bg-slate-950/40 border-slate-800 text-slate-400 hover:border-slate-700'"
              >
                <input type="radio" value="addon" v-model="form.type" class="hidden" />
                <span class="material-symbols-outlined text-18px" :class="form.type === 'addon' ? 'text-amber-400' : 'text-slate-500'">bolt</span>
                <div>
                  <div class="font-bold text-xs">⚡ Token 加油包</div>
                  <div class="text-[10px] text-slate-400">一次性购买叠加算力，叫“购买”非订阅，随时可买</div>
                </div>
              </label>
            </div>
            <p v-if="form.type === 'addon'" class="text-[10px] text-amber-300/80 bg-amber-500/10 p-1.5 rounded border border-amber-500/20">
              💡 提示：Token 加油包购买成功后直接累加用户的 Token 限额，不篡改其基础会员订阅方案和到期时间。
            </p>
          </div>

          <!-- 订阅等级选择（仅限会员订阅套餐） -->
          <div v-if="form.type === 'subscription'" class="p-3 bg-slate-900/80 rounded-xl border border-slate-800 flex flex-col gap-2">
            <div class="flex items-center justify-between">
              <label class="block text-slate-200 font-bold">订阅层级 (Tier 等级)</label>
              <span class="text-[10px] text-indigo-300 bg-indigo-500/10 px-2 py-0.5 rounded border border-indigo-500/20">
                升级阶梯: Pro &lt; MAX &lt; MAX+ &lt; MAX++（仅支持自下而上升级，严禁反向降级）
              </span>
            </div>
            <div class="grid grid-cols-2 sm:grid-cols-4 gap-2">
              <label
                v-for="tierOpt in tierOptions"
                :key="tierOpt.value"
                class="flex flex-col items-center justify-center p-2.5 rounded-lg border cursor-pointer transition-all text-center"
                :class="form.tier === tierOpt.value ? tierOpt.activeClass : 'bg-slate-950/40 border-slate-800 text-slate-400 hover:border-slate-700'"
              >
                <input type="radio" :value="tierOpt.value" v-model="form.tier" class="hidden" />
                <div class="font-extrabold text-xs tracking-wide" :class="tierOpt.textClass">{{ tierOpt.label }}</div>
                <div class="text-[10px] text-slate-400 mt-0.5">{{ tierOpt.desc }}</div>
              </label>
            </div>
          </div>

          <div class="grid grid-cols-2 gap-4">
            <div>
              <label class="block text-slate-300 font-semibold mb-1">套餐名称</label>
              <input v-model="form.name" type="text" required class="input-dark w-full" :placeholder="form.type === 'addon' ? '如: 5000万 Token 算力加油包' : '如: Pro 开发者版'" />
            </div>
            <div>
              <label class="block text-slate-300 font-semibold mb-1">售价 (单位: 元，最低 1 元)</label>
              <input v-model.number="formPriceYuan" type="number" min="1" step="0.01" required class="input-dark w-full" placeholder="39.00" />
            </div>
          </div>

          <div class="grid grid-cols-2 sm:grid-cols-4 gap-4">
            <div>
              <label class="block text-slate-300 font-semibold mb-1">有效天数 (0=永久)</label>
              <input v-model.number="form.durationDays" type="number" required class="input-dark w-full" placeholder="30" />
            </div>
            <div>
              <label class="block text-slate-300 font-semibold mb-1">Token 限制 (0=不限)</label>
              <input v-model.number="form.tokenLimit" type="number" min="0" step="10000" class="input-dark w-full" placeholder="0 表示不限制" />
              <div class="text-[10px] text-slate-400 mt-0.5 truncate">
                {{ form.tokenLimit > 0 ? `约 ${formatTokenDisplay(form.tokenLimit)} Tokens` : '无限制' }}
              </div>
            </div>
            <div>
              <label class="block text-slate-300 font-semibold mb-1">限速 RPM (次/分)</label>
              <input v-model.number="form.rateLimit" type="number" required class="input-dark w-full" placeholder="30" />
            </div>
            <div>
              <label class="block text-slate-300 font-semibold mb-1">状态</label>
              <select v-model="form.status" class="input-dark w-full cursor-pointer">
                <option value="active">上架 (active)</option>
                <option value="inactive">下架 (inactive)</option>
              </select>
            </div>
          </div>

          <div>
            <label class="block text-slate-300 font-semibold mb-1">套餐简述</label>
            <input v-model="form.description" type="text" class="input-dark w-full" placeholder="适合个人日常编程..." />
          </div>

          <!-- 模型权限白名单多选配置 -->
          <div class="p-3 bg-slate-900/70 rounded-xl border border-slate-800 flex flex-col gap-3">
            <div class="flex items-center justify-between">
              <label class="text-slate-200 font-bold flex items-center gap-1.5">
                <span class="material-symbols-outlined text-indigo-400 text-16px">hub</span>
                <span>套餐授权包含的模型 (Allowed Models，已选 {{ form.allowedModels.length }} 个)</span>
              </label>
              <span class="text-11px text-slate-500">支持搜索、回车自定义模型或点击标签勾选</span>
            </div>

            <!-- 通用模型搜索多选组件 -->
            <ModelSearchSelect
              :multiple="true"
              :model-ids="form.allowedModels"
              :options="availableModels"
              :allow-custom="true"
              placeholder="搜索并多选授权模型，或键盘输入自定义模型按回车添加..."
              @update:model-ids="(val) => form.allowedModels = val"
            />

            <!-- 若已选 auto，配置该套餐专属的 Auto 包含模型说明清单（支持自主多选组件勾选、输入或从系统候选一键填入） -->
            <div v-if="hasAutoSelected" class="p-3.5 rounded-xl bg-gradient-to-b from-amber-500/10 to-amber-500/5 border border-amber-500/30 flex flex-col gap-2.5 transition-all shadow-sm">
              <div class="flex items-center justify-between gap-2">
                <div class="flex items-center gap-1.5">
                  <span class="material-symbols-outlined text-18px text-amber-400">bolt</span>
                  <label class="text-xs font-bold text-amber-300">
                    Auto 实际包含模型说明 (已配置 {{ form.autoModels.length }} 款)
                  </label>
                </div>
                <div class="flex items-center gap-2">
                  <button
                    v-if="autoCandidateModels.length > 0"
                    type="button"
                    class="text-[11px] font-semibold px-2.5 py-1 rounded bg-amber-500/20 hover:bg-amber-500/30 text-amber-200 border border-amber-500/40 cursor-pointer flex items-center gap-1 transition-all"
                    @click="importSystemAutoCandidates"
                    title="将系统全局 Auto 候选模型一键带入本套餐说明"
                  >
                    <span class="material-symbols-outlined text-13px">download</span>
                    <span>一键填入系统候选 ({{ autoCandidateModels.length }}个)</span>
                  </button>
                  <button
                    v-if="form.autoModels.length > 0"
                    type="button"
                    class="text-[11px] text-slate-400 hover:text-rose-300 cursor-pointer flex items-center gap-0.5"
                    @click="form.autoModels = []"
                    title="清空 Auto 包含模型说明"
                  >
                    <span class="material-symbols-outlined text-13px">clear_all</span>
                    <span>清空</span>
                  </button>
                </div>
              </div>
              <p class="text-[11px] text-amber-200/70">
                可自主搜索勾选或回车添加模型；保存后将在购买页作为此套餐的 Auto 包含模型明确告知用户。
              </p>

              <!-- Auto 包含模型的多选组件 -->
              <ModelSearchSelect
                :multiple="true"
                :model-ids="form.autoModels"
                :options="availableModelsForAuto"
                :allow-custom="true"
                placeholder="搜索并多选 Auto 包含的具体模型，或键盘输入自定义模型按回车添加..."
                @update:model-ids="(val) => form.autoModels = val"
              />
            </div>

          </div>

          <div class="flex items-center justify-end gap-3 pt-2">
            <button type="button" class="btn-secondary text-xs" :disabled="saving" @click="showModal = false">取消</button>
            <button type="submit" :disabled="saving" class="btn-primary text-xs flex items-center gap-1.5 shadow-md shadow-indigo-600/30 disabled:opacity-60">
              <span v-if="saving" class="material-symbols-outlined text-14px animate-spin">progress_activity</span>
              <span>{{ saving ? '保存中...' : '保存套餐' }}</span>
            </button>
          </div>
        </form>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import { planApi, mappingApi, autoApi } from '../../../api/client'
import ModelSearchSelect from '../../../components/common/ModelSearchSelect.vue'
import LoadingSpinner from '../../../components/common/LoadingSpinner.vue'

const plans = ref<any[]>([])
const loading = ref(false)
const saving = ref(false)
const deletingId = ref<number | null>(null)
const availableModels = ref<string[]>([])
const autoConfig = ref<any>(null)
const showModal = ref(false)
const isEditing = ref(false)
const editingId = ref<number | null>(null)
const formPriceYuan = ref<number>(39)

const form = reactive({
  name: '',
  type: 'subscription',
  tier: 'pro',
  description: '',
  durationDays: 30,
  tokenLimit: 0,
  rateLimit: 30,
  status: 'active',
  allowedModels: [] as string[],
  autoModels: [] as string[],
})

const tierOptions = [
  { value: 'pro', label: 'Pro', desc: '入门基础版 (L1)', activeClass: 'bg-blue-600/20 border-blue-500 text-white ring-1 ring-blue-500/30', textClass: 'text-blue-300' },
  { value: 'max', label: 'MAX', desc: '进阶高频版 (L2)', activeClass: 'bg-indigo-600/20 border-indigo-500 text-white ring-1 ring-indigo-500/30', textClass: 'text-indigo-300' },
  { value: 'max+', label: 'MAX+', desc: '专业旗舰版 (L3)', activeClass: 'bg-purple-600/20 border-purple-500 text-white ring-1 ring-purple-500/30', textClass: 'text-purple-300' },
  { value: 'max++', label: 'MAX++', desc: '顶级终极版 (L4)', activeClass: 'bg-amber-600/20 border-amber-500 text-white ring-1 ring-amber-500/30', textClass: 'text-amber-300' },
]

function formatTierName(tier?: string): string {
  const t = (tier || 'pro').toLowerCase()
  if (t === 'max++') return 'MAX++'
  if (t === 'max+') return 'MAX+'
  if (t === 'max') return 'MAX'
  return 'Pro'
}

function getTierBadgeClass(tier?: string): string {
  const t = (tier || 'pro').toLowerCase()
  if (t === 'max++') return 'bg-amber-500/20 text-amber-300 border border-amber-500/40'
  if (t === 'max+') return 'bg-purple-500/20 text-purple-300 border border-purple-500/40'
  if (t === 'max') return 'bg-indigo-500/20 text-indigo-300 border border-indigo-500/40'
  return 'bg-blue-500/20 text-blue-300 border border-blue-500/40'
}

function formatTokenDisplay(val: number): string {
  if (!val || val <= 0) return '不限额度'
  if (val >= 100000000) return (val / 100000000).toFixed(val % 100000000 === 0 ? 0 : 2) + ' 亿'
  if (val >= 10000) return (val / 10000).toFixed(val % 10000 === 0 ? 0 : 1) + ' 万'
  if (val >= 1000000) return (val / 1000000).toFixed(1) + 'M'
  if (val >= 1000) return (val / 1000).toFixed(0) + 'K'
  return val.toLocaleString()
}

const hasAutoSelected = computed(() => form.allowedModels.includes('auto'))
const availableModelsForAuto = computed(() => availableModels.value.filter(m => m !== 'auto'))
const autoCandidateModels = computed(() => {
  if (!autoConfig.value) return []
  return Array.isArray(autoConfig.value.candidateModels) ? autoConfig.value.candidateModels : []
})

async function fetchPlans() {
  loading.value = true
  try {
    plans.value = await planApi.adminList()
  } catch (err) {
    console.error(err)
  } finally {
    loading.value = false
  }
}

async function fetchAvailableModels() {
  try {
    // 强制使用系统图二配置的模型映射列表 (纯净对外 Client Models，杜绝底层原始杂乱模型)
    const clientList = await mappingApi.getMappingClientModels()
    if (Array.isArray(clientList) && clientList.length > 0) {
      availableModels.value = clientList
      return
    }
    // 兜底：从 getMappings 中提取 clientModel 集合
    const mappings = await mappingApi.getMappings()
    const set = new Set<string>()
    set.add('auto')
    if (Array.isArray(mappings)) {
      mappings.forEach((m: any) => {
        if (m.clientModel && typeof m.clientModel === 'string' && m.clientModel.trim()) {
          set.add(m.clientModel.trim())
        }
      })
    }
    availableModels.value = Array.from(set).sort()
  } catch (err) {
    console.error('获取系统模型映射列表失败:', err)
  }
}

async function fetchAutoConfig() {
  try {
    autoConfig.value = await autoApi.getGlobalConfig()
  } catch (err) {
    console.error('加载全局 Auto 竞速配置失败:', err)
  }
}

function importSystemAutoCandidates() {
  const currentSet = new Set(form.autoModels)
  for (const m of autoCandidateModels.value) {
    if (m && m !== 'auto') {
      currentSet.add(m)
    }
  }
  form.autoModels = Array.from(currentSet)
}

function openCreateModal() {
  isEditing.value = false
  editingId.value = null
  form.name = ''
  form.type = 'subscription'
  form.tier = 'pro'
  form.description = ''
  formPriceYuan.value = 39
  form.durationDays = 30
  form.tokenLimit = 0
  form.rateLimit = 30
  form.status = 'active'
  form.allowedModels = []
  form.autoModels = []
  showModal.value = true
}

function openEditModal(plan: any) {
  isEditing.value = true
  editingId.value = plan.id
  form.name = plan.name
  form.type = plan.type || 'subscription'
  form.tier = plan.tier || 'pro'
  form.description = plan.description
  formPriceYuan.value = plan.priceCents / 100
  form.durationDays = plan.durationDays
  form.tokenLimit = plan.tokenLimit || 0
  form.rateLimit = plan.rateLimit
  form.status = plan.status
  form.allowedModels = [...(plan.allowedModels || [])]
  form.autoModels = [...(plan.autoModels || [])]
  showModal.value = true
}

async function savePlan() {
  if (typeof formPriceYuan.value !== 'number' || formPriceYuan.value < 1) {
    alert('套餐售价最低不能低于 1.00 元')
    return
  }

  const payload = {
    ...form,
    tier: form.type === 'subscription' ? (form.tier || 'pro') : '',
    priceCents: Math.round(formPriceYuan.value * 100),
  }

  saving.value = true
  try {
    if (isEditing.value && editingId.value) {
      await planApi.adminUpdate(editingId.value, payload)
    } else {
      await planApi.adminCreate(payload)
    }
    showModal.value = false
    await fetchPlans()
  } catch (err: any) {
    alert(err.message || '保存失败')
  } finally {
    saving.value = false
  }
}

async function handleDeletePlan(id: number) {
  if (!confirm('确定要删除此套餐吗？')) return
  deletingId.value = id
  try {
    await planApi.adminDelete(id)
    await fetchPlans()
  } catch (err: any) {
    alert(err.message || '删除失败')
  } finally {
    deletingId.value = null
  }
}

onMounted(() => {
  fetchPlans()
  fetchAvailableModels()
  fetchAutoConfig()
})
</script>
