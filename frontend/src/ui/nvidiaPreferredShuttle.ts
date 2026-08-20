/**
 * NVIDIA 号池全局专属模型清单 Modal（双列穿梭框）模块：从 accountsController.ts 抽离的独立模块。
 *
 * 高内聚：本模块自带 28 个 DOM handle、4 个模块级状态、26 个函数，
 * 对外仅暴露 initNvidiaPreferredShuttle() 与 refreshNvidiaPreferredSourceI18n() 两个入口，
 * 由 accountsController.initAccountsEvents 统一调用。不依赖 renderAccounts 等跨簇符号；
 * i18n 经 import 引入，window.__nvidiaPreferredDict 保留为历史兼容注入点。
 */
import { ipcRenderer } from '../shared/ipc';
import i18n from '../shared/i18n';
import state from './dashboardState';
import { computeStaleLower, createDiffBadge, diffSummaryText, DiffKind } from './nvidiaPreferredDiff';

// ===== NVIDIA 全局专属模型清单 Modal (双列穿梭框) =====
let btnNvidiaPreferredModels: HTMLButtonElement | null;
let badgeNvidiaPreferredCount: HTMLSpanElement | null;
let nvidiaPreferredModal: HTMLDivElement | null;
let nvidiaPreferredModalContainer: HTMLDivElement | null;
let btnNvidiaPreferredModalCancel: HTMLButtonElement | null;
let btnNvidiaPreferredModalClose: HTMLButtonElement | null;
let btnNvidiaPreferredSave: HTMLButtonElement | null;
let btnNvidiaPreferredSrcLocal: HTMLButtonElement | null;
let btnNvidiaPreferredSrcRemote: HTMLButtonElement | null;
let nvidiaPreferredCurrentSource: 'local' | 'remote' = 'local';
let btnNvidiaPreferredMoveLeft: HTMLButtonElement | null;
let btnNvidiaPreferredMoveRight: HTMLButtonElement | null;
let btnNvidiaPreferredMoveAllLeft: HTMLButtonElement | null;
let btnNvidiaPreferredMoveAllRight: HTMLButtonElement | null;
let btnNvidiaPreferredFetch: HTMLButtonElement | null;
let iconNvidiaPreferredFetch: HTMLSpanElement | null;
let inputNvidiaPreferredSearchLeft: HTMLInputElement | null;
let inputNvidiaPreferredSearchRight: HTMLInputElement | null;
let chkNvidiaPreferredSelectAllLeft: HTMLInputElement | null;
let chkNvidiaPreferredSelectAllRight: HTMLInputElement | null;
let nvidiaPreferredModelsListLeft: HTMLDivElement | null;
let nvidiaPreferredModelsListRight: HTMLDivElement | null;
let nvidiaPreferredEmptyLeft: HTMLDivElement | null;
let nvidiaPreferredEmptyRight: HTMLDivElement | null;
let lblNvidiaPreferredCount: HTMLSpanElement | null;
let lblNvidiaPreferredSource: HTMLDivElement | null;
let lblNvidiaPreferredVisibleLeft: HTMLSpanElement | null;
let lblNvidiaPreferredVisibleRight: HTMLSpanElement | null;
let nvidiaPreferredError: HTMLDivElement | null;
let nvidiaPreferredSourceRespEventBound = false;
// 穿梭框内存态:跨 Modal 打开/远端刷新保留。左列=已选(给客户端的清单),右列=上游候选全集。
let nvidiaPreferredLeftIDs: string[] = [];
let nvidiaPreferredRightIDs: string[] = [];
// diff 标记集合(跨 Modal 打开保留,随下次远端刷新整体覆盖):
//   addedSetLower —— 本轮「新增」(本次远端 − 上次快照)id 的小写集合,标在右列候选行;
//   staleSetLower —— 「已选但远端已删除」id 的小写集合,标在左列已选行;
//   lastRemoteFull —— 最近一次成功拉取的远端全集(用作「失效」比对基准,拉取失败/未拉时为 null → 不下失效判定)。
// 三者由 fetchAndRenderNvidiaPreferredModels 在远端返回后更新,render 路径读取后给行挂徽章。
let addedSetLower: Set<string> = new Set();
let staleSetLower: Set<string> = new Set();
let lastRemoteFull: string[] | null = null;

