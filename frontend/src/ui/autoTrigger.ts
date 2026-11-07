/**
 * 自动化定时与刷新任务包 Modal 控制逻辑：从 accountsController.ts 抽离的独立模块。
 *
 * 高内聚：本模块承担自动化触发任务包的列表渲染、新建/编辑表单填充、各账号独立模型配置、保存与状态切换；
 * 依赖：state（语言+账号列表）、ipcRenderer、全局 $confirm / alert；无跨簇 handle 引用。
 */
import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';
import {
    fetchChannelModels,
    mergeModelsWithPreserved,
    getCachedModels,
    renderCategorizedCheckboxes,
} from './channelModelFetcher';

// 自动化任务包 Modal 变量定义
let btnManageAutoTrigger: HTMLButtonElement | null,
    btnAutoTriggerModalClose: HTMLButtonElement | null,
    btnAutoTriggerModalCloseSecondary: HTMLButtonElement | null,
    btnCreateNewTask: HTMLButtonElement | null,
    btnEditFetchModels: HTMLButtonElement | null,
    btnEditSelectAllAccounts: HTMLButtonElement | null,
    btnEditClearAllAccounts: HTMLButtonElement | null,
    btnEditSelectAllModels: HTMLButtonElement | null,
    btnEditClearAllModels: HTMLButtonElement | null,
    btnEditAutoFillModels: HTMLButtonElement | null,
    btnEditSyncToAllAccounts: HTMLButtonElement | null,
    btnCancelEditTask: HTMLButtonElement | null,
    btnSaveTask: HTMLButtonElement | null;

let autoTriggerModal: HTMLDivElement | null,
    autoTriggerModalContainer: HTMLDivElement | null,
    panelTaskList: HTMLDivElement | null,
    panelTaskEdit: HTMLDivElement | null,
    footerTaskList: HTMLDivElement | null,
    footerTaskEdit: HTMLDivElement | null,
    containerTaskInterval: HTMLDivElement | null,
    editAccountsGrid: HTMLDivElement | null,
    editAccountTabsContainer: HTMLDivElement | null,
    activeAccountLabel: HTMLSpanElement | null,
    activeAccountSelectedCount: HTMLDivElement | null,
    editModelsGemini: HTMLDivElement | null,
    editModelsClaude: HTMLDivElement | null,
    editModelsOthers: HTMLDivElement | null,
    editModelsStatusMsg: HTMLDivElement | null;

let editTaskId: HTMLInputElement | null,
    editTaskName: HTMLInputElement | null,
    editTaskPrompt: HTMLInputElement | null,
    editTaskInterval: HTMLInputElement | null;

let editTaskTriggerType: HTMLSelectElement | null,
    autoTriggerTasksTableBody: HTMLTableSectionElement | null,
    iconEditFetchModels: HTMLElement | null,
    textEditFetchModels: HTMLSpanElement | null,
    currentEditingTask: any = null;

// 各账号专属模型配置状态字典: accountID -> modelNames[]
let taskAccountModels: Record<string, string[]> = {};
let activeAccountId: string | null = null;

// ==================== 自动化定时与刷新任务 Modal 控制逻辑 ====================

