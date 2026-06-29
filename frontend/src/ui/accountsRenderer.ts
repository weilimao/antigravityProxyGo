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
    if (acc && acc.provider !== 'antigravity' && acc.provider !== 'gemini-cli') {
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
            groupContainer.className = `flex flex-col gap-1.5 bg-[#f8fafc]/60 dark:bg-[#20293d]/30 border border-slate-100 dark:border-slate-800/30 rounded-lg p-2 ${idx > 0 ? 'mt-1.5' : 'mt-1'}`;
            
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
    
    // 1. Filter accounts by channel, searchQuery, statusFilter, and tierFilter
    const filteredAccounts = accounts.filter(acc => {
        const accountChannel = acc.provider;
        if (accountChannel !== state.currentViewTab) return false;

        // Search query filter
        if (state.accountSearchQuery) {
            const q = state.accountSearchQuery.toLowerCase().trim();
            const email = (acc.email || '').toLowerCase();
            const projectId = (acc.projectId || '').toLowerCase();
            if (!email.includes(q) && !projectId.includes(q)) return false;
        }

        // Status filter
        if (state.accountStatusFilter && state.accountStatusFilter !== 'all') {
            const isEnabled = acc.enabled !== false;
            const now = Date.now();
            let isCooling = false;
            if (acc.cooldowns) {
                isCooling = Object.values(acc.cooldowns).some((u: any) => typeof u === 'number' && u > now);
            }
            if (!isCooling && acc.cooldownUntil && acc.cooldownUntil > now) {
                isCooling = true;
            }

            if (state.accountStatusFilter === 'enabled' && !isEnabled) return false;
            if (state.accountStatusFilter === 'disabled' && isEnabled) return false;
            if (state.accountStatusFilter === 'cooling' && !isCooling) return false;
        }

        // Tier filter
        if (state.accountTierFilter && state.accountTierFilter !== 'all') {
            const tier = (acc.tier || 'free').toLowerCase();
            if (tier !== state.accountTierFilter.toLowerCase()) return false;
        }

        return true;
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
        renderPaginationUI(0, 0, 0, 1);
        return;
    }
    
    if (accountsEmptyState) {
        accountsEmptyState.classList.add('hidden');
        accountsEmptyState.classList.remove('flex');
    }
    accountsList.classList.remove('hidden');
    
    // 2. Pagination Calculation (10 accounts per page)
    const itemsPerPage = state.accountItemsPerPage || 10;
    const totalItems = filteredAccounts.length;
    const totalPages = Math.max(1, Math.ceil(totalItems / itemsPerPage));

    if (state.accountCurrentPage > totalPages) {
        state.accountCurrentPage = totalPages;
    }
    if (state.accountCurrentPage < 1) {
        state.accountCurrentPage = 1;
    }

    const startIndex = (state.accountCurrentPage - 1) * itemsPerPage;
    const endIndex = Math.min(startIndex + itemsPerPage, totalItems);
    const paginatedAccounts = filteredAccounts.slice(startIndex, endIndex);

    // 3. DOM Pruning: Remove cards of accounts that are no longer in current page
    const targetIds = new Set(paginatedAccounts.map(a => a.id));
    const currentCards = Array.from(accountsList.children);
    currentCards.forEach(child => {
        const accId = child.getAttribute('data-account-id');
        if (accId && !targetIds.has(accId)) {
            accountsList!.removeChild(child);
        }
    });

    // 4. Loop through current page accounts and render/patch cards
    paginatedAccounts.forEach(acc => {
        let card = accountsList!.querySelector(`[data-account-id="${acc.id}"]`) as HTMLElement | null;
        let quotaBars: HTMLElement | null = null;
        let refreshBtn: HTMLElement | null = null;
        
        let isCooling = false;
        let coolingCategories: string[] = [];
        let minCooldownTime = 0;
        const now = Date.now();
        if (acc.cooldowns) {
            Object.entries(acc.cooldowns).forEach(([cat, until]) => {
                const u = until as number;
                if (u && u > now) {
                    coolingCategories.push(cat);
                    if (minCooldownTime === 0 || u < minCooldownTime) {
                        minCooldownTime = u;
                    }
                }
            });
        }
        if (coolingCategories.length === 0 && acc.cooldownUntil) {
            if (acc.cooldownUntil > now) {
                coolingCategories.push('all');
                minCooldownTime = acc.cooldownUntil;
            }
        }
        isCooling = coolingCategories.length > 0;
        
        const totalCategoriesCount = 2;
        const isOverallCooling = coolingCategories.includes('all') || (coolingCategories.length === totalCategoriesCount);

        if (!card) {
            // Card doesn't exist, create it from scratch and bind events
            card = document.createElement('div');
            card.setAttribute('data-account-id', acc.id);
            card.className = 'bg-white dark:bg-[#1a1f30] border border-outline-variant/30 rounded-xl p-3 flex flex-col gap-2 shadow-sm relative overflow-hidden';
            
            // Background decorative icon
            const bgIcon = document.createElement('div');
            bgIcon.className = 'absolute -right-4 -bottom-4 text-primary opacity-[0.03] pointer-events-none';
            bgIcon.innerHTML = '<span class="material-symbols-outlined" style="font-size: 50px;">account_circle</span>';
            card.appendChild(bgIcon);
            
            // ---- Header ----
            const header = document.createElement('div');
            header.className = 'flex items-start justify-between gap-1.5';

            const leftGroup = document.createElement('div');
            leftGroup.className = 'flex items-start gap-2 min-w-0 flex-1';

            const checkboxEl = document.createElement('input');
            checkboxEl.type = 'checkbox';
            checkboxEl.className = 'account-card-checkbox w-4 h-4 rounded border-outline-variant/40 dark:border-white/20 text-primary focus:ring-primary cursor-pointer mt-0.5 flex-shrink-0';
            checkboxEl.setAttribute('data-account-id', acc.id);
            checkboxEl.checked = state.selectedAccountIds.includes(acc.id);
            checkboxEl.addEventListener('change', (e: any) => {
                const isChecked = e.target.checked;
                if (isChecked) {
                    if (!state.selectedAccountIds.includes(acc.id)) {
                        state.selectedAccountIds.push(acc.id);
                    }
                } else {
                    state.selectedAccountIds = state.selectedAccountIds.filter(id => id !== acc.id);
                }
                document.dispatchEvent(new CustomEvent('account-selection-changed'));
            });

            const info = document.createElement('div');
            info.className = 'acc-info-header flex flex-col flex-1 min-w-0 mr-2';

            const providerBadge = acc.provider === 'antigravity'
                ? '<span class="px-1.5 py-0.5 rounded bg-primary/10 text-primary text-[9px] font-bold border border-primary/20 ml-2 mt-0.5 self-center">Antigravity</span>'
                : (acc.provider === 'gemini-cli'
                    ? '<span class="px-1.5 py-0.5 rounded bg-slate-100 text-slate-500 dark:bg-white/10 dark:text-slate-300 text-[9px] font-bold border border-outline-variant/30 ml-2 mt-0.5 self-center">Gemini CLI</span>'
                    : '');

            const projectBadge = (acc.provider !== 'antigravity' && acc.provider !== 'gemini-cli' && acc.projectId)
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
                const dateStr = formatCooldownTime(minCooldownTime);
                statusBadge.innerHTML = `<span class="material-symbols-outlined text-[12px]">hourglass_empty</span> 冷静中 (${dateStr}恢复)`;
            } else {
                statusBadge.className = 'acc-status-badge flex items-center gap-1 text-[10px] font-bold text-emerald-600 bg-emerald-50 dark:bg-emerald-900/30 dark:text-emerald-400 px-2 py-0.5 rounded text-nowrap self-start flex-shrink-0';
                statusBadge.innerHTML = '<span class="material-symbols-outlined text-[12px]">check_circle</span> 有效';
            }
            
            leftGroup.appendChild(checkboxEl);
            leftGroup.appendChild(info);
            header.appendChild(leftGroup);
            header.appendChild(statusBadge);
            card.appendChild(header);
            
            // ---- AI Credit Section ----
            if (acc.provider === 'antigravity') {
                const creditSection = document.createElement('div');
                creditSection.className = 'flex flex-col gap-1 border-t border-outline-variant/20 pt-2';
                
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
                overagesToggleWrapper.className = 'flex items-center justify-between text-[11px] mt-1 select-none cursor-pointer';
                
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
            quotaSection.className = 'flex flex-col gap-1.5 border-t border-outline-variant/20 pt-2';

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
            quotaBars.className = 'flex flex-col gap-1.5';
            quotaSection.appendChild(quotaBars);

            refreshBtn.onclick = () => loadAccountQuota(acc.id, quotaBars, refreshBtn, true, acc.cooldowns);
            card.appendChild(quotaSection);

            // ---- Footer ----
            const footer = document.createElement('div');
            footer.className = 'flex justify-between items-center pt-3 border-t border-outline-variant/20 mt-auto';
            
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
            
            // 0. Update account header (email, badges, tier)
            const infoHeader = card.querySelector('.acc-info-header') as HTMLElement;
            if (infoHeader) {
                const providerBadge = acc.provider === 'antigravity'
                    ? '<span class="px-1.5 py-0.5 rounded bg-primary/10 text-primary text-[9px] font-bold border border-primary/20 ml-2 mt-0.5 self-center">Antigravity</span>'
                    : (acc.provider === 'gemini-cli'
                        ? '<span class="px-1.5 py-0.5 rounded bg-slate-100 text-slate-500 dark:bg-white/10 dark:text-slate-300 text-[9px] font-bold border border-outline-variant/30 ml-2 mt-0.5 self-center">Gemini CLI</span>'
                        : '');

                const projectBadge = (acc.provider !== 'antigravity' && acc.provider !== 'gemini-cli' && acc.projectId)
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

                infoHeader.innerHTML = `
                    <div class="flex items-center">
                        <span class="text-[13px] font-bold text-on-surface dark:text-white truncate" title="${acc.email}">${acc.email}</span>
                        ${providerBadge}
                        ${projectBadge}
                        ${tierBadge}
                    </div>
                    <span class="text-[11px] text-outline mt-0.5 truncate">添加于: ${new Date(acc.addedAt).toLocaleString()}${projectInfoStr}</span>
                `;
            }

            // 1. Update cooldown status
            const statusBadge = card.querySelector('.acc-status-badge') as HTMLElement;
            if (statusBadge) {
                if (isOverallCooling) {
                    statusBadge.className = 'acc-status-badge flex items-center gap-1 text-[10px] font-bold text-amber-600 bg-amber-50 dark:bg-amber-900/30 dark:text-amber-400 px-2 py-0.5 rounded text-nowrap self-start flex-shrink-0';
                    const dateStr = formatCooldownTime(minCooldownTime);
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

            // 4. Update selected checkbox state
            const checkboxEl = card.querySelector('.account-card-checkbox') as HTMLInputElement | null;
            if (checkboxEl) {
                checkboxEl.checked = state.selectedAccountIds.includes(acc.id);
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

    renderPaginationUI(totalItems, startIndex, endIndex, totalPages);
    document.dispatchEvent(new CustomEvent('account-selection-changed'));
}

function renderPaginationUI(totalItems: number, startIndex: number, endIndex: number, totalPages: number) {
    const info = document.getElementById('accountsPaginationInfo');
    const btnPrev = document.getElementById('btnPrevAccountPage') as HTMLButtonElement | null;
    const btnNext = document.getElementById('btnNextAccountPage') as HTMLButtonElement | null;
    const numbersContainer = document.getElementById('accountPageNumbers');

    if (info) {
        if (totalItems === 0) {
            info.textContent = '显示 0 - 0 条，共 0 条';
        } else {
            info.textContent = `显示 ${startIndex + 1} - ${endIndex} 条，共 ${totalItems} 条`;
        }
    }

    if (btnPrev) {
        btnPrev.disabled = state.accountCurrentPage <= 1;
    }
    if (btnNext) {
        btnNext.disabled = state.accountCurrentPage >= totalPages;
    }

    if (numbersContainer) {
        numbersContainer.innerHTML = '';
        for (let i = 1; i <= totalPages; i++) {
            const btn = document.createElement('button');
            const isActive = i === state.accountCurrentPage;
            btn.className = `w-6 h-6 rounded flex items-center justify-center text-[11px] font-medium transition-colors cursor-pointer ${
                isActive 
                    ? 'bg-primary text-white font-bold shadow-sm' 
                    : 'text-outline hover:bg-slate-100 dark:hover:bg-white/10'
            }`;
            btn.textContent = i.toString();
            btn.onclick = () => {
                state.accountCurrentPage = i;
                renderAccounts(state.currentAccountsList);
            };
            numbersContainer.appendChild(btn);
        }
    }
}

function getFamilyLifetimeTokens(stats: any, isGemini: boolean): number {
    if (!stats || !stats.models) return 0;
    let total = 0;
    for (const modelName of Object.keys(stats.models)) {
        const isClaude = modelName.toLowerCase().includes('claude');
        if (isGemini && !isClaude) {
            total += (stats.models[modelName].inputTokens || 0) + (stats.models[modelName].outputTokens || 0);
        } else if (!isGemini && isClaude) {
            total += (stats.models[modelName].inputTokens || 0) + (stats.models[modelName].outputTokens || 0);
        }
    }
    return total;
}

export function updateAggregateQuotaUI() {
    const panel = document.getElementById('aggregate-quota-panel');
    const grid = document.getElementById('aggregate-quota-grid');
    const info = document.getElementById('aggregate-quota-info');
    if (!panel || !grid || !info) return;

    const isPool = poolModeToggle ? poolModeToggle.checked : false;
    const isRemote = !!state.isRemoteMode;
    const hasRemoteStats = !!(state.remoteStats && state.remoteStats.quotas);

    // 绝对第一优先级：如果是远程模式，不论本地开没开负载均衡，一律禁止显示本地数据
    if (isRemote && !hasRemoteStats) {
        panel.classList.add('hidden');
        panel.classList.remove('flex');
        return;
    }
    
    // 第二优先级：如果没有开远程，且（没开负载均衡，或当前账户列表为空，或处于项目通道），则隐藏面板
    if (!isRemote && (!isPool || !state.currentAccountsList || state.currentAccountsList.length === 0 || state.currentActiveChannel === 'project')) {
        panel.classList.add('hidden');
        panel.classList.remove('flex');
        return;
    }

    panel.classList.remove('hidden');
    panel.classList.add('flex');
    grid.innerHTML = '';
    
    if (isRemote && hasRemoteStats) {
        // Render Remote Quotas instead of Local Pool Quotas
        const q = state.remoteStats.quotas;
        const usage = state.remoteStats.currentUsage || {};
        const resetAt = state.remoteStats.resetAt || {};
        const isZH = state.currentLanguage === 'zh';
        
        const pkgName = state.remoteStats.packageName || (isZH ? '自定义配置' : 'Custom');
        info.textContent = isZH ? `远程中继套餐: ${pkgName}` : `Remote Plan: ${pkgName}`;
        info.className = 'text-[11px] px-2 py-0.5 rounded-full font-medium bg-emerald-500/10 text-emerald-600 dark:text-emerald-400';
        
        const renderRemoteQuotaBar = (label: string, limitTokens: number, usedTokens: number, resetTimeIso?: string, isDaily?: boolean) => {
            const percent = limitTokens > 0 ? (usedTokens / limitTokens) * 100 : 0;
            const remaining = Math.max(0, limitTokens - usedTokens);
            const remainPercent = Math.max(0, Math.min(100, 100 - percent));
            
            const colorClass = remainPercent > 20 ? 'bg-emerald-500' : 'bg-red-500';
            let resetBadge = '';
            if (usedTokens > 0 && resetTimeIso) {
                const d = new Date(resetTimeIso);
                const hours = d.getHours().toString().padStart(2, '0');
                const minutes = d.getMinutes().toString().padStart(2, '0');
                let timeStr = `${hours}:${minutes}`;
                if (isDaily) {
                    const month = (d.getMonth() + 1).toString().padStart(2, '0');
                    const day = d.getDate().toString().padStart(2, '0');
                    timeStr = `${month}-${day} ${hours}:${minutes}`;
                }
                const labelReset = isZH ? `预计 ${timeStr} 刷新` : `Resets at ${timeStr}`;
                resetBadge = ` <span class="text-[10px] text-emerald-600 dark:text-emerald-400 bg-emerald-500/10 px-1.5 py-0.5 rounded ml-1 font-normal">${labelReset}</span>`;
            }

            const formatTokenCount = (num: number): string => {
                if (num >= 1000000) {
                    return (num / 1000000).toFixed(2) + 'M';
                }
                if (num >= 1000) {
                    return (num / 1000).toFixed(1) + 'K';
                }
                return num.toString();
            };

            const remainingText = formatTokenCount(remaining);
            const limitText = formatTokenCount(limitTokens);
            
            grid.innerHTML += `
                <div class="flex flex-col">
                    <div class="flex justify-between items-end mb-1.5">
                        <span class="text-[12px] font-medium text-on-surface dark:text-white truncate flex items-center" title="${label}">${label}${resetBadge}</span>
                        <div class="text-[12px] flex items-center gap-1.5 font-bold">
                            <span class="text-outline/70 font-data-mono font-medium">${remainingText}/${limitText}</span>
                            <span class="${remainPercent > 20 ? 'text-emerald-500' : 'text-red-500'} font-data-mono">${Math.round(remainPercent)}%</span>
                        </div>
                    </div>
                    <div class="h-[6px] w-full bg-slate-200 dark:bg-slate-700/50 rounded-full overflow-hidden">
                        <div class="h-full ${colorClass} transition-all duration-500 relative" style="width: ${remainPercent}%">
                            <div class="absolute inset-0 bg-white/20"></div>
                        </div>
                    </div>
                </div>
            `;
        };
        
        if (q.gemini) {
            if (q.gemini.enableFixed) {
                const label = isZH ? '远端 Gemini 总额度' : 'Remote Gemini Total';
                renderRemoteQuotaBar(label, q.gemini.fixedTokens, getFamilyLifetimeTokens(state.remoteStats, true));
            }
            if (q.gemini.enableHourly) {
                const label = isZH 
                    ? `远端 Gemini ${q.gemini.hourlyHours}小时限额` 
                    : `Remote Gemini ${q.gemini.hourlyHours}-Hour`;
                renderRemoteQuotaBar(label, q.gemini.hourlyTokens, usage.gemini_hourly || 0, resetAt.gemini_hourly, false);
            }
            if (q.gemini.enableDaily) {
                const label = isZH 
                    ? `远端 Gemini ${q.gemini.dailyDays}天限额` 
                    : `Remote Gemini ${q.gemini.dailyDays}-Day`;
                renderRemoteQuotaBar(label, q.gemini.dailyTokens, usage.gemini_daily || 0, resetAt.gemini_daily, true);
            }
        }
        
        if (q.claude) {
            if (q.claude.enableFixed) {
                const label = isZH ? '远端 Claude 总额度' : 'Remote Claude Total';
                renderRemoteQuotaBar(label, q.claude.fixedTokens, getFamilyLifetimeTokens(state.remoteStats, false));
            }
            if (q.claude.enableHourly) {
                const label = isZH 
                    ? `远端 Claude ${q.claude.hourlyHours}小时限额` 
                    : `Remote Claude ${q.claude.hourlyHours}-Hour`;
                renderRemoteQuotaBar(label, q.claude.hourlyTokens, usage.claude_hourly || 0, resetAt.claude_hourly, false);
            }
            if (q.claude.enableDaily) {
                const label = isZH 
                    ? `远端 Claude ${q.claude.dailyDays}天限额` 
                    : `Remote Claude ${q.claude.dailyDays}-Day`;
                renderRemoteQuotaBar(label, q.claude.dailyTokens, usage.claude_daily || 0, resetAt.claude_daily, true);
            }
        }
        
        if (grid.innerHTML === '') {
            panel.classList.add('hidden');
            panel.classList.remove('flex');
        } else {
            const childCount = grid.children.length;
            if (childCount === 2) {
                grid.className = 'grid grid-cols-1 sm:grid-cols-2 gap-4';
            } else if (childCount === 1) {
                grid.className = 'grid grid-cols-1 gap-4';
            } else {
                grid.className = 'grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4';
            }
        }
        return;
    }

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
        const accountChannel = a.provider;
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

    const childCount = grid.children.length;
    if (childCount === 2) {
        grid.className = 'grid grid-cols-1 sm:grid-cols-2 gap-4';
    } else if (childCount === 1) {
        grid.className = 'grid grid-cols-1 gap-4';
    } else {
        grid.className = 'grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4';
    }
}
