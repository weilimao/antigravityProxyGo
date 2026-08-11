/**
 * Grok 一键解冻 Modal 控制逻辑:从 accountsController 委托的独立模块。
 *
 * 高内聚:承担 Grok 号池「一键解冻冷却账号」弹窗生命周期——打开时从 state.currentAccountsList
 * 过滤出冷却中的 Grok 账号、渲染可勾选列表、支持全选/多选、批量 invoke grok:clear-cooldown。
 * 自带 DOM 句柄 + 事件绑定,由 accountsController.initAccountsEvents 委托
 * initGrokThawModalEvents() 完成句柄赋值与事件绑定。
 * 依赖:ipcRenderer、state(只读 currentAccountsList / currentLanguage)、i18n。
 */
import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';
import i18n from '../shared/i18n';
import { getNvidiaCooldownRemaining } from './accountCardHelpers';

let btnGrokOneClickThaw: HTMLButtonElement | null;
let grokThawModal: HTMLDivElement | null;
let grokThawModalContainer: HTMLDivElement | null;
let grokThawListBody: HTMLDivElement | null;
let grokThawSelectAllCheckbox: HTMLInputElement | null;
let grokThawSelectAllLabel: HTMLElement | null;
let grokThawConfirmBtn: HTMLButtonElement | null;
let grokThawConfirmIcon: HTMLElement | null;
let grokThawConfirmText: HTMLElement | null;
let grokThawCancelBtn: HTMLButtonElement | null;
let grokThawCloseBtn: HTMLButtonElement | null;
let grokThawCountBadge: HTMLElement | null;

// 当前弹窗内冷却账号快照(供全选/提交回读),按下标对齐 checkbox data-index。
let grokThawAccounts: any[] = [];
// 选中态集合(Set<index>),与全选 checkbox 联动。
let grokThawSelected: Set<number> = new Set();

// 工具:判定单账号是否处于 Grok 冷却中(与 renderGrokAccountQuota 同口径)。
function isGrokAccountCooling(acc: any): boolean {
    if (!acc || acc.provider !== 'grok') return false;
    const now = Date.now();
    if (acc.cooldowns && typeof acc.cooldowns.grok === 'number' && acc.cooldowns.grok > now) {
        return true;
    }
    if (acc.cooldowns && typeof acc.cooldowns.all === 'number' && acc.cooldowns.all > now) {
        return true;
    }
    if (acc.cooldownUntil && acc.cooldownUntil > now) {
        return true;
    }
    return false;
}

// 句柄赋值 + 事件绑定(由 accountsController.initAccountsEvents 委托调用)
export function initGrokThawModalEvents(): void {
    btnGrokOneClickThaw = document.getElementById('btnGrokOneClickThaw') as HTMLButtonElement | null;
    grokThawModal = document.getElementById('grokThawModal') as HTMLDivElement | null;
    grokThawModalContainer = document.getElementById('grokThawModalContainer') as HTMLDivElement | null;
    grokThawListBody = document.getElementById('grokThawListBody') as HTMLDivElement | null;
    grokThawSelectAllCheckbox = document.getElementById('grokThawSelectAll') as HTMLInputElement | null;
    grokThawSelectAllLabel = document.getElementById('grokThawSelectAllLabel') as HTMLElement | null;
    grokThawConfirmBtn = document.getElementById('grokThawConfirmBtn') as HTMLButtonElement | null;
    grokThawConfirmIcon = document.getElementById('grokThawConfirmIcon') as HTMLElement | null;
    grokThawConfirmText = document.getElementById('grokThawConfirmText') as HTMLElement | null;
    grokThawCancelBtn = document.getElementById('grokThawCancelBtn') as HTMLButtonElement | null;
    grokThawCloseBtn = document.getElementById('grokThawCloseBtn') as HTMLButtonElement | null;
    grokThawCountBadge = document.getElementById('grokThawCountBadge') as HTMLElement | null;

    if (btnGrokOneClickThaw) {
        btnGrokOneClickThaw.addEventListener('click', openGrokThawModal);
    }
    if (grokThawCloseBtn) {
        grokThawCloseBtn.addEventListener('click', closeGrokThawModal);
    }
    if (grokThawCancelBtn) {
        grokThawCancelBtn.addEventListener('click', closeGrokThawModal);
    }
    if (grokThawConfirmBtn) {
        grokThawConfirmBtn.addEventListener('click', submitGrokThaw);
    }
    if (grokThawSelectAllCheckbox) {
        grokThawSelectAllCheckbox.addEventListener('change', (e: any) => {
            const checked = e.target.checked;
            if (checked) {
                grokThawSelected = new Set(grokThawAccounts.map((_, i) => i));
            } else {
                grokThawSelected.clear();
            }
            syncRowCheckboxes();
            updateThawSubmitState();
        });
    }
}

