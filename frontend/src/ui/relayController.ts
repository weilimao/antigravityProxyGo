import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';

let relayUsers: any[] = [];

export function initRelayEvents() {
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

    // Load persisted users and packages on init
    refreshRelayUsers();
    refreshRelayPackages();

    // Fetch initial config state to sync UI
    ipcRenderer.invoke('relay:get-config')
        .then((config: any) => {
            if (chkRelayEnabled) chkRelayEnabled.checked = !!config?.enabled;
            if (relayPortInput) relayPortInput.value = config?.port || '18444';
        })
        .catch((err: any) => console.error('[RelayController] Failed to get initial config:', err));
}

let relayPackages: any[] = [];

export async function refreshRelayPackages() {
    try {
        const pkgs = await ipcRenderer.invoke('relay:get-packages');
        relayPackages = pkgs || [];
        renderRelayPackages();
    } catch (err) {
        console.error('[RelayController] Failed to get packages:', err);
    }
}

function formatTokenCount(val: number): string {
    if (!val) return '0';
    if (val >= 100000000) {
        return (val / 100000000) + '亿';
    }
    if (val >= 10000) {
        return (val / 10000) + '万';
    }
    return val.toString();
}

function formatQuotaSummary(q: any): string {
    if (!q) return '不限制';
    let parts: string[] = [];
    if (q.enableFixed && q.fixedTokens > 0) parts.push(`总量 ${formatTokenCount(q.fixedTokens)}`);
    if (q.enableHourly && q.hourlyHours > 0) parts.push(`${q.hourlyHours}小时 ${formatTokenCount(q.hourlyTokens)}`);
    if (q.enableDaily && q.dailyDays > 0) parts.push(`${q.dailyDays}天 ${formatTokenCount(q.dailyTokens)}`);
    if (parts.length === 0) return '不限制';
    return parts.join(' | ');
}

function renderRelayPackages() {
    const container = document.getElementById('relayPackagesList');
    if (!container) return;

    if (relayPackages.length === 0) {
        container.innerHTML = `<div class="col-span-full text-center text-outline/60 py-6 text-[13px]">暂无套餐，请新建套餐</div>`;
        return;
    }

    container.innerHTML = relayPackages.map(pkg => {
        const q = pkg.quotas || {};
        let validStr = '永久';
        if (q.validDuration > 0) {
            const unitMap: any = { days: '天', months: '个月', years: '年' };
            validStr = `${q.validDuration}${unitMap[q.validUnit] || '天'}`;
        }
        
        return `
            <div class="p-4 rounded-xl border border-outline-variant/30 bg-white/50 dark:bg-[#1a2033] hover:border-primary/50 transition-all cursor-pointer shadow-sm flex flex-col justify-between group" onclick="window._relayOpenPackageSettings('${pkg.id}')">
                <div>
                    <div class="flex items-center justify-between mb-3">
                        <div class="flex items-center gap-2">
                            <span class="text-[14px] font-bold text-on-surface dark:text-white group-hover:text-primary transition-colors">${pkg.name}</span>
                            <span class="text-[10px] bg-primary/10 text-primary px-2 py-0.5 rounded-full font-medium">有效期: ${validStr}</span>
                        </div>
                        <button class="text-outline hover:text-red-500 transition-colors p-1" onclick="event.stopPropagation(); window._relayDeletePackage('${pkg.id}')">
                            <span class="material-symbols-outlined text-[16px]">delete</span>
                        </button>
                    </div>
                    <div class="space-y-2 text-[12px]">
                        <div class="flex items-center justify-between bg-outline-variant/5 p-2 rounded-lg border border-outline-variant/10">
                            <span class="text-outline font-medium">Gemini系列</span>
                            <span class="text-on-surface dark:text-slate-200 font-bold">${formatQuotaSummary(q.gemini)}</span>
                        </div>
                        <div class="flex items-center justify-between bg-outline-variant/5 p-2 rounded-lg border border-outline-variant/10">
                            <span class="text-outline font-medium">Claude系列</span>
                            <span class="text-on-surface dark:text-slate-200 font-bold">${formatQuotaSummary(q.claude)}</span>
                        </div>
                    </div>
                </div>
                <div class="mt-3 text-[11px] text-outline text-right flex items-center justify-end gap-1 group-hover:text-primary transition-colors">
                    <span>点击修改配置</span>
                    <span class="material-symbols-outlined text-[14px]">edit</span>
                </div>
            </div>
        `;
    }).join('');
    
    // Also re-render dynamic quota presets inside the modal
    const presetsContainer = document.getElementById('dynamicQuotaPresets');
    if (presetsContainer) {
        presetsContainer.innerHTML = relayPackages.map(pkg => `
            <button data-pkg-id="${pkg.id}" onclick="window._relayApplyDynamicPreset('${pkg.id}')" class="flex-1 py-1.5 px-2 text-[12px] bg-white dark:bg-slate-800 border border-outline-variant/30 hover:border-primary/60 text-on-surface rounded-lg transition-all">${pkg.name}</button>
        `).join('');
    }
}

