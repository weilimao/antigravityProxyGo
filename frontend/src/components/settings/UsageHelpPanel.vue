<template>
  <div class="flex flex-col gap-6 w-full" id="settings-panel-help" style="display: none !important;">
    <!-- 卡片 1：IDE 更新后拦截失效解决指南（高亮突显） -->
    <div class="glass-card rounded-xl p-6 flex flex-col gap-4 border-l-4 border-l-primary">
      <div class="flex items-center justify-between">
        <h2 class="text-[15px] font-bold text-on-surface dark:text-white flex items-center gap-2">
          <span class="material-symbols-outlined text-primary text-[22px]">published_with_changes</span>
          <span data-i18n="help_ide_update_title">Antigravity 更新后请求未拦截解决指南</span>
        </h2>
        <span class="px-2.5 py-0.5 text-[11px] font-bold bg-primary/10 text-primary rounded-full" data-i18n="help_ide_update_tag">常见排查</span>
      </div>
      <p class="text-xs text-outline leading-relaxed" data-i18n="help_ide_update_desc">
        当 Antigravity IDE 升级到新版本（如 Version 2.4.2）时，官方安装程序会全量覆盖重置核心组件 app.asar 与配置文件。这会导致之前注入的本地代理拦截代码被冲掉，从而恢复直连。
      </p>

      <div class="flex flex-col gap-3 mt-1 bg-slate-50 dark:bg-white/5 p-4 rounded-lg border border-outline-variant/30">
        <div class="text-[13px] font-bold text-on-surface dark:text-white flex items-center gap-1.5">
          <span class="material-symbols-outlined text-[18px] text-emerald-500">checklist</span>
          <span data-i18n="help_three_step_title">恢复代理拦截的“三步操作法”：</span>
        </div>
        
        <div class="grid grid-cols-1 md:grid-cols-3 gap-3 mt-1">
          <!-- 第一步 -->
          <div class="flex flex-col gap-1.5 p-3 bg-white dark:bg-[#1a1f30] rounded-md border border-outline-variant/20 shadow-sm">
            <div class="flex items-center gap-2">
              <span class="w-5 h-5 rounded-full bg-primary text-white text-[11px] font-bold flex items-center justify-center">1</span>
              <span class="text-[12px] font-bold text-on-surface dark:text-white" data-i18n="help_step1_title">重新热插拔拦截开关</span>
            </div>
            <p class="text-[11px] text-outline leading-normal pl-7" data-i18n="help_step1_desc">
              在客户端右上角，将紫色的【开启】开关切到【关闭】，然后再拨回【开启】（或重启本代理软件）。系统会自动将补丁重新挂载到新版 IDE。
            </p>
          </div>

          <!-- 第二步 -->
          <div class="flex flex-col gap-1.5 p-3 bg-white dark:bg-[#1a1f30] rounded-md border border-outline-variant/20 shadow-sm">
            <div class="flex items-center gap-2">
              <span class="w-5 h-5 rounded-full bg-primary text-white text-[11px] font-bold flex items-center justify-center">2</span>
              <span class="text-[12px] font-bold text-on-surface dark:text-white" data-i18n="help_step2_title">完全重启 IDE</span>
            </div>
            <p class="text-[11px] text-outline leading-normal pl-7" data-i18n="help_step2_desc">
              关闭并重新打开 Antigravity IDE 客户端，使其语言服务器进程重新加载刚注入的 127.0.0.1:18443 代理环境变量。
            </p>
          </div>

          <!-- 第三步 -->
          <div class="flex flex-col gap-1.5 p-3 bg-white dark:bg-[#1a1f30] rounded-md border border-outline-variant/20 shadow-sm">
            <div class="flex items-center gap-2">
              <span class="w-5 h-5 rounded-full bg-emerald-500 text-white text-[11px] font-bold flex items-center justify-center">3</span>
              <span class="text-[12px] font-bold text-on-surface dark:text-white" data-i18n="help_step3_title">抓包验证</span>
            </div>
            <p class="text-[11px] text-outline leading-normal pl-7" data-i18n="help_step3_desc">
              在 IDE 中发送一条测试对话，观察代理客户端主界面的“控制台日志”或“抓包分析”中是否重新开始实时打印拦截日志。
            </p>
          </div>
        </div>
      </div>
    </div>

    <!-- 卡片 2：根证书（CA 证书）与 HTTPS 解密作用 -->
    <div class="glass-card rounded-xl p-6 flex flex-col gap-4">
      <h2 class="text-[15px] font-bold text-on-surface dark:text-white flex items-center gap-2">
        <span class="material-symbols-outlined text-primary text-[20px]">verified_user</span>
        <span data-i18n="help_cert_title">根证书（CA 证书）与 HTTPS 解密作用</span>
      </h2>
      <p class="text-xs text-outline leading-relaxed" data-i18n="help_cert_desc">
        本代理的核心解密引擎（MITM）需要将本机发往谷歌 API（如 generativelanguage.googleapis.com）的加密 HTTPS 流量解密并转换为高可用负载均衡请求。
      </p>

      <div class="grid grid-cols-1 md:grid-cols-2 gap-4 mt-1">
        <div class="flex flex-col gap-2 p-3.5 bg-slate-50 dark:bg-white/5 rounded-lg border border-outline-variant/20">
          <div class="flex items-center gap-2 text-[12px] font-bold text-emerald-600 dark:text-emerald-400">
            <span class="material-symbols-outlined text-[18px]">add_moderator</span>
            <span data-i18n="help_cert_install_title">【安装证书 / 已信任】的作用</span>
          </div>
          <p class="text-[11px] text-outline leading-relaxed" data-i18n="help_cert_install_desc">
            点击顶部菜单的“已信任/安装证书”，会在 Windows 受信任的根证书颁发机构中植入唯一的本地自签名 CA 证书。有了此证书，IDE 在通过代理中转时才不会报 SSL/TLS 证书无效错误。
          </p>
        </div>

        <div class="flex flex-col gap-2 p-3.5 bg-slate-50 dark:bg-white/5 rounded-lg border border-outline-variant/20">
          <div class="flex items-center gap-2 text-[12px] font-bold text-rose-500">
            <span class="material-symbols-outlined text-[18px]">remove_moderator</span>
            <span data-i18n="help_cert_uninstall_title">【卸载证书】的作用</span>
          </div>
          <p class="text-[11px] text-outline leading-relaxed" data-i18n="help_cert_uninstall_desc">
            随时从操作系统中彻底清除本代理植入的根证书，恢复系统原生的 SSL 信任状态，安全无残留。
          </p>
        </div>
      </div>
    </div>

    <!-- 卡片 3：系统代理与开关拦截原理 -->
    <div class="glass-card rounded-xl p-6 flex flex-col gap-4">
      <h2 class="text-[15px] font-bold text-on-surface dark:text-white flex items-center gap-2">
        <span class="material-symbols-outlined text-primary text-[20px]">toggle_on</span>
        <span data-i18n="help_switch_title">拦截开关（开启 / 关闭）的作用机制</span>
      </h2>
      <p class="text-xs text-outline leading-relaxed" data-i18n="help_switch_desc">
        右上角的紫色开关控制着整个桌面代理的本地网络拦截生命周期：
      </p>

      <div class="flex flex-col gap-3 mt-1">
        <div class="flex items-start gap-3 p-3 bg-slate-50 dark:bg-white/5 rounded-lg border border-outline-variant/20">
          <span class="px-2 py-1 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 font-bold text-[11px] rounded whitespace-nowrap" data-i18n="help_switch_on_tag">开启拦截</span>
          <div class="flex flex-col gap-1 text-[11px] text-outline">
            <span class="font-bold text-on-surface dark:text-white" data-i18n="help_switch_on_title">网络代理绑定 + 代码自动打补丁</span>
            <span data-i18n="help_switch_on_desc">系统自动监听 127.0.0.1:18443 拦截端口，同时在后台自动更新系统的网络代理配置，并将 HTTP_PROXY 环境变量重写挂载到 Antigravity IDE 和 Agent 中。</span>
          </div>
        </div>

        <div class="flex flex-col sm:flex-row items-start gap-3 p-3 bg-slate-50 dark:bg-white/5 rounded-lg border border-outline-variant/20">
          <span class="px-2 py-1 bg-slate-400/10 text-slate-500 font-bold text-[11px] rounded whitespace-nowrap" data-i18n="help_switch_off_tag">关闭拦截</span>
          <div class="flex flex-col gap-1 text-[11px] text-outline">
            <span class="font-bold text-on-surface dark:text-white" data-i18n="help_switch_off_title">停用代理 + 自动清除环境污染</span>
            <span data-i18n="help_switch_off_desc">立刻关闭本地拦截端口，还原 Windows 系统的网络代理设置，并恢复 app.asar 原始文件，保证绝不污染您的本地网络。</span>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
// UsageHelpPanel Component with i18n support
</script>
