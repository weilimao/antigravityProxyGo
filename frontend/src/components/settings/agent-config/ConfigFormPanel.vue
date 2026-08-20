<template>
  <div class="flex flex-col gap-4 overflow-y-auto flex-1 min-h-0">
    <div v-for="(section, sIdx) in schema.sections" :key="sIdx" class="glass-card rounded-xl p-5 flex flex-col gap-3">
      <div class="flex items-center justify-between gap-2 border-b border-outline-variant/20 pb-2">
        <div class="flex items-center gap-2">
          <span v-if="section.icon" class="material-symbols-outlined text-primary text-[18px]">{{ section.icon }}</span>
          <span class="text-[13px] font-bold text-on-surface dark:text-white">{{ section.title }}</span>
        </div>
        <button
          v-if="section.repeatable"
          class="text-[11px] text-primary hover:text-primary/80 font-medium flex items-center gap-1 cursor-pointer shrink-0 px-2 py-1 rounded-md hover:bg-primary/10 transition-colors"
          @click="openAddSection(section)"
        >
          <span class="material-symbols-outlined text-[14px]">add_circle</span>
          新增 {{ section.title.replace(/\(.*\)/, '') }}
        </button>
      </div>

      <!-- Repeatable section: Inline Add Bar -->
      <div
        v-if="section.repeatable && addingSectionState[section.title]?.open"
        class="flex flex-col gap-2 p-3 bg-primary/5 dark:bg-primary/10 border border-primary/30 rounded-lg animate-fadeIn"
      >
        <div class="flex items-center justify-between">
          <span class="text-[12px] font-bold text-primary dark:text-primary-fixed-dim">
            新增 {{ section.title.replace(/\(.*\)/, '') }}
          </span>
          <button
            class="text-[11px] text-outline hover:text-on-surface dark:hover:text-white cursor-pointer"
            @click="cancelAddSection(section)"
          >
            <span class="material-symbols-outlined text-[14px]">close</span>
          </button>
        </div>
        <div class="flex items-center gap-2">
          <input
            type="text"
            v-model="addingSectionState[section.title].name"
            @keydown.enter.prevent="confirmAddSection(section)"
            @keydown.esc.prevent="cancelAddSection(section)"
            :placeholder="`请输入${section.itemKeyField || '名称'}，例如 antigravityproxy`"
            class="flex-1 px-2.5 py-1.5 text-[12px] rounded-md bg-white dark:bg-white/5 border border-outline-variant/40 focus:border-primary focus:outline-none text-on-surface dark:text-white"
            autofocus
          />
          <button
            class="px-3 py-1.5 text-[11px] font-bold bg-primary hover:bg-primary/90 text-white rounded-md flex items-center gap-1 cursor-pointer transition-colors shadow-sm shrink-0"
            @click="confirmAddSection(section)"
          >
            <span class="material-symbols-outlined text-[13px]">check</span>
            添加并配置
          </button>
          <button
            class="px-2.5 py-1.5 text-[11px] bg-slate-100 dark:bg-white/5 hover:bg-slate-200 dark:hover:bg-white/10 text-on-surface dark:text-white border border-outline-variant/30 rounded-md cursor-pointer transition-colors shrink-0"
            @click="cancelAddSection(section)"
          >
            取消
          </button>
        </div>
        <p v-if="addingSectionState[section.title]?.error" class="text-[11px] text-error font-medium">
          {{ addingSectionState[section.title].error }}
        </p>
      </div>

      <!-- Non-repeatable section -->
      <template v-if="!section.repeatable">
        <div v-for="(field, fIdx) in section.fields" :key="fIdx" class="flex flex-col gap-1">
          <FieldRenderer
            :field="field"
            :value="formData[field.key]"
            :availableModels="props.availableModels"
            @update:value="onFieldUpdate(field, $event)"
          />
        </div>
      </template>

      <!-- Repeatable section (like provider, mcp) -->
      <template v-else>
        <div v-for="(item, itemIdx) in repeatableItems(section)" :key="itemIdx" class="flex flex-col gap-2 border border-outline-variant/20 rounded-lg overflow-hidden">
          <div
            class="flex items-center justify-between px-3 py-2 cursor-pointer hover:bg-slate-50 dark:hover:bg-white/5 transition-colors select-none"
            @click="toggleProviderExpansion(section.title + '.' + item)"
          >
            <div class="flex items-center gap-1.5">
              <span class="material-symbols-outlined text-[14px] text-on-surface dark:text-white transition-transform duration-200" :style="{ transform: isProviderExpanded(section.title + '.' + item) ? 'rotate(0deg)' : 'rotate(-90deg)' }">expand_more</span>
              <span class="text-[12px] font-bold text-on-surface dark:text-white">{{ repeatableItemLabel(section, item) }}</span>
            </div>
            <button
              class="text-[11px] text-error hover:text-error/80 font-medium flex items-center gap-1 cursor-pointer"
              @click.stop="removeRepeatableItem(section, item)"
            >
              <span class="material-symbols-outlined text-[14px]">delete</span>
              删除
            </button>
          </div>
          <div v-show="isProviderExpanded(section.title + '.' + item)" class="px-3 pb-3 flex flex-col gap-2">
            <div v-for="(field, fIdx) in section.fields" :key="fIdx" class="flex flex-col gap-1">
            <!-- Nested repeatable-object (e.g. provider.{name}.models.{modelName}.field) -->
            <template v-if="field.type === 'repeatable-object'">
              <div class="flex items-center justify-between gap-2 mt-2 mb-1">
                <div class="flex items-center gap-2">
                  <span class="text-[12px] font-bold text-on-surface dark:text-white">{{ field.label }}</span>
                  <span v-if="field.description" class="text-[10px] text-outline">{{ field.description }}</span>
                </div>
                <button
                  class="text-[11px] text-primary hover:text-primary/80 font-medium flex items-center gap-1 cursor-pointer shrink-0 px-2 py-1 rounded-md hover:bg-primary/10 transition-colors"
                  @click="openAddObjectChild(field, item)"
                >
                  <span class="material-symbols-outlined text-[14px]">add_circle</span>
                  新增 {{ field.childKeyLabel || '项目' }}
                </button>
              </div>

              <!-- Inline Add Model / Repeatable-Object Bar -->
              <div
                v-if="addingObjectChildState[getObjectChildStateKey(field, item)]?.open"
                class="flex flex-col gap-2.5 p-3.5 bg-primary/5 dark:bg-primary/10 border-2 border-dashed border-primary/40 rounded-xl transition-all mb-2"
              >
                <div class="flex items-center justify-between">
                  <div class="flex items-center gap-1.5">
                    <span class="material-symbols-outlined text-primary text-[16px]">smart_toy</span>
                    <span class="text-[12px] font-bold text-primary dark:text-primary-fixed-dim">
                      新增 {{ field.childKeyLabel || '模型' }}
                    </span>
                    <span class="text-[10px] text-outline">（可下拉选择已有模型，或手动输入模型名）</span>
                  </div>
                  <button
                    class="text-[11px] text-outline hover:text-on-surface dark:hover:text-white cursor-pointer"
                    @click="cancelAddObjectChild(field, item)"
                    title="关闭"
                  >
                    <span class="material-symbols-outlined text-[14px]">close</span>
                  </button>
                </div>

                <div class="grid grid-cols-1 md:grid-cols-2 gap-2">
                  <!-- 方式1：从已有模型快速选择 -->
                  <div class="flex flex-col gap-1">
                    <label class="text-[11px] font-semibold text-slate-600 dark:text-slate-300 flex items-center gap-1">
                      <span class="material-symbols-outlined text-[13px] text-primary">list</span>
                      从已有可用模型选择：
                    </label>
                    <ModelSearchSelect
                      :model-value="addingObjectChildState[getObjectChildStateKey(field, item)].selectedModel"
                      :options="props.availableModels || []"
                      placeholder="搜索或选择已有模型..."
                      @update:model-value="(val) => onSelectAvailableModel(field, item, val)"
                      class="w-full"
                    />
                  </div>

                  <!-- 方式2：自定义输入模型名称 -->
                  <div class="flex flex-col gap-1">
                    <label class="text-[11px] font-semibold text-slate-600 dark:text-slate-300 flex items-center gap-1">
                      <span class="material-symbols-outlined text-[13px] text-primary">edit</span>
                      或输入模型名称：
                    </label>
                    <input
                      type="text"
                      v-model="addingObjectChildState[getObjectChildStateKey(field, item)].customModel"
                      @input="onCustomModelInput(field, item)"
                      @keydown.enter.prevent="confirmAddObjectChild(field, item)"
                      @keydown.esc.prevent="cancelAddObjectChild(field, item)"
                      :placeholder="`请输入${field.childKeyLabel || '模型名'}，例如 deepseek-chat 或 claude-3-7-sonnet`"
                      class="w-full px-2.5 py-1.5 text-[12px] rounded-md bg-white dark:bg-[#1a1f30] border border-outline-variant/40 focus:border-primary focus:outline-none text-on-surface dark:text-white"
                      autofocus
                    />
                  </div>
                </div>

                <div class="flex items-center justify-between pt-1 border-t border-primary/10">
                  <div class="text-[11px] text-error font-medium">
                    {{ addingObjectChildState[getObjectChildStateKey(field, item)]?.error || '' }}
                  </div>
                  <div class="flex items-center gap-2">
                    <button
                      class="px-2.5 py-1.5 text-[11px] bg-slate-100 dark:bg-white/5 hover:bg-slate-200 dark:hover:bg-white/10 text-on-surface dark:text-white border border-outline-variant/30 rounded-md cursor-pointer transition-colors"
                      @click="cancelAddObjectChild(field, item)"
                    >
                      取消
                    </button>
                    <button
                      class="px-3.5 py-1.5 text-[11px] font-bold bg-primary hover:bg-primary/90 text-white rounded-md flex items-center gap-1 cursor-pointer transition-colors shadow-sm"
                      @click="confirmAddObjectChild(field, item)"
                    >
                      <span class="material-symbols-outlined text-[13px]">add_task</span>
                      保存并展开配置
                    </button>
                  </div>
                </div>
              </div>

              <!-- Existing model items -->
              <div
                v-for="(childName, cIdx) in repeatableObjectNames(field, item)"
                :key="cIdx"
                class="flex flex-col gap-1.5 border border-primary/20 dark:border-primary/30 rounded-lg bg-primary/5 dark:bg-primary/10 overflow-hidden"
              >
                <div
                  class="flex items-center justify-between px-3 py-2 cursor-pointer hover:bg-primary/10 dark:hover:bg-primary/15 transition-colors select-none"
                  @click="toggleModelCardExpansion(field.key + '.' + item + '.' + childName)"
                >
                  <div class="flex items-center gap-1.5">
                    <span class="material-symbols-outlined text-[14px] text-primary transition-transform duration-200" :style="{ transform: isModelCardExpanded(field.key + '.' + item + '.' + childName) ? 'rotate(0deg)' : 'rotate(-90deg)' }">expand_more</span>
                    <span class="text-[11px] font-bold text-primary dark:text-primary-fixed-dim">{{ field.childKeyLabel || '名称' }}: {{ childName }}</span>
                  </div>
                  <button
                    class="text-[10px] text-error hover:text-error/80 font-medium flex items-center gap-0.5 cursor-pointer"
                    @click.stop="removeRepeatableObjectChild(field, item, childName)"
                  >
                    <span class="material-symbols-outlined text-[12px]">delete</span>
                    删除
                  </button>
                </div>
                <div v-show="isModelCardExpanded(field.key + '.' + item + '.' + childName)" class="px-3 pb-3 flex flex-col gap-0.5">
                  <div v-for="(childField, cfIdx) in field.children" :key="cfIdx" class="flex flex-col gap-0.5">
                  <!-- multiselect-variants: 多选候选思考等级，勾选自动生成对应名字 variant 卡片 -->
                  <template v-if="childField.type === 'multiselect-variants' && childField.options">
                    <div class="flex items-center gap-2 mt-2 mb-1">
                      <span class="text-[12px] font-bold text-on-surface dark:text-white">{{ childField.label }}</span>
                    </div>
                    <p v-if="childField.description" class="text-[10px] text-outline leading-relaxed mb-1">{{ childField.description }}</p>
                    <div class="flex flex-wrap gap-1.5 ml-3">
                      <button
                        v-for="opt in childField.options"
                        :key="opt"
                        @click="toggleVariantEffort(field, item, childName, childField, opt)"
                        :class="isVariantEffortChecked(field, item, childName, childField, opt)
                          ? 'bg-primary text-white border-primary'
                          : 'bg-slate-100 dark:bg-white/5 text-slate-600 dark:text-slate-300 border-outline-variant/30 hover:bg-slate-200 dark:hover:bg-white/10'"
                        class="px-2.5 py-1 text-[11px] font-bold rounded-lg border cursor-pointer transition-all flex items-center gap-1"
                        :title="isVariantEffortChecked(field, item, childName, childField, opt) ? '点击取消勾选 / 删除该变体' : '点击勾选 / 自动生成该变体'"
                      >
                        <span v-if="isVariantEffortChecked(field, item, childName, childField, opt)" class="material-symbols-outlined text-[13px]">check</span>
                        <span>{{ opt }}</span>
                      </button>
                    </div>
                  </template>

                  <!-- 嵌套 repeatable-object 子字段（如模型内的 variants 变体列表） -->
                  <template v-else-if="childField.type === 'repeatable-object' && childField.children">
                    <div class="flex items-center justify-between gap-2 mt-2 mb-1">
                      <div class="flex items-center gap-2">
                        <span class="text-[12px] font-bold text-on-surface dark:text-white">{{ childField.label }}</span>
                        <span v-if="childField.description" class="text-[10px] text-outline">{{ childField.description }}</span>
                      </div>
                      <button
                        class="text-[11px] text-primary hover:text-primary/80 font-medium flex items-center gap-1 cursor-pointer shrink-0 px-2 py-1 rounded-md hover:bg-primary/10 transition-colors"
                        @click="openAddNestedChild(field, item, childName, childField)"
                      >
                        <span class="material-symbols-outlined text-[14px]">add_circle</span>
                        新增 {{ childField.childKeyLabel || '项目' }}
                      </button>
                    </div>

                    <!-- Inline Add Nested Object Bar (e.g. variants) -->
                    <div
                      v-if="addingNestedChildState[getNestedBasePath(field, item, childName, childField)]?.open"
                      class="flex flex-col gap-2 p-3 bg-primary/5 dark:bg-primary/10 border border-primary/30 rounded-lg ml-3 mb-2 animate-fadeIn"
                    >
                      <div class="flex items-center justify-between">
                        <span class="text-[11px] font-bold text-primary dark:text-primary-fixed-dim">
                          新增 {{ childField.childKeyLabel || '变体' }}
                        </span>
                        <button
                          class="text-[10px] text-outline hover:text-on-surface dark:hover:text-white cursor-pointer"
                          @click="cancelAddNestedChild(field, item, childName, childField)"
                        >
                          <span class="material-symbols-outlined text-[12px]">close</span>
                        </button>
                      </div>
                      <div class="flex items-center gap-2">
                        <input
                          type="text"
                          v-model="addingNestedChildState[getNestedBasePath(field, item, childName, childField)].name"
                          @keydown.enter.prevent="confirmAddNestedChild(field, item, childName, childField)"
                          @keydown.esc.prevent="cancelAddNestedChild(field, item, childName, childField)"
                          placeholder="请输入变体名称，例如 high / max / low"
                          class="flex-1 px-2.5 py-1 text-[11px] rounded-md bg-white dark:bg-[#1a1f30] border border-outline-variant/40 focus:border-primary focus:outline-none text-on-surface dark:text-white"
                          autofocus
                        />
                        <button
                          class="px-2.5 py-1 text-[11px] font-bold bg-primary hover:bg-primary/90 text-white rounded-md flex items-center gap-1 cursor-pointer transition-colors shadow-sm"
                          @click="confirmAddNestedChild(field, item, childName, childField)"
                        >
                          <span class="material-symbols-outlined text-[12px]">check</span>
                          添加
                        </button>
                        <button
                          class="px-2 py-1 text-[11px] bg-slate-100 dark:bg-white/5 hover:bg-slate-200 dark:hover:bg-white/10 text-on-surface dark:text-white border border-outline-variant/30 rounded-md cursor-pointer transition-colors"
                          @click="cancelAddNestedChild(field, item, childName, childField)"
                        >
                          取消
                        </button>
                      </div>
                      <p v-if="addingNestedChildState[getNestedBasePath(field, item, childName, childField)]?.error" class="text-[10px] text-error font-medium">
                        {{ addingNestedChildState[getNestedBasePath(field, item, childName, childField)].error }}
                      </p>
                    </div>

                    <!-- Existing variant items -->
                    <div
                      v-for="(variantName, vIdx) in nestedRepeatableObjectNames(field, item, childName, childField)"
                      :key="vIdx"
                      class="flex flex-col gap-1.5 border border-primary/15 dark:border-primary/25 rounded-lg p-3 bg-primary/3 dark:bg-primary/5 ml-3"
                    >
                      <div class="flex items-center justify-between">
                        <span class="text-[11px] font-bold text-primary dark:text-primary-fixed-dim">{{ childField.childKeyLabel || '名称' }}: {{ variantName }}</span>
                        <button
                          class="text-[10px] text-error hover:text-error/80 font-medium flex items-center gap-0.5 cursor-pointer"
                          @click="removeNestedRepeatableObjectChild(field, item, childName, childField, variantName)"
                        >
                          <span class="material-symbols-outlined text-[12px]">delete</span>
                          删除
                        </button>
                      </div>
                      <div v-for="(variantField, vfIdx) in childField.children" :key="vfIdx" class="flex flex-col gap-0.5">
                        <FieldRenderer
                          :field="variantField"
                          :name-resolver="variantName"
                          :value="getNestedRepeatableObjectChildValue(field, item, childName, childField, variantName, variantField)"
                          :availableModels="props.availableModels"
                          @update:value="onNestedRepeatableObjectChildUpdate(field, item, childName, childField, variantName, variantField, $event)"
                        />
                      </div>
                    </div>
                  </template>

                  <!-- 普通子字段 -->
                  <template v-else>
                    <FieldRenderer
                      :field="childField"
                      :name-resolver="childName"
                      :value="getRepeatableObjectChildValue(field, item, childName, childField)"
                      :availableModels="props.availableModels"
                      @update:value="onRepeatableObjectChildUpdate(field, item, childName, childField, $event)"
                    />
                  </template>
                  </div>
                </div>
              </div>
            </template>

            <!-- Regular field -->
            <template v-else>
              <FieldRenderer
                :field="field"
                :name-resolver="item"
                :value="getRepeatableFieldValue(field, item)"
                :availableModels="props.availableModels"
                @update:value="onRepeatableFieldUpdate(field, item, $event)"
              />
            </template>
           </div>
          </div>
        </div>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue';