export async function refreshRelayUsers() {
    try {
        const users = await ipcRenderer.invoke('relay:get-users');
        relayUsers = users || [];
        renderRelayUsers();
    } catch (err) {
        console.error('[RelayController] Failed to get users:', err);
    }
}

function renderRelayUsers() {
    const container = document.getElementById('relayUsersList');
    if (!container) return;

    if (relayUsers.length === 0) {
        container.innerHTML = `<div class="text-center text-outline/60 py-8 text-[13px]">暂无中继用户，请点击上方按钮添加</div>`;
        return;
    }

    container.innerHTML = relayUsers.map(user => {
        const q = user.quotas || {};
        let pkgName = '无限制';
        let matched = false;
        
        if (relayPackages && relayPackages.length > 0) {
            for (const pkg of relayPackages) {
                if (pkg && pkg.quotas) {
                    const q1 = q;
                    const q2 = pkg.quotas;
                    const check = (f: 'gemini' | 'claude') => {
                        if (!q1[f] || !q2[f]) return false;
                        return !!q1[f].enableFixed === !!q2[f].enableFixed &&
                               (q1[f].fixedTokens || 0) === (q2[f].fixedTokens || 0) &&
                               !!q1[f].enableHourly === !!q2[f].enableHourly &&
                               (q1[f].hourlyHours || 0) === (q2[f].hourlyHours || 0) &&
                               (q1[f].hourlyTokens || 0) === (q2[f].hourlyTokens || 0) &&
                               !!q1[f].enableDaily === !!q2[f].enableDaily &&
                               (q1[f].dailyDays || 0) === (q2[f].dailyDays || 0) &&
                               (q1[f].dailyTokens || 0) === (q2[f].dailyTokens || 0);
                    };
                    if (check('gemini') && check('claude') && (q1.validDuration || 0) === (q2.validDuration || 0) && (q1.validUnit || 'months') === (q2.validUnit || 'months')) {
                        pkgName = pkg.name;
                        matched = true;
                        break;
                    }
                }
            }
        }
        
        if (!matched && (q.gemini?.enableFixed || q.gemini?.enableHourly || q.gemini?.enableDaily || q.claude?.enableFixed || q.claude?.enableHourly || q.claude?.enableDaily)) {
            pkgName = '自定义限额';
        }

        let expireStr = '永久有效';
        let durationStr = '';
        if (q.validDuration > 0) {
            const unitMap: any = { days: '天', months: '个月', years: '年' };
            durationStr = ` (${q.validDuration}${unitMap[q.validUnit] || '天'})`;
        }
        if (q.expireAt > 0) {
            const expDate = new Date(q.expireAt * 1000);
            const isExpired = Date.now() > expDate.getTime();
            expireStr = `${expDate.toLocaleDateString()} 到期`;
            if (isExpired) {
                expireStr = `<span class="text-red-500 font-bold">已于 ${expDate.toLocaleDateString()} 到期</span>`;
            }
        }

        return `
            <div class="flex items-center justify-between p-3 rounded-lg border border-outline-variant/20 bg-white/50 dark:bg-white/5 mb-2 hover:border-primary/30 transition-colors">
                <div class="flex items-center gap-3">
                    <div class="w-2 h-2 rounded-full ${user.enabled ? 'bg-emerald-500' : 'bg-slate-300 dark:bg-slate-600'}"></div>
                    <div>
                        <div class="flex items-center gap-2 flex-wrap">
                            <span class="text-[13px] font-semibold text-on-surface dark:text-white">${user.key}</span>
                            <span class="text-[10px] px-2 py-0.5 rounded font-medium bg-indigo-500/10 text-indigo-500 border border-indigo-500/20">${pkgName}${durationStr}</span>
                            <span class="text-[10px] px-2 py-0.5 rounded font-medium bg-outline-variant/10 text-outline">${expireStr}</span>
                        </div>
                        <div class="text-[11px] text-outline/60 mt-1">${user.remark || '无备注'} · 创建于 ${new Date(user.createdAt).toLocaleDateString()}</div>
                    </div>
                </div>
                <div class="flex items-center gap-2">
                    <label class="relative inline-block w-8 h-4 cursor-pointer">
                        <input type="checkbox" class="sr-only peer" ${user.enabled ? 'checked' : ''}
                            onchange="window._relayToggleUser('${user.id}', this.checked)" />
                        <div class="w-8 h-4 bg-slate-300 dark:bg-slate-600 rounded-full peer-checked:bg-emerald-500 transition-colors"></div>
                        <div class="absolute left-0.5 top-0.5 w-3 h-3 bg-white rounded-full transition-transform peer-checked:translate-x-4"></div>
                    </label>
                    <button onclick="window._relayOpenQuotaSettings('${user.id}')" 
                        class="text-indigo-400 hover:text-indigo-600 transition-colors p-1" title="限额配置">
                        <span class="material-symbols-outlined text-[16px]">settings</span>
                    </button>
                    <button onclick="window._relayViewUserStats('${user.id}')" 
                        class="text-primary hover:text-primary/80 transition-colors p-1" title="查看数据">
                        <span class="material-symbols-outlined text-[16px]">bar_chart</span>
                    </button>
                    <button onclick="window._relayRemoveUser('${user.id}')" 
                        class="text-red-400 hover:text-red-600 transition-colors p-1" title="删除">
                        <span class="material-symbols-outlined text-[16px]">delete</span>
                    </button>
                </div>
            </div>
        `;
    }).join('');
}

