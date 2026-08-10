/**
 * 触发测试回复 Modal 控制逻辑：从 accountsController.ts 抽离的独立模块。
 *
 * 高内聚：承担批量选号 → 触发上游模型回复测试 → 渲染结果表的弹窗生命周期；
 * 自带 14 个 DOM handle、5 个函数，由 accountsController.initAccountsEvents 委托
 * 调用 initTriggerTestModalEvents() 完成 DOM 句柄赋值与事件绑定。
 * 依赖：state（选中账号）、ipcRenderer、i18n；跨模块回调 loadAccountQuota（accountsRenderer）
 * 与 updateBatchActionBarUI（accountsController 仍在用）由 init 时注入,避免循环 import。
 */
import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';
import i18n from '../shared/i18n';
import { loadAccountQuota } from './accountsRenderer';

// updateBatchActionBarUI 仍在 accountsController(本模块的"父"协调器)内定义,直接 import 会
// 形成循环(accountsController → triggerTestModal → accountsController)。改由 controller 在
// init 时把自身 updateBatchActionBarUI 注入 state.callbacks.updateBatchActionBarUI,本模块
// 经回调间接调用,切断循环 import。
function updateBatchActionBarUI(): void {
    if (typeof state.callbacks.updateBatchActionBarUI === 'function') {
        state.callbacks.updateBatchActionBarUI();
    }
}

// 触发测试回复 Modal 变量定义
let triggerTestModal: HTMLDivElement | null;
let triggerTestModalContainer: HTMLDivElement | null;
let btnTriggerModalClose: HTMLButtonElement | null;
let btnTriggerModalCancel: HTMLButtonElement | null;
let btnStartTriggerTest: HTMLButtonElement | null;
let btnStartTriggerIcon: HTMLElement | null;
let inputTriggerPrompt: HTMLInputElement | null;
let btnTriggerModalSelectAll: HTMLButtonElement | null;
let btnTriggerModalClearAll: HTMLButtonElement | null;
let triggerLogsArea: HTMLDivElement | null;
let triggerResultsContainer: HTMLDivElement | null;
let triggerResultsTableBody: HTMLTableSectionElement | null;
let triggerModalAccountCount: HTMLSpanElement | null;

// 句柄赋值 + 事件绑定（由 accountsController.initAccountsEvents 委托调用）
export function initTriggerTestModalEvents(): void {
    // 触发测试回复按钮绑定
    const btnTrigger = document.getElementById('btnTriggerTestResponse') as HTMLButtonElement | null;
    if (btnTrigger) {
        btnTrigger.addEventListener('click', triggerTestResponse);
    }

    // 触发测试刷新 Modal DOM 与事件绑定
    triggerTestModal = document.getElementById('triggerTestModal') as HTMLDivElement | null;
    triggerTestModalContainer = document.getElementById('triggerTestModalContainer') as HTMLDivElement | null;
    btnTriggerModalClose = document.getElementById('btnTriggerModalClose') as HTMLButtonElement | null;
    btnTriggerModalCancel = document.getElementById('btnTriggerModalCancel') as HTMLButtonElement | null;
    btnStartTriggerTest = document.getElementById('btnStartTriggerTest') as HTMLButtonElement | null;
    btnStartTriggerIcon = document.getElementById('btnStartTriggerIcon');
    inputTriggerPrompt = document.getElementById('inputTriggerPrompt') as HTMLInputElement | null;
    btnTriggerModalSelectAll = document.getElementById('btnTriggerModalSelectAll') as HTMLButtonElement | null;
    btnTriggerModalClearAll = document.getElementById('btnTriggerModalClearAll') as HTMLButtonElement | null;
    triggerLogsArea = document.getElementById('triggerLogsArea') as HTMLDivElement | null;
    triggerResultsContainer = document.getElementById('triggerResultsContainer') as HTMLDivElement | null;
    triggerResultsTableBody = document.getElementById('triggerResultsTableBody') as HTMLTableSectionElement | null;
    triggerModalAccountCount = document.getElementById('triggerModalAccountCount') as HTMLSpanElement | null;

    if (btnTriggerModalClose) {
        btnTriggerModalClose.addEventListener('click', hideTriggerTestModal);
    }
    if (btnTriggerModalCancel) {
        btnTriggerModalCancel.addEventListener('click', hideTriggerTestModal);
    }
    if (btnTriggerModalSelectAll) {
        btnTriggerModalSelectAll.addEventListener('click', () => {
            const checkboxes = document.querySelectorAll('.trigger-model-checkbox') as NodeListOf<HTMLInputElement>;
            checkboxes.forEach(cb => cb.checked = true);
        });
    }
    if (btnTriggerModalClearAll) {
        btnTriggerModalClearAll.addEventListener('click', () => {
            const checkboxes = document.querySelectorAll('.trigger-model-checkbox') as NodeListOf<HTMLInputElement>;
            checkboxes.forEach(cb => cb.checked = false);
        });
    }
    if (btnStartTriggerTest) {
        btnStartTriggerTest.addEventListener('click', startTriggerTestExecution);
    }
}