import { AgentSchema, ConfigField, ConfigSection } from './types';
import FieldRenderer from './FieldRenderer.vue';
import ModelSearchSelect from './ModelSearchSelect.vue';

const props = defineProps<{
  schema: AgentSchema;
  formData: Record<string, any>;
  availableModels?: string[];
}>();

const emit = defineEmits<{
  'update:formData': [data: Record<string, any>];
}>();

const localFormData = ref<Record<string, any>>({ ...props.formData });

// 模型卡片折叠状态：Set 中存在 key = 已展开；空集 = 全部默认折叠。
// key 构造: field.key + '.' + parentItem + '.' + childName (含模型名,保唯一)
const expandedModels = ref<Set<string>>(new Set());

// Provider 卡片折叠状态：Set 中存在 key = 已展开；空集 = 全部默认折叠。
// key 构造: section.title + '.' + item(key名,如 antigravityproxy)
const expandedProviders = ref<Set<string>>(new Set());

// ===== 内联新增状态 =====
// 1. Repeatable Section 内联添加状态 (key: section.title)
const addingSectionState = ref<Record<string, { open: boolean; name: string; error: string }>>({});

// 2. Repeatable Object (如 models) 内联添加状态 (key: field.key + '.' + parentName)
const addingObjectChildState = ref<Record<string, {
  open: boolean;
  selectedModel: string;
  customModel: string;
  error: string;
}>>({});

