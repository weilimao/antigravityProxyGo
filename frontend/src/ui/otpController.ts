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

// Search and Pagination States
let searchQuery = '';
let currentPage = 1;
const pageSize = 10;
let lastOtpList: any[] = [];

export function initOtpEvents() {
    initRendererElements();

    const btnAddNewOtp = document.getElementById('btnAddNewOtp');
    if (btnAddNewOtp) {
        btnAddNewOtp.addEventListener('click', handleAddNewOtp);
    }

    // Reset pagination/search state on initialization
    searchQuery = '';
    currentPage = 1;

    const otpSearchInput = document.getElementById('otpSearchInput') as HTMLInputElement;
    if (otpSearchInput) {
        otpSearchInput.value = '';
        otpSearchInput.addEventListener('input', (e) => {
            searchQuery = (e.target as HTMLInputElement).value.trim();
            currentPage = 1; // reset page on search
            renderFilteredAndPaginatedOtp();
        });
    }

    const btnOtpPrevPage = document.getElementById('btnOtpPrevPage');
    if (btnOtpPrevPage) {
        btnOtpPrevPage.addEventListener('click', () => {
            if (currentPage > 1) {
                currentPage--;
                renderFilteredAndPaginatedOtp();
            }
        });
    }

    const btnOtpNextPage = document.getElementById('btnOtpNextPage');
    if (btnOtpNextPage) {
        btnOtpNextPage.addEventListener('click', () => {
            const filteredList = lastOtpList.filter((item: any) => 
                item.email.toLowerCase().includes(searchQuery.toLowerCase())
            );
            const totalPages = Math.max(1, Math.ceil(filteredList.length / pageSize));
            if (currentPage < totalPages) {
                currentPage++;
                renderFilteredAndPaginatedOtp();
            }
        });
    }

    const instantSecretInput = document.getElementById('instantSecretInput') as HTMLInputElement;
    if (instantSecretInput) {
        instantSecretInput.value = '';
        instantSecretInput.addEventListener('input', () => {
            refreshInstantOtp();
        });
    }

    const btnCopyInstantOtp = document.getElementById('btnCopyInstantOtp');
    if (btnCopyInstantOtp) {
        btnCopyInstantOtp.addEventListener('click', handleCopyInstantOtp);
    }
}

function renderFilteredAndPaginatedOtp() {
    const filteredList = lastOtpList.filter((item: any) => 
        item.email.toLowerCase().includes(searchQuery.toLowerCase())
    );
    
    const totalFilteredCount = filteredList.length;
    const totalPages = Math.max(1, Math.ceil(totalFilteredCount / pageSize));
    
    if (currentPage > totalPages) {
        currentPage = totalPages;
    }
    if (currentPage < 1) {
        currentPage = 1;
    }

    const start = (currentPage - 1) * pageSize;
    const paginatedList = filteredList.slice(start, start + pageSize);

    renderOtpTable(
        paginatedList,
        totalFilteredCount,
        currentPage,
        pageSize,
        handleEditSecret,
        handleClearSecret,
        handleCopyCode
    );

    // Update global countdown based on the first item on current page (or overall first active item)
    const activeItem = paginatedList.find((item: any) => item.hasSecret && typeof item.remaining === 'number');
    if (activeItem) {
        updateOtpCountdown(activeItem.remaining);
    } else {
        updateOtpCountdown(-1);
    }
}

export async function refreshOtpList() {
    const spinner = document.getElementById('otpRefreshSpinner');
    if (spinner && isFirstSync) {
        spinner.classList.remove('hidden');
    }

    try {
        lastOtpList = await ipcRenderer.invoke('totp:get-codes');
        
        renderFilteredAndPaginatedOtp();
        
        // Also refresh instant OTP code if input has a value
        refreshInstantOtp();

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
    const otpItem = lastOtpList.find(item => item.accountId === accountId);
    if (otpItem && otpItem.secret) {
        currentSecretVal = otpItem.secret;
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
    if (!await $confirm(`确定要清除账号 ${email} 的 2FA 密匙吗？`)) {
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

async function refreshInstantOtp() {
    const inputEl = document.getElementById('instantSecretInput') as HTMLInputElement;
    const resultContainer = document.getElementById('instantOtpResultContainer');
    const errorEl = document.getElementById('instantOtpError');
    const codeEl = document.getElementById('instantOtpCode');
    const countdownEl = document.getElementById('instantOtpCountdown');
    
    if (!inputEl) return;
    const secret = inputEl.value.trim();
    if (!secret) {
        resultContainer?.classList.add('hidden');
        resultContainer?.classList.remove('flex');
        errorEl?.classList.add('hidden');
        return;
    }

    try {
        const res = await ipcRenderer.invoke('totp:generate-code', secret);
        if (res && res.success) {
            errorEl?.classList.add('hidden');
            resultContainer?.classList.remove('hidden');
            resultContainer?.classList.add('flex');
            
            if (codeEl) codeEl.textContent = res.code;
            if (countdownEl) {
                countdownEl.textContent = `(${res.remaining}s)`;
                if (res.remaining <= 5) {
                    countdownEl.className = 'text-[11px] text-red-500 font-bold font-mono animate-pulse';
                } else if (res.remaining <= 10) {
                    countdownEl.className = 'text-[11px] text-amber-500 font-medium font-mono';
                } else {
                    countdownEl.className = 'text-[11px] text-primary/60 dark:text-primary-fixed-dim/60 font-mono';
                }
            }
        } else {
            resultContainer?.classList.add('hidden');
            resultContainer?.classList.remove('flex');
            if (errorEl) {
                errorEl.textContent = res.error || '密钥格式无效';
                errorEl.classList.remove('hidden');
            }
        }
    } catch (err: any) {
        resultContainer?.classList.add('hidden');
        resultContainer?.classList.remove('flex');
        if (errorEl) {
            errorEl.textContent = '计算失败';
            errorEl.classList.remove('hidden');
        }
    }
}

function handleCopyInstantOtp() {
    const codeEl = document.getElementById('instantOtpCode');
    const btnEl = document.getElementById('btnCopyInstantOtp');
    if (!codeEl || !btnEl) return;
    const code = codeEl.textContent;
    if (!code || code === '------') return;
    
    navigator.clipboard.writeText(code).then(() => {
        const origHtml = btnEl.innerHTML;
        btnEl.innerHTML = 'done';
        btnEl.classList.add('text-emerald-600', 'dark:text-emerald-400');
        setTimeout(() => {
            btnEl.innerHTML = origHtml;
            btnEl.classList.remove('text-emerald-600', 'dark:text-emerald-400');
        }, 1200);
    });
}
