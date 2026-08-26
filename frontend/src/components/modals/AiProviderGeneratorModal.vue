<template>
  <!--
    AiProviderGeneratorModal.vue: OpenCode provider 的「AI 一键生成」弹窗。
    在 Agent 配置 → OpenCode → 可视化配置标题栏点「AI 生成 Provider」打开。
    弹窗内: ModelSearchSelect(选生成用的模型) + 可编辑提示词 textarea + 生成按钮。
    成功后 emit('generated', content) 把 AI 输出的 JSON 片段回传父组件回填。
  -->
  <Teleport to="body">
    <div
      v-if="visible"
      class="fixed inset-0 z-[100] bg-slate-950/75 flex items-center justify-center px-4"
      @click.self="close"
    >
      <div class="bg-white dark:bg-[#1e2538] rounded-2xl border border-outline-variant/60 shadow-2xl flex flex-col w-[680px] max-w-[95vw] max-h-[88vh]">
        <!-- Header -->
        <div class="px-6 py-4 border-b border-outline-variant/30 flex justify-between items-center rounded-t-2xl bg-slate-50/50 dark:bg-white/5">
          <div class="flex items-center gap-2">
            <span class="material-symbols-outlined text-primary text-[20px]">auto_awesome</span>
            <span class="text-sm font-bold text-on-surface dark:text-white">AI 生成 Provider 配置</span>
          </div>
          <button
            class="text-outline hover:text-primary transition-colors p-1 rounded-full hover:bg-slate-100 dark:hover:bg-white/5 cursor-pointer"
            @click="close"
          >
            <span class="material-symbols-outlined text-[18px]">close</span>
          </button>
        </div>

        <!-- Body -->
        <div class="flex-grow overflow-y-auto p-6 flex flex-col gap-4">
          <!-- 生成用的模型 -->
          <div class="flex flex-col gap-1.5">
            <label class="text-[12px] font-medium text-on-surface dark:text-white flex items-center gap-1">
              <span class="material-symbols-outlined text-[14px] text-primary">smart_toy</span>
              用哪个模型生成
            </label>
            <p class="text-[11px] text-outline">选择中继服务暴露的任意模型,AI 会用它来生成 provider 配置</p>
            <ModelSearchSelect
              :model-value="selectedModel"
              :options="availableModels"
              :refresh-on-open="true"
              placeholder="搜索或选择生成用的模型..."
              @update:model-value="(v: string) => (selectedModel = v)"
              @refresh="$emit('refresh-models')"
              class="w-full"
            />
          </div>

          <!-- 生成哪些模型 (多选, 数据源=中继模型映射, 默认预选调用量 Top 10) -->
          <div class="flex flex-col gap-1.5">
            <div class="flex items-center justify-between">
              <label class="text-[12px] font-medium text-on-surface dark:text-white flex items-center gap-1">
                <span class="material-symbols-outlined text-[14px] text-primary">checklist</span>
                生成哪些模型
                <span v-if="selectedModels.length > 0" class="text-[11px] text-primary font-bold">({{ selectedModels.length }})</span>
              </label>
              <button
                class="text-[11px] text-outline hover:text-primary transition-colors flex items-center gap-0.5 cursor-pointer"
                @click="preselectTopModels"
                title="重新按中继调用量 Top 10 预选"
              >
                <span class="material-symbols-outlined text-[13px]">trending_up</span>
                按调用量 Top10 预选
              </button>
            </div>
            <p class="text-[11px] text-outline">
              从中继模型映射中选择要生成的模型(默认预选调用量前十)。AI 只会为选中的模型生成条目,不会编造模型
            </p>
            <ModelSearchSelect
              v-model:modelIds="selectedModels"
              :multiple="true"
              :options="availableModels"
              :refresh-on-open="true"
              placeholder="搜索并勾选要生成的模型..."
              @refresh="$emit('refresh-models')"
              class="w-full"
            />
          </div>

          <!-- 用户需求补充 -->
          <div class="flex flex-col gap-1.5">
            <label class="text-[12px] font-medium text-on-surface dark:text-white flex items-center gap-1">
              <span class="material-symbols-outlined text-[14px] text-primary">edit_note</span>
              需求描述 (可选)
            </label>
            <p class="text-[11px] text-outline">描述你想接入的上游,例如:接入 DeepSeek,模型 deepseek-chat,baseURL 用官方地址</p>
            <textarea
              v-model="userInput"
              rows="2"
              placeholder="例如:接入 DeepSeek 官方上游,模型 deepseek-chat 与 deepseek-reasoner,走官方 baseURL"
              class="px-3 py-2 bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[13px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all resize-y"
            />
          </div>

          <!-- 可编辑提示词模板 -->
          <div class="flex flex-col gap-1.5">
            <div class="flex items-center justify-between">
              <label class="text-[12px] font-medium text-on-surface dark:text-white flex items-center gap-1">
                <span class="material-symbols-outlined text-[14px] text-primary">prompt</span>
                系统提示词 (可编辑)
              </label>
              <button
                class="text-[11px] text-outline hover:text-primary transition-colors flex items-center gap-0.5 cursor-pointer"
                @click="systemPrompt = defaultPrompt"
                title="恢复默认模板"
              >
                <span class="material-symbols-outlined text-[13px]">restart_alt</span>
                重置模板
              </button>
            </div>
            <textarea
              v-model="systemPrompt"
              rows="10"
              class="px-3 py-2 bg-slate-50 dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[11px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all resize-y font-mono leading-relaxed"
            />
          </div>

          <!-- 错误提示 -->
          <div
            v-if="errorMsg"
            class="text-[12px] text-error bg-error/10 border border-error/20 rounded-lg px-3 py-2 flex items-start gap-2"
          >
            <span class="material-symbols-outlined text-[14px] mt-0.5 shrink-0">error</span>
            <span class="break-all">{{ errorMsg }}</span>
          </div>

          <!-- 预览(生成成功后) -->
          <div v-if="previewJson" class="flex flex-col gap-1.5">
            <label class="text-[12px] font-medium text-emerald-500 flex items-center gap-1">
              <span class="material-symbols-outlined text-[14px]">check_circle</span>
              生成成功 (已校验为合法 JSON,点击下方「应用」回填)
            </label>
            <pre class="px-3 py-2 bg-slate-50 dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[11px] text-on-surface dark:text-white overflow-x-auto max-h-[180px] font-mono">{{ previewJson }}</pre>
          </div>
        </div>

        <!-- Footer -->
        <div class="px-6 py-4 border-t border-outline-variant/30 flex justify-end gap-3 rounded-b-2xl shrink-0 bg-slate-50/50 dark:bg-white/5">
          <button
            class="px-4 py-2 text-[13px] font-medium text-on-surface dark:text-white hover:bg-slate-100 dark:hover:bg-white/5 rounded-lg transition-colors border border-outline-variant/30 cursor-pointer"
            @click="close"
          >
            取消
          </button>
          <button
            v-if="!previewJson"
            class="px-4 py-2 text-[13px] font-bold text-white bg-primary hover:bg-primary/90 rounded-lg transition-colors shadow-sm disabled:opacity-50 disabled:pointer-events-none flex items-center gap-1.5 cursor-pointer"
            :disabled="generating || !selectedModel || selectedModels.length === 0"
            @click="onGenerate"
          >
            <span v-if="generating" class="material-symbols-outlined text-[15px] animate-spin">progress_activity</span>
            <span v-else class="material-symbols-outlined text-[15px]">auto_awesome</span>
            {{ generating ? '生成中...' : '生成配置' }}
          </button>
          <template v-else>
            <button
              class="px-4 py-2 text-[13px] font-medium text-primary hover:bg-primary/10 rounded-lg transition-colors border border-primary/30 flex items-center gap-1.5 cursor-pointer"
              @click="onRegenerate"
              :disabled="generating"
            >
              <span class="material-symbols-outlined text-[15px]">refresh</span>
              重新生成
            </button>
            <button
              class="px-4 py-2 text-[13px] font-bold text-white bg-emerald-500 hover:bg-emerald-600 rounded-lg transition-colors shadow-sm flex items-center gap-1.5 cursor-pointer"
              @click="onApply"
            >
              <span class="material-symbols-outlined text-[15px]">check</span>
              应用到配置
            </button>
          </template>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue';
