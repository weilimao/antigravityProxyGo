/**
 * 账号可用模型检测 Modal 控制器
 * 负责触发各个账号真实可用模型检测、动态展示号池差异模型分布、每个账号完整可用/不可用模型清单
 */
import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';

let accountModelsModal: HTMLDivElement | null = null;
let accountModelsModalContainer: HTMLDivElement | null = null;
let btnAccountModelsModalClose: HTMLButtonElement | null = null;
let btnAccountModelsCloseSecondary: HTMLButtonElement | null = null;
let btnAccountModelsReProbe: HTMLButtonElement | null = null;
let iconAccountModelsReProbe: HTMLElement | null = null;
let btnProbeAccountModels: HTMLButtonElement | null = null;
let iconProbeModels: HTMLElement | null = null;

let accountModelsSummaryCount: HTMLSpanElement | null = null;
let statProbeTotalAccounts: HTMLElement | null = null;
let statProbeAllModelsCount: HTMLElement | null = null;
let statProbeDiffModelsCount: HTMLElement | null = null;
let accountModelsCoverageSection: HTMLDivElement | null = null;
let accountModelsCoverageChips: HTMLDivElement | null = null;
let accountModelsSearchInput: HTMLInputElement | null = null;
let accountModelsLoadingBar: HTMLDivElement | null = null;
let accountModelsTableBody: HTMLTableSectionElement | null = null;

let isProbing = false;
let lastProbeData: any = null;
let activeFamilyFilter: string | null = null;
let searchKeyword = '';
const expandedSupportedAccounts = new Set<string>();
const expandedMissingAccounts = new Set<string>();

export function initAccountModelsModalEvents(): void {
    btnProbeAccountModels = document.getElementById('btnProbeAccountModels') as HTMLButtonElement | null;
    iconProbeModels = document.getElementById('iconProbeModels');

    accountModelsModal = document.getElementById('accountModelsModal') as HTMLDivElement | null;
    accountModelsModalContainer = document.getElementById('accountModelsModalContainer') as HTMLDivElement | null;
    btnAccountModelsModalClose = document.getElementById('btnAccountModelsModalClose') as HTMLButtonElement | null;
    btnAccountModelsCloseSecondary = document.getElementById('btnAccountModelsCloseSecondary') as HTMLButtonElement | null;
    btnAccountModelsReProbe = document.getElementById('btnAccountModelsReProbe') as HTMLButtonElement | null;
    iconAccountModelsReProbe = document.getElementById('iconAccountModelsReProbe');

    accountModelsSummaryCount = document.getElementById('accountModelsSummaryCount') as HTMLSpanElement | null;
    statProbeTotalAccounts = document.getElementById('statProbeTotalAccounts');
    statProbeAllModelsCount = document.getElementById('statProbeAllModelsCount');
    statProbeDiffModelsCount = document.getElementById('statProbeDiffModelsCount');
    accountModelsCoverageSection = document.getElementById('accountModelsCoverageSection') as HTMLDivElement | null;
    accountModelsCoverageChips = document.getElementById('accountModelsCoverageChips') as HTMLDivElement | null;
    accountModelsSearchInput = document.getElementById('accountModelsSearchInput') as HTMLInputElement | null;
    accountModelsLoadingBar = document.getElementById('accountModelsLoadingBar') as HTMLDivElement | null;
    accountModelsTableBody = document.getElementById('accountModelsTableBody') as HTMLTableSectionElement | null;

    if (btnProbeAccountModels) {
        btnProbeAccountModels.addEventListener('click', () => {
            openAccountModelsModal(true);
        });
    }

    if (btnAccountModelsModalClose) {
        btnAccountModelsModalClose.addEventListener('click', hideAccountModelsModal);
    }
    if (btnAccountModelsCloseSecondary) {
        btnAccountModelsCloseSecondary.addEventListener('click', hideAccountModelsModal);
    }
    if (btnAccountModelsReProbe) {
        btnAccountModelsReProbe.addEventListener('click', () => {
            if (!isProbing) {
                runAccountModelsProbe();
            }
        });
    }

    if (accountModelsSearchInput) {
        accountModelsSearchInput.addEventListener('input', () => {
            searchKeyword = (accountModelsSearchInput?.value || '').trim().toLowerCase();
            if (lastProbeData) {
                renderProbeResults(lastProbeData);
            }
        });
    }

    if (accountModelsCoverageChips) {
        accountModelsCoverageChips.addEventListener('click', (e) => {
            const btn = (e.target as HTMLElement)?.closest('[data-family-filter]') as HTMLElement | null;
            if (!btn) return;
            const fam = btn.getAttribute('data-family-filter') || '';
            activeFamilyFilter = (fam === '' || activeFamilyFilter === fam) ? null : fam;
            if (lastProbeData) {
                renderProbeResults(lastProbeData);
            }
        });
    }

    if (accountModelsTableBody) {
        accountModelsTableBody.addEventListener('click', async (e) => {
            const target = e.target as HTMLElement;
            const toggleSupBtn = target.closest('[data-toggle-supported]') as HTMLElement | null;
            if (toggleSupBtn) {
                const accId = toggleSupBtn.getAttribute('data-toggle-supported') || '';
                if (expandedSupportedAccounts.has(accId)) {
                    expandedSupportedAccounts.delete(accId);
                } else {
                    expandedSupportedAccounts.add(accId);
                }
                if (lastProbeData) renderProbeResults(lastProbeData);
                return;
            }

            const toggleMisBtn = target.closest('[data-toggle-missing]') as HTMLElement | null;
            if (toggleMisBtn) {
                const accId = toggleMisBtn.getAttribute('data-toggle-missing') || '';
                if (expandedMissingAccounts.has(accId)) {
                    expandedMissingAccounts.delete(accId);
                } else {
                    expandedMissingAccounts.add(accId);
                }
                if (lastProbeData) renderProbeResults(lastProbeData);
                return;
            }

            const copyBtn = target.closest('[data-copy-models]') as HTMLButtonElement | null;
            if (copyBtn && lastProbeData) {
                const accId = copyBtn.getAttribute('data-copy-models') || '';
                const acc = (lastProbeData.results || []).find((r: any) => r.id === accId);
                if (acc && Array.isArray(acc.supportedModels)) {
                    try {
                        await navigator.clipboard.writeText(acc.supportedModels.join('\n'));
                        const orig = copyBtn.innerHTML;
                        copyBtn.textContent = '已复制';
                        setTimeout(() => { copyBtn.innerHTML = orig; }, 1200);
                    } catch (_) {}
                }
            }
        });
    }
}

