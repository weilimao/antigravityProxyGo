<template>
<div class="hidden flex-col gap-6 w-full" id="settings-panel-about">
<div class="glass-card rounded-xl p-8 flex flex-col items-center text-center gap-4 max-w-2xl mx-auto w-full">
<!-- App Logo -->
<img class="w-16 h-16 rounded-2xl shadow-lg object-cover shadow-primary/20" src="/src/assets/appicon.png"/>
<h2 class="text-xl font-bold text-on-surface dark:text-white">Antigravity Proxy</h2>
<!-- Pill Buttons Row -->
<div class="flex flex-wrap justify-center gap-2 mt-1">
<span class="px-2.5 py-0.5 bg-slate-100 dark:bg-white/5 border border-outline-variant/30 text-outline rounded-full text-[11px] font-semibold font-data-mono flex items-center" id="lblCurrentVersion">v1.0.0</span>
<button class="px-2.5 py-0.5 bg-primary/10 hover:bg-primary/20 text-primary border border-primary/20 rounded-full text-[11px] font-semibold flex items-center gap-1 transition-all cursor-pointer" id="btnCheckUpdate">
<span class="material-symbols-outlined text-[12px] animate-none" id="iconCheckUpdate">sync</span>
<span data-i18n="btnCheckUpdate" id="lblBtnCheckUpdate">检查更新</span>
</button>
<button class="px-2.5 py-0.5 bg-slate-100 hover:bg-slate-200 dark:bg-white/5 dark:hover:bg-white/10 text-on-surface dark:text-white border border-outline-variant/30 rounded-full text-[11px] font-medium flex items-center gap-1 transition-all cursor-pointer" id="btnAboutChangelog">
<span class="material-symbols-outlined text-[12px]">description</span>
<span data-i18n="btnReleaseNotes">更新记录</span>
</button>
</div>
<p class="text-[12px] text-outline max-w-md mt-1" data-i18n="aboutDesc">基于大模型的智能代理与多账号调度流量管理解决方案</p>
<!-- 四宫格卡片布局 -->
<div class="grid grid-cols-1 sm:grid-cols-2 gap-4 w-full mt-4 border-t border-outline-variant/20 pt-6">
<!-- Card 1: 主作者 -->
<div class="glass-card hover:border-primary/30 hover:bg-primary/5 transition-all duration-300 rounded-xl p-4 flex flex-col items-center justify-center text-center gap-1.5 border border-outline-variant/20">
<span class="material-symbols-outlined text-primary/80 text-[20px]">person</span>
<span class="text-[12px] font-bold text-on-surface dark:text-white" data-i18n="aboutAuthorLabel">主作者</span>
<span class="text-[11px] text-outline font-data-mono">weilimao</span>
</div>
<!-- Card 2: 开源仓库 -->
<button class="glass-card hover:border-primary/30 hover:bg-primary/5 transition-all duration-300 rounded-xl p-4 flex flex-col items-center justify-center text-center gap-1.5 border border-outline-variant/20 cursor-pointer" id="btnAboutRepo">
<svg class="w-5 h-5 text-primary/80 fill-current" viewBox="0 0 24 24">
<path d="M12 0c-6.626 0-12 5.373-12 12 0 5.302 3.438 9.8 8.207 11.387.599.111.793-.261.793-.577v-2.234c-3.338.726-4.033-1.416-4.033-1.416-.546-1.387-1.333-1.756-1.333-1.756-1.089-.745.083-.729.083-.729 1.205.084 1.839 1.237 1.839 1.237 1.07 1.834 2.807 1.304 3.492.997.107-.775.418-1.305.762-1.604-2.665-.305-5.467-1.334-5.467-5.931 0-1.311.469-2.381 1.236-3.221-.124-.303-.535-1.524.117-3.176 0 0 1.008-.322 3.301 1.23.957-.266 1.983-.399 3.003-.404 1.02.005 2.047.138 3.006.404 2.291-1.552 3.297-1.23 3.297-1.23.653 1.653.242 2.874.118 3.176.77.84 1.235 1.911 1.235 3.221 0 4.609-2.807 5.624-5.479 5.921.43.372.823 1.102.823 2.222v3.293c0 .319.192.694.801.576 4.765-1.589 8.199-6.086 8.199-11.386 0-6.627-5.373-12-12-12z"></path>
</svg>
<span class="text-[12px] font-bold text-on-surface dark:text-white" data-i18n="aboutRepoLabel">开源仓库</span>
<span class="text-[11px] text-outline font-data-mono">antigravityProxyGo</span>
</button>
<!-- Card 3: 赞助支持 -->
<div class="glass-card hover:border-primary/30 hover:bg-primary/5 transition-all duration-300 rounded-xl p-4 flex flex-col items-center justify-center text-center gap-1.5 border border-outline-variant/20">
<span class="material-symbols-outlined text-primary/80 text-[20px]">favorite</span>
<span class="text-[12px] font-bold text-on-surface dark:text-white" data-i18n="aboutSponsorLabel">赞助支持</span>
<span class="text-[11px] text-outline" data-i18n="aboutSponsorDesc">支持项目持续开发</span>
</div>
<!-- Card 4: 意见反馈 -->
<button class="glass-card hover:border-primary/30 hover:bg-primary/5 transition-all duration-300 rounded-xl p-4 flex flex-col items-center justify-center text-center gap-1.5 border border-outline-variant/20 cursor-pointer" id="btnAboutFeedback">
<span class="material-symbols-outlined text-primary/80 text-[20px]">sms</span>
<span class="text-[12px] font-bold text-on-surface dark:text-white" data-i18n="aboutFeedbackLabel">意见反馈</span>
<span class="text-[11px] text-outline" data-i18n="aboutFeedbackDesc">报告问题或提交建议</span>
</button>
</div>
<!-- 版权声明 -->
<div class="text-[11px] text-slate-400/80 dark:text-slate-500/80 mt-6 font-medium select-none text-center">
                    Copyright © 2026 weilimao. All rights reserved.
                </div>
</div>
</div>
</template>

<script setup lang="ts">
// AboutPanel: 从 Settings.vue 提取的纯展示面板。
// 保留所有 id / data-i18n / onclick / class 属性，
// 使 settingsController / relayController 的 getElementById 与 classList 操作零改动。
</script>
