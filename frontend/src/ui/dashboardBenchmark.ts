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
import { ensureBenchmarkTimer, updateBenchmarkCountdownDom } from './dashboardBenchmarkTimer';

function el(id: string): HTMLElement | null { return document.getElementById(id); }
function dict(): any { return (i18n as any)[state.currentLanguage] || {}; }
function isZh(): boolean { return state.currentLanguage === 'zh'; }

// benchRetestingSet: 正在单模型重测的模型名集合, 供卡片行内 ▷ 按钮显示 spinning 态;
// benchmark-updated 事件到达时整体清空(该事件携带最新结果 = 单模型重测/整批测速均已完成)。
const benchRetestingSet = new Set<string>();

// BENCH_COLLAPSED_KEY: 卡片折叠状态持久化 key(localStorage, 与 accounts_layout 等 UI pref 同机制)。
// 折叠后仅保留 header + 底部 meta 行(高度约 1/5), 解决「测速网格一夜占一屏、请求日志需滚动」的痛点。
const BENCH_COLLAPSED_KEY = 'benchmark_card_collapsed';

function isBenchmarkCollapsed(): boolean {
    try { return localStorage.getItem(BENCH_COLLAPSED_KEY) === '1'; } catch { return false; }
}

/** applyBenchmarkCollapse: 按 target 显示/隐藏模型网格与工具栏并刷新三角图标与 title 文案。 */
function applyBenchmarkCollapse(collapsed: boolean): void {
    const body = el('benchmarkCardBody');
    if (body) body.classList.toggle('hidden', collapsed);
    const toolbar = el('benchmarkToolbar');
    if (toolbar) toolbar.classList.toggle('hidden', collapsed || !benchHasData);
    const icon = el('benchmarkCollapseIcon');
    if (icon) icon.textContent = collapsed ? 'expand_more' : 'expand_less';
    const btn = el('btnBenchmarkCollapse') as HTMLButtonElement | null;
    if (btn) {
        const d = dict();
        btn.title = collapsed ? (d.benchmarkExpand || '展开测速列表') : (d.benchmarkCollapse || '收起测速列表');
    }
}

function toggleBenchmarkCollapse(): void {
    const next = !isBenchmarkCollapsed();
    try { localStorage.setItem(BENCH_COLLAPSED_KEY, next ? '1' : '0'); } catch { /* localStorage 不可用时静默忽略 */ }
    applyBenchmarkCollapse(next);
}

// ---- 卡片工具栏状态: 模糊搜索 + 排序(排序偏好持久化, 搜索词仅会话内有效) ----
let benchHasData = false;   // 当前是否有可展示的模型卡(决定工具栏是否显示)
let benchFilterQuery = '';  // 卡片模糊搜索关键字(小写)
const BENCH_SORT_KEY = 'benchmark_card_sort';
const BENCH_SORT_DIR_KEY = 'benchmark_card_sort_dir';

type BenchSortMode = 'rank' | 'total' | 'name' | 'config';

function benchSortMode(): BenchSortMode {
    try {
        const v = localStorage.getItem(BENCH_SORT_KEY);
        if (v === 'total' || v === 'name' || v === 'config') return v;
    } catch { /* localStorage 不可用时回落默认 */ }
    return 'rank';
}

function setBenchSortMode(m: BenchSortMode): void {
    try { localStorage.setItem(BENCH_SORT_KEY, m); } catch { /* ignore */ }
}

function benchSortAsc(): boolean {
    try { return localStorage.getItem(BENCH_SORT_DIR_KEY) !== 'desc'; } catch { return true; }
}

function setBenchSortAsc(asc: boolean): void {
    try { localStorage.setItem(BENCH_SORT_DIR_KEY, asc ? 'asc' : 'desc'); } catch { /* ignore */ }
}

/** updateSortDirBtn: 同步方向按钮图标/悬停文案/禁用态(配置顺序无方向语义)。 */
function updateSortDirBtn(): void {
    const btn = el('benchmarkSortDir') as HTMLButtonElement | null;
    const icon = el('benchmarkSortDirIcon');
    const d = dict(); const zh = isZh();
    const asc = benchSortAsc();
    if (icon) icon.textContent = asc ? 'arrow_upward' : 'arrow_downward';
    if (btn) {
        btn.title = asc
            ? (d.benchmarkSortAsc || (zh ? '升序(最快在前)' : 'Ascending (fastest first)'))
            : (d.benchmarkSortDesc || (zh ? '降序(最慢在前)' : 'Descending (slowest first)'));
        btn.disabled = benchSortMode() === 'config';
    }
}