// 3. Nested Repeatable Object (如 variants) 内联添加状态 (key: nestedBase)
const addingNestedChildState = ref<Record<string, { open: boolean; name: string; error: string }>>({});

function isModelCardExpanded(key: string): boolean {
  return expandedModels.value.has(key);
}

function toggleModelCardExpansion(key: string) {
  if (expandedModels.value.has(key)) {
    expandedModels.value.delete(key);
  } else {
    expandedModels.value.add(key);
  }
  expandedModels.value = new Set(expandedModels.value);
}

function isProviderExpanded(key: string): boolean {
  return expandedProviders.value.has(key);
}

function toggleProviderExpansion(key: string) {
  if (expandedProviders.value.has(key)) {
    expandedProviders.value.delete(key);
  } else {
    expandedProviders.value.add(key);
  }
  expandedProviders.value = new Set(expandedProviders.value);
}

// Only sync from parent → child when parent value differs (e.g. JSON sync)
watch(() => props.formData, (newVal) => {
  localFormData.value = { ...newVal };
}, { deep: false });

function cleanModelName(val: any): string {
  if (val === undefined || val === null) return '';
  const str = String(val).trim();
  return str.replace(/\[1M\]$/i, '').trim();
}

// Emit changes directly from event handlers, NOT from a watch on localFormData
// (which would create an infinite loop: child watch → emit → parent update → child watch)
function onFieldUpdate(field: ConfigField, value: any) {
  localFormData.value[field.key] = value;
  if (field.syncTargetKey) {
    if (value !== undefined && value !== null && value !== '') {
      localFormData.value[field.syncTargetKey] = cleanModelName(value);
    } else {
      localFormData.value[field.syncTargetKey] = '';
    }
  }
  emit('update:formData', { ...localFormData.value });
}

