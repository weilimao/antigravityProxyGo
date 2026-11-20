/**
 * 账号可用模型检测 Modal 渲染模块
 * 负责绘制模型家族覆盖分布 chips、单账号可用/不可用模型清单、未检测状态及每行「单独检测」按钮。
 */

export interface ProbeRenderRefs {
    accountModelsSummaryCount: HTMLSpanElement | null;
    statProbeTotalAccounts: HTMLElement | null;
    statProbeAllModelsCount: HTMLElement | null;
    statProbeDiffModelsCount: HTMLElement | null;
    accountModelsCoverageSection: HTMLDivElement | null;
    accountModelsCoverageChips: HTMLDivElement | null;
    accountModelsTableBody: HTMLTableSectionElement | null;
}

export interface ProbeRenderState {
    activeFamilyFilter: string | null;
    searchKeyword: string;
    expandedSupportedAccounts: Set<string>;
    expandedMissingAccounts: Set<string>;
    probingAccountIds: Set<string>;
    isGlobalProbing: boolean;
    singleProbeErrors: Record<string, string>;
}

export function renderCoverageChips(
    summary: any,
    refs: ProbeRenderRefs,
    activeFamilyFilter: string | null,
): void {
    const { accountModelsCoverageSection, accountModelsCoverageChips } = refs;
    if (!accountModelsCoverageSection || !accountModelsCoverageChips) return;

    const families: any[] = Array.isArray(summary.familyCoverage) ? summary.familyCoverage : [];
    if (families.length === 0) {
        accountModelsCoverageSection.classList.add('hidden');
        accountModelsCoverageChips.innerHTML = '';
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
        html += `<span class="text-[10.5px] text-outline px-1.5 py-0.5 self-center">已检全员共有 ${commonFamilies.length} 个基础系列</span>`;
    }
    accountModelsCoverageChips.innerHTML = html;
}

export function renderProbeResults(
    data: any,
    refs: ProbeRenderRefs,
    renderState: ProbeRenderState,
): void {
    const summary = data.summary || {};
    const allResults: any[] = data.results || [];
    const allPoolModels: string[] = summary.allPoolModels || [];
    const diffModelSet = new Set<string>((summary.differentiatedModels || []).map((m: string) => m.toLowerCase()));

    if (refs.accountModelsSummaryCount) {
        const probed = summary.probedAccounts ?? allResults.filter((r) => r.probed).length;
        const total = summary.totalAccounts ?? allResults.length;
        refs.accountModelsSummaryCount.textContent = `（共 ${total} 个账号 · 已检 ${probed}）`;
    }
    if (refs.statProbeTotalAccounts) {
        refs.statProbeTotalAccounts.textContent = String(summary.totalAccounts ?? allResults.length);
    }
    if (refs.statProbeAllModelsCount) {
        refs.statProbeAllModelsCount.textContent = String(allPoolModels.length);
    }
    if (refs.statProbeDiffModelsCount) {
        refs.statProbeDiffModelsCount.textContent = String(diffModelSet.size);
    }

    renderCoverageChips(summary, refs, renderState.activeFamilyFilter);

    const familyTargetModels = new Set<string>();
    if (renderState.activeFamilyFilter && Array.isArray(summary.familyCoverage)) {
        const matchedFam = summary.familyCoverage.find((f: any) => f.family === renderState.activeFamilyFilter);
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
        if (renderState.searchKeyword) {
            const emailMatch = (acc.email || '').toLowerCase().includes(renderState.searchKeyword);
            const modelMatch = supList.some((m) => m.toLowerCase().includes(renderState.searchKeyword));
            if (!emailMatch && !modelMatch) return false;
        }
        return true;
    });

    const tbody = refs.accountModelsTableBody;
    if (!tbody) return;
    if (filteredResults.length === 0) {
        tbody.innerHTML = `
            <tr>
                <td colspan="4" class="p-8 text-center text-outline italic">
                    ${allResults.length === 0 ? '暂无可检测的启用账号' : '当前筛选条件下无匹配账号'}
                </td>
            </tr>
        `;
        return;
    }

    const fragment = document.createDocumentFragment();
    filteredResults.forEach((acc: any) => {
        const tr = createAccountRow(acc, diffModelSet, renderState);
        fragment.appendChild(tr);
    });
    tbody.innerHTML = '';
    tbody.appendChild(fragment);
}

