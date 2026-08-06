import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';
import i18n from '../shared/i18n';
import { showOneStopAuthModal } from './accountsAuthModal';
import {
    initRendererElements,
    renderAccounts,
    updateAggregateQuotaUI,
    loadAccountQuota,
    getNvidiaCooldownRemaining
} from './accountsRenderer';

// NVIDIA 冷却秒级翻牌倒计时定时器：单实例、自驱动、自停止。
// 由 renderAccounts 末尾的 ensureNvidiaCooldownTimer 启动；tick 每秒只更新
// .nvidia-cooldown-tick 节点的 textContent + className，零 IPC、零整列重渲染。
let nvidiaCooldownTimer: any = null;

export function ensureNvidiaCooldownTimer() {
    if (nvidiaCooldownTimer) return;
    nvidiaCooldownTimer = setInterval(tickNvidiaCooldown, 1000);
}

export function stopNvidiaCooldownTimer() {
    if (nvidiaCooldownTimer) {
        clearInterval(nvidiaCooldownTimer);
        nvidiaCooldownTimer = null;
    }
}

function tickNvidiaCooldown() {
    // 切走账号池视图则自停（renderAccounts 切回时会重新 ensure）。
    if (state.activeView !== 'accounts') {
        stopNvidiaCooldownTimer();
        return;
    }
    let anyActive = false;
    const ticks = document.querySelectorAll('.nvidia-cooldown-tick');
    ticks.forEach((el: any) => {
        const until = parseInt(el.getAttribute('data-until') || '0', 10);
        const { text, expired, seconds } = getNvidiaCooldownRemaining(until);
        el.textContent = text;
        const colorCls = expired
            ? 'text-slate-500'
            : seconds <= 10 ? 'text-red-500 animate-pulse'
            : seconds <= 60 ? 'text-amber-600'
            : 'text-amber-500/80';
        el.className = `nvidia-cooldown-tick ${colorCls}`;
        if (!expired) anyActive = true;
    });
    // 全部到期或无冷却账号 → 自停，下次 renderAccounts 再 ensure。
    if (!anyActive) stopNvidiaCooldownTimer();
}


let btnAddAccount: HTMLButtonElement | null;
let addAccountDropdown: HTMLDivElement | null;
let poolModeToggle: HTMLInputElement | null;
let accountsList: HTMLDivElement | null;
let btnRefreshAllQuota: HTMLButtonElement | null;
let btnRefreshAllIcon: HTMLElement | null;
let btnClearSessions: HTMLButtonElement | null;
let btnRefreshAggregateQuota: HTMLButtonElement | null;
let btnRefreshAggregateIcon: HTMLElement | null;
let poolModeContainer: HTMLDivElement | null;
let lblPoolMode: HTMLElement | null;
let btnChannelAntigravity: HTMLButtonElement | null;
let btnChannelProject: HTMLButtonElement | null;
let btnChannelGeminiCli: HTMLButtonElement | null;
let btnChannelNvidia: HTMLButtonElement | null;
let btnChannelOther: HTMLButtonElement | null;
let btnAddOtherAccount: HTMLButtonElement | null;
let otherGroupTabs: HTMLDivElement | null;
let otherLBModeContainer: HTMLDivElement | null;
let otherLBModeSelect: HTMLSelectElement | null;
let groupSelectOther: HTMLSelectElement | null;
let otherAccountModal: HTMLDivElement | null;
let otherAccountModalContainer: HTMLDivElement | null;
let btnOtherModalSave: HTMLButtonElement | null;
let btnOtherModalCancel: HTMLButtonElement | null;
let btnOtherModalClose: HTMLButtonElement | null;
let btnOtherFetchModels: HTMLButtonElement | null;
let otherModalError: HTMLDivElement | null;
let nvidiaPoolModeContainer: HTMLDivElement | null;
let nvidiaPoolModeToggle: HTMLInputElement | null;
let nvidiaLBModeContainer: HTMLDivElement | null;
let nvidiaLBModeSelect: HTMLSelectElement | null;
let btnAddNvidiaAccount: HTMLButtonElement | null;
let nvidiaAccountModal: HTMLDivElement | null;
let nvidiaAccountModalContainer: HTMLDivElement | null;
let btnNvidiaModalSave: HTMLButtonElement | null;
let btnNvidiaModalCancel: HTMLButtonElement | null;
let btnNvidiaModalClose: HTMLButtonElement | null;
let nvidiaModalError: HTMLDivElement | null;
let btnNvidiaFetchModels: HTMLButtonElement | null;
// ===== NVIDIA 全局专属模型清单 Modal (双列穿梭框) =====
let btnNvidiaPreferredModels: HTMLButtonElement | null;
let badgeNvidiaPreferredCount: HTMLSpanElement | null;
let nvidiaPreferredModal: HTMLDivElement | null;
let nvidiaPreferredModalContainer: HTMLDivElement | null;
let btnNvidiaPreferredModalCancel: HTMLButtonElement | null;
let btnNvidiaPreferredModalClose: HTMLButtonElement | null;
let btnNvidiaPreferredSave: HTMLButtonElement | null;
let btnNvidiaPreferredSrcLocal: HTMLButtonElement | null;
let btnNvidiaPreferredSrcRemote: HTMLButtonElement | null;
let nvidiaPreferredCurrentSource: 'local' | 'remote' = 'local';
let btnNvidiaPreferredMoveLeft: HTMLButtonElement | null;
let btnNvidiaPreferredMoveRight: HTMLButtonElement | null;
let btnNvidiaPreferredMoveAllLeft: HTMLButtonElement | null;
let btnNvidiaPreferredMoveAllRight: HTMLButtonElement | null;
let btnNvidiaPreferredFetch: HTMLButtonElement | null;
let iconNvidiaPreferredFetch: HTMLSpanElement | null;
let inputNvidiaPreferredSearchLeft: HTMLInputElement | null;
let inputNvidiaPreferredSearchRight: HTMLInputElement | null;
let chkNvidiaPreferredSelectAllLeft: HTMLInputElement | null;
let chkNvidiaPreferredSelectAllRight: HTMLInputElement | null;
let nvidiaPreferredModelsListLeft: HTMLDivElement | null;
let nvidiaPreferredModelsListRight: HTMLDivElement | null;
let nvidiaPreferredEmptyLeft: HTMLDivElement | null;
let nvidiaPreferredEmptyRight: HTMLDivElement | null;
let lblNvidiaPreferredCount: HTMLSpanElement | null;
let lblNvidiaPreferredSource: HTMLDivElement | null;
let lblNvidiaPreferredVisibleLeft: HTMLSpanElement | null;
let lblNvidiaPreferredVisibleRight: HTMLSpanElement | null;
let nvidiaPreferredError: HTMLDivElement | null;
let nvidiaPreferredSourceRespEventBound = false;
// 穿梭框内存态:跨 Modal 打开/远端刷新保留。左列=已选(给客户端的清单),右列=上游候选全集。
let nvidiaPreferredLeftIDs: string[] = [];
let nvidiaPreferredRightIDs: string[] = [];
let btnExportAccounts: HTMLButtonElement | null;
let btnImportAccounts: HTMLButtonElement | null;
let btnLayoutGrid: HTMLButtonElement | null;
let btnLayoutList: HTMLButtonElement | null;

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

let isGlobalEventsInitialized = false;

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

let btnShowSessionBindings: HTMLButtonElement | null;
let sessionBindingsModal: HTMLDivElement | null;
let sessionBindingsModalCloseBtn: HTMLButtonElement | null;
let sessionBindingsModalCloseBtnSecondary: HTMLButtonElement | null;
let sessionBindingsTableBody: HTMLTableSectionElement | null;
let sessionBindingsModalClearAllBtn: HTMLButtonElement | null;
let sessionBindingsCount: HTMLSpanElement | null;

export function updatePoolModeUI() {
    if (!poolModeToggle) return;
    const isPool = poolModeToggle.checked;
    const label = poolModeToggle.nextElementSibling;
    if (!label) return;
    
    if (isPool) {
        poolModeToggle.className = 'toggle-checkbox absolute block w-5 h-5 rounded-full bg-white border-4 border-primary appearance-none cursor-pointer translate-x-5 transition-transform duration-200 ease-in-out';
        label.className = 'toggle-label block overflow-hidden h-5 rounded-full bg-primary cursor-pointer';
    } else {
        poolModeToggle.className = 'toggle-checkbox absolute block w-5 h-5 rounded-full bg-white border-4 border-outline-variant appearance-none cursor-pointer translate-x-0 transition-transform duration-200 ease-in-out';
        label.className = 'toggle-label block overflow-hidden h-5 rounded-full bg-outline-variant/50 dark:bg-white/10 cursor-pointer';
    }
}

export function updateLayoutUI() {
    const gridBtn = btnLayoutGrid || (document.getElementById('btnLayoutGrid') as HTMLButtonElement | null);
    const listBtn = btnLayoutList || (document.getElementById('btnLayoutList') as HTMLButtonElement | null);
    const selectGridColumns = document.getElementById('selectGridColumns') as HTMLSelectElement | null;
    const accountsListEl = document.getElementById('accountsList');
    
    const activeClass = 'p-1 rounded-md cursor-pointer transition-all duration-200 bg-white dark:bg-[#1a1f30] text-primary dark:text-primary-fixed-dim shadow-sm flex items-center justify-center';
    const inactiveClass = 'p-1 rounded-md cursor-pointer transition-all duration-200 text-slate-400 hover:text-slate-700 dark:hover:text-slate-200 flex items-center justify-center';
    
    if (state.accountLayout === 'grid') {
        if (gridBtn) gridBtn.className = activeClass;
        if (listBtn) listBtn.className = inactiveClass;
        if (selectGridColumns) {
            selectGridColumns.classList.remove('hidden');
            selectGridColumns.value = String(state.accountGridColumns);
        }
        if (accountsListEl) {
            accountsListEl.classList.remove('layout-list');
            accountsListEl.classList.add('layout-grid');
            accountsListEl.classList.remove('cols-3', 'cols-4', 'cols-5');
            accountsListEl.classList.add(`cols-${state.accountGridColumns}`);
        }
    } else {
        if (gridBtn) gridBtn.className = inactiveClass;
        if (listBtn) listBtn.className = activeClass;
        if (selectGridColumns) {
            selectGridColumns.classList.add('hidden');
        }
        if (accountsListEl) {
            accountsListEl.classList.remove('layout-grid', 'cols-3', 'cols-4', 'cols-5');
            accountsListEl.classList.add('layout-list');
        }
    }
}

export function updateViewTabUI() {
    if (btnChannelAntigravity && btnChannelProject) {
        const activeClass = 'px-4 py-1.5 rounded-md font-bold cursor-pointer transition-all duration-200 bg-white dark:bg-[#1a1f30] text-primary dark:text-primary-fixed-dim shadow-sm';
        const inactiveClass = 'px-4 py-1.5 rounded-md font-medium cursor-pointer transition-all duration-200 text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200';
        const dict = i18n[state.currentLanguage] || i18n.zh;

        if (state.currentViewTab === 'antigravity') {
            btnChannelAntigravity.className = activeClass;
            btnChannelProject.className = inactiveClass;
            if (btnChannelGeminiCli) btnChannelGeminiCli.className = inactiveClass;
            if (btnChannelNvidia) btnChannelNvidia.className = inactiveClass;
            if (btnChannelOther) btnChannelOther.className = inactiveClass;

            if (poolModeContainer) poolModeContainer.classList.remove('hidden');
            if (nvidiaPoolModeContainer) nvidiaPoolModeContainer.classList.add('hidden');
            if (lblPoolMode) lblPoolMode.innerText = dict.poolLoadBalance || '账号负载均衡';
            if (poolModeToggle && state.lastBackendData) {
                poolModeToggle.checked = state.lastBackendData.poolMode;
            }
        /* } else if (state.currentViewTab === 'gemini-cli') {
            if (btnChannelGeminiCli) btnChannelGeminiCli.className = activeClass;
            btnChannelAntigravity.className = inactiveClass;
            btnChannelProject.className = inactiveClass;

            if (poolModeContainer) poolModeContainer.classList.remove('hidden');
            if (lblPoolMode) lblPoolMode.innerText = 'CLI号池负载均衡';
            if (poolModeToggle && state.lastBackendData) {
                poolModeToggle.checked = state.lastBackendData.geminiCliPoolMode;
            } */
        } else if (state.currentViewTab === 'nvidia') {
            if (btnChannelNvidia) btnChannelNvidia.className = activeClass;
            btnChannelAntigravity.className = inactiveClass;
            btnChannelProject.className = inactiveClass;
            if (btnChannelGeminiCli) btnChannelGeminiCli.className = inactiveClass;
            if (btnChannelOther) btnChannelOther.className = inactiveClass;

            // NVIDIA 用独立算法选择框
            if (poolModeContainer) poolModeContainer.classList.add('hidden');
            if (nvidiaLBModeContainer) nvidiaLBModeContainer.classList.remove('hidden');
            if (btnNvidiaPreferredModels) btnNvidiaPreferredModels.classList.remove('hidden');
            if (nvidiaLBModeSelect && state.lastBackendData) {
                nvidiaLBModeSelect.value = state.lastBackendData.nvidiaLBMode || 'round-robin';
            }
        } else if (state.currentViewTab === 'other') {
            if (btnChannelOther) btnChannelOther.className = activeClass;
            btnChannelAntigravity.className = inactiveClass;
            btnChannelProject.className = inactiveClass;
            if (btnChannelGeminiCli) btnChannelGeminiCli.className = inactiveClass;
            if (btnChannelNvidia) btnChannelNvidia.className = inactiveClass;

            // Other 号池:暂无独立负载均衡控件(组内轮换由后端 LBMode 控制),隐藏两个 toggle 容器。
            if (poolModeContainer) poolModeContainer.classList.add('hidden');
            if (nvidiaLBModeContainer) nvidiaLBModeContainer.classList.add('hidden');
            if (btnNvidiaPreferredModels) btnNvidiaPreferredModels.classList.add('hidden');
        } else {
            btnChannelProject.className = activeClass;
            btnChannelAntigravity.className = inactiveClass;
            if (btnChannelGeminiCli) btnChannelGeminiCli.className = inactiveClass;
            if (btnChannelNvidia) btnChannelNvidia.className = inactiveClass;
            if (btnChannelOther) btnChannelOther.className = inactiveClass;

            if (poolModeContainer) poolModeContainer.classList.remove('hidden');
            if (nvidiaLBModeContainer) nvidiaLBModeContainer.classList.add('hidden');
            if (btnNvidiaPreferredModels) btnNvidiaPreferredModels.classList.add('hidden');
            if (lblPoolMode) lblPoolMode.innerText = dict.projectLoadBalancing || '项目负载均衡';
            if (poolModeToggle && state.lastBackendData) {
                poolModeToggle.checked = state.lastBackendData.projectPoolMode;
            }
        }
        updatePoolModeUI();
    }

    const btnAntigravityLogin = document.getElementById('btnAntigravityLogin');
    const btnGeminiCliLogin = document.getElementById('btnGeminiCliLogin');
    const btnProjectLogin = document.getElementById('btnProjectLogin');

    if (state.currentViewTab === 'antigravity') {
        if (btnAntigravityLogin) btnAntigravityLogin.classList.remove('hidden');
        if (btnGeminiCliLogin) btnGeminiCliLogin.classList.add('hidden');
        if (btnProjectLogin) btnProjectLogin.classList.add('hidden');
        if (btnAddNvidiaAccount) btnAddNvidiaAccount.classList.add('hidden');
        if (btnAddOtherAccount) btnAddOtherAccount.classList.add('hidden');
        if (btnNvidiaPreferredModels) btnNvidiaPreferredModels.classList.add('hidden');
    /* } else if (state.currentViewTab === 'gemini-cli') {
        if (btnAntigravityLogin) btnAntigravityLogin.classList.add('hidden');
        if (btnGeminiCliLogin) btnGeminiCliLogin.classList.remove('hidden');
        if (btnProjectLogin) btnProjectLogin.classList.add('hidden'); */
    } else if (state.currentViewTab === 'nvidia') {
        if (btnAntigravityLogin) btnAntigravityLogin.classList.add('hidden');
        if (btnGeminiCliLogin) btnGeminiCliLogin.classList.add('hidden');
        if (btnProjectLogin) btnProjectLogin.classList.add('hidden');
        if (btnAddNvidiaAccount) btnAddNvidiaAccount.classList.remove('hidden');
        if (btnAddOtherAccount) btnAddOtherAccount.classList.add('hidden');
    } else if (state.currentViewTab === 'other') {
        if (btnAntigravityLogin) btnAntigravityLogin.classList.add('hidden');
        if (btnGeminiCliLogin) btnGeminiCliLogin.classList.add('hidden');
        if (btnProjectLogin) btnProjectLogin.classList.add('hidden');
        if (btnAddNvidiaAccount) btnAddNvidiaAccount.classList.add('hidden');
        if (btnAddOtherAccount) btnAddOtherAccount.classList.remove('hidden');
        if (btnNvidiaPreferredModels) btnNvidiaPreferredModels.classList.add('hidden');
    } else {
        if (btnAntigravityLogin) btnAntigravityLogin.classList.add('hidden');
        if (btnGeminiCliLogin) btnGeminiCliLogin.classList.add('hidden');
        if (btnProjectLogin) btnProjectLogin.classList.remove('hidden');
        if (btnAddNvidiaAccount) btnAddNvidiaAccount.classList.add('hidden');
        if (btnAddOtherAccount) btnAddOtherAccount.classList.add('hidden');
        if (btnNvidiaPreferredModels) btnNvidiaPreferredModels.classList.add('hidden');
    }
}

