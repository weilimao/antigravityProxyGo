/**
 * 账号可用模型检测 Modal 控制器
 * 负责打开弹窗时复显持久化缓存、触发全池/单账号真实模型探测，以及同步“优先低阶模型账号”开关。
 */
import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';
import {
    ProbeRenderRefs,
    ProbeRenderState,
    renderProbeError,
    renderProbeResults,
} from './accountModelsRenderer';

let accountModelsModal: HTMLDivElement | null = null;
let accountModelsModalContainer: HTMLDivElement | null = null;
let btnAccountModelsModalClose: HTMLButtonElement | null = null;
let btnAccountModelsCloseSecondary: HTMLButtonElement | null = null;
let btnAccountModelsReProbe: HTMLButtonElement | null = null;
let iconAccountModelsReProbe: HTMLElement | null = null;
let btnProbeAccountModels: HTMLButtonElement | null = null;
let iconProbeModels: HTMLElement | null = null;

let preferLowTierAccountToggle: HTMLInputElement | null = null;
let preferLowTierStatusBadge: HTMLElement | null = null;

let accountModelsSearchInput: HTMLInputElement | null = null;
let accountModelsLoadingBar: HTMLDivElement | null = null;

const refs: ProbeRenderRefs = {
    accountModelsSummaryCount: null,
    statProbeTotalAccounts: null,
    statProbeAllModelsCount: null,
    statProbeDiffModelsCount: null,
    accountModelsCoverageSection: null,
    accountModelsCoverageChips: null,
    accountModelsTableBody: null,
};

let lastProbeData: any = null;
let isAccountModelsModalEventsInitialized = false;
const renderState: ProbeRenderState = {
    activeFamilyFilter: null,
    searchKeyword: '',
    expandedSupportedAccounts: new Set<string>(),
    expandedMissingAccounts: new Set<string>(),
    probingAccountIds: new Set<string>(),
    isGlobalProbing: false,
    singleProbeErrors: {},
};

function parseIpcResponse(rawRes: any): any {
    if (typeof rawRes === 'string') {
        try {
            return JSON.parse(rawRes);
        } catch (_) {
            return rawRes;
        }
    }
    return rawRes;
}

function renderCurrentData(): void {
    if (lastProbeData) {
        renderProbeResults(lastProbeData, refs, renderState);
    }
}

export function syncPreferLowTierFromBackend(data?: any): void {
    const src = data || state.lastBackendData;
    const enabled = Boolean(src?.preferLowTierAccount);
    if (preferLowTierAccountToggle) {
        preferLowTierAccountToggle.checked = enabled;
    }
    if (preferLowTierStatusBadge) {
        if (enabled) {
            preferLowTierStatusBadge.textContent = '已开启';
            preferLowTierStatusBadge.className = 'px-1.5 py-0.5 rounded text-[10px] font-medium bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 border border-emerald-500/20';
        } else {
            preferLowTierStatusBadge.textContent = '已关闭 (默认)';
            preferLowTierStatusBadge.className = 'px-1.5 py-0.5 rounded text-[10px] font-medium bg-slate-500/10 text-slate-600 dark:text-slate-400 border border-slate-500/20';
        }
    }
}

