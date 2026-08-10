/**
 * 自动化定时与刷新任务包 Modal 控制逻辑：从 accountsController.ts 抽离的独立模块。
 *
 * 高内聚：本模块承担自动化触发任务包的列表渲染、新建/编辑表单填充、保存与状态切换；
 * 自带 27 个 DOM handle、3 个模型常量与 9 个函数，由 accountsController.initAccountsEvents
 * 委托调用 initAutoTriggerModalEvents()；switchAutoTriggerPanel 被 controller re-export，
 * 保持 autotriggerHistoryController.ts 既有的 import 面零改动。
 * 依赖：state（语言+账号列表）、ipcRenderer、全局 $confirm / alert；无跨簇 handle 引用。
 */
import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';

// 自动化任务包 Modal 变量定义
let btnManageAutoTrigger: HTMLButtonElement | null;
let autoTriggerModal: HTMLDivElement | null;
let autoTriggerModalContainer: HTMLDivElement | null;
let btnAutoTriggerModalClose: HTMLButtonElement | null;
let btnAutoTriggerModalCloseSecondary: HTMLButtonElement | null;
let panelTaskList: HTMLDivElement | null;
let panelTaskEdit: HTMLDivElement | null;
let footerTaskList: HTMLDivElement | null;
let footerTaskEdit: HTMLDivElement | null;
let btnCreateNewTask: HTMLButtonElement | null;
let autoTriggerTasksTableBody: HTMLTableSectionElement | null;

let editTaskId: HTMLInputElement | null;
let editTaskName: HTMLInputElement | null;
let editTaskPrompt: HTMLInputElement | null;
let editTaskTriggerType: HTMLSelectElement | null;
let editTaskInterval: HTMLInputElement | null;
let containerTaskInterval: HTMLDivElement | null;
let editAccountsGrid: HTMLDivElement | null;
let editModelsGemini: HTMLDivElement | null;
let editModelsClaude: HTMLDivElement | null;
let editModelsOthers: HTMLDivElement | null;
let btnEditSelectAllAccounts: HTMLButtonElement | null;
let btnEditClearAllAccounts: HTMLButtonElement | null;
let btnEditSelectAllModels: HTMLButtonElement | null;
let btnEditClearAllModels: HTMLButtonElement | null;
let btnCancelEditTask: HTMLButtonElement | null;
let btnSaveTask: HTMLButtonElement | null;

// ==================== 自动化定时与刷新任务 Modal 控制逻辑 ====================

const AUTO_MODELS_GEMINI = [
    "gemini-3.5-flash", "gemini-3.5-flash-low", "gemini-3.5-flash-extra-low",
    "gemini-3.1-flash-lite", "gemini-3.1-pro-low", "gemini-3.1-pro-preview",
    "gemini-3-flash", "gemini-3-flash-preview", "gemini-3-flash-agent",
    "gemini-pro-agent", "gemini-2.5-flash", "gemini-2.5-flash-lite"
];
const AUTO_MODELS_CLAUDE = [
    "claude-sonnet-4-6", "claude-opus-4-6-thinking"
];
const AUTO_MODELS_OTHERS = [
    "gpt-oss-120b-medium", "tab_flash_lite_preview", "tab_jump_flash_lite_preview"
];

