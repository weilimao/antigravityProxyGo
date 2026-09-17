/**
 * quotaBarsRenderer 配额进度条渲染 + 单账号配额拉取:从 accountsRenderer.ts 抽离的独立模块。
 *
 * 高内聚:renderQuotaBars 按 buckets 渲染官方通道配额进度条(分簇/不打簇两条路径);
 * NVIDIA/Other/自定义上游封面"无额度限制"/"无统一配额"气泡分流;loadAccountQuota async
 * 经 quota:fetch 探活并落 quotaCache + quotaLoadingState,含 NVIDIA 冷却短路 + 在途去重。
 * 依赖 state/i18n/ipcRenderer;复用 accountCardHelpers 的 renderNvidiaAccountQuota /
 * getRelativeResetTime / formatCooldownTime,以及 aggregateQuotaUI 的 updateAggregateQuotaUI
 * (配额刷新后聚合面板联动重算)。岗位 hub:accountsRenderer,triggerTestModal 经 hub re-export 调 loadAccountQuota。
 */
import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';
import i18n from '../shared/i18n';
import { renderNvidiaAccountQuota, renderGrokAccountQuota, renderWorkBuddyAccountQuota, isDomesticWorkBuddyAccount, getRelativeResetTime, formatCooldownTime } from './accountCardHelpers';
import { updateAggregateQuotaUI } from './aggregateQuotaUI';