export function openAccountModelsModal(autoProbe: boolean = true): void {
    if (!accountModelsModal || !accountModelsModalContainer) {
        initAccountModelsModalEvents();
    }
    if (!accountModelsModal || !accountModelsModalContainer) return;

    accountModelsModal.classList.remove('opacity-0', 'pointer-events-none', 'hidden');
    accountModelsModalContainer.classList.remove('scale-95');
    accountModelsModalContainer.classList.add('scale-100');

    if (autoProbe && (!lastProbeData || isProbing)) {
        runAccountModelsProbe();
    } else if (lastProbeData) {
        renderProbeResults(lastProbeData);
    }
}

export function hideAccountModelsModal(): void {
    if (!accountModelsModal || !accountModelsModalContainer) return;
    accountModelsModalContainer.classList.remove('scale-100');
    accountModelsModalContainer.classList.add('scale-95');
    accountModelsModal.classList.add('opacity-0', 'pointer-events-none');
}

export async function runAccountModelsProbe(): Promise<void> {
    if (isProbing) return;
    isProbing = true;

    if (iconProbeModels) iconProbeModels.classList.add('animate-spin');
    if (iconAccountModelsReProbe) iconAccountModelsReProbe.classList.add('animate-spin');
    if (accountModelsLoadingBar) accountModelsLoadingBar.classList.remove('hidden');

    const channel = state.currentViewTab || 'antigravity';

    try {
        const rawRes = await ipcRenderer.invoke('accounts:probe-models', {
            channel,
            accountIds: [],
        });

        let data = rawRes;
        if (typeof rawRes === 'string') {
            try {
                data = JSON.parse(rawRes);
            } catch (_) {}
        }

        if (data && data.success) {
            lastProbeData = data;
            activeFamilyFilter = null;
            renderProbeResults(data);
        } else {
            renderProbeError(data?.error || '检测失败，请检查网络或账号有效性');
        }
    } catch (err: any) {
        renderProbeError(err?.message || '探测接口调用失败');
    } finally {
        isProbing = false;
        if (iconProbeModels) iconProbeModels.classList.remove('animate-spin');
        if (iconAccountModelsReProbe) iconAccountModelsReProbe.classList.remove('animate-spin');
        if (accountModelsLoadingBar) accountModelsLoadingBar.classList.add('hidden');
    }
}