// 绑定 DOM 句柄（由 accountsController.initAccountsEvents 委托调用，避免命名冲突）
function initNvidiaPreferredShuttleHandles(): void {
    btnNvidiaPreferredModels = document.getElementById('btnNvidiaPreferredModels') as HTMLButtonElement | null;
    badgeNvidiaPreferredCount = document.getElementById('badgeNvidiaPreferredCount') as HTMLSpanElement | null;
    nvidiaPreferredModal = document.getElementById('nvidiaPreferredModelsModal') as HTMLDivElement | null;
    nvidiaPreferredModalContainer = document.getElementById('nvidiaPreferredModelsModalContainer') as HTMLDivElement | null;
    btnNvidiaPreferredModalCancel = document.getElementById('btnNvidiaPreferredModalCancel') as HTMLButtonElement | null;
    btnNvidiaPreferredModalClose = document.getElementById('btnNvidiaPreferredModalClose') as HTMLButtonElement | null;
    btnNvidiaPreferredSave = document.getElementById('btnNvidiaPreferredSave') as HTMLButtonElement | null;
    btnNvidiaPreferredSrcLocal = document.getElementById('btnNvidiaPreferredSrcLocal') as HTMLButtonElement | null;
    btnNvidiaPreferredSrcRemote = document.getElementById('btnNvidiaPreferredSrcRemote') as HTMLButtonElement | null;
    btnNvidiaPreferredMoveLeft = document.getElementById('btnNvidiaPreferredMoveLeft') as HTMLButtonElement | null;
    btnNvidiaPreferredMoveRight = document.getElementById('btnNvidiaPreferredMoveRight') as HTMLButtonElement | null;
    btnNvidiaPreferredMoveAllLeft = document.getElementById('btnNvidiaPreferredMoveAllLeft') as HTMLButtonElement | null;
    btnNvidiaPreferredMoveAllRight = document.getElementById('btnNvidiaPreferredMoveAllRight') as HTMLButtonElement | null;
    btnNvidiaPreferredFetch = document.getElementById('btnNvidiaPreferredFetch') as HTMLButtonElement | null;
    iconNvidiaPreferredFetch = document.getElementById('iconNvidiaPreferredFetch') as HTMLSpanElement | null;
    inputNvidiaPreferredSearchLeft = document.getElementById('inputNvidiaPreferredSearchLeft') as HTMLInputElement | null;
    inputNvidiaPreferredSearchRight = document.getElementById('inputNvidiaPreferredSearchRight') as HTMLInputElement | null;
    chkNvidiaPreferredSelectAllLeft = document.getElementById('chkNvidiaPreferredSelectAllLeft') as HTMLInputElement | null;
    chkNvidiaPreferredSelectAllRight = document.getElementById('chkNvidiaPreferredSelectAllRight') as HTMLInputElement | null;
    nvidiaPreferredModelsListLeft = document.getElementById('nvidiaPreferredModelsListLeft') as HTMLDivElement | null;
    nvidiaPreferredModelsListRight = document.getElementById('nvidiaPreferredModelsListRight') as HTMLDivElement | null;
    nvidiaPreferredEmptyLeft = document.getElementById('nvidiaPreferredEmptyLeft') as HTMLDivElement | null;
    nvidiaPreferredEmptyRight = document.getElementById('nvidiaPreferredEmptyRight') as HTMLDivElement | null;
    lblNvidiaPreferredCount = document.getElementById('lblNvidiaPreferredCount') as HTMLSpanElement | null;
    lblNvidiaPreferredSource = document.getElementById('lblNvidiaPreferredSource') as HTMLDivElement | null;
    lblNvidiaPreferredVisibleLeft = document.getElementById('lblNvidiaPreferredVisibleLeft') as HTMLSpanElement | null;
    lblNvidiaPreferredVisibleRight = document.getElementById('lblNvidiaPreferredVisibleRight') as HTMLSpanElement | null;
    nvidiaPreferredError = document.getElementById('nvidiaPreferredError') as HTMLDivElement | null;
}