function triggerTestResponse() {
    if (state.selectedAccountIds.length === 0) {
        alert('请先勾选需要触发测试回复的账号！');
        return;
    }
    showTriggerTestModal();
}

// 全局 log 事件转发：由 accountsController.initAccountsGlobalEvents 中的 ipcRenderer.on('log')
// 回调委托调用,把带 [测试回复] 标记的日志渲染进触发测试 Modal 的进程日志区。
// 抽离原因:triggerLogsArea 句柄已随 triggerTestModal 迁出 controller,controller 无法再直接写。
export function appendTriggerTestLog(logText: string): void {
    if (!triggerLogsArea) return;
    if (!logText || !logText.includes('[测试回复]')) return;
    if (triggerLogsArea.innerHTML.includes('等待配置')) {
        triggerLogsArea.innerHTML = '';
    }
    const div = document.createElement('div');
    if (logText.includes('❌')) {
        div.className = 'text-red-400';
    } else if (logText.includes('✅')) {
        div.className = 'text-emerald-400';
    } else if (logText.includes('⚡') || logText.includes('🏁')) {
        div.className = 'text-amber-400 font-medium';
    } else {
        div.className = 'text-slate-300';
    }
    div.textContent = logText;
    triggerLogsArea.appendChild(div);

    // 限制子节点上限 100,防止 DOM 内存泄漏
    if (triggerLogsArea.children.length > 100) {
        while (triggerLogsArea.children.length > 80) {
            if (triggerLogsArea.firstChild) {
                triggerLogsArea.removeChild(triggerLogsArea.firstChild);
            }
        }
    }

    triggerLogsArea.scrollTop = triggerLogsArea.scrollHeight;
}

function showTriggerTestModal() {
    if (!triggerTestModal || !triggerTestModalContainer) return;

    if (triggerModalAccountCount) {
        triggerModalAccountCount.textContent = `已选择 ${state.selectedAccountIds.length} 个账号`;
    }

    if (inputTriggerPrompt) {
        inputTriggerPrompt.value = 'ok';
        inputTriggerPrompt.disabled = false;
    }

    const checkboxes = document.querySelectorAll('.trigger-model-checkbox') as NodeListOf<HTMLInputElement>;
    checkboxes.forEach(cb => {
        cb.disabled = false;
        cb.checked = (cb.value === 'gemini-3.5-flash');
    });

    if (triggerLogsArea) {
        triggerLogsArea.innerHTML = '<div class="text-outline dark:text-outline-variant italic">等待配置并开始触发...</div>';
    }

    if (triggerResultsContainer) {
        triggerResultsContainer.classList.add('hidden');
    }

    if (triggerResultsTableBody) {
        triggerResultsTableBody.innerHTML = '';
    }

    if (btnStartTriggerTest) {
        btnStartTriggerTest.disabled = false;
        const span = btnStartTriggerTest.querySelector('span:last-child');
        if (span) span.textContent = '开始触发';
    }
    if (btnTriggerModalCancel) {
        btnTriggerModalCancel.textContent = '取消';
        btnTriggerModalCancel.disabled = false;
    }
    if (btnTriggerModalClose) {
        btnTriggerModalClose.disabled = false;
    }

    triggerTestModal.classList.remove('pointer-events-none', 'opacity-0');
    triggerTestModal.classList.add('opacity-100');
    triggerTestModalContainer.classList.remove('scale-95');
    triggerTestModalContainer.classList.add('scale-100');
}

