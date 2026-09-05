import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';
import i18n from '../shared/i18n';
import {
    nvidiaRevealAccountId,
    otherRevealAccountId,
    setRevealKeyWarning
} from '../shared/revealKeyState';
import {
    initRendererElements,
    renderAccounts,
    updateAggregateQuotaUI,
    loadAccountQuota
} from './accountsRenderer';
import * as hitRateFilter from './hitRateFilter';
import { initNvidiaPreferredShuttle, setNvidiaPreferredModelsButtonVisible } from './nvidiaPreferredShuttle';
import { initAutoTriggerModalEvents, switchAutoTriggerPanel } from './autoTrigger';
export { switchAutoTriggerPanel } from './autoTrigger';
import { initTriggerTestModalEvents, appendTriggerTestLog } from './triggerTestModal';
import { initSessionBindingsModalEvents } from './sessionBindingsModal';
import { initNvidiaAccountModalEvents, writeNvidiaModalError, openNvidiaAccountModal } from './nvidiaAccountModal';
import { initNvidiaBatchAssignIPModal } from './nvidiaBatchAssignIPModal';
import { initOtherAccountModalEvents, writeOtherModalError, openOtherAccountModal } from './otherAccountModal';
import { initOtherCooldownModalEvents } from './otherCooldownModal';
import { initGrokAccountModalEvents, writeGrokModalError, openGrokAccountModal } from './grokAccountModal';
import { initGrokThawModalEvents } from './grokThawModal';
import { initGrokCheckAuthEvents } from './grokCheckAuth';
import { ensureNvidiaCooldownTimer } from './nvidiaCooldownTimer';
import { ensureGrokCooldownTimer } from './grokCooldownTimer';
import { initOtherGroupTabsEvents, renderOtherGroupTabs, renderOtherLBMode, otherLBModeSelectorVisible } from './otherGroupTabs';
import { initQuotaRenderEvents, initQuotaRenderGlobalEvents, tryInitialSilentQuotaRefresh } from './quotaRender';
export { renderOtherGroupTabs } from './otherGroupTabs';
export { ensureNvidiaCooldownTimer } from './nvidiaCooldownTimer';
export { ensureGrokCooldownTimer } from './grokCooldownTimer';
export { openEditOtherAccount } from './otherAccountModal';
export { openEditNvidiaAccount } from './nvidiaAccountModal';
export { openEditGrokAccount } from './grokAccountModal';

// 账号导出 / 布局缓存 / 批量操作栏 / Grok·NVIDIA 工具栏按钮显隐等纯工具函数已迁至 accountsUtil.ts,
// 此处 re-export 保持对外 API 完全兼容(exportAccountConfig / refreshAccountLayoutFromCache /
// updateBatchActionBarUI 等符号对外不变)。exportSingleAccount 已在 accountsUtil 顶层注册到 window。
import { exportAccountConfig, refreshAccountLayoutFromCache, setGrokThawButtonVisible, setGrokCheckAuthButtonVisible, setNvidiaBatchAssignIPButtonVisible, updateBatchActionBarUI } from './accountsUtil';
export { refreshAccountLayoutFromCache, updateBatchActionBarUI } from './accountsUtil';
import { startProjectLogin } from './accountsLogin';

let btnAddAccount: HTMLButtonElement | null;
let addAccountDropdown: HTMLDivElement | null;
let poolModeToggle: HTMLInputElement | null;
let poolModeContainer: HTMLDivElement | null;
let lblPoolMode: HTMLElement | null;
let btnChannelAntigravity: HTMLButtonElement | null;
let btnChannelProject: HTMLButtonElement | null;
let btnChannelGeminiCli: HTMLButtonElement | null;
let btnChannelNvidia: HTMLButtonElement | null;
let btnChannelOther: HTMLButtonElement | null;
let btnChannelGrok: HTMLButtonElement | null;
let btnAddOtherAccount: HTMLButtonElement | null;
let nvidiaPoolModeContainer: HTMLDivElement | null;
let nvidiaPoolModeToggle: HTMLInputElement | null;
let nvidiaLBModeContainer: HTMLDivElement | null;
let nvidiaLBModeSelect: HTMLSelectElement | null;
// 单账号在途并发上限 input(0=未配置回退默认 10;超过自动换号)。
// nvidiaNvidiaMaxConcurrency:NVIDIA 池;poolMaxConcurrency:antigravity/project 两 Tab 共用;
// otherMaxConcurrency:Other 按组配置(选中具体组时显示)。
let nvidiaMaxConcurrency: HTMLInputElement | null;
let poolMaxConcurrency: HTMLInputElement | null;
// Antigravity 池全局 Hub 客户端版本号(号池单值,对仗 poolMaxConcurrency):发往 Google 上游 User-Agent 身份头,默认 2.3.1。
let antigravityCliVersionWrap: HTMLDivElement | null;
let antigravityCliVersion: HTMLInputElement | null;
let btnAddNvidiaAccount: HTMLButtonElement | null;
let btnAddGrokAccount: HTMLButtonElement | null;
// Grok 池控件:LB 算法 select + 单池单值并发上限(与 NVIDIA 同构,无总开关 toggle)。
let grokLBModeContainer: HTMLDivElement | null;
let grokLBModeSelect: HTMLSelectElement | null;
let grokMaxConcurrency: HTMLInputElement | null;
// Grok 池全局 CLI 客户端版本号(号池单值,对仗 grokMaxConcurrency):发往 chat-proxy 上游身份头,默认 1.0.0。
let grokCliVersion: HTMLInputElement | null;
// Grok 池「额度超限后冷却时长」(号池单值, 单位小时, 对仗 grokCliVersion):单账号 429/403 等待 5s 重试 1 次仍失败
// 即挂此冷却(默认 24h=1 天)。0/留空回退默认 24; 网络错误走 60s 短冷却不受此值影响。
let grokQuotaCooldownHours: HTMLInputElement | null;
// Grok 池 Cloudflare Worker 出口代理(号池单值,对仗 NVIDIA 设置面板同款):
// 启用后 relay.grok.go 把上游 baseURL 改写为 Worker URL,真实上游经 X-Target-Upstream 头透传。
let grokWorkerProxyEnabled: HTMLInputElement | null;
let grokWorkerProxyUrl: HTMLInputElement | null;
// Antigravity 池 Cloudflare Worker 出口代理(号池单值,对仗 grokWorkerProxyEnabled):
// 启用后 relay.compat.finalRequester 把目标 URL 的 scheme/host 改写为 Worker 地址(保留 path/query),
// 真实上游经 X-Target-Upstream 头透传。
let antigravityWorkerProxyWrap: HTMLDivElement | null;
let antigravityWorkerProxyEnabled: HTMLInputElement | null;
let antigravityWorkerProxyUrl: HTMLInputElement | null;
let btnExportAccounts: HTMLButtonElement | null;
let btnImportAccounts: HTMLButtonElement | null;
let btnLayoutGrid: HTMLButtonElement | null;
let btnLayoutList: HTMLButtonElement | null;

let isGlobalEventsInitialized = false;

