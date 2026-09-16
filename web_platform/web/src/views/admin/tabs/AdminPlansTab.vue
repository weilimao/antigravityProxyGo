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
      <div class="overflow-x-auto">
        <table class="table-dark">
          <thead>
            <tr>
              <th>套餐名称</th>
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
                <div>{{ plan.name }}</div>
                <div class="text-11px text-slate-400">{{ plan.description }}</div>
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
                <button type="button" class="btn-danger cursor-pointer" @click="handleDeletePlan(plan.id)">
                  删除
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
          <div class="grid grid-cols-2 gap-4">
            <div>
              <label class="block text-slate-300 font-semibold mb-1">套餐名称</label>
              <input v-model="form.name" type="text" required class="input-dark w-full" placeholder="如: Pro 开发者版" />
            </div>
            <div>
              <label class="block text-slate-300 font-semibold mb-1">售价 (单位: 元)</label>
              <input v-model.number="formPriceYuan" type="number" step="0.01" required class="input-dark w-full" placeholder="39.00" />
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
            <button type="button" class="btn-secondary text-xs" @click="showModal = false">取消</button>
            <button type="submit" class="btn-primary text-xs">保存套餐</button>
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

const plans = ref<any[]>([])
const availableModels = ref<string[]>([])
const autoConfig = ref<any>(null)
const showModal = ref(false)
const isEditing = ref(false)
const editingId = ref<number | null>(null)
const formPriceYuan = ref<number>(39)

const form = reactive({
  name: '',
  description: '',
  durationDays: 30,
  tokenLimit: 0,
  rateLimit: 30,
  status: 'active',
  allowedModels: [] as string[],
  autoModels: [] as string[],
})

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
  try {
    plans.value = await planApi.adminList()
  } catch (err) {
    console.error(err)
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
  const payload = {
    ...form,
    priceCents: Math.round(formPriceYuan.value * 100),
  }

  try {
    if (isEditing.value && editingId.value) {
      await planApi.adminUpdate(editingId.value, payload)
    } else {
      await planApi.adminCreate(payload)
    }
    showModal.value = false
    fetchPlans()
  } catch (err: any) {
    alert(err.message || '保存失败')
  }
}

async function handleDeletePlan(id: number) {
  if (!confirm('确定要删除此套餐吗？')) return
  try {
    await planApi.adminDelete(id)
    fetchPlans()
  } catch (err: any) {
    alert(err.message || '删除失败')
  }
}

onMounted(() => {
  fetchPlans()
  fetchAvailableModels()
  fetchAutoConfig()
})
</script>
