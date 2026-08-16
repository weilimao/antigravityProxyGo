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

type AssignMode = 'unique' | 'single';

let currentMode: AssignMode = 'unique';
let subnetsCache: ResidentialSubnet[] = [];
let selectedSubnetSet: Set<string> = new Set();
let selectedSingleSubnetId: string = '';

let modalEl: HTMLDivElement | null = null;
let containerEl: HTMLDivElement | null = null;
let subnetListContainer: HTMLDivElement | null = null;
let singleSubnetListContainer: HTMLDivElement | null = null;
let modeUniqueContainer: HTMLDivElement | null = null;
let modeSingleContainer: HTMLDivElement | null = null;
let inputSingleIP: HTMLInputElement | null = null;
let btnRandomSingleIP: HTMLButtonElement | null = null;
let validationTipEl: HTMLSpanElement | null = null;
let countEl: HTMLSpanElement | null = null;
let btnConfirm: HTMLButtonElement | null = null;

const ACTIVE_CARD_CLASS = ['bg-primary/5', 'border-primary/40', 'dark:bg-primary/10', 'dark:border-primary/50'];
const INACTIVE_CARD_CLASS = ['bg-white', 'dark:bg-white/5', 'border-outline-variant/30', 'opacity-75'];

const SINGLE_ACTIVE_CLASS = ['bg-amber-500/10', 'border-amber-500/50', 'dark:bg-amber-500/15', 'dark:border-amber-500/60', 'ring-1', 'ring-amber-500/30'];
const SINGLE_INACTIVE_CLASS = ['bg-white', 'dark:bg-white/5', 'border-outline-variant/30', 'hover:border-primary/40'];

export async function initNvidiaBatchAssignIPModal(): Promise<void> {
    ensureElements();

    const btnOpen = document.getElementById('btnNvidiaBatchAssignIP');
    const btnClose = document.getElementById('btnCloseNvidiaBatchAssignIPModal');
    const btnCancel = document.getElementById('btnCancelNvidiaBatchAssignIP');
    const btnSelectAll = document.getElementById('btnSelectAllSubnets');
    const btnDeselectAll = document.getElementById('btnDeselectAllSubnets');
    const radUnique = document.getElementById('radAssignModeUnique') as HTMLInputElement | null;
    const radSingle = document.getElementById('radAssignModeSingle') as HTMLInputElement | null;

    if (btnOpen) btnOpen.addEventListener('click', openNvidiaBatchAssignIPModal);
    if (btnClose) btnClose.addEventListener('click', closeNvidiaBatchAssignIPModal);
    if (btnCancel) btnCancel.addEventListener('click', closeNvidiaBatchAssignIPModal);
    if (btnSelectAll) btnSelectAll.addEventListener('click', handleSelectAll);
    if (btnDeselectAll) btnDeselectAll.addEventListener('click', handleDeselectAll);
    if (btnConfirm) btnConfirm.addEventListener('click', handleConfirmBatchAssign);

    if (radUnique) {
        radUnique.addEventListener('change', () => switchAssignMode('unique'));
    }
    if (radSingle) {
        radSingle.addEventListener('change', () => switchAssignMode('single'));
    }

    if (inputSingleIP) {
        inputSingleIP.addEventListener('input', () => {
            validateSingleIPInput();
        });
    }

    if (btnRandomSingleIP) {
        btnRandomSingleIP.addEventListener('click', () => {
            void generateSingleRandomIP(selectedSingleSubnetId);
        });
    }

    // 模式 A: 多选网段列表事件委托
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

    // 模式 B: 单选网段列表事件委托 (点击即生成)
    if (singleSubnetListContainer) {
        singleSubnetListContainer.addEventListener('click', (e: Event) => {
            const target = (e.target as HTMLElement).closest('div[data-single-subnet-id]') as HTMLDivElement | null;
            if (!target) return;
            const subnetId = target.dataset.singleSubnetId;
            if (!subnetId) return;

            selectedSingleSubnetId = subnetId;
            syncSingleSubnetCardsStyle();
            void generateSingleRandomIP(subnetId);
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
                buildSingleSubnetCardsOnce();
            }
        } catch (e) {
            console.error('Failed to fetch subnets', e);
        }
    }

    // 重置模式为默认独立打散
    currentMode = 'unique';
    const radUnique = document.getElementById('radAssignModeUnique') as HTMLInputElement | null;
    if (radUnique) radUnique.checked = true;
    switchAssignMode('unique');

    // 默认全选所有网段
    selectedSubnetSet = new Set(subnetsCache.map(s => s.id));
    syncAllCardsCheckedAndStyle();

    // 默认选定首个网段作为单选备选
    if (subnetsCache.length > 0 && !selectedSingleSubnetId) {
        selectedSingleSubnetId = subnetsCache[0].id;
    }
    syncSingleSubnetCardsStyle();

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
    if (!singleSubnetListContainer) singleSubnetListContainer = document.getElementById('singleSubnetListContainer') as HTMLDivElement | null;
    if (!modeUniqueContainer) modeUniqueContainer = document.getElementById('modeUniqueContainer') as HTMLDivElement | null;
    if (!modeSingleContainer) modeSingleContainer = document.getElementById('modeSingleContainer') as HTMLDivElement | null;
    if (!inputSingleIP) inputSingleIP = document.getElementById('inputBatchAssignSingleIP') as HTMLInputElement | null;
    if (!btnRandomSingleIP) btnRandomSingleIP = document.getElementById('btnBatchAssignRandomSingleIP') as HTMLButtonElement | null;
    if (!validationTipEl) validationTipEl = document.getElementById('lblSingleIPValidationTip') as HTMLSpanElement | null;
    if (!countEl) countEl = document.getElementById('lblSelectedSubnetCount') as HTMLSpanElement | null;
    if (!btnConfirm) btnConfirm = document.getElementById('btnConfirmNvidiaBatchAssignIP') as HTMLButtonElement | null;
}