// Global handlers for inline onclick
(window as any).refreshRelayUsers = refreshRelayUsers;

(window as any)._relayToggleUser = async (id: string, enabled: boolean) => {
    try {
        await ipcRenderer.invoke('relay:toggle-user', id, enabled);
        await refreshRelayUsers();
    } catch (err) {
        console.error('[RelayController] Failed to toggle user:', err);
    }
};

(window as any)._relayRemoveUser = async (id: string) => {
    if (!confirm('确定要删除该中继用户吗？')) return;
    try {
        await ipcRenderer.invoke('relay:remove-user', id);
        await refreshRelayUsers();
    } catch (err) {
        console.error('[RelayController] Failed to remove user:', err);
    }
};

(window as any)._relayOpenQuotaSettings = (id: string) => {
    const user = relayUsers.find(u => u.id === id);
    if (!user) return;
    
    (document.getElementById('quotaUserId') as HTMLInputElement).value = id;
    (document.getElementById('quotaPackageId') as HTMLInputElement).value = '';
    document.getElementById('quotaPackageNameContainer')?.classList.add('hidden');
    document.getElementById('quotaPresetsContainer')?.classList.remove('hidden');
    (document.getElementById('relayQuotaModalTitle') as HTMLElement).innerText = '用户限额配置';
    
    const quotas = user.quotas || {
        gemini: { enableFixed: false, fixedTokens: 0, enableHourly: false, hourlyHours: 0, hourlyTokens: 0, enableDaily: false, dailyDays: 0, dailyTokens: 0 },
        claude: { enableFixed: false, fixedTokens: 0, enableHourly: false, hourlyHours: 0, hourlyTokens: 0, enableDaily: false, dailyDays: 0, dailyTokens: 0 },
        validDuration: 0,
        validUnit: 'months'
    };
    
    (document.getElementById('quotaValidDuration') as HTMLInputElement).value = quotas.validDuration?.toString() || '0';
    (document.getElementById('quotaValidUnit') as HTMLInputElement).value = quotas.validUnit || 'months';
    
    const setupForm = (family: 'gemini' | 'claude') => {
        const q = quotas[family];
        (document.getElementById(`${family}EnableFixed`) as HTMLInputElement).checked = !!q.enableFixed;
        (document.getElementById(`${family}FixedTokens`) as HTMLInputElement).value = q.fixedTokens?.toString() || '';

        (document.getElementById(`${family}EnableHourly`) as HTMLInputElement).checked = !!q.enableHourly;
        (document.getElementById(`${family}HourlyHours`) as HTMLInputElement).value = q.hourlyHours?.toString() || '';
        (document.getElementById(`${family}HourlyTokens`) as HTMLInputElement).value = q.hourlyTokens?.toString() || '';

        (document.getElementById(`${family}EnableDaily`) as HTMLInputElement).checked = !!q.enableDaily;
        (document.getElementById(`${family}DailyDays`) as HTMLInputElement).value = q.dailyDays?.toString() || '';
        (document.getElementById(`${family}DailyTokens`) as HTMLInputElement).value = q.dailyTokens?.toString() || '';
    };
    
    setupForm('gemini');
    setupForm('claude');
    
    // Check if current user quotas match any package
    const presetsContainer = document.getElementById('dynamicQuotaPresets');
    if (presetsContainer) {
        const buttons = presetsContainer.querySelectorAll('button');
        buttons.forEach(btn => {
            const pkgId = btn.getAttribute('data-pkg-id');
            const pkg = relayPackages.find(p => p.id === pkgId);
            let matched = false;
            if (pkg && pkg.quotas) {
                const q1 = quotas;
                const q2 = pkg.quotas;
                const check = (f: 'gemini' | 'claude') => {
                    if (!q1[f] || !q2[f]) return false;
                    return !!q1[f].enableFixed === !!q2[f].enableFixed &&
                           (q1[f].fixedTokens || 0) === (q2[f].fixedTokens || 0) &&
                           !!q1[f].enableHourly === !!q2[f].enableHourly &&
                           (q1[f].hourlyHours || 0) === (q2[f].hourlyHours || 0) &&
                           (q1[f].hourlyTokens || 0) === (q2[f].hourlyTokens || 0) &&
                           !!q1[f].enableDaily === !!q2[f].enableDaily &&
                           (q1[f].dailyDays || 0) === (q2[f].dailyDays || 0) &&
                           (q1[f].dailyTokens || 0) === (q2[f].dailyTokens || 0);
                };
                matched = check('gemini') && check('claude') && (q1.validDuration || 0) === (q2.validDuration || 0) && (q1.validUnit || 'months') === (q2.validUnit || 'months');
            }
            if (matched) {
                btn.className = "flex-1 py-1.5 px-2 text-[12px] bg-primary/10 dark:bg-primary/20 border-2 border-primary text-primary font-bold rounded-lg transition-all shadow-sm";
            } else {
                btn.className = "flex-1 py-1.5 px-2 text-[12px] bg-white dark:bg-slate-800 border border-outline-variant/30 hover:border-primary/60 text-on-surface rounded-lg transition-all";
            }
        });
    }
    
    document.getElementById('relayUserQuotaModal')?.classList.remove('hidden');
};

