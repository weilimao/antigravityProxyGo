/**
 * aggregateQuotaUI 聚合配额面板渲染:从 accountsRenderer.ts 抽离的独立模块(leaf)。
 *
 * 高内聚:getFamilyLifetimeTokens(私有)计算远端套餐全生命周期 token 累计;
 * updateAggregateQuotaUI(erolled export) 负责汇总配额面板呈现——本地负载均衡池·
 * 四类别(Gemini/Claude 周限额·5小时限额)平均剩余百分比聚合柱,或远程中继套餐·
 * 总额度/小时/天限额进度条二选一渲染。poolModeToggle 改为 fresh getElementById
 * 读取(从 hub 的 initRendererElements 缓存中剥离,避免 hub 持有该句柄)。
 * 仅依赖 state/i18n。岗位 hub:accountsRenderer,accountsController·quotaRender 经 hub re-export 调用。
 */
import state from './dashboardState';
import i18n from '../shared/i18n';

function getFamilyLifetimeTokens(stats: any, isGemini: boolean): number {
    if (!stats || !stats.models) return 0;
    let total = 0;
    for (const modelName of Object.keys(stats.models)) {
        const isClaude = modelName.toLowerCase().includes('claude');
        if (isGemini && !isClaude) {
            total += (stats.models[modelName].inputTokens || 0) + (stats.models[modelName].outputTokens || 0);
        } else if (!isGemini && isClaude) {
            total += (stats.models[modelName].inputTokens || 0) + (stats.models[modelName].outputTokens || 0);
        }
    }
    return total;
}

