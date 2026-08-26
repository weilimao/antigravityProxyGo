// agentConfigController.ts: Agent 配置可视化的前端控制器。
// 负责: IPC 通信(加载 Agent 列表/配置、保存配置、重载、恢复备份)
//       以及双向绑定工具函数(formToJSON / jsonToForm)。

import { ipcRenderer } from '../shared/ipc';
import { AgentProfile, AgentSchema, ConfigField, ConfigSection } from '../components/settings/agent-config/types';
import { getSchema } from '../components/settings/agent-config/schemas';

let currentAgentId: string = '';
let agents: AgentProfile[] = [];
let configLoadCallback: ((jsonStr: string) => void) | null = null;
let agentsLoadCallback: ((agents: AgentProfile[]) => void) | null = null;

export function initAgentConfig(): void {
  ipcRenderer.on('externalconfig:agents-res', (_event: any, data: AgentProfile[]) => {
    agents = data || [];
    if (agentsLoadCallback) agentsLoadCallback(agents);
  });

  ipcRenderer.on('externalconfig:config-res', (_event: any, data: { agentId: string; success: boolean; jsonStr?: string; error?: string }) => {
    if (data.agentId !== currentAgentId) return;
    if (configLoadCallback && data.success && data.jsonStr !== undefined) {
      configLoadCallback(data.jsonStr);
    }
  });

  // 延迟请求，确保 Wails runtime 已就绪（与 ipc.ts 的 initWailsReady 机制对齐）
  requestAgents();
}

function requestAgents(): void {
  try {
    ipcRenderer.send('externalconfig:get-agents');
  } catch (e) {
    // runtime 尚未就绪，延迟重试
    setTimeout(() => requestAgents(), 500);
  }
}

export function setConfigLoadCallback(cb: (jsonStr: string) => void): void {
  configLoadCallback = cb;
}

export function setAgentsLoadCallback(cb: (agents: AgentProfile[]) => void): void {
  agentsLoadCallback = cb;
}

export function getAgents(): AgentProfile[] {
  return agents;
}

export function selectAgent(agentId: string): void {
  currentAgentId = agentId;
  ipcRenderer.send('externalconfig:get-config', agentId);
}

export function getCurrentAgentId(): string {
  return currentAgentId;
}

export function getAgentSchema(agentId: string): AgentSchema | null {
  return getSchema(agentId);
}

export async function saveConfig(agentId: string, jsonStr: string): Promise<{ success: boolean; error?: string }> {
  try {
    const res = await ipcRenderer.invoke('externalconfig:save-config', agentId, jsonStr);
    return res;
  } catch (err) {
    return { success: false, error: String(err) };
  }
}

export async function reloadConfig(agentId: string): Promise<{ success: boolean; jsonStr?: string; error?: string }> {
  try {
    return await ipcRenderer.invoke('externalconfig:reload-config', agentId);
  } catch (err) {
    return { success: false, error: String(err) };
  }
}

export async function restoreBackup(agentId: string): Promise<{ success: boolean; error?: string }> {
  try {
    return await ipcRenderer.invoke('externalconfig:restore-backup', agentId);
  } catch (err) {
    return { success: false, error: String(err) };
  }
}

// fetchRelayModels: 拉取中继服务配置的模型映射列表，返回所有 clientModel 名称数组。
// 供 Agent 配置面板的 model-select 下拉使用，让用户直接从中继映射中选择模型。
export async function fetchRelayModels(): Promise<string[]> {
  try {
    const mappings = await ipcRenderer.invoke('relay:get-model-mapping');
    if (!Array.isArray(mappings)) return [];
    return mappings
      .filter((m: any) => m && m.clientModel)
      .map((m: any) => m.clientModel as string);
  } catch (err) {
    console.error('[AgentConfigController] Failed to fetch relay models:', err);
    return [];
  }
}