export function initAutoTriggerModalEvents(): void {
    const byId = <T extends HTMLElement>(id: string) => document.getElementById(id) as T | null;

    btnManageAutoTrigger = byId('btnManageAutoTrigger');
    autoTriggerModal = byId('autoTriggerModal');
    autoTriggerModalContainer = byId('autoTriggerModalContainer');
    btnAutoTriggerModalClose = byId('btnAutoTriggerModalClose');
    btnAutoTriggerModalCloseSecondary = byId('btnAutoTriggerModalCloseSecondary');
    panelTaskList = byId('panelTaskList');
    panelTaskEdit = byId('panelTaskEdit');
    footerTaskList = byId('footerTaskList');
    footerTaskEdit = byId('footerTaskEdit');
    btnCreateNewTask = byId('btnCreateNewTask');
    autoTriggerTasksTableBody = byId('autoTriggerTasksTableBody');
    editTaskId = byId('editTaskId');
    editTaskName = byId('editTaskName');
    editTaskPrompt = byId('editTaskPrompt');
    editTaskTriggerType = byId('editTaskTriggerType');
    editTaskInterval = byId('editTaskInterval');
    containerTaskInterval = byId('containerTaskInterval');
    editAccountsGrid = byId('editAccountsGrid');
    editAccountTabsContainer = byId('editAccountTabsContainer');
    activeAccountLabel = byId('activeAccountLabel');
    activeAccountSelectedCount = byId('activeAccountSelectedCount');
    editModelsGemini = byId('editModelsGemini');
    editModelsClaude = byId('editModelsClaude');
    editModelsOthers = byId('editModelsOthers');
    btnEditFetchModels = byId('btnEditFetchModels');
    btnEditAutoFillModels = byId('btnEditAutoFillModels');
    btnEditSyncToAllAccounts = byId('btnEditSyncToAllAccounts');
    iconEditFetchModels = byId('iconEditFetchModels');
    textEditFetchModels = byId('textEditFetchModels');
    editModelsStatusMsg = byId('editModelsStatusMsg');
    btnEditSelectAllAccounts = byId('btnEditSelectAllAccounts');
    btnEditClearAllAccounts = byId('btnEditClearAllAccounts');
    btnEditSelectAllModels = byId('btnEditSelectAllModels');
    btnEditClearAllModels = byId('btnEditClearAllModels');
    btnCancelEditTask = byId('btnCancelEditTask');
    btnSaveTask = byId('btnSaveTask');

    btnEditFetchModels?.addEventListener('click', () => loadAndRenderAutoTriggerModels(currentEditingTask, true));
    btnEditAutoFillModels?.addEventListener('click', handleAutoFillModelsForActiveAccount);
    btnEditSyncToAllAccounts?.addEventListener('click', handleSyncModelsToAllAccounts);
    btnManageAutoTrigger?.addEventListener('click', openAutoTriggerModal);
    btnAutoTriggerModalClose?.addEventListener('click', closeAutoTriggerModal);
    btnAutoTriggerModalCloseSecondary?.addEventListener('click', closeAutoTriggerModal);
    btnCreateNewTask?.addEventListener('click', () => { prepareTaskEditForm(); switchAutoTriggerPanel('edit'); });
    btnCancelEditTask?.addEventListener('click', () => switchAutoTriggerPanel('list'));
    btnSaveTask?.addEventListener('click', saveAutoTriggerTask);
    editTaskTriggerType?.addEventListener('change', () => {
        containerTaskInterval?.classList.toggle('hidden', editTaskTriggerType?.value !== 'timer');
    });

    const toggleAll = (selector: string, checked: boolean, root: ParentNode = document) =>
        root.querySelectorAll<HTMLInputElement>(selector).forEach(cb => cb.checked = checked);

    btnEditSelectAllAccounts?.addEventListener('click', () => {
        toggleAll('input[type="checkbox"]', true, editAccountsGrid || document);
        handleAccountsSelectionChange();
    });
    btnEditClearAllAccounts?.addEventListener('click', () => {
        toggleAll('input[type="checkbox"]', false, editAccountsGrid || document);
        handleAccountsSelectionChange();
    });
    btnEditSelectAllModels?.addEventListener('click', () => {
        toggleAll('.edit-model-cb', true);
        saveCurrentAccountSelection();
        updateActiveAccountCount();
        updateAccountTabBadge(activeAccountId);
    });
    btnEditClearAllModels?.addEventListener('click', () => {
        toggleAll('.edit-model-cb', false);
        saveCurrentAccountSelection();
        updateActiveAccountCount();
        updateAccountTabBadge(activeAccountId);
    });
}

function openAutoTriggerModal() {
    if (!autoTriggerModal || !autoTriggerModalContainer) return;
    autoTriggerModal.classList.remove('opacity-0', 'pointer-events-none');
    autoTriggerModalContainer.classList.remove('scale-95');
    autoTriggerModalContainer.classList.add('scale-100');
    
    switchAutoTriggerPanel('list');
    loadAutoTriggerTasks();
}

function closeAutoTriggerModal() {
    if (!autoTriggerModal || !autoTriggerModalContainer) return;
    autoTriggerModalContainer.classList.remove('scale-100');
    autoTriggerModalContainer.classList.add('scale-95');
    autoTriggerModal.classList.add('opacity-0', 'pointer-events-none');
}

export function switchAutoTriggerPanel(panel: 'list' | 'edit' | 'history') {
    if (!panelTaskList || !panelTaskEdit || !footerTaskList || !footerTaskEdit) return;
    const panelTaskHistory = document.getElementById('panelTaskHistory');

    // Hide all panels
    panelTaskList.classList.add('hidden');
    footerTaskList.classList.add('hidden');
    panelTaskEdit.classList.add('hidden');
    footerTaskEdit.classList.add('hidden');
    if (panelTaskHistory) panelTaskHistory.classList.add('hidden');

    if (panel === 'list') {
        panelTaskList.classList.remove('hidden');
        footerTaskList.classList.remove('hidden');
    } else if (panel === 'edit') {
        panelTaskEdit.classList.remove('hidden');
        footerTaskEdit.classList.remove('hidden');
    } else if (panel === 'history') {
        if (panelTaskHistory) panelTaskHistory.classList.remove('hidden');
    }
}