export function updateAggregateQuotaUI() {
    const panel = document.getElementById('aggregate-quota-panel');
    const grid = document.getElementById('aggregate-quota-grid');
    const info = document.getElementById('aggregate-quota-info');
    if (!panel || !grid || !info) return;

    const dict = i18n[state.currentLanguage] || i18n.zh;

    let isPool = false;
    if (state.lastBackendData) {
        if (state.currentActiveChannel === 'antigravity') {
            isPool = !!state.lastBackendData.poolMode;
        } else if (state.currentActiveChannel === 'gemini-cli') {
            isPool = !!state.lastBackendData.geminiCliPoolMode;
        } else if (state.currentActiveChannel === 'project') {
            isPool = !!state.lastBackendData.projectPoolMode;
        } else if (state.currentActiveChannel === 'nvidia') {
            isPool = !!state.lastBackendData.nvidiaPoolMode;
        } else if (state.currentActiveChannel === 'grok') {
            isPool = !!state.lastBackendData.grokPoolMode;
        }
    } else {
        const tog = document.getElementById('poolModeToggle') as HTMLInputElement | null;
        if (tog) isPool = tog.checked;
    }
    const isRemote = !!state.isRemoteMode;
    const hasRemoteStats = !!(state.remoteStats && state.remoteStats.quotas);

    // 绝对第一优先级：如果是远程模式，不论本地开没开负载均衡，一律禁止显示本地数据
    if (isRemote && !hasRemoteStats) {
        panel.classList.add('hidden');
        panel.classList.remove('flex');
        return;
    }
    
    // 第二优先级：如果没有开远程，且（没开负载均衡，或当前账户列表为空，或处于 project/nvidia/other 等无统一配额的通道），则隐藏面板
    if (!isRemote && (!isPool || !state.currentAccountsList || state.currentAccountsList.length === 0 || state.currentActiveChannel === 'project' || state.currentActiveChannel === 'nvidia' || state.currentActiveChannel === 'other' || state.currentActiveChannel === 'grok')) {
        panel.classList.add('hidden');
        panel.classList.remove('flex');
        return;
    }

    panel.classList.remove('hidden');
    panel.classList.add('flex');
    grid.innerHTML = '';
    
    if (isRemote && hasRemoteStats) {
        // Render Remote Quotas instead of Local Pool Quotas
        const q = state.remoteStats.quotas;
        const usage = state.remoteStats.currentUsage || {};
        const resetAt = state.remoteStats.resetAt || {};
        const isZH = state.currentLanguage === 'zh';
        const dict = i18n[state.currentLanguage] || {};
        
        const pkgName = state.remoteStats.packageName || (isZH ? '自定义配置' : 'Custom');
        info.textContent = isZH ? `远程中继套餐: ${pkgName}` : `Remote Plan: ${pkgName}`;
        info.className = 'text-[11px] px-2 py-0.5 rounded-full font-medium bg-emerald-500/10 text-emerald-600 dark:text-emerald-400';
        
        const renderRemoteQuotaBar = (label: string, limitTokens: number, usedTokens: number, resetTimeIso?: string, isDaily?: boolean) => {
            const percent = limitTokens > 0 ? (usedTokens / limitTokens) * 100 : 0;
            const remaining = Math.max(0, limitTokens - usedTokens);
            const remainPercent = Math.max(0, Math.min(100, 100 - percent));
            
            const colorClass = remainPercent > 20 ? 'bg-emerald-500' : 'bg-red-500';
            let resetBadge = '';
            if (usedTokens > 0 && resetTimeIso) {
                const d = new Date(resetTimeIso);
                const hours = d.getHours().toString().padStart(2, '0');
                const minutes = d.getMinutes().toString().padStart(2, '0');
                let timeStr = `${hours}:${minutes}`;
                if (isDaily) {
                    const month = (d.getMonth() + 1).toString().padStart(2, '0');
                    const day = d.getDate().toString().padStart(2, '0');
                    timeStr = `${month}-${day} ${hours}:${minutes}`;
                }
                const labelReset = isZH ? `预计 ${timeStr} 刷新` : `Resets at ${timeStr}`;
                resetBadge = ` <span class="text-[10px] text-emerald-600 dark:text-emerald-400 bg-emerald-500/10 px-1.5 py-0.5 rounded ml-1 font-normal">${labelReset}</span>`;
            }

            const formatTokenCount = (num: number): string => {
                if (num >= 1000000) {
                    return (num / 1000000).toFixed(2) + 'M';
                }
                if (num >= 1000) {
                    return (num / 1000).toFixed(1) + 'K';
                }
                return num.toString();
            };

            const remainingText = formatTokenCount(remaining);
            const limitText = formatTokenCount(limitTokens);
            
            grid.innerHTML += `
                <div class="flex flex-col">
                    <div class="flex justify-between items-end mb-1.5">
                        <span class="text-[12px] font-medium text-on-surface dark:text-white truncate flex items-center" title="${label}">${label}${resetBadge}</span>
                        <div class="text-[12px] flex items-center gap-1.5 font-bold">
                            <span class="text-outline/70 font-data-mono font-medium">${remainingText}/${limitText}</span>
                            <span class="${remainPercent > 20 ? 'text-emerald-500' : 'text-red-500'} font-data-mono">${Math.round(remainPercent)}%</span>
                        </div>
                    </div>
                    <div class="h-[6px] w-full bg-slate-200 dark:bg-slate-700/50 rounded-full overflow-hidden">
                        <div class="h-full ${colorClass} transition-all duration-500 relative" style="width: ${remainPercent}%">
                            <div class="absolute inset-0 bg-white/20"></div>
                        </div>
                    </div>
                </div>
            `;
        };
        
        if (q.gemini) {
            if (q.gemini.enableFixed) {
                const label = dict.remoteGeminiTotal || (state.currentLanguage === 'zh' ? '远端 Gemini 总额度' : 'Remote Gemini Total');
                renderRemoteQuotaBar(label, q.gemini.fixedTokens, getFamilyLifetimeTokens(state.remoteStats, true));
            }
            if (q.gemini.enableHourly) {
                const label = (dict.remoteGeminiHourly || (state.currentLanguage === 'zh' ? '远端 Gemini {hours}小时限额' : 'Remote Gemini {hours}-Hour'))
                    .replace('{hours}', String(q.gemini.hourlyHours));
                renderRemoteQuotaBar(label, q.gemini.hourlyTokens, usage.gemini_hourly || 0, resetAt.gemini_hourly, false);
            }
            if (q.gemini.enableDaily) {
                const label = (dict.remoteGeminiDaily || (state.currentLanguage === 'zh' ? '远端 Gemini {days}天限额' : 'Remote Gemini {days}-Day'))
                    .replace('{days}', String(q.gemini.dailyDays));
                renderRemoteQuotaBar(label, q.gemini.dailyTokens, usage.gemini_daily || 0, resetAt.gemini_daily, true);
            }
        }
        
        if (q.claude) {
            if (q.claude.enableFixed) {
                const label = dict.remoteClaudeTotal || (state.currentLanguage === 'zh' ? '远端 Claude 总额度' : 'Remote Claude Total');
                renderRemoteQuotaBar(label, q.claude.fixedTokens, getFamilyLifetimeTokens(state.remoteStats, false));
            }
            if (q.claude.enableHourly) {
                const label = (dict.remoteClaudeHourly || (state.currentLanguage === 'zh' ? '远端 Claude {hours}小时限额' : 'Remote Claude {hours}-Hour'))
                    .replace('{hours}', String(q.claude.hourlyHours));
                renderRemoteQuotaBar(label, q.claude.hourlyTokens, usage.claude_hourly || 0, resetAt.claude_hourly, false);
            }
            if (q.claude.enableDaily) {
                const label = (dict.remoteClaudeDaily || (state.currentLanguage === 'zh' ? '远端 Claude {days}天限额' : 'Remote Claude {days}-Day'))
                    .replace('{days}', String(q.claude.dailyDays));
                renderRemoteQuotaBar(label, q.claude.dailyTokens, usage.claude_daily || 0, resetAt.claude_daily, true);
            }
        }
        
        if (grid.innerHTML === '') {
            panel.classList.add('hidden');
            panel.classList.remove('flex');
        } else {
            const childCount = grid.children.length;
            if (childCount === 2) {
                grid.className = 'grid grid-cols-1 sm:grid-cols-2 gap-4';
            } else if (childCount === 1) {
                grid.className = 'grid grid-cols-1 gap-4';
            } else {
                grid.className = 'grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4';
            }
        }
        return;
    }

    let categories = [
        { group: 'Gemini Models', modelId: 'Weekly Limit', label: 'Gemini Weekly', key: 'gemini_weekly' },
        { group: 'Gemini Models', modelId: 'Five Hour Limit', label: 'Gemini 5-Hour', key: 'gemini_5hour' },
        { group: 'Claude and GPT models', modelId: 'Weekly Limit', label: 'Claude Weekly', key: 'claude_weekly' },
        { group: 'Claude and GPT models', modelId: 'Five Hour Limit', label: 'Claude 5-Hour', key: 'claude_5hour' }
    ];

    if (state.currentActiveChannel === 'project') {
        categories = categories.filter(c => c.key === 'gemini_weekly');
    }

    const sums: { [key: string]: { sum: number; count: number } } = {
        gemini_weekly: { sum: 0, count: 0 },
        gemini_5hour: { sum: 0, count: 0 },
        claude_weekly: { sum: 0, count: 0 },
        claude_5hour: { sum: 0, count: 0 }
    };

    const enabledAccounts = state.currentAccountsList.filter(a => {
        const accountChannel = a.provider;
        return accountChannel === state.currentActiveChannel && a.enabled !== false;
    });

    enabledAccounts.forEach(acc => {
        const buckets = state.quotaCache[acc.id];
        if (buckets && buckets.length > 0) {
            categories.forEach(cat => {
                const bucket = buckets.find(b => {
                    const bg = (b.group || '').toLowerCase();
                    const bm = (b.modelId || b.model || '').toLowerCase();
                    const cg = cat.group.toLowerCase();
                    const cm = cat.modelId.toLowerCase();
                    return (bg.includes(cg) || cg.includes(bg)) && (bm.includes(cm) || cm.includes(bm));
                });
                
                if (bucket) {
                    const percent = typeof bucket.remainPercent === 'number' ? bucket.remainPercent : (bucket.remainingFraction * 100);
                    sums[cat.key].sum += percent;
                    sums[cat.key].count += 1;
                }
            });
        }
    });

    grid.innerHTML = '';
    let totalAccountsWithQuota = 0;
    const enabledAccountIds = new Set(enabledAccounts.map(a => a.id));
    
    Object.keys(state.quotaCache).forEach(accId => {
        if (enabledAccountIds.has(accId)) {
            totalAccountsWithQuota++;
        }
    });
    
    info.textContent = (state.currentLanguage === 'zh' ? '汇总 {count}/{total} 个账号的额度' : 'Aggregated quota of {count}/{total} accounts')
        .replace('{count}', String(totalAccountsWithQuota))
        .replace('{total}', String(enabledAccounts.length));

    categories.forEach(cat => {
        const data = sums[cat.key];
        const cell = document.createElement('div');
        cell.className = 'flex flex-col gap-1 bg-slate-50/50 dark:bg-white/5 p-2 rounded-lg border border-outline-variant/20 flex-1 min-w-0';

        let displayLabel = cat.label;
        if (state.currentLanguage === 'zh') {
            if (cat.key === 'gemini_weekly') displayLabel = 'Gemini 周限额';
            else if (cat.key === 'gemini_5hour') displayLabel = 'Gemini 5小时限额';
            else if (cat.key === 'claude_weekly') displayLabel = 'Claude 周限额';
            else if (cat.key === 'claude_5hour') displayLabel = 'Claude 5小时限额';
        }

        if (data.count > 0) {
            const avgPercent = Math.round(data.sum / data.count);
            
            let colorClass = 'bg-emerald-500';
            let textClass = 'text-emerald-500 dark:text-emerald-400';
            if (avgPercent < 30) {
                colorClass = 'bg-red-500';
                textClass = 'text-red-500 dark:text-red-400';
            } else if (avgPercent < 60) {
                colorClass = 'bg-amber-500';
                textClass = 'text-amber-500 dark:text-amber-400';
            }

            cell.innerHTML = `
                <div class="flex justify-between text-[11px] font-semibold items-center">
                    <span class="text-on-surface dark:text-white truncate pr-1" title="${cat.group} - ${cat.modelId}">${displayLabel}</span>
                    <span class="${textClass} font-bold">${avgPercent}%</span>
                </div>
                <div class="w-full h-1 bg-outline-variant/20 dark:bg-white/5 rounded-full overflow-hidden">
                    <div class="${colorClass} h-full transition-all duration-300" style="width: ${avgPercent}%;"></div>
                </div>
            `;
        } else {
            cell.innerHTML = `
                <div class="flex justify-between text-[11px] font-semibold items-center">
                    <span class="text-on-surface dark:text-white truncate" title="${cat.group} - ${cat.modelId}">${displayLabel}</span>
                    <span class="text-outline/40 font-bold">-</span>
                </div>
                <div class="w-full h-1 bg-outline-variant/20 dark:bg-white/5 rounded-full overflow-hidden flex items-center justify-center">
                    <div class="bg-outline-variant/30 h-full w-0"></div>
                </div>
            `;
        }
        grid.appendChild(cell);
    });

    const childCount = grid.children.length;
    if (childCount === 2) {
        grid.className = 'grid grid-cols-1 sm:grid-cols-2 gap-4';
    } else if (childCount === 1) {
        grid.className = 'grid grid-cols-1 gap-4';
    } else {
        grid.className = 'grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4';
    }
}
