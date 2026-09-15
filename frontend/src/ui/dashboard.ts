import { ipcRenderer } from '../shared/ipc';
import { formatDuration } from './dashboardUtils';
import { maybeDrawTrendChart, redrawTrendChartAnimated } from './dashboardTrends';
import { LogsRowSlot, logsRowSlots, viewBtnLogMap, buildLogsRowSlot, updateLogsRowSlot, mergeRetryRows } from './dashboardLogs';
import { renderModelPerfBar } from './dashboardModelPerf';
import { initBenchmarkEvents, refreshBenchmarkI18n } from './dashboardBenchmark';
import { ensureBenchmarkTimer, stopBenchmarkTimer } from './dashboardBenchmarkTimer';
import { initModalDom, showModal, hideModal } from './dashboardModal';
import { initConsoleEvents } from './dashboardConsole';
import state from './dashboardState';
import i18n from '../shared/i18n';
import * as chartRenderer from './chartRenderer';
import * as usageDetails from './usageDetails';
import * as pricingController from './pricingController';
import * as hitRateFilter from './hitRateFilter';
import { refreshDataDir } from './migrationController';
import { initAppVersion } from './updaterController';
import { startOtpTimer, stopOtpTimer } from './otpController';
import { refreshRelayPackages, refreshRelayUsers } from './relayController';
import { deactivateSettings } from './settingsController';
import { refreshOtherGroupSelectI18n } from './otherAccountModal';
import { refreshNvidiaPreferredSourceI18n } from './nvidiaPreferredShuttle';

// DOM Elements
let html: HTMLElement;
let proxyToggle: HTMLInputElement | null;
let proxyToggleLabel: HTMLElement | null;
let statusText: HTMLElement | null;
let certStatusBadge: HTMLElement | null;
let btnInstallCert: HTMLButtonElement | null;
let btnUninstallCert: HTMLButtonElement | null;
let certStatusRetryTimer: any = null;

// Metrics Cards
let valReqs: HTMLElement | null;
let valTokens: HTMLElement | null;
let valTokensIn: HTMLElement | null;
let valTokensOut: HTMLElement | null;
let valCached: HTMLElement | null;
let valSavedCost: HTMLElement | null;
let valTotalCost: HTMLElement | null;
let valHitRate: HTMLElement | null;
let gaugeCircle: HTMLElement | null;
let barTokensIn: HTMLElement | null;
let barTokensOut: HTMLElement | null;
let valRetries: HTMLElement | null;
let valErrors: HTMLElement | null;
let barSuccess: HTMLElement | null;
let barErrors: HTMLElement | null;
let valSuccessRate: HTMLElement | null;

// Tab Controls
let tabModels: HTMLElement | null;
let tabLogs: HTMLElement | null;
let tabPricing: HTMLElement | null;
let modelsContent: HTMLElement | null;
let logsContent: HTMLElement | null;
let pricingContent: HTMLElement | null;
let logSearchRow: HTMLElement | null;
let tableFooter: HTMLElement | null;

// 首帧趋势自愈: 打开后首个全量帧(get-state 响应, 携带 trends/nvidiaTrends)若因
// 事件竞态未落地, 趋势区会干等到下一个 30s 全量节拍才出图(用户体感"等好久")。
// 此处仅在 initDashboardEvents 里挂一次: 1.5s 后若两个桶仍都为空, 补发唯一一次
// get-state(幂等; 正常路径下首帧已先到, 两桶非空, 此检查静默跳过)。
// 30s 周期刷新语义保持不变。
let initialTrendsRecoveryDone = false;
function scheduleInitialTrendsRecovery(): void {
    if (initialTrendsRecoveryDone) return;
    initialTrendsRecoveryDone = true;
    setTimeout(() => {
        const globalEmpty = !state.trendsData || state.trendsData.length === 0;
        const nvidiaEmpty = !state.nvidiaTrendsData || state.nvidiaTrendsData.length === 0;
        if (globalEmpty && nvidiaEmpty) {
            ipcRenderer.send('get-state');
        }
    }, 1500);
}

// Tables
let modelsTableBody: HTMLElement | null;
let logsTableBody: HTMLElement | null;
let logSearchInput: HTMLInputElement | null;
let btnClearLogSearch: HTMLButtonElement | null;
let logStatusFilterGroup: HTMLElement | null;
let logPageSizeSelect: HTMLSelectElement | null;
let logCountBadge: HTMLElement | null;

// Pagination elements
let valShowingText: HTMLElement | null;
let paginationControls: HTMLElement | null;

let lastStatsUpdatedSig = '';




// Toggles in Header
let toggleZH: HTMLElement | null;
let toggleEN: HTMLElement | null;
let toggleTheme: HTMLElement | null;
let themeIcon: HTMLElement | null;





