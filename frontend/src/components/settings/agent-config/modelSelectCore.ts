// modelSelectCore.ts: 供 ModelSearchSelect 及三个调用面共用的纯函数逻辑。
// 不依赖 Vue / DOM，可被 Jest 直接测试；组件层只做事件绑定与渲染。

export interface MappingLike {
    clientModel?: string;
    targetModel?: string;
    expose?: boolean;
    targetProvider?: string;
}

/**
 * uniqueSorted: 去重 + 按 locale 排序（Agent 配置原排序方式）。
 * 空值/纯空白被过滤。
 */
export function uniqueSorted(list: string[]): string[] {
    return Array.from(new Set(list.map(s => (s || '').trim()).filter(Boolean)))
        .sort((a, b) => a.localeCompare(b));
}

/**
 * buildOcrCandidates: OCR 模型下拉候选组装。
 * - 仅取 expose=true 的 clientModel
 * - 追加当前已存值（若值存在且不在候选内，保证可选回显）
 */
export function buildOcrCandidates(
    mappings: MappingLike[],
    currentValue: string,
): string[] {
    const fromRelay = (mappings || [])
        .filter(m => m && m.expose === true && m.clientModel)
        .map(m => String(m.clientModel));
    return uniqueSorted([...fromRelay, currentValue || '']);
}

/**
 * buildSummaryCandidates: 会话压缩/摘要模型候选。
 * - 取映射中的 clientModel，仅在存在真实映射或有效已存值时提供选项
 * - 拒绝硬编码假数据，号池无账号时严格为空
 */
export function buildSummaryCandidates(
    mappings: MappingLike[],
    currentValue: string,
    defaultModels: string[] = [],
): string[] {
    const fromRelay = (mappings || [])
        .map(m => (m && m.clientModel ? String(m.clientModel) : ''))
        .filter(Boolean);
    return uniqueSorted([...fromRelay, ...defaultModels, currentValue || '']);
}

/**
 * buildOverrideTargetCandidates: 全局模型覆写「目标模型 ID」候选。
 * - 优先 relay 的 clientModel，否则 fallback 到当前已存值。
 */
export function buildOverrideTargetCandidates(
    mappings: MappingLike[],
    currentValue: string,
): string[] {
    const fromRelay = (mappings || [])
        .map(m => (m && m.clientModel ? String(m.clientModel) : ''))
        .filter(Boolean);
    const base = fromRelay.length ? fromRelay : (currentValue ? [currentValue] : []);
    return uniqueSorted([...base, currentValue || '']);
}

/**
 * buildRelayMappingTargetCandidates: 中继映射表格「真实目标模型 / 注入 Template Kwargs」候选。
 * @param channelModels 当前号池「获取号池模型」返回的远端全集
 * @param currentValue 当前行已选（可能不在 channelModels 时仍要回显）
 */
export function buildRelayMappingTargetCandidates(
    channelModels: string[],
    currentValue: string,
): string[] {
    return uniqueSorted([...(channelModels || []), currentValue || '']);
}

/**
 * filterModels: ModelSearchSelect 内置的过滤逻辑（忽略大小写、包含匹配）。
 * 与组件内实现保持完全一致的语义，供单元测试直接断言。
 */
export function filterModels(options: string[], query: string): string[] {
    const q = (query || '').trim().toLowerCase();
    if (!q) return options || [];
    return (options || []).filter(item => (item || '').toLowerCase().includes(q));
}