function openGrokThawModal() {
    if (!grokThawModal) return;
    grokThawModal.classList.remove('pointer-events-none', 'opacity-0');
    grokThawModal.classList.add('opacity-100');
    if (grokThawModalContainer) {
        grokThawModalContainer.classList.remove('scale-95');
        grokThawModalContainer.classList.add('scale-100');
    }
    renderGrokThawList();
}

function closeGrokThawModal() {
    if (!grokThawModal) return;
    grokThawModal.classList.add('opacity-0', 'pointer-events-none');
    grokThawModal.classList.remove('opacity-100');
    if (grokThawModalContainer) {
        grokThawModalContainer.classList.add('scale-95');
        grokThawModalContainer.classList.remove('scale-100');
    }
}

// 渲染冷却账号列表:从 state.currentAccountsList 过滤 Grok + 冷却中,逐行渲染 checkbox + 邮箱 + 倒计时。
function renderGrokThawList() {
    const dict = i18n[state.currentLanguage] || i18n.zh;
    // 重置选中态(每次打开弹窗重新拉最新快照)。
    grokThawSelected = new Set();

    // 从当前账号快照过滤冷却中的 Grok 账号(不限于当前分页,覆盖整个号池,符合"一键解冻"语义)。
    const allAccounts = state.currentAccountsList || [];
    grokThawAccounts = allAccounts.filter(isGrokAccountCooling);

    if (grokThawCountBadge) {
        const totalText = (dict.grokThawModalCount || '共 {count} 个冷却账号').replace('{count}', String(grokThawAccounts.length));
        grokThawCountBadge.textContent = totalText;
    }

    if (!grokThawListBody) return;
    if (grokThawAccounts.length === 0) {
        grokThawListBody.innerHTML = `
            <div class="p-8 text-center text-outline dark:text-outline-variant italic flex flex-col items-center gap-2">
                <span class="material-symbols-outlined text-[28px] text-emerald-500">check_circle</span>
                <span>${escapeHtml(dict.grokThawEmpty || '当前没有处于冷却中的 Grok 账号')}</span>
            </div>
        `;
        if (grokThawSelectAllCheckbox) grokThawSelectAllCheckbox.disabled = true;
        if (grokThawSelectAllLabel) grokThawSelectAllLabel.classList.add('opacity-50');
        updateThawSubmitState();
        return;
    }

    if (grokThawSelectAllCheckbox) grokThawSelectAllCheckbox.disabled = false;
    if (grokThawSelectAllLabel) grokThawSelectAllLabel.classList.remove('opacity-50');
    if (grokThawSelectAllCheckbox) grokThawSelectAllCheckbox.checked = false;

    grokThawListBody.innerHTML = grokThawAccounts.map((acc, i) => {
        // 取最早到期冷却时间,与卡片徽标 minCooldownTime 口径一致。
        let until = 0;
        if (acc.cooldowns) {
            for (const v of Object.values(acc.cooldowns as Record<string, number>)) {
                if (typeof v === 'number' && v > until) until = v;
            }
        }
        if (!until && acc.cooldownUntil) until = acc.cooldownUntil;
        const remaining = getNvidiaCooldownRemaining(until);
        const email = escapeHtml(acc.email || acc.id || '');
        const label = acc.label ? ` · ${escapeHtml(acc.label)}` : '';
        return `
            <label class="grok-thaw-row flex items-center gap-3 p-2.5 rounded-lg hover:bg-slate-50 dark:hover:bg-white/5 cursor-pointer transition-colors border border-transparent hover:border-outline-variant/20" data-index="${i}">
                <input type="checkbox" class="grok-thaw-row-checkbox w-4 h-4 rounded border-outline-variant/40 dark:border-white/20 text-sky-500 focus:ring-sky-500 cursor-pointer flex-shrink-0" data-index="${i}" />
                <span class="material-symbols-outlined text-sky-500 text-[18px] flex-shrink-0">ac_unit</span>
                <div class="flex flex-col min-w-0 flex-1">
                    <span class="text-[12px] font-medium text-on-surface dark:text-white truncate">${email}${label}</span>
                    <span class="text-[10px] text-amber-600 dark:text-amber-400 truncate">${escapeHtml(remaining.text)}</span>
                </div>
            </label>
        `;
    }).join('');

    // 行 checkbox 绑定:点 label 触发 checkbox 默认行为,此处仅监听 change 同步集合。
    const rowCheckboxes = grokThawListBody.querySelectorAll<HTMLInputElement>('.grok-thaw-row-checkbox');
    rowCheckboxes.forEach(cb => {
        cb.addEventListener('change', (e: any) => {
            const idx = Number(cb.getAttribute('data-index') || -1);
            if (idx < 0) return;
            if (e.target.checked) {
                grokThawSelected.add(idx);
            } else {
                grokThawSelected.delete(idx);
            }
            // 联动全选框:全选则勾上,否则取消。
            if (grokThawSelectAllCheckbox) {
                grokThawSelectAllCheckbox.checked = grokThawAccounts.length > 0 && grokThawSelected.size === grokThawAccounts.length;
            }
            updateThawSubmitState();
        });
    });

    updateThawSubmitState();
}