// 绑定穿梭框按钮事件 + 后端 res 事件订阅（仅绑定一次）
function initNvidiaPreferredShuttleEvents(): void {
    // NVIDIA 全局专属模型清单 Modal:事件绑定 + 来源事件订阅(仅绑定一次)
    if (!nvidiaPreferredSourceRespEventBound) {
        ipcRenderer.on('settings:nvidia-preferred-models-res', (_e: any, res: any) => {
            if (res && res.success) {
                updateNvidiaPreferredBadge(res.count ?? 0);
            }
        });
        nvidiaPreferredSourceRespEventBound = true;
    }
    if (btnNvidiaPreferredModels) btnNvidiaPreferredModels.addEventListener('click', openNvidiaPreferredModelsModal);
    if (btnNvidiaPreferredModalClose) btnNvidiaPreferredModalClose.addEventListener('click', closeNvidiaPreferredModelsModal);
    if (btnNvidiaPreferredModalCancel) btnNvidiaPreferredModalCancel.addEventListener('click', closeNvidiaPreferredModelsModal);
    if (btnNvidiaPreferredSrcLocal) btnNvidiaPreferredSrcLocal.addEventListener('click', () => switchNvidiaPreferredSource('local'));
    if (btnNvidiaPreferredSrcRemote) btnNvidiaPreferredSrcRemote.addEventListener('click', () => switchNvidiaPreferredSource('remote'));
    if (btnNvidiaPreferredFetch) btnNvidiaPreferredFetch.addEventListener('click', () => { void fetchAndRenderNvidiaPreferredModels(false, 'remote'); });
    if (btnNvidiaPreferredSave) btnNvidiaPreferredSave.addEventListener('click', submitNvidiaPreferredModels);
    if (btnNvidiaPreferredMoveLeft) btnNvidiaPreferredMoveLeft.addEventListener('click', moveNvidiaPreferredSelectedToLeft);
    if (btnNvidiaPreferredMoveRight) btnNvidiaPreferredMoveRight.addEventListener('click', moveNvidiaPreferredSelectedToRight);
    if (btnNvidiaPreferredMoveAllLeft) btnNvidiaPreferredMoveAllLeft.addEventListener('click', moveAllNvidiaPreferredToLeft);
    if (btnNvidiaPreferredMoveAllRight) btnNvidiaPreferredMoveAllRight.addEventListener('click', moveAllNvidiaPreferredToRight);
    if (inputNvidiaPreferredSearchLeft) inputNvidiaPreferredSearchLeft.addEventListener('input', applyNvidiaPreferredSearchLeft);
    if (inputNvidiaPreferredSearchRight) inputNvidiaPreferredSearchRight.addEventListener('input', applyNvidiaPreferredSearchRight);
    if (chkNvidiaPreferredSelectAllLeft) chkNvidiaPreferredSelectAllLeft.addEventListener('change', toggleNvidiaPreferredSelectAllLeft);
    if (chkNvidiaPreferredSelectAllRight) chkNvidiaPreferredSelectAllRight.addEventListener('change', toggleNvidiaPreferredSelectAllRight);
    // 「移除失效已选」按钮:从已选清单移除所有「远端已删除」(stale)的行。仅在有失效标记时可点。
    const btnRemoveStale = document.getElementById('btnNvidiaPreferredRemoveStale');
    if (btnRemoveStale) btnRemoveStale.addEventListener('click', removeStaleNvidiaPreferredSelected);
}

// 穿梭框 Modal 统一初始化套口：DOM 句柄赋值、事件绑定、首屏徽标回读。
export function initNvidiaPreferredShuttle(): void {
    initNvidiaPreferredShuttleHandles();
    initNvidiaPreferredShuttleEvents();
    // 首屏静默回读已保存的 NVIDIA 专属模型清单,刷新入口徽标计数(不打开 Modal)
    // isOpening=true、force=undefined → 后端"cache 优先,空则回退远端";命中本地清单即刷徽标
    void fetchAndRenderNvidiaPreferredModels(true, undefined);
}

// 由 accountsController.updateViewTabUI 委托调用：按当前视图 Tab 控制入口按钮显隐。
// 仅 NVIDIA Tab 显示穿梭框入口；其余 Tab 隐藏。独立于 updateViewTabUI 的内联显隐,避免跨簇共享 DOM 句柄。
export function setNvidiaPreferredModelsButtonVisible(visible: boolean): void {
    if (btnNvidiaPreferredModels) {
        if (visible) btnNvidiaPreferredModels.classList.remove('hidden');
        else btnNvidiaPreferredModels.classList.add('hidden');
    }
}

// 实时把模块级缓存的"已保存计数"刷到入口徽标 + Modal 顶部计数
function updateNvidiaPreferredBadge(count: number): void {
    if (badgeNvidiaPreferredCount) badgeNvidiaPreferredCount.textContent = String(count);
    if (lblNvidiaPreferredCount) lblNvidiaPreferredCount.textContent = String(count);
}

function showNvidiaPreferredError(msg: string | null): void {
    if (!nvidiaPreferredError) return;
    if (msg) {
        nvidiaPreferredError.textContent = msg;
        nvidiaPreferredError.classList.remove('hidden');
    } else {
        nvidiaPreferredError.textContent = '';
        nvidiaPreferredError.classList.add('hidden');
    }
}

// 收集某列当前勾选行的 model id(保持 DOM 顺序)
function collectNvidiaPreferredCheckedIn(list: HTMLDivElement | null): string[] {
    if (!list) return [];
    const checks = list.querySelectorAll<HTMLInputElement>('input[data-model-id]:checked');
    return Array.from(checks).map(chk => chk.dataset.modelId || '').filter(Boolean);
}

// 收集左列全部行(已选清单=保存时落库的对象),不依赖勾选状态
function collectNvidiaPreferredLeftIDs(): string[] {
    if (!nvidiaPreferredModelsListLeft) return nvidiaPreferredLeftIDs.slice();
    const rows = nvidiaPreferredModelsListLeft.querySelectorAll<HTMLLabelElement>('label[data-model-id]');
    return Array.from(rows).map(r => r.dataset.modelId || '').filter(Boolean);
}

