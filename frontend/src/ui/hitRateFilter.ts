// hitRateFilter.ts: 仪表盘「缓存命中率」卡片的号池/组筛选下拉 + 按池重算命中率的渲染逻辑。
//
// 设计意图(详见 plan):
//   - 后端 GlobalStats.Pools 提供 per-pool/per-group 子聚合(antigravity / nvidia / other:<groupId>);
//   - 前端 select 切换 state.currentPoolFilter 后, computeHitRateByPool 取对应池桶的
//     CachedTokens(分子) / CacheEligibleInputTokens(分母)独立计算, 各池/组互不串扰;
//   - 兜底: stats.pools 缺失(远端中继模式)或该池暂无数据时, 回退到旧三档全量口径,
//     与卡片改造前显示一致(远端模式本轮零回归)。
//   - valCached / valSavedCost 改为按池分子显示, 三件套(命中率/缓存Token/节省成本)口径自洽。

import state from './dashboardState';
import i18n from '../shared/i18n';
import * as chartRenderer from './chartRenderer';

// 下拉 DOM id(Dashboard.vue 卡片内)。
const SELECT_ID = 'poolFilterSelect';

// 选项 sig 缓存, 用于 dirty-check: 仅当组列表变化时才重建 select, 避免覆盖用户的 hover/选中态。
let lastOptionsSig = '';

// currentPoolFilter 兜底值: 异常或未初始化时归 antigravity(默认链路, 与卡片默认一致)。
const DEFAULT_POOL = 'antigravity';

// other 组 key 前缀(与后端 stats_pool.go otherKeyPrefix 对齐)。
const OTHER_PREFIX = 'other:';

/**
 * 其他池组 key 前缀(供外部按 other: 前缀拆组列表)。与后端 stats_pool.go otherKeyPrefix 对齐。
 */
export const otherPoolKeyPrefix = OTHER_PREFIX;

/**
 * renderPoolFilterSelect 渲染缓存命中率卡片的号池筛选下拉。
 *
 * 选项构成: antigravity(默认) + nvidia + 各 Other 组(动态, 数据源 state.lastBackendData.otherGroups)。
 * 仅当组列表 sig 变化时重建, 否则保留现状(防覆盖用户交互); 重建后选中态对齐 state.currentPoolFilter,
 * 若当前选中组已被删除则回退到 antigravity。
 *
 * 应在 initDashboard 末尾调用一次, 并在 accounts-res / stats-updated 推送后经 dirty-check 触发重建。
 */
export function renderPoolFilterSelect(): void {
    const sel = document.getElementById(SELECT_ID) as HTMLSelectElement | null;
    if (!sel) return;

    // 聚合 Other 组列表(state.lastBackendData.otherGroups 由 accounts-res 事件写入)。
    const groups = collectOtherGroups();

    // 构建 options sig: 组数 + 每组 groupId/groupName 拼接。与上次相同则跳过重建。
    const sig = buildOptionsSig(groups);
    if (sig === lastOptionsSig) {
        // sig 相同但选中态可能与 state 不同步(如外部改了 state), 仅校正 selected 不重建。
        syncSelectionQuiet(sel);
        return;
    }
    lastOptionsSig = sig;

    // 校验当前选中项是否仍存在; 若被删除(组没了)回退 antigravity。
    if (!isPoolKeyValid(state.currentPoolFilter, groups)) {
        state.currentPoolFilter = DEFAULT_POOL;
    }

    // 重建 options。
    const dict = (i18n as any)[state.currentLanguage] || (i18n as any).zh || {};
    const labelAntigravity = dict.antigravityOfficial || 'Antigravity';
    const labelNvidia = dict.nvidiaPool || 'NVIDIA';
    const labelOther = dict.otherPool || 'Other';
    const otherGroupBadge = dict.otherGroupBadge || '组';

    const options: { value: string; label: string }[] = [];
    options.push({ value: 'antigravity', label: labelAntigravity });
    options.push({ value: 'nvidia', label: labelNvidia });
    for (const g of groups) {
        const gid = String(g.groupId || '').toLowerCase();
        if (!gid) continue;
        const gname = g.groupName || g.groupId;
        options.push({
            value: OTHER_PREFIX + gid,
            label: `${labelOther} · ${otherGroupBadge} ${gname}`,
        });
    }

    // 保留当前选中(重建不丢选中态)。
    const prevValue = state.currentPoolFilter || DEFAULT_POOL;
    sel.innerHTML = '';
    for (const opt of options) {
        const el = document.createElement('option');
        el.value = opt.value;
        el.textContent = opt.label;
        if (opt.value === prevValue) {
            el.selected = true;
        }
        sel.appendChild(el);
    }
}

/**
 * bindPoolFilterSelect 为下拉绑定 change 监听(只需绑一次, 重建 options 不重绑)。
 * 切换后立即按池重算命中率, 不等下一帧 stats-updated。
 */
export function bindPoolFilterSelect(): void {
    const sel = document.getElementById(SELECT_ID) as HTMLSelectElement | null;
    if (!sel) return;
    sel.addEventListener('change', () => {
        state.currentPoolFilter = sel.value || DEFAULT_POOL;
        const stats = state.statsData;
        if (stats) {
            computeHitRateByPool(stats);
        }
    });
}

