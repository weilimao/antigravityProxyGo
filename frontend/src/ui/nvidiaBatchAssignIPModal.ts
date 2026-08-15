import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';
import i18n from '../shared/i18n';

// 预设网段数据结构
interface ResidentialSubnet {
    id: string;
    cidr: string;
    isp: string;
    location: string;
    country: string;
    flag: string;
}

let subnetsCache: ResidentialSubnet[] = [];
let selectedSubnetSet: Set<string> = new Set();

let modalEl: HTMLDivElement | null = null;
let containerEl: HTMLDivElement | null = null;
let subnetListContainer: HTMLDivElement | null = null;
let countEl: HTMLSpanElement | null = null;
let btnConfirm: HTMLButtonElement | null = null;

const ACTIVE_CARD_CLASS = ['bg-primary/5', 'border-primary/40', 'dark:bg-primary/10', 'dark:border-primary/50'];
const INACTIVE_CARD_CLASS = ['bg-white', 'dark:bg-white/5', 'border-outline-variant/30', 'opacity-75'];

export async function initNvidiaBatchAssignIPModal(): Promise<void> {
    modalEl = document.getElementById('nvidiaBatchAssignIPModal') as HTMLDivElement | null;
    containerEl = document.getElementById('nvidiaBatchAssignIPModalContainer') as HTMLDivElement | null;
    subnetListContainer = document.getElementById('subnetListContainer') as HTMLDivElement | null;
    countEl = document.getElementById('lblSelectedSubnetCount') as HTMLSpanElement | null;
    btnConfirm = document.getElementById('btnConfirmNvidiaBatchAssignIP') as HTMLButtonElement | null;

    const btnOpen = document.getElementById('btnNvidiaBatchAssignIP');
    const btnClose = document.getElementById('btnCloseNvidiaBatchAssignIPModal');
    const btnCancel = document.getElementById('btnCancelNvidiaBatchAssignIP');
    const btnSelectAll = document.getElementById('btnSelectAllSubnets');
    const btnDeselectAll = document.getElementById('btnDeselectAllSubnets');

    if (btnOpen) {
        btnOpen.addEventListener('click', openNvidiaBatchAssignIPModal);
    }
    if (btnClose) {
        btnClose.addEventListener('click', closeNvidiaBatchAssignIPModal);
    }
    if (btnCancel) {
        btnCancel.addEventListener('click', closeNvidiaBatchAssignIPModal);
    }
    if (btnSelectAll) {
        btnSelectAll.addEventListener('click', handleSelectAll);
    }
    if (btnDeselectAll) {
        btnDeselectAll.addEventListener('click', handleDeselectAll);
    }
    if (btnConfirm) {
        btnConfirm.addEventListener('click', handleConfirmBatchAssign);
    }

    // 使用父容器事件委托统一捕获 checkbox 变更，避免每个卡片绑定监听器
    if (subnetListContainer) {
        subnetListContainer.addEventListener('change', (e: Event) => {
            const target = e.target as HTMLInputElement | null;
            if (!target || target.type !== 'checkbox') return;
            const subnetId = target.dataset.subnetId;
            if (!subnetId) return;

            if (target.checked) {
                selectedSubnetSet.add(subnetId);
            } else {
                selectedSubnetSet.delete(subnetId);
            }
            updateSingleCardStyle(subnetId, target.checked);
            updateSummaryAndButton();
        });
    }
}

export async function openNvidiaBatchAssignIPModal(): Promise<void> {
    ensureElements();
    if (!modalEl || !containerEl) return;

    // 1. 获取网段列表并单次构建 DOM
    if (subnetsCache.length === 0) {
        try {
            const res = await ipcRenderer.invoke('nvidia:get-residential-subnets');
            if (res && res.success && Array.isArray(res.subnets)) {
                subnetsCache = res.subnets;
                buildSubnetCardsOnce();
            }
        } catch (e) {
            console.error('Failed to fetch subnets', e);
        }
    }

    // 默认全选
    selectedSubnetSet = new Set(subnetsCache.map(s => s.id));
    syncAllCardsCheckedAndStyle();
    updateSummaryAndButton();

    // 统计当前 NVIDIA 账号数
    const nvidiaAccs = (state.currentAccountsList || []).filter(a => a.provider === 'nvidia');
    const targetInfoEl = document.getElementById('lblAssignAccountTargetInfo');
    if (targetInfoEl) {
        targetInfoEl.innerText = `号池共有 ${nvidiaAccs.length} 个 NVIDIA 账号`;
    }

    // BaseModal 标准平滑进场动画
    modalEl.classList.remove('opacity-0', 'pointer-events-none', 'hidden');
    containerEl.classList.remove('scale-95');
    containerEl.classList.add('scale-100');
}

export function closeNvidiaBatchAssignIPModal(): void {
    ensureElements();
    if (!modalEl || !containerEl) return;

    // BaseModal 标准平滑退场动画
    containerEl.classList.remove('scale-100');
    containerEl.classList.add('scale-95');
    modalEl.classList.add('opacity-0', 'pointer-events-none');
}

function ensureElements(): void {
    if (!modalEl) modalEl = document.getElementById('nvidiaBatchAssignIPModal') as HTMLDivElement | null;
    if (!containerEl) containerEl = document.getElementById('nvidiaBatchAssignIPModalContainer') as HTMLDivElement | null;
    if (!subnetListContainer) subnetListContainer = document.getElementById('subnetListContainer') as HTMLDivElement | null;
    if (!countEl) countEl = document.getElementById('lblSelectedSubnetCount') as HTMLSpanElement | null;
    if (!btnConfirm) btnConfirm = document.getElementById('btnConfirmNvidiaBatchAssignIP') as HTMLButtonElement | null;
}