function initAutoTriggerModalEventsImpl() {
    btnManageAutoTrigger = document.getElementById('btnManageAutoTrigger') as HTMLButtonElement | null;
    autoTriggerModal = document.getElementById('autoTriggerModal') as HTMLDivElement | null;
    autoTriggerModalContainer = document.getElementById('autoTriggerModalContainer') as HTMLDivElement | null;
    btnAutoTriggerModalClose = document.getElementById('btnAutoTriggerModalClose') as HTMLButtonElement | null;
    btnAutoTriggerModalCloseSecondary = document.getElementById('btnAutoTriggerModalCloseSecondary') as HTMLButtonElement | null;
    panelTaskList = document.getElementById('panelTaskList') as HTMLDivElement | null;
    panelTaskEdit = document.getElementById('panelTaskEdit') as HTMLDivElement | null;
    footerTaskList = document.getElementById('footerTaskList') as HTMLDivElement | null;
    footerTaskEdit = document.getElementById('footerTaskEdit') as HTMLDivElement | null;
    btnCreateNewTask = document.getElementById('btnCreateNewTask') as HTMLButtonElement | null;
    autoTriggerTasksTableBody = document.getElementById('autoTriggerTasksTableBody') as HTMLTableSectionElement | null;

    editTaskId = document.getElementById('editTaskId') as HTMLInputElement | null;
    editTaskName = document.getElementById('editTaskName') as HTMLInputElement | null;
    editTaskPrompt = document.getElementById('editTaskPrompt') as HTMLInputElement | null;
    editTaskTriggerType = document.getElementById('editTaskTriggerType') as HTMLSelectElement | null;
    editTaskInterval = document.getElementById('editTaskInterval') as HTMLInputElement | null;
    containerTaskInterval = document.getElementById('containerTaskInterval') as HTMLDivElement | null;
    editAccountsGrid = document.getElementById('editAccountsGrid') as HTMLDivElement | null;
    editModelsGemini = document.getElementById('editModelsGemini') as HTMLDivElement | null;
    editModelsClaude = document.getElementById('editModelsClaude') as HTMLDivElement | null;
    editModelsOthers = document.getElementById('editModelsOthers') as HTMLDivElement | null;
    btnEditSelectAllAccounts = document.getElementById('btnEditSelectAllAccounts') as HTMLButtonElement | null;
    btnEditClearAllAccounts = document.getElementById('btnEditClearAllAccounts') as HTMLButtonElement | null;
    btnEditSelectAllModels = document.getElementById('btnEditSelectAllModels') as HTMLButtonElement | null;
    btnEditClearAllModels = document.getElementById('btnEditClearAllModels') as HTMLButtonElement | null;
    btnCancelEditTask = document.getElementById('btnCancelEditTask') as HTMLButtonElement | null;
    btnSaveTask = document.getElementById('btnSaveTask') as HTMLButtonElement | null;

    if (btnManageAutoTrigger) {
        btnManageAutoTrigger.addEventListener('click', openAutoTriggerModal);
    }
    if (btnAutoTriggerModalClose) {
        btnAutoTriggerModalClose.addEventListener('click', closeAutoTriggerModal);
    }
    if (btnAutoTriggerModalCloseSecondary) {
        btnAutoTriggerModalCloseSecondary.addEventListener('click', closeAutoTriggerModal);
    }
    if (btnCreateNewTask) {
        btnCreateNewTask.addEventListener('click', () => {
            prepareTaskEditForm();
            switchAutoTriggerPanel('edit');
        });
    }
    if (btnCancelEditTask) {
        btnCancelEditTask.addEventListener('click', () => {
            switchAutoTriggerPanel('list');
        });
    }
    if (btnSaveTask) {
        btnSaveTask.addEventListener('click', saveAutoTriggerTask);
    }

    if (editTaskTriggerType) {
        editTaskTriggerType.addEventListener('change', () => {
            if (containerTaskInterval) {
                if (editTaskTriggerType?.value === 'timer') {
                    containerTaskInterval.classList.remove('hidden');
                } else {
                    containerTaskInterval.classList.add('hidden');
                }
            }
        });
    }

    if (btnEditSelectAllAccounts) {
        btnEditSelectAllAccounts.addEventListener('click', () => {
            const cbs = editAccountsGrid?.querySelectorAll('input[type="checkbox"]') as NodeListOf<HTMLInputElement>;
            cbs?.forEach(cb => cb.checked = true);
        });
    }
    if (btnEditClearAllAccounts) {
        btnEditClearAllAccounts.addEventListener('click', () => {
            const cbs = editAccountsGrid?.querySelectorAll('input[type="checkbox"]') as NodeListOf<HTMLInputElement>;
            cbs?.forEach(cb => cb.checked = false);
        });
    }

    if (btnEditSelectAllModels) {
        btnEditSelectAllModels.addEventListener('click', () => {
            const cbs = document.querySelectorAll('.edit-model-cb') as NodeListOf<HTMLInputElement>;
            cbs?.forEach(cb => cb.checked = true);
        });
    }
    if (btnEditClearAllModels) {
        btnEditClearAllModels.addEventListener('click', () => {
            const cbs = document.querySelectorAll('.edit-model-cb') as NodeListOf<HTMLInputElement>;
            cbs?.forEach(cb => cb.checked = false);
        });
    }
}

