<template>
  <div class="devtools-color-picker flex flex-col bg-white dark:bg-[#161a27] border border-outline-variant/30 rounded-xl shadow-xl p-3 w-full max-w-[280px] select-none text-on-surface dark:text-white">
    <!-- 1. 2D 饱和度与明度拾色画板 (Saturation-Value Canvas) -->
    <div
      ref="svPanelRef"
      @mousedown="startSvDrag"
      class="relative w-full h-36 rounded-lg cursor-crosshair overflow-hidden shadow-inner border border-black/10"
      :style="{ backgroundColor: `hsl(${hsva.h}, 100%, 50%)` }"
    >
      <!-- 水平白色渐变层 (饱和度) -->
      <div class="absolute inset-0 bg-gradient-to-r from-white to-transparent"></div>
      <!-- 垂直黑色渐变层 (明度) -->
      <div class="absolute inset-0 bg-gradient-to-b from-transparent to-black"></div>

      <!-- 2D 拾色十字光标 -->
      <div
        class="absolute w-3.5 h-3.5 rounded-full border-2 border-white shadow-[0_0_2px_rgba(0,0,0,0.8)] -translate-x-1/2 -translate-y-1/2 pointer-events-none"
        :style="{
          left: `${hsva.s}%`,
          top: `${100 - hsva.v}%`,
          backgroundColor: currentRgbaString
        }"
      ></div>
    </div>

    <!-- 2. 中间控制行 (吸管 + 颜色大圆 + 色相/透明度滑块) -->
    <div class="flex items-center gap-2.5 mt-3">
      <!-- 屏幕吸管工具 -->
      <button
        v-if="hasEyeDropper"
        @click="pickScreenColor"
        class="w-7 h-7 rounded-lg bg-slate-100 dark:bg-white/10 hover:bg-slate-200 dark:hover:bg-white/15 flex items-center justify-center text-outline hover:text-primary transition-colors cursor-pointer shrink-0"
        title="屏幕吸色器 (Eyedropper)"
      >
        <span class="material-symbols-outlined text-[16px]">colorize</span>
      </button>

      <!-- 当前颜色预览大圆点 (支持棋盘格底层) -->
      <div class="relative w-7 h-7 rounded-full overflow-hidden border border-black/20 shrink-0 shadow-sm checkerboard-bg">
        <div class="absolute inset-0" :style="{ backgroundColor: currentRgbaString }"></div>
      </div>

      <!-- 滑块列 (Hue + Alpha) -->
      <div class="flex-1 flex flex-col gap-1.5 min-w-0">
        <!-- 色相彩虹滑块 (Hue) -->
        <input
          v-model.number="hsva.h"
          @input="emitColorChange"
          type="range"
          min="0"
          max="360"
          step="1"
          class="hue-slider w-full h-2.5 rounded-full appearance-none cursor-pointer"
        />

        <!-- 透明度棋盘滑块 (Alpha) -->
        <div class="relative w-full h-2.5 rounded-full overflow-hidden checkerboard-bg flex items-center">
          <input
            v-model.number="hsva.a"
            @input="emitColorChange"
            type="range"
            min="0"
            max="1"
            step="0.01"
            class="alpha-slider w-full h-full rounded-full appearance-none cursor-pointer absolute inset-0 z-10"
            :style="{
              background: `linear-gradient(to right, transparent, rgb(${rgbaValues.r}, ${rgbaValues.g}, ${rgbaValues.b}))`
            }"
          />
        </div>
      </div>
    </div>

    <!-- 3. 色值文本框与格式切换 (HEX / RGBA) -->
    <div class="flex items-center gap-1.5 mt-2.5 bg-slate-50 dark:bg-white/5 p-1 rounded-lg border border-outline-variant/40">
      <input
        v-model="colorInputText"
        @change="handleTextInputChange"
        type="text"
        class="flex-1 bg-transparent px-1.5 py-0.5 text-[11px] font-mono font-bold text-center focus:outline-none text-on-surface dark:text-white"
      />
      <button
        @click="toggleColorMode"
        class="px-1.5 py-0.5 rounded text-[9.5px] font-mono font-semibold bg-slate-200 dark:bg-white/10 text-outline hover:text-primary hover:bg-primary/10 transition-colors cursor-pointer shrink-0"
        title="切换 HEX / RGBA 格式"
      >
        {{ isHexMode ? 'HEX' : 'RGBA' }}
      </button>
    </div>

    <!-- 4. 快捷预设色卡网格 (Swatches Palette) -->
    <div class="mt-3 pt-2.5 border-t border-outline-variant/20 flex flex-col gap-1.5">
      <div class="flex items-center justify-between text-[10px] text-outline font-medium px-0.5">
        <span>快捷调色板</span>
        <span>{{ presetSwatches.length }} 色</span>
      </div>
      <div class="grid grid-cols-8 gap-1.5">
        <button
          v-for="swatch in presetSwatches"
          :key="swatch"
          @click="selectSwatch(swatch)"
          class="w-full aspect-square rounded-[4px] border border-black/15 hover:scale-110 transition-transform cursor-pointer shadow-xs"
          :style="{ backgroundColor: swatch }"
          :title="swatch"
        ></button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, watch, onMounted, onUnmounted } from 'vue';
