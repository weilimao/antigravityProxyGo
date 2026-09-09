<template>
  <div class="flex flex-col gap-6 w-full">
    <!-- 顶部: Auto 竞速模型专属独立配置项卡片 -->
    <AutoModelConfigCard
      :all-mappings="mm.allMappings.value"
      :available-model-options="mm.allKnownModelOptions.value"
      @update:auto-mapping="mm.updateAutoMapping"
    />

    <!-- 常规中继模型映射与号池绑定卡片 -->
    <div class="bg-white dark:bg-[#1e2538] rounded-xl border border-outline-variant/20 p-5">
      <div class="flex items-center justify-between mb-4">
        <h3 class="text-[14px] font-bold text-on-surface dark:text-white flex items-center gap-2">
          <span class="material-symbols-outlined text-[18px] text-primary">alt_route</span>
          <span>自定义中继模型映射与号池绑定</span>
        </h3>
        <div class="flex items-center gap-2">
          <button
            class="flex items-center gap-1 px-3 py-1 text-[12px] font-medium bg-primary/10 text-primary hover:bg-primary/20 rounded-lg transition-colors cursor-pointer"
            @click="mm.addTab()"
          >
            <span class="material-symbols-outlined text-[16px]">add_box</span>
            <span>新增号池 Tab</span>
          </button>
        </div>
      </div>

      <div class="flex items-center gap-2 border-b border-outline-variant/20 pb-2 mb-4 overflow-x-auto">
        <button
          v-for="tab in mm.poolTabs.value"
          :key="tab.id"
          class="px-3 py-1.5 rounded-lg text-[12px] font-bold transition-all cursor-pointer whitespace-nowrap flex items-center gap-1.5"
          :class="tab.id === mm.activeTabId.value
            ? 'bg-primary text-white shadow-sm shadow-primary/30'
            : 'bg-slate-100 dark:bg-white/5 text-slate-600 dark:text-slate-300 hover:bg-slate-200 dark:hover:bg-white/10'"
          @click="mm.selectTab(tab.id)"
        >
          <span>{{ tab.name }}</span>
          <span
            v-if="tab.isCustom"
            class="material-symbols-outlined text-[14px] hover:text-red-400 ml-1 transition-colors"
            title="删除此 Tab"
            @click.stop="mm.deleteTab(tab.id)"
          >close</span>
        </button>
      </div>

      <div class="bg-slate-50 dark:bg-white/5 p-3 rounded-lg border border-outline-variant/15 flex flex-wrap items-center justify-between gap-3 mb-4">
        <div class="flex items-center gap-3 flex-wrap">
          <div class="relative w-56">
            <span class="material-symbols-outlined absolute left-2.5 top-1/2 -translate-y-1/2 text-outline/70 text-[16px] pointer-events-none">search</span>
            <input
              type="text"
              v-model="mm.searchQuery.value"
              class="w-full pl-8 pr-7 py-1 text-[12px] bg-white dark:bg-[#1e2538] border border-outline-variant/30 rounded-lg focus:border-primary focus:ring-2 focus:ring-primary/15 focus:outline-none transition-all placeholder:text-outline/50 text-on-surface dark:text-white"
              placeholder="搜索模型映射..."
            />
            <button
              v-if="mm.searchQuery.value"
              class="absolute right-2 top-1/2 -translate-y-1/2 text-outline hover:text-on-surface dark:hover:text-white p-0.5 rounded-full hover:bg-slate-200 dark:hover:bg-white/10 transition-colors cursor-pointer"
              title="清空搜索"
              @click="mm.searchQuery.value = ''"
            >
              <span class="material-symbols-outlined text-[13px] block">close</span>
            </button>
          </div>

          <span class="text-[12px] font-bold text-on-surface dark:text-white flex items-center gap-1">
            <span class="material-symbols-outlined text-[16px] text-primary">hub</span>
            <span>路由目标账号池 (Target Provider):</span>
          </span>
          <select
            class="px-2 py-1 text-[12px] font-mono rounded border border-outline-variant/30 bg-white dark:bg-[#1e2538] text-on-surface dark:text-white focus:outline-none focus:border-primary"
            :value="currentProviderValue"
            @change="onProviderSelectChange"
          >
            <option v-for="p in providerOptions" :key="p" :value="p">{{ p }}</option>
            <option value="__custom__">+ 自定义 Provider...</option>
          </select>
          <input
            v-if="showCustomProviderInput"
            type="text"
            v-model="customProviderInput"
            @input="onCustomProviderInput"
            class="px-2 py-1 text-[12px] font-mono rounded border border-outline-variant/30 bg-white dark:bg-[#1e2538] text-on-surface dark:text-white w-32"
            placeholder="自定义号池ID"
          />

          <template v-if="!mm.isOtherTab.value">
            <button
              class="flex items-center gap-1 px-2.5 py-1 text-[12px] font-medium bg-primary/10 text-primary hover:bg-primary/20 rounded-lg transition-colors cursor-pointer border border-primary/20 disabled:opacity-50 disabled:cursor-not-allowed"
              :disabled="mm.fetching.value"
              @click="mm.fetchChannelModels()"
            >
              <span class="material-symbols-outlined text-[15px]" :class="{ 'animate-spin': mm.fetching.value }">sync</span>
              <span>{{ mm.fetching.value ? '获取中...' : '获取号池模型' }}</span>
            </button>
          </template>

          <template v-else>
            <button
              v-for="g in mm.otherGroups.value"
              :key="g.groupId"
              class="flex items-center gap-1 px-2.5 py-1 text-[12px] font-medium bg-purple-500/10 text-purple-600 dark:text-purple-300 hover:bg-purple-500/20 rounded-lg transition-colors cursor-pointer border border-purple-500/20 disabled:opacity-50 disabled:cursor-not-allowed"
              :disabled="mm.fetching.value"
              @click="mm.fetchOtherGroupModels(g.groupId, g.groupName || g.groupId)"
            >
              <span class="material-symbols-outlined text-[15px]" :class="{ 'animate-spin': mm.fetching.value }">sync</span>
              <span>获取 {{ g.groupName || g.groupId }}{{ g.formats && g.formats.length ? ` (${g.formats.map(f => f === 'anthropic' ? 'A' : 'O').join('/')})` : '' }}</span>
            </button>
            <span v-if="mm.otherGroups.value.length === 0" class="text-[11px] text-outline italic">Other 号池暂无组,请先在账号池添加 Other 账号创建组</span>
          </template>

          <span v-if="mm.fetchStatusMsg.value" class="text-[11px] text-primary font-medium">{{ mm.fetchStatusMsg.value }}</span>

          <button
            class="flex items-center gap-1 px-2.5 py-1 text-[12px] font-medium bg-red-500/10 text-red-500 hover:bg-red-500/20 rounded-lg transition-colors cursor-pointer border border-red-500/20 disabled:opacity-40 disabled:cursor-not-allowed"
            :disabled="mm.staleCount.value <= 0"
            @click="mm.clearStaleMappings()"
            :title="mm.staleCount.value > 0 ? '' : (mm.hasFetchedStaleBasis(mm.activeTabId.value) ? '当前号池无失效模型映射' : '请先成功「获取号池模型」以核对远端全集')"
          >
            <span class="material-symbols-outlined text-[15px]">cleaning_services</span>
            <span>清除失效模型{{ mm.staleCount.value > 0 ? ` (${mm.staleCount.value})` : '' }}</span>
          </button>
        </div>
        <div class="flex items-center gap-2">
          <button
            v-if="mm.currentTab.value?.isCustom"
            class="text-red-500 hover:text-red-700 text-[12px] font-medium flex items-center gap-1 transition-colors cursor-pointer"
            @click="mm.deleteCurrentTab()"
          >
            <span class="material-symbols-outlined text-[15px]">delete</span>
            <span>删除当前 Tab</span>
          </button>
          <button
            class="flex items-center gap-1 text-[12px] font-medium text-primary hover:text-primary/80 transition-colors cursor-pointer px-2 py-1 rounded bg-primary/10"
            @click="mm.addModelMapping()"
          >
            <span class="material-symbols-outlined text-[16px]">add</span>
            <span>添加映射模型</span>
          </button>
        </div>
      </div>

      <div class="overflow-x-auto max-h-[360px] overflow-y-auto pr-1">
        <table class="w-full text-left text-[12px]">
          <thead>
            <tr class="border-b border-outline-variant/25 text-outline/80">
              <th class="py-2.5 font-bold pl-2">客户端请求模型 (Client Model)</th>
              <th class="py-2.5 font-bold pl-2">真实目标模型 (Target Model)</th>
              <th v-if="mm.isNvidiaTab.value" class="py-2.5 font-bold text-center w-[160px]">注入 Template Kwargs</th>
              <th class="py-2.5 font-bold text-center w-[140px]">多模态</th>
              <th class="py-2.5 font-bold text-center w-[120px]">是否公开 (Expose)</th>
              <th class="py-2.5 font-bold text-center w-[80px]">操作</th>
            </tr>
          </thead>
          <tbody>
            <template v-if="mm.filteredMappings.value.length === 0">
              <tr class="border-b border-outline-variant/10 text-outline/60 text-center">
                <td :colspan="mm.isNvidiaTab.value ? 6 : 5" class="py-8 text-[12px]">
                  <template v-if="mm.currentTabMappings.value.length === 0">暂无模型映射，点击上方「添加映射模型」或「获取号池模型」</template>
                  <template v-else>未找到匹配「<span class="text-primary font-bold">{{ mm.searchQuery.value }}</span>」的模型映射</template>
                </td>
              </tr>
            </template>
            <ModelMappingRow
              v-for="item in mm.pagedMappings.value"
              :key="item._rowKey"
              :item="item"
              :row-models="mm.getRowModels(item)"
              :show-inject-kwargs="mm.isNvidiaTab.value"
              :is-stale="mm.isStaleItem(item)"
              :is-new="mm.isNewItem(item)"
              @update:client-model="(val) => item.clientModel = val"
              @update:target-model="(val) => mm.onTargetModelChange(item, val)"
              @update:expose="(val) => item.expose = val"
              @update:inject-kwargs="(val) => item.injectChatTemplateKwargs = val"
              @update:multimodal="(val) => item.multimodal = val"
              @delete="mm.deleteMapping(item)"
            />
          </tbody>
        </table>
      </div>

      <!-- 分页:映射条目多时避免整表平铺渲染卡顿,每页仅渲染 pageSize 行 -->
      <div class="flex items-center justify-between border-t border-outline-variant/20 pt-3 mt-4">
        <span class="text-[11px] text-outline">
          共 {{ mm.filteredMappings.value.length }} 条映射{{ mm.searchQuery.value ? ` (筛选自 ${mm.currentTabMappings.value.length} 条)` : '' }} · 第 {{ mm.currentPage.value }} / {{ mm.totalPages.value }} 页
        </span>
        <div class="flex items-center gap-1">
          <button
            class="px-2.5 py-1 text-[11px] font-medium border border-outline-variant/30 rounded-md hover:bg-slate-50 dark:hover:bg-white/5 text-on-surface dark:text-white disabled:opacity-50 disabled:pointer-events-none flex items-center gap-0.5 cursor-pointer"
            :disabled="mm.currentPage.value <= 1"
            @click="mm.gotoPage(mm.currentPage.value - 1)"
          >
            <span class="material-symbols-outlined text-[14px]">chevron_left</span>
            <span>上一页</span>
          </button>
          <span class="text-[11px] px-2 text-on-surface dark:text-white font-bold">{{ mm.currentPage.value }}</span>
          <button
            class="px-2.5 py-1 text-[11px] font-medium border border-outline-variant/30 rounded-md hover:bg-slate-50 dark:hover:bg-white/5 text-on-surface dark:text-white disabled:opacity-50 disabled:pointer-events-none flex items-center gap-0.5 cursor-pointer"
            :disabled="mm.currentPage.value >= mm.totalPages.value"
            @click="mm.gotoPage(mm.currentPage.value + 1)"
          >
            <span>下一页</span>
            <span class="material-symbols-outlined text-[14px]">chevron_right</span>
          </button>
        </div>
      </div>

      <div class="flex justify-end gap-3 mt-5 border-t border-outline-variant/20 pt-4">
        <button
          class="px-4 py-1.5 text-[12px] font-bold bg-primary text-white rounded-lg hover:bg-primary/90 transition-all duration-200 cursor-pointer shadow-md shadow-primary/20 flex items-center gap-1 disabled:opacity-50"
          :disabled="mm.saving.value"
          @click="mm.saveModelMappings()"
        >
          <span v-if="mm.saving.value" class="material-symbols-outlined text-[16px] animate-spin">sync</span>
          <span v-else-if="mm.saveStatus.value === 'success'" class="material-symbols-outlined text-[16px]">done</span>
          <span v-else-if="mm.saveStatus.value === 'error'" class="material-symbols-outlined text-[16px]">error</span>
          <span v-else class="material-symbols-outlined text-[16px]">save</span>
          <span>{{ mm.saving.value ? '保存中...' : mm.saveStatus.value === 'success' ? '保存成功' : mm.saveStatus.value === 'error' ? '保存失败' : '保存全部映射与号池配置' }}</span>
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue';
import { useModelMapping } from '../../../ui/useModelMapping';
import AutoModelConfigCard from './AutoModelConfigCard.vue';
import ModelMappingRow from './ModelMappingRow.vue';

