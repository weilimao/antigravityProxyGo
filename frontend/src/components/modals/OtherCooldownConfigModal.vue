<template>
  <BaseModal
    id="otherCooldownModal"
    containerId="otherCooldownModalContainer"
    closeBtnId="btnOtherCooldownModalClose"
    title="自定义冷却策略"
    titleI18n="otherCooldownModalTitle"
    icon="ac_unit"
    maxWidth="w-[520px] max-w-[95vw]"
    maxHeight="max-h-[85vh]"
    bodyClass="px-6 py-5 overflow-y-auto flex flex-col gap-4"
  >
    <!-- 目标组提示 -->
    <div class="text-[12px] text-outline dark:text-outline-variant">
      <span data-i18n="otherCooldownTargetGroup">目标分组</span>:
      <span class="font-bold text-on-surface dark:text-white" id="otherCooldownModalGroup">{{ targetGroupDisplay }}</span>
    </div>

    <!-- 启用开关 -->
    <div class="flex items-center justify-between gap-3 bg-slate-50/50 dark:bg-white/5 border border-outline-variant/30 rounded-lg px-3 py-2.5">
      <div>
        <div class="text-[13px] font-bold text-on-surface dark:text-white" data-i18n="otherCooldownEnabledLabel">启用自定义冷却</div>
        <div class="text-[11px] text-outline mt-0.5" data-i18n="otherCooldownEnabledTip">关闭后该组上游报错仅换号,不写账号冷静期</div>
      </div>
      <label class="relative inline-flex items-center cursor-pointer shrink-0">
        <input type="checkbox" id="otherCooldownModalEnabled" class="sr-only peer" v-model="enabled" />
        <span class="text-[11px] font-medium text-outline mr-2 peer-checked:text-primary dark:peer-checked:text-primary-fixed-dim">{{ enabledText }}</span>
        <span class="relative inline-block w-7 h-3.5 bg-slate-300 dark:bg-white/10 rounded-full peer-checked:bg-primary transition-colors peer-checked:after:translate-x-3 after:content-[''] after:absolute after:top-0.5 after:left-0.5 after:w-3 after:h-3 after:bg-white dark:after:bg-slate-200 after:rounded-full after:shadow-sm after:transition-transform"></span>
      </label>
    </div>

    <!-- 状态码 -->
    <div class="flex flex-col gap-1.5">
      <label class="text-[12px] font-medium text-on-surface dark:text-white" data-i18n="otherCooldownCodesLabel">触发状态码</label>
      <input type="text" id="otherCooldownModalCodes"
        placeholder="429, 402, 5xx"
        data-i18n-placeholder="otherCooldownCodesPlaceholder"
        data-i18n-title="otherCooldownCodesTip"
        title="触发冷却的远端状态码,逗号分隔;支持 5xx 整段写法(如 5xx=500-599)"
        class="w-full px-3 py-2 bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[13px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all font-mono" />
      <div class="text-[11px] text-outline mt-0.5" data-i18n="otherCooldownCodesTip">触发冷却的远端状态码,逗号分隔;支持 5xx 整段写法(如 5xx=500-599);留空则永不冷却</div>
    </div>

    <!-- 冷却时长 -->
    <div class="flex flex-col gap-1.5">
      <label class="text-[12px] font-medium text-on-surface dark:text-white" data-i18n="otherCooldownSecsLabel">冷却时长 (秒)</label>
      <input type="number" id="otherCooldownModalSecs" min="1" max="604800"
        data-i18n-title="otherCooldownSecsTip"
        title="命中后冷却秒数,默认 60"
        class="w-40 px-3 py-2 bg-white dark:bg-[#151b2b] border border-outline-variant/40 rounded-lg text-[13px] text-on-surface dark:text-white focus:outline-none focus:border-primary transition-all text-center" />
      <div class="text-[11px] text-outline mt-0.5" data-i18n="otherCooldownSecsTip">命中后冷却秒数(1-604800,默认 60)</div>
    </div>

    <!-- 模型过滤:多选模型选择器(公共 ModelSearchSelect)+ 获取模型按钮(与编辑账号弹窗同交互)。
         allow-custom 默认开启,可手输 deepseek* 等通配项走「使用自定义模型」加入。 -->
    <div class="flex flex-col gap-1.5">
      <div class="flex items-center justify-between">
        <label class="text-[12px] font-medium text-on-surface dark:text-white flex items-center gap-1">
          <span data-i18n="otherCooldownModelsLabel">模型过滤 (可选)</span>
          <span v-if="selectedModels.length > 0" class="text-[11px] text-primary font-bold">({{ selectedModels.length }})</span>
        </label>
        <button type="button" id="btnOtherCooldownFetchModels" class="px-2.5 py-1 text-[11px] font-bold text-primary bg-primary/10 hover:bg-primary/20 rounded-lg transition-colors flex items-center gap-1 shrink-0 cursor-pointer">
          <span class="material-symbols-outlined text-[14px]">download</span>
          <span data-i18n="otherFetchModels">获取模型</span>
        </button>
      </div>
      <ModelSearchSelect
        v-model:modelIds="selectedModels"
        :multiple="true"
        :options="modelOptions"
        placeholder="搜索并勾选模型,或输入通配如 deepseek* ..."
        class="w-full"
      />
      <div class="text-[11px] text-outline mt-0.5" data-i18n="otherCooldownModelsTip">仅命中模型才触发冷却,逗号分隔;支持 deepseek* 前缀通配;留空=全部模型</div>
    </div>

    <div id="otherCooldownModalError" class="hidden text-[12px] text-red-500 bg-red-500/10 border border-red-500/20 rounded-lg px-3 py-2"></div>

    <template #footer>
      <button class="px-4 py-2 text-[13px] font-medium text-on-surface dark:text-white hover:bg-slate-100 dark:hover:bg-white/5 rounded-lg transition-colors border border-outline-variant/30 cursor-pointer" id="btnOtherCooldownModalCancel" data-i18n="otherModalCancel">取消</button>
      <button class="px-4 py-2 text-[13px] font-bold text-white bg-primary hover:bg-primary/90 rounded-lg transition-colors shadow-sm disabled:opacity-50 disabled:pointer-events-none cursor-pointer" id="btnOtherCooldownModalSave" data-i18n="otherCooldownModalSave">保存</button>
    </template>
  </BaseModal>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import BaseModal from './BaseModal.vue';
import ModelSearchSelect from '../settings/agent-config/ModelSearchSelect.vue';
import {
  otherCooldownEnabled,
  otherCooldownTargetGroupDisplay,
  otherCooldownModelOptions,
  otherCooldownSelectedModels,
} from '../../ui/otherCooldownModal';

// 弹窗内状态由 otherCooldownModal.ts 控制器以导出 ref 共享(与 OtherAccountModal 的
// otherDefaultModel 同范式):开关勾选态与模型多选走 v-model,组显示名只读展示。
const enabled = otherCooldownEnabled;
const targetGroupDisplay = otherCooldownTargetGroupDisplay;
const modelOptions = otherCooldownModelOptions;
const selectedModels = otherCooldownSelectedModels;

const enabledText = computed(() => (enabled.value ? '已启用' : '已关闭'));
</script>
