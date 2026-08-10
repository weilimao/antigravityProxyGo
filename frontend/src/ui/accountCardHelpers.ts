/**
 * accountCardHelpers 数据->字符串助手层:从 accountsRenderer.ts 抽离的独立 leaf 模块。
 *
 * 高内聚:纯函数产出账号卡片头部徽标/冷却文案/配额气泡的 innerHTML 片段字符串,无 HTMLElement
 * 类型入参(renderNvidiaAccountQuota 入参虽是 HTMLElement 但仅写 innerHTML,仍属"拼接产物")。
 * 仅依赖 state/i18n,被 accountsRenderer.renderAccounts 与 quotaBarsRenderer 复用。
 * 从属 hub:accountsRenderer.ts;hub 经 import { escapeHtml, buildNvidiaCooldownTickSpan,
 * formatCooldownTime } 直达本模块,nvidiaCooldownTimer 经 hub re-export 取 getNvidiaCooldownRemaining。
 */
import state from './dashboardState';
import i18n from '../shared/i18n';

// Format reset time relatively
export function getRelativeResetTime(resetTime: any): string {
    const dict = i18n[state.currentLanguage] || i18n.zh;
    try {
        const now = Date.now();
        const reset = new Date(resetTime).getTime();
        const diffMs = reset - now;
        if (diffMs <= 0) {
            return dict.resetStatus || '已重置';
        }
        const diffMins = Math.round(diffMs / 60000);
        if (diffMins < 60) {
            return (dict.resetTimeMinutes || '将在 {minutes} 分钟后重置').replace('{minutes}', String(diffMins));
        }
        const diffHours = Math.floor(diffMins / 60);
        const remMins = diffMins % 60;
        if (diffHours < 24) {
            return (dict.resetTimeHours || '将在 {hours} 小时 {minutes} 分钟后重置').replace('{hours}', String(diffHours)).replace('{minutes}', String(remMins));
        }
        const diffDays = Math.floor(diffHours / 24);
        const remHours = diffHours % 24;
        return (dict.resetTimeDays || '将在 {days} 天 {hours} 小时后重置').replace('{days}', String(diffDays)).replace('{hours}', String(remHours));
    } catch (e) {
        return (dict.absoluteResetTime || '重置时间: {time}').replace('{time}', new Date(resetTime).toLocaleString());
    }
}

// Format cooldown time to absolute text
export function formatCooldownTime(cooldownTime: any): string {
    const isZH = state.currentLanguage === 'zh';
    try {
        const now = new Date();
        const target = new Date(cooldownTime);
        const isToday = now.getFullYear() === target.getFullYear() &&
                        now.getMonth() === target.getMonth() &&
                        now.getDate() === target.getDate();
        
        const timeStr = target.toLocaleTimeString(isZH ? 'zh-CN' : 'en-US', { hour12: false, hour: '2-digit', minute: '2-digit' });
        if (isToday) {
            return timeStr;
        } else {
            const month = target.getMonth() + 1;
            const date = target.getDate();
            return isZH ? `${month}月${date}日 ${timeStr}` : `${month}/${date} ${timeStr}`;
        }
    } catch (e) {
        return new Date(cooldownTime).toLocaleString();
    }
}

// getNvidiaCooldownRemaining 计算 NVIDIA 账号冷却剩余时间并返回秒级翻牌文案。
// 返回 { text, expired, seconds }：
//   - expired: 倒计时已归 0（含 until<=now 或非法值），文案 nvidiaCooldownExpired
//   - <60s / <60min / 否则 分别对应 nvidiaCooldownSeconds/Minutes/Hours
// 与 accountsController 的秒级定时器配合：定时器每秒读 data-until 重算 text，此处仅负责单次渲染。
export function getNvidiaCooldownRemaining(untilMs: any): { text: string; expired: boolean; seconds: number } {
    const dict = i18n[state.currentLanguage] || i18n.zh;
    const until = Number(untilMs) || 0;
    const diffMs = until - Date.now();
    if (diffMs <= 0) {
        return { text: dict.nvidiaCooldownExpired || '已到期，等待验证', expired: true, seconds: 0 };
    }
    const totalSeconds = Math.ceil(diffMs / 1000);
    if (totalSeconds < 60) {
        return {
            text: (dict.nvidiaCooldownSeconds || '{seconds}s 后恢复').replace('{seconds}', String(totalSeconds)),
            expired: false,
            seconds: totalSeconds,
        };
    }
    const totalMinutes = Math.floor(totalSeconds / 60);
    const remSeconds = totalSeconds % 60;
    if (totalMinutes < 60) {
        return {
            text: (dict.nvidiaCooldownMinutes || '{minutes}m {seconds}s 后恢复')
                .replace('{minutes}', String(totalMinutes))
                .replace('{seconds}', String(remSeconds)),
            expired: false,
            seconds: totalSeconds,
        };
    }
    const hours = Math.floor(totalMinutes / 60);
    const remMinutes = totalMinutes % 60;
    return {
        text: (dict.nvidiaCooldownHours || '{hours}h {minutes}m 后恢复')
            .replace('{hours}', String(hours))
            .replace('{minutes}', String(remMinutes)),
        expired: false,
        seconds: totalSeconds,
    };
}

