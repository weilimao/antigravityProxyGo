import { ConfigField } from './types';
import {
  getNestedBasePath,
  findTargetRepeatableField,
  getNestedRepeatableObjectNames,
} from './repeatableHelper';

/**
 * isVariantEffortChecked: 检查某个思考等级变体是否已存在/选中
 */
export function isVariantEffortChecked(
  formData: Record<string, any>,
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
  const existing = getNestedRepeatableObjectNames(formData, outerField, parentName, childName, targetField);
  return existing.some(n => n === opt);
}

/**
 * toggleVariantEffortHelper: 切换（勾选或取消）变体思考等级卡片
 */
export function toggleVariantEffortHelper(
  formData: Record<string, any>,
  outerField: ConfigField,
  parentName: string,
  childName: string,
  pickerField: ConfigField,
  opt: string,
): Record<string, any> {
  const targetKey = pickerField.targetRepeatableKey;
  if (!targetKey) return formData;
  const targetField = findTargetRepeatableField(outerField, targetKey);
  if (!targetField) return formData;

  if (isVariantEffortChecked(formData, outerField, parentName, childName, pickerField, opt)) {
    return removeNestedVariantHelper(formData, outerField, parentName, childName, targetField, opt);
  }

  const nestedBase = getNestedBasePath(outerField, parentName, childName, targetField);
  const updated = { ...formData };
  if (targetField.childTemplate) {
    for (const [tmplKey, tmplVal] of Object.entries(targetField.childTemplate)) {
      const resolvedKey = `${nestedBase}.${opt}.${tmplKey}`;
      if (updated[resolvedKey] === undefined) {
        updated[resolvedKey] = tmplKey === 'reasoningEffort' ? opt : tmplVal;
      }
    }
  } else {
    const resolvedKey = `${nestedBase}.${opt}.reasoningEffort`;
    if (updated[resolvedKey] === undefined) {
      updated[resolvedKey] = opt;
    }
  }
  return updated;
}

/**
 * addNestedVariantHelper: 手动新增一个变体子卡片
 */
export function addNestedVariantHelper(
  formData: Record<string, any>,
  outerField: ConfigField,
  parentName: string,
  childName: string,
  innerField: ConfigField,
  variantName: string,
): { updatedFormData: Record<string, any>; error?: string } {
  const trimmed = variantName.trim();
  if (!trimmed) {
    return { updatedFormData: formData, error: `请输入${innerField.childKeyLabel || '名称'}` };
  }
  const existingNames = getNestedRepeatableObjectNames(formData, outerField, parentName, childName, innerField);
  if (existingNames.includes(trimmed)) {
    return { updatedFormData: formData, error: `「${trimmed}」已存在` };
  }

  const nestedBase = getNestedBasePath(outerField, parentName, childName, innerField);
  const updated = { ...formData };
  if (innerField.childTemplate) {
    for (const [tmplKey, tmplVal] of Object.entries(innerField.childTemplate)) {
      const resolvedKey = `${nestedBase}.${trimmed}.${tmplKey}`;
      if (updated[resolvedKey] === undefined) {
        updated[resolvedKey] = tmplKey === 'reasoningEffort' ? trimmed : tmplVal;
      }
    }
  } else {
    const resolvedKey = `${nestedBase}.${trimmed}.reasoningEffort`;
    if (updated[resolvedKey] === undefined) {
      updated[resolvedKey] = trimmed;
    }
  }
  return { updatedFormData: updated };
}

/**
 * removeNestedVariantHelper: 删除某个嵌套变体子卡片的全部数据
 */
export function removeNestedVariantHelper(
  formData: Record<string, any>,
  outerField: ConfigField,
  parentName: string,
  childName: string,
  innerField: ConfigField,
  variantName: string,
): Record<string, any> {
  const nestedBase = getNestedBasePath(outerField, parentName, childName, innerField);
  const toRemovePrefix = `${nestedBase}.${variantName}.`;
  const updated: Record<string, any> = {};
  for (const [k, v] of Object.entries(formData)) {
    if (k.startsWith(toRemovePrefix) || k === `${nestedBase}.${variantName}`) {
      continue;
    }
    updated[k] = v;
  }
  return updated;
}