(window as any)._relayApplyQuotaPreset = (preset: string) => {
    // defaults
    let hourlyHours = 5, hourlyTokens = 0, dailyDays = 7, dailyTokens = 0;
    
    if (preset === 'pro') {
        hourlyTokens = 50000;
        dailyTokens = 500000;
    } else if (preset === 'pro5x') {
        hourlyTokens = 250000;
        dailyTokens = 2500000;
    } else if (preset === 'pro20x') {
        hourlyTokens = 1000000;
        dailyTokens = 10000000;
    }

    const apply = (family: 'gemini' | 'claude') => {
        (document.getElementById(`${family}EnableFixed`) as HTMLInputElement).checked = false;
        (document.getElementById(`${family}FixedTokens`) as HTMLInputElement).value = '0';

        if (preset === 'unlimited') {
            (document.getElementById(`${family}EnableHourly`) as HTMLInputElement).checked = false;
            (document.getElementById(`${family}HourlyHours`) as HTMLInputElement).value = '0';
            (document.getElementById(`${family}HourlyTokens`) as HTMLInputElement).value = '0';

            (document.getElementById(`${family}EnableDaily`) as HTMLInputElement).checked = false;
            (document.getElementById(`${family}DailyDays`) as HTMLInputElement).value = '0';
            (document.getElementById(`${family}DailyTokens`) as HTMLInputElement).value = '0';
        } else {
            (document.getElementById(`${family}EnableHourly`) as HTMLInputElement).checked = true;
            (document.getElementById(`${family}HourlyHours`) as HTMLInputElement).value = hourlyHours.toString();
            (document.getElementById(`${family}HourlyTokens`) as HTMLInputElement).value = hourlyTokens.toString();

            (document.getElementById(`${family}EnableDaily`) as HTMLInputElement).checked = true;
            (document.getElementById(`${family}DailyDays`) as HTMLInputElement).value = dailyDays.toString();
            (document.getElementById(`${family}DailyTokens`) as HTMLInputElement).value = dailyTokens.toString();
        }
    };
    
    apply('gemini');
    apply('claude');
};