import {
  parseColorToHsva,
  hsvaToRgba,
  hsvaToRgbaString,
  hsvaToHexString,
  HSVA
} from '../../composables/colorUtils';

const props = withDefaults(
  defineProps<{
    modelValue: string;
    showAlpha?: boolean;
  }>(),
  {
    modelValue: '#ffffff',
    showAlpha: true
  }
);

const emit = defineEmits<{
  (e: 'update:modelValue', val: string): void;
  (e: 'change', val: string): void;
}>();

const svPanelRef = ref<HTMLElement | null>(null);
const hsva = reactive<HSVA>({ h: 240, s: 60, v: 80, a: 1 });
const isHexMode = ref(false);
const colorInputText = ref('');
const hasEyeDropper = ref(false);

// 经典 16 色预设色盘
const presetSwatches = [
  '#ef4444', '#f97316', '#f59e0b', '#eab308',
  '#84cc16', '#22c55e', '#10b981', '#14b8a6',
  '#06b6d4', '#0ea5e9', '#3b82f6', '#6366f1',
  '#8b5cf6', '#a855f7', '#ec4899', '#ffffff'
];

const rgbaValues = computed(() => hsvaToRgba(hsva.h, hsva.s, hsva.v, hsva.a));
const currentRgbaString = computed(() => hsvaToRgbaString(hsva.h, hsva.s, hsva.v, hsva.a));

// 同步外部 modelValue 到内部 HSVA
watch(
  () => props.modelValue,
  (newVal) => {
    if (!newVal) return;
    const parsed = parseColorToHsva(newVal);
    // 只有发生实质变动时更新，防止循环更新丢精度
    if (
      Math.abs(parsed.h - hsva.h) > 0.5 ||
      Math.abs(parsed.s - hsva.s) > 0.5 ||
      Math.abs(parsed.v - hsva.v) > 0.5 ||
      Math.abs(parsed.a - hsva.a) > 0.02
    ) {
      Object.assign(hsva, parsed);
    }
    isHexMode.value = newVal.startsWith('#');
    updateInputText();
  },
  { immediate: true }
);

function updateInputText() {
  if (isHexMode.value) {
    colorInputText.value = hsvaToHexString(hsva.h, hsva.s, hsva.v, hsva.a, hsva.a < 0.99);
  } else {
    colorInputText.value = hsvaToRgbaString(hsva.h, hsva.s, hsva.v, hsva.a);
  }
}

