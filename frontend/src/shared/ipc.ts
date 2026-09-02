import { IPCSend, IPCInvoke, OpenPath, ShowItemInFolder } from '../../wailsjs/go/main/App';
import * as wailsRuntime from '../../wailsjs/runtime/runtime';

export const ipcRenderer = {
    send(channel: string, ...args: any[]): void {
        IPCSend(channel, JSON.stringify(args)).catch((err) => {
            console.error(`[IPC] Failed to send to ${channel}:`, err);
        });
    },

    sendSync(channel: string, ...args: any[]): any {
        const configCache = (window as any).wailsConfigCache;
        if (configCache && configCache[channel] !== undefined) {
            return configCache[channel];
        }
        console.warn(`[IPC] Unhandled sendSync on channel: ${channel}`);
        return null;
    },

    async invoke(channel: string, ...args: any[]): Promise<any> {
        try {
            const res = await IPCInvoke(channel, JSON.stringify(args));
            return JSON.parse(res);
        } catch (err) {
            console.error(`[IPC] Error invoking ${channel}:`, err);
            throw err;
        }
    },

    // 返回取消订阅函数:调用方(如 useModelMapping)在组件卸载时调用,避免监听器随挂载次数累积泄漏。
    // 注意: wailsjs 模块导出的 EventsOn 永远存在,真实可用性取决于 window.runtime 是否已注入,
    // 未就绪时缓存到 pending 队列由 initWailsReady 统一 flush(与 ipc.test.ts 契约一致)。
    on(channel: string, callback: (event: any, ...args: any[]) => void): () => void {
        if (wailsRuntime && wailsRuntime.EventsOn && (window as any).runtime) {
            return wailsRuntime.EventsOn(channel, (...args: any[]) => {
                // Electron listener signature is (event, ...args).
                // We mock the event object with a sender reference.
                callback({ sender: ipcRenderer }, ...args);
            });
        }
        const pending = (window as any).wailsPendingListeners || [];
        pending.push({ channel, callback });
        (window as any).wailsPendingListeners = pending;
        return () => {
            const list = (window as any).wailsPendingListeners || [];
            const idx = list.findIndex((item: any) => item.channel === channel && item.callback === callback);
            if (idx >= 0) list.splice(idx, 1);
        };
    }
};

export const shell = {
    openExternal(url: string): void {
        if (wailsRuntime && wailsRuntime.BrowserOpenURL) {
            wailsRuntime.BrowserOpenURL(url);
        } else {
            window.open(url, '_blank');
        }
    },

    openPath(path: string): void {
        OpenPath(path).catch((err) => {
            console.error("[IPC] Failed to open path:", path, err);
        });
    },

    showItemInFolder(path: string): void {
        ShowItemInFolder(path).catch((err) => {
            console.error("[IPC] Failed to show item in folder:", path, err);
        });
    }
};

// Global initializer logic to flush early registered listeners once runtime is ready
(window as any).initWailsReady = function () {
    const pending = (window as any).wailsPendingListeners;
    console.log(`[IPC] Wails runtime ready, flushing ${pending?.length || 0} listeners`);
    if (pending && pending.length > 0) {
        pending.forEach((item: any) => {
            ipcRenderer.on(item.channel, item.callback);
        });
        (window as any).wailsPendingListeners = [];
    }

    // Refresh UI with latest configurations now that window.wailsConfigCache is populated
    if ((window as any).refreshLanguageFromBackend) {
        try {
            (window as any).refreshLanguageFromBackend();
        } catch (err) {
            console.error('[IPC] Failed to run refreshLanguageFromBackend:', err);
        }
    }
    if ((window as any).refreshSettingsUI) {
        try {
            (window as any).refreshSettingsUI();
        } catch (err) {
            console.error('[IPC] Failed to run refreshSettingsUI:', err);
        }
    }
    if ((window as any).refreshDataDir) {
        try {
            (window as any).refreshDataDir();
        } catch (err) {
            console.error('[IPC] Failed to run refreshDataDir:', err);
        }
    }
    if ((window as any).refreshRelayUsers) {
        try {
            (window as any).refreshRelayUsers();
        } catch (err) {
            console.error('[IPC] Failed to run refreshRelayUsers:', err);
        }
    }
    if ((window as any).refreshAccountLayoutFromBackend) {
        try {
            (window as any).refreshAccountLayoutFromBackend();
        } catch (err) {
            console.error('[IPC] Failed to run refreshAccountLayoutFromBackend:', err);
        }
    }

    // Request initial state, accounts, and certificate status once channels are established
    ipcRenderer.send('get-state');
    ipcRenderer.send('accounts:get');
    ipcRenderer.send('cert-status');
};

