/**
 * Other 号池「组名子 Tab + 组内负载均衡方式下拉」控制逻辑：从 accountsController.ts 抽离的独立模块。
 *
 * 高内聚：renderOtherGroupTabs 按 state.lastBackendData.otherGroups 渲染其他通道下方的组名子 Tab,
 * 点击组 Tab 设 state.otherGroupFilter 并触发 renderAccounts 重新过滤；syncOtherGroupsFromBackend
 * 修复「切回 Other 池时缓冲里 otherGroups 为空 → 二级组名 Tab 整行消失且不恢复」的竞态；
 * renderOtherLBMode 在工具栏渲染当前所选组对应的 LB 模式下拉并回填该组并发上限；
 * otherLBModeSelectorVisible 暴露给 controller 判断 LB 下拉是否当前可见(选中具体 Other 组时)。
 * 自带 4 个 DOM handle(otherGroupTabs / otherLBModeContainer / otherLBModeSelect / otherMaxConcurrency)、
 * 1 个 in-flight 标志、4 个函数,由 accountsController.initAccountsEvents 委托
 * initOtherGroupTabsEvents() 完成句柄赋值与 LB 模式下 change/click 事件绑定。
 * 依赖：ipcRenderer、state、i18n、accountsRenderer.renderAccounts(只调不改)。
 */
import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';
import i18n from '../shared/i18n';
import { renderAccounts } from './accountsRenderer';

let otherGroupTabs: HTMLDivElement | null;
let otherLBModeContainer: HTMLDivElement | null;
let otherLBModeSelect: HTMLSelectElement | null;
let otherMaxConcurrency: HTMLInputElement | null;

// 句柄赋值 + 事件绑定（由 accountsController.initAccountsEvents 委托调用）
export function initOtherGroupTabsEvents(): void {
    otherGroupTabs = document.getElementById('otherGroupTabs') as HTMLDivElement | null;
    otherLBModeContainer = document.getElementById('otherLBModeContainer') as HTMLDivElement | null;
    otherLBModeSelect = document.getElementById('otherLBModeSelect') as HTMLSelectElement | null;
    otherMaxConcurrency = document.getElementById('otherMaxConcurrency') as HTMLInputElement | null;

    // 单账号在途并发上限:Other 池按组配置(仅选中具体组时发送,「全部组」下拉不可见,不发送)。
    if (otherMaxConcurrency) {
        let otherDebounce: ReturnType<typeof setTimeout> | null = null;
        otherMaxConcurrency.addEventListener('change', (e: any) => {
            if (!otherLBModeSelectorVisible()) return;
            const v = Math.max(0, Math.min(1000, Number(e.target.value) || 0));
            e.target.value = String(v);
            if (otherDebounce) clearTimeout(otherDebounce);
            otherDebounce = setTimeout(() => {
                ipcRenderer.send('other:set-max-concurrency', state.otherGroupFilter, v);
            }, 300);
        });
        otherMaxConcurrency.addEventListener('click', (e) => e.stopPropagation());
    }

    // Other 号池组内负载均衡方式选择框:作用于当前选中组(「全部组」下拉不可见,不发送)。
    if (otherLBModeSelect) {
        otherLBModeSelect.addEventListener('change', (e: any) => {
            if (!otherLBModeSelectorVisible()) return;
            ipcRenderer.send('other:set-lb-mode', state.otherGroupFilter, e.target.value);
        });
        otherLBModeSelect.addEventListener('click', (e) => e.stopPropagation());
    }
}

