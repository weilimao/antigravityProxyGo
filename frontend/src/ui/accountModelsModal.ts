/**
 * 账号可用模型检测 Modal 控制器
 * 负责触发各个账号真实可用模型检测、展示 Argon / Claude 5.5 缺失状态、及指引
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
let statProbeArgonCount: HTMLElement | null = null;
let statProbeOpus55Count: HTMLElement | null = null;
let accountModelsLoadingBar: HTMLDivElement | null = null;
let accountModelsTableBody: HTMLTableSectionElement | null = null;

let isProbing = false;
let lastProbeData: any = null;

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
    statProbeArgonCount = document.getElementById('statProbeArgonCount');
    statProbeOpus55Count = document.getElementById('statProbeOpus55Count');
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

function renderProbeResults(data: any): void {
    const summary = data.summary || {};
    const results = data.results || [];

    if (accountModelsSummaryCount) {
        accountModelsSummaryCount.textContent = `（共 ${summary.totalAccounts || results.length} 个账号）`;
    }
    if (statProbeTotalAccounts) {
        statProbeTotalAccounts.textContent = String(summary.totalAccounts || results.length);
    }
    if (statProbeArgonCount) {
        statProbeArgonCount.textContent = `${summary.argonSupportedCount || 0} / ${results.length}`;
    }
    if (statProbeOpus55Count) {
        statProbeOpus55Count.textContent = `${summary.opus55SupportedCount || 0} / ${results.length}`;
    }

    const tbody = accountModelsTableBody;
    if (!tbody) return;
    if (results.length === 0) {
        tbody.innerHTML = `
            <tr>
                <td colspan="3" class="p-8 text-center text-outline italic">
                    暂无可检测的启用账号
                </td>
            </tr>
        `;
        return;
    }

    tbody.innerHTML = '';
    results.forEach((acc: any) => {
        const tr = document.createElement('tr');
        tr.className = 'hover:bg-slate-50 dark:hover:bg-white/5 transition-colors border-b border-outline-variant/10 text-[12px]';

        // 1. 账号邮箱与层级
        const tierName = acc.tier || 'Free';
        const isPaid = /pro|ultra|enterprise/i.test(tierName);
        const tierBadgeClass = isPaid
            ? 'bg-purple-500/10 text-purple-600 dark:text-purple-400 border-purple-500/20'
            : 'bg-slate-500/10 text-slate-600 dark:text-slate-400 border-slate-500/20';

        let argonBucketTag = '';
        if (acc.hasArgonBucket === true) {
            argonBucketTag = '<span class="inline-block px-1.5 py-0.2 rounded text-[10px] font-medium bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 border border-emerald-500/20 mt-1">配额桶含 Argon</span>';
        } else if (acc.hasArgonBucket === false) {
            argonBucketTag = '<span class="inline-block px-1.5 py-0.2 rounded text-[10px] font-medium bg-amber-500/10 text-amber-600 dark:text-amber-400 border border-amber-500/20 mt-1">配额桶无 Argon</span>';
        }

        const tdAcc = document.createElement('td');
        tdAcc.className = 'p-3 font-medium';
        tdAcc.innerHTML = `
            <div class="flex flex-col items-start gap-1">
                <div class="flex items-center gap-1.5 font-bold text-on-surface dark:text-white break-all">
                    <span>${escapeHtml(acc.email)}</span>
                    <span class="px-1.5 py-0.5 rounded text-[10px] border ${tierBadgeClass}">${escapeHtml(tierName)}</span>
                </div>
                ${argonBucketTag}
            </div>
        `;

        // 2. 不可用模型清单（请求时自动跳过）
        const tdMissing = document.createElement('td');
        tdMissing.className = 'p-3';

        if (acc.missingModels && acc.missingModels.length > 0) {
            const tags = acc.missingModels.map((m: string) => {
                let colorClass = 'bg-red-500/10 text-red-600 dark:text-red-400 border-red-500/20';
                if (m.includes('argon')) {
                    colorClass = 'bg-rose-500/10 text-rose-600 dark:text-rose-400 border-rose-500/30';
                } else if (m.includes('opus-5-5')) {
                    colorClass = 'bg-amber-500/10 text-amber-600 dark:text-amber-400 border-amber-500/30';
                }
                return `<span class="inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[10.5px] font-mono border ${colorClass} mr-1 mb-1 font-medium" title="该账号未开通此模型，网关请求此模型时将自动跳过该账号">🚫 ${escapeHtml(m)}</span>`;
            }).join('');
            tdMissing.innerHTML = `<div class="flex flex-wrap items-center">${tags}</div>`;
        } else if (acc.error) {
            tdMissing.innerHTML = `<span class="text-red-500 text-[11px]">检测失败: ${escapeHtml(acc.error)}</span>`;
        } else {
            tdMissing.innerHTML = `
                <span class="inline-flex items-center gap-1 px-2 py-0.5 rounded text-[11px] font-medium bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 border border-emerald-500/20">
                    <span class="material-symbols-outlined text-[13px]">check_circle</span>
                    <span>全部核心模型可用</span>
                </span>
            `;
        }

        // 3. 可用模型详情
        const tdCount = document.createElement('td');
        tdCount.className = 'p-3 text-center';
        const supCount = acc.supportedCount || (acc.supportedModels ? acc.supportedModels.length : 0);
        tdCount.innerHTML = `
            <div class="flex flex-col items-center justify-center gap-1">
                <span class="text-[11.5px] font-mono font-bold text-primary dark:text-primary-fixed-dim bg-primary/5 px-2 py-0.5 rounded-md">
                    支持 ${supCount} 个模型
                </span>
            </div>
        `;

        tr.appendChild(tdAcc);
        tr.appendChild(tdMissing);
        tr.appendChild(tdCount);
        tbody.appendChild(tr);
    });
}

function renderProbeError(errMsg: string): void {
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