// Filter and render logs table with pagination
export function renderLogsTable() {
    const dict = i18n[state.currentLanguage] || {};

    // Filter requests
    const filtered = state.allRequests.filter(log => {
        // Status filter
        if (state.logStatusFilter === 'success') {
            if (log.statusCode >= 400) return false;
        } else if (state.logStatusFilter === 'error') {
            if (log.statusCode < 400) return false;
        } else if (state.logStatusFilter === 'hit') {
            if (log.cacheStatus !== 'HIT' && (!log.cachedTokens || log.cachedTokens <= 0)) return false;
        } else if (state.logStatusFilter === 'miss') {
            if (log.cacheStatus === 'HIT' || (log.cachedTokens && log.cachedTokens > 0)) return false;
        }

        // Text search query
        if (!state.searchQuery) return true;
        const q = state.searchQuery.toLowerCase();
        return (log.host || '').toLowerCase().includes(q) ||
            (log.path || '').toLowerCase().includes(q) ||
            (log.model || '').toLowerCase().includes(q) ||
            (log.account || '').toLowerCase().includes(q) ||
            (log.sessionId || '').toLowerCase().includes(q) ||
            (log.method || '').toLowerCase().includes(q);
    });

    // Toggle clear search button
    btnClearLogSearch = document.getElementById('btnClearLogSearch') as HTMLButtonElement | null;
    if (btnClearLogSearch) {
        if (state.searchQuery) {
            btnClearLogSearch.classList.remove('hidden');
        } else {
            btnClearLogSearch.classList.add('hidden');
        }
    }

    // Update log status filter buttons UI
    logStatusFilterGroup = document.getElementById('logStatusFilterGroup');
    if (logStatusFilterGroup) {
        const buttons = logStatusFilterGroup.querySelectorAll('button[data-filter]');
        buttons.forEach((btn: Element) => {
            const filterKey = btn.getAttribute('data-filter');
            if (filterKey === state.logStatusFilter) {
                btn.className = 'px-2.5 py-1 rounded-md transition-all font-semibold bg-white dark:bg-[#1a1f30] text-primary shadow-xs';
            } else {
                btn.className = 'px-2.5 py-1 rounded-md transition-all text-outline hover:text-on-surface dark:hover:text-white font-normal';
            }
        });
    }

    // Update page size select UI
    logPageSizeSelect = document.getElementById('logPageSizeSelect') as HTMLSelectElement | null;
    if (logPageSizeSelect && logPageSizeSelect.value !== state.itemsPerPage.toString()) {
        logPageSizeSelect.value = state.itemsPerPage.toString();
    }

    // 折叠成对 HIT/MISS 重试行(展示层去重,后端落库不动)。同指纹(sessionId+path+model+inTokens)
    // 且 ±3s 时间窗口内的多次客户端重试合并为一行,徽章取 HIT、展示命中那次耗时,并标 ⟳N 角标。
    // 统计/计费仍在后端单条 RequestLog 精确记账,此处不回写 state.allRequests。
    const deduped = mergeRetryRows(filtered);

    // Update total count badge
    logCountBadge = document.getElementById('logCountBadge');
    if (logCountBadge) {
        if (state.searchQuery || state.logStatusFilter !== 'all') {
            logCountBadge.textContent = state.currentLanguage === 'zh'
                ? `筛选 ${deduped.length} / 共 ${state.allRequests.length} 条`
                : `Filtered ${deduped.length} / ${state.allRequests.length}`;
        } else {
            logCountBadge.textContent = state.currentLanguage === 'zh'
                ? `共 ${state.allRequests.length} 条`
                : `Total ${state.allRequests.length}`;
        }
    }

    // Pagination bounds
    const totalItems = deduped.length;
    const totalPages = Math.ceil(totalItems / state.itemsPerPage) || 1;
    if (state.currentPage > totalPages) state.currentPage = totalPages;
    if (state.currentPage < 1) state.currentPage = 1;

    const startIndex = (state.currentPage - 1) * state.itemsPerPage;
    const endIndex = Math.min(startIndex + state.itemsPerPage, totalItems);
    const paginated = deduped.slice(startIndex, endIndex);

    if (!logsTableBody) {
        logsTableBody = document.querySelector('#logsTable tbody');
    }
    if (!logsTableBody) return;

    valShowingText = document.getElementById('valShowingText');
    if (paginated.length === 0) {
        // Empty state: drop the row pool and show a single placeholder row.
        logsRowSlots.length = 0;
        const emptyTip = (state.searchQuery || state.logStatusFilter !== 'all')
            ? (state.currentLanguage === 'zh' ? '未找到匹配的请求日志' : 'No matching request logs found')
            : (dict.noLogs || '暂无日志');
        logsTableBody.innerHTML = `<tr><td colspan="12" class="p-12 text-center text-outline dark:text-outline-variant font-sans text-[13px]"><div class="flex flex-col items-center justify-center gap-2"><span class="material-symbols-outlined text-[32px] text-outline/40">search_off</span><span>${emptyTip}</span></div></td></tr>`;
        if (valShowingText) {
            valShowingText.textContent = state.currentLanguage === 'zh' ? `共 0 条记录` : `Showing 0 entries`;
        }
    } else {
        if (logsRowSlots.length === 0) {
            logsTableBody.innerHTML = '';
        }
        while (logsRowSlots.length < paginated.length) {
            const slot = buildLogsRowSlot();
            logsRowSlots.push(slot);
            logsTableBody.appendChild(slot.tr);
        }
        for (let i = 0; i < paginated.length; i++) {
            updateLogsRowSlot(logsRowSlots[i], paginated[i], dict);
            logsRowSlots[i].tr.classList.remove('hidden');
        }
        for (let i = paginated.length; i < logsRowSlots.length; i++) {
            logsRowSlots[i].tr.classList.add('hidden');
        }

        const showingText = state.currentLanguage === 'zh'
            ? `显示第 ${startIndex + 1} 到 ${endIndex} 条，共 ${totalItems} 条记录`
            : `Showing ${startIndex + 1} to ${endIndex} of ${totalItems} entries`;
        if (valShowingText) {
            valShowingText.textContent = showingText;
        }
    }

    // Render Pagination Controls
    paginationControls = document.getElementById('paginationControls');
    if (!paginationControls) return;
    paginationControls.innerHTML = '';

    const addBtn = (label: string, pageNum: number, isActive = false, isDisabled = false) => {
        const btn = document.createElement('button');
        btn.className = `px-2.5 py-1 border border-outline-variant/60 rounded text-[12px] transition-colors ${isActive ? 'bg-primary text-white border-primary dark:bg-primary-container dark:border-primary-container' : 'bg-white dark:bg-[#1a1f30] text-on-surface dark:text-white hover:bg-slate-50 dark:hover:bg-white/5'
            } ${isDisabled ? 'opacity-40 cursor-not-allowed' : ''}`;
        btn.textContent = label;
        if (!isDisabled) {
            btn.addEventListener('click', () => {
                state.currentPage = pageNum;
                renderLogsTable();
            });
        } else {
            btn.disabled = true;
        }
        paginationControls!.appendChild(btn);
    };

    addBtn(state.currentLanguage === 'zh' ? '上一页' : 'Prev', state.currentPage - 1, false, state.currentPage === 1);

    let startPage = Math.max(1, state.currentPage - 1);
    let endPage = Math.min(totalPages, startPage + 2);
    if (endPage - startPage < 2) {
        startPage = Math.max(1, endPage - 2);
    }

    for (let p = startPage; p <= endPage; p++) {
        addBtn(p.toString(), p, p === state.currentPage);
    }

    if (endPage < totalPages) {
        const span = document.createElement('span');
        span.className = 'px-1 text-outline align-bottom';
        span.textContent = '...';
        paginationControls.appendChild(span);
        addBtn(totalPages.toString(), totalPages);
    }

    addBtn(state.currentLanguage === 'zh' ? '下一页' : 'Next', state.currentPage + 1, false, state.currentPage === totalPages);

    // 顶部「按模型聚合性能统计」条: 基于与表格一致的 filtered 列表(search/状态过滤已生效),
    // 不使用 mergeRetryRows 折叠 —— 客户端多次独立 HTTP 请求在性能统计上仍是多次真实事件。
    renderModelPerfBar(filtered);
}