(window as any)._relayApplyDynamicPreset = (pkgId: string) => {
    const pkg = relayPackages.find(p => p.id === pkgId);
    if (!pkg || !pkg.quotas) return;
    
    // Update active button styling
    const presetsContainer = document.getElementById('dynamicQuotaPresets');
    if (presetsContainer) {
        const buttons = presetsContainer.querySelectorAll('button');
        buttons.forEach(btn => {
            if (btn.getAttribute('data-pkg-id') === pkgId) {
                btn.className = "flex-1 py-1.5 px-2 text-[12px] bg-primary/10 dark:bg-primary/20 border-2 border-primary text-primary font-bold rounded-lg transition-all shadow-sm";
            } else {
                btn.className = "flex-1 py-1.5 px-2 text-[12px] bg-white dark:bg-slate-800 border border-outline-variant/30 hover:border-primary/60 text-on-surface rounded-lg transition-all";
            }
        });
    }
    
    (document.getElementById('quotaValidDuration') as HTMLInputElement).value = pkg.quotas?.validDuration?.toString() || '0';
    (document.getElementById('quotaValidUnit') as HTMLInputElement).value = pkg.quotas?.validUnit || 'months';
    
    const apply = (family: 'gemini' | 'claude') => {
        const q = pkg.quotas[family];
        (document.getElementById(`${family}EnableFixed`) as HTMLInputElement).checked = !!q.enableFixed;
        (document.getElementById(`${family}FixedTokens`) as HTMLInputElement).value = q.fixedTokens?.toString() || '';

        (document.getElementById(`${family}EnableHourly`) as HTMLInputElement).checked = !!q.enableHourly;
        (document.getElementById(`${family}HourlyHours`) as HTMLInputElement).value = q.hourlyHours?.toString() || '';
        (document.getElementById(`${family}HourlyTokens`) as HTMLInputElement).value = q.hourlyTokens?.toString() || '';

        (document.getElementById(`${family}EnableDaily`) as HTMLInputElement).checked = !!q.enableDaily;
        (document.getElementById(`${family}DailyDays`) as HTMLInputElement).value = q.dailyDays?.toString() || '';
        (document.getElementById(`${family}DailyTokens`) as HTMLInputElement).value = q.dailyTokens?.toString() || '';
    };
    
    apply('gemini');
    apply('claude');
};

(window as any)._relayOpenPackageSettings = (id: string) => {
    let quotas = {
        gemini: { enableFixed: false, fixedTokens: 0, enableHourly: false, hourlyHours: 0, hourlyTokens: 0, enableDaily: false, dailyDays: 0, dailyTokens: 0 },
        claude: { enableFixed: false, fixedTokens: 0, enableHourly: false, hourlyHours: 0, hourlyTokens: 0, enableDaily: false, dailyDays: 0, dailyTokens: 0 },
        validDuration: 1,
        validUnit: 'months'
    };
    let name = '';

    if (id) {
        const pkg = relayPackages.find(p => p.id === id);
        if (pkg) {
            quotas = pkg.quotas;
            name = pkg.name;
        }
    }

    (document.getElementById('quotaUserId') as HTMLInputElement).value = '';
    (document.getElementById('quotaPackageId') as HTMLInputElement).value = id;
    (document.getElementById('quotaPackageName') as HTMLInputElement).value = name;
    
    document.getElementById('quotaPackageNameContainer')?.classList.remove('hidden');
    document.getElementById('quotaPresetsContainer')?.classList.add('hidden');
    (document.getElementById('relayQuotaModalTitle') as HTMLElement).innerText = id ? '编辑套餐模板' : '新建套餐模板';
    
    (document.getElementById('quotaValidDuration') as HTMLInputElement).value = quotas.validDuration?.toString() || '0';
    (document.getElementById('quotaValidUnit') as HTMLInputElement).value = quotas.validUnit || 'months';

    const setupForm = (family: 'gemini' | 'claude') => {
        const q = quotas[family] || {};
        (document.getElementById(`${family}EnableFixed`) as HTMLInputElement).checked = !!q.enableFixed;
        (document.getElementById(`${family}FixedTokens`) as HTMLInputElement).value = q.fixedTokens?.toString() || '';

        (document.getElementById(`${family}EnableHourly`) as HTMLInputElement).checked = !!q.enableHourly;
        (document.getElementById(`${family}HourlyHours`) as HTMLInputElement).value = q.hourlyHours?.toString() || '';
        (document.getElementById(`${family}HourlyTokens`) as HTMLInputElement).value = q.hourlyTokens?.toString() || '';

        (document.getElementById(`${family}EnableDaily`) as HTMLInputElement).checked = !!q.enableDaily;
        (document.getElementById(`${family}DailyDays`) as HTMLInputElement).value = q.dailyDays?.toString() || '';
        (document.getElementById(`${family}DailyTokens`) as HTMLInputElement).value = q.dailyTokens?.toString() || '';
    };
    
    setupForm('gemini');
    setupForm('claude');
    
    document.getElementById('relayUserQuotaModal')?.classList.remove('hidden');
};

(window as any)._relayDeletePackage = async (id: string) => {
    if (!confirm('确定要删除该套餐模板吗？')) return;
    try {
        await ipcRenderer.invoke('relay:delete-package', id);
        await refreshRelayPackages();
    } catch (err) {
        console.error('Failed to delete package:', err);
    }
};