function openNvidiaPreferredModelsModal(): void {
    if (!nvidiaPreferredModal || !nvidiaPreferredModalContainer) return;
    // 每次打开重置搜索/全选/错误
    if (inputNvidiaPreferredSearchLeft) inputNvidiaPreferredSearchLeft.value = '';
    if (inputNvidiaPreferredSearchRight) inputNvidiaPreferredSearchRight.value = '';
    if (chkNvidiaPreferredSelectAllLeft) chkNvidiaPreferredSelectAllLeft.checked = false;
    if (chkNvidiaPreferredSelectAllRight) chkNvidiaPreferredSelectAllRight.checked = false;
    showNvidiaPreferredError(null);
    if (lblNvidiaPreferredSource) {
        lblNvidiaPreferredSource.textContent = '';
        lblNvidiaPreferredSource.classList.add('hidden');
    }
    // 默认来源=本地清单(空则后端自动回退远端)
    nvidiaPreferredCurrentSource = 'local';
    applySourceHighlightNvidiaPreferred();
    void fetchAndRenderNvidiaPreferredModels(true, undefined);
    nvidiaPreferredModal.classList.remove('opacity-0', 'pointer-events-none');
    nvidiaPreferredModalContainer.classList.remove('scale-95');
    nvidiaPreferredModalContainer.classList.add('scale-100');
}

function closeNvidiaPreferredModelsModal(): void {
    if (!nvidiaPreferredModal || !nvidiaPreferredModalContainer) return;
    nvidiaPreferredModalContainer.classList.remove('scale-100');
    nvidiaPreferredModalContainer.classList.add('scale-95');
    nvidiaPreferredModal.classList.add('opacity-0', 'pointer-events-none');
}

// 来源切换:local=本地清单(空则后端自动回退远端);remote=强制跳过 cache 打上游
function switchNvidiaPreferredSource(src: 'local' | 'remote'): void {
    if (src === nvidiaPreferredCurrentSource) return;
    nvidiaPreferredCurrentSource = src;
    applySourceHighlightNvidiaPreferred();
    void fetchAndRenderNvidiaPreferredModels(false, src === 'remote' ? 'remote' : undefined);
}

function applySourceHighlightNvidiaPreferred(): void {
    const activeCls = ['bg-primary', 'text-white'];
    const idleCls = ['text-primary', 'bg-primary/10', 'hover:bg-primary/20'];
    const setActive = (btn: HTMLButtonElement | null, active: boolean) => {
        if (!btn) return;
        if (active) { btn.classList.add(...activeCls); btn.classList.remove(...idleCls); }
        else { btn.classList.remove(...activeCls); btn.classList.add(...idleCls); }
    };
    setActive(btnNvidiaPreferredSrcLocal, nvidiaPreferredCurrentSource === 'local');
    setActive(btnNvidiaPreferredSrcRemote, nvidiaPreferredCurrentSource === 'remote');
}

// isOpening=true:首次进入 Modal,读本地清单(空则后端自动回退远端),作为左列已选回显
// force='remote':强制跳过 cache 打上游,结果进右列候选(左列已选不重置)
// force=undefined / 'local':cache 优先,空则回退远端;cache 命中→左列回显、右列保持现状
async function fetchAndRenderNvidiaPreferredModels(isOpening: boolean, force?: 'remote'): Promise<void> {
    if (!btnNvidiaPreferredFetch) return;
    showNvidiaPreferredError(null);

    const origIcon = iconNvidiaPreferredFetch ? iconNvidiaPreferredFetch.textContent : '';
    const origDisabled = btnNvidiaPreferredFetch.disabled;
    btnNvidiaPreferredFetch.disabled = true;
    if (iconNvidiaPreferredFetch) iconNvidiaPreferredFetch.textContent = 'progress_activity';
    if (iconNvidiaPreferredFetch) iconNvidiaPreferredFetch.classList.add('animate-spin');

    try {
        const res = await ipcRenderer.invoke('settings:get-nvidia-preferred-models', force ? { force } : {});
        if (!res || !res.success) {
            const errMsg = (res && res.error) ? res.error : '获取模型失败';
            showNvidiaPreferredError(errMsg);
            return;
        }
        const models: string[] = Array.isArray(res.models) ? res.models : [];
        const source: string = res.source || (force === 'remote' ? 'remote' : 'cache');
        // 后端新增 diff 字段(向后兼容:缺失即空,不破坏旧后端)。
        const snapshot: string[] = Array.isArray(res.snapshot) ? res.snapshot : [];
        const addedFromBackend: string[] = Array.isArray(res.added) ? res.added : [];

        if (lblNvidiaPreferredSource) {
            const dict = i18n[state.currentLanguage] || i18n.zh || {};
            const dl = (window as any).__nvidiaPreferredDict || {};
            lblNvidiaPreferredSource.textContent = source === 'cache'
                ? (dl.nvidiaPreferredModelsSourceCache || dict.nvidiaPreferredModelsSourceCache || '来源:已保存清单')
                : (dl.nvidiaPreferredModelsSourceRemote || dict.nvidiaPreferredModelsSourceRemote || '来源:远端实时');
            lblNvidiaPreferredSource.classList.remove('hidden');
        }

        if (source === 'cache') {
            // 本地清单命中:左列=清单本身(已选回显),右列保持现状(没拉上游)。
            // cache 分支无本轮新增(added=空),但保留历史 added 标记以复原「上次新增」观感:
            // 后端 cache 分支返回上次快照,据其与本地清单关系重算失效(若曾拉过远端)。
            nvidiaPreferredLeftIDs = models.slice();
            // 失效比对需要「最近一次成功远端全集」,cache 命中时用 snapshot 作比基准(若有)。
            // 首次/clean(无快照)时 snapshot 为空 → 不下失效判定,符合「未真相核对不下红标」原则。
            lastRemoteFull = snapshot.length > 0 ? snapshot.slice() : lastRemoteFull;
            staleSetLower = lastRemoteFull ? computeStaleLower(nvidiaPreferredLeftIDs, lastRemoteFull) : new Set();
            renderNvidiaPreferredListLeft(nvidiaPreferredLeftIDs);
            renderNvidiaPreferredListRight(nvidiaPreferredRightIDs);
            // 本地清单命中即把"已保存计数"刷到入口徽标 + Modal 顶部计数
            // 避免长时间停留在 HTML 写死的初值 0、与磁盘里实际保存的清单不同步
            updateNvidiaPreferredBadge(models.length);
        } else {
            // 远端全量候选:进右列,剔除已在左列的 id;左列已选不重置。
            // 更新本轮 diff:added 取后端算好的(本次−上次快照);stale 用本次全集现场对比已选。
            lastRemoteFull = models.slice();
            addedSetLower = new Set(addedFromBackend.map((s: string) => (s || '').trim().toLowerCase()).filter(Boolean));
            staleSetLower = computeStaleLower(nvidiaPreferredLeftIDs, lastRemoteFull);
            nvidiaPreferredRightIDs = models.filter(m => !nvidiaPreferredLeftIDs.includes(m));
            renderNvidiaPreferredListRight(nvidiaPreferredRightIDs);
            renderNvidiaPreferredListLeft(nvidiaPreferredLeftIDs);
        }
        refreshNvidiaPreferredDiffSummary();
    } catch (err: any) {
        showNvidiaPreferredError(err?.message || '获取模型失败');
    } finally {
        btnNvidiaPreferredFetch.disabled = origDisabled;
        if (iconNvidiaPreferredFetch) {
            iconNvidiaPreferredFetch.textContent = origIcon || 'cloud_download';
            iconNvidiaPreferredFetch.classList.remove('animate-spin');
        }
    }
}

