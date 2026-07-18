import { ipcRenderer, shell } from '../shared/ipc';
import state from './dashboardState';
import i18n from '../shared/i18n';

let networkRefreshTimer: any = null;

export function deactivateSettings() {
    if (networkRefreshTimer) {
        clearInterval(networkRefreshTimer);
        networkRefreshTimer = null;
        console.log('[SettingsController] Outbound network logs auto refresh stopped.');
    }
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

        function startNetworkLogsAutoRefresh() {
            if (networkRefreshTimer) return;
            networkRefreshTimer = setInterval(() => {
                try {
                    ipcRenderer.send('settings:get-network-status');
                    ipcRenderer.send('settings:get-network-logs');
                } catch (e) {
                    console.error('[SettingsController] Failed to auto refresh network logs:', e);
                }
            }, 3000);
        }

        function stopNetworkLogsAutoRefresh() {
            if (networkRefreshTimer) {
                clearInterval(networkRefreshTimer);
                networkRefreshTimer = null;
            }
        }

        function switchSettingsTab(activePanel: string) {
            const settingsPanelGeneral = document.getElementById('settings-panel-general');
            const settingsPanelAbout = document.getElementById('settings-panel-about');
            const settingsPanelRelay = document.getElementById('settings-panel-relay');
            const settingsPanelNetwork = document.getElementById('settings-panel-network');

            const btnSettingsTabGeneral = document.getElementById('btnSettingsTabGeneral');
            const btnSettingsTabAbout = document.getElementById('btnSettingsTabAbout');
            const btnSettingsTabRelay = document.getElementById('btnSettingsTabRelay');
            const btnSettingsTabNetwork = document.getElementById('btnSettingsTabNetwork');

            if (settingsPanelGeneral) settingsPanelGeneral.style.setProperty('display', activePanel === 'general' ? 'flex' : 'none', 'important');
            if (settingsPanelAbout) settingsPanelAbout.style.setProperty('display', activePanel === 'about' ? 'flex' : 'none', 'important');
            if (settingsPanelRelay) settingsPanelRelay.style.setProperty('display', activePanel === 'relay' ? 'flex' : 'none', 'important');
            if (settingsPanelNetwork) settingsPanelNetwork.style.setProperty('display', activePanel === 'network' ? 'flex' : 'none', 'important');

            if (btnSettingsTabGeneral) btnSettingsTabGeneral.className = activePanel === 'general' ? activeTabClass : inactiveTabClass;
            if (btnSettingsTabAbout) btnSettingsTabAbout.className = activePanel === 'about' ? activeTabClass : inactiveTabClass;
            if (btnSettingsTabRelay) btnSettingsTabRelay.className = activePanel === 'relay' ? activeTabClass : inactiveTabClass;
            if (btnSettingsTabNetwork) btnSettingsTabNetwork.className = activePanel === 'network' ? activeTabClass : inactiveTabClass;

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
        }

        (window as any).switchSettingsTab = switchSettingsTab;

        const btnSettingsTabGeneral = document.getElementById('btnSettingsTabGeneral');
        const btnSettingsTabAbout = document.getElementById('btnSettingsTabAbout');
        const btnSettingsTabRelay = document.getElementById('btnSettingsTabRelay');
        const btnSettingsTabNetwork = document.getElementById('btnSettingsTabNetwork');

        if (btnSettingsTabGeneral) btnSettingsTabGeneral.addEventListener('click', () => switchSettingsTab('general'));
        if (btnSettingsTabAbout) btnSettingsTabAbout.addEventListener('click', () => switchSettingsTab('about'));
        if (btnSettingsTabRelay) btnSettingsTabRelay.addEventListener('click', () => switchSettingsTab('relay'));
        if (btnSettingsTabNetwork) btnSettingsTabNetwork.addEventListener('click', () => switchSettingsTab('network'));

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

        const chkCustomSocks5Enabled = document.getElementById('chkCustomSocks5Enabled') as HTMLInputElement | null;
        const txtCustomSocks5Address = document.getElementById('txtCustomSocks5Address') as HTMLInputElement | null;
        const txtCustomSocks5Username = document.getElementById('txtCustomSocks5Username') as HTMLInputElement | null;
        const txtCustomSocks5Password = document.getElementById('txtCustomSocks5Password') as HTMLInputElement | null;
        const divCustomSocks5Address = document.getElementById('divCustomSocks5Address');
        const txtFallbackProxyPorts = document.getElementById('txtFallbackProxyPorts') as HTMLInputElement | null;

        if (chkCustomSocks5Enabled) {
            chkCustomSocks5Enabled.addEventListener('change', (e: any) => {
                const enabled = e.target.checked;
                try {
                    ipcRenderer.send('settings:set-custom-socks5-enabled', enabled);
                    if (divCustomSocks5Address) {
                        divCustomSocks5Address.style.display = enabled ? 'flex' : 'none';
                    }
                } catch (err) {
                    console.error('[SettingsController] Failed to save custom socks5 enabled:', err);
                }
            });
        }

        if (txtCustomSocks5Address) {
            txtCustomSocks5Address.addEventListener('change', (e: any) => {
                const val = e.target.value.trim();
                try {
                    ipcRenderer.send('settings:set-custom-socks5-address', val);
                } catch (err) {
                    console.error('[SettingsController] Failed to save custom socks5 address:', err);
                }
            });
        }

        if (txtCustomSocks5Username) {
            txtCustomSocks5Username.addEventListener('change', (e: any) => {
                const val = e.target.value.trim();
                try {
                    ipcRenderer.send('settings:set-custom-socks5-username', val);
                } catch (err) {
                    console.error('[SettingsController] Failed to save custom socks5 username:', err);
                }
            });
        }

        if (txtCustomSocks5Password) {
            txtCustomSocks5Password.addEventListener('change', (e: any) => {
                const val = e.target.value.trim();
                try {
                    ipcRenderer.send('settings:set-custom-socks5-password', val);
                } catch (err) {
                    console.error('[SettingsController] Failed to save custom socks5 password:', err);
                }
            });
        }

        if (txtFallbackProxyPorts) {
            txtFallbackProxyPorts.addEventListener('change', (e: any) => {
                const val = e.target.value.trim();
                try {
                    ipcRenderer.send('settings:set-fallback-proxy-ports', val);
                } catch (err) {
                    console.error('[SettingsController] Failed to save fallback proxy ports:', err);
                }
            });
        }

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
				summaryModel: selSummaryModel.value || 'gemini-2.5-flash-lite',
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

        const chkCustomSocks5Enabled = document.getElementById('chkCustomSocks5Enabled') as HTMLInputElement | null;
        const txtCustomSocks5Address = document.getElementById('txtCustomSocks5Address') as HTMLInputElement | null;
        const txtCustomSocks5Username = document.getElementById('txtCustomSocks5Username') as HTMLInputElement | null;
        const txtCustomSocks5Password = document.getElementById('txtCustomSocks5Password') as HTMLInputElement | null;
        const divCustomSocks5Address = document.getElementById('divCustomSocks5Address');
        const txtFallbackProxyPorts = document.getElementById('txtFallbackProxyPorts') as HTMLInputElement | null;

        if (chkCustomSocks5Enabled) {
            const enabled = ipcRenderer.sendSync('settings:get-custom-socks5-enabled');
            if (enabled !== null && enabled !== undefined) {
                chkCustomSocks5Enabled.checked = !!enabled;
                if (divCustomSocks5Address) {
                    divCustomSocks5Address.style.display = enabled ? 'flex' : 'none';
                }
            }
        }

        if (txtCustomSocks5Address) {
            const addr = ipcRenderer.sendSync('settings:get-custom-socks5-address');
            if (addr !== null && addr !== undefined) {
                txtCustomSocks5Address.value = String(addr);
            }
        }

        if (txtCustomSocks5Username) {
            const username = ipcRenderer.sendSync('settings:get-custom-socks5-username');
            if (username !== null && username !== undefined) {
                txtCustomSocks5Username.value = String(username);
            }
        }

        if (txtCustomSocks5Password) {
            const password = ipcRenderer.sendSync('settings:get-custom-socks5-password');
            if (password !== null && password !== undefined) {
                txtCustomSocks5Password.value = String(password);
            }
        }

        if (txtFallbackProxyPorts) {
            const ports = ipcRenderer.sendSync('settings:get-fallback-proxy-ports');
            if (ports !== null && ports !== undefined) {
                txtFallbackProxyPorts.value = String(ports);
            }
        }

        const txtPromptPrefix = document.getElementById('txtPromptPrefix') as HTMLTextAreaElement | null;
        if (txtPromptPrefix) {
            const prefix = ipcRenderer.sendSync('settings:get-prompt-prefix');
            if (prefix !== null && prefix !== undefined) {
                txtPromptPrefix.value = String(prefix);
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

				ipcRenderer.invoke('relay:get-model-mapping').then((mappings: any) => {
					const modelNames = (mappings || []).map((m: any) => m.clientModel).filter(Boolean);
					selSummaryModel.innerHTML = '';
					const defaultModels = ['gemini-2.5-flash-lite', 'gemini-3.1-flash-lite', 'gemini-2.0-flash', 'gemini-1.5-flash'];
					const allModels = Array.from(new Set([...modelNames, ...defaultModels]));
					allModels.forEach(m => {
						const opt = document.createElement('option');
						opt.value = m;
						opt.textContent = m;
						if (m === cfg.summaryModel) {
							opt.selected = true;
						}
						selSummaryModel.appendChild(opt);
					});
				}).catch(() => {
					const defaultModels = ['gemini-2.5-flash-lite', 'gemini-3.1-flash-lite', 'gemini-2.0-flash', 'gemini-1.5-flash'];
					selSummaryModel.innerHTML = '';
					defaultModels.forEach(m => {
						const opt = document.createElement('option');
						opt.value = m;
						opt.textContent = m;
						if (m === cfg.summaryModel) {
							opt.selected = true;
						}
						selSummaryModel.appendChild(opt);
					});
				});
			}
		}
    } catch (err) {
        console.error('[SettingsController] Failed to refresh settings UI:', err);
    }
}

