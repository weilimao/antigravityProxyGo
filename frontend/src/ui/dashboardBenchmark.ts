/**
 * dashboardBenchmark.ts: 模型响应测速卡片 + 配置弹窗(基于公共 BaseModal)控制器。
 *
 * 卡片渲染后端 benchmark_results(每模型最新一轮首帧/耗时/状态/趋势);
 * 配置弹窗复用公共 BaseModal(与 AutoTrigger/NvidiaPreferred 同款淡入+缩放过渡),
 * 模型选择为「搜索 + 复选框清单」多选(候选来自中继模型映射, 与现有模型选择组件同口径)。
 *
 * 数据流: initBenchmarkEvents() 拉取 benchmark:get 初装 + 订阅 benchmark-updated;
 *         renderBenchmarkCard(payload) 渲染卡片; openBenchmarkConfig/saveConfig 弹窗交互。
 */
import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';
import i18n from '../shared/i18n';
import { formatDuration } from './dashboardUtils';

function el(id: string): HTMLElement | null { return document.getElementById(id); }
function dict(): any { return (i18n as any)[state.currentLanguage] || {}; }
function isZh(): boolean { return state.currentLanguage === 'zh'; }

// benchRetestingSet: 正在单模型重测的模型名集合, 供卡片行内 ▷ 按钮显示 spinning 态;
// benchmark-updated 事件到达时整体清空(该事件携带最新结果 = 单模型重测/整批测速均已完成)。
const benchRetestingSet = new Set<string>();

/** initBenchmarkEvents: 绑定卡片/弹窗按钮 + 订阅事件 + 初装数据。幂等。 */
export function initBenchmarkEvents(): void {
    // ---- 卡片按钮 ----
    const btnRun = el('btnBenchmarkRun');
    const btnConfig = el('btnBenchmarkConfig');
    if (btnRun) {
        btnRun.addEventListener('click', () => {
            ipcRenderer.invoke('benchmark:run-now').catch((e) => console.error('[Benchmark] run-now failed', e));
            const p = state.benchmarkData || {};
            renderBenchmarkCard({ ...p, running: true });
        });
    }
    if (btnConfig) btnConfig.addEventListener('click', () => openBenchmarkConfig());

    // ---- 弹窗按钮 ----
    const btnClose = el('btnBenchmarkConfigClose');
    const btnCancel = el('btnBenchmarkConfigCancel');
    const btnSave = el('btnBenchmarkConfigSave');
    const btnLoad = el('btnBenchmarkLoadModels');
    const btnSelectAll = el('btnBenchmarkSelectAllModels');
    const btnClearAll = el('btnBenchmarkClearAllModels');
    const searchInput = el('benchmarkModelSearch') as HTMLInputElement | null;
    if (btnClose) btnClose.addEventListener('click', closeBenchmarkConfig);
    if (btnCancel) btnCancel.addEventListener('click', closeBenchmarkConfig);
    if (btnSave) btnSave.addEventListener('click', saveBenchmarkConfig);
    if (btnLoad) btnLoad.addEventListener('click', () => loadCandidateModels(false).then(() => renderBenchmarkModelList()));
    if (btnSelectAll) btnSelectAll.addEventListener('click', () => {
        const q = benchSearchQuery.trim().toLowerCase();
        const visible = benchCandidateList.filter(m => !q || m.toLowerCase().includes(q));
        visible.forEach(m => benchSelectedSet.add(m));
        renderBenchmarkModelList();
    });
    if (btnClearAll) btnClearAll.addEventListener('click', () => {
        const q = benchSearchQuery.trim().toLowerCase();
        if (!q) { benchSelectedSet.clear(); }
        else {
            const visible = benchCandidateList.filter(m => m.toLowerCase().includes(q));
            visible.forEach(m => benchSelectedSet.delete(m));
        }
        renderBenchmarkModelList();
    });
    if (searchInput) searchInput.addEventListener('input', (e: any) => {
        benchSearchQuery = e.target.value || '';
        renderBenchmarkModelList();
    });

    // 注: 不绑定遮罩点击关闭 —— 配置中途误点遮罩会丢失已选, 仅留 ×/取消 按钮显式关闭。

    // ---- 订阅后端测速结果推送 ----
    ipcRenderer.on('benchmark-updated', (_e: any, payload: any) => {
        benchRetestingSet.clear();
        state.benchmarkData = payload || {};
        renderBenchmarkCard(payload);
    });

    // 初装
    ipcRenderer.invoke('benchmark:get').then((p: any) => {
        state.benchmarkData = p || {};
        renderBenchmarkCard(p);
    }).catch((e) => console.error('[Benchmark] get failed', e));
}

