/**
 * OpenCode 账号录入/编辑 Modal 控制逻辑 (含本机凭证一键导入与多 Key 批量导入)
 */
import { ipcRenderer } from '../shared/ipc';
import { opencodeRevealAccountId, setRevealKeyWarning } from '../shared/revealKeyState';

let opencodeAccountModal: HTMLDivElement | null = null;
let opencodeAccountModalContainer: HTMLDivElement | null = null;
let btnOpenCodeSave: HTMLButtonElement | null = null;
let btnOpenCodeCancel: HTMLButtonElement | null = null;
let btnOpenCodeModalClose: HTMLButtonElement | null = null;
let opencodeImportAlert: HTMLDivElement | null = null;

let tabOpenCodeSingle: HTMLButtonElement | null = null;
let tabOpenCodeBatch: HTMLButtonElement | null = null;
let sectionOpenCodeSingle: HTMLDivElement | null = null;
let sectionOpenCodeBatch: HTMLDivElement | null = null;

let opencodeEditId: string | null = null;
let currentMode: 'single' | 'batch' = 'single';

function writeOpenCodeModalError(msg: string | null): void {
    const el = document.getElementById('opencodeModalError');
    if (!el) return;
    if (msg) {
        el.textContent = msg;
        el.classList.remove('hidden');
    } else {
        el.textContent = '';
        el.classList.add('hidden');
    }
}

function safeParseIPC(resRaw: any): any {
    if (typeof resRaw === 'string') {
        try {
            return JSON.parse(resRaw);
        } catch {
            return {};
        }
    }
    return resRaw || {};
}

function setImportMode(mode: 'single' | 'batch'): void {
    currentMode = mode;
    if (!tabOpenCodeSingle || !tabOpenCodeBatch || !sectionOpenCodeSingle || !sectionOpenCodeBatch) return;

    const activeTabClass = 'px-3 py-1.5 rounded-lg text-[12px] font-bold bg-white dark:bg-[#1f293d] text-primary dark:text-primary-fixed-dim shadow-sm cursor-pointer';
    const inactiveTabClass = 'px-3 py-1.5 rounded-lg text-[12px] font-medium text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-200 cursor-pointer';

    if (mode === 'single') {
        tabOpenCodeSingle.className = activeTabClass;
        tabOpenCodeBatch.className = inactiveTabClass;
        sectionOpenCodeSingle.classList.remove('hidden');
        sectionOpenCodeBatch.classList.add('hidden');
    } else {
        tabOpenCodeSingle.className = inactiveTabClass;
        tabOpenCodeBatch.className = activeTabClass;
        sectionOpenCodeSingle.classList.add('hidden');
        sectionOpenCodeBatch.classList.remove('hidden');
    }
}

function reanchorOpenCodeModalHandles(): void {
    opencodeAccountModal = document.getElementById('opencodeAccountModal') as HTMLDivElement | null;
    opencodeAccountModalContainer = document.getElementById('opencodeAccountModalContainer') as HTMLDivElement | null;
    btnOpenCodeSave = document.getElementById('btnOpenCodeSave') as HTMLButtonElement | null;
    btnOpenCodeCancel = document.getElementById('btnOpenCodeCancel') as HTMLButtonElement | null;
    btnOpenCodeModalClose = document.getElementById('btnOpenCodeModalClose') as HTMLButtonElement | null;
    opencodeImportAlert = document.getElementById('opencodeImportAlert') as HTMLDivElement | null;

    tabOpenCodeSingle = document.getElementById('tabOpenCodeSingle') as HTMLButtonElement | null;
    tabOpenCodeBatch = document.getElementById('tabOpenCodeBatch') as HTMLButtonElement | null;
    sectionOpenCodeSingle = document.getElementById('sectionOpenCodeSingle') as HTMLDivElement | null;
    sectionOpenCodeBatch = document.getElementById('sectionOpenCodeBatch') as HTMLDivElement | null;
}

async function submitOpenCodeAccount(): Promise<void> {
    writeOpenCodeModalError(null);

    if (currentMode === 'batch') {
        const batchKeysInput = document.getElementById('inputOpenCodeBatchKeys') as HTMLTextAreaElement | null;
        const rawContent = batchKeysInput?.value?.trim() || '';
        if (!rawContent) {
            writeOpenCodeModalError('请输入要批量导入的 API Key (每行一个)');
            return;
        }

        if (btnOpenCodeSave) btnOpenCodeSave.disabled = true;
        try {
            const res = safeParseIPC(await ipcRenderer.invoke('opencode:batch-add', rawContent));
            if (!res.success) {
                writeOpenCodeModalError(res.error || '批量导入失败');
                return;
            }
            if (opencodeImportAlert) {
                opencodeImportAlert.textContent = `📦 批量导入完成: 成功 ${res.imported} 个, 失败 ${res.failed} 个`;
                opencodeImportAlert.classList.remove('hidden');
            }
            setTimeout(() => {
                closeOpenCodeAccountModal();
            }, 1000);
        } catch (e: any) {
            writeOpenCodeModalError('批量导入异常: ' + (e?.message || e));
        } finally {
            if (btnOpenCodeSave) btnOpenCodeSave.disabled = false;
        }
        return;
    }

    // 单 Key 录入 / 编辑
    const inputApiKey = document.getElementById('inputOpenCodeApiKey') as HTMLInputElement | null;
    const inputLabel = document.getElementById('inputOpenCodeLabel') as HTMLInputElement | null;

    const apiKey = inputApiKey?.value?.trim() || '';
    const label = inputLabel?.value?.trim() || '';

    if (!opencodeEditId && !apiKey) {
        writeOpenCodeModalError('请填写 OpenCode API Key (sk-...)');
        return;
    }

    if (btnOpenCodeSave) btnOpenCodeSave.disabled = true;
    try {
        if (opencodeEditId) {
            const res = safeParseIPC(await ipcRenderer.invoke('opencode:update', {
                id: opencodeEditId,
                apiKey,
                label,
            }));
            if (!res.success) {
                writeOpenCodeModalError(res.error || '更新 OpenCode 账号失败');
                return;
            }
        } else {
            const res = safeParseIPC(await ipcRenderer.invoke('opencode:add', {
                apiKey,
                label,
            }));
            if (!res.success) {
                writeOpenCodeModalError(res.error || '添加 OpenCode 账号失败');
                return;
            }
        }
        closeOpenCodeAccountModal();
    } catch (e: any) {
        writeOpenCodeModalError('提交异常: ' + (e?.message || e));
    } finally {
        if (btnOpenCodeSave) btnOpenCodeSave.disabled = false;
    }
}

