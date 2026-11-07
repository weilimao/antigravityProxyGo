import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';
import i18n from '../shared/i18n';
import * as chartRenderer from './chartRenderer';
import { calculateVirtualWindow } from './useVirtualList';
import { renderAccountsTable, initAccountStatsEvents, clearAccountsVirtualTable } from './dashboardAccountStats';

export const MODEL_ITEM_HEIGHT = 44;
export const MODEL_BUFFER_ROWS = 5;
export const DEFAULT_VIEWPORT_HEIGHT = 440;

let scrollContainer: HTMLElement | null = null;
let modelsTableBody: HTMLElement | null = null;
let totalCountEl: HTMLElement | null = null;
let btnScrollTop: HTMLElement | null = null;

let currentEntries: Array<[string, any]> = [];
let lastStartIndex = -1;
let lastEndIndex = -1;
let scrollTicking = false;
let scrollListenerAttached = false;
let resizeObserver: ResizeObserver | null = null;

/**
 * 纯函数: 对模型统计字典进行过滤与排序
 * 1. 剔除 unknown 且 reqs 为 0 的空模型
 * 2. 优先按总 Tokens (inTokens + outTokens) 倒序排列
 * 3. 次级按请求次数 reqs 倒序排列
 */
export function filterAndSortModelEntries(modelsObj: Record<string, any> | null | undefined): Array<[string, any]> {
    if (!modelsObj || typeof modelsObj !== 'object') {
        return [];
    }

    const entries = Object.entries(modelsObj).filter(([model, data]: [string, any]) => {
        if (!data) return false;
        if (model === 'unknown' && (!data.reqs || data.reqs === 0)) return false;
        return true;
    });

    return entries.sort((a: any, b: any) => {
        const totalA = (a[1].inTokens || 0) + (a[1].outTokens || 0);
        const totalB = (b[1].inTokens || 0) + (b[1].outTokens || 0);
        if (totalB !== totalA) return totalB - totalA;
        return (b[1].reqs || 0) - (a[1].reqs || 0);
    });
}

function resolveDOMElements() {
    if (!modelsTableBody) {
        modelsTableBody = document.querySelector('#modelsTable tbody');
    }
    if (!scrollContainer) {
        scrollContainer = document.getElementById('modelsTableScrollContainer');
    }
    if (!totalCountEl) {
        totalCountEl = document.getElementById('modelStatsTotalCount');
    }
    if (!btnScrollTop) {
        btnScrollTop = document.getElementById('btnScrollToTopModels');
    }
}

/**
 * 基于当前滚动位置和视口大小更新虚拟列表 DOM
 */
