import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';
import { switchAutoTriggerPanel } from './accountsController';

// State for pagination
let currentPage = 1;
const pageSize = 10;
let totalRecords = 0;

let btnViewTriggerHistory: HTMLButtonElement | null;
let btnHistoryBackToList: HTMLButtonElement | null;
let btnClearTriggerHistory: HTMLButtonElement | null;
let autoTriggerHistoryTableBody: HTMLTableSectionElement | null;
let btnHistoryPrevPage: HTMLButtonElement | null;
let btnHistoryNextPage: HTMLButtonElement | null;
let historyPageStatus: HTMLDivElement | null;

export function initAutotriggerHistoryEvents() {
    btnViewTriggerHistory = document.getElementById('btnViewTriggerHistory') as HTMLButtonElement | null;
    btnHistoryBackToList = document.getElementById('btnHistoryBackToList') as HTMLButtonElement | null;
    btnClearTriggerHistory = document.getElementById('btnClearTriggerHistory') as HTMLButtonElement | null;
    autoTriggerHistoryTableBody = document.getElementById('autoTriggerHistoryTableBody') as HTMLTableSectionElement | null;
    btnHistoryPrevPage = document.getElementById('btnHistoryPrevPage') as HTMLButtonElement | null;
    btnHistoryNextPage = document.getElementById('btnHistoryNextPage') as HTMLButtonElement | null;
    historyPageStatus = document.getElementById('historyPageStatus') as HTMLDivElement | null;

    if (btnViewTriggerHistory) {
        btnViewTriggerHistory.addEventListener('click', () => {
            switchAutoTriggerPanel('history');
            currentPage = 1;
            loadTriggerHistory();
        });
    }

    if (btnHistoryBackToList) {
        btnHistoryBackToList.addEventListener('click', () => {
            switchAutoTriggerPanel('list');
        });
    }

    if (btnClearTriggerHistory) {
        btnClearTriggerHistory.addEventListener('click', async () => {
            const isZH = state.currentLanguage === 'zh';
            const confirmMsg = isZH 
                ? '确定要清空所有自动化触发历史记录吗？' 
                : 'Are you sure you want to clear all trigger history?';
            
            // Call global $confirm
            const confirmed = await (window as any).$confirm(confirmMsg);
            if (!confirmed) return;

            try {
                const res = await ipcRenderer.invoke('autotrigger:history:clear');
                if (res && res.success) {
                    currentPage = 1;
                    loadTriggerHistory();
                } else {
                    alert((isZH ? '清空历史失败: ' : 'Failed to clear history: ') + (res?.error || 'Unknown error'));
                }
            } catch (err: any) {
                alert((isZH ? '发生异常: ' : 'Exception: ') + err.message);
            }
        });
    }

    if (btnHistoryPrevPage) {
        btnHistoryPrevPage.addEventListener('click', () => {
            if (currentPage > 1) {
                currentPage--;
                loadTriggerHistory();
            }
        });
    }

    if (btnHistoryNextPage) {
        btnHistoryNextPage.addEventListener('click', () => {
            const maxPage = Math.ceil(totalRecords / pageSize);
            if (currentPage < maxPage) {
                currentPage++;
                loadTriggerHistory();
            }
        });
    }
}

async function loadTriggerHistory() {
    if (!autoTriggerHistoryTableBody) return;

    const isZH = state.currentLanguage === 'zh';
    autoTriggerHistoryTableBody.innerHTML = `
        <tr>
            <td class="p-8 text-center text-outline dark:text-outline-variant italic" colspan="7">
                ${isZH ? '⏳ 正在加载触发历史记录...' : '⏳ Loading trigger history...'}
            </td>
        </tr>
    `;

    try {
        const res = await ipcRenderer.invoke('autotrigger:history:list', { page: currentPage, pageSize });
        if (res && res.success) {
            totalRecords = res.total;
            renderHistoryTable(res.histories || []);
            updatePaginationUI();
        } else {
            autoTriggerHistoryTableBody.innerHTML = `
                <tr>
                    <td class="p-8 text-center text-red-400" colspan="7">
                        ❌ ${isZH ? '加载失败: ' : 'Load failed: '}${res?.error || (isZH ? '未知错误' : 'Unknown error')}
                    </td>
                </tr>
            `;
        }
    } catch (err: any) {
        autoTriggerHistoryTableBody.innerHTML = `
            <tr>
                <td class="p-8 text-center text-red-400" colspan="7">
                    ❌ ${isZH ? '加载发生异常: ' : 'Exception during loading: '}${err.message}
                </td>
            </tr>
        `;
    }
}

