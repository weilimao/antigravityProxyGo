import { ref } from 'vue';
import { ipcRenderer } from '../shared/ipc';

export interface PoolErrorRetryData {
    enabled: boolean;
    switchCodes: number[];
    retryCodes: number[];
    switchCooldownSecs: number;
    maxRetries: number;
}

export const poolErrorRetryEnabled = ref(false);
export const poolErrorRetrySwitchCodesText = ref('429, 403, 503');
export const poolErrorRetryRetryCodesText = ref('500, 502, 504');
export const poolErrorRetryCooldownSecs = ref(60);
export const poolErrorRetryMaxRetries = ref(3);

let btnOpenPoolErrorRetry: HTMLButtonElement | null = null;
let poolErrorRetryModal: HTMLDivElement | null = null;
let poolErrorRetryModalContainer: HTMLDivElement | null = null;
let currentConfig: PoolErrorRetryData = {
    enabled: false,
    switchCodes: [429, 403, 503],
    retryCodes: [500, 502, 504],
    switchCooldownSecs: 60,
    maxRetries: 3,
};

// parseStatusCodes 解析状态码字符串: 逗号/分号/空格/中文逗号，支持 5xx, 4xx 范围写法
export function parseStatusCodes(raw: string): number[] {
    const out = new Set<number>();
    for (const tok of String(raw || '').split(/[,;，；\s]+/)) {
        const t = tok.trim();
        if (!t) continue;
        const rangeMatch = t.match(/^([1-5])xx$/i);
        if (rangeMatch) {
            const base = Number(rangeMatch[1]) * 100;
            for (let c = base; c < base + 100; c++) out.add(c);
            continue;
        }
        const n = Number(t);
        if (Number.isInteger(n) && n >= 100 && n <= 599) out.add(n);
    }
    return Array.from(out).sort((a, b) => a - b);
}

// updatePoolErrorRetryFromBackend 从后端广播数据更新状态快照
export function updatePoolErrorRetryFromBackend(data: any): void {
    if (!data || !data.poolErrorRetry) return;
    const cfg = data.poolErrorRetry as PoolErrorRetryData;
    currentConfig = {
        enabled: !!cfg.enabled,
        switchCodes: Array.isArray(cfg.switchCodes) ? cfg.switchCodes : [429, 403, 503],
        retryCodes: Array.isArray(cfg.retryCodes) ? cfg.retryCodes : [500, 502, 504],
        switchCooldownSecs: typeof cfg.switchCooldownSecs === 'number' ? cfg.switchCooldownSecs : 60,
        maxRetries: typeof cfg.maxRetries === 'number' && cfg.maxRetries > 0 ? cfg.maxRetries : 3,
    };
    renderPoolErrorRetryButtonBadge();
}

// renderPoolErrorRetryButtonBadge 渲染号池工具栏入口按钮的状态高亮
export function renderPoolErrorRetryButtonBadge(): void {
    if (!btnOpenPoolErrorRetry) {
        btnOpenPoolErrorRetry = document.getElementById('btnOpenPoolErrorRetry') as HTMLButtonElement | null;
    }
    if (!btnOpenPoolErrorRetry) return;

    if (currentConfig.enabled) {
        btnOpenPoolErrorRetry.classList.remove('text-outline', 'dark:text-outline-variant', 'bg-slate-100', 'dark:bg-white/5', 'border-outline-variant/30');
        btnOpenPoolErrorRetry.classList.add('text-primary', 'dark:text-primary-fixed-dim', 'bg-primary/10', 'border-primary/40');
    } else {
        btnOpenPoolErrorRetry.classList.remove('text-primary', 'dark:text-primary-fixed-dim', 'bg-primary/10', 'border-primary/40');
        btnOpenPoolErrorRetry.classList.add('text-outline', 'dark:text-outline-variant', 'bg-slate-100', 'dark:bg-white/5', 'border-outline-variant/30');
    }
}