export function openOpenCodeAccountModal(): void {
    reanchorOpenCodeModalHandles();
    opencodeEditId = null;
    opencodeRevealAccountId.value = null;
    setRevealKeyWarning(writeOpenCodeModalError);
    writeOpenCodeModalError(null);
    if (opencodeImportAlert) opencodeImportAlert.classList.add('hidden');

    const inputApiKey = document.getElementById('inputOpenCodeApiKey') as HTMLInputElement | null;
    const inputLabel = document.getElementById('inputOpenCodeLabel') as HTMLInputElement | null;
    const batchKeysInput = document.getElementById('inputOpenCodeBatchKeys') as HTMLTextAreaElement | null;
    if (inputApiKey) inputApiKey.value = '';
    if (inputLabel) inputLabel.value = '';
    if (batchKeysInput) batchKeysInput.value = '';

    const tabsNav = document.getElementById('opencodeModalTabs');
    if (tabsNav) tabsNav.classList.remove('hidden');
    setImportMode('single');

    if (opencodeAccountModal && opencodeAccountModalContainer) {
        opencodeAccountModal.classList.remove('opacity-0', 'pointer-events-none', 'hidden');
        opencodeAccountModalContainer.classList.remove('scale-95');
        opencodeAccountModalContainer.classList.add('scale-100');
    }
}

export function openEditOpenCodeAccount(acc: any): void {
    if (!acc) return;
    reanchorOpenCodeModalHandles();
    opencodeEditId = acc.id;
    opencodeRevealAccountId.value = acc.id;
    setRevealKeyWarning(writeOpenCodeModalError);
    writeOpenCodeModalError(null);
    if (opencodeImportAlert) opencodeImportAlert.classList.add('hidden');

    const inputApiKey = document.getElementById('inputOpenCodeApiKey') as HTMLInputElement | null;
    const inputLabel = document.getElementById('inputOpenCodeLabel') as HTMLInputElement | null;
    if (inputApiKey) inputApiKey.value = ''; // 不回显明文 Key
    if (inputLabel) inputLabel.value = acc.email || acc.label || '';

    // 编辑模式隐藏 Tab 和批量模式
    const tabsNav = document.getElementById('opencodeModalTabs');
    if (tabsNav) tabsNav.classList.add('hidden');
    setImportMode('single');

    if (opencodeAccountModal && opencodeAccountModalContainer) {
        opencodeAccountModal.classList.remove('opacity-0', 'pointer-events-none', 'hidden');
        opencodeAccountModalContainer.classList.remove('scale-95');
        opencodeAccountModalContainer.classList.add('scale-100');
    }
}

export function closeOpenCodeAccountModal(): void {
    reanchorOpenCodeModalHandles();
    opencodeEditId = null;
    opencodeRevealAccountId.value = null;
    setRevealKeyWarning(null);
    writeOpenCodeModalError(null);

    if (opencodeAccountModal && opencodeAccountModalContainer) {
        opencodeAccountModalContainer.classList.remove('scale-100');
        opencodeAccountModalContainer.classList.add('scale-95');
        opencodeAccountModal.classList.add('opacity-0', 'pointer-events-none');
    }
}

export function initOpenCodeAccountModalEvents(): void {
    reanchorOpenCodeModalHandles();

    if (btnOpenCodeSave) {
        btnOpenCodeSave.addEventListener('click', () => {
            void submitOpenCodeAccount();
        });
    }

    if (btnOpenCodeCancel) {
        btnOpenCodeCancel.addEventListener('click', () => {
            closeOpenCodeAccountModal();
        });
    }

    if (btnOpenCodeModalClose) {
        btnOpenCodeModalClose.addEventListener('click', () => {
            closeOpenCodeAccountModal();
        });
    }

    if (tabOpenCodeSingle) {
        tabOpenCodeSingle.addEventListener('click', () => {
            setImportMode('single');
        });
    }

    if (tabOpenCodeBatch) {
        tabOpenCodeBatch.addEventListener('click', () => {
            setImportMode('batch');
        });
    }

    // 挂载到全局供下拉按钮直接触发
    (window as any).openOpenCodeAccountModal = openOpenCodeAccountModal;
    (window as any).closeOpenCodeAccountModal = closeOpenCodeAccountModal;
    (window as any).openEditOpenCodeAccount = openEditOpenCodeAccount;
}
