import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';
import translations from '../shared/i18n';

let lblCurrentVersion: HTMLElement | null = null;
let btnCheckUpdate: HTMLButtonElement | null = null;
let iconCheckUpdate: HTMLElement | null = null;

let updateModal: HTMLElement | null = null;
let updateModalContainer: HTMLElement | null = null;
let updateModalCloseBtn: HTMLElement | null = null;
let updateModalIcon: HTMLElement | null = null;
let updateModalStatusTitle: HTMLElement | null = null;
let updateModalStatusMsg: HTMLElement | null = null;
let updateModalChangelogContainer: HTMLElement | null = null;
let updateModalChangelog: HTMLElement | null = null;
let updateModalProgressContainer: HTMLElement | null = null;
let updateModalProgressBarFill: HTMLElement | null = null;
let updateModalActions: HTMLElement | null = null;
let btnUpdateModalConfirm: HTMLButtonElement | null = null;
let btnUpdateModalCancel: HTMLButtonElement | null = null;

let latestUpdateAssets: any = null;
let downloadedInstallerPath: string | null = null;
let updaterState = 'idle';
let isManualCheck = false;

function showModal() {
    updateModal = document.getElementById('updateModal');
    updateModalContainer = document.getElementById('updateModalContainer');
    if (updateModal && updateModalContainer) {
        updateModal.classList.remove('opacity-0', 'pointer-events-none');
        updateModalContainer.classList.remove('scale-95');
        updateModalContainer.classList.add('scale-100');
    }
}

function hideModal() {
    updateModal = document.getElementById('updateModal');
    updateModalContainer = document.getElementById('updateModalContainer');
    if (updateModal && updateModalContainer) {
        updateModal.classList.add('opacity-0', 'pointer-events-none');
        updateModalContainer.classList.add('scale-95');
        updateModalContainer.classList.remove('scale-100');
    }
}