// fetchAgentModelCatalog: 拉取指定 Agent 的模型 catalog 文件中的模型 slug 列表。
// 目前仅对 Codex 生效（其 config.toml 中通过 model_catalog_json 声明了外部 catalog 文件）。
// 对未声明 catalog 的 Agent（如 Claude Code / OpenCode）返回空数组，向前兼容。
export async function fetchAgentModelCatalog(agentId: string): Promise<string[]> {
  try {
    const res = await ipcRenderer.invoke('externalconfig:get-model-catalog', agentId);
    if (!res || !res.success || !Array.isArray(res.models)) return [];
    return res.models
      .filter((m: any) => m && m.slug)
      .map((m: any) => m.slug as string);
  } catch (err) {
    console.error('[AgentConfigController] Failed to fetch agent model catalog:', err);
    return [];
  }
}

// fetchAgentCatalogFull: 拉取指定 Agent 的 catalog 文件完整 JSON 字符串。
// 供前端可视化编辑「模型列表」section 使用。未声明 catalog 时返回 "{}"。
export async function fetchAgentCatalogFull(agentId: string): Promise<string> {
  try {
    const res = await ipcRenderer.invoke('externalconfig:get-catalog-full', agentId);
    if (!res || !res.success || typeof res.jsonStr !== 'string') return '{}';
    return res.jsonStr;
  } catch (err) {
    console.error('[AgentConfigController] Failed to fetch agent catalog full:', err);
    return '{}';
  }
}

// saveAgentCatalogFull: 将完整 catalog JSON 字符串写入指定 Agent 的 catalog 文件。
export async function saveAgentCatalogFull(agentId: string, jsonStr: string): Promise<{ success: boolean; error?: string }> {
  try {
    return await ipcRenderer.invoke('externalconfig:save-catalog-full', agentId, jsonStr);
  } catch (err) {
    return { success: false, error: String(err) };
  }
}

// ===== OpenCode Provider AI 一键生成 =====

// DEFAULT_PROVIDER_GEN_PROMPT: AI 生成 provider 配置的默认系统提示词模板。
// 用户可在弹窗里编辑此模板后再生成。要求 AI 只输出 JSON、走本地中继。
export const DEFAULT_PROVIDER_GEN_PROMPT = `你是 OpenCode 配置生成专家。根据用户的需求描述,输出一个合法的 opencode.json 中单个 provider 的 JSON 配置片段。

严格要求:
1. 只输出一个 JSON 对象,绝对不要 markdown 代码块标记(\`\`\`),不要任何解释性前言或总结,回答的第一个字符必须是 '{'。
2. JSON 顶层结构为 { "providerName": { ... } },providerName 用 kebab-case 简短命名(如 deepseek、openai-compatible)。
3. provider 对象内必须包含以下字段:
   - "npm": AI SDK 的 npm 包名。OpenAI 兼容接口用 "@ai-sdk/openai-compatible";Anthropic 原生用 "@ai-sdk/anthropic";Google 用 "@ai-sdk/google"。
   - "api": 协议类型,取值 "openai" 或 "anthropic" 或 "google"。
   - "options": { "baseURL": "<上游 baseURL>", "apiKey": "<留空字符串或环境变量引用>" }
   - "models": { "<模型id>": { ...模型属性... } },至少包含一个模型条目。
4. 每个模型条目可包含: name(显示名)、attachment(是否支持附件 bool)、reasoning(是否支持推理 bool)、tool_call(是否支持工具调用 bool)、temperature(是否支持温度 bool)、limit:{context, output}、cost:{input, output}、options:{reasoningEffort}。
5. baseURL 若用户未指定上游,默认指向本地中继服务: http://127.0.0.1:18444/v1(经中继路由到各上游)。若用户指定了第三方上游(如 https://api.deepseek.com),baseURL 用该上游地址。
6. apiKey 一律留空字符串 ""(由中继服务注入鉴权),不要编造密钥。
7. 模型 id 以用户消息中给出的【强约束】模型列表为准(若提供):models 的 key 必须逐字使用列表中的模型 id,禁止增删改,禁止使用任何列表外的模型 id。若用户未提供列表,模型 id 必须与上游服务真实支持的模型 id 一致。

示例输出形态(仅供参考结构,实际按用户需求填充):
{ "deepseek": { "npm": "@ai-sdk/openai-compatible", "api": "openai", "options": { "baseURL": "http://127.0.0.1:18444/v1", "apiKey": "" }, "models": { "deepseek-chat": { "name": "DeepSeek Chat", "tool_call": true, "reasoning": false, "temperature": true, "limit": { "context": 64000, "output": 8192 } } } } }`;