function renderCoverageChips(summary: any): void {
    if (!accountModelsCoverageSection || !accountModelsCoverageChips) return;
    const families: any[] = Array.isArray(summary.familyCoverage) ? summary.familyCoverage : [];
    if (families.length === 0) {
        accountModelsCoverageSection.classList.add('hidden');
        return;
    }
    accountModelsCoverageSection.classList.remove('hidden');

    const diffFamilies = families.filter((f) => f.isDifferentiated);
    const commonFamilies = families.filter((f) => !f.isDifferentiated);

    const allActive = !activeFamilyFilter;
    let html = `
        <button type="button" data-family-filter="" class="px-2 py-0.5 rounded-md text-[11px] font-medium border transition-colors cursor-pointer ${
            allActive
                ? 'bg-primary text-white border-primary'
                : 'bg-white dark:bg-slate-800/60 text-on-surface dark:text-slate-200 border-outline-variant/30 hover:border-primary/40'
        }">全部 (${summary.validProbedCount || summary.totalAccounts || 0})</button>
    `;

    diffFamilies.forEach((f) => {
        const isSelected = activeFamilyFilter === f.family;
        const cls = isSelected
            ? 'bg-purple-600 text-white border-purple-600'
            : 'bg-purple-500/10 text-purple-700 dark:text-purple-300 border-purple-500/30 hover:bg-purple-500/20';
        const title = `包含变体: ${(f.models || []).join(', ')}`;
        html += `
            <button type="button" data-family-filter="${escapeHtml(f.family)}" title="${escapeHtml(title)}" class="px-2 py-0.5 rounded-md text-[11px] font-mono font-medium border transition-colors cursor-pointer ${cls}">
                ${escapeHtml(f.family)} <span class="opacity-80">${f.supportedCount}/${f.totalProbed}</span>
            </button>
        `;
    });

    if (commonFamilies.length > 0) {
        html += `<span class="text-[10.5px] text-outline px-1.5 py-0.5 self-center">全员共有 ${commonFamilies.length} 个基础系列</span>`;
    }
    accountModelsCoverageChips.innerHTML = html;
}