/** refreshBenchmarkI18n: 语言切换后按缓存数据重渲染卡片 + 刷新弹窗下拉文案。 */
export function refreshBenchmarkI18n(): void {
    if (state.benchmarkData) renderBenchmarkCard(state.benchmarkData);
    const intervalSel = el('benchmarkIntervalSelect') as HTMLSelectElement | null;
    if (intervalSel) {
        const d = dict();
        const opts: Array<[string, string]> = [
            ['1', d.benchmarkInterval1 || '1 min'],
            ['5', d.benchmarkInterval5 || '5 min'],
            ['15', d.benchmarkInterval15 || '15 min'],
            ['30', d.benchmarkInterval30 || '30 min'],
            ['60', d.benchmarkInterval60 || '1 hour'],
        ];
        intervalSel.querySelectorAll('option').forEach((opt, i) => { if (opts[i]) opt.textContent = opts[i][1]; });
    }
    // 弹窗若开着, 刷新模型清单文案
    if (el('benchmarkConfigModal') && !el('benchmarkConfigModal')?.classList.contains('opacity-0')) {
        renderBenchmarkModelList();
    }
}

// ============ 卡片渲染 ============

export function renderBenchmarkCard(payload: any): void {
    const body = el('benchmarkCardBody');
    if (!body) return;
    const d = dict(); const zh = isZh();
    const p = payload || {};
    const config = p.config || {};
    const results: any[] = Array.isArray(p.results) ? p.results : [];
    const running: boolean = !!p.running;

    const intervalChip = el('benchmarkIntervalChip');
    if (intervalChip) {
        const chipTpl = d.benchmarkIntervalChip || (zh ? '{n}分钟/次' : 'every {n}m');
        intervalChip.textContent = chipTpl.replace('{n}', String(config.intervalMinutes ?? 5));
        intervalChip.classList.toggle('hidden', !config.enabled);
    }

    const statusDot = el('benchmarkStatusDot');
    const runBtn = el('btnBenchmarkRun') as HTMLButtonElement | null;
    if (statusDot) {
        if (running) {
            statusDot.className = 'inline-block w-2 h-2 rounded-full bg-amber-500 animate-pulse';
            statusDot.title = d.benchmarkRunning || (zh ? '测速中...' : 'running...');
        } else if (config.enabled) {
            statusDot.className = 'inline-block w-2 h-2 rounded-full bg-emerald-500';
            statusDot.title = zh ? '已启用定时测速' : 'Scheduled benchmark enabled';
        } else {
            statusDot.className = 'inline-block w-2 h-2 rounded-full bg-slate-400';
            statusDot.title = zh ? '未启用' : 'Disabled';
        }
    }
    if (runBtn) {
        const icon = runBtn.querySelector('.material-symbols-outlined');
        if (icon) icon.classList.toggle('animate-spin', running);
        runBtn.disabled = running;
    }

    const meta = el('benchmarkCardMeta');
    if (meta) {
        let timeText = d.benchmarkNotRun || (zh ? '未测试' : 'not run');
        if (p.lastRun && !String(p.lastRun).startsWith('0001-')) {
            try { const t = new Date(p.lastRun); if (!isNaN(t.getTime())) timeText = t.toLocaleTimeString(); } catch { /* ignore */ }
        }
        meta.textContent = `${d.benchmarkLastTest || (zh ? '最新测试' : 'Last test')}: ${timeText}`;
    }

    if (results.length === 0) {
        body.innerHTML = `
            <div class="flex flex-col items-center justify-center gap-1.5 py-6 text-outline dark:text-outline-variant/70">
                <span class="material-symbols-outlined text-[28px] text-outline/40">speed</span>
                <span class="text-[12px] font-medium">${d.benchmarkNoData || (zh ? '未配置模型或暂无测速数据' : 'No data')}</span>
                <span class="text-[11px]">${d.benchmarkNoDataHint || (zh ? '点击右上角齿轮配置要测速的模型' : '')}</span>
            </div>`;
        return;
    }

    // 紧凑网格: 每模型一张小卡(状态点 + 名称 + 指标 + 单模型 ▷ 重测按钮), 不再通栏单行浪费空间。
    const retestTitle = d.benchmarkRetest || (zh ? '重测此模型' : 'Retest this model');
    const cards = results.map((r: any) => {
        const model = esc(r.model || '-');
        const st = r.status || 'ok';
        const ttft = formatDuration(r.ttftMs > 0 ? r.ttftMs : 0);
        const total = formatDuration(r.totalMs > 0 ? r.totalMs : 0);

        let ttftCls = 'text-slate-700 dark:text-slate-200';
        if (st === 'ok') ttftCls = 'text-emerald-600 dark:text-emerald-400 font-semibold';
        else if (st === 'warning') ttftCls = 'text-amber-600 dark:text-amber-400 font-semibold';
        else if (st === 'error') ttftCls = 'text-rose-500 dark:text-rose-400 font-semibold';

        let dotCls = 'bg-emerald-500', dotTitle = d.benchmarkStatusOk || (zh ? '正常' : 'OK');
        if (st === 'warning') { dotCls = 'bg-amber-500'; dotTitle = d.benchmarkStatusWarn || (zh ? '稍慢' : 'Slow'); }
        else if (st === 'error') { dotCls = 'bg-rose-500'; dotTitle = d.benchmarkStatusError || (zh ? '异常' : 'Error'); }

        // 趋势: 与小卡同宽紧凑展示(↑/↓/≈ 符号即可, 悬停 title 显示差值)
        let trendHtml = '<span class="text-slate-400 dark:text-slate-500">—</span>';
        if (st !== 'error' && r.prevTtftMs > 0 && r.ttftMs > 0) {
            const delta = r.ttftMs - r.prevTtftMs;
            if (Math.abs(delta) < 20) trendHtml = `<span class="text-slate-400 dark:text-slate-500" title="${zh ? '无变化' : 'no change'}">≈</span>`;
            else if (delta < 0) trendHtml = `<span class="text-emerald-500" title="${zh ? '比上次快' : 'faster'} ${formatDuration(Math.abs(delta))}">↓</span>`;
            else trendHtml = `<span class="text-rose-500" title="${zh ? '比上次慢' : 'slower'} ${formatDuration(delta)}">↑</span>`;
        }

        const errTitle = r.error ? ` title="${esc(r.error)}"` : '';
        const ttftShown = st === 'error' ? (zh ? '失败' : 'fail') : ttft;
        const totalShown = st === 'error' ? '-' : total;
        const retesting = benchRetestingSet.has(r.model);

        return `
            <div class="flex flex-col gap-1 p-2 rounded-lg border border-outline-variant/20 bg-slate-50/40 dark:bg-white/[0.02] hover:border-primary/30 transition-colors">
                <div class="flex items-center gap-1.5 min-w-0">
                    <span class="w-1.5 h-1.5 rounded-full ${dotCls} shrink-0" title="${dotTitle}"></span>
                    <span class="font-medium text-[11px] text-slate-700 dark:text-slate-100 truncate flex-1 min-w-0" title="${model}">${model}</span>
                    <button class="bench-retest-btn shrink-0 p-0.5 rounded text-amber-500 hover:bg-amber-500/10 transition-colors disabled:opacity-50" data-model="${model}" title="${retestTitle}">
                        <span class="material-symbols-outlined text-[13px] ${retesting ? 'animate-spin' : ''}">refresh</span>
                    </button>
                </div>
                <div class="flex items-center justify-between text-[10px] font-mono" ${errTitle}>
                    <span class="text-slate-400 dark:text-slate-500">${d.benchmarkColTtft || (zh ? '首帧' : 'TTFT')}<span class="${ttftCls} ml-0.5">${ttftShown}</span></span>
                    <span class="text-slate-400 dark:text-slate-500">${d.benchmarkColTotal || (zh ? '耗时' : 'Total')}<span class="text-blue-600 dark:text-blue-400 ml-0.5">${totalShown}</span></span>
                    <span class="w-6 text-center">${trendHtml}</span>
                </div>
            </div>`;
    }).join('');

    body.innerHTML = `<div class="grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-3 gap-2 max-h-[200px] overflow-y-auto pr-0.5">${cards}</div>`;

    // 绑定单模型重测按钮
    body.querySelectorAll('.bench-retest-btn').forEach((btn) => {
        btn.addEventListener('click', () => {
            const model = (btn as HTMLButtonElement).getAttribute('data-model') || '';
            if (!model) return;
            ipcRenderer.invoke('benchmark:run-model', model).catch((e) => console.error('[Benchmark] run-model failed', e));
            benchRetestingSet.add(model);
            const icon = btn.querySelector('.material-symbols-outlined');
            if (icon) icon.classList.add('animate-spin');
            (btn as HTMLButtonElement).disabled = true;
        });
    });
}

