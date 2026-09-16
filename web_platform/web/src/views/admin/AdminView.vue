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
          纳管商业化套餐、模型白名单权限、支付跳转与收银配置、模型路由映射及 OCR 图像自愈配置
        </p>
      </div>
      <router-link to="/dashboard" class="btn-secondary text-xs">
        <span class="material-symbols-outlined text-16px">arrow_back</span>
        <span>返回用户工作台</span>
      </router-link>
    </div>

    <!-- 导航子 Tab 区域（无横向滚动条，放不下则收纳于下拉菜单） -->
    <div class="flex items-center flex-wrap gap-2 pb-3 mb-6 border-b border-slate-800/80 relative">
      <!-- 1. 核心高频业务 Tab（直接平铺展示） -->
      <button
        v-for="tab in primaryTabs"
        :key="tab.id"
        type="button"
        class="px-3.5 py-2 rounded-lg text-xs font-semibold whitespace-nowrap transition-all flex items-center gap-2 cursor-pointer"
        :class="activeTab === tab.id ? 'bg-indigo-600 text-white shadow-md shadow-indigo-600/30' : 'text-slate-400 hover:text-white hover:bg-slate-800/60'"
        @click="selectTab(tab.id)"
      >
        <span class="material-symbols-outlined text-16px">{{ tab.icon }}</span>
        <span>{{ tab.name }}</span>
      </button>

      <!-- 2. 下拉菜单容器（类似桌面端控制台下拉设计，容纳高级与网关配置） -->
      <div ref="dropdownRef" class="relative inline-block text-left">
        <button
          type="button"
          class="px-3.5 py-2 rounded-lg text-xs font-semibold whitespace-nowrap transition-all flex items-center gap-2 cursor-pointer border"
          :class="isDropdownActive
            ? 'bg-indigo-600 text-white border-indigo-500 shadow-md shadow-indigo-600/30'
            : isDropdownOpen
              ? 'bg-slate-800 text-white border-slate-700'
              : 'text-slate-400 hover:text-white hover:bg-slate-800/60 border-slate-800/80 bg-slate-900/40'"
          @click.stop="toggleDropdown"
        >
          <span class="material-symbols-outlined text-16px">
            {{ isDropdownActive && currentDropdownTab ? currentDropdownTab.icon : 'tune' }}
          </span>
          <span>
            {{ isDropdownActive && currentDropdownTab ? currentDropdownTab.name : '更多管理配置' }}
          </span>
          <span
            class="material-symbols-outlined text-16px transition-transform duration-200"
            :class="{ 'rotate-180': isDropdownOpen }"
          >
            expand_more
          </span>
        </button>

        <!-- 下拉菜单弹窗浮层 -->
        <transition
          enter-active-class="transition duration-150 ease-out"
          enter-from-class="transform scale-95 opacity-0 -translate-y-1"
          enter-to-class="transform scale-100 opacity-100 translate-y-0"
          leave-active-class="transition duration-100 ease-in"
          leave-from-class="transform scale-100 opacity-100 translate-y-0"
          leave-to-class="transform scale-95 opacity-0 -translate-y-1"
        >
          <div
            v-if="isDropdownOpen"
            class="absolute left-0 mt-2 w-72 origin-top-left rounded-2xl bg-[#0f172a]/95 backdrop-blur-xl border border-slate-700/80 shadow-2xl shadow-black/80 z-50 p-2 text-slate-200"
          >
            <!-- 分组 1: 网关与模型调度（移植自桌面端） -->
            <div class="px-2.5 py-1.5 text-[11px] font-bold tracking-wider text-indigo-400 uppercase flex items-center gap-1.5">
              <span class="material-symbols-outlined text-14px">hub</span>
              <span>网关集群与模型调度</span>
            </div>
            <div class="space-y-0.5 mb-2">
              <button
                v-for="tab in gatewayTabs"
                :key="tab.id"
                type="button"
                class="w-full text-left px-3 py-2 rounded-xl text-xs transition-all flex items-center justify-between group cursor-pointer"
                :class="activeTab === tab.id ? 'bg-indigo-600/20 text-indigo-300 font-semibold border border-indigo-500/40' : 'hover:bg-slate-800/70 text-slate-300 hover:text-white'"
                @click="selectTab(tab.id)"
              >
                <div class="flex items-center gap-2.5 min-w-0">
                  <div
                    class="w-7 h-7 rounded-lg flex items-center justify-center transition-colors"
                    :class="activeTab === tab.id ? 'bg-indigo-600 text-white' : 'bg-slate-800/80 text-slate-400 group-hover:text-indigo-400 group-hover:bg-slate-700/60'"
                  >
                    <span class="material-symbols-outlined text-16px">{{ tab.icon }}</span>
                  </div>
                  <div class="truncate">
                    <div class="font-medium truncate">{{ tab.name }}</div>
                    <div class="text-[10px] text-slate-400 font-normal truncate">{{ tab.desc }}</div>
                  </div>
                </div>
                <span v-if="activeTab === tab.id" class="material-symbols-outlined text-16px text-indigo-400">check</span>
              </button>
            </div>

            <!-- 分组 2: 系统与监控审计 -->
            <div class="border-t border-slate-800/80 pt-2">
              <div class="px-2.5 py-1.5 text-[11px] font-bold tracking-wider text-slate-400 uppercase flex items-center gap-1.5">
                <span class="material-symbols-outlined text-14px">monitoring</span>
                <span>系统与审计监控</span>
              </div>
              <div class="space-y-0.5">
                <button
                  v-for="tab in opsTabs"
                  :key="tab.id"
                  type="button"
                  class="w-full text-left px-3 py-2 rounded-xl text-xs transition-all flex items-center justify-between group cursor-pointer"
                  :class="activeTab === tab.id ? 'bg-indigo-600/20 text-indigo-300 font-semibold border border-indigo-500/40' : 'hover:bg-slate-800/70 text-slate-300 hover:text-white'"
                  @click="selectTab(tab.id)"
                >
                  <div class="flex items-center gap-2.5 min-w-0">
                    <div
                      class="w-7 h-7 rounded-lg flex items-center justify-center transition-colors"
                      :class="activeTab === tab.id ? 'bg-indigo-600 text-white' : 'bg-slate-800/80 text-slate-400 group-hover:text-indigo-400 group-hover:bg-slate-700/60'"
                    >
                      <span class="material-symbols-outlined text-16px">{{ tab.icon }}</span>
                    </div>
                    <div class="truncate">
                      <div class="font-medium truncate">{{ tab.name }}</div>
                      <div class="text-[10px] text-slate-400 font-normal truncate">{{ tab.desc }}</div>
                    </div>
                  </div>
                  <span v-if="activeTab === tab.id" class="material-symbols-outlined text-16px text-indigo-400">check</span>
                </button>
              </div>
            </div>
          </div>
        </transition>
      </div>
    </div>

    <!-- Tab 1: 套餐与模型范围管理 -->
    <div v-if="activeTab === 'plans'">
      <AdminPlansTab />
    </div>

    <!-- Tab 2: 订单与流水管理 -->
    <div v-else-if="activeTab === 'orders'">
      <AdminOrdersTab />
    </div>

    <!-- Tab 2.5: 支付跳转配置 -->
    <div v-else-if="activeTab === 'payment'">
      <AdminPaymentTab />
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

    <!-- Tab 7: 账号池管理 (移植桌面端) -->
    <div v-else-if="activeTab === 'accounts'">
      <AdminAccountsTab />
    </div>

    <!-- Tab 8: 系统与 API 服务配置 -->
    <div v-else-if="activeTab === 'system'">
      <AdminSystemTab />
    </div>

    <!-- Tab 9: 请求命中模型日志 (按账号筛选/全量监控) -->
    <div v-else-if="activeTab === 'logs'">
      <!-- <RequestLogsPanel mode="admin" /> -->
      <div class="glass-card p-8 text-center text-slate-400 text-xs flex flex-col items-center justify-center gap-2">
        <span class="material-symbols-outlined text-32px text-slate-500">pending</span>
        <span>请求命中模型日志面板维护中，组件即将恢复...</span>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted, computed } from 'vue'
