/**
 * WorkBuddy 账号录入/编辑 Modal 控制逻辑 (含官方网页一键授权登录)
 */
import { ipcRenderer } from '../shared/ipc';

let workbuddyAccountModal: HTMLDivElement | null;
let workbuddyAccountModalContainer: HTMLDivElement | null;
let btnWorkBuddySave: HTMLButtonElement | null;
let btnWorkBuddyCancel: HTMLButtonElement | null;
let btnWorkBuddyModalClose: HTMLButtonElement | null;
let btnWorkBuddyImportLocal: HTMLButtonElement | null;
let workbuddyImportAlert: HTMLDivElement | null;

// 官方网页登录 DOM 节点
let workbuddyWebLoginSection: HTMLDivElement | null;
let tabWorkBuddyDomestic: HTMLButtonElement | null;
let tabWorkBuddyOverseas: HTMLButtonElement | null;
let wbOAuthDescription: HTMLParagraphElement | null;
let wbOAuthStateIdle: HTMLDivElement | null;
let wbOAuthStateLogging: HTMLDivElement | null;
let wbOAuthStateSuccess: HTMLDivElement | null;
let btnWorkBuddyWebLogin: HTMLButtonElement | null;
let btnWorkBuddyWebLoginText: HTMLSpanElement | null;
let btnWorkBuddyIdleCopyUrl: HTMLButtonElement | null;
let btnWorkBuddyIdleCopyUrlText: HTMLSpanElement | null;
let wbOAuthLoggingTip: HTMLParagraphElement | null;
let btnWorkBuddyCopyLoginUrl: HTMLButtonElement | null;
let btnWorkBuddyCopyLoginUrlText: HTMLSpanElement | null;
let btnWorkBuddyCancelLogin: HTMLButtonElement | null;
let wbOAuthSuccessName: HTMLSpanElement | null;

let selectedOAuthEdition: 'domestic' | 'international' = 'domestic';

let workbuddyEditId: string | null = null;
let currentOAuthState: string | null = null;
let currentOAuthBrowserUrl: string | null = null;
let oauthPollTimer: ReturnType<typeof setInterval> | null = null;

function switchOAuthEdition(edition: 'domestic' | 'international'): void {
    selectedOAuthEdition = edition;
    if (!tabWorkBuddyDomestic || !tabWorkBuddyOverseas) return;

    if (edition === 'domestic') {
        tabWorkBuddyDomestic.className = 'px-3 py-1 text-[12px] font-bold rounded-md transition-all flex items-center gap-1.5 cursor-pointer bg-blue-600 text-white shadow-sm';
        tabWorkBuddyOverseas.className = 'px-3 py-1 text-[12px] font-medium rounded-md transition-all flex items-center gap-1.5 cursor-pointer text-slate-600 dark:text-slate-300 hover:text-blue-600';
        if (wbOAuthDescription) {
            wbOAuthDescription.innerHTML = '国内版官方授权登录，支持在浏览器中通过<strong>微信扫码</strong>或<strong>手机验证码</strong>一键登录并自动入库（免安装客户端）';
        }
        if (btnWorkBuddyWebLoginText) {
            btnWorkBuddyWebLoginText.textContent = '微信扫码 / 官方网页授权';
        }
        const inputBaseUrl = document.getElementById('inputWorkBuddyBaseUrl') as HTMLInputElement | null;
        if (inputBaseUrl && (!inputBaseUrl.value || inputBaseUrl.value === 'https://www.codebuddy.ai' || inputBaseUrl.value === 'https://www.workbuddy.ai')) {
            inputBaseUrl.value = 'https://copilot.tencent.com';
        }
    } else {
        tabWorkBuddyOverseas.className = 'px-3 py-1 text-[12px] font-bold rounded-md transition-all flex items-center gap-1.5 cursor-pointer bg-blue-600 text-white shadow-sm';
        tabWorkBuddyDomestic.className = 'px-3 py-1 text-[12px] font-medium rounded-md transition-all flex items-center gap-1.5 cursor-pointer text-slate-600 dark:text-slate-300 hover:text-blue-600';
        if (wbOAuthDescription) {
            wbOAuthDescription.innerHTML = '国际版官方网页授权登录，支持在浏览器中通过 Google / GitHub / X 账号一键登录并自动入库';
        }
        if (btnWorkBuddyWebLoginText) {
            btnWorkBuddyWebLoginText.textContent = '国际版官方网页一键授权';
        }
        const inputBaseUrl = document.getElementById('inputWorkBuddyBaseUrl') as HTMLInputElement | null;
        if (inputBaseUrl && (!inputBaseUrl.value || inputBaseUrl.value === 'https://copilot.tencent.com' || inputBaseUrl.value === 'https://www.codebuddy.ai')) {
            inputBaseUrl.value = 'https://www.workbuddy.ai';
        }
    }
}