export function setUpdaterUIState(uiState: string, info: any = {}) {
    updaterState = uiState;
    const dict = translations[state.currentLanguage] || {};

    lblCurrentVersion = document.getElementById('lblCurrentVersion');
    btnCheckUpdate = document.getElementById('btnCheckUpdate') as HTMLButtonElement | null;
    iconCheckUpdate = document.getElementById('iconCheckUpdate');

    updateModal = document.getElementById('updateModal');
    updateModalContainer = document.getElementById('updateModalContainer');
    updateModalCloseBtn = document.getElementById('updateModalCloseBtn');
    updateModalIcon = document.getElementById('updateModalIcon');
    updateModalStatusTitle = document.getElementById('updateModalStatusTitle');
    updateModalStatusMsg = document.getElementById('updateModalStatusMsg');
    updateModalChangelogContainer = document.getElementById('updateModalChangelogContainer');
    updateModalChangelog = document.getElementById('updateModalChangelog');
    updateModalProgressContainer = document.getElementById('updateModalProgressContainer');
    updateModalProgressBarFill = document.getElementById('updateModalProgressBarFill');
    updateModalActions = document.getElementById('updateModalActions');
    btnUpdateModalConfirm = document.getElementById('btnUpdateModalConfirm') as HTMLButtonElement | null;
    btnUpdateModalCancel = document.getElementById('btnUpdateModalCancel') as HTMLButtonElement | null;

    if (!updateModal || !updateModalContainer || !updateModalIcon || !updateModalStatusTitle || 
        !updateModalStatusMsg || !updateModalChangelogContainer || !updateModalChangelog || 
        !updateModalProgressContainer || !updateModalProgressBarFill || !updateModalActions || 
        !btnUpdateModalCancel || !btnUpdateModalConfirm) {
        return;
    }

    // Reset default visibility/states
    updateModalChangelogContainer.classList.add('hidden');
    updateModalProgressContainer.classList.add('hidden');
    updateModalActions.classList.add('hidden');
    btnUpdateModalCancel.classList.remove('hidden');
    btnUpdateModalConfirm.classList.remove('hidden');
    if (updateModalCloseBtn) {
        updateModalCloseBtn.classList.remove('pointer-events-none', 'opacity-50');
    }

    if (uiState === 'idle') {
        hideModal();
    } else if (uiState === 'checking') {
        if (isManualCheck) {
            showModal();
            updateModalIcon.textContent = 'sync';
            updateModalIcon.className = 'material-symbols-outlined text-[36px] text-primary animate-spin';
            updateModalStatusTitle.textContent = dict.checkingUpdates || '正在检查更新...';
            updateModalStatusMsg.textContent = '';
            
            // Allow cancelling check
            updateModalActions.classList.remove('hidden');
            btnUpdateModalConfirm.classList.add('hidden');
            btnUpdateModalCancel.textContent = dict.btnClose || '关闭';
            btnUpdateModalCancel.onclick = () => {
                hideModal();
                setUpdaterUIState('idle');
            };
        }
    } else if (uiState === 'update-available') {
        showModal();
        updateModalIcon.textContent = 'rocket_launch';
        updateModalIcon.className = 'material-symbols-outlined text-[36px] text-amber-500 animate-none';
        updateModalStatusTitle.textContent = (dict.updateAvailable || '发现新版本可用！') + ` (v${info.latestVersion})`;
        updateModalStatusMsg.textContent = `${dict.currentVersionLabel || '当前版本'}: v${info.currentVersion}`;
        
        if (info.releaseNotes) {
            updateModalChangelogContainer.classList.remove('hidden');
            updateModalChangelog.textContent = info.releaseNotes;
        }

        updateModalActions.classList.remove('hidden');
        btnUpdateModalConfirm.textContent = dict.btnUpdateNow || '立即更新';
        btnUpdateModalConfirm.className = 'px-4 py-2 text-[12px] font-bold bg-primary text-white hover:bg-primary/90 rounded-lg transition-colors shadow-sm cursor-pointer';
        btnUpdateModalConfirm.onclick = async () => {
            if (latestUpdateAssets) {
                setUpdaterUIState('downloading');
                try {
                    await ipcRenderer.invoke('app:start-download-update', latestUpdateAssets);
                } catch (err: any) {
                    setUpdaterUIState('error', { message: err.message || err });
                }
            }
        };

        btnUpdateModalCancel.textContent = dict.btnUpdateLater || '暂不更新';
        btnUpdateModalCancel.onclick = () => {
            hideModal();
            setUpdaterUIState('idle');
        };
    } else if (uiState === 'no-update') {
        if (isManualCheck) {
            showModal();
            updateModalIcon.textContent = 'check_circle';
            updateModalIcon.className = 'material-symbols-outlined text-[36px] text-emerald-500 animate-none';
            updateModalStatusTitle.textContent = dict.alreadyLatest || '已是最新版本';
            updateModalStatusMsg.textContent = `${dict.currentVersionLabel || '当前版本'}: v${info.currentVersion || '1.0.4'}`;
            
            updateModalActions.classList.remove('hidden');
            btnUpdateModalConfirm.classList.add('hidden');
            btnUpdateModalCancel.textContent = dict.btnClose || '关闭';
            btnUpdateModalCancel.onclick = () => {
                hideModal();
                setUpdaterUIState('idle');
            };
        }
    } else if (uiState === 'downloading') {
        showModal();
        if (updateModalCloseBtn) {
            updateModalCloseBtn.classList.add('pointer-events-none', 'opacity-50');
        }

        updateModalIcon.textContent = 'download';
        updateModalIcon.className = 'material-symbols-outlined text-[36px] text-primary animate-bounce';
        updateModalStatusTitle.textContent = dict.downloadingUpdate || '正在下载更新包...';
        
        const percent = info.percent || 0;
        updateModalStatusMsg.textContent = `Progress: ${percent}%`;
        
        updateModalProgressContainer.classList.remove('hidden');
        updateModalProgressBarFill.style.width = `${percent}%`;
    } else if (uiState === 'downloaded') {
        showModal();
        updateModalIcon.textContent = 'download_done';
        updateModalIcon.className = 'material-symbols-outlined text-[36px] text-emerald-500 animate-none';
        updateModalStatusTitle.textContent = dict.downloadComplete || '下载完成，重启后生效';
        updateModalStatusMsg.textContent = '';
        
        updateModalActions.classList.remove('hidden');
        btnUpdateModalConfirm.textContent = dict.btnRestartNow || '立即重启';
        btnUpdateModalConfirm.className = 'px-4 py-2 text-[12px] font-bold bg-emerald-600 text-white hover:bg-emerald-700 rounded-lg transition-colors shadow-sm cursor-pointer';
        btnUpdateModalConfirm.onclick = () => {
            if (downloadedInstallerPath) {
                ipcRenderer.send('app:install-update', downloadedInstallerPath);
            }
        };

        btnUpdateModalCancel.textContent = dict.btnLaterRestart || '稍后重启';
        btnUpdateModalCancel.onclick = () => {
            hideModal();
            setUpdaterUIState('idle');
        };
    } else if (uiState === 'error') {
        showModal();
        updateModalIcon.textContent = 'error';
        updateModalIcon.className = 'material-symbols-outlined text-[36px] text-rose-500 animate-none';
        updateModalStatusTitle.textContent = dict.updateFailed || '更新失败';
        updateModalStatusMsg.textContent = info.message || 'Unknown error occurred.';
        
        updateModalActions.classList.remove('hidden');
        btnUpdateModalConfirm.classList.add('hidden');
        btnUpdateModalCancel.textContent = dict.btnClose || '关闭';
        btnUpdateModalCancel.onclick = () => {
            hideModal();
            setUpdaterUIState('idle');
        };
    }
}

