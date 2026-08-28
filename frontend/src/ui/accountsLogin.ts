import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';
import i18n from '../shared/i18n';
import { showOneStopAuthModal } from './accountsAuthModal';

// accountsLogin.ts: 从 accountsController.ts 拆出的账号登录逻辑(startLogin / startProjectLogin)。
// 采用「自包含 DOM 句柄」策略:函数内部直接 getElementById,不共享 accountsController 的模块级
// 句柄缓存,零跨文件耦合。物理搬移,逻辑逐行等价,零回归。

// Start Google account pool login
export async function startLogin(provider: any) {
    const btnAddAccount = document.getElementById('btnAddAccount') as HTMLButtonElement | null;
    const addAccountDropdown = document.getElementById('addAccountDropdown') as HTMLDivElement | null;
    if (state.isLoadingAuth) return;
    state.isLoadingAuth = true;
    if (addAccountDropdown) addAccountDropdown.classList.add('hidden');

    if (btnAddAccount) {
        const origText = btnAddAccount.innerHTML;
        const dict = i18n[state.currentLanguage] || i18n.zh;
        btnAddAccount.innerHTML = `<span class="material-symbols-outlined text-[16px] animate-spin">refresh</span> ${dict.btnLoggingIn || (state.currentLanguage === 'zh' ? '登录中...' : 'Logging in...')}`;
        btnAddAccount.classList.add('opacity-70');

        const handleMouseEnter = () => {
            if (state.isLoadingAuth && btnAddAccount) {
                btnAddAccount.innerHTML = `<span class="material-symbols-outlined text-[16px]">cancel</span> ${dict.btnCancelLogin || (state.currentLanguage === 'zh' ? '取消登录' : 'Cancel Login')}`;
                btnAddAccount.classList.remove('bg-primary', 'hover:bg-primary/90');
                btnAddAccount.classList.add('bg-red-500', 'hover:bg-red-600');
            }
        };

        const handleMouseLeave = () => {
            if (state.isLoadingAuth && btnAddAccount) {
                btnAddAccount.innerHTML = `<span class="material-symbols-outlined text-[16px] animate-spin">refresh</span> ${dict.btnLoggingIn || (state.currentLanguage === 'zh' ? '登录中...' : 'Logging in...')}`;
                btnAddAccount.classList.remove('bg-red-500', 'hover:bg-red-600');
                btnAddAccount.classList.add('bg-primary', 'hover:bg-primary/90');
            }
        };

        btnAddAccount.addEventListener('mouseenter', handleMouseEnter);
        btnAddAccount.addEventListener('mouseleave', handleMouseLeave);

        try {
            const authRequest = typeof provider === 'object' && provider !== null
                ? provider
                : { provider };
            const res = await ipcRenderer.invoke('auth:login', authRequest);
            if (!res.success) {
                alert('登录失败或已取消: ' + res.error);
            }
        } catch (err: any) {
            alert('登录出错: ' + err.message);
        } finally {
            state.isLoadingAuth = false;
            if (btnAddAccount) {
                btnAddAccount.removeEventListener('mouseenter', handleMouseEnter);
                btnAddAccount.removeEventListener('mouseleave', handleMouseLeave);
                btnAddAccount.innerHTML = origText;
                btnAddAccount.classList.remove('opacity-70', 'bg-red-500', 'hover:bg-red-600');
                btnAddAccount.classList.add('bg-primary', 'hover:bg-primary/90');
            }
        }
    }
}

// Start Project binding login
export async function startProjectLogin() {
    const addAccountDropdown = document.getElementById('addAccountDropdown') as HTMLDivElement | null;
    if (addAccountDropdown) addAccountDropdown.classList.add('hidden');
    if (state.isLoadingAuth) return;
    state.isLoadingAuth = true;
    
    try {
        const result = await showOneStopAuthModal();
        if (result && result.success) {
            // GCP login successful
        }
    } catch (err: any) {
        alert('登录发生错误: ' + err.message);
    } finally {
        state.isLoadingAuth = false;
    }
}

// 全局窗口注册(替代原 accountsController.ts 内的 window 注册,保持 DOM inline onclick 可用)。
(window as any).startLogin = startLogin;
(window as any).startProjectLogin = startProjectLogin;