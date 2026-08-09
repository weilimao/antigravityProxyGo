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
 * 三态口径(修复"池桶缺失时冒充全局数"误导, 见用户反馈: 缓存token/节约成本串到 Other·日日新):
 *  1) stats.pools 非空(已启用按池记账) 且选中池桶存在 → 严格用该池真实分子分母
 *     (含 0 值, 不再要求 poolEligible>0 才采用), 各池/组互不串扰;
 *  2) stats.pools 非空但选中池桶缺失 → 该池自筛选上线后从未记账, 诚实显示 0%/$0/0,
 *     不再回退全局 totalCachedTokens 冒充该池(根治"某池无数据却显示全局 9.57B");
 *  3) stats.pools 字段缺失(远端中继模式 app_monitor.go 远端分支不带 Pools) 或为空 map
 *     (刚升级、尚未产生任何池增量) → 保留旧三档全量口径, 与卡片改造前一致, 零回归。
 *
 * valCached / valSavedCost 同步由 totalCached 派生, 三件套口径自洽。
 */
export function computeHitRateByPool(stats: any): void {
    if (!stats) return;

    const poolKey = (state.currentPoolFilter || DEFAULT_POOL);
    const pools = stats.pools;
    // 区分 "远端模式/空 map → 全局兜底" 与 "已启用按池记账 → 严格按池"。
    // pools===undefined/null: 后端未下发 Pools(远端中继模式); {} 空对象: 刚升级尚未增量;
    // 两者都视为"按池口径不可用", 回退旧三档全量, 零回归。
    const hasPoolsField = (pools !== undefined && pools !== null);
    const poolBucketsExist = hasPoolsField && Object.keys(pools).length > 0;
    const ps = hasPoolsField ? pools[poolKey] : undefined;

    let totalCached = 0;
    let hitDenom = 0;

    if (poolBucketsExist && ps && typeof ps === 'object') {
        // 已启用按池记账且该池桶存在: 严格用池真实值(含 0 值), 不再喂全局数。
        totalCached = Number(ps.cachedTokens || 0);
        hitDenom = Number(ps.cacheEligibleInputTokens || 0);
    } else if (!poolBucketsExist) {
        // 远端模式(pools 字段缺失) 或 刚升级尚未产生桶数据: 保留旧三档全局兜底, 零回归。
        totalCached = Number(stats.totalCachedTokens || 0);
        hitDenom = computeLegacyHitDenom(stats);
    }
    // else: poolBucketsExist && ps 缺失 → 该池从未记账, 显式显示 0% / $0 / 0
    //       (不再回退全局冒充该池, 根治"Other·日日新 无数据却显示全局 9.57B")。

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
// 仅在 stats.pools 字段缺失(远端中继模式)或为空 map(刚升级无增量)时使用, 保证这两种场景零回归;
// 一旦按池口径已启用(至少一个池桶存在), 本函数不再触达, 避免尾包全量数冒充某个空池。
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
