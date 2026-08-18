<template>
  <!-- 左侧：参数调谐控制区 (7列，独立平滑滚动) -->
  <div class="lg:col-span-7 flex flex-col gap-5 lg:max-h-[calc(100vh-210px)] lg:overflow-y-auto lg:pr-2.5 custom-tuner-scrollbar">
    <!-- 详细调谐参数卡片 -->
    <div class="glass-card rounded-xl p-5 flex flex-col gap-4">
      <div class="flex items-center justify-between border-b border-outline-variant/20 pb-3">
        <h3 class="text-[14px] font-bold text-on-surface dark:text-white flex items-center gap-2">
          <span class="material-symbols-outlined text-primary text-[18px]">tune</span>
          当前壁纸视觉细节与通透度调谐
        </h3>
        <span class="text-[11px] text-outline font-mono truncate max-w-[200px]" :title="selectedFileName">
          {{ selectedFileName || '未选择壁纸' }}
        </span>
      </div>

      <!-- 网络图片自定义 URL 备用输入栏 -->
      <div class="flex flex-col gap-1.5">
        <div class="flex justify-between items-center text-[12px]">
          <span class="font-bold text-on-surface dark:text-white">网络图片直链 URL (可选)</span>
          <button @click="saveUrlToGallery" class="text-[11px] text-primary hover:underline font-bold cursor-pointer">
            + 保存此 URL 到壁纸库
          </button>
        </div>
        <div class="flex gap-2">
          <input
            v-model="customUrlInput"
            type="text"
            placeholder="输入网络图片 URL (例如 https://images.unsplash.com/...)"
            class="flex-grow px-3 py-1.5 text-[12px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:outline-none text-on-surface dark:text-white font-mono"
          />
          <button
            @click="applyCustomUrl"
            class="px-3 py-1.5 bg-slate-100 dark:bg-white/10 hover:bg-slate-200 dark:hover:bg-white/15 text-[12px] font-bold rounded-md cursor-pointer"
          >
            载入预览
          </button>
        </div>
      </div>

      <!-- 壁纸不透明度 (Opacity) -->
      <div class="flex flex-col gap-1.5 pt-2 border-t border-outline-variant/20">
        <div class="flex justify-between items-center text-[12px]">
          <span class="font-bold text-on-surface dark:text-white">壁纸不透明度 (Opacity)</span>
          <span class="font-mono text-primary font-bold">{{ Math.round(config.opacity * 100) }}%</span>
        </div>
        <input
          v-model.number="config.opacity"
          type="range"
          min="0.05"
          max="1.0"
          step="0.01"
          :style="getSliderTrackStyle(config.opacity, 0.05, 1.0, '#6366f1')"
          class="w-full h-1.5 rounded-lg appearance-none cursor-pointer accent-primary"
        />
        <span class="text-[10px] text-outline">控制全景壁纸可见度。建议 35%~60% 配合暗色遮罩呈现最佳美感。</span>
      </div>

      <!-- 毛玻璃模糊度 (Blur) -->
      <div class="flex flex-col gap-1.5 pt-2 border-t border-outline-variant/20">
        <div class="flex justify-between items-center text-[12px]">
          <span class="font-bold text-on-surface dark:text-white">毛玻璃模糊度 (Blur Radius)</span>
          <span class="font-mono text-primary font-bold">{{ config.blur }} px</span>
        </div>
        <input
          v-model.number="config.blur"
          type="range"
          min="0"
          max="40"
          step="1"
          :style="getSliderTrackStyle(config.blur, 0, 40, '#6366f1')"
          class="w-full h-1.5 rounded-lg appearance-none cursor-pointer accent-primary"
        />
        <span class="text-[10px] text-outline">虚化背景轮廓，增加空间景深感。0 为超清原画。</span>
      </div>

      <!-- 遮罩暗度 (Dark Overlay) -->
      <div class="flex flex-col gap-1.5 pt-2 border-t border-outline-variant/20">
        <div class="flex justify-between items-center text-[12px]">
          <span class="font-bold text-on-surface dark:text-white">暗色遮罩浓度 (Dark Tint)</span>
          <span class="font-mono text-primary font-bold">{{ Math.round(config.darkOverlay * 100) }}%</span>
        </div>
        <input
          v-model.number="config.darkOverlay"
          type="range"
          min="0.0"
          max="0.85"
          step="0.01"
          :style="getSliderTrackStyle(config.darkOverlay, 0.0, 0.85, '#6366f1')"
          class="w-full h-1.5 rounded-lg appearance-none cursor-pointer accent-primary"
        />
        <span class="text-[10px] text-outline">在壁纸上方覆盖深色径向半透明遮罩，大幅增强白字可读性与文字对比度。</span>
      </div>

      <!-- 界面卡片通透度 (UI Glass Alpha) -->
      <div class="flex flex-col gap-1.5 pt-2 border-t border-outline-variant/20">
        <div class="flex justify-between items-center text-[12px]">
          <span class="font-bold text-on-surface dark:text-white">侧边栏与 IDE 容器底色通透度</span>
          <span class="font-mono text-primary font-bold">{{ Math.round(config.glassAlpha * 100) }}%</span>
        </div>
        <input
          v-model.number="config.glassAlpha"
          type="range"
          min="0.4"
          max="1.0"
          step="0.01"
          :style="getSliderTrackStyle(config.glassAlpha, 0.4, 1.0, '#6366f1')"
          class="w-full h-1.5 rounded-lg appearance-none cursor-pointer accent-primary"
        />
        <span class="text-[10px] text-outline">控制 Antigravity 左侧边栏、对话工作区及编辑器的半透明磨砂程度。</span>
      </div>

      <!-- 深色主题自动联动 (Auto Dark Theme) -->
      <div class="flex flex-col gap-2 pt-2 border-t border-outline-variant/20">
        <div class="flex justify-between items-center text-[12px]">
          <label class="font-bold text-on-surface dark:text-white flex items-center gap-1.5 cursor-pointer">
            <input
              type="checkbox"
              v-model="config.autoDarkTheme"
              class="w-4 h-4 rounded text-primary focus:ring-primary accent-primary cursor-pointer"
            />
            <span>🌓 自动将 Antigravity 切换为深色主题 (推荐)</span>
          </label>
          <span class="text-[11px] font-mono font-bold text-primary" v-if="config.autoDarkTheme">已启用</span>
        </div>
        <div v-if="config.autoDarkTheme" class="flex items-center gap-2 pl-5">
          <span class="text-[11px] text-outline">深色预设:</span>
          <select
            v-model="config.colorTheme"
            class="px-2.5 py-1 text-[11px] bg-slate-50 dark:bg-white/5 border border-outline-variant/60 rounded-md focus:outline-none text-on-surface dark:text-white font-medium"
          >
            <option value="Default Dark Modern">Default Dark Modern (现代深色 - 最佳磨砂透光)</option>
            <option value="Default Dark+">Default Dark+ (经典深色)</option>
            <option value="Solarized Dark">Solarized Dark (深空暗蓝)</option>
            <option value="Abyss">Abyss (深渊暗黑)</option>
          </select>
        </div>
        <span class="text-[10px] text-outline pl-5">应用壁纸时自动将 Antigravity 切换为深色模式，杜绝纯白大色块阻挡壁纸。</span>
      </div>

      <!-- 动态视频与动效播放控制 (仅在选择动态视频时展开) -->
      <div v-if="isVideoMedia(config.mediaType, config.imageData || config.filePath)" class="flex flex-col gap-2.5 pt-2 border-t border-indigo-500/20 bg-indigo-500/5 p-3 rounded-xl border border-indigo-500/20">
        <div class="flex justify-between items-center text-[12px]">
          <span class="font-bold text-indigo-600 dark:text-indigo-400 flex items-center gap-1.5">
            <span class="material-symbols-outlined text-[16px]">slow_motion_video</span>
            动态视频播放速度 (Playback Speed)
          </span>
          <span class="font-mono text-indigo-600 dark:text-indigo-400 font-bold bg-indigo-500/10 px-2 py-0.5 rounded">{{ config.playbackRate }}x</span>
        </div>

        <!-- 快捷预设小胶囊 -->
        <div class="flex items-center gap-1.5 flex-wrap">
          <button
            v-for="preset in speedPresets"
            :key="preset.val"
            type="button"
            @click="config.playbackRate = preset.val"
            class="px-2 py-0.5 rounded text-[10px] font-bold transition-all cursor-pointer border"
            :class="config.playbackRate === preset.val ? 'bg-indigo-600 text-white border-indigo-600 shadow-sm' : 'bg-white/5 hover:bg-white/10 text-outline hover:text-on-surface dark:hover:text-white border-outline-variant/30'"
          >
            {{ preset.label }}
          </button>
        </div>

        <!-- 视频倍速滑块 -->
        <input
          v-model.number="config.playbackRate"
          type="range"
          min="0.25"
          max="2.5"
          step="0.25"
          :style="getSliderTrackStyle(config.playbackRate, 0.25, 2.5, '#6366f1')"
          class="w-full h-1.5 rounded-lg appearance-none cursor-pointer accent-indigo-500"
        />

        <!-- 精确数学坐标对齐刻度 -->
        <div class="relative w-full h-3 text-[9px] text-outline select-none">
          <span
            v-for="tick in speedTicks"
            :key="tick.val"
            class="absolute cursor-pointer transition-colors hover:text-indigo-500"
            :class="config.playbackRate === tick.val ? 'text-indigo-600 dark:text-indigo-400 font-bold' : ''"
            :style="{
              left: `${((tick.val - 0.25) / 2.25) * 100}%`,
              transform: tick.val === 0.25 ? 'none' : tick.val === 2.5 ? 'translateX(-100%)' : 'translateX(-50%)'
            }"
            @click="config.playbackRate = tick.val"
          >
            {{ tick.label }}
          </span>
        </div>

        <div class="flex items-center gap-4 pt-1 text-[11px] text-outline">
          <label class="flex items-center gap-1 cursor-pointer">
            <input type="checkbox" v-model="config.loop" class="w-3.5 h-3.5 accent-indigo-500 rounded" />
            <span>无缝循环播放</span>
          </label>
          <label class="flex items-center gap-1 cursor-pointer">
            <input type="checkbox" v-model="config.muted" class="w-3.5 h-3.5 accent-indigo-500 rounded" />
            <span>静音播放 (低功耗)</span>
          </label>
        </div>
      </div>
    </div>

    <!-- 全界面 2D 专业调色盘 Studio -->
    <div class="glass-card rounded-xl p-5 flex flex-col gap-4">
      <div class="flex items-center justify-between border-b border-outline-variant/20 pb-3">
        <div>
          <h3 class="text-[14px] font-bold text-on-surface dark:text-white flex items-center gap-2">
            <span class="material-symbols-outlined text-primary text-[18px]">palette</span>
            全界面 2D 专业调色盘 Studio
          </h3>
          <p class="text-[11px] text-outline mt-0.5">采用专业 DevTools 2D 拾色器，点击下方模块卡片即可自由取色与调节透明度</p>
        </div>
        <button
          @click="resetTextColors"
          class="text-[11px] text-outline hover:text-primary transition-colors flex items-center gap-1 cursor-pointer shrink-0"
        >
          <span class="material-symbols-outlined text-[13px]">replay</span>
          恢复默认色彩
        </button>
      </div>

      <!-- 1. 一键主题风格搭配预设 (One-Click Style Packs) -->
      <div class="flex flex-col gap-2">
        <span class="text-[12px] font-bold text-on-surface dark:text-white flex items-center gap-1.5">
          <span class="material-symbols-outlined text-amber-500 text-[15px]">auto_awesome</span>
          一键色彩风格方案
        </span>
        <div class="grid grid-cols-2 sm:grid-cols-4 gap-2">
          <button
            v-for="pack in colorThemePacks"
            :key="pack.name"
            @click="applyColorThemePack(pack)"
            class="p-2.5 rounded-lg border text-left transition-all cursor-pointer flex flex-col gap-1 hover:border-primary/60 hover:bg-primary/5 bg-slate-50/50 dark:bg-white/5 border-outline-variant/30"
          >
            <span class="text-[11px] font-bold text-on-surface dark:text-white truncate">{{ pack.name }}</span>
            <span class="text-[9.5px] text-outline truncate">{{ pack.desc }}</span>
          </button>
        </div>
      </div>

      <!-- 2. 8 大模块选择网格 (8 Modules Matrix) -->
      <div class="flex flex-col gap-2 pt-2 border-t border-outline-variant/20">
        <span class="text-[12px] font-bold text-on-surface dark:text-white flex items-center justify-between">
          <span class="flex items-center gap-1.5">
            <span class="material-symbols-outlined text-primary text-[15px]">touch_app</span>
            选择调节区域 (点击卡片切换调色焦点)
          </span>
          <span class="text-[10px] text-outline">正在调节: <span class="text-primary font-bold">{{ currentModule.name }}</span></span>
        </span>

        <div class="grid grid-cols-2 sm:grid-cols-4 gap-2">
          <button
            v-for="mod in colorModules"
            :key="mod.id"
            @click="activeModuleId = mod.id"
            class="p-2 rounded-lg border transition-all cursor-pointer flex items-center gap-2 text-left"
            :class="activeModuleId === mod.id ? 'border-primary ring-2 ring-primary/40 bg-primary/10 shadow-sm' : 'border-outline-variant/30 hover:bg-slate-100 dark:hover:bg-white/5 bg-slate-50/30 dark:bg-white/[0.02]'"
          >
            <!-- 颜色预览色块 -->
            <div class="w-5 h-5 rounded-full border border-black/20 shrink-0 shadow-xs flex items-center justify-center" :style="{ backgroundColor: (config as any)[mod.id] }">
              <span v-if="activeModuleId === mod.id" class="material-symbols-outlined text-[12px] text-white drop-shadow">check</span>
            </div>
            <div class="flex flex-col min-w-0 flex-1">
              <span class="text-[11px] font-bold text-on-surface dark:text-white truncate">{{ mod.name }}</span>
              <span class="text-[9px] font-mono text-outline truncate">{{ (config as any)[mod.id] }}</span>
            </div>
          </button>
        </div>
      </div>

      <!-- 3. 专业 2D 调色盘工作台 (Active 2D Color Studio) -->
      <div class="mt-2 pt-3 border-t border-outline-variant/20 flex flex-col md:flex-row gap-5 items-start bg-slate-50/50 dark:bg-black/20 p-4 rounded-xl border border-outline-variant/30">
        <!-- 左侧：Chrome / VSCode 风格 2D 调色盘 -->
        <div class="w-full md:w-auto flex justify-center shrink-0">
          <DevtoolsColorPicker v-model="(config as any)[activeModuleId]" />
        </div>

        <!-- 右侧：当前模块详情与专属推荐色卡 -->
        <div class="flex-1 flex flex-col gap-3 min-w-0">
          <div class="flex items-center justify-between border-b border-outline-variant/20 pb-2">
            <div class="flex items-center gap-2">
              <span class="material-symbols-outlined text-primary text-[20px]">{{ currentModule.icon }}</span>
              <div>
                <h4 class="text-[13px] font-bold text-on-surface dark:text-white">{{ currentModule.name }}</h4>
                <p class="text-[10.5px] text-outline">{{ currentModule.desc }}</p>
              </div>
            </div>
            <span class="text-[11px] font-mono font-bold px-2 py-0.5 rounded bg-primary/10 text-primary border border-primary/20">
              {{ (config as any)[activeModuleId] }}
            </span>
          </div>

          <!-- 专属推荐色卡 (如果该模块有专属预设) -->
          <div v-if="currentModule.presets && currentModule.presets.length > 0" class="flex flex-col gap-2">
            <span class="text-[11px] font-bold text-outline">专属精选配色推荐:</span>
            <div class="flex items-center gap-1.5 flex-wrap">
              <button
                v-for="chip in currentModule.presets"
                :key="chip.color"
                @click="(config as any)[activeModuleId] = chip.color"
                class="flex items-center gap-1.5 px-2 py-1 rounded-md text-[10.5px] font-bold border transition-all cursor-pointer"
                :class="(config as any)[activeModuleId].toLowerCase() === chip.color.toLowerCase() ? 'border-primary ring-1 ring-primary/40 bg-primary/10 text-primary' : 'border-outline-variant/30 hover:bg-slate-100 dark:hover:bg-white/10 text-on-surface dark:text-white'"
              >
                <span class="w-2.5 h-2.5 rounded-full border border-black/20" :style="{ backgroundColor: chip.color }"></span>
                <span>{{ chip.label }}</span>
              </button>
            </div>
          </div>

          <!-- 视觉操作提示 -->
          <div class="text-[10px] text-outline flex items-center gap-1 mt-auto pt-2 bg-slate-100 dark:bg-white/5 p-2 rounded-lg">
            <span class="material-symbols-outlined text-[13px] text-primary">info</span>
            <span>在左侧二维色板中拖拽十字光标选色，拉动第二条滑块可精准调节透明磨砂浓度。</span>
          </div>
        </div>
      </div>
    </div>

    <!-- 操作按钮栏 -->
    <div class="flex flex-col gap-2">
      <div v-if="status.isRunning" class="text-[11px] text-sky-600 dark:text-sky-400 bg-sky-50 dark:bg-sky-950/30 px-3 py-1.5 rounded-lg border border-sky-200 dark:border-sky-800/30 flex items-center gap-1.5">
        <span class="material-symbols-outlined text-[14px]">info</span>
        <span>检测到 Antigravity 正在运行，注入时将自动静默解除文件锁定并在完成后重新拉起。</span>
      </div>

      <div class="flex flex-wrap items-center gap-3">
        <button
          @click="handleApply"
          :disabled="isApplying || !config.imageData"
          class="flex-1 py-3 px-6 bg-primary hover:bg-primary/90 disabled:opacity-50 text-white rounded-xl text-[14px] font-bold transition-all shadow-md shadow-primary/20 flex items-center justify-center gap-2 cursor-pointer"
        >
          <span class="material-symbols-outlined text-[20px]">{{ isApplying ? 'hourglass_top' : 'rocket_launch' }}</span>
          <span>{{ isApplying ? '正在注入并应用...' : '🚀 一键注入并应用到 Antigravity' }}</span>
        </button>

        <button
          @click="handleRestore"
          :disabled="isRestoring || !status.hasBackup"
          class="py-3 px-5 bg-slate-100 dark:bg-white/10 hover:bg-slate-200 dark:hover:bg-white/15 disabled:opacity-40 text-on-surface dark:text-white rounded-xl text-[13px] font-bold transition-all flex items-center gap-1.5 cursor-pointer"
          title="还原为未修改的官方出厂状态"
        >
          <span class="material-symbols-outlined text-[18px]">restore</span>
          <span>恢复出厂原版</span>
        </button>

        <button
          v-if="status.installed"
          @click="handleLaunch"
          class="py-3 px-4 bg-emerald-600 hover:bg-emerald-700 text-white rounded-xl text-[13px] font-bold transition-all flex items-center gap-1.5 cursor-pointer"
          title="直接拉起或重启 Antigravity 客户端"
        >
          <span class="material-symbols-outlined text-[18px]">play_arrow</span>
          <span>启动 Antigravity</span>
        </button>
      </div>
    </div>

    <!-- 提示信息 -->
    <div v-if="opMessage" class="p-3 rounded-lg text-[12px] flex items-center gap-2" :class="opMessage.success ? 'bg-emerald-50 dark:bg-emerald-950/40 text-emerald-700 dark:text-emerald-300 border border-emerald-200 dark:border-emerald-800/40' : 'bg-red-50 dark:bg-red-950/40 text-red-700 dark:text-red-300 border border-red-200 dark:border-red-800/40'">
      <span class="material-symbols-outlined text-[18px]">{{ opMessage.success ? 'check_circle' : 'error' }}</span>
      <span>{{ opMessage.text }}</span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue';
