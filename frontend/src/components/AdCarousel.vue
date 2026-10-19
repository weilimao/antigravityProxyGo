<template>
  <div 
    v-if="isVisible && ads.length > 0" 
    class="w-full relative group rounded-xl overflow-hidden border border-outline-variant/30 shadow-sm hover:shadow-md transition-all duration-300 bg-white/70 dark:bg-[#1a1f30]/80 backdrop-blur select-none mb-2"
    @mouseenter="handleMouseEnter"
    @mouseleave="handleMouseLeave"
  >
    <!-- 广告横幅容器 -->
    <div class="relative w-full h-[88px] sm:h-[100px] md:h-[110px] cursor-pointer overflow-hidden" @click="handleAdClick(currentAd)">
      <transition name="fade-slide" mode="out-in">
        <div :key="currentAd.id" class="w-full h-full relative flex items-center">
          <!-- 背景图 -->
          <img 
            :src="currentAd.image_url" 
            :alt="getDisplayTitle(currentAd)"
            class="absolute inset-0 w-full h-full object-cover object-center transform transition-transform duration-700 group-hover:scale-[1.01]"
            @error="handleImageError"
          />
          <!-- 渐变遮罩保护文本可读性 -->
          <div class="absolute inset-0 bg-gradient-to-r from-black/70 via-black/40 to-transparent"></div>
          
          <!-- 广告标题与副文本 -->
          <div class="relative z-10 px-5 sm:px-8 max-w-[75%] sm:max-w-[70%] flex flex-col gap-1 justify-center">
            <div class="flex items-center gap-2">
              <span class="px-1.5 py-0.5 text-[10px] font-bold tracking-wider rounded bg-primary/80 text-white uppercase shadow-sm">
                AD
              </span>
              <h3 class="text-white text-[14px] sm:text-[16px] md:text-[17px] font-bold line-clamp-1 drop-shadow-sm">
                {{ getDisplayTitle(currentAd) }}
              </h3>
            </div>
            <p class="text-white/80 text-[11px] sm:text-[12px] line-clamp-1 drop-shadow-sm flex items-center gap-1">
              <span>点击立即了解详情</span>
              <span class="material-symbols-outlined text-[14px] inline-block transition-transform duration-200 group-hover:translate-x-0.5">open_in_new</span>
            </p>
          </div>
        </div>
      </transition>
    </div>

    <!-- 左右快速翻页控制 (多于 1 条广告时显示) -->
    <template v-if="ads.length > 1">
      <button 
        @click.stop="prevSlide" 
        class="absolute left-2 top-1/2 -translate-y-1/2 w-7 h-7 sm:w-8 sm:h-8 rounded-full bg-black/40 hover:bg-black/70 text-white flex items-center justify-center opacity-0 group-hover:opacity-100 transition-opacity duration-200 z-20"
        title="上一张"
      >
        <span class="material-symbols-outlined text-[18px]">chevron_left</span>
      </button>
      <button 
        @click.stop="nextSlide" 
        class="absolute right-9 top-1/2 -translate-y-1/2 w-7 h-7 sm:w-8 sm:h-8 rounded-full bg-black/40 hover:bg-black/70 text-white flex items-center justify-center opacity-0 group-hover:opacity-100 transition-opacity duration-200 z-20"
        title="下一张"
      >
        <span class="material-symbols-outlined text-[18px]">chevron_right</span>
      </button>

      <!-- 底部指示圆点 (Dots) -->
      <div class="absolute bottom-2 left-1/2 -translate-x-1/2 flex items-center gap-1.5 z-20">
        <button
          v-for="(ad, index) in ads"
          :key="ad.id"
          @click.stop="goToSlide(index)"
          :class="[
            'h-1.5 rounded-full transition-all duration-300',
            currentIndex === index ? 'w-5 bg-white shadow-sm' : 'w-1.5 bg-white/40 hover:bg-white/70'
          ]"
          :title="`切换到第 ${index + 1} 张广告`"
        />
      </div>
    </template>

    <!-- 右上角关闭/收起按钮 -->
    <button 
      @click.stop="closeBanner" 
      class="absolute top-2 right-2 w-6 h-6 rounded-full bg-black/30 hover:bg-black/60 text-white/80 hover:text-white flex items-center justify-center transition-all duration-200 z-20"
      title="暂时关闭此广告条"
    >
      <span class="material-symbols-outlined text-[14px]">close</span>
    </button>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue';
