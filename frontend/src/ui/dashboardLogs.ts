/**
 * dashboardLogs.ts: 请求日志表格行池 builder + 行 slot 刷新(内存稳定 row-pool)。
 *
 * 从 dashboard.ts 抽离(原 L137-327 单一连续段):LogsRowSlot interface + logsRowSlots 数组 +
 * viewBtnLogMap WeakMap + buildLogsRowSlot(纯 DOM 建元素,无 state 依赖)+ updateLogsRowSlot(读 state + formatDuration)。
 * logsRowSlots/viewBtnLogMap 为 export const:hub renderLogsTable 跨模块就地 mutate(push/length=0/[i]读、set/get),
 * init 点击委托跨模块读 viewBtnLogMap.get(btn) —— const 的 live 引用支持跨模块 mutation,语义等价于原同模块 mutation。
 */
import state from './dashboardState';
import { formatDuration, formatDisplayModel } from './dashboardUtils';

// --- 成对 HIT/MISS 重试行折叠(展示层去重,后端落库不动) ---
//
// 背景(已验证事实):
//   - 全仓唯一产生 MISS 的代码点是 internal/proxy/helpers.go:176-185(cachedTokens==0 → MISS);
//     单次请求只落一条日志(logged 标志位),故"成对双行"来自客户端在流式被中断后
//     同秒内发了两次独立请求,两次 ServeHTTP 各落一条。第一次上游 Gemini 现场写 Prompt
//     Cache(慢,MISS),第二次命中缓存(快,HIT)。前端按秒级时间戳排序,两行紧贴,观感"碍眼"。
//   - 后端 AddRequestLog/TrackRequest/计费/SQLite 落库全部基于单条 RequestLog 精确记账,
//     去重须只在展示层做,以保证统计/账单不失真。本函数即在 renderLogsTable 分页前对
//     filtered 列表做原地折叠,输出合并后的展示行(带 retryN 标记)。
//
// 折叠规则:
//   - 指纹 = sessionId + path + model + inTokens;时间窗口 ±3s(MM/DD HH:MM:SS 秒级,窗口吸收
//     同秒与边界跨秒)。同指纹且窗口内的多条视为同一次请求的客户端重试。
//   - 同组若同时含 HIT 与 MISS:合并为 1 条,徽章取 HIT(最终成功结果)且展示命中那次的
//     duration/firstByteMs/cachedTokens/inTokens/outTokens/cost;重复字段取第一条。组内全部
//     为 HIT(多次命中)或全部为 MISS 也合并并标记重试次数。组内仅 1 条则原样返回。
//   - 合并行上挂 __retryN 字段(重试次数=组内条数),渲染时在徽章旁显示 ⟳N 角标 +
//     tooltip(文案 i18n key=retryMerged,模板 {n} 由 updateLogsRowSlot 替换)。
//   - 不回写 state.allRequests,不污染统计;每轮 render 现算,无跨渲染缓存。

// RENDER_RETRY_WINDOW_MS: 同指纹视为重试的时间窗口。3s 既覆盖同秒成对,又留够余量吸收
// 客户端 sub-second 重发与边界跨秒;过大会误并真实连续的不同请求(同指纹含 inTokens,
// 16 万 token 级,连续两次内容不同还恰好同 inTokens 的概率极低,故 3s 安全)。
const RENDER_RETRY_WINDOW_MS = 3000;

// parseLogTimestampToMs: 把 RequestLog.Timestamp("MM/DD HH:MM:SS",本地时区秒级)解析成 epoch ms。
// Date 解析 "MM/DD HH:MM:SS"(无年无时区)在不同引擎行为不一(Chrome 当 UTC),为避免 8h 偏差,
// 显式补当前年份并按本地时区 new Date(year, M-1, D, H, m, s) 构造,保证与后端 time.Now().Format
// 的本地时区语义一致(后端用机器本地时区格式化,前端须用同口径解析)。
function parseLogTimestampToMs(ts: string): number {
    // 形如 "08/10 11:42:39";缺省内防御性兜底。
    const m = /^(\d{2})\/(\d{2})\s+(\d{2}):(\d{2}):(\d{2})$/.exec(ts || '');
    if (!m) return NaN;
    const now = new Date();
    const year = now.getFullYear();
    const dt = new Date(year, parseInt(m[1], 10) - 1, parseInt(m[2], 10),
        parseInt(m[3], 10), parseInt(m[4], 10), parseInt(m[5], 10));
    return dt.getTime();
}

