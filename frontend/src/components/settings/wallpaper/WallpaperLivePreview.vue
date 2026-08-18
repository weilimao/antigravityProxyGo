<template>
  <!-- 右侧：全屏通透实时模拟视窗 (5列，视口吸顶锁定固定，所见即所得) -->
  <div class="lg:col-span-5 flex flex-col gap-3 self-start lg:sticky lg:top-0 z-10 w-full">
    <div class="flex items-center justify-between">
      <span class="text-[13px] font-bold text-on-surface dark:text-white flex items-center gap-1.5">
        <span class="material-symbols-outlined text-primary text-[18px]">visibility</span>
        Antigravity 全屏通透渲染模拟
        <span v-if="isVideoMedia(config.mediaType, config.imageData || config.filePath)" class="text-[9px] px-1.5 py-0.5 rounded bg-indigo-500 text-white font-mono font-bold">
          LIVE 视频
        </span>
      </span>
      <span class="text-[11px] text-outline">全景磨砂所见即所得</span>
    </div>

    <!-- 模拟 Antigravity IDE 窗口容器 (16:10 宽屏三栏还原，自适应高度贴合视口) -->
    <!-- contain 不取 strict/paint，避免整块 IDE 模拟升为独立 backing store；layout/style 已足分离回流 -->
    <div class="relative w-full rounded-2xl overflow-hidden shadow-2xl border border-white/10 bg-[#0a0d14] flex flex-col aspect-[16/10] min-h-[420px] lg:max-h-[calc(100vh-250px)]" style="contain: layout style;">
      <!-- 全屏底层动态视频/静态图片渲染 (软件渲染下按需 + 休眠感知) -->
      <!-- 视频：仅当是视频且页面可见且 activeTab 时真播放；否则静态图兜底，软解码管道关闭 -->
      <video
        v-if="isVideoMedia(config.mediaType, config.imageData || config.filePath) && isActiveTab && isPageVisible"
        ref="videoRef"
        :src="config.imageData"
        autoplay
        loop
        muted
        playsinline
        preload="metadata"
        class="absolute inset-0 w-full h-full object-cover pointer-events-none transition-opacity duration-150"
        :style="{
          opacity: config.opacity,
          filter: config.blur > 0 ? `blur(${config.blur}px)` : 'none',
          transform: config.blur > 0 ? 'scale(1.08) translate3d(0, 0, 0)' : 'scale(1.01) translate3d(0, 0, 0)',
          willChange: config.blur > 0 ? 'transform, filter' : 'auto'
        }"
      ></video>
      <!-- 静态图 v-else 兜底（含视频非活跃时的静态海报），避免非活跃时维持软解码管道 -->
      <div
        v-else
        class="absolute inset-0 pointer-events-none transition-opacity duration-150"
        :style="{
          backgroundImage: previewBgImage ? `url(${previewBgImage})` : 'none',
          backgroundSize: 'cover',
          backgroundPosition: 'center',
          opacity: config.opacity,
          filter: config.blur > 0 ? `blur(${config.blur}px)` : 'none',
          transform: config.blur > 0 ? 'scale(1.08) translateZ(0)' : 'scale(1.01) translateZ(0)',
          willChange: config.blur > 0 ? 'transform, filter' : 'auto'
        }"
      ></div>

      <!-- 径向暗色遮罩层 (移除 translateZ 独立提升，避免多一块软件 backing store) -->
      <div
        class="absolute inset-0 pointer-events-none"
        :style="{
          background: `radial-gradient(circle at center, rgba(12, 15, 25, ${config.darkOverlay * 0.75}), rgba(6, 8, 14, ${config.darkOverlay}))`
        }"
      ></div>

      <!-- 前端 UI 层 (绑定自定义侧边栏与正文文字颜色) -->
      <div class="relative z-10 flex flex-col h-full text-[11px] select-none">
        <!-- 顶部 Titlebar & Menu (对齐图一) -->
        <div
          class="h-7 px-3 flex items-center justify-between border-b border-white/10 shrink-0"
          :style="{
            backgroundColor: `rgba(12, 15, 24, ${Math.min(0.95, config.glassAlpha * 0.85)})`,
            color: config.sidebarTextColor
          }"
        >
          <div class="flex items-center gap-3 text-[10px]">
            <span class="font-bold tracking-tight" :style="{ color: config.sidebarTextColor }">Antigravity</span>
            <div class="flex items-center gap-2 opacity-80 text-[9.5px]">
              <span class="hover:opacity-100 cursor-pointer">File</span>
              <span class="hover:opacity-100 cursor-pointer">Edit</span>
              <span class="hover:opacity-100 cursor-pointer">View</span>
              <span class="hover:opacity-100 cursor-pointer">Window</span>
            </div>
          </div>
          <div class="flex items-center gap-2 text-[10px] opacity-70">
            <span class="hover:opacity-100 cursor-pointer">─</span>
            <span class="hover:opacity-100 cursor-pointer text-[9px]">□</span>
            <span class="hover:opacity-100 cursor-pointer">✕</span>
          </div>
        </div>

        <!-- 主体三栏结构 (左侧导航 + 中间轨迹对话 + 右侧代码编辑器) -->
        <div class="flex-1 flex overflow-hidden">
          <!-- 1. 左侧边栏 (约 24% 宽度，绑定自定义 sidebarBgColor 与 sidebarTextColor) -->
          <div
            class="w-[24%] max-w-[150px] min-w-[110px] border-r border-white/10 flex flex-col p-2 gap-1 overflow-hidden shrink-0 transition-colors duration-200"
            :style="{
              backgroundColor: config.sidebarBgColor || 'rgba(12, 15, 24, 0.85)',
              color: config.sidebarTextColor
            }"
          >
            <!-- + New Conversation -->
            <div class="px-2 py-1 rounded-md bg-white/10 hover:bg-white/15 text-[9.5px] font-semibold flex items-center gap-1 shadow-sm border border-white/10 cursor-pointer shrink-0" :style="{ color: config.sidebarTextColor }">
              <span class="material-symbols-outlined text-[11px]">add</span>
              <span class="truncate">New Conversation</span>
            </div>

            <!-- Conversation History -->
            <div class="px-1.5 py-0.5 rounded hover:bg-white/5 text-[9px] flex items-center gap-1 opacity-85 mt-0.5 shrink-0 cursor-pointer" :style="{ color: config.sidebarTextColor }">
              <span class="material-symbols-outlined text-[11px] opacity-70">chat_bubble_outline</span>
              <span class="truncate">Conversation History</span>
            </div>

            <!-- Pinned Conversations -->
            <div class="text-[8px] font-bold tracking-wider mt-1 px-1 opacity-60 uppercase shrink-0" :style="{ color: config.sidebarTextColor }">Pinned Conversations</div>
            <div class="flex flex-col gap-0.5 shrink-0">
              <div class="px-1.5 py-0.5 rounded hover:bg-white/5 flex flex-col opacity-90 cursor-pointer">
                <span class="text-[8.5px] truncate font-medium" :style="{ color: config.sidebarTextColor }">Troubleshooting Desktop Log...</span>
                <span class="text-[7.5px] opacity-60 flex items-center justify-between">
                  <span class="truncate">antigravity-proxy</span>
                  <span>2mo</span>
                </span>
              </div>
              <div class="px-1.5 py-0.5 rounded hover:bg-white/5 flex flex-col opacity-90 cursor-pointer">
                <span class="text-[8.5px] truncate font-medium" :style="{ color: config.sidebarTextColor }">Clipboard Path Pasting Support</span>
                <span class="text-[7.5px] opacity-60 flex items-center justify-between">
                  <span class="truncate">screenshotStorage</span>
                  <span>2mo</span>
                </span>
              </div>
            </div>

            <!-- Projects -->
            <div class="text-[8px] font-bold tracking-wider mt-1 px-1 opacity-60 uppercase flex items-center justify-between shrink-0" :style="{ color: config.sidebarTextColor }">
              <span>Projects</span>
              <span class="material-symbols-outlined text-[10px] opacity-60">unfold_more</span>
            </div>
            <div class="flex-1 flex flex-col gap-0.5 text-[8.5px] overflow-hidden">
              <div class="px-1.5 py-0.5 rounded hover:bg-white/5 flex items-center gap-1 truncate opacity-80" :style="{ color: config.sidebarTextColor }">
                <span class="material-symbols-outlined text-[10px] opacity-50">folder</span>
                <span class="truncate">ruida-asset-web</span>
              </div>
              <div class="px-1.5 py-0.5 rounded hover:bg-white/5 flex items-center gap-1 truncate opacity-80" :style="{ color: config.sidebarTextColor }">
                <span class="material-symbols-outlined text-[10px] opacity-50">folder</span>
                <span class="truncate">ovu infoport (路桥)</span>
              </div>
              <div class="px-1.5 py-0.5 rounded hover:bg-white/5 flex items-center gap-1 truncate opacity-80" :style="{ color: config.sidebarTextColor }">
                <span class="material-symbols-outlined text-[10px] opacity-50">folder</span>
                <span class="truncate">luan_xinyihua_screen</span>
              </div>
              <div class="px-1.5 py-0.5 rounded hover:bg-white/5 flex items-center gap-1 truncate opacity-80" :style="{ color: config.sidebarTextColor }">
                <span class="material-symbols-outlined text-[10px] opacity-50">folder</span>
                <span class="truncate">知丘304协同控制系统</span>
              </div>
              <div class="px-1.5 py-0.5 rounded bg-primary/25 font-semibold flex items-center gap-1 truncate border border-primary/40 shadow-sm" :style="{ color: config.sidebarTextColor }">
                <span class="material-symbols-outlined text-[10px] text-primary">folder_open</span>
                <span class="truncate">antigravity-proxy-go</span>
              </div>
              <div class="px-1.5 py-0.5 rounded hover:bg-white/5 flex items-center gap-1 truncate opacity-80" :style="{ color: config.sidebarTextColor }">
                <span class="material-symbols-outlined text-[10px] opacity-50">folder</span>
                <span class="truncate">autoWorkflowAntigravity</span>
              </div>
            </div>

            <!-- Settings -->
            <div class="mt-auto pt-1 border-t border-white/10 flex items-center gap-1 text-[8.5px] opacity-80 shrink-0" :style="{ color: config.sidebarTextColor }">
              <span class="material-symbols-outlined text-[11px]">settings</span>
              <span>Settings</span>
            </div>
          </div>

          <!-- 2. 中间：对话与 Agent 执行轨迹流 (约 40% 宽度，绑定自定义 contentTextColor) -->
          <div
            class="flex-1 border-r border-white/10 flex flex-col justify-between p-2 overflow-hidden"
            :style="{
              backgroundColor: `rgba(10, 12, 20, ${Math.min(0.82, config.glassAlpha * 0.5)})`,
              color: config.contentTextColor
            }"
          >
            <!-- 中间顶部面包屑 -->
            <div class="flex items-center justify-between pb-1.5 border-b border-white/10 text-[9px] shrink-0 opacity-85" :style="{ color: config.contentTextColor }">
              <div class="flex items-center gap-1 truncate">
                <span class="font-semibold">antigravity-proxy-desktop-go</span>
                <span class="opacity-40">/</span>
                <span class="opacity-80 truncate">网页及应用开发优化建议</span>
              </div>
              <div class="flex items-center gap-0.5 px-1.5 py-0.5 rounded bg-white/10 text-[8px] font-medium shrink-0 cursor-pointer hover:bg-white/15">
                <span class="material-symbols-outlined text-[10px]">terminal</span>
                <span>Open IDE</span>
              </div>
            </div>

            <!-- 用户消息气泡实况 (User Message Bubble Preview) -->
            <div
              class="rounded-xl px-2.5 py-1.5 shadow-sm shrink-0 flex items-center justify-between text-[8.5px] my-1 transition-all"
              :style="{
                backgroundColor: config.userMessageBgColor || 'rgba(255, 255, 255, 0.08)',
                color: config.contentTextColor
              }"
            >
              <span class="truncate font-medium">请帮我优化用户消息气泡配色并与全景透光融合</span>
              <span class="material-symbols-outlined text-[10px] opacity-40 ml-1">check</span>
            </div>

            <!-- 执行轨迹卡片流 (Trajectory Flow，绑定 agentMessageBgColor) -->
            <div class="flex-1 my-1 flex flex-col gap-1 overflow-hidden text-[8.5px]">
              <div
                class="rounded-lg p-2 flex flex-col gap-1 shadow-sm overflow-hidden transition-colors duration-200"
                :style="{
                  backgroundColor: config.agentMessageBgColor || 'rgba(15, 18, 28, 0.75)',
                  color: config.contentTextColor
                }"
              >
                <div class="font-bold text-[9px] flex items-center gap-1 border-b border-white/10 pb-1" :style="{ color: config.contentTextColor }">
                  <span class="material-symbols-outlined text-[11px] text-amber-400">tune</span>
                  <span>优化</span>
                </div>
                <div class="flex flex-col gap-0.5 font-mono text-[8px] opacity-90 overflow-hidden leading-tight">
                  <div class="flex items-center gap-1 opacity-70">
                    <span class="w-1 h-1 rounded-full bg-slate-400"></span>
                    <span>Explored 1 file</span>
                  </div>
                  <div class="flex items-center gap-1 opacity-70 truncate">
                    <span class="w-1 h-1 rounded-full bg-emerald-400"></span>
                    <span>Run python checkpoint.py</span>
                  </div>
                  <div class="flex items-center gap-1 text-primary">
                    <span class="w-1 h-1 rounded-full bg-primary"></span>
                    <span>Edited manager.go</span>
                  </div>
                  <div class="flex items-center gap-1 text-emerald-400">
                    <span class="w-1 h-1 rounded-full bg-emerald-400"></span>
                    <span>Run go test ./... finished</span>
                  </div>
                  <div class="flex items-center gap-1 text-amber-400 animate-pulse pt-0.5">
                    <span class="w-1.5 h-1.5 rounded-full bg-amber-400"></span>
                    <span class="font-bold">Working...</span>
                  </div>
                </div>
              </div>
            </div>

            <!-- 项目工作区选择胶囊 (Workspace Selector Badge) -->
            <div class="flex items-center justify-center mb-1 shrink-0">
              <div
                class="px-2 py-0.5 rounded-full bg-white/10 text-[8px] font-medium shadow-sm flex items-center gap-1 transition-colors duration-200"
                :style="{ color: config.contentTextColor }"
              >
                <span>antigravity-proxy-desktop-go</span>
                <span class="material-symbols-outlined text-[9px] opacity-70">expand_more</span>
              </div>
            </div>

            <!-- 居中浮动磨砂输入胶囊 (Frosted Composer，绑定 composerBgColor) -->
            <div
              class="rounded-xl p-1.5 flex flex-col gap-1 border border-white/10 shadow-2xl shrink-0 transition-colors duration-200"
              :style="{ backgroundColor: config.composerBgColor || 'rgba(18, 22, 34, 0.88)' }"
            >
              <div class="text-[9px] flex items-center justify-between px-1" :style="{ color: config.contentTextColor }">
                <span class="opacity-70 truncate">Ask anything, @ to mention, / for actions</span>
                <span class="material-symbols-outlined text-[13px] text-primary">arrow_circle_up</span>
              </div>
              <div class="flex items-center justify-between pt-1 border-t border-white/10 text-[8px] text-slate-400 px-1">
                <span class="flex items-center gap-1 text-primary font-medium">
                  <span class="material-symbols-outlined text-[10px]">auto_awesome</span>
                  Gemini 2.5 Flash High
                </span>
                <span class="flex items-center gap-1 opacity-70">
                  <span class="material-symbols-outlined text-[10px]">mic</span>
                  Local
                </span>
              </div>
            </div>
          </div>

          <!-- 3. 右侧：代码编辑器 (约 36% 宽度，绑定 codeBlockBgColor) -->
          <!-- backdropFilter 仅在 blur>0 时启用，避免 blur(0px) 仍强制对背后视频做一次 backdrop 光栅 -->
          <div
            class="w-[36%] flex flex-col overflow-hidden text-[8.5px] transition-colors duration-200"
            :style="{
              backgroundColor: config.codeBlockBgColor || 'rgba(10, 12, 20, 0.92)',
              color: config.contentTextColor,
              backdropFilter: config.blur > 0 ? `blur(${config.blur}px)` : 'none',
              WebkitBackdropFilter: config.blur > 0 ? `blur(${config.blur}px)` : 'none'
            }"
          >
            <!-- 标签页 Tab Bar -->
            <div class="h-6 flex items-center bg-black/40 border-b border-white/10 px-2 shrink-0 justify-between">
              <div class="flex items-center gap-1.5 px-2 py-0.5 bg-white/10 rounded-t border-t border-x border-white/15 text-[8.5px]">
                <span class="px-1 rounded bg-blue-600 text-[7px] text-white font-mono font-bold leading-none py-0.5">TS</span>
                <span class="font-medium text-white/90 truncate">useWallpaper.ts</span>
                <span class="text-[9px] opacity-60 hover:opacity-100 cursor-pointer ml-1">✕</span>
              </div>
            </div>

            <!-- 面包屑 Breadcrumb -->
            <div class="px-2 py-0.5 text-[7.5px] text-slate-400 border-b border-white/5 truncate font-mono shrink-0">
              antigravity-proxy-desktop-go &gt; frontend &gt; src &gt; composables &gt; useWallpaper.ts
            </div>

            <!-- 代码编辑区 (带行号与语法高亮) -->
            <div class="flex-1 p-2 font-mono text-[8px] leading-relaxed overflow-hidden flex gap-2">
              <!-- 行号 Gutter -->
              <div class="flex flex-col text-slate-500 text-right select-none opacity-50 pr-1 border-r border-white/10 shrink-0 font-mono text-[7.5px]">
                <span>110</span>
                <span>111</span>
                <span>112</span>
                <span>113</span>
                <span>114</span>
                <span>115</span>
                <span>116</span>
                <span>117</span>
                <span>118</span>
                <span>119</span>
                <span>120</span>
                <span>121</span>
                <span>122</span>
                <span>123</span>
              </div>

              <!-- 代码内容与语法高亮 -->
              <div class="flex-1 flex flex-col overflow-hidden text-[7.5px] leading-normal" :style="{ color: config.contentTextColor }">
                <div><span class="text-sky-300">name</span>: <span class="text-amber-300">'深空星光 (MP4 动态)'</span>,</div>
                <div><span class="text-sky-300">url</span>: <span class="text-amber-300">'https://images.unsplash.com/...'</span>,</div>
                <div><span class="text-sky-300">mediaType</span>: <span class="text-amber-300">'image'</span></div>
                <div class="opacity-40">// 视频与静态媒体格式判断</div>
                <div>
                  <span class="text-purple-400">export function</span> <span class="text-blue-300">isVideoMedia</span>(mediaType?: <span class="text-teal-300">string</span>): <span class="text-teal-300">boolean</span> {
                </div>
                <div class="pl-2">
                  <span class="text-purple-400">if</span> (mediaType === <span class="text-amber-300">'video'</span>) <span class="text-purple-400">return true</span>;
                </div>
                <div class="pl-2">
                  <span class="text-purple-400">return</span> urlOrData.<span class="text-blue-300">endsWith</span>(<span class="text-amber-300">'.mp4'</span>);
                </div>
                <div>}</div>
                <div class="opacity-40 pt-0.5">// 滑块进度渐变计算</div>
                <div>
                  <span class="text-purple-400">export function</span> <span class="text-blue-300">getSliderTrackStyle</span>(val: <span class="text-teal-300">number</span>) {
                </div>
                <div class="pl-2">
                  <span class="text-purple-400">const</span> pct = Math.<span class="text-blue-300">round</span>(val * <span class="text-orange-400">100</span>);
                </div>
                <div class="pl-2">
                  <span class="text-purple-400">return</span> { background: <span class="text-amber-300">`linear-gradient(...)`</span> };
                </div>
                <div>}</div>
              </div>
            </div>
          </div>
        </div>

        <!-- 底部状态栏 -->
        <div
          class="h-4 px-3 flex items-center justify-between text-[8px] border-t border-white/10 shrink-0"
          :style="{
            backgroundColor: `rgba(12, 15, 24, ${config.glassAlpha * 0.8})`,
            color: config.sidebarTextColor
          }"
        >
          <div class="flex items-center gap-2">
            <span>UTF-8</span>
            <span>•</span>
            <span>TypeScript</span>
            <span>•</span>
            <span>antigravity-proxy-desktop-go</span>
          </div>
          <span class="text-emerald-400 flex items-center gap-1 font-mono">
            <span class="w-1.5 h-1.5 rounded-full bg-emerald-400 animate-pulse"></span>
            Online
          </span>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted, onBeforeUnmount, onUnmounted, nextTick } from 'vue';