const mm = useModelMapping();

const customProviderInput = ref('');
const showCustomProviderInput = ref(false);

const currentProviderValue = computed(() => {
  const tab = mm.currentTab.value;
  if (!tab) return '';
  return tab.targetProvider;
});

const providerOptions = computed(() => {
  const tab = mm.currentTab.value;
  if (!tab) return [];
  return Array.from(new Set([
    'google', 'nvidia', 'gcp', 'antigravity', 'deepseek', 'qwen', 'anthropic', 'moonshot',
    ...mm.availableChannels.value, tab.targetProvider,
  ].filter(Boolean)));
});

function onProviderSelectChange(e: Event) {
  const val = (e.target as HTMLSelectElement).value;
  if (val === '__custom__') {
    showCustomProviderInput.value = true;
    customProviderInput.value = mm.currentTab.value?.targetProvider || '';
  } else {
    showCustomProviderInput.value = false;
    mm.updateTabProvider(val);
  }
}

function onCustomProviderInput() {
  const val = customProviderInput.value.trim();
  mm.updateTabProvider(val);
}

watch(currentProviderValue, (newVal) => {
  const providers = providerOptions.value;
  if (providers.includes(newVal)) {
    showCustomProviderInput.value = false;
  } else {
    showCustomProviderInput.value = true;
    customProviderInput.value = newVal;
  }
}, { immediate: true });

onMounted(() => {
  mm.loadModelMappings();
});
</script>
