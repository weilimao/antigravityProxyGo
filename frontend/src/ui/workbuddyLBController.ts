/**
 * WorkBuddy 号池负载均衡与轮询调度控制模块。
 *
 * 高内聚: 负责 WorkBuddy 号池顶部轮询算法 (游标轮询 / 粘性会话) 与单账号在途并发上限
 * 的 DOM 句柄初始化、事件监听 (带 300ms 防抖) 以及后端推送数据回填。
 * 从 accountsController.ts 解耦, 严守单文件 1000 行红线。
 */
import { ipcRenderer } from '../shared/ipc';

let workbuddyLBModeContainer: HTMLDivElement | null = null;
let workbuddyLBModeSelect: HTMLSelectElement | null = null;
let workbuddyMaxConcurrency: HTMLInputElement | null = null;
let wbConcurrencyDebounce: ReturnType<typeof setTimeout> | null = null;

/**
 * 初始化 DOM 句柄与事件监听。由 accountsController.initAccountsEvents 委托调用。
 */
export function initWorkBuddyLBEvents(): void {
    workbuddyLBModeContainer = document.getElementById('workbuddyLBModeContainer') as HTMLDivElement | null;
    workbuddyLBModeSelect = document.getElementById('workbuddyLBModeSelect') as HTMLSelectElement | null;
    workbuddyMaxConcurrency = document.getElementById('workbuddyMaxConcurrency') as HTMLInputElement | null;

    if (workbuddyLBModeSelect) {
        workbuddyLBModeSelect.addEventListener('change', (e: any) => {
            ipcRenderer.send('workbuddy:set-lb-mode', e.target.value);
        });
    }

    if (workbuddyMaxConcurrency) {
        workbuddyMaxConcurrency.addEventListener('change', (e: any) => {
            const v = Math.max(0, Math.min(1000, Number(e.target.value) || 0));
            e.target.value = String(v);
            if (wbConcurrencyDebounce) clearTimeout(wbConcurrencyDebounce);
            wbConcurrencyDebounce = setTimeout(() => {
                ipcRenderer.send('workbuddy:set-max-concurrency', v);
            }, 300);
        });
    }

    const btnCheckinAll = document.getElementById('btnWorkBuddyCheckinAll') as HTMLButtonElement | null;
    if (btnCheckinAll) {
        btnCheckinAll.addEventListener('click', async () => {
            const icon = document.getElementById('iconWbCheckin');
            const text = document.getElementById('textWbCheckin');
            if (icon) icon.classList.add('animate-spin');
            if (text) text.textContent = '签到中...';
            btnCheckinAll.disabled = true;

            try {
                await ipcRenderer.invoke('workbuddy:checkin-all', true);
            } catch (e: any) {
                console.error('[WorkBuddy] Checkin error:', e);
            } finally {
                setTimeout(() => {
                    if (icon) icon.classList.remove('animate-spin');
                    if (text) text.textContent = '每日签到';
                    btnCheckinAll.disabled = false;
                }, 1000);
            }
        });
    }
}

/**
 * 切换 WorkBuddy 负载均衡控件容器的显隐。
 */
export function setWorkBuddyLBContainerVisible(visible: boolean): void {
    if (!workbuddyLBModeContainer) {
        workbuddyLBModeContainer = document.getElementById('workbuddyLBModeContainer') as HTMLDivElement | null;
    }
    if (workbuddyLBModeContainer) {
        if (visible) {
            workbuddyLBModeContainer.classList.remove('hidden');
        } else {
            workbuddyLBModeContainer.classList.add('hidden');
        }
    }
}

/**
 * 根据后端 accounts-res 数据回填前端选中值。
 */
export function updateWorkBuddyLBUI(lastBackendData: any): void {
    if (!lastBackendData) return;
    if (!workbuddyLBModeSelect) {
        workbuddyLBModeSelect = document.getElementById('workbuddyLBModeSelect') as HTMLSelectElement | null;
    }
    if (!workbuddyMaxConcurrency) {
        workbuddyMaxConcurrency = document.getElementById('workbuddyMaxConcurrency') as HTMLInputElement | null;
    }

    if (workbuddyLBModeSelect) {
        workbuddyLBModeSelect.value = lastBackendData.workbuddyLBMode || 'round-robin';
    }
    if (workbuddyMaxConcurrency) {
        workbuddyMaxConcurrency.value = String(lastBackendData.workbuddyMaxConcurrency ?? 10);
    }
}