// 由 accountsController.initAccountsEvents 委托调用：句柄赋值 + 弹窗/任务包表单事件绑定。
export function initAutoTriggerModalEvents(): void {
    initAutoTriggerModalEventsImpl();
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

async function loadAutoTriggerTasks() {
    if (!autoTriggerTasksTableBody) return;
    autoTriggerTasksTableBody.innerHTML = `
        <tr>
            <td class="p-8 text-center text-outline dark:text-outline-variant italic" colspan="6">
                ${state.currentLanguage === 'zh' ? '⏳ 正在加载定时任务列表...' : '⏳ Loading task list...'}
            </td>
        </tr>
    `;

    try {
        const res = await ipcRenderer.invoke('autotrigger:list');
        if (res && res.success && res.tasks) {
            renderAutoTriggerTasksTable(res.tasks);
        } else {
            autoTriggerTasksTableBody.innerHTML = `
                <tr>
                    <td class="p-8 text-center text-red-400" colspan="6">
                        ❌ ${state.currentLanguage === 'zh' ? '加载失败: ' : 'Load failed: '}${res?.error || (state.currentLanguage === 'zh' ? '未知错误' : 'Unknown error')}
                    </td>
                </tr>
            `;
        }
    } catch (err: any) {
        autoTriggerTasksTableBody.innerHTML = `
            <tr>
                <td class="p-8 text-center text-red-400" colspan="6">
                    ❌ ${state.currentLanguage === 'zh' ? '加载发生异常: ' : 'Exception during loading: '}${err.message}
                </td>
            </tr>
        `;
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
        const modelCount = task.modelNames ? task.modelNames.length : 0;

        const isChecked = task.enabled ? 'checked' : '';

        tr.innerHTML = `
            <td class="p-3 font-bold text-on-surface dark:text-white truncate" title="${task.name}">${task.name}</td>
            <td class="p-3">${triggerTypeBadge}</td>
            <td class="p-3 font-mono text-[11px]">${state.currentLanguage === 'zh' ? `${accCount} 个账号` : `${accCount} accounts`}</td>
            <td class="p-3 font-mono text-[11px]">${state.currentLanguage === 'zh' ? `${modelCount} 个模型` : `${modelCount} models`}</td>
            <td class="p-3 text-center">
                <label class="switch">
                    <input type="checkbox" class="task-toggle-cb" data-task-id="${task.id}" ${isChecked}>
                    <span class="slider"></span>
                </label>
            </td>
            <td class="p-3 text-center">
                <div class="flex items-center justify-center gap-2">
                    <button class="btn-task-edit text-primary dark:text-primary-fixed-dim hover:underline font-bold" data-task-id="${task.id}">${state.currentLanguage === 'zh' ? '编辑' : 'Edit'}</button>
                    <span class="text-outline/30">|</span>
                    <button class="btn-task-delete text-red-400 hover:underline font-bold" data-task-id="${task.id}">${state.currentLanguage === 'zh' ? '删除' : 'Delete'}</button>
                </div>
            </td>
        `;

        tableBody.appendChild(tr);
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

function prepareTaskEditForm(task?: any) {
    const accGrid = editAccountsGrid;
    if (!accGrid || !editTaskId || !editTaskName || !editTaskPrompt || !editTaskTriggerType || !editTaskInterval || !containerTaskInterval || !editModelsGemini || !editModelsClaude || !editModelsOthers) return;

    if (task) {
        editTaskId.value = task.id.toString();
        editTaskName.value = task.name || '';
        editTaskPrompt.value = task.prompt || 'ok';
        editTaskTriggerType.value = task.triggerType || 'timer';
        editTaskInterval.value = Math.round((task.intervalSeconds || 3600) / 60).toString();
    } else {
        editTaskId.value = '';
        editTaskName.value = '';
        editTaskPrompt.value = 'ok';
        editTaskTriggerType.value = 'timer';
        editTaskInterval.value = '60';
    }

    if (editTaskTriggerType.value === 'timer') {
        containerTaskInterval.classList.remove('hidden');
    } else {
        containerTaskInterval.classList.add('hidden');
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
            // 新建时默认不勾选任何账号
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
            accGrid.appendChild(div);
        });
    }

    const renderModelGroup = (container: HTMLDivElement, modelsList: string[]) => {
        container.innerHTML = '';
        modelsList.forEach(m => {
            // 新建时模型默认不勾选
            const isChecked = task && task.modelNames ? task.modelNames.includes(m) : false;
            const div = document.createElement('div');
            div.className = 'flex items-center gap-1.5 text-[11px] truncate';
            div.innerHTML = `
                <input type="checkbox" id="chk_mod_${m.replace(/[^a-zA-Z0-9]/g, '_')}" value="${m}" class="edit-model-cb rounded border-outline-variant/40 text-primary focus:ring-primary cursor-pointer" ${isChecked ? 'checked' : ''}>
                <label for="chk_mod_${m.replace(/[^a-zA-Z0-9]/g, '_')}" class="truncate font-mono text-[10.5px] cursor-pointer select-none" title="${m}">${m}</label>
            `;
            container.appendChild(div);
        });
    };

    renderModelGroup(editModelsGemini, AUTO_MODELS_GEMINI);
    renderModelGroup(editModelsClaude, AUTO_MODELS_CLAUDE);
    renderModelGroup(editModelsOthers, AUTO_MODELS_OTHERS);
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

    const accCbs = editAccountsGrid?.querySelectorAll('.edit-acc-cb:checked') as NodeListOf<HTMLInputElement>;
    const selectedAccountIDs: string[] = [];
    accCbs?.forEach(cb => selectedAccountIDs.push(cb.value));

    if (selectedAccountIDs.length === 0) {
        alert(state.currentLanguage === 'zh' ? '请至少选择一个关联账号！' : 'Please select at least one associated account!');
        return;
    }

    const modelCbs = document.querySelectorAll('.edit-model-cb:checked') as NodeListOf<HTMLInputElement>;
    const selectedModelNames: string[] = [];
    modelCbs?.forEach(cb => selectedModelNames.push(cb.value));

    if (selectedModelNames.length === 0) {
        alert(state.currentLanguage === 'zh' ? '请至少选择一个触发测试模型！' : 'Please select at least one trigger test model!');
        return;
    }

    const id = editTaskId?.value ? parseInt(editTaskId.value, 10) : 0;

    const payload = {
        id: id,
        name: name,
        accountIds: selectedAccountIDs,
        modelNames: selectedModelNames,
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
