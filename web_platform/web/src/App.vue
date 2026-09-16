<template>
  <div class="min-h-screen flex flex-col bg-[#0b0f19] text-slate-100">
    <!-- 全局顶部导航栏 -->
    <header v-if="!isAuthPage" class="sticky top-0 z-40 bg-[#0f172a]/80 backdrop-blur-md border-b border-slate-800">
      <div class="max-w-7xl mx-auto px-4 h-16 flex items-center justify-between">
        <!-- Brand Logo -->
        <router-link to="/" class="flex items-center gap-2.5 text-white no-underline">
          <div class="w-9 h-9 rounded-xl bg-indigo-600/20 text-indigo-400 border border-indigo-500/30 flex items-center justify-center">
            <span class="material-symbols-outlined text-22px">rocket_launch</span>
          </div>
          <div>
            <span class="text-base font-extrabold tracking-tight gradient-text">{{ siteName }}</span>
            <span class="text-[10px] block text-slate-400 -mt-1 font-mono">Proxy & Subscriptions</span>
          </div>
        </router-link>

        <!-- 中间导航项 -->
        <nav class="hidden md:flex items-center gap-1">
          <router-link
            to="/pricing"
            class="px-3.5 py-1.5 rounded-lg text-xs font-semibold transition-colors flex items-center gap-1.5"
            :class="$route.path === '/pricing' ? 'bg-indigo-600/20 text-indigo-400 border border-indigo-500/30' : 'text-slate-400 hover:text-white'"
          >
            <span class="material-symbols-outlined text-16px">workspace_premium</span>
            <span>订阅套餐</span>
          </router-link>

          <router-link
            v-if="isLoggedIn"
            to="/dashboard"
            class="px-3.5 py-1.5 rounded-lg text-xs font-semibold transition-colors flex items-center gap-1.5"
            :class="$route.path === '/dashboard' ? 'bg-indigo-600/20 text-indigo-400 border border-indigo-500/30' : 'text-slate-400 hover:text-white'"
          >
            <span class="material-symbols-outlined text-16px">dashboard</span>
            <span>用户控制台</span>
          </router-link>

          <router-link
            v-if="isLoggedIn"
            to="/orders"
            class="px-3.5 py-1.5 rounded-lg text-xs font-semibold transition-colors flex items-center gap-1.5"
            :class="$route.path.startsWith('/orders') ? 'bg-indigo-600/20 text-indigo-400 border border-indigo-500/30' : 'text-slate-400 hover:text-white'"
          >
            <span class="material-symbols-outlined text-16px">receipt_long</span>
            <span>我的订单</span>
          </router-link>

          <router-link
            v-if="isAdmin"
            to="/admin"
            class="px-3.5 py-1.5 rounded-lg text-xs font-semibold transition-colors flex items-center gap-1.5"
            :class="$route.path.startsWith('/admin') ? 'bg-amber-500/20 text-amber-400 border border-amber-500/30' : 'text-slate-400 hover:text-amber-300'"
          >
            <span class="material-symbols-outlined text-16px">admin_panel_settings</span>
            <span>管理后台</span>
          </router-link>
        </nav>

        <!-- 右侧用户状态与登出 -->
        <div class="flex items-center gap-3">
          <template v-if="isLoggedIn">
            <span class="text-xs text-slate-300 hidden sm:inline-flex items-center gap-1">
              <span class="material-symbols-outlined text-16px text-slate-400">person</span>
              <span>{{ username }}</span>
            </span>
            <button
              type="button"
              class="btn-secondary text-xs py-1.5 px-3 cursor-pointer"
              @click="handleLogout"
            >
              <span class="material-symbols-outlined text-14px">logout</span>
              <span>退出</span>
            </button>
          </template>
          <template v-else>
            <router-link to="/login" class="btn-primary text-xs py-1.5 px-4">
              <span>登录 / 注册</span>
            </router-link>
          </template>
        </div>
      </div>
    </header>

    <!-- 核心主体内容 -->
    <main class="flex-1">
      <router-view />
    </main>

    <!-- 页脚 -->
    <footer v-if="!isAuthPage" class="border-t border-slate-800/80 py-6 text-center text-xs text-slate-500">
      <div class="max-w-7xl mx-auto px-4 flex flex-col sm:flex-row items-center justify-between gap-2">
        <span>{{ siteName }} · 下一代高吞吐企业级大模型服务平台</span>
        <span class="font-mono text-11px text-slate-600">Pure Go + Modern Vue 3 · 企业级高可用算力保障</span>
      </div>
    </footer>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { authState, clearToken, refreshCurrentUser, systemApi, authApi } from './api/client'

const router = useRouter()
const route = useRoute()

const siteName = ref('MAX API')
const isAuthPage = computed(() => route.path === '/login')
const isLoggedIn = computed(() => !!authState.token)
const username = computed(() => authState.user?.username || '')
const role = computed(() => authState.user?.role || '')
const isAdmin = computed(() => role.value === 'admin')

async function handleLogout() {
  try {
    await authApi.logout()
  } catch (e) {
    // 登出容错
  }
  clearToken()
  router.push('/login')
}

watch(() => route.path, () => {
  if (authState.token && !authState.user) {
    refreshCurrentUser()
  }
})

onMounted(async () => {
  if (authState.token) {
    refreshCurrentUser()
  }
  try {
    const cfg = await systemApi.getPublicConfig()
    if (cfg && cfg.siteName && cfg.siteName !== 'Antigravity Web' && cfg.siteName !== 'open max api') {
      siteName.value = cfg.siteName
    } else {
      siteName.value = 'MAX API'
    }
  } catch {
    // 降级使用默认 MAX API
  }
})
</script>