import { useWallpaper } from '../../../composables/useWallpaper';

const { config, isActiveTab, isVideoMedia } = useWallpaper();
const videoRef = ref<HTMLVideoElement | null>(null);

// 页面可见性：桌面端窗口最小化 / 切到后台时 document.hidden=true，
// 此闸与 isActiveTab 双重控制 <video> v-if，非可见时直接卸载视频节点，
// 关闭软件解码管道 (GPU 已禁用，视频软解码是主要内存/CPU 消费源)。
const isPageVisible = ref(typeof document !== 'undefined' ? !document.hidden : true);

function handleVisibilityChange() {
  isPageVisible.value = !document.hidden;
}

// 动态视频非活跃 (切走/最小化) 时静态兜底用背景图：
// - 静态图壁纸：始终走 CSS background 低耗路径 (不进解码器)
// - 动态视频壁纸：非活跃时不渲染视频帧，返回空串显示纯黑底，避免把 mp4 URL 喂给 CSS background-image (无效且占内存)
const previewBgImage = computed(() => {
  if (isVideoMedia(config.mediaType, config.imageData || config.filePath)) return '';
  return config.imageData || '';
});

function releaseVideoMemory() {
  if (videoRef.value) {
    try {
      videoRef.value.pause();
      videoRef.value.removeAttribute('src');
      videoRef.value.load(); // 关键：强制通知 Chromium/WebKit 彻底销毁媒体解码器上下文并释放解压帧内存
    } catch (e) {}
  }
}