export function updatePoolModeUI() {
    if (!poolModeToggle) return;
    const isPool = poolModeToggle.checked;
    const label = poolModeToggle.nextElementSibling;
    if (!label) return;
    
    if (isPool) {
        poolModeToggle.className = 'toggle-checkbox absolute block w-5 h-5 rounded-full bg-white border-4 border-primary appearance-none cursor-pointer translate-x-5 transition-transform duration-200 ease-in-out';
        label.className = 'toggle-label block overflow-hidden h-5 rounded-full bg-primary cursor-pointer';
    } else {
        poolModeToggle.className = 'toggle-checkbox absolute block w-5 h-5 rounded-full bg-white border-4 border-outline-variant appearance-none cursor-pointer translate-x-0 transition-transform duration-200 ease-in-out';
        label.className = 'toggle-label block overflow-hidden h-5 rounded-full bg-outline-variant/50 dark:bg-white/10 cursor-pointer';
    }
}

export function updateLayoutUI() {
    const gridBtn = btnLayoutGrid || (document.getElementById('btnLayoutGrid') as HTMLButtonElement | null);
    const listBtn = btnLayoutList || (document.getElementById('btnLayoutList') as HTMLButtonElement | null);
    const selectGridColumns = document.getElementById('selectGridColumns') as HTMLSelectElement | null;
    const accountsListEl = document.getElementById('accountsList');
    
    const activeClass = 'p-1 rounded-md cursor-pointer transition-all duration-200 bg-white dark:bg-[#1a1f30] text-primary dark:text-primary-fixed-dim shadow-sm flex items-center justify-center';
    const inactiveClass = 'p-1 rounded-md cursor-pointer transition-all duration-200 text-slate-400 hover:text-slate-700 dark:hover:text-slate-200 flex items-center justify-center';
    
    if (state.accountLayout === 'grid') {
        if (gridBtn) gridBtn.className = activeClass;
        if (listBtn) listBtn.className = inactiveClass;
        if (selectGridColumns) {
            selectGridColumns.classList.remove('hidden');
            selectGridColumns.value = String(state.accountGridColumns);
        }
        if (accountsListEl) {
            accountsListEl.classList.remove('layout-list');
            accountsListEl.classList.add('layout-grid');
            accountsListEl.classList.remove('cols-3', 'cols-4', 'cols-5');
            accountsListEl.classList.add(`cols-${state.accountGridColumns}`);
        }
    } else {
        if (gridBtn) gridBtn.className = inactiveClass;
        if (listBtn) listBtn.className = activeClass;
        if (selectGridColumns) {
            selectGridColumns.classList.add('hidden');
        }
        if (accountsListEl) {
            accountsListEl.classList.remove('layout-grid', 'cols-3', 'cols-4', 'cols-5');
            accountsListEl.classList.add('layout-list');
        }
    }
}

// refreshAccountLayoutFromCache 已迁至 accountsUtil.ts(见顶部 re-export)。

// setGrokThawButtonVisible / setGrokCheckAuthButtonVisible / setNvidiaBatchAssignIPButtonVisible
// 已迁至 accountsUtil.ts,经顶部 import 复用。