// 切换分配模式
function switchAssignMode(mode: AssignMode): void {
    currentMode = mode;
    ensureElements();

    const lblModeCardUnique = document.getElementById('lblModeCardUnique');
    const lblModeCardSingle = document.getElementById('lblModeCardSingle');

    if (mode === 'unique') {
        modeUniqueContainer?.classList.remove('hidden');
        modeSingleContainer?.classList.add('hidden');

        if (lblModeCardUnique) {
            lblModeCardUnique.className = 'flex items-start gap-2.5 p-3 rounded-xl border border-primary/40 bg-primary/5 dark:bg-primary/10 cursor-pointer transition-all';
        }
        if (lblModeCardSingle) {
            lblModeCardSingle.className = 'flex items-start gap-2.5 p-3 rounded-xl border border-outline-variant/30 hover:border-primary/50 cursor-pointer transition-all bg-white dark:bg-white/5';
        }
        updateSummaryAndButton();
    } else {
        modeSingleContainer?.classList.remove('hidden');
        modeUniqueContainer?.classList.add('hidden');

        if (lblModeCardSingle) {
            lblModeCardSingle.className = 'flex items-start gap-2.5 p-3 rounded-xl border border-amber-500/40 bg-amber-500/5 dark:bg-amber-500/10 cursor-pointer transition-all';
        }
        if (lblModeCardUnique) {
            lblModeCardUnique.className = 'flex items-start gap-2.5 p-3 rounded-xl border border-outline-variant/30 hover:border-primary/50 cursor-pointer transition-all bg-white dark:bg-white/5';
        }

        // 若输入框为空，自动按选定网段生成初始 IP
        if (inputSingleIP && !inputSingleIP.value.trim() && subnetsCache.length > 0) {
            if (!selectedSingleSubnetId) selectedSingleSubnetId = subnetsCache[0].id;
            syncSingleSubnetCardsStyle();
            void generateSingleRandomIP(selectedSingleSubnetId);
        } else {
            validateSingleIPInput();
        }
    }
}

// 校验 IPv4 格式
function isValidIPv4(ip: string): boolean {
    const trimmed = ip.trim();
    if (!trimmed) return false;
    const parts = trimmed.split('.');
    if (parts.length !== 4) return false;
    for (const p of parts) {
        if (!/^\d{1,3}$/.test(p)) return false;
        const num = Number(p);
        if (num < 0 || num > 255) return false;
        if (p.length > 1 && p.startsWith('0')) return false;
    }
    return true;
}