// Repeatable section logic
function getSectionPrefix(section: ConfigSection): string {
  if (!section.fields.length) return '';
  const firstKey = section.fields[0].key;
  const idx = firstKey.indexOf('{name}');
  if (idx < 0) return '';
  const prefix = firstKey.substring(0, idx);
  return prefix.endsWith('.') ? prefix.slice(0, -1) : prefix;
}

function repeatableItems(section: ConfigSection): string[] {
  const prefix = getSectionPrefix(section);

  // 分别收集顶层普通字段后缀和嵌套 repeatable-object 的中间路径标记（如 ".models."）
  const normalSuffixes: string[] = [];
  const roSubPaths: string[] = [];

  for (const f of section.fields) {
    const idx = f.key.indexOf('{name}');
    if (idx < 0) continue;
    const afterName = f.key.substring(idx + '{name}'.length); // 如 ".npm" 或 ".options.apiKey" 或 ".models"
    if (f.type === 'repeatable-object') {
      roSubPaths.push(afterName + '.'); // 如 ".models."
    } else if (afterName) {
      normalSuffixes.push(afterName);
    }
  }

  const names = new Set<string>();
  const prefixDot = prefix ? prefix + '.' : '';
  for (const key of Object.keys(localFormData.value)) {
    if (!key.startsWith(prefixDot)) continue;
    const afterPrefix = key.substring(prefixDot.length);
    if (!afterPrefix) continue;

    // 1. 优先识别是否属于嵌套 repeatable-object（如 "antigravityproxy.models.gemini-3.7..."）
    let matchedRO = false;
    for (const roPath of roSubPaths) {
      const roIdx = afterPrefix.indexOf(roPath);
      if (roIdx > 0) {
        names.add(afterPrefix.substring(0, roIdx));
        matchedRO = true;
        break;
      }
    }
    if (matchedRO) continue;

    // 2. 匹配顶层普通字段后缀（如 "other/aliyun/qwen3.8-max.slug"）
    let matchedSuffix = false;
    for (const suffix of normalSuffixes) {
      if (suffix.startsWith('.') && afterPrefix.endsWith(suffix)) {
        const namePart = afterPrefix.substring(0, afterPrefix.length - suffix.length);
        if (namePart) {
          names.add(namePart);
          matchedSuffix = true;
          break;
        }
      }
    }
    if (matchedSuffix) continue;

    // 3. 兜底：仅当既无普通后缀也无 RO 路径定义时提取
    if (!normalSuffixes.length && !roSubPaths.length) {
      const dotIdx = afterPrefix.indexOf('.');
      if (dotIdx > 0) {
        names.add(afterPrefix.substring(0, dotIdx));
      }
    }
  }
  return Array.from(names);
}

