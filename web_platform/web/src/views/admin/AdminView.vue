<template>
  <div class="max-w-7xl mx-auto px-4 py-6">
    <!-- 管理员控制台头部 -->
    <div class="flex items-center justify-between mb-6 pb-4 border-b border-slate-800">
      <div>
        <h1 class="text-2xl font-extrabold text-white flex items-center gap-2">
          <span class="material-symbols-outlined text-indigo-400">admin_panel_settings</span>
          <span>系统管理控制台 (Admin Workspace)</span>
        </h1>
        <p class="text-xs text-slate-400 mt-1">
          纳管商业化套餐、模型白名单权限、极客工坊切单流水、中继模型映射及 OCR 图像自愈配置
        </p>
      </div>
      <router-link to="/dashboard" class="btn-secondary text-xs">
        <span class="material-symbols-outlined text-16px">arrow_back</span>
        <span>返回用户工作台</span>
      </router-link>
    </div>

    <!-- 导航子 Tab -->
    <div class="flex items-center gap-2 overflow-x-auto pb-3 mb-6 border-b border-slate-800/80">
      <button
        v-for="tab in tabs"
        :key="tab.id"
        type="button"
        class="px-3.5 py-2 rounded-lg text-xs font-semibold whitespace-nowrap transition-all flex items-center gap-2 cursor-pointer"
        :class="activeTab === tab.id ? 'bg-indigo-600 text-white shadow-md shadow-indigo-600/30' : 'text-slate-400 hover:text-white hover:bg-slate-800/60'"
        @click="activeTab = tab.id"
      >
        <span class="material-symbols-outlined text-16px">{{ tab.icon }}</span>
        <span>{{ tab.name }}</span>
      </button>
    </div>

    <!-- Tab 1: 套餐与模型范围管理 -->
    <div v-if="activeTab === 'plans'">
      <AdminPlansTab />
    </div>

    <!-- Tab 2: 订单与流水管理 -->
    <div v-else-if="activeTab === 'orders'">
      <AdminOrdersTab />
    </div>

    <!-- Tab 3: 用户管理 -->
    <div v-else-if="activeTab === 'users'">
      <AdminUsersTab />
    </div>

    <!-- Tab 4: 模型映射配置 (移植桌面端) -->
    <div v-else-if="activeTab === 'mappings'">
      <AdminMappingsTab />
    </div>

    <!-- Tab 5: Auto 并发竞速池配置 -->
    <div v-else-if="activeTab === 'auto'">
      <AdminAutoTab />
    </div>

    <!-- Tab 6: OCR 图像模型配置 (移植桌面端) -->
    <div v-else-if="activeTab === 'ocr'">
      <AdminOcrTab />
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { authState, refreshCurrentUser } from '../../api/client'
import AdminPlansTab from './tabs/AdminPlansTab.vue'
import AdminOrdersTab from './tabs/AdminOrdersTab.vue'
import AdminUsersTab from './tabs/AdminUsersTab.vue'
import AdminMappingsTab from './tabs/AdminMappingsTab.vue'
import AdminAutoTab from './tabs/AdminAutoTab.vue'
import AdminOcrTab from './tabs/AdminOcrTab.vue'

const router = useRouter()
const activeTab = ref('plans')

onMounted(async () => {
  if (!authState.user && authState.token) {
    await refreshCurrentUser()
  }
  if (authState.user?.role !== 'admin') {
    router.replace('/dashboard')
  }
})

const tabs = [
  { id: 'plans', name: '套餐与模型白名单管理', icon: 'layers' },
  { id: 'orders', name: '交易订单与核销', icon: 'receipt_long' },
  { id: 'users', name: '用户管理与套餐分配', icon: 'group' },
  { id: 'mappings', name: '中继模型映射配置', icon: 'alt_route' },
  { id: 'auto', name: 'Auto 并发竞速池配置', icon: 'bolt' },
  { id: 'ocr', name: 'OCR 图像自愈降级配置', icon: 'document_scanner' },
]
</script>