// refreshNvidiaPreferredSourceI18n:语言切换时由 dashboard.setLanguage 调用,
// 即时重刷来源徽标文案(若弹窗已打开且徽标非空)。弹窗未打开或徽标未显示时无副作用。
// 修历史 __nvidiaPreferredDict 失联导致徽标恒冒中文兜底的盲区。
export function refreshNvidiaPreferredSourceI18n(): void {
    if (!lblNvidiaPreferredSource) return;
    if (lblNvidiaPreferredSource.classList.contains('hidden')) return;
    const dict = i18n[state.currentLanguage] || i18n.zh || {};
    const dl = (window as any).__nvidiaPreferredDict || {};
    lblNvidiaPreferredSource.textContent = nvidiaPreferredCurrentSource === 'local'
        ? (dl.nvidiaPreferredModelsSourceCache || dict.nvidiaPreferredModelsSourceCache || '来源:已保存清单')
        : (dl.nvidiaPreferredModelsSourceRemote || dict.nvidiaPreferredModelsSourceRemote || '来源:远端实时');
}

// 渲染左列(已选清单);checkedSet 为空=不勾选(左列勾选仅供"移出"用,默认不勾)
function renderNvidiaPreferredListLeft(models: string[]): void {
    if (!nvidiaPreferredModelsListLeft) return;
    nvidiaPreferredModelsListLeft.innerHTML = '';
    if (models.length === 0) {
        if (nvidiaPreferredEmptyLeft) nvidiaPreferredEmptyLeft.classList.remove('hidden');
        if (lblNvidiaPreferredVisibleLeft) lblNvidiaPreferredVisibleLeft.textContent = '0 / 0';
        if (chkNvidiaPreferredSelectAllLeft) chkNvidiaPreferredSelectAllLeft.checked = false;
        refreshNvidiaPreferredDiffSummary();
        return;
    }
    if (nvidiaPreferredEmptyLeft) nvidiaPreferredEmptyLeft.classList.add('hidden');
    for (const m of models) {
        // 左列只标「失效」(远端已删除),不标新增;新增标在右列候选。
        const badgeKind: DiffKind | null = staleSetLower.has(m.trim().toLowerCase()) ? 'stale' : null;
        nvidiaPreferredModelsListLeft.appendChild(buildNvidiaPreferredRow(m, false, badgeKind));
    }
    applyNvidiaPreferredSearchLeft();
    syncNvidiaPreferredSelectAllLeft();
    refreshNvidiaPreferredDiffSummary();
}

