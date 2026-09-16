<template>
  <div class="relative min-h-screen flex flex-col justify-center items-center px-4 py-8 sm:py-12 overflow-hidden bg-[#060913] text-slate-100 selection:bg-indigo-500 selection:text-white">
    <!-- 背景流光氛围与科技网格 -->
    <div class="absolute inset-0 bg-[radial-gradient(#1e293b_1px,transparent_1px)] [background-size:32px_32px] opacity-25 pointer-events-none"></div>
    <div class="absolute -top-40 -left-40 w-[30rem] h-[30rem] bg-indigo-600/20 rounded-full blur-[120px] pointer-events-none"></div>
    <div class="absolute -bottom-40 -right-40 w-[30rem] h-[30rem] bg-purple-600/15 rounded-full blur-[130px] pointer-events-none"></div>
    <div class="absolute top-1/3 left-1/2 -translate-x-1/2 w-[40rem] h-[40rem] bg-cyan-600/10 rounded-full blur-[140px] pointer-events-none"></div>

    <!-- 主体双栏容器 -->
    <div class="relative z-10 w-full max-w-6xl mx-auto my-auto">
      <div class="grid grid-cols-1 lg:grid-cols-12 gap-8 lg:gap-12 items-center">
        
        <!-- 左侧：品牌科技感展示区（移动端精简，桌面端丰满） -->
        <div class="lg:col-span-7 flex flex-col justify-center text-left">
          <!-- 状态与品牌徽章 -->
          <div class="inline-flex items-center gap-2 px-3.5 py-1.5 rounded-full bg-indigo-500/10 border border-indigo-500/25 text-indigo-400 text-xs font-mono font-medium mb-5 w-fit shadow-sm shadow-indigo-500/10">
            <span class="inline-block w-2 h-2 rounded-full bg-emerald-400 animate-pulse"></span>
            <span>MAX API · Enterprise Model Cloud</span>
          </div>

          <!-- 主标题与导语 -->
          <h1 class="text-3xl sm:text-4xl lg:text-5xl font-extrabold tracking-tight text-white leading-tight">
            高吞吐企业级大模型<br />
            <span class="gradient-text">算力云与商业化服务平台</span>
          </h1>
          <p class="text-sm sm:text-base text-slate-400 mt-4 max-w-xl leading-relaxed">
            面向下一代 AI 应用的企业级大模型算力平台。提供超低延迟推理响应、高可用弹性计算集群、Token 级精准额度计量与全自动订阅履约。
          </p>

          <!-- 4 大核心能力微卡片矩阵 -->
          <div class="grid grid-cols-1 sm:grid-cols-2 gap-3.5 mt-8 max-w-xl">
            <div class="p-3.5 rounded-xl bg-slate-900/60 border border-slate-800/80 backdrop-blur-md flex items-start gap-3 hover:border-slate-700 transition-colors">
              <div class="w-8 h-8 rounded-lg bg-indigo-500/20 text-indigo-400 flex items-center justify-center shrink-0">
                <span class="material-symbols-outlined text-18px">hub</span>
              </div>
              <div>
                <h4 class="text-xs font-bold text-slate-200">多模型全矩阵部署</h4>
                <p class="text-[11px] text-slate-400 mt-0.5">DeepSeek / OpenAI / Claude / Gemini 兼容协议极速响应</p>
              </div>
            </div>

            <div class="p-3.5 rounded-xl bg-slate-900/60 border border-slate-800/80 backdrop-blur-md flex items-start gap-3 hover:border-slate-700 transition-colors">
              <div class="w-8 h-8 rounded-lg bg-cyan-500/20 text-cyan-400 flex items-center justify-center shrink-0">
                <span class="material-symbols-outlined text-18px">bolt</span>
              </div>
              <div>
                <h4 class="text-xs font-bold text-slate-200">集群高可用保障</h4>
                <p class="text-[11px] text-slate-400 mt-0.5">节点毫秒级健康巡检与弹性负载高可用调度</p>
              </div>
            </div>

            <div class="p-3.5 rounded-xl bg-slate-900/60 border border-slate-800/80 backdrop-blur-md flex items-start gap-3 hover:border-slate-700 transition-colors">
              <div class="w-8 h-8 rounded-lg bg-emerald-500/20 text-emerald-400 flex items-center justify-center shrink-0">
                <span class="material-symbols-outlined text-18px">account_balance_wallet</span>
              </div>
              <div>
                <h4 class="text-xs font-bold text-slate-200">细粒度 Token 计量</h4>
                <p class="text-[11px] text-slate-400 mt-0.5">精准跟踪多 API Key 用量流水与实时计费限流</p>
              </div>
            </div>

            <div class="p-3.5 rounded-xl bg-slate-900/60 border border-slate-800/80 backdrop-blur-md flex items-start gap-3 hover:border-slate-700 transition-colors">
              <div class="w-8 h-8 rounded-lg bg-purple-500/20 text-purple-400 flex items-center justify-center shrink-0">
                <span class="material-symbols-outlined text-18px">verified_user</span>
              </div>
              <div>
                <h4 class="text-xs font-bold text-slate-200">高安全数字凭证</h4>
                <p class="text-[11px] text-slate-400 mt-0.5">HMAC-SHA256 签名校验与多级凭证安全隔离</p>
              </div>
            </div>
          </div>

          <!-- 极客终端调用代码预览 -->
          <div class="mt-8 max-w-xl hidden sm:block">
            <div class="rounded-xl bg-slate-950/90 border border-slate-800/90 shadow-2xl overflow-hidden">
              <div class="flex items-center justify-between px-4 py-2.5 bg-slate-900/80 border-b border-slate-800/80 text-xs text-slate-400 font-mono">
                <div class="flex items-center gap-1.5">
                  <span class="w-2.5 h-2.5 rounded-full bg-rose-500/70 inline-block"></span>
                  <span class="w-2.5 h-2.5 rounded-full bg-amber-500/70 inline-block"></span>
                  <span class="w-2.5 h-2.5 rounded-full bg-emerald-500/70 inline-block"></span>
                  <span class="ml-2 text-slate-400 font-semibold">Terminal · MAX API</span>
                </div>
                <span class="text-emerald-400 flex items-center gap-1 font-semibold">
                  <span class="w-1.5 h-1.5 rounded-full bg-emerald-400"></span>
                  Ready
                </span>
              </div>
              <div class="p-3.5 font-mono text-xs text-slate-300 space-y-1 bg-black/40 overflow-x-auto">
                <div class="text-slate-500"># 发起标准兼容推理请求并由 MAX API 高性能算力集群响应</div>
                <div><span class="text-pink-400">curl</span> https://api.openmax.io/v1/chat/completions \</div>
                <div class="pl-4 text-slate-400">-H <span class="text-amber-300">"Authorization: Bearer sk-openmax-live"</span> \</div>
                <div class="pl-4 text-slate-400">-d <span class="text-indigo-300">'{"model": "auto", "messages": [{"role": "user", "content": "Hi!"}]}'</span></div>
                <div class="text-emerald-400 pt-1">
                  &lt; HTTP/1.1 200 OK [Latency: 86ms · Accelerated]
                </div>
              </div>
            </div>
          </div>
        </div>

        <!-- 右侧：高质感玻璃拟态认证交互中心 -->
        <div class="lg:col-span-5 w-full max-w-[460px] mx-auto">
          <!-- 顶部快捷辅助工具行 -->
          <div class="flex items-center justify-between mb-3 text-xs">
            <router-link
              to="/pricing"
              class="inline-flex items-center gap-1.5 text-slate-400 hover:text-indigo-300 transition-colors py-1 px-2.5 rounded-lg bg-slate-900/40 border border-slate-800 hover:border-slate-700"
            >
              <span class="material-symbols-outlined text-15px">arrow_back</span>
              <span>返回订阅套餐</span>
            </router-link>
            <span class="font-mono text-[11px] text-indigo-400/90 font-medium px-2 py-0.5 rounded-md bg-indigo-500/10 border border-indigo-500/20">
              MAX API Gateway
            </span>
          </div>

          <!-- 核心认证卡片 -->
          <div class="relative bg-slate-900/85 backdrop-blur-2xl border border-slate-800/90 shadow-2xl shadow-indigo-950/50 rounded-3xl p-6 sm:p-8">
            <!-- 品牌标识与标题 -->
            <div class="text-center mb-6">
              <div class="inline-flex items-center justify-center w-14 h-14 rounded-2xl bg-gradient-to-tr from-indigo-600/30 via-indigo-500/20 to-purple-500/30 text-indigo-400 mb-3.5 border border-indigo-500/40 shadow-lg shadow-indigo-500/25">
                <span class="material-symbols-outlined text-30px">rocket_launch</span>
              </div>
              <h2 class="text-2xl font-black gradient-text tracking-tight">MAX API</h2>
              <p class="text-xs text-slate-400 mt-1.5 font-medium">进入开发者控制台以管理 API 密钥与订阅</p>
            </div>

            <!-- Tab 胶囊切换栏 -->
            <div class="flex items-center p-1 bg-slate-950/80 rounded-xl border border-slate-800/90 mb-5">
              <button
                type="button"
                class="flex-1 py-2 text-xs font-semibold rounded-lg transition-all duration-200 cursor-pointer flex items-center justify-center gap-1.5"
                :class="isLogin ? 'bg-indigo-600 text-white shadow-md shadow-indigo-600/30' : 'text-slate-400 hover:text-slate-200'"
                @click="switchTab(true)"
              >
                <span class="material-symbols-outlined text-15px">login</span>
                <span>账号登录</span>
              </button>
              <button
                type="button"
                class="flex-1 py-2 text-xs font-semibold rounded-lg transition-all duration-200 cursor-pointer flex items-center justify-center gap-1.5"
                :class="!isLogin ? 'bg-indigo-600 text-white shadow-md shadow-indigo-600/30' : 'text-slate-400 hover:text-slate-200'"
                @click="switchTab(false)"
              >
                <span class="material-symbols-outlined text-15px">person_add</span>
                <span>新用户注册</span>
              </button>
            </div>

            <!-- 认证表单（抗浏览器嗅探与干扰设计） -->
            <div class="flex flex-col gap-4" @keydown.enter="handleSubmit">
              <!-- 用户名输入 -->
              <div class="flex flex-col gap-1.5">
                <div class="flex items-center justify-between">
                  <label class="text-xs font-medium text-slate-300">用户名</label>
                  <span class="text-[11px] text-slate-500">必填</span>
                </div>
                <div class="relative flex items-center">
                  <span class="absolute left-3.5 text-slate-400 pointer-events-none material-symbols-outlined text-18px select-none">
                    person
                  </span>
                  <input
                    v-model="form.username"
                    type="text"
                    required
                    autocomplete="off"
                    data-lpignore="true"
                    data-form-type="other"
                    class="w-full bg-slate-950/80 border border-slate-800 rounded-xl pl-10 pr-4 py-2.5 text-sm text-slate-100 placeholder-slate-500 focus:outline-none focus:border-indigo-500 focus:ring-2 focus:ring-indigo-500/20 transition-all"
                    placeholder="请输入登录用户名"
                  />
                </div>
              </div>

              <!-- 邮箱输入（注册时显示） -->
              <div v-if="!isLogin" class="flex flex-col gap-1.5">
                <div class="flex items-center justify-between">
                  <label class="text-xs font-medium text-slate-300">电子邮箱</label>
                  <span class="text-[11px] text-slate-500">选填 · 凭证与通知</span>
                </div>
                <div class="relative flex items-center">
                  <span class="absolute left-3.5 text-slate-400 pointer-events-none material-symbols-outlined text-18px select-none">
                    mail
                  </span>
                  <input
                    v-model="form.email"
                    type="email"
                    autocomplete="off"
                    data-lpignore="true"
                    data-form-type="other"
                    class="w-full bg-slate-950/80 border border-slate-800 rounded-xl pl-10 pr-4 py-2.5 text-sm text-slate-100 placeholder-slate-500 focus:outline-none focus:border-indigo-500 focus:ring-2 focus:ring-indigo-500/20 transition-all"
                    placeholder="user@example.com"
                  />
                </div>
              </div>

              <!-- 密码输入（采用 -webkit-text-security 安全密文伪装，从物理层阻断浏览器弱口令泄露弹窗嗅探） -->
              <div class="flex flex-col gap-1.5">
                <div class="flex items-center justify-between">
                  <label class="text-xs font-medium text-slate-300">
                    {{ isLogin ? '登录密码' : '设置密码' }}
                  </label>
                  <span class="text-[11px] text-slate-500">不少于 6 位</span>
                </div>
                <div class="relative flex items-center">
                  <span class="absolute left-3.5 text-slate-400 pointer-events-none material-symbols-outlined text-18px select-none">
                    lock
                  </span>
                  <input
                    v-model="form.password"
                    type="text"
                    required
                    minlength="6"
                    autocomplete="off"
                    data-lpignore="true"
                    data-form-type="other"
                    :class="{ 'password-mask': !showPassword }"
                    class="w-full bg-slate-950/80 border border-slate-800 rounded-xl pl-10 pr-10 py-2.5 text-sm text-slate-100 placeholder-slate-500 focus:outline-none focus:border-indigo-500 focus:ring-2 focus:ring-indigo-500/20 transition-all"
                    placeholder="请输入密码（不少于6位）"
                  />
                  <button
                    type="button"
                    class="absolute right-3 p-1 text-slate-400 hover:text-slate-200 transition-colors cursor-pointer flex items-center"
                    @click="showPassword = !showPassword"
                    title="切换密码可见性"
                  >
                    <span class="material-symbols-outlined text-18px">
                      {{ showPassword ? 'visibility_off' : 'visibility' }}
                    </span>
                  </button>
                </div>
              </div>

              <!-- 确认密码（注册时显示） -->
              <div v-if="!isLogin" class="flex flex-col gap-1.5">
                <div class="flex items-center justify-between">
                  <label class="text-xs font-medium text-slate-300">确认密码</label>
                  <span class="text-[11px] text-slate-500">再次输入密码</span>
                </div>
                <div class="relative flex items-center">
                  <span class="absolute left-3.5 text-slate-400 pointer-events-none material-symbols-outlined text-18px select-none">
                    verified_user
                  </span>
                  <input
                    v-model="form.confirmPassword"
                    type="text"
                    required
                    minlength="6"
                    autocomplete="off"
                    data-lpignore="true"
                    data-form-type="other"
                    :class="{ 'password-mask': !showConfirmPassword }"
                    class="w-full bg-slate-950/80 border border-slate-800 rounded-xl pl-10 pr-10 py-2.5 text-sm text-slate-100 placeholder-slate-500 focus:outline-none focus:border-indigo-500 focus:ring-2 focus:ring-indigo-500/20 transition-all"
                    placeholder="请再次输入密码以防输错"
                  />
                  <button
                    type="button"
                    class="absolute right-3 p-1 text-slate-400 hover:text-slate-200 transition-colors cursor-pointer flex items-center"
                    @click="showConfirmPassword = !showConfirmPassword"
                    title="切换确认密码可见性"
                  >
                    <span class="material-symbols-outlined text-18px">
                      {{ showConfirmPassword ? 'visibility_off' : 'visibility' }}
                    </span>
                  </button>
                </div>
              </div>

              <!-- 错误信息提示 -->
              <div
                v-if="errorMsg"
                class="p-3 rounded-xl bg-rose-500/10 border border-rose-500/30 text-rose-400 text-xs flex items-center justify-between gap-2"
              >
                <div class="flex items-center gap-2">
                  <span class="material-symbols-outlined text-16px text-rose-400">error</span>
                  <span>{{ errorMsg }}</span>
                </div>
                <button
                  type="button"
                  class="text-rose-400/80 hover:text-rose-300 cursor-pointer"
                  @click="errorMsg = ''"
                >
                  <span class="material-symbols-outlined text-14px">close</span>
                </button>
              </div>

              <!-- 提交主按钮 -->
              <button
                type="button"
                :disabled="loading"
                @click="handleSubmit"
                class="w-full py-3 px-4 mt-1 bg-gradient-to-r from-indigo-500 via-indigo-600 to-purple-600 hover:from-indigo-600 hover:to-purple-700 text-white font-semibold text-sm rounded-xl shadow-lg shadow-indigo-600/30 hover:shadow-indigo-600/50 hover:-translate-y-0.5 active:translate-y-0 transition-all duration-200 cursor-pointer flex items-center justify-center gap-2 disabled:opacity-60 disabled:cursor-not-allowed disabled:transform-none"
              >
                <span v-if="loading" class="material-symbols-outlined text-18px animate-spin">
                  progress_activity
                </span>
                <span v-else class="material-symbols-outlined text-18px">
                  {{ isLogin ? 'login' : 'how_to_reg' }}
                </span>
                <span>{{ loading ? '处理中...' : (isLogin ? '立即登录进入控制台' : '创建账号并开启服务') }}</span>
              </button>
            </div>
          </div>

          <!-- 底部版权信息 -->
          <div class="mt-5 text-center text-xs text-slate-500 font-mono">
            MAX API · Enterprise High Throughput Model Gateway
          </div>
        </div>

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
const showPassword = ref(false)
const showConfirmPassword = ref(false)

const form = reactive({
  username: '',
  email: '',
  password: '',
  confirmPassword: '',
})

function switchTab(login: boolean) {
  isLogin.value = login
  errorMsg.value = ''
}

async function handleSubmit() {
  errorMsg.value = ''

  if (!isLogin.value) {
    if (form.password.length < 6) {
      errorMsg.value = '密码长度不能少于 6 位'
      return
    }
    if (form.password !== form.confirmPassword) {
      errorMsg.value = '两次输入的密码不一致，请重新确认'
      return
    }
  }

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

<style scoped>
.password-mask {
  -webkit-text-security: disc;
}
</style>
