/**
 * dashboardBenchmarkTimer.ts: 模型响应测速卡片秒级倒计时定时器模块。
 *
 * 高内聚、轻量单例架构（对齐 nvidiaCooldownTimer.ts / grokCooldownTimer.ts）：
 * 1. 提供纯计算函数 computeBenchmarkCountdown，涵盖所有边界状态判定，供单元测试端到端覆盖。
 * 2. 单例自驱动定时器（benchmarkTimer）以 1s tick 精准计算下次请求剩余时间。
 * 3. 仅微量修改顶部倒计时胶囊与底部 Meta 文本节点，零 IPC 通信、零整卡重绘。
 * 4. 视图感知：离开 dashboard 视图时自动挂起停表，切回时由路由自动唤醒。
 */
import state from './dashboardState';
import i18n from '../shared/i18n';
import { ipcRenderer } from '../shared/ipc';

export interface BenchmarkCountdownInfo {
    state: 'running' | 'disabled' | 'no_models' | 'imminent' | 'counting';
    remainingSeconds: number;
    chipText: string;
    chipTitle: string;
    chipVisible: boolean;
    metaText: string;
    isImminent: boolean;
    isRunning: boolean;
}

function getDict(): any {
    return (i18n as any)[state.currentLanguage] || {};
}

function el(id: string): HTMLElement | null {
    return document.getElementById(id);
}

/**
 * 纯函数：根据测速载荷推算当前倒计时信息。
 * @param payload 后端推送或 state 缓存的 benchmarkData
 * @param nowMs 当前时间戳毫秒数
 * @param dict 当前语言字典
 */
export function computeBenchmarkCountdown(payload: any, nowMs: number, dict: any): BenchmarkCountdownInfo {
    const d = dict || {};
    const p = payload || {};
    const cfg = p.config || {};
    const enabled = !!cfg.enabled;
    const models = Array.isArray(cfg.models) ? cfg.models : [];
    const isRunning = !!p.running;

    // 1. 测速运行中
    if (isRunning) {
        const text = d.benchmarkCountdownRunning || '测速中...';
        return {
            state: 'running',
            remainingSeconds: 0,
            chipText: text,
            chipTitle: text,
            chipVisible: true,
            metaText: ` · ${text}`,
            isImminent: false,
            isRunning: true,
        };
    }

    // 2. 定时未启用 (手动模式)
    if (!enabled) {
        const text = d.benchmarkCountdownDisabled || '定时未启用';
        return {
            state: 'disabled',
            remainingSeconds: 0,
            chipText: '',
            chipTitle: text,
            chipVisible: false,
            metaText: ` · ${text}`,
            isImminent: false,
            isRunning: false,
        };
    }

    // 3. 未配置模型
    if (models.length === 0) {
        const text = d.benchmarkCountdownNoModels || '未配置模型';
        return {
            state: 'no_models',
            remainingSeconds: 0,
            chipText: '',
            chipTitle: text,
            chipVisible: false,
            metaText: ` · ${text}`,
            isImminent: false,
            isRunning: false,
        };
    }

    // 4. 解析触发间隔与上次执行时间
    const intervalMinutes = Math.max(1, Number(cfg.intervalMinutes) || 5);
    const intervalMs = intervalMinutes * 60 * 1000;
    let lastRunMs = 0;
    if (p.lastRun && !String(p.lastRun).startsWith('0001-')) {
        try {
            const parsed = new Date(p.lastRun);
            if (!isNaN(parsed.getTime())) {
                lastRunMs = parsed.getTime();
            }
        } catch {
            lastRunMs = 0;
        }
    }

    // 5. 从未运行过或调度清空 lastRun（刚保存）：即刻/即将首次触发
    if (lastRunMs === 0) {
        const text = d.benchmarkCountdownImminent || '即将请求...';
        return {
            state: 'imminent',
            remainingSeconds: 0,
            chipText: text,
            chipTitle: text,
            chipVisible: true,
            metaText: ` · ${text}`,
            isImminent: true,
            isRunning: false,
        };
    }

    // 6. 计算下次触发时刻与剩余秒数
    const nextRunMs = lastRunMs + intervalMs;
    const diffSec = Math.floor((nextRunMs - nowMs) / 1000);

    // 倒计时已归零，等待调度器 15s 节拍触发
    if (diffSec <= 0) {
        const text = d.benchmarkCountdownImminent || '即将请求...';
        return {
            state: 'imminent',
            remainingSeconds: 0,
            chipText: text,
            chipTitle: text,
            chipVisible: true,
            metaText: ` · ${text}`,
            isImminent: true,
            isRunning: false,
        };
    }

    // 正常倒计时中
    const minutes = Math.floor(diffSec / 60);
    const seconds = diffSec % 60;
    const timeStr = `${String(minutes).padStart(2, '0')}:${String(seconds).padStart(2, '0')}`;
    const tpl = d.benchmarkCountdownChipTitle || '距离下一次自动测速请求还剩 {time}';
    const chipTitle = tpl.replace('{time}', timeStr);
    const prefix = d.benchmarkCountdownMetaPrefix || '下次请求';

    return {
        state: 'counting',
        remainingSeconds: diffSec,
        chipText: timeStr,
        chipTitle,
        chipVisible: true,
        metaText: ` · ${prefix}: ${timeStr}`,
        isImminent: diffSec <= 10,
        isRunning: false,
    };
}