function setTableMessage(body: HTMLTableSectionElement | null, msg: string, isErr = false) {
    if (!body) return;
    body.innerHTML = `<tr><td class="p-8 text-center ${isErr ? 'text-red-400' : 'text-outline dark:text-outline-variant italic'}" colspan="6">${msg}</td></tr>`;
}

async function loadAutoTriggerTasks() {
    setTableMessage(autoTriggerTasksTableBody, state.currentLanguage === 'zh' ? '⏳ 正在加载定时任务列表...' : '⏳ Loading task list...');
    try {
        const res = await ipcRenderer.invoke('autotrigger:list');
        if (res && res.success && res.tasks) {
            renderAutoTriggerTasksTable(res.tasks);
        } else {
            setTableMessage(autoTriggerTasksTableBody, `❌ ${state.currentLanguage === 'zh' ? '加载失败: ' : 'Load failed: '}${res?.error || (state.currentLanguage === 'zh' ? '未知错误' : 'Unknown error')}`, true);
        }
    } catch (err: any) {
        setTableMessage(autoTriggerTasksTableBody, `❌ ${state.currentLanguage === 'zh' ? '加载发生异常: ' : 'Exception during loading: '}${err.message}`, true);
    }
}

function renderAutoTriggerTasksTable(tasks: Array<any>) {
    const tableBody = autoTriggerTasksTableBody;
    if (!tableBody) return;
    tableBody.innerHTML = '';

    if (tasks.length === 0) {
        tableBody.innerHTML = `
            <tr>
                <td class="p-8 text-center text-outline dark:text-outline-variant italic" colspan="6">
                    ${state.currentLanguage === 'zh' ? '暂无配置好的自动化任务包，点击上方“新建任务包”添加。' : 'No automated tasks configured, click "New Task Package" above to add.'}
                </td>
            </tr>
        `;
        return;
    }

    tasks.forEach(task => {
        const tr = document.createElement('tr');
        tr.className = 'hover:bg-slate-50 dark:hover:bg-white/5 transition-colors border-b border-outline-variant/10';

        const triggerTypeBadge = task.triggerType === 'timer'
            ? `<span class="inline-flex items-center gap-1 px-2 py-0.5 rounded text-[10px] font-bold bg-blue-100 dark:bg-blue-950/40 text-blue-500">
                <span class="material-symbols-outlined text-[12px]">schedule</span>
                ${state.currentLanguage === 'zh' ? `定时 (${Math.round(task.intervalSeconds / 60)}分钟)` : `Timer (${Math.round(task.intervalSeconds / 60)}m)`}
               </span>`
            : `<span class="inline-flex items-center gap-1 px-2 py-0.5 rounded text-[10px] font-bold bg-purple-100 dark:bg-purple-950/40 text-purple-400">
                <span class="material-symbols-outlined text-[12px]">sync</span>
                ${state.currentLanguage === 'zh' ? '到达重置时间' : 'Quota Reset Time'}
               </span>`;

        const accCount = task.accountIds ? task.accountIds.length : 0;
        let modelBadgeHtml = '';
        if (task.accountModels && Object.keys(task.accountModels).length > 0) {
            const distinctCount = task.modelNames ? task.modelNames.length : 0;
            modelBadgeHtml = `<span class="text-primary dark:text-primary-fixed-dim font-bold" title="${state.currentLanguage === 'zh' ? '各账号已配置专属触发模型' : 'Configured per-account'}">${distinctCount} 种 (${state.currentLanguage === 'zh' ? '按账号定制' : 'Custom'})</span>`;
        } else {
            const modelCount = task.modelNames ? task.modelNames.length : 0;
            modelBadgeHtml = `${state.currentLanguage === 'zh' ? `${modelCount} 个模型` : `${modelCount} models`}`;
        }

        const isChecked = task.enabled ? 'checked' : '';

        tr.innerHTML = `
            <td class="p-3 font-bold text-on-surface dark:text-white truncate" title="${task.name}">${task.name}</td>
            <td class="p-3">${triggerTypeBadge}</td>
            <td class="p-3 font-mono text-[11px]">${state.currentLanguage === 'zh' ? `${accCount} 个账号` : `${accCount} accounts`}</td>
            <td class="p-3 font-mono text-[11px]">${modelBadgeHtml}</td>
            <td class="p-3 text-center">
                <label class="switch">
                    <input type="checkbox" class="task-toggle-cb" data-task-id="${task.id}" ${isChecked}>
                    <span class="slider"></span>
                </label>
            </td>
            <td class="p-3 text-center whitespace-nowrap">
                <div class="inline-flex items-center justify-center gap-1.5 whitespace-nowrap text-[11px]">
                    <button class="btn-task-run text-emerald-600 dark:text-emerald-400 hover:underline font-bold whitespace-nowrap" data-task-id="${task.id}">${state.currentLanguage === 'zh' ? '立即触发' : 'Run Now'}</button>
                    <span class="text-outline/30 select-none">|</span>
                    <button class="btn-task-edit text-primary dark:text-primary-fixed-dim hover:underline font-bold whitespace-nowrap" data-task-id="${task.id}">${state.currentLanguage === 'zh' ? '编辑' : 'Edit'}</button>
                    <span class="text-outline/30 select-none">|</span>
                    <button class="btn-task-delete text-red-400 hover:underline font-bold whitespace-nowrap" data-task-id="${task.id}">${state.currentLanguage === 'zh' ? '删除' : 'Delete'}</button>
                </div>
            </td>
        `;

        tableBody.appendChild(tr);
    });

    const runBtns = tableBody.querySelectorAll('.btn-task-run') as NodeListOf<HTMLButtonElement>;
    runBtns.forEach(btn => {
        btn.addEventListener('click', async () => {
            const id = parseInt(btn.getAttribute('data-task-id') || '0', 10);
            const originalText = btn.textContent;
            btn.disabled = true;
            btn.textContent = state.currentLanguage === 'zh' ? '触发中...' : 'Running...';
            try {
                const res = await ipcRenderer.invoke('autotrigger:run', { id });
                if (res && res.success) {
                    alert(state.currentLanguage === 'zh'
                        ? '✅ 任务已开始后台执行，可在“查看历史记录”中查看实时状态与回复结果。'
                        : '✅ Task execution started, you can check progress in "History".');
                } else {
                    alert((state.currentLanguage === 'zh' ? '触发失败: ' : 'Failed to trigger: ') + (res?.error || (state.currentLanguage === 'zh' ? '未知错误' : 'Unknown error')));
                }
            } catch (err: any) {
                alert((state.currentLanguage === 'zh' ? '触发引发异常: ' : 'Exception triggering task: ') + err.message);
            } finally {
                btn.disabled = false;
                btn.textContent = originalText;
            }
        });
    });

    const toggleCbs = tableBody.querySelectorAll('.task-toggle-cb') as NodeListOf<HTMLInputElement>;
    toggleCbs.forEach(cb => {
        cb.addEventListener('change', async (e: any) => {
            const id = parseInt(cb.getAttribute('data-task-id') || '0', 10);
            const enabled = e.target.checked;
            try {
                await ipcRenderer.invoke('autotrigger:toggle', { id, enabled });
            } catch (err: any) {
                alert((state.currentLanguage === 'zh' ? '切换状态失败: ' : 'Failed to toggle status: ') + err.message);
                cb.checked = !enabled;
            }
        });
    });

    const editBtns = tableBody.querySelectorAll('.btn-task-edit') as NodeListOf<HTMLButtonElement>;
    editBtns.forEach(btn => {
        btn.addEventListener('click', () => {
            const id = parseInt(btn.getAttribute('data-task-id') || '0', 10);
            const targetTask = tasks.find(t => t.id === id);
            if (targetTask) {
                prepareTaskEditForm(targetTask);
                switchAutoTriggerPanel('edit');
            }
        });
    });

    const deleteBtns = tableBody.querySelectorAll('.btn-task-delete') as NodeListOf<HTMLButtonElement>;
    deleteBtns.forEach(btn => {
        btn.addEventListener('click', async () => {
            const id = parseInt(btn.getAttribute('data-task-id') || '0', 10);
            if (await $confirm(state.currentLanguage === 'zh' ? '确定要删除该自动化触发任务包吗？' : 'Are you sure you want to delete this automated task package?')) {
                try {
                    await ipcRenderer.invoke('autotrigger:delete', { id });
                    loadAutoTriggerTasks();
                } catch (err: any) {
                    alert((state.currentLanguage === 'zh' ? '删除失败: ' : 'Failed to delete: ') + err.message);
                }
            }
        });
    });
}

