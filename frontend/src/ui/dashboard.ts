import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';
import i18n from '../shared/i18n';
import * as chartRenderer from './chartRenderer';
import * as usageDetails from './usageDetails';
import * as pricingController from './pricingController';
import { refreshDataDir } from './migrationController';
import { initAppVersion } from './updaterController';
import { startOtpTimer, stopOtpTimer } from './otpController';

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

// Details Modal Elements
let detailsModal: HTMLElement | null = null;
let modalContainer: HTMLElement | null = null;
let modalCloseBtn: HTMLElement | null = null;
let modalCloseBtnSecondary: HTMLElement | null = null;
let modalCopyBtn: HTMLElement | null = null;
let modalCopyHeadersBtn: HTMLElement | null = null;

let modalTime: HTMLElement | null = null;
let modalSession: HTMLElement | null = null;
let modalModel: HTMLElement | null = null;
let modalPath: HTMLElement | null = null;
let modalTokens: HTMLElement | null = null;
let modalStatus: HTMLElement | null = null;
let modalCost: HTMLElement | null = null;
let modalAccount: HTMLElement | null = null;
let modalAccountWrapper: HTMLElement | null = null;
let modalDuration: HTMLElement | null = null;
let modalJsonArea: HTMLElement | null = null;
let modalHeaderArea: HTMLElement | null = null;

// Pagination elements
let valShowingText: HTMLElement | null;
let paginationControls: HTMLElement | null;

// Console Log Panel
let consoleHeader: HTMLElement | null;
let systemConsole: HTMLElement | null;
let consoleBody: HTMLElement | null;
let isConsoleScrollScheduled = false;
let lastStatsUpdatedSig = '';

// Toggles in Header
let toggleZH: HTMLElement | null;
let toggleEN: HTMLElement | null;
let toggleTheme: HTMLElement | null;
let themeIcon: HTMLElement | null;
let btnExportLogs: HTMLButtonElement | null;