// ==================== Other 号池二级组名子 Tab + Modal 组自动填充 ====================
// renderOtherGroupTabs:按 state.lastBackendData.otherGroups 渲染 Other 通道下方的组名子 Tab。
// 0 组时整行隐藏。点击组 Tab 设 state.otherGroupFilter 并触发 renderAccounts 重新过滤。
export function renderOtherGroupTabs() {
    if (!otherGroupTabs) {
        otherGroupTabs = document.getElementById('otherGroupTabs') as HTMLDivElement | null;
    }
    if (!otherGroupTabs) return;

    const groups = (state.lastBackendData && Array.isArray(state.lastBackendData.otherGroups))
        ? state.lastBackendData.otherGroups
        : [];
    // 仅 other 通道显示;0 组时整行隐藏。
    if (state.currentViewTab !== 'other' || groups.length === 0) {
        otherGroupTabs.classList.add('hidden');
        otherGroupTabs.classList.remove('flex');
        otherGroupTabs.innerHTML = '';
        // 切走 other 通道或 0 组时回到「全部组」默认过滤,避免残留上一个组过滤。
        state.otherGroupFilter = 'ALL';
        return;
    }

    // 渲染前校验当前过滤组仍存在:若所选组被删除(删光该组最后一个账号),回落「全部组」,
    // 避免列表空态且无高亮 Tab 提示当前过滤目标。
    const validIds = new Set<string>();
    for (const g of groups) {
        const gid = String(g.groupId || g.groupID || g.id || '');
        if (gid) validIds.add(gid);
    }
    if (state.otherGroupFilter !== 'ALL' && !validIds.has(state.otherGroupFilter)) {
        state.otherGroupFilter = 'ALL';
    }

    const dict = i18n[state.currentLanguage] || i18n.zh;
    const activeClass = 'px-3 py-1 rounded-md font-bold cursor-pointer transition-all duration-200 bg-white dark:bg-[#1a1f30] text-primary dark:text-primary-fixed-dim shadow-sm whitespace-nowrap';
    const inactiveClass = 'px-3 py-1 rounded-md font-medium cursor-pointer transition-all duration-200 text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200 whitespace-nowrap';

    // 总账号数 = 各组合计 accountCount(与列表默认口径一致,含停用)。
    let totalCount = 0;
    for (const g of groups) totalCount += Number(g.accountCount) || 0;

    otherGroupTabs.innerHTML = '';
    otherGroupTabs.className = 'flex flex-wrap items-center gap-1 bg-slate-100 dark:bg-white/5 p-1 rounded-lg text-[12px]';

    const mkBtn = (label: string, filter: string, title: string) => {
        const b = document.createElement('button');
        b.type = 'button';
        b.className = state.otherGroupFilter === filter ? activeClass : inactiveClass;
        b.title = title;
        b.textContent = label;
        b.addEventListener('click', () => {
            state.otherGroupFilter = filter;
            renderOtherGroupTabs();
            if (state.currentAccountsList) renderAccounts(state.currentAccountsList);
        });
        return b;
    };

    // 「全部组」首项:filter='ALL'。
    otherGroupTabs.appendChild(mkBtn(
        `${dict.otherAllGroups || '全部组'} (${totalCount})`,
        'ALL',
        dict.otherAllGroups || '全部组'
    ));

    for (const g of groups) {
        const gid = String(g.groupId || g.groupID || g.id || '');
        if (!gid) continue;
        const gname = String(g.groupName || g.groupId || '');
        const count = Number(g.accountCount) || 0;
        const fmtTag = Array.isArray(g.formats) && g.formats.length
            ? g.formats.join(', ')
            : '';
        otherGroupTabs.appendChild(mkBtn(
            `${gname} (${count})`,
            gid,
            `${gid}${fmtTag ? ' · ' + fmtTag : ''}`
        ));
    }

    // 组内 LB 模式下拉已移至工具栏(otherLBModeContainer),见 renderOtherLBMode。
    renderOtherLBMode();
}

// renderOtherLBMode:在工具栏(otherLBModeContainer)渲染当前 Other 组过滤对应的负载均衡方式下拉。
// 仅 Other 通道、存在组、且选中了具体组时显示;「全部组」无单一 LB 模式可配置,隐藏。
function renderOtherLBMode() {
    if (!otherLBModeContainer) {
        otherLBModeContainer = document.getElementById('otherLBModeContainer') as HTMLDivElement | null;
    }
    if (!otherLBModeSelect) {
        otherLBModeSelect = document.getElementById('otherLBModeSelect') as HTMLSelectElement | null;
    }
    if (!otherLBModeContainer || !otherLBModeSelect) return;

    const groups = (state.lastBackendData && Array.isArray(state.lastBackendData.otherGroups))
        ? state.lastBackendData.otherGroups
        : [];
    // 仅 Other 通道、存在组、且选中了具体组时显示;「全部组」无单一 LB 模式可配置,隐藏。
    if (state.currentViewTab !== 'other' || groups.length === 0 || !state.otherGroupFilter || state.otherGroupFilter === 'ALL') {
        otherLBModeContainer.classList.add('hidden');
        otherLBModeContainer.classList.remove('flex');
        return;
    }

    // 当前选中组的 LB 模式(未设置时回退 round-robin)。
    let curMode = 'round-robin';
    const g = groups.find(x => String(x.groupId || x.groupID || x.id || '') === state.otherGroupFilter);
    if (g) curMode = String(g.lbMode || g.lb_mode || 'round-robin');

    otherLBModeSelect.value = curMode;
    otherLBModeContainer.classList.remove('hidden');
    otherLBModeContainer.classList.add('flex');
}

// otherLBModeSelectorVisible:工具栏 LB 模式下拉是否可见(选中具体 Other 组时)。
function otherLBModeSelectorVisible(): boolean {
    return state.currentViewTab === 'other'
        && !!state.otherGroupFilter
        && state.otherGroupFilter !== 'ALL';
}

// populateOtherGroupSelect:打开 Other 账号 Modal 时,若号池已有组则填充右侧「选择已有组」下拉。
// 0 组时隐藏下拉。每次打开不预选任何组(避免误覆盖用户手动输入)。
function populateOtherGroupSelect() {
    if (!groupSelectOther) {
        groupSelectOther = document.getElementById('selectOtherGroup') as HTMLSelectElement | null;
    }
    if (!groupSelectOther) return;
    const groups = (state.lastBackendData && Array.isArray(state.lastBackendData.otherGroups))
        ? state.lastBackendData.otherGroups
        : [];
    if (groups.length === 0) {
        groupSelectOther.classList.add('hidden');
        groupSelectOther.innerHTML = '<option value="" data-i18n="otherGroupSelectPlaceholder">选择已有组...</option>';
        groupSelectOther.value = '';
        return;
    }
    groupSelectOther.innerHTML = '<option value="" data-i18n="otherGroupSelectPlaceholder">选择已有组...</option>';
    for (const g of groups) {
        const gid = String(g.groupId || g.groupID || g.id || '');
        if (!gid) continue;
        const gname = String(g.groupName || g.groupId || '');
        const count = Number(g.accountCount) || 0;
        const opt = document.createElement('option');
        opt.value = gid;
        opt.textContent = `${gname} (${count})`;
        groupSelectOther.appendChild(opt);
    }
    groupSelectOther.value = '';
    groupSelectOther.classList.remove('hidden');
}

// onOtherGroupSelectChange:选中已有组时自动填充 groupId/groupName/baseUrl/formats/默认模型,
// 仅留 API Key 与展示名给用户填写。切回占位项("")不清空(避免误触抹掉输入)。
function onOtherGroupSelectChange() {
    if (!groupSelectOther || !otherModalError) return;
    const gid = groupSelectOther.value;
    otherModalError.classList.add('hidden');
    otherModalError.textContent = '';
    if (!gid) return;

    const groups = (state.lastBackendData && Array.isArray(state.lastBackendData.otherGroups))
        ? state.lastBackendData.otherGroups
        : [];
    const g = groups.find((x: any) => String(x.groupId || x.groupID || x.id || '') === gid);
    if (!g) return;

    const inputGroupId = document.getElementById('inputOtherGroupId') as HTMLInputElement | null;
    const inputGroupName = document.getElementById('inputOtherGroupName') as HTMLInputElement | null;
    const inputBaseUrl = document.getElementById('inputOtherBaseUrl') as HTMLInputElement | null;
    const inputModelDefault = document.getElementById('inputOtherModelDefault') as HTMLInputElement | null;
    const chkFmtOpenai = document.getElementById('chkOtherFmtOpenai') as HTMLInputElement | null;
    const chkFmtAnthropic = document.getElementById('chkOtherFmtAnthropic') as HTMLInputElement | null;

    if (inputGroupId) inputGroupId.value = gid;
    if (inputGroupName) inputGroupName.value = String(g.groupName || g.groupId || '');
    if (inputBaseUrl) inputBaseUrl.value = String(g.baseUrl || '');

    // 据组 Formats 勾选协议 checkbox(openai 含则勾,否则取消;anthropic 同理)。
    const fmts: string[] = Array.isArray(g.formats) ? g.formats.map((f: any) => String(f).toLowerCase()) : [];
    if (chkFmtOpenai) chkFmtOpenai.checked = fmts.includes('openai');
    if (chkFmtAnthropic) chkFmtAnthropic.checked = fmts.includes('anthropic');

    // 默认模型:从该组首个带 defaultModel 的账号取;取不到留空(不强制)。
    let defModel = '';
    if (state.currentAccountsList) {
        const hit = state.currentAccountsList.find(a =>
            a && a.groupId === gid && a.defaultModel
        );
        if (hit) defModel = String(hit.defaultModel);
    }
    if (inputModelDefault) inputModelDefault.value = defModel;
    // 不填 API Key / 展示名(用户自行填写);groupId 保持可编辑(不设 readonly)。
}

export async function refreshAllQuotas() {
    if (state.isRefreshingAll) return;
    state.isRefreshingAll = true;

    if (btnRefreshAllIcon && btnRefreshAllQuota) {
        btnRefreshAllIcon.classList.add('animate-spin');
        btnRefreshAllQuota.disabled = true;
        btnRefreshAllQuota.classList.add('opacity-60', 'cursor-not-allowed');
    }

    try {
        // 收集容器内所有账号卡片的刷新按钮，并按当前页签(currentViewTab)二次过滤，
        // 即便 DOM 因极端时序残留其它 tab 卡片，也只刷新当前 tab 账号，绝不跨 tab。
        const cardRefreshBtns = accountsList ? accountsList.querySelectorAll('[data-quota-refresh-btn]') : [];
        const activeTab = state.currentViewTab;
        // 用当前账号列表建立 id -> provider 映射，用于二次判过滤
        const accList = state.currentAccountsList || [];
        const idToProvider: Record<string, string> = {};
        for (const a of accList) {
            if (a && a.id) idToProvider[a.id] = a.provider;
        }

        const pendingBtns: HTMLButtonElement[] = [];
        for (let i = 0; i < cardRefreshBtns.length; i++) {
            const btn = cardRefreshBtns[i] as HTMLButtonElement;
            const card = btn.closest('[data-account-id]') as HTMLElement | null;
            const accId = card ? card.getAttribute('data-account-id') : null;
            // 严格白名单：仅刷新“明确属于当前 tab”的账号卡片。
            // 未在 idToProvider 命中的卡片视为跨 tab 残留(如异步 re-render 竞态下遗留的
            // 上一个 tab 卡片)，一律跳过，避免在当前 tab 误刷到其它 provider 的账号。
            if (!accId || !idToProvider[accId] || idToProvider[accId] !== activeTab) {
                continue;
            }
            pendingBtns.push(btn);
        }

        // 无卡片可刷新时直接结束，不再回退到 accounts:list（后端无该 handler，
        // 调用会静默失败且无意义），避免历史死代码引发歧义。
        for (let i = 0; i < pendingBtns.length; i++) {
            pendingBtns[i].click();
            await new Promise(r => setTimeout(r, 200));
        }
    } finally {
        await new Promise(r => setTimeout(r, 800));
        if (btnRefreshAllIcon && btnRefreshAllQuota) {
            btnRefreshAllIcon.classList.remove('animate-spin');
            btnRefreshAllQuota.disabled = false;
            btnRefreshAllQuota.classList.remove('opacity-60', 'cursor-not-allowed');
        }
        state.isRefreshingAll = false;
    }
}

