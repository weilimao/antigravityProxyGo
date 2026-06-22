import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';

// DOM Elements cache
let accountsList: HTMLElement | null;
let accountsEmptyState: HTMLElement | null;
let accountCountBadge: HTMLElement | null;
let poolModeToggle: HTMLInputElement | null;

export function initRendererElements() {
    accountsList = document.getElementById('accountsList');
    accountsEmptyState = document.getElementById('accountsEmptyState');
    accountCountBadge = document.getElementById('accountCountBadge');
    poolModeToggle = document.getElementById('poolModeToggle') as HTMLInputElement | null;
}

// Format reset time relatively
export function getRelativeResetTime(resetTime: any): string {
    try {
        const now = Date.now();
        const reset = new Date(resetTime).getTime();
        const diffMs = reset - now;
        if (diffMs <= 0) {
            return '已重置';
        }
        const diffMins = Math.round(diffMs / 60000);
        if (diffMins < 60) {
            return `将在 ${diffMins} 分钟后重置`;
        }
        const diffHours = Math.floor(diffMins / 60);
        const remMins = diffMins % 60;
        if (diffHours < 24) {
            return `将在 ${diffHours} 小时 ${remMins} 分钟后重置`;
        }
        const diffDays = Math.floor(diffHours / 24);
        const remHours = diffHours % 24;
        return `将在 ${diffDays} 天 ${remHours} 小时后重置`;
    } catch (e) {
        return `重置时间: ${new Date(resetTime).toLocaleString()}`;
    }
}

// Format cooldown time to absolute text
export function formatCooldownTime(cooldownTime: any): string {
    try {
        const now = new Date();
        const target = new Date(cooldownTime);
        const isToday = now.getFullYear() === target.getFullYear() &&
                        now.getMonth() === target.getMonth() &&
                        now.getDate() === target.getDate();
        
        const timeStr = target.toLocaleTimeString('zh-CN', { hour12: false, hour: '2-digit', minute: '2-digit' });
        if (isToday) {
            return timeStr;
        } else {
            const month = target.getMonth() + 1;
            const date = target.getDate();
            return `${month}月${date}日 ${timeStr}`;
        }
    } catch (e) {
        return new Date(cooldownTime).toLocaleString();
    }
}

