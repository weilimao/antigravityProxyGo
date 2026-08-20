/**
 * relayModelDiff.ts: 中继「模型映射」面板「新增/失效」diff 计算 + 行标记 + 清除失效逻辑(从 relayModelMapping.ts 抽离)。
 *
 * 职责内聚:只管「本轮新增集合」「失效映射集合」纯函数 + 徽章/标记构造 + 「清除失效」删除动作。
 * 不持有 allMappings/poolTabs 等模块态 —— 调用方(relayModelMapping)拥有并传参,本模块算完返回结果由调用方落地。
 * 抽离动机:relayModelMapping.ts 已 929 行(逼近硬红线 1000),增量徽章+清除按钮须外抽,严禁继续堆。
 */
import i18n from '../shared/i18n';
import state from './dashboardState';

// 映射条目(与 settings.ModelMappingEntry 对齐的内存态子集,本模块只需 targetModel/clientModel)。
export interface MappingLike {
    clientModel?: string;
    targetModel?: string;
    [k: string]: any;
}

export type DiffKind = 'new' | 'stale';

// normalizeLower:trim + 小写,空串返回空串。与后端 diffRemoteAdded 同口径。
function normalizeLower(s: string): string {
    return (s || '').trim().toLowerCase();
}

// computeRemoteAddedLower 返回「本轮新增」模型 id 的【小写】集合(本次远端 − 上次快照,大小写不敏感)。
// snapshot 为空(首次/旧配置)时本次全集全部视为新增。
export function computeRemoteAddedLower(current: string[], snapshot: string[]): Set<string> {
    const out = new Set<string>();
    if (!current || current.length === 0) return out;
    const snapSet = new Set<string>();
    for (const s of snapshot || []) {
        const k = normalizeLower(s);
        if (k) snapSet.add(k);
    }
    for (const c of current) {
        const k = normalizeLower(c);
        if (k && !snapSet.has(k)) out.add(k);
    }
    return out;
}

// computeStaleMappings 返回「真实目标模型已从远端下架」的映射条目引用列表(按 clientModel/targetModel 原样比对)。
// 口径:TargetModel(真实上游名,裸名)不在本次远端全集 → 失效。大小写不敏感。
// liveRemote 为空(拉取失败/未拉取)时返回空数组(绝不下失效判定,避免误删)。
// 反映用户决策:仅删远端拉取确认失效的(需有成功远端全集作基准)。
export function computeStaleMappings(mappings: MappingLike[], liveRemote: string[]): MappingLike[] {
    if (!liveRemote || liveRemote.length === 0) return [];
    const liveSet = new Set<string>();
    for (const r of liveRemote) {
        const k = normalizeLower(r);
        if (k) liveSet.add(k);
    }
    const stale: MappingLike[] = [];
    for (const m of mappings || []) {
        const tm = normalizeLower((m && m.targetModel) || '');
        if (!tm) continue; // 用户尚未填目标模型,不判失效
        if (!liveSet.has(tm)) stale.push(m);
    }
    return stale;
}

// shouldMarkStale 判断单条映射是否应标失效(供行渲染逐条调用,避免重复建 liveSet)。
// liveSetLower 为调用方预先建好的「本次远端全集小写集合」;空集时恒返 false(未拉取不下判)。
export function shouldMarkStale(mapping: MappingLike, liveSetLower: Set<string>): boolean {
    if (!liveSetLower || liveSetLower.size === 0) return false;
    const tm = normalizeLower((mapping && mapping.targetModel) || '');
    if (!tm) return false;
    return !liveSetLower.has(tm);
}

// shouldMarkNew 判断某 targetModel 是否标新增(供行渲染逐条调用)。
// addedSetLower 为「本轮新增」小写集合(本次远端 − 上次快照)。
export function shouldMarkNew(targetModel: string, addedSetLower: Set<string>): boolean {
    if (!addedSetLower || addedSetLower.size === 0) return false;
    const tm = normalizeLower(targetModel || '');
    if (!tm) return false;
    return addedSetLower.has(tm);
}

// diffI18n 取文案,双通道兜底(与 nvidiaPreferredDiff 同款):优先 i18n 字典 + 中文硬串兜底。
function diffI18n(key: DiffKind): string {
    const dict = (i18n as any)[state.currentLanguage] || (i18n as any).zh || {};
    if (key === 'new') return (dict.relayBadgeNew || '新增');
    return (dict.relayBadgeStale || '远端已删除');
}

// mappingBadgeHTML 生成行内徽章 HTML 片段,供 renderCurrentTabTable 的 innerHTML 字符串拼装(与既有 tr.innerHTML 同款)。
// 注意:此处走 innerHTML 字符串而非 DOM 构造,与 relayModelMapping 既有渲染范式一致,保持单一渲染通道。
export function mappingBadgeHTML(kind: DiffKind): string {
    if (kind === 'new') {
        return `<span class="shrink-0 px-1.5 py-0.5 rounded text-[10px] font-bold bg-green-500/15 text-green-600 dark:text-green-400 border border-green-500/30">${escapeHtmlLocal(diffI18n(kind))}</span>`;
    }
    return `<span class="shrink-0 px-1.5 py-0.5 rounded text-[10px] font-bold bg-red-500/15 text-red-500 dark:text-red-400 border border-red-500/30">${escapeHtmlLocal(diffI18n(kind))}</span>`;
}

// escapeHtmlLocal 转义 HTML 特殊字符,防徽章文案/模型名注入(本模块自含,避免依赖 relayModelMapping 内部函数)。
function escapeHtmlLocal(s: string): string {
    return (s || '')
        .replace(/&/g, '&')
        .replace(/</g, '<')
        .replace(/>/g, '>')
        .replace(/"/g, '"')
        .replace(/'/g, '&#039;');
}

// diffSummaryText 按新增/失效计数生成摘要文案(供面板顶部可选展示)。
export function diffSummaryText(kind: DiffKind, count: number): string {
    const dict = (i18n as any)[state.currentLanguage] || (i18n as any).zh || {};
    if (kind === 'new') {
        const tpl = (dict.relayDiffSummaryNew || '本次新增 {n} 个模型');
        return tpl.replace('{n}', String(count));
    }
    const tpl = (dict.relayDiffSummaryStale || '{n} 条映射目标模型已在远端下架');
    return tpl.replace('{n}', String(count));
}

// buildLiveSetLower 把远端全集模型 id 列表转成小写集合,供 shouldMarkStale/shouldMarkNew 行渲染复用。
export function buildLiveSetLower(liveRemote: string[] | null): Set<string> {
    const out = new Set<string>();
    if (!liveRemote) return out;
    for (const r of liveRemote) {
        const k = normalizeLower(r);
        if (k) out.add(k);
    }
    return out;
}

// buildStaleConfirmPrompt 生成「清除失效」确认弹窗文本,列出待删映射的 clientModel→targetModel,逐行。
export function buildStaleConfirmPrompt(stale: MappingLike[]): string {
    const dict = (i18n as any)[state.currentLanguage] || (i18n as any).zh || {};
    const tpl = (dict.relayClearStaleConfirm ||
        '检测到 {n} 条映射的「真实目标模型」已从远端下架,将从列表删除(未保存不落盘):\n{list}');
    const list = stale
        .map(s => `${escapeHtmlLocal((s.clientModel || '').trim())} → ${escapeHtmlLocal((s.targetModel || '').trim())}`)
        .join('\n');
    return tpl.replace('{n}', String(stale.length)).replace('{list}', list);
}