function hideTriggerTestModal() {
    if (!triggerTestModal || !triggerTestModalContainer) return;

    if (btnStartTriggerTest && btnStartTriggerTest.disabled && triggerResultsContainer?.classList.contains('hidden')) {
        return;
    }

    triggerTestModal.classList.add('opacity-0', 'pointer-events-none');
    triggerTestModal.classList.remove('opacity-100');
    triggerTestModalContainer.classList.add('scale-95');
    triggerTestModalContainer.classList.remove('scale-100');

    if (state.selectedAccountIds.length > 0 && triggerResultsContainer && !triggerResultsContainer.classList.contains('hidden')) {
        const accountsListEl = document.getElementById('accountsList');
        if (accountsListEl) {
            for (const accountId of state.selectedAccountIds) {
                const card = accountsListEl.querySelector(`[data-account-id="${accountId}"]`);
                const quotaBars = document.getElementById(`quotaBars-${accountId}`);
                const refreshBtn = card?.querySelector('[data-quota-refresh-btn]') as HTMLElement | null;
                if (quotaBars) {
                    loadAccountQuota(accountId, quotaBars, refreshBtn, true, {});
                }
            }
        }

        state.selectedAccountIds = [];
        updateBatchActionBarUI();
        const chkAll = document.getElementById('chkSelectAllAccounts') as HTMLInputElement | null;
        if (chkAll) chkAll.checked = false;
    }
}

async function startTriggerTestExecution() {
    if (!btnStartTriggerTest || !triggerLogsArea) return;

    const checkedBoxes = document.querySelectorAll('.trigger-model-checkbox:checked') as NodeListOf<HTMLInputElement>;
    if (checkedBoxes.length === 0) {
        alert('请先选择至少一个测试模型！');
        return;
    }

    const modelNames = Array.from(checkedBoxes).map(cb => cb.value);
    const prompt = inputTriggerPrompt ? inputTriggerPrompt.value.trim() : 'ok';

    if (inputTriggerPrompt) inputTriggerPrompt.disabled = true;
    const checkboxes = document.querySelectorAll('.trigger-model-checkbox') as NodeListOf<HTMLInputElement>;
    checkboxes.forEach(cb => cb.disabled = true);
    
    btnStartTriggerTest.disabled = true;
    if (btnTriggerModalCancel) btnTriggerModalCancel.disabled = true;
    if (btnTriggerModalClose) btnTriggerModalClose.disabled = true;

    if (btnStartTriggerIcon) btnStartTriggerIcon.classList.add('animate-spin');
    const span = btnStartTriggerTest.querySelector('span:last-child');
    if (span) span.textContent = '正在触发...';

    triggerLogsArea.innerHTML = '<div class="text-primary font-bold">⚡ [测试任务] 开始批量向后端发送请求...</div>';

    try {
        const res = await ipcRenderer.invoke('accounts:trigger-test-response', {
            accountIds: state.selectedAccountIds,
            modelNames: modelNames,
            prompt: prompt
        });

        if (res && res.success && res.results) {
            renderTriggerResultsTable(res.results);
            const finishDiv = document.createElement('div');
            finishDiv.className = 'text-emerald-400 font-bold mt-2';
            finishDiv.textContent = `🏁 [测试任务] 执行完毕！成功数量: ${res.successCount}/${res.totalCount}`;
            triggerLogsArea.appendChild(finishDiv);
            triggerLogsArea.scrollTop = triggerLogsArea.scrollHeight;
        } else {
            const errDiv = document.createElement('div');
            errDiv.className = 'text-red-400 font-bold mt-2';
            errDiv.textContent = '❌ [测试任务] 执行失败: ' + (res?.error || '未知错误');
            triggerLogsArea.appendChild(errDiv);
        }
    } catch (err: any) {
        const errDiv = document.createElement('div');
        errDiv.className = 'text-red-400 font-bold mt-2';
        errDiv.textContent = '❌ [测试任务] 执行时发生错误: ' + err.message;
        triggerLogsArea.appendChild(errDiv);
    } finally {
        if (btnStartTriggerIcon) btnStartTriggerIcon.classList.remove('animate-spin');
        if (span) span.textContent = '已完成';
        
        if (btnTriggerModalCancel) {
            btnTriggerModalCancel.disabled = false;
            btnTriggerModalCancel.textContent = '关闭';
        }
        if (btnTriggerModalClose) btnTriggerModalClose.disabled = false;
    }
}

