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
let btnExportAccounts: HTMLButtonElement | null;
let btnImportAccounts: HTMLButtonElement | null;

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
        if (state.currentViewTab === 'antigravity') {
            btnChannelAntigravity.className = 'px-4 py-1.5 rounded-md font-bold cursor-pointer transition-all duration-200 bg-white dark:bg-[#1a1f30] text-primary dark:text-primary-fixed-dim shadow-sm';
            btnChannelProject.className = 'px-4 py-1.5 rounded-md font-medium cursor-pointer transition-all duration-200 text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200';
            
            if (poolModeContainer) poolModeContainer.classList.remove('hidden');
            if (lblPoolMode) lblPoolMode.innerText = '账号负载均衡';
            if (poolModeToggle && state.lastBackendData) {
                poolModeToggle.checked = state.lastBackendData.poolMode;
            }
        } else {
            btnChannelProject.className = 'px-4 py-1.5 rounded-md font-bold cursor-pointer transition-all duration-200 bg-white dark:bg-[#1a1f30] text-primary dark:text-primary-fixed-dim shadow-sm';
            btnChannelAntigravity.className = 'px-4 py-1.5 rounded-md font-medium cursor-pointer transition-all duration-200 text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200';
            
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
        if (btnGeminiCliLogin) btnGeminiCliLogin.classList.remove('hidden');
        if (btnProjectLogin) btnProjectLogin.classList.add('hidden');
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
    if (state.isRefreshingAggregate || !state.currentAccountsList || state.currentAccountsList.length === 0) return;
    state.isRefreshingAggregate = true;

    if (btnRefreshAggregateQuota && btnRefreshAggregateIcon) {
        btnRefreshAggregateIcon.classList.add('animate-spin');
        btnRefreshAggregateQuota.disabled = true;
        btnRefreshAggregateQuota.classList.add('opacity-60', 'cursor-not-allowed');
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
        if (btnRefreshAggregateQuota && btnRefreshAggregateIcon) {
            btnRefreshAggregateIcon.classList.remove('animate-spin');
            btnRefreshAggregateQuota.disabled = false;
            btnRefreshAggregateQuota.classList.remove('opacity-60', 'cursor-not-allowed');
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
        btnAddAccount.classList.add('opacity-70', 'cursor-not-allowed');

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
            btnAddAccount.innerHTML = origText;
            btnAddAccount.classList.remove('opacity-70', 'cursor-not-allowed');
        }
    }
}

// Start Project binding login
export async function startProjectLogin() {
    if (addAccountDropdown) addAccountDropdown.classList.add('hidden');
    if (state.isLoadingAuth) return;
    state.isLoadingAuth = true;
    
    if (btnAddAccount) {
        const origText = btnAddAccount.innerHTML;
        btnAddAccount.innerHTML = '<span class="material-symbols-outlined text-[16px] animate-spin">refresh</span> 登录中...';
        btnAddAccount.classList.add('opacity-70', 'cursor-not-allowed');

        try {
            const result = await showOneStopAuthModal();
            if (result && result.success) {
                // GCP login successful
            }
        } catch (err: any) {
            alert('登录发生错误: ' + err.message);
        } finally {
            state.isLoadingAuth = false;
            btnAddAccount.innerHTML = origText;
            btnAddAccount.classList.remove('opacity-70', 'cursor-not-allowed');
        }
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
    btnExportAccounts = document.getElementById('btnExportAccounts') as HTMLButtonElement | null;
    btnImportAccounts = document.getElementById('btnImportAccounts') as HTMLButtonElement | null;

    if (btnRefreshAllQuota) {
        btnRefreshAllQuota.addEventListener('click', refreshAllQuotas);
    }
    if (btnRefreshAggregateQuota) {
        btnRefreshAggregateQuota.addEventListener('click', refreshAllAccountsQuotas);
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
        btnAddAccount.addEventListener('click', () => {
            if (state.isLoadingAuth) return;
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


// Global window registration for DOM inline click events
(window as any).startLogin = startLogin;
(window as any).startProjectLogin = startProjectLogin;

// Register shared callbacks
state.callbacks.renderAccounts = renderAccounts;
state.callbacks.updateAggregateQuotaUI = updateAggregateQuotaUI;