export function updateViewTabUI() {
    if (btnChannelAntigravity && btnChannelProject) {
        const activeClass = 'px-4 py-1.5 rounded-md font-bold cursor-pointer transition-all duration-200 bg-white dark:bg-[#1a1f30] text-primary dark:text-primary-fixed-dim shadow-sm';
        const inactiveClass = 'px-4 py-1.5 rounded-md font-medium cursor-pointer transition-all duration-200 text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200';
        const dict = i18n[state.currentLanguage] || i18n.zh;

        if (state.currentViewTab === 'antigravity') {
            btnChannelAntigravity.className = activeClass;
            btnChannelProject.className = inactiveClass;
            if (btnChannelGeminiCli) btnChannelGeminiCli.className = inactiveClass;
            if (btnChannelNvidia) btnChannelNvidia.className = inactiveClass;
            if (btnChannelOther) btnChannelOther.className = inactiveClass;
            if (btnChannelGrok) btnChannelGrok.className = inactiveClass;

            if (poolModeContainer) poolModeContainer.classList.remove('hidden');
            if (nvidiaPoolModeContainer) nvidiaPoolModeContainer.classList.add('hidden');
            if (nvidiaLBModeContainer) nvidiaLBModeContainer.classList.add('hidden');
            if (grokLBModeContainer) grokLBModeContainer.classList.add('hidden');
            setGrokThawButtonVisible(false);
            setGrokCheckAuthButtonVisible(false);
            setNvidiaBatchAssignIPButtonVisible(false);
            if (lblPoolMode) lblPoolMode.innerText = dict.poolLoadBalance || '账号负载均衡';
            if (poolModeToggle && state.lastBackendData) {
                poolModeToggle.checked = state.lastBackendData.poolMode;
            }
            // antigravity Tab:并发上限 input 用 antigravityMaxConcurrency 回填(?? 10 兜底)。
            if (poolMaxConcurrency && state.lastBackendData) {
                poolMaxConcurrency.value = String(state.lastBackendData.antigravityMaxConcurrency ?? 10);
            }
            if (antigravityCliVersionWrap) antigravityCliVersionWrap.classList.remove('hidden');
            if (antigravityCliVersion && state.lastBackendData) {
                antigravityCliVersion.value = state.lastBackendData.antigravityCliVersion || '2.3.1';
            }
            // Antigravity Tab:Worker 代理出口 toggle+URL input 随容器一起显隐,回填自 settings 层。
            if (antigravityWorkerProxyWrap) antigravityWorkerProxyWrap.classList.remove('hidden');
            if (antigravityWorkerProxyEnabled && state.lastBackendData) {
                antigravityWorkerProxyEnabled.checked = !!state.lastBackendData.antigravityWorkerProxyEnabled;
            }
            if (antigravityWorkerProxyUrl && state.lastBackendData) {
                antigravityWorkerProxyUrl.value = state.lastBackendData.antigravityWorkerProxyUrl || '';
            }
        /* } else if (state.currentViewTab === 'gemini-cli') {
            if (btnChannelGeminiCli) btnChannelGeminiCli.className = activeClass;
            btnChannelAntigravity.className = inactiveClass;
            btnChannelProject.className = inactiveClass;

            if (poolModeContainer) poolModeContainer.classList.remove('hidden');
            if (lblPoolMode) lblPoolMode.innerText = 'CLI号池负载均衡';
            if (poolModeToggle && state.lastBackendData) {
                poolModeToggle.checked = state.lastBackendData.geminiCliPoolMode;
            } */
        } else if (state.currentViewTab === 'nvidia') {
            if (btnChannelNvidia) btnChannelNvidia.className = activeClass;
            btnChannelAntigravity.className = inactiveClass;
            btnChannelProject.className = inactiveClass;
            if (btnChannelGeminiCli) btnChannelGeminiCli.className = inactiveClass;
            if (btnChannelOther) btnChannelOther.className = inactiveClass;
            if (btnChannelGrok) btnChannelGrok.className = inactiveClass;

            // NVIDIA 用独立算法选择框
            if (poolModeContainer) poolModeContainer.classList.add('hidden');
            if (nvidiaLBModeContainer) nvidiaLBModeContainer.classList.remove('hidden');
            if (grokLBModeContainer) grokLBModeContainer.classList.add('hidden');
            // NVIDIA Tab 显示穿梭框入口按钮与分配住宅IP按钮。
            setNvidiaPreferredModelsButtonVisible(true);
            setNvidiaBatchAssignIPButtonVisible(true);
            setGrokThawButtonVisible(false);
            setGrokCheckAuthButtonVisible(false);
            if (nvidiaLBModeSelect && state.lastBackendData) {
                nvidiaLBModeSelect.value = state.lastBackendData.nvidiaLBMode || 'round-robin';
            }
            // NVIDIA Tab:并发上限 input 用 nvidiaMaxConcurrency 回填(?? 10 兜底)。
            if (nvidiaMaxConcurrency && state.lastBackendData) {
                nvidiaMaxConcurrency.value = String(state.lastBackendData.nvidiaMaxConcurrency ?? 10);
            }
        } else if (state.currentViewTab === 'grok') {
            if (btnChannelGrok) btnChannelGrok.className = activeClass;
            btnChannelAntigravity.className = inactiveClass;
            btnChannelProject.className = inactiveClass;
            if (btnChannelGeminiCli) btnChannelGeminiCli.className = inactiveClass;
            if (btnChannelNvidia) btnChannelNvidia.className = inactiveClass;
            if (btnChannelOther) btnChannelOther.className = inactiveClass;

            // Grok 用独立 LB 算法选择框(与 NVIDIA 同构),无总开关 toggle(决策 C)。
            if (poolModeContainer) poolModeContainer.classList.add('hidden');
            if (nvidiaLBModeContainer) nvidiaLBModeContainer.classList.add('hidden');
            if (grokLBModeContainer) grokLBModeContainer.classList.remove('hidden');
            setNvidiaPreferredModelsButtonVisible(false);
            setNvidiaBatchAssignIPButtonVisible(false);
            setGrokThawButtonVisible(true);
            setGrokCheckAuthButtonVisible(true);
            if (grokLBModeSelect && state.lastBackendData) {
                grokLBModeSelect.value = state.lastBackendData.grokLBMode || 'round-robin';
            }
            // Grok Tab:并发上限 input 用 grokMaxConcurrency 回填(?? 10 兜底)。
            if (grokMaxConcurrency && state.lastBackendData) {
                grokMaxConcurrency.value = String(state.lastBackendData.grokMaxConcurrency ?? 10);
            }
            // Grok Tab:全局 CLI 版本号 input 用 grokCliVersion 回填(?? '1.0.0' 兜底,与后端 GetGrokCliVersion 默认一致)。
            if (grokCliVersion && state.lastBackendData) {
                grokCliVersion.value = state.lastBackendData.grokCliVersion || '1.0.0';
            }
            // Grok Tab:额度超限冷却时长 input 用 grokQuotaCooldownHours 回填(?? 24 兜底,与后端 GetGrokQuotaCooldownHours 默认一致)。
            if (grokQuotaCooldownHours && state.lastBackendData) {
                grokQuotaCooldownHours.value = String((state.lastBackendData.grokQuotaCooldownHours as number) ?? 24);
            }
            // Grok Tab:Worker 代理出口 toggle+URL input 回填(settings 层承接持久化与启用断言)。
            // grokWorkerProxyEnabled 容器恒显(在 grokLBModeContainer 内部,跟随父容器显隐)。
            if (grokWorkerProxyEnabled && state.lastBackendData) {
                grokWorkerProxyEnabled.checked = !!state.lastBackendData.grokWorkerProxyEnabled;
            }
            if (grokWorkerProxyUrl && state.lastBackendData) {
                grokWorkerProxyUrl.value = state.lastBackendData.grokWorkerProxyUrl || '';
            }
        } else if (state.currentViewTab === 'other') {
            if (btnChannelOther) btnChannelOther.className = activeClass;
            btnChannelAntigravity.className = inactiveClass;
            btnChannelProject.className = inactiveClass;
            if (btnChannelGeminiCli) btnChannelGeminiCli.className = inactiveClass;
            if (btnChannelNvidia) btnChannelNvidia.className = inactiveClass;
            if (btnChannelGrok) btnChannelGrok.className = inactiveClass;

            // Other 号池:暂无独立负载均衡控件(组内轮换由后端 LBMode 控制),隐藏两个 toggle 容器。
            if (poolModeContainer) poolModeContainer.classList.add('hidden');
            if (nvidiaLBModeContainer) nvidiaLBModeContainer.classList.add('hidden');
            if (grokLBModeContainer) grokLBModeContainer.classList.add('hidden');
            setNvidiaPreferredModelsButtonVisible(false);
            setNvidiaBatchAssignIPButtonVisible(false);
            setGrokThawButtonVisible(false);
            setGrokCheckAuthButtonVisible(false);
        } else {
            btnChannelProject.className = activeClass;
            btnChannelAntigravity.className = inactiveClass;
            if (btnChannelGeminiCli) btnChannelGeminiCli.className = inactiveClass;
            if (btnChannelNvidia) btnChannelNvidia.className = inactiveClass;
            if (btnChannelOther) btnChannelOther.className = inactiveClass;
            if (btnChannelGrok) btnChannelGrok.className = inactiveClass;

            if (poolModeContainer) poolModeContainer.classList.remove('hidden');
            if (nvidiaLBModeContainer) nvidiaLBModeContainer.classList.add('hidden');
            if (grokLBModeContainer) grokLBModeContainer.classList.add('hidden');
            setNvidiaPreferredModelsButtonVisible(false);
            setNvidiaBatchAssignIPButtonVisible(false);
            setGrokThawButtonVisible(false);
            setGrokCheckAuthButtonVisible(false);
            if (lblPoolMode) lblPoolMode.innerText = dict.projectLoadBalancing || '项目负载均衡';
            if (poolModeToggle && state.lastBackendData) {
                poolModeToggle.checked = state.lastBackendData.projectPoolMode;
            }
            if (antigravityCliVersionWrap) antigravityCliVersionWrap.classList.add('hidden');
            if (antigravityWorkerProxyWrap) antigravityWorkerProxyWrap.classList.add('hidden');
            // project Tab:并发上限 input 用 projectMaxConcurrency 回填(?? 10 兜底)。
            if (poolMaxConcurrency && state.lastBackendData) {
                poolMaxConcurrency.value = String(state.lastBackendData.projectMaxConcurrency ?? 10);
            }
        }
        updatePoolModeUI();
    }

    const btnAntigravityLogin = document.getElementById('btnAntigravityLogin');
    const btnGeminiCliLogin = document.getElementById('btnGeminiCliLogin');
    const btnProjectLogin = document.getElementById('btnProjectLogin');

    if (state.currentViewTab === 'antigravity') {
        if (btnAntigravityLogin) btnAntigravityLogin.classList.remove('hidden');
        if (btnGeminiCliLogin) btnGeminiCliLogin.classList.add('hidden');
        if (btnProjectLogin) btnProjectLogin.classList.add('hidden');
        if (btnAddNvidiaAccount) btnAddNvidiaAccount.classList.add('hidden');
        if (btnAddOtherAccount) btnAddOtherAccount.classList.add('hidden');
        if (btnAddGrokAccount) btnAddGrokAccount.classList.add('hidden');
        setNvidiaPreferredModelsButtonVisible(false);
    /* } else if (state.currentViewTab === 'gemini-cli') {
        if (btnAntigravityLogin) btnAntigravityLogin.classList.add('hidden');
        if (btnGeminiCliLogin) btnGeminiCliLogin.classList.remove('hidden');
        if (btnProjectLogin) btnProjectLogin.classList.add('hidden'); */
    } else if (state.currentViewTab === 'nvidia') {
        if (btnAntigravityLogin) btnAntigravityLogin.classList.add('hidden');
        if (btnGeminiCliLogin) btnGeminiCliLogin.classList.add('hidden');
        if (btnProjectLogin) btnProjectLogin.classList.add('hidden');
        if (btnAddNvidiaAccount) btnAddNvidiaAccount.classList.remove('hidden');
        if (btnAddOtherAccount) btnAddOtherAccount.classList.add('hidden');
        if (btnAddGrokAccount) btnAddGrokAccount.classList.add('hidden');
        // NVIDIA Tab 显示穿梭框入口按钮（委托穿梭框模块控制显隐,不跨簇共享 DOM 句柄）。
        setNvidiaPreferredModelsButtonVisible(true);
    } else if (state.currentViewTab === 'grok') {
        if (btnAntigravityLogin) btnAntigravityLogin.classList.add('hidden');
        if (btnGeminiCliLogin) btnGeminiCliLogin.classList.add('hidden');
        if (btnProjectLogin) btnProjectLogin.classList.add('hidden');
        if (btnAddNvidiaAccount) btnAddNvidiaAccount.classList.add('hidden');
        if (btnAddOtherAccount) btnAddOtherAccount.classList.add('hidden');
        if (btnAddGrokAccount) btnAddGrokAccount.classList.remove('hidden');
        setNvidiaPreferredModelsButtonVisible(false);
    } else if (state.currentViewTab === 'other') {
        if (btnAntigravityLogin) btnAntigravityLogin.classList.add('hidden');
        if (btnGeminiCliLogin) btnGeminiCliLogin.classList.add('hidden');
        if (btnProjectLogin) btnProjectLogin.classList.add('hidden');
        if (btnAddNvidiaAccount) btnAddNvidiaAccount.classList.add('hidden');
        if (btnAddOtherAccount) btnAddOtherAccount.classList.remove('hidden');
        if (btnAddGrokAccount) btnAddGrokAccount.classList.add('hidden');
        setNvidiaPreferredModelsButtonVisible(false);
    } else {
        if (btnAntigravityLogin) btnAntigravityLogin.classList.add('hidden');
        if (btnGeminiCliLogin) btnGeminiCliLogin.classList.add('hidden');
        if (btnProjectLogin) btnProjectLogin.classList.remove('hidden');
        if (btnAddNvidiaAccount) btnAddNvidiaAccount.classList.add('hidden');
        if (btnAddOtherAccount) btnAddOtherAccount.classList.add('hidden');
        if (btnAddGrokAccount) btnAddGrokAccount.classList.add('hidden');
        setNvidiaPreferredModelsButtonVisible(false);
    }
}

