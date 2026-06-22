import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';
import i18n from '../shared/i18n';

// DOM Elements
let retryErrorLogsModal: HTMLElement | null = null;
let retryErrorLogsModalContainer: HTMLElement | null = null;
let retryErrorLogsCount: HTMLElement | null = null;
let logTypeFilter: HTMLSelectElement | null = null;
let retryErrorLogsModalCloseBtn: HTMLElement | null = null;
let retryErrorLogsModalCloseBtnSecondary: HTMLElement | null = null;
let retryErrorLogsTableBody: HTMLElement | null = null;
let retryErrorLogsEmpty: HTMLElement | null = null;
let btnClearRetryErrorLogs: HTMLElement | null = null;
let btnExportRetryErrorLogs: HTMLElement | null = null;

let retryErrorLogsPaginationWrapper: HTMLElement | null = null;
let valRetryErrorLogsShowingText: HTMLElement | null = null;
let retryErrorLogsPaginationControls: HTMLElement | null = null;

let btnViewRetries: HTMLElement | null = null;
let btnViewErrors: HTMLElement | null = null;

let isModalOpen = false;
let allLogs: any[] = [];
let retryErrorCurrentPage = 1;
const retryErrorItemsPerPage = 10;

/**
 * Initialize retry and error logs modal events
 */
export function initRetryErrorLogsEvents() {
    retryErrorLogsModal = document.getElementById('retryErrorLogsModal');
    retryErrorLogsModalContainer = document.getElementById('retryErrorLogsModalContainer');
    retryErrorLogsCount = document.getElementById('retryErrorLogsCount');
    logTypeFilter = document.getElementById('logTypeFilter') as HTMLSelectElement | null;
    retryErrorLogsModalCloseBtn = document.getElementById('retryErrorLogsModalCloseBtn');
    retryErrorLogsModalCloseBtnSecondary = document.getElementById('retryErrorLogsModalCloseBtnSecondary');
    retryErrorLogsTableBody = document.getElementById('retryErrorLogsTableBody');
    retryErrorLogsEmpty = document.getElementById('retryErrorLogsEmpty');
    btnClearRetryErrorLogs = document.getElementById('btnClearRetryErrorLogs');
    btnExportRetryErrorLogs = document.getElementById('btnExportRetryErrorLogs');

    retryErrorLogsPaginationWrapper = document.getElementById('retryErrorLogsPaginationWrapper');
    valRetryErrorLogsShowingText = document.getElementById('valRetryErrorLogsShowingText');
    retryErrorLogsPaginationControls = document.getElementById('retryErrorLogsPaginationControls');

    btnViewRetries = document.getElementById('btnViewRetries');
    btnViewErrors = document.getElementById('btnViewErrors');

    // Click on retries count metric card element to open retry logs modal
    if (btnViewRetries) {
        btnViewRetries.addEventListener('click', () => {
            if (logTypeFilter) logTypeFilter.value = 'RETRY';
            retryErrorCurrentPage = 1;
            openModal();
        });
    }

    // Click on errors count metric card element to open error logs modal
    if (btnViewErrors) {
        btnViewErrors.addEventListener('click', () => {
            if (logTypeFilter) logTypeFilter.value = 'ERROR';
            retryErrorCurrentPage = 1;
            openModal();
        });
    }

    // Close buttons
    if (retryErrorLogsModalCloseBtn) {
        retryErrorLogsModalCloseBtn.addEventListener('click', closeModal);
    }
    if (retryErrorLogsModalCloseBtnSecondary) {
        retryErrorLogsModalCloseBtnSecondary.addEventListener('click', closeModal);
    }

    // Backdrop click to close modal
    if (retryErrorLogsModal) {
        retryErrorLogsModal.addEventListener('click', (e) => {
            if (e.target === retryErrorLogsModal) {
                closeModal();
            }
        });
    }

    // Dropdown filter change
    if (logTypeFilter) {
        logTypeFilter.addEventListener('change', () => {
            retryErrorCurrentPage = 1;
            renderLogs();
        });
    }

    // Clear logs button click
    if (btnClearRetryErrorLogs) {
        btnClearRetryErrorLogs.addEventListener('click', async () => {
            const dict = i18n[state.currentLanguage] || {};
            const filterValue = logTypeFilter ? logTypeFilter.value : 'ALL';
            const confirmMsg = dict.clearConfirm || '确定要清空这些日志吗？';
            
            if (confirm(confirmMsg)) {
                try {
                    await ipcRenderer.invoke('retry-error-logs:clear', filterValue);
                    retryErrorCurrentPage = 1;
                    await fetchAndRenderLogs();
                } catch (e) {
                    console.error('Failed to clear logs:', e);
                }
            }
        });
    }

    // Export logs button click
    if (btnExportRetryErrorLogs) {
        btnExportRetryErrorLogs.addEventListener('click', async () => {
            try {
                await ipcRenderer.invoke('retry-error-logs:export');
            } catch (e) {
                console.error('Failed to export logs:', e);
            }
        });
    }

    // Real-time updates: refresh logs if modal is active and backend notifies stats-updated
    ipcRenderer.on('stats-updated', async () => {
        if (isModalOpen) {
            await fetchAndRenderLogs();
        }
    });
}

