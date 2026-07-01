import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';

let statsSyncTimer: ReturnType<typeof setInterval> | null = null;

export function initRemoteEvents() {
    const btnRemoteConnect = document.getElementById('btnRemoteConnect');
    const btnRemoteDisconnect = document.getElementById('btnRemoteDisconnect');
    const btnRemoteTest = document.getElementById('btnRemoteTest');
    const btnRemoteLogin = document.getElementById('btnRemoteLogin');
    const btnRemoteCancel = document.getElementById('btnRemoteCancel');

    const btnRemoteEnable = document.getElementById('btnRemoteEnable');
    const btnRemoteDisable = document.getElementById('btnRemoteDisable');

    if (btnRemoteConnect) {
        btnRemoteConnect.addEventListener('click', openRemoteModal);
    }
    if (btnRemoteDisconnect) {
        btnRemoteDisconnect.addEventListener('click', handleDisconnect);
    }
    if (btnRemoteEnable) {
        btnRemoteEnable.addEventListener('click', handleEnableRemote);
    }
    if (btnRemoteDisable) {
        btnRemoteDisable.addEventListener('click', handleDisableRemote);
    }
    if (btnRemoteTest) {
        btnRemoteTest.addEventListener('click', handleTestConnection);
    }
    if (btnRemoteLogin) {
        btnRemoteLogin.addEventListener('click', handleLogin);
    }
    if (btnRemoteCancel) {
        btnRemoteCancel.addEventListener('click', closeRemoteModal);
    }
    const btnManageApiKeys = document.getElementById('btnManageApiKeys');
    if (btnManageApiKeys) {
        btnManageApiKeys.addEventListener('click', openRemoteKeysModal);
    }
    const btnRemoteKeysClose = document.getElementById('btnRemoteKeysClose');
    if (btnRemoteKeysClose) {
        btnRemoteKeysClose.addEventListener('click', () => {
            const m = document.getElementById('remoteKeysModal');
            if (m) m.classList.add('hidden');
        });
    }
    const btnRemoteCreateKey = document.getElementById('btnRemoteCreateKey');
    if (btnRemoteCreateKey) {
        btnRemoteCreateKey.addEventListener('click', handleCreateRemoteKey);
    }

    // Listen for remote state changes
    ipcRenderer.on('remote-state', (_e: any, config: any) => {
        updateRemoteStatusUI(config);
    });

    // Check initial remote status
    checkRemoteStatus();
}

async function checkRemoteStatus() {
    try {
        const status = await ipcRenderer.invoke('remote:get-status');
        if (status) updateRemoteStatusUI(status);
    } catch (err) {
        // Ignore - not connected
    }
}

function openRemoteModal() {
    const modal = document.getElementById('remoteModal');
    if (modal) modal.classList.remove('hidden');
    
    // Pre-fill saved config
    const hostInput = document.getElementById('remoteHostInput') as HTMLInputElement;
    const portInput = document.getElementById('remotePortInput') as HTMLInputElement;
    
    if (hostInput && state.remoteHost) hostInput.value = state.remoteHost;
    if (portInput && state.remotePort) portInput.value = state.remotePort;
    
    // Clear result
    const result = document.getElementById('remoteLoginResult');
    if (result) result.classList.add('hidden');
}

function closeRemoteModal() {
    const modal = document.getElementById('remoteModal');
    if (modal) modal.classList.add('hidden');
}

function showLoginResult(message: string, isError: boolean) {
    const result = document.getElementById('remoteLoginResult');
    if (!result) return;
    result.classList.remove('hidden');
    result.textContent = message;
    result.className = `mt-3 text-[12px] px-3 py-2 rounded-lg ${isError 
        ? 'text-red-600 bg-red-50 dark:bg-red-950/30 dark:text-red-400' 
        : 'text-emerald-600 bg-emerald-50 dark:bg-emerald-950/30 dark:text-emerald-400'}`;
}

