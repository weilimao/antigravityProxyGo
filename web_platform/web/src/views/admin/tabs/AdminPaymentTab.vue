<template>
  <div class="w-full flex flex-col gap-6">
    <div class="glass-card p-6">
      <!-- 头部控制栏 -->
      <div class="flex items-center justify-between gap-4 mb-4 pb-3 border-b border-slate-800">
        <div class="flex items-center gap-3 min-w-0">
          <span class="w-10 h-10 rounded-xl bg-indigo-500/10 text-indigo-400 flex items-center justify-center border border-indigo-500/20 shrink-0">
            <span class="material-symbols-outlined text-24px">payments</span>
          </span>
          <div class="min-w-0">
            <h3 class="text-base font-bold text-white">支付网关与切单中继跳转配置 (Payment & Relay Pipeline)</h3>
            <p class="text-xs text-slate-400 truncate sm:whitespace-normal">
              参考 ProxySubForClash：配置前台商业化套餐订阅支付跳转通道，支持极客工坊合规收银中继切单、彩虹易支付直连及开发沙箱
            </p>
          </div>
        </div>
        <div class="flex items-center gap-2 shrink-0">
          <button type="button" :disabled="loading" class="btn-secondary text-xs flex items-center gap-1.5 whitespace-nowrap" @click="loadConfig" title="重新从数据库读取最新配置">
            <span class="material-symbols-outlined text-16px" :class="{ 'animate-spin': loading }">refresh</span>
            <span>刷新</span>
          </button>
          <button type="button" :disabled="saving" class="btn-primary text-xs flex items-center gap-1.5 whitespace-nowrap" @click="saveConfig">
            <span class="material-symbols-outlined text-16px" :class="{ 'animate-spin': saving }">save</span>
            <span>{{ saving ? '保存中...' : '保存支付配置' }}</span>
          </button>
        </div>
      </div>

      <!-- 反馈提示条 -->
      <div v-if="feedbackMsg" class="mb-4 p-3 rounded-lg flex items-center justify-between text-xs" :class="feedbackSuccess ? 'bg-emerald-500/10 text-emerald-300 border border-emerald-500/30' : 'bg-rose-500/10 text-rose-300 border border-rose-500/30'">
        <div class="flex items-center gap-2">
          <span class="material-symbols-outlined text-16px">{{ feedbackSuccess ? 'check_circle' : 'error' }}</span>
          <span>{{ feedbackMsg }}</span>
        </div>
        <button type="button" @click="feedbackMsg = ''" class="hover:opacity-80">
          <span class="material-symbols-outlined text-14px">close</span>
        </button>
      </div>

      <!-- 1. 主通道选择与模式状态指示 -->
      <div class="p-4 rounded-xl bg-slate-900/60 border border-slate-800 flex flex-col md:flex-row md:items-center justify-between gap-4 mb-6">
        <div class="flex items-center gap-3">
          <div class="text-xs">
            <span class="block font-bold text-white">当前主支付通道模式</span>
            <span class="text-slate-400 text-11px">控制前台用户在精选套餐结算时唤起哪种收银跳转方式</span>
          </div>
          <select v-model="form.payProvider" class="input-dark text-xs py-1.5 px-3 rounded-lg font-semibold bg-slate-800 border-slate-700 text-white cursor-pointer">
            <option value="epay">⚡ 易支付直连 / 中继切单 (epay)</option>
            <option value="fake">🧪 本地开发模拟沙箱 (fake)</option>
          </select>
        </div>

        <!-- 模式状态横幅 -->
        <div class="flex items-center gap-2">
          <template v-if="form.payProvider === 'epay'">
            <span v-if="form.relayUrl" class="badge badge-emerald flex items-center gap-1.5 py-1 px-2.5 text-xs font-semibold">
              <span class="material-symbols-outlined text-14px">hub</span>
              <span>跨站切单中继模式已就绪 (优先切单)</span>
            </span>
            <span v-else class="badge badge-amber flex items-center gap-1.5 py-1 px-2.5 text-xs font-semibold">
              <span class="material-symbols-outlined text-14px">open_in_new</span>
              <span>易支付直连模式 (中继地址留空时直连)</span>
            </span>
          </template>
          <template v-else>
            <span class="badge badge-cyan flex items-center gap-1.5 py-1 px-2.5 text-xs font-semibold">
              <span class="material-symbols-outlined text-14px">science</span>
              <span>开发测试沙箱已激活 (即时履约)</span>
            </span>
          </template>
        </div>
      </div>

      <!-- 2. 可视化拓扑流水线 (对标 ProxySubForClash) -->
      <div class="flex flex-col gap-4">
        <!-- 节点 1: A 站业务商城 -->
        <div class="p-4 rounded-xl bg-slate-900/40 border border-slate-800 hover:border-slate-700 transition-all">
          <div class="flex items-center justify-between mb-3 pb-2 border-b border-slate-800/80 gap-3">
            <div class="flex items-center gap-2 min-w-0">
              <span class="px-2 py-0.5 rounded text-10px font-mono font-bold bg-indigo-500/20 text-indigo-400 border border-indigo-500/30 shrink-0">节点 1</span>
              <span class="font-bold text-white text-xs flex items-center gap-1 shrink-0">
                <span class="material-symbols-outlined text-16px text-indigo-400">storefront</span>
                <span>A 站业务商城 (MAX API)</span>
              </span>
              <span class="text-11px text-slate-400 truncate hidden md:inline">发起套餐购买、生成内部业务订单并承接最终履约</span>
            </div>
            <span class="badge badge-indigo text-10px shrink-0 whitespace-nowrap">业务发起端</span>
          </div>

          <div class="grid grid-cols-1 md:grid-cols-2 gap-4 text-xs">
            <div>
              <label class="block text-slate-300 font-semibold mb-1">本站对外基础 URL (Site URL)</label>
              <div class="flex gap-1.5">
                <input v-model="form.siteUrl" type="text" placeholder="http://127.0.0.1:8100" class="input-dark w-full text-xs font-mono" />
                <button type="button" class="btn-secondary px-2.5 text-xs shrink-0" @click="copyText(form.siteUrl, '本站地址')" title="复制本站地址">
                  <span class="material-symbols-outlined text-14px">content_copy</span>
                </button>
              </div>
              <p class="text-11px text-slate-500 mt-1">系统计算默认回调与通知 URL 时的域名基准（如线上可填 https://api.yourdomain.com）。</p>
            </div>
            <div>
              <label class="block text-slate-300 font-semibold mb-1">支付成功跳转 URL (Return URL)</label>
              <div class="flex gap-1.5">
                <input v-model="form.payReturnUrl" type="text" :placeholder="defaultPayReturnUrl" class="input-dark w-full text-xs font-mono" />
                <button type="button" class="btn-secondary px-2.5 text-xs shrink-0" @click="form.payReturnUrl = defaultPayReturnUrl" title="恢复默认">
                  <span class="material-symbols-outlined text-14px">restart_alt</span>
                </button>
                <button type="button" class="btn-secondary px-2.5 text-xs shrink-0" @click="copyText(form.payReturnUrl || defaultPayReturnUrl, '跳转地址')" title="复制跳转地址">
                  <span class="material-symbols-outlined text-14px">content_copy</span>
                </button>
              </div>
              <p class="text-11px text-slate-500 mt-1">用户在收银台付款完成后，浏览器自动跳回的页面（默认 {site_url}/#/dashboard）。</p>
            </div>
          </div>
        </div>

        <!-- 流程指示 1: 切单流向 -->
        <div class="flex items-center justify-center gap-3 py-1">
          <div class="h-px bg-slate-800 flex-1"></div>
          <div class="flex items-center gap-1.5 px-3 py-1 rounded-full bg-slate-800/80 border border-slate-700/60 text-11px text-slate-400 font-mono shrink-0">
            <span class="material-symbols-outlined text-14px text-indigo-400">arrow_downward</span>
            <span>① 跨站中继切单下单 (POST relay_url) ── [HMAC-SHA256 签名]</span>
          </div>
          <div class="h-px bg-slate-800 flex-1"></div>
        </div>

        <!-- 节点 2: B 站合规收银中继 (极客工坊) -->
        <div class="p-4 rounded-xl bg-slate-900/40 border transition-all" :class="form.relayUrl ? 'border-emerald-500/40 bg-emerald-950/10' : 'border-slate-800 opacity-80'">
          <div class="flex items-center justify-between mb-3 pb-2 border-b border-slate-800/80 gap-3">
            <div class="flex items-center gap-2 min-w-0">
              <span class="px-2 py-0.5 rounded text-10px font-mono font-bold bg-emerald-500/20 text-emerald-400 border border-emerald-500/30 shrink-0">节点 2</span>
              <span class="font-bold text-white text-xs flex items-center gap-1 shrink-0">
                <span class="material-symbols-outlined text-16px text-emerald-400">handyman</span>
                <span>B 站合规收银中继 (极客工坊)</span>
              </span>
              <span class="text-11px text-slate-400 truncate hidden md:inline">合规在线效率工具箱伪装壳，提供独立高保真收银台与双向安全验签</span>
            </div>
            <span v-if="form.relayUrl" class="badge badge-emerald text-10px shrink-0 whitespace-nowrap">切单中继已启用</span>
            <span v-else class="badge text-slate-400 bg-slate-800 text-10px shrink-0 whitespace-nowrap">留空则直连易支付</span>
          </div>

          <div class="grid grid-cols-1 md:grid-cols-2 gap-4 text-xs">
            <div>
              <label class="block text-slate-300 font-semibold mb-1">中继下单接口 URL (Relay API)</label>
              <div class="flex gap-1.5">
                <input v-model="form.relayUrl" type="text" placeholder="http://127.0.0.1:8000/api/v1/relay/create" class="input-dark w-full text-xs font-mono" />
                <button type="button" class="btn-secondary px-2.5 text-xs shrink-0" @click="copyText(form.relayUrl, '中继下单地址')">
                  <span class="material-symbols-outlined text-14px">content_copy</span>
                </button>
              </div>
              <p class="text-11px text-slate-500 mt-1">B 站服务接收 A 站订单的后端接口。填入地址即开启切单中继。</p>
            </div>

            <div>
              <label class="block text-slate-300 font-semibold mb-1">中继收银台基准 URL (Relay Checkout Base)</label>
              <div class="flex gap-1.5">
                <input v-model="form.relayCheckoutBase" type="text" placeholder="http://crosslinkdev.online:8080" class="input-dark w-full text-xs font-mono" />
                <button type="button" class="btn-secondary px-2.5 text-xs shrink-0" @click="copyText(form.relayCheckoutBase, '收银台基准地址')">
                  <span class="material-symbols-outlined text-14px">content_copy</span>
                </button>
              </div>
              <div class="flex items-center gap-2 mt-1.5 flex-wrap">
                <span class="text-11px text-slate-500">快捷预设:</span>
                <button type="button" class="text-10px px-2 py-0.5 rounded bg-slate-800 hover:bg-slate-700 text-indigo-300 border border-slate-700 cursor-pointer" @click="form.relayCheckoutBase = 'http://crosslinkdev.online:8080'">
                  💻 本地 8080
                </button>
                <button type="button" class="text-10px px-2 py-0.5 rounded bg-slate-800 hover:bg-slate-700 text-emerald-300 border border-slate-700 cursor-pointer" @click="form.relayCheckoutBase = 'https://crosslinkdev.online'">
                  🚀 线上生产
                </button>
              </div>
              <p class="text-11px text-slate-500 mt-1">控制用户下单后跳往 B 站哪个协议、域名及端口的收银台。</p>
            </div>

            <div>
              <label class="block text-slate-300 font-semibold mb-1">跨站通信密钥 (Relay Secret)</label>
              <input v-model="form.relaySecret" type="text" placeholder="两站共享的通信鉴权密钥 (RELAY_SECRET)" class="input-dark w-full text-xs font-mono" />
              <p class="text-11px text-slate-500 mt-1">用于生成与验证 HMAC-SHA256 数字防伪签名，防止伪造支付回调。</p>
            </div>

            <div>
              <label class="block text-slate-300 font-semibold mb-1">中继异步通知 URL (Relay Notify URL)</label>
              <div class="flex gap-1.5">
                <input v-model="form.relayNotifyUrl" type="text" :placeholder="defaultRelayNotifyUrl" class="input-dark w-full text-xs font-mono" />
                <button type="button" class="btn-secondary px-2.5 text-xs shrink-0" @click="form.relayNotifyUrl = defaultRelayNotifyUrl" title="恢复默认">
                  <span class="material-symbols-outlined text-14px">restart_alt</span>
                </button>
                <button type="button" class="btn-secondary px-2.5 text-xs shrink-0" @click="copyText(form.relayNotifyUrl || defaultRelayNotifyUrl, '中继通知地址')">
                  <span class="material-symbols-outlined text-14px">content_copy</span>
                </button>
              </div>
              <p class="text-11px text-slate-500 mt-1">B 站收到易支付付款后通知 A 站的接口（默认 {site_url}/api/v1/pay/notify/relay）。</p>
            </div>
          </div>
        </div>

        <!-- 流程指示 2: 易支付收单流向 -->
        <div class="flex items-center justify-center gap-3 py-1">
          <div class="h-px bg-slate-800 flex-1"></div>
          <div class="flex items-center gap-1.5 px-3 py-1 rounded-full bg-slate-800/80 border border-slate-700/60 text-11px text-slate-400 font-mono shrink-0">
            <span class="material-symbols-outlined text-14px text-amber-400">arrow_downward</span>
            <span>② 携带授权域名发起支付 (submit.php) ── [MD5 签名]</span>
          </div>
          <div class="h-px bg-slate-800 flex-1"></div>
        </div>

        <!-- 节点 3: 易支付官方支付网关 -->
        <div class="p-4 rounded-xl bg-slate-900/40 border border-slate-800 hover:border-slate-700 transition-all">
          <div class="flex items-center justify-between mb-3 pb-2 border-b border-slate-800/80 gap-3">
            <div class="flex items-center gap-2 min-w-0">
              <span class="px-2 py-0.5 rounded text-10px font-mono font-bold bg-amber-500/20 text-amber-400 border border-amber-500/30 shrink-0">节点 3</span>
              <span class="font-bold text-white text-xs flex items-center gap-1 shrink-0">
                <span class="material-symbols-outlined text-16px text-amber-400">attach_money</span>
                <span>易支付官方支付网关 (EPay)</span>
              </span>
              <span class="text-11px text-slate-400 truncate hidden md:inline">接入彩虹易支付标准协议，支持直连与切单双模式聚合结算</span>
            </div>
            <span class="badge badge-amber text-10px shrink-0 whitespace-nowrap">资金结算通道</span>
          </div>

          <div class="grid grid-cols-1 lg:grid-cols-3 gap-4 text-xs">
            <div class="lg:col-span-2 flex flex-col gap-4">
              <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
                <div>
                  <label class="block text-slate-300 font-semibold mb-1">易支付网关地址 (Epay URL)</label>
                  <input v-model="form.epayUrl" type="text" placeholder="https://www.ezfp.cn" class="input-dark w-full text-xs font-mono" />
                  <p class="text-11px text-slate-500 mt-1">第三方易支付平台提交接口地址。</p>
                </div>
                <div>
                  <label class="block text-slate-300 font-semibold mb-1">商户 ID (PID)</label>
                  <input v-model="form.epayPid" type="text" placeholder="如 1000" class="input-dark w-full text-xs font-mono" />
                  <p class="text-11px text-slate-500 mt-1">在易支付商户后台分配的商户 PID。</p>
                </div>
                <div>
                  <label class="block text-slate-300 font-semibold mb-1">商户通信密钥 (Key)</label>
                  <input v-model="form.epayKey" type="password" placeholder="易支付商户通信密钥 Key" class="input-dark w-full text-xs font-mono" />
                  <p class="text-11px text-slate-500 mt-1">易支付商户通信密钥，用于 MD5 签名与验签。</p>
                </div>
                <div>
                  <label class="block text-slate-300 font-semibold mb-1">支付通道编码 (Epay Type)</label>
                  <input v-model="form.epayType" type="text" placeholder="alipay" class="input-dark w-full text-xs font-mono" />
                  <p class="text-11px text-slate-500 mt-1">默认调起支付宝 <code>alipay</code> 或微信支付 <code>wxpay</code>。</p>
                </div>
              </div>

              <div v-if="!form.relayUrl">
                <label class="block text-slate-300 font-semibold mb-1">直连异步通知 URL (Direct Notify URL)</label>
                <div class="flex gap-1.5">
                  <input v-model="form.epayNotifyUrl" type="text" :placeholder="defaultEpayNotifyUrl" class="input-dark w-full text-xs font-mono" />
                  <button type="button" class="btn-secondary px-2.5 text-xs shrink-0" @click="form.epayNotifyUrl = defaultEpayNotifyUrl" title="恢复默认">
                    <span class="material-symbols-outlined text-14px">restart_alt</span>
                  </button>
                  <button type="button" class="btn-secondary px-2.5 text-xs shrink-0" @click="copyText(form.epayNotifyUrl || defaultEpayNotifyUrl, '直连通知地址')">
                    <span class="material-symbols-outlined text-14px">content_copy</span>
                  </button>
                </div>
                <p class="text-11px text-slate-500 mt-1">直连易支付时生效，易支付将直接回调该地址（默认 {site_url}/api/v1/pay/notify/epay）。</p>
              </div>
            </div>

            <!-- 易支付官方后台填报指引小卡片 -->
            <div class="p-3.5 rounded-xl bg-slate-800/60 border border-slate-700/60 flex flex-col gap-2.5 text-xs">
              <div class="flex items-center gap-1.5 font-bold text-amber-400">
                <span class="material-symbols-outlined text-16px">info</span>
                <span>易支付商户后台填报小助手</span>
              </div>
              <div>
                <span class="text-slate-400 block mb-1">授权网站域名 (Domain):</span>
                <div class="flex items-center justify-between p-2 rounded bg-slate-900 border border-slate-700 font-mono text-11px text-emerald-400">
                  <span>{{ form.relayUrl ? 'crosslinkdev.online' : (form.siteUrl ? form.siteUrl.replace(/https?:\/\//, '').split('/')[0] : '本站公网域名') }}</span>
                  <button type="button" class="hover:text-white" @click="copyText(form.relayUrl ? 'crosslinkdev.online' : (form.siteUrl ? form.siteUrl.replace(/https?:\/\//, '').split('/')[0] : ''), '授权域名')">
                    <span class="material-symbols-outlined text-14px">content_copy</span>
                  </button>
                </div>
                <p class="text-10px text-slate-500 mt-1">切单中继模式下务必填 B 站授权域名，避免易支付风控拦截非授权来源。</p>
              </div>
              <div class="pt-2 border-t border-slate-700/60">
                <span class="text-slate-400 block mb-1">通知与跳转设置:</span>
                <p class="text-10px text-slate-400 leading-relaxed">
                  系统下单时会自动动态携带签名后的 <code>notify_url</code> 与 <code>return_url</code>，无需在易支付商户后台手动死写。
                </p>
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- 3. 底部发货闭环指示条 -->
      <div class="mt-6 p-3.5 rounded-xl bg-emerald-950/20 border border-emerald-500/30 flex items-start gap-3">
        <span class="w-8 h-8 rounded-lg bg-emerald-500/20 text-emerald-400 flex items-center justify-center shrink-0 border border-emerald-500/30">
          <span class="material-symbols-outlined text-18px">sync_alt</span>
        </span>
        <div class="text-xs">
          <span class="block font-bold text-white mb-0.5">逆向异步回调与自动发货闭环 (Fulfillment Loop)</span>
          <p class="text-slate-300 leading-relaxed">
            <template v-if="form.payProvider === 'fake'">
              用户点击购买 ➔ 本地模拟沙箱秒级创建并标记订单已支付 ➔ 自动激活并顺延套餐 ➔ 浏览器跳回 {{ form.payReturnUrl || defaultPayReturnUrl }}
            </template>
            <template v-else-if="form.relayUrl">
              用户付款成功 ➔ 易支付向 B站 POST /api/v1/epay/notify ➔ B站 MD5 验签成功 ➔ B站向 A站 POST {{ form.relayNotifyUrl || defaultRelayNotifyUrl }} ➔ A站校验 HMAC 签名无误自动开通/顺延套餐 ➔ 浏览器跳回 {{ form.payReturnUrl || defaultPayReturnUrl }}
            </template>
            <template v-else>
              用户付款成功 ➔ 易支付向 A站 POST/GET {{ form.epayNotifyUrl || defaultEpayNotifyUrl }} ➔ A站 MD5 验签无误自动开通/顺延套餐 ➔ 浏览器跳回 {{ form.payReturnUrl || defaultPayReturnUrl }}
            </template>
          </p>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import { paymentApi, type PaymentConfig } from '../../../api/client'

const loading = ref(false)
const saving = ref(false)
const feedbackMsg = ref('')
const feedbackSuccess = ref(true)

const form = reactive<PaymentConfig>({
  payProvider: 'epay',
  siteUrl: '',
  payReturnUrl: '',
  relayUrl: '',
  relayCheckoutBase: '',
  relaySecret: '',
  relayNotifyUrl: '',
  epayUrl: '',
  epayPid: '',
  epayKey: '',
  epayType: 'alipay',
  epayNotifyUrl: '',
})

const cleanSiteUrl = computed(() => {
  return (form.siteUrl || window.location.origin).replace(/\/+$/, '')
})

const defaultPayReturnUrl = computed(() => `${cleanSiteUrl.value}/#/dashboard`)
const defaultRelayNotifyUrl = computed(() => `${cleanSiteUrl.value}/api/v1/pay/notify/relay`)
const defaultEpayNotifyUrl = computed(() => `${cleanSiteUrl.value}/api/v1/pay/notify/epay`)

async function loadConfig() {
  loading.value = true
  feedbackMsg.value = ''
  try {
    const cfg = await paymentApi.getConfig()
    if (cfg) {
      Object.assign(form, cfg)
      if (!form.siteUrl) {
        form.siteUrl = window.location.origin
      }
    }
  } catch (err: any) {
    feedbackSuccess.value = false
    feedbackMsg.value = err.message || '获取支付配置失败'
  } finally {
    loading.value = false
  }
}

async function saveConfig() {
  saving.value = true
  feedbackMsg.value = ''
  try {
    await paymentApi.saveConfig(form)
    feedbackSuccess.value = true
    feedbackMsg.value = '支付跳转与收银切单配置已成功保存并立即生效！'
  } catch (err: any) {
    feedbackSuccess.value = false
    feedbackMsg.value = err.message || '保存支付配置失败'
  } finally {
    saving.value = false
  }
}

function copyText(text: string, label: string = '内容') {
  if (!text) {
    feedbackSuccess.value = false
    feedbackMsg.value = `${label}为空，无法复制`
    return
  }
  navigator.clipboard
    .writeText(text)
    .then(() => {
      feedbackSuccess.value = true
      feedbackMsg.value = `已成功复制 ${label} 到剪贴板`
    })
    .catch(() => {
      feedbackSuccess.value = false
      feedbackMsg.value = '复制失败，请手动选择复制'
    })
}

onMounted(() => {
  loadConfig()
})
</script>