// Render account quota progress bars
export function renderQuotaBars(containerEl: HTMLElement | null, buckets: any[], cooldowns: any = {}) {
    if (!containerEl) return;
    containerEl.innerHTML = '';

    // 针对 Project 渠道（API 级别/按量付费项目）进行特殊展示，不显示假周限额进度条
    const accountId = containerEl.id ? containerEl.id.replace('quotaBars-', '') : '';
    const acc = state.currentAccountsList?.find(a => a.id === accountId);
    if (acc && acc.provider !== 'antigravity') {
        containerEl.innerHTML = `
            <div class="flex items-center gap-1.5 bg-emerald-500/10 dark:bg-emerald-500/5 border border-emerald-500/20 rounded-lg p-2.5 mt-1">
                <span class="w-1.5 h-1.5 rounded-full bg-emerald-500 animate-pulse"></span>
                <span class="text-[10px] font-medium text-emerald-600 dark:text-emerald-400">云项目 API (按量付费)，无额度限制</span>
            </div>
        `;
        return;
    }

    if (!buckets || buckets.length === 0) {
        containerEl.innerHTML = '<span class="text-[10px] text-outline/50 italic">暂无配额数据</span>';
        return;
    }

    const hasGroups = buckets.some(b => b.group);

    if (hasGroups) {
        const groups: { [key: string]: any[] } = {};
        buckets.forEach(b => {
            const groupName = b.group || '其他模型';
            if (!groups[groupName]) {
                groups[groupName] = [];
            }
            groups[groupName].push(b);
        });

        Object.keys(groups).forEach((groupName, idx) => {
            const groupBuckets = groups[groupName];
            const isClaude = groupName.toLowerCase().includes('claude');
            const category = isClaude ? 'claude' : 'gemini';
            
            let isCategoryCooling = false;
            let categoryCooldownUntil = 0;
            if (cooldowns && cooldowns[category]) {
                const now = Date.now();
                if (cooldowns[category] > now) {
                    isCategoryCooling = true;
                    categoryCooldownUntil = cooldowns[category];
                }
            }

            const groupContainer = document.createElement('div');
            groupContainer.className = `flex flex-col gap-2 bg-[#f8fafc]/60 dark:bg-[#20293d]/30 border border-slate-100 dark:border-slate-800/30 rounded-lg p-2.5 ${idx > 0 ? 'mt-2' : 'mt-1'}`;
            
            const groupTitle = document.createElement('div');
            groupTitle.className = 'text-[10px] font-bold text-on-surface dark:text-white flex items-center justify-between border-b border-outline-variant/10 pb-1.5 mb-1';
            
            let cooldownBadge = '';
            if (isCategoryCooling) {
                const dateStr = formatCooldownTime(categoryCooldownUntil);
                cooldownBadge = `<span class="px-1 py-0.5 rounded bg-amber-500/10 text-amber-500 text-[8px] font-bold border border-amber-500/20">${dateStr} 恢复</span>`;
            }

            groupTitle.innerHTML = `
                <div class="flex items-center gap-1.5">
                    <span class="w-1.5 h-1.5 rounded-full ${isCategoryCooling ? 'bg-amber-500' : 'bg-primary'} animate-pulse"></span>
                    <span>${groupName}</span>
                </div>
                ${cooldownBadge}
            `;
            groupContainer.appendChild(groupTitle);

            groupBuckets.forEach(b => {
                const pct = b.remainPercent;
                const barColor = pct > 50
                    ? 'bg-emerald-500'
                    : pct > 20
                        ? 'bg-amber-400'
                        : 'bg-red-500';

                const resetStr = b.resetTime
                    ? getRelativeResetTime(b.resetTime)
                    : null;

                const row = document.createElement('div');
                row.className = 'flex flex-col gap-0.5 mt-1';
                row.innerHTML = `
                    <div class="flex justify-between items-center">
                        <span class="text-[10px] text-outline dark:text-outline-variant truncate max-w-[70%]" title="${b.modelId}">${b.modelId}</span>
                        <span class="text-[10px] font-bold text-on-surface dark:text-white">${pct}%</span>
                    </div>
                    <div class="h-1.5 bg-outline-variant/20 dark:bg-white/10 rounded-full overflow-hidden">
                        <div class="h-full ${barColor} rounded-full transition-all duration-700" style="width: ${pct}%"></div>
                    </div>
                    ${resetStr ? `<span class="text-[9px] text-outline/50 mt-0.5">${resetStr}</span>` : ''}
                `;
                groupContainer.appendChild(row);
            });

            containerEl.appendChild(groupContainer);
        });
    } else {
        buckets.forEach(b => {
            const pct = b.remainPercent;
            const barColor = pct > 50
                ? 'bg-emerald-500'
                : pct > 20
                    ? 'bg-amber-400'
                    : 'bg-red-500';

            const resetStr = b.resetTime
                ? new Date(b.resetTime).toLocaleString()
                : null;

            const row = document.createElement('div');
            row.className = 'flex flex-col gap-0.5';
            row.innerHTML = `
                <div class="flex justify-between items-center">
                    <span class="text-[10px] text-outline dark:text-outline-variant truncate max-w-[70%]" title="${b.modelId}">${b.modelId}</span>
                    <span class="text-[10px] font-bold text-on-surface dark:text-white">${pct}%</span>
                </div>
                <div class="h-1.5 bg-outline-variant/20 dark:bg-white/10 rounded-full overflow-hidden">
                    <div class="h-full ${barColor} rounded-full transition-all duration-700" style="width: ${pct}%"></div>
                </div>
                ${resetStr ? `<span class="text-[9px] text-outline/50 mt-0.5">重置于: ${resetStr}</span>` : ''}
            `;
            containerEl.appendChild(row);
        });
    }
}

// Fetch and load individual account quota
export async function loadAccountQuota(accountId: string, containerEl: HTMLElement | null, refreshBtn: HTMLElement | null, force: boolean = false, cooldowns: any = {}) {
    if (!state.quotaLoadingState) {
        state.quotaLoadingState = {};
    }

    if (!force && state.quotaCache[accountId]) {
        const activeContainer = document.getElementById(`quotaBars-${accountId}`) || containerEl;
        renderQuotaBars(activeContainer, state.quotaCache[accountId], cooldowns);
        updateAggregateQuotaUI();
        return;
    }

    // 避免非强制加载时重复请求
    if (!force && (state.quotaLoadingState[accountId] === 'loading' || state.quotaLoadingState[accountId] === 'error' || state.quotaLoadingState[accountId] === 'success')) {
        if (state.quotaLoadingState[accountId] === 'loading') {
            const icon = refreshBtn?.querySelector('.material-symbols-outlined') || refreshBtn;
            if (icon) icon.classList.add('animate-spin');
            const activeContainer = document.getElementById(`quotaBars-${accountId}`) || containerEl;
            if (activeContainer) activeContainer.innerHTML = '<span class="text-[10px] text-outline/50">加载中...</span>';
        } else if (state.quotaLoadingState[accountId] === 'error') {
            const activeContainer = document.getElementById(`quotaBars-${accountId}`) || containerEl;
            if (activeContainer) activeContainer.innerHTML = `<span class="text-[10px] text-red-400">加载失败</span>`;
        }
        return;
    }

    const icon = refreshBtn?.querySelector('.material-symbols-outlined') || refreshBtn;
    if (icon) icon.classList.add('animate-spin');
    const initContainer = document.getElementById(`quotaBars-${accountId}`) || containerEl;
    if (initContainer) initContainer.innerHTML = '<span class="text-[10px] text-outline/50">加载中...</span>';
    
    state.quotaLoadingState[accountId] = 'loading';

    try {
        const result = await ipcRenderer.invoke('quota:fetch', accountId);
        const activeContainer = document.getElementById(`quotaBars-${accountId}`) || containerEl;
        
        if (result.error) {
            state.quotaLoadingState[accountId] = 'error';
            if (activeContainer) activeContainer.innerHTML = `<span class="text-[10px] text-red-400">${result.error}</span>`;
        } else {
            state.quotaLoadingState[accountId] = 'success';
            state.quotaCache[accountId] = result.buckets;
            renderQuotaBars(activeContainer, result.buckets, cooldowns);
            updateAggregateQuotaUI();
        }
    } catch (e) {
        state.quotaLoadingState[accountId] = 'error';
        const activeContainer = document.getElementById(`quotaBars-${accountId}`) || containerEl;
        if (activeContainer) activeContainer.innerHTML = `<span class="text-[10px] text-red-400">请求失败</span>`;
    } finally {
        const icon = refreshBtn?.querySelector('.material-symbols-outlined') || refreshBtn;
        if (icon) icon.classList.remove('animate-spin');
    }
}