// ============ 配置弹窗(公共 BaseModal + 搜索式模型多选) ============

let benchSelectedSet = new Set<string>();
let benchCandidateList: string[] = [];
let benchSearchQuery = '';

function openBenchmarkConfig(): void {
    const modal = el('benchmarkConfigModal');
    const container = el('benchmarkConfigModalContainer');
    if (!modal || !container) return;

    const cfg = (state.benchmarkData || {}).config || {};
    benchSelectedSet = new Set((cfg.models || []) as string[]);
    benchSearchQuery = '';

    const search = el('benchmarkModelSearch') as HTMLInputElement | null;
    if (search) search.value = '';
    const intervalSel = el('benchmarkIntervalSelect') as HTMLSelectElement | null;
    if (intervalSel) intervalSel.value = String(cfg.intervalMinutes ?? 5);
    const promptInput = el('benchmarkPromptInput') as HTMLInputElement | null;
    if (promptInput) promptInput.value = cfg.prompt || 'Hi';
    const timeoutInput = el('benchmarkTimeoutInput') as HTMLInputElement | null;
    if (timeoutInput) timeoutInput.value = String(cfg.timeoutMs ?? 30000);
    const enabledToggle = el('benchmarkEnabledToggle') as HTMLInputElement | null;
    if (enabledToggle) enabledToggle.checked = !!cfg.enabled;

    // 载入候选, 并把已选不在候选里的并入候选清单保证可见
    loadCandidateModels(true).then(() => renderBenchmarkModelList());
    renderBenchmarkModelList();

    // 过渡动画: 移除 opacity-0/scale-95, 由 BaseModal 的 transition-opacity/transition-transform 接管
    modal.classList.remove('opacity-0', 'pointer-events-none');
    container.classList.remove('scale-95');
    container.classList.add('scale-100');
}