// Multi-language Text Translation
export function setLanguage(lang: string) {
    state.currentLanguage = lang;

    toggleZH = document.getElementById('toggleZH');
    toggleEN = document.getElementById('toggleEN');
    logSearchInput = document.getElementById('logSearchInput') as HTMLInputElement | null;

    if (lang === 'zh') {
        if (toggleZH) toggleZH.className = 'px-2 py-0.5 text-[11px] font-medium bg-white dark:bg-[#1a1f30] text-primary dark:text-primary-fixed-dim rounded-full shadow-sm';
        if (toggleEN) toggleEN.className = 'px-2 py-0.5 text-[11px] font-medium text-outline rounded-full transition-all';
    } else {
        if (toggleEN) toggleEN.className = 'px-2 py-0.5 text-[11px] font-medium bg-white dark:bg-[#1a1f30] text-primary dark:text-primary-fixed-dim rounded-full shadow-sm';
        if (toggleZH) toggleZH.className = 'px-2 py-0.5 text-[11px] font-medium text-outline rounded-full transition-all';
    }

    const dict = i18n[lang] || {};

    document.querySelectorAll('[data-i18n]').forEach(el => {
        const key = el.getAttribute('data-i18n');
        if (key && dict[key]) {
            el.textContent = dict[key];
        }
    });

    document.querySelectorAll('[data-i18n-title]').forEach(el => {
        const key = el.getAttribute('data-i18n-title');
        if (key && dict[key]) {
            el.setAttribute('title', dict[key]);
        }
    });

    document.querySelectorAll('[data-i18n-placeholder]').forEach(el => {
        const key = el.getAttribute('data-i18n-placeholder');
        if (key && dict[key]) {
            (el as HTMLInputElement).placeholder = dict[key];
        }
    });

    if (logSearchInput) {
        logSearchInput.placeholder = dict.placeholderSearchLogs || (lang === 'zh' ? '搜索日志 (域名 / API / 模型 / 会话)...' : 'Search logs (host, path, model, session)...');
    }

    if (logPageSizeSelect) {
        const unit = lang === 'zh' ? '条/页' : '/ page';
        logPageSizeSelect.querySelectorAll('option').forEach(opt => {
            opt.textContent = `${opt.value} ${unit}`;
        });
    }

    updateStatusLabel();
    // 语言切换后缓存命中率卡片的下拉文案需重建:重置 dirty-check sig 强制下次重画。
    hitRateFilter.resetPoolFilterSig();
    hitRateFilter.renderPoolFilterSelect();
    if (state.statsData) {
        hitRateFilter.computeHitRateByPool(state.statsData);
    }
    ipcRenderer.send('get-state');

    // Trigger re-rendering of dynamic UI components with the new language
    if (state.callbacks.renderAccounts && state.currentAccountsList) {
        state.callbacks.renderAccounts(state.currentAccountsList);
    }
    if (state.callbacks.updateAggregateQuotaUI) {
        state.callbacks.updateAggregateQuotaUI();
    }
    if (state.callbacks.updateAnalyzeAccountSelect) {
        state.callbacks.updateAnalyzeAccountSelect();
    }
    if (state.callbacks.updateRemoteStatus) {
        state.callbacks.updateRemoteStatus();
    }
    if (state.callbacks.renderLogsTable && state.lastBackendData && state.lastBackendData.logs) {
        state.callbacks.renderLogsTable();
    }
    if ((state.callbacks as any).refreshRelayUI) {
        (state.callbacks as any).refreshRelayUI();
    }
    // 语言切换后重刷 Other 弹窗「选择已有组」与「默认模型」下拉占位文案:
    // 这两处由 otherAccountModal.ts innerHTML 动态写入,绕过 data-i18n 遍历,
    // 需显式重刷避免弹窗重开时冒旧语言。NVIDIA 专属模型来源徽标同理(经 __dict 注入失败兜底)。
    refreshOtherGroupSelectI18n();
    refreshNvidiaPreferredSourceI18n();
    // 测速卡片动态文案(间隔徽章/状态/趋势)与弹窗下拉选项随语言重刷。
    refreshBenchmarkI18n();
}