import DevtoolsColorPicker from '../../common/DevtoolsColorPicker.vue';
import { useWallpaper } from '../../../composables/useWallpaper';

const {
  status,
  config,
  selectedFileName,
  customUrlInput,
  isApplying,
  isRestoring,
  opMessage,
  sidebarColorPresets,
  contentColorPresets,
  userMessageColorPresets,
  agentMessageColorPresets,
  sidebarBgColorPresets,
  composerBgColorPresets,
  colorThemePacks,
  speedPresets,
  speedTicks,
  isVideoMedia,
  getSliderTrackStyle,
  applyCustomUrl,
  saveUrlToGallery,
  applyColorThemePack,
  handleApply,
  handleRestore,
  handleLaunch,
  resetTextColors
} = useWallpaper();

const activeModuleId = ref<string>('userMessageBgColor');

const colorModules = computed(() => [
  { id: 'userMessageBgColor', name: '用户消息气泡', icon: 'chat_bubble', desc: '用户发送的消息气泡底色与通透度', presets: userMessageColorPresets },
  { id: 'agentMessageBgColor', name: 'Agent 助手卡片', icon: 'smart_toy', desc: 'AI 回复卡片与执行轨迹卡片底色', presets: agentMessageColorPresets },
  { id: 'composerBgColor', name: '底部输入胶囊', icon: 'edit_square', desc: '居中浮动输入框与搜索框底色', presets: composerBgColorPresets },
  { id: 'sidebarBgColor', name: '左侧边栏底色', icon: 'dock_to_left', desc: '左侧会话历史与项目导航栏底色', presets: sidebarBgColorPresets },
  { id: 'sidebarTextColor', name: '侧边栏文字', icon: 'text_fields', desc: '侧边栏会话与导航文字颜色', presets: sidebarColorPresets },
  { id: 'contentTextColor', name: '正文/对话文字', icon: 'format_size', desc: '主工作区正文排版与 Markdown 文字', presets: contentColorPresets },
  { id: 'modalBgColor', name: '设置与弹出菜单', icon: 'widgets', desc: '设置弹窗、下拉菜单与浮动遮罩底色', presets: [] },
  { id: 'codeBlockBgColor', name: '代码块与编辑器', icon: 'code', desc: '聊天代码预览块与 Monaco 编辑器底色', presets: [] }
]);

const currentModule = computed(() => colorModules.value.find(m => m.id === activeModuleId.value) || colorModules.value[0]);
</script>

<style scoped>
/* 优雅平滑定制滚动条 */
.custom-tuner-scrollbar {
  scrollbar-width: thin;
  scrollbar-color: rgba(148, 163, 184, 0.25) transparent;
}
.custom-tuner-scrollbar::-webkit-scrollbar {
  width: 5px;
}
.custom-tuner-scrollbar::-webkit-scrollbar-track {
  background: transparent;
}
.custom-tuner-scrollbar::-webkit-scrollbar-thumb {
  background: rgba(148, 163, 184, 0.25);
  border-radius: 9999px;
}
.custom-tuner-scrollbar::-webkit-scrollbar-thumb:hover {
  background: rgba(99, 102, 241, 0.55);
}
</style>
