import state from './dashboardState';
import i18n from '../shared/i18n';

const PANEL_ID = 'usageStatsPanel';
const openAccounts = new Set<string>();

let currentUsageData: any = null;
let searchQuery = '';
let currentPage = 1;
const pageSize = 10;

export function escapeHtml(value: any): string {
    return String(value == null ? '' : value)
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;')
        .replace(/'/g, '&#39;');
}

export function formatNumber(value: any): string {
    const n = Number(value) || 0;
    return n.toLocaleString();
}

export function formatMoney(value: any): string {
    return `$${(Number(value) || 0).toFixed(4)}`;
}

export function formatHitRate(tokens: number, cachedTokens: number, requests: number, cacheHitRequests: number): string {
    const tokenRate = tokens > 0 ? (cachedTokens / tokens) * 100 : 0;
    const requestRate = requests > 0 ? (cacheHitRequests / requests) * 100 : 0;
    return `${tokenRate.toFixed(1)}% / ${requestRate.toFixed(1)}%`;
}

export function getToneClasses(tone: string): string {
    switch (tone) {
        case 'primary':
            return 'text-primary dark:text-primary-fixed-dim';
        case 'emerald':
            return 'text-emerald-600 dark:text-emerald-400';
        case 'amber':
            return 'text-amber-600 dark:text-amber-400';
        default:
            return 'text-slate-600 dark:text-slate-300';
    }
}

export function sortUsageItems(items: any[]): any[] {
    return [...items].sort((a, b) => {
        const costDelta = (Number(b.totalCost) || 0) - (Number(a.totalCost) || 0);
        if (costDelta !== 0) return costDelta;
        return (Number(b.requestCount) || 0) - (Number(a.requestCount) || 0);
    });
}

export function sortModelsByTokens(items: any[]): any[] {
    return [...items].sort((a, b) => {
        const totalA = (Number(a.inputTokens) || 0) + (Number(a.outputTokens) || 0);
        const totalB = (Number(b.inputTokens) || 0) + (Number(b.outputTokens) || 0);
        if (totalB !== totalA) return totalB - totalA;
        return (Number(b.requestCount) || 0) - (Number(a.requestCount) || 0);
    });
}

export function ensurePanel(): HTMLElement | null {
    let panel = document.getElementById(PANEL_ID);
    if (panel) return panel;

    const host = document.getElementById('view-usage-details');
    if (!host) return null;

    panel = document.createElement('div');
    panel.id = PANEL_ID;
    panel.className = 'w-full';
    host.appendChild(panel);
    return panel;
}

export function renderSummaryChip(label: string, value: string, tone = 'slate'): string {
    return `
        <div class="flex flex-col gap-0.5 min-w-0">
            <span class="text-[10px] uppercase tracking-normal text-outline dark:text-outline-variant">${escapeHtml(label)}</span>
            <span class="text-[13px] font-bold ${getToneClasses(tone)}">${escapeHtml(value)}</span>
        </div>
    `;
}

export function renderModelRows(models: any): string {
    const dict = i18n[state.currentLanguage] || i18n.zh;
    const sorted = sortModelsByTokens(Object.values(models || {}));
    if (sorted.length === 0) {
        return `<tr><td colspan="10" class="px-3 py-3 text-center text-[12px] text-outline dark:text-outline-variant">${dict.usage_noModelUsage || '暂无模型用量'}</td></tr>`;
    }

    return sorted.map(model => {
        return `
            <tr class="border-b border-outline-variant/10 dark:border-white/5">
                <td class="px-3 py-2 font-semibold text-on-surface dark:text-white">${escapeHtml(model.model || 'unknown')}</td>
                <td class="px-3 py-2 text-right">${formatNumber(model.requestCount)}</td>
                <td class="px-3 py-2 text-right text-outline dark:text-outline-variant">${formatNumber(model.inputTokens)}</td>
                <td class="px-3 py-2 text-right text-on-surface dark:text-white">${formatNumber(model.outputTokens)}</td>
                <td class="px-3 py-2 text-right text-slate-500 dark:text-slate-400">${formatNumber(model.cachedTokens)}</td>
                <td class="px-3 py-2 text-right">${formatHitRate(model.inputTokens || 0, model.cachedTokens || 0, model.requestCount || 0, model.cacheHitRequests || 0)}</td>
                <td class="px-3 py-2 text-right text-amber-600 dark:text-amber-400 font-semibold">${formatMoney(model.inputCost)}</td>
                <td class="px-3 py-2 text-right text-sky-600 dark:text-sky-400 font-semibold">${formatMoney(model.outputCost)}</td>
                <td class="px-3 py-2 text-right text-violet-600 dark:text-violet-400 font-semibold">${formatMoney(model.cachedCost)}</td>
                <td class="px-3 py-2 text-right text-primary dark:text-primary-fixed-dim font-bold">${formatMoney(model.totalCost)}</td>
            </tr>
        `;
    }).join('');
}

export function renderAccountBlock(account: any): string {
    const dict = i18n[state.currentLanguage] || i18n.zh;
    const tokens = (Number(account.inputTokens) || 0) + (Number(account.outputTokens) || 0);
    const provider = account.provider || 'direct';
    const badgeClass = provider === 'antigravity'
        ? 'bg-primary/10 text-primary border-primary/20'
        : provider === 'project'
            ? 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 border-emerald-500/20'
            : 'bg-slate-100 text-slate-500 dark:bg-white/10 dark:text-slate-300 border-outline-variant/30';

    const accountKey = account.email || account.accountId || 'Direct';
    const isOpen = openAccounts.has(accountKey);

    return `
        <details data-account-key="${escapeHtml(accountKey)}" ${isOpen ? 'open' : ''} class="group border border-outline-variant/25 rounded-xl overflow-hidden bg-white dark:bg-[#1a1f30]">
            <summary class="cursor-pointer list-none px-4 py-3 flex items-center justify-between gap-3 hover:bg-slate-50/60 dark:hover:bg-white/5">
                <div class="min-w-0 flex items-center gap-2">
                    <span class="text-[13px] font-bold text-on-surface dark:text-white truncate" title="${escapeHtml(account.email || account.accountId || 'Direct')}">${escapeHtml(account.email || account.accountId || 'Direct')}</span>
                    <span class="text-[9px] font-bold uppercase px-1.5 py-0.5 rounded border ${badgeClass}">${escapeHtml(provider)}</span>
                </div>
                <div class="flex flex-wrap justify-end gap-3 text-right text-[11px] min-w-0">
                    <div class="flex flex-col">
                        <span class="text-outline dark:text-outline-variant">${dict.usage_calls || '调用'}</span>
                        <span class="font-bold text-on-surface dark:text-white">${formatNumber(account.requestCount)}</span>
                    </div>
                    <div class="flex flex-col">
                        <span class="text-outline dark:text-outline-variant">Tokens</span>
                        <span class="font-bold text-on-surface dark:text-white">${formatNumber(tokens)}</span>
                    </div>
                    <div class="flex flex-col">
                        <span class="text-outline dark:text-outline-variant">${dict.usage_cache || '缓存'}</span>
                        <span class="font-bold text-on-surface dark:text-white">${formatNumber(account.cachedTokens)}</span>
                    </div>
                    <div class="flex flex-col">
                        <span class="text-outline dark:text-outline-variant">${dict.usage_hitRate || '命中率'}</span>
                        <span class="font-bold text-on-surface dark:text-white">${formatHitRate(account.inputTokens || 0, account.cachedTokens || 0, account.requestCount || 0, account.cacheHitRequests || 0)}</span>
                    </div>
                    <div class="flex flex-col">
                        <span class="text-outline dark:text-outline-variant">${dict.usage_inputCost || '输入成本'}</span>
                        <span class="font-bold text-amber-600 dark:text-amber-400">${formatMoney(account.inputCost)}</span>
                    </div>
                    <div class="flex flex-col">
                        <span class="text-outline dark:text-outline-variant">${dict.usage_outputCost || '输出成本'}</span>
                        <span class="font-bold text-sky-600 dark:text-sky-400">${formatMoney(account.outputCost)}</span>
                    </div>
                    <div class="flex flex-col">
                        <span class="text-outline dark:text-outline-variant">${dict.usage_cacheCost || '缓存成本'}</span>
                        <span class="font-bold text-violet-600 dark:text-violet-400">${formatMoney(account.cachedCost)}</span>
                    </div>
                    <div class="flex flex-col">
                        <span class="text-outline dark:text-outline-variant">${dict.usage_totalCost || '总成本'}</span>
                        <span class="font-bold text-primary dark:text-primary-fixed-dim">${formatMoney(account.totalCost)}</span>
                    </div>
                </div>
            </summary>
            <div class="border-t border-outline-variant/20 bg-slate-50/40 dark:bg-white/5">
                <div class="overflow-x-auto">
                    <table class="w-full text-left table-fixed border-collapse">
                        <thead>
                            <tr class="border-b border-outline-variant/40">
                                <th class="px-3 py-2 text-[10px] font-bold text-outline uppercase tracking-wider">${dict.usage_model || '模型'}</th>
                                <th class="px-3 py-2 text-[10px] font-bold text-outline uppercase tracking-wider text-right">${dict.usage_calls || '调用'}</th>
                                <th class="px-3 py-2 text-[10px] font-bold text-outline uppercase tracking-wider text-right">${dict.usage_input || '输入'}</th>
                                <th class="px-3 py-2 text-[10px] font-bold text-outline uppercase tracking-wider text-right">${dict.usage_output || '输出'}</th>
                                <th class="px-3 py-2 text-[10px] font-bold text-outline uppercase tracking-wider text-right">${dict.usage_cache || '缓存'}</th>
                                <th class="px-3 py-2 text-[10px] font-bold text-outline uppercase tracking-wider text-right">${dict.usage_hitRate || '命中率'}</th>
                                <th class="px-3 py-2 text-[10px] font-bold text-outline uppercase tracking-wider text-right">${dict.usage_inputCost || '输入成本'}</th>
                                <th class="px-3 py-2 text-[10px] font-bold text-outline uppercase tracking-wider text-right">${dict.usage_outputCost || '输出成本'}</th>
                                <th class="px-3 py-2 text-[10px] font-bold text-outline uppercase tracking-wider text-right">${dict.usage_cacheCost || '缓存成本'}</th>
                                <th class="px-3 py-2 text-[10px] font-bold text-outline uppercase tracking-wider text-right">${dict.usage_totalCost || '总成本'}</th>
                            </tr>
                        </thead>
                        <tbody class="text-[12px] font-data-mono text-on-surface dark:text-white">
                            ${renderModelRows(account.models)}
                        </tbody>
                    </table>
                </div>
            </div>
        </details>
    `;
}

function renderPageButtons(totalPages: number, current: number): string {
    if (totalPages <= 1) return '';
    let html = '';
    for (let i = 1; i <= totalPages; i++) {
        if (i === current) {
            html += `<button class="px-2 py-0.5 rounded bg-primary text-white text-[11px] font-bold shadow-sm">${i}</button>`;
        } else {
            html += `<button class="btn-usage-page-num px-2 py-0.5 rounded text-outline hover:text-primary hover:bg-primary/5 text-[11px] transition-colors" data-page="${i}">${i}</button>`;
        }
    }
    return html;
}

export function render(usage?: any) {
    if (usage !== undefined) {
        currentUsageData = usage;
    }

    const panel = ensurePanel();
    if (!panel) return;

    const allAccounts = currentUsageData && currentUsageData.accounts ? Object.values(currentUsageData.accounts) : [];
    const dict = i18n[state.currentLanguage] || i18n.zh;
    if (allAccounts.length === 0) {
        panel.classList.remove('hidden');
        panel.innerHTML = `
            <div class="glass-card rounded-xl p-8 flex flex-col items-center justify-center text-outline/50 border border-outline-variant/30">
                <span class="material-symbols-outlined text-[48px] mb-2">analytics</span>
                <span class="text-[13px]">${dict.usage_noAccountUsage || '暂无账号用量统计数据'}</span>
            </div>
        `;
        return;
    }

    const totals = currentUsageData.totals || {};
    const sortedAccounts = sortUsageItems(allAccounts);

    // 过滤账号列表
    const query = searchQuery.trim().toLowerCase();
    const filteredAccounts = sortedAccounts.filter((acc: any) => {
        if (!query) return true;
        const name = (acc.email || acc.accountId || '').toLowerCase();
        return name.includes(query);
    });

    // 计算分页
    const totalItems = filteredAccounts.length;
    const totalPages = Math.max(1, Math.ceil(totalItems / pageSize));
    if (currentPage > totalPages) currentPage = totalPages;
    if (currentPage < 1) currentPage = 1;

    const startIdx = (currentPage - 1) * pageSize;
    const endIdx = Math.min(startIdx + pageSize, totalItems);
    const pageAccounts = filteredAccounts.slice(startIdx, endIdx);

    const startItem = totalItems > 0 ? startIdx + 1 : 0;
    const endItem = endIdx;

    const tokenHits = totals.inputTokens > 0 ? (totals.cachedTokens / totals.inputTokens) * 100 : 0;
    const requestHits = totals.requestCount > 0 ? (totals.cacheHitRequests / totals.requestCount) * 100 : 0;

    // 绘制整体结构（如果结构已存在，只需更新内容与绑定，避免重新构建整个DOM导致input失去焦点）
    const containerExists = document.getElementById('usageContainerCard') !== null;

    const showingTemplate = dict.usage_showingEntries || "显示 {start} - {end} 条，共 {total} 条";
    const showingText = showingTemplate
        .replace('{start}', String(startItem))
        .replace('{end}', String(endItem))
        .replace('{total}', String(totalItems));

    if (!containerExists) {
        panel.classList.remove('hidden');
        panel.innerHTML = `
            <div id="usageContainerCard" class="glass-card rounded-xl flex flex-col flex-1 border border-outline-variant/30 min-h-[400px]">
                <!-- 工具栏与统计汇总 -->
                <div class="p-4 border-b border-outline-variant/30 flex flex-wrap items-center justify-between gap-4 bg-slate-50/50 dark:bg-white/5 rounded-t-xl">
                    <div class="flex items-center gap-3">
                        <div class="relative flex items-center">
                            <span class="material-symbols-outlined absolute left-2.5 text-[16px] text-outline pointer-events-none">search</span>
                            <input type="text" id="inputUsageSearch" value="${escapeHtml(searchQuery)}" placeholder="${dict.usage_searchPlaceholder || '按账号名称/邮箱查询...'}" class="pl-8 pr-3 py-1.5 bg-white dark:bg-[#1a1f30] border border-outline-variant/40 rounded-lg text-[12px] text-on-surface dark:text-white focus:outline-none focus:border-primary w-56 sm:w-64 transition-all" />
                        </div>
                    </div>
                    <div class="flex items-center gap-4 text-right" id="usageSummaryChips">
                        ${renderSummaryChip(dict.usage_accounts || '账号数', String(totalItems), 'primary')}
                        ${renderSummaryChip(dict.usage_callsCount || '调用次数', formatNumber(totals.requestCount), 'slate')}
                        ${renderSummaryChip(dict.usage_totalCost || '总成本', formatMoney(totals.totalCost), 'emerald')}
                        ${renderSummaryChip(dict.usage_hitRate || '命中率', `${tokenHits.toFixed(1)}% / ${requestHits.toFixed(1)}%`, 'amber')}
                    </div>
                </div>
                
                <!-- 账号用量数据块列表 -->
                <div class="p-4 flex flex-col gap-3 flex-grow overflow-y-auto" id="usageAccountsList">
                    ${pageAccounts.length > 0 
                        ? pageAccounts.map(renderAccountBlock).join('')
                        : `<div class="flex flex-col items-center justify-center py-12 text-outline/50">
                             <span class="material-symbols-outlined text-[48px] mb-2">search_off</span>
                             <span class="text-[13px]">${dict.usage_noMatchingData || '未找到符合条件的账号用量数据'}</span>
                           </div>`
                    }
                </div>

                <!-- 底部分页栏 -->
                <div class="flex flex-wrap items-center justify-between px-4 py-3 border-t border-outline-variant/20 text-[12px]" id="usagePaginationFooter">
                    <span class="text-outline text-[11px]" id="usagePaginationInfo">${showingText}</span>
                    <div class="flex items-center gap-1.5">
                        <button id="btnPrevUsagePage" ${currentPage <= 1 ? 'disabled' : ''} class="px-2 py-1 rounded border border-outline-variant/30 text-outline hover:text-primary hover:bg-primary/5 disabled:opacity-40 disabled:pointer-events-none transition-colors text-[11px] flex items-center gap-0.5">
                            <span class="material-symbols-outlined text-[14px]">chevron_left</span> ${dict.usage_prevPage || '上一页'}
                        </button>
                        <div id="usagePageNumbers" class="flex items-center gap-1">
                            ${renderPageButtons(totalPages, currentPage)}
                        </div>
                        <button id="btnNextUsagePage" ${currentPage >= totalPages ? 'disabled' : ''} class="px-2 py-1 rounded border border-outline-variant/30 text-outline hover:text-primary hover:bg-primary/5 disabled:opacity-40 disabled:pointer-events-none transition-colors text-[11px] flex items-center gap-0.5">
                            ${dict.usage_nextPage || '下一页'} <span class="material-symbols-outlined text-[14px]">chevron_right</span>
                        </button>
                    </div>
                </div>
            </div>
        `;

        // 首次初始化事件绑定
        const searchInput = document.getElementById('inputUsageSearch') as HTMLInputElement;
        if (searchInput) {
            searchInput.addEventListener('input', (e) => {
                searchQuery = (e.target as HTMLInputElement).value;
                currentPage = 1;
                render();
            });
        }
    } else {
        // 更新汇总芯片
        const chipsEl = document.getElementById('usageSummaryChips');
        if (chipsEl) {
            chipsEl.innerHTML = `
                ${renderSummaryChip(dict.usage_accounts || '账号数', String(totalItems), 'primary')}
                ${renderSummaryChip(dict.usage_callsCount || '调用次数', formatNumber(totals.requestCount), 'slate')}
                ${renderSummaryChip(dict.usage_totalCost || '总成本', formatMoney(totals.totalCost), 'emerald')}
                ${renderSummaryChip(dict.usage_hitRate || '命中率', `${tokenHits.toFixed(1)}% / ${requestHits.toFixed(1)}%`, 'amber')}
            `;
        }

        // 更新账号列表内容
        const listEl = document.getElementById('usageAccountsList');
        if (listEl) {
            listEl.innerHTML = pageAccounts.length > 0 
                ? pageAccounts.map(renderAccountBlock).join('')
                : `<div class="flex flex-col items-center justify-center py-12 text-outline/50">
                     <span class="material-symbols-outlined text-[48px] mb-2">search_off</span>
                     <span class="text-[13px]">${dict.usage_noMatchingData || '未找到符合条件的账号用量数据'}</span>
                   </div>`;
        }

        // 更新分页栏
        const infoEl = document.getElementById('usagePaginationInfo');
        if (infoEl) {
            infoEl.textContent = showingText;
        }

        const pageNumsEl = document.getElementById('usagePageNumbers');
        if (pageNumsEl) {
            pageNumsEl.innerHTML = renderPageButtons(totalPages, currentPage);
        }

        const prevBtn = document.getElementById('btnPrevUsagePage') as HTMLButtonElement;
        if (prevBtn) prevBtn.disabled = currentPage <= 1;

        const nextBtn = document.getElementById('btnNextUsagePage') as HTMLButtonElement;
        if (nextBtn) nextBtn.disabled = currentPage >= totalPages;
    }
}

export function init() {
    const panel = ensurePanel();
    if (!panel) return;

    // 1. 事件委托：使用捕获阶段监听 details 标签的 toggle 事件
    panel.addEventListener('toggle', (e) => {
        const target = e.target as HTMLElement;
        if (target && target.tagName.toLowerCase() === 'details') {
            const key = target.getAttribute('data-account-key');
            if (key) {
                const detailsEl = target as HTMLDetailsElement;
                if (detailsEl.open) {
                    openAccounts.add(key);
                } else {
                    openAccounts.delete(key);
                }
            }
        }
    }, true);

    // 2. 事件委托：监听分页按钮点击事件
    panel.addEventListener('click', (e) => {
        const target = e.target as HTMLElement;

        // 上一页
        const prevBtn = target.closest('#btnPrevUsagePage');
        if (prevBtn && currentPage > 1 && !(prevBtn as HTMLButtonElement).disabled) {
            currentPage--;
            render();
            return;
        }

        // 下一页
        const nextBtn = target.closest('#btnNextUsagePage');
        if (nextBtn && !(nextBtn as HTMLButtonElement).disabled) {
            const allAccounts = currentUsageData && currentUsageData.accounts ? Object.values(currentUsageData.accounts) : [];
            const query = searchQuery.trim().toLowerCase();
            const filteredAccounts = allAccounts.filter((acc: any) => {
                if (!query) return true;
                const name = (acc.email || acc.accountId || '').toLowerCase();
                return name.includes(query);
            });
            const totalItems = filteredAccounts.length;
            const totalPages = Math.max(1, Math.ceil(totalItems / pageSize));

            if (currentPage < totalPages) {
                currentPage++;
                render();
            }
            return;
        }

        // 页码点击
        const pageNumBtn = target.closest('.btn-usage-page-num');
        if (pageNumBtn) {
            const p = Number(pageNumBtn.getAttribute('data-page'));
            if (p && p !== currentPage) {
                currentPage = p;
                render();
            }
            return;
        }
    });
}
