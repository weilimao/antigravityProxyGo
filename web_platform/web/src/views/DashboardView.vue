<template>
  <div class="max-w-6xl mx-auto px-4 py-6 flex flex-col gap-6">
    <!-- 用户概览与订阅状态卡片 -->
    <div class="glass-card-glow p-6 flex flex-wrap items-center justify-between gap-4">
      <div class="flex items-center gap-4">
        <div class="w-14 h-14 rounded-2xl bg-gradient-to-tr from-indigo-600 to-pink-500 flex items-center justify-center text-white shadow-md">
          <span class="material-symbols-outlined text-32px">account_circle</span>
        </div>
        <div>
          <div class="flex items-center gap-2">
            <h2 class="text-xl font-bold text-white">{{ user?.username || '加载中...' }}</h2>
            <span v-if="user?.role === 'admin'" class="badge badge-amber">管理员</span>
            <span v-if="user?.isActive" class="badge badge-emerald">已激活订阅</span>
            <span v-else class="badge badge-rose">无有效订阅</span>
          </div>
          <p class="text-xs text-slate-400 mt-1">
            当前套餐: <strong class="text-white">{{ user?.plan?.name || '无激活套餐' }}</strong>
            <span v-if="user?.planExpireAt" class="ml-2">
              (有效期至: {{ formatTime(user?.planExpireAt) }})
            </span>
            <span v-if="user?.plan" class="ml-2 text-slate-300">
              · 额度限制: <strong class="text-indigo-300">{{ formatTokenDisplay(user?.plan?.tokenLimit) }}</strong>
            </span>
          </p>
          <!-- 若套餐配置了 Auto 包含模型说明，在控制台顶部清晰回显 -->
          <div v-if="user?.plan?.autoModels && user.plan.autoModels.length > 0" class="flex flex-wrap items-center gap-1 mt-1.5 text-[11px] text-amber-300/90">
            <span class="material-symbols-outlined text-[13px] text-amber-400">bolt</span>
            <span>Auto包含:</span>
            <span v-for="am in user.plan.autoModels" :key="am" class="px-1.5 py-0.2 rounded text-[10px] font-mono bg-amber-500/15 text-amber-200 border border-amber-500/30">
              {{ am }}
            </span>
          </div>
        </div>
      </div>

      <div class="flex items-center gap-3">
        <router-link to="/pricing" class="btn-primary text-xs">
          <span class="material-symbols-outlined text-16px">upgrade</span>
          <span>{{ user?.isActive ? '续费或升配套餐' : '立即选购套餐' }}</span>
        </router-link>
      </div>
    </div>

    <!-- 套餐使用量监控面板 (Package Token Usage & Quota Bar) -->
    <div class="glass-card p-6 flex flex-col gap-4 border-indigo-500/20 bg-slate-900/50 relative overflow-hidden">
      <!-- 装饰背景光晕 -->
      <div class="absolute -right-20 -top-20 w-60 h-60 rounded-full bg-indigo-500/10 blur-3xl pointer-events-none"></div>

      <div class="flex items-center justify-between flex-wrap gap-2">
        <div class="flex items-center gap-2">
          <span class="w-8 h-8 rounded-lg bg-indigo-500/15 text-indigo-400 border border-indigo-500/25 flex items-center justify-center">
            <span class="material-symbols-outlined text-18px">data_usage</span>
          </span>
          <div>
            <h3 class="text-sm font-bold text-white flex items-center gap-2">
              <span>套餐算力使用量</span>
              <span class="text-11px px-2 py-0.5 rounded-full font-medium" :class="usageBadgeClass">
                {{ usageBadgeText }}
              </span>
            </h3>
            <p class="text-11px text-slate-400 mt-0.5">一眼看清当前套餐总额度消耗与各 API Key 算力调用进度</p>
          </div>
        </div>

        <!-- 刷新与操作 -->
        <div class="flex items-center gap-2">
          <button
            type="button"
            class="btn-secondary text-11px py-1 px-2.5 flex items-center gap-1 cursor-pointer"
            :disabled="refreshingUsage"
            @click="refreshAllUsage"
            title="刷新最新使用量统计"
          >
            <span class="material-symbols-outlined text-14px" :class="{ 'animate-spin': refreshingUsage }">refresh</span>
            <span>刷新用量</span>
          </button>
          <router-link to="/pricing" class="btn-primary text-11px py-1 px-3 flex items-center gap-1">
            <span class="material-symbols-outlined text-14px">upgrade</span>
            <span>升级/增配</span>
          </router-link>
        </div>
      </div>

      <!-- 核心指标统计三列卡片 -->
      <div class="grid grid-cols-1 sm:grid-cols-3 gap-3.5">
        <!-- 1. 已消耗 Tokens -->
        <div class="p-3.5 rounded-xl bg-slate-950/60 border border-slate-800/80 flex flex-col justify-between">
          <div class="flex items-center justify-between text-slate-400 text-11px mb-1.5">
            <span>已消耗 Token</span>
            <span class="material-symbols-outlined text-amber-400 text-16px">trending_up</span>
          </div>
          <div class="flex items-baseline gap-1.5">
            <span class="text-xl font-mono font-extrabold text-white">{{ formatTokenNumber(totalUsedTokens) }}</span>
            <span class="text-10px text-slate-400 font-sans">Tokens</span>
          </div>
          <div class="text-[10px] text-slate-400 mt-1">
            约 {{ formatTokenDisplay(totalUsedTokens) }}
          </div>
        </div>

        <!-- 2. 剩余可用 Tokens -->
        <div class="p-3.5 rounded-xl bg-slate-950/60 border border-slate-800/80 flex flex-col justify-between">
          <div class="flex items-center justify-between text-slate-400 text-11px mb-1.5">
            <span>剩余可用 Token</span>
            <span class="material-symbols-outlined text-emerald-400 text-16px">check_circle</span>
          </div>
          <div class="flex items-baseline gap-1.5">
            <span class="text-xl font-mono font-extrabold" :class="remainingTokensColor">
              {{ planTokenLimit > 0 ? formatTokenNumber(remainingTokens) : '不限额度' }}
            </span>
            <span v-if="planTokenLimit > 0" class="text-10px text-slate-400 font-sans">Tokens</span>
          </div>
          <div class="text-[10px] text-slate-400 mt-1">
            {{ planTokenLimit > 0 ? `约 ${formatTokenDisplay(remainingTokens)}` : '无限配额，畅享全系模型' }}
          </div>
        </div>

        <!-- 3. 套餐总配额上限 -->
        <div class="p-3.5 rounded-xl bg-slate-950/60 border border-slate-800/80 flex flex-col justify-between">
          <div class="flex items-center justify-between text-slate-400 text-11px mb-1.5">
            <span>套餐总额度</span>
            <span class="material-symbols-outlined text-indigo-400 text-16px">toll</span>
          </div>
          <div class="flex items-baseline gap-1.5">
            <span class="text-xl font-mono font-extrabold text-indigo-300">
              {{ planTokenLimit > 0 ? formatTokenNumber(planTokenLimit) : '无上限' }}
            </span>
            <span v-if="planTokenLimit > 0" class="text-10px text-slate-400 font-sans">Tokens</span>
          </div>
          <div class="text-[10px] text-slate-400 mt-1">
            {{ planTokenLimit > 0 ? `约 ${formatTokenDisplay(planTokenLimit)}` : '不设配额上限' }}
          </div>
        </div>
      </div>

      <!-- 动态图形化进度条 -->
      <div class="p-3.5 bg-slate-950/40 rounded-xl border border-slate-800/70 flex flex-col gap-2">
        <div class="flex items-center justify-between text-xs font-semibold">
          <span class="text-slate-300 flex items-center gap-1.5">
            <span class="material-symbols-outlined text-14px text-indigo-400">speed</span>
            <span>算力额度消耗进度</span>
          </span>
          <span class="font-mono" :class="usagePercentColor">
            {{ planTokenLimit > 0 ? `${usagePercent.toFixed(1)}%` : '无限制畅用' }}
          </span>
        </div>

        <!-- 进度条背景槽 -->
        <div class="w-full h-3 bg-slate-800/80 rounded-full overflow-hidden p-0.5 border border-slate-700/50">
          <div
            v-if="planTokenLimit > 0"
            class="h-full rounded-full transition-all duration-500 shadow-sm"
            :class="progressBarClass"
            :style="{ width: `${Math.min(100, Math.max(0, usagePercent))}%` }"
          ></div>
          <div
            v-else
            class="h-full rounded-full bg-gradient-to-r from-emerald-500 via-teal-400 to-cyan-500 w-full animate-pulse"
          ></div>
        </div>

        <div class="flex items-center justify-between text-[10px] text-slate-400 font-mono">
          <span>0 Tokens</span>
          <span v-if="planTokenLimit > 0">总限额: {{ formatTokenDisplay(planTokenLimit) }}</span>
          <span v-else>无限制</span>
        </div>
      </div>
    </div>


    <!-- API Key 凭证管理卡片 -->
    <div class="glass-card p-6">
      <div class="flex items-center justify-between mb-4 pb-3 border-b border-slate-800">
        <div class="flex items-center gap-2">
          <span class="material-symbols-outlined text-indigo-400 text-20px">key</span>
          <h3 class="text-sm font-bold text-white">API 调用密钥 (API Keys)</h3>
        </div>
        <button type="button" class="btn-primary text-xs cursor-pointer" @click="showCreateKeyModal = true">
          <span class="material-symbols-outlined text-16px">add</span>
          <span>新建 API Key</span>
        </button>
      </div>

      <!-- Key 列表表格 -->
      <div class="overflow-x-auto">
        <table class="table-dark">
          <thead>
            <tr>
              <th>密钥名称</th>
              <th>密钥内容 (Key)</th>
              <th>授权模型范围</th>
              <th>Token 限额</th>
              <th>已用量 (Used)</th>
              <th>限频 (RPM)</th>
              <th>创建时间</th>
              <th class="text-right">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="k in keys" :key="k.id">
              <td class="font-medium text-white">{{ k.name }}</td>
              <td class="font-mono text-xs text-indigo-300">
                <span>{{ k.key.slice(0, 10) }}...{{ k.key.slice(-6) }}</span>
                <button
                  type="button"
                  class="ml-2 text-slate-400 hover:text-white cursor-pointer inline-flex items-center"
                  title="复制完整密钥"
                  @click="copyToClipboard(k.key)"
                >
                  <span class="material-symbols-outlined text-14px">content_copy</span>
                </button>
              </td>
              <td>
                <div class="flex flex-wrap gap-1 max-w-xs">
                  <span
                    v-for="m in (k.allowedModels || [])"
                    :key="m"
                    class="badge text-10px font-mono"
                    :class="m === 'auto' ? 'badge-amber font-bold' : 'badge-cyan'"
                    :title="m === 'auto' && user?.plan?.autoModels?.length ? `Auto实际包含: ${user.plan.autoModels.join(', ')}` : m"
                  >
                    {{ m === 'auto' ? '⚡ 支持 auto 模型' : m }}
                  </span>
                </div>
              </td>
              <td class="font-mono text-xs">
                <span v-if="!k.limitTokens || k.limitTokens <= 0" class="badge badge-emerald">不限额度</span>
                <span v-else class="text-indigo-300">{{ formatTokenDisplay(k.limitTokens) }}</span>
              </td>
              <td class="font-mono text-xs">
                <div class="flex flex-col gap-1">
                  <div class="flex items-center gap-1.5">
                    <span class="text-white font-semibold">{{ formatTokenNumber(k.usedTokens || 0) }}</span>
                    <span v-if="k.limitTokens > 0" class="text-[10px] text-slate-400">
                      ({{ (((k.usedTokens || 0) / k.limitTokens) * 100).toFixed(1) }}%)
                    </span>
                  </div>
                  <!-- 微型进度条 -->
                  <div v-if="k.limitTokens > 0" class="w-20 h-1.5 bg-slate-800 rounded-full overflow-hidden">
                    <div
                      class="h-full rounded-full transition-all duration-300"
                      :class="((k.usedTokens || 0) / k.limitTokens) >= 0.9 ? 'bg-rose-500' : ((k.usedTokens || 0) / k.limitTokens) >= 0.7 ? 'bg-amber-400' : 'bg-indigo-400'"
                      :style="{ width: `${Math.min(100, ((k.usedTokens || 0) / k.limitTokens) * 100)}%` }"
                    ></div>
                  </div>
                </div>
              </td>
              <td><span class="text-xs font-mono text-slate-300">{{ k.rateLimit }}</span></td>
              <td class="text-xs text-slate-400">{{ formatTime(k.createdAt) }}</td>
              <td class="text-right">
                <button
                  type="button"
                  class="btn-danger cursor-pointer"
                  @click="handleDeleteKey(k.id)"
                >
                  <span class="material-symbols-outlined text-14px">delete</span>
                  <span>删除</span>
                </button>
              </td>
            </tr>
            <tr v-if="keys.length === 0">
              <td colspan="8" class="text-center py-8 text-slate-500 text-xs">
                暂未创建 API 密钥，请点击右上角新建以开始调用。
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- 快速接入客户端指导卡片 (OpenAI 格式 与 Anthropic 格式) -->
    <div class="glass-card p-6">
      <div class="flex items-center justify-between mb-4 pb-3 border-b border-slate-800/80">
        <h3 class="text-sm font-bold text-white flex items-center gap-2">
          <span class="material-symbols-outlined text-indigo-400 text-18px">terminal</span>
          <span>快速接入与客户端配置</span>
        </h3>
        <!-- 复制状态轻量提示条 -->
        <transition name="fade">
          <div v-if="copyToast" class="flex items-center gap-1.5 px-3 py-1 rounded-full bg-emerald-500/20 text-emerald-300 border border-emerald-500/30 text-11px font-medium shadow-sm">
            <span class="material-symbols-outlined text-14px">check_circle</span>
            <span>{{ copyToast }}</span>
          </div>
        </transition>
      </div>

      <div class="grid grid-cols-1 lg:grid-cols-2 gap-5 text-xs">
        <!-- 1. OpenAI 兼容协议接入卡片 -->
        <div class="p-4 bg-slate-900/60 rounded-xl border border-slate-800 hover:border-slate-700/80 transition-all flex flex-col justify-between gap-4">
          <div>
            <!-- 头部协议标识 -->
            <div class="flex items-center justify-between mb-3 pb-2.5 border-b border-slate-800/60">
              <div class="flex items-center gap-2">
                <span class="w-7 h-7 rounded-lg bg-teal-500/15 text-teal-400 border border-teal-500/25 flex items-center justify-center">
                  <span class="material-symbols-outlined text-16px">smart_toy</span>
                </span>
                <div>
                  <h4 class="text-xs font-bold text-white flex items-center gap-1.5">
                    <span>OpenAI 兼容协议</span>
                    <span class="text-10px px-1.5 py-0.5 rounded bg-teal-500/20 text-teal-300 font-mono">OpenAI Compatible</span>
                  </h4>
                  <p class="text-10px text-slate-400 mt-0.5">NextChat · Cherry Studio · Cursor · Python SDK · 各种兼容客户端</p>
                </div>
              </div>
            </div>

            <!-- 参数区域 -->
            <div class="flex flex-col gap-2.5 font-mono text-11px">
              <div>
                <span class="text-slate-400 text-10px block mb-1 font-sans font-semibold">API 接口地址 (Base URL)</span>
                <div class="flex items-center justify-between bg-black/40 px-2.5 py-2 rounded-lg border border-slate-800 text-indigo-300 break-all group">
                  <span class="truncate mr-2">{{ openaiBaseUrl }}</span>
                  <button
                    type="button"
                    class="text-slate-400 hover:text-white shrink-0 cursor-pointer p-1 rounded hover:bg-slate-800 transition-colors"
                    @click="copyText(openaiBaseUrl, 'OpenAI Base URL')"
                    title="点击复制 Base URL"
                  >
                    <span class="material-symbols-outlined text-14px">content_copy</span>
                  </button>
                </div>
              </div>

              <div>
                <div class="flex items-center justify-between mb-1 font-sans">
                  <span class="text-slate-400 text-10px font-semibold">调用密钥 (API Key)</span>
                  <span v-if="keys.length === 0" class="text-10px text-amber-400">尚未建 Key，点击右上角新建</span>
                </div>
                <div class="flex items-center justify-between bg-black/40 px-2.5 py-2 rounded-lg border border-slate-800 text-slate-300 break-all">
                  <span class="truncate mr-2 font-mono text-11px">{{ activeApiKey }}</span>
                  <button
                    type="button"
                    :disabled="keys.length === 0"
                    class="text-slate-400 hover:text-white shrink-0 cursor-pointer p-1 rounded hover:bg-slate-800 transition-colors disabled:opacity-40"
                    @click="copyText(activeApiKey, 'API Key')"
                    title="点击复制 API Key"
                  >
                    <span class="material-symbols-outlined text-14px">content_copy</span>
                  </button>
                </div>
              </div>

              <div>
                <span class="text-slate-400 text-10px block mb-1 font-sans font-semibold">终端环境变量一键配置 (Shell)</span>
                <pre class="bg-slate-950/80 p-2.5 rounded-lg border border-slate-800/80 text-11px text-slate-300 leading-relaxed overflow-x-auto whitespace-pre-wrap select-all">export OPENAI_BASE_URL="{{ openaiBaseUrl }}"