function getCheckedAccountIDs(): string[] {
    const cbs = editAccountsGrid?.querySelectorAll('.edit-acc-cb:checked') as NodeListOf<HTMLInputElement>;
    const ids: string[] = [];
    cbs?.forEach(cb => ids.push(cb.value));
    return ids;
}

function handleAccountsSelectionChange(): void {
    const checkedIds = getCheckedAccountIDs();
    // 为新勾选的账号初始化模型配置
    checkedIds.forEach(id => {
        if (!taskAccountModels[id]) {
            // 若当前有 active 账号已配置的模型，继承其作为默认初值；否则为空数组
            const defaultModels = activeAccountId && taskAccountModels[activeAccountId]
                ? [...taskAccountModels[activeAccountId]]
                : [];
            taskAccountModels[id] = defaultModels;
        }
    });

    if (!activeAccountId || !checkedIds.includes(activeAccountId)) {
        activeAccountId = checkedIds.length > 0 ? checkedIds[0] : null;
    }

    renderAccountTabs();
    updateModelCheckboxesForActiveAccount();
}

function saveCurrentAccountSelection(): void {
    if (activeAccountId) {
        taskAccountModels[activeAccountId] = getCurrentSelectedModelNames();
    }
}

function updateActiveAccountCount(): void {
    if (!activeAccountSelectedCount) return;
    const count = activeAccountId ? (taskAccountModels[activeAccountId] || []).length : 0;
    activeAccountSelectedCount.textContent = state.currentLanguage === 'zh'
        ? `已选 ${count} 个模型`
        : `${count} models selected`;
}