// 单 IP 模式下的实时格式校验与提示
function validateSingleIPInput(): boolean {
    ensureElements();
    if (!inputSingleIP || !btnConfirm) return false;

    const val = inputSingleIP.value.trim();
    const valid = isValidIPv4(val);

    if (validationTipEl) {
        if (!val) {
            validationTipEl.className = 'text-[11px] font-normal text-outline';
            validationTipEl.textContent = '请输入有效公网 IPv4 地址';
        } else if (valid) {
            validationTipEl.className = 'text-[11px] font-medium text-emerald-600 dark:text-emerald-400 flex items-center gap-0.5';
            validationTipEl.innerHTML = '<span class="material-symbols-outlined text-[13px]">check_circle</span> 格式正确';
        } else {
            validationTipEl.className = 'text-[11px] font-medium text-rose-500 dark:text-rose-400 flex items-center gap-0.5';
            validationTipEl.innerHTML = '<span class="material-symbols-outlined text-[13px]">error</span> 请输入合法的 IPv4 地址 (如 114.34.88.192)';
        }
    }

    btnConfirm.disabled = !valid;
    return valid;
}

// 从指定网段随机生成一个 IP 并填入输入框
async function generateSingleRandomIP(subnetId: string): Promise<void> {
    ensureElements();
    if (btnRandomSingleIP) {
        btnRandomSingleIP.disabled = true;
        btnRandomSingleIP.innerHTML = `<span class="material-symbols-outlined text-[16px] animate-spin">sync</span><span>生成中...</span>`;
    }

    try {
        const res = await ipcRenderer.invoke('nvidia:generate-subnet-ip', subnetId);
        if (res && res.success && res.ip) {
            if (inputSingleIP) {
                inputSingleIP.value = res.ip;
                validateSingleIPInput();
            }
        } else {
            console.error('Failed to generate IP:', res?.error);
        }
    } catch (e) {
        console.error('Error generating random subnet IP:', e);
    } finally {
        if (btnRandomSingleIP) {
            btnRandomSingleIP.disabled = false;
            const dict = i18n[state.currentLanguage] || i18n.zh;
            btnRandomSingleIP.innerHTML = `<span class="material-symbols-outlined text-[16px]">casino</span><span>${dict.batchAssignRandomOneBtn || '🎲 随机换一个'}</span>`;
        }
    }
}

// 首次拉取数据时单次生成多选网段卡片 DOM
function buildSubnetCardsOnce(): void {
    if (!subnetListContainer) return;
    subnetListContainer.innerHTML = '';

    const fragment = document.createDocumentFragment();

    subnetsCache.forEach(subnet => {
        const card = document.createElement('label');
        card.dataset.subnetCardId = subnet.id;
        card.className = 'flex items-center justify-between p-2.5 rounded-xl border text-[12px] cursor-pointer transition-all bg-primary/5 border-primary/40 dark:bg-primary/10 dark:border-primary/50';

        card.innerHTML = `
            <div class="flex items-center gap-2.5 overflow-hidden pointer-events-none">
                <input type="checkbox" data-subnet-id="${subnet.id}" checked class="pointer-events-auto w-4 h-4 rounded text-primary focus:ring-primary cursor-pointer" />
                <span class="text-[15px] shrink-0">${subnet.flag}</span>
                <div class="flex flex-col truncate">
                    <span class="font-bold text-on-surface dark:text-white truncate text-[11.5px]">${subnet.isp}</span>
                    <span class="text-[10px] text-outline truncate">${subnet.country} · ${subnet.location}</span>
                </div>
            </div>
            <span class="font-mono text-[10.5px] font-semibold px-2 py-0.5 rounded-md bg-slate-100 dark:bg-black/40 text-slate-700 dark:text-slate-200 border border-outline-variant/30 dark:border-white/10 flex-shrink-0 ml-2 shadow-2xs">
                ${subnet.cidr}
            </span>
        `;

        fragment.appendChild(card);
    });

    subnetListContainer.appendChild(fragment);
}