// Render account quota progress bars
export function renderQuotaBars(containerEl: HTMLElement | null, buckets: any[], cooldowns: any = {}) {
    if (!containerEl) return;
    containerEl.innerHTML = '';
    const dict = i18n[state.currentLanguage] || i18n.zh;
    const isZH = state.currentLanguage === 'zh';

    // 针对非官方渠道进行特殊展示，不显示假周限额进度条
    const accountId = containerEl.id ? containerEl.id.replace('quotaBars-', '') : '';
    const acc = state.currentAccountsList?.find(a => a.id === accountId);
    if (acc && acc.provider === 'nvidia') {
        renderNvidiaAccountQuota(containerEl, acc, isZH, dict);
        return;
    }
    // Grok 号池:镜像 NVIDIA 的 4 态渲染(停用/冷却/失败/可用),走 renderGrokAccountQuota。
    if (acc && acc.provider === 'grok') {
        renderGrokAccountQuota(containerEl, acc, isZH, dict);
        return;
    }
    // WorkBuddy 号池: 4 态渲染(停用/冷却/失败/可用·官方免费积分), 走 renderWorkBuddyAccountQuota。
    if (acc && acc.provider === 'workbuddy') {
        renderWorkBuddyAccountQuota(containerEl, acc, isZH, dict);
        return;
    }
    // Other 号池:配额语义不适用(自定义多上游组),显示无额度限制提示,不画假进度条。
    if (acc && acc.provider === 'other') {
        containerEl.innerHTML = `
            <div class="flex items-center gap-1.5 bg-purple-500/10 dark:bg-purple-500/5 border border-purple-500/20 rounded-lg p-2.5 mt-1">
                <span class="w-1.5 h-1.5 rounded-full bg-purple-500 animate-pulse"></span>
                <span class="text-[10px] font-medium text-purple-600 dark:text-purple-300">Other 自定义上游 (组内多账号轮换)，无统一配额</span>
            </div>
        `;
        return;
    }
    if (acc && acc.provider !== 'antigravity' && acc.provider !== 'gemini-cli') {
        containerEl.innerHTML = `
            <div class="flex items-center gap-1.5 bg-emerald-500/10 dark:bg-emerald-500/5 border border-emerald-500/20 rounded-lg p-2.5 mt-1">
                <span class="w-1.5 h-1.5 rounded-full bg-emerald-500 animate-pulse"></span>
                <span class="text-[10px] font-medium text-emerald-600 dark:text-emerald-400">${dict.quotaNoLimit || '云项目 API (按量付费)，无额度限制'}</span>
            </div>
        `;
        return;
    }

    if (!buckets || buckets.length === 0) {
        containerEl.innerHTML = `<span class="text-[10px] text-outline/50 italic">${dict.noQuotaData || '暂无配额数据'}</span>`;
        return;
    }

    const hasGroups = buckets.some(b => b.group);

    if (hasGroups) {
        const groups: { [key: string]: any[] } = {};
        buckets.forEach(b => {
            const groupName = b.group || (dict.otherModels || '其他模型');
            if (!groups[groupName]) {
                groups[groupName] = [];
            }
            groups[groupName].push(b);
        });

        Object.keys(groups).forEach((groupName, idx) => {
            const groupBuckets = groups[groupName];
            const lowerGroup = groupName.toLowerCase();
            // 冷却类别与后端 account.go 解耦为三族：gemini / claude / nvidia。
            // NVIDIA 号池走独立 "nvidia" 冷却键，避免误读 gemini 冷静条。
            const category = lowerGroup.includes('claude')
                ? 'claude'
                : lowerGroup.includes('nvidia')
                    ? 'nvidia'
                    : 'gemini';
            
            let isCategoryCooling = false;
            let categoryCooldownUntil = 0;
            if (cooldowns && cooldowns[category]) {
                const now = Date.now();
                if (cooldowns[category] > now) {
                    isCategoryCooling = true;
                    categoryCooldownUntil = cooldowns[category];
                }
            }

            const groupContainer = document.createElement('div');
            groupContainer.className = `flex flex-col gap-1.5 bg-[#f8fafc]/60 dark:bg-[#20293d]/30 border border-slate-100 dark:border-slate-800/30 rounded-lg p-2 ${idx > 0 ? 'mt-1.5' : 'mt-1'}`;
            
            const groupTitle = document.createElement('div');
            groupTitle.className = 'text-[10px] font-bold text-on-surface dark:text-white flex items-center justify-between border-b border-outline-variant/10 pb-1.5 mb-1';
            
            let cooldownBadge = '';
            if (isCategoryCooling) {
                const dateStr = formatCooldownTime(categoryCooldownUntil);
                cooldownBadge = `<span class="px-1 py-0.5 rounded bg-amber-500/10 text-amber-500 text-[8px] font-bold border border-amber-500/20">${isZH ? `${dateStr} 恢复` : `Resumes at ${dateStr}`}</span>`;
            }

            groupTitle.innerHTML = `
                <div class="flex items-center gap-1.5">
                    <span class="w-1.5 h-1.5 rounded-full ${isCategoryCooling ? 'bg-amber-500' : 'bg-primary'} animate-pulse"></span>
                    <span>${groupName}</span>
                </div>
                ${cooldownBadge}
            `;
            groupContainer.appendChild(groupTitle);

            groupBuckets.forEach(b => {
                const pct = b.remainPercent;
                const barColor = pct > 50
                    ? 'bg-emerald-500'
                    : pct > 20
                        ? 'bg-amber-400'
                        : 'bg-red-500';

                const resetStr = b.resetTime
                    ? getRelativeResetTime(b.resetTime)
                    : null;

                const row = document.createElement('div');
                row.className = 'flex flex-col gap-0.5 mt-1';
                row.innerHTML = `
                    <div class="flex justify-between items-center">
                        <span class="text-[10px] text-outline dark:text-outline-variant truncate max-w-[70%]" title="${b.modelId}">${b.modelId}</span>
                        <span class="text-[10px] font-bold text-on-surface dark:text-white">${pct}%</span>
                    </div>
                    <div class="h-1.5 bg-outline-variant/20 dark:bg-white/10 rounded-full overflow-hidden">
                        <div class="h-full ${barColor} rounded-full transition-all duration-700" style="width: ${pct}%"></div>
                    </div>
                    ${resetStr ? `<span class="text-[9px] text-outline/50 mt-0.5">${resetStr}</span>` : ''}
                `;
                groupContainer.appendChild(row);
            });

            containerEl.appendChild(groupContainer);
        });
    } else {
        buckets.forEach(b => {
            const pct = b.remainPercent;
            const barColor = pct > 50
                ? 'bg-emerald-500'
                : pct > 20
                    ? 'bg-amber-400'
                    : 'bg-red-500';

            const resetStr = b.resetTime
                ? new Date(b.resetTime).toLocaleString()
                : null;

            const row = document.createElement('div');
            row.className = 'flex flex-col gap-0.5';
            row.innerHTML = `
                <div class="flex justify-between items-center">
                    <span class="text-[10px] text-outline dark:text-outline-variant truncate max-w-[70%]" title="${b.modelId}">${b.modelId}</span>
                    <span class="text-[10px] font-bold text-on-surface dark:text-white">${pct}%</span>
                </div>
                <div class="h-1.5 bg-outline-variant/20 dark:bg-white/10 rounded-full overflow-hidden">
                    <div class="h-full ${barColor} rounded-full transition-all duration-700" style="width: ${pct}%"></div>
                </div>
                ${resetStr ? `<span class="text-[9px] text-outline/50 mt-0.5">${isZH ? '重置于: ' : 'Reset at: '}${resetStr}</span>` : ''}
            `;
            containerEl.appendChild(row);
        });
    }
}