export async function refreshAllAccountsQuotas() {
    if (state.isRefreshingAggregate) return;

    // 实时获取/兜底 DOM 元素，防止静态缓存失效
    const icon = btnRefreshAggregateIcon || document.getElementById('btnRefreshAggregateIcon');
    const btn = btnRefreshAggregateQuota || (document.getElementById('btnRefreshAggregateQuota') as HTMLButtonElement | null);

    if (state.isRemoteMode) {
        state.isRefreshingAggregate = true;
        if (btn && icon) {
            icon.classList.add('animate-spin');
            btn.disabled = true;
            btn.classList.add('opacity-60', 'cursor-not-allowed');
        }

        const startTime = Date.now();
        try {
            const stats = await ipcRenderer.invoke('remote:sync-stats');
            if (stats) {
                state.remoteStats = stats;
                updateAggregateQuotaUI();
                document.dispatchEvent(new CustomEvent('remote-stats-updated', { detail: stats }));
            }
        } catch (err) {
            console.error('[AccountsController] Failed to sync remote stats on click:', err);
        } finally {
            // 保障至少 800 毫秒的旋转时间，提供良好的刷新视觉反馈
            const elapsed = Date.now() - startTime;
            if (elapsed < 800) {
                await new Promise(r => setTimeout(r, 800 - elapsed));
            }

            state.isRefreshingAggregate = false;
            if (btn && icon) {
                icon.classList.remove('animate-spin');
                btn.disabled = false;
                btn.classList.remove('opacity-60', 'cursor-not-allowed');
            }
        }
        return;
    }

    if (!state.currentAccountsList || state.currentAccountsList.length === 0) return;
    state.isRefreshingAggregate = true;

    if (btn && icon) {
        icon.classList.add('animate-spin');
        btn.disabled = true;
        btn.classList.add('opacity-60', 'cursor-not-allowed');
    }

    try {
        for (const acc of state.currentAccountsList) {
            // 自动过滤并跳过已停用的灰色账号及 NVIDIA 账号
            if (!acc.enabled || acc.provider === 'nvidia') {
                continue;
            }
            try {
                const result = await ipcRenderer.invoke('quota:fetch', acc.id);
                if (result && !result.error) {
                    state.quotaCache[acc.id] = result.buckets;
                    updateAggregateQuotaUI(); // 每刷新一个账号，就重新计算并更新一次总额度条
                }
            } catch (err) {
                console.error(`Failed to refresh quota for ${acc.email}:`, err);
            }
            await new Promise(r => setTimeout(r, 100));
        }
        if (accountsList && accountsList.children.length > 0) {
            renderAccounts(state.currentAccountsList);
        }
    } finally {
        state.isRefreshingAggregate = false;
        if (btn && icon) {
            icon.classList.remove('animate-spin');
            btn.disabled = false;
            btn.classList.remove('opacity-60', 'cursor-not-allowed');
        }
    }
}

// Start Google account pool login
export async function startLogin(provider: any) {
    if (state.isLoadingAuth) return;
    state.isLoadingAuth = true;
    if (addAccountDropdown) addAccountDropdown.classList.add('hidden');

    if (btnAddAccount) {
        const origText = btnAddAccount.innerHTML;
        const dict = i18n[state.currentLanguage] || i18n.zh;
        btnAddAccount.innerHTML = `<span class="material-symbols-outlined text-[16px] animate-spin">refresh</span> ${dict.btnLoggingIn || (state.currentLanguage === 'zh' ? '登录中...' : 'Logging in...')}`;
        btnAddAccount.classList.add('opacity-70');

        const handleMouseEnter = () => {
            if (state.isLoadingAuth && btnAddAccount) {
                btnAddAccount.innerHTML = `<span class="material-symbols-outlined text-[16px]">cancel</span> ${dict.btnCancelLogin || (state.currentLanguage === 'zh' ? '取消登录' : 'Cancel Login')}`;
                btnAddAccount.classList.remove('bg-primary', 'hover:bg-primary/90');
                btnAddAccount.classList.add('bg-red-500', 'hover:bg-red-600');
            }
        };

        const handleMouseLeave = () => {
            if (state.isLoadingAuth && btnAddAccount) {
                btnAddAccount.innerHTML = `<span class="material-symbols-outlined text-[16px] animate-spin">refresh</span> ${dict.btnLoggingIn || (state.currentLanguage === 'zh' ? '登录中...' : 'Logging in...')}`;
                btnAddAccount.classList.remove('bg-red-500', 'hover:bg-red-600');
                btnAddAccount.classList.add('bg-primary', 'hover:bg-primary/90');
            }
        };

        btnAddAccount.addEventListener('mouseenter', handleMouseEnter);
        btnAddAccount.addEventListener('mouseleave', handleMouseLeave);

        try {
            const authRequest = typeof provider === 'object' && provider !== null
                ? provider
                : { provider };
            const res = await ipcRenderer.invoke('auth:login', authRequest);
            if (!res.success) {
                alert('登录失败或已取消: ' + res.error);
            }
        } catch (err: any) {
            alert('登录出错: ' + err.message);
        } finally {
            state.isLoadingAuth = false;
            if (btnAddAccount) {
                btnAddAccount.removeEventListener('mouseenter', handleMouseEnter);
                btnAddAccount.removeEventListener('mouseleave', handleMouseLeave);
                btnAddAccount.innerHTML = origText;
                btnAddAccount.classList.remove('opacity-70', 'bg-red-500', 'hover:bg-red-600');
                btnAddAccount.classList.add('bg-primary', 'hover:bg-primary/90');
            }
        }
    }
}

// Start Project binding login
export async function startProjectLogin() {
    if (addAccountDropdown) addAccountDropdown.classList.add('hidden');
    if (state.isLoadingAuth) return;
    state.isLoadingAuth = true;
    
    try {
        const result = await showOneStopAuthModal();
        if (result && result.success) {
            // GCP login successful
        }
    } catch (err: any) {
        alert('登录发生错误: ' + err.message);
    } finally {
        state.isLoadingAuth = false;
    }
}

