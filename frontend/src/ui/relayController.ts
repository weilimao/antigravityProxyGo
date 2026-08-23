import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';

// Force import the modules to ensure window bindings are registered
import { refreshRelayPackages } from './relayPackages';
import { 
    refreshRelayUsers, 
    setCurrentPage, 
    setCurrentSearchQuery, 
    setCurrentPackageFilter,
    currentPage, 
    totalUsersCount, 
    pageSize,
    openAddUserModal,
    closeAddUserModal,
    handleAddUser
} from './relayUsers';
import './relayUserStats';
// Model mapping panel now rendered as Vue SFC (ModelMappingPanel.vue), no DOM init needed.

// Facade re-exports for external modules (e.g., dashboard.ts)
export { refreshRelayPackages } from './relayPackages';
export { refreshRelayUsers } from './relayUsers';

export function initRelayEvents() {
    (state.callbacks as any).refreshRelayUI = () => {
        refreshRelayPackages().finally(() => {
            refreshRelayUsers();
        });
    };


    // Toggle relay server
    const chkRelayEnabled = document.getElementById('chkRelayEnabled') as HTMLInputElement;
    const relayPortInput = document.getElementById('relayPortInput') as HTMLInputElement;
    const btnAddRelayUser = document.getElementById('btnAddRelayUser');
    
    if (chkRelayEnabled) {
        chkRelayEnabled.addEventListener('change', async () => {
            const port = relayPortInput?.value || '18444';
            try {
                await ipcRenderer.invoke('relay:set-config', { enabled: chkRelayEnabled.checked, port });
            } catch (err) {
                console.error('[RelayController] Failed to set config:', err);
            }
        });
    }

    if (btnAddRelayUser) {
        btnAddRelayUser.addEventListener('click', () => openAddUserModal());
    }

    // Add user modal buttons
    const btnRelayUserConfirm = document.getElementById('btnRelayUserConfirm');
    const btnRelayUserCancel = document.getElementById('btnRelayUserCancel');
    
    if (btnRelayUserConfirm) {
        btnRelayUserConfirm.addEventListener('click', handleAddUser);
    }
    if (btnRelayUserCancel) {
        btnRelayUserCancel.addEventListener('click', closeAddUserModal);
    }


    // Search input event (300ms debounce)
    const searchInput = document.getElementById('relayUserSearchInput') as HTMLInputElement;
    if (searchInput) {
        let debounceTimer: any;
        searchInput.addEventListener('input', () => {
            clearTimeout(debounceTimer);
            debounceTimer = setTimeout(() => {
                setCurrentSearchQuery(searchInput.value.trim());
                setCurrentPage(1);
                refreshRelayUsers();
            }, 300);
        });
    }

    // Package filter event
    const packageFilter = document.getElementById('relayUserPackageFilter') as HTMLSelectElement;
    if (packageFilter) {
        packageFilter.addEventListener('change', () => {
            setCurrentPackageFilter(packageFilter.value);
            setCurrentPage(1);
            refreshRelayUsers();
        });
    }

    // Pagination events
    const btnPrev = document.getElementById('btnRelayUserPrevPage');
    if (btnPrev) {
        btnPrev.addEventListener('click', () => {
            if (currentPage > 1) {
                setCurrentPage(currentPage - 1);
                refreshRelayUsers();
            }
        });
    }

    const btnNext = document.getElementById('btnRelayUserNextPage');
    if (btnNext) {
        btnNext.addEventListener('click', () => {
            const totalPages = Math.ceil(totalUsersCount / pageSize) || 1;
            if (currentPage < totalPages) {
                setCurrentPage(currentPage + 1);
                refreshRelayUsers();
            }
        });
    }

    // Load persisted packages then users on init
    refreshRelayPackages().finally(() => {
        refreshRelayUsers();
    });

    // Fetch initial config state to sync UI
    ipcRenderer.invoke('relay:get-config')
        .then((config: any) => {
            if (chkRelayEnabled) chkRelayEnabled.checked = !!config?.enabled;
            if (relayPortInput) relayPortInput.value = config?.port || '18444';
        })
        .catch((err: any) => console.error('[RelayController] Failed to get initial config:', err));

    // ========== 子 Tab 切换与配置管理 ==========
    const btnRelaySubTabUsers = document.getElementById('btnRelaySubTabUsers');
    const btnRelaySubTabPackages = document.getElementById('btnRelaySubTabPackages');
    const btnRelaySubTabSecurity = document.getElementById('btnRelaySubTabSecurity');
    const btnRelaySubTabModelMapping = document.getElementById('btnRelaySubTabModelMapping');
    const btnRelaySubTabTutorial = document.getElementById('btnRelaySubTabTutorial');

    const panelUsers = document.getElementById('relay-sub-panel-users');
    const panelPackages = document.getElementById('relay-sub-panel-packages');
    const panelSecurity = document.getElementById('relay-sub-panel-security');
    const panelModelMapping = document.getElementById('relay-sub-panel-modelmapping');
    const panelTutorial = document.getElementById('relay-sub-panel-tutorial');

    const subTabActiveClass = 'px-4 py-1.5 text-[12px] font-bold bg-primary/10 text-primary dark:bg-primary/20 rounded-lg cursor-pointer transition-all duration-200';
    const subTabInactiveClass = 'px-4 py-1.5 text-[12px] font-medium text-slate-500 hover:text-slate-800 dark:text-slate-400 dark:hover:text-slate-200 rounded-lg cursor-pointer transition-all duration-200';

    function switchSubTab(active: 'users' | 'packages' | 'security' | 'modelmapping' | 'tutorial') {
        if (panelUsers) panelUsers.classList.toggle('hidden', active !== 'users');
        if (panelPackages) panelPackages.classList.toggle('hidden', active !== 'packages');
        if (panelSecurity) panelSecurity.classList.toggle('hidden', active !== 'security');
        if (panelModelMapping) panelModelMapping.classList.toggle('hidden', active !== 'modelmapping');
        if (panelTutorial) panelTutorial.classList.toggle('hidden', active !== 'tutorial');

        if (btnRelaySubTabUsers) btnRelaySubTabUsers.className = active === 'users' ? subTabActiveClass : subTabInactiveClass;
        if (btnRelaySubTabPackages) btnRelaySubTabPackages.className = active === 'packages' ? subTabActiveClass : subTabInactiveClass;
        if (btnRelaySubTabSecurity) btnRelaySubTabSecurity.className = active === 'security' ? subTabActiveClass : subTabInactiveClass;
        if (btnRelaySubTabModelMapping) btnRelaySubTabModelMapping.className = active === 'modelmapping' ? subTabActiveClass : subTabInactiveClass;
        if (btnRelaySubTabTutorial) btnRelaySubTabTutorial.className = active === 'tutorial' ? subTabActiveClass : subTabInactiveClass;

        if (active === 'modelmapping') {
            // Model mapping panel loads data via Vue onMounted in ModelMappingPanel.vue
        }
    }

    if (btnRelaySubTabUsers) btnRelaySubTabUsers.addEventListener('click', () => switchSubTab('users'));
    if (btnRelaySubTabPackages) btnRelaySubTabPackages.addEventListener('click', () => switchSubTab('packages'));
    if (btnRelaySubTabSecurity) btnRelaySubTabSecurity.addEventListener('click', () => switchSubTab('security'));
    if (btnRelaySubTabModelMapping) btnRelaySubTabModelMapping.addEventListener('click', () => switchSubTab('modelmapping'));
    if (btnRelaySubTabTutorial) btnRelaySubTabTutorial.addEventListener('click', () => switchSubTab('tutorial'));

    // 绑定安全拦截设置元素
    const chkSSRF = document.getElementById('chkRelaySSRFBlock') as HTMLInputElement | null;
    const chkPort = document.getElementById('chkRelayPortBlock') as HTMLInputElement | null;
    const chkDomain = document.getElementById('chkRelayDomainFilter') as HTMLInputElement | null;
    const txtWhitelist = document.getElementById('txtRelayDomainWhitelist') as HTMLTextAreaElement | null;
    const btnSaveRelaySecurity = document.getElementById('btnSaveRelaySecurity');

    // 加载初始安全拦截设置
    ipcRenderer.invoke('relay:get-security-config')
        .then((cfg: any) => {
            if (cfg) {
                if (chkSSRF) chkSSRF.checked = !!cfg.relaySSRFBlock;
                if (chkPort) chkPort.checked = !!cfg.relayPortBlock;
                if (chkDomain) chkDomain.checked = !!cfg.relayDomainFilter;
                if (txtWhitelist && cfg.relayDomainWhitelist) {
                    txtWhitelist.value = cfg.relayDomainWhitelist.join('\n');
                }
            }
        })
        .catch((err: any) => console.error('[RelayController] Failed to get initial security config:', err));

    const saveSecurityConfig = async () => {
        const ssrf = !!chkSSRF?.checked;
        const port = !!chkPort?.checked;
        const domain = !!chkDomain?.checked;
        const whitelist = txtWhitelist?.value.split('\n')
            .map(line => line.trim())
            .filter(line => line !== '') || [];

        try {
            await ipcRenderer.invoke('relay:set-security-config', {
                relaySSRFBlock: ssrf,
                relayPortBlock: port,
                relayDomainFilter: domain,
                relayDomainWhitelist: whitelist
            });
        } catch (err) {
            console.error('[RelayController] Failed to save security config:', err);
        }
    };

    // 改变开关时自动保存
    if (chkSSRF) chkSSRF.addEventListener('change', saveSecurityConfig);
    if (chkPort) chkPort.addEventListener('change', saveSecurityConfig);
    if (chkDomain) chkDomain.addEventListener('change', saveSecurityConfig);

    // 点击保存按钮时保存配置与白名单
    if (btnSaveRelaySecurity) {
        btnSaveRelaySecurity.addEventListener('click', async () => {
            const originalText = btnSaveRelaySecurity.innerHTML;
            btnSaveRelaySecurity.textContent = '⏳ 保存中...';
            await saveSecurityConfig();
            btnSaveRelaySecurity.innerHTML = originalText;
        });
    }

    // Model mapping panel is now a Vue SFC, no DOM init needed.
}

// Listen for relay config updates globally (only once when module loads)
ipcRenderer.on('relay-state', (_e: any, config: any) => {
    const chkRelayEnabled = document.getElementById('chkRelayEnabled') as HTMLInputElement | null;
    const relayPortInput = document.getElementById('relayPortInput') as HTMLInputElement | null;
    if (chkRelayEnabled) chkRelayEnabled.checked = !!config?.enabled;
    if (relayPortInput) relayPortInput.value = config?.port || '18444';
});