function renderTriggerResultsTable(results: Array<{
    email: string;
    success: boolean;
    modelResults: Array<{
        model: string;
        success: boolean;
        response?: string;
        error?: string;
    }>;
}>) {
    const tableBody = triggerResultsTableBody;
    if (!tableBody || !triggerResultsContainer) return;
    const dict = i18n[state.currentLanguage] || i18n.zh;

    tableBody.innerHTML = '';
    
    results.forEach(accRes => {
        const email = accRes.email;
        if (!accRes.modelResults || accRes.modelResults.length === 0) {
            const tr = document.createElement('tr');
            tr.className = 'hover:bg-slate-50 dark:hover:bg-white/5 transition-colors border-b border-outline-variant/10';
            tr.innerHTML = `
                <td class="p-2.5 font-medium truncate" title="${email}">${email}</td>
                <td class="p-2.5 text-outline">-</td>
                <td class="p-2.5 text-center">
                    <span class="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-bold bg-red-100 dark:bg-red-950/40 text-red-500">${state.currentLanguage === 'zh' ? '失败' : 'Failed'}</span>
                </td>
                <td class="p-2.5 text-red-400 truncate" title="${state.currentLanguage === 'zh' ? '未返回模型结果' : 'No response returned'}">${state.currentLanguage === 'zh' ? '未返回模型结果' : 'No response'}</td>
            `;
            tableBody.appendChild(tr);
            return;
        }

        accRes.modelResults.forEach(modelRes => {
            const tr = document.createElement('tr');
            tr.className = 'hover:bg-slate-50 dark:hover:bg-white/5 transition-colors border-b border-outline-variant/10';
            
            const statusBadge = modelRes.success 
                ? `<span class="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-bold bg-emerald-100 dark:bg-emerald-950/40 text-emerald-500">${state.currentLanguage === 'zh' ? '成功' : 'Success'}</span>`
                : `<span class="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-bold bg-red-100 dark:bg-red-950/40 text-red-500">${state.currentLanguage === 'zh' ? '失败' : 'Failed'}</span>`;
            
            let detailText = '-';
            let detailClass = 'text-outline';
            if (modelRes.success) {
                detailText = modelRes.response || (state.currentLanguage === 'zh' ? '(无内容)' : '(No content)');
                detailClass = 'text-emerald-500 dark:text-emerald-400 truncate font-mono';
            } else {
                detailText = modelRes.error || (state.currentLanguage === 'zh' ? '未知错误' : 'Unknown error');
                detailClass = 'text-red-400 truncate';
            }

            const cursorClass = detailText !== '-' ? 'cursor-pointer hover:underline hover:text-primary dark:hover:text-primary-fixed-dim detail-cell' : '';

            tr.innerHTML = `
                <td class="p-2.5 font-medium truncate" title="${email}">${email}</td>
                <td class="p-2.5 font-mono text-[11px]">${modelRes.model}</td>
                <td class="p-2.5 text-center">${statusBadge}</td>
                <td class="p-2.5 ${detailClass} ${cursorClass}" title="${detailText !== '-' ? (state.currentLanguage === 'zh' ? '点击可在上方进程日志区查看格式化 JSON' : 'Click to view formatted JSON in the log area above') : ''}">${detailText}</td>
            `;

            const detailTd = tr.querySelector('.detail-cell');
            if (detailTd) {
                detailTd.addEventListener('click', () => {
                    const logArea = triggerLogsArea;
                    if (logArea) {
                        let formattedText = detailText;
                        try {
                            const parsed = JSON.parse(detailText);
                            formattedText = JSON.stringify(parsed, null, 4);
                        } catch (e) {
                            // ignore, keep raw
                        }

                        if (logArea.innerHTML.includes('等待配置')) {
                            logArea.innerHTML = '';
                        }

                        const div = document.createElement('div');
                        div.className = 'mt-3 p-3 bg-slate-900 border border-primary/20 rounded-lg text-emerald-400 font-mono text-[11px] whitespace-pre-wrap leading-relaxed animate-fadeIn';
                        div.innerHTML = state.currentLanguage === 'zh'
                            ? `<span class="text-amber-400 font-bold">📋 [详情查看] 账号 ${email} - 模型 ${modelRes.model} 的响应 JSON:</span>\n${formattedText}`
                            : `<span class="text-amber-400 font-bold">📋 [Details View] Account ${email} - Model ${modelRes.model} Response JSON:</span>\n${formattedText}`;

                        logArea.appendChild(div);
                        logArea.scrollTop = logArea.scrollHeight;
                    }
                });
            }

            tableBody.appendChild(tr);
        });
    });

    triggerResultsContainer.classList.remove('hidden');
}
