import state from './dashboardState';
import i18n from '../shared/i18n';
import * as chartRenderer from './chartRenderer';
import { calculateVirtualWindow } from './useVirtualList';
export { calculateVirtualWindow };

export const ACCOUNT_ITEM_HEIGHT = 44;
export const ACCOUNT_BUFFER_ROWS = 5;
export const DEFAULT_VIEWPORT_HEIGHT = 440;

let accountsTableBody: HTMLElement | null = null;
let totalAccountsCountEl: HTMLElement | null = null;
let totalModelCountEl: HTMLElement | null = null;
let btnScrollTopAccounts: HTMLElement | null = null;
let accountsScrollContainer: HTMLElement | null = null;
let btnExpandAll: HTMLElement | null = null;
let btnCollapseAll: HTMLElement | null = null;

// 记录用户展开的账号 key 集合 (默认全部折叠，空集合)
export const expandedAccounts = new Set<string>();

export interface AccountStatEntry {
    accountId: string;
    email: string;
    provider?: string;
    requestCount: number;
    inputTokens: number;
    outputTokens: number;
    cachedTokens: number;
    totalCost: number;
    models: Record<string, {
        model?: string;
        requestCount?: number;
        reqs?: number;
        inputTokens?: number;
        inTokens?: number;
        outputTokens?: number;
        outTokens?: number;
        cachedTokens?: number;
        totalCost?: number;
        cost?: number;
    }>;
}

export interface ModelDetailItem {
    name: string;
    reqs: number;
    inTokens: number;
    outTokens: number;
    cachedTokens: number;
    totalTokens: number;
    cost: number;
    hitRate: number;
    avgCost: number;
}

export type FlatRowItem =
    | {
        type: 'account';
        displayName: string;
        provider?: string;
        requestCount: number;
        inputTokens: number;
        outputTokens: number;
        cachedTokens: number;
        totalTokens: number;
        totalCost: number;
        avgCost: number;
        hitRate: number;
        modelCount: number;
        isExpanded: boolean;
    }
    | {
        type: 'model';
        accountDisplayName: string;
        model: ModelDetailItem;
        isLast: boolean;
    };

let flatRows: FlatRowItem[] = [];
let lastAccountsData: any = null;
let lastDataSignature = '';
let lastStartIndex = -1;
let lastEndIndex = -1;
let scrollTicking = false;
let scrollListenerAttached = false;
let delegationAttached = false;

export function normalizeAndSortAccountEntries(accountsObj: Record<string, any> | null | undefined): AccountStatEntry[] {
    if (!accountsObj || typeof accountsObj !== 'object') return [];
    const entries: AccountStatEntry[] = [];

    for (const [key, raw] of Object.entries(accountsObj)) {
        if (!raw) continue;
        const reqs = Number(raw.requestCount || raw.reqs || 0);
        const inT = Number(raw.inputTokens || raw.inTokens || 0);
        const outT = Number(raw.outputTokens || raw.outTokens || 0);
        const cacheT = Number(raw.cachedTokens || 0);
        const cost = Number(raw.totalCost || raw.cost || 0);
        const email = String(raw.email || raw.accountId || key || '直连').trim();
        const accountId = String(raw.accountId || key || email).trim();
        const provider = raw.provider ? String(raw.provider).trim() : undefined;
        const models = (raw.models && typeof raw.models === 'object') ? raw.models : {};

        if (reqs === 0 && inT === 0 && outT === 0) continue;

        entries.push({
            accountId,
            email,
            provider,
            requestCount: reqs,
            inputTokens: inT,
            outputTokens: outT,
            cachedTokens: cacheT,
            totalCost: cost,
            models,
        });
    }

    return entries.sort((a, b) => {
        const totalA = a.inputTokens + a.outputTokens;
        const totalB = b.inputTokens + b.outputTokens;
        if (totalB !== totalA) return totalB - totalA;
        return b.requestCount - a.requestCount;
    });
}

