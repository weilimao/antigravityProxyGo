/**
 * settingsNetwork.ts: 网络监控面板的状态与出站网络日志自动轮询模块。
 *
 * 遵循功能模块化与 KISS 原则，从 settingsController.ts 抽离：
 * - 监听 settings:network-status-res 与 settings:network-logs-res；
 * - 管理切换到网络监控面板时的 3 秒定时拉取与离开时的资源销毁；
 * - 格式化并渲染出站日志表格与网络代理状态标签。
 */
import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';

let networkRefreshTimer: ReturnType<typeof setInterval> | null = null;
let isNetworkListenersInited = false;

export function startNetworkLogsAutoRefresh(): void {
    if (networkRefreshTimer) return;
    networkRefreshTimer = setInterval(() => {
        try {
            ipcRenderer.send('settings:get-network-status');
            ipcRenderer.send('settings:get-network-logs');
        } catch (e) {
            console.error('[SettingsNetwork] Failed to auto refresh network logs:', e);
        }
    }, 3000);
}

export function stopNetworkLogsAutoRefresh(): void {
    if (networkRefreshTimer) {
        clearInterval(networkRefreshTimer);
        networkRefreshTimer = null;
    }
}

export function deactivateNetworkLogs(): void {
    stopNetworkLogsAutoRefresh();
    console.log('[SettingsNetwork] Outbound network logs auto refresh stopped.');
}

/**
 * 注册网络状态与网络日志 IPC 广播监听
 */
export function initSettingsNetworkListeners(): void {
    if (isNetworkListenersInited) return;

    ipcRenderer.on('settings:network-status-res', (_event, data: any) => {
        const lblNetStatusFallback = document.getElementById('lblNetStatusFallback');
        const lblNetStatusCustomSocks = document.getElementById('lblNetStatusCustomSocks');

        if (lblNetStatusFallback) {
            lblNetStatusFallback.textContent = data.cachedLocalProxy
                ? data.cachedLocalProxy
                : (state.currentLanguage === 'zh' ? '直连 (无探测代理)' : 'DIRECT (No scan proxy)');
            if (data.cachedLocalProxy) {
                lblNetStatusFallback.className = 'text-[13px] font-mono font-bold text-primary dark:text-primary-fixed-dim';
            } else {
                lblNetStatusFallback.className = 'text-[13px] font-mono font-bold text-outline';
            }
        }

        if (lblNetStatusCustomSocks) {
            if (data.customSocks5Enabled) {
                lblNetStatusCustomSocks.textContent = (state.currentLanguage === 'zh' ? '启用' : 'Enabled') + ` (${data.customSocks5Address})`;
                lblNetStatusCustomSocks.className = 'text-[13px] font-mono font-bold text-green-600 dark:text-green-400';
            } else {
                lblNetStatusCustomSocks.textContent = state.currentLanguage === 'zh' ? '未启用' : 'Disabled';
                lblNetStatusCustomSocks.className = 'text-[13px] font-mono font-bold text-outline';
            }
        }

        const lblNetStatusFallbackProxy = document.getElementById('lblNetStatusFallbackProxy');
        if (lblNetStatusFallbackProxy) {
            if (data.fallbackProxyEnabled) {
                lblNetStatusFallbackProxy.textContent = (state.currentLanguage === 'zh' ? '启用' : 'Enabled') + ` (${data.fallbackProxyAddress})`;
                lblNetStatusFallbackProxy.className = 'text-[13px] font-mono font-bold text-green-600 dark:text-green-400';
            } else {
                lblNetStatusFallbackProxy.textContent = state.currentLanguage === 'zh' ? '未启用' : 'Disabled';
                lblNetStatusFallbackProxy.className = 'text-[13px] font-mono font-bold text-outline';
            }
        }
    });

    ipcRenderer.on('settings:network-logs-res', (_event, logs: any[]) => {
        const tblNetworkLogsBody = document.getElementById('tblNetworkLogsBody');
        if (!tblNetworkLogsBody) return;

        if (!logs || logs.length === 0) {
            const emptyMsg = state.currentLanguage === 'zh'
                ? '暂无连接记录，正在等待出站网络活动...'
                : 'No connection logs. Waiting for outbound network activity...';
            tblNetworkLogsBody.innerHTML = `
                <tr>
                    <td colspan="5" class="py-6 text-center text-outline/60">${emptyMsg}</td>
                </tr>
            `;
            return;
        }

        // Newest log on top
        const sortedLogs = [...logs].reverse();

        let html = '';
        sortedLogs.forEach((log: any) => {
            const isSuccess = log.status === 'SUCCESS';
            const statusClass = isSuccess 
                ? 'text-green-600 dark:text-green-400 font-bold' 
                : 'text-red-500 font-bold truncate max-w-[240px] inline-block';
            const proxyClass = log.proxyUsed === 'DIRECT' 
                ? 'text-outline font-bold' 
                : 'text-primary dark:text-primary-fixed-dim font-bold';

            html += `
                <tr class="border-b border-outline-variant/10 hover:bg-slate-50 dark:hover:bg-white/5 transition-colors">
                    <td class="py-2 px-3 text-slate-400 font-medium select-none">${log.timestamp}</td>
                    <td class="py-2 px-3 text-on-surface dark:text-slate-200 font-bold font-mono">${log.target}</td>
                    <td class="py-2 px-3 ${proxyClass} font-mono">${log.proxyUsed}</td>
                    <td class="py-2 px-3 text-center text-on-surface dark:text-slate-300 font-bold">${log.duration}</td>
                    <td class="py-2 px-3 ${statusClass}" title="${log.status}">${log.status}</td>
                </tr>
            `;
        });

        tblNetworkLogsBody.innerHTML = html;
    });

    isNetworkListenersInited = true;
}