function repeatableItemLabel(section: ConfigSection, name: string): string {
  return name || '(unnamed)';
}

function resolveKey(keyTemplate: string, name: string): string {
  return keyTemplate.replace('{name}', name);
}

function getRepeatableFieldValue(field: ConfigField, name: string): any {
  const resolvedKey = resolveKey(field.key, name);
  return localFormData.value[resolvedKey];
}

function onRepeatableFieldUpdate(field: ConfigField, name: string, value: any) {
  const resolvedKey = resolveKey(field.key, name);
  localFormData.value[resolvedKey] = value;
  if (field.syncTargetKey) {
    const resolvedTargetKey = resolveKey(field.syncTargetKey, name);
    if (value !== undefined && value !== null && value !== '') {
      localFormData.value[resolvedTargetKey] = cleanModelName(value);
    } else {
      localFormData.value[resolvedTargetKey] = '';
    }
  }
  emit('update:formData', { ...localFormData.value });
}

// ===== Repeatable Section (Provider / MCP) 内联新增逻辑 =====
function openAddSection(section: ConfigSection) {
  const defaultName = section.itemKeyField === 'name' ? 'antigravityproxy' : `${section.itemKeyField || 'item'}_${Date.now().toString(36)}`;
  addingSectionState.value[section.title] = {
    open: true,
    name: defaultName,
    error: '',
  };
}

