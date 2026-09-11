import { ipcRenderer, shell } from '../shared/ipc';
import { bindProxySettings, loadProxyState } from './settingsProxy';
import state from './dashboardState';
import {
    deactivateNetworkLogs,
    initSettingsNetworkListeners,
    startNetworkLogsAutoRefresh,
    stopNetworkLogsAutoRefresh,
} from './settingsNetwork';
import { refreshOcrModel } from './ocrSettings';

export function deactivateSettings() {
    deactivateNetworkLogs();
}

export function onSettingsTabChanged(cb: (tab: string) => void): () => void {
    const handler = (e: any) => {
        if (e && e.detail && e.detail.activePanel) {
            cb(e.detail.activePanel);
        }
    };
    window.addEventListener('settings:tab-change', handler);
    return () => window.removeEventListener('settings:tab-change', handler);
}

function updatePacketCaptureVisibility(enabled: boolean) {
    const ids = ['navPacketsLink', 'navPacketsLinkDropdown'];
    ids.forEach(id => {
        const el = document.getElementById(id);
        if (el) {
            if (enabled) {
                el.style.setProperty('display', 'flex', 'important');
            } else {
                el.style.setProperty('display', 'none', 'important');
            }
        }
    });
    if (!enabled && window.location.hash.includes('/packets')) {
        window.location.hash = '#/dashboard';
    }
}

