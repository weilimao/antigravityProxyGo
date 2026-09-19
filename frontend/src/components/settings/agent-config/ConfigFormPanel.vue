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
          <!-- 模型列表类型：使用公共组件 ModelSearchSelect -->
          <ModelSearchSelect
            v-if="isModelSection(section)"
            :model-value="addingSectionState[section.title].name"
            :options="props.availableModels || []"
            placeholder="搜索或选择模型（支持输入自定义模型名）..."
            :allow-custom="true"
            :refresh-on-open="true"
            @update:model-value="(val) => onSectionModelSelect(section, val)"
            @submit="confirmAddSection(section)"
            @refresh="emit('refresh-models')"
            class="flex-1 min-w-0"
          />
          <!-- 普通 Section：保持文本输入框 -->
          <input
            v-else
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
            @refresh-models="emit('refresh-models')"
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
                      :refresh-on-open="true"
                      @update:model-value="(val) => onSelectAvailableModel(field, item, val)"
                      @refresh="emit('refresh-models')"
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
                  <div class="flex items-center gap-2">
                    <button
                      class="text-[10px] text-outline hover:text-primary font-medium flex items-center gap-0.5 cursor-pointer"
                      :title="`编辑${field.childKeyLabel || '模型名'}`"
                      @click.stop="openEditObjectChild(field, item, childName)"
                    >
                      <span class="material-symbols-outlined text-[12px]">edit</span>
                      编辑
                    </button>
                    <button
                      class="text-[10px] text-outline hover:text-primary font-medium flex items-center gap-0.5 cursor-pointer"
                      :title="isModelCopied(field.key + '.' + item + '.' + childName) ? '已复制' : '复制模型名'"
                      @click.stop="copyModelName(field, item, childName)"
                    >
                      <span class="material-symbols-outlined text-[12px]">{{ isModelCopied(field.key + '.' + item + '.' + childName) ? 'check' : 'content_copy' }}</span>
                      {{ isModelCopied(field.key + '.' + item + '.' + childName) ? '已复制' : '复制' }}
                    </button>
                    <button
                      class="text-[10px] text-error hover:text-error/80 font-medium flex items-center gap-0.5 cursor-pointer"
                      @click.stop="removeRepeatableObjectChild(field, item, childName)"
                    >
                      <span class="material-symbols-outlined text-[12px]">delete</span>
                      删除
                    </button>
                  </div>
                </div>
                <!-- 编辑模型名称内联区域 -->
                <div
                  v-if="isEditingObjectChild(field, item, childName)"
                  class="px-3 py-2 bg-primary/10 dark:bg-primary/20 border-t border-b border-primary/20 flex flex-col gap-1.5"
                  @click.stop
                >
                  <div class="flex items-center justify-between">
                    <div class="flex items-center gap-1">
                      <span class="material-symbols-outlined text-[13px] text-primary">edit</span>
                      <span class="text-[11px] font-bold text-primary dark:text-primary-fixed-dim">
                        修改{{ field.childKeyLabel || '模型名' }}
                      </span>
                    </div>
                    <button
                      class="text-[11px] text-outline hover:text-on-surface dark:hover:text-white cursor-pointer"
                      @click="cancelEditObjectChild(field, item, childName)"
                      title="关闭"
                    >
                      <span class="material-symbols-outlined text-[13px]">close</span>
                    </button>
                  </div>
                  <div class="flex items-center gap-2">
                    <input
                      type="text"
                      v-model="getEditingState(field, item, childName).newName"
                      @input="clearEditObjectChildError(field, item, childName)"
                      @keydown.enter.prevent="confirmEditObjectChild(field, item, childName)"
                      @keydown.esc.prevent="cancelEditObjectChild(field, item, childName)"
                      :placeholder="`请输入新的${field.childKeyLabel || '模型名'}`"
                      class="flex-1 px-2.5 py-1 text-[11px] rounded bg-white dark:bg-[#1a1f30] border border-outline-variant/40 focus:border-primary focus:outline-none text-on-surface dark:text-white"
                      autofocus
                    />
                    <button
                      class="px-2.5 py-1 text-[11px] font-bold bg-primary hover:bg-primary/90 text-white rounded flex items-center gap-1 cursor-pointer transition-colors shadow-xs shrink-0"
                      @click="confirmEditObjectChild(field, item, childName)"
                    >
                      <span class="material-symbols-outlined text-[12px]">check</span>
                      保存
                    </button>
                    <button
                      class="px-2 py-1 text-[11px] bg-slate-100 dark:bg-white/5 hover:bg-slate-200 dark:hover:bg-white/10 text-on-surface dark:text-white border border-outline-variant/30 rounded cursor-pointer transition-colors shrink-0"
                      @click="cancelEditObjectChild(field, item, childName)"
                    >
                      取消
                    </button>
                  </div>
                  <div
                    v-if="getEditingState(field, item, childName)?.error"
                    class="text-[10px] text-error font-medium"
                  >
                    {{ getEditingState(field, item, childName).error }}
                  </div>
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
                          @refresh-models="emit('refresh-models')"
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
                      @refresh-models="emit('refresh-models')"
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
                @refresh-models="emit('refresh-models')"
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
import {
  cleanModelName,
  getChildPrefix,
  getObjectChildStateKey,
  getRepeatableObjectNames,
  resolveChildKey,
  getNestedBasePath,
  findTargetRepeatableField,
  getNestedRepeatableObjectNames,
  renameRepeatableObjectChild,
  removeRepeatableObjectChildKeys,
  getSectionPrefix,
  getRepeatableItems,
  resolveSectionKey,
  removeRepeatableItemKeys,
  addRepeatableObjectChildHelper,
  addSectionItemHelper,
  copyToClipboard,
} from './repeatableHelper';
import {
  isVariantEffortChecked as isVariantEffortCheckedHelper,
  toggleVariantEffortHelper,
  addNestedVariantHelper,
  removeNestedVariantHelper,
} from './variantsHelper';