function cancelAddSection(section: ConfigSection) {
  if (addingSectionState.value[section.title]) {
    addingSectionState.value[section.title].open = false;
    addingSectionState.value[section.title].error = '';
  }
}

function confirmAddSection(section: ConfigSection) {
  const st = addingSectionState.value[section.title];
  if (!st) return;
  const name = st.name.trim();
  if (!name) {
    st.error = '请输入名称';
    return;
  }
  const existing = repeatableItems(section);
  if (existing.includes(name)) {
    st.error = `「${name}」已存在，请使用其他名称`;
    return;
  }

  if (section.itemTemplate) {
    const prefix = getSectionPrefix(section);
    for (const [tmplKey, tmplVal] of Object.entries(section.itemTemplate)) {
      const resolvedKey = `${prefix}.${name}.${tmplKey}`;
      localFormData.value[resolvedKey] = tmplVal;
    }
  }
  localFormData.value = { ...localFormData.value };
  emit('update:formData', { ...localFormData.value });

  // 自动展开新建的 Section 项
  expandedProviders.value.add(section.title + '.' + name);
  expandedProviders.value = new Set(expandedProviders.value);

  st.open = false;
  st.name = '';
  st.error = '';
}

function removeRepeatableItem(section: ConfigSection, name: string) {
  const prefix = getSectionPrefix(section);
  const toRemove: string[] = [];
  for (const key of Object.keys(localFormData.value)) {
    if (key.startsWith(`${prefix}.${name}.`)) {
      toRemove.push(key);
    }
  }
  for (const key of toRemove) {
    delete localFormData.value[key];
  }
  localFormData.value = { ...localFormData.value };
  emit('update:formData', { ...localFormData.value });
}

// ===== repeatable-object nested logic (e.g. provider.{name}.models.{modelName}.field) =====

function getChildPrefix(field: ConfigField, parentName: string): string {
  return field.key.replace('{name}', parentName);
}

function getObjectChildStateKey(field: ConfigField, parentName: string): string {
  return `${field.key}.${parentName}`;
}

function repeatableObjectNames(field: ConfigField, parentName: string): string[] {
  const childPrefix = getChildPrefix(field, parentName);
  const names = new Set<string>();
  const leafSuffixes = collectLeafSuffixesRecursive(field);

  for (const key of Object.keys(localFormData.value)) {
    if (!key.startsWith(childPrefix + '.')) continue;
    const afterPrefix = key.substring(childPrefix.length + 1);
    for (const suffix of leafSuffixes) {
      const namePart = matchSuffix(afterPrefix, suffix);
      if (namePart) {
        names.add(namePart);
        break;
      }
    }
  }
  return Array.from(names);
}

function collectLeafSuffixesRecursive(field: ConfigField): string[] {
  if (!field.children) return [];
  const suffixes: string[] = [];
  for (const child of field.children) {
    if (child.type === 'repeatable-object' && child.children && child.children.length > 0) {
      const nestedLeaves = collectLeafSuffixesRecursive(child);
      for (const leaf of nestedLeaves) {
        suffixes.push('.' + child.key + '.*' + leaf);
      }
    } else {
      suffixes.push('.' + child.key);
    }
  }
  return suffixes;
}

function matchSuffix(afterPrefix: string, suffix: string): string | null {
  if (!suffix.includes('*')) {
    if (afterPrefix.endsWith(suffix)) {
      const namePart = afterPrefix.substring(0, afterPrefix.length - suffix.length);
      if (namePart && !namePart.endsWith('.')) return namePart;
    }
    return null;
  }
  const suffixParts = suffix.split('.');
  const starIdx = suffixParts.indexOf('*');
  if (starIdx < 0) return null;
  const fixedPrefix = suffixParts.slice(0, starIdx).join('.');
  const fixedSuffix = suffixParts.slice(starIdx + 1).join('.');
  if (!afterPrefix.endsWith(fixedSuffix)) return null;
  const withoutSuffix = afterPrefix.substring(0, afterPrefix.length - fixedSuffix.length);
  if (fixedPrefix) {
    const lastIdx = withoutSuffix.lastIndexOf(fixedPrefix + '.');
    if (lastIdx < 0) return null;
    const starValue = withoutSuffix.substring(lastIdx + fixedPrefix.length + 1);
    if (!starValue || starValue.includes('.')) return null;
    const namePart = afterPrefix.substring(0, lastIdx);
    if (namePart && !namePart.endsWith('.')) return namePart;
    return null;
  }
  return null;
}

function resolveChildKey(field: ConfigField, parentName: string, childName: string, childField: ConfigField): string {
  const childPrefix = getChildPrefix(field, parentName);
  return `${childPrefix}.${childName}.${childField.key}`;
}

function getRepeatableObjectChildValue(field: ConfigField, parentName: string, childName: string, childField: ConfigField): any {
  const key = resolveChildKey(field, parentName, childName, childField);
  return localFormData.value[key];
}

function onRepeatableObjectChildUpdate(field: ConfigField, parentName: string, childName: string, childField: ConfigField, value: any) {
  const key = resolveChildKey(field, parentName, childName, childField);
  localFormData.value[key] = value;
  if (childField.syncTargetKey) {
    const targetKey = resolveChildKey(field, parentName, childName, { ...childField, key: childField.syncTargetKey });
    if (value !== undefined && value !== null && value !== '') {
      localFormData.value[targetKey] = cleanModelName(value);
    } else {
      localFormData.value[targetKey] = '';
    }
  }
  emit('update:formData', { ...localFormData.value });
}

