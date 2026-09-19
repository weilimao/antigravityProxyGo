/**
 * 账号配额刷新控制逻辑：从 accountsController.ts 抽离的独立模块。
 *
 * 高内聚：承担「逐卡片刷新全部配额(refreshAllQuotas) / 聚合额度条刷新(refreshAllAccountsQuotas) /
 * 后台静默刷新(refreshAllAccountsQuotasSilently)」三类配额刷新动作,以及「清空会话」按钮的点击反馈;
 * 自带 6 个 DOM handle、1 个首次加载标志(isInitialQuotaLoaded)、3 个刷新函数 + 1 个清空反馈内联块。
 * initQuotaRenderEvents() 由 accountsController.initAccountsEvents 委托,完成句柄赋值与
 * btnRefreshAllQuota/btnClearSessions 绑定;initQuotaRenderGlobalEvents() 由
 * initAccountsGlobalEvents 委托,完成聚合刷新按钮 btnRefreshAggregateQuota 的赋值与绑定。
 * tryInitialSilentQuotaRefresh(accounts) 封装「首次拉到账号列表即静默刷新一次」的 do-once 语义,
 * 由 accounts-res 监听器委托调用,把 isInitialQuotaLoaded 状态私藏在本模块内。
 * 依赖：ipcRenderer、state、accountsRenderer.updateAggregateQuotaUI / renderAccounts(只调不改)。
 */
import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';
import { updateAggregateQuotaUI, renderAccounts } from './accountsRenderer';

let accountsList: HTMLDivElement | null;
let btnRefreshAllQuota: HTMLButtonElement | null;
let btnRefreshAllIcon: HTMLElement | null;
let btnClearSessions: HTMLButtonElement | null;
let btnRefreshAggregateQuota: HTMLButtonElement | null;
let btnRefreshAggregateIcon: HTMLElement | null;

// 句柄赋值 + 事件绑定（由 accountsController.initAccountsEvents 委托调用）
export function initQuotaRenderEvents(): void {
    accountsList = document.getElementById('accountsList') as HTMLDivElement | null;
    btnRefreshAllQuota = document.getElementById('btnRefreshAllQuota') as HTMLButtonElement | null;
    btnRefreshAllIcon = document.getElementById('btnRefreshAllIcon');
    btnClearSessions = document.getElementById('btnClearSessions') as HTMLButtonElement | null;

    if (btnRefreshAllQuota) {
        btnRefreshAllQuota.addEventListener('click', refreshAllQuotas);
    }

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
            // 自动过滤并跳过已停用的灰色账号及 NVIDIA/Grok/WorkBuddy/OpenCode 账号
            // (nvidia/grok/workbuddy/opencode 不走此异步聚合循环,其配额逐卡刷新走 renderAccounts→loadAccountQuota)。
            if (!acc.enabled || acc.provider === 'nvidia' || acc.provider === 'grok' || acc.provider === 'workbuddy' || acc.provider === 'opencode') {
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

let isInitialQuotaLoaded = false;

export async function refreshAllAccountsQuotasSilently() {
    if (!state.currentAccountsList || state.currentAccountsList.length === 0) return;
    try {
        for (const acc of state.currentAccountsList) {
            if (!acc.enabled || acc.provider === 'nvidia' || acc.provider === 'grok' || acc.provider === 'workbuddy' || acc.provider === 'opencode') {
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

// tryInitialSilentQuotaRefresh:accounts-res 监听器收到首份账号列表时静默刷新一次配额。
// do-once 语义由内部 isInitialQuotaLoaded 私藏,controller 不再直接持有该标志。
export function tryInitialSilentQuotaRefresh(accounts?: any[]): void {
    if (!isInitialQuotaLoaded && accounts && accounts.length > 0) {
        isInitialQuotaLoaded = true;
        refreshAllAccountsQuotasSilently();
    }
}

// initQuotaRenderGlobalEvents:全局事件注册期(由 accountsController.initAccountsGlobalEvents 委托),
// 注册聚合刷新按钮 btnRefreshAggregateQuota 的句柄赋值 + click 绑定(时机同原 inline 位置)。
export function initQuotaRenderGlobalEvents(): void {
    btnRefreshAggregateQuota = document.getElementById('btnRefreshAggregateQuota') as HTMLButtonElement | null;
    btnRefreshAggregateIcon = document.getElementById('btnRefreshAggregateIcon');
    if (btnRefreshAggregateQuota) {
        btnRefreshAggregateQuota.addEventListener('click', refreshAllAccountsQuotas);
    }
}
