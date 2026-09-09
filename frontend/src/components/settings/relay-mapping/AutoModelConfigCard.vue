<template>
  <div class="bg-white dark:bg-[#1e2538] rounded-xl border border-outline-variant/20 p-5 flex flex-col gap-4 shadow-sm">
    <!-- 卡片头部: 标题 + 启用开关 -->
    <div class="flex items-center justify-between border-b border-outline-variant/15 pb-3">
      <div class="flex items-center gap-2.5">
        <span class="flex items-center justify-center w-8 h-8 rounded-lg bg-amber-500/10 text-amber-500 dark:text-amber-400">
          <span class="material-symbols-outlined text-[20px]">bolt</span>
        </span>
        <div>
          <div class="flex items-center gap-2">
            <h3 class="text-[14px] font-bold text-on-surface dark:text-white">Auto 并发竞速模型配置</h3>
            <span class="px-1.5 py-0.2 rounded text-[10px] font-bold bg-amber-500/15 text-amber-600 dark:text-amber-400 border border-amber-500/30">
              跨号池并发
            </span>
          </div>
          <p class="text-[11px] text-outline/80 mt-0.5">
            客户端调用 <code class="px-1 py-0.5 rounded bg-slate-100 dark:bg-white/10 font-mono text-[11px] text-primary">auto</code> 模型时，多候选模型同时发送请求，最先响应（首字）的模型胜出并直接流式回传，败方立即中断。
          </p>
        </div>
      </div>

      <div class="flex items-center gap-2">
        <span class="text-[12px] font-medium text-slate-600 dark:text-slate-300">
          {{ isExposed ? '已启用并暴露' : '已关闭' }}
        </span>
        <label class="relative inline-block w-9 h-5 cursor-pointer">
          <input
            type="checkbox"
            class="sr-only peer"
            :checked="isExposed"
            @change="onToggleExpose"
          />
          <div class="w-9 h-5 bg-slate-300 dark:bg-slate-600 rounded-full peer-checked:bg-primary transition-colors"></div>
          <div class="absolute left-0.5 top-0.5 w-4 h-4 bg-white rounded-full transition-transform peer-checked:translate-x-4 shadow-sm"></div>
        </label>
      </div>
    </div>

    <!-- 模式选择区: 自定义模型选择 vs 竞速模型池 (控制台测速池) -->
    <div class="flex flex-col gap-2">
      <label class="text-[12px] font-bold text-on-surface dark:text-white flex items-center gap-1.5">
        <span class="material-symbols-outlined text-[16px] text-primary">tune</span>
        <span>候选模型源类型：</span>
      </label>

      <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
        <!-- 选项 1: 自定义模型选择 -->
        <div
          class="flex items-start gap-2.5 p-3 rounded-lg border transition-all cursor-pointer select-none"
          :class="poolMode === 'custom'
            ? 'border-primary bg-primary/5 ring-1 ring-primary/30'
            : 'border-outline-variant/30 hover:border-outline-variant/60 bg-slate-50/50 dark:bg-white/5'"
          @click="setMode('custom')"
        >
          <input
            type="radio"
            name="autoPoolMode"
            class="mt-0.5 text-primary focus:ring-primary"
            :checked="poolMode === 'custom'"
            @change="setMode('custom')"
          />
          <div>
            <div class="text-[12px] font-bold text-on-surface dark:text-white flex items-center gap-1">
              <span>自定义模型选择</span>
            </div>
            <div class="text-[11px] text-outline mt-0.5">
              自由挑选、多选或手动输入需要并发竞速的指定模型。
            </div>
          </div>
        </div>

        <!-- 选项 2: 竞速模型池 (控制台测速池) -->
        <div
          class="flex items-start gap-2.5 p-3 rounded-lg border transition-all cursor-pointer select-none"
          :class="poolMode === 'benchmark'
            ? 'border-primary bg-primary/5 ring-1 ring-primary/30'
            : 'border-outline-variant/30 hover:border-outline-variant/60 bg-slate-50/50 dark:bg-white/5'"
          @click="setMode('benchmark')"
        >
          <input
            type="radio"
            name="autoPoolMode"
            class="mt-0.5 text-primary focus:ring-primary"
            :checked="poolMode === 'benchmark'"
            @change="setMode('benchmark')"
          />
          <div class="flex-1 min-w-0">
            <div class="text-[12px] font-bold text-on-surface dark:text-white flex items-center gap-1">
              <span>竞速模型池 (控制台测速池)</span>
              <span class="px-1.5 py-0.2 rounded text-[10px] bg-primary/10 text-primary font-mono">
                {{ benchmarkModels.length }} 个模型
              </span>
            </div>
            <div class="text-[11px] text-outline mt-0.5">
              自动实时同步控制台「模型响应测速」配置的模型池，免手动维护。
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- 动态配置交互区 -->
    <div class="mt-1">
      <!-- 1. 自定义模型选择模式: 显示模型选择器 -->
      <div v-if="poolMode === 'custom'" class="flex flex-col gap-1.5 p-3.5 bg-slate-50 dark:bg-white/5 rounded-lg border border-outline-variant/20">
        <div class="flex items-center justify-between">
          <span class="text-[12px] font-bold text-on-surface dark:text-white flex items-center gap-1">
            <span class="material-symbols-outlined text-[15px] text-primary">playlist_add_check</span>
            <span>已选候选模型 ({{ candidateList.length }} 个)：</span>
          </span>
          <span class="text-[11px] text-outline">可跨任意号池选择或键盘输入自定义模型</span>
        </div>
        <ModelSearchSelect
          :multiple="true"
          :model-ids="candidateList"
          :options="availableModelOptions"
          :allow-custom="true"
          placeholder="搜索并多选模型，或键盘输入自定义模型按回车添加..."
          @update:model-ids="onCandidateModelsChange"
          class="w-full"
        />
      </div>

      <!-- 2. 竞速模型池模式: 自动回显控制台测速池模型 -->
      <div v-else class="flex flex-col gap-2 p-3.5 bg-slate-50 dark:bg-white/5 rounded-lg border border-outline-variant/20">
        <div class="flex items-center justify-between">
          <div class="flex items-center gap-1.5 text-[12px] font-bold text-on-surface dark:text-white">
            <span class="material-symbols-outlined text-[16px] text-amber-500 animate-pulse">speed</span>
            <span>控制台测速池回显模型清单：</span>
          </div>
          <button
            type="button"
            class="text-[11px] text-primary hover:underline flex items-center gap-0.5 cursor-pointer"
            @click="fetchBenchmarkModels"
          >
            <span class="material-symbols-outlined text-[13px]">refresh</span>
            <span>刷新测速池</span>
          </button>
        </div>

        <div v-if="benchmarkModels.length > 0" class="flex flex-wrap gap-1.5 py-1 max-h-36 overflow-y-auto">
          <span
            v-for="m in benchmarkModels"
            :key="m"
            class="inline-flex items-center gap-1 px-2 py-1 rounded bg-amber-500/10 dark:bg-amber-500/20 text-amber-700 dark:text-amber-300 text-[11px] font-mono border border-amber-500/25"
          >
            <span class="material-symbols-outlined text-[13px]">model_training</span>
            <span>{{ m }}</span>
          </span>
        </div>
        <div v-else class="text-[12px] text-outline italic py-2">
          控制台测速池当前暂未添加模型，请前往控制台「模型响应测速」卡片配置测速模型。
        </div>

        <div class="text-[11px] text-outline/80 flex items-center gap-1">
          <span class="material-symbols-outlined text-[13px]">info</span>
          <span>每次请求将直接发往上方回显的全部模型进行首字竞速，控制台测速池增减模型将自动热生效。</span>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue';
