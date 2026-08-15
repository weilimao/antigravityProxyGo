<template>
<div class="hidden flex-col gap-6 w-full" id="settings-panel-nvidia">
<!-- NVIDIA 流式思考回译设置卡片 -->
<div class="glass-card rounded-xl p-6 flex flex-col gap-4">
<h2 class="text-[15px] font-bold text-on-surface dark:text-white flex items-center gap-2">
<span class="material-symbols-outlined text-primary text-[20px]">psychology</span>
<span data-i18n="nvidiaReasoningAsTextTitle">NVIDIA 流式思考回译</span>
</h2>
<p class="text-xs text-outline leading-relaxed" data-i18n="nvidiaReasoningAsTextTip">
仅作用于 NVIDIA 上游链路的流式回译：开启后，将上游思维链 (Reasoning Content) 直接作为文本流逐字打屏输出，避免 CLI 客户端界面默认收起折叠。
</p>
<div class="flex items-center justify-between border-t border-outline-variant/20 pt-4 mt-2">
<div class="flex flex-col gap-0.5">
<span class="text-[13px] font-bold text-on-surface dark:text-white" data-i18n="reasoningAsTextLabel">思考过程直吐正文 (打字机模式)</span>
<span class="text-[11px] text-outline text-wrap max-w-[80%]" data-i18n="reasoningAsTextDesc">开启后，将上游思维链 (Reasoning Content) 直接作为文本流逐字打屏输出，避免 CLI 客户端界面默认收起折叠。</span>
</div>
<!-- Toggle Switch -->
<label class="relative inline-flex items-center cursor-pointer">
<input class="sr-only peer" id="chkReasoningAsText" type="checkbox"/>
<div class="w-11 h-6 bg-slate-200 dark:bg-white/10 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full rtl:peer-checked:after:-translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:start-[2px] after:bg-white after:border-gray-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all dark:border-gray-600 peer-checked:bg-primary"></div>
</label>
</div>
</div>

<!-- Debugger 调试模式与日志落盘卡片 (NVIDIA 入站专属落盘) -->
<div class="glass-card rounded-xl p-6 flex flex-col gap-4">
<h2 class="text-[15px] font-bold text-on-surface dark:text-white flex items-center gap-2">
<span class="material-symbols-outlined text-primary text-[20px]">bug_report</span>
<span data-i18n="debuggerModeCardTitle">Debugger 调试模式与全量请求日志落盘</span>
</h2>
<p class="text-xs text-outline leading-relaxed" data-i18n="debuggerModeCardTip">
开启后，系统将把每一笔中继请求的 Headers、Body、上游地址、状态码以及原始 SSE 流逐帧毫秒级实时记录存储到指定日志文件中，便于精准诊断断流与异常排查。
</p>
<div class="flex items-center justify-between border-t border-outline-variant/20 pt-4 mt-2">
<div class="flex flex-col gap-0.5">
<span class="text-[13px] font-bold text-on-surface dark:text-white" data-i18n="enableDebuggerModeLabel">启用 Debugger 调试模式</span>
<span class="text-[11px] text-outline text-wrap max-w-[80%]" data-i18n="enableDebuggerModeDesc">开启后实时将中继与上游交互的全量原始字节与 SSE 帧落盘到指定的调试日志文件夹中（默认关闭）。</span>
</div>
<!-- Toggle Switch -->
<label class="relative inline-flex items-center cursor-pointer">
<input class="sr-only peer" id="chkEnableDebuggerMode" type="checkbox"/>
<div class="w-11 h-6 bg-slate-200 dark:bg-white/10 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full rtl:peer-checked:after:-translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:start-[2px] after:bg-white after:border-gray-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all dark:border-gray-600 peer-checked:bg-primary"></div>
</label>
</div>
<div class="flex flex-col gap-2 mt-2 pt-3 border-t border-outline-variant/10">
<label class="text-[12px] font-bold text-outline" data-i18n="debuggerLogPathLabel">调试日志保存目录</label>
<div class="flex gap-2">
<input class="flex-grow px-3 py-2 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:outline-none text-on-surface dark:text-white" id="txtDebuggerLogPath" type="text" placeholder="logs/debugger" data-i18n-placeholder="debuggerLogPathPlaceholder">
<button class="px-4 py-2 bg-primary text-white hover:bg-primary/90 rounded-md text-[13px] font-bold transition-colors shadow-sm flex items-center gap-1.5 cursor-pointer" id="btnBrowseDebuggerDir">
<span class="material-symbols-outlined text-[16px]">folder</span>
<span data-i18n="btnBrowseDebuggerDir">更改目录</span>
</button>
</div>
</div>
</div>