// Initialize account pool controls and bindings
export function initAccountsEvents() {
    initRendererElements();

    btnAddAccount = document.getElementById('btnAddAccount') as HTMLButtonElement | null;
    addAccountDropdown = document.getElementById('addAccountDropdown') as HTMLDivElement | null;
    poolModeToggle = document.getElementById('poolModeToggle') as HTMLInputElement | null;
    accountsList = document.getElementById('accountsList') as HTMLDivElement | null;
    btnRefreshAllQuota = document.getElementById('btnRefreshAllQuota') as HTMLButtonElement | null;
    btnRefreshAllIcon = document.getElementById('btnRefreshAllIcon');
    btnClearSessions = document.getElementById('btnClearSessions') as HTMLButtonElement | null;
    poolModeContainer = document.getElementById('poolModeContainer') as HTMLDivElement | null;
    lblPoolMode = document.getElementById('lblPoolMode');
    btnChannelAntigravity = document.getElementById('btnChannelAntigravity') as HTMLButtonElement | null;
    btnChannelProject = document.getElementById('btnChannelProject') as HTMLButtonElement | null;
    btnChannelGeminiCli = document.getElementById('btnChannelGeminiCli') as HTMLButtonElement | null;
    btnChannelNvidia = document.getElementById('btnChannelNvidia') as HTMLButtonElement | null;
    btnChannelOther = document.getElementById('btnChannelOther') as HTMLButtonElement | null;
    otherGroupTabs = document.getElementById('otherGroupTabs') as HTMLDivElement | null;
    otherLBModeContainer = document.getElementById('otherLBModeContainer') as HTMLDivElement | null;
    otherLBModeSelect = document.getElementById('otherLBModeSelect') as HTMLSelectElement | null;
    groupSelectOther = document.getElementById('selectOtherGroup') as HTMLSelectElement | null;
    nvidiaPoolModeContainer = document.getElementById('nvidiaPoolModeContainer') as HTMLDivElement | null;
    nvidiaPoolModeToggle = document.getElementById('nvidiaPoolModeToggle') as HTMLInputElement | null;
    nvidiaLBModeContainer = document.getElementById('nvidiaLBModeContainer') as HTMLDivElement | null;
    nvidiaLBModeSelect = document.getElementById('nvidiaLBModeSelect') as HTMLSelectElement | null;
    btnAddNvidiaAccount = document.getElementById('btnAddNvidiaAccount') as HTMLButtonElement | null;
    btnAddOtherAccount = document.getElementById('btnAddOtherAccount') as HTMLButtonElement | null;
    otherAccountModal = document.getElementById('otherAccountModal') as HTMLDivElement | null;
    otherAccountModalContainer = document.getElementById('otherAccountModalContainer') as HTMLDivElement | null;
    btnOtherModalSave = document.getElementById('btnOtherModalSave') as HTMLButtonElement | null;
    btnOtherModalCancel = document.getElementById('btnOtherModalCancel') as HTMLButtonElement | null;
    btnOtherModalClose = document.getElementById('btnOtherModalClose') as HTMLButtonElement | null;
    btnOtherFetchModels = document.getElementById('btnOtherFetchModels') as HTMLButtonElement | null;
    otherModalError = document.getElementById('otherModalError') as HTMLDivElement | null;
    nvidiaAccountModal = document.getElementById('nvidiaAccountModal') as HTMLDivElement | null;
    nvidiaAccountModalContainer = document.getElementById('nvidiaAccountModalContainer') as HTMLDivElement | null;
    btnNvidiaModalSave = document.getElementById('btnNvidiaModalSave') as HTMLButtonElement | null;
    btnNvidiaModalCancel = document.getElementById('btnNvidiaModalCancel') as HTMLButtonElement | null;
    btnNvidiaModalClose = document.getElementById('btnNvidiaModalClose') as HTMLButtonElement | null;
    nvidiaModalError = document.getElementById('nvidiaModalError') as HTMLDivElement | null;
    btnNvidiaFetchModels = document.getElementById('btnNvidiaFetchModels') as HTMLButtonElement | null;
    btnNvidiaPreferredModels = document.getElementById('btnNvidiaPreferredModels') as HTMLButtonElement | null;
    badgeNvidiaPreferredCount = document.getElementById('badgeNvidiaPreferredCount') as HTMLSpanElement | null;
    nvidiaPreferredModal = document.getElementById('nvidiaPreferredModelsModal') as HTMLDivElement | null;
    nvidiaPreferredModalContainer = document.getElementById('nvidiaPreferredModelsModalContainer') as HTMLDivElement | null;
    btnNvidiaPreferredModalCancel = document.getElementById('btnNvidiaPreferredModalCancel') as HTMLButtonElement | null;
    btnNvidiaPreferredModalClose = document.getElementById('btnNvidiaPreferredModalClose') as HTMLButtonElement | null;
    btnNvidiaPreferredSave = document.getElementById('btnNvidiaPreferredSave') as HTMLButtonElement | null;
    btnNvidiaPreferredSrcLocal = document.getElementById('btnNvidiaPreferredSrcLocal') as HTMLButtonElement | null;
    btnNvidiaPreferredSrcRemote = document.getElementById('btnNvidiaPreferredSrcRemote') as HTMLButtonElement | null;
    btnNvidiaPreferredMoveLeft = document.getElementById('btnNvidiaPreferredMoveLeft') as HTMLButtonElement | null;
    btnNvidiaPreferredMoveRight = document.getElementById('btnNvidiaPreferredMoveRight') as HTMLButtonElement | null;
    btnNvidiaPreferredMoveAllLeft = document.getElementById('btnNvidiaPreferredMoveAllLeft') as HTMLButtonElement | null;
    btnNvidiaPreferredMoveAllRight = document.getElementById('btnNvidiaPreferredMoveAllRight') as HTMLButtonElement | null;
    btnNvidiaPreferredFetch = document.getElementById('btnNvidiaPreferredFetch') as HTMLButtonElement | null;
    iconNvidiaPreferredFetch = document.getElementById('iconNvidiaPreferredFetch') as HTMLSpanElement | null;
    inputNvidiaPreferredSearchLeft = document.getElementById('inputNvidiaPreferredSearchLeft') as HTMLInputElement | null;
    inputNvidiaPreferredSearchRight = document.getElementById('inputNvidiaPreferredSearchRight') as HTMLInputElement | null;
    chkNvidiaPreferredSelectAllLeft = document.getElementById('chkNvidiaPreferredSelectAllLeft') as HTMLInputElement | null;
    chkNvidiaPreferredSelectAllRight = document.getElementById('chkNvidiaPreferredSelectAllRight') as HTMLInputElement | null;
    nvidiaPreferredModelsListLeft = document.getElementById('nvidiaPreferredModelsListLeft') as HTMLDivElement | null;
    nvidiaPreferredModelsListRight = document.getElementById('nvidiaPreferredModelsListRight') as HTMLDivElement | null;
    nvidiaPreferredEmptyLeft = document.getElementById('nvidiaPreferredEmptyLeft') as HTMLDivElement | null;
    nvidiaPreferredEmptyRight = document.getElementById('nvidiaPreferredEmptyRight') as HTMLDivElement | null;
    lblNvidiaPreferredCount = document.getElementById('lblNvidiaPreferredCount') as HTMLSpanElement | null;
    lblNvidiaPreferredSource = document.getElementById('lblNvidiaPreferredSource') as HTMLDivElement | null;
    lblNvidiaPreferredVisibleLeft = document.getElementById('lblNvidiaPreferredVisibleLeft') as HTMLSpanElement | null;
    lblNvidiaPreferredVisibleRight = document.getElementById('lblNvidiaPreferredVisibleRight') as HTMLSpanElement | null;
    nvidiaPreferredError = document.getElementById('nvidiaPreferredError') as HTMLDivElement | null;
    btnExportAccounts = document.getElementById('btnExportAccounts') as HTMLButtonElement | null;
    btnImportAccounts = document.getElementById('btnImportAccounts') as HTMLButtonElement | null;

    btnShowSessionBindings = document.getElementById('btnShowSessionBindings') as HTMLButtonElement | null;
    sessionBindingsModal = document.getElementById('sessionBindingsModal') as HTMLDivElement | null;
    sessionBindingsModalCloseBtn = document.getElementById('sessionBindingsModalCloseBtn') as HTMLButtonElement | null;
    sessionBindingsModalCloseBtnSecondary = document.getElementById('sessionBindingsModalCloseBtnSecondary') as HTMLButtonElement | null;
    sessionBindingsTableBody = document.getElementById('sessionBindingsTableBody') as HTMLTableSectionElement | null;
    sessionBindingsModalClearAllBtn = document.getElementById('sessionBindingsModalClearAllBtn') as HTMLButtonElement | null;
    sessionBindingsCount = document.getElementById('sessionBindingsCount') as HTMLSpanElement | null;

    if (btnShowSessionBindings) {
        btnShowSessionBindings.addEventListener('click', showSessionBindings);
    }
    if (sessionBindingsModalCloseBtn) {
        sessionBindingsModalCloseBtn.addEventListener('click', hideSessionBindings);
    }
    if (sessionBindingsModalCloseBtnSecondary) {
        sessionBindingsModalCloseBtnSecondary.addEventListener('click', hideSessionBindings);
    }
    if (sessionBindingsModalClearAllBtn) {
        sessionBindingsModalClearAllBtn.addEventListener('click', clearAllSessionBindings);
    }
    if (btnRefreshAllQuota) {
        btnRefreshAllQuota.addEventListener('click', refreshAllQuotas);
    }

    // Filter & Pagination Event Bindings
    const inputAccountSearch = document.getElementById('inputAccountSearch') as HTMLInputElement | null;
    const selectAccountStatus = document.getElementById('selectAccountStatus') as HTMLSelectElement | null;
    const selectAccountTier = document.getElementById('selectAccountTier') as HTMLSelectElement | null;
    const btnPrevAccountPage = document.getElementById('btnPrevAccountPage') as HTMLButtonElement | null;
    const btnNextAccountPage = document.getElementById('btnNextAccountPage') as HTMLButtonElement | null;

    if (inputAccountSearch) {
        inputAccountSearch.addEventListener('input', (e: any) => {
            state.accountSearchQuery = e.target.value;
            state.accountCurrentPage = 1;
            renderAccounts(state.currentAccountsList);
        });
    }
    if (selectAccountStatus) {
        selectAccountStatus.addEventListener('change', (e: any) => {
            state.accountStatusFilter = e.target.value;
            state.accountCurrentPage = 1;
            renderAccounts(state.currentAccountsList);
        });
    }
    if (selectAccountTier) {
        selectAccountTier.addEventListener('change', (e: any) => {
            state.accountTierFilter = e.target.value;
            state.accountCurrentPage = 1;
            renderAccounts(state.currentAccountsList);
        });
    }
    if (btnPrevAccountPage) {
        btnPrevAccountPage.addEventListener('click', () => {
            if (state.accountCurrentPage > 1) {
                state.accountCurrentPage--;
                renderAccounts(state.currentAccountsList);
            }
        });
    }
    if (btnNextAccountPage) {
        btnNextAccountPage.addEventListener('click', () => {
            state.accountCurrentPage++;
            renderAccounts(state.currentAccountsList);
        });
    }

    btnLayoutGrid = document.getElementById('btnLayoutGrid') as HTMLButtonElement | null;
    btnLayoutList = document.getElementById('btnLayoutList') as HTMLButtonElement | null;

    if (btnLayoutGrid) {
        btnLayoutGrid.addEventListener('click', () => {
            if (state.accountLayout === 'grid') return;
            state.accountLayout = 'grid';
            localStorage.setItem('accounts_layout', 'grid');
            updateLayoutUI();
            renderAccounts(state.currentAccountsList);
        });
    }
    if (btnLayoutList) {
        btnLayoutList.addEventListener('click', () => {
            if (state.accountLayout === 'list') return;
            state.accountLayout = 'list';
            localStorage.setItem('accounts_layout', 'list');
            updateLayoutUI();
            renderAccounts(state.currentAccountsList);
        });
    }

    const selectGridColumns = document.getElementById('selectGridColumns') as HTMLSelectElement | null;
    if (selectGridColumns) {
        selectGridColumns.addEventListener('change', (e: any) => {
            const cols = Number(e.target.value);
            state.accountGridColumns = cols;
            localStorage.setItem('accounts_grid_columns', String(cols));
            updateLayoutUI();
            renderAccounts(state.currentAccountsList);
        });
    }

    updateLayoutUI();

    if (btnClearSessions) {
        btnClearSessions.addEventListener('click', async () => {
            if (!btnClearSessions) return;
            const icon = btnClearSessions.querySelector('.material-symbols-outlined');
            const label = btnClearSessions.querySelector('span:last-child');
            if (!label) return;
            const origLabel = label.textContent || '';
            
            if (icon) icon.classList.add('animate-spin');
            label.textContent = '清空中...';
            btnClearSessions.disabled = true;
            try {
                const res = await ipcRenderer.invoke('pool:clear-sessions');
                if (res && res.success) {
                    label.textContent = `已清空 ${res.cleared} 条`;
                    setTimeout(() => { label.textContent = origLabel; }, 2000);
                }
            } catch (err) {
                label.textContent = '清空失败';
                setTimeout(() => { label.textContent = origLabel; }, 2000);
            } finally {
                if (icon) icon.classList.remove('animate-spin');
                btnClearSessions.disabled = false;
            }
        });
    }

    if (btnAddAccount && addAccountDropdown) {
        btnAddAccount.addEventListener('click', async () => {
            if (state.isLoadingAuth) {
                try {
                    await ipcRenderer.invoke('auth:cancel-login');
                } catch (err) {
                    console.error('Failed to cancel login:', err);
                }
                return;
            }
            if (addAccountDropdown) addAccountDropdown.classList.toggle('hidden');
        });
    }

    // Dynamic project-based button appending
    if (addAccountDropdown && !document.getElementById('btnProjectLogin')) {
        const projectLoginButton = document.createElement('button');
        projectLoginButton.id = 'btnProjectLogin';
        projectLoginButton.className = 'w-full text-left px-4 py-2 text-[13px] text-on-surface dark:text-white hover:bg-slate-50 dark:hover:bg-white/5 transition-colors flex items-center gap-2 border-t border-outline-variant/10 mt-1 pt-3';
        projectLoginButton.type = 'button';
        const dict = i18n[state.currentLanguage] || i18n.zh;
        projectLoginButton.innerHTML = `
            <span class="material-symbols-outlined text-emerald-500 text-[16px]">cloud</span>
            <div>
                <div class="font-bold" data-i18n="useGcpProjectTitle">${dict.useGcpProjectTitle || 'Use a Google Cloud project'}</div>
                <div class="text-[10px] text-outline" data-i18n="useGcpProjectDesc">${dict.useGcpProjectDesc || '风控原因，需要提供带账单项目'}</div>
            </div>
        `;
        projectLoginButton.addEventListener('click', () => startProjectLogin());
        if (addAccountDropdown.children.length >= 2) {
            addAccountDropdown.insertBefore(projectLoginButton, addAccountDropdown.children[1]);
        } else {
            addAccountDropdown.appendChild(projectLoginButton);
        }
    }

    // NVIDIA 通道切换 Tab
    if (btnChannelNvidia) {
        btnChannelNvidia.addEventListener('click', () => {
            state.selectedAccountIds = [];
            state.currentViewTab = 'nvidia';
            updateViewTabUI();
            renderOtherGroupTabs();
            if (state.currentAccountsList) {
                renderAccounts(state.currentAccountsList);
            }
            updateAggregateQuotaUI();
            updateBatchActionBarUI();
        });
    }

    // Other 通道切换 Tab
    if (btnChannelOther) {
        btnChannelOther.addEventListener('click', () => {
            state.selectedAccountIds = [];
            state.currentViewTab = 'other';
            updateViewTabUI();
            renderOtherGroupTabs();
            if (state.currentAccountsList) {
                renderAccounts(state.currentAccountsList);
            }
            updateAggregateQuotaUI();
            updateBatchActionBarUI();
        });
    }

    // Other 添加账号下拉项 → 打开 Other 账号模态
    if (btnAddOtherAccount) {
        btnAddOtherAccount.addEventListener('click', () => {
            if (addAccountDropdown) addAccountDropdown.classList.add('hidden');
            openOtherAccountModal();
        });
    }

    // Other 账号模态:关闭/取消/保存/获取模型
    if (btnOtherModalClose) btnOtherModalClose.addEventListener('click', closeOtherAccountModal);
    if (btnOtherModalCancel) btnOtherModalCancel.addEventListener('click', closeOtherAccountModal);
    if (btnOtherModalSave) btnOtherModalSave.addEventListener('click', submitOtherAccount);
    if (btnOtherFetchModels) btnOtherFetchModels.addEventListener('click', fetchOtherModels);
    if (groupSelectOther) groupSelectOther.addEventListener('change', onOtherGroupSelectChange);

    // NVIDIA 添加账号下拉项 → 打开 NVIDIA 账号模态
    if (btnAddNvidiaAccount) {
        btnAddNvidiaAccount.addEventListener('click', () => {
            if (addAccountDropdown) addAccountDropdown.classList.add('hidden');
            openNvidiaAccountModal();
        });
    }

    if (poolModeToggle) {
        poolModeToggle.addEventListener('change', (e: any) => {
            if (state.currentViewTab === 'project') {
                ipcRenderer.send('pool:toggle-project', e.target.checked);
            /* } else if (state.currentViewTab === 'gemini-cli') {
                ipcRenderer.send('pool:toggle-gemini-cli', e.target.checked); */
            } else {
                ipcRenderer.send('pool:toggle', e.target.checked);
            }
            updatePoolModeUI();
            updateAggregateQuotaUI();
        });
    }

    // NVIDIA 池算法选择框
    if (nvidiaLBModeSelect) {
        nvidiaLBModeSelect.addEventListener('change', (e: any) => {
            ipcRenderer.send('nvidia:set-lb-mode', e.target.value);
            updateViewTabUI();
        });
    }

    // Other 号池组内负载均衡方式选择框:作用于当前选中组(「全部组」下拉不可见,不发送)。
    if (otherLBModeSelect) {
        otherLBModeSelect.addEventListener('change', (e: any) => {
            if (!otherLBModeSelectorVisible()) return;
            ipcRenderer.send('other:set-lb-mode', state.otherGroupFilter, e.target.value);
        });
        otherLBModeSelect.addEventListener('click', (e) => e.stopPropagation());
    }

    // NVIDIA 账号模态：关闭/取消/保存
    if (btnNvidiaModalClose) btnNvidiaModalClose.addEventListener('click', closeNvidiaAccountModal);
    if (btnNvidiaModalCancel) btnNvidiaModalCancel.addEventListener('click', closeNvidiaAccountModal);
    if (btnNvidiaModalSave) btnNvidiaModalSave.addEventListener('click', submitNvidiaAccount);
    if (btnNvidiaFetchModels) btnNvidiaFetchModels.addEventListener('click', fetchNvidiaModels);

    // NVIDIA 全局专属模型清单 Modal:事件绑定 + 来源事件订阅(仅绑定一次)
    if (!nvidiaPreferredSourceRespEventBound) {
        ipcRenderer.on('settings:nvidia-preferred-models-res', (_e: any, res: any) => {
            if (res && res.success) {
                updateNvidiaPreferredBadge(res.count ?? 0);
            }
        });
        nvidiaPreferredSourceRespEventBound = true;
    }
    if (btnNvidiaPreferredModels) btnNvidiaPreferredModels.addEventListener('click', openNvidiaPreferredModelsModal);
    if (btnNvidiaPreferredModalClose) btnNvidiaPreferredModalClose.addEventListener('click', closeNvidiaPreferredModelsModal);
    if (btnNvidiaPreferredModalCancel) btnNvidiaPreferredModalCancel.addEventListener('click', closeNvidiaPreferredModelsModal);
    if (btnNvidiaPreferredSrcLocal) btnNvidiaPreferredSrcLocal.addEventListener('click', () => switchNvidiaPreferredSource('local'));
    if (btnNvidiaPreferredSrcRemote) btnNvidiaPreferredSrcRemote.addEventListener('click', () => switchNvidiaPreferredSource('remote'));
    if (btnNvidiaPreferredFetch) btnNvidiaPreferredFetch.addEventListener('click', () => { void fetchAndRenderNvidiaPreferredModels(false, 'remote'); });
    if (btnNvidiaPreferredSave) btnNvidiaPreferredSave.addEventListener('click', submitNvidiaPreferredModels);
    if (btnNvidiaPreferredMoveLeft) btnNvidiaPreferredMoveLeft.addEventListener('click', moveNvidiaPreferredSelectedToLeft);
    if (btnNvidiaPreferredMoveRight) btnNvidiaPreferredMoveRight.addEventListener('click', moveNvidiaPreferredSelectedToRight);
    if (btnNvidiaPreferredMoveAllLeft) btnNvidiaPreferredMoveAllLeft.addEventListener('click', moveAllNvidiaPreferredToLeft);
    if (btnNvidiaPreferredMoveAllRight) btnNvidiaPreferredMoveAllRight.addEventListener('click', moveAllNvidiaPreferredToRight);
    if (inputNvidiaPreferredSearchLeft) inputNvidiaPreferredSearchLeft.addEventListener('input', applyNvidiaPreferredSearchLeft);
    if (inputNvidiaPreferredSearchRight) inputNvidiaPreferredSearchRight.addEventListener('input', applyNvidiaPreferredSearchRight);
    if (chkNvidiaPreferredSelectAllLeft) chkNvidiaPreferredSelectAllLeft.addEventListener('change', toggleNvidiaPreferredSelectAllLeft);
    if (chkNvidiaPreferredSelectAllRight) chkNvidiaPreferredSelectAllRight.addEventListener('change', toggleNvidiaPreferredSelectAllRight);

    if (btnExportAccounts) {
        btnExportAccounts.addEventListener('click', () => {
            const provider = state.currentViewTab || 'antigravity';
            ipcRenderer.send('accounts:export-all', provider);
        });
    }

    if (btnImportAccounts) {
        btnImportAccounts.addEventListener('click', () => {
            ipcRenderer.send('accounts:import');
        });
    }

    if (btnChannelAntigravity) {
        btnChannelAntigravity.addEventListener('click', () => {
            state.currentViewTab = 'antigravity';
            state.selectedAccountIds = [];
            updateViewTabUI();
            renderOtherGroupTabs();
            if (state.currentAccountsList) {
                renderAccounts(state.currentAccountsList);
            }
            updateAggregateQuotaUI();
            updateBatchActionBarUI();
        });
    }
    if (btnChannelProject) {
        btnChannelProject.addEventListener('click', () => {
            state.selectedAccountIds = [];
            state.currentViewTab = 'project';
            updateViewTabUI();
            renderOtherGroupTabs();
            if (state.currentAccountsList) {
                renderAccounts(state.currentAccountsList);
            }
            updateAggregateQuotaUI();
            updateBatchActionBarUI();
        });
    }
    /* if (btnChannelGeminiCli) {
        btnChannelGeminiCli.addEventListener('click', () => {
            state.currentViewTab = 'gemini-cli';
            ipcRenderer.send('channel:switch', 'gemini-cli');
            updateViewTabUI();
            if (state.currentAccountsList) {
                renderAccounts(state.currentAccountsList);
            }
            updateAggregateQuotaUI();
        });
    } */

    // Register accounts data update channel listener
    // (Moved to global initAccountsGlobalEvents below)


    // 全选按钮绑定
    const chkAll = document.getElementById('chkSelectAllAccounts') as HTMLInputElement | null;
    if (chkAll) {
        chkAll.addEventListener('change', (e: any) => {
            const isChecked = e.target.checked;
            const visibleCheckboxes = document.querySelectorAll('.account-card-checkbox') as NodeListOf<HTMLInputElement>;
            visibleCheckboxes.forEach(cb => {
                const accId = cb.getAttribute('data-account-id');
                if (!accId) return;
                cb.checked = isChecked;
                if (isChecked) {
                    if (!state.selectedAccountIds.includes(accId)) {
                        state.selectedAccountIds.push(accId);
                    }
                } else {
                    state.selectedAccountIds = state.selectedAccountIds.filter(id => id !== accId);
                }
            });
            updateBatchActionBarUI();
        });
    }

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

    // 当 DOM 挂载时，根据已经同步过的 accounts 数据对 DOM 状态进行一轮初始化
    if (state.lastBackendData) {
        updateViewTabUI();
        updatePoolModeUI();
        updateLayoutUI();
        renderOtherGroupTabs();
        if (state.currentAccountsList) {
            renderAccounts(state.currentAccountsList);
        }
        updateAggregateQuotaUI();
    }

    // 首屏静默回读已保存的 NVIDIA 专属模型清单,刷新入口徽标计数(不打开 Modal)
    // isOpening=true、force=undefined → 后端"cache 优先,空则回退远端";命中本地清单即刷徽标
    void fetchAndRenderNvidiaPreferredModels(true, undefined);

    // 主动触发一次账号数据同步，确保在前端初始化完毕后拉取到最新数据
    ipcRenderer.send('accounts:get');
    initAutoTriggerModalEvents();
}

