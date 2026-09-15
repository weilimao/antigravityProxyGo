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
                    class="badge badge-cyan text-10px font-mono"
                  >
                    {{ m }}
                  </span>
                  <span v-if="!plan.allowedModels || plan.allowedModels.length === 0" class="text-11px text-slate-500">
                    全量公共模型
                  </span>
                </div>
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

          <div class="grid grid-cols-3 gap-4">
            <div>
              <label class="block text-slate-300 font-semibold mb-1">有效天数 (0=永久)</label>
              <input v-model.number="form.durationDays" type="number" required class="input-dark w-full" placeholder="30" />
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

            <!-- 若已选 auto，实时联动回显当前服务端/系统 Auto 竞速绑定的底层候选模型清单 -->
            <div v-if="hasAutoSelected" class="mb-3 p-3 rounded-lg bg-amber-500/10 border border-amber-500/25 transition-all">
              <div class="flex items-center justify-between gap-2 mb-2">
                <span class="text-xs font-bold text-amber-300 flex items-center gap-1.5">
                  <span class="material-symbols-outlined text-16px text-amber-400">bolt</span>
                  <span>Auto 跨号池首字竞速包含模型 ({{ autoCandidateModels.length }} 个)</span>
                </span>
                <button
                  v-if="autoCandidateModels.length > 0"
                  type="button"
                  class="text-[11px] font-semibold px-2.5 py-1 rounded bg-amber-500/20 hover:bg-amber-500/30 text-amber-200 border border-amber-500/40 cursor-pointer flex items-center gap-1 transition-all"
                  @click="importAutoCandidates"
                  title="一键将 Auto 关联的所有候选大模型全部追加到本套餐白名单中"
                >
                  <span class="material-symbols-outlined text-13px">playlist_add</span>
                  <span>一键导入全部候选模型到本套餐</span>
                </button>
              </div>
              <div v-if="autoCandidateModels.length > 0" class="flex flex-wrap gap-1.5">
                <span
                  v-for="m in autoCandidateModels"
                  :key="m"
                  class="px-2 py-0.5 rounded text-[11px] font-mono bg-black/50 text-amber-200 border border-amber-500/30 flex items-center gap-1"
                >
                  <span class="material-symbols-outlined text-11px text-amber-400">check_circle</span>
                  <span>{{ m }}</span>
                </span>
              </div>
              <div v-else class="text-[11px] text-amber-200/70 italic flex items-center gap-1">
                <span class="material-symbols-outlined text-13px">info</span>
                <span>系统暂未配置 Auto 候选模型，可前往上方「Auto竞速」Tab 添加。</span>
              </div>
            </div>

            <!-- 候选模型快速添加池 -->
            <div>
              <span class="text-11px text-slate-400 block mb-1.5">快速勾选候选模型:</span>
              <div v-if="availableModels.length > 0" class="flex flex-wrap gap-1.5">
                <button
                  v-for="cand in availableModels"
                  :key="cand"
                  type="button"
                  class="px-2 py-0.5 rounded text-11px font-mono transition-all cursor-pointer border flex items-center gap-1"
                  :class="form.allowedModels.includes(cand)
                    ? (cand === 'auto' ? 'bg-amber-600 text-white border-amber-500' : 'bg-indigo-600 text-white border-indigo-500')
                    : (cand === 'auto' ? 'bg-amber-950/40 text-amber-300 border-amber-700/50 hover:bg-amber-900/50' : 'bg-slate-800 text-slate-400 border-slate-700 hover:text-white')"
                  @click="toggleModel(cand)"
                >
                  <span v-if="cand === 'auto'" class="material-symbols-outlined text-12px text-amber-400">bolt</span>
                  <span>{{ cand }}</span>
                  <span v-if="cand === 'auto' && autoCandidateModels.length > 0" class="text-[10px] opacity-75 font-sans">
                    ({{ autoCandidateModels.length }})
                  </span>
                </button>
              </div>
              <div v-else class="text-[11px] text-slate-500 italic flex items-center gap-1 py-1">
                <span class="material-symbols-outlined text-14px">info</span>
                <span>平台暂未配置可用模型映射，请先在上方「模型映射配置」Tab 中添加入站模型。</span>
              </div>
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
  rateLimit: 30,
  status: 'active',
  allowedModels: [] as string[],
})

const hasAutoSelected = computed(() => form.allowedModels.includes('auto'))
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

function importAutoCandidates() {
  for (const m of autoCandidateModels.value) {
    if (!form.allowedModels.includes(m)) {
      form.allowedModels.push(m)
    }
  }
}

function openCreateModal() {
  isEditing.value = false
  editingId.value = null
  form.name = ''
  form.description = ''
  formPriceYuan.value = 39
  form.durationDays = 30
  form.rateLimit = 30
  form.status = 'active'
  form.allowedModels = []
  showModal.value = true
}

function openEditModal(plan: any) {
  isEditing.value = true
  editingId.value = plan.id
  form.name = plan.name
  form.description = plan.description
  formPriceYuan.value = plan.priceCents / 100
  form.durationDays = plan.durationDays
  form.rateLimit = plan.rateLimit
  form.status = plan.status
  form.allowedModels = [...(plan.allowedModels || [])]
  showModal.value = true
}

function toggleModel(modelName: string) {
  const idx = form.allowedModels.indexOf(modelName)
  if (idx >= 0) {
    form.allowedModels.splice(idx, 1)
  } else {
    form.allowedModels.push(modelName)
  }
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