/** rerenderBenchmarkCards: 搜索/排序交互后按缓存数据即时重画。 */
function rerenderBenchmarkCards(): void {
    if (state.benchmarkData) renderBenchmarkCard(state.benchmarkData);
}

/** initBenchmarkEvents: 绑定卡片/弹窗按钮 + 订阅事件 + 初装数据。幂等。 */
export function initBenchmarkEvents(): void {
    // ---- 卡片按钮 ----
    const btnRun = el('btnBenchmarkRun');
    const btnConfig = el('btnBenchmarkConfig');
    if (btnRun) {
        btnRun.addEventListener('click', () => {
            const p = state.benchmarkData || {};
            const cfg = p.config || {};
            const allModels = Array.isArray(cfg.models) ? cfg.models : [];
            state.benchmarkData = { ...p, pendingModels: allModels, running: true };
            renderBenchmarkCard(state.benchmarkData);
            ipcRenderer.invoke('benchmark:run-now').catch((e) => {
                console.error('[Benchmark] run-now failed', e);
                renderBenchmarkCard({ ...p, pendingModels: [], running: false });
            });
        });
    }
    if (btnConfig) btnConfig.addEventListener('click', () => openBenchmarkConfig());

    // 折叠按钮: 保存偏好状态并在初始应用(应用重启后保持上次用户的折叠选择)
    const btnCollapse = el('btnBenchmarkCollapse');
    if (btnCollapse) btnCollapse.addEventListener('click', toggleBenchmarkCollapse);
    applyBenchmarkCollapse(isBenchmarkCollapsed());

    // ---- 卡片工具栏: 模糊搜索 + 排序方式/方向 ----
    const cardSearch = el('benchmarkCardSearch') as HTMLInputElement | null;
    if (cardSearch) cardSearch.addEventListener('input', (e: any) => {
        benchFilterQuery = String((e.target as HTMLInputElement).value || '').toLowerCase();
        rerenderBenchmarkCards();
    });
    const sortSelect = el('benchmarkSortSelect') as HTMLSelectElement | null;
    if (sortSelect) {
        sortSelect.value = benchSortMode();
        sortSelect.addEventListener('change', () => {
            const v = sortSelect.value;
            setBenchSortMode(v === 'total' || v === 'name' || v === 'config' ? v : 'rank');
            updateSortDirBtn();
            rerenderBenchmarkCards();
        });
    }
    const sortDirBtn = el('benchmarkSortDir');
    if (sortDirBtn) sortDirBtn.addEventListener('click', () => {
        setBenchSortAsc(!benchSortAsc());
        updateSortDirBtn();
        rerenderBenchmarkCards();
    });
    updateSortDirBtn();

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
        const pending = new Set<string>(Array.isArray(payload?.pendingModels) ? payload.pendingModels : []);
        if (Array.isArray(payload?.pendingModels)) {
            for (const m of benchRetestingSet) {
                if (!pending.has(m)) benchRetestingSet.delete(m);
            }
        } else if (!payload?.running) {
            benchRetestingSet.clear();
        }
        state.benchmarkData = payload || {};
        renderBenchmarkCard(payload);
    });

    // 初装
    ipcRenderer.invoke('benchmark:get').then((p: any) => {
        state.benchmarkData = p || {};
        renderBenchmarkCard(p);
    }).catch((e) => console.error('[Benchmark] get failed', e));
}