// startLogin / startProjectLogin 已迁至 accountsLogin.ts(采用自包含 DOM 句柄策略,零耦合)。
// window.startLogin / window.startProjectLogin 亦由 accountsLogin.ts 顶层注册完成。

// Initialize account pool controls and bindings
export function initAccountsEvents() {
    initRendererElements();

    btnAddAccount = document.getElementById('btnAddAccount') as HTMLButtonElement | null;
    addAccountDropdown = document.getElementById('addAccountDropdown') as HTMLDivElement | null;
    poolModeToggle = document.getElementById('poolModeToggle') as HTMLInputElement | null;
    poolModeContainer = document.getElementById('poolModeContainer') as HTMLDivElement | null;
    lblPoolMode = document.getElementById('lblPoolMode');
    btnChannelAntigravity = document.getElementById('btnChannelAntigravity') as HTMLButtonElement | null;
    btnChannelProject = document.getElementById('btnChannelProject') as HTMLButtonElement | null;
    btnChannelGeminiCli = document.getElementById('btnChannelGeminiCli') as HTMLButtonElement | null;
    btnChannelNvidia = document.getElementById('btnChannelNvidia') as HTMLButtonElement | null;
    btnChannelOther = document.getElementById('btnChannelOther') as HTMLButtonElement | null;
    btnChannelGrok = document.getElementById('btnChannelGrok') as HTMLButtonElement | null;
    nvidiaPoolModeContainer = document.getElementById('nvidiaPoolModeContainer') as HTMLDivElement | null;
    nvidiaPoolModeToggle = document.getElementById('nvidiaPoolModeToggle') as HTMLInputElement | null;
    nvidiaLBModeContainer = document.getElementById('nvidiaLBModeContainer') as HTMLDivElement | null;
    nvidiaLBModeSelect = document.getElementById('nvidiaLBModeSelect') as HTMLSelectElement | null;
    // 并发上限 input 绑 DOM(三处容器共用 poolMaxConcurrency:NVIDIA 用 nvidiaMaxConcurrency,
    // antigravity/project 两 Tab 共用 poolMaxConcurrency 按 currentViewTab 分流,Other 用 otherMaxConcurrency)。
    nvidiaMaxConcurrency = document.getElementById('nvidiaMaxConcurrency') as HTMLInputElement | null;
    poolMaxConcurrency = document.getElementById('poolMaxConcurrency') as HTMLInputElement | null;
    antigravityCliVersionWrap = document.getElementById('antigravityCliVersionWrap') as HTMLDivElement | null;
    antigravityCliVersion = document.getElementById('antigravityCliVersion') as HTMLInputElement | null;
    btnAddNvidiaAccount = document.getElementById('btnAddNvidiaAccount') as HTMLButtonElement | null;
    btnAddOtherAccount = document.getElementById('btnAddOtherAccount') as HTMLButtonElement | null;
    btnAddGrokAccount = document.getElementById('btnAddGrokAccount') as HTMLButtonElement | null;
    // Grok 池 LB 容器 + 算法 select + 并发上限 input + CLI 版本号 input(与 NVIDIA 同构,DOM 在 Accounts.vue)。
    grokLBModeContainer = document.getElementById('grokLBModeContainer') as HTMLDivElement | null;
    grokLBModeSelect = document.getElementById('grokLBModeSelect') as HTMLSelectElement | null;
    grokMaxConcurrency = document.getElementById('grokMaxConcurrency') as HTMLInputElement | null;
    grokCliVersion = document.getElementById('grokCliVersion') as HTMLInputElement | null;
    grokQuotaCooldownHours = document.getElementById('grokQuotaCooldownHours') as HTMLInputElement | null;
    // Grok 池 Worker 代理出口(号池单值,DOM 在 Accounts.vue grokLBModeContainer 尾部)。
    grokWorkerProxyEnabled = document.getElementById('grokWorkerProxyEnabled') as HTMLInputElement | null;
    grokWorkerProxyUrl = document.getElementById('grokWorkerProxyUrl') as HTMLInputElement | null;
    // Antigravity 池 Worker 代理出口(号池单值,DOM 在 Accounts.vue poolModeContainer 尾部)。
    antigravityWorkerProxyWrap = document.getElementById('antigravityWorkerProxyWrap') as HTMLDivElement | null;
    antigravityWorkerProxyEnabled = document.getElementById('antigravityWorkerProxyEnabled') as HTMLInputElement | null;
    antigravityWorkerProxyUrl = document.getElementById('antigravityWorkerProxyUrl') as HTMLInputElement | null;
    // Other 号池组名子 Tab + LB 模式：句柄赋值 + 事件绑定（已抽离 otherGroupTabs.ts）
    initOtherGroupTabsEvents();

    // Other 账号 Modal：句柄赋值 + 事件绑定（已抽离 otherAccountModal.ts）
    initOtherAccountModalEvents();

    // Other 组级自定义冷却 Modal:句柄赋值 + 事件绑定(已抽离 otherCooldownModal.ts,
    // 工具栏入口按钮 btnOtherCooldownConfig 在 Other 通道选中具体组时显示)。
    initOtherCooldownModalEvents();

    // NVIDIA 账号 Modal：句柄赋值 + 事件绑定（已抽离 nvidiaAccountModal.ts）
    initNvidiaAccountModalEvents();

    // NVIDIA 批量分配住宅 IP Modal：句柄赋值 + 事件绑定
    initNvidiaBatchAssignIPModal();

    // Grok 账号 Modal：句柄赋值 + 事件绑定（已抽离 grokAccountModal.ts）
    initGrokAccountModalEvents();

    // Grok 一键解冻 Modal:句柄赋值 + 事件绑定(已抽离 grokThawModal.ts,工具栏按钮在 Grok Tab 显示)。
    initGrokThawModalEvents();

    // Grok 「检查授权」按钮:句柄赋值 + 事件绑定(已抽离 grokCheckAuth.ts,工具栏按钮在 Grok Tab 显示)。
    // 与一键解冻成对,走 grok:check-auth IPC,后端复用 1h 定时同一套 CheckAndPurgeGrokAuth 逻辑。
    initGrokCheckAuthEvents();

    btnExportAccounts = document.getElementById('btnExportAccounts') as HTMLButtonElement | null;
    btnImportAccounts = document.getElementById('btnImportAccounts') as HTMLButtonElement | null;

    // 会话绑定 Modal：句柄赋值 + 事件绑定（已抽离 sessionBindingsModal.ts）
    initSessionBindingsModalEvents();

    // 配额刷新器：句柄赋值 + 事件绑定（已抽离 quotaRender.ts）
    initQuotaRenderEvents();

    // Filter & Pagination Event Bindings
    const inputAccountSearch = document.getElementById('inputAccountSearch') as HTMLInputElement | null;
    const selectAccountStatus = document.getElementById('selectAccountStatus') as HTMLSelectElement | null;
    const selectAccountTier = document.getElementById('selectAccountTier') as HTMLSelectElement | null;
    const btnPrevAccountPage = document.getElementById('btnPrevAccountPage') as HTMLButtonElement | null;
    const btnNextAccountPage = document.getElementById('btnNextAccountPage') as HTMLButtonElement | null;

    if (inputAccountSearch) {
        inputAccountSearch.addEventListener('input', (e: any) => {
            state.accountSearchQuery = e.target.value;
            state.accountCurrentPage = 1;
            renderAccounts(state.currentAccountsList);
        });
    }
    if (selectAccountStatus) {
        selectAccountStatus.addEventListener('change', (e: any) => {
            state.accountStatusFilter = e.target.value;
            state.accountCurrentPage = 1;
            renderAccounts(state.currentAccountsList);
        });
    }
    if (selectAccountTier) {
        selectAccountTier.addEventListener('change', (e: any) => {
            state.accountTierFilter = e.target.value;
            state.accountCurrentPage = 1;
            renderAccounts(state.currentAccountsList);
        });
    }
    if (btnPrevAccountPage) {
        btnPrevAccountPage.addEventListener('click', () => {
            if (state.accountCurrentPage > 1) {
                state.accountCurrentPage--;
                renderAccounts(state.currentAccountsList);
            }
        });
    }
    if (btnNextAccountPage) {
        btnNextAccountPage.addEventListener('click', () => {
            state.accountCurrentPage++;
            renderAccounts(state.currentAccountsList);
        });
    }

    // domReady 已注入 wailsConfigCache,此处同步回填后端持久化的布局/列数(见 refreshAccountLayoutFromCache)。
    refreshAccountLayoutFromCache();

    btnLayoutGrid = document.getElementById('btnLayoutGrid') as HTMLButtonElement | null;
    btnLayoutList = document.getElementById('btnLayoutList') as HTMLButtonElement | null;

    if (btnLayoutGrid) {
        btnLayoutGrid.addEventListener('click', () => {
            if (state.accountLayout === 'grid') return;
            state.accountLayout = 'grid';
            ipcRenderer.send('settings:set-account-layout', 'grid');
            updateLayoutUI();
            renderAccounts(state.currentAccountsList);
        });
    }
    if (btnLayoutList) {
        btnLayoutList.addEventListener('click', () => {
            if (state.accountLayout === 'list') return;
            state.accountLayout = 'list';
            ipcRenderer.send('settings:set-account-layout', 'list');
            updateLayoutUI();
            renderAccounts(state.currentAccountsList);
        });
    }

    const selectGridColumns = document.getElementById('selectGridColumns') as HTMLSelectElement | null;
    if (selectGridColumns) {
        // 列数切换写入后端 config.json:300ms debounce 防频繁落盘(对齐 grok:set-cli-version 范式)。
        let colsDebounce: ReturnType<typeof setTimeout> | null = null;
        selectGridColumns.addEventListener('change', (e: any) => {
            const cols = Number(e.target.value);
            state.accountGridColumns = cols;
            updateLayoutUI();
            renderAccounts(state.currentAccountsList);
            if (colsDebounce) clearTimeout(colsDebounce);
            colsDebounce = setTimeout(() => {
                ipcRenderer.send('settings:set-account-grid-columns', cols);
            }, 300);
        });
    }

    updateLayoutUI();

    if (btnAddAccount && addAccountDropdown) {
        btnAddAccount.addEventListener('click', async () => {
            if (state.isLoadingAuth) {
                try {
                    await ipcRenderer.invoke('auth:cancel-login');
                } catch (err) {
                    console.error('Failed to cancel login:', err);
                }
                return;
            }
            if (addAccountDropdown) addAccountDropdown.classList.toggle('hidden');
        });
    }

    // Dynamic project-based button appending
    if (addAccountDropdown && !document.getElementById('btnProjectLogin')) {
        const projectLoginButton = document.createElement('button');
        projectLoginButton.id = 'btnProjectLogin';
        projectLoginButton.className = 'w-full text-left px-4 py-2 text-[13px] text-on-surface dark:text-white hover:bg-slate-50 dark:hover:bg-white/5 transition-colors flex items-center gap-2 border-t border-outline-variant/10 mt-1 pt-3';
        projectLoginButton.type = 'button';
        const dict = i18n[state.currentLanguage] || i18n.zh;
        projectLoginButton.innerHTML = `
            <span class="material-symbols-outlined text-emerald-500 text-[16px]">cloud</span>
            <div>
                <div class="font-bold" data-i18n="useGcpProjectTitle">${dict.useGcpProjectTitle || 'Use a Google Cloud project'}</div>
                <div class="text-[10px] text-outline" data-i18n="useGcpProjectDesc">${dict.useGcpProjectDesc || '风控原因，需要提供带账单项目'}</div>
            </div>
        `;
        projectLoginButton.addEventListener('click', () => startProjectLogin());
        if (addAccountDropdown.children.length >= 2) {
            addAccountDropdown.insertBefore(projectLoginButton, addAccountDropdown.children[1]);
        } else {
            addAccountDropdown.appendChild(projectLoginButton);
        }
    }

    // NVIDIA 通道切换 Tab
    if (btnChannelNvidia) {
        btnChannelNvidia.addEventListener('click', () => {
            state.selectedAccountIds = [];
            state.currentViewTab = 'nvidia';
            updateViewTabUI();
            renderOtherGroupTabs();
            if (state.currentAccountsList) {
                renderAccounts(state.currentAccountsList);
            }
            updateAggregateQuotaUI();
            updateBatchActionBarUI();
        });
    }

    // Other 通道切换 Tab
    if (btnChannelOther) {
        btnChannelOther.addEventListener('click', () => {
            state.selectedAccountIds = [];
            state.currentViewTab = 'other';
            updateViewTabUI();
            renderOtherGroupTabs();
            if (state.currentAccountsList) {
                renderAccounts(state.currentAccountsList);
            }
            updateAggregateQuotaUI();
            updateBatchActionBarUI();
        });
    }

    // Grok 通道切换 Tab
    if (btnChannelGrok) {
        btnChannelGrok.addEventListener('click', () => {
            state.selectedAccountIds = [];
            state.currentViewTab = 'grok';
            updateViewTabUI();
            renderOtherGroupTabs();
            if (state.currentAccountsList) {
                renderAccounts(state.currentAccountsList);
            }
            updateAggregateQuotaUI();
            updateBatchActionBarUI();
        });
    }

    // Other 添加账号下拉项 → 打开 Other 账号模态
    if (btnAddOtherAccount) {
        btnAddOtherAccount.addEventListener('click', () => {
            if (addAccountDropdown) addAccountDropdown.classList.add('hidden');
            openOtherAccountModal();
        });
    }

    // NVIDIA 添加账号下拉项 → 打开 NVIDIA 账号模态
    if (btnAddNvidiaAccount) {
        btnAddNvidiaAccount.addEventListener('click', () => {
            if (addAccountDropdown) addAccountDropdown.classList.add('hidden');
            openNvidiaAccountModal();
        });
    }

    // Grok 添加账号下拉项 → 打开 Grok 账号模态
    if (btnAddGrokAccount) {
        btnAddGrokAccount.addEventListener('click', () => {
            if (addAccountDropdown) addAccountDropdown.classList.add('hidden');
            openGrokAccountModal();
        });
    }

    if (poolModeToggle) {
        poolModeToggle.addEventListener('change', (e: any) => {
            if (state.currentViewTab === 'project') {
                ipcRenderer.send('pool:toggle-project', e.target.checked);
            /* } else if (state.currentViewTab === 'gemini-cli') {
                ipcRenderer.send('pool:toggle-gemini-cli', e.target.checked); */
            } else {
                ipcRenderer.send('pool:toggle', e.target.checked);
            }
            updatePoolModeUI();
            updateAggregateQuotaUI();
        });
    }

    // NVIDIA 池算法选择框
    if (nvidiaLBModeSelect) {
        nvidiaLBModeSelect.addEventListener('change', (e: any) => {
            ipcRenderer.send('nvidia:set-lb-mode', e.target.value);
        });
    }

    // Grok 池算法选择框(与 NVIDIA 同构)
    if (grokLBModeSelect) {
        grokLBModeSelect.addEventListener('change', (e: any) => {
            ipcRenderer.send('grok:set-lb-mode', e.target.value);
        });
    }

    // 单账号在途并发上限:NVIDIA 池。300ms debounce 防频繁落盘(对齐既有高频输入节流范式)。
    if (nvidiaMaxConcurrency) {
        let nvDebounce: ReturnType<typeof setTimeout> | null = null;
        nvidiaMaxConcurrency.addEventListener('change', (e: any) => {
            const v = Math.max(0, Math.min(1000, Number(e.target.value) || 0));
            e.target.value = String(v);
            if (nvDebounce) clearTimeout(nvDebounce);
            nvDebounce = setTimeout(() => {
                ipcRenderer.send('nvidia:set-max-concurrency', v);
            }, 300);
        });
    }

    // 单账号在途并发上限:Grok 池。300ms debounce(与 NVIDIA 同构)。
    if (grokMaxConcurrency) {
        let gkDebounce: ReturnType<typeof setTimeout> | null = null;
        grokMaxConcurrency.addEventListener('change', (e: any) => {
            const v = Math.max(0, Math.min(1000, Number(e.target.value) || 0));
            e.target.value = String(v);
            if (gkDebounce) clearTimeout(gkDebounce);
            gkDebounce = setTimeout(() => {
                ipcRenderer.send('grok:set-max-concurrency', v);
            }, 300);
        });
    }

    // Grok 池全局 CLI 客户端版本号(号池单值,对仗并发上限):300ms debounce。空串回退默认 1.0.0
    // (与后端 SetGrokCliVersion TrimSpace + GetGrokCliVersion 回退一致), 避免清空导致重新触发 426。
    if (grokCliVersion) {
        let gcvDebounce: ReturnType<typeof setTimeout> | null = null;
        grokCliVersion.addEventListener('change', (e: any) => {
            const v = (e.target.value || '').trim() || '1.0.0';
            e.target.value = v;
            if (gcvDebounce) clearTimeout(gcvDebounce);
            gcvDebounce = setTimeout(() => {
                ipcRenderer.send('grok:set-cli-version', v);
            }, 300);
        });
    }

    // Grok 池「额度超限后冷却时长」(号池单值, 单位小时, 对仗 grokCliVersion):300ms debounce。
    // 0/留空回退默认 24(与后端 SetGrokQuotaCooldownHours 负数钳 0 + GetGrokQuotaCooldownHours 回退一致)。
    // 仅在单账号 429/403 等待 5s 重试 1 次仍失败时挂该冷却; 网络错误仍走 60s 短冷却。
    if (grokQuotaCooldownHours) {
        let gqcDebounce: ReturnType<typeof setTimeout> | null = null;
        grokQuotaCooldownHours.addEventListener('change', (e: any) => {
            const v = Math.max(0, Math.min(720, Math.floor(Number(e.target.value) || 0)));
            e.target.value = String(v);
            if (gqcDebounce) clearTimeout(gqcDebounce);
            gqcDebounce = setTimeout(() => {
                ipcRenderer.send('grok:set-quota-cooldown-hours', v);
            }, 300);
        });
    }

    // Antigravity 池全局 Hub 客户端版本号(号池单值,对仗并发上限):300ms debounce。空串回退默认 2.3.1
    if (antigravityCliVersion) {
        let acvDebounce: ReturnType<typeof setTimeout> | null = null;
        antigravityCliVersion.addEventListener('change', (e: any) => {
            const v = (e.target.value || '').trim() || '2.3.1';
            e.target.value = v;
            if (acvDebounce) clearTimeout(acvDebounce);
            acvDebounce = setTimeout(() => {
                ipcRenderer.send('antigravity:set-cli-version', v);
            }, 300);
        });
    }

    // Antigravity 池 Cloudflare Worker 代理出口(enable toggle + URL input 组合,与 grokWorkerProxyEnabled 同构)。
    // 未传 URL,开启后立即触发「Worker 改写但空 URL 视为未启用」的后端兜底,与 settings.IsXxxWorkerProxyEnabled 口径一致。
    if (antigravityWorkerProxyEnabled) {
        antigravityWorkerProxyEnabled.addEventListener('change', (e: any) => {
            ipcRenderer.send('settings:set-antigravity-worker-proxy-enabled', !!e.target.checked);
        });
    }
    if (antigravityWorkerProxyUrl) {
        let awpDebounce: ReturnType<typeof setTimeout> | null = null;
        antigravityWorkerProxyUrl.addEventListener('change', (e: any) => {
            const v = (e.target.value || '').trim();
            if (awpDebounce) clearTimeout(awpDebounce);
            awpDebounce = setTimeout(() => {
                ipcRenderer.send('settings:set-antigravity-worker-proxy-url', v);
            }, 300);
        });
    }

    // Grok 池 Cloudflare Worker 代理出口(号池单值,与 antigravityWorkerProxyEnabled 同构)。
    if (grokWorkerProxyEnabled) {
        grokWorkerProxyEnabled.addEventListener('change', (e: any) => {
            ipcRenderer.send('settings:set-grok-worker-proxy-enabled', !!e.target.checked);
        });
    }
    if (grokWorkerProxyUrl) {
        let gwpDebounce: ReturnType<typeof setTimeout> | null = null;
        grokWorkerProxyUrl.addEventListener('change', (e: any) => {
            const v = (e.target.value || '').trim();
            if (gwpDebounce) clearTimeout(gwpDebounce);
            gwpDebounce = setTimeout(() => {
                ipcRenderer.send('settings:set-grok-worker-proxy-url', v);
            }, 300);
        });
    }

    // 单账号在途并发上限:antigravity/project 两 Tab 共用此 input,按 currentViewTab 分流 IPC 通道。
    if (poolMaxConcurrency) {
        let poolDebounce: ReturnType<typeof setTimeout> | null = null;
        poolMaxConcurrency.addEventListener('change', (e: any) => {
            const v = Math.max(0, Math.min(1000, Number(e.target.value) || 0));
            e.target.value = String(v);
            if (poolDebounce) clearTimeout(poolDebounce);
            poolDebounce = setTimeout(() => {
                if (state.currentViewTab === 'project') {
                    ipcRenderer.send('project:set-max-concurrency', v);
                } else {
                    ipcRenderer.send('antigravity:set-max-concurrency', v);
                }
            }, 300);
        });
    }

    // 明文查看失败提示统一挂到 NVIDIA 模态框 error 红条:
    // 单例 handler 会在打开/关闭编辑弹窗时的各对应函数中被重新注入,这里仅保证「有兜底」。
    setRevealKeyWarning((msg: string) => {
        // NVIDIA / Other / Grok 红条句柄已分别迁入各自 Modal 模块,经其导出函数间接写。
        writeNvidiaModalError(msg);
        writeOtherModalError(msg);
        writeGrokModalError(msg);
    });

    if (btnExportAccounts) {
        btnExportAccounts.addEventListener('click', async () => {
            const provider = state.currentViewTab || 'antigravity';
            // 导出账号配置：走统一文件服务，后端负责对话框+目录记忆+自动打开文件夹
            await exportAccountConfig(provider);
        });
    }

    if (btnImportAccounts) {
        btnImportAccounts.addEventListener('click', async () => {
            // 导入账号配置：后端 Open 对话框 + 目录记忆。
            // 后端返回 {success, added, dir}：dir 为本次导入文件所在目录，
            // 据此"定位到之前选择的文件夹"，与导出侧行为对称。
            try {
                const res = await ipcRenderer.invoke('accounts:import');
                const payload = (res && typeof res === 'object') ? res : null;
                if (!payload || payload.success === false) {
                    // 后端表示用户取消或失败，不做提示
                    return;
                }
                // 兜底刷新:后端导入成功后会广播 accounts-res,但为确保任何路径下
                // 前端 currentAccountsList 都拿到最新快照(用户可能停在非账号页/广播时序交错),
                // 这里主动再拉一次全量账号,避免"导入后切 Grok 等号池 Tab 看不到新账号"的旧快照问题。
                // 注意:导入成功后不再自动打开文件夹——"打开/定位文件"是导出侧(后端 RevealFile)
                // 和下载专属语义,导入只需刷新列表即可;后端返回的 dir 字段保留但前端不消费。
                ipcRenderer.send('accounts:get');
            } catch (err) {
                console.error('Failed to import accounts:', err);
            }
        });
    }

    if (btnChannelAntigravity) {
        btnChannelAntigravity.addEventListener('click', () => {
            state.currentViewTab = 'antigravity';
            state.selectedAccountIds = [];
            updateViewTabUI();
            renderOtherGroupTabs();
            if (state.currentAccountsList) {
                renderAccounts(state.currentAccountsList);
            }
            updateAggregateQuotaUI();
            updateBatchActionBarUI();
        });
    }
    if (btnChannelProject) {
        btnChannelProject.addEventListener('click', () => {
            state.selectedAccountIds = [];
            state.currentViewTab = 'project';
            updateViewTabUI();
            renderOtherGroupTabs();
            if (state.currentAccountsList) {
                renderAccounts(state.currentAccountsList);
            }
            updateAggregateQuotaUI();
            updateBatchActionBarUI();
        });
    }
    /* if (btnChannelGeminiCli) {
        btnChannelGeminiCli.addEventListener('click', () => {
            state.currentViewTab = 'gemini-cli';
            ipcRenderer.send('channel:switch', 'gemini-cli');
            updateViewTabUI();
            if (state.currentAccountsList) {
                renderAccounts(state.currentAccountsList);
            }
            updateAggregateQuotaUI();
        });
    } */

    // Register accounts data update channel listener
    // (Moved to global initAccountsGlobalEvents below)

    // 全选按钮绑定
    const chkAll = document.getElementById('chkSelectAllAccounts') as HTMLInputElement | null;
    if (chkAll) {
        chkAll.addEventListener('change', (e: any) => {
            const isChecked = e.target.checked;
            const visibleCheckboxes = document.querySelectorAll('.account-card-checkbox') as NodeListOf<HTMLInputElement>;
            visibleCheckboxes.forEach(cb => {
                const accId = cb.getAttribute('data-account-id');
                if (!accId) return;
                cb.checked = isChecked;
                if (isChecked) {
                    if (!state.selectedAccountIds.includes(accId)) {
                        state.selectedAccountIds.push(accId);
                    }
                } else {
                    state.selectedAccountIds = state.selectedAccountIds.filter(id => id !== accId);
                }
            });
            updateBatchActionBarUI();
        });
    }

    // 批量删除按钮绑定与二次确认弹窗
    const btnBatchDelete = document.getElementById('btnBatchDeleteAccounts') as HTMLButtonElement | null;
    if (btnBatchDelete) {
        btnBatchDelete.addEventListener('click', async () => {
            const count = state.selectedAccountIds.length;
            if (count === 0) return;

            const dict = i18n[state.currentLanguage] || i18n.zh;
            const confirmMsg = (dict.batchDeleteAccountsConfirm || '确定要删除选中的 {count} 个账号吗？删除后不可恢复！')
                .replace('{count}', String(count));

            const $confirm = (window as any).$confirm;
            let confirmed = false;
            if (typeof $confirm === 'function') {
                confirmed = await $confirm(confirmMsg);
            } else {
                confirmed = confirm(confirmMsg);
            }
            if (!confirmed) return;

            const toDeleteIds = [...state.selectedAccountIds];
            try {
                await ipcRenderer.invoke('accounts:batch-remove', toDeleteIds);
            } catch (err) {
                // 兜底 send 兼容
                ipcRenderer.send('accounts:batch-remove', toDeleteIds);
            }

            state.selectedAccountIds = [];
            updateBatchActionBarUI();
            ipcRenderer.send('accounts:get');
        });
    }

    // 触发测试回复 Modal：句柄赋值 + 事件绑定（已抽离 triggerTestModal.ts）
    initTriggerTestModalEvents();

    // 当 DOM 挂载时，根据已经同步过的 accounts 数据对 DOM 状态进行一轮初始化
    if (state.lastBackendData) {
        updateViewTabUI();
        updatePoolModeUI();
        updateLayoutUI();
        renderOtherGroupTabs();
        if (state.currentAccountsList) {
            renderAccounts(state.currentAccountsList);
        }
        updateAggregateQuotaUI();
    }

    // 主动触发一次账号数据同步，确保在前端初始化完毕后拉取到最新数据
    // NVIDIA 专属模型清单穿梭框：句柄赋值 + 事件绑定 + 首屏徽标回读（已抽离 nvidiaPreferredShuttle.ts）
    initNvidiaPreferredShuttle();

    ipcRenderer.send('accounts:get');
    initAutoTriggerModalEvents();
}

