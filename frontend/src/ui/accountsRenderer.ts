import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';
import i18n from '../shared/i18n';
import { ensureNvidiaCooldownTimer, ensureGrokCooldownTimer, openEditNvidiaAccount, openEditGrokAccount, openEditOtherAccount, openEditWorkBuddyAccount, renderOtherGroupTabs } from './accountsController';
import { escapeHtml, buildNvidiaCooldownTickSpan, buildGrokCooldownTickSpan, formatCooldownTime } from './accountCardHelpers';
import { loadAccountQuota } from './quotaBarsRenderer';
import { updateAggregateQuotaUI } from './aggregateQuotaUI';

// DOM Elements cache
let accountsList: HTMLElement | null;
let accountsEmptyState: HTMLElement | null;
let accountCountBadge: HTMLElement | null;

export function initRendererElements() {
    accountsList = document.getElementById('accountsList');
    accountsEmptyState = document.getElementById('accountsEmptyState');
    accountCountBadge = document.getElementById('accountCountBadge');
}



// ==================== renderAccounts 文件私有去重助手组 ====================
// 这组助手把 create 分支与 patch 分支中逐字节相同(仅缩进差异)的徽标头/状态徽标/开关翻转
// 逻辑各封装为一处,两分支均调用同一函数,消除 Builder/patch 重复且保证 DOM 等价输出。

// toggle-checkbox / toggle-label 的"开/关"类名字面量只有这两组,抽成常量避免 14 处散落重复。
const TOGGLE_CHECKBOX_ON = 'toggle-checkbox absolute block w-4 h-4 rounded-full bg-white border-2 border-primary appearance-none cursor-pointer translate-x-4 transition-transform duration-200 ease-in-out';
const TOGGLE_CHECKBOX_OFF = 'toggle-checkbox absolute block w-4 h-4 rounded-full bg-white border-2 border-outline-variant appearance-none cursor-pointer translate-x-0 transition-transform duration-200 ease-in-out';
const TOGGLE_LABEL_ON = 'toggle-label block overflow-hidden h-4 rounded-full bg-primary cursor-pointer';
const TOGGLE_LABEL_OFF = 'toggle-label block overflow-hidden h-4 rounded-full bg-outline-variant/50 dark:bg-white/10 cursor-pointer';

// applySwitchState:统一 checkbox+label 的开/关类名翻转。
//   - on=true  → checkbox.TranslateX4+border-primary,label.bg-primary
//   - on=false → checkbox.TranslateX0+border-outline-variant,label.bg-outline-variant(关)
// opts.labelText 可选(供 acc-toggle 顺手刷启停文案节点);opts.card 可选(供 acc-toggle 顺带 opacity-60),
// overages 开关无 labelText/card 诉求时省略这两个字段即可。
function applySwitchState(
    checkbox: HTMLInputElement,
    label: HTMLElement,
    on: boolean,
    opts?: { labelText?: HTMLSpanElement; dict?: any; card?: HTMLElement }
): void {
    checkbox.className = on ? TOGGLE_CHECKBOX_ON : TOGGLE_CHECKBOX_OFF;
    label.className = on ? TOGGLE_LABEL_ON : TOGGLE_LABEL_OFF;
    if (opts?.labelText) {
        const dict = opts.dict;
        opts.labelText.className = `text-[11px] font-bold ${on ? 'text-emerald-500' : 'text-outline'} acc-toggle-label-text`;
        opts.labelText.textContent = on ? (dict.statusEnabledAccount || '启用中') : (dict.statusDisabledAccount || '已停用');
    }
    if (opts?.card) {
        if (on) opts.card.classList.remove('opacity-60');
        else opts.card.classList.add('opacity-60');
    }
}

