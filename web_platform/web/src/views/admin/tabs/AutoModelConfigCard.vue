<template>
  <div class="bg-slate-900/50 rounded-xl border border-slate-800 p-5 flex flex-col gap-4 shadow-sm">
    <div class="flex items-center justify-between border-b border-slate-800 pb-3">
      <div class="flex items-center gap-2.5">
        <span class="flex items-center justify-center w-8 h-8 rounded-lg bg-amber-500/10 text-amber-400">
          <span class="material-symbols-outlined text-[20px]">bolt</span>
        </span>
        <div>
          <div class="flex items-center gap-2 flex-wrap">
            <h3 class="text-[14px] font-bold text-white">Auto 并发竞速模型配置</h3>
            <span class="px-1.5 py-0.5 rounded text-[10px] font-bold bg-amber-500/15 text-amber-400 border border-amber-500/30">
              跨号池并发
            </span>
          </div>
          <p class="text-[11px] text-slate-400 mt-1">
            客户端调用 <code class="px-1 py-0.5 rounded bg-white/10 font-mono text-indigo-400">auto</code> 模型时，多候选模型同时发送请求，最先响应（首字）的模型胜出并直接流式回传，败方立即中断。
          </p>
        </div>
      </div>
      
      <div class="flex items-center gap-3">
        <div class="flex items-center gap-1.5 pl-2 border-l border-slate-800">
          <span class="text-[12px] font-medium text-slate-300">
            {{ isExposed ? '已启用' : '已关闭' }}
          </span>
          <label class="relative inline-block w-9 h-5 cursor-pointer">
            <input
              type="checkbox"
              class="sr-only peer"
              :checked="isExposed"
              @change="onToggleExpose"
            />
            <div class="w-9 h-5 bg-slate-700 rounded-full peer-checked:bg-indigo-500 transition-colors"></div>
            <div class="absolute left-0.5 top-0.5 w-4 h-4 bg-white rounded-full transition-transform peer-checked:translate-x-4 shadow-sm"></div>
          </label>
        </div>
      </div>
    </div>

    <div class="flex flex-col gap-2">
      <label class="text-[12px] font-bold text-white flex items-center gap-1.5">
        <span class="material-symbols-outlined text-[16px] text-indigo-400">tune</span>
        <span>候选模型源类型：</span>
      </label>

      <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
        <div
          class="flex items-start gap-2.5 p-3 rounded-lg border transition-all cursor-pointer select-none"
          :class="poolMode === 'custom'
            ? 'border-indigo-500 bg-indigo-500/10 ring-1 ring-indigo-500/30'
            : 'border-slate-800 hover:border-slate-700 bg-white/5'"
          @click="setMode('custom')"
        >
          <input
            type="radio"
            class="mt-0.5 accent-indigo-500 cursor-pointer"
            :checked="poolMode === 'custom'"
            @change="setMode('custom')"
          />
          <div>
            <div class="text-[12px] font-bold text-white flex items-center gap-1">
              <span>自定义模型选择</span>
            </div>
            <div class="text-[11px] text-slate-400 mt-0.5">
              自由挑选、多选或手动输入需要并发竞速的指定模型。
            </div>
          </div>
        </div>

        <div
          class="flex items-start gap-2.5 p-3 rounded-lg border transition-all cursor-pointer select-none"
          :class="poolMode === 'benchmark'
            ? 'border-indigo-500 bg-indigo-500/10 ring-1 ring-indigo-500/30'
            : 'border-slate-800 hover:border-slate-700 bg-white/5'"
          @click="setMode('benchmark')"
        >
          <input
            type="radio"
            class="mt-0.5 accent-indigo-500 cursor-pointer"
            :checked="poolMode === 'benchmark'"
            @change="setMode('benchmark')"
          />
          <div class="flex-1 min-w-0">
            <div class="text-[12px] font-bold text-white flex items-center gap-1">
              <span>竞速模型池 (控制台测速池)</span>
            </div>
            <div class="text-[11px] text-slate-400 mt-0.5">
              自动实时同步网关「模型响应测速」配置的模型池，免手动维护。
            </div>
          </div>
        </div>
      </div>
    </div>

    <div class="mt-1">
      <div v-if="poolMode === 'custom'" class="flex flex-col gap-1.5 p-3.5 bg-white/5 rounded-lg border border-slate-800">
        <div class="flex items-center justify-between">
          <span class="text-[12px] font-bold text-white flex items-center gap-1">
            <span class="material-symbols-outlined text-[15px] text-indigo-400">playlist_add_check</span>
            <span>已选候选模型 ({{ candidateList.length }} 个)：</span>
          </span>
        </div>
        <ModelSearchSelect
          :multiple="true"
          :model-ids="candidateList"
          :options="availableModelOptions"
          :allow-custom="true"
          placeholder="搜索并多选候选模型，或键盘输入自定义模型按回车添加..."
          @update:model-ids="val => { candidateList = val; onCandidateModelsChange(); }"
        />
      </div>
      <div v-else class="flex flex-col gap-2 p-3.5 bg-white/5 rounded-lg border border-slate-800">
        <div class="text-[11px] text-slate-400 flex items-center gap-1">
          <span class="material-symbols-outlined text-[13px]">info</span>
          <span>每次请求将直接发往网关当前生效的全部测速池模型进行首字竞速。</span>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue';
import type { ModelMappingEntry } from '../../../composables/mappingTypes';
import ModelSearchSelect from '../../../components/common/ModelSearchSelect.vue';

const props = defineProps<{
  allMappings: ModelMappingEntry[];
  availableModelOptions: string[];
}>();

const emit = defineEmits<{
  'update:autoMapping': [entry: ModelMappingEntry];
}>();

const autoEntry = computed(() => {
  return props.allMappings.find(m => m.clientModel?.trim().toLowerCase() === 'auto');
});

const isExposed = computed(() => {
  return autoEntry.value ? autoEntry.value.expose : true;
});

const poolMode = ref<'custom' | 'benchmark'>('custom');
const candidateList = ref<string[]>([]);

const options = computed(() => props.availableModelOptions.map(m => ({ value: m, label: m })));

function syncFromEntry() {
  if (!autoEntry.value) {
    poolMode.value = 'custom';
    candidateList.value = [];
    return;
  }
  if ((autoEntry.value as any).useBenchmarkPool === true) {
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
      targetProvider: 'auto',
      expose: true,
      ownedBy: 'google',
      candidateModels: [],
    };
  }
  emit('update:autoMapping', { ...base, ...patch });
}

function onToggleExpose(e: Event) {
  const checked = (e.target as HTMLInputElement).checked;
  notifyUpdate({ expose: checked });
}

function setMode(m: 'custom' | 'benchmark') {
  poolMode.value = m;
  if (m === 'benchmark') {
    notifyUpdate({ candidateModels: [], useBenchmarkPool: true } as any);
  } else {
    notifyUpdate({ candidateModels: candidateList.value, useBenchmarkPool: false } as any);
  }
}

function onCandidateModelsChange() {
  if (poolMode.value === 'custom') {
    notifyUpdate({ candidateModels: candidateList.value, useBenchmarkPool: false } as any);
  }
}

watch(() => props.allMappings, () => {
  syncFromEntry();
}, { deep: true });

onMounted(() => {
  syncFromEntry();
});
</script>