export OPENAI_API_KEY="{{ activeApiKey }}"</pre>
              </div>
            </div>
          </div>

          <div class="pt-2 border-t border-slate-800/60 flex items-center justify-between">
            <span class="text-10px text-slate-500 font-sans">模型填写建议：<code class="text-indigo-400">auto</code> (并发竞速) 或套餐授权模型</span>
            <button
              type="button"
              class="btn-secondary text-10px py-1 px-2.5 flex items-center gap-1 shrink-0 cursor-pointer"
              @click="copyOpenAIEnv"
            >
              <span class="material-symbols-outlined text-12px">terminal</span>
              <span>复制环境变量</span>
            </button>
          </div>
        </div>

        <!-- 2. Anthropic 兼容协议接入卡片 -->
        <div class="p-4 bg-slate-900/60 rounded-xl border border-slate-800 hover:border-slate-700/80 transition-all flex flex-col justify-between gap-4">
          <div>
            <!-- 头部协议标识 -->
            <div class="flex items-center justify-between mb-3 pb-2.5 border-b border-slate-800/60">
              <div class="flex items-center gap-2">
                <span class="w-7 h-7 rounded-lg bg-amber-500/15 text-amber-400 border border-amber-500/25 flex items-center justify-center">
                  <span class="material-symbols-outlined text-16px">psychology</span>
                </span>
                <div>
                  <h4 class="text-xs font-bold text-white flex items-center gap-1.5">
                    <span>Anthropic 兼容协议</span>
                    <span class="text-10px px-1.5 py-0.5 rounded bg-amber-500/20 text-amber-300 font-mono">Anthropic Compatible</span>
                  </h4>
                  <p class="text-10px text-slate-400 mt-0.5">Claude Code · Claude Desktop · Cline · Roo Code · 官方 SDK</p>
                </div>
              </div>
            </div>

            <!-- 参数区域 -->
            <div class="flex flex-col gap-2.5 font-mono text-11px">
              <div>
                <span class="text-slate-400 text-10px block mb-1 font-sans font-semibold">API 接口地址 (Base URL)</span>
                <div class="flex items-center justify-between bg-black/40 px-2.5 py-2 rounded-lg border border-slate-800 text-amber-300 break-all group">
                  <span class="truncate mr-2">{{ anthropicBaseUrl }}</span>
                  <button
                    type="button"
                    class="text-slate-400 hover:text-white shrink-0 cursor-pointer p-1 rounded hover:bg-slate-800 transition-colors"
                    @click="copyText(anthropicBaseUrl, 'Anthropic Base URL')"
                    title="点击复制 Base URL"
                  >
                    <span class="material-symbols-outlined text-14px">content_copy</span>
                  </button>
                </div>
              </div>

              <div>
                <div class="flex items-center justify-between mb-1 font-sans">
                  <span class="text-slate-400 text-10px font-semibold">调用密钥 (API Key)</span>
                  <span v-if="keys.length === 0" class="text-10px text-amber-400">尚未建 Key，点击右上角新建</span>
                </div>
                <div class="flex items-center justify-between bg-black/40 px-2.5 py-2 rounded-lg border border-slate-800 text-slate-300 break-all">
                  <span class="truncate mr-2 font-mono text-11px">{{ activeApiKey }}</span>
                  <button
                    type="button"
                    :disabled="keys.length === 0"
                    class="text-slate-400 hover:text-white shrink-0 cursor-pointer p-1 rounded hover:bg-slate-800 transition-colors disabled:opacity-40"
                    @click="copyText(activeApiKey, 'API Key')"
                    title="点击复制 API Key"
                  >
                    <span class="material-symbols-outlined text-14px">content_copy</span>
                  </button>
                </div>
              </div>

              <div>
                <span class="text-slate-400 text-10px block mb-1 font-sans font-semibold">Claude Code 终端环境直连接入</span>
                <pre class="bg-slate-950/80 p-2.5 rounded-lg border border-slate-800/80 text-11px text-slate-300 leading-relaxed overflow-x-auto whitespace-pre-wrap select-all">export ANTHROPIC_BASE_URL="{{ anthropicBaseUrl }}"