(window as any)._relaySaveQuota = async () => {
    const userId = (document.getElementById('quotaUserId') as HTMLInputElement).value;
    const isPackageMode = !document.getElementById('quotaPackageNameContainer')?.classList.contains('hidden');
    
    if (!isPackageMode && !userId) return;
    
    const getFormData = (family: 'gemini' | 'claude') => {
        return {
            enableFixed: (document.getElementById(`${family}EnableFixed`) as HTMLInputElement).checked,
            fixedTokens: parseInt((document.getElementById(`${family}FixedTokens`) as HTMLInputElement).value) || 0,
            
            enableHourly: (document.getElementById(`${family}EnableHourly`) as HTMLInputElement).checked,
            hourlyHours: parseInt((document.getElementById(`${family}HourlyHours`) as HTMLInputElement).value) || 0,
            hourlyTokens: parseInt((document.getElementById(`${family}HourlyTokens`) as HTMLInputElement).value) || 0,
            
            enableDaily: (document.getElementById(`${family}EnableDaily`) as HTMLInputElement).checked,
            dailyDays: parseInt((document.getElementById(`${family}DailyDays`) as HTMLInputElement).value) || 0,
            dailyTokens: parseInt((document.getElementById(`${family}DailyTokens`) as HTMLInputElement).value) || 0
        };
    };
    
    const quotas = {
        gemini: getFormData('gemini'),
        claude: getFormData('claude'),
        validDuration: parseInt((document.getElementById('quotaValidDuration') as HTMLInputElement).value) || 0,
        validUnit: (document.getElementById('quotaValidUnit') as HTMLInputElement).value || 'months'
    };
    
    try {
        if (isPackageMode) {
            const pkgId = (document.getElementById('quotaPackageId') as HTMLInputElement).value;
            const pkgName = (document.getElementById('quotaPackageName') as HTMLInputElement).value || '未命名套餐';
            await ipcRenderer.invoke('relay:save-package', { id: pkgId, name: pkgName, quotas });
            await refreshRelayPackages();
        } else {
            await ipcRenderer.invoke('relay:update-user-quota', userId, quotas);
            await refreshRelayUsers();
        }
        document.getElementById('relayUserQuotaModal')?.classList.add('hidden');
    } catch (err) {
        console.error('[RelayController] Failed to save quotas:', err);
        alert('保存限额配置失败');
    }
};

