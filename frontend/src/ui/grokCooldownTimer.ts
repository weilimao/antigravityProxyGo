/**
 * Grok 冷却秒级翻牌定时器：从 accountsController.ts 抽离的独立模块。
 *
 * 高内聚：单一定时器(grokCooldownTimer)以 1s tick 遍历页内所有 .grok-cooldown-tick 节点,
 * 据其 data-until 用 getNvidiaCooldownRemaining 重算文案与配色;全部过期或离开 accounts 视图即停表。
 * 由 accountsRenderer.renderAccounts 渲染完毕时经 accountsController re-export 的
 * ensureGrokCooldownTimer() 幂等启动(已在渲染中即不重复 setInterval)。
 *
 * 决策 A:Grok 冷却 tick 文案复用 nvidia 命名空间的 nvidiaCooldownSeconds/Minutes/Hours/Expired 键
 * (其值为池无关通用文案),故本模块直接 import getNvidiaCooldownRemaining 复用同一文案计算函数;
 * 仅 tick 节点 class 用 grok-cooldown-tick(与 nvidia-cooldown-tick 物理隔离,两 timer 各扫各的)。
 * 依赖：state.activeView、accountCardHelpers.getNvidiaCooldownRemaining(只读)。
 */
import state from './dashboardState';
import { getNvidiaCooldownRemaining } from './accountCardHelpers';

// Grok 冷却秒级翻牌倒计时定时器：单实例、自驱动、自停止。
// 由 renderAccounts 末尾的 ensureGrokCooldownTimer 启动；tick 每秒只更新
// .grok-cooldown-tick 节点的 textContent + className，零 IPC、零整列重渲染。
let grokCooldownTimer: any = null;

export function ensureGrokCooldownTimer() {
    if (grokCooldownTimer) return;
    grokCooldownTimer = setInterval(tickGrokCooldown, 1000);
}

export function stopGrokCooldownTimer() {
    if (grokCooldownTimer) {
        clearInterval(grokCooldownTimer);
        grokCooldownTimer = null;
    }
}

function tickGrokCooldown() {
    // 切走账号池视图则自停（renderAccounts 切回时会重新 ensure）。
    if (state.activeView !== 'accounts') {
        stopGrokCooldownTimer();
        return;
    }
    let anyActive = false;
    const ticks = document.querySelectorAll('.grok-cooldown-tick');
    ticks.forEach((el: any) => {
        const until = parseInt(el.getAttribute('data-until') || '0', 10);
        const { text, expired, seconds } = getNvidiaCooldownRemaining(until);
        el.textContent = text;
        const colorCls = expired
            ? 'text-slate-500'
            : seconds <= 10 ? 'text-red-500 animate-pulse'
            : seconds <= 60 ? 'text-amber-600'
            : 'text-amber-500/80';
        el.className = `grok-cooldown-tick ${colorCls}`;
        if (!expired) anyActive = true;
    });
    // 全部到期或无冷却账号 → 自停，下次 renderAccounts 再 ensure。
    if (!anyActive) stopGrokCooldownTimer();
}