// Fetch and load individual account quota
export async function loadAccountQuota(accountId: string, containerEl: HTMLElement | null, refreshBtn: HTMLElement | null, force: boolean = false, cooldowns: any = {}) {
    const isZH = state.currentLanguage === 'zh';
    if (!state.quotaLoadingState) {
        state.quotaLoadingState = {};
    }

    // NVIDIA 账号同样需要向后端 quota:fetch 发起探活请求(/v1/models)以校验可用性并
    // 取得可用模型数。此前这里对 nvidia provider 提前 return 导致刷新按钮对 NVIDIA
    // 账号"什么也不做"，且当其它 tab 残留卡片被误触发时反而刷到了 antigravity 账号。
    // 统一流程后，特定 provider 的差异化渲染由下方 success/error 分支中的
    // accForProbe.provider === 'nvidia' 判定负责。

    // 当前账号对象，提前取用以判断 NVIDIA 冷却短路（与下方 accForProbe 同源）。
    const accForProbe0 = state.currentAccountsList?.find(a => a.id === accountId);
    if (accForProbe0 && accForProbe0.provider === 'other') {
        // Other 号池:无配额端点,直接走无统一配额气泡渲染,不发 quota:fetch 探活。
        const activeContainer = document.getElementById(`quotaBars-${accountId}`) || containerEl;
        if (activeContainer) renderQuotaBars(activeContainer, [], accForProbe0.cooldowns || cooldowns);
        if (refreshBtn) {
            const icon = refreshBtn.querySelector('.material-symbols-outlined') || refreshBtn;
            if (icon) icon.classList.remove('animate-spin');
        }
        return;
    }
    if (accForProbe0 && accForProbe0.provider === 'nvidia') {
        const now = Date.now();
        const cdNv = accForProbe0.cooldowns && typeof accForProbe0.cooldowns.nvidia === 'number'
            ? accForProbe0.cooldowns.nvidia : 0;
        const cdUntil = (cdNv > now) ? cdNv
            : (accForProbe0.cooldownUntil && accForProbe0.cooldownUntil > now ? accForProbe0.cooldownUntil : 0);
        if (cdUntil > 0) {
            // 冷却中：直接走冷静气泡渲染，不发 quota:fetch 探活，避免浪费上游请求。
            const activeContainer = document.getElementById(`quotaBars-${accountId}`) || containerEl;
            if (activeContainer) renderQuotaBars(activeContainer, [], accForProbe0.cooldowns || cooldowns);
            if (refreshBtn) {
                const icon = refreshBtn.querySelector('.material-symbols-outlined') || refreshBtn;
                if (icon) icon.classList.remove('animate-spin');
            }
            return;
        }
    }
    if (accForProbe0 && accForProbe0.provider === 'grok') {
        // Grok 冷却短路:读 cooldowns.grok(后端单冷却族 "grok"),兜底 cooldownUntil。
        // 冷却中走冷静气泡渲染,不发 quota:fetch 探活,与 nvidia 同构。
        const now = Date.now();
        const cdGk = accForProbe0.cooldowns && typeof accForProbe0.cooldowns.grok === 'number'
            ? accForProbe0.cooldowns.grok : 0;
        const cdUntil = (cdGk > now) ? cdGk
            : (accForProbe0.cooldownUntil && accForProbe0.cooldownUntil > now ? accForProbe0.cooldownUntil : 0);
        if (cdUntil > 0) {
            const activeContainer = document.getElementById(`quotaBars-${accountId}`) || containerEl;
            if (activeContainer) renderQuotaBars(activeContainer, [], accForProbe0.cooldowns || cooldowns);
            if (refreshBtn) {
                const icon = refreshBtn.querySelector('.material-symbols-outlined') || refreshBtn;
                if (icon) icon.classList.remove('animate-spin');
            }
            return;
        }
    }
    if (accForProbe0 && accForProbe0.provider === 'workbuddy') {
        // WorkBuddy 冷却短路:读 cooldowns.workbuddy(后端单冷却族 "workbuddy")。
        // 冷却中走冷静气泡渲染,不发 quota:fetch 探活。
        const now = Date.now();
        const cdWb = accForProbe0.cooldowns && typeof accForProbe0.cooldowns.workbuddy === 'number'
            ? accForProbe0.cooldowns.workbuddy : 0;
        const cdUntil = (cdWb > now) ? cdWb : 0;
        if (cdUntil > 0) {
            const activeContainer = document.getElementById(`quotaBars-${accountId}`) || containerEl;
            if (activeContainer) renderQuotaBars(activeContainer, [], accForProbe0.cooldowns || cooldowns);
            if (refreshBtn) {
                const icon = refreshBtn.querySelector('.material-symbols-outlined') || refreshBtn;
                if (icon) icon.classList.remove('animate-spin');
            }
            return;
        }
        // 若账号为国内邮箱注册账号，短路不发网络探活
        if (isDomesticWorkBuddyAccount(accForProbe0)) {
            const activeContainer = document.getElementById(`quotaBars-${accountId}`) || containerEl;
            if (activeContainer) renderQuotaBars(activeContainer, [], accForProbe0.cooldowns || cooldowns);
            if (refreshBtn) {
                const icon = refreshBtn.querySelector('.material-symbols-outlined') || refreshBtn;
                if (icon) icon.classList.remove('animate-spin');
            }
            return;
        }
    }

    if (!force && state.quotaCache[accountId]) {
        const activeContainer = document.getElementById(`quotaBars-${accountId}`) || containerEl;
        renderQuotaBars(activeContainer, state.quotaCache[accountId], cooldowns);
        updateAggregateQuotaUI();
        return;
    }

    // 避免非强制加载时重复请求
    if (!force && (state.quotaLoadingState[accountId] === 'loading' || state.quotaLoadingState[accountId] === 'error' || state.quotaLoadingState[accountId] === 'success')) {
        if (state.quotaLoadingState[accountId] === 'loading') {
            const icon = refreshBtn?.querySelector('.material-symbols-outlined') || refreshBtn;
            if (icon) icon.classList.add('animate-spin');
            const activeContainer = document.getElementById(`quotaBars-${accountId}`) || containerEl;
            if (activeContainer) activeContainer.innerHTML = `<span class="text-[10px] text-outline/50">${isZH ? '加载中...' : 'Loading...'}</span>`;
        } else if (state.quotaLoadingState[accountId] === 'error') {
            const activeContainer = document.getElementById(`quotaBars-${accountId}`) || containerEl;
            if (activeContainer) activeContainer.innerHTML = `<span class="text-[10px] text-red-400">${isZH ? '加载失败' : 'Failed to load'}</span>`;
        }
        return;
    }

    const icon = refreshBtn?.querySelector('.material-symbols-outlined') || refreshBtn;
    if (icon) icon.classList.add('animate-spin');
    const initContainer = document.getElementById(`quotaBars-${accountId}`) || containerEl;
    if (initContainer) initContainer.innerHTML = `<span class="text-[10px] text-outline/50">${isZH ? '加载中...' : 'Loading...'}</span>`;

    state.quotaLoadingState[accountId] = 'loading';
    // 清除上轮 NVIDIA 探活失败原因，避免成功后被误判为失败
    if (state.nvidiaQuotaError) delete state.nvidiaQuotaError[accountId];

    // 当前账号对象，用于 nvidia 分支判定 provider
    const accForProbe = state.currentAccountsList?.find(a => a.id === accountId);

    try {
        const result = await ipcRenderer.invoke('quota:fetch', accountId);
        const activeContainer = document.getElementById(`quotaBars-${accountId}`) || containerEl;

        if (result.error) {
            state.quotaLoadingState[accountId] = 'error';
            if (accForProbe && (accForProbe.provider === 'nvidia' || accForProbe.provider === 'grok' || accForProbe.provider === 'workbuddy')) {
                // NVIDIA/Grok/WorkBuddy 失败：记失败原因并走红泡渲染(展示上游 HTTP/错误简述)
                // 决策 B:Grok/WorkBuddy 失败亦写入 state.nvidiaQuotaError[acc.id](该 map 按 acc.id 索引,内容通用)。
                if (!state.nvidiaQuotaError) state.nvidiaQuotaError = {};
                state.nvidiaQuotaError[accountId] = String(result.error);
                renderQuotaBars(activeContainer, [], cooldowns);
            } else {
                if (activeContainer) activeContainer.innerHTML = `<span class="text-[10px] text-red-400">${result.error}</span>`;
            }
        } else {
            state.quotaLoadingState[accountId] = 'success';
            state.quotaCache[accountId] = result.buckets;
            renderQuotaBars(activeContainer, result.buckets, cooldowns);
            updateAggregateQuotaUI();
        }
    } catch (e) {
        state.quotaLoadingState[accountId] = 'error';
        const activeContainer = document.getElementById(`quotaBars-${accountId}`) || containerEl;
        if (accForProbe && (accForProbe.provider === 'nvidia' || accForProbe.provider === 'grok' || accForProbe.provider === 'workbuddy')) {
            const reason = (e && (e as any).message) ? String((e as any).message) : (isZH ? '请求失败' : 'Request failed');
            if (!state.nvidiaQuotaError) state.nvidiaQuotaError = {};
            state.nvidiaQuotaError[accountId] = reason;
            renderQuotaBars(activeContainer, [], cooldowns);
        } else {
            if (activeContainer) activeContainer.innerHTML = `<span class="text-[10px] text-red-400">${isZH ? '请求失败' : 'Request failed'}</span>`;
        }
    } finally {
        // 实时从当前最新的 DOM 中获取按钮元素，防止因为页面重绘导致闭包内的旧 DOM 节点已被销毁而无法清除旋转动画的问题
        const currentCard = document.querySelector(`[data-account-id="${accountId}"]`);
        const currentBtn = currentCard?.querySelector('[data-quota-refresh-btn]') as HTMLElement | null;
        const icon = currentBtn?.querySelector('.material-symbols-outlined') || currentBtn;
        if (icon) icon.classList.remove('animate-spin');
    }
}