// 把 grokThawSelected 集合同步回 DOM checkbox(全选/反选场景用)。
function syncRowCheckboxes() {
    if (!grokThawListBody) return;
    const rowCheckboxes = grokThawListBody.querySelectorAll<HTMLInputElement>('.grok-thaw-row-checkbox');
    rowCheckboxes.forEach(cb => {
        const idx = Number(cb.getAttribute('data-index') || -1);
        cb.checked = grokThawSelected.has(idx);
    });
}

// 更新确认按钮可用态 + 文案(显示已选数量,保留 icon span 不动)。
function updateThawSubmitState() {
    const dict = i18n[state.currentLanguage] || i18n.zh;
    if (!grokThawConfirmBtn) return;
    const count = grokThawSelected.size;
    grokThawConfirmBtn.disabled = count === 0;
    if (grokThawConfirmText) {
        if (count > 0) {
            grokThawConfirmText.textContent = (dict.grokThawConfirmCount || '解冻 {count} 个账号').replace('{count}', String(count));
        } else {
            grokThawConfirmText.textContent = dict.grokThawConfirm || '解冻选中账号';
        }
    }
}

// 提交:逐个 invoke grok:clear-cooldown,统计成功数,完成后关闭弹窗并提示。
async function submitGrokThaw() {
    const dict = i18n[state.currentLanguage] || i18n.zh;
    const selectedIds = Array.from(grokThawSelected).map(i => grokThawAccounts[i]?.id).filter(Boolean);
    if (selectedIds.length === 0) return;

    if (grokThawConfirmBtn) {
        grokThawConfirmBtn.disabled = true;
        const progressText = (dict.grokThawProgress || '正在解冻... {done}/{total}').replace('{done}', '0').replace('{total}', String(selectedIds.length));
        if (grokThawConfirmText) grokThawConfirmText.textContent = progressText;
    }

    let success = 0;
    let done = 0;
    for (const id of selectedIds) {
        try {
            const res = await ipcRenderer.invoke('grok:clear-cooldown', id);
            if (res && res.success === true) success++;
        } catch {
            // 单账号失败不中断批量,继续处理剩余账号。
        }
        done++;
        if (grokThawConfirmBtn && grokThawConfirmText) {
            const progressText = (dict.grokThawProgress || '正在解冻... {done}/{total}').replace('{done}', String(done)).replace('{total}', String(selectedIds.length));
            grokThawConfirmText.textContent = progressText;
        }
    }

    closeGrokThawModal();
    // 后端 emitAccountsRes 会广播刷新账号卡片,此处兜底提示成功数。
    setTimeout(() => {
        alert((dict.grokThawSuccess || '已成功解冻 {count} 个账号').replace('{count}', String(success)));
    }, 100);
}

// escapeHtml 转义 HTML 特殊字符,避免邮箱/label 注入(复用 accountCardHelpers 同名实现,此处自带一份避免循环依赖)。
function escapeHtml(s: string): string {
    const AMP = String.fromCharCode(38);
    const LT = String.fromCharCode(60);
    const GT = String.fromCharCode(62);
    const QUOT = String.fromCharCode(34);
    const APOS = String.fromCharCode(39);
    return String(s)
        .replace(new RegExp(AMP, 'g'), AMP + 'amp;')
        .replace(new RegExp(LT, 'g'), AMP + 'lt;')
        .replace(new RegExp(GT, 'g'), AMP + 'gt;')
        .replace(new RegExp(QUOT, 'g'), AMP + 'quot;')
        .replace(new RegExp(APOS, 'g'), AMP + '#39;');
}
