<template>
  <!-- 顶部状态与概览卡片 -->
  <div class="glass-card rounded-xl p-6 flex flex-col gap-4">
    <div class="flex flex-wrap items-center justify-between gap-4">
      <div class="flex items-center gap-3">
        <div class="w-10 h-10 rounded-lg bg-primary/10 dark:bg-primary/20 flex items-center justify-center text-primary dark:text-primary-fixed-dim">
          <span class="material-symbols-outlined text-[24px]">wallpaper</span>
        </div>
        <div>
          <h2 class="text-[16px] font-bold text-on-surface dark:text-white flex items-center gap-2">
            Antigravity 桌面端壁纸与外观调谐中心
            <span v-if="status.os" class="text-[11px] px-2 py-0.5 rounded font-mono font-medium uppercase border"
              :class="status.os === 'darwin' ? 'bg-indigo-50 dark:bg-indigo-950/40 text-indigo-600 dark:text-indigo-400 border-indigo-200 dark:border-indigo-800/40' : 'bg-blue-50 dark:bg-blue-950/40 text-blue-600 dark:text-blue-400 border-blue-200 dark:border-blue-800/40'">
              {{ status.os === 'darwin' ? 'macOS (Darwin)' : status.os }}
            </span>
          </h2>
          <p class="text-xs text-outline leading-relaxed mt-0.5">
            可视化配置并一键注入自定义壁纸、全屏通透、毛玻璃及侧边栏/正文字体配色到 Google Antigravity 桌面端，无缝适配 macOS 与 Windows。
          </p>
        </div>
      </div>

      <!-- 状态指示徽章 -->
      <div class="flex items-center gap-2 flex-wrap">
        <div v-if="status.installed" class="flex items-center gap-1.5 text-[12px] font-medium px-2.5 py-1 rounded-full bg-emerald-50 dark:bg-emerald-950/40 text-emerald-600 dark:text-emerald-400 border border-emerald-200 dark:border-emerald-800/40">
          <span class="material-symbols-outlined text-[15px]">check_circle</span>
          <span>已识别安装路径</span>
        </div>
        <div v-else class="flex items-center gap-1.5 text-[12px] font-medium px-2.5 py-1 rounded-full bg-amber-50 dark:bg-amber-950/40 text-amber-600 dark:text-amber-400 border border-amber-200 dark:border-amber-800/40">
          <span class="material-symbols-outlined text-[15px]">warning</span>
          <span>未检测到默认安装路径</span>
        </div>

        <div v-if="status.patched" class="flex items-center gap-1.5 text-[12px] font-medium px-2.5 py-1 rounded-full bg-primary/10 text-primary dark:text-primary-fixed-dim border border-primary/20">
          <span class="material-symbols-outlined text-[15px]">brush</span>
          <span>已注入壁纸补丁</span>
        </div>
        <div v-else class="flex items-center gap-1.5 text-[12px] font-medium px-2.5 py-1 rounded-full bg-slate-100 dark:bg-white/5 text-outline border border-outline-variant/30">
          <span class="material-symbols-outlined text-[15px]">radio_button_unchecked</span>
          <span>官方原版状态</span>
        </div>

        <div v-if="status.isRunning" class="flex items-center gap-1.5 text-[12px] font-medium px-2.5 py-1 rounded-full bg-sky-50 dark:bg-sky-950/40 text-sky-600 dark:text-sky-400 border border-sky-200 dark:border-sky-800/40">
          <span class="w-2 h-2 rounded-full bg-sky-500 animate-pulse"></span>
          <span>进程运行中</span>
        </div>
      </div>
    </div>

    <!-- 路径检测与自定义路径输入 -->
    <div class="flex flex-col gap-2 pt-3 border-t border-outline-variant/20">
      <label class="text-[12px] font-bold text-outline flex items-center justify-between">
        <span>Antigravity 应用程序路径 (app.asar / .app / 安装目录)</span>
        <button @click="refreshStatus" class="text-primary hover:underline text-[11px] font-normal flex items-center gap-1 cursor-pointer">
          <span class="material-symbols-outlined text-[14px]">refresh</span>
          刷新探测
        </button>
      </label>
      <div class="flex gap-2">
        <input
          v-model="customAppPath"
          type="text"
          :placeholder="status.os === 'darwin' ? '/Applications/Antigravity.app' : 'C:\\Users\\...\\AppData\\Local\\Programs\\antigravity'"
          class="flex-grow px-3 py-2 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:outline-none text-on-surface dark:text-white font-mono"
        />
        <button
          @click="refreshStatus"
          class="px-3 py-2 bg-slate-100 dark:bg-white/10 hover:bg-slate-200 dark:hover:bg-white/15 text-on-surface dark:text-white rounded-md text-[12px] font-medium transition-colors cursor-pointer"
        >
          校验路径
        </button>
      </div>
      <div v-if="status.asarPath" class="text-[11px] text-outline font-mono truncate">
        核心 ASAR: {{ status.asarPath }}
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useWallpaper } from '../../../composables/useWallpaper';

const { status, customAppPath, refreshStatus } = useWallpaper();
</script>