async function handleTestConnection() {
    const hostInput = document.getElementById('remoteHostInput') as HTMLInputElement;
    const portInput = document.getElementById('remotePortInput') as HTMLInputElement;
    const host = hostInput?.value?.trim();
    const port = portInput?.value?.trim() || '18444';
    
    if (!host) {
        showLoginResult('❌ 请输入 IP 地址或域名', true);
        return;
    }
    
    showLoginResult('⏳ 测试连接中...', false);
    try {
        const res = await ipcRenderer.invoke('remote:test', host, port);
        if (res?.success) {
            showLoginResult(`✅ 连接成功 (${res.latencyMs || 0}ms)`, false);
        } else {
            showLoginResult(`❌ 连接失败: ${res?.error || '未知错误'}`, true);
        }
    } catch (err) {
        showLoginResult('❌ 连接失败: 网络错误', true);
    }
}

async function handleLogin() {
    const hostInput = document.getElementById('remoteHostInput') as HTMLInputElement;
    const portInput = document.getElementById('remotePortInput') as HTMLInputElement;
    const keyInput = document.getElementById('remoteKeyInput') as HTMLInputElement;
    const pwdInput = document.getElementById('remotePasswordInput') as HTMLInputElement;
    
    const host = hostInput?.value?.trim();
    const port = portInput?.value?.trim() || '18444';
    const key = keyInput?.value?.trim();
    const password = pwdInput?.value;
    
    if (!host) {
        showLoginResult('❌ 请输入 IP 地址或域名', true);
        return;
    }
    if (!key || !password) {
        showLoginResult('❌ 请输入 Key 和密码', true);
        return;
    }
    
    showLoginResult('⏳ 登录中...', false);
    try {
        const res = await ipcRenderer.invoke('remote:login', host, port, key, password);
        if (res?.success) {
            showLoginResult('✅ 登录成功，正在切换到远程模式...', false);
            setTimeout(() => closeRemoteModal(), 800);
        } else {
            showLoginResult(`❌ 登录失败: ${res?.error || '未知错误'}`, true);
        }
    } catch (err) {
        showLoginResult('❌ 登录失败: 网络错误', true);
    }
}

async function handleDisconnect() {
    try {
        await ipcRenderer.invoke('remote:disconnect');
        await checkRemoteStatus();
    } catch (err) {
        console.error('[RemoteController] Failed to disconnect:', err);
    }
}

async function handleEnableRemote() {
    const statusText = document.getElementById('remoteStatusText');
    if (statusText) statusText.textContent = '⏳ 正在启用远程...';
    try {
        const res = await ipcRenderer.invoke('remote:enable');
        if (res?.success) {
            await checkRemoteStatus();
        } else {
            alert(`❌ 启用远程模式失败: ${res?.error || '未知错误'}`);
            await checkRemoteStatus();
        }
    } catch (err) {
        alert('❌ 启用远程模式失败: 网络错误');
        await checkRemoteStatus();
    }
}

async function handleDisableRemote() {
    try {
        const res = await ipcRenderer.invoke('remote:disable');
        if (res?.success) {
            await checkRemoteStatus();
        } else {
            console.error('[RemoteController] Failed to disable remote:', res?.error);
        }
    } catch (err) {
        console.error('[RemoteController] Failed to disable remote:', err);
    }
}