// retryFingerprint: 返回命中折叠分组的指纹串;任一关键字段缺失返回 null(不参与折叠,原样保留)。
// 含 inTokens 是关键:连续两次真正不同的请求恰好同 sessionId+path+model 而内容不同的概率被
// inTokens(通常 16 万级且随对话增长)进一步压到可忽略。
function retryFingerprint(log: any): string | null {
    if (!log) return null;
    const sid = log.sessionId;
    const path = log.path;
    const model = log.model;
    if (!sid || !path || !model) return null;
    if (typeof log.inTokens !== 'number' || log.inTokens <= 0) return null;
    return `${sid}|${path}|${model}|${log.inTokens}`;
}

// pickDisplayLog: 同组多条中选出"最终成功展示"的那一条。
//   优先 HIT(cachedTokens>0,上游 Prompt Cache 命中),其次按 cacheStatus==='HIT' 字面,
//   再退而取 statusCode<400 的最后一条,最后兜底组内最后一条(列表时间序靠后=更晚)。
function pickDisplayLog(group: any[]): any {
    const hit = group.find(g => g.cacheStatus === 'HIT') ||
        group.find(g => (g.cachedTokens || 0) > 0) ||
        group.find(g => g.statusCode < 400);
    return hit || group[group.length - 1];
}

// mergeRetryRows: 对已 filtered(含搜索过滤后的 state.allRequests 子集)按指纹+时间窗口折叠。
// 输入顺序即 state.allRequests 顺序(后端 prepend newest first,最新在前);输出保持 newest first。
// 用 Map<指纹, {firstIdx, items[]}> 在单次线性扫描里完成邻域分组——只合并时间相邻的同指纹行,
// 避免把间隔很远(>3s)的两次同指纹请求错并(后者应视为两次独立请求)。
export function mergeRetryRows(logs: any[]): any[] {
    if (!Array.isArray(logs) || logs.length < 2) return logs;
    const out: any[] = [];
    // i 指向当前未处理起点;每次 either(独条直推)或收集完一个连续同指纹窗口后推进。
    for (let i = 0; i < logs.length;) {
        const fp = retryFingerprint(logs[i]);
        if (fp === null) {
            out.push(logs[i]);
            i++;
            continue;
        }
        const t0 = parseLogTimestampToMs(logs[i].timestamp);
        if (isNaN(t0)) {
            out.push(logs[i]);
            i++;
            continue;
        }
        // 收集从 i 起,指纹相同且时间在 [t0-WIN, t0+WIN] 内的连续行。
        // 注意时间序:logs newest first,故后出现的 timestamp 更小(更早),窗口判断用绝对差即可。
        const group: any[] = [logs[i]];
        let j = i + 1;
        for (; j < logs.length; j++) {
            if (retryFingerprint(logs[j]) !== fp) break;
            const tj = parseLogTimestampToMs(logs[j].timestamp);
            if (isNaN(tj) || Math.abs(tj - t0) > RENDER_RETRY_WINDOW_MS) break;
            group.push(logs[j]);
        }
        if (group.length === 1) {
            out.push(logs[i]);
        } else {
            const display = { ...pickDisplayLog(group) };
            display.__retryN = group.length;
            out.push(display);
        }
        i = j;
    }
    return out;
}

// --- Logs table row pool ---
// A fixed pool of reusable <tr> nodes (grown up to itemsPerPage) is patched
// in place instead of rebuilding the table via innerHTML on every stats tick.
// Under heavy traffic the create+destroy churn of innerHTML caused Blink's DOM
// node pools to inflate without returning memory to the OS.
export interface LogsRowSlot {
    tr: HTMLTableRowElement;
    timestamp: HTMLTableCellElement;
    method: HTMLSpanElement;
    host: HTMLSpanElement;
    methodHostCell: HTMLTableCellElement;
    path: HTMLTableCellElement;
    sessionId: HTMLTableCellElement;
    modelName: HTMLSpanElement;
    reasoningBadge: HTMLSpanElement;
    nvidiaBadge: HTMLSpanElement;
    account: HTMLSpanElement;
    modelCell: HTMLTableCellElement;
    inTokens: HTMLSpanElement;
    outTokens: HTMLSpanElement;
    cost: HTMLTableCellElement;
    duration: HTMLTableCellElement;
    responseTime: HTMLTableCellElement;
    hitRate: HTMLTableCellElement;
    cacheBadge: HTMLSpanElement;
    httpCode: HTMLSpanElement;
    viewBtn: HTMLButtonElement;
}

export const logsRowSlots: LogsRowSlot[] = [];

