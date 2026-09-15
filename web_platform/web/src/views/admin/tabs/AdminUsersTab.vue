<template>
  <div class="flex flex-col gap-6">
    <div class="flex items-center justify-between">
      <div>
        <h3 class="text-base font-bold text-white">注册用户与订阅状态列表</h3>
        <p class="text-xs text-slate-400">查看注册用户、当前套餐状态，并可手动为其指派或调整订阅方案</p>
      </div>
      <div class="flex items-center gap-2">
        <input
          v-model="search"
          type="text"
          placeholder="搜索用户名或邮箱..."
          class="input-dark text-xs w-48"
          @keyup.enter="fetchUsers"
        />
        <button type="button" class="btn-secondary text-xs" @click="fetchUsers">
          搜索
        </button>
      </div>
    </div>

    <!-- 用户表格 -->
    <div class="glass-card overflow-hidden">
      <div class="overflow-x-auto">
        <table class="table-dark">
          <thead>
            <tr>
              <th>用户 ID</th>
              <th>用户名</th>
              <th>电子邮箱</th>
              <th>角色</th>
              <th>账号状态</th>
              <th>当前订阅套餐</th>
              <th>有效期截止</th>
              <th class="text-right">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="u in users" :key="u.id">
              <td class="font-mono text-xs text-slate-400">#{{ u.id }}</td>
              <td class="font-bold text-white">{{ u.username }}</td>
              <td class="text-xs text-slate-300">{{ u.email || '-' }}</td>
              <td>
                <span :class="u.role === 'admin' ? 'badge badge-amber' : 'badge badge-indigo'">
                  {{ u.role }}
                </span>
              </td>
              <td>
                <span :class="u.status === 'active' ? 'badge badge-emerald' : 'badge badge-rose'">
                  {{ u.status === 'active' ? '正常' : '已封禁' }}
                </span>
              </td>
              <td>
                <span v-if="u.plan" class="badge badge-cyan font-semibold">{{ u.plan.name }}</span>
                <span v-else class="text-xs text-slate-500">未订阅</span>
              </td>
              <td class="text-xs text-slate-400">
                <span v-if="u.planExpireAt === 0 && u.planId" class="text-emerald-400 font-bold">永久有效</span>
                <span v-else-if="u.planExpireAt">{{ formatTime(u.planExpireAt) }}</span>
                <span v-else>-</span>
              </td>
              <td class="text-right whitespace-nowrap">
                <button
                  type="button"
                  class="btn-secondary text-xs py-1 px-2.5 mr-2 cursor-pointer"
                  @click="openAssignModal(u)"
                >
                  分配套餐
                </button>
                <button
                  type="button"
                  :class="u.status === 'active' ? 'btn-danger' : 'btn-primary'"
                  class="text-xs py-1 px-2.5 cursor-pointer"
                  @click="toggleUser(u.id)"
                >
                  {{ u.status === 'active' ? '封禁' : '解封' }}
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- 手动分配套餐 Modal 弹窗 -->
    <div v-if="showAssignModal" class="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/70 backdrop-blur-sm">
      <div class="glass-card-glow w-full max-w-sm p-6">
        <h3 class="text-base font-bold text-white mb-3">为用户分配套餐</h3>
        <p class="text-xs text-slate-400 mb-4">
          正在为用户 <strong class="text-indigo-400 font-mono">{{ selectedUser?.username }}</strong> 指派套餐，授权模型白名单将即时生效：
        </p>

        <div class="flex flex-col gap-3">
          <div>
            <label class="block text-xs text-slate-300 mb-1">选择目标套餐</label>
            <select v-model="selectedPlanId" class="input-dark w-full cursor-pointer text-xs">
              <option v-for="p in allPlans" :key="p.id" :value="p.id">
                {{ p.name }} (¥{{ (p.priceCents / 100).toFixed(2) }} / {{ p.durationDays === 0 ? '永久' : `${p.durationDays}天` }})
              </option>
            </select>
          </div>

          <div class="flex items-center justify-end gap-3 mt-4">
            <button type="button" class="btn-secondary text-xs" @click="showAssignModal = false">取消</button>
            <button type="button" class="btn-primary text-xs" @click="confirmAssign">确认指派</button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { adminApi, planApi } from '../../../api/client'

const users = ref<any[]>([])
const allPlans = ref<any[]>([])
const search = ref('')
const showAssignModal = ref(false)
const selectedUser = ref<any>(null)
const selectedPlanId = ref<number | null>(null)

async function fetchUsers() {
  try {
    const res = await adminApi.listUsers(1, 50, search.value)
    users.value = res.list || []
  } catch (err) {
    console.error(err)
  }
}

async function fetchPlans() {
  try {
    allPlans.value = await planApi.adminList()
  } catch (err) {
    console.error(err)
  }
}

function openAssignModal(u: any) {
  selectedUser.value = u
  if (allPlans.value.length > 0) {
    selectedPlanId.value = allPlans.value[0].id
  }
  showAssignModal.value = true
}

async function confirmAssign() {
  if (!selectedUser.value || !selectedPlanId.value) return
  try {
    await adminApi.assignPlan(selectedUser.value.id, selectedPlanId.value)
    alert('套餐指派成功！')
    showAssignModal.value = false
    fetchUsers()
  } catch (err: any) {
    alert(err.message || '指派失败')
  }
}

async function toggleUser(id: number) {
  try {
    await adminApi.toggleUserStatus(id)
    fetchUsers()
  } catch (err: any) {
    alert(err.message || '操作失败')
  }
}

function formatTime(t: string | number) {
  if (!t) return '-'
  const d = typeof t === 'number' ? new Date(t * 1000) : new Date(t)
  return d.toLocaleDateString()
}

onMounted(() => {
  fetchUsers()
  fetchPlans()
})
</script>
