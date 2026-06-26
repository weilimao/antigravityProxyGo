import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';

let relayUsers: any[] = [];

export function initRelayEvents() {
    // Toggle relay server
    const chkRelayEnabled = document.getElementById('chkRelayEnabled') as HTMLInputElement;
    const relayPortInput = document.getElementById('relayPortInput') as HTMLInputElement;
    const btnAddRelayUser = document.getElementById('btnAddRelayUser');
    
    if (chkRelayEnabled) {
        chkRelayEnabled.addEventListener('change', async () => {
            const port = relayPortInput?.value || '18444';
            try {
                await ipcRenderer.invoke('relay:set-config', { enabled: chkRelayEnabled.checked, port });
            } catch (err) {
                console.error('[RelayController] Failed to set config:', err);
            }
        });
    }

    if (btnAddRelayUser) {
        btnAddRelayUser.addEventListener('click', () => openAddUserModal());
    }

    // Add user modal buttons
    const btnRelayUserConfirm = document.getElementById('btnRelayUserConfirm');
    const btnRelayUserCancel = document.getElementById('btnRelayUserCancel');
    
    if (btnRelayUserConfirm) {
        btnRelayUserConfirm.addEventListener('click', handleAddUser);
    }
    if (btnRelayUserCancel) {
        btnRelayUserCancel.addEventListener('click', closeAddUserModal);
    }

    // Listen for relay config updates
    ipcRenderer.on('relay-state', (_e: any, config: any) => {
        if (chkRelayEnabled) chkRelayEnabled.checked = !!config?.enabled;
        if (relayPortInput) relayPortInput.value = config?.port || '18444';
    });

    // Load persisted users on init
    refreshRelayUsers();
}

export async function refreshRelayUsers() {
    try {
        const users = await ipcRenderer.invoke('relay:get-users');
        relayUsers = users || [];
        renderRelayUsers();
    } catch (err) {
        console.error('[RelayController] Failed to get users:', err);
    }
}

function renderRelayUsers() {
    const container = document.getElementById('relayUsersList');
    if (!container) return;

    if (relayUsers.length === 0) {
        container.innerHTML = `<div class="text-center text-outline/60 py-8 text-[13px]">暂无中继用户，请点击上方按钮添加</div>`;
        return;
    }

    container.innerHTML = relayUsers.map(user => `
        <div class="flex items-center justify-between p-3 rounded-lg border border-outline-variant/20 bg-white/50 dark:bg-white/5 mb-2">
            <div class="flex items-center gap-3">
                <div class="w-2 h-2 rounded-full ${user.enabled ? 'bg-emerald-500' : 'bg-slate-300 dark:bg-slate-600'}"></div>
                <div>
                    <div class="text-[13px] font-semibold text-on-surface dark:text-white">${user.key}</div>
                    <div class="text-[11px] text-outline/60">${user.remark || '无备注'} · 创建于 ${new Date(user.createdAt).toLocaleDateString()}</div>
                </div>
            </div>
            <div class="flex items-center gap-2">
                <label class="relative inline-block w-8 h-4 cursor-pointer">
                    <input type="checkbox" class="sr-only peer" ${user.enabled ? 'checked' : ''}
                        onchange="window._relayToggleUser('${user.id}', this.checked)" />
                    <div class="w-8 h-4 bg-slate-300 dark:bg-slate-600 rounded-full peer-checked:bg-emerald-500 transition-colors"></div>
                    <div class="absolute left-0.5 top-0.5 w-3 h-3 bg-white rounded-full transition-transform peer-checked:translate-x-4"></div>
                </label>
                <button onclick="window._relayRemoveUser('${user.id}')" 
                    class="text-red-400 hover:text-red-600 transition-colors">
                    <span class="material-symbols-outlined text-[16px]">delete</span>
                </button>
            </div>
        </div>
    `).join('');
}

// Global handlers for inline onclick
(window as any).refreshRelayUsers = refreshRelayUsers;

(window as any)._relayToggleUser = async (id: string, enabled: boolean) => {
    try {
        await ipcRenderer.invoke('relay:toggle-user', id, enabled);
        await refreshRelayUsers();
    } catch (err) {
        console.error('[RelayController] Failed to toggle user:', err);
    }
};

(window as any)._relayRemoveUser = async (id: string) => {
    if (!confirm('确定要删除该中继用户吗？')) return;
    try {
        await ipcRenderer.invoke('relay:remove-user', id);
        await refreshRelayUsers();
    } catch (err) {
        console.error('[RelayController] Failed to remove user:', err);
    }
};

function openAddUserModal() {
    const modal = document.getElementById('relayUserModal');
    if (modal) modal.classList.remove('hidden');
    // Clear inputs
    const keyInput = document.getElementById('relayUserKeyInput') as HTMLInputElement;
    const pwdInput = document.getElementById('relayUserPasswordInput') as HTMLInputElement;
    const remarkInput = document.getElementById('relayUserRemarkInput') as HTMLInputElement;
    if (keyInput) keyInput.value = '';
    if (pwdInput) pwdInput.value = '';
    if (remarkInput) remarkInput.value = '';
}

function closeAddUserModal() {
    const modal = document.getElementById('relayUserModal');
    if (modal) modal.classList.add('hidden');
}

async function handleAddUser() {
    const keyInput = document.getElementById('relayUserKeyInput') as HTMLInputElement;
    const pwdInput = document.getElementById('relayUserPasswordInput') as HTMLInputElement;
    const remarkInput = document.getElementById('relayUserRemarkInput') as HTMLInputElement;
    
    const key = keyInput?.value?.trim();
    const password = pwdInput?.value;
    const remark = remarkInput?.value?.trim() || '';
    
    if (!key || !password) {
        alert('Key 和密码不能为空');
        return;
    }
    
    try {
        const res = await ipcRenderer.invoke('relay:add-user', key, password, remark);
        if (res?.success) {
            closeAddUserModal();
            await refreshRelayUsers();
        } else {
            alert(res?.error || '添加失败');
        }
    } catch (err) {
        console.error('[RelayController] Failed to add user:', err);
        alert('添加用户失败');
    }
}
