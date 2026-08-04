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
            loadModelMappings();
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

    // ========== 动态号池 Tab 与模型映射配置交互 ==========
    interface PoolTabInfo {
        id: string;
        name: string;
        targetProvider: string;
        isCustom?: boolean;
    }

    let allMappings: any[] = [];
    let poolTabs: PoolTabInfo[] = [];
    let activeTabId: string = 'google';
    let availableChannels: string[] = ['antigravity', 'google', 'gcp', 'nvidia'];

    async function loadModelMappings() {
        try {
            // 1. 获取动态账号池 Channel 列表
            try {
                const chans = await ipcRenderer.invoke('relay:get-account-channels');
                if (Array.isArray(chans) && chans.length > 0) {
                    availableChannels = Array.from(new Set([...availableChannels, ...chans]));
                }
            } catch (e) {
                console.warn('[RelayController] Failed to get channels:', e);
            }

            // 2. 获取模型映射数据
            const list = await ipcRenderer.invoke('relay:get-model-mapping');
            allMappings = list || [];

            // 3. 构建默认 Tab 列表 (默认只预设前三个)
            poolTabs = [
                { id: 'google', name: 'Gemini (Google)', targetProvider: 'google' },
                { id: 'nvidia', name: 'NVIDIA 号池', targetProvider: 'nvidia' },
                { id: 'gcp', name: '谷歌云 API', targetProvider: 'gcp' }
            ];

            // 4. 根据已有 mappings 里的 ownedBy 填充可删除的自定义 Tab
            const knownIds = new Set(poolTabs.map(t => t.id));
            allMappings.forEach(m => {
                if (m.ownedBy && !knownIds.has(m.ownedBy)) {
                    knownIds.add(m.ownedBy);
                    poolTabs.push({
                        id: m.ownedBy,
                        name: (m.ownedBy.charAt(0).toUpperCase() + m.ownedBy.slice(1)) + ' 号池',
                        targetProvider: m.targetProvider || m.ownedBy,
                        isCustom: true
                    });
                }
            });

            // 确保 activeTabId 合法
            if (!poolTabs.some(t => t.id === activeTabId)) {
                activeTabId = poolTabs[0].id;
            }

            renderPoolTabs();
            renderTargetProviderSelector();
            renderCurrentTabTable();
        } catch (err) {
            console.error('[RelayController] Failed to get model mappings:', err);
        }
    }

    function deleteTabById(tabId: string) {
        const tabToDelete = poolTabs.find(t => t.id === tabId);
        if (!tabToDelete || !tabToDelete.isCustom) return;

        if (!confirm(`确定要删除号池 Tab「${tabToDelete.name}」及其下的所有模型映射吗？`)) return;

        allMappings = allMappings.filter(m => getMappingTab(m) !== tabId);
        poolTabs = poolTabs.filter(t => t.id !== tabId);

        if (activeTabId === tabId) {
            activeTabId = poolTabs[0]?.id || 'google';
        }

        renderPoolTabs();
        renderTargetProviderSelector();
        renderCurrentTabTable();
    }

    function renderPoolTabs() {
        const nav = document.getElementById('modelMappingTabsNav');
        if (!nav) return;
        nav.innerHTML = '';
        poolTabs.forEach(tab => {
            const btn = document.createElement('button');
            const isActive = tab.id === activeTabId;
            btn.className = `px-3 py-1.5 rounded-lg text-[12px] font-bold transition-all cursor-pointer whitespace-nowrap flex items-center gap-1.5 ${
                isActive
                    ? 'bg-primary text-white shadow-sm shadow-primary/30'
                    : 'bg-slate-100 dark:bg-white/5 text-slate-600 dark:text-slate-300 hover:bg-slate-200 dark:hover:bg-white/10'
            }`;
            btn.innerHTML = `<span>${tab.name}</span>${tab.isCustom ? `<span class="material-symbols-outlined text-[14px] hover:text-red-400 ml-1 transition-colors btn-delete-tab-icon" title="删除此 Tab">close</span>` : ''}`;
            btn.addEventListener('click', (e) => {
                const target = e.target as HTMLElement;
                if (target.classList.contains('btn-delete-tab-icon')) {
                    e.stopPropagation();
                    deleteTabById(tab.id);
                    return;
                }
                activeTabId = tab.id;
                renderPoolTabs();
                renderTargetProviderSelector();
                renderCurrentTabTable();
            });
            nav.appendChild(btn);
        });
    }

    function renderTargetProviderSelector() {
        const select = document.getElementById('tabTargetProviderSelect') as HTMLSelectElement | null;
        const customInput = document.getElementById('tabTargetProviderCustom') as HTMLInputElement | null;
        const btnDeleteTab = document.getElementById('btnDeleteCurrentTab');
        if (!select) return;

        const currentTab = poolTabs.find(t => t.id === activeTabId) || poolTabs[0];

        // 收集所有去重的 Provider 选项
        const providers = Array.from(new Set(['google', 'nvidia', 'gcp', 'antigravity', 'deepseek', 'qwen', 'anthropic', 'moonshot', ...availableChannels, currentTab.targetProvider].filter(Boolean)));

        select.innerHTML = providers.map(p => `<option value="${p}">${p}</option>`).join('') + `<option value="__custom__">+ 自定义 Provider...</option>`;

        if (providers.includes(currentTab.targetProvider)) {
            select.value = currentTab.targetProvider;
            if (customInput) customInput.classList.add('hidden');
        } else {
            select.value = '__custom__';
            if (customInput) {
                customInput.classList.remove('hidden');
                customInput.value = currentTab.targetProvider;
            }
        }

        // 控制删除按钮显示状态
        if (btnDeleteTab) {
            if (currentTab.isCustom) {
                btnDeleteTab.classList.remove('hidden');
            } else {
                btnDeleteTab.classList.add('hidden');
            }
        }

        // 绑定下拉切换事件
        select.onchange = () => {
            if (select.value === '__custom__') {
                if (customInput) {
                    customInput.classList.remove('hidden');
                    customInput.focus();
                }
            } else {
                if (customInput) customInput.classList.add('hidden');
                currentTab.targetProvider = select.value;
                // 同步更新当前 Tab 下的所有映射 targetProvider
                allMappings.forEach(m => {
                    if (getMappingTab(m) === activeTabId) {
                        m.targetProvider = select.value;
                    }
                });
            }
        };

        if (customInput) {
            customInput.oninput = () => {
                const val = customInput.value.trim();
                currentTab.targetProvider = val;
                allMappings.forEach(m => {
                    if (getMappingTab(m) === activeTabId) {
                        m.targetProvider = val;
                    }
                });
            };
        }
    }

    function getMappingTab(m: any): string {
        if (m.ownedBy) return m.ownedBy;
        // 隐式根据 clientModel/targetModel 归类
        const modelName = (m.clientModel || m.targetModel || '').toLowerCase();
        if (modelName.startsWith('nvidia/') || modelName.endsWith('-nemotron')) return 'nvidia';
        if (modelName.startsWith('deepseek')) return 'deepseek';
        if (modelName.startsWith('qwen')) return 'qwen';
        if (modelName.startsWith('claude')) return 'anthropic';
        return 'google';
    }

    function getTabMappings(): any[] {
        return allMappings.filter(m => getMappingTab(m) === activeTabId);
    }

    let channelModelsCache: Record<string, string[]> = {};

    function updateDatalist(models: string[]) {
        const datalist = document.getElementById('channelModelsDatalist');
        if (!datalist) return;
        datalist.innerHTML = models.map(m => `<option value="${m}">${m}</option>`).join('');
    }

    (window as any)._relayFetchChannelModels = async () => {
        const btnFetch = document.getElementById('btnFetchChannelModels');
        const lblCount = document.getElementById('lblFetchedModelsCount');
        const currentTab = poolTabs.find(t => t.id === activeTabId) || poolTabs[0];
        const channel = currentTab.targetProvider || currentTab.id;

        if (btnFetch) {
            btnFetch.innerHTML = `<span class="material-symbols-outlined text-[15px] animate-spin">sync</span><span>获取中...</span>`;
            (btnFetch as HTMLButtonElement).disabled = true;
        }

        try {
            const res = await ipcRenderer.invoke('relay:fetch-channel-models', channel);
            if (res && res.success && Array.isArray(res.models)) {
                channelModelsCache[channel] = res.models;
                updateDatalist(res.models);
                if (lblCount) {
                    lblCount.textContent = `✅ 已获取 ${res.models.length} 个模型`;
                    lblCount.classList.remove('hidden');
                }
                renderCurrentTabTable();
            } else {
                if (lblCount) {
                    lblCount.textContent = `❌ 获取失败: ${res?.error || '网络超时'}`;
                    lblCount.classList.remove('hidden');
                }
            }
        } catch (e: any) {
            console.error('[RelayController] Fetch models error:', e);
            if (lblCount) {
                lblCount.textContent = `❌ 获取出错`;
                lblCount.classList.remove('hidden');
            }
        } finally {
            if (btnFetch) {
                btnFetch.innerHTML = `<span class="material-symbols-outlined text-[15px]">sync</span><span>获取号池模型</span>`;
                (btnFetch as HTMLButtonElement).disabled = false;
            }
        }
    };

    function renderCurrentTabTable() {
        const tbody = document.getElementById('modelMappingTableBody');
        if (!tbody) return;
        tbody.innerHTML = '';

        const currentTabMappings = getTabMappings();
        const currentTab = poolTabs.find(t => t.id === activeTabId) || poolTabs[0];
        const fetchedModels = channelModelsCache[currentTab.targetProvider || currentTab.id] || [];

        const isNvidiaTab = (currentTab.targetProvider === 'nvidia' || currentTab.id === 'nvidia');
        const thInjectKwargs = document.getElementById('thInjectKwargs');
        if (thInjectKwargs) {
            if (isNvidiaTab) {
                thInjectKwargs.classList.remove('hidden');
            } else {
                thInjectKwargs.classList.add('hidden');
            }
        }

        if (fetchedModels.length > 0) {
            updateDatalist(fetchedModels);
        }

        currentTabMappings.forEach((item, index) => {
            const tr = document.createElement('tr');
            tr.className = 'border-b border-outline-variant/15 hover:bg-slate-50 dark:hover:bg-white/5';
            tr.innerHTML = `
                <td class="py-2 px-1">
                    <input type="text" class="w-full px-2 py-1 text-[12px] rounded border border-outline-variant/30 bg-transparent text-on-surface dark:text-white client-model-input" value="${item.clientModel || ''}" data-index="${index}" placeholder="例如: gpt-4o" />
                </td>
                <td class="py-2 px-1">
                    <div class="flex items-center gap-1.5">
                        <input type="text" class="w-full px-2 py-1 text-[12px] rounded border border-outline-variant/30 bg-transparent text-on-surface dark:text-white target-model-input" value="${item.targetModel || ''}" data-index="${index}" list="channelModelsDatalist" placeholder="例如: gemini-1.5-pro" />
                        <select class="px-2 py-1 text-[11px] font-mono rounded border border-outline-variant/30 bg-slate-100 dark:bg-white/10 text-on-surface dark:text-white target-model-quick-select cursor-pointer w-32 flex-shrink-0" data-index="${index}">
                            <option value="">${fetchedModels.length > 0 ? '选择模型...' : '未拉取模型'}</option>
                            ${fetchedModels.map(m => `<option value="${m}" ${m === item.targetModel ? 'selected' : ''}>${m}</option>`).join('')}
                        </select>
                    </div>
                </td>
                <td class="py-2 text-center inject-kwargs-cell ${isNvidiaTab ? '' : 'hidden'}">
                    <input type="checkbox" class="text-primary focus:ring-primary rounded inject-kwargs-checkbox" ${item.injectChatTemplateKwargs !== false ? 'checked' : ''} data-index="${index}" title="是否向 NVIDIA 等上游注入 chat_template_kwargs (默认勾选)" />
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

        // 绑定删除按钮
        tbody.querySelectorAll('.btn-delete-mapping').forEach(btn => {
            btn.addEventListener('click', (e) => {
                const idx = parseInt((e.currentTarget as HTMLElement).getAttribute('data-index') || '0');
                const targetItem = currentTabMappings[idx];
                const mainIdx = allMappings.indexOf(targetItem);
                if (mainIdx !== -1) {
                    allMappings.splice(mainIdx, 1);
                }
                renderCurrentTabTable();
            });
        });

        // 绑定输入改变
        tbody.querySelectorAll('.client-model-input').forEach(input => {
            input.addEventListener('input', (e) => {
                const target = e.target as HTMLInputElement;
                const idx = parseInt(target.getAttribute('data-index') || '0');
                currentTabMappings[idx].clientModel = target.value.trim();
            });
        });

        tbody.querySelectorAll('.target-model-input').forEach(input => {
            const handleTargetModelChange = (e: Event) => {
                const target = e.target as HTMLInputElement;
                const idx = parseInt(target.getAttribute('data-index') || '0');
                const val = target.value.trim();
                currentTabMappings[idx].targetModel = val;

                // 若 Client Model 为空，自动联动把选中的模型名同步填入 Client Model
                const clientInput = tbody.querySelector(`.client-model-input[data-index="${idx}"]`) as HTMLInputElement | null;
                if (clientInput && (!clientInput.value || !clientInput.value.trim())) {
                    currentTabMappings[idx].clientModel = val;
                    clientInput.value = val;
                }
            };

            input.addEventListener('input', handleTargetModelChange);
            input.addEventListener('change', handleTargetModelChange);
        });

        tbody.querySelectorAll('.target-model-quick-select').forEach(sel => {
            sel.addEventListener('change', (e) => {
                const target = e.target as HTMLSelectElement;
                const idx = parseInt(target.getAttribute('data-index') || '0');
                const val = target.value;
                if (val) {
                    // 1. 填入 Target Model 文本框与底层数据
                    currentTabMappings[idx].targetModel = val;
                    const targetInput = tbody.querySelector(`.target-model-input[data-index="${idx}"]`) as HTMLInputElement | null;
                    if (targetInput) targetInput.value = val;

                    // 2. 若 Client Model 为空，自动联动把选中的模型名同步填入 Client Model 文本框
                    const clientInput = tbody.querySelector(`.client-model-input[data-index="${idx}"]`) as HTMLInputElement | null;
                    if (clientInput && (!clientInput.value || !clientInput.value.trim())) {
                        currentTabMappings[idx].clientModel = val;
                        clientInput.value = val;
                    }
                }
            });
        });

        tbody.querySelectorAll('.inject-kwargs-checkbox').forEach(chk => {
            chk.addEventListener('change', (e) => {
                const target = e.target as HTMLInputElement;
                const idx = parseInt(target.getAttribute('data-index') || '0');
                currentTabMappings[idx].injectChatTemplateKwargs = target.checked;
            });
        });

        tbody.querySelectorAll('.expose-checkbox').forEach(chk => {
            chk.addEventListener('change', (e) => {
                const target = e.target as HTMLInputElement;
                const idx = parseInt(target.getAttribute('data-index') || '0');
                currentTabMappings[idx].expose = target.checked;
            });
        });
    }

    (window as any)._relayAddTab = () => {
        const tabName = prompt('请输入新号池 Tab 名称（例如：DeepSeek 账号池）：');
        if (!tabName || !tabName.trim()) return;

        const providerId = prompt('请输入该 Tab 绑定的号池 Provider 标识（例如：deepseek）：') || tabName.trim().toLowerCase();
        const tabId = 'custom_' + Date.now();

        const newTab: PoolTabInfo = {
            id: tabId,
            name: tabName.trim(),
            targetProvider: providerId.trim().toLowerCase(),
            isCustom: true
        };

        poolTabs.push(newTab);
        activeTabId = tabId;

        renderPoolTabs();
        renderTargetProviderSelector();
        renderCurrentTabTable();
    };

    (window as any)._relayDeleteCurrentTab = () => {
        const currentTab = poolTabs.find(t => t.id === activeTabId);
        if (!currentTab || !currentTab.isCustom) return;

        if (!confirm(`确定要删除号池 Tab「${currentTab.name}」及其下的所有模型映射吗？`)) return;

        // 删除属于该 Tab 的所有映射
        allMappings = allMappings.filter(m => getMappingTab(m) !== activeTabId);
        poolTabs = poolTabs.filter(t => t.id !== activeTabId);

        activeTabId = poolTabs[0]?.id || 'google';

        renderPoolTabs();
        renderTargetProviderSelector();
        renderCurrentTabTable();
    };

    (window as any)._relayAddModelMapping = () => {
        const currentTab = poolTabs.find(t => t.id === activeTabId);
        const targetProv = currentTab ? currentTab.targetProvider : '';

        allMappings.unshift({
            clientModel: '',
            targetModel: '',
            expose: true,
            injectChatTemplateKwargs: true,
            ownedBy: activeTabId,
            targetProvider: targetProv
        });

        renderCurrentTabTable();
    };

    (window as any)._relaySaveModelMapping = async () => {
        const btnSaveModelMapping = document.getElementById('btnSaveModelMapping');
        if (!btnSaveModelMapping) return;
        const originalText = btnSaveModelMapping.innerHTML;
        const dict = i18n[state.currentLanguage] || {};
        btnSaveModelMapping.innerHTML = `<span class="material-symbols-outlined text-[16px] animate-spin">sync</span><span>${dict.relaySaving || '保存中...'}</span>`;

        // 整理补齐所有 mappings 的 ownedBy 与 targetProvider
        allMappings.forEach(m => {
            const tabId = getMappingTab(m);
            const tabObj = poolTabs.find(t => t.id === tabId);
            if (!m.ownedBy) m.ownedBy = tabId;
            if (tabObj && tabObj.targetProvider) {
                m.targetProvider = tabObj.targetProvider;
            }
        });

        // 过滤空映射
        const filteredMappings = allMappings.filter(m => m.clientModel && m.clientModel.trim() !== '' && m.targetModel && m.targetModel.trim() !== '');

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

// Listen for relay config updates globally (only once when module loads)
ipcRenderer.on('relay-state', (_e: any, config: any) => {
    const chkRelayEnabled = document.getElementById('chkRelayEnabled') as HTMLInputElement | null;
    const relayPortInput = document.getElementById('relayPortInput') as HTMLInputElement | null;
    if (chkRelayEnabled) chkRelayEnabled.checked = !!config?.enabled;
    if (relayPortInput) relayPortInput.value = config?.port || '18444';
});