export function initSettings() {
    console.log('[SettingsController] Initializing settings controller...');
    try {
        const chkEnableSystemLog = document.getElementById('chkEnableSystemLog') as HTMLInputElement | null;
        const systemConsole = document.getElementById('systemConsole');
        const chkEnableAutoStart = document.getElementById('chkEnableAutoStart') as HTMLInputElement | null;
        const chkEnableSilentStart = document.getElementById('chkEnableSilentStart') as HTMLInputElement | null;
        const numMaxRetries = document.getElementById('numMaxRetries') as HTMLInputElement | null;
        const numMaxRetryDelay = document.getElementById('numMaxRetryDelay') as HTMLInputElement | null;
        const numMaxRequestBodyMB = document.getElementById('numMaxRequestBodyMB') as HTMLInputElement | null;
        const numRequestTimeout = document.getElementById('numRequestTimeout') as HTMLInputElement | null;

        // Tab switching
        const activeTabClass = 'px-4 py-1.5 text-[12px] bg-white dark:bg-[#1a1f30] text-primary dark:text-primary-fixed-dim rounded-md shadow-sm font-bold cursor-pointer transition-all duration-200';
        const inactiveTabClass = 'px-4 py-1.5 text-[12px] text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200 rounded-md font-medium cursor-pointer transition-all duration-200';

        initSettingsNetworkListeners();

        function switchSettingsTab(activePanel: string) {
            const settingsPanelGeneral = document.getElementById('settings-panel-general');
            const settingsPanelAbout = document.getElementById('settings-panel-about');
            const settingsPanelRelay = document.getElementById('settings-panel-relay');
            const settingsPanelNetwork = document.getElementById('settings-panel-network');
            const settingsPanelHelp = document.getElementById('settings-panel-help');
            const settingsPanelNvidia = document.getElementById('settings-panel-nvidia');
            const settingsPanelAgentConfig = document.getElementById('settings-panel-agentconfig');
            const settingsPanelWallpaper = document.getElementById('settings-panel-wallpaper');

            const btnSettingsTabGeneral = document.getElementById('btnSettingsTabGeneral');
            const btnSettingsTabAbout = document.getElementById('btnSettingsTabAbout');
            const btnSettingsTabRelay = document.getElementById('btnSettingsTabRelay');
            const btnSettingsTabNetwork = document.getElementById('btnSettingsTabNetwork');
            const btnSettingsTabHelp = document.getElementById('btnSettingsTabHelp');
            const btnSettingsTabNvidia = document.getElementById('btnSettingsTabNvidia');
            const btnSettingsTabAgentConfig = document.getElementById('btnSettingsTabAgentConfig');
            const btnSettingsTabWallpaper = document.getElementById('btnSettingsTabWallpaper');

            if (settingsPanelGeneral) settingsPanelGeneral.style.setProperty('display', activePanel === 'general' ? 'flex' : 'none', 'important');
            if (settingsPanelAbout) settingsPanelAbout.style.setProperty('display', activePanel === 'about' ? 'flex' : 'none', 'important');
            if (settingsPanelRelay) settingsPanelRelay.style.setProperty('display', activePanel === 'relay' ? 'flex' : 'none', 'important');
            if (settingsPanelNetwork) settingsPanelNetwork.style.setProperty('display', activePanel === 'network' ? 'flex' : 'none', 'important');
            if (settingsPanelHelp) settingsPanelHelp.style.setProperty('display', activePanel === 'help' ? 'flex' : 'none', 'important');
            if (settingsPanelNvidia) settingsPanelNvidia.style.setProperty('display', activePanel === 'nvidia' ? 'flex' : 'none', 'important');
            if (settingsPanelAgentConfig) settingsPanelAgentConfig.style.setProperty('display', activePanel === 'agentconfig' ? 'flex' : 'none', 'important');
            if (settingsPanelWallpaper) settingsPanelWallpaper.style.setProperty('display', activePanel === 'wallpaper' ? 'flex' : 'none', 'important');

            if (btnSettingsTabGeneral) btnSettingsTabGeneral.className = activePanel === 'general' ? activeTabClass : inactiveTabClass;
            if (btnSettingsTabAbout) btnSettingsTabAbout.className = activePanel === 'about' ? activeTabClass : inactiveTabClass;
            if (btnSettingsTabRelay) btnSettingsTabRelay.className = activePanel === 'relay' ? activeTabClass : inactiveTabClass;
            if (btnSettingsTabNetwork) btnSettingsTabNetwork.className = activePanel === 'network' ? activeTabClass : inactiveTabClass;
            if (btnSettingsTabHelp) btnSettingsTabHelp.className = activePanel === 'help' ? activeTabClass : inactiveTabClass;
            if (btnSettingsTabNvidia) btnSettingsTabNvidia.className = activePanel === 'nvidia' ? activeTabClass : inactiveTabClass;
            if (btnSettingsTabAgentConfig) btnSettingsTabAgentConfig.className = activePanel === 'agentconfig' ? activeTabClass : inactiveTabClass;
            if (btnSettingsTabWallpaper) btnSettingsTabWallpaper.className = activePanel === 'wallpaper' ? activeTabClass : inactiveTabClass;

            if (activePanel === 'network') {
                try {
                    ipcRenderer.send('settings:get-network-status');
                    ipcRenderer.send('settings:get-network-logs');
                    startNetworkLogsAutoRefresh();
                } catch (e) {
                    console.error('[SettingsController] Failed to load network data:', e);
                }
            } else {
                stopNetworkLogsAutoRefresh();
            }

            try {
                window.dispatchEvent(new CustomEvent('settings:tab-change', { detail: { activePanel } }));
            } catch (e) {
                // ignore
            }
        }

        (window as any).switchSettingsTab = switchSettingsTab;

        const btnSettingsTabGeneral = document.getElementById('btnSettingsTabGeneral');
        const btnSettingsTabAbout = document.getElementById('btnSettingsTabAbout');
        const btnSettingsTabRelay = document.getElementById('btnSettingsTabRelay');
        const btnSettingsTabNetwork = document.getElementById('btnSettingsTabNetwork');
        const btnSettingsTabHelp = document.getElementById('btnSettingsTabHelp');
        const btnSettingsTabNvidia = document.getElementById('btnSettingsTabNvidia');
        const btnSettingsTabAgentConfig = document.getElementById('btnSettingsTabAgentConfig');
        const btnSettingsTabWallpaper = document.getElementById('btnSettingsTabWallpaper');

        if (btnSettingsTabGeneral) btnSettingsTabGeneral.addEventListener('click', () => switchSettingsTab('general'));
        if (btnSettingsTabAbout) btnSettingsTabAbout.addEventListener('click', () => switchSettingsTab('about'));
        if (btnSettingsTabRelay) btnSettingsTabRelay.addEventListener('click', () => switchSettingsTab('relay'));
        if (btnSettingsTabNetwork) btnSettingsTabNetwork.addEventListener('click', () => switchSettingsTab('network'));
        if (btnSettingsTabHelp) btnSettingsTabHelp.addEventListener('click', () => switchSettingsTab('help'));
        if (btnSettingsTabNvidia) btnSettingsTabNvidia.addEventListener('click', () => switchSettingsTab('nvidia'));
        if (btnSettingsTabAgentConfig) btnSettingsTabAgentConfig.addEventListener('click', () => switchSettingsTab('agentconfig'));
        if (btnSettingsTabWallpaper) btnSettingsTabWallpaper.addEventListener('click', () => switchSettingsTab('wallpaper'));

        const btnRefreshNetLogs = document.getElementById('btnRefreshNetLogs');
        if (btnRefreshNetLogs) {
            btnRefreshNetLogs.addEventListener('click', () => {
                try {
                    ipcRenderer.send('settings:get-network-status');
                    ipcRenderer.send('settings:get-network-logs');
                } catch (e) {
                    console.error('[SettingsController] Manual refresh failed:', e);
                }
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

        if (numMaxRetryDelay) {
            numMaxRetryDelay.addEventListener('change', (e: any) => {
                const val = parseInt(e.target.value, 10);
                if (!isNaN(val) && val > 0) {
                    try {
                        ipcRenderer.send('settings:set-max-retry-delay', val);
                    } catch (err) {
                        console.error('[SettingsController] Failed to save max retry delay:', err);
                    }
                }
            });
        }

        if (numMaxRequestBodyMB) {
            numMaxRequestBodyMB.addEventListener('change', (e: any) => {
                const val = parseInt(e.target.value, 10);
                if (!isNaN(val) && val > 0) {
                    try {
                        ipcRenderer.send('settings:set-max-request-body-mb', val);
                    } catch (err) {
                        console.error('[SettingsController] Failed to save max request body MB:', err);
                    }
                }
            });
        }

        if (numRequestTimeout) {
            numRequestTimeout.addEventListener('change', (e: any) => {
                const val = parseInt(e.target.value, 10);
                if (!isNaN(val) && val > 0) {
                    try {
                        ipcRenderer.send('settings:set-request-timeout', val);
                    } catch (err) {
                        console.error('[SettingsController] Failed to save request timeout:', err);
                    }
                }
            });
        }

        const chkEnablePacketCapture = document.getElementById('chkEnablePacketCapture') as HTMLInputElement | null;
        if (chkEnablePacketCapture) {
            chkEnablePacketCapture.addEventListener('change', (e: any) => {
                const enabled = e.target.checked;
                try {
                    ipcRenderer.send('settings:set-packet-capture-enabled', enabled);
                    updatePacketCaptureVisibility(enabled);
                } catch (err) {
                    console.error('[SettingsController] Failed to save packet capture settings:', err);
                }
            });
        }

        const chkEnableDebuggerMode = document.getElementById('chkEnableDebuggerMode') as HTMLInputElement | null;
        const txtDebuggerLogPath = document.getElementById('txtDebuggerLogPath') as HTMLInputElement | null;
        const btnBrowseDebuggerDir = document.getElementById('btnBrowseDebuggerDir');

        if (chkEnableDebuggerMode) {
            chkEnableDebuggerMode.addEventListener('change', (e: any) => {
                const enabled = e.target.checked;
                try {
                    ipcRenderer.send('settings:set-debugger-mode', enabled);
                } catch (err) {
                    console.error('[SettingsController] Failed to save debugger mode settings:', err);
                }
            });
        }

        if (txtDebuggerLogPath) {
            txtDebuggerLogPath.addEventListener('change', (e: any) => {
                const pathVal = e.target.value;
                try {
                    ipcRenderer.send('settings:set-debugger-log-path', pathVal);
                } catch (err) {
                    console.error('[SettingsController] Failed to save debugger log path:', err);
                }
            });
        }

        if (btnBrowseDebuggerDir) {
            btnBrowseDebuggerDir.addEventListener('click', () => {
                try {
                    ipcRenderer.send('settings:select-debugger-log-dir');
                } catch (err) {
                    console.error('[SettingsController] Failed to browse debugger log dir:', err);
                }
            });
        }

        ipcRenderer.on('settings:debugger-mode-res', (_: any, res: any) => {
            if (res) {
                if (chkEnableDebuggerMode) {
                    chkEnableDebuggerMode.checked = res.enabled ?? false;
                }
                if (txtDebuggerLogPath) {
                    txtDebuggerLogPath.value = res.path || 'logs/debugger';
                }
            }
        });

        ipcRenderer.on('settings:debugger-log-path-res', (_: any, newPath: string) => {
            if (txtDebuggerLogPath && newPath) {
                txtDebuggerLogPath.value = newPath;
            }
        });

        try {
            ipcRenderer.send('settings:get-debugger-mode');
        } catch (e) {
            console.error('[SettingsController] Failed to get debugger mode:', e);
        }

        bindProxySettings();

        const txtPromptPrefix = document.getElementById('txtPromptPrefix') as HTMLTextAreaElement | null;
        if (txtPromptPrefix) {
            txtPromptPrefix.addEventListener('change', (e: any) => {
                const val = e.target.value;
                try {
                    ipcRenderer.send('settings:set-prompt-prefix', val);
                } catch (err) {
                    console.error('[SettingsController] Failed to save prompt prefix:', err);
                }
            });
        }

        const chkEnableCustomModelOverride = document.getElementById('chkEnableCustomModelOverride') as HTMLInputElement | null;
        if (chkEnableCustomModelOverride) {
            chkEnableCustomModelOverride.addEventListener('change', (e: any) => {
                try {
                    ipcRenderer.send('settings:set-custom-model-override-enabled', e.target.checked);
                } catch (err) {
                    console.error('[SettingsController] Failed to save custom model override enabled:', err);
                }
            });
        }

        const txtCustomModelOverrideID = document.getElementById('txtCustomModelOverrideID') as HTMLInputElement | null;
        if (txtCustomModelOverrideID) {
            txtCustomModelOverrideID.addEventListener('change', (e: any) => {
                try {
                    ipcRenderer.send('settings:set-custom-model-override-id', e.target.value);
                } catch (err) {
                    console.error('[SettingsController] Failed to save custom model override ID:', err);
                }
            });
        }

        const txtBypassOverridePrefixes = document.getElementById('txtBypassOverridePrefixes') as HTMLInputElement | null;
        if (txtBypassOverridePrefixes) {
            txtBypassOverridePrefixes.addEventListener('change', (e: any) => {
                try {
                    // 逗号分隔字符串 → 去空白后过滤空串成数组,与后端 []string IPC 对齐。
                    const raw: string = e.target.value || '';
                    const prefixes = raw
                        .split(',')
                        .map((s: string) => s.trim())
                        .filter((s: string) => s !== '');
                    ipcRenderer.send('settings:set-bypass-override-prefixes', prefixes);
                } catch (err) {
                    console.error('[SettingsController] Failed to save bypass override prefixes:', err);
                }
            });
        }

        const chkEnableCustomThinkingOverride = document.getElementById('chkEnableCustomThinkingOverride') as HTMLInputElement | null;
        if (chkEnableCustomThinkingOverride) {
            chkEnableCustomThinkingOverride.addEventListener('change', (e: any) => {
                try {
                    ipcRenderer.send('settings:set-custom-thinking-override-enabled', e.target.checked);
                } catch (err) {
                    console.error('[SettingsController] Failed to save custom thinking override enabled:', err);
                }
            });
        }

        const chkCustomThinkingSupports = document.getElementById('chkCustomThinkingSupports') as HTMLInputElement | null;
        if (chkCustomThinkingSupports) {
            chkCustomThinkingSupports.addEventListener('change', (e: any) => {
                try {
                    ipcRenderer.send('settings:set-custom-thinking-supports', e.target.checked);
                } catch (err) {
                    console.error('[SettingsController] Failed to save custom thinking supports:', err);
                }
            });
        }

        const txtCustomThinkingBudget = document.getElementById('txtCustomThinkingBudget') as HTMLInputElement | null;
        if (txtCustomThinkingBudget) {
            txtCustomThinkingBudget.addEventListener('change', (e: any) => {
                try {
                    const val = parseInt(e.target.value, 10);
                    ipcRenderer.send('settings:set-custom-thinking-budget', isNaN(val) ? -1 : val);
                } catch (err) {
                    console.error('[SettingsController] Failed to save custom thinking budget:', err);
                }
            });
        }

        const txtCustomThinkingMinBudget = document.getElementById('txtCustomThinkingMinBudget') as HTMLInputElement | null;
        if (txtCustomThinkingMinBudget) {
            txtCustomThinkingMinBudget.addEventListener('change', (e: any) => {
                try {
                    const val = parseInt(e.target.value, 10);
                    ipcRenderer.send('settings:set-custom-thinking-min-budget', isNaN(val) ? 32 : val);
                } catch (err) {
                    console.error('[SettingsController] Failed to save custom thinking min budget:', err);
                }
            });
        }

        const txtCustomMaxOutputTokens = document.getElementById('txtCustomMaxOutputTokens') as HTMLInputElement | null;
        if (txtCustomMaxOutputTokens) {
            txtCustomMaxOutputTokens.addEventListener('change', (e: any) => {
                try {
                    const val = parseInt(e.target.value, 10);
                    ipcRenderer.send('settings:set-custom-max-output-tokens', isNaN(val) ? 65536 : val);
                } catch (err) {
                    console.error('[SettingsController] Failed to save custom max output tokens:', err);
                }
            });
        }

        const chkReasoningAsText = document.getElementById('chkReasoningAsText') as HTMLInputElement | null;
        if (chkReasoningAsText) {
            chkReasoningAsText.addEventListener('change', (e: any) => {
                try {
                    ipcRenderer.send('settings:set-reasoning-as-text', e.target.checked);
                } catch (err) {
                    console.error('[SettingsController] Failed to save reasoning as text setting:', err);
                }
            });
        }

        const chkEnableThinkingMode = document.getElementById('chkEnableThinkingMode') as HTMLInputElement | null;
        if (chkEnableThinkingMode) {
            chkEnableThinkingMode.addEventListener('change', (e: any) => {
                try {
                    ipcRenderer.send('settings:set-enable-thinking-mode', e.target.checked);
                } catch (err) {
                    console.error('[SettingsController] Failed to save enable thinking mode setting:', err);
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

		const chkEnableCustomCompression = document.getElementById('chkEnableCustomCompression') as HTMLInputElement | null;
		const numMaxTokensThreshold = document.getElementById('numMaxTokensThreshold') as HTMLInputElement | null;
		const numKeepRecentTurns = document.getElementById('numKeepRecentTurns') as HTMLInputElement | null;
		const selSummaryModel = document.getElementById('selSummaryModel') as HTMLSelectElement | null;
		const divSessionCompressionOptions = document.getElementById('divSessionCompressionOptions');

		function saveSessionOptimization() {
			if (!chkEnableCustomCompression || !numMaxTokensThreshold || !numKeepRecentTurns || !selSummaryModel) return;
			const cfg = {
				enableCustomCompression: chkEnableCustomCompression.checked,
				maxTokensThreshold: parseInt(numMaxTokensThreshold.value, 10) || 100000,
				compressionStrategy: 'summarize',
				summaryModel: selSummaryModel.value || '',
				keepRecentTurns: parseInt(numKeepRecentTurns.value, 10) || 5
			};
			try {
				ipcRenderer.send('settings:set-session-optimization', cfg);
			} catch (err) {
				console.error('[SettingsController] Failed to save session optimization:', err);
			}
		}

		if (chkEnableCustomCompression) {
			chkEnableCustomCompression.addEventListener('change', (e: any) => {
				const enabled = e.target.checked;
				if (divSessionCompressionOptions) {
					divSessionCompressionOptions.style.display = enabled ? 'flex' : 'none';
				}
				saveSessionOptimization();
			});
		}
		if (numMaxTokensThreshold) {
			numMaxTokensThreshold.addEventListener('change', saveSessionOptimization);
		}
		if (numKeepRecentTurns) {
			numKeepRecentTurns.addEventListener('change', saveSessionOptimization);
		}
		if (selSummaryModel) {
			selSummaryModel.addEventListener('change', saveSessionOptimization);
		}

        // Initial load
        refreshSettingsUI();
        switchSettingsTab('general');

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
        const numMaxRetryDelay = document.getElementById('numMaxRetryDelay') as HTMLInputElement | null;

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

        if (numMaxRetryDelay) {
            const delay = ipcRenderer.sendSync('settings:get-max-retry-delay');
            if (delay !== null && delay !== undefined) {
                numMaxRetryDelay.value = String(delay);
            } else {
                numMaxRetryDelay.value = '10';
            }
        }

        const numMaxRequestBodyMB = document.getElementById('numMaxRequestBodyMB') as HTMLInputElement | null;
        if (numMaxRequestBodyMB) {
            const bodyMB = ipcRenderer.sendSync('settings:get-max-request-body-mb');
            if (bodyMB !== null && bodyMB !== undefined) {
                numMaxRequestBodyMB.value = String(bodyMB);
            } else {
                numMaxRequestBodyMB.value = '50';
            }
        }

        const numRequestTimeout = document.getElementById('numRequestTimeout') as HTMLInputElement | null;
        if (numRequestTimeout) {
            const timeout = ipcRenderer.sendSync('settings:get-request-timeout');
            if (timeout !== null && timeout !== undefined) {
                numRequestTimeout.value = String(timeout);
            } else {
                numRequestTimeout.value = '300';
            }
        }

        const chkEnablePacketCapture = document.getElementById('chkEnablePacketCapture') as HTMLInputElement | null;
        const packetCaptureEnabled = ipcRenderer.sendSync('settings:get-packet-capture-enabled');
        const isCaptureEnabled = packetCaptureEnabled !== null && packetCaptureEnabled !== undefined ? !!packetCaptureEnabled : true;
        if (chkEnablePacketCapture) {
            chkEnablePacketCapture.checked = isCaptureEnabled;
        }
        updatePacketCaptureVisibility(isCaptureEnabled);

        loadProxyState();

        const txtPromptPrefix = document.getElementById('txtPromptPrefix') as HTMLTextAreaElement | null;
        if (txtPromptPrefix) {
            const prefix = ipcRenderer.sendSync('settings:get-prompt-prefix');
            if (prefix !== null && prefix !== undefined) {
                txtPromptPrefix.value = String(prefix);
            }
        }

        const chkEnableCustomModelOverride = document.getElementById('chkEnableCustomModelOverride') as HTMLInputElement | null;
        if (chkEnableCustomModelOverride) {
            const enabled = ipcRenderer.sendSync('settings:get-custom-model-override-enabled');
            if (enabled !== null && enabled !== undefined) {
                chkEnableCustomModelOverride.checked = !!enabled;
            }
        }

        const txtCustomModelOverrideID = document.getElementById('txtCustomModelOverrideID') as HTMLInputElement | null;
        if (txtCustomModelOverrideID) {
            const overrideID = ipcRenderer.sendSync('settings:get-custom-model-override-id');
            if (overrideID !== null && overrideID !== undefined) {
                txtCustomModelOverrideID.value = String(overrideID);
            }
        }

        const txtBypassOverridePrefixes = document.getElementById('txtBypassOverridePrefixes') as HTMLInputElement | null;
        if (txtBypassOverridePrefixes) {
            const prefixes = ipcRenderer.sendSync('settings:get-bypass-override-prefixes');
            if (Array.isArray(prefixes)) {
                // []string → 逗号分隔展示,默认 ["tab"] 显示为 "tab"。
                txtBypassOverridePrefixes.value = prefixes.filter((s: any) => typeof s === 'string' && s !== '').join(', ');
            }
        }

        const chkEnableCustomThinkingOverride = document.getElementById('chkEnableCustomThinkingOverride') as HTMLInputElement | null;
        if (chkEnableCustomThinkingOverride) {
            const enabled = ipcRenderer.sendSync('settings:get-custom-thinking-override-enabled');
            if (enabled !== null && enabled !== undefined) {
                chkEnableCustomThinkingOverride.checked = !!enabled;
            }
        }

        const chkCustomThinkingSupports = document.getElementById('chkCustomThinkingSupports') as HTMLInputElement | null;
        if (chkCustomThinkingSupports) {
            const supports = ipcRenderer.sendSync('settings:get-custom-thinking-supports');
            if (supports !== null && supports !== undefined) {
                chkCustomThinkingSupports.checked = !!supports;
            }
        }

        const txtCustomThinkingBudget = document.getElementById('txtCustomThinkingBudget') as HTMLInputElement | null;
        if (txtCustomThinkingBudget) {
            const budget = ipcRenderer.sendSync('settings:get-custom-thinking-budget');
            if (budget !== null && budget !== undefined) {
                txtCustomThinkingBudget.value = String(budget);
            }
        }

        const txtCustomThinkingMinBudget = document.getElementById('txtCustomThinkingMinBudget') as HTMLInputElement | null;
        if (txtCustomThinkingMinBudget) {
            const minBudget = ipcRenderer.sendSync('settings:get-custom-thinking-min-budget');
            if (minBudget !== null && minBudget !== undefined) {
                txtCustomThinkingMinBudget.value = String(minBudget);
            }
        }

        const txtCustomMaxOutputTokens = document.getElementById('txtCustomMaxOutputTokens') as HTMLInputElement | null;
        if (txtCustomMaxOutputTokens) {
            const maxTokens = ipcRenderer.sendSync('settings:get-custom-max-output-tokens');
            if (maxTokens !== null && maxTokens !== undefined) {
                txtCustomMaxOutputTokens.value = String(maxTokens);
            }
        }

        const chkReasoningAsText = document.getElementById('chkReasoningAsText') as HTMLInputElement | null;
        if (chkReasoningAsText) {
            const enabled = ipcRenderer.sendSync('settings:get-reasoning-as-text');
            if (enabled !== null && enabled !== undefined) {
                chkReasoningAsText.checked = !!enabled;
            }
        }

        const chkEnableThinkingMode = document.getElementById('chkEnableThinkingMode') as HTMLInputElement | null;
        if (chkEnableThinkingMode) {
            const enabled = ipcRenderer.sendSync('settings:get-enable-thinking-mode');
            if (enabled === false || enabled === 'false' || enabled === 0) {
                chkEnableThinkingMode.checked = false;
            } else {
                chkEnableThinkingMode.checked = true; // 除非明确显式返回 false，一律默认开启
            }
        }

		const chkEnableCustomCompression = document.getElementById('chkEnableCustomCompression') as HTMLInputElement | null;
		const numMaxTokensThreshold = document.getElementById('numMaxTokensThreshold') as HTMLInputElement | null;
		const numKeepRecentTurns = document.getElementById('numKeepRecentTurns') as HTMLInputElement | null;
		const selSummaryModel = document.getElementById('selSummaryModel') as HTMLSelectElement | null;
		const divSessionCompressionOptions = document.getElementById('divSessionCompressionOptions');

		if (chkEnableCustomCompression && numMaxTokensThreshold && numKeepRecentTurns && selSummaryModel) {
			const cfg = ipcRenderer.sendSync('settings:get-session-optimization');
			if (cfg) {
				chkEnableCustomCompression.checked = !!cfg.enableCustomCompression;
				numMaxTokensThreshold.value = String(cfg.maxTokensThreshold || 100000);
				numKeepRecentTurns.value = String(cfg.keepRecentTurns || 5);
				if (divSessionCompressionOptions) {
					divSessionCompressionOptions.style.display = cfg.enableCustomCompression ? 'flex' : 'none';
				}

				// 单次 relay:get-model-mapping 填充摘要模型下拉(OCR 模型已解耦,改由 refreshOcrModelSelect 独立填充)。
				ipcRenderer.invoke('relay:get-model-mapping').then((mappings: any) => {
					const modelNames = (mappings || []).map((m: any) => m.clientModel).filter(Boolean);
					selSummaryModel.innerHTML = '';
					const allModels = Array.from(new Set<string>(modelNames));
					if (cfg.summaryModel && !allModels.includes(cfg.summaryModel)) {
						allModels.unshift(cfg.summaryModel);
					}
					if (allModels.length === 0) {
						const opt = document.createElement('option');
						opt.value = '';
						opt.textContent = state.currentLanguage === 'zh' ? '暂无可用模型' : 'No available models';
						selSummaryModel.appendChild(opt);
					} else {
						allModels.forEach(m => {
							const opt = document.createElement('option');
							opt.value = m;
							opt.textContent = m;
							if (m === cfg.summaryModel) {
								opt.selected = true;
							}
							selSummaryModel.appendChild(opt);
						});
					}
				}).catch(() => {
					selSummaryModel.innerHTML = '';
					if (cfg.summaryModel) {
						const opt = document.createElement('option');
						opt.value = cfg.summaryModel;
						opt.textContent = cfg.summaryModel;
						opt.selected = true;
						selSummaryModel.appendChild(opt);
					} else {
						const opt = document.createElement('option');
						opt.value = '';
						opt.textContent = state.currentLanguage === 'zh' ? '暂无可用模型' : 'No available models';
						selSummaryModel.appendChild(opt);
					}
				});
			}
		}
		// OCR 图片分析模型: 已升级为 ModelSearchSelect 响应式公共组件，统一刷新候选与已保存值
		void refreshOcrModel();
    } catch (err) {
        console.error('[SettingsController] Failed to refresh settings UI:', err);
    }
}

// Global hook
(window as any).refreshSettingsUI = refreshSettingsUI;