import ModelSearchSelect from '../settings/agent-config/ModelSearchSelect.vue';
import { generateOpenCodeProvider, fetchTopRelayModels, DEFAULT_PROVIDER_GEN_PROMPT } from '../../ui/agentConfigController';

const props = defineProps<{
  visible: boolean;
  availableModels: string[];
  defaultPrompt?: string;
}>();

const emit = defineEmits<{
  close: [];
  'refresh-models': [];
  // 生成成功且用户点「应用」后, 把 AI 输出的 JSON 片段回传父组件回填
  apply: [content: string];
}>();

const defaultPrompt = props.defaultPrompt || DEFAULT_PROVIDER_GEN_PROMPT;
const selectedModel = ref('');
const selectedModels = ref<string[]>([]); // 要生成的模型列表(多选, 数据源=中继映射)
const userInput = ref('');
const systemPrompt = ref(defaultPrompt);
const generating = ref(false);
const errorMsg = ref('');
const previewJson = ref(''); // 生成成功后展示 + 应用时回传
let topModelsCache: string[] = []; // 中继调用量降序的模型名缓存

// preselectTopModels: 按中继调用量 Top 10 预选要生成的模型。
// 只预选"模型映射里真实存在"的条目(交集), 统计里可能残留映射已删掉的模型名。
function preselectTopModels() {
  const inMapping = props.availableModels || [];
  const picked: string[] = [];
  for (const name of topModelsCache) {
    if (picked.length >= 10) break;
    if (inMapping.includes(name)) picked.push(name);
  }
  selectedModels.value = picked;
}

