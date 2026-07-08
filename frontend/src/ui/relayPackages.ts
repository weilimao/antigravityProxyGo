import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';
import i18n from '../shared/i18n';

export let relayPackages: any[] = [];

export async function refreshRelayPackages() {
    try {
        const pkgs = await ipcRenderer.invoke('relay:get-packages');
        relayPackages = pkgs || [];
        renderRelayPackages();
        updatePackageFilterOptions();
    } catch (err) {
        console.error('[RelayController] Failed to get packages:', err);
    }
}

export function updatePackageFilterOptions() {
    const filterSelect = document.getElementById('relayUserPackageFilter') as HTMLSelectElement;
    if (!filterSelect) return;

    const currentVal = filterSelect.value;
    const dict = i18n[state.currentLanguage] || {};
    let html = `
        <option value="all">${dict.relayFilterAllPackages || '所有套餐类型'}</option>
        <option value="unlimited">${dict.relayFilterUnlimited || '无限制'}</option>
        <option value="custom">${dict.relayFilterCustom || '自定义限额'}</option>
    `;

    relayPackages.forEach(pkg => {
        if (pkg && pkg.name) {
            html += `<option value="${pkg.name}">${pkg.name}</option>`;
        }
    });

    filterSelect.innerHTML = html;
    filterSelect.value = currentVal;
}

export function formatTokenCount(val: number): string {
    if (!val) return '0';
    const isZH = state.currentLanguage === 'zh';
    if (isZH) {
        const dict = i18n[state.currentLanguage] || {};
        if (val >= 100000000) {
            return (val / 100000000) + (dict.relayTokenHundredMillion || '亿');
        }
        if (val >= 10000) {
            return (val / 10000) + (dict.relayTokenTenThousand || '万');
        }
        return val.toString();
    } else {
        if (val >= 1000000000) {
            return (val / 1000000000) + 'B';
        }
        if (val >= 1000000) {
            return (val / 1000000) + 'M';
        }
        if (val >= 1000) {
            return (val / 1000) + 'K';
        }
        return val.toString();
    }
}

export function formatQuotaSummary(q: any): string {
    const dict = i18n[state.currentLanguage] || {};
    const isZH = state.currentLanguage === 'zh';
    if (!q) return dict.relayNoLimit || '不限制';
    let parts: string[] = [];
    if (q.enableFixed && q.fixedTokens > 0) parts.push((isZH ? '总量 ' : 'Total ') + formatTokenCount(q.fixedTokens));
    if (q.enableHourly && q.hourlyHours > 0) parts.push(`${q.hourlyHours}${dict.relayHour || '小时'} ${formatTokenCount(q.hourlyTokens)}`);
    if (q.enableDaily && q.dailyDays > 0) parts.push(`${q.dailyDays}${dict.relayDay || '天'} ${formatTokenCount(q.dailyTokens)}`);
    if (parts.length === 0) return dict.relayNoLimit || '不限制';
    return parts.join(' | ');
}

