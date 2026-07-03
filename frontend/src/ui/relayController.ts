import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';
import i18n from '../shared/i18n';

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

// Facade re-exports for external modules (e.g., dashboard.ts)
export { refreshRelayPackages } from './relayPackages';
export { refreshRelayUsers } from './relayUsers';

export function initRelayEvents() {
    (state.callbacks as any).refreshRelayUI = () => {
        refreshRelayPackages().finally(() => {
            refreshRelayUsers();
        });
    };

    (window as any)._relayOpenModal = (id: string) => {
        const modal = document.getElementById(id);
        if (!modal) return;
        modal.classList.remove('hidden');
        void modal.offsetWidth;
        modal.classList.add('show');
    };

    (window as any)._relayCloseModal = (id: string) => {
        const modal = document.getElementById(id);
        if (!modal) return;
        modal.classList.remove('show');
        const onTransitionEnd = (e: TransitionEvent) => {
            if (e.propertyName === 'opacity' && !modal.classList.contains('show')) {
                modal.classList.add('hidden');
                modal.removeEventListener('transitionend', onTransitionEnd);
            }
        };
        modal.addEventListener('transitionend', onTransitionEnd);
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

    // Listen for relay config updates
    ipcRenderer.on('relay-state', (_e: any, config: any) => {
        if (chkRelayEnabled) chkRelayEnabled.checked = !!config?.enabled;
        if (relayPortInput) relayPortInput.value = config?.port || '18444';
    });

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
    
    const panelUsers = document.getElementById('relay-sub-panel-users');
    const panelPackages = document.getElementById('relay-sub-panel-packages');
    const panelSecurity = document.getElementById('relay-sub-panel-security');
    const panelModelMapping = document.getElementById('relay-sub-panel-modelmapping');

    const subTabActiveClass = 'px-4 py-1.5 text-[12px] font-bold bg-primary/10 text-primary dark:bg-primary/20 rounded-lg cursor-pointer transition-all duration-200';
    const subTabInactiveClass = 'px-4 py-1.5 text-[12px] font-medium text-slate-500 hover:text-slate-800 dark:text-slate-400 dark:hover:text-slate-200 rounded-lg cursor-pointer transition-all duration-200';

    function switchSubTab(active: 'users' | 'packages' | 'security' | 'modelmapping') {
        if (panelUsers) panelUsers.classList.toggle('hidden', active !== 'users');
        if (panelPackages) panelPackages.classList.toggle('hidden', active !== 'packages');
        if (panelSecurity) panelSecurity.classList.toggle('hidden', active !== 'security');
        if (panelModelMapping) panelModelMapping.classList.toggle('hidden', active !== 'modelmapping');

        if (btnRelaySubTabUsers) btnRelaySubTabUsers.className = active === 'users' ? subTabActiveClass : subTabInactiveClass;
        if (btnRelaySubTabPackages) btnRelaySubTabPackages.className = active === 'packages' ? subTabActiveClass : subTabInactiveClass;
        if (btnRelaySubTabSecurity) btnRelaySubTabSecurity.className = active === 'security' ? subTabActiveClass : subTabInactiveClass;
        if (btnRelaySubTabModelMapping) btnRelaySubTabModelMapping.className = active === 'modelmapping' ? subTabActiveClass : subTabInactiveClass;

        if (active === 'modelmapping') {
            loadModelMappings();
        }
    }

    if (btnRelaySubTabUsers) btnRelaySubTabUsers.addEventListener('click', () => switchSubTab('users'));
    if (btnRelaySubTabPackages) btnRelaySubTabPackages.addEventListener('click', () => switchSubTab('packages'));
    if (btnRelaySubTabSecurity) btnRelaySubTabSecurity.addEventListener('click', () => switchSubTab('security'));
    if (btnRelaySubTabModelMapping) btnRelaySubTabModelMapping.addEventListener('click', () => switchSubTab('modelmapping'));

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

    // ========== 模型映射配置交互 ==========
    let modelMappings: any[] = [];

    async function loadModelMappings() {
        try {
            const list = await ipcRenderer.invoke('relay:get-model-mapping');
            modelMappings = list || [];
            renderModelMappingTable(modelMappings);
        } catch (err) {
            console.error('[RelayController] Failed to get model mappings:', err);
        }
    }

    function renderModelMappingTable(mappings: any[]) {
        const tbody = document.getElementById('modelMappingTableBody');
        if (!tbody) return;
        tbody.innerHTML = '';
        mappings.forEach((item, index) => {
            const tr = document.createElement('tr');
            tr.className = 'border-b border-outline-variant/15 hover:bg-slate-50 dark:hover:bg-white/5';
            tr.innerHTML = `
                <td class="py-2 px-1">
                    <input type="text" class="w-full px-2 py-1 text-[12px] rounded border border-outline-variant/30 bg-transparent text-on-surface dark:text-white client-model-input" value="${item.clientModel || ''}" data-index="${index}" placeholder="例如: gpt-4o" />
                </td>
                <td class="py-2 px-1">
                    <input type="text" class="w-full px-2 py-1 text-[12px] rounded border border-outline-variant/30 bg-transparent text-on-surface dark:text-white target-model-input" value="${item.targetModel || ''}" data-index="${index}" placeholder="例如: gemini-1.5-pro" />
                </td>
                <td class="py-2 text-center">
                    <input type="checkbox" class="text-primary focus:ring-primary rounded expose-checkbox" ${item.expose ? 'checked' : ''} data-index="${index}" />
                </td>
                <td class="py-2 text-center">
                    <button class="text-red-500 hover:text-red-700 transition-colors flex items-center justify-center mx-auto btn-delete-mapping cursor-pointer" data-index="${index}">
                        <span class="material-symbols-outlined text-[18px]">delete</span>
                    </button>
                </td>
            `;
            tbody.appendChild(tr);
        });

        // 绑定删除按钮事件
        tbody.querySelectorAll('.btn-delete-mapping').forEach(btn => {
            btn.addEventListener('click', (e) => {
                const idx = parseInt((e.currentTarget as HTMLElement).getAttribute('data-index') || '0');
                mappings.splice(idx, 1);
                renderModelMappingTable(mappings);
            });
        });

        // 绑定输入改变事件
        tbody.querySelectorAll('.client-model-input').forEach(input => {
            input.addEventListener('input', (e) => {
                const target = e.target as HTMLInputElement;
                const idx = parseInt(target.getAttribute('data-index') || '0');
                mappings[idx].clientModel = target.value.trim();
            });
        });
        tbody.querySelectorAll('.target-model-input').forEach(input => {
            input.addEventListener('input', (e) => {
                const target = e.target as HTMLInputElement;
                const idx = parseInt(target.getAttribute('data-index') || '0');
                mappings[idx].targetModel = target.value.trim();
            });
        });
        tbody.querySelectorAll('.expose-checkbox').forEach(chk => {
            chk.addEventListener('change', (e) => {
                const target = e.target as HTMLInputElement;
                const idx = parseInt(target.getAttribute('data-index') || '0');
                mappings[idx].expose = target.checked;
            });
        });
    }

    (window as any)._relayAddModelMapping = () => {
        modelMappings.unshift({ clientModel: '', targetModel: '', expose: true });
        renderModelMappingTable(modelMappings);
    };

    (window as any)._relaySaveModelMapping = async () => {
        const btnSaveModelMapping = document.getElementById('btnSaveModelMapping');
        if (!btnSaveModelMapping) return;
        const originalText = btnSaveModelMapping.innerHTML;
        const dict = i18n[state.currentLanguage] || {};
        btnSaveModelMapping.innerHTML = `<span class="material-symbols-outlined text-[16px] animate-spin">sync</span><span>${dict.relaySaving || '保存中...'}</span>`;
        
        // 过滤空映射
        const filteredMappings = modelMappings.filter(m => m.clientModel && m.clientModel.trim() !== '' && m.targetModel && m.targetModel.trim() !== '');
        try {
            const res = await ipcRenderer.invoke('relay:set-model-mapping', filteredMappings);
            if (res && res.success) {
                btnSaveModelMapping.innerHTML = `<span class="material-symbols-outlined text-[16px]">done</span><span>${dict.relaySaveSuccess || '保存成功'}</span>`;
                setTimeout(() => {
                    btnSaveModelMapping.innerHTML = originalText;
                }, 2000);
            } else {
                btnSaveModelMapping.innerHTML = `<span class="material-symbols-outlined text-[16px]">error</span><span>${dict.relaySaveFailed || '保存失败'}</span>`;
                setTimeout(() => {
                    btnSaveModelMapping.innerHTML = originalText;
                }, 2000);
            }
        } catch (err) {
            console.error('[RelayController] Failed to save model mappings:', err);
            btnSaveModelMapping.innerHTML = `<span class="material-symbols-outlined text-[16px]">error</span><span>${dict.relaySaveFailed || '保存失败'}</span>`;
            setTimeout(() => {
                btnSaveModelMapping.innerHTML = originalText;
            }, 2000);
        }
    };
}