// 弹窗打开时重置状态(保留 selectedModel/selectedModels 方便连续生成), 并预取调用量 Top10 预选
watch(
  () => props.visible,
  (v) => {
    if (v) {
      errorMsg.value = '';
      previewJson.value = '';
      generating.value = false;
      // 首次打开(或映射有更新)时拉取调用量统计并预选 Top10; 已有手选内容不覆盖
      fetchTopRelayModels().then((rows) => {
        topModelsCache = rows.map((r) => r.model).filter(Boolean);
        if (selectedModels.value.length === 0) {
          preselectTopModels();
        }
      });
    }
  },
);

function close() {
  emit('close');
}

// 提取 AI 输出里第一个 '{' 到最后一个 '}' 的子串, 容错 markdown 包裹与前后解释文本
function extractJsonObject(text: string): string {
  const start = text.indexOf('{');
  const end = text.lastIndexOf('}');
  if (start < 0 || end < 0 || end <= start) return '';
  return text.slice(start, end + 1).trim();
}

async function onGenerate() {
  if (!selectedModel.value) {
    errorMsg.value = '请先选择生成用的模型';
    return;
  }
  if (selectedModels.value.length === 0) {
    errorMsg.value = '请至少勾选一个要生成的模型(从中继模型映射中选择, 避免 AI 编造模型 id)';
    return;
  }
  if (!systemPrompt.value.trim()) {
    errorMsg.value = '系统提示词不能为空';
    return;
  }
  generating.value = true;
  errorMsg.value = '';
  previewJson.value = '';
  try {
    const res = await generateOpenCodeProvider(selectedModel.value, systemPrompt.value, userInput.value, selectedModels.value);
    if (!res || !res.success || !res.content) {
      errorMsg.value = res?.error || 'AI 未返回有效内容';
      return;
    }
    const raw = res.content.trim();
    // 尝试直接 parse; 失败则抽取 {} 子串再 parse; 都失败给错误并展示原始文本
    let parsed: any;
    let jsonStr = '';
    try {
      parsed = JSON.parse(raw);
      jsonStr = JSON.stringify(parsed, null, 2);
    } catch {
      const extracted = extractJsonObject(raw);
      if (!extracted) {
        throw new Error('AI 输出不是合法 JSON,且未找到 {} 片段');
      }
      parsed = JSON.parse(extracted); // 若仍失败抛给 catch
      jsonStr = JSON.stringify(parsed, null, 2);
    }
    // 校验顶层是 { providerName: {...} } 形态
    if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
      throw new Error('AI 输出顶层不是 JSON 对象');
    }
    const topKeys = Object.keys(parsed);
    if (topKeys.length === 0) {
      throw new Error('AI 输出的 JSON 对象为空');
    }
    previewJson.value = jsonStr;
  } catch (err: any) {
    errorMsg.value = '解析失败: ' + (err?.message || String(err));
  } finally {
    generating.value = false;
  }
}

function onRegenerate() {
  previewJson.value = '';
  errorMsg.value = '';
  onGenerate();
}

function onApply() {
  if (!previewJson.value) return;
  emit('apply', previewJson.value);
  close();
}
</script>
