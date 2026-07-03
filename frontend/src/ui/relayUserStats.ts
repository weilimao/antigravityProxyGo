import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';
import i18n from '../shared/i18n';
import { formatTokenCount } from './relayPackages';

(window as any)._relayViewUserStats = async (id: string) => {
    try {
        const res = await ipcRenderer.invoke('relay:get-user-stats', id);
        const modal = document.getElementById('relayUserStatsModal');
        const content = document.getElementById('relayUserStatsContent');
        const dict = i18n[state.currentLanguage] || {};
        const isZH = state.currentLanguage === 'zh';
        if (modal && content) {
            if (!res) {
                content.innerHTML = `<div class="text-[13px] text-outline/60 text-center py-4">${dict.relayStatsEmpty || '暂无数据记录'}</div>`;
            } else {
                const stats = res.stats || {};
                const user = res.user || {};
                const totalTokens = (stats.totalInputTokens || 0) + (stats.totalOutputTokens || 0);
                
                let html = `
                    <div class="grid grid-cols-2 gap-3 mb-4">
                        <div class="bg-slate-50 dark:bg-slate-800/50 p-3 rounded-lg border border-outline-variant/20">
                            <div class="text-[11px] text-outline/60 mb-1">${dict.relayStatsTotalRequests || '总请求数'}</div>
                            <div class="text-[16px] font-bold text-on-surface dark:text-white">${stats.totalRequests || 0}</div>
                        </div>
                        <div class="bg-slate-50 dark:bg-slate-800/50 p-3 rounded-lg border border-outline-variant/20">
                            <div class="text-[11px] text-outline/60 mb-1">${dict.relayStatsTotalCost || '总花费'}</div>
                            <div class="text-[16px] font-bold text-emerald-500">$${(stats.totalCost || 0).toFixed(4)}</div>
                        </div>
                        <div class="bg-slate-50 dark:bg-slate-800/50 p-3 rounded-lg border border-outline-variant/20 col-span-2 flex items-center justify-between">
                            <div>
                                <div class="text-[11px] text-outline/60 mb-1">${dict.relayStatsTotalTokens || '总 Token 数'}</div>
                                <div class="text-[16px] font-bold text-indigo-500">${totalTokens}</div>
                            </div>
                            <div class="text-right">
                                <div class="text-[11px] text-outline/60 mb-1">${dict.relayStatsCacheHit || '缓存命中'}</div>
                                <div class="text-[16px] font-bold text-teal-500">${stats.totalCachedTokens || 0}</div>
                            </div>
                        </div>
                        <div class="bg-slate-50 dark:bg-slate-800/50 p-3 rounded-lg border border-outline-variant/20">
                            <div class="text-[11px] text-outline/60 mb-1">${dict.relayStatsInputTokens || '输入 Token'}</div>
                            <div class="text-[16px] font-bold text-blue-500">${stats.totalInputTokens || 0}</div>
                        </div>
                        <div class="bg-slate-50 dark:bg-slate-800/50 p-3 rounded-lg border border-outline-variant/20">
                            <div class="text-[11px] text-outline/60 mb-1">${dict.relayStatsOutputTokens || '输出 Token'}</div>
                            <div class="text-[16px] font-bold text-purple-500">${stats.totalOutputTokens || 0}</div>
                        </div>
                    </div>
                `;

                if (user && user.quotas) {
                    const q = user.quotas;
                    const renderFamilyQuota = (familyTitle: string, quota: any, lifetimeUsed: number, hourlyUsed: number, dailyUsed: number, hourlyResetAt?: string, dailyResetAt?: string) => {
                        if (!quota || (!quota.enableFixed && !quota.enableHourly && !quota.enableDaily)) {
                            return `
                                <div class="bg-white/50 dark:bg-white/5 p-3 rounded-lg border border-outline-variant/20 text-[12px] mb-3">
                                    <div class="font-semibold text-on-surface dark:text-white mb-1">${familyTitle} <span class="text-[10px] text-red-500 font-normal bg-red-500/10 px-2 py-0.5 rounded">${dict.relayNoPermission || '无权限'}</span></div>
                                    <div class="text-outline/70 text-[11px]">${isZH ? `当前已用总计: ${formatTokenCount(lifetimeUsed)} Token` : `Total used so far: ${formatTokenCount(lifetimeUsed)} Tokens`}</div>
                                </div>
                            `;
                        }

                        let items: string[] = [];
                        if (quota.enableFixed) {
                            const remain = Math.max(0, quota.fixedTokens - lifetimeUsed);
                            const pct = Math.min(100, Math.round((lifetimeUsed / quota.fixedTokens) * 100));
                            items.push(`
                                <div class="mb-2">
                                    <div class="flex justify-between text-[11px] mb-1">
                                        <span class="text-outline">${isZH ? `总配额限制 (${formatTokenCount(quota.fixedTokens)})` : `Fixed quota limit (${formatTokenCount(quota.fixedTokens)})`}</span>
                                        <span class="font-bold ${remain > 0 ? 'text-indigo-500' : 'text-red-500'}">${dict.relayRemain || '剩余:'} ${formatTokenCount(remain)}</span>
                                    </div>
                                    <div class="w-full bg-slate-200 dark:bg-slate-700 h-1.5 rounded-full overflow-hidden">
                                        <div class="bg-indigo-500 h-full rounded-full" style="width: ${pct}%"></div>
                                    </div>
                                </div>
                            `);
                        }
                        if (quota.enableHourly) {
                            const remain = Math.max(0, quota.hourlyTokens - hourlyUsed);
                            const pct = Math.min(100, Math.round((hourlyUsed / quota.hourlyTokens) * 100));
                            let resetStr = '';
                            if (hourlyUsed > 0 && hourlyResetAt) {
                                const d = new Date(hourlyResetAt);
                                const timeStr = `${d.getHours().toString().padStart(2, '0')}:${d.getMinutes().toString().padStart(2, '0')}`;
                                let resetText = dict.relayExpectedRefresh || "预计 {time} 刷新";
                                resetText = resetText.replace('{time}', timeStr);
                                resetStr = ` <span class="text-[10px] text-primary bg-primary/10 px-1.5 py-0.5 rounded ml-1 font-normal">${resetText}</span>`;
                            }
                            items.push(`
                                <div class="mb-2">
                                    <div class="flex justify-between text-[11px] mb-1 items-center">
                                        <span class="text-outline flex items-center">${isZH ? `${quota.hourlyHours}小时级限额` : `${quota.hourlyHours}-hour limit`} (${formatTokenCount(quota.hourlyTokens)})${resetStr}</span>
                                        <span class="font-bold ${remain > 0 ? 'text-primary' : 'text-red-500'}">${dict.relayRemain || '剩余:'} ${formatTokenCount(remain)}</span>
                                    </div>
                                    <div class="w-full bg-slate-200 dark:bg-slate-700 h-1.5 rounded-full overflow-hidden">
                                        <div class="bg-primary h-full rounded-full" style="width: ${pct}%"></div>
                                    </div>
                                </div>
                            `);
                        }
                        if (quota.enableDaily) {
                            const remain = Math.max(0, quota.dailyTokens - dailyUsed);
                            const pct = Math.min(100, Math.round((dailyUsed / quota.dailyTokens) * 100));
                            let resetStr = '';
                            if (dailyUsed > 0 && dailyResetAt) {
                                const d = new Date(dailyResetAt);
                                const month = (d.getMonth() + 1).toString().padStart(2, '0');
                                const day = d.getDate().toString().padStart(2, '0');
                                const hours = d.getHours().toString().padStart(2, '0');
                                const minutes = d.getMinutes().toString().padStart(2, '0');
                                let resetText = dict.relayExpectedRefreshDate || "预计 {month}-{day} {time} 刷新";
                                resetText = resetText.replace('{month}', month).replace('{day}', day).replace('{time}', `${hours}:${minutes}`);
                                resetStr = ` <span class="text-[10px] text-emerald-600 dark:text-emerald-400 bg-emerald-500/10 px-1.5 py-0.5 rounded ml-1 font-normal">${resetText}</span>`;
                            }
                            items.push(`
                                <div class="mb-2">
                                    <div class="flex justify-between text-[11px] mb-1 items-center">
                                        <span class="text-outline flex items-center">${isZH ? `${quota.dailyDays}天级限额` : `${quota.dailyDays}-day limit`} (${formatTokenCount(quota.dailyTokens)})${resetStr}</span>
                                        <span class="font-bold ${remain > 0 ? 'text-emerald-500' : 'text-red-500'}">${dict.relayRemain || '剩余:'} ${formatTokenCount(remain)}</span>
                                    </div>
                                    <div class="w-full bg-slate-200 dark:bg-slate-700 h-1.5 rounded-full overflow-hidden">
                                        <div class="bg-emerald-500 h-full rounded-full" style="width: ${pct}%"></div>
                                    </div>
                                </div>
                            `);
                        }

                        return `
                            <div class="bg-white/50 dark:bg-white/5 p-3 rounded-lg border border-outline-variant/20 text-[12px] mb-3">
                                <div class="font-semibold text-on-surface dark:text-white mb-2 flex items-center justify-between">
                                    <span>${familyTitle}</span>
                                    <span class="text-[10px] text-outline font-normal">${isZH ? '已用' : 'Used'}: ${formatTokenCount(lifetimeUsed)}</span>
                                </div>
                                ${items.join('')}
                            </div>
                        `;
                    };

                    html += `<div class="text-[12px] font-bold mb-2 mt-4 text-on-surface dark:text-white">${dict.relayUserUsageTracking || '用户剩余用量实时追踪'}</div>`;
                    html += renderFamilyQuota(dict.relayQuotaGeminiTitle || 'Gemini 系列模型', q.gemini, res.geminiLifetime || 0, res.geminiHourlyUsed || 0, res.geminiDailyUsed || 0, res.geminiHourlyResetAt, res.geminiDailyResetAt);
                    html += renderFamilyQuota(dict.relayQuotaClaudeTitle || 'Claude 系列模型', q.claude, res.claudeLifetime || 0, res.claudeHourlyUsed || 0, res.claudeDailyUsed || 0, res.claudeHourlyResetAt, res.claudeDailyResetAt);
                }
                
                if (stats.models && Object.keys(stats.models).length > 0) {
                    html += `<div class="text-[12px] font-bold mb-2 mt-4 text-on-surface dark:text-white">${dict.relayStatsModelTitle || '按模型统计'}</div>`;
                    for (const [modelName, modelStats] of Object.entries<any>(stats.models)) {
                        const modelTotalTokens = (modelStats.inputTokens || 0) + (modelStats.outputTokens || 0);
                        html += `
                            <div class="bg-white/50 dark:bg-white/5 p-3 rounded-lg border border-outline-variant/20 text-[12px] mb-2">
                                <div class="font-semibold text-primary mb-2">${modelName}</div>
                                <div class="grid grid-cols-3 gap-2 text-outline/80">
                                    <span>${isZH ? `请求: ${modelStats.requestCount}` : `Requests: ${modelStats.requestCount}`}</span>
                                    <span>Token: ${modelTotalTokens}</span>
                                    <span class="text-right">${isZH ? `花费: $${(modelStats.totalCost || 0).toFixed(4)}` : `Cost: $${(modelStats.totalCost || 0).toFixed(4)}`}</span>
                                </div>
                            </div>
                        `;
                    }
                }
                
                content.innerHTML = html;
            }
            (window as any)._relayOpenModal('relayUserStatsModal');
        }
    } catch (err) {
        console.error('[RelayController] Failed to view user stats:', err);
    }
};