function updateRemoteStatusUI(status: any) {
    const badge = document.getElementById('remoteStatusBadge');
    const statusText = document.getElementById('remoteStatusText');
    const btnConnect = document.getElementById('btnRemoteConnect');
    
    const btnRemoteDisconnect = document.getElementById('btnRemoteDisconnect');
    const btnRemoteEnable = document.getElementById('btnRemoteEnable');
    const btnRemoteDisable = document.getElementById('btnRemoteDisable');
    
    const proxyToggle = document.getElementById('proxyToggle') as HTMLInputElement;
    
    const isConnected = status?.connected === true;
    const hasSaved = status?.hasSavedCredentials === true || !!status?.host;
    const isEnabled = status?.remoteEnabled === true;
    
    state.isRemoteMode = isConnected && isEnabled;
    state.remoteHost = status?.host || status?.savedHost || '';
    state.remotePort = status?.port || status?.savedPort || '';
    state.remoteUserKey = status?.userKey || status?.savedKey || '';
    state.remoteToken = status?.token || '';
    
    if (isConnected && isEnabled) {
        // Active remote mode
        if (badge) {
            badge.classList.remove('hidden');
            badge.className = "flex items-center gap-1.5 text-[12px] font-medium text-emerald-600 bg-emerald-50 dark:bg-emerald-950/30 dark:text-emerald-400 px-2.5 py-0.5 rounded-full border border-emerald-100 dark:border-emerald-900/30 whitespace-nowrap flex-shrink-0";
            badge.setAttribute('title', `远程主机: ${state.remoteHost}:${state.remotePort}\n用户Key: ${state.remoteUserKey}`);
        }
        if (statusText) {
            statusText.textContent = `远端: ${state.remoteHost}:${state.remotePort}`;
        }
        if (btnConnect) btnConnect.classList.add('hidden');
        
        const btnCopy = document.getElementById('btnManageApiKeys');
        if (btnCopy) btnCopy.classList.remove('hidden');
        
        if (btnRemoteDisable) btnRemoteDisable.classList.remove('hidden');
        if (btnRemoteEnable) btnRemoteEnable.classList.add('hidden');
        if (btnRemoteDisconnect) btnRemoteDisconnect.classList.remove('hidden');
    } else if (hasSaved && !isEnabled) {
        // Disabled remote mode (local mode active)
        if (badge) {
            badge.classList.remove('hidden');
            badge.className = "flex items-center gap-1.5 text-[12px] font-medium text-amber-600 bg-amber-50 dark:bg-amber-950/30 dark:text-amber-400 px-2.5 py-0.5 rounded-full border border-amber-100 dark:border-amber-900/30 whitespace-nowrap flex-shrink-0";
            badge.setAttribute('title', `已保存配置:\n主机: ${state.remoteHost}:${state.remotePort}\n用户Key: ${state.remoteUserKey}`);
        }
        if (statusText) {
            statusText.textContent = `远程已停用`;
        }
        if (btnConnect) btnConnect.classList.add('hidden');
        
        if (btnRemoteDisable) btnRemoteDisable.classList.add('hidden');
        if (btnRemoteEnable) btnRemoteEnable.classList.remove('hidden');
        if (btnRemoteDisconnect) btnRemoteDisconnect.classList.remove('hidden');
        
        const btnCopy = document.getElementById('btnManageApiKeys');
        if (btnCopy) btnCopy.classList.add('hidden');
    } else {
        // Not logged in / disconnected completely
        if (badge) badge.classList.add('hidden');
        if (btnConnect) btnConnect.classList.remove('hidden');
        
        const btnCopy = document.getElementById('btnManageApiKeys');
        if (btnCopy) btnCopy.classList.add('hidden');
    }
    
    // Disable intercept toggle in active remote mode
    if (proxyToggle) {
        const disableToggle = isConnected && isEnabled;
        proxyToggle.disabled = disableToggle;
        if (disableToggle) {
            proxyToggle.parentElement?.classList.add('opacity-40', 'pointer-events-none');
        } else {
            proxyToggle.parentElement?.classList.remove('opacity-40', 'pointer-events-none');
        }
    }
    
    // Start/stop stats sync
    if (isConnected && isEnabled) {
        startStatsSync();
    } else {
        stopStatsSync();
    }
}

function startStatsSync() {
    stopStatsSync();
    syncRemoteStats(); // Immediate first sync
    statsSyncTimer = setInterval(syncRemoteStats, 30000); // Every 30s
}

function stopStatsSync() {
    if (statsSyncTimer) {
        clearInterval(statsSyncTimer);
        statsSyncTimer = null;
    }
    // 当彻底停止同步时，强制清除内存里的残影数据并重绘UI
    if (state.remoteStats !== null) {
        state.remoteStats = null;
        if (state.callbacks.updateAggregateQuotaUI) {
            state.callbacks.updateAggregateQuotaUI();
        }
    }
}