export ANTHROPIC_API_KEY="{{ activeApiKey }}"</pre>
              </div>
            </div>
          </div>

          <div class="pt-2 border-t border-slate-800/60 flex items-center justify-between">
            <span class="text-10px text-slate-500 font-sans">终端执行上述命令后，直接输入 <code class="text-amber-400">claude</code> 即可启动</span>
            <button
              type="button"
              class="btn-secondary text-10px py-1 px-2.5 flex items-center gap-1 shrink-0 cursor-pointer"
              @click="copyAnthropicEnv"
            >
              <span class="material-symbols-outlined text-12px">terminal</span>
              <span>复制 Claude Code 命令</span>
            </button>
          </div>
        </div>
      </div>
    </div>

    <!-- 请求命中模型日志监控卡片 (按当前用户账号隔离) -->
    <div class="glass-card p-6 flex flex-col gap-4">
      <RequestLogsPanel mode="user" />
    </div>

    <!-- 新建 Key Modal 弹窗 -->
    <div v-if="showCreateKeyModal" class="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/70 backdrop-blur-sm">
      <div class="glass-card-glow w-full max-w-md p-6">
        <div class="flex items-center justify-between mb-4">
          <h3 class="text-base font-bold text-white">新建 API 调用密钥</h3>
          <span class="material-symbols-outlined text-slate-400 hover:text-white cursor-pointer" @click="showCreateKeyModal = false">close</span>
        </div>
        <div class="flex flex-col gap-4">
          <div>
            <label class="block text-xs text-slate-300 mb-1">密钥描述名称</label>
            <input v-model="newKeyForm.name" type="text" class="input-dark w-full" placeholder="如: OpenCode 专用" />
          </div>
          <p class="text-11px text-slate-400 leading-relaxed">
            该密钥由中继服务实时签发与绑定，授权调用范围为 <code class="text-amber-400 font-mono">auto</code> (并发竞速) 以及管理员在后台配置的当前套餐模型。非授权模型将被中继网关自动拦截。
          </p>
          <div class="flex items-center justify-end gap-3 mt-2">
            <button type="button" class="btn-secondary text-xs" @click="showCreateKeyModal = false">取消</button>
            <button type="button" class="btn-primary text-xs" @click="handleCreateKey">确认生成</button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import { authApi, keyApi, systemApi, type SystemConfig } from '../api/client'