// viewBtnLogMap: 渲染时把当前 lite 日志捕获到「查看」按钮 DOM 上(WeakMap, key=按钮元素),
// 使点击不再依赖「该 id 点击时刻仍在 state.allRequests 里」这一脆弱前提——OCR 行等高频
// 覆盖/已挤出 50 窗口/旧残留场景下 data-log-id 查表会落空,改用捕获对象直接开弹窗根治。
// WeakMap:按钮 DOM 被回收时条目自动释放,驻留内存恒等于可见按钮数,无泄漏。
export const viewBtnLogMap = new WeakMap<HTMLButtonElement, any>();

export function buildLogsRowSlot(): LogsRowSlot {
    const tr = document.createElement('tr');
    tr.className = 'hover:bg-slate-50/80 dark:hover:bg-white/[0.04] transition-colors border-b border-outline-variant/15';

    const makeTd = (className: string): HTMLTableCellElement => {
        const td = document.createElement('td');
        td.className = className;
        return td;
    };
    const makeSpan = (className: string): HTMLSpanElement => {
        const s = document.createElement('span');
        s.className = className;
        return s;
    };

    const timestamp = makeTd('py-3 px-3 text-slate-500 dark:text-slate-400 font-data-mono text-[11px] whitespace-nowrap');

    const methodHostCell = makeTd('py-3 px-3 font-data-mono truncate');
    const methodHostDiv = document.createElement('div');
    methodHostDiv.className = 'flex items-center min-w-0';
    const method = makeSpan('inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-bold font-mono tracking-wide mr-1.5 flex-none');
    const host = makeSpan('text-[11.5px] text-slate-700 dark:text-slate-200 truncate font-medium');
    methodHostDiv.appendChild(method);
    methodHostDiv.appendChild(host);
    methodHostCell.appendChild(methodHostDiv);

    const path = makeTd('py-3 px-3 text-slate-500 dark:text-slate-400 font-data-mono text-[11px] truncate');
    const sessionId = makeTd('py-3 px-2 text-slate-400 dark:text-slate-500 font-data-mono text-[11px] truncate');

    const modelCell = makeTd('py-3 px-3 min-w-0');
    const modelDiv = document.createElement('div');
    modelDiv.className = 'flex flex-col min-w-0 gap-0.5';
    const modelNameRow = document.createElement('div');
    modelNameRow.className = 'flex items-center gap-1.5 min-w-0';
    const modelName = makeSpan('font-semibold text-[11.5px] text-slate-800 dark:text-slate-100 truncate');
    const reasoningBadge = makeSpan('inline-flex items-center px-1.5 py-0.2 rounded text-[9px] font-bold bg-purple-50 text-purple-700 border border-purple-200 dark:bg-purple-950/40 dark:text-purple-300 dark:border-purple-800/40 flex-none');
    reasoningBadge.style.display = 'none';
    const nvidiaBadge = makeSpan('inline-flex items-center px-1.5 py-0.2 rounded text-[9px] font-bold bg-emerald-50 text-emerald-700 border border-emerald-200 dark:bg-emerald-950/30 dark:text-emerald-400 dark:border-emerald-900/40 flex-none');
    nvidiaBadge.style.display = 'none';
    nvidiaBadge.textContent = 'NVIDIA';
    modelNameRow.appendChild(modelName);
    modelNameRow.appendChild(reasoningBadge);
    modelNameRow.appendChild(nvidiaBadge);
    const account = makeSpan('text-[10px] text-slate-400 dark:text-slate-500 font-data-mono truncate');
    modelDiv.appendChild(modelNameRow);
    modelDiv.appendChild(account);
    modelCell.appendChild(modelDiv);

    const tokensCell = makeTd('py-3 px-3 text-left font-data-mono');
    const tokensDiv = document.createElement('div');
    tokensDiv.className = 'flex flex-col items-start gap-0.5';
    const inTokens = makeSpan('text-[10px] text-slate-500 dark:text-slate-400 font-medium');
    const outTokens = makeSpan('text-[11px] text-emerald-600 dark:text-emerald-400 font-semibold');
    tokensDiv.appendChild(inTokens);
    tokensDiv.appendChild(outTokens);
    tokensCell.appendChild(tokensDiv);

    const cost = makeTd('py-3 px-2 text-left font-data-mono text-emerald-600 dark:text-emerald-400 font-bold text-[11.5px]');
    const responseTime = makeTd('py-3 px-2 text-left font-data-mono text-[11.5px]');
    const duration = makeTd('py-3 px-2 text-left font-data-mono text-[11.5px]');
    const hitRate = makeTd('py-3 px-2 text-left font-data-mono text-[11.5px]');

    const statusCell = makeTd('py-3 px-2 text-left');
    const statusDiv = document.createElement('div');
    statusDiv.className = 'inline-flex flex-col items-start gap-0.5';
    const cacheBadge = makeSpan('inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-bold tracking-wide');
    const httpCode = makeSpan('text-[10px] font-mono font-bold leading-tight');
    statusDiv.appendChild(cacheBadge);
    statusDiv.appendChild(httpCode);
    statusCell.appendChild(statusDiv);

    const btnCell = makeTd('py-3 px-2 text-left');
    const viewBtn = document.createElement('button');
    viewBtn.className = 'inline-flex items-center justify-center px-2 py-1 text-[11px] font-medium bg-primary/10 hover:bg-primary/20 text-primary dark:text-primary-fixed-dim rounded-md border border-primary/20 hover:border-primary/40 transition-all cursor-pointer view-details-btn shadow-2xs';
    btnCell.appendChild(viewBtn);

    tr.appendChild(timestamp);
    tr.appendChild(methodHostCell);
    tr.appendChild(path);
    tr.appendChild(sessionId);
    tr.appendChild(modelCell);
    tr.appendChild(tokensCell);
    tr.appendChild(cost);
    tr.appendChild(responseTime);
    tr.appendChild(duration);
    tr.appendChild(hitRate);
    tr.appendChild(statusCell);
    tr.appendChild(btnCell);

    return { tr, timestamp, method, host, methodHostCell, path, sessionId, modelName, reasoningBadge, nvidiaBadge, account, modelCell, inTokens, outTokens, cost, responseTime, duration, hitRate, cacheBadge, httpCode, viewBtn };
}