// generateOpenCodeProvider: 调后端让 AI 生成一份 provider.{name} JSON 片段。
// model 为中继暴露的模型名, systemPrompt 为可编辑的提示词模板, userInput 为用户补充需求,
// selectedModels 为用户多选的真实模型列表(非空时后端强约束 AI 只为这些模型生成条目)。
// 返回 AI 输出的原始文本(预期是 JSON), 由调用方 JSON.parse 校验后回填。
export async function generateOpenCodeProvider(
  model: string,
  systemPrompt: string,
  userInput: string,
  selectedModels: string[] = [],
): Promise<{ success: boolean; content?: string; error?: string }> {
  try {
    return await ipcRenderer.invoke('externalconfig:ai-generate-provider', model, systemPrompt, userInput, selectedModels);
  } catch (err) {
    return { success: false, error: String(err) };
  }
}

// fetchTopRelayModels: 拉取中继调用量 Top 模型统计(跨用户聚合, 按请求次数降序)。
// 供 AI 生成弹窗默认预选调用量前十的模型, 避免预选到已下架/无流量的模型。
export async function fetchTopRelayModels(): Promise<{ model: string; requests: number; inputTokens: number; outputTokens: number }[]> {
  try {
    const res = await ipcRenderer.invoke('externalconfig:get-top-models');
    if (!res || !res.success || !Array.isArray(res.models)) return [];
    return res.models;
  } catch (err) {
    console.error('[AgentConfigController] Failed to fetch top relay models:', err);
    return [];
  }
}

// ===== 双向绑定工具 =====

// getNestedValue: 按点分路径从 JSON 对象中取值。
function getNestedValue(obj: any, path: string): any {
  const parts = path.split('.');
  let cur = obj;
  for (const p of parts) {
    if (cur == null) return undefined;
    cur = cur[p];
  }
  return cur;
}

// setNestedValue: 按点分路径写入 JSON 对象。
function setNestedValue(obj: any, path: string, value: any): void {
  const parts = path.split('.');
  let cur = obj;
  for (let i = 0; i < parts.length - 1; i++) {
    if (cur[parts[i]] == null || typeof cur[parts[i]] !== 'object') {
      cur[parts[i]] = {};
    }
    cur = cur[parts[i]];
  }
  cur[parts[parts.length - 1]] = value;
}

// resolveKey: 将 {name} 占位符替换为实际 key 值。
function resolveKey(keyTemplate: string, nameMap: Record<string, string>): string {
  let resolved = keyTemplate;
  for (const [placeholder, actual] of Object.entries(nameMap)) {
    resolved = resolved.replace('{name}', actual);
  }
  return resolved;
}

