import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';
import i18n from '../shared/i18n';
import { relayPackages, refreshRelayPackages, formatTokenCount } from './relayPackages';

export let relayUsers: any[] = [];
export let currentPage = 1;
export const pageSize = 10;
export let totalUsersCount = 0;
export let currentSearchQuery = '';
export let currentPackageFilter = 'all';

export function setCurrentPage(val: number) {
    currentPage = val;
}
export function setCurrentSearchQuery(val: string) {
    currentSearchQuery = val;
}
export function setCurrentPackageFilter(val: string) {
    currentPackageFilter = val;
}

export async function refreshRelayUsers() {
    try {
        const res = await ipcRenderer.invoke('relay:get-users', {
            page: currentPage,
            pageSize: pageSize,
            search: currentSearchQuery,
            packageTag: currentPackageFilter
        });
        relayUsers = res?.users || [];
        totalUsersCount = res?.total || 0;

        renderRelayUsers();
        renderPagination();
    } catch (err) {
        console.error('[RelayController] Failed to get users:', err);
    }
}

function renderPagination() {
    const info = document.getElementById('relayUserPaginationInfo');
    const pageNum = document.getElementById('relayUserCurrentPage');
    const btnPrev = document.getElementById('btnRelayUserPrevPage') as HTMLButtonElement;
    const btnNext = document.getElementById('btnRelayUserNextPage') as HTMLButtonElement;

    if (pageNum) pageNum.innerText = currentPage.toString();

    const totalPages = Math.ceil(totalUsersCount / pageSize) || 1;
    if (currentPage > totalPages) {
        currentPage = totalPages;
        refreshRelayUsers();
        return;
    }

    if (btnPrev) btnPrev.disabled = currentPage <= 1;
    if (btnNext) btnNext.disabled = currentPage >= totalPages;

    if (info) {
        const dict = i18n[state.currentLanguage] || {};
        if (totalUsersCount === 0) {
            let emptyText = dict.relayUserCountText || '共 {total} 个用户';
            info.innerText = emptyText.replace('{total}', '0');
        } else {
            const start = (currentPage - 1) * pageSize + 1;
            const end = Math.min(currentPage * pageSize, totalUsersCount);
            let pageText = dict.relayUserPaginationText || '显示第 {start} - {end} 个用户，共 {total} 个';
            pageText = pageText.replace('{start}', String(start)).replace('{end}', String(end)).replace('{total}', String(totalUsersCount));
            info.innerText = pageText;
        }
    }
}