export function updateLogsRowSlot(slot: LogsRowSlot, log: any, dict: any) {
    slot.timestamp.textContent = log.timestamp;

    const method = log.method || 'POST';
    slot.method.textContent = method;
    if (method === 'GET') {
        slot.method.className = 'inline-flex items-center px-1.5 py-0.5 rounded text-[9.5px] font-bold font-mono tracking-wide mr-1.5 flex-none bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 border border-emerald-500/20';
    } else {
        slot.method.className = 'inline-flex items-center px-1.5 py-0.5 rounded text-[9.5px] font-bold font-mono tracking-wide mr-1.5 flex-none bg-sky-500/10 text-sky-600 dark:text-sky-400 border border-sky-500/20';
    }
    slot.host.textContent = log.host || '-';
    slot.methodHostCell.setAttribute('title', `${method} ${log.host || ''}`);

    slot.path.textContent = log.path || '-';
    slot.path.setAttribute('title', log.path || '');

    slot.sessionId.textContent = log.sessionId || '-';
    slot.sessionId.setAttribute('title', log.sessionId || '-');

    slot.modelName.textContent = log.model || '-';
    slot.modelCell.setAttribute('title', formatDisplayModel(log.model, log.reasoningEffort));

    // 思考等级微标签 (high / max / low 等)
    if (log.reasoningEffort && log.reasoningEffort !== 'none') {
        slot.reasoningBadge.style.display = '';
        slot.reasoningBadge.textContent = log.reasoningEffort;
        slot.reasoningBadge.setAttribute('title', `思考等级: ${log.reasoningEffort}`);
    } else {
        slot.reasoningBadge.style.display = 'none';
    }

    // NVIDIA 号池专属标签
    if (log.family === 'nvidia') {
        slot.nvidiaBadge.style.display = '';
    } else {
        slot.nvidiaBadge.style.display = 'none';
    }

    if (log.account) {
        slot.account.textContent = log.account;
        slot.account.setAttribute('title', log.account);
        slot.account.className = 'text-[10px] text-slate-400 dark:text-slate-500 font-data-mono truncate mt-0.5';
    } else {
        slot.account.textContent = state.currentLanguage === 'zh' ? '直连分发' : 'Direct';
        slot.account.className = 'text-[10px] text-slate-400 dark:text-slate-500 font-data-mono truncate mt-0.5';
    }

    const inVal = typeof log.inTokens === 'number' ? log.inTokens.toLocaleString() : '0';
    const outVal = typeof log.outTokens === 'number' ? log.outTokens.toLocaleString() : '0';
    slot.inTokens.textContent = `↑ ${inVal}`;
    slot.inTokens.setAttribute('title', `${dict.colInputTokens || '输入 Tokens'}: ${inVal}`);
    slot.outTokens.textContent = `↓ ${outVal}`;
    slot.outTokens.setAttribute('title', `${dict.colOutputTokens || '输出 Tokens'}: ${outVal}`);

    slot.cost.textContent = `$${(log.cost || 0).toFixed(6)}`;

    // 响应时间 (firstByteMs / TTFT 首字到达时长，对应表头「响应时间」)
    slot.responseTime.textContent = formatDuration(log.firstByteMs);
    if (log.firstByteMs && log.firstByteMs >= 15000) {
        slot.responseTime.className = 'py-3 px-2 text-left font-data-mono text-[11.5px] text-rose-500 dark:text-rose-400 font-bold';
    } else if (log.firstByteMs && log.firstByteMs >= 5000) {
        slot.responseTime.className = 'py-3 px-2 text-left font-data-mono text-[11.5px] text-amber-600 dark:text-amber-400 font-semibold';
    } else if (log.firstByteMs && log.firstByteMs <= 1000) {
        slot.responseTime.className = 'py-3 px-2 text-left font-data-mono text-[11.5px] text-emerald-600 dark:text-emerald-400 font-semibold';
    } else {
        slot.responseTime.className = 'py-3 px-2 text-left font-data-mono text-[11.5px] text-slate-700 dark:text-slate-300';
    }

    // 耗时 (durationMs / 流式传输耗时，对应表头「耗时」)
    slot.duration.textContent = formatDuration(log.durationMs);
    if (log.durationMs && log.durationMs >= 10000) {
        slot.duration.className = 'py-3 px-2 text-left font-data-mono text-[11.5px] text-rose-500 dark:text-rose-400 font-bold';
    } else if (log.durationMs && log.durationMs >= 3000) {
        slot.duration.className = 'py-3 px-2 text-left font-data-mono text-[11.5px] text-amber-600 dark:text-amber-400 font-semibold';
    } else {
        slot.duration.className = 'py-3 px-2 text-left font-data-mono text-[11.5px] text-slate-500 dark:text-slate-400';
    }

    // 缓存率
    const hitRateVal = log.inTokens > 0 ? (log.cachedTokens / log.inTokens * 100).toFixed(1) : '0.0';
    slot.hitRate.textContent = `${hitRateVal}%`;
    if (log.cachedTokens > 0) {
        slot.hitRate.className = 'py-3 px-2 text-left font-data-mono text-[11.5px] text-emerald-600 dark:text-emerald-400 font-bold';
    } else {
        slot.hitRate.className = 'py-3 px-2 text-left font-data-mono text-[11.5px] text-slate-400 dark:text-slate-500';
    }

    // 状态与 HTTP 码
    let statusClass = 'bg-slate-500/10 text-slate-600 dark:text-slate-400 border border-slate-500/20';
    let statusLabel = dict.statusMiss || 'MISS';
    if (log.cacheStatus === 'HIT') {
        statusClass = 'bg-emerald-500/10 text-emerald-700 dark:text-emerald-400 border border-emerald-500/25';
        statusLabel = dict.statusHit || 'HIT';
    } else if (log.cacheStatus === 'NONE') {
        statusClass = 'bg-purple-500/10 text-purple-700 dark:text-purple-300 border border-purple-500/25';
        statusLabel = dict.statusNone || 'NONE';
    }
    slot.cacheBadge.textContent = statusLabel;
    slot.cacheBadge.className = `inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-bold tracking-wide ${statusClass}`;

    const retryN: number = log.__retryN || 0;
    if (retryN > 1) {
        slot.cacheBadge.textContent = `${statusLabel} ⟳${retryN}`;
        const tmpl = dict.retryMerged || 'Client retried {n} time(s); merged';
        slot.cacheBadge.title = tmpl.replace('{n}', String(retryN));
    } else {
        slot.cacheBadge.title = '';
    }

    const isError = log.statusCode >= 400;
    const statusColor = isError ? 'text-rose-500 font-bold' : 'text-emerald-600 dark:text-emerald-400 font-semibold';
    slot.httpCode.textContent = `HTTP ${log.statusCode}`;
    slot.httpCode.className = `text-[9.5px] font-mono leading-tight ${statusColor}`;

    // 如果是错误行，整行高亮微红
    if (isError) {
        slot.tr.className = 'bg-rose-500/[0.03] hover:bg-rose-500/[0.07] transition-colors border-b border-rose-500/20';
    } else {
        slot.tr.className = 'hover:bg-slate-50/80 dark:hover:bg-white/[0.04] transition-colors border-b border-outline-variant/15';
    }

    slot.viewBtn.setAttribute('data-log-id', log.id);
    viewBtnLogMap.set(slot.viewBtn, log);
    slot.viewBtn.textContent = state.currentLanguage === 'zh' ? '查看' : 'View';
}