export function updateStatusLabel() {
    proxyToggle = document.getElementById('proxyToggle') as HTMLInputElement | null;
    proxyToggleLabel = document.getElementById('proxyToggleLabel');
    statusText = document.getElementById('statusText');

    if (!proxyToggle || !statusText || !proxyToggleLabel) return;
    const isIntercept = proxyToggle.checked;
    const dict = i18n[state.currentLanguage] || {};
    statusText.textContent = isIntercept ? (dict.statusOn || '开启') : (dict.statusOff || '关闭');

    if (isIntercept) {
        statusText.className = 'text-[13px] font-bold text-emerald-600 dark:text-emerald-400';
        proxyToggle.className = 'toggle-checkbox absolute block w-5 h-5 rounded-full bg-white border-4 border-primary appearance-none cursor-pointer translate-x-5 transition-transform duration-200 ease-in-out';
        proxyToggleLabel.className = 'toggle-label block overflow-hidden h-5 rounded-full bg-primary cursor-pointer';
    } else {
        statusText.className = 'text-[13px] font-bold text-outline';
        proxyToggle.className = 'toggle-checkbox absolute block w-5 h-5 rounded-full bg-white border-4 border-outline-variant appearance-none cursor-pointer translate-x-0 transition-transform duration-200 ease-in-out';
        proxyToggleLabel.className = 'toggle-label block overflow-hidden h-5 rounded-full bg-outline-variant/50 dark:bg-white/10 cursor-pointer';
    }
}

// Theme Change Handler
export function setTheme(theme: string) {
    state.currentTheme = theme;
    html = document.documentElement;
    themeIcon = document.getElementById('themeIcon');

    if (theme === 'dark') {
        html.classList.add('dark');
        html.setAttribute('data-theme', 'dark');
        if (themeIcon) themeIcon.textContent = 'light_mode';
    } else {
        html.classList.remove('dark');
        html.setAttribute('data-theme', 'light');
        if (themeIcon) themeIcon.textContent = 'dark_mode';
    }
}

// UI tab switching
export function switchTab(tab: string) {
    state.activeTab = tab;

    tabModels = document.getElementById('tabModels');
    tabLogs = document.getElementById('tabLogs');
    tabPricing = document.getElementById('tabPricing');
    modelsContent = document.getElementById('modelsContent');
    logsContent = document.getElementById('logsContent');
    pricingContent = document.getElementById('pricingContent');
    logSearchRow = document.getElementById('logSearchRow');
    tableFooter = document.getElementById('tableFooter');

    const activeClass = 'px-4 py-2 text-[13px] font-bold text-primary border-b-2 border-primary';
    const inactiveClass = 'px-4 py-2 text-[13px] font-bold text-outline hover:text-primary transition-colors border-b-2 border-transparent';

    if (tabModels) tabModels.className = tab === 'models' ? activeClass : inactiveClass;
    if (tabLogs) tabLogs.className = tab === 'logs' ? activeClass : inactiveClass;
    if (tabPricing) tabPricing.className = tab === 'pricing' ? activeClass : inactiveClass;

    if (modelsContent) modelsContent.classList.toggle('hidden', tab !== 'models');
    if (logsContent) logsContent.classList.toggle('hidden', tab !== 'logs');
    if (pricingContent) pricingContent.classList.toggle('hidden', tab !== 'pricing');

    if (logSearchRow) {
        logSearchRow.classList.toggle('hidden', tab !== 'logs');
    }
    if (tableFooter) {
        tableFooter.classList.toggle('hidden', tab !== 'logs');
    }
    // 「按模型性能统计」条与 logSearchRow/tableFooter 同生命周期: 仅 logs tab 可见。
    const modelPerfBar = document.getElementById('modelPerfBar');
    if (modelPerfBar) {
        modelPerfBar.classList.toggle('hidden', tab !== 'logs');
    }

    if (tab === 'pricing') {
        pricingController.fetchPricing();
    }

    renderActiveView();
}

// Update Certificate Installation UI
export function updateCertUI(isInstalled: boolean, isProcessing = false) {
    certStatusBadge = document.getElementById('certStatusBadge');
    btnInstallCert = document.getElementById('btnInstallCert') as HTMLButtonElement | null;
    btnUninstallCert = document.getElementById('btnUninstallCert') as HTMLButtonElement | null;

    if (!certStatusBadge || !btnInstallCert || !btnUninstallCert) return;

    const dict = i18n[state.currentLanguage] || {};
    if (isProcessing) {
        certStatusBadge.innerHTML = `<span class="material-symbols-outlined text-[15px] animate-spin">sync</span><span>${dict.certProcessing || '处理中...'}</span>`;
        certStatusBadge.className = 'flex items-center gap-1.5 text-[12px] font-medium text-amber-600 bg-amber-50 dark:bg-amber-950/30 dark:text-amber-400 px-2.5 py-0.5 rounded-full border border-amber-100 dark:border-amber-900/30';
        btnInstallCert.disabled = true;
        btnUninstallCert.disabled = true;
        return;
    }

    if (isInstalled) {
        certStatusBadge.innerHTML = `<span class="material-symbols-outlined text-[15px]">verified</span><span>${dict.certTrusted || '已信任'}</span>`;
        certStatusBadge.className = 'flex items-center gap-1.5 text-[12px] font-medium text-emerald-600 bg-emerald-50 dark:bg-emerald-950/30 dark:text-emerald-400 px-2.5 py-0.5 rounded-full border border-emerald-100 dark:border-emerald-900/30';
        btnInstallCert.disabled = true;
        btnUninstallCert.disabled = false;
    } else {
        certStatusBadge.innerHTML = `<span class="material-symbols-outlined text-[15px]">gpp_maybe</span><span>${dict.certUntrusted || '未信任'}</span>`;
        certStatusBadge.className = 'flex items-center gap-1.5 text-[12px] font-medium text-rose-600 bg-rose-50 dark:bg-rose-950/30 dark:text-rose-400 px-2.5 py-0.5 rounded-full border border-rose-100 dark:border-rose-900/30';
        btnInstallCert.disabled = false;
        btnUninstallCert.disabled = true;
    }
}