function formatDuration(ms: number | undefined): string {
    if (ms === undefined || ms === null || ms === 0) return '-';
    if (ms < 1000) return `${ms}ms`;
    return `${(ms / 1000).toFixed(2)}s`;
}

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

    // Pagination bounds
    const totalItems = filtered.length;
    const totalPages = Math.ceil(totalItems / state.itemsPerPage) || 1;
    if (state.currentPage > totalPages) state.currentPage = totalPages;
    if (state.currentPage < 1) state.currentPage = 1;

    const startIndex = (state.currentPage - 1) * state.itemsPerPage;
    const endIndex = Math.min(startIndex + state.itemsPerPage, totalItems);
    const paginated = filtered.slice(startIndex, endIndex);

    if (!logsTableBody) {
        logsTableBody = document.querySelector('#logsTable tbody');
    }
    if (!logsTableBody) return;
    logsTableBody.innerHTML = '';
    
    valShowingText = document.getElementById('valShowingText');
    if (paginated.length === 0) {
        logsTableBody.innerHTML = `<tr><td colspan="11" class="p-8 text-center text-outline dark:text-outline-variant italic">${dict.noLogs || '暂无日志'}</td></tr>`;
        if (valShowingText) {
            valShowingText.textContent = state.currentLanguage === 'zh' ? `共 0 条记录` : `Showing 0 entries`;
        }
    } else {
        paginated.forEach(log => {
            const tr = document.createElement('tr');
            tr.className = 'hover:bg-slate-50 dark:hover:bg-white/5 transition-colors';
            
            let statusClass = 'bg-slate-100 text-slate-600 border border-slate-200 dark:bg-slate-900/40 dark:text-slate-400 dark:border-slate-800';
            let statusLabel = dict.statusMiss || 'MISS';
            if (log.cacheStatus === 'HIT') {
                statusClass = 'bg-emerald-50 text-emerald-700 border border-emerald-200 dark:bg-emerald-950/20 dark:text-emerald-400 dark:border-emerald-900/30';
                statusLabel = dict.statusHit || 'HIT';
            } else if (log.cacheStatus === 'NONE') {
                statusClass = 'bg-purple-50 text-purple-700 border border-purple-200 dark:bg-purple-950/20 dark:text-purple-400 dark:border-purple-900/30';
                statusLabel = dict.statusNone || 'NONE';
            }

            const isError = log.statusCode >= 400;
            const statusColor = isError ? 'text-rose-500' : 'text-emerald-600 dark:text-emerald-400';
            
            const hitRateVal = log.inTokens > 0 ? (log.cachedTokens / log.inTokens * 100).toFixed(1) : '0.0';
            const hitRateColor = log.cachedTokens > 0 ? 'text-emerald-600 dark:text-emerald-400 font-bold' : 'text-slate-400 dark:text-slate-500';

            tr.innerHTML = `
                <td class="p-3 text-outline dark:text-outline-variant font-data-mono text-[12px] whitespace-nowrap">${log.timestamp}</td>
                <td class="p-3 font-data-mono truncate" title="${log.method} ${log.host}">
                    <span class="text-[#0ea5e9] font-bold mr-2">${log.method}</span>
                    <span class="text-on-surface dark:text-white">${log.host}</span>
                </td>
                <td class="p-3 text-outline dark:text-outline-variant font-data-mono text-[12px] truncate" title="${log.path}">${log.path}</td>
                <td class="p-3 text-outline dark:text-outline-variant font-data-mono text-[12px] truncate" title="${log.sessionId || '-'}">${log.sessionId || '-'}</td>
                <td class="p-3 font-sans font-medium text-on-surface dark:text-white truncate" title="${log.model}">
                    <div class="flex flex-col min-w-0">
                        <span class="font-semibold text-on-surface dark:text-white truncate">${log.model}</span>
                        ${log.account ? `<span class="text-[10px] text-outline dark:text-outline-variant font-data-mono truncate mt-0.5" title="${log.account}">${log.account}</span>` : '<span class="text-[10px] text-slate-400 dark:text-slate-500 font-data-mono truncate mt-0.5">直连</span>'}
                    </div>
                </td>
                <td class="p-3 text-right font-data-mono">
                    <div class="flex flex-col items-end">
                        <span class="text-[10px] text-outline dark:text-outline-variant">${dict.input || '输入'}: ${log.inTokens.toLocaleString()}</span>
                        <span class="text-on-surface dark:text-white">${dict.output || '输出'}: ${log.outTokens.toLocaleString()}</span>
                    </div>
                </td>
                <td class="p-3 text-right font-data-mono text-emerald-600 dark:text-emerald-400 font-bold">$${(log.cost || 0).toFixed(6)}</td>
                <td class="p-3 text-right font-data-mono">${formatDuration(log.durationMs)}</td>
                <td class="p-3 text-center font-data-mono ${hitRateColor}">${hitRateVal}%</td>
                <td class="p-3 text-center">
                    <span class="inline-flex items-center px-2 py-0.5 rounded text-[11px] font-medium ${statusClass}">${statusLabel}</span>
                    <span class="block text-[10px] font-bold ${statusColor} mt-1">HTTP ${log.statusCode}</span>
                </td>
                <td class="p-3 text-center">
                    <button class="px-2 py-1 text-[11px] bg-primary/10 hover:bg-primary/20 text-primary dark:text-primary-fixed-dim rounded font-medium transition-all view-details-btn">
                        查看
                    </button>
                </td>
            `;

            const detailBtn = tr.querySelector('.view-details-btn');
            if (detailBtn) {
                detailBtn.addEventListener('click', () => {
                    showModal(log);
                });
            }

            logsTableBody!.appendChild(tr);
        });

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
        btn.className = `px-2.5 py-1 border border-outline-variant/60 rounded text-[12px] transition-colors ${
            isActive ? 'bg-primary text-white border-primary dark:bg-primary-container dark:border-primary-container' : 'bg-white dark:bg-[#1a1f30] text-on-surface dark:text-white hover:bg-slate-50 dark:hover:bg-white/5'
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

    if (logSearchInput) {
        logSearchInput.placeholder = lang === 'zh' ? '搜索日志...' : 'Search logs...';
    }

    updateStatusLabel();
    ipcRenderer.send('get-state');
    ipcRenderer.send('settings:language-changed', lang);
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
    const viewDashboard = document.getElementById('view-dashboard');
    const viewAccounts = document.getElementById('view-accounts');
    const viewSettings = document.getElementById('view-settings');
    const viewPackets = document.getElementById('view-packets');
    const viewOtp = document.getElementById('view-otp');
    
    const navDashboard = document.getElementById('nav-dashboard');
    const navAccounts = document.getElementById('nav-accounts');
    const navPackets = document.getElementById('nav-packets');
    const navSettings = document.getElementById('nav-settings');
    const navOtp = document.getElementById('nav-otp');

    if (viewDashboard) viewDashboard.classList.toggle('hidden', viewName !== 'dashboard');
    if (viewAccounts) viewAccounts.classList.toggle('hidden', viewName !== 'accounts');
    if (viewSettings) viewSettings.classList.toggle('hidden', viewName !== 'settings');
    if (viewPackets) viewPackets.classList.toggle('hidden', viewName !== 'packets');
    if (viewOtp) viewOtp.classList.toggle('hidden', viewName !== 'otp');

    const activeNavClass = 'text-primary dark:text-primary-fixed-dim border-b-2 border-primary pb-0.5 flex flex-col items-center';
    const inactiveNavClass = 'text-outline hover:text-primary transition-colors pb-0.5 flex flex-col items-center';

    if (navDashboard) navDashboard.className = viewName === 'dashboard' ? activeNavClass : inactiveNavClass;
    if (navAccounts) navAccounts.className = viewName === 'accounts' ? activeNavClass : inactiveNavClass;
    if (navPackets) navPackets.className = viewName === 'packets' ? activeNavClass : inactiveNavClass;
    if (navSettings) navSettings.className = viewName === 'settings' ? activeNavClass : inactiveNavClass;
    if (navOtp) navOtp.className = viewName === 'otp' ? activeNavClass : inactiveNavClass;

    // Manage OTP timer loop
    if (viewName === 'otp') {
        startOtpTimer();
    } else {
        stopOtpTimer();
    }

    if (viewName === 'settings') {
        refreshDataDir();
        initAppVersion();
    } else if (viewName === 'accounts') {
        if (state.currentAccountsList) {
            // Re-render accounts on tab switch
            state.callbacks.renderAccounts(state.currentAccountsList);
        }
        state.callbacks.updateAggregateQuotaUI();
    } else if (viewName === 'packets') {
        state.callbacks.refreshPacketsList();
        state.callbacks.updateAnalyzeAccountSelect();
    }

    renderActiveView();
}

export function initDashboardEvents() {
    detailsModal = document.getElementById('detailsModal');
    modalContainer = document.getElementById('modalContainer');
    modalCloseBtn = document.getElementById('modalCloseBtn');
    modalCloseBtnSecondary = document.getElementById('modalCloseBtnSecondary');
    modalCopyBtn = document.getElementById('modalCopyBtn');
    modalCopyHeadersBtn = document.getElementById('modalCopyHeadersBtn');

    modalTime = document.getElementById('modalTime');
    modalSession = document.getElementById('modalSession');
    modalModel = document.getElementById('modalModel');
    modalPath = document.getElementById('modalPath');
    modalTokens = document.getElementById('modalTokens');
    modalStatus = document.getElementById('modalStatus');
    modalCost = document.getElementById('modalCost');
    modalAccount = document.getElementById('modalAccount');
    modalAccountWrapper = document.getElementById('modalAccountWrapper');
    modalDuration = document.getElementById('modalDuration');
    modalJsonArea = document.getElementById('modalJsonArea');
    modalHeaderArea = document.getElementById('modalHeaderArea');

    if (modalCloseBtn) modalCloseBtn.addEventListener('click', hideModal);
    if (modalCloseBtnSecondary) modalCloseBtnSecondary.addEventListener('click', hideModal);
    if (detailsModal) {
        detailsModal.addEventListener('click', (e) => {
            if (e.target === detailsModal) hideModal();
        });
    }

    if (modalCopyHeadersBtn) {
        modalCopyHeadersBtn.addEventListener('click', () => {
            const textToCopy = modalHeaderArea?.textContent || '';
            navigator.clipboard.writeText(textToCopy).then(() => {
                const span = modalCopyHeadersBtn!.querySelector('span:not(.material-symbols-outlined)');
                if (span) {
                    span.textContent = state.currentLanguage === 'zh' ? '已复制！' : 'Copied!';
                    setTimeout(() => { span.textContent = state.currentLanguage === 'zh' ? '复制' : 'Copy'; }, 1500);
                }
            });
        });
    }

    if (modalCopyBtn) {
        modalCopyBtn.addEventListener('click', () => {
            const textToCopy = modalJsonArea?.textContent || '';
            navigator.clipboard.writeText(textToCopy).then(() => {
                const span = modalCopyBtn!.querySelector('span:not(.material-symbols-outlined)');
                if (span) {
                    span.textContent = state.currentLanguage === 'zh' ? '已复制！' : 'Copied!';
                    setTimeout(() => { span.textContent = state.currentLanguage === 'zh' ? '复制 JSON' : 'Copy JSON'; }, 1500);
                }
            });
        });
    }

    proxyToggle = document.getElementById('proxyToggle') as HTMLInputElement | null;
    btnInstallCert = document.getElementById('btnInstallCert') as HTMLButtonElement | null;
    btnUninstallCert = document.getElementById('btnUninstallCert') as HTMLButtonElement | null;
    tabModels = document.getElementById('tabModels');
    tabLogs = document.getElementById('tabLogs');
    tabPricing = document.getElementById('tabPricing');
    logSearchInput = document.getElementById('logSearchInput') as HTMLInputElement | null;
    consoleHeader = document.getElementById('consoleHeader');
    systemConsole = document.getElementById('systemConsole');
    consoleBody = document.getElementById('consoleBody');
    toggleZH = document.getElementById('toggleZH');
    toggleEN = document.getElementById('toggleEN');
    toggleTheme = document.getElementById('toggleTheme');
    btnExportLogs = document.getElementById('btnExportLogs') as HTMLButtonElement | null;

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

    // Collapsible console logs
    if (consoleHeader && systemConsole) {
        consoleHeader.addEventListener('click', () => {
            systemConsole!.classList.toggle('expanded');
        });
    }

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

    // Export Logs Button
    if (btnExportLogs) {
        btnExportLogs.addEventListener('click', () => {
            try {
                const dirInfo = ipcRenderer.sendSync('settings:get-dir-sync') || {};
                const activeDir = dirInfo.activeDir || ipcRenderer.sendSync('get-userdata-path') || '';
                ipcRenderer.send('settings:open-folder', activeDir);
            } catch (err) {
                console.error('Failed to open folder:', err);
            }
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

        const { stats, trends, requests, usage } = payload;

        // Construct current payload signature for dirty-checking
        const statsSig = stats ? `${stats.totalRequests}_${stats.totalErrors}_${stats.totalRetries}_${stats.totalInputTokens}_${stats.totalOutputTokens}_${stats.totalCachedTokens}_${stats.totalCost}` : '';
        const trendsLen = trends ? trends.length : 0;
        const lastReqSig = (requests && requests.length > 0) ? `${requests[0].timestamp}_${requests[0].statusCode}_${requests[0].cost}` : '';
        const reqsLen = requests ? requests.length : 0;
        const usageSig = usage ? JSON.stringify(usage) : '';
        
        const currentSig = `${statsSig}|${trendsLen}|${reqsLen}_${lastReqSig}|${usageSig}`;
        if (currentSig === lastStatsUpdatedSig) {
            return; // Skip rendering if no relevant metrics have changed
        }
        lastStatsUpdatedSig = currentSig;
        
        if (stats) state.statsData = stats;
        if (trends !== undefined && trends !== null) {
            state.trendsData = trends;
        }
        if (requests) state.allRequests = requests;
        if (usage) state.usageData = usage;

        renderActiveView();
    });

    // Appending raw logs to console tray
    ipcRenderer.on('log', (event: any, log: string) => {
        if (!consoleBody) {
            consoleBody = document.getElementById('consoleBody');
        }
        if (!consoleBody) return;
        const entry = document.createElement('div');
        entry.className = 'console-entry';
        if (log.includes('⚠️')) entry.classList.add('warn');
        if (log.includes('❌')) entry.classList.add('error');
        if (log.includes('✅') || log.includes('🚀')) entry.classList.add('info');
        entry.textContent = log;
        consoleBody.appendChild(entry);
        
        // Batch prune console elements if count exceeds 150 to reduce child removal frequency
        if (consoleBody.children.length > 150) {
            while (consoleBody.children.length > 120) {
                if (consoleBody.firstChild) {
                    consoleBody.removeChild(consoleBody.firstChild);
                }
            }
        }

        // Scroll to bottom only if console pane is expanded, using rAF throttling to prevent layout thrashing
        if (systemConsole && systemConsole.classList.contains('expanded')) {
            if (!isConsoleScrollScheduled) {
                isConsoleScrollScheduled = true;
                requestAnimationFrame(() => {
                    if (consoleBody) {
                        consoleBody.scrollTop = consoleBody.scrollHeight;
                    }
                    isConsoleScrollScheduled = false;
                });
            }
        }
    });

    // CA status check
    ipcRenderer.on('cert-status-res', (event: any, isInstalled: boolean) => {
        if (certStatusRetryTimer) {
            clearTimeout(certStatusRetryTimer);
            certStatusRetryTimer = null;
        }
        updateCertUI(isInstalled);
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

        const hitRate = stats.totalInputTokens > 0 ? (stats.totalCachedTokens / stats.totalInputTokens * 100) : 0;
        if (valHitRate) valHitRate.textContent = hitRate.toFixed(1) + '%';
        if (valCached) valCached.textContent = chartRenderer.formatCompactNumber(stats.totalCachedTokens);
        if (valSavedCost) valSavedCost.textContent = `$${(stats.totalCachedTokens * 0.3125 / 1000000).toFixed(2)}`;

        if (gaugeCircle) gaugeCircle.setAttribute('stroke-dasharray', `${hitRate.toFixed(1)}, 100`);

        // 2. Draw SVG Area Trend line (only if we have trendsData)
        if (state.trendsData && state.trendsData.length > 0) {
            const filteredTrends = chartRenderer.getFilteredTrends(state.trendsData, state.currentRange);
            chartRenderer.drawTrendChartSVG(filteredTrends, state.currentRange);
        }

        // 3. Render sub-tabs table (only the active one!)
        if (state.activeTab === 'models') {
            renderModelsTable(stats);
        } else if (state.activeTab === 'logs') {
            renderLogsTable();
        }
    } else if (state.activeView === 'accounts') {
        if (state.usageData) {
            usageDetails.render(state.usageData);
        }
    }
}

export function hideModal() {
    if (!detailsModal || !modalContainer) return;
    detailsModal.classList.add('opacity-0', 'pointer-events-none');
    modalContainer.classList.add('scale-95');
    modalContainer.classList.remove('scale-100');

    // Clear massive text areas to release memory of large API requests/responses in DOM tree immediately
    if (modalJsonArea) modalJsonArea.textContent = '';
    if (modalHeaderArea) modalHeaderArea.textContent = '';
}

export function showModal(log: any) {
    if (!detailsModal || !modalContainer) return;
    
    if (modalTime) modalTime.textContent = log.timestamp || '-';
    if (modalSession) modalSession.textContent = log.sessionId || '-';
    if (modalModel) modalModel.textContent = log.model || '-';
    if (modalPath) modalPath.textContent = `${log.method || 'POST'} ${log.host || ''}${log.path || ''}`;
    if (modalDuration) modalDuration.textContent = formatDuration(log.durationMs);
    if (modalCost) modalCost.textContent = `$${(log.cost || 0).toFixed(6)}`;
    
    if (log.account) {
        if (modalAccountWrapper) modalAccountWrapper.classList.remove('hidden');
        if (modalAccount) modalAccount.textContent = log.account;
    } else {
        if (modalAccountWrapper) modalAccountWrapper.classList.add('hidden');
    }
    
    const inT = log.inTokens || 0;
    const outT = log.outTokens || 0;
    const cachedT = log.cachedTokens || 0;
    if (modalTokens) modalTokens.textContent = `In: ${inT.toLocaleString()} | Out: ${outT.toLocaleString()} | Cache: ${cachedT.toLocaleString()}`;
    
    let cacheBadge = log.cacheStatus || 'NONE';
    let statusColor = log.statusCode >= 400 ? 'text-rose-500' : 'text-emerald-500';
    if (modalStatus) modalStatus.innerHTML = `<span class="text-primary dark:text-primary-fixed-dim mr-2">${cacheBadge}</span><span class="${statusColor}">HTTP ${log.statusCode}</span>`;
    
    let formattedJson = '';
    if (log.requestBody) {
        try {
            if (typeof log.requestBody === 'object') {
                formattedJson = JSON.stringify(log.requestBody, null, 2);
            } else {
                const parsed = JSON.parse(log.requestBody);
                formattedJson = JSON.stringify(parsed, null, 2);
            }
        } catch (e) {
            formattedJson = String(log.requestBody);
        }
    } else {
        formattedJson = '{\n  "message": "无请求参数"\n}';
    }
    if (modalJsonArea) modalJsonArea.textContent = formattedJson;

    let formattedHeaders = '';
    if (log.requestHeaders) {
        try {
            formattedHeaders = JSON.stringify(log.requestHeaders, null, 2);
        } catch (e) {
            formattedHeaders = String(log.requestHeaders);
        }
    } else {
        formattedHeaders = '{\n  "message": "无请求头数据"\n}';
    }
    if (modalHeaderArea) modalHeaderArea.textContent = formattedHeaders;

    detailsModal.classList.remove('opacity-0', 'pointer-events-none');
    modalContainer.classList.remove('scale-95');
    modalContainer.classList.add('scale-100');
}

// Global hooks
(window as any).switchView = switchView;
(window as any).switchTab = switchTab;
(window as any).showModal = showModal;
(window as any).hideModal = hideModal;

state.callbacks.renderLogsTable = renderLogsTable;
state.callbacks.updateStatusLabel = updateStatusLabel;
state.callbacks.setLanguage = setLanguage;
