/**
 * aiPricingController.ts —— 「AI 一键生成计费」前端控制器。
 *
 * 职责:
 *   - 绑定 Dashboard.vue 计费配置工具条上的 #btnAiPricing 按钮 → 打开 Modal + 算候选;
 *   - 算候选模型清单(纯函数 aiPricingCandidates.computeAiPricingCandidates),0 个则禁用生成;
 *   - 填充账号下拉(限定 Antigravity/gemini-cli provider,避免误选非 Google token 打 daily-cloudcode-pa 失败);
 *   - 监听后端 pricing:ai-progress 事件 → 把 Generate 6 阶段进度文案实时写入 #aiPricingProgressMsg;
 *   - 点「开始生成」→ ipcRenderer.invoke('pricing:ai-generate', accId, candidates);
 *     返回 {模型名: AIPriceResult{rate,grounded,estimated,anchorConflict,sources}};
 *   - 成功 → 渲染可编辑表(input/output/cached 三列 type=number)+ 估算/未联网/锚点冲突标记 + 来源 URL;
 *   - 用户确认 → 收集表格行 ipcRenderer.send('update-pricing-batch', rows) → 关闭弹窗,
 *     后端经 get-pricing-res 自动刷新主计费表(无需本控制器再主动 fetch)。
 *
 * 依赖: state(currentAccountsList/statsData/pricingConfig/currentLanguage)、
 *       ipcRenderer、shell(打开来源 URL)、i18n、全局 alert(沿用现有惯例,pricingController 同款)。
 * 不依赖任何跨簇 handle。
 */
import { ipcRenderer, shell } from '../shared/ipc';
import i18n from '../shared/i18n';
import state from './dashboardState';
import { computeAiPricingCandidates } from './aiPricingCandidates';

let candidatesCache: string[] = [];
let progressBound = false; // 防止 initAiPricingEvents 多次注册重复 pricing:ai-progress 监听

function dict() {
    return i18n[state.currentLanguage] || i18n.zh;
}

// 把后端 step 键映射到 i18n 文案键,与 Generate 阶段边界一一对应。
function stageI18nKey(step: string): string {
    switch (step) {
        case 'fetch-token': return 'aiPricingStageFetchToken';
        case 'grounding-search': return 'aiPricingStageGrounding';
        case 'grounding-degraded': return 'aiPricingStageDegraded';
        case 'token-refresh': return 'aiPricingStageRefresh';
        case 'parse-result': return 'aiPricingStageParse';
        case 'done': return 'aiPricingStageDone';
        case 'error': return 'aiPricingStageError';
        default: return 'aiPricingGenFetching';
    }
}

