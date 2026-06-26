import { ipcRenderer, shell } from '../shared/ipc';

export function initSettings() {
    console.log('[SettingsController] Initializing settings controller...');
    try {
        const chkEnableSystemLog = document.getElementById('chkEnableSystemLog') as HTMLInputElement | null;
        const systemConsole = document.getElementById('systemConsole');
        const chkEnableAutoStart = document.getElementById('chkEnableAutoStart') as HTMLInputElement | null;
        const chkEnableSilentStart = document.getElementById('chkEnableSilentStart') as HTMLInputElement | null;
        const numMaxRetries = document.getElementById('numMaxRetries') as HTMLInputElement | null;

        // Tab switching
        const btnSettingsTabGeneral = document.getElementById('btnSettingsTabGeneral');
        const btnSettingsTabAbout = document.getElementById('btnSettingsTabAbout');
        const btnSettingsTabRelay = document.getElementById('btnSettingsTabRelay');
        const settingsPanelGeneral = document.getElementById('settings-panel-general');
        const settingsPanelAbout = document.getElementById('settings-panel-about');
        const settingsPanelRelay = document.getElementById('settings-panel-relay');

        const activeTabClass = 'px-4 py-1.5 text-[12px] bg-white dark:bg-[#1a1f30] text-primary dark:text-primary-fixed-dim rounded-md shadow-sm font-bold cursor-pointer transition-all duration-200';
        const inactiveTabClass = 'px-4 py-1.5 text-[12px] text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200 rounded-md font-medium cursor-pointer transition-all duration-200';

        function switchSettingsTab(activePanel: string) {
            if (settingsPanelGeneral) settingsPanelGeneral.style.setProperty('display', activePanel === 'general' ? 'flex' : 'none', 'important');
            if (settingsPanelAbout) settingsPanelAbout.style.setProperty('display', activePanel === 'about' ? 'flex' : 'none', 'important');
            if (settingsPanelRelay) settingsPanelRelay.style.setProperty('display', activePanel === 'relay' ? 'flex' : 'none', 'important');

            if (btnSettingsTabGeneral) btnSettingsTabGeneral.className = activePanel === 'general' ? activeTabClass : inactiveTabClass;
            if (btnSettingsTabAbout) btnSettingsTabAbout.className = activePanel === 'about' ? activeTabClass : inactiveTabClass;
            if (btnSettingsTabRelay) btnSettingsTabRelay.className = activePanel === 'relay' ? activeTabClass : inactiveTabClass;
        }

        if (btnSettingsTabGeneral && btnSettingsTabAbout && settingsPanelGeneral && settingsPanelAbout) {
            btnSettingsTabGeneral.addEventListener('click', () => switchSettingsTab('general'));
            btnSettingsTabAbout.addEventListener('click', () => switchSettingsTab('about'));
            if (btnSettingsTabRelay) {
                btnSettingsTabRelay.addEventListener('click', () => switchSettingsTab('relay'));
            }
        } else {
            console.error('[SettingsController] Tab elements not found:', {
                btnSettingsTabGeneral: !!btnSettingsTabGeneral,
                btnSettingsTabAbout: !!btnSettingsTabAbout,
                settingsPanelGeneral: !!settingsPanelGeneral,
                settingsPanelAbout: !!settingsPanelAbout
            });
        }

        // About Panel External links
        const btnAboutRepo = document.getElementById('btnAboutRepo');
        const btnAboutChangelog = document.getElementById('btnAboutChangelog');
        const btnAboutFeedback = document.getElementById('btnAboutFeedback');

        if (btnAboutRepo) {
            btnAboutRepo.addEventListener('click', (e) => {
                e.preventDefault();
                shell.openExternal('https://github.com/weilimao/antigravityProxyGo');
            });
        }

        if (btnAboutChangelog) {
            btnAboutChangelog.addEventListener('click', (e) => {
                e.preventDefault();
                shell.openExternal('https://github.com/weilimao/antigravityProxyGo/releases');
            });
        }

        if (btnAboutFeedback) {
            btnAboutFeedback.addEventListener('click', (e) => {
                e.preventDefault();
                shell.openExternal('https://github.com/weilimao/antigravityProxyGo/issues');
            });
        }

        // Toggle listener
        if (chkEnableSystemLog) {
            chkEnableSystemLog.addEventListener('change', (e: any) => {
                const enabled = e.target.checked;
                try {
                    ipcRenderer.send('settings:set-system-log-enabled', enabled);
                    updateConsoleVisibility(enabled);
                } catch (err) {
                    console.error('[SettingsController] Failed to save log settings:', err);
                }
            });
        }

        if (chkEnableAutoStart) {
            chkEnableAutoStart.addEventListener('change', (e: any) => {
                const enabled = e.target.checked;
                try {
                    ipcRenderer.send('settings:set-auto-start', enabled);
                } catch (err) {
                    console.error('[SettingsController] Failed to save auto start settings:', err);
                }
            });
        }

        if (chkEnableSilentStart) {
            chkEnableSilentStart.addEventListener('change', (e: any) => {
                const enabled = e.target.checked;
                try {
                    ipcRenderer.send('settings:set-silent-start', enabled);
                } catch (err) {
                    console.error('[SettingsController] Failed to save silent start settings:', err);
                }
            });
        }

        if (numMaxRetries) {
            numMaxRetries.addEventListener('change', (e: any) => {
                const val = parseInt(e.target.value, 10);
                if (!isNaN(val) && val > 0) {
                    try {
                        ipcRenderer.send('settings:set-max-retries', val);
                    } catch (err) {
                        console.error('[SettingsController] Failed to save max retries:', err);
                    }
                }
            });
        }

        function updateConsoleVisibility(enabled: boolean) {
            if (systemConsole) {
                if (enabled) {
                    systemConsole.style.display = 'flex';
                } else {
                    systemConsole.style.display = 'none';
                }
            }
        }

        // Initial load
        refreshSettingsUI();

    } catch (globalErr) {
        console.error('[SettingsController] Global init error:', globalErr);
    }
}

