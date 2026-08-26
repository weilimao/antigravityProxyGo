<template>
  <div class="hidden flex-col gap-4 w-full" id="settings-panel-agentconfig">
    <!-- Agent Tab 栏 -->
    <div class="glass-card rounded-xl p-4 flex flex-col gap-3">
      <div class="flex items-center gap-2 border-b border-outline-variant/20 pb-3">
        <span class="material-symbols-outlined text-primary text-[20px]">tune</span>
        <div>
          <span class="text-[15px] font-bold text-on-surface dark:text-white">Agent 配置管理</span>
          <p class="text-[11px] text-outline">可视化编辑外部 Agent 配置文件，左侧表单与右侧 JSON 双向同步</p>
        </div>
      </div>
      <div v-if="agentList.length > 0" class="flex gap-1 bg-slate-100 dark:bg-white/5 p-1 rounded-lg text-[12px] overflow-x-auto">
        <button
          v-for="agent in agentList"
          :key="agent.id"
          @click="onAgentSelect(agent.id)"
          :class="selectedAgentId === agent.id
            ? 'bg-white dark:bg-[#1a1f30] text-primary dark:text-primary-fixed-dim rounded-md shadow-sm font-bold cursor-pointer transition-all duration-200'
            : 'text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200 rounded-md font-medium cursor-pointer transition-all duration-200'"
          class="px-4 py-1.5 text-[12px] whitespace-nowrap flex items-center gap-1.5"
        >
          {{ agent.displayName }}
          <span v-if="!agent.fileExists" class="text-[10px] text-warning">(!)</span>
        </button>
      </div>
      <div v-else class="text-[12px] text-outline py-2">未检测到已注册的 Agent 配置。</div>
    </div>

    <!-- Body: left form + right JSON -->
    <template v-if="selectedAgentId && currentSchema">
      <div class="flex gap-4" style="height: calc(100vh - 440px);">
        <!-- Left: Form -->
        <div class="flex flex-col gap-3 overflow-hidden" style="width: 50%; min-height: 0;">
          <div class="flex items-center justify-between flex-shrink-0">
            <span class="text-[13px] font-bold text-on-surface dark:text-white">可视化配置</span>
            <div class="flex items-center gap-3">
              <button
                v-if="selectedAgentId === 'opencode'"
                class="text-[11px] text-primary hover:text-primary/80 font-medium flex items-center gap-1 cursor-pointer px-2 py-1 rounded-md hover:bg-primary/10 transition-colors"
                @click="showAiGenModal = true"
                title="用 AI 一键生成 provider 配置"
              >
                <span class="material-symbols-outlined text-[14px]">auto_awesome</span>
                AI 生成 Provider
              </button>
              <button
                class="text-[11px] text-primary hover:text-primary/80 font-medium flex items-center gap-1 cursor-pointer"
                @click="syncFormFromJSON"
              >
                <span class="material-symbols-outlined text-[14px]">sync</span>
                从 JSON 同步
              </button>
            </div>
          </div>
          <ConfigFormPanel
            :schema="currentSchema"
            v-model:formData="formData"
            :availableModels="availableModels"
            @update:formData="onFormChange"
            @refresh-models="onRefreshModels"
          />
        </div>

        <!-- Right: JSON Editor -->
        <div class="flex flex-col gap-3 overflow-hidden" style="width: 50%; min-height: 0;">
          <JsonEditorPanel
            v-model="jsonText"
            @parse-error="onParseError"
          />
        </div>
      </div>

      <!-- Footer: Actions (fixed bottom) -->
      <div class="fixed bottom-0 left-0 right-0 z-20 glass-card rounded-t-xl p-3 mx-4 flex items-center justify-between gap-3" style="margin-bottom: 36px;">
        <div class="flex items-center gap-2">
          <span v-if="saveStatus === 'success'" class="text-[12px] text-success font-medium flex items-center gap-1">
            <span class="material-symbols-outlined text-[14px]">check_circle</span>
            保存成功
          </span>
          <span v-else-if="saveStatus === 'error'" class="text-[12px] text-error font-medium flex items-center gap-1">
            <span class="material-symbols-outlined text-[14px]">error</span>
            {{ saveError }}
          </span>
          <span v-else-if="saveStatus === 'saving'" class="text-[12px] text-primary font-medium flex items-center gap-1">
            <span class="material-symbols-outlined text-[14px] animate-spin">progress_activity</span>
            保存中...
          </span>
          <span v-else class="text-[12px] text-outline">
            配置文件: {{ activeConfigPath }}
          </span>
        </div>
        <div class="flex items-center gap-2">
          <button
            class="px-3 py-2 text-[12px] bg-slate-100 dark:bg-white/5 hover:bg-slate-200 dark:hover:bg-white/10 text-on-surface dark:text-white border border-outline-variant/30 rounded-md font-medium flex items-center gap-1.5 cursor-pointer transition-colors"
            @click="onReload"
          >
            <span class="material-symbols-outlined text-[14px]">refresh</span>
            重载文件
          </button>
          <button
            class="px-3 py-2 text-[12px] bg-slate-100 dark:bg-white/5 hover:bg-slate-200 dark:hover:bg-white/10 text-on-surface dark:text-white border border-outline-variant/30 rounded-md font-medium flex items-center gap-1.5 cursor-pointer transition-colors"
            @click="onRestoreBackup"
          >
            <span class="material-symbols-outlined text-[14px]">restore</span>
            恢复备份
          </button>
          <button
            class="px-4 py-2 text-[12px] bg-primary hover:bg-primary/90 text-white rounded-md font-bold flex items-center gap-1.5 cursor-pointer transition-colors shadow-sm"
            :disabled="hasParseError || saveStatus === 'saving'"
            :class="{ 'opacity-50 cursor-not-allowed': hasParseError || saveStatus === 'saving' }"
            @click="onSave"
          >
            <span class="material-symbols-outlined text-[14px]">save</span>
            保存配置
          </button>
        </div>
      </div>
    </template>

    <!-- Empty state -->
    <div v-else class="glass-card rounded-xl p-10 flex flex-col items-center justify-center gap-3 text-center" style="min-height: 400px;">
      <span class="material-symbols-outlined text-outline text-[48px]">tune</span>
      <span class="text-[13px] text-outline">请选择上方的 Agent Tab 开始编辑配置</span>
    </div>

    <!-- OpenCode Provider AI 生成弹窗 (仅 opencode 选中时挂载) -->
    <AiProviderGeneratorModal
      v-if="selectedAgentId === 'opencode'"
      :visible="showAiGenModal"
      :available-models="availableModels"
      @close="showAiGenModal = false"
      @refresh-models="onRefreshModels"
      @apply="onAiProviderGenerated"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue';
