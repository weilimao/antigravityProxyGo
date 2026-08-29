/**
 * settingsProxy.ts: 自定义 SOCKS5 + NVIDIA 断流兜底出站代理的表单绑定(SET)与回填(GET)。
 *
 * 从 settingsController.ts initSettings(L322-439)/ refreshSettingsUI(L705-786) 闭包体抽离,
 * 两簇字面镜像同一套 DOM id 与 settings:set-/get- 系列 IPC 通道,唯一外部依赖 ipcRenderer。
 * 各整体减 4 空格基缩进(函数体基缩进 8→4),无 let 可变状态,幂等重绑/重读,无跨 mount 残留。
 */
import { ipcRenderer } from '../shared/ipc';

// 自定义 SOCKS5 + NVIDIA 兜底代理:逐字段绑 change → ipcRenderer.send('settings:set-*')。
export function bindProxySettings(): void {
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

    // ===== NVIDIA 断流兜底出站代理:与上方专属 SOCKS5 配置独立,镜像其 wiring =====
    const chkFallbackProxyEnabled = document.getElementById('chkFallbackProxyEnabled') as HTMLInputElement | null;
    const txtFallbackProxyAddress = document.getElementById('txtFallbackProxyAddress') as HTMLInputElement | null;
    const txtFallbackProxyUsername = document.getElementById('txtFallbackProxyUsername') as HTMLInputElement | null;
    const txtFallbackProxyPassword = document.getElementById('txtFallbackProxyPassword') as HTMLInputElement | null;
    const divFallbackProxyAddress = document.getElementById('divFallbackProxyAddress');

    if (chkFallbackProxyEnabled) {
        chkFallbackProxyEnabled.addEventListener('change', (e: any) => {
            const enabled = e.target.checked;
            try {
                ipcRenderer.send('settings:set-fallback-proxy-enabled', enabled);
                if (divFallbackProxyAddress) {
                    divFallbackProxyAddress.style.display = enabled ? 'flex' : 'none';
                }
            } catch (err) {
                console.error('[SettingsController] Failed to save fallback proxy enabled:', err);
            }
        });
    }

    if (txtFallbackProxyAddress) {
        txtFallbackProxyAddress.addEventListener('change', (e: any) => {
            const val = e.target.value.trim();
            try {
                ipcRenderer.send('settings:set-fallback-proxy-address', val);
            } catch (err) {
                console.error('[SettingsController] Failed to save fallback proxy address:', err);
            }
        });
    }

    if (txtFallbackProxyUsername) {
        txtFallbackProxyUsername.addEventListener('change', (e: any) => {
            const val = e.target.value.trim();
            try {
                ipcRenderer.send('settings:set-fallback-proxy-username', val);
            } catch (err) {
                console.error('[SettingsController] Failed to save fallback proxy username:', err);
            }
        });
    }

    if (txtFallbackProxyPassword) {
        txtFallbackProxyPassword.addEventListener('change', (e: any) => {
            const val = e.target.value.trim();
            try {
                ipcRenderer.send('settings:set-fallback-proxy-password', val);
            } catch (err) {
                console.error('[SettingsController] Failed to save fallback proxy password:', err);
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

    // ===== NVIDIA Cloudflare 代理出口 Worker =====
    const chkNvidiaWorkerProxyEnabled = document.getElementById('chkNvidiaWorkerProxyEnabled') as HTMLInputElement | null;
    const txtNvidiaWorkerProxyUrl = document.getElementById('txtNvidiaWorkerProxyUrl') as HTMLInputElement | null;
    const divNvidiaWorkerProxyUrl = document.getElementById('divNvidiaWorkerProxyUrl');

    if (chkNvidiaWorkerProxyEnabled) {
        chkNvidiaWorkerProxyEnabled.addEventListener('change', (e: any) => {
            const enabled = e.target.checked;
            try {
                ipcRenderer.send('settings:set-nvidia-worker-proxy-enabled', enabled);
                if (divNvidiaWorkerProxyUrl) {
                    divNvidiaWorkerProxyUrl.style.display = enabled ? 'flex' : 'none';
                }
                // 智能互斥：开启 Worker 代理出口时，自动关闭并收起专属 SOCKS5 代理
                if (enabled && chkNvidiaDedicatedProxyEnabled && chkNvidiaDedicatedProxyEnabled.checked) {
                    chkNvidiaDedicatedProxyEnabled.checked = false;
                    if (divNvidiaDedicatedProxyAddress) {
                        divNvidiaDedicatedProxyAddress.style.display = 'none';
                    }
                    ipcRenderer.send('settings:set-nvidia-dedicated-proxy-enabled', false);
                }
            } catch (err) {
                console.error('[SettingsController] Failed to save nvidia worker proxy enabled:', err);
            }
        });
    }

    if (txtNvidiaWorkerProxyUrl) {
        txtNvidiaWorkerProxyUrl.addEventListener('change', (e: any) => {
            const val = e.target.value.trim();
            try {
                ipcRenderer.send('settings:set-nvidia-worker-proxy-url', val);
            } catch (err) {
                console.error('[SettingsController] Failed to save nvidia worker proxy url:', err);
            }
        });
    }

    // ===== NVIDIA 专属 SOCKS5/HTTP 出口代理 =====
    const chkNvidiaDedicatedProxyEnabled = document.getElementById('chkNvidiaDedicatedProxyEnabled') as HTMLInputElement | null;
    const txtNvidiaDedicatedProxyAddress = document.getElementById('txtNvidiaDedicatedProxyAddress') as HTMLInputElement | null;
    const txtNvidiaDedicatedProxyUsername = document.getElementById('txtNvidiaDedicatedProxyUsername') as HTMLInputElement | null;
    const txtNvidiaDedicatedProxyPassword = document.getElementById('txtNvidiaDedicatedProxyPassword') as HTMLInputElement | null;
    const divNvidiaDedicatedProxyAddress = document.getElementById('divNvidiaDedicatedProxyAddress');

    if (chkNvidiaDedicatedProxyEnabled) {
        chkNvidiaDedicatedProxyEnabled.addEventListener('change', (e: any) => {
            const enabled = e.target.checked;
            try {
                ipcRenderer.send('settings:set-nvidia-dedicated-proxy-enabled', enabled);
                if (divNvidiaDedicatedProxyAddress) {
                    divNvidiaDedicatedProxyAddress.style.display = enabled ? 'flex' : 'none';
                }
                // 智能互斥：开启专属 SOCKS5 代理时，自动关闭并收起 Worker 代理出口
                if (enabled && chkNvidiaWorkerProxyEnabled && chkNvidiaWorkerProxyEnabled.checked) {
                    chkNvidiaWorkerProxyEnabled.checked = false;
                    if (divNvidiaWorkerProxyUrl) {
                        divNvidiaWorkerProxyUrl.style.display = 'none';
                    }
                    ipcRenderer.send('settings:set-nvidia-worker-proxy-enabled', false);
                }
            } catch (err) {
                console.error('[SettingsController] Failed to save nvidia dedicated proxy enabled:', err);
            }
        });
    }

    if (txtNvidiaDedicatedProxyAddress) {
        txtNvidiaDedicatedProxyAddress.addEventListener('change', (e: any) => {
            const val = e.target.value.trim();
            try {
                ipcRenderer.send('settings:set-nvidia-dedicated-proxy-address', val);
            } catch (err) {
                console.error('[SettingsController] Failed to save nvidia dedicated proxy address:', err);
            }
        });
    }

    if (txtNvidiaDedicatedProxyUsername) {
        txtNvidiaDedicatedProxyUsername.addEventListener('change', (e: any) => {
            const val = e.target.value.trim();
            try {
                ipcRenderer.send('settings:set-nvidia-dedicated-proxy-username', val);
            } catch (err) {
                console.error('[SettingsController] Failed to save nvidia dedicated proxy username:', err);
            }
        });
    }

    if (txtNvidiaDedicatedProxyPassword) {
        txtNvidiaDedicatedProxyPassword.addEventListener('change', (e: any) => {
            const val = e.target.value.trim();
            try {
                ipcRenderer.send('settings:set-nvidia-dedicated-proxy-password', val);
            } catch (err) {
                console.error('[SettingsController] Failed to save nvidia dedicated proxy password:', err);
            }
        });
    }

    // ===== NVIDIA 对冲请求 (可选,默认关):开关 + 触发延迟(毫秒) + 并发数 =====
    const chkNvidiaHedgeEnabled = document.getElementById('chkNvidiaHedgeEnabled') as HTMLInputElement | null;
    const txtNvidiaHedgeDelayMs = document.getElementById('txtNvidiaHedgeDelayMs') as HTMLInputElement | null;
    const txtNvidiaHedgeMaxParallel = document.getElementById('txtNvidiaHedgeMaxParallel') as HTMLInputElement | null;
    const divNvidiaHedgeDelay = document.getElementById('divNvidiaHedgeDelay');

    if (chkNvidiaHedgeEnabled) {
        chkNvidiaHedgeEnabled.addEventListener('change', (e: any) => {
            const enabled = e.target.checked;
            try {
                ipcRenderer.send('settings:set-nvidia-hedge-enabled', enabled);
                if (divNvidiaHedgeDelay) {
                    divNvidiaHedgeDelay.style.display = enabled ? 'flex' : 'none';
                }
            } catch (err) {
                console.error('[SettingsController] Failed to save nvidia hedge enabled:', err);
            }
        });
    }

    if (txtNvidiaHedgeDelayMs) {
        txtNvidiaHedgeDelayMs.addEventListener('change', (e: any) => {
            const parsed = parseInt(String(e.target.value).trim(), 10);
            // 回显必须与后端归一化逐位一致(settings.normalizeNvidiaHedgeDelayMs):
            // NaN/<=0 → 10000 默认;其余钳位 [2000,60000]。落盘值即生效值,不做二次换算。
            const normalized = isNaN(parsed) || parsed <= 0 ? 10000 : Math.min(60000, Math.max(2000, parsed));
            e.target.value = String(normalized);
            try {
                ipcRenderer.send('settings:set-nvidia-hedge-delay-ms', normalized);
            } catch (err) {
                console.error('[SettingsController] Failed to save nvidia hedge delay:', err);
            }
        });
    }

    if (txtNvidiaHedgeMaxParallel) {
        txtNvidiaHedgeMaxParallel.addEventListener('change', (e: any) => {
            const parsed = parseInt(String(e.target.value).trim(), 10);
            // 回显与后端归一化逐位一致:NaN/<=0 → 2 默认;上限动态跟随号池
            // (max 属性由 loadProxyState 以启用账号数写入,兜底 2)。最坏上游计费 = 并发数 倍。
            const dynMax = Math.max(2, parseInt(txtNvidiaHedgeMaxParallel.max, 10) || 2);
            const normalized = isNaN(parsed) || parsed <= 0 ? 2 : Math.min(dynMax, Math.max(2, parsed));
            e.target.value = String(normalized);
            try {
                ipcRenderer.send('settings:set-nvidia-hedge-max-parallel', normalized);
            } catch (err) {
                console.error('[SettingsController] Failed to save nvidia hedge max parallel:', err);
            }
        });
    }

    // 即刻竞赛开关:主与全部对冲 t=0 同刻出发(不再等触发延迟)。
    const chkNvidiaHedgeImmediate = document.getElementById('chkNvidiaHedgeImmediate') as HTMLInputElement | null;
    if (chkNvidiaHedgeImmediate) {
        chkNvidiaHedgeImmediate.addEventListener('change', (e: any) => {
            try {
                ipcRenderer.send('settings:set-nvidia-hedge-immediate', !!e.target.checked);
            } catch (err) {
                console.error('[SettingsController] Failed to save nvidia hedge immediate:', err);
            }
        });
    }
}

// 自定义 SOCKS5 + NVIDIA 兜底代理:逐字段 ipcRenderer.sendSync('settings:get-*') 回填。
export function loadProxyState(): void {
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

    // ===== NVIDIA 断流兜底出站代理:加载已保存配置,镜像上方专属 SOCKS5 取值范式 =====
    const chkFallbackProxyEnabled = document.getElementById('chkFallbackProxyEnabled') as HTMLInputElement | null;
    const txtFallbackProxyAddress = document.getElementById('txtFallbackProxyAddress') as HTMLInputElement | null;
    const txtFallbackProxyUsername = document.getElementById('txtFallbackProxyUsername') as HTMLInputElement | null;
    const txtFallbackProxyPassword = document.getElementById('txtFallbackProxyPassword') as HTMLInputElement | null;
    const divFallbackProxyAddress = document.getElementById('divFallbackProxyAddress');

    if (chkFallbackProxyEnabled) {
        const enabled = ipcRenderer.sendSync('settings:get-fallback-proxy-enabled');
        if (enabled !== null && enabled !== undefined) {
            chkFallbackProxyEnabled.checked = !!enabled;
            if (divFallbackProxyAddress) {
                divFallbackProxyAddress.style.display = enabled ? 'flex' : 'none';
            }
        }
    }

    if (txtFallbackProxyAddress) {
        const addr = ipcRenderer.sendSync('settings:get-fallback-proxy-address');
        if (addr !== null && addr !== undefined) {
            txtFallbackProxyAddress.value = String(addr);
        }
    }

    if (txtFallbackProxyUsername) {
        const username = ipcRenderer.sendSync('settings:get-fallback-proxy-username');
        if (username !== null && username !== undefined) {
            txtFallbackProxyUsername.value = String(username);
        }
    }

    if (txtFallbackProxyPassword) {
        const password = ipcRenderer.sendSync('settings:get-fallback-proxy-password');
        if (password !== null && password !== undefined) {
            txtFallbackProxyPassword.value = String(password);
        }
    }

    if (txtFallbackProxyPorts) {
        const ports = ipcRenderer.sendSync('settings:get-fallback-proxy-ports');
        if (ports !== null && ports !== undefined) {
            txtFallbackProxyPorts.value = String(ports);
        }
    }

    // ===== NVIDIA Cloudflare 代理出口 Worker 回填 =====
    const chkNvidiaWorkerProxyEnabled = document.getElementById('chkNvidiaWorkerProxyEnabled') as HTMLInputElement | null;
    const txtNvidiaWorkerProxyUrl = document.getElementById('txtNvidiaWorkerProxyUrl') as HTMLInputElement | null;
    const divNvidiaWorkerProxyUrl = document.getElementById('divNvidiaWorkerProxyUrl');

    const workerProxyState = ipcRenderer.sendSync('settings:get-nvidia-worker-proxy');
    if (workerProxyState) {
        if (chkNvidiaWorkerProxyEnabled) {
            chkNvidiaWorkerProxyEnabled.checked = !!workerProxyState.nvidiaWorkerProxyEnabled;
            if (divNvidiaWorkerProxyUrl) {
                divNvidiaWorkerProxyUrl.style.display = workerProxyState.nvidiaWorkerProxyEnabled ? 'flex' : 'none';
            }
        }
        if (txtNvidiaWorkerProxyUrl && workerProxyState.nvidiaWorkerProxyUrl !== undefined) {
            txtNvidiaWorkerProxyUrl.value = String(workerProxyState.nvidiaWorkerProxyUrl);
        }
    }

    // ===== NVIDIA 专属 SOCKS5 出口代理回填 =====
    const chkNvidiaDedicatedProxyEnabled = document.getElementById('chkNvidiaDedicatedProxyEnabled') as HTMLInputElement | null;
    const txtNvidiaDedicatedProxyAddress = document.getElementById('txtNvidiaDedicatedProxyAddress') as HTMLInputElement | null;
    const txtNvidiaDedicatedProxyUsername = document.getElementById('txtNvidiaDedicatedProxyUsername') as HTMLInputElement | null;
    const txtNvidiaDedicatedProxyPassword = document.getElementById('txtNvidiaDedicatedProxyPassword') as HTMLInputElement | null;
    const divNvidiaDedicatedProxyAddress = document.getElementById('divNvidiaDedicatedProxyAddress');

    const dedicatedProxyState = ipcRenderer.sendSync('settings:get-nvidia-dedicated-proxy');
    if (dedicatedProxyState) {
        if (chkNvidiaDedicatedProxyEnabled) {
            chkNvidiaDedicatedProxyEnabled.checked = !!dedicatedProxyState.enabled;
            if (divNvidiaDedicatedProxyAddress) {
                divNvidiaDedicatedProxyAddress.style.display = dedicatedProxyState.enabled ? 'flex' : 'none';
            }
        }
        if (txtNvidiaDedicatedProxyAddress && dedicatedProxyState.address !== undefined) {
            txtNvidiaDedicatedProxyAddress.value = String(dedicatedProxyState.address);
        }
        if (txtNvidiaDedicatedProxyUsername && dedicatedProxyState.username !== undefined) {
            txtNvidiaDedicatedProxyUsername.value = String(dedicatedProxyState.username);
        }
        if (txtNvidiaDedicatedProxyPassword && dedicatedProxyState.password !== undefined) {
            txtNvidiaDedicatedProxyPassword.value = String(dedicatedProxyState.password);
        }
    }

    // ===== NVIDIA 对冲请求回填(wailsConfigCache 通道,见 app_lifecycle.go settings:get-nvidia-hedge) =====
    const chkNvidiaHedgeEnabled = document.getElementById('chkNvidiaHedgeEnabled') as HTMLInputElement | null;
    const txtNvidiaHedgeDelayMs = document.getElementById('txtNvidiaHedgeDelayMs') as HTMLInputElement | null;
    const txtNvidiaHedgeMaxParallel = document.getElementById('txtNvidiaHedgeMaxParallel') as HTMLInputElement | null;
    const divNvidiaHedgeDelay = document.getElementById('divNvidiaHedgeDelay');

    const hedgeState = ipcRenderer.sendSync('settings:get-nvidia-hedge');
    if (hedgeState) {
        if (chkNvidiaHedgeEnabled) {
            chkNvidiaHedgeEnabled.checked = !!hedgeState.enabled;
            if (divNvidiaHedgeDelay) {
                divNvidiaHedgeDelay.style.display = hedgeState.enabled ? 'flex' : 'none';
            }
        }
        if (txtNvidiaHedgeDelayMs && hedgeState.delayMs !== undefined && hedgeState.delayMs !== null) {
            txtNvidiaHedgeDelayMs.value = String(hedgeState.delayMs);
        }
        if (txtNvidiaHedgeMaxParallel && hedgeState.maxParallel !== undefined && hedgeState.maxParallel !== null) {
            txtNvidiaHedgeMaxParallel.value = String(hedgeState.maxParallel);
        }
        const chkNvidiaHedgeImmediate = document.getElementById('chkNvidiaHedgeImmediate') as HTMLInputElement | null;
        if (chkNvidiaHedgeImmediate) {
            chkNvidiaHedgeImmediate.checked = !!hedgeState.immediate;
        }
        // 动态上限:上限跟随当前启用中的 NVIDIA 账号数(poolSize)——
        // 输入框 max 与 badge 同步;号池未知/为空时按 2 兜底。
        const poolSize = Number(hedgeState.poolSize);
        const dynMax = Math.max(2, isNaN(poolSize) ? 2 : poolSize);
        if (txtNvidiaHedgeMaxParallel) {
            txtNvidiaHedgeMaxParallel.max = String(dynMax);
        }
        const lblNvidiaHedgeMaxPool = document.getElementById('lblNvidiaHedgeMaxPool');
        if (lblNvidiaHedgeMaxPool) {
            lblNvidiaHedgeMaxPool.textContent = String(dynMax);
        }
    }
}