// buildAccountHeaderInnerHTML:返回 .acc-info-header 完整 innerHTML(含 projectInfoStr + addedAt span)。
// create 分支与 patch 分支逐字节相同(仅缩进差异),统一以此函数产出,保证两路径 DOM 等价。
function buildAccountHeaderInnerHTML(acc: any, dict: any): string {
    const providerBadge = acc.provider === 'antigravity'
        ? '<span class="px-1.5 py-0.5 rounded bg-primary/10 text-primary text-[9px] font-bold border border-primary/20 ml-2 mt-0.5 self-center flex-shrink-0 whitespace-nowrap">Antigravity</span>'
        : (acc.provider === 'gemini-cli'
            ? '<span class="px-1.5 py-0.5 rounded bg-slate-100 text-slate-500 dark:bg-white/10 dark:text-slate-300 text-[9px] font-bold border border-outline-variant/30 ml-2 mt-0.5 self-center flex-shrink-0 whitespace-nowrap">Gemini CLI</span>'
            : (acc.provider === 'nvidia' ? '<span class="px-1.5 py-0.5 rounded bg-amber-500/10 text-amber-500 dark:text-amber-400 text-[9px] font-bold border border-amber-500/20 ml-2 mt-0.5 self-center flex-shrink-0 whitespace-nowrap">NVIDIA</span>'
            : (acc.provider === 'grok' ? '<span class="px-1.5 py-0.5 rounded bg-sky-500/10 text-sky-600 dark:text-sky-400 text-[9px] font-bold border border-sky-500/20 ml-2 mt-0.5 self-center flex-shrink-0 whitespace-nowrap">Grok</span>'
            : (acc.provider === 'workbuddy' ? '<span class="px-1.5 py-0.5 rounded bg-teal-500/10 text-teal-600 dark:text-teal-400 text-[9px] font-bold border border-teal-500/20 ml-2 mt-0.5 self-center flex-shrink-0 whitespace-nowrap">WorkBuddy</span>'
            : (acc.provider === 'other' ? '<span class="px-1.5 py-0.5 rounded bg-purple-500/10 text-purple-500 dark:text-purple-300 text-[9px] font-bold border border-purple-500/20 ml-2 mt-0.5 self-center flex-shrink-0 whitespace-nowrap">Other</span>' : '')))));
    let otherExtraBadges = '';
    if (acc.provider === 'other') {
        const groupName = acc.groupName || acc.groupId || '';
        if (groupName) {
            otherExtraBadges += `<span class="px-1.5 py-0.5 rounded bg-purple-500/5 text-purple-500/90 dark:text-purple-300/90 text-[9px] font-bold border border-purple-500/10 ml-1 mt-0.5 self-center flex-shrink-0 whitespace-nowrap" title="组: ${escapeHtml(groupName)}">${escapeHtml(groupName)}</span>`;
        }
        if (Array.isArray(acc.formats) && acc.formats.length > 0) {
            const fmtLabels = acc.formats.map((f: string) => f === 'anthropic' ? 'A' : (f === 'openai' ? 'O' : f.charAt(0).toUpperCase())).join('/');
            const fmtFull = acc.formats.join(', ');
            otherExtraBadges += `<span class="px-1.5 py-0.5 rounded bg-slate-100 text-slate-500 dark:bg-white/10 dark:text-slate-300 text-[9px] font-bold border border-outline-variant/30 ml-1 mt-0.5 self-center flex-shrink-0 whitespace-nowrap" title="协议: ${escapeHtml(fmtFull)}">${fmtLabels}</span>`;
        }
    }

    const projectBadge = (acc.provider !== 'antigravity' && acc.provider !== 'gemini-cli' && acc.provider !== 'nvidia' && acc.provider !== 'other' && acc.provider !== 'grok' && acc.provider !== 'workbuddy' && acc.projectId)
        ? '<span class="px-1.5 py-0.5 rounded bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 text-[9px] font-bold border border-emerald-500/20 ml-2 mt-0.5 self-center flex-shrink-0 whitespace-nowrap">Project</span>'
        : '';

    let tierBadge = '';
    if (acc.tier) {
        // 去重:NVIDIA/Grok 号池后端配额探测把 Tier 写成了 provider 名(quota.go:628/666),
        // 与 providerBadge 文本重复(quota 回写见 account_monitor.go:263-264),此处跳过该 tier 徽标;
        // antigravity(Pro/Ultra)、project(Pay-As-You-Go)、other(自定义上游)等真实 tier 不受影响。
        const tierUpper = acc.tier.toUpperCase();
        const dupProviderName =
            (acc.provider === 'nvidia' && tierUpper === 'NVIDIA') ||
            (acc.provider === 'grok' && tierUpper === 'GROK');
        if (!dupProviderName) {
            const tierStr = acc.tier.toUpperCase();
            if (tierStr === 'PRO') {
                tierBadge = '<span class="px-1.5 py-0.5 rounded bg-rose-500/10 text-rose-500 dark:text-rose-400 text-[9px] font-bold border border-rose-500/20 ml-2 mt-0.5 self-center flex-shrink-0 whitespace-nowrap">Pro</span>';
            } else if (tierStr === 'ULTRA') {
                tierBadge = '<span class="px-1.5 py-0.5 rounded bg-purple-500/10 text-purple-600 dark:text-purple-400 text-[9px] font-bold border border-purple-500/20 ml-2 mt-0.5 self-center font-extrabold tracking-wide flex-shrink-0 whitespace-nowrap">Ultra</span>';
            } else if (tierStr === 'ENTERPRISE') {
                tierBadge = '<span class="px-1.5 py-0.5 rounded bg-blue-500/10 text-blue-600 dark:text-blue-400 text-[9px] font-bold border border-blue-500/20 ml-2 mt-0.5 self-center flex-shrink-0 whitespace-nowrap">Enterprise</span>';
            } else if (tierStr === 'STANDARD') {
                tierBadge = '<span class="px-1.5 py-0.5 rounded bg-sky-500/10 text-sky-600 dark:text-sky-400 text-[9px] font-bold border border-sky-500/20 ml-2 mt-0.5 self-center flex-shrink-0 whitespace-nowrap">Standard</span>';
            } else if (tierStr === 'FREE') {
                tierBadge = '<span class="px-1.5 py-0.5 rounded bg-slate-100 text-slate-500 dark:bg-white/10 dark:text-slate-300 text-[9px] font-bold border border-outline-variant/30 ml-2 mt-0.5 self-center flex-shrink-0 whitespace-nowrap">Free</span>';
            } else {
                tierBadge = `<span class="px-1.5 py-0.5 rounded bg-slate-100 text-slate-500 dark:bg-white/10 dark:text-slate-300 text-[9px] font-bold border border-outline-variant/30 ml-2 mt-0.5 self-center flex-shrink-0 whitespace-nowrap">${acc.tier}</span>`;
            }
        }
    }

    let projectInfoStr = '';
    if (acc.provider === 'antigravity' && acc.projectId) {
        projectInfoStr = state.currentLanguage === 'zh' ? ` | 绑定项目: ${acc.projectId}` : ` | Project: ${acc.projectId}`;
    }

    return `
                <div class="flex items-center flex-wrap">
                    <span class="text-[13px] font-bold text-on-surface dark:text-white truncate" title="${acc.email}">${acc.email}</span>
                    ${providerBadge}${otherExtraBadges}
                    ${projectBadge}
                    ${tierBadge}
                </div>
                <span class="text-[11px] text-outline mt-0.5 truncate">${dict.addedAtLabel || '添加于: '}${new Date(acc.addedAt).toLocaleString()}${projectInfoStr}</span>
            `;
}

