import { ipcRenderer } from '../shared/ipc';
import { formatDuration } from './dashboardUtils';
import { maybeDrawTrendChart, redrawTrendChartAnimated } from './dashboardTrends';
import { LogsRowSlot, logsRowSlots, viewBtnLogMap, buildLogsRowSlot, updateLogsRowSlot, mergeRetryRows } from './dashboardLogs';
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

// Tables
let modelsTableBody: HTMLElement | null;
let logsTableBody: HTMLElement | null;
let logSearchInput: HTMLInputElement | null;


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
        if (!state.searchQuery) return true;
        const q = state.searchQuery.toLowerCase();
        return log.host.toLowerCase().includes(q) ||
            log.path.toLowerCase().includes(q) ||
            log.model.toLowerCase().includes(q) ||
            (log.sessionId || '').toLowerCase().includes(q) ||
            log.method.toLowerCase().includes(q);
    });

    // 折叠成对 HIT/MISS 重试行(展示层去重,后端落库不动)。同指纹(sessionId+path+model+inTokens)
    // 且 ±3s 时间窗口内的多次客户端重试合并为一行,徽章取 HIT、展示命中那次耗时,并标 ⟳N 角标。
    // 统计/计费仍在后端单条 RequestLog 精确记账,此处不回写 state.allRequests。
    const deduped = mergeRetryRows(filtered);

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
        // (Only on filter transitions / no data, so churn here is acceptable.)
        logsRowSlots.length = 0;
        logsTableBody.innerHTML = `<tr><td colspan="12" class="p-8 text-center text-outline dark:text-outline-variant italic">${dict.noLogs || '暂无日志'}</td></tr>`;
        if (valShowingText) {
            valShowingText.textContent = state.currentLanguage === 'zh' ? `共 0 条记录` : `Showing 0 entries`;
        }
    } else {
        // Reuse a fixed pool of <tr> nodes (grown up to itemsPerPage) and patch
        // cell textContent / classNames in place. This avoids the destroy-and-
        // recreate churn of `innerHTML =` that inflated Blink's DOM node pools
        // under sustained traffic.
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
        logSearchInput.placeholder = lang === 'zh' ? '搜索日志...' : 'Search logs...';
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

    // Log search
    if (logSearchInput) {
        logSearchInput.addEventListener('input', (e: any) => {
            state.searchQuery = e.target.value;
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
            console.log('[Dashboard] Window visible, syncing state and logs from backend...');
            ipcRenderer.send('get-state');
        }
    });
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
            const totalTokens = data.inTokens + data.outTokens;

            tr.innerHTML = `
                <td class="p-3 font-sans font-semibold text-on-surface dark:text-white">${model}</td>
                <td class="p-3 text-right">${data.reqs}</td>
                <td class="p-3 text-right font-semibold">${totalTokens.toLocaleString()}</td>
                <td class="p-3 text-right text-outline dark:text-outline-variant">${data.inTokens.toLocaleString()}</td>
                <td class="p-3 text-right text-on-surface dark:text-white">${data.outTokens.toLocaleString()}</td>
                <td class="p-3 text-right">${modelHitRate.toFixed(1)}%</td>
                <td class="p-3 text-right text-primary dark:text-primary-fixed-dim font-bold">$${data.cost.toFixed(4)}</td>
                <td class="p-3 text-right text-outline dark:text-outline-variant">$${avgCost.toFixed(5)}</td>
            `;
            modelsTableBody!.appendChild(tr);
        });
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

        if (valTokens) valTokens.textContent = (stats.totalInputTokens + stats.totalOutputTokens).toLocaleString();

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
            renderModelsTable(stats);
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