function updateAccountTabBadge(accId: string | null): void {
    if (!accId || !editAccountTabsContainer) return;
    const tabBtn = editAccountTabsContainer.querySelector(`[data-acc-id="${accId}"]`);
    if (tabBtn) {
        const badge = tabBtn.querySelector('.acc-tab-badge');
        const count = (taskAccountModels[accId] || []).length;
        if (badge) badge.textContent = `(${count})`;
    }
}

function renderAccountTabs(): void {
    const container = editAccountTabsContainer;
    if (!container) return;
    container.innerHTML = '';

    const checkedIds = getCheckedAccountIDs();
    if (checkedIds.length === 0) {
        container.innerHTML = `<span class="text-[10.5px] text-outline italic py-0.5">${state.currentLanguage === 'zh' ? '请先勾选上方关联账号' : 'Please check accounts above first'}</span>`;
        if (activeAccountLabel) activeAccountLabel.textContent = state.currentLanguage === 'zh' ? '未选择账号' : 'No account selected';
        updateActiveAccountCount();
        return;
    }

    checkedIds.forEach(accId => {
        const acc = (state.currentAccountsList || []).find((a: any) => a.id === accId);
        const email = acc ? acc.email : accId;
        const count = (taskAccountModels[accId] || []).length;
        const isActive = accId === activeAccountId;

        const btn = document.createElement('button');
        btn.type = 'button';
        btn.setAttribute('data-acc-id', accId);
        btn.className = isActive
            ? 'flex items-center gap-1 px-2.5 py-1 rounded-lg text-[11px] font-bold bg-primary text-white shadow-sm transition-all cursor-pointer whitespace-nowrap'
            : 'flex items-center gap-1 px-2.5 py-1 rounded-lg text-[11px] font-medium bg-slate-100 hover:bg-slate-200 dark:bg-white/5 dark:hover:bg-white/10 text-on-surface dark:text-slate-300 border border-outline-variant/30 transition-all cursor-pointer whitespace-nowrap';

        btn.innerHTML = `
            <span class="truncate max-w-[150px]">${escapeHtml(email)}</span>
            <span class="acc-tab-badge text-[10px] opacity-80">(${count})</span>
        `;

        btn.addEventListener('click', () => {
            saveCurrentAccountSelection();
            activeAccountId = accId;
            renderAccountTabs();
            updateModelCheckboxesForActiveAccount();
        });

        container.appendChild(btn);
    });

    // 更新当前正在配置的账号标签
    if (activeAccountLabel) {
        const activeAcc = (state.currentAccountsList || []).find((a: any) => a.id === activeAccountId);
        if (activeAcc) {
            const tierStr = activeAcc.tier ? ` [${activeAcc.tier}]` : '';
            activeAccountLabel.textContent = `${activeAcc.email}${tierStr}`;
        } else {
            activeAccountLabel.textContent = activeAccountId || (state.currentLanguage === 'zh' ? '未选择账号' : 'None');
        }
    }
    updateActiveAccountCount();
}