export function openPoolErrorRetryModal(): void {
    poolErrorRetryModal = document.getElementById('poolErrorRetryModal') as HTMLDivElement | null;
    poolErrorRetryModalContainer = document.getElementById('poolErrorRetryModalContainer') as HTMLDivElement | null;

    if (!poolErrorRetryModal || !poolErrorRetryModalContainer) return;

    // 回填表单
    poolErrorRetryEnabled.value = currentConfig.enabled;
    poolErrorRetrySwitchCodesText.value = currentConfig.switchCodes.length > 0 ? currentConfig.switchCodes.join(', ') : '';
    poolErrorRetryRetryCodesText.value = currentConfig.retryCodes.length > 0 ? currentConfig.retryCodes.join(', ') : '';
    poolErrorRetryCooldownSecs.value = currentConfig.switchCooldownSecs ?? 60;
    poolErrorRetryMaxRetries.value = currentConfig.maxRetries ?? 3;

    poolErrorRetryModal.classList.remove('opacity-0', 'pointer-events-none', 'hidden');
    poolErrorRetryModalContainer.classList.remove('scale-95');
    poolErrorRetryModalContainer.classList.add('scale-100');
}

export function closePoolErrorRetryModal(): void {
    poolErrorRetryModal = document.getElementById('poolErrorRetryModal') as HTMLDivElement | null;
    poolErrorRetryModalContainer = document.getElementById('poolErrorRetryModalContainer') as HTMLDivElement | null;

    if (!poolErrorRetryModal || !poolErrorRetryModalContainer) return;
    poolErrorRetryModalContainer.classList.remove('scale-100');
    poolErrorRetryModalContainer.classList.add('scale-95');
    poolErrorRetryModal.classList.add('opacity-0', 'pointer-events-none');
}

export function submitPoolErrorRetry(): void {
    const swCodes = parseStatusCodes(poolErrorRetrySwitchCodesText.value);
    const rtCodes = parseStatusCodes(poolErrorRetryRetryCodesText.value);
    const cdSecs = Math.max(0, Math.min(86400 * 7, Number(poolErrorRetryCooldownSecs.value) || 60));
    const retries = Math.max(1, Math.min(10, Number(poolErrorRetryMaxRetries.value) || 3));

    const payload: PoolErrorRetryData = {
        enabled: !!poolErrorRetryEnabled.value,
        switchCodes: swCodes,
        retryCodes: rtCodes,
        switchCooldownSecs: cdSecs,
        maxRetries: retries,
    };

    currentConfig = payload;
    renderPoolErrorRetryButtonBadge();
    ipcRenderer.send('accounts:set-pool-error-retry', JSON.stringify(payload));
    closePoolErrorRetryModal();
}

export function initPoolErrorRetryEvents(): void {
    btnOpenPoolErrorRetry = document.getElementById('btnOpenPoolErrorRetry') as HTMLButtonElement | null;
    poolErrorRetryModal = document.getElementById('poolErrorRetryModal') as HTMLDivElement | null;
    poolErrorRetryModalContainer = document.getElementById('poolErrorRetryModalContainer') as HTMLDivElement | null;

    if (btnOpenPoolErrorRetry) {
        btnOpenPoolErrorRetry.onclick = (e) => {
            e.stopPropagation();
            openPoolErrorRetryModal();
        };
    }

    const btnSave = document.getElementById('btnPoolErrorRetryModalSave');
    if (btnSave) {
        btnSave.onclick = submitPoolErrorRetry;
    }

    const btnCancel = document.getElementById('btnPoolErrorRetryModalCancel');
    if (btnCancel) {
        btnCancel.onclick = closePoolErrorRetryModal;
    }

    const btnClose = document.getElementById('btnPoolErrorRetryModalClose');
    if (btnClose) {
        btnClose.onclick = closePoolErrorRetryModal;
    }

    renderPoolErrorRetryButtonBadge();
}

// 挂载至全局 window，确保可以在 HTML onclick 中直接调用
if (typeof window !== 'undefined') {
    (window as any).openPoolErrorRetryModal = openPoolErrorRetryModal;
    (window as any).closePoolErrorRetryModal = closePoolErrorRetryModal;
}