// 渲染右列(上游候选),checkedSet 为空=不勾选
function renderNvidiaPreferredListRight(models: string[]): void {
    if (!nvidiaPreferredModelsListRight) return;
    nvidiaPreferredModelsListRight.innerHTML = '';
    if (models.length === 0) {
        if (nvidiaPreferredEmptyRight) nvidiaPreferredEmptyRight.classList.remove('hidden');
        if (lblNvidiaPreferredVisibleRight) lblNvidiaPreferredVisibleRight.textContent = '0 / 0';
        if (chkNvidiaPreferredSelectAllRight) chkNvidiaPreferredSelectAllRight.checked = false;
        return;
    }
    if (nvidiaPreferredEmptyRight) nvidiaPreferredEmptyRight.classList.add('hidden');
    for (const m of models) {
        // 右列标「新增」(本次远端新增);失效标在左列已选。
        const badgeKind: DiffKind | null = addedSetLower.has(m.trim().toLowerCase()) ? 'new' : null;
        nvidiaPreferredModelsListRight.appendChild(buildNvidiaPreferredRow(m, false, badgeKind));
    }
    applyNvidiaPreferredSearchRight();
    syncNvidiaPreferredSelectAllRight();
}

// refreshNvidiaPreferredDiffSummary 刷新 Modal 顶部「新增 N / 失效 M」摘要区。
// 避免空集时残留文案;据当前 addedSetLower/staleSetLower 实时算数,跨 Modal / re-render 安全。
function refreshNvidiaPreferredDiffSummary(): void {
    const node = document.getElementById('nvidiaPreferredDiffSummary');
    const newCount = addedSetLower.size;
    const staleCount = staleSetLower.size;
    if (node) {
        if (newCount === 0 && staleCount === 0) {
            node.textContent = '';
            node.classList.add('hidden');
        } else {
            const parts: string[] = [];
            if (newCount > 0) parts.push(diffSummaryText('new', newCount));
            if (staleCount > 0) parts.push(diffSummaryText('stale', staleCount));
            node.textContent = parts.join(' · ');
            node.classList.remove('hidden');
        }
    }
    // 同步「移除失效已选」按钮可点性:仅当存在失效标记(staleCount>0)时启用,否则禁用。
    const btnRemoveStale = document.getElementById('btnNvidiaPreferredRemoveStale') as HTMLButtonElement | null;
    if (btnRemoveStale) {
        btnRemoveStale.disabled = staleCount === 0;
        const dl = (window as any).__nvidiaPreferredDict || {};
        const dict = i18n[state.currentLanguage] || i18n.zh || {};
        if (staleCount > 0) {
            const tpl = (dl.nvidiaPreferredRemoveStaleCountBtn || dict.nvidiaPreferredRemoveStaleCountBtn || '移除失效已选 ({n})');
            btnRemoveStale.textContent = tpl.replace('{n}', String(staleCount));
        } else {
            btnRemoveStale.textContent = (dl.nvidiaPreferredRemoveStale || dict.nvidiaPreferredRemoveStale || '移除失效已选');
        }
    }
}

// 构造一行:label>input[data-model-id]+span.mono;change 事件同步该列全选框
// badgeKind 非 null 时在行尾追加一个 diff 徽章(新增=绿/失效=红),便于用户一眼定位本轮变化项。
function buildNvidiaPreferredRow(modelId: string, checked: boolean, badgeKind: DiffKind | null = null): HTMLLabelElement {
    const row = document.createElement('label');
    row.dataset.modelId = modelId;
    row.className = 'flex items-center gap-2 px-3 py-1.5 cursor-pointer hover:bg-slate-50/60 dark:hover:bg-white/5 transition-colors select-none';

    const chk = document.createElement('input');
    chk.type = 'checkbox';
    chk.dataset.modelId = modelId;
    chk.className = 'w-3.5 h-3.5 rounded border-outline-variant/40 dark:border-white/20 text-primary focus:ring-primary cursor-pointer shrink-0';
    chk.checked = checked;

    const span = document.createElement('span');
    span.className = 'flex-1 min-w-0 text-[12px] font-mono text-on-surface dark:text-white truncate';
    span.textContent = modelId;
    span.title = modelId;

    row.appendChild(chk);
    row.appendChild(span);
    if (badgeKind) {
        // 失效已选行用红字弱视觉效果提醒(不阻断勾选/移出);新增候选用绿徽章。
        if (badgeKind === 'stale') span.classList.add('line-through', 'text-red-500', 'dark:text-red-400', 'opacity-70');
        row.appendChild(createDiffBadge(badgeKind));
    }
    return row;
}

// 移入已选:右列勾选项 → 左列(保留左列已有顺序,新项追加),从右列移除
function moveNvidiaPreferredSelectedToLeft(): void {
    if (!nvidiaPreferredModelsListRight) return;
    const chosen = collectNvidiaPreferredCheckedIn(nvidiaPreferredModelsListRight);
    if (chosen.length === 0) return;
    for (const id of chosen) {
        if (!nvidiaPreferredLeftIDs.includes(id)) nvidiaPreferredLeftIDs.push(id);
        nvidiaPreferredRightIDs = nvidiaPreferredRightIDs.filter(x => x !== id);
    }
    renderNvidiaPreferredListLeft(nvidiaPreferredLeftIDs);
    renderNvidiaPreferredListRight(nvidiaPreferredRightIDs);
}

