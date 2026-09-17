<template>
  <div class="min-h-screen flex flex-col text-slate-100 relative selection:bg-indigo-500 selection:text-white">
    <!-- 全局顶部导航栏 -->
    <header v-if="!isAuthPage" class="sticky top-0 z-40 bg-[#0a0e1a]/95 backdrop-blur-md border-b border-slate-800/80">
      <div class="max-w-[1680px] mx-auto px-4 sm:px-6 lg:px-8 h-16 flex items-center justify-between">
        <!-- Brand Logo -->
        <router-link to="/" class="flex items-center gap-2.5 text-white no-underline group">
          <div class="w-9 h-9 rounded-xl bg-indigo-600/20 text-indigo-400 border border-indigo-500/30 flex items-center justify-center transition-all group-hover:bg-indigo-600/30">
            <span class="material-symbols-outlined text-22px">rocket_launch</span>
          </div>
          <div>
            <span class="text-base font-extrabold tracking-tight gradient-text">{{ siteName }}</span>
            <span class="text-[10px] block text-slate-400 -mt-1 font-mono tracking-wider">PROXY & SUBSCRIPTIONS</span>
          </div>
        </router-link>

        <!-- 中间导航项 -->
        <nav class="hidden md:flex items-center gap-1.5 p-1 rounded-xl bg-slate-900/60 border border-slate-800">
          <router-link
            to="/pricing"
            class="px-3.5 py-1.5 rounded-lg text-xs font-semibold transition-all flex items-center gap-1.5"
            :class="$route.path === '/pricing' ? 'bg-indigo-600 text-white shadow-sm' : 'text-slate-400 hover:text-white hover:bg-slate-800/50'"
          >
            <span class="material-symbols-outlined text-16px">workspace_premium</span>
            <span>订阅套餐</span>
          </router-link>

          <router-link
            v-if="isLoggedIn"
            to="/dashboard"
            class="px-3.5 py-1.5 rounded-lg text-xs font-semibold transition-all flex items-center gap-1.5"
            :class="$route.path === '/dashboard' ? 'bg-indigo-600 text-white shadow-sm' : 'text-slate-400 hover:text-white hover:bg-slate-800/50'"
          >
            <span class="material-symbols-outlined text-16px">dashboard</span>
            <span>用户控制台</span>
          </router-link>

          <router-link
            v-if="isLoggedIn"
            to="/orders"
            class="px-3.5 py-1.5 rounded-lg text-xs font-semibold transition-all flex items-center gap-1.5"
            :class="$route.path.startsWith('/orders') ? 'bg-indigo-600 text-white shadow-sm' : 'text-slate-400 hover:text-white hover:bg-slate-800/50'"
          >
            <span class="material-symbols-outlined text-16px">receipt_long</span>
            <span>我的订单</span>
          </router-link>

          <router-link
            v-if="isAdmin"
            to="/admin"
            class="px-3.5 py-1.5 rounded-lg text-xs font-semibold transition-all flex items-center gap-1.5"
            :class="$route.path.startsWith('/admin') ? 'bg-amber-500/20 text-amber-300 border border-amber-500/40' : 'text-slate-400 hover:text-amber-300 hover:bg-slate-800/50'"
          >
            <span class="material-symbols-outlined text-16px text-amber-400">admin_panel_settings</span>
            <span>管理后台</span>
          </router-link>
        </nav>

        <!-- 右侧用户状态与登出 -->
        <div class="flex items-center gap-3">
          <template v-if="isLoggedIn">
            <span class="text-xs text-slate-300 hidden sm:inline-flex items-center gap-1.5 px-2.5 py-1 rounded-lg bg-white/[0.04] border border-white/[0.06]">
              <span class="w-2 h-2 rounded-full bg-emerald-400 shadow-[0_0_6px_#34d399]"></span>
              <span class="font-mono text-slate-200">{{ username }}</span>
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
      <!-- 导航栏底部微米科技高光反射线 -->
      <div class="specular-line"></div>
    </header>

    <!-- 核心主体内容 -->
    <main class="flex-1">
      <router-view />
    </main>

    <!-- 页脚 -->
    <footer v-if="!isAuthPage" class="border-t border-white/[0.06] py-6 text-center text-xs text-slate-500 bg-[#06080f]/50 backdrop-blur">
      <div class="max-w-[1680px] mx-auto px-4 sm:px-6 lg:px-8 flex flex-col sm:flex-row items-center justify-between gap-2">
        <span class="text-slate-400">{{ siteName }} · 下一代高吞吐企业级大模型服务平台</span>
        <span class="text-11px text-cyan-400/70 font-mono flex items-center gap-1">
          <span class="w-1.5 h-1.5 rounded-full bg-emerald-400 shadow-[0_0_6px_#34d399]"></span>
          <span>SYSTEM RUNNING · 高可用算力引擎</span>
        </span>
      </div>
    </footer>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { authApi } from './api/client'
import { useUserStore, useSystemStore } from './stores'

const router = useRouter()
const route = useRoute()
const userStore = useUserStore()
const systemStore = useSystemStore()

const siteName = computed(() => systemStore.siteName)
const isAuthPage = computed(() => route.path === '/login')
const isLoggedIn = computed(() => userStore.isLoggedIn)
const username = computed(() => userStore.username)
const isAdmin = computed(() => userStore.isAdmin)

async function handleLogout() {
  try {
    await authApi.logout()
  } catch (e) {
    // 登出容错
  }
  userStore.logout()
  router.push('/login')
}

onMounted(() => {
  if (userStore.token) {
    userStore.fetchUserProfile()
  }
  systemStore.fetchSystemConfig()
})
</script>