import { ipcRenderer } from '../../../shared/ipc';
import { AgentProfile, AgentSchema } from '../agent-config/types';
import { getSchema } from '../agent-config/schemas';
import {
  initAgentConfig,
  setConfigLoadCallback,
  setAgentsLoadCallback,
  selectAgent,
  saveConfig,
  reloadConfig,
  restoreBackup,
  fetchRelayModels,
  fetchAgentModelCatalog,
  fetchAgentCatalogFull,
  saveAgentCatalogFull,
  formToJSON,
  jsonToForm,
} from '../../../ui/agentConfigController';
import ConfigFormPanel from '../agent-config/ConfigFormPanel.vue';
import JsonEditorPanel from '../agent-config/JsonEditorPanel.vue';
import AiProviderGeneratorModal from '../../modals/AiProviderGeneratorModal.vue';

const agentList = ref<AgentProfile[]>([]);
const selectedAgentId = ref('');
const activeConfigPath = ref('');
const currentSchema = computed<AgentSchema | null>(() => {
  if (!selectedAgentId.value) return null;
  return getSchema(selectedAgentId.value);
});
const jsonText = ref('{}');
const formData = ref<Record<string, any>>({});
const hasParseError = ref(false);
const saveStatus = ref<'idle' | 'saving' | 'success' | 'error'>('idle');
const saveError = ref('');
const availableModels = ref<string[]>([]);
const showAiGenModal = ref(false);
let relayModelCache: string[] = [];
let catalogModelCache: string[] = [];

