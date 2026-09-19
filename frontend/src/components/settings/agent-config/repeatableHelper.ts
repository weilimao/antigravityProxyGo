import { ConfigField, ConfigSection, AgentSchema } from './types';

/**
 * cleanModelName: 剥离模型名称后缀（如 [1M]）
 */
export function cleanModelName(val: any): string {
  if (val === undefined || val === null) return '';
  const str = String(val).trim();
  return str.replace(/\[1M\]$/i, '').trim();
}

/**
 * getChildPrefix: 计算 repeatable-object 的前缀路径（如 provider.antigravityproxy.models）
 */
export function getChildPrefix(field: ConfigField, parentName: string): string {
  return field.key.replace('{name}', parentName);
}

/**
 * getObjectChildStateKey: 状态管理 key
 */
export function getObjectChildStateKey(field: ConfigField, parentName: string): string {
  return `${field.key}.${parentName}`;
}

/**
 * collectLeafSuffixesRecursive: 递归收集所有叶子字段后缀
 */
export function collectLeafSuffixesRecursive(field: ConfigField): string[] {
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

/**
 * matchSuffix: 模式匹配后缀以提取条目名称
 */
export function matchSuffix(afterPrefix: string, suffix: string): string | null {
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

/**
 * getRepeatableObjectNames: 从 formData 中提取某 parent 下 repeatable-object 的所有子项名称列表
 */
export function getRepeatableObjectNames(
  formData: Record<string, any>,
  field: ConfigField,
  parentName: string,
): string[] {
  const childPrefix = getChildPrefix(field, parentName);
  const names = new Set<string>();
  const leafSuffixes = collectLeafSuffixesRecursive(field);

  for (const key of Object.keys(formData)) {
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

/**
 * resolveChildKey: 构造子字段对应的完整 formData 键名
 */
export function resolveChildKey(
  field: ConfigField,
  parentName: string,
  childName: string,
  childField: ConfigField,
): string {
  const childPrefix = getChildPrefix(field, parentName);
  return `${childPrefix}.${childName}.${childField.key}`;
}

/**
 * getNestedBasePath: 获取嵌套 repeatable-object（如 variants）的基础路径
 */
export function getNestedBasePath(
  outerField: ConfigField,
  parentName: string,
  childName: string,
  innerField: ConfigField,
): string {
  const outerBase = getChildPrefix(outerField, parentName);
  return `${outerBase}.${childName}.${innerField.key}`;
}

/**
 * findTargetRepeatableField: 在父字段 children 中查找目标 repeatable-object
 */
export function findTargetRepeatableField(
  parentField: ConfigField,
  targetKey: string,
): ConfigField | undefined {
  if (!parentField.children) return undefined;
  return parentField.children.find(c => c.key === targetKey && c.type === 'repeatable-object');
}

/**
 * getNestedRepeatableObjectNames: 获取嵌套对象（如 variants）的子项名称列表
 */
export function getNestedRepeatableObjectNames(
  formData: Record<string, any>,
  outerField: ConfigField,
  parentName: string,
  childName: string,
  innerField: ConfigField,
): string[] {
  const nestedBase = getNestedBasePath(outerField, parentName, childName, innerField);
  const names = new Set<string>();
  const leafSuffixes = (innerField.children || []).map(c => '.' + c.key);

  for (const key of Object.keys(formData)) {
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

/**
 * renameRepeatableObjectChild: 重命名模型或子对象键名并无损迁移全部下属属性
 */
export function renameRepeatableObjectChild(
  formData: Record<string, any>,
  field: ConfigField,
  parentName: string,
  oldName: string,
  newName: string,
): { updatedFormData: Record<string, any>; error?: string } {
  const trimmed = newName.trim();
  if (!trimmed) {
    return { updatedFormData: formData, error: `请输入${field.childKeyLabel || '名称'}` };
  }
  if (trimmed === oldName) {
    return { updatedFormData: formData };
  }
  const existingNames = getRepeatableObjectNames(formData, field, parentName);
  if (existingNames.includes(trimmed)) {
    return { updatedFormData: formData, error: `「${trimmed}」已存在，请勿使用重复名称` };
  }

  const childPrefix = getChildPrefix(field, parentName);
  const oldPrefix = `${childPrefix}.${oldName}.`;
  const newPrefix = `${childPrefix}.${trimmed}.`;

  const updated: Record<string, any> = {};
  for (const [k, v] of Object.entries(formData)) {
    if (k.startsWith(oldPrefix)) {
      const rest = k.substring(oldPrefix.length);
      // 若 display name (name 字段) 与旧模型名相同或为空，自动同步为新模型名
      if (rest === 'name' && (v === oldName || !v)) {
        updated[`${newPrefix}${rest}`] = trimmed;
      } else {
        updated[`${newPrefix}${rest}`] = v;
      }
    } else if (k === `${childPrefix}.${oldName}`) {
      updated[`${childPrefix}.${trimmed}`] = v;
    } else {
      updated[k] = v;
    }
  }

  return { updatedFormData: updated };
}

/**
 * removeRepeatableObjectChildKeys: 彻底移除某子项的全部 formData 键
 */
export function removeRepeatableObjectChildKeys(
  formData: Record<string, any>,
  field: ConfigField,
  parentName: string,
  childName: string,
): Record<string, any> {
  const childPrefix = getChildPrefix(field, parentName);
  const oldPrefix = `${childPrefix}.${childName}.`;
  const updated: Record<string, any> = {};
  for (const [k, v] of Object.entries(formData)) {
    if (k.startsWith(oldPrefix) || k === `${childPrefix}.${childName}`) {
      continue;
    }
    updated[k] = v;
  }
  return updated;
}

/**
 * collectDynamicContainerPatterns: 从 Schema 中收集所有动态集合容器路径模式（如 ['provider'], ['provider', '*', 'models'], ['mcpServers']）
 */
export function collectDynamicContainerPatterns(schema: AgentSchema | null): string[][] {
  const patterns: string[][] = [
    ['provider'],
    ['provider', '*', 'models'],
    ['provider', '*', 'models', '*', 'variants'],
    ['mcpServers'],
    ['model_provider'],
  ];

  if (!schema) return patterns;

  for (const section of schema.sections) {
    if (section.repeatable) {
      const parts = section.fields[0]?.key ? section.fields[0].key.split('.') : [];
      const starIdx = parts.indexOf('{name}');
      if (starIdx > 0) {
        patterns.push(parts.slice(0, starIdx));
      }
    }
    for (const field of section.fields) {
      if (field.type === 'repeatable-object') {
        const parts = field.key.split('.').map(p => (p === '{name}' ? '*' : p));
        patterns.push(parts);

        if (field.children) {
          for (const child of field.children) {
            if (child.type === 'repeatable-object') {
              patterns.push([...parts, '*', child.key]);
            }
          }
        }
      }
    }
  }

  return patterns;
}

function matchesPattern(path: string[], pattern: string[]): boolean {
  if (path.length !== pattern.length) return false;
  for (let i = 0; i < path.length; i++) {
    if (pattern[i] !== '*' && pattern[i] !== path[i]) {
      return false;
    }
  }
  return true;
}

/**
 * deepMergeConfig: 针对配置的深合并。对于 Schema 定义的动态映射容器（如 models），以 source 的项为主导，
 * 剔除已被重命名或删除的旧键，同时保留普通对象中的未知自定义扩展属性。
 */
export function deepMergeConfig(
  target: any,
  source: any,
  schema: AgentSchema | null = null,
  currentPath: string[] = [],
): any {
  if (typeof target !== 'object' || target === null) return source;
  if (typeof source !== 'object' || source === null) return source;

  const patterns = collectDynamicContainerPatterns(schema);
  const isDynamic = patterns.some(p => matchesPattern(currentPath, p));

  const result = Array.isArray(target) ? [...target] : {};

  if (isDynamic) {
    // 动态集合容器（如 models、variants、mcpServers）：
    // 键集由 source（表单实际存在项）严格主导，剔除已删除或更名的旧键
    for (const key of Object.keys(source)) {
      if (typeof source[key] === 'object' && source[key] !== null && !Array.isArray(source[key])) {
        result[key] = deepMergeConfig(target[key] || {}, source[key], schema, [...currentPath, key]);
      } else {
        result[key] = source[key];
      }
    }
  } else {
    // 普通对象：保留 target 中的未知/自定义扩展字段，同时叠加 source
    Object.assign(result, target);
    for (const key of Object.keys(source)) {
      if (typeof source[key] === 'object' && source[key] !== null && !Array.isArray(source[key])) {
        result[key] = deepMergeConfig(result[key] || {}, source[key], schema, [...currentPath, key]);
      } else {
        result[key] = source[key];
      }
    }
  }

  return result;
}

/**
 * getSectionPrefix: 获取可重复 Section 的前缀路径（如 provider）
 */
export function getSectionPrefix(section: ConfigSection): string {
  if (!section.fields.length) return '';
  const firstKey = section.fields[0].key;
  const idx = firstKey.indexOf('{name}');
  if (idx < 0) return '';
  const prefix = firstKey.substring(0, idx);
  return prefix.endsWith('.') ? prefix.slice(0, -1) : prefix;
}

/**
 * getRepeatableItems: 从 formData 中提取可重复 Section 的全部条目名称
 */
export function getRepeatableItems(formData: Record<string, any>, section: ConfigSection): string[] {
  const prefix = getSectionPrefix(section);
  const normalSuffixes: string[] = [];
  const roSubPaths: string[] = [];

  for (const f of section.fields) {
    const idx = f.key.indexOf('{name}');
    if (idx < 0) continue;
    const afterName = f.key.substring(idx + '{name}'.length);
    if (f.type === 'repeatable-object') {
      roSubPaths.push(afterName + '.');
    } else if (afterName) {
      normalSuffixes.push(afterName);
    }
  }

  const names = new Set<string>();
  const prefixDot = prefix ? prefix + '.' : '';
  for (const key of Object.keys(formData)) {
    if (!key.startsWith(prefixDot)) continue;
    const afterPrefix = key.substring(prefixDot.length);
    if (!afterPrefix) continue;

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

    if (!normalSuffixes.length && !roSubPaths.length) {
      const dotIdx = afterPrefix.indexOf('.');
      if (dotIdx > 0) {
        names.add(afterPrefix.substring(0, dotIdx));
      }
    }
  }
  return Array.from(names);
}

/**
 * resolveSectionKey: 解析替换 Section key 中的 {name}
 */
export function resolveSectionKey(keyTemplate: string, name: string): string {
  return keyTemplate.replace('{name}', name);
}

/**
 * removeRepeatableItemKeys: 移除 repeatable section 条目的全部键
 */
export function removeRepeatableItemKeys(
  formData: Record<string, any>,
  section: ConfigSection,
  name: string,
): Record<string, any> {
  const prefix = getSectionPrefix(section);
  const toRemovePrefix = `${prefix}.${name}.`;
  const updated: Record<string, any> = {};
  for (const [k, v] of Object.entries(formData)) {
    if (k.startsWith(toRemovePrefix) || k === `${prefix}.${name}`) {
      continue;
    }
    updated[k] = v;
  }
  return updated;
}

/**
 * addRepeatableObjectChildHelper: 向 formData 中添加新的 repeatable-object 子项
 */
export function addRepeatableObjectChildHelper(
  formData: Record<string, any>,
  field: ConfigField,
  parentName: string,
  childName: string,
): { updatedFormData: Record<string, any>; error?: string } {
  const trimmed = childName.trim();
  if (!trimmed) {
    return { updatedFormData: formData, error: `请输入或选择${field.childKeyLabel || '名称'}` };
  }
  const existingNames = getRepeatableObjectNames(formData, field, parentName);
  if (existingNames.includes(trimmed)) {
    return { updatedFormData: formData, error: `「${trimmed}」已存在，请勿重复添加` };
  }

  const updated = { ...formData };
  if (field.childTemplate) {
    const childPrefix = getChildPrefix(field, parentName);
    for (const [tmplKey, tmplVal] of Object.entries(field.childTemplate)) {
      const resolvedKey = `${childPrefix}.${trimmed}.${tmplKey}`;
      if (updated[resolvedKey] === undefined) {
        updated[resolvedKey] = tmplVal;
      }
    }
  }
  return { updatedFormData: updated };
}

/**
 * addSectionItemHelper: 向 formData 中添加新的 repeatable section 条目
 */
export function addSectionItemHelper(
  formData: Record<string, any>,
  section: ConfigSection,
  name: string,
): { updatedFormData: Record<string, any>; error?: string } {
  const trimmed = name.trim();
  if (!trimmed) {
    const isModel = !!section.isModelList || section.itemKeyField === 'slug' || section.title.includes('模型列表');
    return { updatedFormData: formData, error: isModel ? '请选择或输入模型名称' : '请输入名称' };
  }
  const existing = getRepeatableItems(formData, section);
  if (existing.includes(trimmed)) {
    return { updatedFormData: formData, error: `「${trimmed}」已存在，请使用其他名称` };
  }

  const prefix = getSectionPrefix(section);
  const updated = { ...formData };
  if (section.itemTemplate) {
    for (const [tmplKey, tmplVal] of Object.entries(section.itemTemplate)) {
      const resolvedKey = `${prefix}.${trimmed}.${tmplKey}`;
      updated[resolvedKey] = tmplVal;
    }
  }
  if (section.itemKeyField) {
    const keyFieldKey = `${prefix}.${trimmed}.${section.itemKeyField}`;
    if (!updated[keyFieldKey]) {
      updated[keyFieldKey] = trimmed;
    }
  }
  const displayNameKey = `${prefix}.${trimmed}.display_name`;
  if (updated[displayNameKey] === '') {
    updated[displayNameKey] = trimmed;
  }
  return { updatedFormData: updated };
}

/**
 * copyToClipboard: 跨环境剪贴板文本写入辅助函数
 */
export async function copyToClipboard(text: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(text);
  } catch {
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.style.position = 'fixed';
    ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.select();
    try {
      document.execCommand('copy');
    } catch { /* 忽略异常 */ }
    document.body.removeChild(ta);
  }
}