import { useRouter } from 'vue-router'
import { authState, refreshCurrentUser } from '../../api/client'
import AdminPlansTab from './tabs/AdminPlansTab.vue'
import AdminOrdersTab from './tabs/AdminOrdersTab.vue'
import AdminPaymentTab from './tabs/AdminPaymentTab.vue'
import AdminUsersTab from './tabs/AdminUsersTab.vue'
import AdminMappingsTab from './tabs/AdminMappingsTab.vue'
import AdminAccountsTab from './tabs/AdminAccountsTab.vue'
import AdminAutoTab from './tabs/AdminAutoTab.vue'
import AdminOcrTab from './tabs/AdminOcrTab.vue'
import AdminSystemTab from './tabs/AdminSystemTab.vue'
// import RequestLogsPanel from '../../components/logs/RequestLogsPanel.vue'

const router = useRouter()
const activeTab = ref('plans')

// 下拉菜单控制
const isDropdownOpen = ref(false)
const dropdownRef = ref<HTMLElement | null>(null)

const toggleDropdown = () => {
  isDropdownOpen.value = !isDropdownOpen.value
}

const selectTab = (tabId: string) => {
  activeTab.value = tabId
  isDropdownOpen.value = false
}

const handleClickOutside = (e: MouseEvent) => {
  if (dropdownRef.value && !dropdownRef.value.contains(e.target as Node)) {
    isDropdownOpen.value = false
  }
}