let isInitialQuotaLoaded = false;

export async function refreshAllAccountsQuotasSilently() {
    if (!state.currentAccountsList || state.currentAccountsList.length === 0) return;
    try {
        for (const acc of state.currentAccountsList) {
            if (!acc.enabled || acc.provider === 'nvidia') {
                continue;
            }
            try {
                const result = await ipcRenderer.invoke('quota:fetch', acc.id);
                if (result && !result.error) {
                    state.quotaCache[acc.id] = result.buckets;
                    updateAggregateQuotaUI(); // 每刷新一个账号，就重新计算并更新一次总额度条
                }
            } catch (err) {
                console.error(`[Silent Refresh] Failed to refresh quota for ${acc.email}:`, err);
            }
            await new Promise(r => setTimeout(r, 100));
        }
        const accountsListEl = document.getElementById('accountsList');
        if (accountsListEl && state.callbacks.renderAccounts) {
            state.callbacks.renderAccounts(state.currentAccountsList);
        }
    } catch (err) {
        console.error('[Silent Refresh] Error during global silent quota refresh:', err);
    }
}

export function initAccountsGlobalEvents() {
    // 注册全局一次性监听器，防止多次打开账号页面时重复绑定及闭包导致的 Detached DOM 内存泄漏
    if (!isGlobalEventsInitialized) {
        // 获取主页“一键刷新”按钮 DOM 并进行事件绑定，因为主页 DOM 在程序启动时即始终存在
        btnRefreshAggregateQuota = document.getElementById('btnRefreshAggregateQuota') as HTMLButtonElement | null;
        btnRefreshAggregateIcon = document.getElementById('btnRefreshAggregateIcon');
        if (btnRefreshAggregateQuota) {
            btnRefreshAggregateQuota.addEventListener('click', refreshAllAccountsQuotas);
        }

        // 1. 注册账号数据更新频道监听
        ipcRenderer.on('accounts-res', (event: any, data: any) => {
            // 合并而非整对象覆盖:后端某些广播源(如 OnAccountsUpdated 旧薄载荷)可能缺 otherGroups/
            // otherPoolMode 等字段,展开 {...old, ...data} 在缺字段时保留上一次完整值,避免 Other
            // 分组子 Tab 被冲成空数组而偶发隐藏。
            state.lastBackendData = { ...(state.lastBackendData || {}), ...data };
            if (data && typeof data.activeChannel !== 'undefined') {
                state.currentActiveChannel = data.activeChannel;
            }
            if (!state.currentViewTab) {
                state.currentViewTab = state.currentActiveChannel;
            }
            
            // 尝试更新视图 Tab
            updateViewTabUI();

            if (data.accounts) {
                state.currentAccountsList = data.accounts;
                // 每当后端广播账号快照,刷新 Other 二级组名子 Tab(新增/删除组、计数变化即时反映)。
                renderOtherGroupTabs();
                // 只有当 DOM 列表容器存在时才重新绘制账号卡片
                const accountsListEl = document.getElementById('accountsList');
                if (accountsListEl) {
                    renderAccounts(data.accounts);
                }
            }
            
            updateAggregateQuotaUI();
            
            if (state.callbacks.updateAnalyzeAccountSelect) {
                state.callbacks.updateAnalyzeAccountSelect();
            }

            // 第一次加载账号时，自动在后台静默刷新所有账号池的额度信息
            if (!isInitialQuotaLoaded && data.accounts && data.accounts.length > 0) {
                isInitialQuotaLoaded = true;
                refreshAllAccountsQuotasSilently();
            }
        });

        // 1.2 注册配额实时更新频道监听
        ipcRenderer.on('quota-updated', (event: any, data: any) => {
            if (data && data.accountId) {
                state.quotaCache[data.accountId] = data.buckets;
                const acc = state.currentAccountsList.find(a => a.id === data.accountId);
                const cooldowns = acc ? acc.cooldowns : {};
                loadAccountQuota(data.accountId, null, null, false, cooldowns);
            }
        });

        // 监听账号多选事件以刷新批量操作栏
        document.addEventListener('account-selection-changed', updateBatchActionBarUI);

        // 绑定全局点击事件，用于关闭添加账号的下拉菜单
        document.addEventListener('click', (e: any) => {
            if (btnAddAccount && addAccountDropdown && !btnAddAccount.contains(e.target) && !addAccountDropdown.contains(e.target)) {
                addAccountDropdown.classList.add('hidden');
            }
        });

        // 2. 绑定全局 log 事件，过滤显示测试进度
        ipcRenderer.on('log', (event: any, logText: string) => {
            if (logText && logText.includes('[测试回复]') && triggerLogsArea) {
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
                
                // Limit child nodes to 100 to prevent DOM memory leak
                if (triggerLogsArea.children.length > 100) {
                    while (triggerLogsArea.children.length > 80) {
                        if (triggerLogsArea.firstChild) {
                            triggerLogsArea.removeChild(triggerLogsArea.firstChild);
                        }
                    }
                }
                
                triggerLogsArea.scrollTop = triggerLogsArea.scrollHeight;
            }
        });

        isGlobalEventsInitialized = true;
    }

    // 3. 页面打开时拉取一次最新账号信息
    ipcRenderer.send('accounts:get');
}

async function loadSessionBindings() {
    const dict = i18n[state.currentLanguage] || {};
    const tableBody = sessionBindingsTableBody;
    if (!tableBody) return;
    tableBody.innerHTML = `
        <tr>
            <td colspan="4" class="p-8 text-center text-outline dark:text-outline-variant italic">
                <span class="inline-block animate-spin mr-2">⏳</span>${dict.sessionBindingsLoading || '正在加载会话绑定数据...'}
            </td>
        </tr>
    `;
    
    try {
        const list = await ipcRenderer.invoke('sessions:get', state.currentViewTab || '') as Array<{
            sessionKey: string;
            accountId: string;
            accountEmail: string;
            provider?: string;
            lastActive: number;
        }>;
        
        if (sessionBindingsCount) {
            const totalText = (dict.sessionBindingsTotal || '共 {count} 条记录').replace('{count}', list.length.toString());
            sessionBindingsCount.textContent = totalText;
        }
        
        if (list.length === 0) {
            tableBody.innerHTML = `
                <tr>
                    <td colspan="4" class="p-8 text-center text-outline dark:text-outline-variant italic">
                        📭 ${dict.sessionBindingsEmpty || '当前暂无会话路由绑定关系'}
                    </td>
                </tr>
            `;
            return;
        }
        
        // 按照最后活跃时间降序排序
        list.sort((a, b) => b.lastActive - a.lastActive);
        
        tableBody.innerHTML = '';
        list.forEach(item => {
            const tr = document.createElement('tr');
            tr.className = 'hover:bg-slate-50 dark:hover:bg-white/5 transition-colors border-b border-outline-variant/10';
            
            // 格式化活跃时间
            const timeStr = new Date(item.lastActive).toLocaleString('zh-CN', {
                hour12: false,
                month: '2-digit',
                day: '2-digit',
                hour: '2-digit',
                minute: '2-digit',
                second: '2-digit'
            });
            
            // 为了美观，给 SessionKey 不同的类型不同的徽章
            let keyBadge = '';
            let channelBadge = '';
            let keyText = item.sessionKey;
            
            const projectLbText = dict.projectLoadBalancing || '项目负载均衡';
            const accountLbText = dict.poolLoadBalance || '账号负载均衡';
            
            if (item.sessionKey.startsWith('auth:prj:')) {
                keyBadge = '<span class="px-1.5 py-0.5 rounded bg-primary/10 text-primary dark:text-primary-fixed-dim text-[10px] font-bold mr-1.5">Bearer</span>';
                channelBadge = `<span class="px-1.5 py-0.5 rounded bg-purple-500/10 text-purple-500 dark:text-purple-300 text-[10px] font-bold mr-1.5">${projectLbText}</span>`;
                keyText = item.sessionKey.substring(9);
            } else if (item.sessionKey.startsWith('auth:acc:')) {
                keyBadge = '<span class="px-1.5 py-0.5 rounded bg-primary/10 text-primary dark:text-primary-fixed-dim text-[10px] font-bold mr-1.5">Bearer</span>';
                channelBadge = `<span class="px-1.5 py-0.5 rounded bg-teal-500/10 text-teal-600 dark:text-teal-300 text-[10px] font-bold mr-1.5">${accountLbText}</span>`;
                keyText = item.sessionKey.substring(9);
            } else if (item.sessionKey.startsWith('sock:prj:')) {
                keyBadge = '<span class="px-1.5 py-0.5 rounded bg-amber-500/10 text-amber-500 text-[10px] font-bold mr-1.5">Socket</span>';
                channelBadge = `<span class="px-1.5 py-0.5 rounded bg-purple-500/10 text-purple-500 dark:text-purple-300 text-[10px] font-bold mr-1.5">${projectLbText}</span>`;
                keyText = item.sessionKey.substring(9);
            } else if (item.sessionKey.startsWith('sock:acc:')) {
                keyBadge = '<span class="px-1.5 py-0.5 rounded bg-amber-500/10 text-amber-500 text-[10px] font-bold mr-1.5">Socket</span>';
                channelBadge = `<span class="px-1.5 py-0.5 rounded bg-teal-500/10 text-teal-600 dark:text-teal-300 text-[10px] font-bold mr-1.5">${accountLbText}</span>`;
                keyText = item.sessionKey.substring(9);
            } else if (item.sessionKey.startsWith('auth:')) {
                keyBadge = '<span class="px-1.5 py-0.5 rounded bg-primary/10 text-primary dark:text-primary-fixed-dim text-[10px] font-bold mr-1.5">Bearer</span>';
                keyText = item.sessionKey.substring(5);
            } else if (item.sessionKey.startsWith('sock:')) {
                keyBadge = '<span class="px-1.5 py-0.5 rounded bg-amber-500/10 text-amber-500 text-[10px] font-bold mr-1.5">Socket</span>';
                keyText = item.sessionKey.substring(5);
            }
            
            tr.innerHTML = `
                <td class="p-3 font-data-mono break-all max-w-[280px]">
                    <div class="flex items-center flex-wrap gap-1">
                        ${keyBadge}
                        ${channelBadge}
                        <span class="text-on-surface dark:text-white font-medium">${keyText}</span>
                    </div>
                </td>
                <td class="p-3 text-outline dark:text-outline-variant font-medium break-all max-w-[200px]">${item.accountEmail}</td>
                <td class="p-3 text-outline/80 dark:text-outline-variant/80 font-data-mono">${timeStr}</td>
                <td class="p-3 text-center">
                    <button class="unbind-btn text-red-500 hover:text-white hover:bg-red-500 active:bg-red-600 px-2 py-1 rounded transition-all text-[11px] font-bold border border-red-500/20" data-key="${item.sessionKey}">
                        ${dict.btnUnbind || '解绑'}
                    </button>
                </td>
            `;
            
            // 绑定解绑事件
            const unbindBtn = tr.querySelector('.unbind-btn');
            if (unbindBtn) {
                unbindBtn.addEventListener('click', async (e) => {
                    const key = (e.currentTarget as HTMLButtonElement).getAttribute('data-key');
                    if (!key) return;
                    
                    (e.currentTarget as HTMLButtonElement).disabled = true;
                    (e.currentTarget as HTMLButtonElement).textContent = dict.btnUnbindProcessing || '处理中...';
                    
                    try {
                        const res = await ipcRenderer.invoke('sessions:unbind', key);
                        if (res && res.success) {
                            loadSessionBindings();
                        } else {
                            alert(dict.unbindFailed || '解绑失败，请重试');
                            (e.currentTarget as HTMLButtonElement).disabled = false;
                            (e.currentTarget as HTMLButtonElement).textContent = dict.btnUnbind || '解绑';
                        }
                    } catch (err) {
                        console.error('Failed to unbind session:', err);
                        alert(dict.unbindRequestFailed || '解绑请求失败');
                        (e.currentTarget as HTMLButtonElement).disabled = false;
                        (e.currentTarget as HTMLButtonElement).textContent = dict.btnUnbind || '解绑';
                    }
                });
            }
            
            tableBody.appendChild(tr);
        });
        
    } catch (err) {
        console.error('Failed to load session bindings:', err);
        const errMsg = (dict.loadBindingsFailed || '获取绑定关系失败: {error}').replace('{error}', (err as Error).message);
        tableBody.innerHTML = `
            <tr>
                <td colspan="4" class="p-8 text-center text-red-500 italic">
                    ❌ ${errMsg}
                </td>
            </tr>
        `;
    }
}

function showSessionBindings() {
    if (!sessionBindingsModal) return;
    sessionBindingsModal.classList.remove('pointer-events-none', 'opacity-0');
    sessionBindingsModal.classList.add('opacity-100');
    const container = sessionBindingsModal.querySelector('#sessionBindingsModalContainer');
    if (container) {
        container.classList.remove('scale-95');
        container.classList.add('scale-100');
    }
    loadSessionBindings();
}

function hideSessionBindings() {
    if (!sessionBindingsModal) return;
    sessionBindingsModal.classList.add('opacity-0', 'pointer-events-none');
    sessionBindingsModal.classList.remove('opacity-100');
    const container = sessionBindingsModal.querySelector('#sessionBindingsModalContainer');
    if (container) {
        container.classList.add('scale-95');
        container.classList.remove('scale-100');
    }
}

async function clearAllSessionBindings() {
    const dict = i18n[state.currentLanguage] || {};
    const confirmMsg = dict.btnClearAllBindingsConfirm || '您确定要清空所有的会话路由绑定关系吗？这将会使后续客户端的请求重新在可用账号池中进行轮询或一致性哈希分配。';
    if (!await $confirm(confirmMsg)) {
        return;
    }
    if (sessionBindingsModalClearAllBtn) {
        sessionBindingsModalClearAllBtn.disabled = true;
        const span = sessionBindingsModalClearAllBtn.querySelector('span:last-child');
        if (span) span.textContent = dict.btnClearAllBindingsProcessing || '清空中...';
    }
    try {
        const res = await ipcRenderer.invoke('pool:clear-sessions');
        if (res && res.success) {
            loadSessionBindings();
        }
    } catch (err) {
        console.error('Failed to clear sessions:', err);
    } finally {
        if (sessionBindingsModalClearAllBtn) {
            sessionBindingsModalClearAllBtn.disabled = false;
            const span = sessionBindingsModalClearAllBtn.querySelector('span:last-child');
            if (span) span.textContent = dict.btnClearAllBindings || '清空所有绑定';
        }
    }
}


// Global window registration for DOM inline click events
(window as any).startLogin = startLogin;
(window as any).startProjectLogin = startProjectLogin;