/**
 * Open the logs modal
 */
async function openModal() {
    if (!retryErrorLogsModal || !retryErrorLogsModalContainer) return;
    
    isModalOpen = true;
    retryErrorLogsModal.classList.remove('opacity-0', 'pointer-events-none');
    retryErrorLogsModalContainer.classList.remove('scale-95');
    retryErrorLogsModalContainer.classList.add('scale-100');

    await fetchAndRenderLogs();
}

/**
 * Close the logs modal and release memory immediately
 */
function closeModal() {
    if (!retryErrorLogsModal || !retryErrorLogsModalContainer) return;
    
    isModalOpen = false;
    retryErrorLogsModal.classList.add('opacity-0', 'pointer-events-none');
    retryErrorLogsModalContainer.classList.add('scale-95');
    retryErrorLogsModalContainer.classList.remove('scale-100');

    // Memory release: immediately clear DOM elements in tbody and clear array cache reference
    if (retryErrorLogsTableBody) {
        retryErrorLogsTableBody.innerHTML = '';
    }
    if (retryErrorLogsPaginationControls) {
        retryErrorLogsPaginationControls.innerHTML = '';
    }
    if (valRetryErrorLogsShowingText) {
        valRetryErrorLogsShowingText.textContent = '';
    }
    allLogs = [];
}

/**
 * Fetch logs from backend and render them
 */
async function fetchAndRenderLogs() {
    try {
        allLogs = await ipcRenderer.invoke('retry-error-logs:get') || [];
    } catch (e) {
        console.error('Failed to fetch retry/error logs:', e);
        allLogs = [];
    }
    renderLogs();
}

/**
 * Filter and render logs based on the current filter selection
 */
