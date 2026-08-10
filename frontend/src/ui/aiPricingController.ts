/**
 * aiPricingController.ts —— 「AI 一键生成计费」前端控制器。
 *
 * 职责:
 *   - 绑定 Dashboard.vue 计费配置工具条上的 #btnAiPricing 按钮 → 打开 Modal + 算候选;
 *   - 算候选模型清单(纯函数 aiPricingCandidates.computeAiPricingCandidates),0 个则禁用生成;
 *   - 填充账号下拉(限定 Antigravity/gemini-cli provider,避免误选非 Google token 打 daily-cloudcode-pa 失败);
 *   - 点「开始生成」→ ipcRenderer.invoke('pricing:ai-generate', accId, candidates);
 *   - 成功 → 渲染可编辑表(input/output/cached 三列 type=number)让用户微调;
 *   - 用户确认 → 收集表格行 ipcRenderer.send('update-pricing-batch', rows) → 关闭弹窗,
 *     后端经 get-pricing-res 自动刷新主计费表(无需本控制器再主动 fetch)。
 *
 * 依赖: state(currentAccountsList/statsData/pricingConfig/currentLanguage)、
 *       ipcRenderer、i18n、全局 alert/$confirm(沿用现有惯例,pricingController 同款)。
 * 不依赖任何跨簇 handle。
 */
import { ipcRenderer } from '../shared/ipc';
import i18n from '../shared/i18n';
import state from './dashboardState';
import { computeAiPricingCandidates } from './aiPricingCandidates';

let candidatesCache: string[] = [];

function dict() {
    return i18n[state.currentLanguage] || i18n.zh;
}

// 账号下拉填充:限定 Antigravity / gemini-cli provider(与 packet analyze 的「全 enabled」相比更严,
// 避免误选 NVIDIA/Grok token 直连 daily-cloudcode-pa 失败)。
function fillAccountSelect(): boolean {
    const sel = document.getElementById('aiPricingAccountSelect') as HTMLSelectElement | null;
    if (!sel) return false;
    const d = dict();
    const accs = (state.currentAccountsList || []).filter(
        (a: any) => a.enabled && (a.provider === 'antigravity' || a.provider === 'gemini-cli')
    );
    const pl = `<option value="" data-i18n="aiPricingAccountLabel">${d.aiPricingAccountLabel}</option>`;
    if (accs.length === 0) {
        sel.innerHTML = pl + `<option value="" disabled>${d.aiPricingNoAccount}</option>`;
        return false;
    }
    sel.innerHTML = pl + accs.map((a: any) => {
        const t = a.tier ? ` [${a.tier}]` : '';
        return `<option value="${a.id}">${a.email}${t}</option>`;
    }).join('');
    return true;
}

// 渲染候选数量提示 + 启停生成按钮。
function syncGenButtonState(): void {
    const d = dict();
    const genBtn = document.getElementById('btnAiPricingGen') as HTMLButtonElement | null;
    const pre = document.getElementById('aiPricingPreSection');
    if (!genBtn || !pre) return;
    const n = candidatesCache.length;
    if (n === 0) {
        genBtn.disabled = true;
        genBtn.innerHTML = `<span class="material-symbols-outlined text-[16px]">auto_awesome</span><span>${d.aiPricingNoCandidates}</span>`;
        return;
    }
    genBtn.disabled = false;
    genBtn.innerHTML = `<span class="material-symbols-outlined text-[16px]">auto_awesome</span><span>${d.aiPricingGenBtn} (${n})</span>`;
}

// 渲染结果可编辑表。
function renderResultTable(rates: { [k: string]: any }): void {
    const body = document.getElementById('aiPricingTableBody');
    if (!body) return;
    body.innerHTML = '';
    if (!rates || Object.keys(rates).length === 0) {
        body.innerHTML = `<tr><td colspan="5" class="p-4 text-center text-outline">${dict().aiPricingEmptyResult}</td></tr>`;
        return;
    }
    const d = dict();
    candidatesCache.forEach((name) => {
        const r = rates[name] || { input: 0, output: 0, cached: 0 };
        const tr = document.createElement('tr');
        tr.className = 'hover:bg-slate-50/50 dark:hover:bg-white/5';
        tr.innerHTML = `
            <td class="p-2.5 font-semibold text-on-surface dark:text-white align-middle">${name}</td>
            <td class="p-2 text-right"><input type="number" step="0.000001" min="0" value="${Number(r.input || 0).toFixed(6)}" data-field="input" class="w-28 text-right px-2 py-1 bg-white dark:bg-[#1a1f30] border border-outline-variant/50 rounded text-[12px] focus:border-primary focus:outline-none font-data-mono"/></td>
            <td class="p-2 text-right"><input type="number" step="0.000001" min="0" value="${Number(r.output || 0).toFixed(6)}" data-field="output" class="w-28 text-right px-2 py-1 bg-white dark:bg-[#1a1f30] border border-outline-variant/50 rounded text-[12px] focus:border-primary focus:outline-none font-data-mono"/></td>
            <td class="p-2 text-right"><input type="number" step="0.000001" min="0" value="${Number(r.cached || 0).toFixed(6)}" data-field="cached" class="w-28 text-right px-2 py-1 bg-white dark:bg-[#1a1f30] border border-outline-variant/50 rounded text-[12px] focus:border-primary focus:outline-none font-data-mono"/></td>
            <td class="p-2 text-center">
                <button class="btn-ai-row-del text-red-500 hover:underline text-[11px] font-bold" title="${d.aiPricingRowRemove}">${d.aiPricingRowRemove}</button>
            </td>
        `;
        tr.querySelector('.btn-ai-row-del')?.addEventListener('click', () => {
            tr.remove();
            updateConfirmCount();
        });
        body.appendChild(tr);
    });
    updateConfirmCount();
}