import RequestLogsPanel from '../components/logs/RequestLogsPanel.vue'

const user = ref<any>(null)
const keys = ref<any[]>([])
const systemConfig = ref<SystemConfig | null>(null)
const copyToast = ref('')
let toastTimer: any = null

const savingAuto = ref(false)
const showCreateKeyModal = ref(false)

const newKeyForm = reactive({
  name: '',
})

const currentHostname = window.location.hostname || '127.0.0.1'

const apiBaseUrl = computed(() => {
  if (systemConfig.value?.apiBaseUrl) {
    return systemConfig.value.apiBaseUrl.trim().replace(/\/+$/, '')
  }
  return `http://${currentHostname}:18444`
})

const openaiBaseUrl = computed(() => {
  return `${apiBaseUrl.value}/v1`
})

const anthropicBaseUrl = computed(() => {
  return apiBaseUrl.value
})

const activeApiKey = computed(() => {
  if (keys.value && keys.value.length > 0) {
    return keys.value[0].key || 'sk-ant-xxxxxxxx'
  }
  return 'sk-ant-xxxxxxxx'
})

function copyText(val: string, label: string) {
  if (!val) return
  navigator.clipboard.writeText(val)
  copyToast.value = `${label} 已复制到剪贴板`
  if (toastTimer) clearTimeout(toastTimer)
  toastTimer = setTimeout(() => {
    copyToast.value = ''
  }, 2500)
}