onMounted(() => {
  setAgentsLoadCallback((agents: AgentProfile[]) => {
    agentList.value = agents || [];
    // 若当前未选中任何 Agent 且列表非空，默认自动选中第一个 Agent
    if (!selectedAgentId.value && agentList.value.length > 0) {
      onAgentSelect(agentList.value[0].id);
    }
  });
  setConfigLoadCallback((loadedJson: string) => {
    try {
      const parsed = JSON.parse(loadedJson);
      jsonText.value = JSON.stringify(parsed, null, 2);
    } catch {
      jsonText.value = loadedJson;
    }
    syncFormFromJSON();
    refreshAvailableModels();
    // 若为 Codex，异步拉取 catalog JSON 合并到 jsonText 供可视化「模型列表」section 编辑
    if (selectedAgentId.value === 'codex') {
      mergeCatalogIntoJson();
    }
  });
  initAgentConfig();
  fetchRelayModels().then((models) => {
    relayModelCache = [...models];
    availableModels.value = models;
    refreshAvailableModels();
  });
});

// refreshAvailableModels: 合并中继模型映射列表 + Agent catalog 模型 + JSON 配置内 provider.*.models 的模型名
// 确保用户在 JSON 中手动添加的模型也能在 model-select 下拉中回显选中
function refreshAvailableModels() {
  const jsonModelNames: string[] = [];
  try {
    const parsed = JSON.parse(jsonText.value);
    if (parsed && parsed.provider && typeof parsed.provider === 'object') {
      for (const provName of Object.keys(parsed.provider)) {
        const prov = parsed.provider[provName];
        if (prov && prov.models && typeof prov.models === 'object') {
          for (const modelName of Object.keys(prov.models)) {
            const fullId = `${provName}/${modelName}`;
            jsonModelNames.push(fullId);
          }
        }
      }
    }
  } catch { /* JSON 解析失败时忽略，仅用中继模型列表 */ }
  // 合并去重：中继模型 + Agent catalog 模型 + JSON 已配置的 provider/model
  const merged = Array.from(new Set([...relayModelCache, ...catalogModelCache, ...jsonModelNames]));
  availableModels.value = merged.sort((a, b) => a.localeCompare(b));
}

function onAgentSelect(agentId: string) {
  selectedAgentId.value = agentId;
  const agent = agentList.value.find((a) => a.id === agentId);
  if (agent) {
    activeConfigPath.value = agent.configPath;
  }
  jsonText.value = '{}';
  formData.value = {};
  catalogModelCache = [];
  selectAgent(agentId);
  refreshAvailableModels();
  // 异步拉取该 Agent 的 catalog 模型列表（对 Codex 生效，其他 Agent 返回空数组）
  fetchAgentModelCatalog(agentId).then((catalogModels) => {
    catalogModelCache = catalogModels;
    refreshAvailableModels();
  });
}

function onFormChange(data: Record<string, any>) {
  formData.value = data;
  if (currentSchema.value) {
    const newJson = formToJSON(data, currentSchema.value);
    try {
      const existing = JSON.parse(jsonText.value);
      const generated = JSON.parse(newJson);
      const merged = deepMerge(existing, generated);
      jsonText.value = JSON.stringify(merged, null, 2);
    } catch {
      jsonText.value = newJson;
    }
  }
  refreshAvailableModels();
}

// onRefreshModels: 模型下拉打开时，重新拉取最新中继映射并合并至 availableModels。
// 用户在中继面板新增映射后切回 Agent 配置面板，第一次点下拉即可搜到新模型。
async function onRefreshModels() {
  try {
    const models = await fetchRelayModels();
    relayModelCache = [...models];
    refreshAvailableModels();
  } catch { /* 静默失败，保留当前缓存 */ }
}