/** refreshBenchmarkI18n: 语言切换后按缓存数据重渲染卡片。 */
export function refreshBenchmarkI18n(): void {
    if (state.benchmarkData) renderBenchmarkCard(state.benchmarkData);
    applyBenchmarkCollapse(isBenchmarkCollapsed());
    updateSortDirBtn();
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
    const configModels: string[] = Array.isArray(config.models) ? config.models : [];
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
            statusDot.title = d.benchmarkManualMode || (zh ? '手动测速模式(定时未启用)' : 'Manual Mode (Scheduled disabled)');
        }
    }
    if (runBtn) {
        const icon = runBtn.querySelector('.material-symbols-outlined');
        if (icon) {
            icon.classList.add('inline-block');
            icon.classList.toggle('animate-spin', running);
        }
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

    // 工具栏跟随数据/折叠状态显隐(空态时隐藏, 避免孤零零一条搜索框)
    benchHasData = !(configModels.length === 0 && results.length === 0);
    const toolbar = el('benchmarkToolbar');
    if (toolbar) toolbar.classList.toggle('hidden', !benchHasData || isBenchmarkCollapsed());

    // 若既没有配置模型，也无历史测速结果，展示空态提示
    if (configModels.length === 0 && results.length === 0) {
        updateBenchmarkCountdownDom(p);
        body.innerHTML = `
            <div class="flex flex-col items-center justify-center gap-1.5 py-6 text-outline dark:text-outline-variant/70">
                <span class="material-symbols-outlined text-[28px] text-outline/40">speed</span>
                <span class="text-[12px] font-medium">${d.benchmarkNoData || (zh ? '未配置模型或暂无测速数据' : 'No data')}</span>
                <span class="text-[11px]">${d.benchmarkNoDataHint || (zh ? '点击右上角齿轮配置要测速的模型' : '')}</span>
            </div>`;
        return;
    }

    // 建立现有测速结果 Map，以配置的模型列表为准对齐补齐
    const resultMap = new Map<string, any>();
    results.forEach((r) => {
        if (r && r.model) resultMap.set(r.model, r);
    });

    const displayList: any[] = [];
    if (configModels.length > 0) {
        for (const m of configModels) {
            if (resultMap.has(m)) {
                displayList.push(resultMap.get(m));
            } else {
                // 尚未有测试结果的模型(如刚添加) -> 自动补齐为待测/测速中项
                displayList.push({
                    model: m,
                    ttftMs: 0,
                    totalMs: 0,
                    prevTtftMs: 0,
                    prevTotalMs: 0,
                    status: 'pending',
                    error: '',
                    testedAt: '',
                });
            }
        }
    } else {
        displayList.push(...results);
    }

    const pendingSet = new Set<string>(Array.isArray(p.pendingModels) ? p.pendingModels : []);

    // ---- 排名: 仅对有有效首帧的成功模型排名(首帧升序, 并列按总耗时); 失败/待测不参与排名 ----
    const rankMap = new Map<string, number>();
    displayList
        .filter((r: any) => {
            const st = r.status || 'ok';
            return st !== 'error' && st !== 'pending' && r.ttftMs > 0;
        })
        .slice()
        .sort((a: any, b: any) => (a.ttftMs - b.ttftMs) || ((a.totalMs || 0) - (b.totalMs || 0)))
        .forEach((r: any, i: number) => rankMap.set(r.model, i + 1));

    // ---- 模糊搜索 + 排序(卡片左上工具栏交互) ----
    const q = benchFilterQuery.trim().toLowerCase();
    const mode = benchSortMode();
    const asc = benchSortAsc();
    // 失败/待测固定垫底, 成功组内按所选指标排序 —— 避免降序时待测/失败项霸占榜首
    const groupOf = (r: any): number => {
        const st = r.status || 'ok';
        return st === 'error' ? 1 : st === 'pending' ? 2 : 0;
    };
    const metricOf = (r: any): number => {
        const m = mode === 'total' ? r.totalMs : r.ttftMs;
        return m > 0 ? m : Number.MAX_SAFE_INTEGER;
    };
    const sortedList = displayList.slice();
    if (mode === 'rank' || mode === 'total') {
        sortedList.sort((a: any, b: any) => {
            const ga = groupOf(a); const gb = groupOf(b);
            if (ga !== gb) return ga - gb;
            if (ga !== 0) return 0;
            const diff = metricOf(a) - metricOf(b);
            return asc ? diff : -diff;
        });
    } else if (mode === 'name') {
        sortedList.sort((a: any, b: any) => asc
            ? String(a.model || '').localeCompare(String(b.model || ''))
            : String(b.model || '').localeCompare(String(a.model || '')));
    } // config: 保持用户配置顺序
    const finalList = q ? sortedList.filter((r: any) => String(r.model || '').toLowerCase().includes(q)) : sortedList;

    // 紧凑网格: 每模型一张小卡(状态点 + 排名徽章 + 名称 + 指标 + 单模型 ▷ 重测按钮)
    const retestTitle = d.benchmarkRetest || (zh ? '重测此模型' : 'Retest this model');
    const rankTitleTpl = d.benchmarkRankTitle || (zh ? '响应速度排名第 {n}' : 'Speed rank #{n}');
    const cards = finalList.map((r: any) => {
        const model = esc(r.model || '-');
        const st = r.status || 'ok';
        const isPending = st === 'pending';
        // 单个模型测试中判定:
        // 若后端传入 pendingModels，则精准以是否在此名单中为准(不在名单代表已出结果, 立即显示且停转);
        // 否则以本地重测集合或全局测速中待测状态兜底。
        const isTestingThisModel = pendingSet.size > 0
            ? pendingSet.has(r.model)
            : (benchRetestingSet.has(r.model) || (running && isPending));
        const isSpinning = isTestingThisModel;

        const ttft = formatDuration(r.ttftMs > 0 ? r.ttftMs : 0);
        const total = formatDuration(r.totalMs > 0 ? r.totalMs : 0);

        // 上次对照数据: 后端把旧 current 平移进 prevTtftMs/prevTotalMs 持久化, 任一有值即补一行灰色对照; 首测(全 0)不显示
        const hasPrev = (r.prevTtftMs > 0) || (r.prevTotalMs > 0);
        const prevTtft = r.prevTtftMs > 0 ? formatDuration(r.prevTtftMs) : '-';
        const prevTotal = r.prevTotalMs > 0 ? formatDuration(r.prevTotalMs) : '-';
        const prevLabel = d.benchmarkPrev || (zh ? '上次' : 'Prev');

        let ttftCls = 'text-slate-700 dark:text-slate-200';
        if (isPending) ttftCls = 'text-amber-500 dark:text-amber-400 font-normal';
        else if (st === 'ok') ttftCls = 'text-emerald-600 dark:text-emerald-400 font-semibold';
        else if (st === 'warning') ttftCls = 'text-amber-600 dark:text-amber-400 font-semibold';
        else if (st === 'error') ttftCls = 'text-rose-500 dark:text-rose-400 font-semibold';

        let dotCls = 'bg-emerald-500', dotTitle = d.benchmarkStatusOk || (zh ? '正常' : 'OK');
        if (isPending) {
            dotCls = isSpinning ? 'bg-amber-500 animate-pulse' : 'bg-slate-400 dark:bg-slate-500';
            dotTitle = isSpinning ? (d.benchmarkRunning || (zh ? '测速中...' : 'running...')) : (d.benchmarkPending || (zh ? '待测' : 'Pending'));
        } else if (st === 'warning') {
            dotCls = 'bg-amber-500'; dotTitle = d.benchmarkStatusWarn || (zh ? '稍慢' : 'Slow');
        } else if (st === 'error') {
            dotCls = 'bg-rose-500'; dotTitle = d.benchmarkStatusError || (zh ? '异常' : 'Error');
        }

        // 趋势: 与小卡同宽紧凑展示(↑/↓/≈ 符号即可, 悬停 title 显示差值)
        let trendHtml = '<span class="text-slate-400 dark:text-slate-500">—</span>';
        if (!isPending && st !== 'error' && r.prevTtftMs > 0 && r.ttftMs > 0) {
            const delta = r.ttftMs - r.prevTtftMs;
            if (Math.abs(delta) < 20) trendHtml = `<span class="text-slate-400 dark:text-slate-500" title="${zh ? '无变化' : 'no change'}">≈</span>`;
            else if (delta < 0) trendHtml = `<span class="text-emerald-500" title="${zh ? '比上次快' : 'faster'} ${formatDuration(Math.abs(delta))}">↓</span>`;
            else trendHtml = `<span class="text-rose-500" title="${zh ? '比上次慢' : 'slower'} ${formatDuration(delta)}">↑</span>`;
        }

        const errTitle = r.error ? ` title="${esc(r.error)}"` : '';
        let ttftShown = ttft;
        let totalShown = total;
        if (isPending) {
            ttftShown = isSpinning ? (d.benchmarkRunning || (zh ? '测速中...' : 'running...')) : (d.benchmarkPending || (zh ? '待测' : 'Pending'));
            totalShown = '--';
        } else if (st === 'error') {
            ttftShown = zh ? '失败' : 'fail';
            totalShown = '-';
        }

        // 排名徽章: 前三名高亮(金/银/铜), 其余灰色; 失败/待测/无首帧数据不参与排名, 占位对齐
        const rank = rankMap.get(r.model) || 0;
        let rankHtml = '<span class="shrink-0 min-w-[26px]"></span>';
        if (rank > 0) {
            const rankCls = rank === 1 ? 'text-amber-500 bg-amber-500/10 dark:text-amber-400'
                : rank === 2 ? 'text-slate-500 bg-slate-500/10 dark:text-slate-300'
                : rank === 3 ? 'text-orange-500 bg-orange-500/10 dark:text-orange-400'
                : 'text-slate-400 bg-slate-400/10 dark:text-slate-500';
            rankHtml = `<span class="shrink-0 min-w-[26px] text-center px-1 py-px rounded text-[11px] font-bold font-mono ${rankCls}" title="${rankTitleTpl.replace('{n}', String(rank))}">#${rank}</span>`;
        }

        return `
            <div class="flex flex-col gap-1.5 p-2.5 rounded-lg border border-outline-variant/20 bg-slate-50/40 dark:bg-white/[0.02] hover:border-primary/30 transition-colors">
                <div class="flex items-center gap-1.5 min-w-0">
                    <span class="w-2 h-2 rounded-full ${dotCls} shrink-0" title="${dotTitle}"></span>
                    ${rankHtml}
                    <span class="font-medium text-[13px] text-slate-700 dark:text-slate-100 truncate flex-1 min-w-0" title="${model}">${model}</span>
                    <button class="bench-retest-btn shrink-0 p-1 rounded text-amber-500 hover:bg-amber-500/10 transition-colors disabled:opacity-50" data-model="${model}" title="${retestTitle}" ${isSpinning ? 'disabled' : ''}>
                        <span class="material-symbols-outlined text-[16px] inline-block ${isSpinning ? 'animate-spin' : ''}">refresh</span>
                    </button>
                </div>
                <div class="flex items-center justify-between text-[12px] font-mono" ${errTitle}>
                    <span class="text-slate-400 dark:text-slate-500">${d.benchmarkColTtft || (zh ? '首帧' : 'TTFT')}<span class="${ttftCls} ml-1">${ttftShown}</span></span>
                    <span class="text-slate-400 dark:text-slate-500">${d.benchmarkColTotal || (zh ? '耗时' : 'Total')}<span class="text-blue-600 dark:text-blue-400 ml-1">${totalShown}</span></span>
                    <span class="w-7 text-center">${trendHtml}</span>
                </div>
                ${hasPrev ? `
                <div class="flex items-center justify-between text-[12px] font-mono text-slate-400/80 dark:text-slate-500/80" title="${zh ? '上一轮测速数据' : 'Previous round data'}">
                    <span>${prevLabel}<span class="ml-1 text-slate-500 dark:text-slate-400">${prevTtft}</span></span>
                    <span>${d.benchmarkColTotal || (zh ? '耗时' : 'Total')}<span class="ml-1 text-slate-500 dark:text-slate-400">${prevTotal}</span></span>
                    <span class="w-7"></span>
                </div>` : ''}
            </div>`;
    }).join('');

    if (finalList.length === 0) {
        // 搜索关键字无命中: 展示空态而非空网格
        body.innerHTML = `<div class="text-center text-[12px] text-outline dark:text-outline-variant/70 py-6 select-none">${d.benchmarkNoMatch || (zh ? '无匹配模型' : 'No matching models')}</div>`;
    } else {
        body.innerHTML = `<div class="grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-3 gap-2.5 max-h-[280px] overflow-y-auto pr-0.5">${cards}</div>`;
    }

    // 绑定单模型重测按钮
    body.querySelectorAll('.bench-retest-btn').forEach((btn) => {
        btn.addEventListener('click', () => {
            const model = (btn as HTMLButtonElement).getAttribute('data-model') || '';
            if (!model) return;
            benchRetestingSet.add(model);
            const icon = btn.querySelector('.material-symbols-outlined');
            if (icon) icon.classList.add('inline-block', 'animate-spin');
            (btn as HTMLButtonElement).disabled = true;
            ipcRenderer.invoke('benchmark:run-model', model).catch((e) => {
                console.error('[Benchmark] run-model failed', e);
                benchRetestingSet.delete(model);
                if (icon) icon.classList.remove('animate-spin');
                (btn as HTMLButtonElement).disabled = false;
            });
        });
    });

    updateBenchmarkCountdownDom(p);
    ensureBenchmarkTimer();
}