const handleKeydown = (e: KeyboardEvent) => {
  if (e.key === 'Escape') {
    isDropdownOpen.value = false
  }
}

onMounted(async () => {
  document.addEventListener('click', handleClickOutside)
  document.addEventListener('keydown', handleKeydown)

  if (!authState.user && authState.token) {
    await refreshCurrentUser()
  }
  if (authState.user?.role !== 'admin') {
    router.replace('/dashboard')
  }
})

onUnmounted(() => {
  document.removeEventListener('click', handleClickOutside)
  document.removeEventListener('keydown', handleKeydown)
})

// 1. 常驻核心 Tab（直接平铺）
const primaryTabs = [
  { id: 'plans', name: '套餐与白名单', icon: 'layers' },
  { id: 'orders', name: '交易订单与核销', icon: 'receipt_long' },
  { id: 'users', name: '用户管理与分配', icon: 'group' },
  { id: 'payment', name: '支付跳转配置', icon: 'payments' },
]

// 2. 网关与集群调度类 Tab（收纳于下拉菜单第一组）
const gatewayTabs = [
  { id: 'accounts', name: '账号池管理', icon: 'account_tree', desc: '上游渠道与并发负载' },
  { id: 'mappings', name: '模型路由映射', icon: 'alt_route', desc: '入站模型分流与重定向' },
  { id: 'auto', name: 'Auto 并发竞速', icon: 'bolt', desc: '多模型智能并发竞速策略' },
  { id: 'ocr', name: 'OCR 图像自愈降级', icon: 'document_scanner', desc: '图片多模态容灾重试' },
]

// 3. 系统与监控类 Tab（收纳于下拉菜单第二组）
const opsTabs = [
  { id: 'system', name: '系统与 API 服务', icon: 'settings', desc: '全局站点与 API 域名参数' },
  { id: 'logs', name: '请求命中模型日志', icon: 'fact_check', desc: '全链路请求与命中审计' },
]

const dropdownTabs = computed(() => [...gatewayTabs, ...opsTabs])

// 当前选中的 Tab 是否属于下拉菜单中的项
const isDropdownActive = computed(() => {
  return dropdownTabs.value.some(t => t.id === activeTab.value)
})

const currentDropdownTab = computed(() => {
  return dropdownTabs.value.find(t => t.id === activeTab.value)
})
</script>