// formToJSON: 根据表单数据(键值对)和 schema 生成 JSON 对象。
// formData: { [resolvedKey]: value }
export function formToJSON(formData: Record<string, any>, schema: AgentSchema | null): string {
  if (!schema) return '{}';
  const result: any = {};

  for (const section of schema.sections) {
    if (section.repeatable) {
      // repeatable section: 从 formData 中提取所有 {name} 值
      const prefix = getSectionPrefix(section);
      const names = extractRepeatableNames(formData, prefix, section);

      // 准备 prefix 容器，例如 result['models'] 或 result['model_providers']
      const prefixParts = prefix.split('.');
      let prefixContainer: any = result;
      for (const p of prefixParts) {
        if (prefixContainer[p] == null || typeof prefixContainer[p] !== 'object') {
          prefixContainer[p] = {};
        }
        prefixContainer = prefixContainer[p];
      }

      for (const name of names) {
        if (prefixContainer[name] == null || typeof prefixContainer[name] !== 'object') {
          prefixContainer[name] = {};
        }
        const itemContainer = prefixContainer[name];
        const nameMap: Record<string, string> = { '{name}': name };

        for (const field of section.fields) {
          if (field.type === 'repeatable-object' && field.children) {
            // 嵌套 repeatable-object: 递归写入
            const modelsPath = field.key.replace('{name}', name);
            const fieldSuffix = field.key.substring(field.key.indexOf('{name}') + '{name}'.length);
            const relativeKey = fieldSuffix.startsWith('.') ? fieldSuffix.substring(1) : fieldSuffix;
            if (relativeKey) {
              if (itemContainer[relativeKey] == null || typeof itemContainer[relativeKey] !== 'object') {
                itemContainer[relativeKey] = {};
              }
              writeRepeatableObjectIntoContainer(formData, itemContainer[relativeKey], modelsPath, field);
            } else {
              writeRepeatableObjectIntoContainer(formData, itemContainer, modelsPath, field);
            }
          } else {
            const resolvedKey = resolveKey(field.key, nameMap);
            const rawVal = formData[resolvedKey];
            if (rawVal === undefined) continue;
            const processedVal = processValueForJSON(field, rawVal);
            const fieldSuffix = field.key.substring(field.key.indexOf('{name}') + '{name}'.length);
            const relativeKey = fieldSuffix.startsWith('.') ? fieldSuffix.substring(1) : fieldSuffix;
            if (relativeKey) {
              setNestedValue(itemContainer, relativeKey, processedVal);
            } else {
              prefixContainer[name] = processedVal;
            }
          }
        }
      }
    } else {
      for (const field of section.fields) {
        const rawVal = formData[field.key];
        if (rawVal === undefined) continue;
        const processedVal = processValueForJSON(field, rawVal);
        setNestedValue(result, field.key, processedVal);
      }
    }
  }

  return JSON.stringify(result, null, 2);
}

// writeRepeatableObjectIntoContainer: 将 formData 中以 basePrefix 为前缀的 repeatable-object
// 字段数据递归写入 container 对象。container 已经是 models 字段所在的结构层容器(如
// provider.antigravityproxy.models 指向的对象), 不再用 split 切, 避免数据键(模型名/变体名)
// 含点号被错误拆开。
//
// basePath 用于构造 formData 子项 key 的前缀(如 "provider.antigravityproxy.models"
// 或 "provider.antigravityproxy.models.gemini-3.7-flash-high.variants"), 切勿使用 split。
function writeRepeatableObjectIntoContainer(formData: Record<string, any>, container: any, basePath: string, field: ConfigField): void {
  if (!field.children) return;
  // formData 子项 key 格式: basePath.{childName}.{childField.key}
  const childPrefix = basePath + '.';
  const childSuffixes = collectAllLeafSuffixes(field);
  const childNames = extractRepeatableNamesWithSuffixes(formData, childPrefix, childSuffixes);
  for (const childName of childNames) {
    if (!container[childName] || typeof container[childName] !== 'object') {
      container[childName] = {};
    }
    for (const childField of field.children) {
      if (childField.type === 'repeatable-object' && childField.children) {
        // 递归写入嵌套的 repeatable-object 子字段。
        // 嵌套 container = container[childName][childField.key], 这样构建不依赖 split,
        // 模型名/变体名即便含点号也作为单一 key 直接写入。
        if (!container[childName][childField.key] || typeof container[childName][childField.key] !== 'object') {
          container[childName][childField.key] = {};
        }
        const nestedBasePath = `${childPrefix}${childName}.${childField.key}`;
        writeRepeatableObjectIntoContainer(formData, container[childName][childField.key], nestedBasePath, childField);
      } else {
        const resolvedKey = `${childPrefix}${childName}.${childField.key}`;
        const rawVal = formData[resolvedKey];
        if (rawVal === undefined) continue;
        const processedVal = processValueForJSON(childField, rawVal);
        setNestedValue(container[childName], childField.key, processedVal);
      }
    }
  }
}

function getSectionPrefix(section: ConfigSection): string {
  if (!section.fields.length) return '';
  const firstKey = section.fields[0].key;
  const idx = firstKey.indexOf('{name}');
  if (idx < 0) return '';
  // Strip trailing dot to avoid getNestedValue splitting on trailing dot
  // e.g. "provider.{name}.npm" -> "provider." -> should become "provider"
  const prefix = firstKey.substring(0, idx);
  return prefix.endsWith('.') ? prefix.slice(0, -1) : prefix;
}