export function requestCertStatus() {
    if (certStatusRetryTimer) {
        clearTimeout(certStatusRetryTimer);
        certStatusRetryTimer = null;
    }
    try {
        ipcRenderer.send('cert-status');
        certStatusRetryTimer = setTimeout(() => {
            ipcRenderer.send('cert-status');
        }, 1200);
    } catch (e) {
        console.error('[Dashboard] Failed to request cert status:', e);
    }
}

// Global page tab-switching router
export function switchView(viewName: string) {
    state.activeView = viewName;
    // Handle OTP timer and settings update natively if needed
    if (viewName === 'otp') {
        startOtpTimer();
    } else {
        stopOtpTimer();
    }

    if (viewName === 'dashboard') {
        ensureBenchmarkTimer();
    } else {
        stopBenchmarkTimer();
    }

    if (viewName === 'settings') {
        refreshDataDir();
        initAppVersion();
        refreshRelayPackages();
        refreshRelayUsers();
    } else {
        deactivateSettings();
    }

    if (viewName === 'accounts') {
        if (state.currentAccountsList) {
            // Re-render accounts on tab switch
            state.callbacks.renderAccounts(state.currentAccountsList);
        }
        state.callbacks.updateAggregateQuotaUI();
    } else if (viewName === 'packets') {
        state.callbacks.refreshPacketsList();
        state.callbacks.updateAnalyzeAccountSelect();
    } else if (viewName === 'usage') {
        if (state.usageData) {
            usageDetails.render(state.usageData);
        }
    }

    renderActiveView();

    // 切回 dashboard view 时强制带动画重画一次趋势图。
    // renderActiveView 内的 maybeDrawTrendChart 会因趋势签名未变而短路跳过重画，
    // 故这里补一次"跳过短路、强制动画"的重绘，保证每次切回仪表盘都看到左到右画线。
    if (viewName === 'dashboard' && state.trendsData && state.trendsData.length > 0) {
        redrawTrendChartAnimated();
    }
}