import { fetchTopBannerAds, type AdvertisementItem } from '../shared/adService';
import { shell } from '../shared/ipc';

const props = withDefaults(
  defineProps<{
    lang?: string;
    autoPlayInterval?: number;
  }>(),
  {
    lang: 'zh',
    autoPlayInterval: 5000,
  }
);

const isVisible = ref(true);
const ads = ref<AdvertisementItem[]>([]);
const currentIndex = ref(0);
let timer: ReturnType<typeof setInterval> | null = null;

const currentAd = computed<AdvertisementItem>(() => {
  if (ads.value.length === 0) {
    return {
      id: 0,
      title: '',
      title_en: '',
      image_url: '',
      target_url: '',
      redirect_url: '',
      position: '',
      sort: 0,
    };
  }
  return ads.value[currentIndex.value] || ads.value[0];
});

function getDisplayTitle(ad: AdvertisementItem): string {
  if (props.lang === 'en' && ad.title_en && ad.title_en.trim()) {
    return ad.title_en;
  }
  return ad.title || ad.title_en || '推荐推荐';
}

function nextSlide() {
  if (ads.value.length <= 1) return;
  currentIndex.value = (currentIndex.value + 1) % ads.value.length;
}

function prevSlide() {
  if (ads.value.length <= 1) return;
  currentIndex.value = (currentIndex.value - 1 + ads.value.length) % ads.value.length;
}

function goToSlide(index: number) {
  currentIndex.value = index;
}

function startTimer() {
  stopTimer();
  if (ads.value.length > 1) {
    timer = setInterval(nextSlide, props.autoPlayInterval);
  }
}

function stopTimer() {
  if (timer) {
    clearInterval(timer);
    timer = null;
  }
}

function handleMouseEnter() {
  stopTimer();
}

function handleMouseLeave() {
  startTimer();
}

function handleAdClick(ad: AdvertisementItem) {
  if (!ad) return;
  // 优先通过 redirect_url 访问 (可触发 ProxySubForClash 点击统计并 307 跳转)，若无则使用 target_url
  const destUrl = ad.redirect_url || ad.target_url;
  if (destUrl) {
    shell.openExternal(destUrl);
  }
}

function handleImageError(event: Event) {
  const target = event.target as HTMLImageElement;
  if (target) {
    // 图片加载失败时替换为内置的高雅渐变占位背景
    target.style.display = 'none';
  }
}

function closeBanner() {
  isVisible.value = false;
  stopTimer();
  try {
    sessionStorage.setItem('antigravity_top_ad_dismissed', 'true');
  } catch {
    // 忽略存储异常
  }
}

onMounted(async () => {
  // 检查是否在此会话中已被关闭
  try {
    if (sessionStorage.getItem('antigravity_top_ad_dismissed') === 'true') {
      isVisible.value = false;
      return;
    }
  } catch {
    // 忽略异常
  }

  try {
    const list = await fetchTopBannerAds();
    ads.value = list;
    if (list.length > 1) {
      startTimer();
    }
  } catch (err) {
    console.warn('[AdCarousel] Failed to load ads:', err);
  }
});

onUnmounted(() => {
  stopTimer();
});
</script>

<style scoped>
.fade-slide-enter-active,
.fade-slide-leave-active {
  transition: opacity 0.35s ease, transform 0.35s ease;
}

.fade-slide-enter-from {
  opacity: 0;
  transform: translateX(12px);
}

.fade-slide-leave-to {
  opacity: 0;
  transform: translateX(-12px);
}
</style>
