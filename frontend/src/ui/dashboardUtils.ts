/**
 * dashboardUtils.ts: 仪表盘小工具(纯函数,无 DOM/模块状态依赖)。
 *
 * formatDuration 从 dashboard.ts 抽离(原 L136-140),被详情弹窗(dashboardModal)与
 * 日志行池(dashboardLogs)复用,作为叶子 util 避免 sibling 互引。
 */
export function formatDuration(ms: number | undefined): string {
    if (ms === undefined || ms === null || ms === 0) return '-';
    if (ms < 1000) return `${ms}ms`;
    return `${(ms / 1000).toFixed(2)}s`;
}