function extractRepeatableNames(formData: Record<string, any>, prefix: string, section?: ConfigSection): string[] {
  const normalSuffixes: string[] = [];
  const roSubPaths: string[] = [];

  if (section && section.fields) {
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
  }

  const names = new Set<string>();
  const prefixDot = prefix ? prefix + '.' : '';
  for (const key of Object.keys(formData)) {
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
    if (!section || (!normalSuffixes.length && !roSubPaths.length)) {
      const dotIdx = afterPrefix.indexOf('.');
      if (dotIdx > 0) {
        names.add(afterPrefix.substring(0, dotIdx));
      }
    }
  }
  return Array.from(names);
}

// collectAllLeafSuffixes: 递归收集一个 repeatable-object 字段所有叶子子字段的后缀（含嵌套展开）。
// 对于直接子字段（非 repeatable-object），后缀就是 '.' + childField.key。
// 对于嵌套 repeatable-object 子字段，后缀形如 '.childKey.*.leafKey'
// （用 '*' 通配表示嵌套的 variant 子项名）。
function collectAllLeafSuffixes(field: ConfigField): string[] {
  if (!field.children) return [];
  const suffixes: string[] = [];
  for (const child of field.children) {
    if (child.type === 'repeatable-object' && child.children && child.children.length > 0) {
      // 嵌套 repeatable-object: 递归收集叶子后缀，并在前面加上 .childKey.* 通配段
      const nestedLeaves = collectAllLeafSuffixes(child);
      for (const leaf of nestedLeaves) {
        // leaf 形如 '.disabled' → 完整路径 '.variants.*.disabled'
        suffixes.push('.' + child.key + '.*' + leaf);
      }
    } else {
      suffixes.push('.' + child.key);
    }
  }
  return suffixes;
}

// extractRepeatableNamesWithSuffixes: 利用已知叶子子字段后缀来精确分割模型名。
// suffixes 可能含 '*' 通配段（用于嵌套 repeatable-object）。
// 例如 suffixes = ['.name', '.attachment', '.variants.*.disabled']
// afterPrefix = 'gemini-3.7-flash-high.variants.high.disabled'
// 匹配 '.variants.*.disabled' → 通配 '*' = 'high'，模型名 = 'gemini-3.7-flash-high'
function extractRepeatableNamesWithSuffixes(formData: Record<string, any>, prefix: string, suffixes: string[]): string[] {
  const names = new Set<string>();
  for (const key of Object.keys(formData)) {
    if (!key.startsWith(prefix)) continue;
    const afterPrefix = key.substring(prefix.length);
    let found = false;
    for (const suffix of suffixes) {
      const namePart = matchSuffix(afterPrefix, suffix);
      if (namePart) {
        names.add(namePart);
        found = true;
        break;
      }
    }
    if (!found) {
      const dotIdx = afterPrefix.indexOf('.');
      if (dotIdx >= 0) names.add(afterPrefix.substring(0, dotIdx));
    }
  }
  return Array.from(names);
}