// ==================== Other 号池二级组名子 Tab + Modal 组自动填充 ====================
// renderOtherGroupTabs:按 state.lastBackendData.otherGroups 渲染 Other 通道下方的组名子 Tab。
// 0 组时整行隐藏。点击组 Tab 设 state.otherGroupFilter 并触发 renderAccounts 重新过滤。
// 竞态修复:缓冲 lastBackendData.otherGroups 缺失/为空时(切 Tab 不主动拉、广播未达即可触发),
// 回退主动调 other:list-groups 拉取真实组列表并写回缓冲后重渲染,避免在其他池切回时整行消失且不恢复。
let otherGroupTabsSyncInFlight = false;
export function renderOtherGroupTabs() {
    if (!otherGroupTabs) {
        otherGroupTabs = document.getElementById('otherGroupTabs') as HTMLDivElement | null;
    }
    if (!otherGroupTabs) return;

    const groups = (state.lastBackendData && Array.isArray(state.lastBackendData.otherGroups))
        ? state.lastBackendData.otherGroups
        : [];
    // 仅 other 通道显示;0 组时整行隐藏。
    if (state.currentViewTab !== 'other') {
        otherGroupTabs.classList.add('hidden');
        otherGroupTabs.classList.remove('flex');
        otherGroupTabs.innerHTML = '';
        // 切走 other 通道时回到「全部组」默认过滤,避免残留上一个组过滤。
        state.otherGroupFilter = 'ALL';
        return;
    }

    // 竞态兜底:当前处于 other 通道但缓冲里没有组列表(广播未达/被薄载荷覆盖),主动向后端拉一次。
    // 拉取成功即写回缓冲并重绘;确认后端确实 0 组才隐藏。用 in-flight 标志防并发重复请求。
    if (groups.length === 0 && !otherGroupTabsSyncInFlight) {
        otherGroupTabsSyncInFlight = true;
        const tabEl = otherGroupTabs;
        syncOtherGroupsFromBackend().then(reloaded => {
            otherGroupTabsSyncInFlight = false;
            if (reloaded) {
                // 已写回 lastBackendData.otherGroups 并重渲染,无需再走隐藏分支。
                return;
            }
            // 后端确认 0 组(或拉取失败):保留隐藏态,避免空 Tab 行占位。
            const curGroups = (state.lastBackendData && Array.isArray(state.lastBackendData.otherGroups))
                ? state.lastBackendData.otherGroups
                : [];
            if (curGroups.length === 0) {
                tabEl.classList.add('hidden');
                tabEl.classList.remove('flex');
                tabEl.innerHTML = '';
                state.otherGroupFilter = 'ALL';
                renderOtherLBMode();
            }
        });
        return;
    }

    if (groups.length === 0) {
        otherGroupTabs.classList.add('hidden');
        otherGroupTabs.classList.remove('flex');
        otherGroupTabs.innerHTML = '';
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

// syncOtherGroupsFromBackend:主动调 other:list-groups 拉取真实 Other 组列表,写回
// state.lastBackendData.otherGroups 后重渲染 renderOtherGroupTabs。返回 true 表示已成功写回并重绘。
// 用于修复「切回 Other 池时缓冲里 otherGroups 为空 → 二级组名 Tab 整行消失且不恢复」的竞态。
async function syncOtherGroupsFromBackend(): Promise<boolean> {
    try {
        const res = await ipcRenderer.invoke('other:list-groups');
        const list = (res && res.success && Array.isArray(res.groups)) ? res.groups : [];
        if (!state.lastBackendData) {
            state.lastBackendData = {};
        }
        // 写回缓冲,让后续 renderOtherGroupTabs / renderOtherLBMode / renderOtherGroupFetchButtons 复用。
        state.lastBackendData.otherGroups = list;
        if (list.length > 0) {
            renderOtherGroupTabs();
            return true;
        }
        return false;
    } catch (e) {
        console.error('[renderOtherGroupTabs] syncOtherGroupsFromBackend failed:', e);
        return false;
    }
}

// renderOtherLBMode:在工具栏(otherLBModeContainer)渲染当前 Other 组过滤对应的负载均衡方式下拉。
// 仅 Other 通道、存在组、且选中了具体组时显示;「全部组」无单一 LB 模式可配置,隐藏。
export function renderOtherLBMode() {
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

    // 同步回填该组单账号并发上限(未配置为 0,前端 ?? 10 兜底显示)。
    if (!otherMaxConcurrency) {
        otherMaxConcurrency = document.getElementById('otherMaxConcurrency') as HTMLInputElement | null;
    }
    if (otherMaxConcurrency && g) {
        otherMaxConcurrency.value = String((g as any).maxConcurrency ?? 10);
    }
}

// otherLBModeSelectorVisible:工具栏 LB 模式下拉是否可见(选中具体 Other 组时)。
export function otherLBModeSelectorVisible(): boolean {
    return state.currentViewTab === 'other'
        && !!state.otherGroupFilter
        && state.otherGroupFilter !== 'ALL';
}