// ===== Repeatable Object (模型列表) 内联新增操作 =====
function openAddObjectChild(field: ConfigField, parentName: string) {
  const key = getObjectChildStateKey(field, parentName);
  const defaultSelect = (props.availableModels && props.availableModels.length > 0) ? props.availableModels[0] : '';
  addingObjectChildState.value[key] = {
    open: true,
    selectedModel: defaultSelect,
    customModel: defaultSelect,
    error: '',
  };
}

function cancelAddObjectChild(field: ConfigField, parentName: string) {
  const key = getObjectChildStateKey(field, parentName);
  if (addingObjectChildState.value[key]) {
    addingObjectChildState.value[key].open = false;
    addingObjectChildState.value[key].error = '';
  }
}

function onSelectAvailableModel(field: ConfigField, parentName: string, modelName: string) {
  const key = getObjectChildStateKey(field, parentName);
  if (addingObjectChildState.value[key]) {
    addingObjectChildState.value[key].selectedModel = modelName;
    if (modelName) {
      addingObjectChildState.value[key].customModel = modelName;
    }
    addingObjectChildState.value[key].error = '';
  }
}

function onCustomModelInput(field: ConfigField, parentName: string) {
  const key = getObjectChildStateKey(field, parentName);
  if (addingObjectChildState.value[key]) {
    addingObjectChildState.value[key].error = '';
  }
}

function confirmAddObjectChild(field: ConfigField, parentName: string) {
  const key = getObjectChildStateKey(field, parentName);
  const st = addingObjectChildState.value[key];
  if (!st) return;
  const childName = (st.customModel.trim() || st.selectedModel.trim());
  if (!childName) {
    st.error = `请输入或选择${field.childKeyLabel || '名称'}`;
    return;
  }
  const existingNames = repeatableObjectNames(field, parentName);
  if (existingNames.includes(childName)) {
    st.error = `「${childName}」已存在，请勿重复添加`;
    return;
  }

  if (field.childTemplate) {
    const childPrefix = getChildPrefix(field, parentName);
    for (const [tmplKey, tmplVal] of Object.entries(field.childTemplate)) {
      const resolvedKey = `${childPrefix}.${childName}.${tmplKey}`;
      if (localFormData.value[resolvedKey] === undefined) {
        localFormData.value[resolvedKey] = tmplVal;
      }
    }
  }
  localFormData.value = { ...localFormData.value };
  emit('update:formData', { ...localFormData.value });

  // 关键体验：自动展开新建的模型卡片，方便用户立即配置
  const modelCardKey = `${field.key}.${parentName}.${childName}`;
  expandedModels.value.add(modelCardKey);
  expandedModels.value = new Set(expandedModels.value);

  st.open = false;
  st.customModel = '';
  st.selectedModel = '';
  st.error = '';
}

function removeRepeatableObjectChild(field: ConfigField, parentName: string, childName: string) {
  const childPrefix = getChildPrefix(field, parentName);
  const toRemove: string[] = [];
  for (const key of Object.keys(localFormData.value)) {
    if (key.startsWith(`${childPrefix}.${childName}.`)) {
      toRemove.push(key);
    }
  }
  for (const key of toRemove) {
    delete localFormData.value[key];
  }
  localFormData.value = { ...localFormData.value };
  emit('update:formData', { ...localFormData.value });
}

// ===== 嵌套 repeatable-object 辅助（模型内的 variants 变体列表） =====

function getNestedBasePath(outerField: ConfigField, parentName: string, childName: string, innerField: ConfigField): string {
  const outerBase = getChildPrefix(outerField, parentName);
  return `${outerBase}.${childName}.${innerField.key}`;
}

function findTargetRepeatableField(parentField: ConfigField, targetKey: string): ConfigField | undefined {
  if (!parentField.children) return undefined;
  return parentField.children.find(c => c.key === targetKey && c.type === 'repeatable-object');
}

function isVariantEffortChecked(
  outerField: ConfigField,
  parentName: string,
  childName: string,
  pickerField: ConfigField,
  opt: string,
): boolean {
  const targetKey = pickerField.targetRepeatableKey;
  if (!targetKey) return false;
  const targetField = findTargetRepeatableField(outerField, targetKey);
  if (!targetField) return false;
  const existing = nestedRepeatableObjectNames(outerField, parentName, childName, targetField);
  return existing.some(n => n === opt);
}

function toggleVariantEffort(
  outerField: ConfigField,
  parentName: string,
  childName: string,
  pickerField: ConfigField,
  opt: string,
) {
  const targetKey = pickerField.targetRepeatableKey;
  if (!targetKey) return;
  const targetField = findTargetRepeatableField(outerField, targetKey);
  if (!targetField) return;
  if (isVariantEffortChecked(outerField, parentName, childName, pickerField, opt)) {
    removeNestedRepeatableObjectChild(outerField, parentName, childName, targetField, opt);
  } else {
    const nestedBase = getNestedBasePath(outerField, parentName, childName, targetField);
    if (targetField.childTemplate) {
      for (const [tmplKey, tmplVal] of Object.entries(targetField.childTemplate)) {
        const resolvedKey = `${nestedBase}.${opt}.${tmplKey}`;
        if (localFormData.value[resolvedKey] === undefined) {
          localFormData.value[resolvedKey] = tmplKey === 'reasoningEffort' ? opt : tmplVal;
        }
      }
    } else {
      const resolvedKey = `${nestedBase}.${opt}.reasoningEffort`;
      if (localFormData.value[resolvedKey] === undefined) {
        localFormData.value[resolvedKey] = opt;
      }
    }
    localFormData.value = { ...localFormData.value };
    emit('update:formData', { ...localFormData.value });
  }
}