export function initAccountModelsModalEvents(): void {
    btnProbeAccountModels = document.getElementById('btnProbeAccountModels') as HTMLButtonElement | null;
    iconProbeModels = document.getElementById('iconProbeModels');

    accountModelsModal = document.getElementById('accountModelsModal') as HTMLDivElement | null;
    accountModelsModalContainer = document.getElementById('accountModelsModalContainer') as HTMLDivElement | null;
    btnAccountModelsModalClose = document.getElementById('btnAccountModelsModalClose') as HTMLButtonElement | null;
    btnAccountModelsCloseSecondary = document.getElementById('btnAccountModelsCloseSecondary') as HTMLButtonElement | null;
    btnAccountModelsReProbe = document.getElementById('btnAccountModelsReProbe') as HTMLButtonElement | null;
    iconAccountModelsReProbe = document.getElementById('iconAccountModelsReProbe');

    preferLowTierAccountToggle = document.getElementById('preferLowTierAccountToggle') as HTMLInputElement | null;
    preferLowTierStatusBadge = document.getElementById('preferLowTierStatusBadge');

    refs.accountModelsSummaryCount = document.getElementById('accountModelsSummaryCount') as HTMLSpanElement | null;
    refs.statProbeTotalAccounts = document.getElementById('statProbeTotalAccounts');
    refs.statProbeAllModelsCount = document.getElementById('statProbeAllModelsCount');
    refs.statProbeDiffModelsCount = document.getElementById('statProbeDiffModelsCount');
    refs.accountModelsCoverageSection = document.getElementById('accountModelsCoverageSection') as HTMLDivElement | null;
    refs.accountModelsCoverageChips = document.getElementById('accountModelsCoverageChips') as HTMLDivElement | null;
    refs.accountModelsTableBody = document.getElementById('accountModelsTableBody') as HTMLTableSectionElement | null;

    accountModelsSearchInput = document.getElementById('accountModelsSearchInput') as HTMLInputElement | null;
    accountModelsLoadingBar = document.getElementById('accountModelsLoadingBar') as HTMLDivElement | null;

    // 防止在路由切换或反复调用时重复绑定事件监听器
    if (isAccountModelsModalEventsInitialized) {
        syncPreferLowTierFromBackend();
        return;
    }
    isAccountModelsModalEventsInitialized = true;

    if (btnProbeAccountModels) {
        btnProbeAccountModels.addEventListener('click', () => {
            openAccountModelsModal();
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
            if (!renderState.isGlobalProbing) {
                runAccountModelsProbe();
            }
        });
    }

    if (preferLowTierAccountToggle) {
        preferLowTierAccountToggle.addEventListener('change', async (e: Event) => {
            const input = e.target as HTMLInputElement;
            const targetEnabled = !!input.checked;
            try {
                const rawRes = await ipcRenderer.invoke('accounts:set-prefer-low-tier', { enabled: targetEnabled });
                const res = parseIpcResponse(rawRes);
                if (res && res.success) {
                    state.lastBackendData = { ...(state.lastBackendData || {}), preferLowTierAccount: targetEnabled };
                    syncPreferLowTierFromBackend(state.lastBackendData);
                } else {
                    input.checked = !targetEnabled;
                }
            } catch (_) {
                input.checked = !targetEnabled;
            }
        });
    }

    if (accountModelsSearchInput) {
        accountModelsSearchInput.addEventListener('input', () => {
            renderState.searchKeyword = (accountModelsSearchInput?.value || '').trim().toLowerCase();
            renderCurrentData();
        });
    }

    if (refs.accountModelsCoverageChips) {
        refs.accountModelsCoverageChips.addEventListener('click', (e) => {
            const btn = (e.target as HTMLElement)?.closest('[data-family-filter]') as HTMLElement | null;
            if (!btn) return;
            const fam = btn.getAttribute('data-family-filter') || '';
            renderState.activeFamilyFilter = (fam === '' || renderState.activeFamilyFilter === fam) ? null : fam;
            renderCurrentData();
        });
    }

    if (refs.accountModelsTableBody) {
        refs.accountModelsTableBody.addEventListener('click', async (e) => {
            const target = e.target as HTMLElement;

            const probeSingleBtn = target.closest('[data-probe-single]') as HTMLButtonElement | null;
            if (probeSingleBtn) {
                const accId = probeSingleBtn.getAttribute('data-probe-single') || '';
                if (accId) {
                    await runSingleAccountModelsProbe(accId);
                }
                return;
            }

            const toggleSupBtn = target.closest('[data-toggle-supported]') as HTMLElement | null;
            if (toggleSupBtn) {
                const accId = toggleSupBtn.getAttribute('data-toggle-supported') || '';
                if (renderState.expandedSupportedAccounts.has(accId)) {
                    renderState.expandedSupportedAccounts.delete(accId);
                } else {
                    renderState.expandedSupportedAccounts.add(accId);
                }
                renderCurrentData();
                return;
            }

            const toggleMisBtn = target.closest('[data-toggle-missing]') as HTMLElement | null;
            if (toggleMisBtn) {
                const accId = toggleMisBtn.getAttribute('data-toggle-missing') || '';
                if (renderState.expandedMissingAccounts.has(accId)) {
                    renderState.expandedMissingAccounts.delete(accId);
                } else {
                    renderState.expandedMissingAccounts.add(accId);
                }
                renderCurrentData();
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

    syncPreferLowTierFromBackend();
}

export function openAccountModelsModal(): void {
    if (!accountModelsModal || !accountModelsModalContainer) {
        initAccountModelsModalEvents();
    }
    if (!accountModelsModal || !accountModelsModalContainer) return;

    // 1. 同步低阶偏好开关状态，并立即加载/渲染缓存数据（0 延迟呈现）
    syncPreferLowTierFromBackend();
    loadCachedAccountModels();

    // 2. 瞬间呈现弹窗（移出透明遮罩与点击穿透，noScale 模式下零几何变形掉帧）
    accountModelsModal.classList.remove('opacity-0', 'pointer-events-none', 'hidden');
    accountModelsModalContainer.classList.remove('scale-95');
    accountModelsModalContainer.classList.add('scale-100');
}

export function hideAccountModelsModal(): void {
    if (!accountModelsModal) return;

    // 3. 瞬间收起弹窗，响应干脆利落
    accountModelsModal.classList.add('opacity-0', 'pointer-events-none');
    if (accountModelsModalContainer) {
        accountModelsModalContainer.classList.remove('scale-100');
        accountModelsModalContainer.classList.add('scale-95');
    }
}

export async function loadCachedAccountModels(resetFilter: boolean = false): Promise<void> {
    const channel = state.currentViewTab || 'antigravity';
    try {
        const rawRes = await ipcRenderer.invoke('accounts:get-cached-models', { channel });
        const data = parseIpcResponse(rawRes);
        if (data && data.success) {
            lastProbeData = data;
            if (resetFilter) {
                renderState.activeFamilyFilter = null;
            }
            renderCurrentData();
        } else if (data?.error) {
            renderProbeError(data.error, refs);
        }
    } catch (err: any) {
        renderProbeError(err?.message || '读取缓存模型数据失败', refs);
    }
}

export async function runAccountModelsProbe(): Promise<void> {
    if (renderState.isGlobalProbing) return;
    renderState.isGlobalProbing = true;
    renderState.singleProbeErrors = {};

    if (iconProbeModels) iconProbeModels.classList.add('animate-spin');
    if (iconAccountModelsReProbe) iconAccountModelsReProbe.classList.add('animate-spin');
    if (accountModelsLoadingBar) accountModelsLoadingBar.classList.remove('hidden');
    renderCurrentData();

    const channel = state.currentViewTab || 'antigravity';
    try {
        const rawRes = await ipcRenderer.invoke('accounts:probe-models', {
            channel,
            accountIds: [],
        });
        const data = parseIpcResponse(rawRes);
        if (data && data.success) {
            lastProbeData = data;
            renderState.activeFamilyFilter = null;
            renderCurrentData();
        } else {
            renderProbeError(data?.error || '检测失败，请检查网络或账号有效性', refs);
        }
    } catch (err: any) {
        renderProbeError(err?.message || '探测接口调用失败', refs);
    } finally {
        renderState.isGlobalProbing = false;
        if (iconProbeModels) iconProbeModels.classList.remove('animate-spin');
        if (iconAccountModelsReProbe) iconAccountModelsReProbe.classList.remove('animate-spin');
        if (accountModelsLoadingBar) accountModelsLoadingBar.classList.add('hidden');
        renderCurrentData();
    }
}

export async function runSingleAccountModelsProbe(accountId: string): Promise<void> {
    if (!accountId || renderState.isGlobalProbing || renderState.probingAccountIds.has(accountId)) {
        return;
    }
    renderState.probingAccountIds.add(accountId);
    delete renderState.singleProbeErrors[accountId];
    renderCurrentData();

    const channel = state.currentViewTab || 'antigravity';
    try {
        const rawRes = await ipcRenderer.invoke('accounts:probe-models', {
            channel,
            accountIds: [accountId],
        });
        const data = parseIpcResponse(rawRes);
        const firstResult = Array.isArray(data?.results) ? data.results[0] : null;
        if (!data?.success) {
            renderState.singleProbeErrors[accountId] = data?.error || '单独检测失败';
        } else if (firstResult?.error) {
            renderState.singleProbeErrors[accountId] = firstResult.error;
        }
        await loadCachedAccountModels(false);
    } catch (err: any) {
        renderState.singleProbeErrors[accountId] = err?.message || '单独检测接口调用失败';
        renderCurrentData();
    } finally {
        renderState.probingAccountIds.delete(accountId);
        renderCurrentData();
    }
}