export function refreshSettingsUI() {
    try {
        const chkEnableSystemLog = document.getElementById('chkEnableSystemLog') as HTMLInputElement | null;
        const systemConsole = document.getElementById('systemConsole');
        const chkEnableAutoStart = document.getElementById('chkEnableAutoStart') as HTMLInputElement | null;
        const chkEnableSilentStart = document.getElementById('chkEnableSilentStart') as HTMLInputElement | null;
        const numMaxRetries = document.getElementById('numMaxRetries') as HTMLInputElement | null;

        if (chkEnableSystemLog) {
            const enabled = ipcRenderer.sendSync('settings:get-system-log-enabled');
            if (enabled !== null && enabled !== undefined) {
                chkEnableSystemLog.checked = !!enabled;
                if (systemConsole) {
                    systemConsole.style.display = enabled ? 'flex' : 'none';
                }
            }
        }

        if (chkEnableAutoStart && chkEnableSilentStart) {
            const startupOptions = ipcRenderer.sendSync('settings:get-startup-options');
            if (startupOptions) {
                chkEnableAutoStart.checked = !!startupOptions.autoStart;
                chkEnableSilentStart.checked = !!startupOptions.silentStart;
            }
        }

        if (numMaxRetries) {
            const retries = ipcRenderer.sendSync('settings:get-max-retries');
            if (retries !== null && retries !== undefined) {
                numMaxRetries.value = String(retries);
            } else {
                numMaxRetries.value = '20';
            }
        }
    } catch (err) {
        console.error('[SettingsController] Failed to refresh settings UI:', err);
    }
}

// Global hook
(window as any).refreshSettingsUI = refreshSettingsUI;

