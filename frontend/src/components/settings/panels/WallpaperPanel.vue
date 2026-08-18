<template>
  <div class="flex flex-col gap-6 w-full" id="settings-panel-wallpaper">
    <!-- 顶部状态与路径检测卡片 -->
    <WallpaperHeader />

    <!-- 壁纸收藏库卡片 (多壁纸轻量缩略图网格) -->
    <WallpaperGallery />

    <!-- 主体：左侧调谐控制 + 右侧实时模拟视窗 -->
    <div class="grid grid-cols-1 lg:grid-cols-12 gap-6 items-start relative">
      <!-- 左侧参数调节卡片 -->
      <WallpaperTuner />

      <!-- 右侧全景通透实时模拟视窗 (休眠感知 + 硬件加速) -->
      <WallpaperLivePreview />
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted } from 'vue';
import WallpaperHeader from '../wallpaper/WallpaperHeader.vue';
import WallpaperGallery from '../wallpaper/WallpaperGallery.vue';
import WallpaperTuner from '../wallpaper/WallpaperTuner.vue';
import WallpaperLivePreview from '../wallpaper/WallpaperLivePreview.vue';
import { useWallpaper } from '../../../composables/useWallpaper';

const { initWallpaperPanel, destroyWallpaperPanel } = useWallpaper();

onMounted(() => {
  initWallpaperPanel();
});

onUnmounted(() => {
  destroyWallpaperPanel();
});
</script>