function updateModelCheckboxesForActiveAccount(): void {
    if (!activeAccountId) {
        document.querySelectorAll<HTMLInputElement>('.edit-model-cb').forEach(cb => cb.checked = false);
        updateActiveAccountCount();
        return;
    }

    const selectedSet = new Set<string>(taskAccountModels[activeAccountId] || []);
    const cbs = document.querySelectorAll<HTMLInputElement>('.edit-model-cb');
    cbs.forEach(cb => {
        cb.checked = selectedSet.has(cb.value);
    });

    updateActiveAccountCount();
    updateAccountTabBadge(activeAccountId);
}

function handleAutoFillModelsForActiveAccount(): void {
    if (!activeAccountId) {
        alert(state.currentLanguage === 'zh' ? '请先勾选并选择要配置的账号！' : 'Please select an account first!');
        return;
    }

    const activeAcc = (state.currentAccountsList || []).find((a: any) => a.id === activeAccountId);
    if (!activeAcc) return;

    if (activeAcc.supportedModels && activeAcc.supportedModels.length > 0) {
        taskAccountModels[activeAccountId] = [...activeAcc.supportedModels];
        updateModelCheckboxesForActiveAccount();
        updateAccountTabBadge(activeAccountId);
        alert(state.currentLanguage === 'zh'
            ? `✅ 已为账号 ${activeAcc.email} 自动填充其支持的 ${activeAcc.supportedModels.length} 个模型！`
            : `✅ Auto-filled ${activeAcc.supportedModels.length} supported models for ${activeAcc.email}!`);
    } else {
        alert(state.currentLanguage === 'zh'
            ? `⚠️ 账号 ${activeAcc.email} 尚未探测过可用模型列表。请先在账号池管理工具栏点击「检测可用模型」获取精准模型画像！`
            : `⚠️ Account ${activeAcc.email} has not probed available models yet.`);
    }
}

function handleSyncModelsToAllAccounts(): void {
    if (!activeAccountId) {
        alert(state.currentLanguage === 'zh' ? '请先勾选并选择要配置的账号！' : 'Please select an account first!');
        return;
    }

    saveCurrentAccountSelection();
    const currentModels = taskAccountModels[activeAccountId] || [];
    const checkedIds = getCheckedAccountIDs();

    if (checkedIds.length <= 1) {
        alert(state.currentLanguage === 'zh' ? '当前只有一个关联账号，无需同步。' : 'Only one account selected, no need to sync.');
        return;
    }

    const confirmMsg = state.currentLanguage === 'zh'
        ? `确定要将当前账号 (${currentModels.length} 个模型) 的勾选配置复制同步到其余 ${checkedIds.length - 1} 个关联账号吗？`
        : `Sync this account's model config to all other ${checkedIds.length - 1} accounts?`;

    if (confirm(confirmMsg)) {
        checkedIds.forEach(id => {
            taskAccountModels[id] = [...currentModels];
        });
        renderAccountTabs();
        alert(state.currentLanguage === 'zh'
            ? `✅ 已成功同步至所有 ${checkedIds.length} 个关联账号！`
            : `✅ Synced to all ${checkedIds.length} accounts!`);
    }
}