function copyOpenAIEnv() {
  const text = `export OPENAI_BASE_URL="${openaiBaseUrl.value}"\nexport OPENAI_API_KEY="${activeApiKey.value}"`
  copyText(text, 'OpenAI 环境变量命令')
}

function copyAnthropicEnv() {
  const text = `export ANTHROPIC_BASE_URL="${anthropicBaseUrl.value}"\nexport ANTHROPIC_API_KEY="${activeApiKey.value}"`
  copyText(text, 'Claude Code 启动命令')
}

async function fetchUserData() {
  try {
    user.value = await authApi.getMe()
  } catch (err) {
    console.error(err)
  }
}

async function fetchKeys() {
  try {
    keys.value = await keyApi.list()
  } catch (err) {
    console.error(err)
  }
}

async function fetchSystemConfig() {
  try {
    systemConfig.value = await systemApi.getPublicConfig()
  } catch (err) {
    console.error('获取系统公开配置失败:', err)
  }
}

async function handleCreateKey() {
  try {
    await keyApi.create({
      name: newKeyForm.name || '默认密钥',
    })
    showCreateKeyModal.value = false
    newKeyForm.name = ''
    fetchKeys()
  } catch (err: any) {
    alert(err.message || '创建密钥失败')
  }
}

async function handleDeleteKey(id: number) {
  if (!confirm('确定要删除此 API Key 吗？相关客户端将无法继续调用。')) return
  try {
    await keyApi.delete(id)
    fetchKeys()
  } catch (err: any) {
    alert(err.message || '删除失败')
  }
}