function closeBenchmarkConfig(): void {
    const modal = el('benchmarkConfigModal');
    const container = el('benchmarkConfigModalContainer');
    if (!modal || !container) return;
    modal.classList.add('opacity-0', 'pointer-events-none');
    container.classList.add('scale-95');
    container.classList.remove('scale-100');
}

async function loadCandidateModels(silent: boolean): Promise<void> {
    const hint = el('benchmarkLoadHint');
    const d = dict(); const zh = isZh();
    try {
        const res = await ipcRenderer.invoke('benchmark:models');
        const models: string[] = (res && res.models) || [];
        benchCandidateList = models;
        for (const m of benchSelectedSet) {
            if (!benchCandidateList.includes(m)) benchCandidateList.push(m);
        }
        if (hint) {
            if (models.length === 0) hint.textContent = zh ? '未找到中继映射模型' : 'No relay mapping models found';
            else hint.textContent = (d.benchmarkLoadModelsHint || (zh ? '已载入 {n} 个候选模型' : 'Loaded {n} candidate models')).replace('{n}', String(models.length));
        }
    } catch (e: any) {
        if (hint) hint.textContent = (zh ? '载入失败: ' : 'Load failed: ') + (e?.message || '');
    }
}

function renderBenchmarkModelList(): void {
    const list = el('benchmarkModelsList');
    const countEl = el('lblBenchmarkSelectedCount');
    if (!list) return;
    const d = dict(); const zh = isZh();
    if (countEl) countEl.textContent = String(benchSelectedSet.size);

    if (benchCandidateList.length === 0) {
        list.innerHTML = `<div class="text-center text-outline py-6 select-none">${d.benchmarkModelsEmpty || (zh ? '点击「载入中继映射模型」加载候选' : '')}</div>`;
        return;
    }
    const q = benchSearchQuery.trim().toLowerCase();
    const filtered = benchCandidateList.filter(m => !q || m.toLowerCase().includes(q));
    if (filtered.length === 0) {
        list.innerHTML = `<div class="text-center text-outline py-6 select-none">${d.benchmarkNoMatch || (zh ? '无匹配模型' : 'No matching models')}</div>`;
        return;
    }
    list.innerHTML = filtered.map(m => {
        const checked = benchSelectedSet.has(m);
        const id = 'chk_bench_' + m.replace(/[^a-zA-Z0-9]/g, '_');
        return `<label class="flex items-center gap-1.5 px-1.5 py-1 rounded hover:bg-slate-100 dark:hover:bg-white/5 cursor-pointer select-none" for="${id}">
            <input type="checkbox" id="${id}" class="bench-model-cb rounded border-outline-variant/40 text-primary focus:ring-primary cursor-pointer" value="${esc(m)}" ${checked ? 'checked' : ''}>
            <span class="truncate font-mono text-[11px]" title="${esc(m)}">${esc(m)}</span>
        </label>`;
    }).join('');

    list.querySelectorAll('.bench-model-cb').forEach((cb) => {
        cb.addEventListener('change', () => {
            const node = cb as HTMLInputElement;
            if (node.checked) benchSelectedSet.add(node.value);
            else benchSelectedSet.delete(node.value);
            if (countEl) countEl.textContent = String(benchSelectedSet.size);
        });
    });
}