/**
 * computeHitRateByPool 按 state.currentPoolFilter 计算并写入缓存命中率卡片三件套:
 * valHitRate / valCached / valSavedCost / gaugeCircle。
 *
 * 口径优先级:
 *  1) stats.pools[key] 存在且 cacheEligibleInputTokens>0 → 新口径独立分子分母(各池互不串扰);
 *  2) stats.pools 缺失(远端模式)或该池暂无数据 → 回退旧三档全量口径(modelEligibleSum→
 *     totalCacheEligibleInputTokens→totalInputTokens), 与卡片改造前一致。
 *
 * valCached / valSavedCost 同步改为按池分子(或兜底全量分子), 三件套口径自洽。
 */
export function computeHitRateByPool(stats: any): void {
    if (!stats) return;

    const poolKey = (state.currentPoolFilter || DEFAULT_POOL);
    const pools = stats.pools || {};
    const ps = (pools[poolKey] !== undefined) ? pools[poolKey] : undefined;

    let totalCached = 0;
    let hitDenom = 0;
    let usedNewMetric = false;

    if (ps && typeof ps === 'object') {
        const poolCached = Number(ps.cachedTokens || 0);
        const poolEligible = Number(ps.cacheEligibleInputTokens || 0);
        if (poolEligible > 0) {
            totalCached = poolCached;
            hitDenom = poolEligible;
            usedNewMetric = true;
        }
    }

    // 兜底: 远端模式(后端无 pools)或该池暂无数据 → 旧三档全量口径。
    if (!usedNewMetric) {
        totalCached = Number(stats.totalCachedTokens || 0);
        hitDenom = computeLegacyHitDenom(stats);
    }

    let rawHitRate = hitDenom > 0 ? (totalCached / hitDenom * 100) : 0;
    if (rawHitRate > 100) rawHitRate = 100;
    const hitRate = rawHitRate.toFixed(1);

    const valHitRate = document.getElementById('valHitRate');
    const valCached = document.getElementById('valCached');
    const valSavedCost = document.getElementById('valSavedCost');
    const gaugeCircle = document.getElementById('gaugeCircle');

    if (valHitRate) valHitRate.textContent = hitRate + '%';
    if (valCached) valCached.textContent = chartRenderer.formatCompactNumber(totalCached);
    if (valSavedCost) valSavedCost.textContent = `$${(totalCached * 0.3125 / 1000000).toFixed(2)}`;
    if (gaugeCircle) gaugeCircle.setAttribute('stroke-dasharray', `${hitRate}, 100`);
}

// collectOtherGroups 从 state.lastBackendData.otherGroups 安全提取组列表(数组/非空过滤)。
function collectOtherGroups(): any[] {
    const raw = (state.lastBackendData as any);
    if (!raw) return [];
    const arr = raw.otherGroups;
    if (!Array.isArray(arr)) return [];
    return arr.filter((g: any) => g && g.groupId);
}

// buildOptionsSig 构建组列表签名(组数 + 每组 groupId/groupName 拼接), 供 dirty-check。
function buildOptionsSig(groups: any[]): string {
    const parts = groups.map((g: any) => `${g.groupId}|${g.groupName || ''}`).join(',');
    return `cnt=${groups.length};${parts}`;
}

// syncSelectionQuiet 校正 select 的 selected 与 state.currentPoolFilter 一致(不重建 options)。
function syncSelectionQuiet(sel: HTMLSelectElement): void {
    const target = state.currentPoolFilter || DEFAULT_POOL;
    for (let i = 0; i < sel.options.length; i++) {
        sel.options[i].selected = (sel.options[i].value === target);
    }
}

// isPoolKeyValid 判定当前 filter 仍存在于选项集(组可能被删)。固定项 antigravity/nvidia 恒真。
function isPoolKeyValid(key: string, groups: any[]): boolean {
    if (!key) return true; // 空会兜底到 antigravity
    if (key === 'antigravity' || key === 'nvidia') return true;
    if (key.indexOf(OTHER_PREFIX) === 0) {
        const gid = key.slice(OTHER_PREFIX.length);
        return groups.some((g: any) => String(g.groupId || '').toLowerCase() === gid);
    }
    return false;
}

// computeLegacyHitDenom 复刻改造前 dashboard.ts:1445-1471 的旧三档兜底分母:
// 1) models 里 cachedTokens>0 的模型 inTokens 之和; 2) totalCacheEligibleInputTokens; 3) totalInputTokens。
// 仅在 stats.pools 缺失(远端模式)或该池无数据时使用, 保证远端模式零回归。
function computeLegacyHitDenom(stats: any): number {
    const totalCached = Number(stats.totalCachedTokens || 0);
    let hitDenom = 0;

    if (stats.models && typeof stats.models === 'object') {
        let modelEligibleSum = 0;
        for (const mKey in stats.models) {
            const m = stats.models[mKey];
            if (m) {
                const cTokens = Number(m.cachedTokens || m.CachedTokens || 0);
                const iTokens = Number(m.inTokens || m.InTokens || 0);
                if (cTokens > 0) {
                    modelEligibleSum += iTokens;
                }
            }
        }
        if (modelEligibleSum >= totalCached && modelEligibleSum > 0) {
            hitDenom = modelEligibleSum;
        }
    }

    if (hitDenom <= 0) {
        const rawCE = Number(stats.totalCacheEligibleInputTokens || stats.cacheEligibleInputTokens || 0);
        if (rawCE >= totalCached && rawCE > 0) {
            hitDenom = rawCE;
        }
    }

    if (hitDenom <= 0) {
        hitDenom = Number(stats.totalInputTokens || 0);
    }

    return hitDenom;
}

// resetPoolFilterSig 重置 dirty-check sig, 供语言切换等需要强制重建选项的场景调用。
export function resetPoolFilterSig(): void {
    lastOptionsSig = '';
}
