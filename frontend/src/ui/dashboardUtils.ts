/**
 * dashboardUtils.ts: 仪表盘小工具(纯函数,无 DOM/模块状态依赖)。
 *
 * formatDuration 从 dashboard.ts 抽离(原 L136-140),被详情弹窗(dashboardModal)与
 * 日志行池(dashboardLogs)复用,作为叶子 util 避免 sibling 互引。
 * formatDisplayModel 为「模型名 + 命中思考等级后缀」统一格式化器,被请求日志表格行
 * (dashboardLogs.updateLogsRowSlot)与详情弹窗(dashboardModal.showModal)共同复用,
 * 杜绝两处展示逻辑漂移。
 */
export function formatDuration(ms: number | undefined | null): string {
    if (ms === undefined || ms === null || typeof ms !== 'number' || isNaN(ms) || ms < 0) return '-';
    if (ms < 1000) {
        const rounded = Number(ms.toFixed(2));
        if (rounded >= 1000) {
            return `${(rounded / 1000).toFixed(2)}s`;
        }
        return `${rounded}ms`;
    }
    return `${(ms / 1000).toFixed(2)}s`;
}

/**
 * formatDisplayModel 把「上游命中思考等级」作为后缀拼到模型名上, 供请求日志「模型」列与
 * 详情弹窗「所用模型」字段统一渲染。后缀取「命中上游」的映射折叠值:
 *   - NVIDIA-NIM deepseek 模式 low/medium→high、max→max;
 *   - Grok off→none、on→档(high/low/medium);
 *   - Other 走官方 OpenAI 取值集(max→high)。
 *
 * 拼接规则:
 *   - model 为空(日志缺失)→ 兜底 '-',不拼后缀(供详情弹窗 fallback);
 *   - reasoningEffort 为空串(客户端未开思考/全局关/上游无该概念)→ 不渲染后缀, 仅返回 model;
 *   - reasoningEffort === 'none'(Grok 显式关闭思考, 见 grokApplyThinkingToChat 三态)→
 *     不渲染后缀, 仅返回 model, 避免「grok-4.3(none)」这种对用户无信息量的噪音后缀;
 *   - 其余 → `${model}(${reasoningEffort})`, 如「z-ai/glm-5.2(max)」。
 *
 * 纯函数, 无副作用, 可直接用于 textContent 赋值(安全无需转义)。
 */
export function formatDisplayModel(model: string | undefined | null, reasoningEffort: string | undefined | null): string {
    if (!model) return '-';
    const suffix = reasoningEffort && reasoningEffort !== 'none' ? reasoningEffort : '';
    return suffix ? `${model}(${suffix})` : model;
}