export async function initAppVersion() {
    lblCurrentVersion = document.getElementById('lblCurrentVersion');
    try {
        const ver = await ipcRenderer.invoke('app:get-version');
        if (lblCurrentVersion) {
            lblCurrentVersion.textContent = `v${ver}`;
        }
    } catch (err) {
        console.error('Failed to get app version:', err);
    }
}

export async function initAboutPanelEvents() {
    lblCurrentVersion = document.getElementById('lblCurrentVersion');
    btnCheckUpdate = document.getElementById('btnCheckUpdate') as HTMLButtonElement | null;
    iconCheckUpdate = document.getElementById('iconCheckUpdate');

    // 1. 初始化关于页面的版本号显示
    await initAppVersion();

    // 2. 绑定检查更新按钮的点击事件
    if (btnCheckUpdate) {
        btnCheckUpdate.onclick = async () => {
            isManualCheck = true;
            if (iconCheckUpdate) iconCheckUpdate.classList.add('animate-spin');
            setUpdaterUIState('checking');
            try {
                await ipcRenderer.invoke('app:check-for-updates', true);
            } catch (err: any) {
                setUpdaterUIState('error', { message: err.message || err });
            } finally {
                if (iconCheckUpdate) iconCheckUpdate.classList.remove('animate-spin');
            }
        };
    }
}

export function initUpdaterEvents() {
    updateModalCloseBtn = document.getElementById('updateModalCloseBtn');
    if (updateModalCloseBtn) {
        updateModalCloseBtn.addEventListener('click', () => {
            if (updaterState !== 'downloading') {
                hideModal();
                setUpdaterUIState('idle');
            }
        });
    }

    ipcRenderer.on('app:update-available', (event: any, data: any) => {
        latestUpdateAssets = data.assets;
        downloadedInstallerPath = null;
        setUpdaterUIState('update-available', data);
    });

    ipcRenderer.on('app:update-not-available', (event: any, data: any) => {
        setUpdaterUIState('no-update', data);
    });

    ipcRenderer.on('app:download-progress', (event: any, progress: any) => {
        setUpdaterUIState('downloading', progress);
    });

    ipcRenderer.on('app:download-complete', (event: any, filePath: string) => {
        downloadedInstallerPath = filePath;
        setUpdaterUIState('downloaded');
    });

    ipcRenderer.on('app:update-error', (event: any, errMsg: string) => {
        setUpdaterUIState('error', { message: errMsg });
    });

    // 启动 5 秒后，在后台静默触发一次自动版本检查
    setTimeout(async () => {
        isManualCheck = false;
        console.log('[Updater] Triggering automatic version check on startup...');
        try {
            await ipcRenderer.invoke('app:check-for-updates', false);
        } catch (err) {
            console.error('Failed auto version check on startup:', err);
        }
    }, 5000);
}
