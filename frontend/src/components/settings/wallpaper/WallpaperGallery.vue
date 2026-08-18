<template>
  <!-- 壁纸库管理卡片 (多壁纸轻量缩略图网格，GPU 图层独立隔离) -->
  <div class="glass-card rounded-xl p-5 flex flex-col gap-4">
    <div class="flex items-center justify-between border-b border-outline-variant/20 pb-3">
      <div class="flex items-center gap-2">
        <span class="material-symbols-outlined text-primary text-[20px]">collections</span>
        <h3 class="text-[15px] font-bold text-on-surface dark:text-white">
          我的壁纸收藏库
          <span class="text-[11px] font-normal text-outline ml-1">({{ galleryList.length }} 张已收藏)</span>
        </h3>
      </div>
      <div class="flex items-center gap-2">
        <button
          @click="openImagePicker"
          :disabled="isLoadingImage"
          class="px-3 py-1.5 bg-primary text-white hover:bg-primary/90 rounded-lg text-[12px] font-bold transition-all flex items-center gap-1.5 shadow-sm cursor-pointer"
        >
          <span class="material-symbols-outlined text-[16px]">add_photo_alternate</span>
          <span>导入并收藏新壁纸</span>
        </button>
      </div>
    </div>

    <!-- 壁纸网格 Shelf (添加 GPU 独立分层隔离，滚动极速流畅) -->
    <div class="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6 gap-3">
      <!-- 导入快捷入口卡片 -->
      <button
        @click="openImagePicker"
        :disabled="isLoadingImage"
        class="h-28 rounded-xl border-2 border-dashed border-outline-variant/50 hover:border-primary bg-slate-50/50 dark:bg-white/5 hover:bg-primary/5 flex flex-col items-center justify-center gap-1.5 transition-all text-outline hover:text-primary cursor-pointer group"
        style="contain: layout paint style;"
      >
        <span class="material-symbols-outlined text-[26px] group-hover:scale-110 transition-transform">add_circle</span>
        <span class="text-[11px] font-bold">{{ isLoadingImage ? '读取中...' : '导入本地图片/视频' }}</span>
      </button>

      <!-- 自定义收藏的壁纸卡片列表 (收敛软件渲染下的独立合成层：仅 content-visibility 虚拟化保留，去掉常驻 will-change/translateZ) -->
      <div
        v-for="item in galleryList"
        :key="item.id"
        @click="selectGalleryItem(item)"
        class="relative h-28 rounded-xl overflow-hidden border-2 transition-all cursor-pointer group select-none shadow-sm"
        :class="isCurrentSelected(item) ? 'border-primary ring-2 ring-primary/30 shadow-primary/20 scale-[1.02]' : 'border-outline-variant/30 hover:border-primary/60'"
        style="content-visibility: auto;"
      >
        <img
          :src="item.thumbnail || item.data"
          loading="lazy"
          decoding="async"
          class="w-full h-full object-cover group-hover:scale-105 transition-transform duration-300 pointer-events-none"
        />
        <!-- 遮罩与名字 -->
        <div class="absolute inset-0 bg-gradient-to-t from-black/85 via-black/20 to-transparent flex flex-col justify-between p-2 pointer-events-none">
          <!-- 顶部选中状态、视频徽章与删除按钮 -->
          <div class="flex items-center justify-between pointer-events-auto">
            <span v-if="isCurrentSelected(item)" class="bg-primary text-white text-[9px] font-bold px-1.5 py-0.5 rounded shadow-sm">
              当前选用
            </span>
            <span v-else-if="isVideoMedia(item.mediaType, item.data || item.filePath)" class="bg-indigo-600/90 text-white text-[8px] font-bold px-1.5 py-0.5 rounded shadow-sm flex items-center gap-0.5">
              <span class="material-symbols-outlined text-[10px]">movie</span>
              <span>动态</span>
            </span>
            <span v-else></span>
            <button
              @click.stop="deleteGalleryItem(item.id)"
              class="w-6 h-6 rounded-full bg-black/70 hover:bg-red-600 text-white flex items-center justify-center opacity-0 group-hover:opacity-100 transition-all cursor-pointer"
              title="从壁纸库中移除"
            >
              <span class="material-symbols-outlined text-[14px]">delete</span>
            </button>
          </div>
          <!-- 底部名称 -->
          <div class="text-[10px] font-bold text-white truncate drop-shadow-md flex items-center gap-1">
            <span v-if="isVideoMedia(item.mediaType, item.data || item.filePath)" class="material-symbols-outlined text-[11px] text-indigo-400">play_circle</span>
            <span class="truncate">{{ item.name }}</span>
          </div>
        </div>
      </div>

      <!-- 官方预设风格卡片 (包含动态视频预设) -->
      <div
        v-for="(preset, idx) in presets"
        :key="'preset_' + idx"
        @click="applyPreset(preset)"
        class="relative h-28 rounded-xl overflow-hidden border-2 transition-all cursor-pointer group select-none shadow-sm"
        :class="config.imageData === preset.url ? 'border-primary ring-2 ring-primary/30 scale-[1.02]' : 'border-outline-variant/30 hover:border-primary/60'"
        style="content-visibility: auto;"
      >
        <img
          :src="preset.thumbnail || preset.url"
          loading="lazy"
          decoding="async"
          class="w-full h-full object-cover group-hover:scale-105 transition-transform duration-300 pointer-events-none"
        />
        <div class="absolute inset-0 bg-gradient-to-t from-black/85 via-black/20 to-transparent flex flex-col justify-between p-2 pointer-events-none">
          <div class="flex items-center justify-between">
            <span v-if="config.imageData === preset.url" class="bg-primary text-white text-[9px] font-bold px-1.5 py-0.5 rounded shadow-sm w-fit">
              当前选用
            </span>
            <span v-else-if="isVideoMedia(preset.mediaType, preset.url)" class="bg-indigo-600/90 text-white text-[8px] font-bold px-1.5 py-0.5 rounded shadow-sm flex items-center gap-0.5">
              <span class="material-symbols-outlined text-[10px]">movie</span>
              <span>动态</span>
            </span>
            <span v-else class="text-[9px] text-white/70 font-medium">官方预设</span>
          </div>
          <div class="text-[10px] font-bold text-white truncate drop-shadow-md flex items-center gap-1">
            <span v-if="isVideoMedia(preset.mediaType, preset.url)" class="material-symbols-outlined text-[11px] text-indigo-400">play_circle</span>
            <span class="truncate">{{ preset.name }}</span>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useWallpaper } from '../../../composables/useWallpaper';

const {
  galleryList,
  config,
  presets,
  isLoadingImage,
  isCurrentSelected,
  selectGalleryItem,
  applyPreset,
  openImagePicker,
  deleteGalleryItem,
  isVideoMedia
} = useWallpaper();
</script>