function renderHistoryTable(histories: Array<any>) {
    const tableBody = autoTriggerHistoryTableBody;
    if (!tableBody) return;
    tableBody.innerHTML = '';

    const isZH = state.currentLanguage === 'zh';

    if (histories.length === 0) {
        tableBody.innerHTML = `
            <tr>
                <td class="p-8 text-center text-outline dark:text-outline-variant italic" colspan="7">
                    ${isZH ? '暂无触发历史记录。' : 'No history records found.'}
                </td>
            </tr>
        `;
        return;
    }

    histories.forEach(h => {
        const tr = document.createElement('tr');
        tr.className = 'hover:bg-slate-50 dark:hover:bg-white/5 transition-colors border-b border-outline-variant/10';

        // Format trigger time
        let formattedTime = h.triggerTime;
        try {
            const date = new Date(h.triggerTime);
            if (!isNaN(date.getTime())) {
                const pad = (n: number) => n.toString().padStart(2, '0');
                formattedTime = `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`;
            }
        } catch (_) {}

        // Format trigger type badge
        const triggerTypeBadge = h.triggerType === 'timer'
            ? `<span class="inline-flex items-center gap-0.5 px-1.5 py-0.5 rounded text-[9.5px] font-bold bg-blue-100 dark:bg-blue-950/40 text-blue-500">
                ${isZH ? '定时' : 'Timer'}
               </span>`
            : `<span class="inline-flex items-center gap-0.5 px-1.5 py-0.5 rounded text-[9.5px] font-bold bg-purple-100 dark:bg-purple-950/40 text-purple-400">
                ${isZH ? '配额重置' : 'Quota Reset'}
               </span>`;

        // Format status badge
        const isSuccess = h.status === 'success';
        const statusBadge = isSuccess
            ? `<span class="inline-flex items-center px-1.5 py-0.5 rounded text-[9.5px] font-bold bg-emerald-100 dark:bg-emerald-950/40 text-emerald-500">
                ${isZH ? '成功' : 'Success'}
               </span>`
            : `<span class="inline-flex items-center px-1.5 py-0.5 rounded text-[9.5px] font-bold bg-rose-100 dark:bg-rose-950/40 text-rose-500">
                ${isZH ? '失败' : 'Failed'}
               </span>`;

        // Clean details message to avoid raw HTML injection and format neatly
        const rawMsg = h.message || '';
        const escapedMsg = rawMsg
            .replace(/&/g, '&amp;')
            .replace(/</g, '&lt;')
            .replace(/>/g, '&gt;')
            .replace(/"/g, '&quot;')
            .replace(/'/g, '&#039;');

        tr.innerHTML = `
            <td class="p-3 font-mono text-[10px] text-outline truncate" title="${formattedTime}">${formattedTime}</td>
            <td class="p-3 font-bold text-on-surface dark:text-white truncate" title="${h.taskName}">${h.taskName}</td>
            <td class="p-3">${triggerTypeBadge}</td>
            <td class="p-3 truncate" title="${h.accountEmail}">${h.accountEmail}</td>
            <td class="p-3 font-mono text-[10px] truncate" title="${h.modelName}">${h.modelName}</td>
            <td class="p-3 text-center">${statusBadge}</td>
            <td class="p-3 truncate text-outline max-w-[200px]" title="${escapedMsg}">${escapedMsg}</td>
        `;

        tableBody.appendChild(tr);
    });
}

function updatePaginationUI() {
    if (!btnHistoryPrevPage || !btnHistoryNextPage || !historyPageStatus) return;

    const maxPage = Math.max(1, Math.ceil(totalRecords / pageSize));
    btnHistoryPrevPage.disabled = currentPage === 1;
    btnHistoryNextPage.disabled = currentPage === maxPage;

    const isZH = state.currentLanguage === 'zh';
    if (isZH) {
        historyPageStatus.textContent = `第 ${currentPage} 页 / 共 ${maxPage} 页 (共 ${totalRecords} 条记录)`;
    } else {
        historyPageStatus.textContent = `Page ${currentPage} of ${maxPage} (${totalRecords} records)`;
    }
}