// Render accounts grid UI
export function renderAccounts(accounts: any[]) {
    state.currentAccountsList = accounts;
    if (!accountsList) {
        accountsList = document.getElementById('accountsList');
    }
    if (!accountsList) return;
    
    const filteredAccounts = accounts.filter(acc => {
        const accountChannel = acc.provider === 'antigravity' ? 'antigravity' : 'project';
        return accountChannel === state.currentViewTab;
    });

    if (!accountCountBadge) {
        accountCountBadge = document.getElementById('accountCountBadge');
    }
    if (accountCountBadge) {
        accountCountBadge.textContent = `共 ${filteredAccounts.length} 个账号`;
    }
    
    if (!accountsEmptyState) {
        accountsEmptyState = document.getElementById('accountsEmptyState');
    }

    if (filteredAccounts.length === 0) {
        if (accountsEmptyState) {
            accountsEmptyState.classList.remove('hidden');
            accountsEmptyState.classList.add('flex');
        }
        accountsList.classList.add('hidden');
        accountsList.innerHTML = '';
        return;
    }
    
    if (accountsEmptyState) {
        accountsEmptyState.classList.add('hidden');
        accountsEmptyState.classList.remove('flex');
    }
    accountsList.classList.remove('hidden');
    
    // 1. DOM Pruning: Remove cards of accounts that are no longer present in the active list
    const targetIds = new Set(filteredAccounts.map(a => a.id));
    const currentCards = Array.from(accountsList.children);
    currentCards.forEach(child => {
        const accId = child.getAttribute('data-account-id');
        if (accId && !targetIds.has(accId)) {
            accountsList!.removeChild(child);
        }
    });

    // 2. Loop through filtered accounts, either creating new card or updating existing card's attributes
    filteredAccounts.forEach(acc => {
        let card = accountsList!.querySelector(`[data-account-id="${acc.id}"]`) as HTMLElement | null;
        let quotaBars: HTMLElement | null = null;
        let refreshBtn: HTMLElement | null = null;
        
        let isCooling = false;
        let coolingCategories: string[] = [];
        let maxCooldownTime = 0;
        const now = Date.now();
        if (acc.cooldowns) {
            Object.entries(acc.cooldowns).forEach(([cat, until]) => {
                const u = until as number;
                if (u && u > now) {
                    coolingCategories.push(cat);
                    if (u > maxCooldownTime) {
                        maxCooldownTime = u;
                    }
                }
            });
        }
        if (coolingCategories.length === 0 && acc.cooldownUntil) {
            if (acc.cooldownUntil > now) {
                coolingCategories.push('all');
                maxCooldownTime = acc.cooldownUntil;
            }
        }
        isCooling = coolingCategories.length > 0;
        
        const totalCategoriesCount = 2;
        const isOverallCooling = coolingCategories.includes('all') || (coolingCategories.length === totalCategoriesCount);

        if (!card) {
            // Card doesn't exist, create it from scratch and bind events
            card = document.createElement('div');
            card.setAttribute('data-account-id', acc.id);
            card.className = 'bg-white dark:bg-[#1a1f30] border border-outline-variant/30 rounded-xl p-4 flex flex-col gap-3 shadow-sm relative overflow-hidden';
            
            // Background decorative icon
            const bgIcon = document.createElement('div');
            bgIcon.className = 'absolute -right-4 -bottom-4 text-primary opacity-[0.03] pointer-events-none';
            bgIcon.innerHTML = '<span class="material-symbols-outlined" style="font-size: 80px;">account_circle</span>';
            card.appendChild(bgIcon);
            
            // ---- Header ----
            const header = document.createElement('div');
            header.className = 'flex justify-between items-start';

            const info = document.createElement('div');
            info.className = 'flex flex-col flex-1 min-w-0 mr-2';

            const providerBadge = acc.provider === 'antigravity'
                ? '<span class="px-1.5 py-0.5 rounded bg-primary/10 text-primary text-[9px] font-bold border border-primary/20 ml-2 mt-0.5 self-center">Antigravity</span>'
                : (acc.provider === 'gemini-cli'
                    ? '<span class="px-1.5 py-0.5 rounded bg-slate-100 text-slate-500 dark:bg-white/10 dark:text-slate-300 text-[9px] font-bold border border-outline-variant/30 ml-2 mt-0.5 self-center">Gemini CLI</span>'
                    : '');

            const projectBadge = (acc.provider !== 'antigravity' && acc.projectId)
                ? '<span class="px-1.5 py-0.5 rounded bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 text-[9px] font-bold border border-emerald-500/20 ml-2 mt-0.5 self-center">Project</span>'
                : '';

            let tierBadge = '';
            if (acc.tier) {
                const tierStr = acc.tier.toUpperCase();
                if (tierStr === 'PRO') {
                    tierBadge = '<span class="px-1.5 py-0.5 rounded bg-rose-500/10 text-rose-500 dark:text-rose-400 text-[9px] font-bold border border-rose-500/20 ml-2 mt-0.5 self-center">Pro</span>';
                } else if (tierStr === 'ULTRA') {
                    tierBadge = '<span class="px-1.5 py-0.5 rounded bg-purple-500/10 text-purple-600 dark:text-purple-400 text-[9px] font-bold border border-purple-500/20 ml-2 mt-0.5 self-center font-extrabold tracking-wide">Ultra</span>';
                } else if (tierStr === 'ENTERPRISE') {
                    tierBadge = '<span class="px-1.5 py-0.5 rounded bg-blue-500/10 text-blue-600 dark:text-blue-400 text-[9px] font-bold border border-blue-500/20 ml-2 mt-0.5 self-center">Enterprise</span>';
                } else if (tierStr === 'STANDARD') {
                    tierBadge = '<span class="px-1.5 py-0.5 rounded bg-sky-500/10 text-sky-600 dark:text-sky-400 text-[9px] font-bold border border-sky-500/20 ml-2 mt-0.5 self-center">Standard</span>';
                } else if (tierStr === 'FREE') {
                    tierBadge = '<span class="px-1.5 py-0.5 rounded bg-slate-100 text-slate-500 dark:bg-white/10 dark:text-slate-300 text-[9px] font-bold border border-outline-variant/30 ml-2 mt-0.5 self-center">Free</span>';
                } else {
                    tierBadge = `<span class="px-1.5 py-0.5 rounded bg-slate-100 text-slate-500 dark:bg-white/10 dark:text-slate-300 text-[9px] font-bold border border-outline-variant/30 ml-2 mt-0.5 self-center">${acc.tier}</span>`;
                }
            }

            let projectInfoStr = '';
            if (acc.provider === 'antigravity' && acc.projectId) {
                projectInfoStr = ` | 绑定项目: ${acc.projectId}`;
            }

            info.innerHTML = `
                <div class="flex items-center">
                    <span class="text-[13px] font-bold text-on-surface dark:text-white truncate" title="${acc.email}">${acc.email}</span>
                    ${providerBadge}
                    ${projectBadge}
                    ${tierBadge}
                </div>
                <span class="text-[11px] text-outline mt-0.5 truncate">添加于: ${new Date(acc.addedAt).toLocaleString()}${projectInfoStr}</span>
            `;
            
            const statusBadge = document.createElement('div');
            statusBadge.className = 'acc-status-badge';
            if (isOverallCooling) {
                statusBadge.className = 'acc-status-badge flex items-center gap-1 text-[10px] font-bold text-amber-600 bg-amber-50 dark:bg-amber-900/30 dark:text-amber-400 px-2 py-0.5 rounded text-nowrap self-start flex-shrink-0';
                const dateStr = formatCooldownTime(maxCooldownTime);
                statusBadge.innerHTML = `<span class="material-symbols-outlined text-[12px]">hourglass_empty</span> 冷静中 (${dateStr}恢复)`;
            } else {
                statusBadge.className = 'acc-status-badge flex items-center gap-1 text-[10px] font-bold text-emerald-600 bg-emerald-50 dark:bg-emerald-900/30 dark:text-emerald-400 px-2 py-0.5 rounded text-nowrap self-start flex-shrink-0';
                statusBadge.innerHTML = '<span class="material-symbols-outlined text-[12px]">check_circle</span> 有效';
            }
            
            header.appendChild(info);
            header.appendChild(statusBadge);
            card.appendChild(header);
            
            // ---- AI Credit Section ----
            if (acc.provider === 'antigravity') {
                const creditSection = document.createElement('div');
                creditSection.className = 'flex flex-col gap-1.5 border-t border-outline-variant/20 pt-3';
                
                const creditHeader = document.createElement('div');
                creditHeader.className = 'flex justify-between items-center';
                
                const creditTitle = document.createElement('span');
                creditTitle.className = 'text-[11px] font-semibold text-outline dark:text-outline-variant';
                creditTitle.textContent = 'AI 积分 (AI Credit)';
                
                const creditValue = document.createElement('span');
                creditValue.className = 'acc-credit-value text-[11px] font-bold text-on-surface dark:text-white font-data-mono';
                const creditVal = typeof acc.credits === 'number' ? `$${acc.credits.toFixed(2)}` : '未加载';
                creditValue.textContent = creditVal;
                
                creditHeader.appendChild(creditTitle);
                creditHeader.appendChild(creditValue);
                creditSection.appendChild(creditHeader);
                
                // Overages Toggle Button
                const overagesToggleWrapper = document.createElement('div');
                overagesToggleWrapper.className = 'flex items-center justify-between text-[11px] mt-1.5 select-none cursor-pointer';
                
                const overagesSwitchId = `overagesToggle-${acc.id}`;
                const isOveragesChecked = acc.enableOverages === true;
                overagesToggleWrapper.innerHTML = `
                    <span class="text-outline dark:text-outline-variant">使用积分抵扣超额度部分</span>
                    <div class="relative inline-block w-8 align-middle transition duration-200 ease-in flex-shrink-0 ml-2">
                        <input class="toggle-checkbox absolute block w-4 h-4 rounded-full bg-white border-2 border-outline-variant appearance-none cursor-pointer translate-x-0 transition-transform duration-200 ease-in-out" 
                            id="${overagesSwitchId}" type="checkbox" ${isOveragesChecked ? 'checked' : ''}/>
                        <label class="toggle-label block overflow-hidden h-4 rounded-full bg-outline-variant/50 dark:bg-white/10 cursor-pointer" for="${overagesSwitchId}"></label>
                    </div>
                `;
                
                const overagesCheckbox = overagesToggleWrapper.querySelector('input') as HTMLInputElement;
                const overagesLabel = overagesToggleWrapper.querySelector('label') as HTMLLabelElement;
                
                overagesCheckbox.addEventListener('change', (e: any) => {
                    const enabled = e.target.checked;
                    ipcRenderer.send('accounts:toggle-overages', acc.id, enabled);
                    acc.enableOverages = enabled;
                    if (enabled) {
                        overagesCheckbox.className = 'toggle-checkbox absolute block w-4 h-4 rounded-full bg-white border-2 border-primary appearance-none cursor-pointer translate-x-4 transition-transform duration-200 ease-in-out';
                        overagesLabel.className = 'toggle-label block overflow-hidden h-4 rounded-full bg-primary cursor-pointer';
                    } else {
                        overagesCheckbox.className = 'toggle-checkbox absolute block w-4 h-4 rounded-full bg-white border-2 border-outline-variant appearance-none cursor-pointer translate-x-0 transition-transform duration-200 ease-in-out';
                        overagesLabel.className = 'toggle-label block overflow-hidden h-4 rounded-full bg-outline-variant/50 dark:bg-white/10 cursor-pointer';
                    }
                    updateAggregateQuotaUI();
                });
                
                if (isOveragesChecked) {
                    overagesCheckbox.className = 'toggle-checkbox absolute block w-4 h-4 rounded-full bg-white border-2 border-primary appearance-none cursor-pointer translate-x-4 transition-transform duration-200 ease-in-out';
                    overagesLabel.className = 'toggle-label block overflow-hidden h-4 rounded-full bg-primary cursor-pointer';
                } else {
                    overagesCheckbox.className = 'toggle-checkbox absolute block w-4 h-4 rounded-full bg-white border-2 border-outline-variant appearance-none cursor-pointer translate-x-0 transition-transform duration-200 ease-in-out';
                    overagesLabel.className = 'toggle-label block overflow-hidden h-4 rounded-full bg-outline-variant/50 dark:bg-white/10 cursor-pointer';
                }
                
                creditSection.appendChild(overagesToggleWrapper);
                card.appendChild(creditSection);
            }

            // ---- Quota Section ----
            const quotaSection = document.createElement('div');
            quotaSection.className = 'flex flex-col gap-2 border-t border-outline-variant/20 pt-3';

            const quotaHeader = document.createElement('div');
            quotaHeader.className = 'flex justify-between items-center';
            quotaHeader.innerHTML = '<span class="text-[11px] font-semibold text-outline dark:text-outline-variant">剩余配额</span>';

            refreshBtn = document.createElement('button');
            refreshBtn.className = 'text-outline hover:text-primary transition-colors z-10';
            refreshBtn.title = '刷新配额';
            refreshBtn.setAttribute('data-quota-refresh-btn', '');
            refreshBtn.innerHTML = '<span class="material-symbols-outlined text-[14px]">refresh</span>';

            quotaHeader.appendChild(refreshBtn);
            quotaSection.appendChild(quotaHeader);

            quotaBars = document.createElement('div');
            quotaBars.id = `quotaBars-${acc.id}`;
            quotaBars.className = 'flex flex-col gap-2';
            quotaSection.appendChild(quotaBars);

            refreshBtn.onclick = () => loadAccountQuota(acc.id, quotaBars, refreshBtn, true, acc.cooldowns);
            card.appendChild(quotaSection);

            // ---- Footer ----
            const footer = document.createElement('div');
            footer.className = 'flex justify-between items-center pt-1 border-t border-outline-variant/20';
            
            const toggleWrapper = document.createElement('div');
            toggleWrapper.className = 'flex items-center gap-1.5 select-none cursor-pointer';
            
            const switchId = `accToggle-${acc.id}`;
            const isChecked = acc.enabled !== false;
            toggleWrapper.innerHTML = `
                <div class="relative inline-block w-8 mr-1 align-middle select-none transition duration-200 ease-in">
                    <input class="toggle-checkbox absolute block w-4 h-4 rounded-full bg-white border-2 border-outline-variant appearance-none cursor-pointer translate-x-0 transition-transform duration-200 ease-in-out" 
                        id="${switchId}" type="checkbox" ${isChecked ? 'checked' : ''}/>
                    <label class="toggle-label block overflow-hidden h-4 rounded-full bg-outline-variant/50 dark:bg-white/10 cursor-pointer" for="${switchId}"></label>
                </div>
                <span class="text-[11px] font-bold ${isChecked ? 'text-emerald-500' : 'text-outline'} acc-toggle-label-text">${isChecked ? '启用中' : '已停用'}</span>
            `;
            
            const checkbox = toggleWrapper.querySelector('input') as HTMLInputElement;
            const accLabel = toggleWrapper.querySelector('label') as HTMLLabelElement;
            const labelText = toggleWrapper.querySelector('span') as HTMLSpanElement;
            
            checkbox.addEventListener('change', (e: any) => {
                const enabled = e.target.checked;
                ipcRenderer.send('accounts:toggle-enabled', acc.id, enabled);
                acc.enabled = enabled;
                if (enabled) {
                    checkbox.className = 'toggle-checkbox absolute block w-4 h-4 rounded-full bg-white border-2 border-primary appearance-none cursor-pointer translate-x-4 transition-transform duration-200 ease-in-out';
                    accLabel.className = 'toggle-label block overflow-hidden h-4 rounded-full bg-primary cursor-pointer';
                    labelText.className = 'text-[11px] font-bold text-emerald-500 acc-toggle-label-text';
                    labelText.textContent = '启用中';
                    card!.classList.remove('opacity-60');
                } else {
                    checkbox.className = 'toggle-checkbox absolute block w-4 h-4 rounded-full bg-white border-2 border-outline-variant appearance-none cursor-pointer translate-x-0 transition-transform duration-200 ease-in-out';
                    accLabel.className = 'toggle-label block overflow-hidden h-4 rounded-full bg-outline-variant/50 dark:bg-white/10 cursor-pointer';
                    labelText.className = 'text-[11px] font-bold text-outline acc-toggle-label-text';
                    labelText.textContent = '已停用';
                    card!.classList.add('opacity-60');
                }
                updateAggregateQuotaUI();
            });
            
            if (!isChecked) {
                card.classList.add('opacity-60');
                checkbox.className = 'toggle-checkbox absolute block w-4 h-4 rounded-full bg-white border-2 border-outline-variant appearance-none cursor-pointer translate-x-0 transition-transform duration-200 ease-in-out';
                accLabel.className = 'toggle-label block overflow-hidden h-4 rounded-full bg-outline-variant/50 dark:bg-white/10 cursor-pointer';
            } else {
                checkbox.className = 'toggle-checkbox absolute block w-4 h-4 rounded-full bg-white border-2 border-primary appearance-none cursor-pointer translate-x-4 transition-transform duration-200 ease-in-out';
                accLabel.className = 'toggle-label block overflow-hidden h-4 rounded-full bg-primary cursor-pointer';
            }
            
            const btnDownload = document.createElement('button');
            btnDownload.className = 'text-[11px] font-medium text-primary hover:text-primary/80 hover:bg-primary/5 dark:hover:bg-primary/10 px-2 py-1 rounded transition-colors flex items-center gap-1 z-10 mr-1';
            btnDownload.innerHTML = '<span class="material-symbols-outlined text-[14px]">download</span> 导出';
            btnDownload.title = '导出该账号文件';
            btnDownload.onclick = () => {
                ipcRenderer.send('accounts:export-single', acc.id);
            };

            const btnDelete = document.createElement('button');
            btnDelete.className = 'text-[11px] font-medium text-red-500 hover:text-red-600 hover:bg-red-50 dark:hover:bg-red-900/20 px-2 py-1 rounded transition-colors flex items-center gap-1 z-10';
            btnDelete.innerHTML = '<span class="material-symbols-outlined text-[14px]">delete</span> 移除';
            btnDelete.onclick = () => {
                if (confirm(`确定要移除账号 ${acc.email} 吗？`)) {
                    ipcRenderer.send('accounts:remove', acc.id);
                }
            };
            
            const rightGroup = document.createElement('div');
            rightGroup.className = 'flex items-center gap-1';
            rightGroup.appendChild(btnDownload);
            rightGroup.appendChild(btnDelete);
            
            footer.appendChild(toggleWrapper);
            footer.appendChild(rightGroup);
            card.appendChild(footer);
        } else {
            // Card exists, selectively patch attributes only to avoid DOM recreation
            
            // 1. Update cooldown status
            const statusBadge = card.querySelector('.acc-status-badge') as HTMLElement;
            if (statusBadge) {
                if (isOverallCooling) {
                    statusBadge.className = 'acc-status-badge flex items-center gap-1 text-[10px] font-bold text-amber-600 bg-amber-50 dark:bg-amber-900/30 dark:text-amber-400 px-2 py-0.5 rounded text-nowrap self-start flex-shrink-0';
                    const dateStr = formatCooldownTime(maxCooldownTime);
                    statusBadge.innerHTML = `<span class="material-symbols-outlined text-[12px]">hourglass_empty</span> 冷静中 (${dateStr}恢复)`;
                } else {
                    statusBadge.className = 'acc-status-badge flex items-center gap-1 text-[10px] font-bold text-emerald-600 bg-emerald-50 dark:bg-emerald-900/30 dark:text-emerald-400 px-2 py-0.5 rounded text-nowrap self-start flex-shrink-0';
                    statusBadge.innerHTML = '<span class="material-symbols-outlined text-[12px]">check_circle</span> 有效';
                }
            }

            // 2. Update AI Credits (Antigravity only)
            if (acc.provider === 'antigravity') {
                const creditValue = card.querySelector('.acc-credit-value') as HTMLElement;
                if (creditValue) {
                    const creditVal = typeof acc.credits === 'number' ? `$${acc.credits.toFixed(2)}` : '未加载';
                    creditValue.textContent = creditVal;
                }

                const overagesCheckbox = card.querySelector(`#overagesToggle-${acc.id}`) as HTMLInputElement;
                const overagesLabel = card.querySelector(`[for="overagesToggle-${acc.id}"]`) as HTMLElement;
                if (overagesCheckbox && overagesLabel) {
                    const isOveragesChecked = acc.enableOverages === true;
                    if (overagesCheckbox.checked !== isOveragesChecked) {
                        overagesCheckbox.checked = isOveragesChecked;
                        if (isOveragesChecked) {
                            overagesCheckbox.className = 'toggle-checkbox absolute block w-4 h-4 rounded-full bg-white border-2 border-primary appearance-none cursor-pointer translate-x-4 transition-transform duration-200 ease-in-out';
                            overagesLabel.className = 'toggle-label block overflow-hidden h-4 rounded-full bg-primary cursor-pointer';
                        } else {
                            overagesCheckbox.className = 'toggle-checkbox absolute block w-4 h-4 rounded-full bg-white border-2 border-outline-variant appearance-none cursor-pointer translate-x-0 transition-transform duration-200 ease-in-out';
                            overagesLabel.className = 'toggle-label block overflow-hidden h-4 rounded-full bg-outline-variant/50 dark:bg-white/10 cursor-pointer';
                        }
                    }
                }
            }

            // 3. Update enabled/disabled status and style classes
            const checkbox = card.querySelector(`#accToggle-${acc.id}`) as HTMLInputElement;
            const accLabel = card.querySelector(`[for="accToggle-${acc.id}"]`) as HTMLElement;
            const labelText = card.querySelector('.acc-toggle-label-text') as HTMLSpanElement;
            const isChecked = acc.enabled !== false;
            if (checkbox && accLabel && labelText) {
                if (checkbox.checked !== isChecked) {
                    checkbox.checked = isChecked;
                    if (isChecked) {
                        checkbox.className = 'toggle-checkbox absolute block w-4 h-4 rounded-full bg-white border-2 border-primary appearance-none cursor-pointer translate-x-4 transition-transform duration-200 ease-in-out';
                        accLabel.className = 'toggle-label block overflow-hidden h-4 rounded-full bg-primary cursor-pointer';
                        labelText.className = 'text-[11px] font-bold text-emerald-500 acc-toggle-label-text';
                        labelText.textContent = '启用中';
                        card.classList.remove('opacity-60');
                    } else {
                        checkbox.className = 'toggle-checkbox absolute block w-4 h-4 rounded-full bg-white border-2 border-outline-variant appearance-none cursor-pointer translate-x-0 transition-transform duration-200 ease-in-out';
                        accLabel.className = 'toggle-label block overflow-hidden h-4 rounded-full bg-outline-variant/50 dark:bg-white/10 cursor-pointer';
                        labelText.className = 'text-[11px] font-bold text-outline acc-toggle-label-text';
                        labelText.textContent = '已停用';
                        card.classList.add('opacity-60');
                    }
                }
            }

            quotaBars = document.getElementById(`quotaBars-${acc.id}`);
            refreshBtn = card.querySelector('[data-quota-refresh-btn]') as HTMLElement;
        }

        // Reposition card in accountsList to maintain sequence order
        accountsList!.appendChild(card);

        // Load / update quota bars
        if (quotaBars) {
            loadAccountQuota(acc.id, quotaBars, refreshBtn, false, acc.cooldowns);
        }
    });
}

