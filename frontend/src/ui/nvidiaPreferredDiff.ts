/**
 * nvidiaPreferredDiff.ts: NVIDIA 专属模型弹窗「新增/失效」diff 计算与徽章构造(从 nvidiaPreferredShuttle.ts 抽离)。
 *
 * 职责内聚:只管「本轮新增集合」「失效已选集合」的纯函数计算 + 徽章 DOM 构造 + i18n 文案双通道兜底。
 * 不持有任何 DOM 句柄、不读写模块态列表 —— 集合由调用方(shuttle)持有并传入,徽章由调用方决定挂到哪一行。
 * 抽离动机:nvidiaPreferredShuttle.ts 增量加徽章会逼近 500 行软线,按守 500 拆此纯算法模块。
 */
import i18n from '../shared/i18n';
import state from './dashboardState';

// diff 标记种类:右侧候选列用 'new'(本次远端新增),左侧已选列用 'stale'(远端已删除)。
export type DiffKind = 'new' | 'stale';

// computeRemoteAddedLower 返回「本轮新增」模型 id 的【小写】集合。
// 口径:本次远端全集 current 中、上次快照 snapshot 里没有的(大小写不敏感比对,与后端 diffRemoteAdded 同口径)。
// snapshot 为空(首次/旧配置)时本次全集全部视为新增。逐项 trim+去重,脏数据不污染。
export function computeRemoteAddedLower(current: string[], snapshot: string[]): Set<string> {
    const out = new Set<string>();
    if (!current || current.length === 0) return out;
    const snapSet = new Set<string>();
    for (const s of snapshot || []) {
        const k = (s || '').trim().toLowerCase();
        if (k) snapSet.add(k);
    }
    for (const c of current) {
        const k = (c || '').trim().toLowerCase();
        if (!k) continue;
        if (!snapSet.has(k)) out.add(k);
    }
    return out;
}

// computeStaleLower 返回「已选但远端已删除」模型 id 的【小写】集合。
// 口径:已选清单 selected 中、本次远端全集 currentRemote 里没有的(大小写不敏感)。currentRemote 全用于比对,
// 不依赖 snapshot —— 远端没就是没了,最稳。currentRemote 为空(未拉取/拉取失败)时返回空集(不误判)。
export function computeStaleLower(selected: string[], currentRemote: string[]): Set<string> {
    const out = new Set<string>();
    if (!selected || selected.length === 0) return out;
    if (!currentRemote || currentRemote.length === 0) return out; // 拉取失败/未拉取 → 不下失效判定
    const liveSet = new Set<string>();
    for (const r of currentRemote) {
        const k = (r || '').trim().toLowerCase();
        if (k) liveSet.add(k);
    }
    for (const s of selected) {
        const k = (s || '').trim().toLowerCase();
        if (!k) continue;
        if (!liveSet.has(k)) out.add(k);
    }
    return out;
}

// diffBadgeI18n 按标记种类取文案,双通道兜底(与 refreshNvidiaPreferredSourceI18n 同款):
// window.__nvidiaPreferredDict 注入失败时回退 i18n 字典,再回退中文硬串。
function diffBadgeI18n(key: DiffKind): string {
    const dict = (i18n as any)[state.currentLanguage] || (i18n as any).zh || {};
    const dl = (window as any).__nvidiaPreferredDict || {};
    if (key === 'new') {
        return (dl.nvidiaPreferredBadgeNew || dict.nvidiaPreferredBadgeNew || '新增');
    }
    return (dl.nvidiaPreferredBadgeStale || dict.nvidiaPreferredBadgeStale || '远端已删除');
}

// createDiffBadge 构造一个徽章 span HTMLElement(新增=绿底,失效=红底),供行内追加。
// 调用方负责挂载位置与显隐。返回独立元素,无副作用。
export function createDiffBadge(key: DiffKind): HTMLSpanElement {
    const badge = document.createElement('span');
    if (key === 'new') {
        badge.className = 'shrink-0 px-1.5 py-0.5 rounded text-[10px] font-bold bg-green-500/15 text-green-600 dark:text-green-400 border border-green-500/30';
    } else {
        badge.className = 'shrink-0 px-1.5 py-0.5 rounded text-[10px] font-bold bg-red-500/15 text-red-500 dark:text-red-400 border border-red-500/30';
    }
    badge.textContent = diffBadgeI18n(key);
    return badge;
}

// diffSummaryText 按新增/失效计数生成摘要文案(供 Modal 顶部状态行可选展示)。
// kind='new' 用 n 共享 nvidiaPreferredDiffSummaryNew;kind='stale' 用 nvidiaPreferredDiffSummaryStale。
export function diffSummaryText(kind: DiffKind, count: number): string {
    const dict = (i18n as any)[state.currentLanguage] || (i18n as any).zh || {};
    const dl = (window as any).__nvidiaPreferredDict || {};
    if (kind === 'new') {
        const tpl = (dl.nvidiaPreferredDiffSummaryNew || dict.nvidiaPreferredDiffSummaryNew || '本次新增 {n} 个模型');
        return tpl.replace('{n}', String(count));
    }
    const tpl = (dl.nvidiaPreferredDiffSummaryStale || dict.nvidiaPreferredDiffSummaryStale || '{n} 个已选模型已在远端删除');
    return tpl.replace('{n}', String(count));
}