export function initDashboardEvents() {
    initModalDom();
    scheduleInitialTrendsRecovery();
    initBenchmarkEvents();

    proxyToggle = document.getElementById('proxyToggle') as HTMLInputElement | null;
    btnInstallCert = document.getElementById('btnInstallCert') as HTMLButtonElement | null;
    btnUninstallCert = document.getElementById('btnUninstallCert') as HTMLButtonElement | null;
    tabModels = document.getElementById('tabModels');
    tabLogs = document.getElementById('tabLogs');
    tabPricing = document.getElementById('tabPricing');
    logSearchInput = document.getElementById('logSearchInput') as HTMLInputElement | null;
    initConsoleEvents();
    toggleZH = document.getElementById('toggleZH');
    toggleEN = document.getElementById('toggleEN');
    toggleTheme = document.getElementById('toggleTheme');

    valReqs = document.getElementById('valReqs');
    valTokens = document.getElementById('valTokens');
    valTokensIn = document.getElementById('valTokensIn');
    valTokensOut = document.getElementById('valTokensOut');
    valCached = document.getElementById('valCached');
    valSavedCost = document.getElementById('valSavedCost');
    valTotalCost = document.getElementById('valTotalCost');
    valHitRate = document.getElementById('valHitRate');
    gaugeCircle = document.getElementById('gaugeCircle');
    barTokensIn = document.getElementById('barTokensIn');
    barTokensOut = document.getElementById('barTokensOut');
    valRetries = document.getElementById('valRetries');
    valErrors = document.getElementById('valErrors');
    barSuccess = document.getElementById('barSuccess');
    barErrors = document.getElementById('barErrors');
    valSuccessRate = document.getElementById('valSuccessRate');
    modelsTableBody = document.querySelector('#modelsTable tbody');
    logsTableBody = document.querySelector('#logsTable tbody');

    // 缓存命中率卡片号池筛选下拉: 绑定 change 监听(只需绑一次) + 首次渲染选项。
    // renderActiveView 内的 computeHitRateByPool/renderPoolFilterSelect 会在每次 stats-updated
    // 经 dirty-check 重建选项, 这里 init 时先绑监听 + 画一次初始下拉(account-res 已推送过 otherGroups)。
    hitRateFilter.bindPoolFilterSelect();
    hitRateFilter.renderPoolFilterSelect();

    // 绑定事件委托：全局唯一代理日志表格中“查看”按钮的点击事件，支持 DOM 节点重置
    document.addEventListener('click', (e: Event) => {
        const target = e.target as HTMLElement;
        const btn = target.closest('.view-details-btn') as HTMLButtonElement | null;
        if (btn) {
            // 优先用渲染时绑定到按钮上的 lite 日志对象开弹窗,绕开「id 需在 state.allRequests 里」
            // 的脆弱前提(OCR 高频覆盖 / 已挤出 50 窗口 / 旧残留等 data-log-id 查表落空场景)。
            const captured = viewBtnLogMap.get(btn);
            if (captured) {
                showModal(captured);
                return;
            }
            // 退化回退:捕获缺失(理论上不应发生)时仍按 data-log-id 查表。
            const logId = btn.getAttribute('data-log-id');
            if (logId) {
                const foundLog = state.allRequests.find(l => l.id === logId);
                if (foundLog) {
                    showModal(foundLog);
                }
            }
        }
    });

    // Event Listeners for Intercept Toggle
    if (proxyToggle) {
        proxyToggle.addEventListener('change', (e: any) => {
            const isInterceptMode = e.target.checked;
            updateStatusLabel();
            ipcRenderer.send('toggle', isInterceptMode);
        });
    }

    // CA Cert Operations
    if (btnInstallCert) {
        btnInstallCert.addEventListener('click', () => {
            updateCertUI(false, true);
            ipcRenderer.send('cert-install');
        });
    }

    if (btnUninstallCert) {
        btnUninstallCert.addEventListener('click', () => {
            updateCertUI(false, true);
            ipcRenderer.send('cert-uninstall');
        });
    }

    // Tabs Switching
    if (tabModels) tabModels.addEventListener('click', () => switchTab('models'));
    if (tabLogs) tabLogs.addEventListener('click', () => switchTab('logs'));
    if (tabPricing) tabPricing.addEventListener('click', () => switchTab('pricing'));

    // Log search input
    if (logSearchInput) {
        logSearchInput.addEventListener('input', (e: any) => {
            state.searchQuery = e.target.value;
            state.currentPage = 1;
            renderLogsTable();
        });
    }

    // Clear log search button
    btnClearLogSearch = document.getElementById('btnClearLogSearch') as HTMLButtonElement | null;
    if (btnClearLogSearch) {
        btnClearLogSearch.addEventListener('click', () => {
            if (logSearchInput) logSearchInput.value = '';
            state.searchQuery = '';
            state.currentPage = 1;
            renderLogsTable();
        });
    }

    // Status filter chips
    logStatusFilterGroup = document.getElementById('logStatusFilterGroup');
    if (logStatusFilterGroup) {
        logStatusFilterGroup.addEventListener('click', (e: Event) => {
            const target = e.target as HTMLElement;
            const btn = target.closest('button[data-filter]') as HTMLButtonElement | null;
            if (btn) {
                const filter = btn.getAttribute('data-filter') as any;
                if (filter) {
                    state.logStatusFilter = filter;
                    state.currentPage = 1;
                    renderLogsTable();
                }
            }
        });
    }

    // Log page size select
    logPageSizeSelect = document.getElementById('logPageSizeSelect') as HTMLSelectElement | null;
    if (logPageSizeSelect) {
        logPageSizeSelect.addEventListener('change', (e: any) => {
            state.itemsPerPage = Number(e.target.value) || 10;
            state.currentPage = 1;
            renderLogsTable();
        });
    }

    // Collapsible console logs & Float/Dock window handlers

    // ZH / EN Translation clicks
    if (toggleZH) toggleZH.addEventListener('click', () => setLanguage('zh'));
    if (toggleEN) toggleEN.addEventListener('click', () => setLanguage('en'));

    // Light / Dark Theme click
    if (toggleTheme) {
        toggleTheme.addEventListener('click', () => {
            const nextTheme = state.currentTheme === 'dark' ? 'light' : 'dark';
            setTheme(nextTheme);
        });
    }


    // IPC listeners from main process
    ipcRenderer.on('state', (event: any, isInterceptMode: boolean) => {
        if (proxyToggle) {
            proxyToggle.checked = isInterceptMode;
        }
        updateStatusLabel();
    });

    ipcRenderer.on('memory-stats-updated', (event: any, data: any) => {
        if (!data) return;
        let totalMBVal = 0;
        const valHeapAlloc = document.getElementById('valHeapAlloc');
        if (valHeapAlloc && typeof data.total === 'number') {
            totalMBVal = parseFloat((data.total / (1024 * 1024)).toFixed(1));
            valHeapAlloc.textContent = `${totalMBVal.toFixed(1)} MB`;
        }
        const valProcessCount = document.getElementById('valProcessCount');
        if (valProcessCount && typeof data.processCount === 'number') {
            valProcessCount.textContent = data.processCount;
        }

        const valCpuUsage = document.getElementById('valCpuUsage');
        if (valCpuUsage && typeof data.cpuUsage === 'number') {
            valCpuUsage.textContent = `${data.cpuUsage.toFixed(1)}%`;
        }

        // Render Go HeapAlloc (Go backend heap memory)
        const valMemory = document.getElementById('valMemory');
        if (valMemory && typeof data.heapAlloc === 'number') {
            const heapMB = (data.heapAlloc / (1024 * 1024)).toFixed(1);
            valMemory.textContent = `${heapMB} MB`;
        }

        if (typeof data.total === 'number') {
            if (state.memoryHistory.length === 0) {
                for (let i = 0; i < state.maxMemoryHistoryPoints; i++) {
                    state.memoryHistory.push(totalMBVal);
                }
            } else {
                state.memoryHistory.push(totalMBVal);
                if (state.memoryHistory.length > state.maxMemoryHistoryPoints) {
                    state.memoryHistory.shift();
                }
            }
            chartRenderer.updateMemoryChart();
        }
    });

    ipcRenderer.on('stats-updated', (event: any, payload: any) => {
        if (!payload) return;

        const { stats, trends, nvidiaTrends, requests, usage } = payload;

        // Construct current payload signature for dirty-checking
        // poolsSig 纳入: Pools 子聚合(号池/组命中率分子分母)变化时强制重画命中率卡片,
        // 否则各池新增 token 时 sig 不变会被短路, 卡片停在旧数值。
        const poolsSig = stats && stats.pools ? JSON.stringify(stats.pools) : '';
        const statsSig = stats ? `${stats.totalRequests}_${stats.totalErrors}_${stats.totalRetries}_${stats.totalInputTokens}_${stats.totalOutputTokens}_${stats.totalCachedTokens}_${stats.totalCacheEligibleInputTokens || 0}_${stats.totalCost}_${poolsSig}` : '';
        const trendsLen = trends ? trends.length : 0;
        // nvidiaTrendsLen 纳入 sig: NVIDIA 号池桶有新数据时强制通过 renderActiveView 重画,
        // 否则 sig 不变会被短路, 导致「NVIDIA」Tab 曲线不更新。
        const nvidiaTrendsLen = nvidiaTrends ? nvidiaTrends.length : 0;
        const nvLast = (nvidiaTrends && nvidiaTrends.length > 0) ? `${nvidiaTrends[nvidiaTrends.length - 1].time}_${nvidiaTrends[nvidiaTrends.length - 1].requests}_${nvidiaTrends[nvidiaTrends.length - 1].input}` : '';
        const lastReqSig = (requests && requests.length > 0) ? `${requests[0].timestamp}_${requests[0].statusCode}_${requests[0].cost}` : '';
        const reqsLen = requests ? requests.length : 0;
        const usageSig = usage ? JSON.stringify(usage) : '';

        const currentSig = `${statsSig}|${trendsLen}|nv=${nvidiaTrendsLen}_${nvLast}|${reqsLen}_${lastReqSig}|${usageSig}`;
        if (currentSig === lastStatsUpdatedSig) {
            return; // Skip rendering if no relevant metrics have changed
        }
        lastStatsUpdatedSig = currentSig;

        if (stats) state.statsData = stats;
        if (trends !== undefined && trends !== null) {
            state.trendsData = trends;
        }
        // nvidiaTrends: NVIDIA 号池专用趋势桶。后端恒定下发 (空时为 []),
        // 因此 != null 兜底即写入, 保证「NVIDIA」Tab 在轮询中持续拿到最新序列。
        if (nvidiaTrends !== undefined && nvidiaTrends !== null) {
            state.nvidiaTrendsData = nvidiaTrends;
        }
        if (requests) state.allRequests = requests;
        if (usage) state.usageData = usage;

        renderActiveView();
    });


    // CA status check
    ipcRenderer.on('cert-status-res', (event: any, isInstalled: boolean) => {
        if (certStatusRetryTimer) {
            clearTimeout(certStatusRetryTimer);
            certStatusRetryTimer = null;
        }
        updateCertUI(isInstalled);
    });

    document.addEventListener('visibilitychange', () => {
        if (document.visibilityState === 'visible') {
            ipcRenderer.send('get-state');
        }
    });

    // 监听器注册完毕后立即主动拉取一次最新状态与统计数据，杜绝首屏时序丢失
    ipcRenderer.send('get-state');
}

