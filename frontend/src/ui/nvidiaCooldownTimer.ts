/**
 * NVIDIA 冷却秒级翻牌定时器：从 accountsController.ts 抽离的独立模块。
 *
 * 高内聚：单一定时器(nvidiaCooldownTimer)以 1s tick 遍历页内所有 .nvidia-cooldown-tick 节点,
 * 据其 data-until 用 getNvidiaCooldownRemaining 重算文案与配色;全部过期或离开 accounts 视图即停表。
 * 由 accountsRenderer.renderAccounts 渲染完毕时经 accountsController re-export 的
 * ensureNvidiaCooldownTimer() 幂等启动(已在渲染中即不重复 setInterval)。
 * 依赖：state.activeView、accountsRenderer.getNvidiaCooldownRemaining(只读)。
 */
import state from './dashboardState';
import { getNvidiaCooldownRemaining } from './accountsRenderer';

// NVIDIA 冷却秒级翻牌倒计时定时器：单实例、自驱动、自停止。
// 由 renderAccounts 末尾的 ensureNvidiaCooldownTimer 启动；tick 每秒只更新
// .nvidia-cooldown-tick 节点的 textContent + className，零 IPC、零整列重渲染。
let nvidiaCooldownTimer: any = null;

export function ensureNvidiaCooldownTimer() {
    if (nvidiaCooldownTimer) return;
    nvidiaCooldownTimer = setInterval(tickNvidiaCooldown, 1000);
}

export function stopNvidiaCooldownTimer() {
    if (nvidiaCooldownTimer) {
        clearInterval(nvidiaCooldownTimer);
        nvidiaCooldownTimer = null;
    }
}

function tickNvidiaCooldown() {
    // 切走账号池视图则自停（renderAccounts 切回时会重新 ensure）。
    if (state.activeView !== 'accounts') {
        stopNvidiaCooldownTimer();
        return;
    }
    let anyActive = false;
    const ticks = document.querySelectorAll('.nvidia-cooldown-tick');
    ticks.forEach((el: any) => {
        const until = parseInt(el.getAttribute('data-until') || '0', 10);
        const { text, expired, seconds } = getNvidiaCooldownRemaining(until);
        el.textContent = text;
        const colorCls = expired
            ? 'text-slate-500'
            : seconds <= 10 ? 'text-red-500 animate-pulse'
            : seconds <= 60 ? 'text-amber-600'
            : 'text-amber-500/80';
        el.className = `nvidia-cooldown-tick ${colorCls}`;
        if (!expired) anyActive = true;
    });
    // 全部到期或无冷却账号 → 自停，下次 renderAccounts 再 ensure。
    if (!anyActive) stopNvidiaCooldownTimer();
}