function createAccountRow(acc: any, diffModelSet: Set<string>, renderState: ProbeRenderState): HTMLTableRowElement {
    const tr = document.createElement('tr');
    tr.className = 'hover:bg-slate-50 dark:hover:bg-white/5 transition-colors border-b border-outline-variant/10 text-[12px] align-top';

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

    const isProbed = Boolean(acc.probed) || (Array.isArray(acc.supportedModels) && acc.supportedModels.length > 0);
    const rowErr = renderState.singleProbeErrors[acc.id] || acc.error;

    // 2. 不可用模型清单
    const tdMissing = document.createElement('td');
    tdMissing.className = 'p-3';
    const missingList: string[] = Array.isArray(acc.missingModels) ? acc.missingModels : [];

    if (!isProbed && !rowErr) {
        tdMissing.innerHTML = `
            <span class="inline-flex items-center gap-1 px-2 py-0.5 rounded text-[11px] font-medium bg-slate-500/10 text-slate-600 dark:text-slate-400 border border-slate-500/20">
                <span class="material-symbols-outlined text-[13px]">schedule</span>
                <span>尚未检测</span>
            </span>
        `;
    } else if (missingList.length > 0) {
        const isMisExpanded = renderState.expandedMissingAccounts.has(acc.id);
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
        if (rowErr) {
            tdMissing.innerHTML += `<div class="text-red-500 text-[10.5px] mt-1">本次重检报错（展示历史缓存）: ${escapeHtml(rowErr)}</div>`;
        }
    } else if (rowErr) {
        tdMissing.innerHTML = `<span class="text-red-500 text-[11px]">检测失败: ${escapeHtml(rowErr)}</span>`;
    } else {
        tdMissing.innerHTML = `
            <span class="inline-flex items-center gap-1 px-2 py-0.5 rounded text-[11px] font-medium bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 border border-emerald-500/20">
                <span class="material-symbols-outlined text-[13px]">check_circle</span>
                <span>已检全池模型均可用</span>
            </span>
        `;
    }

    // 3. 可用模型详情
    const tdSupported = document.createElement('td');
    tdSupported.className = 'p-3';
    const supList: string[] = Array.isArray(acc.supportedModels) ? [...acc.supportedModels] : [];
    const supCount = acc.supportedCount || supList.length;

    supList.sort((a, b) => {
        const aDiff = diffModelSet.has(a.toLowerCase()) ? 0 : 1;
        const bDiff = diffModelSet.has(b.toLowerCase()) ? 0 : 1;
        if (aDiff !== bDiff) return aDiff - bDiff;
        return a.localeCompare(b);
    });

    const isSupExpanded = renderState.expandedSupportedAccounts.has(acc.id) || !!renderState.searchKeyword || !!renderState.activeFamilyFilter;
    const previewLimit = 6;
    const visibleSup = isSupExpanded ? supList : supList.slice(0, previewLimit);

    const supTags = visibleSup.map((m: string) => {
        const lower = m.toLowerCase();
        const isDiff = diffModelSet.has(lower);
        const isMatchedSearch = renderState.searchKeyword && lower.includes(renderState.searchKeyword);
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
                    ${isProbed ? `支持 ${supCount} 个模型` : '未检测'}
                </span>
                <div class="flex items-center gap-2">
                    ${toggleSupBtn}
                    ${copySupBtn}
                </div>
            </div>
            ${supList.length > 0
                ? `<div class="flex flex-wrap items-center max-h-44 overflow-y-auto pr-1">${supTags}</div>`
                : `<span class="text-[11px] text-outline italic">${isProbed ? '无可用模型数据' : '点击右侧【单独检测】或下方全检获取'}</span>`}
        </div>
    `;

    // 4. 操作列：单独检测按钮
    const tdActions = document.createElement('td');
    tdActions.className = 'p-3 text-center';
    const isRowBusy = renderState.probingAccountIds.has(acc.id);
    const disabled = isRowBusy || renderState.isGlobalProbing;
    tdActions.innerHTML = `
        <button
            type="button"
            data-probe-single="${escapeHtml(acc.id)}"
            ${disabled ? 'disabled' : ''}
            class="inline-flex items-center justify-center gap-1 px-2.5 py-1 rounded-lg text-[11px] font-medium border transition-colors whitespace-nowrap ${
                disabled
                    ? 'bg-slate-100 dark:bg-slate-800 text-outline border-outline-variant/20 cursor-not-allowed opacity-70'
                    : 'bg-primary/10 hover:bg-primary/20 text-primary dark:text-primary-fixed-dim border-primary/30 cursor-pointer'
            }"
            title="仅单独检测此账号的可用模型权限"
        >
            <span class="material-symbols-outlined text-[13px] ${isRowBusy ? 'animate-spin' : ''}">${isRowBusy ? 'sync' : 'troubleshoot'}</span>
            <span>${isRowBusy ? '检测中...' : '单独检测'}</span>
        </button>
    `;

    tr.appendChild(tdAcc);
    tr.appendChild(tdMissing);
    tr.appendChild(tdSupported);
    tr.appendChild(tdActions);
    return tr;
}

export function renderProbeError(errMsg: string, refs: ProbeRenderRefs): void {
    if (refs.accountModelsCoverageSection) {
        refs.accountModelsCoverageSection.classList.add('hidden');
    }
    if (!refs.accountModelsTableBody) return;
    refs.accountModelsTableBody.innerHTML = `
        <tr>
            <td colspan="4" class="p-8 text-center text-red-500">
                ❌ ${escapeHtml(errMsg)}
            </td>
        </tr>
    `;
}

export function escapeHtml(str: string): string {
    return String(str || '')
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;');
}