// buildNvidiaCooldownTickSpan 构造秒级翻牌倒计时 span 的初始 innerHTML。
// 定时器(accountsController)每秒依据 data-until 用 getNvidiaCooldownRemaining 重算 text 与 className。
export function buildNvidiaCooldownTickSpan(untilMs: number): string {
    const { text, seconds, expired } = getNvidiaCooldownRemaining(untilMs);
    const cls = expired
        ? 'text-slate-500'
        : seconds <= 10 ? 'text-red-500 animate-pulse'
        : seconds <= 60 ? 'text-amber-600'
        : 'text-amber-500/80';
    return `<span class="nvidia-cooldown-tick ${cls}" data-until="${untilMs}">${escapeHtml(text)}</span>`;
}

// 状态优先级：停用 > 刷新失败 > 刷新成功(带模型数) > 未刷新兜底。
// 与后端 fetchNvidiaQuota 的语义 QuotaBucket(GROUP=NVIDIA 第三方 API Key, ModelID="可用模型数 N 个")配合：
// 成功时直接展示 cache[0].modelId 文案作为模型数副标题。
export function renderNvidiaAccountQuota(containerEl: HTMLElement, acc: any, isZH: boolean, dict: any) {
    if (!containerEl) return;
    const isEnabled = acc && acc.enabled !== false;

    // 1. 停用账号：灰泡，不参与探活
    if (!isEnabled) {
        containerEl.innerHTML = `
            <div class="flex items-center gap-1.5 bg-slate-500/10 dark:bg-slate-500/5 border border-slate-500/20 rounded-lg p-2.5 mt-1">
                <span class="w-1.5 h-1.5 rounded-full bg-slate-400"></span>
                <span class="text-[10px] font-medium text-slate-500 dark:text-slate-400" data-i18n="nvidiaAccountDisabled">${isZH ? '账号已停用' : (dict.accountDisabled || 'Account Disabled')}</span>
            </div>
        `;
        return;
    }

    // 1.5 冷却中：琥珀泡 + 秒级翻牌倒计时 + 绝对恢复时间。
    // 优先读 cooldowns.nvidia，兜底 cooldownUntil。冷却期不发探活（见 loadAccountQuota 短路）。
    const now = Date.now();
    let nvidiaCooldownUntil = 0;
    if (acc.cooldowns && typeof acc.cooldowns.nvidia === 'number' && acc.cooldowns.nvidia > now) {
        nvidiaCooldownUntil = acc.cooldowns.nvidia;
    } else if (acc.cooldownUntil && acc.cooldownUntil > now) {
        nvidiaCooldownUntil = acc.cooldownUntil;
    }
    if (nvidiaCooldownUntil > 0) {
        const resumeAbs = formatCooldownTime(nvidiaCooldownUntil);
        containerEl.innerHTML = `
            <div class="flex items-center gap-1.5 bg-amber-500/10 dark:bg-amber-500/5 border border-amber-500/20 rounded-lg p-2.5 mt-1">
                <span class="w-1.5 h-1.5 rounded-full bg-amber-500"></span>
                <span class="material-symbols-outlined text-amber-500 text-[12px]">hourglass_empty</span>
                <span class="text-[10px] font-bold text-amber-600 dark:text-amber-400" data-i18n="nvidiaCooldownBubble">${dict.nvidiaCooldownBubble || '冷静中'}</span>
                ${buildNvidiaCooldownTickSpan(nvidiaCooldownUntil)}
                <span class="text-[9px] text-amber-500/70 dark:text-amber-400/60 ml-auto">${isZH ? `${resumeAbs} 恢复` : `Resumes ${resumeAbs}`}</span>
            </div>
        `;
        return;
    }

    // 2. 刷新失败：红泡"配额请求失败"+原因(来自 state.nvidiaQuotaError[acc.id])
    const loadState = state.quotaLoadingState ? state.quotaLoadingState[acc.id] : undefined;
    const errMsg = state.nvidiaQuotaError ? state.nvidiaQuotaError[acc.id] : '';
    if (loadState === 'error' || errMsg) {
        const reason = errMsg || (isZH ? '未知错误' : 'Unknown error');
        containerEl.innerHTML = `
            <div class="flex flex-col gap-1 bg-red-500/10 dark:bg-red-500/5 border border-red-500/20 rounded-lg p-2.5 mt-1">
                <div class="flex items-center gap-1.5">
                    <span class="w-1.5 h-1.5 rounded-full bg-red-500"></span>
                    <span class="text-[10px] font-bold text-red-600 dark:text-red-400" data-i18n="nvidiaQuotaFail">${isZH ? '配额请求失败' : 'Quota Probe Failed'}</span>
                </div>
                <span class="text-[9px] text-red-500/80 dark:text-red-400/70 truncate" title="${escapeHtml(reason)}">${escapeHtml(reason)}</span>
            </div>
        `;
        return;
    }

    // 3. 刷新成功：绿泡"账号可用"+可用模型数(取 cache[0].modelId 文案或 credits)
    const buckets = state.quotaCache ? state.quotaCache[acc.id] : undefined;
    if (loadState === 'success' && buckets && buckets.length > 0) {
        let modelCountText = '';
        if (typeof buckets[0].modelId === 'string' && buckets[0].modelId) {
            modelCountText = buckets[0].modelId;
        } else if (typeof buckets[0].credits === 'number') {
            modelCountText = isZH ? `可用模型数 ${buckets[0].credits} 个` : `${buckets[0].credits} models`;
        }
        const countBadge = modelCountText
            ? `<span class="text-[10px] font-medium text-emerald-700 dark:text-emerald-300">${escapeHtml(modelCountText)}</span>`
            : '';
        containerEl.innerHTML = `
            <div class="flex items-center gap-1.5 bg-emerald-500/10 dark:bg-emerald-500/5 border border-emerald-500/20 rounded-lg p-2.5 mt-1">
                <span class="w-1.5 h-1.5 rounded-full bg-emerald-500 animate-pulse"></span>
                <span class="text-[10px] font-medium text-emerald-600 dark:text-emerald-400" data-i18n="nvidiaAccountAvailable">${isZH ? '账号可用 (NVIDIA 第三方 API Key)' : 'Account Available (NVIDIA API Key)'}</span>
                ${countBadge}
            </div>
        `;
        return;
    }

    // 4. 未刷新过(首次渲染/loading 中)：绿泡"账号可用"+灰色提示"点击刷新验证"
    if (loadState === 'loading') {
        containerEl.innerHTML = `
            <div class="flex items-center gap-1.5 bg-emerald-500/10 dark:bg-emerald-500/5 border border-emerald-500/20 rounded-lg p-2.5 mt-1">
                <span class="w-1.5 h-1.5 rounded-full bg-emerald-500 animate-pulse"></span>
                <span class="text-[10px] font-medium text-emerald-600 dark:text-emerald-400">${isZH ? '正在请求上游验证...' : 'Probing upstream...'}</span>
            </div>
        `;
        return;
    }
    containerEl.innerHTML = `
        <div class="flex items-center gap-1.5 bg-emerald-500/10 dark:bg-emerald-500/5 border border-emerald-500/20 rounded-lg p-2.5 mt-1">
            <span class="w-1.5 h-1.5 rounded-full bg-emerald-500 animate-pulse"></span>
            <span class="text-[10px] font-medium text-emerald-600 dark:text-emerald-400" data-i18n="nvidiaAccountAvailable">${isZH ? '账号可用 (NVIDIA 第三方 API Key)' : 'Account Available (NVIDIA API Key)'}</span>
            <span class="text-[9px] text-emerald-500/60 dark:text-emerald-400/50">${isZH ? '点击刷新验证' : 'Click refresh to verify'}</span>
        </div>
    `;
}

