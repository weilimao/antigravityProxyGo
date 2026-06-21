import { ipcRenderer, shell } from '../shared/ipc';

export function initSettings() {
    console.log('[SettingsController] Initializing settings controller...');
    try {
        const chkEnableSystemLog = document.getElementById('chkEnableSystemLog') as HTMLInputElement | null;
        const systemConsole = document.getElementById('systemConsole');
        const chkEnableAutoStart = document.getElementById('chkEnableAutoStart') as HTMLInputElement | null;
        const chkEnableSilentStart = document.getElementById('chkEnableSilentStart') as HTMLInputElement | null;

        // Tab switching
        const btnSettingsTabGeneral = document.getElementById('btnSettingsTabGeneral');
        const btnSettingsTabAbout = document.getElementById('btnSettingsTabAbout');
        const settingsPanelGeneral = document.getElementById('settings-panel-general');
        const settingsPanelAbout = document.getElementById('settings-panel-about');

        if (btnSettingsTabGeneral && btnSettingsTabAbout && settingsPanelGeneral && settingsPanelAbout) {
            btnSettingsTabGeneral.addEventListener('click', () => {
                settingsPanelGeneral.style.setProperty('display', 'flex', 'important');
                settingsPanelAbout.style.setProperty('display', 'none', 'important');

                // Active tab styling
                btnSettingsTabGeneral.className = 'px-4 py-1.5 text-[12px] bg-white dark:bg-[#1a1f30] text-primary dark:text-primary-fixed-dim rounded-md shadow-sm font-bold cursor-pointer transition-all duration-200';
                btnSettingsTabAbout.className = 'px-4 py-1.5 text-[12px] text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200 rounded-md font-medium cursor-pointer transition-all duration-200';
            });

            btnSettingsTabAbout.addEventListener('click', () => {
                settingsPanelGeneral.style.setProperty('display', 'none', 'important');
                settingsPanelAbout.style.setProperty('display', 'flex', 'important');

                // Active tab styling
                btnSettingsTabAbout.className = 'px-4 py-1.5 text-[12px] bg-white dark:bg-[#1a1f30] text-primary dark:text-primary-fixed-dim rounded-md shadow-sm font-bold cursor-pointer transition-all duration-200';
                btnSettingsTabGeneral.className = 'px-4 py-1.5 text-[12px] text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200 rounded-md font-medium cursor-pointer transition-all duration-200';
            });
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

        // Fetch initial state
        if (chkEnableSystemLog) {
            try {
                const enabled = ipcRenderer.sendSync('settings:get-system-log-enabled');
                chkEnableSystemLog.checked = !!enabled;
                updateConsoleVisibility(!!enabled);
            } catch (err) {
                console.error('[SettingsController] Failed to fetch initial log settings:', err);
            }

            // Toggle listener
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

        try {
            if (chkEnableAutoStart && chkEnableSilentStart) {
                const startupOptions = ipcRenderer.sendSync('settings:get-startup-options');
                chkEnableAutoStart.checked = !!startupOptions.autoStart;
                chkEnableSilentStart.checked = !!startupOptions.silentStart;
            }
        } catch (err) {
            console.error('[SettingsController] Failed to fetch startup options:', err);
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

        function updateConsoleVisibility(enabled: boolean) {
            if (systemConsole) {
                if (enabled) {
                    systemConsole.style.display = 'flex';
                } else {
                    systemConsole.style.display = 'none';
                }
            }
        }
    } catch (globalErr) {
        console.error('[SettingsController] Global init error:', globalErr);
    }
}