function renderRelayUsers() {
    const container = document.getElementById('relayUsersList');
    if (!container) return;
    const dict = i18n[state.currentLanguage] || {};

    if (relayUsers.length === 0) {
        container.innerHTML = `<div class="text-center text-outline/60 py-8 text-[13px]">${dict.relayUserListEmpty || '暂无中继用户，请点击上方按钮添加'}</div>`;
        return;
    }

    container.innerHTML = relayUsers.map(user => {
        const q = user.quotas || {};
        const isZH = state.currentLanguage === 'zh';
        let pkgName = dict.relayNoLimit || '无限制';
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
                    if (check('gemini') && check('claude') && (q1.validDuration || 0) === (q2.validDuration || 0) && (q1.validUnit || 'months') === (q2.validUnit || 'months') && (q1.rateLimit || 30) === (q2.rateLimit || 30)) {
                        pkgName = pkg.name;
                        matched = true;
                        break;
                    }
                }
            }
        }
        
        if (!matched) {
            const hasAnyQuota = q.gemini?.enableFixed || q.gemini?.enableHourly || q.gemini?.enableDaily ||
                                q.claude?.enableFixed || q.claude?.enableHourly || q.claude?.enableDaily;
            if (hasAnyQuota) {
                pkgName = dict.relayFilterCustom || '自定义限额';
            } else {
                pkgName = dict.relayNoPermission || '无权限';
            }
        }

        let expireStr = dict.relayLifetime || (isZH ? '永久有效' : 'Lifetime');
        let durationStr = '';
        if (q.validDuration > 0) {
            const unitMap: any = {
                days: dict.relayDaysUnit || (isZH ? '天' : 'days'),
                months: dict.relayMonthsUnit || (isZH ? '个月' : 'months'),
                years: dict.relayYearsUnit || (isZH ? '年' : 'years')
            };
            durationStr = ` (${q.validDuration}${unitMap[q.validUnit] || (isZH ? '天' : 'days')})`;
        }
        if (q.expireAt > 0) {
            const expDate = new Date(q.expireAt * 1000);
            const isExpired = Date.now() > expDate.getTime();
            const dateStr = expDate.toLocaleDateString();
            if (isExpired) {
                let text = dict.relayExpiredOn || (isZH ? '已于 {date} 到期' : 'Expired on {date}');
                text = text.replace('{date}', dateStr);
                expireStr = `<span class="text-red-500 font-bold">${text}</span>`;
            } else {
                let text = dict.relayExpiresAt || (isZH ? '{date} 到期' : 'Expires at {date}');
                expireStr = text.replace('{date}', dateStr);
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
                        <div class="text-[11px] text-outline/60 mt-1">${user.remark || (dict.relayNoRemark || '无备注')} · ${dict.relayCreatedAt || '创建于'} ${new Date(user.createdAt).toLocaleDateString()}</div>
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
                        class="text-indigo-400 hover:text-indigo-600 transition-colors p-1" title="${isZH ? '限额配置' : 'Quota Config'}">
                        <span class="material-symbols-outlined text-[16px]">settings</span>
                    </button>
                    <button onclick="window._relayViewUserStats('${user.id}')" 
                        class="text-primary hover:text-primary/80 transition-colors p-1" title="${isZH ? '查看数据' : 'View Stats'}">
                        <span class="material-symbols-outlined text-[16px]">bar_chart</span>
                    </button>
                    <button onclick="window._relayRemoveUser('${user.id}')" 
                        class="text-red-400 hover:text-red-600 transition-colors p-1" title="${isZH ? '删除' : 'Delete'}">
                        <span class="material-symbols-outlined text-[16px]">delete</span>
                    </button>
                </div>
            </div>
        `;
    }).join('');
}

// Bind handlers to window
(window as any)._relayToggleUser = async (id: string, enabled: boolean) => {
    try {
        await ipcRenderer.invoke('relay:toggle-user', id, enabled);
        await refreshRelayUsers();
    } catch (err) {
        console.error('[RelayController] Failed to toggle user:', err);
    }
};

(window as any)._relayRemoveUser = async (id: string) => {
    const $confirm = (window as any).$confirm;
    const dict = i18n[state.currentLanguage] || {};
    if ($confirm && !await $confirm(dict.relayDeleteUserConfirm || '确定要删除该中继用户吗？')) return;
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
    document.getElementById('quotaResetContainer')?.classList.remove('hidden');
    const resetCheckbox = document.getElementById('quotaResetLimit') as HTMLInputElement;
    if (resetCheckbox) resetCheckbox.checked = false;
    const dict = i18n[state.currentLanguage] || {};
    (document.getElementById('relayQuotaModalTitle') as HTMLElement).innerText = dict.relayQuotaModalTitleUser || '用户限额配置';
    
    const quotas = user.quotas || {
        gemini: { enableFixed: false, fixedTokens: 0, enableHourly: false, hourlyHours: 0, hourlyTokens: 0, enableDaily: false, dailyDays: 0, dailyTokens: 0 },
        claude: { enableFixed: false, fixedTokens: 0, enableHourly: false, hourlyHours: 0, hourlyTokens: 0, enableDaily: false, dailyDays: 0, dailyTokens: 0 },
        validDuration: 0,
        validUnit: 'months',
        rateLimit: 30
    };
    
    (document.getElementById('quotaValidDuration') as HTMLInputElement).value = quotas.validDuration?.toString() || '0';
    (document.getElementById('quotaValidUnit') as HTMLInputElement).value = quotas.validUnit || 'months';
    (document.getElementById('quotaRateLimit') as HTMLInputElement).value = quotas.rateLimit?.toString() || '30';
    
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
                matched = check('gemini') && check('claude') && (q1.validDuration || 0) === (q2.validDuration || 0) && (q1.validUnit || 'months') === (q2.validUnit || 'months') && (q1.rateLimit || 30) === (q2.rateLimit || 30);
            }
            if (matched) {
                btn.className = "flex-1 py-1.5 px-2 text-[12px] bg-primary/10 dark:bg-primary/20 border-2 border-primary text-primary font-bold rounded-lg transition-all shadow-sm";
            } else {
                btn.className = "flex-1 py-1.5 px-2 text-[12px] bg-white dark:bg-slate-800 border border-outline-variant/30 hover:border-primary/60 text-on-surface rounded-lg transition-all";
            }
        });
    }
    
    (window as any)._relayOpenModal('relayUserQuotaModal');
};

(window as any)._relayApplyQuotaPreset = (preset: string) => {
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
    (document.getElementById('quotaRateLimit') as HTMLInputElement).value = pkg.quotas?.rateLimit?.toString() || '30';
    
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

(window as any)._relaySaveQuota = async () => {
    const userId = (document.getElementById('quotaUserId') as HTMLInputElement).value;
    const isPackageMode = !document.getElementById('quotaPackageNameContainer')?.classList.contains('hidden');
    
    if (!isPackageMode && !userId) return;
    
    const getFormData = (family: 'gemini' | 'claude') => {
        return {
            enableFixed: (document.getElementById(`${family}EnableFixed`) as HTMLInputElement).checked,
            fixedTokens: parseInt((document.getElementById(`${family}FixedTokens`) as HTMLInputElement).value) || 0,
            
            enableHourly: (document.getElementById(`${family}EnableHourly`) as HTMLInputElement).checked,
            hourlyHours: parseFloat((document.getElementById(`${family}HourlyHours`) as HTMLInputElement).value) || 0,
            hourlyTokens: parseInt((document.getElementById(`${family}HourlyTokens`) as HTMLInputElement).value) || 0,
            
            enableDaily: (document.getElementById(`${family}EnableDaily`) as HTMLInputElement).checked,
            dailyDays: parseFloat((document.getElementById(`${family}DailyDays`) as HTMLInputElement).value) || 0,
            dailyTokens: parseInt((document.getElementById(`${family}DailyTokens`) as HTMLInputElement).value) || 0
        };
    };
    
    const quotas = {
        gemini: getFormData('gemini'),
        claude: getFormData('claude'),
        validDuration: parseInt((document.getElementById('quotaValidDuration') as HTMLInputElement).value) || 0,
        validUnit: (document.getElementById('quotaValidUnit') as HTMLInputElement).value || 'months',
        rateLimit: parseInt((document.getElementById('quotaRateLimit') as HTMLInputElement).value) || 30
    };
    
    try {
        if (isPackageMode) {
            const pkgId = (document.getElementById('quotaPackageId') as HTMLInputElement).value;
            const pkgName = (document.getElementById('quotaPackageName') as HTMLInputElement).value || (state.currentLanguage === 'zh' ? '未命名套餐' : 'Unnamed Package');
            await ipcRenderer.invoke('relay:save-package', { id: pkgId, name: pkgName, quotas });
            await refreshRelayPackages();
        } else {
            const resetLimit = (document.getElementById('quotaResetLimit') as HTMLInputElement)?.checked || false;
            await ipcRenderer.invoke('relay:update-user-quota', userId, quotas, resetLimit);
            await refreshRelayUsers();
        }
        (window as any)._relayCloseModal('relayUserQuotaModal');
    } catch (err) {
        console.error('[RelayController] Failed to save quotas:', err);
        const dict = i18n[state.currentLanguage] || {};
        alert(dict.relaySaveQuotaFailed || '保存限额配置失败');
    }
};

export function openAddUserModal() {
    (window as any)._relayOpenModal('relayUserModal');
    // Clear inputs
    const keyInput = document.getElementById('relayUserKeyInput') as HTMLInputElement;
    const pwdInput = document.getElementById('relayUserPasswordInput') as HTMLInputElement;
    const remarkInput = document.getElementById('relayUserRemarkInput') as HTMLInputElement;
    if (keyInput) keyInput.value = '';
    if (pwdInput) pwdInput.value = '';
    if (remarkInput) remarkInput.value = '';
}

export function closeAddUserModal() {
    (window as any)._relayCloseModal('relayUserModal');
}

export async function handleAddUser() {
    const keyInput = document.getElementById('relayUserKeyInput') as HTMLInputElement;
    const pwdInput = document.getElementById('relayUserPasswordInput') as HTMLInputElement;
    const remarkInput = document.getElementById('relayUserRemarkInput') as HTMLInputElement;
    
    const key = keyInput?.value?.trim();
    const password = pwdInput?.value;
    const remark = remarkInput?.value?.trim() || '';
    
    if (!key || !password) {
        const dict = i18n[state.currentLanguage] || {};
        alert(dict.relayAlertEmptyKeyPassword || 'Key 和密码不能为空');
        return;
    }
    
    try {
        const res = await ipcRenderer.invoke('relay:add-user', key, password, remark);
        if (res?.success) {
            closeAddUserModal();
            await refreshRelayUsers();
        } else {
            alert(res?.error || (state.currentLanguage === 'zh' ? '添加失败' : 'Failed to add user'));
        }
    } catch (err) {
        console.error('[RelayController] Failed to add user:', err);
        const dict = i18n[state.currentLanguage] || {};
        alert(dict.relayAddUserFailed || '添加用户失败');
    }
}