// matchSuffix: 检查 afterPrefix 是否以 suffix 结尾（含通配 '*'），返回模型名部分。
// suffix 可能形如 '.disabled' 或 '.variants.*.disabled'。
// 若不含 '*'，直接endsWith检查；若含 '*'，则按点分段逐个匹配。
function matchSuffix(afterPrefix: string, suffix: string): string | null {
  if (!suffix.includes('*')) {
    // 简单 endsWith 匹配
    if (afterPrefix.endsWith(suffix)) {
      const namePart = afterPrefix.substring(0, afterPrefix.length - suffix.length);
      if (namePart && !namePart.endsWith('.')) return namePart;
    }
    return null;
  }
  // 含 '*' 通配：按点分段，逐个匹配
  // suffix 形如 ".variants.*.disabled"，afterPrefix 形如 "gemini-3.7-flash-high.variants.high.disabled"
  // 从末尾按各段匹配，'*' 段匹配任意非点串
  const suffixParts = suffix.split('.'); // ['', 'variants', '*', 'disabled']
  // afterPrefix 末尾也得有相同数量的 dot 分段（'*' 对应 1 段）
  // 检查每段（逆序），'*' 匹配任意非空无点段，其余段精确匹配
  const apParts = afterPrefix.split('.'); // ['gemini-3.7','flash','high','variants','high','disabled'] — 哦不，gemini-3.7 会拆开
  // 问题：模型名含点号时，apParts 会被错误拆分。只能在确保 suffix 内每段都不为 '*' 时用 endsWith 段匹配，
  // 用 '*' 段的右侧段反推 '*': 找 '*' 段的右邻段在 afterPrefix 中的最后出现，取 '*' 前缀段作为 "*"
  // 找 '*' 在 suffixParts 中的位置
  const starIdx = suffixParts.indexOf('*');
  if (starIdx < 0) return null;
  // suffix 由 3 部分构成: [0..starIdx) 固定前缀(含尾点) | '*' 段 | (starIdx+1..] 固定后缀(含头点)
  const fixedPrefix = suffixParts.slice(0, starIdx).join('.'); // '.variants' or '' if starIdx=0
  const fixedSuffix = suffixParts.slice(starIdx + 1).join('.'); // '.disabled' — 含头点
  // afterPrefix 须以 fixedSuffix 结尾，且 fixedPrefix 段需精确出现在 '*' 段之前
  if (!afterPrefix.endsWith(fixedSuffix)) return null;
  // 去掉 fixedSuffix 尾部
  const withoutSuffix = afterPrefix.substring(0, afterPrefix.length - fixedSuffix.length);
  // withoutSuffix 形如 'gemini-3.7-flash-high.variants.high'（对我们例子）
  // 现在需检查 fixedPrefix 段（如 '.variants'）出现在 '*' 段之前
  // 去掉 fixedPrefix 末尾：取 '*' 段前缀 = withoutSuffix 末尾部分 = fixedPrefix 之后的部分直至末尾
  // fixedPrefix 须是 withoutSuffix 中最后出现的某子串，其后跟一段 '*' 段
  if (fixedPrefix) {
    // fixedPrefix 须出现在 withoutSuffix 中
    const lastIdx = withoutSuffix.lastIndexOf(fixedPrefix + '.');
    if (lastIdx < 0) return null;
    // '* 段' = fixedPrefix + '.' 后到 withoutSuffix 末尾
    const starValue = withoutSuffix.substring(lastIdx + fixedPrefix.length + 1);
    if (!starValue || starValue.includes('.')) return null; // '*' 匹配一段无点
    // 模型名 = withoutPrefix[0..lastIdx-1]（含去掉 fixedPrefix 尾部的点）
    // lastIdx 之前是模型名
    const namePart = afterPrefix.substring(0, lastIdx); // lastIdx 去掉 ".variants" 段起始
    // 但模型名后面不应是点 — 检查 namePart 不空且不.endsWith('.')
    if (namePart && !namePart.endsWith('.')) return namePart;
    return null;
  } else {
    // fixedPrefix 为空：'*' 在最前面
    // withoutSuffix 已是 '* 段' 本身
    if (!/^[^.]+$/.test(withoutSuffix)) return null;
    return null; // '*' 段不能在最前面 —— 模型名分段不成立
  }
}

function processValueForJSON(field: ConfigField, rawVal: any): any {
  switch (field.type) {
    case 'number':
      return rawVal === '' || rawVal === null ? undefined : Number(rawVal);
    case 'boolean':
      return Boolean(rawVal);
    case 'array':
      if (typeof rawVal === 'string') {
        return rawVal.split('\n').map((s) => s.trim()).filter((s) => s !== '');
      }
      return rawVal;
    case 'kv-list':
      if (typeof rawVal === 'string') {
        const obj: Record<string, string> = {};
        for (const line of rawVal.split('\n')) {
          const eqIdx = line.indexOf('=');
          if (eqIdx > 0) {
            const k = line.substring(0, eqIdx).trim();
            const v = line.substring(eqIdx + 1).trim();
            if (k) obj[k] = v;
          }
        }
        return obj;
      }
      return rawVal;
    default:
      return rawVal;
  }
}

