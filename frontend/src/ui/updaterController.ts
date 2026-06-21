import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';
import translations from '../shared/i18n';

let lblCurrentVersion: HTMLElement | null;
let btnCheckUpdate: HTMLButtonElement | null;
let iconCheckUpdate: HTMLElement | null;
let updateStatusContainer: HTMLElement | null;
let updateStatusIcon: HTMLElement | null;
let updateStatusTitle: HTMLElement | null;
let updateStatusMsg: HTMLElement | null;
let updateProgressBarContainer: HTMLElement | null;
let updateProgressBarFill: HTMLElement | null;
let updateActionsGroup: HTMLElement | null;
let btnUpdateActionConfirm: HTMLButtonElement | null;
let btnUpdateActionCancel: HTMLButtonElement | null;

let latestUpdateAssets: any = null;
let downloadedInstallerPath: string | null = null;
let updaterState = 'idle';

export function setUpdaterUIState(uiState: string, info: any = {}) {
    updaterState = uiState;
    const dict = translations[state.currentLanguage] || {};

    lblCurrentVersion = document.getElementById('lblCurrentVersion');
    btnCheckUpdate = document.getElementById('btnCheckUpdate') as HTMLButtonElement | null;
    iconCheckUpdate = document.getElementById('iconCheckUpdate');
    updateStatusContainer = document.getElementById('updateStatusContainer');
    updateStatusIcon = document.getElementById('updateStatusIcon');
    updateStatusTitle = document.getElementById('updateStatusTitle');
    updateStatusMsg = document.getElementById('updateStatusMsg');
    updateProgressBarContainer = document.getElementById('updateProgressBarContainer');
    updateProgressBarFill = document.getElementById('updateProgressBarFill');
    updateActionsGroup = document.getElementById('updateActionsGroup');
    btnUpdateActionConfirm = document.getElementById('btnUpdateActionConfirm') as HTMLButtonElement | null;
    btnUpdateActionCancel = document.getElementById('btnUpdateActionCancel') as HTMLButtonElement | null;

    if (!updateStatusContainer || !updateProgressBarContainer || !updateActionsGroup || !btnCheckUpdate || !iconCheckUpdate || !updateStatusIcon || !updateStatusTitle || !updateStatusMsg || !updateProgressBarFill) return;

    updateStatusContainer.classList.remove('hidden');
    updateProgressBarContainer.classList.add('hidden');
    updateActionsGroup.classList.add('hidden');
    btnCheckUpdate.disabled = false;
    iconCheckUpdate.classList.remove('animate-spin');

    if (uiState === 'idle') {
        updateStatusContainer.classList.add('hidden');
    } else if (uiState === 'checking') {
        btnCheckUpdate.disabled = true;
        iconCheckUpdate.classList.add('animate-spin');
        updateStatusIcon.textContent = 'sync';
        updateStatusIcon.className = 'material-symbols-outlined text-[16px] text-primary animate-spin';
        updateStatusTitle.textContent = dict.checkingUpdates || '正在检查更新...';
        updateStatusMsg.textContent = '';
    } else if (uiState === 'update-available') {
        updateStatusIcon.textContent = 'rocket_launch';
        updateStatusIcon.className = 'material-symbols-outlined text-[16px] text-amber-500';
        updateStatusTitle.textContent = (dict.updateAvailable || '发现新版本可用！') + ` (${info.latestVersion})`;
        updateStatusMsg.textContent = info.releaseNotes || 'No release notes.';
        
        updateActionsGroup.classList.remove('hidden');
        if (btnUpdateActionConfirm) {
            btnUpdateActionConfirm.textContent = dict.btnUpdateNow || '立即更新';
            btnUpdateActionConfirm.className = 'px-3 py-1.5 bg-primary text-white hover:bg-primary/90 rounded-md text-[12px] font-bold transition-all shadow-sm cursor-pointer';
            btnUpdateActionConfirm.onclick = async () => {
                if (latestUpdateAssets) {
                    setUpdaterUIState('downloading');
                    try {
                        await ipcRenderer.invoke('app:start-download-update', latestUpdateAssets);
                    } catch (err: any) {
                        setUpdaterUIState('error', { message: err.message || err });
                    }
                }
            };
        }

        if (btnUpdateActionCancel) {
            btnUpdateActionCancel.textContent = dict.btnUpdateLater || '暂不更新';
            btnUpdateActionCancel.onclick = () => setUpdaterUIState('idle');
        }
    } else if (uiState === 'no-update') {
        updateStatusIcon.textContent = 'check_circle';
        updateStatusIcon.className = 'material-symbols-outlined text-[16px] text-emerald-500';
        updateStatusTitle.textContent = dict.alreadyLatest || '已是最新版本';
        updateStatusMsg.textContent = '';
        setTimeout(() => {
            if (updaterState === 'no-update') setUpdaterUIState('idle');
        }, 3000);
    } else if (uiState === 'downloading') {
        btnCheckUpdate.disabled = true;
        updateStatusIcon.textContent = 'download';
        updateStatusIcon.className = 'material-symbols-outlined text-[16px] text-primary animate-bounce';
        updateStatusTitle.textContent = dict.downloadingUpdate || '正在下载更新包...';
        
        const percent = info.percent || 0;
        updateStatusMsg.textContent = `Progress: ${percent}%`;
        updateProgressBarContainer.classList.remove('hidden');
        updateProgressBarFill.style.width = `${percent}%`;
    } else if (uiState === 'downloaded') {
        btnCheckUpdate.disabled = true;
        updateStatusIcon.textContent = 'download_done';
        updateStatusIcon.className = 'material-symbols-outlined text-[16px] text-emerald-500';
        updateStatusTitle.textContent = dict.downloadComplete || '下载完成，重启后生效';
        updateStatusMsg.textContent = '';
        
        updateActionsGroup.classList.remove('hidden');
        if (btnUpdateActionConfirm) {
            btnUpdateActionConfirm.textContent = dict.btnRestartNow || '立即重启';
            btnUpdateActionConfirm.className = 'px-3 py-1.5 bg-emerald-600 text-white hover:bg-emerald-700 rounded-md text-[12px] font-bold transition-all shadow-sm cursor-pointer';
            btnUpdateActionConfirm.onclick = () => {
                if (downloadedInstallerPath) {
                    ipcRenderer.send('app:install-update', downloadedInstallerPath);
                }
            };
        }

        if (btnUpdateActionCancel) {
            btnUpdateActionCancel.textContent = dict.btnLaterRestart || '稍后重启';
            btnUpdateActionCancel.onclick = () => setUpdaterUIState('idle');
        }
    } else if (uiState === 'error') {
        updateStatusIcon.textContent = 'error';
        updateStatusIcon.className = 'material-symbols-outlined text-[16px] text-rose-500';
        updateStatusTitle.textContent = dict.updateFailed || '更新失败';
        updateStatusMsg.textContent = info.message || 'Unknown error occurred.';
        setTimeout(() => {
            if (updaterState === 'error') setUpdaterUIState('idle');
        }, 5000);
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

export function initUpdaterEvents() {
    btnCheckUpdate = document.getElementById('btnCheckUpdate') as HTMLButtonElement | null;
    
    if (btnCheckUpdate) {
        btnCheckUpdate.addEventListener('click', async () => {
            setUpdaterUIState('checking');
            try {
                await ipcRenderer.invoke('app:check-for-updates', true);
            } catch (err: any) {
                setUpdaterUIState('error', { message: err.message || err });
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
}