function renderProbeResults(data: any): void {
    const summary = data.summary || {};
    const allResults: any[] = data.results || [];
    const allPoolModels: string[] = summary.allPoolModels || [];
    const diffModelSet = new Set<string>((summary.differentiatedModels || []).map((m: string) => m.toLowerCase()));

    if (accountModelsSummaryCount) {
        accountModelsSummaryCount.textContent = `（共 ${summary.totalAccounts || allResults.length} 个账号）`;
    }
    if (statProbeTotalAccounts) {
        statProbeTotalAccounts.textContent = String(summary.totalAccounts || allResults.length);
    }
    if (statProbeAllModelsCount) {
        statProbeAllModelsCount.textContent = String(allPoolModels.length);
    }
    if (statProbeDiffModelsCount) {
        statProbeDiffModelsCount.textContent = String(diffModelSet.size);
    }

    renderCoverageChips(summary);

    // 根据选中家族标签和搜索关键词过滤展示账号
    const familyTargetModels = new Set<string>();
    if (activeFamilyFilter && Array.isArray(summary.familyCoverage)) {
        const matchedFam = summary.familyCoverage.find((f: any) => f.family === activeFamilyFilter);
        if (matchedFam && Array.isArray(matchedFam.models)) {
            matchedFam.models.forEach((m: string) => familyTargetModels.add(m.toLowerCase()));
        }
    }

    const filteredResults = allResults.filter((acc) => {
        const supList: string[] = Array.isArray(acc.supportedModels) ? acc.supportedModels : [];
        if (familyTargetModels.size > 0) {
            const hasAny = supList.some((m) => familyTargetModels.has(m.toLowerCase()));
            if (!hasAny) return false;
        }
        if (searchKeyword) {
            const emailMatch = (acc.email || '').toLowerCase().includes(searchKeyword);
            const modelMatch = supList.some((m) => m.toLowerCase().includes(searchKeyword));
            if (!emailMatch && !modelMatch) return false;
        }
        return true;
    });

    const tbody = accountModelsTableBody;
    if (!tbody) return;
    if (filteredResults.length === 0) {
        tbody.innerHTML = `
            <tr>
                <td colspan="3" class="p-8 text-center text-outline italic">
                    ${allResults.length === 0 ? '暂无可检测的启用账号' : '当前筛选条件下无匹配账号'}
                </td>
            </tr>
        `;
        return;
    }

    tbody.innerHTML = '';
    filteredResults.forEach((acc: any) => {
        const tr = document.createElement('tr');
        tr.className = 'hover:bg-slate-50 dark:hover:bg-white/5 transition-colors border-b border-outline-variant/10 text-[12px] align-top';

        // 1. 账号邮箱与层级
        const tierName = acc.tier || 'Free';
        const isPaid = /pro|ultra|enterprise/i.test(tierName);
        const tierBadgeClass = isPaid
            ? 'bg-purple-500/10 text-purple-600 dark:text-purple-400 border-purple-500/20'
            : 'bg-slate-500/10 text-slate-600 dark:text-slate-400 border-slate-500/20';

        let argonBucketTag = '';
        if (acc.hasArgonBucket === true) {
            argonBucketTag = '<span class="inline-block px-1.5 py-0.2 rounded text-[10px] font-medium bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 border border-emerald-500/20 mt-0.5">配额桶含 Argon</span>';
        } else if (acc.hasArgonBucket === false) {
            argonBucketTag = '<span class="inline-block px-1.5 py-0.2 rounded text-[10px] font-medium bg-amber-500/10 text-amber-600 dark:text-amber-400 border border-amber-500/20 mt-0.5">配额桶无 Argon</span>';
        }

        const tdAcc = document.createElement('td');
        tdAcc.className = 'p-3 font-medium';
        tdAcc.innerHTML = `
            <div class="flex flex-col items-start gap-1">
                <div class="flex flex-wrap items-center gap-1.5 font-bold text-on-surface dark:text-white break-all">
                    <span>${escapeHtml(acc.email)}</span>
                    <span class="px-1.5 py-0.5 rounded text-[10px] border ${tierBadgeClass}">${escapeHtml(tierName)}</span>
                </div>
                ${argonBucketTag}
            </div>
        `;

        // 2. 不可用模型清单（请求时自动跳过）
        const tdMissing = document.createElement('td');
        tdMissing.className = 'p-3';
        const missingList: string[] = Array.isArray(acc.missingModels) ? acc.missingModels : [];

        if (missingList.length > 0) {
            const isMisExpanded = expandedMissingAccounts.has(acc.id);
            const maxVisible = 6;
            const visibleMissing = isMisExpanded ? missingList : missingList.slice(0, maxVisible);
            const tags = visibleMissing.map((m: string) => {
                const lower = m.toLowerCase();
                let colorClass = 'bg-red-500/10 text-red-600 dark:text-red-400 border-red-500/20';
                if (lower.includes('hydrogen')) {
                    colorClass = 'bg-indigo-500/10 text-indigo-600 dark:text-indigo-400 border-indigo-500/30';
                } else if (lower.includes('argon')) {
                    colorClass = 'bg-rose-500/10 text-rose-600 dark:text-rose-400 border-rose-500/30';
                } else if (lower.includes('opus-5-5') || lower.includes('sonnet-5-5')) {
                    colorClass = 'bg-amber-500/10 text-amber-600 dark:text-amber-400 border-amber-500/30';
                }
                return `<span class="inline-flex items-center gap-0.5 px-1.5 py-0.5 rounded text-[10.5px] font-mono border ${colorClass} mr-1 mb-1 font-medium" title="该账号未开通此模型，网关请求此模型时将自动跳过该账号">🚫 ${escapeHtml(m)}</span>`;
            }).join('');

            const toggleMissingBtn = missingList.length > maxVisible
                ? `<button type="button" data-toggle-missing="${escapeHtml(acc.id)}" class="text-[10.5px] text-primary hover:underline font-medium mt-0.5 cursor-pointer">${isMisExpanded ? '收起' : `展开全部缺失 (${missingList.length})`}</button>`
                : '';

            tdMissing.innerHTML = `<div class="flex flex-wrap items-center">${tags}</div>${toggleMissingBtn}`;
        } else if (acc.error) {
            tdMissing.innerHTML = `<span class="text-red-500 text-[11px]">检测失败: ${escapeHtml(acc.error)}</span>`;
        } else {
            tdMissing.innerHTML = `
                <span class="inline-flex items-center gap-1 px-2 py-0.5 rounded text-[11px] font-medium bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 border border-emerald-500/20">
                    <span class="material-symbols-outlined text-[13px]">check_circle</span>
                    <span>全池模型均可用</span>
                </span>
            `;
        }

        // 3. 可用模型详情（动态展示该账号拥有的全部模型，差异特有模型置顶高亮）
        const tdSupported = document.createElement('td');
        tdSupported.className = 'p-3';
        const supList: string[] = Array.isArray(acc.supportedModels) ? [...acc.supportedModels] : [];
        const supCount = acc.supportedCount || supList.length;

        // 将号池差异模型排在前面展示，方便一眼识别各号独有权限
        supList.sort((a, b) => {
            const aDiff = diffModelSet.has(a.toLowerCase()) ? 0 : 1;
            const bDiff = diffModelSet.has(b.toLowerCase()) ? 0 : 1;
            if (aDiff !== bDiff) return aDiff - bDiff;
            return a.localeCompare(b);
        });

        const isSupExpanded = expandedSupportedAccounts.has(acc.id) || !!searchKeyword || !!activeFamilyFilter;
        const previewLimit = 6;
        const visibleSup = isSupExpanded ? supList : supList.slice(0, previewLimit);

        const supTags = visibleSup.map((m: string) => {
            const lower = m.toLowerCase();
            const isDiff = diffModelSet.has(lower);
            const isMatchedSearch = searchKeyword && lower.includes(searchKeyword);
            let cls = 'bg-slate-500/10 text-slate-700 dark:text-slate-300 border-slate-500/20';
            if (isMatchedSearch) {
                cls = 'bg-emerald-500/20 text-emerald-700 dark:text-emerald-300 border-emerald-500/40 ring-1 ring-emerald-500/30';
            } else if (isDiff) {
                cls = 'bg-purple-500/10 text-purple-700 dark:text-purple-300 border-purple-500/30 font-semibold';
            }
            const badgePrefix = isDiff ? '★ ' : '';
            return `<span class="inline-flex items-center px-1.5 py-0.5 rounded text-[10.5px] font-mono border ${cls} mr-1 mb-1">${badgePrefix}${escapeHtml(m)}</span>`;
        }).join('');

        const toggleSupBtn = supList.length > previewLimit
            ? `<button type="button" data-toggle-supported="${escapeHtml(acc.id)}" class="text-[11px] font-medium text-primary hover:underline cursor-pointer">${isSupExpanded ? '收起清单' : `展开全部 ${supCount} 个`}</button>`
            : '';

        const copySupBtn = supList.length > 0
            ? `<button type="button" data-copy-models="${escapeHtml(acc.id)}" class="text-[10.5px] text-outline hover:text-primary px-1.5 py-0.5 rounded border border-outline-variant/20 cursor-pointer" title="复制该账号全部可用模型 ID">复制</button>`
            : '';

        tdSupported.innerHTML = `
            <div class="space-y-1.5">
                <div class="flex items-center justify-between gap-2">
                    <span class="text-[11px] font-mono font-bold text-primary dark:text-primary-fixed-dim bg-primary/5 px-2 py-0.5 rounded-md">
                        支持 ${supCount} 个模型
                    </span>
                    <div class="flex items-center gap-2">
                        ${toggleSupBtn}
                        ${copySupBtn}
                    </div>
                </div>
                ${supList.length > 0 ? `<div class="flex flex-wrap items-center max-h-44 overflow-y-auto pr-1">${supTags}</div>` : '<span class="text-[11px] text-outline italic">无可用模型数据</span>'}
            </div>
        `;

        tr.appendChild(tdAcc);
        tr.appendChild(tdMissing);
        tr.appendChild(tdSupported);
        tbody.appendChild(tr);
    });
}

function renderProbeError(errMsg: string): void {
    if (accountModelsCoverageSection) {
        accountModelsCoverageSection.classList.add('hidden');
    }
    if (!accountModelsTableBody) return;
    accountModelsTableBody.innerHTML = `
        <tr>
            <td colspan="3" class="p-8 text-center text-red-500">
                ❌ ${escapeHtml(errMsg)}
            </td>
        </tr>
    `;
}

function escapeHtml(str: string): string {
    return String(str || '')
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;');
}