// 移出已选:左列勾选项 → 右列,从左列移除
function moveNvidiaPreferredSelectedToRight(): void {
    if (!nvidiaPreferredModelsListLeft) return;
    const chosen = collectNvidiaPreferredCheckedIn(nvidiaPreferredModelsListLeft);
    if (chosen.length === 0) return;
    for (const id of chosen) {
        if (!nvidiaPreferredRightIDs.includes(id)) nvidiaPreferredRightIDs.push(id);
        nvidiaPreferredLeftIDs = nvidiaPreferredLeftIDs.filter(x => x !== id);
    }
    renderNvidiaPreferredListLeft(nvidiaPreferredLeftIDs);
    renderNvidiaPreferredListRight(nvidiaPreferredRightIDs);
}

function moveAllNvidiaPreferredToLeft(): void {
    if (nvidiaPreferredRightIDs.length === 0) return;
    for (const id of nvidiaPreferredRightIDs) {
        if (!nvidiaPreferredLeftIDs.includes(id)) nvidiaPreferredLeftIDs.push(id);
    }
    nvidiaPreferredRightIDs = [];
    renderNvidiaPreferredListLeft(nvidiaPreferredLeftIDs);
    renderNvidiaPreferredListRight(nvidiaPreferredRightIDs);
}

function moveAllNvidiaPreferredToRight(): void {
    if (nvidiaPreferredLeftIDs.length === 0) return;
    for (const id of nvidiaPreferredLeftIDs) {
        if (!nvidiaPreferredRightIDs.includes(id)) nvidiaPreferredRightIDs.push(id);
    }
    nvidiaPreferredLeftIDs = [];
    renderNvidiaPreferredListLeft(nvidiaPreferredLeftIDs);
    renderNvidiaPreferredListRight(nvidiaPreferredRightIDs);
}

// 左列搜索过滤
function applyNvidiaPreferredSearchLeft(): void {
    applyNvidiaPreferredSearchOne(
        nvidiaPreferredModelsListLeft,
        inputNvidiaPreferredSearchLeft,
        lblNvidiaPreferredVisibleLeft,
        chkNvidiaPreferredSelectAllLeft,
    );
}

// 右列搜索过滤
function applyNvidiaPreferredSearchRight(): void {
    applyNvidiaPreferredSearchOne(
        nvidiaPreferredModelsListRight,
        inputNvidiaPreferredSearchRight,
        lblNvidiaPreferredVisibleRight,
        chkNvidiaPreferredSelectAllRight,
    );
}

// 单列搜索过滤:隐藏不匹配行,更新可见计数与全选框状态(基于可见行)
function applyNvidiaPreferredSearchOne(
    list: HTMLDivElement | null,
    input: HTMLInputElement | null,
    visibleLbl: HTMLSpanElement | null,
    selectAllChk: HTMLInputElement | null,
): void {
    if (!list) return;
    const kw = (input?.value || '').trim().toLowerCase();
    const rows = list.querySelectorAll<HTMLLabelElement>('label');
    let visible = 0;
    let visibleChecked = 0;
    rows.forEach(row => {
        const chk = row.querySelector<HTMLInputElement>('input[type="checkbox"]');
        const id = row.dataset.modelId || '';
        const matched = !kw || id.toLowerCase().includes(kw);
        row.classList.toggle('hidden', !matched);
        if (matched) {
            visible++;
            if (chk?.checked) visibleChecked++;
        }
    });
    if (visibleLbl) visibleLbl.textContent = `${visible} / ${rows.length}`;
    if (selectAllChk) {
        selectAllChk.checked = visible > 0 && (visibleChecked === visible);
    }
}

// 左列全选框同步:基于可见行的勾选状态反向更新全选框
function syncNvidiaPreferredSelectAllLeft(): void {
    syncNvidiaPreferredSelectAllOne(nvidiaPreferredModelsListLeft, chkNvidiaPreferredSelectAllLeft, lblNvidiaPreferredVisibleLeft, inputNvidiaPreferredSearchLeft);
}

function syncNvidiaPreferredSelectAllRight(): void {
    syncNvidiaPreferredSelectAllOne(nvidiaPreferredModelsListRight, chkNvidiaPreferredSelectAllRight, lblNvidiaPreferredVisibleRight, inputNvidiaPreferredSearchRight);
}