// jsonToForm: 从 JSON 字符串解析值回填到表单数据(键值对)。
export function jsonToForm(jsonStr: string, schema: AgentSchema | null): Record<string, any> {
  if (!schema) return {};
  let parsed: any;
  try {
    parsed = JSON.parse(jsonStr);
  } catch {
    return {};
  }

  const result: Record<string, any> = {};

  for (const section of schema.sections) {
    if (section.repeatable) {
      const prefix = getSectionPrefix(section);
      const container = getNestedValue(parsed, prefix);
      if (container && typeof container === 'object') {
        for (const name of Object.keys(container)) {
          const itemObj = container[name];
          if (itemObj == null) continue;
          const nameMap: Record<string, string> = { '{name}': name };
          for (const field of section.fields) {
            if (field.type === 'repeatable-object' && field.children) {
              const basePath = field.key.replace('{name}', name);
              const fieldSuffix = field.key.substring(field.key.indexOf('{name}') + '{name}'.length);
              const relativeKey = fieldSuffix.startsWith('.') ? fieldSuffix.substring(1) : fieldSuffix;
              const modelsContainer = relativeKey && typeof itemObj === 'object' ? itemObj[relativeKey] : itemObj;
              if (modelsContainer && typeof modelsContainer === 'object') {
                readRepeatableObject(parsed, modelsContainer, result, basePath, field);
              }
            } else {
              const resolvedKey = resolveKey(field.key, nameMap);
              const fieldSuffix = field.key.substring(field.key.indexOf('{name}') + '{name}'.length);
              const relativeKey = fieldSuffix.startsWith('.') ? fieldSuffix.substring(1) : fieldSuffix;
              const val = (relativeKey && typeof itemObj === 'object') ? getNestedValue(itemObj, relativeKey) : itemObj;
              if (val !== undefined) {
                result[resolvedKey] = processValueForForm(field, val);
              }
            }
          }
        }
      }
    } else {
      for (const field of section.fields) {
        const val = getNestedValue(parsed, field.key);
        if (val !== undefined) {
          result[field.key] = processValueForForm(field, val);
        }
      }
    }
  }

  return result;
}

// readRepeatableObject: 递归从解析后的 JSON 对象读取 repeatable-object 子字段数据，
// 回填到 formData（以 basePath 为前缀）。
// rootData: 完整的 parsed JSON（未用，但保留以防未来扩展）
// container: basePath 路径上的容器对象，其 keys 是子项名（如模型名、变体名）
// result: formData 记录，写入键值对
// basePath: 形如 "provider.antigravityproxy.models"
function readRepeatableObject(rootData: any, container: any, result: Record<string, any>, basePath: string, field: ConfigField): void {
  if (!field.children || !container || typeof container !== 'object') return;
  for (const childName of Object.keys(container)) {
    const childObj = container[childName];
    if (!childObj || typeof childObj !== 'object') continue;
    for (const childField of field.children) {
      if (childField.type === 'repeatable-object' && childField.children) {
        // 递归读取嵌套 repeatable-object
        // 嵌套容器路径: basePath.{childName}.{childField.key}
        const nestedContainer = childObj[childField.key];
        if (nestedContainer && typeof nestedContainer === 'object') {
          readRepeatableObject(rootData, nestedContainer, result, `${basePath}.${childName}.${childField.key}`, childField);
        }
      } else {
        // 用子字段 key 在 childObj 内取值
        const val = getNestedValue(childObj, childField.key);
        if (val !== undefined) {
          const resolvedKey = `${basePath}.${childName}.${childField.key}`;
          result[resolvedKey] = processValueForForm(childField, val);
        }
      }
    }
  }
}

function processValueForForm(field: ConfigField, val: any): any {
  switch (field.type) {
    case 'array':
      if (Array.isArray(val)) {
        return val.join('\n');
      }
      return '';
    case 'kv-list':
      if (val && typeof val === 'object') {
        return Object.entries(val).map(([k, v]) => `${k}=${v}`).join('\n');
      }
      return '';
    case 'boolean':
      return Boolean(val);
    case 'number':
      return val;
    default:
      return String(val);
  }
}