(window as any)._relayViewUserStats = async (id: string) => {
    try {
        const res = await ipcRenderer.invoke('relay:get-user-stats', id);
        const modal = document.getElementById('relayUserStatsModal');
        const content = document.getElementById('relayUserStatsContent');
        if (modal && content) {
            if (!res) {
                content.innerHTML = '<div class="text-[13px] text-outline/60 text-center py-4">暂无数据记录</div>';
            } else {
                const stats = res.stats || {};
                const user = res.user || {};
                const totalTokens = (stats.totalInputTokens || 0) + (stats.totalOutputTokens || 0) + (stats.totalCachedTokens || 0);
                
                let html = `
                    <div class="grid grid-cols-2 gap-3 mb-4">
                        <div class="bg-slate-50 dark:bg-slate-800/50 p-3 rounded-lg border border-outline-variant/20">
                            <div class="text-[11px] text-outline/60 mb-1">总请求数</div>
                            <div class="text-[16px] font-bold text-on-surface dark:text-white">${stats.totalRequests || 0}</div>
                        </div>
                        <div class="bg-slate-50 dark:bg-slate-800/50 p-3 rounded-lg border border-outline-variant/20">
                            <div class="text-[11px] text-outline/60 mb-1">总花费</div>
                            <div class="text-[16px] font-bold text-emerald-500">$${(stats.totalCost || 0).toFixed(4)}</div>
                        </div>
                        <div class="bg-slate-50 dark:bg-slate-800/50 p-3 rounded-lg border border-outline-variant/20 col-span-2 flex items-center justify-between">
                            <div>
                                <div class="text-[11px] text-outline/60 mb-1">总 Token 数</div>
                                <div class="text-[16px] font-bold text-indigo-500">${totalTokens}</div>
                            </div>
                            <div class="text-right">
                                <div class="text-[11px] text-outline/60 mb-1">缓存命中</div>
                                <div class="text-[16px] font-bold text-teal-500">${stats.totalCachedTokens || 0}</div>
                            </div>
                        </div>
                        <div class="bg-slate-50 dark:bg-slate-800/50 p-3 rounded-lg border border-outline-variant/20">
                            <div class="text-[11px] text-outline/60 mb-1">输入 Token</div>
                            <div class="text-[16px] font-bold text-blue-500">${stats.totalInputTokens || 0}</div>
                        </div>
                        <div class="bg-slate-50 dark:bg-slate-800/50 p-3 rounded-lg border border-outline-variant/20">
                            <div class="text-[11px] text-outline/60 mb-1">输出 Token</div>
                            <div class="text-[16px] font-bold text-purple-500">${stats.totalOutputTokens || 0}</div>
                        </div>
                    </div>
                `;

                if (user && user.quotas) {
                    const q = user.quotas;
                    const renderFamilyQuota = (familyTitle: string, quota: any, lifetimeUsed: number, hourlyUsed: number, dailyUsed: number, hourlyResetAt?: string, dailyResetAt?: string) => {
                        if (!quota || (!quota.enableFixed && !quota.enableHourly && !quota.enableDaily)) {
                            return `
                                <div class="bg-white/50 dark:bg-white/5 p-3 rounded-lg border border-outline-variant/20 text-[12px] mb-3">
                                    <div class="font-semibold text-on-surface dark:text-white mb-1">${familyTitle} <span class="text-[10px] text-emerald-500 font-normal bg-emerald-500/10 px-2 py-0.5 rounded">无限制</span></div>
                                    <div class="text-outline/70 text-[11px]">当前已用总计: ${formatTokenCount(lifetimeUsed)} Token</div>
                                </div>
                            `;
                        }

                        let items: string[] = [];
                        if (quota.enableFixed) {
                            const remain = Math.max(0, quota.fixedTokens - lifetimeUsed);
                            const pct = Math.min(100, Math.round((lifetimeUsed / quota.fixedTokens) * 100));
                            items.push(`
                                <div class="mb-2">
                                    <div class="flex justify-between text-[11px] mb-1">
                                        <span class="text-outline">总配额限制 (${formatTokenCount(quota.fixedTokens)})</span>
                                        <span class="font-bold ${remain > 0 ? 'text-indigo-500' : 'text-red-500'}">剩余: ${formatTokenCount(remain)}</span>
                                    </div>
                                    <div class="w-full bg-slate-200 dark:bg-slate-700 h-1.5 rounded-full overflow-hidden">
                                        <div class="bg-indigo-500 h-full rounded-full" style="width: ${pct}%"></div>
                                    </div>
                                </div>
                            `);
                        }
                        if (quota.enableHourly) {
                            const remain = Math.max(0, quota.hourlyTokens - hourlyUsed);
                            const pct = Math.min(100, Math.round((hourlyUsed / quota.hourlyTokens) * 100));
                            let resetStr = '';
                            if (hourlyUsed > 0 && hourlyResetAt) {
                                const d = new Date(hourlyResetAt);
                                const timeStr = `${d.getHours().toString().padStart(2, '0')}:${d.getMinutes().toString().padStart(2, '0')}`;
                                resetStr = ` <span class="text-[10px] text-primary bg-primary/10 px-1.5 py-0.5 rounded ml-1 font-normal">预计 ${timeStr} 刷新</span>`;
                            }
                            items.push(`
                                <div class="mb-2">
                                    <div class="flex justify-between text-[11px] mb-1 items-center">
                                        <span class="text-outline flex items-center">${quota.hourlyHours}小时级限额 (${formatTokenCount(quota.hourlyTokens)})${resetStr}</span>
                                        <span class="font-bold ${remain > 0 ? 'text-primary' : 'text-red-500'}">剩余: ${formatTokenCount(remain)}</span>
                                    </div>
                                    <div class="w-full bg-slate-200 dark:bg-slate-700 h-1.5 rounded-full overflow-hidden">
                                        <div class="bg-primary h-full rounded-full" style="width: ${pct}%"></div>
                                    </div>
                                </div>
                            `);
                        }
                        if (quota.enableDaily) {
                            const remain = Math.max(0, quota.dailyTokens - dailyUsed);
                            const pct = Math.min(100, Math.round((dailyUsed / quota.dailyTokens) * 100));
                            let resetStr = '';
                            if (dailyUsed > 0 && dailyResetAt) {
                                const d = new Date(dailyResetAt);
                                const month = (d.getMonth() + 1).toString().padStart(2, '0');
                                const day = d.getDate().toString().padStart(2, '0');
                                const hours = d.getHours().toString().padStart(2, '0');
                                const minutes = d.getMinutes().toString().padStart(2, '0');
                                resetStr = ` <span class="text-[10px] text-emerald-600 dark:text-emerald-400 bg-emerald-500/10 px-1.5 py-0.5 rounded ml-1 font-normal">预计 ${month}-${day} ${hours}:${minutes} 刷新</span>`;
                            }
                            items.push(`
                                <div class="mb-2">
                                    <div class="flex justify-between text-[11px] mb-1 items-center">
                                        <span class="text-outline flex items-center">${quota.dailyDays}天级限额 (${formatTokenCount(quota.dailyTokens)})${resetStr}</span>
                                        <span class="font-bold ${remain > 0 ? 'text-emerald-500' : 'text-red-500'}">剩余: ${formatTokenCount(remain)}</span>
                                    </div>
                                    <div class="w-full bg-slate-200 dark:bg-slate-700 h-1.5 rounded-full overflow-hidden">
                                        <div class="bg-emerald-500 h-full rounded-full" style="width: ${pct}%"></div>
                                    </div>
                                </div>
                            `);
                        }

                        return `
                            <div class="bg-white/50 dark:bg-white/5 p-3 rounded-lg border border-outline-variant/20 text-[12px] mb-3">
                                <div class="font-semibold text-on-surface dark:text-white mb-2 flex items-center justify-between">
                                    <span>${familyTitle}</span>
                                    <span class="text-[10px] text-outline font-normal">已用: ${formatTokenCount(lifetimeUsed)}</span>
                                </div>
                                ${items.join('')}
                            </div>
                        `;
                    };

                    html += `<div class="text-[12px] font-bold mb-2 mt-4 text-on-surface dark:text-white">用户剩余用量实时追踪</div>`;
                    html += renderFamilyQuota('Gemini 系列模型', q.gemini, res.geminiLifetime || 0, res.geminiHourlyUsed || 0, res.geminiDailyUsed || 0, res.geminiHourlyResetAt, res.geminiDailyResetAt);
                    html += renderFamilyQuota('Claude 系列模型', q.claude, res.claudeLifetime || 0, res.claudeHourlyUsed || 0, res.claudeDailyUsed || 0, res.claudeHourlyResetAt, res.claudeDailyResetAt);
                }
                
                if (stats.models && Object.keys(stats.models).length > 0) {
                    html += '<div class="text-[12px] font-bold mb-2 mt-4 text-on-surface dark:text-white">按模型统计</div>';
                    for (const [modelName, modelStats] of Object.entries<any>(stats.models)) {
                        const modelTotalTokens = (modelStats.inputTokens || 0) + (modelStats.outputTokens || 0) + (modelStats.cachedTokens || 0);
                        html += `
                            <div class="bg-white/50 dark:bg-white/5 p-3 rounded-lg border border-outline-variant/20 text-[12px] mb-2">
                                <div class="font-semibold text-primary mb-2">${modelName}</div>
                                <div class="grid grid-cols-3 gap-2 text-outline/80">
                                    <span>请求: ${modelStats.requestCount}</span>
                                    <span>Token: ${modelTotalTokens}</span>
                                    <span class="text-right">花费: $${(modelStats.totalCost || 0).toFixed(4)}</span>
                                </div>
                            </div>
                        `;
                    }
                }
                
                content.innerHTML = html;
            }
            modal.classList.remove('hidden');
        }
    } catch (err) {
        console.error('[RelayController] Failed to view user stats:', err);
    }
};

