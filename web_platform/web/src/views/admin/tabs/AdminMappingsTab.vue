<template>
  <div class="flex flex-col gap-6">
    <div class="flex flex-col sm:flex-row items-start sm:items-center justify-between gap-4">
      <div>
        <h3 class="text-base font-bold text-white flex items-center gap-2">
          <span>中继模型映射与多号池路由配置</span>
          <span class="px-1.5 py-0.5 rounded text-[10px] font-bold bg-indigo-500/15 text-indigo-400 border border-indigo-500/30">对标桌面端核心</span>
        </h3>
        <p class="text-xs text-slate-400 mt-1">
          配置入站客户端请求模型名（ClientModel）到上游真实模型名（TargetModel）的转译映射、号池分流与原生多模态控制
        </p>
      </div>

      <div class="flex items-center gap-3">
        <button
          type="button"
          :disabled="fetching"
          class="btn-secondary text-xs flex items-center gap-1.5"
          @click="loadModelMappings"
          title="从远端 18444 服务端网关拉取最新模型映射配置"
        >
          <span class="material-symbols-outlined text-16px" :class="{ 'animate-spin': fetching }">cloud_download</span>
          <span>{{ fetching ? '同步中...' : '从服务端同步' }}</span>
        </button>
        <button type="button" class="btn-secondary text-xs" @click="addModelMapping">
          <span class="material-symbols-outlined text-16px">add</span>
          <span>添加映射行</span>
        </button>
        <button type="button" :disabled="saving" class="btn-primary text-xs" @click="saveModelMappings">
          <span class="material-symbols-outlined text-16px" :class="{ 'animate-spin': saving }">save</span>
          <span>{{ saving ? '保存中...' : '保存并下发网关' }}</span>
        </button>
      </div>
    </div>

    <!-- Auto 模型配置卡片 -->
    <AutoModelConfigCard
      :all-mappings="allMappings"
      :available-model-options="allKnownModelOptions"
      @update:auto-mapping="updateAutoMapping"
    />

    <div class="flex flex-col bg-slate-900/50 rounded-xl border border-slate-800 shadow-sm overflow-hidden">
      <!-- 动态号池 Tabs 栏 -->
      <div class="flex items-center gap-1 p-2 bg-slate-800/30 overflow-x-auto select-none border-b border-slate-800">
        <button
          v-for="tab in poolTabs"
          :key="tab.id"
          class="shrink-0 px-3 py-1.5 text-[12px] font-bold rounded-lg transition-all flex items-center gap-1.5 border border-transparent"
          :class="activeTabId === tab.id
            ? 'bg-indigo-500 text-white shadow-sm border-indigo-400/30'
            : 'text-slate-400 hover:bg-white/5 hover:text-slate-200'"
          @click="selectTab(tab.id)"
        >
          <span>{{ tab.name }}</span>
          <button
            v-if="tab.isCustom"
            @click.stop="deleteTab(tab.id)"
            class="ml-1 w-4 h-4 flex items-center justify-center rounded-full hover:bg-black/20 text-white/70 hover:text-white"
            title="删除该自定义号池 Tab"
          >
            <span class="material-symbols-outlined text-[12px]">close</span>
          </button>
        </button>
        
        <button
          class="shrink-0 ml-1 px-2.5 py-1.5 text-[12px] font-bold rounded-lg text-slate-400 hover:bg-white/5 hover:text-slate-200 transition-colors flex items-center gap-1"
          @click="addTab"
          title="添加自定义号池 Tab (仅作前端分组用)"
        >
          <span class="material-symbols-outlined text-[14px]">add</span>
          <span>自定义组</span>
        </button>
      </div>

      <!-- Tab 工具栏 -->
      <div class="p-3 bg-slate-800/10 border-b border-slate-800 flex flex-col sm:flex-row sm:items-center justify-between gap-3">
        <div class="flex items-center gap-2">
          <!-- 获取号池模型 (Google/NVIDIA) -->
          <button
            v-if="!isOtherTab"
            class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[12px] font-bold transition-all border"
            :class="fetching
              ? 'bg-slate-800 text-slate-500 border-slate-700 cursor-not-allowed'
              : 'bg-indigo-500/10 hover:bg-indigo-500/20 text-indigo-400 border-indigo-500/20'"
            @click="fetchChannelModels"
            :disabled="fetching"
          >
            <span class="material-symbols-outlined text-[16px]" :class="{ 'animate-spin': fetching }">sync</span>
            <span>获取{{ currentTab?.name || '号池' }}模型</span>
          </button>

          <span v-if="fetchStatusMsg" class="text-[11px] text-slate-400 font-medium ml-1">
            {{ fetchStatusMsg }}
          </span>

          <div v-if="staleCount > 0" class="w-px h-4 bg-slate-700 mx-1"></div>
          
          <button
            v-if="staleCount > 0"
            class="flex items-center gap-1 px-2.5 py-1.5 rounded text-[11px] font-medium transition-colors bg-rose-500/10 hover:bg-rose-500/20 text-rose-400 border border-rose-500/20"
            @click="clearStaleMappings"
          >
            <span class="material-symbols-outlined text-[14px]">cleaning_services</span>
            <span>清除 {{ staleCount }} 个已删除模型</span>
          </button>
        </div>

        <div class="flex items-center gap-2">
          <!-- 搜索框 -->
          <div class="relative w-full sm:w-64">
            <span class="material-symbols-outlined absolute left-2.5 top-1/2 -translate-y-1/2 text-slate-500 text-[16px]">search</span>
            <input
              v-model="searchQuery"
              type="text"
              placeholder="搜索当前 Tab 映射..."
              class="input-dark text-[12px] pl-8 w-full py-1.5"
            />
          </div>
          
          <!-- 分页器 -->
          <div class="flex items-center gap-1 text-[12px] text-slate-400 bg-slate-800/50 rounded-lg border border-slate-700 px-1.5">
            <button @click="gotoPage(currentPage - 1)" :disabled="currentPage === 1" class="p-1 hover:text-white disabled:opacity-30">
              <span class="material-symbols-outlined text-[16px]">chevron_left</span>
            </button>
            <span class="font-mono min-w-[3rem] text-center">{{ currentPage }} / {{ totalPages }}</span>
            <button @click="gotoPage(currentPage + 1)" :disabled="currentPage >= totalPages" class="p-1 hover:text-white disabled:opacity-30">
              <span class="material-symbols-outlined text-[16px]">chevron_right</span>
            </button>
          </div>
        </div>
      </div>

      <!-- Other 号池专属：合格渠道最新模型【请求按钮】与视图切换栏 -->
      <div v-if="isOtherTab" class="px-4 py-2.5 bg-slate-800/40 border-b border-slate-800 flex items-center gap-2 flex-wrap select-none">
        <div class="flex items-center gap-1.5 text-[12px] font-bold text-purple-400 mr-2 shrink-0">
          <span class="material-symbols-outlined text-[18px]">cloud_download</span>
          <span>获取渠道最新模型:</span>
        </div>

        <!-- 各合格渠道的请求按钮 -->
        <button
          v-for="sg in otherSubGroups"
          :key="sg.groupId"
          type="button"
          class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[12px] font-semibold transition-all border cursor-pointer shadow-sm disabled:opacity-50 disabled:cursor-not-allowed"
          :class="fetchingGroupId === sg.groupId
            ? 'bg-purple-600 text-white border-purple-400 shadow-purple-500/25 shadow-md'
            : (selectedOtherSubGroup === sg.groupId
                ? 'bg-purple-500/20 text-purple-300 border-purple-500/50 hover:bg-purple-500/30'
                : 'bg-slate-800/90 text-slate-300 border-slate-700 hover:text-white hover:border-purple-500/40 hover:bg-slate-700/80')"
          :disabled="fetching"
          @click="fetchOtherGroupModels(sg.groupId, sg.groupName || sg.groupId)"
          :title="`向 ${sg.groupName || sg.groupId} 上游 API 发送请求，拉取该渠道最新可用模型并自动注入映射`"
        >
          <span class="material-symbols-outlined text-[15px]" :class="{ 'animate-spin': fetchingGroupId === sg.groupId }">sync</span>
          <span>获取 {{ sg.groupName || sg.groupId }}{{ sg.formats && sg.formats.length ? ` (${sg.formats.map((f: any) => f === 'anthropic' ? 'A' : 'O').join('/')})` : '' }}</span>
          <span class="text-[10px] px-1.5 py-0.2 rounded bg-purple-500/25 text-purple-300 font-mono">({{ sg.count }})</span>
        </button>

        <span v-if="otherSubGroups.length === 0" class="text-[11px] text-slate-500 italic">
          Other 号池暂无渠道配置，请先在桌面端账号池添加 Other 账号创建组
        </span>

        <!-- 右侧：查看全部视图 -->
        <div class="ml-auto flex items-center gap-2">
          <span class="text-[11px] text-slate-400 flex items-center gap-1">
            <span class="material-symbols-outlined text-[14px]">tune</span>
            <span>视图:</span>
          </span>
          <button
            type="button"
            class="px-2.5 py-1 rounded-md text-[11px] font-medium transition-all border cursor-pointer"
            :class="selectedOtherSubGroup === 'all'
              ? 'bg-purple-600 text-white border-purple-400 shadow-sm'
              : 'bg-slate-800 text-slate-400 border-slate-700 hover:text-white hover:border-slate-600'"
            @click="selectOtherSubGroup('all')"
          >
            全部显示 ({{ totalOtherMappingsCount }})
          </button>
        </div>
      </div>

      <!-- 表格内容 -->
      <div class="overflow-x-auto min-h-[400px]">
        <table class="w-full text-left border-collapse min-w-[800px]">
          <thead class="bg-slate-800/80 sticky top-0 z-10 shadow-sm border-b border-slate-700">
            <tr>
              <th class="py-2.5 px-3 text-[11px] font-semibold text-slate-400 w-[28%]">入站客户端请求模型 (Client Model)</th>
              <th class="py-2.5 px-3 text-[11px] font-semibold text-slate-400 w-[30%]">路由目标上游模型 (Target Model)</th>
              <th v-if="currentTab?.targetProvider !== 'other'" class="py-2.5 px-3 text-[11px] font-semibold text-slate-400 text-center w-[8%]">注入参数</th>
              <th class="py-2.5 px-3 text-[11px] font-semibold text-slate-400 text-center w-[12%]">多模态模式</th>
              <th class="py-2.5 px-3 text-[11px] font-semibold text-slate-400 text-center w-[8%]">对外暴露</th>
              <th class="py-2.5 px-3 text-[11px] font-semibold text-slate-400 text-right w-[8%]">操作</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-slate-800/50">
            <tr v-if="pagedMappings.length === 0">
              <td colspan="6" class="text-center py-12 text-slate-500 text-[12px]">
                <div class="flex flex-col items-center justify-center gap-2">
                  <span class="material-symbols-outlined text-[32px] opacity-20">data_array</span>
                  <span>当前分组暂无任何模型映射记录</span>
                </div>
              </td>
            </tr>
            <ModelMappingRow
              v-for="item in pagedMappings"
              :key="item._rowKey"
              :item="item"
              :row-models="getRowModels(item)"
              :show-inject-kwargs="currentTab?.targetProvider !== 'other'"
              :is-stale="isStaleItem(item)"
              :is-new="isNewItem(item)"
              @update:client-model="val => { item.clientModel = val; item.expose = true; }"
              @update:target-model="val => onTargetModelChange(item, val)"
              @update:expose="val => item.expose = val"
              @update:inject-kwargs="val => item.injectChatTemplateKwargs = val"
              @update:multimodal="val => item.multimodal = val"
              @delete="deleteMapping(item)"
            />
          </tbody>
        </table>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted } from 'vue';
import { useModelMapping } from '../../../composables/useModelMapping';
import AutoModelConfigCard from './AutoModelConfigCard.vue';
import ModelMappingRow from './ModelMappingRow.vue';

const {
  allMappings,
  allKnownModelOptions,
  updateAutoMapping,
  poolTabs,
  activeTabId,
  searchQuery,
  saving,
  fetching,
  fetchStatusMsg,
  currentTab,
  isOtherTab,
  currentPage,
  totalPages,
  pagedMappings,
  gotoPage,
  staleCount,
  loadModelMappings,
  fetchChannelModels,
  fetchOtherGroupModels,
  selectTab,
  addTab,
  deleteTab,
  addModelMapping,
  deleteMapping,
  onTargetModelChange,
  saveModelMappings,
  clearStaleMappings,
  getRowModels,
  isStaleItem,
  isNewItem,
  otherGroups,
  otherSubGroups,
  fetchingGroupId,
  totalOtherMappingsCount,
  selectedOtherSubGroup,
  selectOtherSubGroup,
} = useModelMapping();

onMounted(() => {
  loadModelMappings();
});
</script>