function syncNvidiaPreferredSelectAllOne(
    list: HTMLDivElement | null,
    selectAllChk: HTMLInputElement | null,
    visibleLbl: HTMLSpanElement | null,
    input: HTMLInputElement | null,
): void {
    if (!list || !selectAllChk) return;
    const kw = (input?.value || '').trim().toLowerCase();
    const rows = list.querySelectorAll<HTMLLabelElement>('label:not(.hidden)');
    if (rows.length === 0) {
        selectAllChk.checked = false;
    } else {
        let checkedCount = 0;
        rows.forEach(row => {
            const chk = row.querySelector<HTMLInputElement>('input[type="checkbox"]');
            if (chk?.checked) checkedCount++;
        });
        selectAllChk.checked = (checkedCount === rows.length);
    }
    if (visibleLbl) {
        const all = list.querySelectorAll<HTMLLabelElement>('label').length;
        let visible = 0;
        list.querySelectorAll<HTMLLabelElement>('label').forEach(row => {
            const id = row.dataset.modelId || '';
            if (!kw || id.toLowerCase().includes(kw)) visible++;
        });
        visibleLbl.textContent = `${visible} / ${all}`;
    }
}

// 左列全选/取消:仅作用于当前可见行
function toggleNvidiaPreferredSelectAllLeft(): void {
    if (!nvidiaPreferredModelsListLeft || !chkNvidiaPreferredSelectAllLeft) return;
    const target = chkNvidiaPreferredSelectAllLeft.checked;
    toggleVisibleRowsNvidiaPreferred(nvidiaPreferredModelsListLeft, target);
    if (inputNvidiaPreferredSearchLeft) applyNvidiaPreferredSearchLeft();
}

function toggleNvidiaPreferredSelectAllRight(): void {
    if (!nvidiaPreferredModelsListRight || !chkNvidiaPreferredSelectAllRight) return;
    const target = chkNvidiaPreferredSelectAllRight.checked;
    toggleVisibleRowsNvidiaPreferred(nvidiaPreferredModelsListRight, target);
    if (inputNvidiaPreferredSearchRight) applyNvidiaPreferredSearchRight();
}

function toggleVisibleRowsNvidiaPreferred(list: HTMLDivElement, targetChecked: boolean): void {
    list.querySelectorAll<HTMLLabelElement>('label:not(.hidden)').forEach(row => {
        const chk = row.querySelector<HTMLInputElement>('input[type="checkbox"]');
        if (chk) chk.checked = targetChecked;
    });
}

// 保存清单:收集左列全部行 → settings:set-nvidia-preferred-models → 关闭
function submitNvidiaPreferredModels(): void {
    if (!btnNvidiaPreferredSave) return;
    const selected = collectNvidiaPreferredLeftIDs();
    showNvidiaPreferredError(null);
    btnNvidiaPreferredSave.disabled = true;
    try {
        ipcRenderer.send('settings:set-nvidia-preferred-models', selected);
        updateNvidiaPreferredBadge(selected.length);
        // 后端会 emit settings:nvidia-preferred-models-res 事件刷新徽标;乐观关闭 Modal
        closeNvidiaPreferredModelsModal();
    } finally {
        btnNvidiaPreferredSave.disabled = false;
    }
}

// removeStaleNvidiaPreferredSelected:从左列已选一次性移除所有「远端已删除」(staleSetLower 命中)的行。
// 仅在 staleSetLower 非空(即曾成功拉取远端并发现失效)时可点,且用户确认后再动。
// 移除后失效标记自然清空,新增标记不变;未保存不落盘(走「保存清单」按钮,与既有移出一致挽回路径)。
function removeStaleNvidiaPreferredSelected(): void {
    if (staleSetLower.size === 0) return;
    const dict = i18n[state.currentLanguage] || i18n.zh || {};
    const dl = (window as any).__nvidiaPreferredDict || {};
    const staleIds = nvidiaPreferredLeftIDs.filter(id => staleSetLower.has((id || '').trim().toLowerCase()));
    if (staleIds.length === 0) return;
    const tpl = (dl.nvidiaPreferredRemoveStaleCountBtn || dict.nvidiaPreferredRemoveStaleCountBtn || '移除失效已选 ({n})');
    const promptMsg = tpl.replace('{n}', String(staleIds.length)) + '\n' + staleIds.join('\n');
    // eslint-disable-next-line no-alert
    if (!window.confirm(promptMsg)) return;
    const removeLower = new Set(staleIds.map(s => s.trim().toLowerCase()));
    nvidiaPreferredLeftIDs = nvidiaPreferredLeftIDs.filter(id => !removeLower.has((id || '').trim().toLowerCase()));
    // 移除后失效标记自然清空(这些 id 已不在已选);移出的 id 回到右列候选(若远端仍存在则在右列可见,
    // 若远端已删则两边都没有——这正是「失效」的语义,不必特殊处理)。
    for (const id of staleIds) {
        if (!nvidiaPreferredRightIDs.includes(id) && (lastRemoteFull?.includes(id) ?? false)) {
            nvidiaPreferredRightIDs.push(id);
        }
    }
    staleSetLower = new Set();
    renderNvidiaPreferredListLeft(nvidiaPreferredLeftIDs);
    renderNvidiaPreferredListRight(nvidiaPreferredRightIDs);
}