const props = defineProps<{
  schema: AgentSchema;
  formData: Record<string, any>;
  availableModels?: string[];
}>();

const emit = defineEmits<{
  'update:formData': [data: Record<string, any>];
  /** 模型下拉打开时上抛，请求父级刷新中继模型映射 */
  'refresh-models': [];
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
  if (expandedModels.value.has(key)) expandedModels.value.delete(key);
  else expandedModels.value.add(key);
  expandedModels.value = new Set(expandedModels.value);
}

function isProviderExpanded(key: string): boolean {
  return expandedProviders.value.has(key);
}

function toggleProviderExpansion(key: string) {
  if (expandedProviders.value.has(key)) expandedProviders.value.delete(key);
  else expandedProviders.value.add(key);
  expandedProviders.value = new Set(expandedProviders.value);
}

// Only sync from parent → child when parent value differs (e.g. JSON sync)
watch(() => props.formData, (newVal) => {
  localFormData.value = { ...newVal };
}, { deep: false });

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
function repeatableItems(section: ConfigSection): string[] {
  return getRepeatableItems(localFormData.value, section);
}

function repeatableItemLabel(section: ConfigSection, name: string): string {
  return name || '(unnamed)';
}

function resolveKey(keyTemplate: string, name: string): string {
  return resolveSectionKey(keyTemplate, name);
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

// ===== Repeatable Section (Provider / MCP / ModelCatalog) 内联新增逻辑 =====
function isModelSection(section: ConfigSection): boolean {
  return !!section.isModelList || section.itemKeyField === 'slug' || section.title.includes('模型列表');
}

function onSectionModelSelect(section: ConfigSection, val: string) {
  if (!addingSectionState.value[section.title]) return;
  addingSectionState.value[section.title].name = val;
  addingSectionState.value[section.title].error = '';
}

function openAddSection(section: ConfigSection) {
  let defaultName = '';
  if (isModelSection(section)) {
    defaultName = '';
  } else if (section.itemKeyField === 'name') {
    defaultName = 'antigravityproxy';
  } else {
    defaultName = `${section.itemKeyField || 'item'}_${Date.now().toString(36)}`;
  }
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
  const res = addSectionItemHelper(localFormData.value, section, st.name);
  if (res.error) {
    st.error = res.error;
    return;
  }
  const name = st.name.trim();
  localFormData.value = res.updatedFormData;
  emit('update:formData', { ...localFormData.value });

  // 自动展开新建的 Section 项
  expandedProviders.value.add(section.title + '.' + name);
  expandedProviders.value = new Set(expandedProviders.value);

  st.open = false;
  st.name = '';
  st.error = '';
}

function removeRepeatableItem(section: ConfigSection, name: string) {
  localFormData.value = removeRepeatableItemKeys(localFormData.value, section, name);
  emit('update:formData', { ...localFormData.value });
}

// ===== repeatable-object nested logic (e.g. provider.{name}.models.{modelName}.field) =====

function repeatableObjectNames(field: ConfigField, parentName: string): string[] {
  return getRepeatableObjectNames(localFormData.value, field, parentName);
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
  addingObjectChildState.value[key] = {
    open: true,
    selectedModel: '',
    customModel: '',
    error: '',
  };
}

function cancelAddObjectChild(field: ConfigField, parentName: string) {
  const key = getObjectChildStateKey(field, parentName);
  if (addingObjectChildState.value[key]) {
    addingObjectChildState.value[key].open = false;
    addingObjectChildState.value[key].error = '';
    addingObjectChildState.value[key].selectedModel = '';
    addingObjectChildState.value[key].customModel = '';
  }
}

// 选中下拉模型：只更新 selectedModel，并清空 customModel 避免误提交残留值。
// 若用户后续在"自定义输入"框中手填，则以 customModel 优先（用户显式意图）。
function onSelectAvailableModel(field: ConfigField, parentName: string, modelName: string) {
  const key = getObjectChildStateKey(field, parentName);
  if (addingObjectChildState.value[key]) {
    addingObjectChildState.value[key].selectedModel = modelName;
    addingObjectChildState.value[key].customModel = '';
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
  const res = addRepeatableObjectChildHelper(localFormData.value, field, parentName, childName);
  if (res.error) {
    st.error = res.error;
    return;
  }

  localFormData.value = res.updatedFormData;
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
  localFormData.value = removeRepeatableObjectChildKeys(localFormData.value, field, parentName, childName);
  emit('update:formData', { ...localFormData.value });
}

// ===== Repeatable Object (模型) 内联编辑操作 =====
const editingObjectChildState = ref<Record<string, {
  open: boolean;
  newName: string;
  error: string;
}>>({});

function getEditingChildKey(field: ConfigField, parentName: string, childName: string): string {
  return `${field.key}.${parentName}.${childName}`;
}

function isEditingObjectChild(field: ConfigField, parentName: string, childName: string): boolean {
  const key = getEditingChildKey(field, parentName, childName);
  return Boolean(editingObjectChildState.value[key]?.open);
}

function getEditingState(field: ConfigField, parentName: string, childName: string) {
  const key = getEditingChildKey(field, parentName, childName);
  if (!editingObjectChildState.value[key]) {
    editingObjectChildState.value[key] = {
      open: false,
      newName: childName,
      error: '',
    };
  }
  return editingObjectChildState.value[key];
}

function openEditObjectChild(field: ConfigField, parentName: string, childName: string) {
  const key = getEditingChildKey(field, parentName, childName);
  editingObjectChildState.value[key] = {
    open: true,
    newName: childName,
    error: '',
  };
}

function cancelEditObjectChild(field: ConfigField, parentName: string, childName: string) {
  const key = getEditingChildKey(field, parentName, childName);
  if (editingObjectChildState.value[key]) {
    editingObjectChildState.value[key].open = false;
    editingObjectChildState.value[key].error = '';
  }
}

function clearEditObjectChildError(field: ConfigField, parentName: string, childName: string) {
  const key = getEditingChildKey(field, parentName, childName);
  if (editingObjectChildState.value[key]) {
    editingObjectChildState.value[key].error = '';
  }
}

function confirmEditObjectChild(field: ConfigField, parentName: string, oldName: string) {
  const key = getEditingChildKey(field, parentName, oldName);
  const st = editingObjectChildState.value[key];
  if (!st) return;

  const res = renameRepeatableObjectChild(
    localFormData.value,
    field,
    parentName,
    oldName,
    st.newName,
  );

  if (res.error) {
    st.error = res.error;
    return;
  }

  const newName = st.newName.trim();
  if (newName !== oldName) {
    localFormData.value = res.updatedFormData;
    emit('update:formData', { ...localFormData.value });

    // 迁移折叠卡片展开状态
    const oldCardKey = `${field.key}.${parentName}.${oldName}`;
    const newCardKey = `${field.key}.${parentName}.${newName}`;
    if (expandedModels.value.has(oldCardKey)) {
      expandedModels.value.delete(oldCardKey);
      expandedModels.value.add(newCardKey);
      expandedModels.value = new Set(expandedModels.value);
    }

    // 迁移复制提示状态
    if (copiedModelKeys.value.has(oldCardKey)) {
      copiedModelKeys.value.delete(oldCardKey);
      copiedModelKeys.value = new Set(copiedModelKeys.value);
    }
  }

  st.open = false;
  st.error = '';
  delete editingObjectChildState.value[key];
}

// ===== 模型名一键复制 =====
// 复制成功的卡片 key 集合,用于把按钮图标/文案临时切为"已复制",定时器恢复。
const copiedModelKeys = ref<Set<string>>(new Set());
const copyTimers = new Map<string, number>();

function isModelCopied(key: string): boolean {
  return copiedModelKeys.value.has(key);
}

async function copyModelName(field: ConfigField, parentName: string, childName: string) {
  const key = `${field.key}.${parentName}.${childName}`;
  await copyToClipboard(childName);
  copiedModelKeys.value.add(key);
  copiedModelKeys.value = new Set(copiedModelKeys.value);
  const prev = copyTimers.get(key);
  if (prev) window.clearTimeout(prev);
  copyTimers.set(key, window.setTimeout(() => {
    copiedModelKeys.value.delete(key);
    copiedModelKeys.value = new Set(copiedModelKeys.value);
    copyTimers.delete(key);
  }, 1500));
}

// ===== 嵌套 repeatable-object 辅助（模型内的 variants 变体列表） =====


function isVariantEffortChecked(
  outerField: ConfigField,
  parentName: string,
  childName: string,
  pickerField: ConfigField,
  opt: string,
): boolean {
  return isVariantEffortCheckedHelper(localFormData.value, outerField, parentName, childName, pickerField, opt);
}

function toggleVariantEffort(
  outerField: ConfigField,
  parentName: string,
  childName: string,
  pickerField: ConfigField,
  opt: string,
) {
  localFormData.value = toggleVariantEffortHelper(localFormData.value, outerField, parentName, childName, pickerField, opt);
  emit('update:formData', { ...localFormData.value });
}

function nestedRepeatableObjectNames(
  outerField: ConfigField,
  parentName: string,
  childName: string,
  innerField: ConfigField,
): string[] {
  return getNestedRepeatableObjectNames(localFormData.value, outerField, parentName, childName, innerField);
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

  const res = addNestedVariantHelper(localFormData.value, outerField, parentName, childName, innerField, st.name);
  if (res.error) {
    st.error = res.error;
    return;
  }

  localFormData.value = res.updatedFormData;
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
  localFormData.value = removeNestedVariantHelper(localFormData.value, outerField, parentName, childName, innerField, variantName);
  emit('update:formData', { ...localFormData.value });
}
</script>
