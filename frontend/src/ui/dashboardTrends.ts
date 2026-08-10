/**
 * dashboardTrends.ts: 趋势图绘制与节流(自包含 5 个模块级 let + CHART_DRAW_MIN_INTERVAL const)。
 *
 * 从 dashboard.ts 抽离:lastTrendsSig/lastChartRange/lastChartScope/lastChartDrawTs/chartRedrawTimer
 * 5 个 let 仅在 maybeDrawTrendChart / redrawTrendChartAnimated 再赋值,两函数随簇迁出,自包含。
 * maybeDrawTrendChart 由 hub renderActiveView 调;redrawTrendChartAnimated 由 hub switchView 调(切回 dashboard 强制动画)。
 */
import state from './dashboardState';
import * as chartRenderer from './chartRenderer';

// Trend chart redraw throttle: the chart only needs to refresh every few
// seconds, not on every 1s stats-updated tick. Avoids per-second path/string
// rebuilds and onmousemove-closure reassignment.
let lastTrendsSig = '';
let lastChartRange = '';
// lastChartScope: 上次重画时的趋势 scope (all/nvidia), 用于检测 scope 切换并触发动画重画。
let lastChartScope = 'all';
let lastChartDrawTs = 0;
let chartRedrawTimer: any = null;
const CHART_DRAW_MIN_INTERVAL = 3000;
// currentTrendsSource: 按 currentTrendScope 返回当前应喂给趋势图的数据序列。
// 'all' = 综合全局桶 (state.trendsData, 口径零回归);
// 'nvidia' = NVIDIA 号池专用桶 (state.nvidiaTrendsData)。两桶由后端物理隔离下发。
function currentTrendsSource(): any[] {
    return state.currentTrendScope === 'nvidia' ? state.nvidiaTrendsData : state.trendsData;
}

// Throttled trend-chart redraw: re-draw at most once per CHART_DRAW_MIN_INTERVAL,
// with a trailing draw so the final state is always reflected. Range changes
// draw immediately (with left-to-right animation); within-range polling updates
// are coalesced and drawn SILENTLY (no animation) so staying on the page does
// not cause the chart to animate every few seconds. Skips entirely when the
// filtered trends signature has not changed.
export function maybeDrawTrendChart() {
    const src = currentTrendsSource();
    if (!src || src.length === 0) {
        // 切到 scope 后该桶暂无数据 (如 NVIDIA 号池尚无请求): 清空残留曲线避免误读,
        // 并清空 sig 使后续真实数据到来时必定重画。
        chartRenderer.clearTrendChart();
        lastTrendsSig = `scope=${state.currentTrendScope}:empty`;
        return;
    }
    const filteredTrends = chartRenderer.getFilteredTrends(src, state.currentRange);
    const last = filteredTrends[filteredTrends.length - 1];
    // sig 含 scope: 切换 tab 即使数据签名碰巧相同也强制重画, 保证画面与 scope 一致。
    const sig = `scope=${state.currentTrendScope}:${state.currentRange}:${filteredTrends.length}:${last ? `${last.time}_${last.requests}_${last.input}` : ''}`;
    if (sig === lastTrendsSig) return;

    const rangeChanged = state.currentRange !== lastChartRange;
    // scope 切换视为"范围级"变化, 触发左到右动画重画, 给用户明确视觉反馈。
    const scopeChanged = state.currentTrendScope !== lastChartScope;
    const now = Date.now();
    if (rangeChanged || scopeChanged || now - lastChartDrawTs >= CHART_DRAW_MIN_INTERVAL) {
        // rangeChanged=true 或 scopeChanged=true：切范围/切 scope/首进 app → 播左到右动画；
        // 仅 tick 到期但范围与 scope 均未变 → 轮询静默重画，不动画。
        chartRenderer.drawTrendChartSVG(filteredTrends, state.currentRange, rangeChanged || scopeChanged);
        lastTrendsSig = sig;
        lastChartRange = state.currentRange;
        lastChartScope = state.currentTrendScope;
        lastChartDrawTs = now;
        if (chartRedrawTimer) {
            clearTimeout(chartRedrawTimer);
            chartRedrawTimer = null;
        }
    } else if (!chartRedrawTimer) {
        chartRedrawTimer = setTimeout(() => {
            chartRedrawTimer = null;
            maybeDrawTrendChart();
        }, CHART_DRAW_MIN_INTERVAL - (now - lastChartDrawTs));
    }
}

// 强制带动画重画趋势图：跳过 sig 短路与节流，供 switchView 切回 dashboard 时调用，
// 保证每次切回仪表盘都看到一次左到右画线动画（即使 trends 签名未变也不会被短路）。
export function redrawTrendChartAnimated() {
    const src = currentTrendsSource();
    if (!src || src.length === 0) {
        chartRenderer.clearTrendChart();
        lastTrendsSig = `scope=${state.currentTrendScope}:empty`;
        lastChartScope = state.currentTrendScope;
        return;
    }
    const filteredTrends = chartRenderer.getFilteredTrends(src, state.currentRange);
    chartRenderer.drawTrendChartSVG(filteredTrends, state.currentRange, true);
    const last = filteredTrends[filteredTrends.length - 1];
    const sig = `scope=${state.currentTrendScope}:${state.currentRange}:${filteredTrends.length}:${last ? `${last.time}_${last.requests}_${last.input}` : ''}`;
    lastTrendsSig = sig;
    lastChartRange = state.currentRange;
    lastChartScope = state.currentTrendScope;
    lastChartDrawTs = Date.now();
    if (chartRedrawTimer) {
        clearTimeout(chartRedrawTimer);
        chartRedrawTimer = null;
    }
}