// 应当播放的判定：当前在壁纸子页签 且 页面可见 且 是视频媒体
function shouldPlayVideo(): boolean {
  return isActiveTab.value && isPageVisible.value &&
    isVideoMedia(config.mediaType, config.imageData || config.filePath);
}

// resumeVideo 在 v-if 渲染出 <video> 之后 (post-flush) 安全调用 play
function resumeVideo() {
  nextTick(() => {
    if (videoRef.value && config.imageData) {
      try {
        if (videoRef.value.src !== config.imageData) {
          videoRef.value.src = config.imageData;
          videoRef.value.load();
        }
        videoRef.value.play().catch(() => {});
      } catch (e) {}
    }
  });
}

watch(isActiveTab, (active) => {
  // 切走壁纸子页签：pre-flush 时 videoRef 仍指向即将被 v-if 移除的 <video>，
  // 在此显式 release 解码器上下文；随后 Vue patch 移除该节点。
  if (!active) {
    releaseVideoMemory();
  } else if (shouldPlayVideo()) {
    // 切回壁纸子页签：post-flush 等新 <video> 挂载后恢复播放
    resumeVideo();
  }
});

watch(isPageVisible, (visible) => {
  if (!visible) {
    // 最小化/后台：v-if 即将移除 <video>，pre-flush 先释放解码器
    releaseVideoMemory();
  } else if (shouldPlayVideo()) {
    // 恢复可见：post-flush 恢复播放
    resumeVideo();
  }
});

watch(() => config.imageData, (newUrl) => {
  if (shouldPlayVideo() && newUrl) {
    nextTick(() => {
      if (videoRef.value) {
        videoRef.value.src = newUrl;
        videoRef.value.load();
        videoRef.value.play().catch(() => {});
      }
    });
  }
});

onMounted(() => {
  if (typeof document !== 'undefined') {
    document.addEventListener('visibilitychange', handleVisibilityChange);
    isPageVisible.value = !document.hidden;
  }
  if (shouldPlayVideo()) {
    resumeVideo();
  }
});

onBeforeUnmount(() => {
  releaseVideoMemory();
  if (typeof document !== 'undefined') {
    document.removeEventListener('visibilitychange', handleVisibilityChange);
  }
});

onUnmounted(() => {
  releaseVideoMemory();
});
</script>