// 确认按钮文案随剩余行数动态更新。
function updateConfirmCount(): void {
    const btn = document.getElementById('btnAiPricingConfirm') as HTMLButtonElement | null;
    if (!btn) return;
    const d = dict();
    const n = document.querySelectorAll('#aiPricingTableBody tr').length;
    btn.disabled = n === 0;
    btn.innerHTML = `<span>${d.aiPricingConfirm}${n > 0 ? ` (${n})` : ''}</span>`;
}

function showSection(which: 'pre' | 'loading' | 'result'): void {
    const pre = document.getElementById('aiPricingPreSection');
    const loading = document.getElementById('aiPricingLoading');
    const res = document.getElementById('aiPricingResultSection');
    if (pre) pre.classList.toggle('hidden', which !== 'pre');
    if (loading) loading.classList.toggle('hidden', which !== 'loading');
    if (res) res.classList.toggle('hidden', which !== 'result');
    const confirmBtn = document.getElementById('btnAiPricingConfirm') as HTMLButtonElement | null;
    if (confirmBtn) confirmBtn.disabled = which !== 'result' || document.querySelectorAll('#aiPricingTableBody tr').length === 0;
}

export function showAiPricingModal() {
    const modal = document.getElementById('aiPricingModal');
    const container = document.getElementById('aiPricingModalContainer');
    if (!modal || !container) return;
    // 每次打开重算候选(模型统计可能在两次打开间变化)。
    candidatesCache = computeAiPricingCandidates(
        state.statsData && state.statsData.models,
        state.pricingConfig
    );
    fillAccountSelect();
    syncGenButtonState();
    showSection('pre');
    modal.classList.remove('opacity-0', 'pointer-events-none');
    container.classList.remove('scale-95');
    container.classList.add('scale-100');
}

export function hideAiPricingModal() {
    const modal = document.getElementById('aiPricingModal');
    const container = document.getElementById('aiPricingModalContainer');
    if (!modal || !container) return;
    modal.classList.add('opacity-0', 'pointer-events-none');
    container.classList.add('scale-95');
    container.classList.remove('scale-100');
}

export function initAiPricingEvents() {
    const btnOpen = document.getElementById('btnAiPricing');
    const btnGen = document.getElementById('btnAiPricingGen');
    const btnCancel = document.getElementById('btnAiPricingCancel');
    const btnConfirm = document.getElementById('btnAiPricingConfirm');
    const btnClose = document.getElementById('btnAiPricingModalClose');

    if (btnOpen) {
        btnOpen.addEventListener('click', () => showAiPricingModal());
    }

    if (btnGen) {
        btnGen.addEventListener('click', async () => {
            const sel = document.getElementById('aiPricingAccountSelect') as HTMLSelectElement | null;
            const accId = sel?.value || '';
            const d = dict();
            if (!accId) {
                alert(d.aiPricingErrSelectAccount);
                return;
            }
            if (candidatesCache.length === 0) return;
            showSection('loading');
            try {
                const res = await ipcRenderer.invoke('pricing:ai-generate', accId, candidatesCache);
                if (res && res.error) {
                    showSection('pre');
                    alert(`${d.aiPricingGenFail}: ${res.error}`);
                    return;
                }
                if (!res || Object.keys(res).length === 0) {
                    showSection('pre');
                    alert(d.aiPricingEmptyResult);
                    return;
                }
                renderResultTable(res);
                showSection('result');
            } catch (err: any) {
                showSection('pre');
                alert(`${d.aiPricingGenFail}: ${err?.message || err}`);
            }
        });
    }

    if (btnConfirm) {
        btnConfirm.addEventListener('click', async () => {
            const rows = document.querySelectorAll('#aiPricingTableBody tr');
            if (rows.length === 0) return;
            const d = dict();
            const batch: { [name: string]: { input: number; output: number; cached: number } } = {};
            let bad = false;
            rows.forEach((tr) => {
                const cels = (tr as HTMLElement).querySelectorAll('input[data-field]');
                let nameEl = (tr as HTMLElement).querySelector('td.font-semibold');
                if (!nameEl) {
                    const tds = (tr as HTMLElement).querySelectorAll('td');
                    nameEl = tds[0] as HTMLElement;
                }
                const name = (nameEl?.textContent || '').trim().toLowerCase();
                if (!name) return;
                const vals: { input: number; output: number; cached: number } = { input: 0, output: 0, cached: 0 };
                cels.forEach((inp) => {
                    const field = (inp as HTMLInputElement).getAttribute('data-field') || '';
                    const v = parseFloat((inp as HTMLInputElement).value);
                    if (isNaN(v) || v < 0) { bad = true; return; }
                    (vals as any)[field] = v;
                });
                batch[name] = vals;
            });
            if (bad) {
                alert(d.aiPricingGenFail + ': ' + (state.currentLanguage === 'zh' ? '存在非法价格(请填非负数字)' : 'invalid price (non-negative number required)'));
                return;
            }
            if (Object.keys(batch).length === 0) return;
            ipcRenderer.send('update-pricing-batch', batch);
            hideAiPricingModal();
        });
    }

    if (btnCancel) btnCancel.addEventListener('click', () => hideAiPricingModal());
    if (btnClose) btnClose.addEventListener('click', () => hideAiPricingModal());
}