function prepareTaskEditForm(task?: any) {
    const accGrid = editAccountsGrid;
    if (!accGrid || !editTaskId || !editTaskName || !editTaskPrompt || !editTaskTriggerType || !editTaskInterval || !containerTaskInterval || !editModelsGemini || !editModelsClaude || !editModelsOthers) return;

    editTaskId.value = task ? task.id.toString() : '';
    editTaskName.value = task?.name || '';
    editTaskPrompt.value = task?.prompt || 'ok';
    editTaskTriggerType.value = task?.triggerType || 'timer';
    editTaskInterval.value = task ? Math.round((task.intervalSeconds || 3600) / 60).toString() : '60';
    containerTaskInterval.classList.toggle('hidden', editTaskTriggerType.value !== 'timer');

    // 初始化任务各账号专属模型映射
    taskAccountModels = {};
    if (task && task.accountModels && Object.keys(task.accountModels).length > 0) {
        for (const [k, v] of Object.entries(task.accountModels)) {
            taskAccountModels[k] = Array.isArray(v) ? [...v] : [];
        }
    } else if (task && Array.isArray(task.accountIds) && Array.isArray(task.modelNames)) {
        task.accountIds.forEach((accId: string) => {
            taskAccountModels[accId] = [...task.modelNames];
        });
    }

    accGrid.innerHTML = '';
    let currentAccs = (state.currentAccountsList || []).filter((acc: any) => acc.provider === state.currentViewTab);

    // 对于官方通道账号（provider = 'antigravity'）按邮箱 email 去重，防止同一邮箱多实例展示
    if (state.currentViewTab === 'antigravity') {
        const seenEmails = new Set<string>();
        currentAccs = currentAccs.filter((acc: any) => {
            if (seenEmails.has(acc.email)) return false;
            seenEmails.add(acc.email);
            return true;
        });
    }

    if (currentAccs.length === 0) {
        accGrid.innerHTML = `<div class="col-span-2 text-outline italic">${state.currentLanguage === 'zh' ? '当前通道无可用账号' : 'No available accounts in this channel'}</div>`;
    } else {
        currentAccs.forEach((acc: any) => {
            const isChecked = task && task.accountIds ? task.accountIds.includes(acc.id) : false;
            const displayName = acc.provider === 'project' && acc.projectId
                ? `${acc.email} (${state.currentLanguage === 'zh' ? '项目' : 'Project'}: ${acc.projectId})`
                : acc.email;

            const div = document.createElement('div');
            div.className = 'flex items-center gap-1.5 truncate';
            div.innerHTML = `
                <input type="checkbox" id="chk_acc_${acc.id}" value="${acc.id}" class="edit-acc-cb rounded border-outline-variant/40 text-primary focus:ring-primary cursor-pointer" ${isChecked ? 'checked' : ''}>
                <label for="chk_acc_${acc.id}" class="truncate cursor-pointer select-none" title="${displayName}">${displayName}</label>
            `;
            const cb = div.querySelector('.edit-acc-cb') as HTMLInputElement;
            cb.addEventListener('change', () => {
                handleAccountsSelectionChange();
            });
            accGrid.appendChild(div);
        });
    }

    currentEditingTask = task || null;

    // 确定初始 activeAccountId
    const checkedAccs = getCheckedAccountIDs();
    activeAccountId = checkedAccs.length > 0 ? checkedAccs[0] : null;

    renderAccountTabs();
    loadAndRenderAutoTriggerModels(task);
}

function getTargetChannelForTask(task?: any): string {
    if (task && Array.isArray(task.accountIds) && task.accountIds.length > 0) {
        const firstAcc = (state.currentAccountsList || []).find((a: any) => a.id === task.accountIds[0]);
        if (firstAcc && firstAcc.provider) {
            return firstAcc.provider;
        }
    }
    return state.currentViewTab || 'google';
}

function getCurrentSelectedModelNames(): string[] {
    const cbs = document.querySelectorAll('.edit-model-cb:checked') as NodeListOf<HTMLInputElement>;
    const names: string[] = [];
    cbs.forEach(cb => names.push(cb.value));
    return names;
}

function renderAutoTriggerModelGroups(models: string[], selectedSet: Set<string>) {
    renderCategorizedCheckboxes(models, selectedSet, {
        geminiContainer: editModelsGemini,
        claudeContainer: editModelsClaude,
        othersContainer: editModelsOthers,
        checkboxClass: 'edit-model-cb',
        idPrefix: 'chk_mod',
        emptyTexts: {
            gemini: state.currentLanguage === 'zh' ? '暂无 Gemini 模型' : 'No Gemini models',
            claude: state.currentLanguage === 'zh' ? '暂无 Claude 模型' : 'No Claude models',
            others: state.currentLanguage === 'zh' ? '暂无其它模型' : 'No other models',
        },
    });

    // 绑定模型复选框即时变更事件
    const cbs = document.querySelectorAll<HTMLInputElement>('.edit-model-cb');
    cbs.forEach(cb => {
        cb.addEventListener('change', () => {
            saveCurrentAccountSelection();
            updateActiveAccountCount();
            updateAccountTabBadge(activeAccountId);
        });
    });
}