function copyToClipboard(text: string) {
  navigator.clipboard.writeText(text)
  copyToast.value = '密钥已复制到剪贴板'
  if (toastTimer) clearTimeout(toastTimer)
  toastTimer = setTimeout(() => {
    copyToast.value = ''
  }, 2500)
}

function formatTime(timestamp: number | string): string {
  if (!timestamp) return '未设置'
  let d: Date
  if (typeof timestamp === 'number') {
    d = new Date(timestamp * 1000)
  } else {
    d = new Date(timestamp)
  }
  return d.toLocaleString()
}

function formatTokenDisplay(val?: number): string {
  if (!val || val <= 0) return '不限额度'
  if (val >= 100000000) return (val / 100000000).toFixed(val % 100000000 === 0 ? 0 : 2) + ' 亿 Tokens'
  if (val >= 10000) return (val / 10000).toFixed(val % 10000 === 0 ? 0 : 1) + ' 万 Tokens'
  if (val >= 1000000) return (val / 1000000).toFixed(1) + 'M Tokens'
  if (val >= 1000) return (val / 1000).toFixed(0) + 'K Tokens'
  return `${val.toLocaleString()} Tokens`
}

const refreshingUsage = ref(false)

const totalUsedTokens = computed(() => {
  return keys.value.reduce((acc, k) => acc + (Number(k.usedTokens) || 0), 0)
})