export function renderModelsTable(stats: any) {
    if (!modelsTableBody) {
        modelsTableBody = document.querySelector('#modelsTable tbody');
    }
    if (!modelsTableBody) return;

    modelsTableBody.innerHTML = '';
    const dict = i18n[state.currentLanguage] || {};
    const modelEntries = Object.entries(stats.models || {}).sort((a: any, b: any) => {
        const totalA = (a[1].inTokens || 0) + (a[1].outTokens || 0);
        const totalB = (b[1].inTokens || 0) + (b[1].outTokens || 0);
        if (totalB !== totalA) return totalB - totalA;
        return (b[1].reqs || 0) - (a[1].reqs || 0);
    });

    if (modelEntries.length === 0) {
        modelsTableBody.innerHTML = `<tr><td colspan="8" class="p-8 text-center text-outline dark:text-outline-variant italic">${dict.noData || '暂无数据'}</td></tr>`;
    } else {
        modelEntries.forEach(([model, data]: [string, any]) => {
            if (model === 'unknown' && data.reqs === 0) return;
            const tr = document.createElement('tr');
            tr.className = 'hover:bg-slate-50 dark:hover:bg-white/5 transition-colors';

            const modelHitRate = data.inTokens > 0 ? (data.cachedTokens / data.inTokens * 100) : 0;
            const avgCost = data.reqs > 0 ? (data.cost / data.reqs) : 0;
            const totalTokens = (data.inTokens || 0) + (data.outTokens || 0);

            tr.innerHTML = `
                <td class="p-3 font-sans font-semibold text-on-surface dark:text-white">${model}</td>
                <td class="p-3 text-right">${data.reqs}</td>
                <td class="p-3 text-right font-semibold" title="${totalTokens.toLocaleString()}">${chartRenderer.formatTokenCount(totalTokens)}</td>
                <td class="p-3 text-right text-outline dark:text-outline-variant" title="${(data.inTokens || 0).toLocaleString()}">${chartRenderer.formatTokenCount(data.inTokens || 0)}</td>
                <td class="p-3 text-right text-on-surface dark:text-white" title="${(data.outTokens || 0).toLocaleString()}">${chartRenderer.formatTokenCount(data.outTokens || 0)}</td>
                <td class="p-3 text-right">${modelHitRate.toFixed(1)}%</td>
                <td class="p-3 text-right text-primary dark:text-primary-fixed-dim font-bold">$${data.cost.toFixed(4)}</td>
                <td class="p-3 text-right text-outline dark:text-outline-variant">$${avgCost.toFixed(5)}</td>
            `;
            modelsTableBody!.appendChild(tr);
        });
    }
}