// 渲染进度文案到 #aiPricingProgressMsg(loading 区)。
function applyProgress(step: string): void {
    const msgEl = document.getElementById('aiPricingProgressMsg');
    if (!msgEl) return;
    const key = stageI18nKey(step);
    const txt = (dict() as any)[key] || (dict() as any).aiPricingGenFetching;
    // error 阶段文案红色,done 阶段绿色,其余默认 outline 灰。
    msgEl.textContent = txt;
    msgEl.className = 'text-[12px] mt-1 font-medium ' + (
        step === 'error' ? 'text-rose-600 dark:text-rose-400' :
        step === 'done' ? 'text-emerald-600 dark:text-emerald-400' :
        step === 'grounding-degraded' ? 'text-amber-600 dark:text-amber-400' :
        'text-outline'
    );
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

// 转义:模型名/URL/title 都经 escapeHtml 防注入;数字走 toFixed 直接进 value 属性不构成注入面。
function escapeHtml(s: string): string {
    return String(s == null ? '' : s)
        .replace(/&/g, '&')
        .replace(/</g, '<')
        .replace(/>/g, '>')
        .replace(/"/g, '"')
        .replace(/'/g, '&#39;');
}

// 组装一行模型的标记 chip:估算/未联网/已联网/锚点冲突。返回内层 HTML 字符串。
function renderRowTags(r: any): string {
    const d = dict();
    const tags: string[] = [];
    if (r.anchorConflict) {
        tags.push(`<span class="inline-block px-1.5 py-0.5 text-[10px] font-bold rounded bg-rose-100 dark:bg-rose-900/40 text-rose-700 dark:text-rose-300 border border-rose-300 dark:border-rose-700/50" title="${escapeHtml(d.aiPricingTagAnchorConflict)}">⚠ ${escapeHtml(d.aiPricingTagAnchorConflict)}</span>`);
    }
    if (!r.grounded && r.estimated) {
        tags.push(`<span class="inline-block px-1.5 py-0.5 text-[10px] font-bold rounded bg-amber-100 dark:bg-amber-900/40 text-amber-700 dark:text-amber-300 border border-amber-300 dark:border-amber-700/50" title="${escapeHtml(d.aiPricingTagNotGrounded)}">${escapeHtml(d.aiPricingTagNotGrounded)}</span>`);
    } else if (r.estimated) {
        tags.push(`<span class="inline-block px-1.5 py-0.5 text-[10px] font-bold rounded bg-amber-100 dark:bg-amber-900/40 text-amber-700 dark:text-amber-300 border border-amber-300 dark:border-amber-700/50" title="${escapeHtml(d.aiPricingTagEstimated)}">${escapeHtml(d.aiPricingTagEstimated)}</span>`);
    } else if (r.grounded) {
        tags.push(`<span class="inline-block px-1.5 py-0.5 text-[10px] font-bold rounded bg-emerald-100 dark:bg-emerald-900/40 text-emerald-700 dark:text-emerald-300 border border-emerald-300 dark:border-emerald-700/50" title="${escapeHtml(d.aiPricingTagGrounded)}">${escapeHtml(d.aiPricingTagGrounded)}</span>`);
    }
    if (!tags.length) return '';
    return `<div class="flex flex-wrap gap-1 mt-0.5">${tags.join('')}</div>`;
}

// 组装来源 URL 小字(可点击,经 shell.openExternal 打开系统浏览器)。最多展示 3 条避免行过高。
function renderRowSources(sources: any[] | undefined): string {
    if (!sources || sources.length === 0) return '';
    const d = dict();
    const links = sources.slice(0, 3).map((s: any) => {
        const uri = escapeHtml(s.uri || '');
        const title = escapeHtml(s.title || s.uri || '');
        if (!uri) return '';
        return `<a href="#" data-src-url="${uri}" class="text-primary hover:underline truncate max-w-[220px] inline-block align-bottom" title="${uri}">${title}</a>`;
    }).filter(Boolean);
    if (!links.length) return '';
    return `<div class="flex flex-wrap items-center gap-x-1 mt-0.5 text-[10px] text-outline"><span>${escapeHtml(d.aiPricingSourcesLabel)}:</span>${links.join('<span class="text-outline-variant mx-0.5">·</span>')}</div>`;
}

// 绑定行内来源链接点击(委托方式,渲染后统一挂到 tbody)。
function bindSourceLinks(body: HTMLElement): void {
    body.querySelectorAll('a[data-src-url]').forEach((a) => {
        a.addEventListener('click', (e) => {
            e.preventDefault();
            const url = (a as HTMLAnchorElement).getAttribute('data-src-url') || '';
            if (url) shell.openExternal(url);
        });
    });
}

// 渲染结果可编辑表。rates 形如 {模型名: AIPriceResult{rate,grounded,estimated,anchorConflict,sources}}。
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
        const r = rates[name] || { rate: { input: 0, output: 0, cached: 0 } };
        const rate = r.rate || { input: 0, output: 0, cached: 0 };
        const tr = document.createElement('tr');
        tr.className = 'hover:bg-slate-50/50 dark:hover:bg-white/5';
        // 名称放 data-model-name 属性,供确认收集阶段可靠读取(避免 td 内 tag/source 文本污染 name)。
        tr.innerHTML = `
            <td class="p-2.5 align-middle">
                <div class="font-semibold text-on-surface dark:text-white" data-model-name="${escapeHtml(name)}">${escapeHtml(name)}</div>
                ${renderRowTags(r)}
                ${renderRowSources(r.sources)}
            </td>
            <td class="p-2 text-right align-middle"><input type="number" step="0.000001" min="0" value="${Number(rate.input || 0).toFixed(6)}" data-field="input" class="w-28 text-right px-2 py-1 bg-white dark:bg-[#1a1f30] border border-outline-variant/50 rounded text-[12px] focus:border-primary focus:outline-none font-data-mono"/></td>
            <td class="p-2 text-right align-middle"><input type="number" step="0.000001" min="0" value="${Number(rate.output || 0).toFixed(6)}" data-field="output" class="w-28 text-right px-2 py-1 bg-white dark:bg-[#1a1f30] border border-outline-variant/50 rounded text-[12px] focus:border-primary focus:outline-none font-data-mono"/></td>
            <td class="p-2 text-right align-middle"><input type="number" step="0.000001" min="0" value="${Number(rate.cached || 0).toFixed(6)}" data-field="cached" class="w-28 text-right px-2 py-1 bg-white dark:bg-[#1a1f30] border border-outline-variant/50 rounded text-[12px] focus:border-primary focus:outline-none font-data-mono"/></td>
            <td class="p-2 text-center align-middle">
                <button class="btn-ai-row-del text-red-500 hover:underline text-[11px] font-bold" title="${escapeHtml(d.aiPricingRowRemove)}">${escapeHtml(d.aiPricingRowRemove)}</button>
            </td>
        `;
        tr.querySelector('.btn-ai-row-del')?.addEventListener('click', () => {
            tr.remove();
            updateConfirmCount();
        });
        body.appendChild(tr);
    });
    bindSourceLinks(body);
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
    // 进入 loading 区时先把进度文案重置为默认 fetching 文案,等后端 progress 事件逐帧覆盖。
    if (which === 'loading') {
        const msgEl = document.getElementById('aiPricingProgressMsg');
        if (msgEl) {
            msgEl.textContent = (dict() as any).aiPricingGenFetching || '正在调用 Gemini 生成定价...';
            msgEl.className = 'text-[12px] text-outline mt-1 font-medium';
        }
    }
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

    // 监听后端 pricing:ai-progress 事件,套用 settings:migration-progress 同款 {step,status} 协议。
    // 仅注册一次(progressBound 守卫),避免 init 多次调用累加重复监听。
    if (!progressBound) {
        ipcRenderer.on('pricing:ai-progress', (event: any, data: any) => {
            if (!data || !data.step) return;
            applyProgress(String(data.step));
        });
        progressBound = true;
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
                    applyProgress('error');
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
        btnConfirm.addEventListener('click', () => {
            const rows = document.querySelectorAll('#aiPricingTableBody tr');
            if (rows.length === 0) return;
            const d = dict();
            const batch: { [name: string]: { input: number; output: number; cached: number } } = {};
            let bad = false;
            rows.forEach((tr) => {
                const cels = (tr as HTMLElement).querySelectorAll('input[data-field]');
                // 名称从 data-model-name 属性读取,避免 td 内 tag/source 文本污染。
                const nameEl = (tr as HTMLElement).querySelector('[data-model-name]');
                const name = (nameEl?.getAttribute('data-model-name') || '').trim().toLowerCase();
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