export function updateVirtualTable(force: boolean = false): void {
    resolveDOMElements();
    if (!modelsTableBody) return;

    const dict = i18n[state.currentLanguage] || {};
    const totalCount = currentEntries.length;

    if (totalCountEl) {
        totalCountEl.textContent = totalCount.toString();
    }

    if (btnScrollTop) {
        if (totalCount > 12) {
            btnScrollTop.classList.remove('hidden');
            btnScrollTop.classList.add('flex');
        } else {
            btnScrollTop.classList.add('hidden');
            btnScrollTop.classList.remove('flex');
        }
    }

    if (totalCount === 0) {
        modelsTableBody.innerHTML = `<tr><td colspan="8" class="p-8 text-center text-outline dark:text-outline-variant italic">${dict.noData || '暂无数据'}</td></tr>`;
        lastStartIndex = -1;
        lastEndIndex = -1;
        return;
    }

    const scrollTop = scrollContainer ? scrollContainer.scrollTop : 0;
    const viewportHeight = (scrollContainer && scrollContainer.clientHeight > 0)
        ? scrollContainer.clientHeight
        : DEFAULT_VIEWPORT_HEIGHT;

    const win = calculateVirtualWindow(
        totalCount,
        scrollTop,
        viewportHeight,
        MODEL_ITEM_HEIGHT,
        MODEL_BUFFER_ROWS
    );

    if (!force && win.startIndex === lastStartIndex && win.endIndex === lastEndIndex) {
        return;
    }

    lastStartIndex = win.startIndex;
    lastEndIndex = win.endIndex;

    const fragment = document.createDocumentFragment();

    // 顶部虚拟高度占位行
    if (win.topSpacerHeight > 0) {
        const topTr = document.createElement('tr');
        topTr.className = 'vl-top-spacer';
        topTr.style.height = `${win.topSpacerHeight}px`;
        topTr.style.padding = '0';
        topTr.style.border = 'none';
        const td = document.createElement('td');
        td.colSpan = 8;
        td.style.padding = '0';
        td.style.border = 'none';
        td.style.height = `${win.topSpacerHeight}px`;
        topTr.appendChild(td);
        fragment.appendChild(topTr);
    }

    // 可见区域行渲染
    const visibleEntries = currentEntries.slice(win.startIndex, win.endIndex);
    visibleEntries.forEach(([model, data]: [string, any]) => {
        const tr = document.createElement('tr');
        tr.className = 'hover:bg-slate-50 dark:hover:bg-white/5 transition-colors h-[44px]';

        const inTokens = data.inTokens || 0;
        const outTokens = data.outTokens || 0;
        const cachedTokens = data.cachedTokens || 0;
        const reqs = data.reqs || 0;
        const cost = data.cost || 0;

        const modelHitRate = inTokens > 0 ? (cachedTokens / inTokens * 100) : 0;
        const avgCost = reqs > 0 ? (cost / reqs) : 0;
        const totalTokens = inTokens + outTokens;

        tr.innerHTML = `
            <td class="p-3 font-sans font-semibold text-on-surface dark:text-white truncate max-w-[260px]" title="${model}">${model}</td>
            <td class="p-3 text-right">${reqs}</td>
            <td class="p-3 text-right font-semibold" title="${totalTokens.toLocaleString()}">${chartRenderer.formatTokenCount(totalTokens)}</td>
            <td class="p-3 text-right text-outline dark:text-outline-variant" title="${inTokens.toLocaleString()}">${chartRenderer.formatTokenCount(inTokens)}</td>
            <td class="p-3 text-right text-on-surface dark:text-white" title="${outTokens.toLocaleString()}">${chartRenderer.formatTokenCount(outTokens)}</td>
            <td class="p-3 text-right">${modelHitRate.toFixed(1)}%</td>
            <td class="p-3 text-right text-primary dark:text-primary-fixed-dim font-bold">$${cost.toFixed(4)}</td>
            <td class="p-3 text-right text-outline dark:text-outline-variant">$${avgCost.toFixed(5)}</td>
        `;
        fragment.appendChild(tr);
    });

    // 底部虚拟高度占位行
    if (win.bottomSpacerHeight > 0) {
        const bottomTr = document.createElement('tr');
        bottomTr.className = 'vl-bottom-spacer';
        bottomTr.style.height = `${win.bottomSpacerHeight}px`;
        bottomTr.style.padding = '0';
        bottomTr.style.border = 'none';
        const td = document.createElement('td');
        td.colSpan = 8;
        td.style.padding = '0';
        td.style.border = 'none';
        td.style.height = `${win.bottomSpacerHeight}px`;
        bottomTr.appendChild(td);
        fragment.appendChild(bottomTr);
    }

    modelsTableBody.innerHTML = '';
    modelsTableBody.appendChild(fragment);
}

/**
 * 平滑或瞬时滚动至模型列表顶部
 */
export function scrollToTopModels(smooth: boolean = true): void {
    resolveDOMElements();
    if (!scrollContainer) return;

    if (smooth && typeof scrollContainer.scrollTo === 'function') {
        scrollContainer.scrollTo({ top: 0, behavior: 'smooth' });
    } else {
        scrollContainer.scrollTop = 0;
    }
    updateVirtualTable(true);
}

/**
 * 接收后端或过滤后的 stats 载荷，触发虚拟列表重绘
 */
export function renderModelsTable(stats: any): void {
    resolveDOMElements();
    currentEntries = filterAndSortModelEntries(stats ? stats.models : null);

    // 边界自愈: 若当前滚动偏离最大可滚动高度 (例如切换为少模型切片后), 夹回合法最大值
    if (scrollContainer && currentEntries.length > 0) {
        const viewportHeight = scrollContainer.clientHeight || DEFAULT_VIEWPORT_HEIGHT;
        const maxScroll = Math.max(0, currentEntries.length * MODEL_ITEM_HEIGHT - viewportHeight);
        if (scrollContainer.scrollTop > maxScroll) {
            scrollContainer.scrollTop = maxScroll;
        }
    }

    initModelStatsVirtualList();
    updateVirtualTable(true);
}