import { ipcRenderer } from '../../../shared/ipc';
import ModelSearchSelect from '../agent-config/ModelSearchSelect.vue';
import type { ModelMappingEntry } from './types';

const props = defineProps<{
  allMappings: ModelMappingEntry[];
  availableModelOptions: string[];
}>();

const emit = defineEmits<{
  'update:autoMapping': [entry: ModelMappingEntry];
}>();

// 内部持有当前 auto 映射条目
const autoEntry = computed(() => {
  return props.allMappings.find(m => m.clientModel?.trim().toLowerCase() === 'auto');
});

const isExposed = computed(() => {
  return autoEntry.value ? autoEntry.value.expose : true;
});

// 模式: 'custom' 自定义模型选择 | 'benchmark' 竞速模型池 (控制台测速池)
const poolMode = ref<'custom' | 'benchmark'>('custom');
const benchmarkModels = ref<string[]>([]);
const candidateList = ref<string[]>([]);

function syncFromEntry() {
  if (!autoEntry.value) {
    poolMode.value = 'custom';
    candidateList.value = [];
    return;
  }
  if (autoEntry.value.useBenchmarkPool === true) {
    poolMode.value = 'benchmark';
  } else {
    poolMode.value = 'custom';
  }
  candidateList.value = Array.isArray(autoEntry.value.candidateModels)
    ? [...autoEntry.value.candidateModels]
    : [];
}

function notifyUpdate(patch: Partial<ModelMappingEntry>) {
  let base: ModelMappingEntry;
  if (autoEntry.value) {
    base = { ...autoEntry.value };
  } else {
    base = {
      clientModel: 'auto',
      targetModel: 'auto',
      targetProvider: 'google',
      expose: true,
      candidateModels: [],
      useBenchmarkPool: false,
    };
  }
  const updated = { ...base, ...patch };
  emit('update:autoMapping', updated);
}

function onToggleExpose(e: Event) {
  const checked = (e.target as HTMLInputElement).checked;
  notifyUpdate({ expose: checked });
}

function setMode(mode: 'custom' | 'benchmark') {
  poolMode.value = mode;
  if (mode === 'benchmark') {
    notifyUpdate({
      useBenchmarkPool: true,
      targetModel: 'benchmark-pool',
    });
    fetchBenchmarkModels();
  } else {
    notifyUpdate({
      useBenchmarkPool: false,
      targetModel: candidateList.value.length > 0 ? candidateList.value.join(', ') : 'auto',
    });
  }
}

function onCandidateModelsChange(newModels: string[]) {
  candidateList.value = newModels;
  notifyUpdate({
    candidateModels: newModels,
    targetModel: newModels.length > 0 ? newModels.join(', ') : 'auto',
  });
}

async function fetchBenchmarkModels() {
  try {
    const res: any = await ipcRenderer.invoke('benchmark:get');
    if (res && res.config && Array.isArray(res.config.models)) {
      benchmarkModels.value = res.config.models;
    }
  } catch (err) {
    console.warn('[AutoModelConfigCard] fetchBenchmarkModels failed:', err);
  }
}

onMounted(() => {
  syncFromEntry();
  fetchBenchmarkModels();
});
</script>