function emitColorChange() {
  updateInputText();
  const output = isHexMode.value
    ? hsvaToHexString(hsva.h, hsva.s, hsva.v, hsva.a, hsva.a < 0.99)
    : hsvaToRgbaString(hsva.h, hsva.s, hsva.v, hsva.a);
  emit('update:modelValue', output);
  emit('change', output);
}

function toggleColorMode() {
  isHexMode.value = !isHexMode.value;
  emitColorChange();
}

function handleTextInputChange() {
  if (!colorInputText.value) return;
  const parsed = parseColorToHsva(colorInputText.value);
  Object.assign(hsva, parsed);
  emitColorChange();
}

function selectSwatch(swatch: string) {
  const parsed = parseColorToHsva(swatch);
  Object.assign(hsva, parsed);
  emitColorChange();
}

// 2D 拾色器鼠标拖拽处理
let isDraggingSv = false;

function updateSvFromPointer(e: MouseEvent) {
  if (!svPanelRef.value) return;
  const rect = svPanelRef.value.getBoundingClientRect();
  const x = Math.max(0, Math.min(rect.width, e.clientX - rect.left));
  const y = Math.max(0, Math.min(rect.height, e.clientY - rect.top));

  hsva.s = Math.round((x / rect.width) * 100);
  hsva.v = Math.round((1 - y / rect.height) * 100);
  emitColorChange();
}

function startSvDrag(e: MouseEvent) {
  isDraggingSv = true;
  updateSvFromPointer(e);
  window.addEventListener('mousemove', onSvMouseMove);
  window.addEventListener('mouseup', onSvMouseUp);
}

function onSvMouseMove(e: MouseEvent) {
  if (!isDraggingSv) return;
  updateSvFromPointer(e);
}

function onSvMouseUp() {
  isDraggingSv = false;
  window.removeEventListener('mousemove', onSvMouseMove);
  window.removeEventListener('mouseup', onSvMouseUp);
}

// 原生屏幕吸管
async function pickScreenColor() {
  if ((window as any).EyeDropper) {
    try {
      const eyeDropper = new (window as any).EyeDropper();
      const result = await eyeDropper.open();
      if (result && result.sRGBHex) {
        selectSwatch(result.sRGBHex);
      }
    } catch (e) {
      // 用户取消吸管
    }
  }
}

onMounted(() => {
  hasEyeDropper.value = typeof (window as any).EyeDropper !== 'undefined';
});

onUnmounted(() => {
  window.removeEventListener('mousemove', onSvMouseMove);
  window.removeEventListener('mouseup', onSvMouseUp);
});
</script>

<style scoped>
.checkerboard-bg {
  background-image: linear-gradient(45deg, #ccc 25%, transparent 25%),
    linear-gradient(-45deg, #ccc 25%, transparent 25%),
    linear-gradient(45deg, transparent 75%, #ccc 75%),
    linear-gradient(-45deg, transparent 75%, #ccc 75%);
  background-size: 8px 8px;
  background-position: 0 0, 0 4px, 4px -4px, -4px 0px;
}

.hue-slider {
  background: linear-gradient(
    to right,
    #ff0000 0%,
    #ffff00 17%,
    #00ff00 33%,
    #00ffff 50%,
    #0000ff 67%,
    #ff00ff 83%,
    #ff0000 100%
  );
}

.hue-slider::-webkit-slider-thumb,
.alpha-slider::-webkit-slider-thumb {
  -webkit-appearance: none;
  appearance: none;
  width: 14px;
  height: 14px;
  border-radius: 50%;
  background: #ffffff;
  border: 2px solid rgba(0, 0, 0, 0.4);
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.4);
  cursor: pointer;
}

.hue-slider::-moz-range-thumb,
.alpha-slider::-moz-range-thumb {
  width: 14px;
  height: 14px;
  border-radius: 50%;
  background: #ffffff;
  border: 2px solid rgba(0, 0, 0, 0.4);
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.4);
  cursor: pointer;
}
</style>
