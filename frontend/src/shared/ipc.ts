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

    on(channel: string, callback: (event: any, ...args: any[]) => void): void {
        if (wailsRuntime && wailsRuntime.EventsOn) {
            wailsRuntime.EventsOn(channel, (...args: any[]) => {
                // Electron listener signature is (event, ...args).
                // We mock the event object with a sender reference.
                callback({ sender: ipcRenderer }, ...args);
            });
        } else {
            const pending = (window as any).wailsPendingListeners || [];
            pending.push({ channel, callback });
            (window as any).wailsPendingListeners = pending;
        }
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

    // Request initial state, accounts, and certificate status once channels are established
    ipcRenderer.send('get-state');
    ipcRenderer.send('accounts:get');
    ipcRenderer.send('cert-status');
};