function writeWorkBuddyModalError(msg: string | null): void {
    const el = document.getElementById('workbuddyModalError');
    if (!el) return;
    if (msg) {
        el.textContent = msg;
        el.classList.remove('hidden');
    } else {
        el.textContent = '';
        el.classList.add('hidden');
    }
}

function setOAuthUIState(state: 'idle' | 'logging' | 'success'): void {
    if (!wbOAuthStateIdle || !wbOAuthStateLogging || !wbOAuthStateSuccess) return;

    wbOAuthStateIdle.classList.add('hidden');
    wbOAuthStateLogging.classList.add('hidden');
    wbOAuthStateSuccess.classList.add('hidden');

    if (state === 'idle') {
        wbOAuthStateIdle.classList.remove('hidden');
    } else if (state === 'logging') {
        wbOAuthStateLogging.classList.remove('hidden');
    } else if (state === 'success') {
        wbOAuthStateSuccess.classList.remove('hidden');
    }
}

function stopOAuthPolling(): void {
    if (oauthPollTimer) {
        clearInterval(oauthPollTimer);
        oauthPollTimer = null;
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

function reanchorWorkBuddyModalHandles(): void {
    workbuddyAccountModal = document.getElementById('workbuddyAccountModal') as HTMLDivElement | null;
    workbuddyAccountModalContainer = document.getElementById('workbuddyAccountModalContainer') as HTMLDivElement | null;
    btnWorkBuddySave = document.getElementById('btnWorkBuddySave') as HTMLButtonElement | null;
    btnWorkBuddyCancel = document.getElementById('btnWorkBuddyCancel') as HTMLButtonElement | null;
    btnWorkBuddyModalClose = document.getElementById('btnWorkBuddyModalClose') as HTMLButtonElement | null;
    btnWorkBuddyImportLocal = document.getElementById('btnWorkBuddyImportLocal') as HTMLButtonElement | null;
    workbuddyImportAlert = document.getElementById('workbuddyImportAlert') as HTMLDivElement | null;

    workbuddyWebLoginSection = document.getElementById('workbuddyWebLoginSection') as HTMLDivElement | null;
    tabWorkBuddyDomestic = document.getElementById('tabWorkBuddyDomestic') as HTMLButtonElement | null;
    tabWorkBuddyOverseas = document.getElementById('tabWorkBuddyOverseas') as HTMLButtonElement | null;
    wbOAuthDescription = document.getElementById('wbOAuthDescription') as HTMLParagraphElement | null;
    wbOAuthStateIdle = document.getElementById('wbOAuthStateIdle') as HTMLDivElement | null;
    wbOAuthStateLogging = document.getElementById('wbOAuthStateLogging') as HTMLDivElement | null;
    wbOAuthStateSuccess = document.getElementById('wbOAuthStateSuccess') as HTMLDivElement | null;
    btnWorkBuddyWebLogin = document.getElementById('btnWorkBuddyWebLogin') as HTMLButtonElement | null;
    btnWorkBuddyWebLoginText = document.getElementById('btnWorkBuddyWebLoginText') as HTMLSpanElement | null;
    btnWorkBuddyIdleCopyUrl = document.getElementById('btnWorkBuddyIdleCopyUrl') as HTMLButtonElement | null;
    btnWorkBuddyIdleCopyUrlText = document.getElementById('btnWorkBuddyIdleCopyUrlText') as HTMLSpanElement | null;
    wbOAuthLoggingTip = document.getElementById('wbOAuthLoggingTip') as HTMLParagraphElement | null;
    btnWorkBuddyCopyLoginUrl = document.getElementById('btnWorkBuddyCopyLoginUrl') as HTMLButtonElement | null;
    btnWorkBuddyCopyLoginUrlText = document.getElementById('btnWorkBuddyCopyLoginUrlText') as HTMLSpanElement | null;
    btnWorkBuddyCancelLogin = document.getElementById('btnWorkBuddyCancelLogin') as HTMLButtonElement | null;
    wbOAuthSuccessName = document.getElementById('wbOAuthSuccessName') as HTMLSpanElement | null;

    if (tabWorkBuddyDomestic) tabWorkBuddyDomestic.onclick = () => switchOAuthEdition('domestic');
    if (tabWorkBuddyOverseas) tabWorkBuddyOverseas.onclick = () => switchOAuthEdition('international');

    if (btnWorkBuddyModalClose) btnWorkBuddyModalClose.onclick = closeWorkBuddyAccountModal;
    if (btnWorkBuddyCancel) btnWorkBuddyCancel.onclick = closeWorkBuddyAccountModal;
    if (btnWorkBuddySave) btnWorkBuddySave.onclick = submitWorkBuddyAccount;
    if (btnWorkBuddyImportLocal) btnWorkBuddyImportLocal.onclick = importLocalWorkBuddyAccount;

    if (btnWorkBuddyWebLogin) btnWorkBuddyWebLogin.onclick = () => startWorkBuddyWebLogin(true);
    if (btnWorkBuddyIdleCopyUrl) btnWorkBuddyIdleCopyUrl.onclick = () => startWorkBuddyWebLogin(false);
    if (btnWorkBuddyCopyLoginUrl) btnWorkBuddyCopyLoginUrl.onclick = copyWorkBuddyLoginUrl;
    if (btnWorkBuddyCancelLogin) btnWorkBuddyCancelLogin.onclick = cancelWorkBuddyWebLogin;
}

export function initWorkBuddyAccountModalEvents(): void {
    reanchorWorkBuddyModalHandles();
}

export function openWorkBuddyAccountModal(): void {
    reanchorWorkBuddyModalHandles();
    if (!workbuddyAccountModal || !workbuddyAccountModalContainer) return;

    workbuddyEditId = null;
    stopOAuthPolling();
    currentOAuthState = null;
    currentOAuthBrowserUrl = null;
    setOAuthUIState('idle');
    switchOAuthEdition('domestic');

    if (workbuddyWebLoginSection) {
        workbuddyWebLoginSection.classList.remove('hidden');
    }

    const titleEl = workbuddyAccountModal.querySelector('[data-i18n="workbuddyAddModalTitle"]') as HTMLElement | null;
    if (titleEl) titleEl.textContent = '添加 WorkBuddy 号池账号';
    if (btnWorkBuddySave) btnWorkBuddySave.textContent = '保存账号';

    const inputBaseUrl = document.getElementById('inputWorkBuddyBaseUrl') as HTMLInputElement | null;
    const inputToken = document.getElementById('inputWorkBuddyToken') as HTMLInputElement | null;
    const inputLabel = document.getElementById('inputWorkBuddyLabel') as HTMLInputElement | null;

    if (inputBaseUrl) inputBaseUrl.value = 'https://copilot.tencent.com';
    if (inputToken) inputToken.value = '';
    if (inputLabel) inputLabel.value = '';

    writeWorkBuddyModalError(null);
    if (btnWorkBuddyIdleCopyUrlText) btnWorkBuddyIdleCopyUrlText.textContent = '复制授权链接';
    if (btnWorkBuddyCopyLoginUrlText) btnWorkBuddyCopyLoginUrlText.textContent = '复制登录链接';
    if (workbuddyImportAlert) {
        workbuddyImportAlert.classList.add('hidden');
        workbuddyImportAlert.textContent = '';
    }

    workbuddyAccountModal.classList.remove('opacity-0', 'pointer-events-none', 'hidden');
    workbuddyAccountModalContainer.classList.remove('scale-95');
    workbuddyAccountModalContainer.classList.add('scale-100');
}

export function openEditWorkBuddyAccount(acc: any): void {
    reanchorWorkBuddyModalHandles();
    if (!workbuddyAccountModal || !workbuddyAccountModalContainer || !acc) return;

    workbuddyEditId = acc.id;
    stopOAuthPolling();

    // 编辑已有账号时折叠网页授权卡片
    if (workbuddyWebLoginSection) {
        workbuddyWebLoginSection.classList.add('hidden');
    }

    const titleEl = workbuddyAccountModal.querySelector('[data-i18n="workbuddyAddModalTitle"]') as HTMLElement | null;
    if (titleEl) titleEl.textContent = '编辑 WorkBuddy 号池账号';
    if (btnWorkBuddySave) btnWorkBuddySave.textContent = '保存修改';

    const inputBaseUrl = document.getElementById('inputWorkBuddyBaseUrl') as HTMLInputElement | null;
    const inputToken = document.getElementById('inputWorkBuddyToken') as HTMLInputElement | null;
    const inputLabel = document.getElementById('inputWorkBuddyLabel') as HTMLInputElement | null;

    if (inputBaseUrl) {
        let u = acc.baseUrl || 'https://www.workbuddy.ai';
        if (u.includes('codebuddy.ai')) u = 'https://www.workbuddy.ai';
        inputBaseUrl.value = u;
    }
    if (inputToken) inputToken.value = acc.maskedKey || '';
    if (inputLabel) inputLabel.value = acc.email || '';

    writeWorkBuddyModalError(null);
    if (workbuddyImportAlert) {
        workbuddyImportAlert.classList.add('hidden');
        workbuddyImportAlert.textContent = '';
    }

    workbuddyAccountModal.classList.remove('opacity-0', 'pointer-events-none', 'hidden');
    workbuddyAccountModalContainer.classList.remove('scale-95');
    workbuddyAccountModalContainer.classList.add('scale-100');
}

export function closeWorkBuddyAccountModal(): void {
    reanchorWorkBuddyModalHandles();
    if (!workbuddyAccountModal || !workbuddyAccountModalContainer) return;
    writeWorkBuddyModalError(null);

    // 弹窗关闭时若仍在轮询，取消后端登录会话
    if (currentOAuthState && oauthPollTimer) {
        cancelWorkBuddyWebLogin();
    }
    stopOAuthPolling();

    workbuddyAccountModalContainer.classList.remove('scale-100');
    workbuddyAccountModalContainer.classList.add('scale-95');
    workbuddyAccountModal.classList.add('opacity-0', 'pointer-events-none');
    setTimeout(() => {
        workbuddyAccountModal?.classList.add('hidden');
    }, 150);
}

async function startWorkBuddyWebLogin(openBrowser: boolean = true): Promise<void> {
    writeWorkBuddyModalError(null);
    setOAuthUIState('logging');

    const authMethodTip = selectedOAuthEdition === 'domestic' ? '微信扫码或短信验证码' : '官方网页授权';
    if (wbOAuthLoggingTip) {
        if (openBrowser) {
            wbOAuthLoggingTip.textContent = `已自动为你唤起系统默认浏览器。若浏览器未自动打开，请复制链接在浏览器中完成${authMethodTip}：`;
        } else {
            wbOAuthLoggingTip.textContent = `授权链接已复制到剪贴板！请在任意浏览器中粘贴打开并完成${authMethodTip}，完成后将自动入库：`;
        }
    }

    try {
        const noOpen = !openBrowser;
        const version = selectedOAuthEdition === 'domestic' ? '5.5.6' : '5.5.2';
        const res = safeParseIPC(await ipcRenderer.invoke('workbuddy:oauth-start', version, noOpen, selectedOAuthEdition));
        if (!res.success) {
            writeWorkBuddyModalError(res.error || '发起官方登录失败');
            setOAuthUIState('idle');
            return;
        }

        currentOAuthState = res.state;
        currentOAuthBrowserUrl = res.browserUrl;

        // 若是仅复制链接模式，自动将链接复制到剪贴板并给按钮短暂提示
        if (!openBrowser && res.browserUrl) {
            try {
                await navigator.clipboard.writeText(res.browserUrl);
                if (btnWorkBuddyCopyLoginUrlText) {
                    btnWorkBuddyCopyLoginUrlText.textContent = '已复制！';
                    setTimeout(() => {
                        if (btnWorkBuddyCopyLoginUrlText) btnWorkBuddyCopyLoginUrlText.textContent = '复制登录链接';
                    }, 2000);
                }
            } catch {
                writeWorkBuddyModalError('无法自动写入剪贴板，请手动点击下方“复制登录链接”');
            }
        }

        // 启动前端高频轮询检查状态
        stopOAuthPolling();
        oauthPollTimer = setInterval(async () => {
            if (!currentOAuthState) return;
            try {
                const statusRes = safeParseIPC(await ipcRenderer.invoke('workbuddy:oauth-status', currentOAuthState));
                if (!statusRes.success) return;

                if (statusRes.status === 'success') {
                    stopOAuthPolling();
                    setOAuthUIState('success');
                    if (wbOAuthSuccessName) {
                        const email = statusRes.account?.email || statusRes.account?.uid || '账号';
                        const editionTag = (statusRes.edition === 'domestic' || selectedOAuthEdition === 'domestic') ? '国内版' : '国际版';
                        wbOAuthSuccessName.textContent = `🎉 ${email} (${editionTag}) 登录成功！`;
                    }
                    setTimeout(() => {
                        closeWorkBuddyAccountModal();
                    }, 1200);
                } else if (statusRes.status === 'failed' || statusRes.status === 'timeout') {
                    stopOAuthPolling();
                    setOAuthUIState('idle');
                    writeWorkBuddyModalError(statusRes.errorMessage || '登录失败，请重试');
                } else if (statusRes.status === 'canceled') {
                    stopOAuthPolling();
                    setOAuthUIState('idle');
                }
            } catch {}
        }, 1200);

    } catch (e: any) {
        writeWorkBuddyModalError('发起官方登录异常: ' + (e?.message || e));
        setOAuthUIState('idle');
    }
}

async function cancelWorkBuddyWebLogin(): Promise<void> {
    stopOAuthPolling();
    if (currentOAuthState) {
        try {
            await ipcRenderer.invoke('workbuddy:oauth-cancel', currentOAuthState);
        } catch {}
        currentOAuthState = null;
    }
    setOAuthUIState('idle');
}

async function copyWorkBuddyLoginUrl(): Promise<void> {
    if (!currentOAuthBrowserUrl) return;
    try {
        await navigator.clipboard.writeText(currentOAuthBrowserUrl);
        if (btnWorkBuddyCopyLoginUrlText) {
            btnWorkBuddyCopyLoginUrlText.textContent = '已复制！';
            setTimeout(() => {
                if (btnWorkBuddyCopyLoginUrlText) btnWorkBuddyCopyLoginUrlText.textContent = '复制登录链接';
            }, 2000);
        }
    } catch {
        writeWorkBuddyModalError('无法自动复制到剪贴板，请检查系统权限');
    }
}

async function submitWorkBuddyAccount(): Promise<void> {
    const inputBaseUrl = document.getElementById('inputWorkBuddyBaseUrl') as HTMLInputElement | null;
    const inputToken = document.getElementById('inputWorkBuddyToken') as HTMLInputElement | null;
    const inputLabel = document.getElementById('inputWorkBuddyLabel') as HTMLInputElement | null;

    let baseUrl = inputBaseUrl?.value?.trim() || 'https://www.workbuddy.ai';
    if (baseUrl.includes('codebuddy.ai')) {
        baseUrl = 'https://www.workbuddy.ai';
    }
    const token = inputToken?.value?.trim() || '';
    const label = inputLabel?.value?.trim() || '';

    if (!workbuddyEditId && !token) {
        writeWorkBuddyModalError('请填写 WorkBuddy Access Token (JWT)');
        return;
    }

    try {
        if (workbuddyEditId) {
            const res = safeParseIPC(await ipcRenderer.invoke('workbuddy:update', workbuddyEditId, baseUrl, token, label, ''));
            if (!res.success) {
                writeWorkBuddyModalError(res.error || '更新 WorkBuddy 账号失败');
                return;
            }
        } else {
            const res = safeParseIPC(await ipcRenderer.invoke('workbuddy:add', baseUrl, token, label, ''));
            if (!res.success) {
                writeWorkBuddyModalError(res.error || '添加 WorkBuddy 账号失败');
                return;
            }
        }
        closeWorkBuddyAccountModal();
    } catch (e: any) {
        writeWorkBuddyModalError('保存异常: ' + (e?.message || e));
    }
}

async function importLocalWorkBuddyAccount(): Promise<void> {
    if (!btnWorkBuddyImportLocal) return;
    btnWorkBuddyImportLocal.disabled = true;

    try {
        let res = safeParseIPC(await ipcRenderer.invoke('workbuddy:import-all-local'));
        if (!res.success) {
            res = safeParseIPC(await ipcRenderer.invoke('workbuddy:import-local'));
        }

        if (!res.success) {
            if (workbuddyImportAlert) {
                workbuddyImportAlert.textContent = '导入失败: ' + (res.error || '未发现本地登录态文件');
                workbuddyImportAlert.classList.remove('hidden', 'text-teal-700', 'border-teal-500/30', 'bg-teal-500/10');
                workbuddyImportAlert.classList.add('text-rose-600', 'border-rose-500/30', 'bg-rose-500/10');
            }
            writeWorkBuddyModalError(res.error || '未在本地找到 WorkBuddy 登录凭证');
            return;
        }

        if (workbuddyImportAlert) {
            if (res.count && res.count > 0) {
                workbuddyImportAlert.textContent = `✅ 成功同步本机 WorkBuddy 全部账号 (国内版/国际版共 ${res.count} 个)！`;
            } else {
                const acc = res.account || {};
                workbuddyImportAlert.textContent = `✅ 成功导入本地账号: ${acc.email || ''} (UID: ${acc.uid || ''})`;
            }
            workbuddyImportAlert.classList.remove('hidden', 'text-rose-600', 'border-rose-500/30', 'bg-rose-500/10');
            workbuddyImportAlert.classList.add('text-teal-700', 'border-teal-500/30', 'bg-teal-500/10');
        }
        setTimeout(() => {
            closeWorkBuddyAccountModal();
        }, 1200);
    } catch (e: any) {
        writeWorkBuddyModalError('导入异常: ' + (e?.message || e));
    } finally {
        if (btnWorkBuddyImportLocal) btnWorkBuddyImportLocal.disabled = false;
    }
}

// 挂载至 window 顶层，提供多重兜底调用能力
if (typeof window !== 'undefined') {
    (window as any).openWorkBuddyAccountModal = openWorkBuddyAccountModal;
    (window as any).closeWorkBuddyAccountModal = closeWorkBuddyAccountModal;
}