// ============ 配置弹窗(公共 BaseModal + 搜索式模型多选) ============

let benchSelectedSet = new Set<string>();
let benchCandidateList: string[] = [];
let benchSearchQuery = '';

async function openBenchmarkConfig(): Promise<void> {
    const modal = el('benchmarkConfigModal');
    const container = el('benchmarkConfigModalContainer');
    if (!modal || !container) return;

    // 打开前先拉一次最新配置作为回显基准: 保证输入框展示的是后端持久化值,
    // 而非可能过期/缺失的本地缓存(避免应用重启后缓存为空时回落到 HTML 默认值)。
    // 截图场景: config.json 持久化 120000, 若仍显示 30000 即为缓存未就绪所致。
    let cfg: any = {};
    try {
        const fresh: any = await ipcRenderer.invoke('benchmark:get');
        if (fresh && typeof fresh === 'object' && fresh.config) {
            state.benchmarkData = fresh;
            cfg = fresh.config || {};
        }
    } catch (e) {
        console.warn('[Benchmark] refresh config on open failed', e);
    }
    if (!cfg || typeof cfg !== 'object' || Object.keys(cfg).length === 0) {
        // 拉取失败时退回本地缓存兜底(过期值仍优于静默回退默认值)
        cfg = (state.benchmarkData || {}).config || {};
    }

    benchSelectedSet = new Set((cfg.models || []) as string[]);
    benchSearchQuery = '';

    const search = el('benchmarkModelSearch') as HTMLInputElement | null;
    if (search) search.value = '';
    const intervalInput = el('benchmarkIntervalInput') as HTMLInputElement | null;
    if (intervalInput) intervalInput.value = String(cfg.intervalMinutes ?? 5);
    const promptInput = el('benchmarkPromptInput') as HTMLInputElement | null;
    if (promptInput) promptInput.value = cfg.prompt || 'Hi';
    const timeoutInput = el('benchmarkTimeoutInput') as HTMLInputElement | null;
    if (timeoutInput) timeoutInput.value = String(cfg.timeoutMs ?? 30000);
    const enabledToggle = el('benchmarkEnabledToggle') as HTMLInputElement | null;
    if (enabledToggle) enabledToggle.checked = !!cfg.enabled;

    // 载入候选, 并把已选不在候选里的并入候选清单保证可见
    loadCandidateModels(true).then(() => renderBenchmarkModelList());
    renderBenchmarkModelList();

    // 过渡动画: 移除 opacity-0/pointer-events-none/hidden, 由 BaseModal 的 transition-opacity/transition-transform 接管
    modal.classList.remove('opacity-0', 'pointer-events-none', 'hidden');
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
    const intervalInput = el('benchmarkIntervalInput') as HTMLInputElement | null;
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
    const rawInterval = parseInt(intervalInput?.value || '', 10);
    const intervalMinutes = Number.isFinite(rawInterval) && rawInterval > 0 ? rawInterval : 5;
    const payload = {
        enabled: !!enabledToggle?.checked,
        models,
        intervalMinutes,
        prompt: (promptInput?.value || '').trim() || 'Hi',
        timeoutMs: parseInt(timeoutInput?.value || '30000', 10) || 30000,
    };
    if (btnSave) btnSave.disabled = true;
    try {
        const res = await ipcRenderer.invoke('benchmark:save', payload);
        if (res && res.success) {
            closeBenchmarkConfig();
            // 保存成功后立即补齐新模型列表并置为测速态回显，无论是否启用定时测速
            const oldData = state.benchmarkData || {};
            state.benchmarkData = {
                ...oldData,
                config: payload,
                pendingModels: models,
                running: models.length > 0,
            };
            renderBenchmarkCard(state.benchmarkData);
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