<!-- NVIDIA 断流兜底出站代理卡片 (仅 NVIDIA Anthropic 流式重试耗尽后切此代理再试 1 轮) -->
<div class="glass-card rounded-xl p-6 flex flex-col gap-4">
<h2 class="text-[15px] font-bold text-on-surface dark:text-white flex items-center gap-2">
<span class="material-symbols-outlined text-primary text-[20px]">dns</span>
<span data-i18n="nvidiaFallbackProxyTitle">NVIDIA 断流兜底出站代理</span>
</h2>
<p class="text-xs text-outline leading-relaxed" data-i18n="nvidiaFallbackProxyTip">
仅 NVIDIA 上游链路：直连 5s×5 蓄流重试全部耗尽后，换此代理再试 1 轮（单次请求级，不记忆，不换号）。系统代理与虚拟网卡优先，仅当它们都不通才走此兜底。配置独立于「参数配置」中的专属 SOCKS5。
</p>
<div class="flex flex-col gap-3 border-t border-outline-variant/20 pt-4 mt-2">
<div class="flex items-center justify-between">
<div class="flex flex-col gap-0.5">
<label class="text-[13px] font-bold text-on-surface dark:text-white" data-i18n="fallbackProxyEnabledLabel">启用 NVIDIA 断流兜底出站代理</label>
<span class="text-[11px] text-outline text-wrap max-w-[80%]" data-i18n="fallbackProxyEnabledDesc">仅 NVIDIA 上游链路：直连 5s×5 蓄流重试全部耗尽后，换此代理再试 1 轮（单次请求级，不记忆，不换号）。系统代理与虚拟网卡优先，仅当它们都不通才走此兜底。配置独立于上方专属 SOCKS5。</span>
</div>
<label class="relative inline-flex items-center cursor-pointer">
<input class="sr-only peer" id="chkFallbackProxyEnabled" type="checkbox"/>
<div class="w-11 h-6 bg-slate-200 dark:bg-white/10 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full rtl:peer-checked:after:-translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:start-[2px] after:bg-white after:border-gray-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all dark:border-gray-600 peer-checked:bg-primary"></div>
</label>
</div>
<div class="flex flex-col gap-3 mt-1" id="divFallbackProxyAddress">
<div class="flex flex-col gap-1.5">
<label class="text-[12px] font-bold text-outline" data-i18n="fallbackProxyAddressLabel">兜底代理地址 (协议必须显式配置，如 http:// 或 socks5://)</label>
<input class="px-3 py-2 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:outline-none text-on-surface dark:text-white" id="txtFallbackProxyAddress" placeholder="例如：http://127.0.0.1:8080 或 socks5://127.0.0.1:1080" data-i18n-placeholder="fallbackProxyAddressPlaceholder" type="text"/>
</div>
<div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
<div class="flex flex-col gap-1.5">
<label class="text-[12px] font-bold text-outline" data-i18n="fallbackProxyUsernameLabel">兜底代理用户名 (可选)</label>
<input class="px-3 py-2 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:outline-none text-on-surface dark:text-white" id="txtFallbackProxyUsername" placeholder="无" data-i18n-placeholder="optionalPlaceholder" type="text"/>
</div>
<div class="flex flex-col gap-1.5">
<label class="text-[12px] font-bold text-outline" data-i18n="fallbackProxyPasswordLabel">兜底代理密码 (可选)</label>
<PasswordInput inputId="txtFallbackProxyPassword" placeholder="无" dataI18nPlaceholder="optionalPlaceholder" inputClass="w-full px-3 py-2 pr-9 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:outline-none text-on-surface dark:text-white"/>
</div>
</div>
</div>
</div>
</div>

<!-- NVIDIA Cloudflare 代理出口 Worker 卡片 (通过 Anycast 边缘节点打散出口，避免单 IP 触发 429) -->
<div class="glass-card rounded-xl p-6 flex flex-col gap-4">
<h2 class="text-[15px] font-bold text-on-surface dark:text-white flex items-center gap-2">
<span class="material-symbols-outlined text-amber-500 text-[20px]">cloud</span>
<span data-i18n="nvidiaWorkerProxyTitle">NVIDIA Cloudflare 代理出口 Worker</span>
</h2>
<p class="text-xs text-outline leading-relaxed" data-i18n="nvidiaWorkerProxyEnableDesc">
将 NVIDIA 请求转发至 Cloudflare Worker，通过 Anycast 边缘 IP 轮换打散出口，避免单 IP 突发流量触发 429 拦截。
</p>
<div class="flex flex-col gap-3 border-t border-outline-variant/20 pt-4 mt-2">
<div class="flex items-center justify-between">
<div class="flex flex-col gap-0.5">
<label class="text-[13px] font-bold text-on-surface dark:text-white" data-i18n="nvidiaWorkerProxyEnableLabel">启用 Cloudflare 边缘代理出口</label>
<span class="text-[11px] text-outline text-wrap max-w-[80%]" data-i18n="nvidiaWorkerProxyEnableDesc">将 NVIDIA 请求转发至 Cloudflare Worker，通过 Anycast 边缘 IP 轮换打散出口，避免触发单 IP 429 限流</span>
</div>
<label class="relative inline-flex items-center cursor-pointer">
<input class="sr-only peer" id="chkNvidiaWorkerProxyEnabled" type="checkbox"/>
<div class="w-11 h-6 bg-slate-200 dark:bg-white/10 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full rtl:peer-checked:after:-translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:start-[2px] after:bg-white after:border-gray-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all dark:border-gray-600 peer-checked:bg-primary"></div>
</label>
</div>
<div class="flex flex-col gap-2 mt-1" id="divNvidiaWorkerProxyUrl" style="display: none;">
<label class="text-[12px] font-bold text-outline" data-i18n="nvidiaWorkerProxyUrlLabel">Cloudflare Worker 地址 (URL)</label>
<input class="px-3 py-2 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:outline-none text-on-surface dark:text-white font-mono" id="txtNvidiaWorkerProxyUrl" placeholder="https://your-worker.workers.dev" data-i18n-placeholder="nvidiaWorkerProxyUrlPlaceholder" type="text"/>
<span class="text-[11px] text-outline" data-i18n="nvidiaWorkerProxyUrlTip">填写部署好的 Cloudflare Worker 地址。若账号配置了专属出口 IP，将通过 X-Egress-IP 请求头一并透传给 Worker。</span>
</div>

<!-- 部署教程与 Worker 脚本展示 -->
<div class="border-t border-outline-variant/10 pt-4 mt-2 flex flex-col gap-3">
<div class="flex items-center justify-between">
<div class="flex items-center gap-1.5">
<span class="material-symbols-outlined text-[18px] text-primary">menu_book</span>
<span class="text-[13px] font-bold text-on-surface dark:text-white">Cloudflare Worker 部署指南与脚本</span>
</div>
</div>

<p class="text-[12px] text-outline leading-relaxed">
当您使用多个 NVIDIA 账号高频请求时，官方网关可能会因<b>单 IP 短期内吞吐过大</b>触发 429 拦截。通过部署 Cloudflare Worker 作为代理出口，可利用 Cloudflare 全球 Anycast 边缘出站 IP 自动打散流量，并支持账号级独立伪装住宅 IP。
</p>

<!-- Worker 脚本代码编辑器 (带语法着色、行号、原位编辑与一键复制) -->
<div class="flex flex-col gap-1.5">
<div class="text-[11px] font-bold text-on-surface dark:text-white flex items-center gap-1.5">
  <span class="material-symbols-outlined text-[16px] text-amber-500">code</span>
  <span>Worker 脚本代码（支持流式 SSE、防流锁定重试与 X-Egress-IP 伪装，可直接编辑与一键复制）：</span>
</div>
<CodeEditor
  v-model="workerScriptCode"
  :defaultCode="defaultWorkerScriptCode"
  fileName="cloudflare-worker.js"
  language="JavaScript"
/>
</div>

<div class="text-[12px] text-outline leading-relaxed flex flex-col gap-1">
<div class="font-bold text-on-surface dark:text-white">使用步骤：</div>
<div>1. 登录 <a href="https://dash.cloudflare.com" target="_blank" class="text-primary hover:underline">Cloudflare Dashboard</a> &rarr; <b>Workers &amp; Pages</b> &rarr; 点击 <b>Create application</b> &rarr; 创建 Worker。</div>
<div>2. 点击 <b>Quick edit</b>，粘贴上方一键复制的代码并点击 <b>Save and deploy</b>。</div>
<div>3. 复制生成的 Worker 地址（如 <code>https://your-worker.workers.dev</code>），粘贴到上方「Cloudflare Worker 地址」输入框并开启。</div>
<div>4. （可选）在「账号池 &rarr; NVIDIA」点击账号编辑，即可查看到已为各账号分配的专属住宅伪装 IP。</div>
</div>
</div>

</div>
</div>
</div>
</template>

<script setup lang="ts">
import { ref } from 'vue';
import CodeEditor from '../../common/CodeEditor.vue';
import PasswordInput from '../../modals/PasswordInput.vue';

const defaultWorkerScriptCode = `export default {
  async fetch(request, env, ctx) {
    const url = new URL(request.url);

    // 1. 健康检查与 CORS 预检
    if (url.pathname === "/health") {
      return new Response(JSON.stringify({ status: "ok", edge: "cloudflare-worker" }), {
        headers: { "content-type": "application/json" }
      });
    }
    if (request.method === "OPTIONS") {
      return new Response(null, {
        status: 204,
        headers: {
          "Access-Control-Allow-Origin": "*",
          "Access-Control-Allow-Methods": "GET, POST, PUT, DELETE, OPTIONS",
          "Access-Control-Allow-Headers": "*"
        }
      });
    }

    // 2. 构造目标 URL (NVIDIA 官方 API)
    const targetUrl = \`https://integrate.api.nvidia.com\${url.pathname}\${url.search}\`;

    // 3. 构造出站 Headers（修正 Host 并注入专属伪装 IP，剔除逐跳头）
    const newHeaders = new Headers(request.headers);
    newHeaders.set("host", "integrate.api.nvidia.com");

    const egressIP = request.headers.get("x-egress-ip");
    if (egressIP && egressIP.trim() !== "") {
      const cleanIP = egressIP.trim();
      newHeaders.set("cf-connecting-ip", cleanIP);
      newHeaders.set("x-real-ip", cleanIP);
      newHeaders.set("x-forwarded-for", cleanIP);
    } else {
      newHeaders.delete("cf-connecting-ip");
      newHeaders.delete("x-real-ip");
      newHeaders.delete("x-forwarded-for");
    }
    newHeaders.delete("x-egress-ip");

    // 4. 优化请求体转发与 429 智能重试
    let bodyBytes = null;
    if (request.body && request.method !== "GET" && request.method !== "HEAD") {
      bodyBytes = await request.arrayBuffer();
    }

    const maxRetries = 2;
    let response;

    for (let i = 0; i <= maxRetries; i++) {
      try {
        response = await fetch(targetUrl, {
          method: request.method,
          headers: newHeaders,
          body: bodyBytes,
          redirect: "follow",
          cf: {
            cacheEverything: false,
            cacheTtl: 0
          }
        });

        // 遇到正常响应 (2xx/3xx/4xx除429外) 直接跳出重试循环，进入极速返回通道
        if (![429, 500, 502, 503, 504].includes(response.status)) {
          break;
        }

        // 仅在 429/5xx 且未耗尽重试次数时进行毫秒级退避重试 (100ms * (i + 1))
        if (i < maxRetries) {
          await new Promise(r => setTimeout(r, 100 * (i + 1)));
        }
      } catch (err) {
        if (i === maxRetries) throw err;
        await new Promise(r => setTimeout(r, 100 * (i + 1)));
      }
    }

    // 5. 构造下游响应（透传 SSE 流式传输，禁用 Cloudflare 边缘缓存与压缩缓冲）
    const respHeaders = new Headers(response.headers);
    respHeaders.set("access-control-allow-origin", "*");
    respHeaders.set("cache-control", "no-cache, no-transform");
    respHeaders.set("x-accel-buffering", "no");
    respHeaders.delete("content-length");

    return new Response(response.body, {
      status: response.status,
      headers: respHeaders
    });
  }
};`;

const workerScriptCode = ref(defaultWorkerScriptCode);
</script>
