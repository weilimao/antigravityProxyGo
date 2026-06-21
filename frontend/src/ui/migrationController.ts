import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';

let txtDataDir: HTMLInputElement | null;
let btnBrowseDir: HTMLButtonElement | null;
let migrationStatus: HTMLElement | null;
let migrationStatusMsg: HTMLElement | null;

export function refreshDataDir() {
    txtDataDir = document.getElementById('txtDataDir') as HTMLInputElement | null;
    try {
        const res = ipcRenderer.sendSync('settings:get-dir-sync');
        if (res && res.activeDir && txtDataDir) {
            txtDataDir.value = res.activeDir;
        }
    } catch (err) {
        console.error('Failed to get data directory:', err);
    }
}

function showMigrationError(errText: string) {
    migrationStatus = document.getElementById('migrationStatus');
    migrationStatusMsg = document.getElementById('migrationStatusMsg');
    if (!migrationStatus || !migrationStatusMsg) return;

    migrationStatus.classList.remove('hidden');
    migrationStatus.className = 'text-[12px] p-3 rounded-lg border bg-rose-50 dark:bg-rose-950/30 border-rose-100 dark:border-rose-900/30 flex flex-col gap-1';
    const isZH = state.currentLanguage === 'zh';
    migrationStatusMsg.innerText = (isZH ? '❌ 迁移失败：' : '❌ Migration failed: ') + errText;
    migrationStatusMsg.className = 'text-[12px] text-rose-600 dark:text-rose-400 mt-1 font-medium';
}

export function initMigrationEvents() {
    txtDataDir = document.getElementById('txtDataDir') as HTMLInputElement | null;
    btnBrowseDir = document.getElementById('btnBrowseDir') as HTMLButtonElement | null;
    migrationStatus = document.getElementById('migrationStatus');
    migrationStatusMsg = document.getElementById('migrationStatusMsg');

    if (btnBrowseDir) {
        btnBrowseDir.addEventListener('click', async () => {
            if (!migrationStatus || !migrationStatusMsg || !btnBrowseDir) return;
            migrationStatus.classList.add('hidden');
            migrationStatusMsg.innerText = '';
            btnBrowseDir.disabled = true;
            try {
                const result = await ipcRenderer.invoke('settings:change-dir');
                if (result.success && result.activeDir && txtDataDir) {
                    txtDataDir.value = result.activeDir;
                } else if (result.error && result.error !== '用户取消选择') {
                    showMigrationError(result.error);
                }
            } catch (err: any) {
                showMigrationError(err.message);
            } finally {
                btnBrowseDir.disabled = false;
            }
        });
    }

    ipcRenderer.on('settings:migration-progress', (event: any, data: any) => {
        if (!migrationStatus || !migrationStatusMsg) return;
        migrationStatus.classList.remove('hidden');
        const isZH = state.currentLanguage === 'zh';

        if (data.step === 'error') {
            showMigrationError(data.status);
        } else if (data.step === 'success') {
            migrationStatus.className = 'text-[12px] p-3 rounded-lg border bg-emerald-50 dark:bg-emerald-950/30 border-emerald-100 dark:border-emerald-900/30 flex flex-col gap-1';
            migrationStatusMsg.innerText = isZH ? '🎉 数据迁移成功！已重定向至新存储路径。' : '🎉 Migration completed successfully! Redirected to the new path.';
            migrationStatusMsg.className = 'text-[12px] text-emerald-600 dark:text-emerald-400 mt-1 font-medium';
        } else {
            migrationStatus.className = 'text-[12px] p-3 rounded-lg border bg-slate-50 dark:bg-white/5 border-outline-variant/30 flex flex-col gap-1';
            let statusText = data.status;
            if (!isZH) {
                if (data.step === 'stop-proxy') statusText = 'Stopping proxy server...';
                else if (data.step === 'migrate-files') statusText = 'Migrating data files and certificates (Do not close)...';
                else if (data.step === 'update-paths') statusText = 'Redirecting internal path services...';
                else if (data.step === 'patch-externals') statusText = 'Updating external settings and certificate patches...';
                else if (data.step === 'restart-proxy') statusText = 'Restarting proxy server...';
            }
            migrationStatusMsg.innerText = statusText;
            migrationStatusMsg.className = 'text-[12px] text-outline mt-1 font-medium';
        }
    });
}

// Global hook
(window as any).refreshDataDir = refreshDataDir;