const planTokenLimit = computed(() => {
  return Number(user.value?.plan?.tokenLimit) || 0
})

const remainingTokens = computed(() => {
  if (planTokenLimit.value <= 0) return 0
  return Math.max(0, planTokenLimit.value - totalUsedTokens.value)
})

const usagePercent = computed(() => {
  if (planTokenLimit.value <= 0) return 0
  return Math.min(100, (totalUsedTokens.value / planTokenLimit.value) * 100)
})

const usageBadgeText = computed(() => {
  if (planTokenLimit.value <= 0) return '不限配额'
  if (usagePercent.value >= 90) return '额度告急'
  if (usagePercent.value >= 70) return '注意余量'
  return '额度充裕'
})

const usageBadgeClass = computed(() => {
  if (planTokenLimit.value <= 0) return 'bg-emerald-500/15 text-emerald-300 border border-emerald-500/30'
  if (usagePercent.value >= 90) return 'bg-rose-500/15 text-rose-300 border border-rose-500/30'
  if (usagePercent.value >= 70) return 'bg-amber-500/15 text-amber-300 border border-amber-500/30'
  return 'bg-indigo-500/15 text-indigo-300 border border-indigo-500/30'
})

const remainingTokensColor = computed(() => {
  if (planTokenLimit.value <= 0) return 'text-emerald-400'
  if (usagePercent.value >= 90) return 'text-rose-400'
  if (usagePercent.value >= 70) return 'text-amber-400'
  return 'text-emerald-400'
})

const usagePercentColor = computed(() => {
  if (planTokenLimit.value <= 0) return 'text-emerald-400'
  if (usagePercent.value >= 90) return 'text-rose-400'
  if (usagePercent.value >= 70) return 'text-amber-400'
  return 'text-indigo-400'
})

const progressBarClass = computed(() => {
  if (usagePercent.value >= 90) return 'bg-gradient-to-r from-rose-600 via-rose-500 to-red-400'
  if (usagePercent.value >= 70) return 'bg-gradient-to-r from-amber-500 via-orange-500 to-amber-400'
  return 'bg-gradient-to-r from-indigo-500 via-cyan-400 to-emerald-400'
})

function formatTokenNumber(val: number): string {
  if (!val || isNaN(val)) return '0'
  return Math.round(val).toLocaleString()
}

async function refreshAllUsage() {
  refreshingUsage.value = true
  try {
    await Promise.all([fetchUserData(), fetchKeys()])
  } finally {
    setTimeout(() => {
      refreshingUsage.value = false
    }, 400)
  }
}

onMounted(() => {
  fetchUserData()
  fetchKeys()
  fetchSystemConfig()
})
</script>
