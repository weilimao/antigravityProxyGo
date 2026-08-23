/**
 * dashboardModelPerf.ts: 请求日志「按模型聚合性能统计」条。
 *
 * 在「综合趋势 → 请求日志」选项卡的搜索控制条下方展示一条横向 chip 列表,
 * 基于当前已加载的 state.allRequests(后端滚动窗口,截图所示"近 150 条")
 * 按 model 字段分组,计算每个模型的:
 *   - 请求次数 (count)
 *   - 平均首帧响应时长 (avg firstByteMs)
 *   - 平均总耗时 (avg durationMs)
 *
 * 口径约定(与下方"请求日志"表格严格一致):
 *   - 数据源:renderLogsTable 已对 state.allRequests 应用搜索/状态过滤后的 filtered，
 *     本模块接收同一份数组, 因此统计条始终与用户当前看到的表格行同步;
 *   - 不调用 mergeRetryRows —— 客户端发起的多次独立 HTTP 请求(无论是否被 UI 折叠)
 *     在统计上仍是多次真实事件, 折叠仅用于视觉;
 *   - 分组 key 使用 log.model 裸名, 与「模型统计」选项卡同口径,
 *     相同 model 不同 reasoningEffort 后缀会被合并为一组;
 *   - firstByteMs/durationMs 缺失或非正数的行不计入对应均值(避免脏数据污染),
 *     但 count 仍记录原始条数。
 *
 * 性能/内存:数据规模约 ≤150 条, O(n) 单次扫描 + 极少 DOM 重建, 开销可忽略。
 * 模块对外仅暴露 renderModelPerfBar, 无副作用可独立测试 computeModelPerf。
 */

import i18n from '../shared/i18n';
import state from './dashboardState';
import { formatDuration } from './dashboardUtils';

export interface ModelPerfEntry {
    model: string;
    count: number;          // 该模型在样本中的总条数
    ttfbCount: number;      // 有效 firstByteMs 样本数
    durationCount: number;  // 有效 durationMs 样本数
    avgFirstByteMs: number; // 平均首帧(ms),无样本时为 0
    avgDurationMs: number;  // 平均耗时(ms),无样本时为 0
}

/**
 * computeModelPerf: 按 model 分组聚合 firstByteMs/durationMs 平均值。
 * 纯函数,不依赖 DOM/state;传入即下方表格当前所见的数组(已搜索/已过滤)。
 */
export function computeModelPerf(logs: any[]): ModelPerfEntry[] {
    if (!Array.isArray(logs) || logs.length === 0) return [];
    const map = new Map<string, {
        count: number;
        ttfbSum: number; ttfbCount: number;
        durSum: number; durCount: number;
    }>();
    for (const log of logs) {
        if (!log) continue;
        const model: string = typeof log.model === 'string' && log.model ? log.model : '-';
        let bucket = map.get(model);
        if (!bucket) {
            bucket = { count: 0, ttfbSum: 0, ttfbCount: 0, durSum: 0, durCount: 0 };
            map.set(model, bucket);
        }
        bucket.count += 1;
        const fb = log.firstByteMs;
        if (typeof fb === 'number' && !isNaN(fb) && fb > 0) {
            bucket.ttfbSum += fb;
            bucket.ttfbCount += 1;
        }
        const dm = log.durationMs;
        if (typeof dm === 'number' && !isNaN(dm) && dm > 0) {
            bucket.durSum += dm;
            bucket.durCount += 1;
        }
    }
    const out: ModelPerfEntry[] = [];
    map.forEach((v, k) => {
        out.push({
            model: k,
            count: v.count,
            ttfbCount: v.ttfbCount,
            durationCount: v.durCount,
            avgFirstByteMs: v.ttfbCount > 0 ? v.ttfbSum / v.ttfbCount : 0,
            avgDurationMs: v.durCount > 0 ? v.durSum / v.durCount : 0,
        });
    });
    // 排序: 先按请求次数降序, 平手按平均耗时降序, 便于用户快速识别热点模型。
    out.sort((a, b) => {
        if (b.count !== a.count) return b.count - a.count;
        return b.avgDurationMs - a.avgDurationMs;
    });
    return out;
}

