import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';
import { showOneStopAuthModal } from './accountsAuthModal';
import { 
    initRendererElements, 
    renderAccounts, 
    updateAggregateQuotaUI, 
    loadAccountQuota 
} from './accountsRenderer';

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
let btnExportAccounts: HTMLButtonElement | null;
let btnImportAccounts: HTMLButtonElement | null;

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

export function updateViewTabUI() {
    if (btnChannelAntigravity && btnChannelProject) {
        const activeClass = 'px-4 py-1.5 rounded-md font-bold cursor-pointer transition-all duration-200 bg-white dark:bg-[#1a1f30] text-primary dark:text-primary-fixed-dim shadow-sm';
        const inactiveClass = 'px-4 py-1.5 rounded-md font-medium cursor-pointer transition-all duration-200 text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200';

        if (state.currentViewTab === 'antigravity') {
            btnChannelAntigravity.className = activeClass;
            btnChannelProject.className = inactiveClass;
            if (btnChannelGeminiCli) btnChannelGeminiCli.className = inactiveClass;
            
            if (poolModeContainer) poolModeContainer.classList.remove('hidden');
            if (lblPoolMode) lblPoolMode.innerText = '账号负载均衡';
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
        } else {
            btnChannelProject.className = activeClass;
            btnChannelAntigravity.className = inactiveClass;
            if (btnChannelGeminiCli) btnChannelGeminiCli.className = inactiveClass;
            
            if (poolModeContainer) poolModeContainer.classList.remove('hidden');
            if (lblPoolMode) lblPoolMode.innerText = '项目负载均衡';
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
    /* } else if (state.currentViewTab === 'gemini-cli') {
        if (btnAntigravityLogin) btnAntigravityLogin.classList.add('hidden');
        if (btnGeminiCliLogin) btnGeminiCliLogin.classList.remove('hidden');
        if (btnProjectLogin) btnProjectLogin.classList.add('hidden'); */
    } else {
        if (btnAntigravityLogin) btnAntigravityLogin.classList.add('hidden');
        if (btnGeminiCliLogin) btnGeminiCliLogin.classList.add('hidden');
        if (btnProjectLogin) btnProjectLogin.classList.remove('hidden');
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
        const cardRefreshBtns = accountsList ? accountsList.querySelectorAll('[data-quota-refresh-btn]') : [];
        if (cardRefreshBtns.length === 0) {
            state.quotaCache = {};
            const accounts = await ipcRenderer.invoke('accounts:list');
            renderAccounts(accounts);
        } else {
            for (let i = 0; i < cardRefreshBtns.length; i++) {
                const btn = cardRefreshBtns[i] as HTMLButtonElement;
                btn.click();
                await new Promise(r => setTimeout(r, 200));
            }
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
            try {
                const result = await ipcRenderer.invoke('quota:fetch', acc.id);
                if (result && !result.error) {
                    state.quotaCache[acc.id] = result.buckets;
                }
            } catch (err) {
                console.error(`Failed to refresh quota for ${acc.email}:`, err);
            }
            await new Promise(r => setTimeout(r, 100));
        }
        updateAggregateQuotaUI();
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
        btnAddAccount.innerHTML = '<span class="material-symbols-outlined text-[16px] animate-spin">refresh</span> 登录中...';
        btnAddAccount.classList.add('opacity-70');

        const handleMouseEnter = () => {
            if (state.isLoadingAuth && btnAddAccount) {
                btnAddAccount.innerHTML = '<span class="material-symbols-outlined text-[16px]">cancel</span> 取消登录';
                btnAddAccount.classList.remove('bg-primary', 'hover:bg-primary/90');
                btnAddAccount.classList.add('bg-red-500', 'hover:bg-red-600');
            }
        };

        const handleMouseLeave = () => {
            if (state.isLoadingAuth && btnAddAccount) {
                btnAddAccount.innerHTML = '<span class="material-symbols-outlined text-[16px] animate-spin">refresh</span> 登录中...';
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
    btnRefreshAggregateQuota = document.getElementById('btnRefreshAggregateQuota') as HTMLButtonElement | null;
    btnRefreshAggregateIcon = document.getElementById('btnRefreshAggregateIcon');
    poolModeContainer = document.getElementById('poolModeContainer') as HTMLDivElement | null;
    lblPoolMode = document.getElementById('lblPoolMode');
    btnChannelAntigravity = document.getElementById('btnChannelAntigravity') as HTMLButtonElement | null;
    btnChannelProject = document.getElementById('btnChannelProject') as HTMLButtonElement | null;
    btnChannelGeminiCli = document.getElementById('btnChannelGeminiCli') as HTMLButtonElement | null;
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
    if (sessionBindingsModal) {
        sessionBindingsModal.addEventListener('click', (e: MouseEvent) => {
            if (e.target === sessionBindingsModal) {
                hideSessionBindings();
            }
        });
    }

    if (btnRefreshAllQuota) {
        btnRefreshAllQuota.addEventListener('click', refreshAllQuotas);
    }
    if (btnRefreshAggregateQuota) {
        btnRefreshAggregateQuota.addEventListener('click', refreshAllAccountsQuotas);
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

        document.addEventListener('click', (e: any) => {
            if (btnAddAccount && addAccountDropdown && !btnAddAccount.contains(e.target) && !addAccountDropdown.contains(e.target)) {
                addAccountDropdown.classList.add('hidden');
            }
        });
    }

    // Dynamic project-based button appending
    if (addAccountDropdown && !document.getElementById('btnProjectLogin')) {
        const projectLoginButton = document.createElement('button');
        projectLoginButton.id = 'btnProjectLogin';
        projectLoginButton.className = 'w-full text-left px-4 py-2 text-[13px] text-on-surface dark:text-white hover:bg-slate-50 dark:hover:bg-white/5 transition-colors flex items-center gap-2 border-t border-outline-variant/10 mt-1 pt-3';
        projectLoginButton.type = 'button';
        projectLoginButton.innerHTML = `
            <span class="material-symbols-outlined text-emerald-500 text-[16px]">cloud</span>
            <div>
                <div class="font-bold">Use a Google Cloud project</div>
                <div class="text-[10px] text-outline">先选项目，再登录并绑定到该项目</div>
            </div>
        `;
        projectLoginButton.addEventListener('click', () => startProjectLogin());
        if (addAccountDropdown.children.length >= 2) {
            addAccountDropdown.insertBefore(projectLoginButton, addAccountDropdown.children[1]);
        } else {
            addAccountDropdown.appendChild(projectLoginButton);
        }
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

    if (btnExportAccounts) {
        btnExportAccounts.addEventListener('click', () => {
            ipcRenderer.send('accounts:export-all');
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
            ipcRenderer.send('channel:switch', 'antigravity');
            updateViewTabUI();
            if (state.currentAccountsList) {
                renderAccounts(state.currentAccountsList);
            }
            updateAggregateQuotaUI();
        });
    }
    if (btnChannelProject) {
        btnChannelProject.addEventListener('click', () => {
            state.currentViewTab = 'project';
            ipcRenderer.send('channel:switch', 'project');
            updateViewTabUI();
            if (state.currentAccountsList) {
                renderAccounts(state.currentAccountsList);
            }
            updateAggregateQuotaUI();
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
    ipcRenderer.on('accounts-res', (event: any, data: any) => {
        state.lastBackendData = data;
        if (data && typeof data.activeChannel !== 'undefined') {
            state.currentActiveChannel = data.activeChannel;
        }
        if (!state.currentViewTab) {
            state.currentViewTab = state.currentActiveChannel;
        }
        updateViewTabUI();
        if (data.accounts) {
            state.currentAccountsList = data.accounts;
            renderAccounts(data.accounts);
        }
        updateAggregateQuotaUI();
        if (state.callbacks.updateAnalyzeAccountSelect) {
            state.callbacks.updateAnalyzeAccountSelect();
        }
    });

    // 主动触发一次账号数据同步，确保在前端初始化完毕后拉取到最新数据
    ipcRenderer.send('accounts:get');
}

async function loadSessionBindings() {
    const tableBody = sessionBindingsTableBody;
    if (!tableBody) return;
    tableBody.innerHTML = `
        <tr>
            <td colspan="4" class="p-8 text-center text-outline dark:text-outline-variant italic">
                <span class="inline-block animate-spin mr-2">⏳</span>正在加载会话绑定数据...
            </td>
        </tr>
    `;
    
    try {
        const list = await ipcRenderer.invoke('sessions:get') as Array<{
            sessionKey: string;
            accountId: string;
            accountEmail: string;
            lastActive: number;
        }>;
        
        if (sessionBindingsCount) {
            sessionBindingsCount.textContent = `共 ${list.length} 条记录`;
        }
        
        if (list.length === 0) {
            tableBody.innerHTML = `
                <tr>
                    <td colspan="4" class="p-8 text-center text-outline dark:text-outline-variant italic">
                        📭 当前暂无会话路由绑定关系
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
            if (item.sessionKey.startsWith('auth:prj:')) {
                keyBadge = '<span class="px-1.5 py-0.5 rounded bg-primary/10 text-primary dark:text-primary-fixed-dim text-[10px] font-bold mr-1.5">Bearer</span>';
                channelBadge = '<span class="px-1.5 py-0.5 rounded bg-purple-500/10 text-purple-500 dark:text-purple-300 text-[10px] font-bold mr-1.5">项目负载均衡</span>';
                keyText = item.sessionKey.substring(9);
            } else if (item.sessionKey.startsWith('auth:acc:')) {
                keyBadge = '<span class="px-1.5 py-0.5 rounded bg-primary/10 text-primary dark:text-primary-fixed-dim text-[10px] font-bold mr-1.5">Bearer</span>';
                channelBadge = '<span class="px-1.5 py-0.5 rounded bg-teal-500/10 text-teal-600 dark:text-teal-300 text-[10px] font-bold mr-1.5">账号负载均衡</span>';
                keyText = item.sessionKey.substring(9);
            } else if (item.sessionKey.startsWith('sock:prj:')) {
                keyBadge = '<span class="px-1.5 py-0.5 rounded bg-amber-500/10 text-amber-500 text-[10px] font-bold mr-1.5">Socket</span>';
                channelBadge = '<span class="px-1.5 py-0.5 rounded bg-purple-500/10 text-purple-500 dark:text-purple-300 text-[10px] font-bold mr-1.5">项目负载均衡</span>';
                keyText = item.sessionKey.substring(9);
            } else if (item.sessionKey.startsWith('sock:acc:')) {
                keyBadge = '<span class="px-1.5 py-0.5 rounded bg-amber-500/10 text-amber-500 text-[10px] font-bold mr-1.5">Socket</span>';
                channelBadge = '<span class="px-1.5 py-0.5 rounded bg-teal-500/10 text-teal-600 dark:text-teal-300 text-[10px] font-bold mr-1.5">账号负载均衡</span>';
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
                        解绑
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
                    (e.currentTarget as HTMLButtonElement).textContent = '处理中...';
                    
                    try {
                        const res = await ipcRenderer.invoke('sessions:unbind', key);
                        if (res && res.success) {
                            loadSessionBindings();
                        } else {
                            alert('解绑失败，请重试');
                            (e.currentTarget as HTMLButtonElement).disabled = false;
                            (e.currentTarget as HTMLButtonElement).textContent = '解绑';
                        }
                    } catch (err) {
                        console.error('Failed to unbind session:', err);
                        alert('解绑请求失败');
                        (e.currentTarget as HTMLButtonElement).disabled = false;
                        (e.currentTarget as HTMLButtonElement).textContent = '解绑';
                    }
                });
            }
            
            tableBody.appendChild(tr);
        });
        
    } catch (err) {
        console.error('Failed to load session bindings:', err);
        tableBody.innerHTML = `
            <tr>
                <td colspan="4" class="p-8 text-center text-red-500 italic">
                    ❌ 获取绑定关系失败：${(err as Error).message}
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
    if (!confirm('您确定要清空所有的会话路由绑定关系吗？这将会使后续客户端的请求重新在可用账号池中进行轮询或一致性哈希分配。')) {
        return;
    }
    if (sessionBindingsModalClearAllBtn) {
        sessionBindingsModalClearAllBtn.disabled = true;
        const span = sessionBindingsModalClearAllBtn.querySelector('span:last-child');
        if (span) span.textContent = '清空中...';
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
            if (span) span.textContent = '清空所有绑定';
        }
    }
}


// Global window registration for DOM inline click events
(window as any).startLogin = startLogin;
(window as any).startProjectLogin = startProjectLogin;

// Register shared callbacks
state.callbacks.renderAccounts = renderAccounts;
state.callbacks.updateAggregateQuotaUI = updateAggregateQuotaUI;