// 仅在首次拉取到数据时单次生成 DOM 树，杜绝反复销毁重建
function buildSubnetCardsOnce(): void {
    if (!subnetListContainer) return;
    subnetListContainer.innerHTML = '';

    const fragment = document.createDocumentFragment();

    subnetsCache.forEach(subnet => {
        const card = document.createElement('label');
        card.dataset.subnetCardId = subnet.id;
        card.className = 'flex items-center justify-between p-3 rounded-xl border text-[12px] cursor-pointer transition-all bg-primary/5 border-primary/40 dark:bg-primary/10 dark:border-primary/50';

        card.innerHTML = `
            <div class="flex items-center gap-2.5 overflow-hidden pointer-events-none">
                <input type="checkbox" data-subnet-id="${subnet.id}" checked class="pointer-events-auto w-4 h-4 rounded text-primary focus:ring-primary cursor-pointer" />
                <span class="text-[15px] shrink-0">${subnet.flag}</span>
                <div class="flex flex-col truncate">
                    <span class="font-bold text-on-surface dark:text-white truncate text-[12px]">${subnet.isp}</span>
                    <span class="text-[10.5px] text-outline truncate">${subnet.country} · ${subnet.location}</span>
                </div>
            </div>
            <span class="font-mono text-[11px] font-semibold px-2 py-0.5 rounded-md bg-slate-100 dark:bg-black/40 text-slate-700 dark:text-slate-200 border border-outline-variant/30 dark:border-white/10 flex-shrink-0 ml-2 shadow-2xs">
                ${subnet.cidr}
            </span>
        `;

        fragment.appendChild(card);
    });

    subnetListContainer.appendChild(fragment);
}

// 全选
function handleSelectAll(): void {
    selectedSubnetSet = new Set(subnetsCache.map(s => s.id));
    syncAllCardsCheckedAndStyle();
    updateSummaryAndButton();
}

// 全不选
function handleDeselectAll(): void {
    selectedSubnetSet.clear();
    syncAllCardsCheckedAndStyle();
    updateSummaryAndButton();
}

// 高性能同步所有卡片的 checked 状态和 class，零 DOM 重建
function syncAllCardsCheckedAndStyle(): void {
    if (!subnetListContainer) return;
    const cards = subnetListContainer.querySelectorAll<HTMLLabelElement>('label[data-subnet-card-id]');
    cards.forEach(card => {
        const subnetId = card.dataset.subnetCardId;
        if (!subnetId) return;
        const isChecked = selectedSubnetSet.has(subnetId);
        const checkbox = card.querySelector<HTMLInputElement>(`input[data-subnet-id="${subnetId}"]`);
        if (checkbox) {
            checkbox.checked = isChecked;
        }
        applyCardClass(card, isChecked);
    });
}

// 单个卡片样式更新
function updateSingleCardStyle(subnetId: string, isChecked: boolean): void {
    if (!subnetListContainer) return;
    const card = subnetListContainer.querySelector<HTMLLabelElement>(`label[data-subnet-card-id="${subnetId}"]`);
    if (card) {
        applyCardClass(card, isChecked);
    }
}

function applyCardClass(card: HTMLLabelElement, isChecked: boolean): void {
    if (isChecked) {
        card.classList.remove(...INACTIVE_CARD_CLASS);
        card.classList.add(...ACTIVE_CARD_CLASS);
    } else {
        card.classList.remove(...ACTIVE_CARD_CLASS);
        card.classList.add(...INACTIVE_CARD_CLASS);
    }
}

function updateSummaryAndButton(): void {
    if (countEl) {
        countEl.innerText = String(selectedSubnetSet.size);
    }
    if (btnConfirm) {
        btnConfirm.disabled = selectedSubnetSet.size === 0;
    }
}

async function handleConfirmBatchAssign(): Promise<void> {
    if (selectedSubnetSet.size === 0) return;

    ensureElements();
    const radOverwrite = document.getElementById('radStrategyOverwrite') as HTMLInputElement | null;
    const overwriteAll = radOverwrite ? radOverwrite.checked : true;

    if (btnConfirm) {
        btnConfirm.disabled = true;
        btnConfirm.innerHTML = `<span class="material-symbols-outlined text-[16px] animate-spin">sync</span><span>分配中...</span>`;
    }

    try {
        const res = await ipcRenderer.invoke('nvidia:batch-assign-egress-ip', Array.from(selectedSubnetSet), overwriteAll);

        if (res && res.success) {
            const count = res.updatedCount || 0;
            const dict = i18n[state.currentLanguage] || i18n.zh;
            const msg = (dict.batchAssignSuccess || '成功为 {count} 个 NVIDIA 账号分配独立住宅 IP').replace('{count}', String(count));
            window.alert(msg);
            closeNvidiaBatchAssignIPModal();
        } else {
            window.alert(res?.error || '批量分配失败');
        }
    } catch (e: any) {
        window.alert(e?.message || '请求异常');
    } finally {
        if (btnConfirm) {
            btnConfirm.disabled = false;
            btnConfirm.innerHTML = `<span class="material-symbols-outlined text-[16px]">shuffle</span><span>确定分配</span>`;
        }
    }
}