function syncFormFromJSON() {
  if (currentSchema.value) {
    formData.value = jsonToForm(jsonText.value, currentSchema.value);
  }
}

// onAiProviderGenerated: AI 生成的 provider JSON 片段(形态 { providerName: {...} })
// merge 进当前 jsonText 的 provider 对象下, 同名 provider 覆盖、新增的追加,
// 然后刷新表单与可用模型列表。
function onAiProviderGenerated(content: string) {
  try {
    const generated = JSON.parse(content); // { providerName: {...} }
    const existing = JSON.parse(jsonText.value);
    if (!existing.provider || typeof existing.provider !== 'object' || Array.isArray(existing.provider)) {
      existing.provider = {};
    }
    // 逐个 merge: 同名覆盖,新增追加。同名校验避免静默覆盖已配置 provider。
    const newNames = Object.keys(generated);
    const overwritten: string[] = [];
    for (const name of newNames) {
      if (existing.provider[name] !== undefined) overwritten.push(name);
      existing.provider[name] = generated[name];
    }
    jsonText.value = JSON.stringify(existing, null, 2);
    syncFormFromJSON();
    refreshAvailableModels();
    const msg = overwritten.length
      ? `已应用 ${newNames.length} 个 provider(${overwritten.join(', ')} 为覆盖)`
      : `已新增 ${newNames.length} 个 provider`;
    showToast(msg, 'success');
  } catch (err: any) {
    showToast('应用 AI 生成结果失败: ' + (err?.message || String(err)), 'error');
  }
}

// mergeCatalogIntoJson: 拉取独立 catalog JSON 文件，把其中的 models 数组转为以 slug 做 key 的 object，
// 合并进当前 jsonText（仅用于前端可视化编辑，不写回 config.toml）。
// 保存时由 extractCatalogFromJson 反向拆出并单独写回 catalog 文件。
function mergeCatalogIntoJson() {
  if (!selectedAgentId.value) return;
  fetchAgentCatalogFull(selectedAgentId.value).then((catalogJson) => {
    try {
      const catalog = JSON.parse(catalogJson);
      const modelsArr = Array.isArray(catalog.models) ? catalog.models : [];
      const modelsObj: Record<string, any> = {};
      for (const m of modelsArr) {
        if (m && typeof m === 'object' && typeof m.slug === 'string' && m.slug) {
          modelsObj[m.slug] = { ...m };
          // 不在 object 里再保留 slug 字段，slug 作为 key 已存在；但 schema 里也读 models.{name}.slug
          // 所以这里需要保留 slug 字段，让 FieldRenderer 回填。
          modelsObj[m.slug].slug = m.slug;
        }
      }
      const existing = JSON.parse(jsonText.value);
      if (Object.keys(modelsObj).length > 0) {
        existing.models = modelsObj;
      } else {
        delete existing.models;
      }
      jsonText.value = JSON.stringify(existing, null, 2);
      syncFormFromJSON();
      refreshAvailableModels();
    } catch { /* catalog JSON 解析失败时忽略 */ }
  });
}

// extractCatalogFromJson: 从 jsonText 中抽取 models object 并转回数组，
// 返回 { configJson: 去掉 models 的 JSON 字符串, catalogJson: 含 models 数组的 JSON 字符串 }。
// 若没有 models 字段，catalogJson 保持原始空数组结构返回 ''（调用方据此决定是否写 catalog）。
function extractCatalogFromJson(): { configJson: string; catalogJson: string | null } {
  try {
    const parsed = JSON.parse(jsonText.value);
    let catalogJson: string | null = null;
    if (parsed && typeof parsed.models === 'object' && !Array.isArray(parsed.models)) {
      const modelsArr: any[] = [];
      for (const slug of Object.keys(parsed.models)) {
        const entry = parsed.models[slug];
        if (entry && typeof entry === 'object') {
          const item: any = { ...entry };
          if (!item.slug) item.slug = slug;
          modelsArr.push(item);
        }
      }
      if (modelsArr.length > 0) {
        catalogJson = JSON.stringify({ models: modelsArr }, null, 2);
      }
      delete parsed.models;
    }
    return { configJson: JSON.stringify(parsed, null, 2), catalogJson };
  } catch {
    return { configJson: jsonText.value, catalogJson: null };
  }
}