function renderLogs() {
    if (!retryErrorLogsTableBody || !retryErrorLogsEmpty || !retryErrorLogsCount) return;

    const filterVal = logTypeFilter ? logTypeFilter.value : 'ALL';
    const dict = i18n[state.currentLanguage] || {};

    // Filter logs based on RETRY or ERROR type
    const filteredLogs = allLogs.filter(log => {
        if (filterVal === 'ALL') return true;
        return log.type === filterVal;
    });

    const totalItems = filteredLogs.length;
    const totalPages = Math.ceil(totalItems / retryErrorItemsPerPage) || 1;
    if (retryErrorCurrentPage > totalPages) retryErrorCurrentPage = totalPages;
    if (retryErrorCurrentPage < 1) retryErrorCurrentPage = 1;

    const startIndex = (retryErrorCurrentPage - 1) * retryErrorItemsPerPage;
    const endIndex = Math.min(startIndex + retryErrorItemsPerPage, totalItems);
    const paginated = filteredLogs.slice(startIndex, endIndex);

    // Update the record count text
    const countText = dict.logCountText || '条记录';
    retryErrorLogsCount.textContent = `${totalItems} ${countText}`;

    // Toggle pagination wrapper visibility
    if (retryErrorLogsPaginationWrapper) {
        retryErrorLogsPaginationWrapper.classList.toggle('hidden', totalItems === 0);
    }

    // Render empty state if there are no matching logs
    if (totalItems === 0) {
        retryErrorLogsTableBody.innerHTML = '';
        retryErrorLogsEmpty.classList.remove('hidden');
        if (retryErrorLogsPaginationControls) retryErrorLogsPaginationControls.innerHTML = '';
        if (valRetryErrorLogsShowingText) valRetryErrorLogsShowingText.textContent = '';
        return;
    }

    retryErrorLogsEmpty.classList.add('hidden');
    retryErrorLogsTableBody.innerHTML = '';

    // Append rows using a DocumentFragment to minimize paint operations
    const fragment = document.createDocumentFragment();
    paginated.forEach(log => {
        const tr = document.createElement('tr');
        tr.className = 'hover:bg-slate-50/50 dark:hover:bg-white/5 transition-colors border-b border-outline-variant/10';

        // Build type badge
        let typeBadge = '';
        if (log.type === 'RETRY') {
            typeBadge = `<span class="px-2 py-0.5 rounded bg-amber-500/10 text-amber-500 font-bold dark:bg-amber-400/10 dark:text-amber-400">RETRY</span>`;
        } else {
            typeBadge = `<span class="px-2 py-0.5 rounded bg-rose-500/10 text-rose-500 font-bold dark:bg-rose-400/10 dark:text-rose-400">ERROR</span>`;
        }

        // Build attempt info
        let attemptText = '';
        if (log.type === 'RETRY') {
            const pattern = dict.attemptText || '第 {attempt} 次';
            attemptText = pattern.replace('{attempt}', String(log.attempt || 1));
        } else {
            attemptText = `<span class="text-rose-500 dark:text-rose-400 font-semibold">${dict.finalFail || '最终失败'}</span>`;
        }

        tr.innerHTML = `
            <td class="px-4 py-3 font-data-mono text-slate-500 dark:text-slate-400 whitespace-nowrap">${log.timestamp || '-'}</td>
            <td class="px-4 py-3 whitespace-nowrap">${typeBadge}</td>
            <td class="px-4 py-3 whitespace-nowrap">${attemptText}</td>
            <td class="px-4 py-3 font-data-mono whitespace-nowrap truncate max-w-[120px]" title="${log.account || '-'}">${log.account || '-'}</td>
            <td class="px-4 py-3 font-semibold text-primary dark:text-primary-fixed-dim whitespace-nowrap">${log.model || '-'}</td>
            <td class="px-4 py-3 font-data-mono text-slate-600 dark:text-slate-300 break-all select-all">${log.path || '-'}</td>
            <td class="px-4 py-3 text-slate-600 dark:text-slate-300 break-all select-text font-sans max-w-[300px] leading-relaxed text-[11px]">${log.error || '-'}</td>
        `;
        fragment.appendChild(tr);
    });

    retryErrorLogsTableBody.appendChild(fragment);

    // Update showing text
    if (valRetryErrorLogsShowingText) {
        valRetryErrorLogsShowingText.textContent = state.currentLanguage === 'zh'
            ? `显示第 ${startIndex + 1} 到 ${endIndex} 条，共 ${totalItems} 条记录`
            : `Showing ${startIndex + 1} to ${endIndex} of ${totalItems} entries`;
    }

    // Render pagination buttons
    if (retryErrorLogsPaginationControls) {
        retryErrorLogsPaginationControls.innerHTML = '';

        const addBtn = (label: string, pageNum: number, isActive = false, isDisabled = false) => {
            const btn = document.createElement('button');
            btn.className = `px-2.5 py-1 border border-outline-variant/60 rounded text-[12px] transition-colors ${
                isActive ? 'bg-primary text-white border-primary dark:bg-primary-container dark:border-primary-container' : 'bg-white dark:bg-[#1a1f30] text-on-surface dark:text-white hover:bg-slate-50 dark:hover:bg-white/5'
            } ${isDisabled ? 'opacity-40 cursor-not-allowed' : ''}`;
            btn.textContent = label;
            if (!isDisabled) {
                btn.addEventListener('click', () => {
                    retryErrorCurrentPage = pageNum;
                    renderLogs();
                });
            } else {
                btn.disabled = true;
            }
            retryErrorLogsPaginationControls!.appendChild(btn);
        };

        addBtn(state.currentLanguage === 'zh' ? '上一页' : 'Prev', retryErrorCurrentPage - 1, false, retryErrorCurrentPage === 1);

        let startPage = Math.max(1, retryErrorCurrentPage - 1);
        let endPage = Math.min(totalPages, startPage + 2);
        if (endPage - startPage < 2) {
            startPage = Math.max(1, endPage - 2);
        }

        for (let p = startPage; p <= endPage; p++) {
            addBtn(p.toString(), p, p === retryErrorCurrentPage);
        }

        if (endPage < totalPages) {
            const span = document.createElement('span');
            span.className = 'px-1 text-outline align-bottom';
            span.textContent = '...';
            retryErrorLogsPaginationControls.appendChild(span);
            addBtn(totalPages.toString(), totalPages);
        }

        addBtn(state.currentLanguage === 'zh' ? '下一页' : 'Next', retryErrorCurrentPage + 1, false, retryErrorCurrentPage === totalPages);
    }
}