/**
 * 纯函数：判定是否应触发超期对齐自愈（倒计时归零时防抖拉取后端最新状态）
 */
export function shouldTriggerAutoSync(info: BenchmarkCountdownInfo, lastSyncMs: number, nowMs: number): boolean {
    if (info.state !== 'imminent' || info.isRunning) return false;
    return (nowMs - lastSyncMs) >= 5000;
}

let benchmarkTimer: any = null;
let lastSyncTimeMs = 0;
let isSyncing = false;
let hasBoundWindowEvents = false;

/**
 * 主动从后端拉取 benchmark 最新载荷并驱动刷新
 */
export async function syncBenchmarkLatest(): Promise<void> {
    if (isSyncing) return;
    isSyncing = true;
    try {
        const res = await ipcRenderer.invoke('benchmark:get');
        if (res && res.success) {
            state.benchmarkData = res;
            updateBenchmarkCountdownDom(res);
        }
    } catch (e) {
        console.warn('[BenchmarkTimer] sync benchmark:get failed', e);
    } finally {
        isSyncing = false;
    }
}

/**
 * 绑定窗口激活与可见性自愈监听
 */
export function bindBenchmarkWindowEvents(): void {
    if (hasBoundWindowEvents || typeof window === 'undefined') return;
    hasBoundWindowEvents = true;
    const handleResume = () => {
        if (state.activeView === 'dashboard') {
            syncBenchmarkLatest();
        }
    };
    window.addEventListener('focus', handleResume);
    document.addEventListener('visibilitychange', () => {
        if (document.visibilityState === 'visible') {
            handleResume();
        }
    });
}

/**
 * 幂等启动秒级倒计时定时器
 */
export function ensureBenchmarkTimer(): void {
    bindBenchmarkWindowEvents();
    if (benchmarkTimer) return;
    tickBenchmarkTimer();
    benchmarkTimer = setInterval(tickBenchmarkTimer, 1000);
}

/**
 * 停止秒级倒计时定时器（切出仪表盘视图时自停）
 */
export function stopBenchmarkTimer(): void {
    if (benchmarkTimer) {
        clearInterval(benchmarkTimer);
        benchmarkTimer = null;
    }
}

/**
 * 每秒刷新倒计时 DOM 节点并执行超期自愈判定
 */
export function tickBenchmarkTimer(): void {
    if (state.activeView !== 'dashboard') {
        stopBenchmarkTimer();
        return;
    }
    updateBenchmarkCountdownDom(state.benchmarkData);

    // 超期防抖自愈拉取: 当处于 imminent 态且后端未广播 running 时, 每 5s 主动查询一次避免事件丢失导致假死
    const dict = getDict();
    const now = Date.now();
    const info = computeBenchmarkCountdown(state.benchmarkData, now, dict);
    if (shouldTriggerAutoSync(info, lastSyncTimeMs, now)) {
        lastSyncTimeMs = now;
        syncBenchmarkLatest();
    }
}

/**
 * 根据最新载荷精准更新倒计时 DOM 节点
 */
export function updateBenchmarkCountdownDom(payload: any): void {
    const chip = el('benchmarkCountdownChip');
    const chipText = el('benchmarkCountdownChipText');
    const chipIcon = el('benchmarkCountdownChipIcon');
    const meta = el('benchmarkCountdownMeta');

    const dict = getDict();
    const info = computeBenchmarkCountdown(payload, Date.now(), dict);

    if (chip && chipText) {
        chip.classList.toggle('hidden', !info.chipVisible);
        chipText.textContent = info.chipText;
        chip.title = info.chipTitle;

        if (chipIcon) {
            chipIcon.classList.toggle('animate-spin', info.isRunning);
            chipIcon.textContent = info.isRunning ? 'progress_activity' : 'schedule';
        }

        // 样式适配
        if (info.isRunning) {
            chip.className = 'text-[10px] px-2 py-0.5 bg-amber-500/10 border border-amber-500/20 text-amber-600 dark:text-amber-400 rounded-full font-semibold font-mono flex items-center gap-1';
        } else if (info.isImminent) {
            chip.className = 'text-[10px] px-2 py-0.5 bg-rose-500/10 border border-rose-500/30 text-rose-600 dark:text-rose-400 rounded-full font-bold font-mono flex items-center gap-1 animate-pulse';
        } else {
            chip.className = 'text-[10px] px-2 py-0.5 bg-amber-50 dark:bg-amber-950/30 border border-amber-500/20 text-amber-600 dark:text-amber-400 rounded-full font-semibold font-mono flex items-center gap-1';
        }
    }

    if (meta) {
        meta.textContent = info.metaText;
        meta.classList.toggle('hidden', !info.metaText);
        if (info.isImminent) {
            meta.className = 'font-mono text-rose-500 font-semibold animate-pulse';
        } else if (info.isRunning) {
            meta.className = 'font-mono text-amber-500 font-semibold';
        } else {
            meta.className = 'font-mono text-amber-600 dark:text-amber-400/90 font-medium';
        }
    }
}
