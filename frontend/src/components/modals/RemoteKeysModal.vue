<template>
  <div>
    <!-- 一级弹窗：管理 API Keys -->
    <BaseModal
      id="remoteKeysModal"
      closeBtnId="btnRemoteKeysClose"
      hideType="hidden"
      zIndex="z-[9999]"
      title="管理 API Keys"
      titleI18n="manageApiKeys"
      icon="key"
      maxWidth="w-[64rem] max-w-[97vw]"
      maxHeight="max-h-[80vh]"
      bodyClass="p-6 flex flex-col overflow-hidden"
    >
      <div class="flex gap-2 mb-4 flex-shrink-0">
        <input class="flex-1 px-3 py-2 text-[13px] rounded-lg border border-outline-variant/30 bg-white dark:bg-[#1a1f30] text-on-surface dark:text-white focus:border-primary focus:ring-1 focus:ring-primary/30 outline-none" id="remoteNewKeyName" placeholder="新 Key 备注名称..." data-i18n-placeholder="newKeyPlaceholder" type="text" />
        <button class="px-4 py-2 text-[12px] font-medium text-white bg-primary hover:bg-primary/90 rounded-lg transition-colors flex items-center gap-1 shadow-sm whitespace-nowrap cursor-pointer" id="btnRemoteCreateKey">
          <span class="material-symbols-outlined text-[16px]">add</span>
          <span data-i18n="btnCreateKey">创建 Key</span>
        </button>
      </div>
      <div class="flex-1 overflow-auto pr-1 min-h-[200px]">
        <table class="w-full text-left text-[12px]">
          <thead class="sticky top-0 bg-white dark:bg-[#1e2538] z-10">
            <tr class="border-b border-outline-variant/25 text-outline/80">
              <th class="py-2.5 font-bold pl-2 w-[110px] whitespace-nowrap" data-i18n="colName">名称</th>
              <th class="py-2.5 font-bold w-[160px] whitespace-nowrap" data-i18n="colApiKey">API Key (脱敏展示)</th>
              <th class="py-2.5 font-bold w-[130px] whitespace-nowrap" data-i18n="colGeminiUsage">通用模型 (已用/限额)</th>
              <th class="py-2.5 font-bold w-[130px] whitespace-nowrap" data-i18n="colClaudeUsage">Claude (已用/限额)</th>
              <th class="py-2.5 font-bold w-[150px] whitespace-nowrap" data-i18n="colAllowedModels">授权模型</th>
              <th class="py-2.5 font-bold text-center w-[90px] whitespace-nowrap" data-i18n="colAction">操作</th>
            </tr>
          </thead>
          <tbody id="remoteKeysTableBody">
            <!-- 动态渲染 -->
          </tbody>
        </table>
      </div>
    </BaseModal>

    <!-- 二级弹窗：设置限额 -->
    <BaseModal
      id="remoteKeyQuotaModal"
      closeBtnId="btnRemoteQuotaClose"
      hideType="hidden"
      zIndex="z-[99999]"
      icon="settings"
      maxWidth="w-[30rem] max-w-[92vw]"
      bodyClass="p-5 flex flex-col"
    >
      <template #header-title>
        <h3 class="text-[14px] font-bold text-on-surface dark:text-white" id="remoteQuotaEditTitle" data-i18n="modifyKeyQuota">修改 Key 限额</h3>
      </template>

      <input type="hidden" id="remoteQuotaEditId" />

      <div class="mb-4">
        <label class="block text-[11px] font-medium text-outline mb-1" data-i18n="geminiQuotaLabel">Gemini 限额 Token 数</label>
        <input class="w-full px-3 py-2 text-[12px] rounded-lg border border-outline-variant/30 bg-transparent text-on-surface dark:text-white focus:outline-none focus:border-primary focus:ring-1 focus:ring-primary/30" id="remoteQuotaEditGemini" placeholder="例如: 500k, 1m 或输入 0/留空不限制" data-i18n-placeholder="quotaPlaceholder" type="text" />
      </div>

      <div class="mb-4">
        <label class="block text-[11px] font-medium text-outline mb-1" data-i18n="claudeQuotaLabel">Claude 限额 Token 数</label>
        <input class="w-full px-3 py-2 text-[12px] rounded-lg border border-outline-variant/30 bg-transparent text-on-surface dark:text-white focus:outline-none focus:border-primary focus:ring-1 focus:ring-primary/30" id="remoteQuotaEditClaude" placeholder="例如: 500k, 1m 或输入 0/留空不限制" data-i18n-placeholder="quotaPlaceholder" type="text" />
      </div>

      <div class="mb-4">
        <label class="block text-[11px] font-medium text-outline mb-1" data-i18n="allowedModelsLabel">授权模型</label>
        <ModelSearchSelect
          multiple
          :model-ids="allowedModels"
          @update:model-ids="allowedModels = $event"
          :options="modelOptions"
          placeholder="点击选择模型, 留空=不限制"
        />
        <p class="text-[10px] text-outline/60 mt-1" data-i18n="allowedModelsHint">留空(不添加任何模型) = 不限制, 可调用全部模型</p>
      </div>

      <template #footer>
        <button class="px-4 py-2 text-[11px] font-medium text-outline hover:text-on-surface border border-outline-variant/30 rounded-lg transition-colors cursor-pointer" id="btnRemoteQuotaCancel" onclick="window._relayCloseModal('remoteKeyQuotaModal')">
          <span data-i18n="btnCancel">取消</span>
        </button>
        <button class="px-4 py-2 text-[11px] font-medium text-white bg-primary hover:bg-primary/90 rounded-lg transition-colors flex items-center gap-1 shadow-sm cursor-pointer" id="btnRemoteQuotaSave">
          <span data-i18n="btnSave">保存</span>
        </button>
      </template>
    </BaseModal>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue';
import BaseModal from './BaseModal.vue';
import ModelSearchSelect from '../settings/agent-config/ModelSearchSelect.vue';
import { ipcRenderer } from '../../shared/ipc';

// 授权模型(精确匹配白名单): 空=不限制(全部), 非空=仅列表内模型可调用。
const allowedModels = ref<string[]>([]);
// 可选模型清单(中继暴露的 Expose 模型), 供下拉选择。
const modelOptions = ref<string[]>([]);

function setAllowedModels(models: string[]) {
    allowedModels.value = Array.isArray(models) ? models.slice() : [];
}
function getAllowedModels(): string[] {
    return allowedModels.value.slice();
}
async function loadModelOptions() {
    try {
        const res = await ipcRenderer.invoke('remote:get-key-models');
        if (res?.success && Array.isArray(res.models)) {
            modelOptions.value = res.models;
        }
    } catch (e) {
        console.error('loadModelOptions error:', e);
    }
}

// 暴露给 remoteController 命令式调用(编辑按钮回填 / 保存读取 / 弹窗打开时加载清单)。
onMounted(() => {
    (window as any)._remoteKeyAllowedModelsSet = setAllowedModels;
    (window as any)._remoteKeyAllowedModelsGet = getAllowedModels;
    (window as any)._remoteKeyAllowedModelsLoad = loadModelOptions;
});
</script>