async function syncRemoteStats() {
    try {
        const stats = await ipcRenderer.invoke('remote:sync-stats');
        if (stats) {
            state.remoteStats = stats;
            if (state.callbacks.updateAggregateQuotaUI) {
                state.callbacks.updateAggregateQuotaUI();
            }
            // Emit event so dashboard can update
            document.dispatchEvent(new CustomEvent('remote-stats-updated', { detail: stats }));
        }
    } catch (err) {
        console.error('[RemoteController] Stats sync failed:', err);
    }
}

async function loadRemoteKeys() {
    try {
        const res = await ipcRenderer.invoke('remote:get-keys');
        if (!res || !res.success) {
            console.error('Failed to load keys:', res?.error);
            return;
        }
        
        const keys = res.keys || [];
        const tbody = document.getElementById('remoteKeysTableBody');
        if (!tbody) return;
        
        if (keys.length === 0) {
            tbody.innerHTML = `<tr><td colspan="4" class="text-center py-4 text-outline/60">暂无 API Key，请点击上方创建</td></tr>`;
            return;
        }
        
        tbody.innerHTML = '';
        keys.forEach((k: any) => {
            const tr = document.createElement('tr');
            tr.className = 'border-b border-outline-variant/10 hover:bg-slate-50 dark:hover:bg-white/5 transition-colors';
            
            const displayKey = k.key.substring(0, 10) + '...' + k.key.substring(k.key.length - 4);
            const date = new Date(k.createdAt).toLocaleString();
            
            tr.innerHTML = `
                <td class="py-2.5 px-2 font-medium">${k.name}</td>
                <td class="py-2.5 font-mono text-outline/80">${displayKey}</td>
                <td class="py-2.5 text-outline/60">${date}</td>
                <td class="py-2.5 text-center">
                    <button class="btn-copy-remote-key text-primary hover:text-primary/80 mr-2" data-key="${k.key}" title="复制"><span class="material-symbols-outlined text-[16px] align-middle">content_copy</span></button>
                    <button class="btn-del-remote-key text-red-400 hover:text-red-600" data-id="${k.id}" title="删除"><span class="material-symbols-outlined text-[16px] align-middle">delete</span></button>
                </td>
            `;
            tbody.appendChild(tr);
        });
        
        document.querySelectorAll('.btn-copy-remote-key').forEach(btn => {
            btn.addEventListener('click', async (e) => {
                const b = e.currentTarget as HTMLButtonElement;
                const k = b.getAttribute('data-key') || '';
                await navigator.clipboard.writeText(k);
                const old = b.innerHTML;
                b.innerHTML = '<span class="material-symbols-outlined text-[16px] align-middle text-emerald-500">done</span>';
                setTimeout(() => { b.innerHTML = old; }, 1500);
            });
        });
        
        document.querySelectorAll('.btn-del-remote-key').forEach(btn => {
            btn.addEventListener('click', async (e) => {
                const b = e.currentTarget as HTMLButtonElement;
                const id = b.getAttribute('data-id') || '';
                if (confirm('确定要删除这个 Key 吗？客户端使用该 Key 将立即失效！')) {
                    await ipcRenderer.invoke('remote:delete-key', id);
                    loadRemoteKeys();
                }
            });
        });
        
    } catch (err) {
        console.error('loadRemoteKeys error:', err);
    }
}

async function openRemoteKeysModal() {
    const modal = document.getElementById('remoteKeysModal');
    if (modal) modal.classList.remove('hidden');
    
    // Clear input
    const input = document.getElementById('remoteNewKeyName') as HTMLInputElement;
    if (input) input.value = '';
    
    await loadRemoteKeys();
}

async function handleCreateRemoteKey() {
    const input = document.getElementById('remoteNewKeyName') as HTMLInputElement;
    const name = input?.value?.trim() || 'Default Key';
    
    try {
        const res = await ipcRenderer.invoke('remote:create-key', name);
        if (res && res.success) {
            if (input) input.value = '';
            await loadRemoteKeys();
        } else {
            alert('创建失败: ' + (res?.error || '未知错误'));
        }
    } catch (err) {
        alert('创建失败: ' + err);
    }
}