async function saveBenchmarkConfig(): Promise<void> {
    const d = dict(); const zh = isZh();
    const intervalSel = el('benchmarkIntervalSelect') as HTMLSelectElement | null;
    const promptInput = el('benchmarkPromptInput') as HTMLInputElement | null;
    const timeoutInput = el('benchmarkTimeoutInput') as HTMLInputElement | null;
    const enabledToggle = el('benchmarkEnabledToggle') as HTMLInputElement | null;
    const btnSave = el('btnBenchmarkConfigSave') as HTMLButtonElement | null;

    const models = Array.from(benchSelectedSet);
    if (models.length === 0) {
        // 清空后保存 = 清空测速: 后端会清 benchmark_results 并推送空态, 配置变为空模型清单。
        // 用确认弹窗替代硬报错, 让「清空→保存」成为合法的重置入口, 也避免误清。
        const ok = await $confirm(d.benchmarkClearConfirm || (zh ? '当前未选择任何模型，保存将清空测速配置与历史结果，确定清空吗？' : 'No models selected. Saving will clear the benchmark config and history. Continue?'));
        if (!ok) return;
    }
    const payload = {
        enabled: !!enabledToggle?.checked,
        models,
        intervalMinutes: parseInt(intervalSel?.value || '5', 10) || 5,
        prompt: (promptInput?.value || '').trim() || 'Hi',
        timeoutMs: parseInt(timeoutInput?.value || '30000', 10) || 30000,
    };
    if (btnSave) btnSave.disabled = true;
    try {
        const res = await ipcRenderer.invoke('benchmark:save', payload);
        if (res && res.success) {
            closeBenchmarkConfig();
        } else {
            alert((zh ? '保存失败: ' : 'Save failed: ') + (res?.error || (zh ? '未知错误' : 'Unknown error')));
        }
    } catch (e: any) {
        alert((zh ? '保存异常: ' : 'Exception: ') + e.message);
    } finally {
        if (btnSave) btnSave.disabled = false;
    }
}

function esc(s: string): string {
    return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}