function onParseError(error: string | null) {
  hasParseError.value = error !== null;
}

async function onSave() {
  if (!selectedAgentId.value) return;
  saveStatus.value = 'saving';
  saveError.value = '';

  // 若为 Codex，先拆分 models 节点：catalog 部分单独写，剩余写 config.toml
  if (selectedAgentId.value === 'codex') {
    const { configJson, catalogJson } = extractCatalogFromJson();
    // 1) 写 catalog 文件（有内容才写）
    if (catalogJson) {
      const catResult = await saveAgentCatalogFull(selectedAgentId.value, catalogJson);
      if (!catResult.success) {
        saveStatus.value = 'error';
        saveError.value = 'Catalog: ' + (catResult.error || '保存失败');
        showToast('Catalog 保存失败: ' + saveError.value, 'error');
        return;
      }
    }
    // 2) 写主配置文件
    const result = await saveConfig(selectedAgentId.value, configJson);
    if (result.success) {
      saveStatus.value = 'success';
      showToast('配置保存成功', 'success');
      setTimeout(() => { saveStatus.value = 'idle'; }, 3000);
    } else {
      saveStatus.value = 'error';
      saveError.value = result.error || '保存失败';
      showToast('保存失败: ' + saveError.value, 'error');
    }
    return;
  }

  const result = await saveConfig(selectedAgentId.value, jsonText.value);
  if (result.success) {
    saveStatus.value = 'success';
    showToast('配置保存成功', 'success');
    setTimeout(() => { saveStatus.value = 'idle'; }, 3000);
  } else {
    saveStatus.value = 'error';
    saveError.value = result.error || '保存失败';
    showToast('保存失败: ' + saveError.value, 'error');
  }
}

function showToast(msg: string, type: 'success' | 'error') {
  const toast = document.createElement('div');
  toast.textContent = msg;
  toast.className = `fixed top-20 left-1/2 -translate-x-1/2 z-[200] px-6 py-3 rounded-lg shadow-xl text-white text-[14px] font-bold transition-all duration-300 ${type === 'success' ? 'bg-emerald-500' : 'bg-red-500'}`;
  toast.style.opacity = '0';
  document.body.appendChild(toast);
  requestAnimationFrame(() => {
    toast.style.opacity = '1';
  });
  setTimeout(() => {
    toast.style.opacity = '0';
    setTimeout(() => toast.remove(), 300);
  }, 2500);
}

async function onReload() {
  if (!selectedAgentId.value) return;
  const result = await reloadConfig(selectedAgentId.value);
  if (result.success && result.jsonStr !== undefined) {
    jsonText.value = result.jsonStr;
    syncFormFromJSON();
  }
}

async function onRestoreBackup() {
  if (!selectedAgentId.value) return;
  const confirmed = typeof (window as any).$confirm === 'function'
    ? await (window as any).$confirm('确定从 .bak 备份恢复配置？当前未保存的修改将丢失。')
    : window.confirm('确定从 .bak 备份恢复配置？当前未保存的修改将丢失。');
  if (!confirmed) return;
  const result = await restoreBackup(selectedAgentId.value);
  if (result.success) {
    await onReload();
  }
}

function deepMerge(target: any, source: any): any {
  if (typeof target !== 'object' || target === null) return source;
  if (typeof source !== 'object' || source === null) return source;
  const result = Array.isArray(target) ? [...target] : { ...target };
  for (const key of Object.keys(source)) {
    if (typeof source[key] === 'object' && source[key] !== null && !Array.isArray(source[key])) {
      result[key] = deepMerge(result[key] || {}, source[key]);
    } else {
      result[key] = source[key];
    }
  }
  return result;
}
</script>