// 首次拉取数据时单次生成单选网段卡片 DOM
function buildSingleSubnetCardsOnce(): void {
    if (!singleSubnetListContainer) return;
    singleSubnetListContainer.innerHTML = '';

    const fragment = document.createDocumentFragment();

    subnetsCache.forEach(subnet => {
        const card = document.createElement('div');
        card.dataset.singleSubnetId = subnet.id;
        card.className = 'flex items-center justify-between p-2.5 rounded-xl border text-[12px] cursor-pointer transition-all bg-white dark:bg-white/5 border-outline-variant/30 hover:border-amber-500/50';

        card.innerHTML = `
            <div class="flex items-center gap-2.5 overflow-hidden pointer-events-none">
                <span class="text-[15px] shrink-0">${subnet.flag}</span>
                <div class="flex flex-col truncate">
                    <span class="font-bold text-on-surface dark:text-white truncate text-[11.5px]">${subnet.isp}</span>
                    <span class="text-[10px] text-outline truncate">${subnet.country} · ${subnet.location}</span>
                </div>
            </div>
            <span class="font-mono text-[10.5px] font-semibold px-2 py-0.5 rounded-md bg-slate-100 dark:bg-black/40 text-slate-700 dark:text-slate-200 border border-outline-variant/30 dark:border-white/10 flex-shrink-0 ml-2 shadow-2xs">
                ${subnet.cidr}
            </span>
        `;

        fragment.appendChild(card);
    });

    singleSubnetListContainer.appendChild(fragment);
}

// 同步单选网段高亮状态
function syncSingleSubnetCardsStyle(): void {
    if (!singleSubnetListContainer) return;
    const cards = singleSubnetListContainer.querySelectorAll<HTMLDivElement>('div[data-single-subnet-id]');
    cards.forEach(card => {
        const isSelected = card.dataset.singleSubnetId === selectedSingleSubnetId;
        if (isSelected) {
            card.classList.remove(...SINGLE_INACTIVE_CLASS);
            card.classList.add(...SINGLE_ACTIVE_CLASS);
        } else {
            card.classList.remove(...SINGLE_ACTIVE_CLASS);
            card.classList.add(...SINGLE_INACTIVE_CLASS);
        }
    });
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

// 高性能同步所有多选卡片的 checked 状态和 class
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
    if (currentMode === 'unique') {
        if (countEl) {
            countEl.innerText = String(selectedSubnetSet.size);
        }
        if (btnConfirm) {
            btnConfirm.disabled = selectedSubnetSet.size === 0;
        }
    } else {
        validateSingleIPInput();
    }
}

// 确认分配
async function handleConfirmBatchAssign(): Promise<void> {
    ensureElements();
    const radOverwrite = document.getElementById('radStrategyOverwrite') as HTMLInputElement | null;
    const overwriteAll = radOverwrite ? radOverwrite.checked : true;

    let payload: any = null;

    if (currentMode === 'unique') {
        if (selectedSubnetSet.size === 0) return;
        payload = {
            mode: 'unique',
            selectedSubnetIds: Array.from(selectedSubnetSet),
            overwriteAll,
        };
    } else {
        const ip = inputSingleIP?.value.trim() || '';
        if (!isValidIPv4(ip)) {
            const dict = i18n[state.currentLanguage] || i18n.zh;
            window.alert(dict.batchAssignInvalidIpAlert || '请输入有效的 IPv4 地址');
            return;
        }
        payload = {
            mode: 'single',
            singleIp: ip,
            selectedSubnetIds: selectedSingleSubnetId ? [selectedSingleSubnetId] : [],
            overwriteAll,
        };
    }

    if (btnConfirm) {
        btnConfirm.disabled = true;
        btnConfirm.innerHTML = `<span class="material-symbols-outlined text-[16px] animate-spin">sync</span><span>分配中...</span>`;
    }

    try {
        const res = await ipcRenderer.invoke('nvidia:batch-assign-egress-ip', payload);

        if (res && res.success) {
            const count = res.updatedCount || 0;
            const dict = i18n[state.currentLanguage] || i18n.zh;
            const msg = (dict.batchAssignSuccess || '成功为 {count} 个 NVIDIA 账号分配出口 IP').replace('{count}', String(count));
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