function nestedRepeatableObjectNames(
  outerField: ConfigField,
  parentName: string,
  childName: string,
  innerField: ConfigField,
): string[] {
  const nestedBase = getNestedBasePath(outerField, parentName, childName, innerField);
  const names = new Set<string>();
  const leafSuffixes = (innerField.children || []).map(c => '.' + c.key);

  for (const key of Object.keys(localFormData.value)) {
    if (!key.startsWith(nestedBase + '.')) continue;
    const afterPrefix = key.substring(nestedBase.length + 1);
    for (const suffix of leafSuffixes) {
      if (afterPrefix.endsWith(suffix)) {
        const namePart = afterPrefix.substring(0, afterPrefix.length - suffix.length);
        if (namePart && !namePart.endsWith('.')) {
          names.add(namePart);
          break;
        }
      }
    }
  }
  return Array.from(names);
}

function getNestedRepeatableObjectChildValue(
  outerField: ConfigField,
  parentName: string,
  childName: string,
  innerField: ConfigField,
  variantName: string,
  variantField: ConfigField,
): any {
  const nestedBase = getNestedBasePath(outerField, parentName, childName, innerField);
  const key = `${nestedBase}.${variantName}.${variantField.key}`;
  return localFormData.value[key];
}

function onNestedRepeatableObjectChildUpdate(
  outerField: ConfigField,
  parentName: string,
  childName: string,
  innerField: ConfigField,
  variantName: string,
  variantField: ConfigField,
  value: any,
) {
  const nestedBase = getNestedBasePath(outerField, parentName, childName, innerField);
  const key = `${nestedBase}.${variantName}.${variantField.key}`;
  localFormData.value[key] = value;
  emit('update:formData', { ...localFormData.value });
}

// ===== Nested Repeatable Object (变体列表) 内联新增操作 =====
function openAddNestedChild(
  outerField: ConfigField,
  parentName: string,
  childName: string,
  innerField: ConfigField,
) {
  const nestedBase = getNestedBasePath(outerField, parentName, childName, innerField);
  addingNestedChildState.value[nestedBase] = {
    open: true,
    name: 'high',
    error: '',
  };
}

function cancelAddNestedChild(
  outerField: ConfigField,
  parentName: string,
  childName: string,
  innerField: ConfigField,
) {
  const nestedBase = getNestedBasePath(outerField, parentName, childName, innerField);
  if (addingNestedChildState.value[nestedBase]) {
    addingNestedChildState.value[nestedBase].open = false;
    addingNestedChildState.value[nestedBase].error = '';
  }
}

function confirmAddNestedChild(
  outerField: ConfigField,
  parentName: string,
  childName: string,
  innerField: ConfigField,
) {
  const nestedBase = getNestedBasePath(outerField, parentName, childName, innerField);
  const st = addingNestedChildState.value[nestedBase];
  if (!st) return;
  const variantName = st.name.trim();
  if (!variantName) {
    st.error = `请输入${innerField.childKeyLabel || '名称'}`;
    return;
  }
  const existingNames = nestedRepeatableObjectNames(outerField, parentName, childName, innerField);
  if (existingNames.includes(variantName)) {
    st.error = `「${variantName}」已存在`;
    return;
  }

  if (innerField.childTemplate) {
    for (const [tmplKey, tmplVal] of Object.entries(innerField.childTemplate)) {
      const resolvedKey = `${nestedBase}.${variantName}.${tmplKey}`;
      if (localFormData.value[resolvedKey] === undefined) {
        localFormData.value[resolvedKey] = tmplKey === 'reasoningEffort' ? variantName : tmplVal;
      }
    }
  } else {
    const resolvedKey = `${nestedBase}.${variantName}.reasoningEffort`;
    if (localFormData.value[resolvedKey] === undefined) {
      localFormData.value[resolvedKey] = variantName;
    }
  }
  localFormData.value = { ...localFormData.value };
  emit('update:formData', { ...localFormData.value });

  st.open = false;
  st.name = '';
  st.error = '';
}

function removeNestedRepeatableObjectChild(
  outerField: ConfigField,
  parentName: string,
  childName: string,
  innerField: ConfigField,
  variantName: string,
) {
  const nestedBase = getNestedBasePath(outerField, parentName, childName, innerField);
  const toRemove: string[] = [];
  for (const key of Object.keys(localFormData.value)) {
    if (key.startsWith(`${nestedBase}.${variantName}.`)) {
      toRemove.push(key);
    }
  }
  for (const key of toRemove) {
    delete localFormData.value[key];
  }
  localFormData.value = { ...localFormData.value };
  emit('update:formData', { ...localFormData.value });
}
</script>