async function loadAndRenderAutoTriggerModels(task?: any, forceRefresh: boolean = false) {
    if (!editModelsGemini || !editModelsClaude || !editModelsOthers) return;

    // 收集当前 active 账号需保留勾选的模型
    const activeModels = activeAccountId ? (taskAccountModels[activeAccountId] || []) : [];
    const preserved = (activeModels.length > 0)
        ? activeModels
        : (task && Array.isArray(task.modelNames) ? task.modelNames : []);
    const selectedSet = new Set<string>(preserved);

    const channel = getTargetChannelForTask(task);

    // 界面展示加载中状态
    if (iconEditFetchModels) iconEditFetchModels.classList.add('animate-spin');
    if (btnEditFetchModels) btnEditFetchModels.disabled = true;
    if (textEditFetchModels) textEditFetchModels.textContent = state.currentLanguage === 'zh' ? '获取中...' : 'Fetching...';

    if (editModelsStatusMsg) {
        editModelsStatusMsg.classList.remove('hidden');
        editModelsStatusMsg.className = 'text-[10.5px] text-primary mt-1.5 flex items-center gap-1 animate-pulse';
        editModelsStatusMsg.innerHTML = `<span class="material-symbols-outlined text-[13px] animate-spin">sync</span><span>${state.currentLanguage === 'zh' ? '正在连接上游获取最新模型...' : 'Fetching latest models...'}</span>`;
    }

    // 若有内存缓存且非强制刷新，先快速铺设，避免空界面
    const cached = getCachedModels(channel);
    if (cached && !forceRefresh) {
        const initialList = mergeModelsWithPreserved(cached, preserved);
        renderAutoTriggerModelGroups(initialList, selectedSet);
    }

    try {
        const res = await fetchChannelModels(channel);
        const merged = mergeModelsWithPreserved(res.models, preserved);
        renderAutoTriggerModelGroups(merged, selectedSet);

        if (editModelsStatusMsg) {
            if (res.success) {
                editModelsStatusMsg.className = 'text-[10.5px] text-emerald-600 dark:text-emerald-400 mt-1.5 flex items-center gap-1';
                editModelsStatusMsg.textContent = state.currentLanguage === 'zh'
                    ? `✅ 已获取 ${res.models.length} 个最新模型`
                    : `✅ Fetched ${res.models.length} models`;
            } else {
                editModelsStatusMsg.className = 'text-[10.5px] text-amber-500 dark:text-amber-400 mt-1.5 flex items-center gap-1';
                editModelsStatusMsg.textContent = state.currentLanguage === 'zh'
                    ? `⚠️ 获取上游模型失败 (${res.error})，已采用本地可用模型`
                    : `⚠️ Failed to fetch models (${res.error}), using fallback`;
            }
        }
    } finally {
        if (iconEditFetchModels) iconEditFetchModels.classList.remove('animate-spin');
        if (btnEditFetchModels) btnEditFetchModels.disabled = false;
        if (textEditFetchModels) textEditFetchModels.textContent = state.currentLanguage === 'zh' ? '获取最新模型' : 'Fetch Models';
        updateModelCheckboxesForActiveAccount();
    }
}

async function saveAutoTriggerTask() {
    if (!editTaskName || !editTaskPrompt || !editTaskTriggerType || !editTaskInterval) return;

    const name = editTaskName.value.trim();
    if (!name) {
        alert(state.currentLanguage === 'zh' ? '请输入任务包名称！' : 'Please enter a task package name!');
        return;
    }

    const prompt = editTaskPrompt.value.trim() || 'ok';
    const triggerType = editTaskTriggerType.value;
    const intervalMinutes = parseInt(editTaskInterval.value || '60', 10);
    const intervalSeconds = Math.max(intervalMinutes * 60, 300);

    saveCurrentAccountSelection();

    const selectedAccountIDs = getCheckedAccountIDs();
    if (selectedAccountIDs.length === 0) {
        alert(state.currentLanguage === 'zh' ? '请至少选择一个关联账号！' : 'Please select at least one associated account!');
        return;
    }

    const cleanAccountModels: Record<string, string[]> = {};
    const unionSet = new Set<string>();
    let hasAnyModel = false;

    selectedAccountIDs.forEach(accId => {
        const mList = taskAccountModels[accId] || [];
        cleanAccountModels[accId] = mList;
        if (mList.length > 0) {
            hasAnyModel = true;
            mList.forEach(m => unionSet.add(m));
        }
    });

    if (!hasAnyModel) {
        alert(state.currentLanguage === 'zh' ? '请至少为一个账号配置触发模型！' : 'Please select at least one trigger model for an account!');
        return;
    }

    const id = editTaskId?.value ? parseInt(editTaskId.value, 10) : 0;

    const payload = {
        id: id,
        name: name,
        accountIds: selectedAccountIDs,
        modelNames: Array.from(unionSet),
        accountModels: cleanAccountModels,
        prompt: prompt,
        triggerType: triggerType,
        intervalSeconds: intervalSeconds,
        enabled: true
    };

    try {
        const res = await ipcRenderer.invoke('autotrigger:save', payload);
        if (res && res.success) {
            switchAutoTriggerPanel('list');
            loadAutoTriggerTasks();
        } else {
            alert((state.currentLanguage === 'zh' ? '保存失败: ' : 'Save failed: ') + (res?.error || (state.currentLanguage === 'zh' ? '未知错误' : 'Unknown error')));
        }
    } catch (err: any) {
        alert((state.currentLanguage === 'zh' ? '保存引发异常: ' : 'Exception during saving: ') + err.message);
    }
}

function escapeHtml(str: string): string {
    return String(str || '')
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;');
}