// Global hook
(window as any).refreshSettingsUI = refreshSettingsUI;

// Register network status and outband connection logs listeners
ipcRenderer.on('settings:network-status-res', (event, data: any) => {
    const lblNetStatusFallback = document.getElementById('lblNetStatusFallback');
    const lblNetStatusCustomSocks = document.getElementById('lblNetStatusCustomSocks');

    if (lblNetStatusFallback) {
        lblNetStatusFallback.textContent = data.cachedLocalProxy ? data.cachedLocalProxy : (state.currentLanguage === 'zh' ? '直连 (无探测代理)' : 'DIRECT (No scan proxy)');
        if (data.cachedLocalProxy) {
            lblNetStatusFallback.className = "text-[13px] font-mono font-bold text-primary dark:text-primary-fixed-dim";
        } else {
            lblNetStatusFallback.className = "text-[13px] font-mono font-bold text-outline";
        }
    }

    if (lblNetStatusCustomSocks) {
        if (data.customSocks5Enabled) {
            lblNetStatusCustomSocks.textContent = (state.currentLanguage === 'zh' ? '启用' : 'Enabled') + ` (${data.customSocks5Address})`;
            lblNetStatusCustomSocks.className = "text-[13px] font-mono font-bold text-green-600 dark:text-green-400";
        } else {
            lblNetStatusCustomSocks.textContent = state.currentLanguage === 'zh' ? '未启用' : 'Disabled';
            lblNetStatusCustomSocks.className = "text-[13px] font-mono font-bold text-outline";
        }
    }
});