function resolveDOMElements() {
    if (!accountsTableBody) accountsTableBody = document.querySelector('#accountsTable tbody');
    if (!accountsScrollContainer) accountsScrollContainer = document.getElementById('accountsTableScrollContainer');
    if (!totalAccountsCountEl) totalAccountsCountEl = document.getElementById('accountStatsTotalCount');
    if (!totalModelCountEl) totalModelCountEl = document.getElementById('accountStatsModelCount');
    if (!btnScrollTopAccounts) btnScrollTopAccounts = document.getElementById('btnScrollToTopAccounts');
    if (!btnExpandAll) btnExpandAll = document.getElementById('btnExpandAllAccounts');
    if (!btnCollapseAll) btnCollapseAll = document.getElementById('btnCollapseAllAccounts');
}

function escapeHtml(str: string): string {
    return str.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

export function extractSortedModels(modelsDict: Record<string, any>): ModelDetailItem[] {
    const list: ModelDetailItem[] = [];
    for (const [mName, mData] of Object.entries(modelsDict || {})) {
        if (!mData) continue;
        const mReqs = Number(mData.requestCount || mData.reqs || 0);
        const mIn = Number(mData.inputTokens || mData.inTokens || 0);
        const mOut = Number(mData.outputTokens || mData.outTokens || 0);
        const mCache = Number(mData.cachedTokens || 0);
        const mCost = Number(mData.totalCost || mData.cost || 0);
        const totalT = mIn + mOut;

        list.push({
            name: mName,
            reqs: mReqs,
            inTokens: mIn,
            outTokens: mOut,
            cachedTokens: mCache,
            totalTokens: totalT,
            cost: mCost,
            hitRate: mIn > 0 ? ((mCache / mIn) * 100) : 0,
            avgCost: mReqs > 0 ? (mCost / mReqs) : 0,
        });
    }

    return list.sort((a, b) => {
        if (b.totalTokens !== a.totalTokens) return b.totalTokens - a.totalTokens;
        return b.reqs - a.reqs;
    });
}

export function flattenAccountEntries(
    accountsData: any,
    expandedSet: Set<string>
): { flatRows: FlatRowItem[]; totalAccounts: number; totalModels: number } {
    const entries = normalizeAndSortAccountEntries(accountsData);
    const rows: FlatRowItem[] = [];
    let totalModels = 0;

    for (const acc of entries) {
        const inTokens = acc.inputTokens || 0;
        const outTokens = acc.outputTokens || 0;
        const cachedTokens = acc.cachedTokens || 0;
        const reqs = acc.requestCount || 0;
        const cost = acc.totalCost || 0;
        const hitRate = inTokens > 0 ? (cachedTokens / inTokens * 100) : 0;
        const avgCost = reqs > 0 ? (cost / reqs) : 0;
        const totalTokens = inTokens + outTokens;
        const displayName = acc.email || acc.accountId || '直连';

        const modelList = extractSortedModels(acc.models);
        totalModels += modelList.length;
        const isExpanded = expandedSet.has(displayName);

        rows.push({
            type: 'account',
            displayName,
            provider: acc.provider,
            requestCount: reqs,
            inputTokens: inTokens,
            outputTokens: outTokens,
            cachedTokens: cachedTokens,
            totalTokens,
            totalCost: cost,
            avgCost,
            hitRate,
            modelCount: modelList.length,
            isExpanded,
        });

        if (isExpanded) {
            modelList.forEach((m, idx) => {
                rows.push({
                    type: 'model',
                    accountDisplayName: displayName,
                    model: m,
                    isLast: idx === modelList.length - 1,
                });
            });
        }
    }

    return { flatRows: rows, totalAccounts: entries.length, totalModels };
}

function computeDataSignature(accountsObj: any): string {
    if (!accountsObj || typeof accountsObj !== 'object') return 'empty';
    let count = 0;
    let totalReqs = 0;
    let totalIn = 0;
    let totalOut = 0;
    for (const raw of Object.values(accountsObj) as any[]) {
        if (!raw) continue;
        count++;
        totalReqs += Number(raw.requestCount || raw.reqs || 0);
        totalIn += Number(raw.inputTokens || raw.inTokens || 0);
        totalOut += Number(raw.outputTokens || raw.outTokens || 0);
    }
    return `${count}:${totalReqs}:${totalIn}:${totalOut}`;
}

function renderAccountRowHTML(row: FlatRowItem & { type: 'account' }, allModelsText: string): string {
    const providerBadge = row.provider && row.provider !== 'direct'
        ? `<span class="ml-1 px-1 py-0.2 text-[9px] rounded uppercase font-bold bg-slate-200 dark:bg-white/10 text-outline">${escapeHtml(row.provider)}</span>`
        : '';

    return `
        <td class="p-3 text-on-surface dark:text-white truncate max-w-[240px]" title="${escapeHtml(row.displayName)}">
            <div class="flex items-center gap-1.5 truncate">
                <span class="material-symbols-outlined text-[16px] text-outline group-hover:text-primary transition-transform duration-150 ${row.isExpanded ? 'rotate-90' : ''}">chevron_right</span>
                <span class="material-symbols-outlined text-[15px] text-outline/80">account_circle</span>
                <span class="truncate font-bold">${escapeHtml(row.displayName)}</span>
                ${providerBadge}
                <span class="text-[10px] text-outline px-1.5 py-0.2 rounded bg-slate-200/70 dark:bg-white/10 font-normal shrink-0">${row.modelCount} 个模型</span>
            </div>
        </td>
        <td class="p-3 text-outline text-[12px] italic select-none">${escapeHtml(allModelsText)}</td>
        <td class="p-3 text-right font-data-mono font-bold">${row.requestCount.toLocaleString()}</td>
        <td class="p-3 text-right font-data-mono font-bold text-on-surface dark:text-white" title="${row.totalTokens.toLocaleString()}">${chartRenderer.formatTokenCount(row.totalTokens)}</td>
        <td class="p-3 text-right font-data-mono font-semibold text-outline dark:text-outline-variant" title="${row.inputTokens.toLocaleString()}">${chartRenderer.formatTokenCount(row.inputTokens)}</td>
        <td class="p-3 text-right font-data-mono font-semibold text-on-surface dark:text-white" title="${row.outputTokens.toLocaleString()}">${chartRenderer.formatTokenCount(row.outputTokens)}</td>
        <td class="p-3 text-right font-data-mono" title="缓存 Tokens: ${row.cachedTokens.toLocaleString()} (${row.hitRate.toFixed(1)}%)">
            <div class="text-[12px] font-bold">${chartRenderer.formatTokenCount(row.cachedTokens)}</div>
            <div class="text-[10px] text-outline/70">${row.hitRate.toFixed(1)}%</div>
        </td>
        <td class="p-3 text-right font-data-mono text-primary dark:text-primary-fixed-dim font-bold">$${row.totalCost.toFixed(4)}</td>
        <td class="p-3 text-right font-data-mono text-outline dark:text-outline-variant">$${row.avgCost.toFixed(5)}</td>
    `;
}

function renderModelRowHTML(row: FlatRowItem & { type: 'model' }): string {
    const m = row.model;
    return `
        <td class="p-2.5 pl-8 text-outline/40 font-mono text-[11px] select-none">${row.isLast ? '└─' : '├─'}</td>
        <td class="p-2.5">
            <span class="inline-flex items-center px-2 py-0.5 rounded text-[11px] font-mono font-medium bg-primary/10 text-primary dark:text-primary-fixed-dim border border-primary/20 max-w-[260px] truncate" title="${escapeHtml(m.name)}">
                ${escapeHtml(m.name)}
            </span>
        </td>
        <td class="p-2.5 text-right font-data-mono font-medium">${m.reqs.toLocaleString()}</td>
        <td class="p-2.5 text-right font-data-mono font-semibold text-on-surface dark:text-white" title="${m.totalTokens.toLocaleString()}">${chartRenderer.formatTokenCount(m.totalTokens)}</td>
        <td class="p-2.5 text-right font-data-mono text-outline dark:text-outline-variant" title="${m.inTokens.toLocaleString()}">${chartRenderer.formatTokenCount(m.inTokens)}</td>
        <td class="p-2.5 text-right font-data-mono text-on-surface dark:text-white" title="${m.outTokens.toLocaleString()}">${chartRenderer.formatTokenCount(m.outTokens)}</td>
        <td class="p-2.5 text-right font-data-mono" title="缓存 Tokens: ${m.cachedTokens.toLocaleString()} (${m.hitRate.toFixed(1)}%)">
            <div class="text-[12px] font-medium">${chartRenderer.formatTokenCount(m.cachedTokens)}</div>
            <div class="text-[10px] text-outline/70">${m.hitRate.toFixed(1)}%</div>
        </td>
        <td class="p-2.5 text-right font-data-mono text-primary dark:text-primary-fixed-dim font-bold">$${m.cost.toFixed(4)}</td>
        <td class="p-2.5 text-right font-data-mono text-outline dark:text-outline-variant">$${m.avgCost.toFixed(5)}</td>
    `;
}

function buildSpacerTr(height: number): HTMLTableRowElement {
    const tr = document.createElement('tr');
    tr.className = 'vl-spacer';
    tr.style.height = `${height}px`;
    tr.style.padding = '0';
    tr.style.border = 'none';
    const td = document.createElement('td');
    td.colSpan = 9;
    td.style.padding = '0';
    td.style.border = 'none';
    td.style.height = `${height}px`;
    tr.appendChild(td);
    return tr;
}

export function updateVirtualAccountsTable(force: boolean = false): void {
    resolveDOMElements();
    if (!accountsTableBody) return;

    const totalCount = flatRows.length;
    if (btnScrollTopAccounts) {
        if (totalCount > 12) {
            btnScrollTopAccounts.classList.remove('hidden');
            btnScrollTopAccounts.classList.add('flex');
        } else {
            btnScrollTopAccounts.classList.add('hidden');
            btnScrollTopAccounts.classList.remove('flex');
        }
    }

    if (totalCount === 0) {
        const dict = i18n[state.currentLanguage] || {};
        accountsTableBody.innerHTML = `<tr><td colspan="9" class="p-8 text-center text-outline dark:text-outline-variant italic">${dict.noData || '暂无数据'}</td></tr>`;
        lastStartIndex = -1;
        lastEndIndex = -1;
        return;
    }

    const scrollTop = accountsScrollContainer ? accountsScrollContainer.scrollTop : 0;
    const viewportHeight = (accountsScrollContainer && accountsScrollContainer.clientHeight > 0)
        ? accountsScrollContainer.clientHeight
        : DEFAULT_VIEWPORT_HEIGHT;

    const win = calculateVirtualWindow(totalCount, scrollTop, viewportHeight, ACCOUNT_ITEM_HEIGHT, ACCOUNT_BUFFER_ROWS);
    if (!force && win.startIndex === lastStartIndex && win.endIndex === lastEndIndex) return;

    lastStartIndex = win.startIndex;
    lastEndIndex = win.endIndex;

    const fragment = document.createDocumentFragment();
    if (win.topSpacerHeight > 0) fragment.appendChild(buildSpacerTr(win.topSpacerHeight));

    const visibleRows = flatRows.slice(win.startIndex, win.endIndex);
    const dict = i18n[state.currentLanguage] || {};
    const allModelsText = dict.allModelsSummary || '全部模型汇总';

    for (const row of visibleRows) {
        const tr = document.createElement('tr');
        if (row.type === 'account') {
            tr.className = 'bg-slate-100/70 dark:bg-white/[0.04] border-b border-outline-variant/30 font-semibold cursor-pointer hover:bg-slate-200/50 dark:hover:bg-white/10 select-none group transition-colors h-[44px]';
            tr.setAttribute('data-acc-name', row.displayName);
            tr.innerHTML = renderAccountRowHTML(row, allModelsText);
        } else {
            tr.className = 'hover:bg-slate-50 dark:hover:bg-white/5 transition-colors border-b border-outline-variant/15 text-[12px] h-[44px]';
            tr.innerHTML = renderModelRowHTML(row);
        }
        fragment.appendChild(tr);
    }

    if (win.bottomSpacerHeight > 0) fragment.appendChild(buildSpacerTr(win.bottomSpacerHeight));

    accountsTableBody.innerHTML = '';
    accountsTableBody.appendChild(fragment);
}

function refreshFlattenedRowsAndRender(force: boolean = true): void {
    const result = flattenAccountEntries(lastAccountsData, expandedAccounts);
    flatRows = result.flatRows;

    if (totalAccountsCountEl) totalAccountsCountEl.textContent = result.totalAccounts.toString();
    if (totalModelCountEl) totalModelCountEl.textContent = result.totalModels.toString();

    updateVirtualAccountsTable(force);
}

export function renderAccountsTable(accountsData: any): void {
    resolveDOMElements();
    lastAccountsData = accountsData;

    const signature = computeDataSignature(accountsData);
    if (signature === lastDataSignature && flatRows.length > 0) return;
    lastDataSignature = signature;

    refreshFlattenedRowsAndRender(true);
}

export function initAccountStatsEvents(): void {
    resolveDOMElements();

    // 1. 回到顶部
    if (btnScrollTopAccounts && !btnScrollTopAccounts.hasAttribute('data-bound')) {
        btnScrollTopAccounts.setAttribute('data-bound', 'true');
        btnScrollTopAccounts.addEventListener('click', () => {
            if (accountsScrollContainer) accountsScrollContainer.scrollTo({ top: 0, behavior: 'smooth' });
        });
    }

    // 2. 虚拟滚动容器监听 (带 requestAnimationFrame 防抖)
    if (accountsScrollContainer && !scrollListenerAttached) {
        scrollListenerAttached = true;
        accountsScrollContainer.addEventListener('scroll', () => {
            if (!scrollTicking) {
                window.requestAnimationFrame(() => {
                    updateVirtualAccountsTable(false);
                    scrollTicking = false;
                });
                scrollTicking = true;
            }
        }, { passive: true });
    }

    // 3. 事件委托: 单一监听器处理账号行点击展开/折叠 (零闭包泄漏)
    if (accountsTableBody && !delegationAttached) {
        delegationAttached = true;
        accountsTableBody.addEventListener('click', (e) => {
            const target = e.target as HTMLElement;
            const tr = target.closest('tr[data-acc-name]');
            if (!tr) return;
            const accName = tr.getAttribute('data-acc-name');
            if (!accName) return;

            if (expandedAccounts.has(accName)) {
                expandedAccounts.delete(accName);
            } else {
                expandedAccounts.add(accName);
            }
            refreshFlattenedRowsAndRender(true);
        });
    }

    // 4. 全部展开
    if (btnExpandAll && !btnExpandAll.hasAttribute('data-bound')) {
        btnExpandAll.setAttribute('data-bound', 'true');
        btnExpandAll.addEventListener('click', () => {
            const entries = normalizeAndSortAccountEntries(lastAccountsData || state.filteredAccountStats);
            entries.forEach(acc => {
                const name = acc.email || acc.accountId || '直连';
                expandedAccounts.add(name);
            });
            refreshFlattenedRowsAndRender(true);
        });
    }

    // 5. 全部折叠
    if (btnCollapseAll && !btnCollapseAll.hasAttribute('data-bound')) {
        btnCollapseAll.setAttribute('data-bound', 'true');
        btnCollapseAll.addEventListener('click', () => {
            expandedAccounts.clear();
            refreshFlattenedRowsAndRender(true);
        });
    }
}

export function clearAccountsVirtualTable(): void {
    resolveDOMElements();
    if (accountsTableBody) accountsTableBody.innerHTML = '';
    lastStartIndex = -1;
    lastEndIndex = -1;
    lastDataSignature = '';
}