/**
 * ttfbClassFor: 首帧均值配色, 与请求日志表「首帧响应时间」列规则一致:
 *   <=1000ms → 绿(emerald), >=15000ms → 红(rose), >=5000ms → 黄(amber), 其他 → 灰(slate)。
 */
function ttfbClassFor(ms: number): string {
    if (ms >= 15000) return 'text-rose-500 dark:text-rose-400 font-bold';
    if (ms >= 5000) return 'text-amber-600 dark:text-amber-400 font-semibold';
    if (ms > 0 && ms <= 1000) return 'text-emerald-600 dark:text-emerald-400 font-semibold';
    return 'text-slate-700 dark:text-slate-300';
}

/**
 * renderModelPerfBar: 把聚合结果渲染到 #modelPerfBar 容器。
 * 容器不存在则静默返回(防卸载时崩溃);空数据时显示一行 empty 提示。
 * 每次调用全量重建 innerHTML —— 数据量 ≤150, 重建成本可忽略,
 * 换来的是渲染分支极少、无 slot 池/无跨渲染脏态。
 */
export function renderModelPerfBar(logs: any[]): void {
    const container = document.getElementById('modelPerfBar');
    if (!container) return;

    const dict: any = (i18n as any)[state.currentLanguage] || {};
    const isZh = state.currentLanguage === 'zh';
    const entries = computeModelPerf(logs);

    // 仅在请求日志 tab 激活时显示; 模型统计 / 计费配置 tab 下隐藏
    const logsContent = document.getElementById('logsContent');
    const logsVisible = !!(logsContent && !logsContent.classList.contains('hidden'));
    container.style.display = logsVisible ? '' : 'none';
    if (!logsVisible) return;

    if (entries.length === 0) {
        const emptyText = dict.modelPerfEmpty || (isZh ? '暂无数据' : 'No data');
        container.innerHTML = `<div class="px-3 py-2 text-[11px] text-outline dark:text-outline-variant/70 font-medium">${emptyText}</div>`;
        return;
    }

    const titleText = dict.modelPerfTitle || (isZh ? '按模型性能统计 (近 {n} 条)' : 'Per-model Performance (last {n})');
    const colTtfb = dict.colModelPerfAvgTtfb || (isZh ? '平均首帧' : 'Avg First Byte');
    const colDur = dict.colModelPerfAvgDuration || (isZh ? '平均耗时' : 'Avg Duration');

    const chips = entries.map(e => {
        const ttfbText = formatDuration(e.avgFirstByteMs);
        const durText = formatDuration(e.avgDurationMs);
        const ttfbCls = ttfbClassFor(e.avgFirstByteMs);
        // 转义模型名防 XSS (model 名后端可控, 理论可信但 innerHTML 拼串仍应防御)
        const escModel = e.model.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
        return `
            <div class="inline-flex items-center gap-2 px-2.5 py-1 rounded-md bg-slate-100/80 dark:bg-white/[0.04] border border-outline-variant/40 text-[11px] font-data-mono whitespace-nowrap" title="${escModel}">
                <span class="font-semibold text-slate-800 dark:text-slate-100">${escModel}</span>
                <span class="text-slate-400 dark:text-slate-500">×${e.count}</span>
                <span class="text-slate-400 dark:text-slate-500">·</span>
                <span class="text-slate-500 dark:text-slate-400">${colTtfb}</span>
                <span class="${ttfbCls}">${ttfbText}</span>
                <span class="text-slate-400 dark:text-slate-500">·</span>
                <span class="text-slate-500 dark:text-slate-400">${colDur}</span>
                <span class="text-slate-700 dark:text-slate-200 font-semibold">${durText}</span>
            </div>
        `.trim();
    }).join('');

    container.innerHTML = `
        <div class="px-3 py-2 border-b border-outline-variant/30 bg-slate-50/40 dark:bg-white/[0.015]">
            <div class="text-[10px] font-bold text-outline dark:text-outline-variant/80 uppercase tracking-wider mb-1.5">
                ${titleText.replace('{n}', String(logs.length))}
            </div>
            <div class="flex flex-wrap items-center gap-1.5">${chips}</div>
        </div>
    `.trim();
}