export function updateBatchActionBarUI() {
    const bar = document.getElementById('batchActionBar');
    const lbl = document.getElementById('lblSelectedCount');
    const chkAll = document.getElementById('chkSelectAllAccounts') as HTMLInputElement | null;
    if (!bar || !lbl) return;

    const dict = i18n[state.currentLanguage] || i18n.zh;
    
    const count = state.selectedAccountIds.length;
    if (count > 0) {
        bar.classList.remove('hidden');
        bar.classList.add('flex');
        lbl.textContent = (dict.selectedAccountsCount || `已选择 {count} 个账号`).replace('{count}', String(count));
    } else {
        bar.classList.remove('flex');
        bar.classList.add('hidden');
    }

    if (chkAll) {
        const visibleCheckboxes = document.querySelectorAll('.account-card-checkbox') as NodeListOf<HTMLInputElement>;
        if (visibleCheckboxes.length > 0) {
            chkAll.checked = Array.from(visibleCheckboxes).every(cb => cb.checked);
        } else {
            chkAll.checked = false;
        }
    }
}

function triggerTestResponse() {
    if (state.selectedAccountIds.length === 0) {
        alert('请先勾选需要触发测试回复的账号！');
        return;
    }
    showTriggerTestModal();
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

// Register shared callbacks
state.callbacks.renderAccounts = renderAccounts;
state.callbacks.updateAggregateQuotaUI = updateAggregateQuotaUI;

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

function initAutoTriggerModalEvents() {
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

function openNvidiaAccountModal() {
    if (!nvidiaAccountModal || !nvidiaAccountModalContainer) return;

    // 添加态:清空编辑态标记(标题/保存按钮文案由 data-i18n 静态文本承载,无需复位)。
    nvidiaEditId = null;

    // Clear previous inputs
    const inputBaseUrl = document.getElementById('inputNvidiaBaseUrl') as HTMLInputElement;
    const inputApiKey = document.getElementById('inputNvidiaApiKey') as HTMLInputElement;
    const inputLabel = document.getElementById('inputNvidiaLabel') as HTMLInputElement;
    const inputModelSonnet = document.getElementById('inputNvidiaModelSonnet') as HTMLInputElement;
    const inputModelOpus = document.getElementById('inputNvidiaModelOpus') as HTMLInputElement;
    const inputModelHaiku = document.getElementById('inputNvidiaModelHaiku') as HTMLInputElement;
    const inputModelFable = document.getElementById('inputNvidiaModelFable') as HTMLInputElement;
    const inputModelDefault = document.getElementById('inputNvidiaModelDefault') as HTMLInputElement;

    if (inputBaseUrl) inputBaseUrl.value = '';
    if (inputApiKey) inputApiKey.value = '';
    if (inputLabel) inputLabel.value = '';
    if (inputModelSonnet) inputModelSonnet.value = '';
    if (inputModelOpus) inputModelOpus.value = '';
    if (inputModelHaiku) inputModelHaiku.value = '';
    if (inputModelFable) inputModelFable.value = '';
    if (inputModelDefault) inputModelDefault.value = '';

    if (nvidiaModalError) {
        nvidiaModalError.classList.add('hidden');
        nvidiaModalError.textContent = '';
    }

    const selectIds = [
        'selectNvidiaModelSonnet',
        'selectNvidiaModelOpus',
        'selectNvidiaModelHaiku',
        'selectNvidiaModelFable',
        'selectNvidiaModelDefault'
    ];
    selectIds.forEach(id => {
        const selectEl = document.getElementById(id) as HTMLSelectElement | null;
        if (selectEl) {
            selectEl.classList.add('hidden');
            selectEl.innerHTML = '<option value="">选择模型...</option>';
        }
    });

    nvidiaAccountModal.classList.remove('opacity-0', 'pointer-events-none');
    nvidiaAccountModalContainer.classList.remove('scale-95');
    nvidiaAccountModalContainer.classList.add('scale-100');
}

function closeNvidiaAccountModal() {
    if (!nvidiaAccountModal || !nvidiaAccountModalContainer) return;
    nvidiaAccountModalContainer.classList.remove('scale-100');
    nvidiaAccountModalContainer.classList.add('scale-95');
    nvidiaAccountModal.classList.add('opacity-0', 'pointer-events-none');
}

// 当前处于编辑态的 NVIDIA 账号 id(添加态为 null),供 submitNvidiaAccount 区分 add/update。
let nvidiaEditId: string | null = null;

// openEditNvidiaAccount:预填 NVIDIA 账号模态框已有字段并切入编辑态。
// 账号展示名 = acc.Email;API Key 因后端不下发明文,编辑框留空,留空表示保持不变。
export function openEditNvidiaAccount(acc: any) {
    if (!nvidiaAccountModal || !nvidiaAccountModalContainer) return;

    nvidiaEditId = acc.id;

    const inputBaseUrl = document.getElementById('inputNvidiaBaseUrl') as HTMLInputElement;
    const inputApiKey = document.getElementById('inputNvidiaApiKey') as HTMLInputElement;
    const inputLabel = document.getElementById('inputNvidiaLabel') as HTMLInputElement;
    const inputModelSonnet = document.getElementById('inputNvidiaModelSonnet') as HTMLInputElement;
    const inputModelOpus = document.getElementById('inputNvidiaModelOpus') as HTMLInputElement;
    const inputModelHaiku = document.getElementById('inputNvidiaModelHaiku') as HTMLInputElement;
    const inputModelFable = document.getElementById('inputNvidiaModelFable') as HTMLInputElement;
    const inputModelDefault = document.getElementById('inputNvidiaModelDefault') as HTMLInputElement;

    if (inputBaseUrl) inputBaseUrl.value = acc.baseUrl || '';
    if (inputApiKey) {
        inputApiKey.value = ''; // 不预填明文 Key,留空保持不变
        inputApiKey.placeholder = acc.maskedKey || 'nvapi-... (留空保持不变)';
    }
    if (inputLabel) inputLabel.value = acc.email || '';
    if (inputModelSonnet) inputModelSonnet.value = acc.modelSonnet || '';
    if (inputModelOpus) inputModelOpus.value = acc.modelOpus || '';
    if (inputModelHaiku) inputModelHaiku.value = acc.modelHaiku || '';
    if (inputModelFable) inputModelFable.value = acc.modelFable || '';
    if (inputModelDefault) inputModelDefault.value = acc.defaultModel || '';

    // 隐藏模型选择下拉(编辑态不自动拉远端模型,保持手填;用户可点"获取模型")。
    const selectIds = [
        'selectNvidiaModelSonnet',
        'selectNvidiaModelOpus',
        'selectNvidiaModelHaiku',
        'selectNvidiaModelFable',
        'selectNvidiaModelDefault'
    ];
    selectIds.forEach(id => {
        const selectEl = document.getElementById(id) as HTMLSelectElement | null;
        if (selectEl) {
            selectEl.classList.add('hidden');
            selectEl.innerHTML = '<option value="">选择模型...</option>';
        }
    });

    if (nvidiaModalError) {
        nvidiaModalError.classList.add('hidden');
        nvidiaModalError.textContent = '';
    }

    // 标题与保存按钮文案切入编辑态。
    const titleEl = nvidiaAccountModal.querySelector('[data-i18n="nvidiaAddModalTitle"]') as HTMLElement | null;
    if (titleEl) titleEl.textContent = '编辑 NVIDIA 号池账号';
    if (btnNvidiaModalSave) {
        btnNvidiaModalSave.textContent = '保存修改';
    }

    nvidiaAccountModal.classList.remove('opacity-0', 'pointer-events-none');
    nvidiaAccountModalContainer.classList.remove('scale-95');
    nvidiaAccountModalContainer.classList.add('scale-100');
}

async function submitNvidiaAccount() {
    const inputBaseUrl = document.getElementById('inputNvidiaBaseUrl') as HTMLInputElement;
    const inputApiKey = document.getElementById('inputNvidiaApiKey') as HTMLInputElement;
    const inputLabel = document.getElementById('inputNvidiaLabel') as HTMLInputElement;
    const inputModelSonnet = document.getElementById('inputNvidiaModelSonnet') as HTMLInputElement;
    const inputModelOpus = document.getElementById('inputNvidiaModelOpus') as HTMLInputElement;
    const inputModelHaiku = document.getElementById('inputNvidiaModelHaiku') as HTMLInputElement;
    const inputModelFable = document.getElementById('inputNvidiaModelFable') as HTMLInputElement;
    const inputModelDefault = document.getElementById('inputNvidiaModelDefault') as HTMLInputElement;

    if (!inputBaseUrl || !inputApiKey) return;

    let baseUrl = inputBaseUrl.value.trim();
    const apiKey = inputApiKey.value.trim();

    if (!baseUrl) {
        baseUrl = 'https://integrate.api.nvidia.com/v1';
    }
    if (!apiKey && !nvidiaEditId) {
        if (nvidiaModalError) {
            nvidiaModalError.textContent = '请输入 API Key';
            nvidiaModalError.classList.remove('hidden');
        }
        return;
    }

    try {
        if (btnNvidiaModalSave) {
            btnNvidiaModalSave.disabled = true;
            btnNvidiaModalSave.textContent = nvidiaEditId ? '正在保存...' : '正在添加...';
        }

        let res;
        if (nvidiaEditId) {
            // 编辑态:定位既有账号;apiKey 留空保持不变。
            res = await ipcRenderer.invoke('nvidia:update',
                nvidiaEditId,
                baseUrl,
                apiKey,
                inputLabel?.value.trim() || '',
                inputModelDefault?.value.trim() || '',
                inputModelSonnet?.value.trim() || '',
                inputModelOpus?.value.trim() || '',
                inputModelHaiku?.value.trim() || '',
                inputModelFable?.value.trim() || ''
            );
        } else {
            res = await ipcRenderer.invoke('nvidia:add',
                baseUrl,
                apiKey,
                inputLabel?.value.trim() || '',
                inputModelDefault?.value.trim() || '',
                inputModelSonnet?.value.trim() || '',
                inputModelOpus?.value.trim() || '',
                inputModelHaiku?.value.trim() || '',
                inputModelFable?.value.trim() || ''
            );
        }

        if (res && res.success) {
            closeNvidiaAccountModal();
            ipcRenderer.send('accounts:get');
        } else {
            if (nvidiaModalError) {
                nvidiaModalError.textContent = res?.error || (nvidiaEditId ? '保存失败' : '添加失败');
                nvidiaModalError.classList.remove('hidden');
            }
        }
    } catch (err: any) {
        if (nvidiaModalError) {
            nvidiaModalError.textContent = err.message || '系统错误';
            nvidiaModalError.classList.remove('hidden');
        }
    } finally {
        if (btnNvidiaModalSave) {
            btnNvidiaModalSave.disabled = false;
            btnNvidiaModalSave.textContent = nvidiaEditId ? '保存修改' : '添加账号';
        }
    }
}

async function fetchNvidiaModels() {
    const inputBaseUrl = document.getElementById('inputNvidiaBaseUrl') as HTMLInputElement | null;
    const inputApiKey = document.getElementById('inputNvidiaApiKey') as HTMLInputElement | null;
    if (!btnNvidiaFetchModels) return;

    const baseUrl = inputBaseUrl ? inputBaseUrl.value.trim() : '';
    const apiKey = inputApiKey ? inputApiKey.value.trim() : '';

    if (nvidiaModalError) {
        nvidiaModalError.classList.add('hidden');
        nvidiaModalError.textContent = '';
    }

    const origHTML = btnNvidiaFetchModels.innerHTML;
    btnNvidiaFetchModels.disabled = true;
    btnNvidiaFetchModels.innerHTML = `<span class="material-symbols-outlined text-[14px] animate-spin">refresh</span><span>获取中...</span>`;

    try {
        const res = await ipcRenderer.invoke('nvidia:fetch-models', baseUrl, apiKey);
        if (res && res.success && Array.isArray(res.models)) {
            populateNvidiaModelSelects(res.models);
        } else {
            if (nvidiaModalError) {
                nvidiaModalError.textContent = (res && res.error) ? res.error : '获取模型列表失败';
                nvidiaModalError.classList.remove('hidden');
            }
        }
    } catch (err: any) {
        if (nvidiaModalError) {
            nvidiaModalError.textContent = err.message || '网络或接口请求出错';
            nvidiaModalError.classList.remove('hidden');
        }
    } finally {
        if (btnNvidiaFetchModels) {
            btnNvidiaFetchModels.disabled = false;
            btnNvidiaFetchModels.innerHTML = origHTML;
        }
    }
}

function populateNvidiaModelSelects(models: string[]) {
    const targetMap: Array<{ inputId: string; selectId: string }> = [
        { inputId: 'inputNvidiaModelSonnet', selectId: 'selectNvidiaModelSonnet' },
        { inputId: 'inputNvidiaModelOpus', selectId: 'selectNvidiaModelOpus' },
        { inputId: 'inputNvidiaModelHaiku', selectId: 'selectNvidiaModelHaiku' },
        { inputId: 'inputNvidiaModelFable', selectId: 'selectNvidiaModelFable' },
        { inputId: 'inputNvidiaModelDefault', selectId: 'selectNvidiaModelDefault' }
    ];

    targetMap.forEach(item => {
        const inputEl = document.getElementById(item.inputId) as HTMLInputElement | null;
        const selectEl = document.getElementById(item.selectId) as HTMLSelectElement | null;
        if (!inputEl || !selectEl) return;

        selectEl.innerHTML = '<option value="">选择模型...</option>';
        models.forEach(m => {
            const opt = document.createElement('option');
            opt.value = m;
            opt.textContent = m;
            selectEl.appendChild(opt);
        });

        selectEl.classList.remove('hidden');

        selectEl.onchange = () => {
            if (selectEl.value) {
                inputEl.value = selectEl.value;
            }
        };
    });
}

// ===== NVIDIA 全局专属模型清单 Modal 逻辑 =====

// ==================== Other 号池 Modal 控制逻辑 ====================
// openOtherAccountModal:打开 Other 账号录入模态并清空表单。
function openOtherAccountModal() {
    if (!otherAccountModal || !otherAccountModalContainer) return;

    // 添加态:清空编辑态标记。
    otherEditId = null;

    const inputGroupId = document.getElementById('inputOtherGroupId') as HTMLInputElement | null;
    const inputGroupName = document.getElementById('inputOtherGroupName') as HTMLInputElement | null;
    const inputBaseUrl = document.getElementById('inputOtherBaseUrl') as HTMLInputElement | null;
    const inputApiKey = document.getElementById('inputOtherApiKey') as HTMLInputElement | null;
    const inputLabel = document.getElementById('inputOtherLabel') as HTMLInputElement | null;
    const inputModelDefault = document.getElementById('inputOtherModelDefault') as HTMLInputElement | null;
    const selectModelDefault = document.getElementById('selectOtherModelDefault') as HTMLSelectElement | null;
    const chkFmtOpenai = document.getElementById('chkOtherFmtOpenai') as HTMLInputElement | null;
    const chkFmtAnthropic = document.getElementById('chkOtherFmtAnthropic') as HTMLInputElement | null;

    if (inputGroupId) inputGroupId.value = '';
    if (inputGroupName) inputGroupName.value = '';
    if (inputBaseUrl) inputBaseUrl.value = '';
    if (inputApiKey) inputApiKey.value = '';
    if (inputLabel) inputLabel.value = '';
    if (inputModelDefault) inputModelDefault.value = '';
    if (selectModelDefault) {
        selectModelDefault.classList.add('hidden');
        selectModelDefault.innerHTML = '<option value="">选择模型...</option>';
    }
    // 默认勾选 OpenAI 格式,与新建态初始 checked 一致。
    if (chkFmtOpenai) chkFmtOpenai.checked = true;
    if (chkFmtAnthropic) chkFmtAnthropic.checked = false;

    if (otherModalError) {
        otherModalError.classList.add('hidden');
        otherModalError.textContent = '';
    }

    // 号池已有组则填充右侧「选择已有组」下拉(0 组时隐藏)。
    populateOtherGroupSelect();

    otherAccountModal.classList.remove('opacity-0', 'pointer-events-none');
    otherAccountModalContainer.classList.remove('scale-95');
    otherAccountModalContainer.classList.add('scale-100');
}

function closeOtherAccountModal() {
    if (!otherAccountModal || !otherAccountModalContainer) return;
    otherAccountModalContainer.classList.remove('scale-100');
    otherAccountModalContainer.classList.add('scale-95');
    otherAccountModal.classList.add('opacity-0', 'pointer-events-none');
}

// 当前处于编辑态的 Other 账号 id(添加态为 null),供 submitOtherAccount 区分 add/update。
let otherEditId: string | null = null;

// openEditOtherAccount:预填 Other 账号模态框已有字段并切入编辑态。
// 展示名 = acc.email;API Key 因后端不下发明文,编辑框留空,留空表示保持不变。
export function openEditOtherAccount(acc: any) {
    if (!otherAccountModal || !otherAccountModalContainer) return;

    otherEditId = acc.id;

    const inputGroupId = document.getElementById('inputOtherGroupId') as HTMLInputElement | null;
    const inputGroupName = document.getElementById('inputOtherGroupName') as HTMLInputElement | null;
    const inputBaseUrl = document.getElementById('inputOtherBaseUrl') as HTMLInputElement | null;
    const inputApiKey = document.getElementById('inputOtherApiKey') as HTMLInputElement | null;
    const inputLabel = document.getElementById('inputOtherLabel') as HTMLInputElement | null;
    const inputModelDefault = document.getElementById('inputOtherModelDefault') as HTMLInputElement | null;
    const selectModelDefault = document.getElementById('selectOtherModelDefault') as HTMLSelectElement | null;
    const chkFmtOpenai = document.getElementById('chkOtherFmtOpenai') as HTMLInputElement | null;
    const chkFmtAnthropic = document.getElementById('chkOtherFmtAnthropic') as HTMLInputElement | null;

    if (inputGroupId) inputGroupId.value = acc.groupId || '';
    if (inputGroupName) inputGroupName.value = acc.groupName || '';
    if (inputBaseUrl) inputBaseUrl.value = acc.baseUrl || '';
    // 编辑态不预填明文 Key:留空表示保持不变;用脱敏掩码当 placeholder 提示已配置 Key。
    if (inputApiKey) {
        inputApiKey.value = '';
        inputApiKey.placeholder = acc.maskedKey || 'sk-... (留空保持不变)';
    }
    if (inputLabel) inputLabel.value = acc.email || '';
    if (inputModelDefault) inputModelDefault.value = acc.defaultModel || '';
    if (selectModelDefault) {
        selectModelDefault.classList.add('hidden');
        selectModelDefault.innerHTML = '<option value="">选择模型...</option>';
    }

    // 据账号 Formats 勾选协议 checkbox(openai 含则勾,否则取消;anthropic 同理)。
    const fmts: string[] = Array.isArray(acc.formats) ? acc.formats.map((f: any) => String(f).toLowerCase()) : [];
    if (chkFmtOpenai) chkFmtOpenai.checked = fmts.includes('openai');
    if (chkFmtAnthropic) chkFmtAnthropic.checked = fmts.includes('anthropic');
    // 若两者都未开(异常数据或无 formats),默认勾 OpenAI 兜底。
    if (!chkFmtOpenai?.checked && !chkFmtAnthropic?.checked && chkFmtOpenai) chkFmtOpenai.checked = true;

    if (otherModalError) {
        otherModalError.classList.add('hidden');
        otherModalError.textContent = '';
    }

    // 编辑态不展示「选择已有组」下拉(避免误改已选账号所属组身份)。
    if (groupSelectOther) groupSelectOther.classList.add('hidden');

    // 标题与保存按钮文案切入编辑态。
    const titleEl = otherAccountModal.querySelector('[data-i18n="otherAddModalTitle"]') as HTMLElement | null;
    if (titleEl) titleEl.textContent = '编辑 Other 号池账号';
    if (btnOtherModalSave) btnOtherModalSave.textContent = '保存修改';

    otherAccountModal.classList.remove('opacity-0', 'pointer-events-none');
    otherAccountModalContainer.classList.remove('scale-95');
    otherAccountModalContainer.classList.add('scale-100');
}

// submitOtherAccount:校验后调 other:add(JSON 对象入参),成功关闭模态并同步账号列表。
async function submitOtherAccount() {
    const inputGroupId = document.getElementById('inputOtherGroupId') as HTMLInputElement | null;
    const inputGroupName = document.getElementById('inputOtherGroupName') as HTMLInputElement | null;
    const inputBaseUrl = document.getElementById('inputOtherBaseUrl') as HTMLInputElement | null;
    const inputApiKey = document.getElementById('inputOtherApiKey') as HTMLInputElement | null;
    const inputLabel = document.getElementById('inputOtherLabel') as HTMLInputElement | null;
    const inputModelDefault = document.getElementById('inputOtherModelDefault') as HTMLInputElement | null;
    const chkFmtOpenai = document.getElementById('chkOtherFmtOpenai') as HTMLInputElement | null;
    const chkFmtAnthropic = document.getElementById('chkOtherFmtAnthropic') as HTMLInputElement | null;

    if (!inputGroupId || !inputBaseUrl || !inputApiKey) return;

    const groupId = inputGroupId.value.trim();
    const baseUrl = inputBaseUrl.value.trim();
    const apiKey = inputApiKey.value.trim();

    if (!groupId) {
        if (otherModalError) {
            otherModalError.textContent = '请填写组 ID';
            otherModalError.classList.remove('hidden');
        }
        return;
    }
    if (!apiKey && !otherEditId) {
        if (otherModalError) {
            otherModalError.textContent = '请输入 API Key';
            otherModalError.classList.remove('hidden');
        }
        return;
    }

    const formats: string[] = [];
    if (chkFmtOpenai && chkFmtOpenai.checked) formats.push('openai');
    if (chkFmtAnthropic && chkFmtAnthropic.checked) formats.push('anthropic');
    if (formats.length === 0) {
        if (otherModalError) {
            otherModalError.textContent = '至少勾选一种协议格式';
            otherModalError.classList.remove('hidden');
        }
        return;
    }

    try {
        if (btnOtherModalSave) {
            btnOtherModalSave.disabled = true;
            btnOtherModalSave.textContent = otherEditId ? '正在保存...' : '正在添加...';
        }

        let res;
        const payloadObj: any = {
            groupId,
            groupName: inputGroupName?.value.trim() || '',
            baseUrl,
            apiKey,
            formats,
            label: inputLabel?.value.trim() || '',
            defaultModel: inputModelDefault?.value.trim() || ''
        };
        if (otherEditId) {
            // 编辑态:以 accountId 定位既有账号;apiKey 留空保持不变。
            payloadObj.accountId = otherEditId;
            res = await ipcRenderer.invoke('other:update', JSON.stringify(payloadObj));
        } else {
            // 采用单对象 JSON 入参形态(后端 parseOtherInputFromArgs 优先按 JSON 解析)。
            res = await ipcRenderer.invoke('other:add', JSON.stringify(payloadObj));
        }

        if (res && res.success) {
            closeOtherAccountModal();
            // accounts-res 由后端 emitAccountsRes 主动广播,前端 global listener 会自动 renderAccounts,
            // 此处不再手动 invoke('accounts:get') 以免与广播竞态重复刷新。
        } else {
            if (otherModalError) {
                otherModalError.textContent = res?.error || (otherEditId ? '保存失败' : '添加失败');
                otherModalError.classList.remove('hidden');
            }
        }
    } catch (err: any) {
        if (otherModalError) {
            otherModalError.textContent = err.message || '系统错误';
            otherModalError.classList.remove('hidden');
        }
    } finally {
        if (btnOtherModalSave) {
            btnOtherModalSave.disabled = false;
            btnOtherModalSave.textContent = otherEditId ? '保存修改' : '添加账号';
        }
    }
}

// fetchOtherModels:调其他组拉模型(透传当前录入框内的 groupId/baseUrl/apiKey 以支持未入库预拉),
// 填入默认模型下拉供选择。
async function fetchOtherModels() {
    const inputGroupId = document.getElementById('inputOtherGroupId') as HTMLInputElement | null;
    const inputBaseUrl = document.getElementById('inputOtherBaseUrl') as HTMLInputElement | null;
    const inputApiKey = document.getElementById('inputOtherApiKey') as HTMLInputElement | null;
    const selectModelDefault = document.getElementById('selectOtherModelDefault') as HTMLSelectElement | null;
    if (!btnOtherFetchModels) return;

    const groupId = inputGroupId ? inputGroupId.value.trim() : '';
    const baseUrl = inputBaseUrl ? inputBaseUrl.value.trim() : '';
    const apiKey = inputApiKey ? inputApiKey.value.trim() : '';

    if (otherModalError) {
        otherModalError.classList.add('hidden');
        otherModalError.textContent = '';
    }

    if (!groupId && !baseUrl) {
        if (otherModalError) {
            otherModalError.textContent = '请先填写组 ID 或 Base URL';
            otherModalError.classList.remove('hidden');
        }
        return;
    }

    const origHTML = btnOtherFetchModels.innerHTML;
    btnOtherFetchModels.disabled = true;
    btnOtherFetchModels.innerHTML = `<span class="material-symbols-outlined text-[14px] animate-spin">refresh</span><span>获取中...</span>`;

    try {
        // 透传 baseUrl/apiKey,便于未入库时按当前表单预拉;后端会优先用透传值,否则查号池该组首个账号。
        const res = await ipcRenderer.invoke('other:fetch-models', JSON.stringify({ groupId, baseUrl, apiKey }));
        if (res && res.success && Array.isArray(res.models)) {
            if (selectModelDefault) {
                selectModelDefault.innerHTML = '<option value="">选择模型...</option>';
                res.models.forEach((m: string) => {
                    const opt = document.createElement('option');
                    opt.value = m;
                    opt.textContent = m;
                    selectModelDefault.appendChild(opt);
                });
                selectModelDefault.classList.remove('hidden');
                selectModelDefault.onchange = () => {
                    if (selectModelDefault.value) {
                        const inputModelDefault = document.getElementById('inputOtherModelDefault') as HTMLInputElement | null;
                        if (inputModelDefault) inputModelDefault.value = selectModelDefault.value;
                    }
                };
            }
        } else {
            // Anthropic-only 上游无 /v1/models 端点会返回 allowManualInput,提示用户手填。
            if (res && res.allowManualInput) {
                if (otherModalError) {
                    otherModalError.textContent = res?.error || '上游暂不支持模型列表,请手动填写模型名';
                    otherModalError.classList.remove('hidden');
                }
                if (selectModelDefault) selectModelDefault.classList.add('hidden');
            } else if (otherModalError) {
                otherModalError.textContent = res?.error || '获取模型列表失败';
                otherModalError.classList.remove('hidden');
            }
        }
    } catch (err: any) {
        if (otherModalError) {
            otherModalError.textContent = err.message || '网络或接口请求出错';
            otherModalError.classList.remove('hidden');
        }
    } finally {
        if (btnOtherFetchModels) {
            btnOtherFetchModels.disabled = false;
            btnOtherFetchModels.innerHTML = origHTML;
        }
    }
}

// 实时把模块级缓存的"已保存计数"刷到入口徽标 + Modal 顶部计数
function updateNvidiaPreferredBadge(count: number): void {
    if (badgeNvidiaPreferredCount) badgeNvidiaPreferredCount.textContent = String(count);
    if (lblNvidiaPreferredCount) lblNvidiaPreferredCount.textContent = String(count);
}

function showNvidiaPreferredError(msg: string | null): void {
    if (!nvidiaPreferredError) return;
    if (msg) {
        nvidiaPreferredError.textContent = msg;
        nvidiaPreferredError.classList.remove('hidden');
    } else {
        nvidiaPreferredError.textContent = '';
        nvidiaPreferredError.classList.add('hidden');
    }
}

// 收集某列当前勾选行的 model id(保持 DOM 顺序)
function collectNvidiaPreferredCheckedIn(list: HTMLDivElement | null): string[] {
    if (!list) return [];
    const checks = list.querySelectorAll<HTMLInputElement>('input[data-model-id]:checked');
    return Array.from(checks).map(chk => chk.dataset.modelId || '').filter(Boolean);
}

// 收集左列全部行(已选清单=保存时落库的对象),不依赖勾选状态
function collectNvidiaPreferredLeftIDs(): string[] {
    if (!nvidiaPreferredModelsListLeft) return nvidiaPreferredLeftIDs.slice();
    const rows = nvidiaPreferredModelsListLeft.querySelectorAll<HTMLLabelElement>('label[data-model-id]');
    return Array.from(rows).map(r => r.dataset.modelId || '').filter(Boolean);
}

function openNvidiaPreferredModelsModal(): void {
    if (!nvidiaPreferredModal || !nvidiaPreferredModalContainer) return;
    // 每次打开重置搜索/全选/错误
    if (inputNvidiaPreferredSearchLeft) inputNvidiaPreferredSearchLeft.value = '';
    if (inputNvidiaPreferredSearchRight) inputNvidiaPreferredSearchRight.value = '';
    if (chkNvidiaPreferredSelectAllLeft) chkNvidiaPreferredSelectAllLeft.checked = false;
    if (chkNvidiaPreferredSelectAllRight) chkNvidiaPreferredSelectAllRight.checked = false;
    showNvidiaPreferredError(null);
    if (lblNvidiaPreferredSource) {
        lblNvidiaPreferredSource.textContent = '';
        lblNvidiaPreferredSource.classList.add('hidden');
    }
    // 默认来源=本地清单(空则后端自动回退远端)
    nvidiaPreferredCurrentSource = 'local';
    applySourceHighlightNvidiaPreferred();
    void fetchAndRenderNvidiaPreferredModels(true, undefined);
    nvidiaPreferredModal.classList.remove('opacity-0', 'pointer-events-none');
    nvidiaPreferredModalContainer.classList.remove('scale-95');
    nvidiaPreferredModalContainer.classList.add('scale-100');
}

function closeNvidiaPreferredModelsModal(): void {
    if (!nvidiaPreferredModal || !nvidiaPreferredModalContainer) return;
    nvidiaPreferredModalContainer.classList.remove('scale-100');
    nvidiaPreferredModalContainer.classList.add('scale-95');
    nvidiaPreferredModal.classList.add('opacity-0', 'pointer-events-none');
}

// 来源切换:local=本地清单(空则后端自动回退远端);remote=强制跳过 cache 打上游
function switchNvidiaPreferredSource(src: 'local' | 'remote'): void {
    if (src === nvidiaPreferredCurrentSource) return;
    nvidiaPreferredCurrentSource = src;
    applySourceHighlightNvidiaPreferred();
    void fetchAndRenderNvidiaPreferredModels(false, src === 'remote' ? 'remote' : undefined);
}

function applySourceHighlightNvidiaPreferred(): void {
    const activeCls = ['bg-primary', 'text-white'];
    const idleCls = ['text-primary', 'bg-primary/10', 'hover:bg-primary/20'];
    const setActive = (btn: HTMLButtonElement | null, active: boolean) => {
        if (!btn) return;
        if (active) { btn.classList.add(...activeCls); btn.classList.remove(...idleCls); }
        else { btn.classList.remove(...activeCls); btn.classList.add(...idleCls); }
    };
    setActive(btnNvidiaPreferredSrcLocal, nvidiaPreferredCurrentSource === 'local');
    setActive(btnNvidiaPreferredSrcRemote, nvidiaPreferredCurrentSource === 'remote');
}

// isOpening=true:首次进入 Modal,读本地清单(空则后端自动回退远端),作为左列已选回显
// force='remote':强制跳过 cache 打上游,结果进右列候选(左列已选不重置)
// force=undefined / 'local':cache 优先,空则回退远端;cache 命中→左列回显、右列保持现状
async function fetchAndRenderNvidiaPreferredModels(isOpening: boolean, force?: 'remote'): Promise<void> {
    if (!btnNvidiaPreferredFetch) return;
    showNvidiaPreferredError(null);

    const origIcon = iconNvidiaPreferredFetch ? iconNvidiaPreferredFetch.textContent : '';
    const origDisabled = btnNvidiaPreferredFetch.disabled;
    btnNvidiaPreferredFetch.disabled = true;
    if (iconNvidiaPreferredFetch) iconNvidiaPreferredFetch.textContent = 'progress_activity';
    if (iconNvidiaPreferredFetch) iconNvidiaPreferredFetch.classList.add('animate-spin');

    try {
        const res = await ipcRenderer.invoke('settings:get-nvidia-preferred-models', force ? { force } : {});
        if (!res || !res.success) {
            const errMsg = (res && res.error) ? res.error : '获取模型失败';
            showNvidiaPreferredError(errMsg);
            return;
        }
        const models: string[] = Array.isArray(res.models) ? res.models : [];
        const source: string = res.source || (force === 'remote' ? 'remote' : 'cache');

        if (lblNvidiaPreferredSource) {
            const dl = (window as any).__nvidiaPreferredDict || {};
            lblNvidiaPreferredSource.textContent = source === 'cache'
                ? (dl.nvidiaPreferredModelsSourceCache || '来源:已保存清单')
                : (dl.nvidiaPreferredModelsSourceRemote || '来源:远端实时');
            lblNvidiaPreferredSource.classList.remove('hidden');
        }

        if (source === 'cache') {
            // 本地清单命中:左列=清单本身(已选回显),右列保持现状(没拉上游)
            nvidiaPreferredLeftIDs = models.slice();
            renderNvidiaPreferredListLeft(nvidiaPreferredLeftIDs);
            renderNvidiaPreferredListRight(nvidiaPreferredRightIDs);
            // 本地清单命中即把"已保存计数"刷到入口徽标 + Modal 顶部计数
            // 避免长时间停留在 HTML 写死的初值 0、与磁盘里实际保存的清单不同步
            updateNvidiaPreferredBadge(models.length);
        } else {
            // 远端全量候选:进右列,剔除已在左列的 id;左列已选不重置
            nvidiaPreferredRightIDs = models.filter(m => !nvidiaPreferredLeftIDs.includes(m));
            renderNvidiaPreferredListRight(nvidiaPreferredRightIDs);
            renderNvidiaPreferredListLeft(nvidiaPreferredLeftIDs);
        }
    } catch (err: any) {
        showNvidiaPreferredError(err?.message || '获取模型失败');
    } finally {
        btnNvidiaPreferredFetch.disabled = origDisabled;
        if (iconNvidiaPreferredFetch) {
            iconNvidiaPreferredFetch.textContent = origIcon || 'cloud_download';
            iconNvidiaPreferredFetch.classList.remove('animate-spin');
        }
    }
}

// 渲染左列(已选清单);checkedSet 为空=不勾选(左列勾选仅供"移出"用,默认不勾)
function renderNvidiaPreferredListLeft(models: string[]): void {
    if (!nvidiaPreferredModelsListLeft) return;
    nvidiaPreferredModelsListLeft.innerHTML = '';
    if (models.length === 0) {
        if (nvidiaPreferredEmptyLeft) nvidiaPreferredEmptyLeft.classList.remove('hidden');
        if (lblNvidiaPreferredVisibleLeft) lblNvidiaPreferredVisibleLeft.textContent = '0 / 0';
        if (chkNvidiaPreferredSelectAllLeft) chkNvidiaPreferredSelectAllLeft.checked = false;
        return;
    }
    if (nvidiaPreferredEmptyLeft) nvidiaPreferredEmptyLeft.classList.add('hidden');
    for (const m of models) {
        nvidiaPreferredModelsListLeft.appendChild(buildNvidiaPreferredRow(m, false));
    }
    applyNvidiaPreferredSearchLeft();
    syncNvidiaPreferredSelectAllLeft();
}

// 渲染右列(上游候选),checkedSet 为空=不勾选
function renderNvidiaPreferredListRight(models: string[]): void {
    if (!nvidiaPreferredModelsListRight) return;
    nvidiaPreferredModelsListRight.innerHTML = '';
    if (models.length === 0) {
        if (nvidiaPreferredEmptyRight) nvidiaPreferredEmptyRight.classList.remove('hidden');
        if (lblNvidiaPreferredVisibleRight) lblNvidiaPreferredVisibleRight.textContent = '0 / 0';
        if (chkNvidiaPreferredSelectAllRight) chkNvidiaPreferredSelectAllRight.checked = false;
        return;
    }
    if (nvidiaPreferredEmptyRight) nvidiaPreferredEmptyRight.classList.add('hidden');
    for (const m of models) {
        nvidiaPreferredModelsListRight.appendChild(buildNvidiaPreferredRow(m, false));
    }
    applyNvidiaPreferredSearchRight();
    syncNvidiaPreferredSelectAllRight();
}

// 构造一行:label>input[data-model-id]+span.mono;change 事件同步该列全选框
function buildNvidiaPreferredRow(modelId: string, checked: boolean): HTMLLabelElement {
    const row = document.createElement('label');
    row.dataset.modelId = modelId;
    row.className = 'flex items-center gap-2 px-3 py-1.5 cursor-pointer hover:bg-slate-50/60 dark:hover:bg-white/5 transition-colors select-none';

    const chk = document.createElement('input');
    chk.type = 'checkbox';
    chk.dataset.modelId = modelId;
    chk.className = 'w-3.5 h-3.5 rounded border-outline-variant/40 dark:border-white/20 text-primary focus:ring-primary cursor-pointer shrink-0';
    chk.checked = checked;

    const span = document.createElement('span');
    span.className = 'flex-1 min-w-0 text-[12px] font-mono text-on-surface dark:text-white truncate';
    span.textContent = modelId;
    span.title = modelId;

    row.appendChild(chk);
    row.appendChild(span);
    return row;
}

// 移入已选:右列勾选项 → 左列(保留左列已有顺序,新项追加),从右列移除
function moveNvidiaPreferredSelectedToLeft(): void {
    if (!nvidiaPreferredModelsListRight) return;
    const chosen = collectNvidiaPreferredCheckedIn(nvidiaPreferredModelsListRight);
    if (chosen.length === 0) return;
    for (const id of chosen) {
        if (!nvidiaPreferredLeftIDs.includes(id)) nvidiaPreferredLeftIDs.push(id);
        nvidiaPreferredRightIDs = nvidiaPreferredRightIDs.filter(x => x !== id);
    }
    renderNvidiaPreferredListLeft(nvidiaPreferredLeftIDs);
    renderNvidiaPreferredListRight(nvidiaPreferredRightIDs);
}

// 移出已选:左列勾选项 → 右列,从左列移除
function moveNvidiaPreferredSelectedToRight(): void {
    if (!nvidiaPreferredModelsListLeft) return;
    const chosen = collectNvidiaPreferredCheckedIn(nvidiaPreferredModelsListLeft);
    if (chosen.length === 0) return;
    for (const id of chosen) {
        if (!nvidiaPreferredRightIDs.includes(id)) nvidiaPreferredRightIDs.push(id);
        nvidiaPreferredLeftIDs = nvidiaPreferredLeftIDs.filter(x => x !== id);
    }
    renderNvidiaPreferredListLeft(nvidiaPreferredLeftIDs);
    renderNvidiaPreferredListRight(nvidiaPreferredRightIDs);
}

function moveAllNvidiaPreferredToLeft(): void {
    if (nvidiaPreferredRightIDs.length === 0) return;
    for (const id of nvidiaPreferredRightIDs) {
        if (!nvidiaPreferredLeftIDs.includes(id)) nvidiaPreferredLeftIDs.push(id);
    }
    nvidiaPreferredRightIDs = [];
    renderNvidiaPreferredListLeft(nvidiaPreferredLeftIDs);
    renderNvidiaPreferredListRight(nvidiaPreferredRightIDs);
}

function moveAllNvidiaPreferredToRight(): void {
    if (nvidiaPreferredLeftIDs.length === 0) return;
    for (const id of nvidiaPreferredLeftIDs) {
        if (!nvidiaPreferredRightIDs.includes(id)) nvidiaPreferredRightIDs.push(id);
    }
    nvidiaPreferredLeftIDs = [];
    renderNvidiaPreferredListLeft(nvidiaPreferredLeftIDs);
    renderNvidiaPreferredListRight(nvidiaPreferredRightIDs);
}

// 左列搜索过滤
function applyNvidiaPreferredSearchLeft(): void {
    applyNvidiaPreferredSearchOne(
        nvidiaPreferredModelsListLeft,
        inputNvidiaPreferredSearchLeft,
        lblNvidiaPreferredVisibleLeft,
        chkNvidiaPreferredSelectAllLeft,
    );
}

// 右列搜索过滤
function applyNvidiaPreferredSearchRight(): void {
    applyNvidiaPreferredSearchOne(
        nvidiaPreferredModelsListRight,
        inputNvidiaPreferredSearchRight,
        lblNvidiaPreferredVisibleRight,
        chkNvidiaPreferredSelectAllRight,
    );
}

// 单列搜索过滤:隐藏不匹配行,更新可见计数与全选框状态(基于可见行)
function applyNvidiaPreferredSearchOne(
    list: HTMLDivElement | null,
    input: HTMLInputElement | null,
    visibleLbl: HTMLSpanElement | null,
    selectAllChk: HTMLInputElement | null,
): void {
    if (!list) return;
    const kw = (input?.value || '').trim().toLowerCase();
    const rows = list.querySelectorAll<HTMLLabelElement>('label');
    let visible = 0;
    let visibleChecked = 0;
    rows.forEach(row => {
        const chk = row.querySelector<HTMLInputElement>('input[type="checkbox"]');
        const id = row.dataset.modelId || '';
        const matched = !kw || id.toLowerCase().includes(kw);
        row.classList.toggle('hidden', !matched);
        if (matched) {
            visible++;
            if (chk?.checked) visibleChecked++;
        }
    });
    if (visibleLbl) visibleLbl.textContent = `${visible} / ${rows.length}`;
    if (selectAllChk) {
        selectAllChk.checked = visible > 0 && (visibleChecked === visible);
    }
}

// 左列全选框同步:基于可见行的勾选状态反向更新全选框
function syncNvidiaPreferredSelectAllLeft(): void {
    syncNvidiaPreferredSelectAllOne(nvidiaPreferredModelsListLeft, chkNvidiaPreferredSelectAllLeft, lblNvidiaPreferredVisibleLeft, inputNvidiaPreferredSearchLeft);
}

function syncNvidiaPreferredSelectAllRight(): void {
    syncNvidiaPreferredSelectAllOne(nvidiaPreferredModelsListRight, chkNvidiaPreferredSelectAllRight, lblNvidiaPreferredVisibleRight, inputNvidiaPreferredSearchRight);
}

function syncNvidiaPreferredSelectAllOne(
    list: HTMLDivElement | null,
    selectAllChk: HTMLInputElement | null,
    visibleLbl: HTMLSpanElement | null,
    input: HTMLInputElement | null,
): void {
    if (!list || !selectAllChk) return;
    const kw = (input?.value || '').trim().toLowerCase();
    const rows = list.querySelectorAll<HTMLLabelElement>('label:not(.hidden)');
    if (rows.length === 0) {
        selectAllChk.checked = false;
    } else {
        let checkedCount = 0;
        rows.forEach(row => {
            const chk = row.querySelector<HTMLInputElement>('input[type="checkbox"]');
            if (chk?.checked) checkedCount++;
        });
        selectAllChk.checked = (checkedCount === rows.length);
    }
    if (visibleLbl) {
        const all = list.querySelectorAll<HTMLLabelElement>('label').length;
        let visible = 0;
        list.querySelectorAll<HTMLLabelElement>('label').forEach(row => {
            const id = row.dataset.modelId || '';
            if (!kw || id.toLowerCase().includes(kw)) visible++;
        });
        visibleLbl.textContent = `${visible} / ${all}`;
    }
}

// 左列全选/取消:仅作用于当前可见行
function toggleNvidiaPreferredSelectAllLeft(): void {
    if (!nvidiaPreferredModelsListLeft || !chkNvidiaPreferredSelectAllLeft) return;
    const target = chkNvidiaPreferredSelectAllLeft.checked;
    toggleVisibleRowsNvidiaPreferred(nvidiaPreferredModelsListLeft, target);
    if (inputNvidiaPreferredSearchLeft) applyNvidiaPreferredSearchLeft();
}

function toggleNvidiaPreferredSelectAllRight(): void {
    if (!nvidiaPreferredModelsListRight || !chkNvidiaPreferredSelectAllRight) return;
    const target = chkNvidiaPreferredSelectAllRight.checked;
    toggleVisibleRowsNvidiaPreferred(nvidiaPreferredModelsListRight, target);
    if (inputNvidiaPreferredSearchRight) applyNvidiaPreferredSearchRight();
}

function toggleVisibleRowsNvidiaPreferred(list: HTMLDivElement, targetChecked: boolean): void {
    list.querySelectorAll<HTMLLabelElement>('label:not(.hidden)').forEach(row => {
        const chk = row.querySelector<HTMLInputElement>('input[type="checkbox"]');
        if (chk) chk.checked = targetChecked;
    });
}

// 保存清单:收集左列全部行 → settings:set-nvidia-preferred-models → 关闭
function submitNvidiaPreferredModels(): void {
    if (!btnNvidiaPreferredSave) return;
    const selected = collectNvidiaPreferredLeftIDs();
    showNvidiaPreferredError(null);
    btnNvidiaPreferredSave.disabled = true;
    try {
        ipcRenderer.send('settings:set-nvidia-preferred-models', selected);
        updateNvidiaPreferredBadge(selected.length);
        // 后端会 emit settings:nvidia-preferred-models-res 事件刷新徽标;乐观关闭 Modal
        closeNvidiaPreferredModelsModal();
    } finally {
        btnNvidiaPreferredSave.disabled = false;
    }
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