export function updateAggregateQuotaUI() {
    const panel = document.getElementById('aggregate-quota-panel');
    const grid = document.getElementById('aggregate-quota-grid');
    const info = document.getElementById('aggregate-quota-info');
    if (!panel || !grid || !info) return;

    const isPool = poolModeToggle ? poolModeToggle.checked : false;
    if (!isPool || !state.currentAccountsList || state.currentAccountsList.length === 0 || state.currentActiveChannel === 'project') {
        panel.classList.add('hidden');
        panel.classList.remove('flex');
        return;
    }

    panel.classList.remove('hidden');
    panel.classList.add('flex');

    let categories = [
        { group: 'Gemini Models', modelId: 'Weekly Limit', label: 'Gemini Weekly', key: 'gemini_weekly' },
        { group: 'Gemini Models', modelId: 'Five Hour Limit', label: 'Gemini 5-Hour', key: 'gemini_5hour' },
        { group: 'Claude and GPT models', modelId: 'Weekly Limit', label: 'Claude Weekly', key: 'claude_weekly' },
        { group: 'Claude and GPT models', modelId: 'Five Hour Limit', label: 'Claude 5-Hour', key: 'claude_5hour' }
    ];

    if (state.currentActiveChannel === 'project') {
        categories = categories.filter(c => c.key === 'gemini_weekly');
    }

    const sums: { [key: string]: { sum: number; count: number } } = {
        gemini_weekly: { sum: 0, count: 0 },
        gemini_5hour: { sum: 0, count: 0 },
        claude_weekly: { sum: 0, count: 0 },
        claude_5hour: { sum: 0, count: 0 }
    };

    const enabledAccounts = state.currentAccountsList.filter(a => {
        const accountChannel = a.provider === 'antigravity' ? 'antigravity' : 'project';
        return accountChannel === state.currentActiveChannel && a.enabled !== false;
    });

    enabledAccounts.forEach(acc => {
        const buckets = state.quotaCache[acc.id];
        if (buckets && buckets.length > 0) {
            categories.forEach(cat => {
                const bucket = buckets.find(b => {
                    const bg = (b.group || '').toLowerCase();
                    const bm = (b.modelId || b.model || '').toLowerCase();
                    const cg = cat.group.toLowerCase();
                    const cm = cat.modelId.toLowerCase();
                    return (bg.includes(cg) || cg.includes(bg)) && (bm.includes(cm) || cm.includes(bm));
                });
                
                if (bucket) {
                    const percent = typeof bucket.remainPercent === 'number' ? bucket.remainPercent : (bucket.remainingFraction * 100);
                    sums[cat.key].sum += percent;
                    sums[cat.key].count += 1;
                }
            });
        }
    });

    grid.innerHTML = '';
    let totalAccountsWithQuota = 0;
    const enabledAccountIds = new Set(enabledAccounts.map(a => a.id));
    
    Object.keys(state.quotaCache).forEach(accId => {
        if (enabledAccountIds.has(accId)) {
            totalAccountsWithQuota++;
        }
    });
    
    info.textContent = `汇总 ${totalAccountsWithQuota}/${enabledAccounts.length} 个账号的额度`;

    categories.forEach(cat => {
        const data = sums[cat.key];
        const cell = document.createElement('div');
        cell.className = 'flex flex-col gap-1 bg-slate-50/50 dark:bg-white/5 p-2 rounded-lg border border-outline-variant/20 flex-1 min-w-0';

        if (data.count > 0) {
            const avgPercent = Math.round(data.sum / data.count);
            
            let colorClass = 'bg-emerald-500';
            let textClass = 'text-emerald-500 dark:text-emerald-400';
            if (avgPercent < 30) {
                colorClass = 'bg-red-500';
                textClass = 'text-red-500 dark:text-red-400';
            } else if (avgPercent < 60) {
                colorClass = 'bg-amber-500';
                textClass = 'text-amber-500 dark:text-amber-400';
            }

            cell.innerHTML = `
                <div class="flex justify-between text-[11px] font-semibold items-center">
                    <span class="text-on-surface dark:text-white truncate pr-1" title="${cat.group} - ${cat.modelId}">${cat.label}</span>
                    <span class="${textClass} font-bold">${avgPercent}%</span>
                </div>
                <div class="w-full h-1 bg-outline-variant/20 dark:bg-white/5 rounded-full overflow-hidden">
                    <div class="${colorClass} h-full transition-all duration-300" style="width: ${avgPercent}%;"></div>
                </div>
            `;
        } else {
            cell.innerHTML = `
                <div class="flex justify-between text-[11px] font-semibold items-center">
                    <span class="text-on-surface dark:text-white truncate" title="${cat.group} - ${cat.modelId}">${cat.label}</span>
                    <span class="text-outline/40 font-bold">-</span>
                </div>
                <div class="w-full h-1 bg-outline-variant/20 dark:bg-white/5 rounded-full overflow-hidden flex items-center justify-center">
                    <div class="bg-outline-variant/30 h-full w-0"></div>
                </div>
            `;
        }
        grid.appendChild(cell);
    });
}