// initModelRangeFilter 绑定模型统计表的时间范围筛选按钮(全部/今日/近三日/近七天)。
// 全部范围统一走 stats:model-range 后端聚合(request_logs 全量), 与今日/3d/7d 同源同口径,
// 保证「全部 ⊇ 近七日 ⊇ 近三日 ⊇ 今日」恒成立。此前「全部」复用内存 statsData.models(stats.json
// 累计) 会与 DB 范围口径漂移, 出现「全部 < 今日」的悖论(stats.json 重启/迁移可能丢量, 或 DB
// 计重试而内存只计最终成功)。范围视图冻结到下次切换(聚合视图不需秒级实时, stats-updated tick
// 不改写 filteredModelStats)。
// 初始化时即按当前高亮范围(默认「全部」)自动拉取一次 DB 聚合, 使首屏口径与手动点击完全一致,
// 避免「首屏展示内存累计、切走再切回全部变成 DB 全量」导致数字跳变。
export function initModelRangeFilter() {
    const sel = document.getElementById('modelRangeSelector');
    if (!sel) return;
    const buttons = sel.querySelectorAll('button[data-mrange]');
    const activeClass = 'px-2.5 py-0.5 text-[10px] bg-white dark:bg-[#1a1f30] text-primary dark:text-primary-fixed-dim rounded-md shadow-sm font-semibold';
    const inactiveClass = 'px-2.5 py-0.5 text-[10px] text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200 rounded-md transition-all font-medium';

    const applyRange = async (range: string) => {
        state.currentModelRange = range as any;
        buttons.forEach((b: any) => {
            b.className = b.getAttribute('data-mrange') === range ? activeClass : inactiveClass;
        });
        // 全部范围同样走后端 DB 聚合(since=""), 与今日/3d/7d 同源, 保证 全部 >= 今日。
        try {
            const resRaw = await ipcRenderer.invoke('stats:model-range', range);
            // 竞态守卫: 拉取期间用户又切换了范围(含初始化自动拉取与手动点击交叠), 丢弃过期响应,
            // 避免旧范围数据覆盖新选择(否则会出现"数据莫名变回上一个范围"的跳变)。
            if (state.currentModelRange !== range) return;
            const res = typeof resRaw === 'string' ? JSON.parse(resRaw) : resRaw;
            const stats = (res && res.stats) ? res.stats : res;
            state.filteredModelStats = stats || { models: {} };
            renderModelsTable(state.filteredModelStats);
        } catch (e) {
            console.error('[Dashboard] model range fetch failed', e);
            if (state.currentModelRange !== range) return;
            // 拉取失败兜底: 用内存 statsData(对全部范围)或空, 不阻断展示。
            state.filteredModelStats = null;
            if (state.statsData) renderModelsTable(state.statsData);
        }
    };

    buttons.forEach(btn => {
        btn.addEventListener('click', () => applyRange(btn.getAttribute('data-mrange') || 'all'));
    });

    // 首屏统一走 DB 聚合口径: 高亮默认虽是「全部」, 但不自动拉取的话首次展示用的是内存
    // statsData(实时累计), 与手动点击「全部」后拿到的 DB 全量口径不一致 —— 即用户切走范围
    // 再切回「全部」时数字跳变的根因。这里启动即按当前范围拉取一次, 高亮与数据真正对齐。
    if (state.filteredModelStats === null) {
        applyRange(state.currentModelRange || 'all');
    }
}

export function renderActiveView() {
    if (state.activeView === 'dashboard') {
        const stats = state.statsData;
        if (!stats) return;

        // 1. Update Metrics Cards
        const totalRequests = (stats.totalRequests || 0) + (stats.totalErrors || 0);
        if (valReqs) valReqs.textContent = totalRequests;

        if (valRetries) {
            valRetries.textContent = stats.totalRetries || 0;
        }
        if (valErrors) {
            valErrors.textContent = stats.totalErrors || 0;
        }

        const successRate = totalRequests > 0 ? (stats.totalRequests / totalRequests * 100) : 100;
        if (valSuccessRate) {
            valSuccessRate.textContent = successRate.toFixed(1) + '%';
        }
        if (barSuccess && barErrors) {
            barSuccess.style.width = `${successRate}%`;
            barErrors.style.width = `${100 - successRate}%`;
        }

        const grandTotalTokens = (stats.totalInputTokens || 0) + (stats.totalOutputTokens || 0);
        if (valTokens) {
            valTokens.textContent = chartRenderer.formatTokenCount(grandTotalTokens);
            valTokens.title = `${state.currentLanguage === 'zh' ? '精确总数' : 'Exact Total'}: ${grandTotalTokens.toLocaleString()}`;
        }

        const totalIn = stats.totalInputTokens - stats.totalCachedTokens;
        if (valTokensIn) valTokensIn.textContent = chartRenderer.formatCompactNumber(totalIn);
        if (valTokensOut) valTokensOut.textContent = chartRenderer.formatCompactNumber(stats.totalOutputTokens);
        if (valTotalCost) {
            valTotalCost.textContent = `$${(stats.totalCost || 0).toFixed(4)}`;
        }

        const totalSum = totalIn + stats.totalOutputTokens;
        const inPercent = totalSum > 0 ? (totalIn / totalSum * 100) : 50;
        const outPercent = 100 - inPercent;
        if (barTokensIn) barTokensIn.style.width = `${inPercent}%`;
        if (barTokensOut) barTokensOut.style.width = `${outPercent}%`;

        // 缓存命中率: 按号池/组筛选(新口径, 各池/组独立互不串扰; 缺失则兜底旧三档全量)。
        // 同时尝试重建下拉选项(dirty-check, 组列表变化才重建, 不覆盖用户交互)。
        hitRateFilter.computeHitRateByPool(stats);
        hitRateFilter.renderPoolFilterSelect();

        // 2. Draw SVG Area Trend line (throttled; only when trends actually change)
        maybeDrawTrendChart();

        // 3. Render sub-tabs table (only the active one!)
        if (state.activeTab === 'models') {
            // 模型统计表: 初始化自动拉取或用户选过任一范围(含「全部」)后, filteredModelStats 已是
            // 后端 DB 聚合快照, 复用它(范围视图冻结到下次切换); 仅当拉取失败(null)时兜底实时 statsData。
            if (state.filteredModelStats) {
                renderModelsTable(state.filteredModelStats);
            } else {
                renderModelsTable(stats);
            }
        } else if (state.activeTab === 'logs') {
            renderLogsTable();
        }
    } else if (state.activeView === 'usage') {
        if (state.usageData) {
            usageDetails.render(state.usageData);
        }
    }
}


// Global hooks
(window as any).switchView = switchView;
(window as any).switchTab = switchTab;
(window as any).showModal = showModal;
(window as any).hideModal = hideModal;

state.callbacks.renderLogsTable = renderLogsTable;
state.callbacks.updateStatusLabel = updateStatusLabel;
state.callbacks.setLanguage = setLanguage;
