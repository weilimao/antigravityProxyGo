import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';
import { 
    initRendererElements, 
    renderOtpTable, 
    updateOtpCountdown, 
    show2FAKeyModal,
    showAdd2FAModal
} from './otpRenderer';

let otpTimer: any = null;
let isFirstSync = true;

export function initOtpEvents() {
    initRendererElements();

    const btnAddNewOtp = document.getElementById('btnAddNewOtp');
    if (btnAddNewOtp) {
        btnAddNewOtp.addEventListener('click', handleAddNewOtp);
    }
}

export async function refreshOtpList() {
    const spinner = document.getElementById('otpRefreshSpinner');
    if (spinner && isFirstSync) {
        spinner.classList.remove('hidden');
    }

    try {
        const otpList = await ipcRenderer.invoke('totp:get-codes');
        
        renderOtpTable(otpList, handleEditSecret, handleClearSecret, handleCopyCode);
        
        const activeItem = otpList.find((item: any) => item.hasSecret && typeof item.remaining === 'number');
        if (activeItem) {
            updateOtpCountdown(activeItem.remaining);
        } else {
            updateOtpCountdown(-1);
        }

        isFirstSync = false;
    } catch (err) {
        console.error('[OTP] Failed to refresh OTP list:', err);
    } finally {
        if (spinner) {
            spinner.classList.add('hidden');
        }
    }
}

export function startOtpTimer() {
    stopOtpTimer();
    isFirstSync = true;
    refreshOtpList();
    otpTimer = setInterval(refreshOtpList, 1000);
}

export function stopOtpTimer() {
    if (otpTimer) {
        clearInterval(otpTimer);
        otpTimer = null;
    }
}

async function handleEditSecret(accountId: string, email: string, currentSecretId: string) {
    let currentSecretVal = '';
    const acc = state.currentAccountsList?.find(a => a.id === accountId);
    if (acc && acc.twofa_secret) {
        currentSecretVal = acc.twofa_secret;
    }

    const saved = await show2FAKeyModal(
        accountId,
        email,
        currentSecretVal,
        async (secret: string) => {
            try {
                const res = await ipcRenderer.invoke('accounts:update-2fa', accountId, secret);
                return res;
            } catch (err: any) {
                return { success: false, error: err.message || '未知错误' };
            }
        }
    );

    if (saved) {
        ipcRenderer.send('accounts:get');
        refreshOtpList();
    }
}

async function handleClearSecret(accountId: string, email: string) {
    if (!confirm(`确定要清除账号 ${email} 的 2FA 密匙吗？`)) {
        return;
    }

    try {
        const res = await ipcRenderer.invoke('accounts:update-2fa', accountId, '');
        if (res && res.success) {
            ipcRenderer.send('accounts:get');
            refreshOtpList();
        } else {
            alert('清除失败: ' + (res.error || '未知错误'));
        }
    } catch (err: any) {
        alert('清除出错: ' + err.message);
    }
}

function handleCopyCode(code: string, btnEl: HTMLElement) {
    navigator.clipboard.writeText(code).then(() => {
        const origHtml = btnEl.innerHTML;
        btnEl.innerHTML = `
            <span class="text-[16px] font-bold tracking-widest font-mono text-emerald-600 dark:text-emerald-400">${code}</span>
            <span class="material-symbols-outlined text-[14px] text-emerald-600 dark:text-emerald-400">done</span>
        `;
        btnEl.classList.add('bg-emerald-500/10', 'border-emerald-500/30');

        setTimeout(() => {
            btnEl.innerHTML = origHtml;
            btnEl.classList.remove('bg-emerald-500/10', 'border-emerald-500/30');
        }, 1200);
    }).catch((err) => {
        console.error('Failed to copy verification code:', err);
    });
}

async function handleAddNewOtp() {
    const saved = await showAdd2FAModal(async (email: string, secret: string) => {
        try {
            const res = await ipcRenderer.invoke('totp:add-account', email, secret);
            return res;
        } catch (err: any) {
            return { success: false, error: err.message || '未知错误' };
        }
    });

    if (saved) {
        ipcRenderer.send('accounts:get');
        refreshOtpList();
    }
}