// buildGrokCooldownTickSpan 构造 Grok 秒级翻牌倒计时 span 的初始 innerHTML(决策 A)。
// tick 节点 class 用 grok-cooldown-tick(与 nvidia-cooldown-tick 物理隔离,两 timer 各扫各的),
// 但文案计算复用 getNvidiaCooldownRemaining(读 nvidiaCooldown* 通用文案键)。
// 定时器(grokCooldownTimer)每秒依据 data-until 用 getNvidiaCooldownRemaining 重算 text 与 className。
export function buildGrokCooldownTickSpan(untilMs: number): string {
    const { text, seconds, expired } = getNvidiaCooldownRemaining(untilMs);
    const cls = expired
        ? 'text-slate-500'
        : seconds <= 10 ? 'text-red-500 animate-pulse'
        : seconds <= 60 ? 'text-amber-600'
        : 'text-amber-500/80';
    return `<span class="grok-cooldown-tick ${cls}" data-until="${untilMs}">${escapeHtml(text)}</span>`;
}

// Grok 号池配额气泡渲染(4 态,镜像 renderNvidiaAccountQuota):
// 1 停用 → 灰泡 grokAccountDisabled;1.5 冷却 → 琥珀泡 grokCooldownBubble + grok-cooldown-tick 翻牌;
// 2 失败 → 红泡 grokQuotaFail + 读 state.nvidiaQuotaError[acc.id](决策 B 复用同一 error map);
// 3 成功 → 绿泡 grokAccountAvailable + buckets[0].modelId(后端 fetchGrokQuota 返回 "可用模型数 N 个");
// 4 未刷新 → 绿泡 grokAccountAvailable + "点击刷新验证"提示。
// 冷却读 acc.cooldowns.grok(后端单冷却族 "grok",account_selector.go GetModelCategoryByProvider),兜底 acc.cooldownUntil。
export function renderGrokAccountQuota(containerEl: HTMLElement, acc: any, isZH: boolean, dict: any) {
    if (!containerEl) return;
    const isEnabled = acc && acc.enabled !== false;

    // 1. 停用账号：灰泡，不参与探活
    if (!isEnabled) {
        containerEl.innerHTML = `
            <div class="flex items-center gap-1.5 bg-slate-500/10 dark:bg-slate-500/5 border border-slate-500/20 rounded-lg p-2.5 mt-1">
                <span class="w-1.5 h-1.5 rounded-full bg-slate-400"></span>
                <span class="text-[10px] font-medium text-slate-500 dark:text-slate-400" data-i18n="grokAccountDisabled">${isZH ? '账号已停用' : (dict.grokAccountDisabled || 'Account Disabled')}</span>
            </div>
        `;
        return;
    }

    // 1.5 冷却中：琥珀泡 + 秒级翻牌倒计时 + 绝对恢复时间。
    // 优先读 cooldowns.grok，兜底 cooldownUntil。冷却期不发探活（见 loadAccountQuota 短路）。
    const now = Date.now();
    let grokCooldownUntil = 0;
    if (acc.cooldowns && typeof acc.cooldowns.grok === 'number' && acc.cooldowns.grok > now) {
        grokCooldownUntil = acc.cooldowns.grok;
    } else if (acc.cooldownUntil && acc.cooldownUntil > now) {
        grokCooldownUntil = acc.cooldownUntil;
    }
    if (grokCooldownUntil > 0) {
        const resumeAbs = formatCooldownTime(grokCooldownUntil);
        containerEl.innerHTML = `
            <div class="flex items-center gap-1.5 bg-amber-500/10 dark:bg-amber-500/5 border border-amber-500/20 rounded-lg p-2.5 mt-1">
                <span class="w-1.5 h-1.5 rounded-full bg-amber-500"></span>
                <span class="material-symbols-outlined text-amber-500 text-[12px]">hourglass_empty</span>
                <span class="text-[10px] font-bold text-amber-600 dark:text-amber-400" data-i18n="grokCooldownBubble">${dict.grokCooldownBubble || '冷静中'}</span>
                ${buildGrokCooldownTickSpan(grokCooldownUntil)}
                <span class="text-[9px] text-amber-500/70 dark:text-amber-400/60 ml-auto">${isZH ? `${resumeAbs} 恢复` : `Resumes ${resumeAbs}`}</span>
            </div>
        `;
        return;
    }

    // 2. 刷新失败：红泡"配额请求失败"+原因(来自 state.nvidiaQuotaError[acc.id],决策 B 复用同一 error map)
    const loadState = state.quotaLoadingState ? state.quotaLoadingState[acc.id] : undefined;
    const errMsg = state.nvidiaQuotaError ? state.nvidiaQuotaError[acc.id] : '';
    if (loadState === 'error' || errMsg) {
        const reason = errMsg || (isZH ? '未知错误' : 'Unknown error');
        containerEl.innerHTML = `
            <div class="flex flex-col gap-1 bg-red-500/10 dark:bg-red-500/5 border border-red-500/20 rounded-lg p-2.5 mt-1">
                <div class="flex items-center gap-1.5">
                    <span class="w-1.5 h-1.5 rounded-full bg-red-500"></span>
                    <span class="text-[10px] font-bold text-red-600 dark:text-red-400" data-i18n="grokQuotaFail">${isZH ? '配额请求失败' : (dict.grokQuotaFail || 'Quota Probe Failed')}</span>
                </div>
                <span class="text-[9px] text-red-500/80 dark:text-red-400/70 truncate" title="${escapeHtml(reason)}">${escapeHtml(reason)}</span>
            </div>
        `;
        return;
    }

    // 3. 刷新成功：绿泡"账号可用"+可用模型数(取 cache[0].modelId 文案或 credits)
    // 后端 fetchGrokQuota 返回 ModelID:"可用模型数 N 个" / Credits=模型数,与 nvidia 同构。
    const buckets = state.quotaCache ? state.quotaCache[acc.id] : undefined;
    if (loadState === 'success' && buckets && buckets.length > 0) {
        let modelCountText = '';
        if (typeof buckets[0].modelId === 'string' && buckets[0].modelId) {
            modelCountText = buckets[0].modelId;
        } else if (typeof buckets[0].credits === 'number') {
            modelCountText = isZH ? `可用模型数 ${buckets[0].credits} 个` : `${buckets[0].credits} models`;
        }
        const countBadge = modelCountText
            ? `<span class="text-[10px] font-medium text-emerald-700 dark:text-emerald-300">${escapeHtml(modelCountText)}</span>`
            : '';
        containerEl.innerHTML = `
            <div class="flex items-center gap-1.5 bg-emerald-500/10 dark:bg-emerald-500/5 border border-emerald-500/20 rounded-lg p-2.5 mt-1">
                <span class="w-1.5 h-1.5 rounded-full bg-emerald-500 animate-pulse"></span>
                <span class="text-[10px] font-medium text-emerald-600 dark:text-emerald-400" data-i18n="grokAccountAvailable">${isZH ? '账号可用 (xAI API Key)' : (dict.grokAccountAvailable || 'Account Available (xAI API Key)')}</span>
                ${countBadge}
            </div>
        `;
        return;
    }

    // 4. 未刷新过(首次渲染/loading 中)：绿泡"账号可用"+灰色提示"点击刷新验证"
    if (loadState === 'loading') {
        containerEl.innerHTML = `
            <div class="flex items-center gap-1.5 bg-emerald-500/10 dark:bg-emerald-500/5 border border-emerald-500/20 rounded-lg p-2.5 mt-1">
                <span class="w-1.5 h-1.5 rounded-full bg-emerald-500 animate-pulse"></span>
                <span class="text-[10px] font-medium text-emerald-600 dark:text-emerald-400">${isZH ? '正在请求上游验证...' : 'Probing upstream...'}</span>
            </div>
        `;
        return;
    }
    containerEl.innerHTML = `
        <div class="flex items-center gap-1.5 bg-emerald-500/10 dark:bg-emerald-500/5 border border-emerald-500/20 rounded-lg p-2.5 mt-1">
            <span class="w-1.5 h-1.5 rounded-full bg-emerald-500 animate-pulse"></span>
            <span class="text-[10px] font-medium text-emerald-600 dark:text-emerald-400" data-i18n="grokAccountAvailable">${isZH ? '账号可用 (xAI API Key)' : (dict.grokAccountAvailable || 'Account Available (xAI API Key)')}</span>
            <span class="text-[9px] text-emerald-500/60 dark:text-emerald-400/50">${isZH ? '点击刷新验证' : 'Click refresh to verify'}</span>
        </div>
    `;
}

// escapeHtml 转义 HTML 特殊字符，避免把上游错误体直接 innerHTML 注入导致 XSS/样式破坏。
// 实体采用显式 String.fromCharCode / 拼接构造，规避编辑器对实体的反转义。
export function escapeHtml(s: string): string {
    const AMP = String.fromCharCode(38);      // &
    const LT = String.fromCharCode(60);       // <
    const GT = String.fromCharCode(62);       // >
    const QUOT = String.fromCharCode(34);     // "
    const APOS = String.fromCharCode(39);     // '
    return String(s)
        .replace(new RegExp(AMP, 'g'), AMP + 'amp;')
        .replace(new RegExp(LT, 'g'), AMP + 'lt;')
        .replace(new RegExp(GT, 'g'), AMP + 'gt;')
        .replace(new RegExp(QUOT, 'g'), AMP + 'quot;')
        .replace(new RegExp(APOS, 'g'), AMP + '#39;');
}
