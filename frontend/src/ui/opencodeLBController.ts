/**
 * OpenCode 号池负载均衡与轮询调度控制模块。
 *
 * 高内聚: 负责 OpenCode 号池顶部轮询算法 (游标轮询 / 粘性会话) 与单账号在途并发上限
 * 的 DOM 句柄初始化、事件监听 (带 300ms 防抖) 以及后端推送数据回填。
 * 从 accountsController.ts 解耦, 严守单文件 1000 行红线。
 */
import { ipcRenderer } from '../shared/ipc';

let opencodeLBModeContainer: HTMLDivElement | null = null;
let opencodeLBModeSelect: HTMLSelectElement | null = null;
let opencodeMaxConcurrency: HTMLInputElement | null = null;
let ocConcurrencyDebounce: ReturnType<typeof setTimeout> | null = null;

/**
 * 初始化 DOM 句柄与事件监听。由 accountsController.initAccountsEvents 委托调用。
 */
export function initOpenCodeLBEvents(): void {
    opencodeLBModeContainer = document.getElementById('opencodeLBModeContainer') as HTMLDivElement | null;
    opencodeLBModeSelect = document.getElementById('opencodeLBModeSelect') as HTMLSelectElement | null;
    opencodeMaxConcurrency = document.getElementById('opencodeMaxConcurrency') as HTMLInputElement | null;

    if (opencodeLBModeSelect) {
        opencodeLBModeSelect.addEventListener('change', (e: any) => {
            ipcRenderer.send('opencode:set-lb-mode', e.target.value);
        });
    }

    if (opencodeMaxConcurrency) {
        opencodeMaxConcurrency.addEventListener('change', (e: any) => {
            const v = Math.max(0, Math.min(1000, Number(e.target.value) || 0));
            e.target.value = String(v);
            if (ocConcurrencyDebounce) clearTimeout(ocConcurrencyDebounce);
            ocConcurrencyDebounce = setTimeout(() => {
                ipcRenderer.send('opencode:set-max-concurrency', v);
            }, 300);
        });
    }
}

/**
 * 切换 OpenCode 负载均衡控件容器的显隐。
 */
export function setOpenCodeLBContainerVisible(visible: boolean): void {
    if (!opencodeLBModeContainer) {
        opencodeLBModeContainer = document.getElementById('opencodeLBModeContainer') as HTMLDivElement | null;
    }
    if (opencodeLBModeContainer) {
        if (visible) {
            opencodeLBModeContainer.classList.remove('hidden');
        } else {
            opencodeLBModeContainer.classList.add('hidden');
        }
    }
}

/**
 * 根据后端 accounts-res 数据回填前端选中值。
 */
export function updateOpenCodeLBUI(lastBackendData: any): void {
    if (!lastBackendData) return;
    if (!opencodeLBModeSelect) {
        opencodeLBModeSelect = document.getElementById('opencodeLBModeSelect') as HTMLSelectElement | null;
    }
    if (!opencodeMaxConcurrency) {
        opencodeMaxConcurrency = document.getElementById('opencodeMaxConcurrency') as HTMLInputElement | null;
    }

    if (opencodeLBModeSelect) {
        opencodeLBModeSelect.value = lastBackendData.opencodeLBMode || 'round-robin';
    }
    if (opencodeMaxConcurrency) {
        opencodeMaxConcurrency.value = String(lastBackendData.opencodeMaxConcurrency ?? 10);
    }
}