export function initAccountsGlobalEvents() {
    // 注册全局一次性监听器，防止多次打开账号页面时重复绑定及闭包导致的 Detached DOM 内存泄漏
    if (!isGlobalEventsInitialized) {
        // 聚合刷新按钮：句柄赋值 + 事件绑定（已抽离 quotaRender.ts）
        initQuotaRenderGlobalEvents();
        // 获取主页“一键刷新”按钮 DOM 并进行事件绑定，因为主页 DOM 在程序启动时即始终存在

        // 1. 注册账号数据更新频道监听
        ipcRenderer.on('accounts-res', (event: any, data: any) => {
            // 合并而非整对象覆盖:后端某些广播源(如 OnAccountsUpdated 旧薄载荷)可能缺 otherGroups/
            // otherPoolMode 等字段,展开 {...old, ...data} 在缺字段时保留上一次完整值,避免 Other
            // 分组子 Tab 被冲成空数组而偶发隐藏。
            state.lastBackendData = { ...(state.lastBackendData || {}), ...data };
            if (data && typeof data.activeChannel !== 'undefined') {
                state.currentActiveChannel = data.activeChannel;
            }
            if (!state.currentViewTab) {
                state.currentViewTab = state.currentActiveChannel;
            }
            
            // 尝试更新视图 Tab
            updateViewTabUI();

            if (data.accounts) {
                state.currentAccountsList = data.accounts;
                // 每当后端广播账号快照,刷新 Other 二级组名子 Tab(新增/删除组、计数变化即时反映)。
                renderOtherGroupTabs();
                // 同步刷新缓存命中率卡片的号池筛选下拉(Other 组新增/删除时即时反映);
                // dirty-check 仅在组列表变化时才重建, 不覆盖用户的 hover/选中态。
                hitRateFilter.renderPoolFilterSelect();
                // 只有当 DOM 列表容器存在时才重新绘制账号卡片
                const accountsListEl = document.getElementById('accountsList');
                if (accountsListEl) {
                    renderAccounts(data.accounts);
                }
            }
            
            updateAggregateQuotaUI();
            // 首次收到账号列表时静默刷新一次配额（已抽离 quotaRender.ts,do-once 状态私藏）
            tryInitialSilentQuotaRefresh(data.accounts);
            
            if (state.callbacks.updateAnalyzeAccountSelect) {
                state.callbacks.updateAnalyzeAccountSelect();
            }

        });

        // 1.2 注册配额实时更新频道监听
        ipcRenderer.on('quota-updated', (event: any, data: any) => {
            if (data && data.accountId) {
                state.quotaCache[data.accountId] = data.buckets;
                const acc = state.currentAccountsList.find(a => a.id === data.accountId);
                const cooldowns = acc ? acc.cooldowns : {};
                loadAccountQuota(data.accountId, null, null, false, cooldowns);
            }
        });

        // 监听账号多选事件以刷新批量操作栏
        document.addEventListener('account-selection-changed', updateBatchActionBarUI);

        // 绑定全局点击事件，用于关闭添加账号的下拉菜单
        document.addEventListener('click', (e: any) => {
            if (btnAddAccount && addAccountDropdown && !btnAddAccount.contains(e.target) && !addAccountDropdown.contains(e.target)) {
                addAccountDropdown.classList.add('hidden');
            }
        });

        // 2. 绑定全局 log 事件，过滤显示测试进度
        ipcRenderer.on('log', (event: any, logText: string) => {
            if (logText && logText.includes('[测试回复]')) {
                appendTriggerTestLog(logText);
            }
        });

        isGlobalEventsInitialized = true;
    }

    // 3. 页面打开时拉取一次最新账号信息
    ipcRenderer.send('accounts:get');
}

// Global window registration (startLogin / startProjectLogin) 已迁至 accountsLogin.ts 顶层注册。

// updateBatchActionBarUI 已迁至 accountsUtil.ts(见顶部 re-export)。

// Register shared callbacks
state.callbacks.renderAccounts = renderAccounts;
state.callbacks.updateAggregateQuotaUI = updateAggregateQuotaUI;
// updateBatchActionBarUI 注入 state.callbacks,供 triggerTestModal 间接调用,切断循环 import。
state.callbacks.updateBatchActionBarUI = updateBatchActionBarUI;

// ===== NVIDIA 全局专属模型清单 Modal 逻辑 =====

// ==================== Other 号池 Modal 控制逻辑 ====================