// buildAccountStatusBadgeHTML:只返回 statusBadge 的 innerHTML 体(不含外层 className,className
// 由调用方各自写 amber/emerald 包装——create 与 patch 仅差 1 字符缩进,不值得再抽)。
// 修正:statusBadge body 在 create(L746-756)与 patch(L1036-1048)逐字节相同,统一产出避免重复。
function buildAccountStatusBadgeHTML(opts: { isOverallCooling: boolean; isNvidiaAcc: boolean; isGrokAcc: boolean; minCooldownTime: number; dict: any }): string {
    if (opts.isOverallCooling) {
        if (opts.isNvidiaAcc) {
            // NVIDIA 用秒级翻牌倒计时 span,配色与文本由 accountsController 定时器每秒刷新。
            // 静态"NVIDIA"卷标 + 琥珀气泡已足够表明冷却语境,tick 文案自带"恢复/已到期"语义,避免出现"恢复恢复"。
            return `<span class="material-symbols-outlined text-[12px]">hourglass_empty</span> ${buildNvidiaCooldownTickSpan(opts.minCooldownTime)}`;
        }
        if (opts.isGrokAcc) {
            // Grok 同构:秒级翻牌倒计时 span(class=grok-cooldown-tick),由 grokCooldownTimer 每秒刷新。
            // tick 文案复用 nvidia 命名空间通用键(决策 A),仅 class 物理隔离。
            return `<span class="material-symbols-outlined text-[12px]">hourglass_empty</span> ${buildGrokCooldownTickSpan(opts.minCooldownTime)}`;
        }
        const dateStr = formatCooldownTime(opts.minCooldownTime);
        return `<span class="material-symbols-outlined text-[12px]">hourglass_empty</span> ${(opts.dict.cooldownText || '冷静中 ({time}恢复)').replace('{time}', dateStr)}`;
    }
    return `<span class="material-symbols-outlined text-[12px]">check_circle</span> ${opts.dict.statusActive || '有效'}`;
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

        // Other 号池二级组名过滤:仅 active 组展示(全部组 'ALL' 不过滤)。
        if (state.currentViewTab === 'other' && state.otherGroupFilter && state.otherGroupFilter !== 'ALL') {
            if ((acc.groupId || '') !== state.otherGroupFilter) return false;
        }

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
        const isZH = state.currentLanguage === 'zh';
        accountCountBadge.textContent = isZH 
            ? `共 ${filteredAccounts.length} 个账号` 
            : `${filteredAccounts.length} accounts`;
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

    // 3. Clear container to force complete redraw for language reactiveness
    accountsList.innerHTML = '';

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

        // NVIDIA / Other / Grok 号池各自只有一个冷却族('nvidia'/'other'/'grok'),单族冷却即应判为整体冷静,
        // 使头部徽标走琥珀分支。GCP/Antigravity 维持原双族聚合判定不变。
        const isNvidiaAcc = acc.provider === 'nvidia';
        const isOtherAcc = acc.provider === 'other';
        const isGrokAcc = acc.provider === 'grok';
        const isWorkBuddyAcc = acc.provider === 'workbuddy';
        const singleCategoryCooling = isNvidiaAcc || isOtherAcc || isGrokAcc || isWorkBuddyAcc;
        const isOverallCooling = isCooling && (singleCategoryCooling
            ? coolingCategories.length >= 1
            : (coolingCategories.includes('all') || (coolingCategories.length === 2)));

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
            header.className = 'flex items-start justify-between gap-1.5 acc-card-header';

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

            const dict = i18n[state.currentLanguage] || i18n.zh;
            info.innerHTML = buildAccountHeaderInnerHTML(acc, dict);
            
            const statusBadge = document.createElement('div');
            statusBadge.className = 'acc-status-badge';
            statusBadge.className = isOverallCooling
                ? 'acc-status-badge flex items-center gap-1 text-[10px] font-bold text-amber-600 bg-amber-50 dark:bg-amber-900/30 dark:text-amber-400 px-2 py-0.5 rounded text-nowrap self-start flex-shrink-0'
                : 'acc-status-badge flex items-center gap-1 text-[10px] font-bold text-emerald-600 bg-emerald-50 dark:bg-emerald-900/30 dark:text-emerald-400 px-2 py-0.5 rounded text-nowrap self-start flex-shrink-0';
            statusBadge.innerHTML = buildAccountStatusBadgeHTML({ isOverallCooling, isNvidiaAcc, isGrokAcc, minCooldownTime, dict });
            
            leftGroup.appendChild(checkboxEl);
            leftGroup.appendChild(info);
            header.appendChild(leftGroup);
            header.appendChild(statusBadge);
            card.appendChild(header);
            
            // ---- AI Credit Section ----
            const creditSection = document.createElement('div');
            creditSection.className = 'flex flex-col gap-1 border-t border-outline-variant/20 pt-2 acc-card-credit';
            
            if (acc.provider === 'antigravity') {
                card.classList.add('has-credit');
                const creditHeader = document.createElement('div');
                creditHeader.className = 'flex justify-between items-center';
                
                const creditTitle = document.createElement('span');
                creditTitle.className = 'text-[11px] font-semibold text-outline dark:text-outline-variant';
                creditTitle.textContent = dict.aiCreditTitle || 'AI 积分 (AI Credit)';
                
                const creditValue = document.createElement('span');
                creditValue.className = 'acc-credit-value text-[11px] font-bold text-on-surface dark:text-white font-data-mono';
                const creditVal = typeof acc.credits === 'number' ? `$${acc.credits.toFixed(2)}` : (dict.creditNotLoaded || '未加载');
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
                    <span class="text-outline dark:text-outline-variant">${dict.deductExcessCredit || '使用积分抵扣超额度部分'}</span>
                    <div class="relative inline-block w-8 align-middle transition duration-200 ease-in flex-shrink-0 ml-2">
                        <input class="${TOGGLE_CHECKBOX_OFF}"
                            id="${overagesSwitchId}" type="checkbox" ${isOveragesChecked ? 'checked' : ''}/>
                        <label class="${TOGGLE_LABEL_OFF}" for="${overagesSwitchId}"></label>
                    </div>
                `;

                const overagesCheckbox = overagesToggleWrapper.querySelector('input') as HTMLInputElement;
                const overagesLabel = overagesToggleWrapper.querySelector('label') as HTMLLabelElement;

                overagesCheckbox.addEventListener('change', (e: any) => {
                    const enabled = e.target.checked;
                    ipcRenderer.send('accounts:toggle-overages', acc.id, enabled);
                    acc.enableOverages = enabled;
                    applySwitchState(overagesCheckbox, overagesLabel, enabled);
                    updateAggregateQuotaUI();
                });

                applySwitchState(overagesCheckbox, overagesLabel, isOveragesChecked);

                creditSection.appendChild(overagesToggleWrapper);
            } else {
                card.classList.remove('has-credit');
                creditSection.classList.add('hidden-grid-placeholder');
            }
            card.appendChild(creditSection);

            // ---- Quota Section ----
            const quotaSection = document.createElement('div');
            quotaSection.className = 'flex flex-col gap-1.5 border-t border-outline-variant/20 pt-2 acc-card-quota';

            const quotaHeader = document.createElement('div');
            quotaHeader.className = 'flex justify-between items-center';
            quotaHeader.innerHTML = `<span class="text-[11px] font-semibold text-outline dark:text-outline-variant">${dict.remainingQuota || '剩余配额'}</span>`;

            refreshBtn = document.createElement('button');
            refreshBtn.className = 'text-outline hover:text-primary transition-colors z-10';
            refreshBtn.title = dict.refreshQuota || '刷新配额';
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
            footer.className = 'flex justify-between items-center pt-3 border-t border-outline-variant/20 mt-auto acc-card-footer';
            
            const toggleWrapper = document.createElement('div');
            toggleWrapper.className = 'flex items-center gap-1.5 select-none cursor-pointer';
            
            const switchId = `accToggle-${acc.id}`;
            const isChecked = acc.enabled !== false;
            toggleWrapper.innerHTML = `
                <div class="relative inline-block w-8 mr-1 align-middle select-none transition duration-200 ease-in">
                    <input class="${TOGGLE_CHECKBOX_OFF}"
                        id="${switchId}" type="checkbox" ${isChecked ? 'checked' : ''}/>
                    <label class="${TOGGLE_LABEL_OFF}" for="${switchId}"></label>
                </div>
                <span class="text-[11px] font-bold ${isChecked ? 'text-emerald-500' : 'text-outline'} acc-toggle-label-text">${isChecked ? (dict.statusEnabledAccount || '启用中') : (dict.statusDisabledAccount || '已停用')}</span>
            `;

            const checkbox = toggleWrapper.querySelector('input') as HTMLInputElement;
            const accLabel = toggleWrapper.querySelector('label') as HTMLLabelElement;
            const labelText = toggleWrapper.querySelector('span') as HTMLSpanElement;

            checkbox.addEventListener('change', (e: any) => {
                const enabled = e.target.checked;
                if (acc.provider === 'nvidia') {
                    ipcRenderer.send('nvidia:toggle-enabled', acc.id, enabled);
                } else if (acc.provider === 'other') {
                    ipcRenderer.send('other:toggle-enabled', acc.id, enabled);
                } else if (acc.provider === 'grok') {
                    void ipcRenderer.invoke('grok:toggle-enabled', acc.id, enabled);
                } else if (acc.provider === 'workbuddy') {
                    void ipcRenderer.invoke('workbuddy:toggle-enabled', acc.id, enabled);
                } else {
                    ipcRenderer.send('accounts:toggle-enabled', acc.id, enabled);
                }
                acc.enabled = enabled;
                applySwitchState(checkbox, accLabel, enabled, { labelText, dict, card: card! });
                updateAggregateQuotaUI();
            });

            applySwitchState(checkbox, accLabel, isChecked, { dict, card });
            
            const btnDownload = document.createElement('button');
            btnDownload.className = 'text-[11px] font-medium text-primary hover:text-primary/80 hover:bg-primary/5 dark:hover:bg-primary/10 px-2 py-1 rounded transition-colors flex items-center gap-1 z-10 whitespace-nowrap flex-shrink-0';
            btnDownload.innerHTML = `<span class="material-symbols-outlined text-[14px]">download</span> ${dict.btnExport || '导出'}`;
            btnDownload.title = dict.exportAccountTitle || '导出该账号文件';
            btnDownload.onclick = () => {
                // 走统一文件服务(invoke)(accountsController 注册的全局方法):
                // 后端负责对话框 + 目录记忆 + 保存成功后精确定位文件。
                const fn = (window as any).exportSingleAccount;
                if (typeof fn === 'function') {
                    fn(acc.id);
                } else {
                    // 兜底仍走 invoke,避免单向 send 无法回传成功态导致误报。
                    void ipcRenderer.invoke('accounts:export-single', acc.id);
                }
            };

            const btnDelete = document.createElement('button');
            btnDelete.className = 'text-[11px] font-medium text-red-500 hover:text-red-600 hover:bg-red-50 dark:hover:bg-red-900/20 px-2 py-1 rounded transition-colors flex items-center gap-1 z-10 whitespace-nowrap flex-shrink-0';
            btnDelete.innerHTML = `<span class="material-symbols-outlined text-[14px]">delete</span> ${dict.btnRemove || '移除'}`;
            btnDelete.onclick = async () => {
                if (await $confirm((dict.removeAccountConfirm || '确定要移除账号 {email} 吗？').replace('{email}', acc.email))) {
                    if (acc.provider === 'nvidia') {
                        ipcRenderer.send('nvidia:remove', acc.id);
                    } else if (acc.provider === 'other') {
                        ipcRenderer.send('other:remove', acc.id);
                    } else if (acc.provider === 'grok') {
                        // 后端 grok:remove 成功后会 emitAccountsRes 主动广播,前端无需手动刷新。
                        void ipcRenderer.invoke('grok:remove', acc.id);
                    } else if (acc.provider === 'workbuddy') {
                        void ipcRenderer.invoke('workbuddy:remove', acc.id);
                    } else {
                        ipcRenderer.send('accounts:remove', acc.id);
                    }
                }
            };

            // 编辑按钮:仅 API Key / WorkBuddy 型号池(NVIDIA / Other / Grok / WorkBuddy)提供,复用各自添加账号模态框做预填编辑。
            const btnEdit = document.createElement('button');
            btnEdit.className = 'text-[11px] font-medium text-primary hover:text-primary/80 hover:bg-primary/5 dark:hover:bg-primary/10 px-2 py-1 rounded transition-colors flex items-center gap-1 z-10 whitespace-nowrap flex-shrink-0';
            btnEdit.innerHTML = `<span class="material-symbols-outlined text-[14px]">edit</span> ${dict.btnEdit || '编辑'}`;
            btnEdit.title = dict.editAccountTitle || '编辑该账号参数';
            btnEdit.onclick = () => {
                if (acc.provider === 'nvidia') {
                    openEditNvidiaAccount(acc);
                } else if (acc.provider === 'other') {
                    openEditOtherAccount(acc);
                } else if (acc.provider === 'grok') {
                    openEditGrokAccount(acc);
                } else if (acc.provider === 'workbuddy') {
                    openEditWorkBuddyAccount(acc);
                }
            };

            // 解冻按钮:仅 Grok 号池 + 当前处于冷却态时显示。点击经二次确认后 invoke grok:clear-cooldown,
            // 后端清冷却 + emitAccountsRes,前端卡片徽标即时翻绿。与编辑/导出/删除同级,置于最左以便冷却态显眼。
            const btnThaw = document.createElement('button');
            btnThaw.className = 'text-[11px] font-medium text-amber-600 hover:text-amber-700 dark:text-amber-400 dark:hover:text-amber-300 hover:bg-amber-50 dark:hover:bg-amber-900/20 px-2 py-1 rounded transition-colors flex items-center gap-1 z-10 whitespace-nowrap flex-shrink-0';
            btnThaw.innerHTML = `<span class="material-symbols-outlined text-[14px]">ac_unit</span> ${dict.grokThaw || '解冻'}`;
            btnThaw.title = dict.grokThawSingleBtnTitle || '手动解冻该账号(立即清除冷却)';
            btnThaw.setAttribute('data-grok-thaw-btn', '');
            btnThaw.onclick = async () => {
                const msg = (dict.grokThawConfirmSingle || '确定要手动解冻账号 {email} 吗？该账号将立即恢复可承接请求。').replace('{email}', acc.email);
                if (await $confirm(msg)) {
                    const res = await ipcRenderer.invoke('grok:clear-cooldown', acc.id);
                    if (!res || res.success !== true) {
                        alert((res && res.error) || (dict.grokThawFailed || '解冻失败'));
                    }
                }
            };

            // WorkBuddy 每日签到按钮
            const btnWbCheckin = document.createElement('button');
            const todayStr = new Date().toISOString().split('T')[0];
            const isWbCheckedIn = acc.lastCheckinDate === todayStr;
            btnWbCheckin.className = isWbCheckedIn
                ? 'text-[11px] font-medium text-emerald-600 bg-emerald-50 dark:bg-emerald-900/20 px-2 py-1 rounded transition-colors flex items-center gap-1 z-10 whitespace-nowrap flex-shrink-0 cursor-pointer'
                : 'text-[11px] font-medium text-primary hover:text-primary-focus hover:bg-primary/10 px-2 py-1 rounded transition-colors flex items-center gap-1 z-10 whitespace-nowrap flex-shrink-0 cursor-pointer';
            btnWbCheckin.innerHTML = isWbCheckedIn
                ? `<span class="material-symbols-outlined text-[14px]">done</span> ${acc.checkinStreak > 0 ? `已签到(${acc.checkinStreak}天)` : '已签到'}`
                : `<span class="material-symbols-outlined text-[14px]">event_available</span> 签到`;
            btnWbCheckin.title = isWbCheckedIn ? '今日已签到，点击可强制再次请求上游探测' : '点击执行每日签到领积分';
            btnWbCheckin.setAttribute('data-wb-checkin-btn', '');
            btnWbCheckin.onclick = async () => {
                btnWbCheckin.disabled = true;
                const oldHTML = btnWbCheckin.innerHTML;
                btnWbCheckin.innerHTML = `<span class="material-symbols-outlined text-[14px] animate-spin">refresh</span> 签到中...`;
                try {
                    const res = await ipcRenderer.invoke('workbuddy:checkin', acc.id, true);
                    if (res && res.success && res.result) {
                        alert(res.result.message || '签到成功！');
                    } else if (res && res.error) {
                        alert(res.error);
                    }
                } catch (err: any) {
                    alert('签到异常: ' + (err.message || String(err)));
                } finally {
                    btnWbCheckin.disabled = false;
                    btnWbCheckin.innerHTML = oldHTML;
                }
            };

            const rightGroup = document.createElement('div');
            rightGroup.className = 'flex items-center gap-1 flex-shrink-0';
            if (acc.provider === 'nvidia' || acc.provider === 'other' || acc.provider === 'grok' || acc.provider === 'workbuddy') {
                rightGroup.appendChild(btnEdit);
            }
            if (acc.provider === 'workbuddy') {
                rightGroup.appendChild(btnWbCheckin);
            }
            // 仅 Grok 号池且整体冷却中时插入解冻按钮(置于编辑之后、导出之前)。
            if (acc.provider === 'grok' && isOverallCooling) {
                rightGroup.appendChild(btnThaw);
            }
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
                const dict = i18n[state.currentLanguage] || i18n.zh;
                infoHeader.innerHTML = buildAccountHeaderInnerHTML(acc, dict);
            }

            // 1. Update cooldown status
            const statusBadge = card.querySelector('.acc-status-badge') as HTMLElement;
            const dict = i18n[state.currentLanguage] || i18n.zh;
            if (statusBadge) {
                statusBadge.className = isOverallCooling
                    ? 'acc-status-badge flex items-center gap-1 text-[10px] font-bold text-amber-600 bg-amber-50 dark:bg-amber-900/30 dark:text-amber-400 px-2 py-0.5 rounded text-nowrap self-start flex-shrink-0'
                    : 'acc-status-badge flex items-center gap-1 text-[10px] font-bold text-emerald-600 bg-emerald-50 dark:bg-emerald-900/30 dark:text-emerald-400 px-2 py-0.5 rounded text-nowrap self-start flex-shrink-0';
                statusBadge.innerHTML = buildAccountStatusBadgeHTML({ isOverallCooling, isNvidiaAcc, isGrokAcc, minCooldownTime, dict });
            }

            // 2. Update AI Credits (Antigravity only)
            if (acc.provider === 'antigravity') {
                card.classList.add('has-credit');
                const creditValue = card.querySelector('.acc-credit-value') as HTMLElement;
                if (creditValue) {
                    const creditVal = typeof acc.credits === 'number' ? `$${acc.credits.toFixed(2)}` : (dict.creditNotLoaded || '未加载');
                    creditValue.textContent = creditVal;
                }

                const overagesCheckbox = card.querySelector(`#overagesToggle-${acc.id}`) as HTMLInputElement;
                const overagesLabel = card.querySelector(`[for="overagesToggle-${acc.id}"]`) as HTMLElement;
                if (overagesCheckbox && overagesLabel) {
                    const isOveragesChecked = acc.enableOverages === true;
                    if (overagesCheckbox.checked !== isOveragesChecked) {
                        overagesCheckbox.checked = isOveragesChecked;
                        applySwitchState(overagesCheckbox, overagesLabel, isOveragesChecked);
                    }
                }
            } else {
                card.classList.remove('has-credit');
            }

            // 3. Update enabled/disabled status and style classes
            const checkbox = card.querySelector(`#accToggle-${acc.id}`) as HTMLInputElement;
            const accLabel = card.querySelector(`[for="accToggle-${acc.id}"]`) as HTMLElement;
            const labelText = card.querySelector('.acc-toggle-label-text') as HTMLSpanElement;
            const isChecked = acc.enabled !== false;
            if (checkbox && accLabel && labelText) {
                if (checkbox.checked !== isChecked) {
                    checkbox.checked = isChecked;
                    applySwitchState(checkbox, accLabel, isChecked, { labelText, dict, card });
                }
            }

            // 4. Update selected checkbox state
            const checkboxEl = card.querySelector('.account-card-checkbox') as HTMLInputElement | null;
            if (checkboxEl) {
                checkboxEl.checked = state.selectedAccountIds.includes(acc.id);
            }

            // 4.5 Update thaw button visibility:Grok 卡片在冷却态切换时同步显隐「解冻」按钮,
            // 避免解冻后按钮残留(patch 分支不重建 DOM)。与 statusBadge 同据 isOverallCooling 联动。
            if (acc.provider === 'grok') {
                const existingThaw = card.querySelector('[data-grok-thaw-btn]') as HTMLButtonElement | null;
                if (isOverallCooling && !existingThaw) {
                    // 临时从冷却态切到冷却态但按钮缺失:补建。正常场景下按钮在 create 分支已建。
                    // (此分支极少触发,主要为 HMR/异常 DOM 篡改兜底)
                } else if (!isOverallCooling && existingThaw) {
                    // 已解冻:移除解冻按钮,徽标已翻绿。
                    existingThaw.remove();
                }
            }

            // 4.6 Update WorkBuddy checkin button state in patch branch
            if (acc.provider === 'workbuddy') {
                const existingWbBtn = card.querySelector('[data-wb-checkin-btn]') as HTMLButtonElement | null;
                if (existingWbBtn) {
                    const todayStr = new Date().toISOString().split('T')[0];
                    const isWbCheckedIn = acc.lastCheckinDate === todayStr;
                    existingWbBtn.className = isWbCheckedIn
                        ? 'text-[11px] font-medium text-emerald-600 bg-emerald-50 dark:bg-emerald-900/20 px-2 py-1 rounded transition-colors flex items-center gap-1 z-10 whitespace-nowrap flex-shrink-0 cursor-pointer'
                        : 'text-[11px] font-medium text-primary hover:text-primary-focus hover:bg-primary/10 px-2 py-1 rounded transition-colors flex items-center gap-1 z-10 whitespace-nowrap flex-shrink-0 cursor-pointer';
                    existingWbBtn.innerHTML = isWbCheckedIn
                        ? `<span class="material-symbols-outlined text-[14px]">done</span> ${acc.checkinStreak > 0 ? `已签到(${acc.checkinStreak}天)` : '已签到'}`
                        : `<span class="material-symbols-outlined text-[14px]">event_available</span> 签到`;
                    existingWbBtn.title = isWbCheckedIn ? '今日已签到，点击可强制再次请求上游探测' : '点击执行每日签到领积分';
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

    renderPaginationUI(totalItems, startIndex, endIndex, totalPages);
    document.dispatchEvent(new CustomEvent('account-selection-changed'));

    // NVIDIA 冷却秒级翻牌：卡片渲染完即时 ensure 定时器（幂等），驱动 .nvidia-cooldown-tick 每秒更新。
    ensureNvidiaCooldownTimer();
    // Grok 冷却秒级翻牌：同构独立定时器,驱动 .grok-cooldown-tick 每秒更新(与 nvidia 各扫各的)。
    ensureGrokCooldownTimer();
}

function renderPaginationUI(totalItems: number, startIndex: number, endIndex: number, totalPages: number) {
    const info = document.getElementById('accountsPaginationInfo');
    const btnPrev = document.getElementById('btnPrevAccountPage') as HTMLButtonElement | null;
    const btnNext = document.getElementById('btnNextAccountPage') as HTMLButtonElement | null;
    const numbersContainer = document.getElementById('accountPageNumbers');

    const dict = i18n[state.currentLanguage] || i18n.zh;
    if (info) {
        if (totalItems === 0) {
            info.textContent = (dict.showingAccountsRange || '显示 {start} - {end} 条，共 {total} 条')
                .replace('{start}', '0')
                .replace('{end}', '0')
                .replace('{total}', '0');
        } else {
            info.textContent = (dict.showingAccountsRange || '显示 {start} - {end} 条，共 {total} 条')
                .replace('{start}', String(startIndex + 1))
                .replace('{end}', String(endIndex))
                .replace('{total}', String(totalItems));
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

    // 每次重绘账号列表后同步刷新 Other 二级组名子 Tab(动态按钮文本需随语言/数据重算)。
    // 语言切换(setLanguage)只调 renderAccounts,不单独调 renderOtherGroupTabs,故在此兜底。
    renderOtherGroupTabs();
}


// ==================== 外部导入契约 re-export 桥接 ====================
// 以下 re-export 保持 accountsRenderer 作为 hub 的对外导入路径 ./accountsRenderer 零改动,
// 使 nvidiaCooldownTimer / accountsController / quotaRender / triggerTestModal 等消费方无需改 import。
export { getNvidiaCooldownRemaining } from './accountCardHelpers';   // nvidiaCooldownTimer.ts 经 hub 取秒级翻牌剩余时间
export { updateAggregateQuotaUI } from './aggregateQuotaUI';        // accountsController + quotaRender 经 hub 取聚合配额面板
export { loadAccountQuota } from './quotaBarsRenderer';            // triggerTestModal + accountsController 经 hub 取单账号配额拉取