/**
 * 初始化虚拟滚动容器事件监听与 ResizeObserver 自适应
 */
export function initModelStatsVirtualList(): void {
    resolveDOMElements();

    if (scrollContainer && !scrollListenerAttached) {
        scrollContainer.addEventListener('scroll', () => {
            if (!scrollTicking) {
                window.requestAnimationFrame(() => {
                    updateVirtualTable(false);
                    scrollTicking = false;
                });
                scrollTicking = true;
            }
        }, { passive: true });
        scrollListenerAttached = true;
    }

    if (btnScrollTop && !btnScrollTop.hasAttribute('data-bound')) {
        btnScrollTop.setAttribute('data-bound', 'true');
        btnScrollTop.addEventListener('click', () => {
            scrollToTopModels(true);
        });
    }

    if (typeof ResizeObserver !== 'undefined' && scrollContainer && !resizeObserver) {
        resizeObserver = new ResizeObserver(() => {
            updateVirtualTable(false);
        });
        resizeObserver.observe(scrollContainer);
    }
}

/**
 * 清理虚拟列表监听器与观察者 (Teardown)
 */
export function cleanupModelStatsVirtualList(): void {
    if (resizeObserver) {
        resizeObserver.disconnect();
        resizeObserver = null;
    }
    scrollListenerAttached = false;
    lastStartIndex = -1;
    lastEndIndex = -1;
}

/**
 * 绑定模型统计表的时间范围筛选按钮 (全部 / 今日 / 近三日 / 近七天)
 */

/**
 * 切换模型统计面板的统计维度 (按模型 / 按账号)
 */
export function switchStatDimension(dimension: 'model' | 'account'): void {
    state.currentStatDimension = dimension;

    const dimSelector = document.getElementById('statDimensionSelector');
    if (dimSelector) {
        const buttons = dimSelector.querySelectorAll('button[data-sdim]');
        const activeClass = 'px-2.5 py-0.5 text-[10px] bg-white dark:bg-[#1a1f30] text-primary dark:text-primary-fixed-dim rounded-md shadow-sm font-semibold';
        const inactiveClass = 'px-2.5 py-0.5 text-[10px] text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200 rounded-md transition-all font-medium';
        buttons.forEach((b: any) => {
            b.className = b.getAttribute('data-sdim') === dimension ? activeClass : inactiveClass;
        });
    }

    const modelsContainer = document.getElementById('modelsTableScrollContainer');
    const accountsContainer = document.getElementById('accountsTableScrollContainer');
    const footerModel = document.getElementById('footerModelInfo');
    const footerAccount = document.getElementById('footerAccountInfo');
    const btnTopModels = document.getElementById('btnScrollToTopModels');
    const btnTopAccounts = document.getElementById('btnScrollToTopAccounts');

    if (dimension === 'account') {
        if (modelsContainer) modelsContainer.classList.add('hidden');
        if (accountsContainer) accountsContainer.classList.remove('hidden');
        if (footerModel) footerModel.classList.add('hidden');
        if (footerAccount) {
            footerAccount.classList.remove('hidden');
            footerAccount.classList.add('flex');
        }
        if (btnTopModels) btnTopModels.classList.add('hidden');

        const accountsData = state.filteredAccountStats || (state.statsData?.usage?.accounts) || (state.usageData?.accounts) || {};
        renderAccountsTable(accountsData);
    } else {
        if (accountsContainer) accountsContainer.classList.add('hidden');
        if (modelsContainer) modelsContainer.classList.remove('hidden');
        if (footerAccount) {
            footerAccount.classList.add('hidden');
            footerAccount.classList.remove('flex');
        }
        if (footerModel) footerModel.classList.remove('hidden');
        if (btnTopAccounts) btnTopAccounts.classList.add('hidden');

        clearAccountsVirtualTable();

        const modelsData = state.filteredModelStats || state.statsData;
        renderModelsTable(modelsData);
    }
}