ipcRenderer.on('settings:network-logs-res', (event, logs: any[]) => {
    const tblNetworkLogsBody = document.getElementById('tblNetworkLogsBody');
    if (!tblNetworkLogsBody) return;

    if (!logs || logs.length === 0) {
        const emptyMsg = state.currentLanguage === 'zh' ? '暂无连接记录，正在等待出站网络活动...' : 'No connection logs. Waiting for outbound network activity...';
        tblNetworkLogsBody.innerHTML = `
            <tr>
                <td colspan="5" class="py-6 text-center text-outline/60">${emptyMsg}</td>
            </tr>
        `;
        return;
    }

    // Newest log on top
    const sortedLogs = [...logs].reverse();

    let html = '';
    sortedLogs.forEach((log: any) => {
        const isSuccess = log.status === 'SUCCESS';
        const statusClass = isSuccess 
            ? 'text-green-600 dark:text-green-400 font-bold' 
            : 'text-red-500 font-bold truncate max-w-[240px] inline-block';
        const proxyClass = log.proxyUsed === 'DIRECT' 
            ? 'text-outline font-bold' 
            : 'text-primary dark:text-primary-fixed-dim font-bold';

        html += `
            <tr class="border-b border-outline-variant/10 hover:bg-slate-50 dark:hover:bg-white/5 transition-colors">
                <td class="py-2 px-3 text-slate-400 font-medium select-none">${log.timestamp}</td>
                <td class="py-2 px-3 text-on-surface dark:text-slate-200 font-bold font-mono">${log.target}</td>
                <td class="py-2 px-3 ${proxyClass} font-mono">${log.proxyUsed}</td>
                <td class="py-2 px-3 text-center text-on-surface dark:text-slate-300 font-bold">${log.duration}</td>
                <td class="py-2 px-3 ${statusClass}" title="${log.status}">${log.status}</td>
            </tr>
        `;
    });

    tblNetworkLogsBody.innerHTML = html;
});

