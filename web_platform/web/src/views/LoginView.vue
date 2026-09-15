<template>
  <div class="min-h-screen flex items-center justify-center p-4">
    <div class="glass-card-glow w-full max-w-md p-8 relative overflow-hidden">
      <!-- 顶部 Logo 与系统标识 -->
      <div class="text-center mb-8">
        <div class="inline-flex items-center justify-center w-12 h-12 rounded-xl bg-indigo-500/20 text-indigo-400 mb-3 border border-indigo-500/30">
          <span class="material-symbols-outlined text-28px">rocket_launch</span>
        </div>
        <h1 class="text-2xl font-bold gradient-text tracking-tight">Antigravity Web</h1>
        <p class="text-xs text-slate-400 mt-1">下一代高吞吐智能模型中继与订阅管理平台</p>
      </div>

      <!-- Tab 切换 -->
      <div class="flex items-center p-1 bg-slate-900/60 rounded-lg border border-slate-800 mb-6">
        <button
          type="button"
          class="flex-1 py-1.5 text-xs font-semibold rounded-md transition-all cursor-pointer"
          :class="isLogin ? 'bg-indigo-600 text-white shadow' : 'text-slate-400 hover:text-white'"
          @click="isLogin = true"
        >
          账号登录
        </button>
        <button
          type="button"
          class="flex-1 py-1.5 text-xs font-semibold rounded-md transition-all cursor-pointer"
          :class="!isLogin ? 'bg-indigo-600 text-white shadow' : 'text-slate-400 hover:text-white'"
          @click="isLogin = false"
        >
          新用户注册
        </button>
      </div>

      <!-- 表单 -->
      <form @submit.prevent="handleSubmit" class="flex flex-col gap-4">
        <div>
          <label class="block text-xs font-medium text-slate-300 mb-1">用户名</label>
          <div class="relative">
            <input
              v-model="form.username"
              type="text"
              required
              class="input-dark w-full pl-9"
              placeholder="请输入登录用户名"
            />
            <span class="material-symbols-outlined absolute left-2.5 top-2.5 text-slate-500 text-18px">person</span>
          </div>
        </div>

        <div v-if="!isLogin">
          <label class="block text-xs font-medium text-slate-300 mb-1">电子邮箱（选填）</label>
          <div class="relative">
            <input
              v-model="form.email"
              type="email"
              class="input-dark w-full pl-9"
              placeholder="user@example.com"
            />
            <span class="material-symbols-outlined absolute left-2.5 top-2.5 text-slate-500 text-18px">mail</span>
          </div>
        </div>

        <div>
          <label class="block text-xs font-medium text-slate-300 mb-1">密码</label>
          <div class="relative">
            <input
              v-model="form.password"
              type="password"
              required
              minlength="6"
              class="input-dark w-full pl-9"
              placeholder="请输入密码（不少于6位）"
            />
            <span class="material-symbols-outlined absolute left-2.5 top-2.5 text-slate-500 text-18px">lock</span>
          </div>
        </div>

        <div v-if="errorMsg" class="p-2.5 rounded-lg bg-rose-500/10 border border-rose-500/30 text-rose-400 text-xs flex items-center gap-2">
          <span class="material-symbols-outlined text-16px">error</span>
          <span>{{ errorMsg }}</span>
        </div>

        <button
          type="submit"
          :disabled="loading"
          class="btn-primary w-full py-2.5 mt-2 cursor-pointer"
        >
          <span class="material-symbols-outlined text-18px" v-if="!loading">
            {{ isLogin ? 'login' : 'how_to_reg' }}
          </span>
          <span>{{ loading ? '处理中...' : (isLogin ? '立即登录' : '创建账号并自动登录') }}</span>
        </button>
      </form>

      <!-- 底部默认提示 -->
      <div class="mt-6 pt-4 border-t border-slate-800 text-center text-xs text-slate-500">
        默认初始管理员账号: <code class="text-indigo-400 font-mono">admin</code> / <code class="text-indigo-400 font-mono">admin123</code>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { authApi, setToken, refreshCurrentUser } from '../api/client'

const router = useRouter()
const route = useRoute()
const isLogin = ref(true)
const loading = ref(false)
const errorMsg = ref('')

const form = reactive({
  username: '',
  email: '',
  password: '',
})

async function handleSubmit() {
  errorMsg.value = ''
  loading.value = true
  try {
    let res: any
    if (isLogin.value) {
      res = await authApi.login({
        username: form.username,
        password: form.password,
      })
    } else {
      res = await authApi.register({
        username: form.username,
        email: form.email,
        password: form.password,
      })
    }

    if (res.token) {
      setToken(res.token)
      await refreshCurrentUser()
      const redirect = (route.query.redirect as string) || '/dashboard'
      router.push(redirect)
    }
  } catch (err: any) {
    errorMsg.value = err.message || '操作失败'
  } finally {
    loading.value = false
  }
}
</script>