export function initModelRangeFilter(): void {
    const sel = document.getElementById('modelRangeSelector');
    if (!sel) return;

    initModelStatsVirtualList();
    initAccountStatsEvents();

    // 绑定统计维度切换器 (按模型 / 按账号)
    const dimSel = document.getElementById('statDimensionSelector');
    if (dimSel) {
        const dimBtns = dimSel.querySelectorAll('button[data-sdim]');
        dimBtns.forEach((btn) => {
            if (!btn.hasAttribute('data-bound')) {
                btn.setAttribute('data-bound', 'true');
                btn.addEventListener('click', () => {
                    const dim = (btn.getAttribute('data-sdim') || 'model') as 'model' | 'account';
                    switchStatDimension(dim);
                });
            }
        });
    }

    const buttons = sel.querySelectorAll('button[data-mrange]');
    const activeClass = 'px-2.5 py-0.5 text-[10px] bg-white dark:bg-[#1a1f30] text-primary dark:text-primary-fixed-dim rounded-md shadow-sm font-semibold';
    const inactiveClass = 'px-2.5 py-0.5 text-[10px] text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200 rounded-md transition-all font-medium';

    const applyRange = async (range: string) => {
        state.currentModelRange = range as any;
        buttons.forEach((b: any) => {
            b.className = b.getAttribute('data-mrange') === range ? activeClass : inactiveClass;
        });

        // 切换时间范围后平滑滚回顶部，避免停留在旧范围的深层滚动偏置
        scrollToTopModels(false);

        // 选定「全部」范围：直接使用全局全量 statsData, 置空 filtered 缓存允许实时刷新
        if (range === 'all') {
            state.filteredModelStats = null;
            state.filteredAccountStats = null;
            if (state.statsData) {
                renderModelsTable(state.statsData);
                const allAccounts = (state.statsData && state.statsData.usage && state.statsData.usage.accounts)
                    ? state.statsData.usage.accounts
                    : (state.usageData ? state.usageData.accounts : {});
                renderAccountsTable(allAccounts);
                return;
            }
        }

        // 选定时间切片范围(today/3d/7d)或全量未就绪时的兜底拉取
        try {
            const resRaw = await ipcRenderer.invoke('stats:model-range', range);
            if (state.currentModelRange !== range) return;
            const res = typeof resRaw === 'string' ? JSON.parse(resRaw) : resRaw;
            const stats = (res && res.stats) ? res.stats : res;
            const models = (stats && stats.models) ? stats.models : {};
            const accounts = (stats && stats.accounts) ? stats.accounts : {};

            if (range === 'all') {
                state.filteredModelStats = null;
                state.filteredAccountStats = null;
                renderModelsTable(stats || { models: {} });
                renderAccountsTable(accounts || state.statsData?.usage?.accounts || {});
            } else {
                state.filteredModelStats = stats || { models: {} };
                state.filteredAccountStats = accounts || {};
                renderModelsTable(state.filteredModelStats);
                renderAccountsTable(state.filteredAccountStats);
            }
        } catch (e) {
            console.error('[Dashboard] model range fetch failed', e);
            if (state.currentModelRange !== range) return;
            state.filteredModelStats = null;
            state.filteredAccountStats = null;
            if (state.statsData) {
                renderModelsTable(state.statsData);
                renderAccountsTable(state.statsData.usage?.accounts || {});
            }
        }
    };

    buttons.forEach(btn => {
        if (!btn.hasAttribute('data-bound')) {
            btn.setAttribute('data-bound', 'true');
            btn.addEventListener('click', () => applyRange(btn.getAttribute('data-mrange') || 'all'));
        }
    });

    // 首屏初始化：如果当前范围是 'all' 且已有实时 statsData，直接渲染全量；否则拉取一次
    if (state.currentModelRange === 'all') {
        state.filteredModelStats = null;
        state.filteredAccountStats = null;
        if (state.statsData) {
            renderModelsTable(state.statsData);
            renderAccountsTable(state.statsData.usage?.accounts || {});
        } else {
            applyRange('all');
        }
    } else if (state.filteredModelStats === null) {
        applyRange(state.currentModelRange || 'all');
    }

    // 初始化维度视图状态
    switchStatDimension(state.currentStatDimension || 'model');
}