export function renderRelayPackages() {
    const container = document.getElementById('relayPackagesList');
    if (!container) return;
    const dict = i18n[state.currentLanguage] || {};
    const isZH = state.currentLanguage === 'zh';

    if (relayPackages.length === 0) {
        container.innerHTML = `<div class="col-span-full text-center text-outline/60 py-6 text-[13px]">${dict.relayTemplateListEmpty || '暂无套餐，请新建套餐'}</div>`;
        return;
    }

    container.innerHTML = relayPackages.map(pkg => {
        const q = pkg.quotas || {};
        let validStr = dict.relayNoLimit || '不限制';
        if (q.validDuration > 0) {
            const unitMap: any = isZH ? { days: '天', months: '个月', years: '年' } : { days: 'days', months: 'months', years: 'years' };
            validStr = `${q.validDuration} ${unitMap[q.validUnit] || (isZH ? '天' : 'days')}`;
        }
        
        return `
            <div class="p-4 rounded-xl border border-outline-variant/30 bg-white/50 dark:bg-[#1a2033] hover:border-primary/50 transition-all cursor-pointer shadow-sm flex flex-col justify-between group" onclick="window._relayOpenPackageSettings('${pkg.id}')">
                <div>
                    <div class="flex items-center justify-between mb-3">
                        <div class="flex items-center gap-2">
                            <span class="text-[14px] font-bold text-on-surface dark:text-white group-hover:text-primary transition-colors">${pkg.name}</span>
                            <span class="text-[10px] bg-primary/10 text-primary px-2 py-0.5 rounded-full font-medium">${dict.relayValidity || '有效期:'} ${validStr}</span>
                        </div>
                        <button class="text-outline hover:text-red-500 transition-colors p-1" onclick="event.stopPropagation(); window._relayDeletePackage('${pkg.id}')">
                            <span class="material-symbols-outlined text-[16px]">delete</span>
                        </button>
                    </div>
                    <div class="space-y-2 text-[12px]">
                        <div class="flex items-center justify-between bg-outline-variant/5 p-2 rounded-lg border border-outline-variant/10">
                            <span class="text-outline font-medium">${dict.relayQuotaGeminiTitle || 'Gemini系列'}</span>
                            <span class="text-on-surface dark:text-slate-200 font-bold">${formatQuotaSummary(q.gemini)}</span>
                        </div>
                        <div class="flex items-center justify-between bg-outline-variant/5 p-2 rounded-lg border border-outline-variant/10">
                            <span class="text-outline font-medium">${dict.relayQuotaClaudeTitle || 'Claude系列'}</span>
                            <span class="text-on-surface dark:text-slate-200 font-bold">${formatQuotaSummary(q.claude)}</span>
                        </div>
                        <div class="flex items-center justify-between bg-outline-variant/5 p-2 rounded-lg border border-outline-variant/10">
                            <span class="text-outline font-medium">${dict.relayRateLimitLabel || (isZH ? '请求速率' : 'Rate Limit')}</span>
                            <span class="text-on-surface dark:text-slate-200 font-bold">${q.rateLimit || 30} ${dict.relayRateLimitText || '次/分钟'}</span>
                        </div>
                    </div>
                </div>
                <div class="mt-3 text-[11px] text-outline text-right flex items-center justify-end gap-1 group-hover:text-primary transition-colors">
                    <span>${dict.relayClickToModify || '点击修改配置'}</span>
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

// Bind to window for global access
(window as any)._relayDeletePackage = async (id: string) => {
    const $confirm = (window as any).$confirm;
    const dict = i18n[state.currentLanguage] || {};
    if ($confirm && !await $confirm(dict.relayDeleteTemplateConfirm || '确定要删除该套餐模板吗？')) return;
    try {
        await ipcRenderer.invoke('relay:delete-package', id);
        await refreshRelayPackages();
    } catch (err) {
        console.error('Failed to delete package:', err);
    }
};

(window as any)._relayOpenPackageSettings = (id: string) => {
    let quotas = {
        gemini: { enableFixed: false, fixedTokens: 0, enableHourly: false, hourlyHours: 0, hourlyTokens: 0, enableDaily: false, dailyDays: 0, dailyTokens: 0 },
        claude: { enableFixed: false, fixedTokens: 0, enableHourly: false, hourlyHours: 0, hourlyTokens: 0, enableDaily: false, dailyDays: 0, dailyTokens: 0 },
        validDuration: 1,
        validUnit: 'months',
        rateLimit: 30
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
    const dict = i18n[state.currentLanguage] || {};
    document.getElementById('quotaPresetsContainer')?.classList.add('hidden');
    document.getElementById('quotaResetContainer')?.classList.add('hidden');
    (document.getElementById('relayQuotaModalTitle') as HTMLElement).innerText = id 
        ? (dict.relayQuotaModalTitleEditTemplate || '编辑套餐模板') 
        : (dict.relayQuotaModalTitleCreateTemplate || '新建套餐模板');
    
    (document.getElementById('quotaValidDuration') as HTMLInputElement).value = quotas.validDuration?.toString() || '0';
    (document.getElementById('quotaValidUnit') as HTMLInputElement).value = quotas.validUnit || 'months';
    (document.getElementById('quotaRateLimit') as HTMLInputElement).value = quotas.rateLimit?.toString() || '30';

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
    
    (window as any)._relayOpenModal('relayUserQuotaModal');
};