function openAddUserModal() {
    const modal = document.getElementById('relayUserModal');
    if (modal) modal.classList.remove('hidden');
    // Clear inputs
    const keyInput = document.getElementById('relayUserKeyInput') as HTMLInputElement;
    const pwdInput = document.getElementById('relayUserPasswordInput') as HTMLInputElement;
    const remarkInput = document.getElementById('relayUserRemarkInput') as HTMLInputElement;
    if (keyInput) keyInput.value = '';
    if (pwdInput) pwdInput.value = '';
    if (remarkInput) remarkInput.value = '';
}

function closeAddUserModal() {
    const modal = document.getElementById('relayUserModal');
    if (modal) modal.classList.add('hidden');
}

async function handleAddUser() {
    const keyInput = document.getElementById('relayUserKeyInput') as HTMLInputElement;
    const pwdInput = document.getElementById('relayUserPasswordInput') as HTMLInputElement;
    const remarkInput = document.getElementById('relayUserRemarkInput') as HTMLInputElement;
    
    const key = keyInput?.value?.trim();
    const password = pwdInput?.value;
    const remark = remarkInput?.value?.trim() || '';
    
    if (!key || !password) {
        alert('Key 和密码不能为空');
        return;
    }
    
    try {
        const res = await ipcRenderer.invoke('relay:add-user', key, password, remark);
        if (res?.success) {
            closeAddUserModal();
            await refreshRelayUsers();
        } else {
            alert(res?.error || '添加失败');
        }
    } catch (err) {
        console.error('[RelayController] Failed to add user:', err);
        alert('添加用户失败');
    }
}
