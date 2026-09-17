import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';
import i18n from '../shared/i18n';
import { saveText } from '../shared/fileService';

// accountsUtil.ts: 从 accountsController.ts 拆出的、不依赖任何模块级 DOM 句柄的纯工具函数。
// 原文件 initAccountsEvents / updateViewTabUI 等仍共享 40 个模块级 DOM 句柄,这些零句柄依赖的
// 函数先迁出以降低主控制器体积。物理搬移,逻辑逐行等价,零回归。

// 统一导出账号配置（走 fileService：后端负责对话框、目录记忆、保存成功后自动打开文件夹）。
async function exportAccountConfig(provider: string) {
    await saveText(
        { channel: 'accounts:export-all', args: [provider] },
        state.currentLanguage === 'zh' ? '账号配置已成功导出！' : 'Account configuration exported successfully!',
        state.currentLanguage === 'zh' ? '导出失败: ' : 'Export failed: ',
    );
}

// 导出单账号配置（全局暴露，供 accountsRenderer 中每账号"导出"按钮调用）。
(window as any).exportSingleAccount = async (accId: string) => {
    await saveText(
        { channel: 'accounts:export-single', args: [accId] },
        state.currentLanguage === 'zh' ? '账号配置已成功导出！' : 'Account configuration exported successfully!',
        state.currentLanguage === 'zh' ? '导出失败: ' : 'Export failed: ',
    );
};

// refreshAccountLayoutFromCache:从 domReady 注入的 wailsConfigCache(后端 config.json)同步回填
// 号池布局/列数。注意:该 cache 在 domReady 才注入,晚于所有前端模块顶层执行,因此不能放在
// dashboardState.ts 模块顶层读取(会读到 undefined 而回退 localStorage 旧值);必须在组件
// onMounted(晚于 domReady)后调用,保证首次 updateLayoutUI 即用后端持久化值。
export function refreshAccountLayoutFromCache(): void {
    const layout = ipcRenderer.sendSync('settings:get-account-layout');
    const cols = Number(ipcRenderer.sendSync('settings:get-account-grid-columns'));
    if (layout === 'grid' || layout === 'list') {
        state.accountLayout = layout;
    }
    if (cols >= 3 && cols <= 5) {
        state.accountGridColumns = cols;
    }
}

// setGrokThawButtonVisible:控制工具栏「一键解冻」按钮在 Grok Tab 显、其它 Tab 隐。
// 镜像 setNvidiaPreferredModelsButtonVisible 范式:每次切 Tab 都幂等调用以同步显隐。
// 按钮默认带 hidden 类(Accounts.vue 模板),仅 grok Tab 分支 remove('hidden')。
function setGrokThawButtonVisible(visible: boolean): void {
    const btn = document.getElementById('btnGrokOneClickThaw');
    if (!btn) return;
    if (visible) btn.classList.remove('hidden');
    else btn.classList.add('hidden');
}

// setGrokCheckAuthButtonVisible:控制工具栏「检查授权」按钮在 Grok Tab 显、其它 Tab 隐。
// 与 setGrokThawButtonVisible 同构范式,两个 Grok 专属按钮成对随 Tab 切换显隐。
// 按钮默认带 hidden 类(Accounts.vue 模板),仅 grok Tab 分支 remove('hidden')。
function setGrokCheckAuthButtonVisible(visible: boolean): void {
    const btn = document.getElementById('btnGrokCheckAuth');
    if (!btn) return;
    if (visible) btn.classList.remove('hidden');
    else btn.classList.add('hidden');
}

// setNvidiaBatchAssignIPButtonVisible: 控制工具栏「分配住宅IP」按钮在 NVIDIA Tab 显、其它 Tab 隐。
function setNvidiaBatchAssignIPButtonVisible(visible: boolean): void {
    const btn = document.getElementById('btnNvidiaBatchAssignIP');
    if (!btn) return;
    if (visible) btn.classList.remove('hidden');
    else btn.classList.add('hidden');
}

export function updateBatchActionBarUI() {
    const bar = document.getElementById('batchActionBar');
    const lbl = document.getElementById('lblSelectedCount');
    const chkAll = document.getElementById('chkSelectAllAccounts') as HTMLInputElement | null;
    if (!bar || !lbl) return;

    const dict = i18n[state.currentLanguage] || i18n.zh;
    
    const count = state.selectedAccountIds.length;
    if (count > 0) {
        bar.classList.remove('hidden');
        bar.classList.add('flex');
        lbl.textContent = (dict.selectedAccountsCount || `已选择 {count} 个账号`).replace('{count}', String(count));
    } else {
        bar.classList.remove('flex');
        bar.classList.add('hidden');
    }

    if (chkAll) {
        const visibleCheckboxes = document.querySelectorAll('.account-card-checkbox') as NodeListOf<HTMLInputElement>;
        if (visibleCheckboxes.length > 0) {
            chkAll.checked = Array.from(visibleCheckboxes).every(cb => cb.checked);
        } else {
            chkAll.checked = false;
        }
    }
}

export function updatePoolModeUI() {
    const poolModeToggle = document.getElementById('poolModeToggle') as HTMLInputElement | null;
    if (!poolModeToggle) return;
    const isPool = poolModeToggle.checked;
    const label = poolModeToggle.nextElementSibling;
    if (!label) return;
    
    if (isPool) {
        poolModeToggle.className = 'toggle-checkbox absolute block w-5 h-5 rounded-full bg-white border-4 border-primary appearance-none cursor-pointer translate-x-5 transition-transform duration-200 ease-in-out';
        label.className = 'toggle-label block overflow-hidden h-5 rounded-full bg-primary cursor-pointer';
    } else {
        poolModeToggle.className = 'toggle-checkbox absolute block w-5 h-5 rounded-full bg-white border-4 border-outline-variant appearance-none cursor-pointer translate-x-0 transition-transform duration-200 ease-in-out';
        label.className = 'toggle-label block overflow-hidden h-5 rounded-full bg-outline-variant/50 dark:bg-white/10 cursor-pointer';
    }
}

export function updateLayoutUI() {
    const gridBtn = document.getElementById('btnLayoutGrid') as HTMLButtonElement | null;
    const listBtn = document.getElementById('btnLayoutList') as HTMLButtonElement | null;
    const selectGridColumns = document.getElementById('selectGridColumns') as HTMLSelectElement | null;
    const accountsListEl = document.getElementById('accountsList');
    
    const activeClass = 'p-1 rounded-md cursor-pointer transition-all duration-200 bg-white dark:bg-[#1a1f30] text-primary dark:text-primary-fixed-dim shadow-sm flex items-center justify-center';
    const inactiveClass = 'p-1 rounded-md cursor-pointer transition-all duration-200 text-slate-400 hover:text-slate-700 dark:hover:text-slate-200 flex items-center justify-center';
    
    if (state.accountLayout === 'grid') {
        if (gridBtn) gridBtn.className = activeClass;
        if (listBtn) listBtn.className = inactiveClass;
        if (selectGridColumns) {
            selectGridColumns.classList.remove('hidden');
            selectGridColumns.value = String(state.accountGridColumns);
        }
        if (accountsListEl) {
            accountsListEl.classList.remove('layout-list');
            accountsListEl.classList.add('layout-grid');
            accountsListEl.classList.remove('cols-3', 'cols-4', 'cols-5');
            accountsListEl.classList.add(`cols-${state.accountGridColumns}`);
        }
    } else {
        if (gridBtn) gridBtn.className = inactiveClass;
        if (listBtn) listBtn.className = activeClass;
        if (selectGridColumns) {
            selectGridColumns.classList.add('hidden');
        }
        if (accountsListEl) {
            accountsListEl.classList.remove('layout-grid', 'cols-3', 'cols-4', 'cols-5');
            accountsListEl.classList.add('layout-list');
        }
    }
}

export function initBatchActionEvents(): void {
    const chkAll = document.getElementById('chkSelectAllAccounts') as HTMLInputElement | null;
    if (chkAll) {
        chkAll.addEventListener('change', (e: any) => {
            const isChecked = e.target.checked;
            const visibleCheckboxes = document.querySelectorAll('.account-card-checkbox') as NodeListOf<HTMLInputElement>;
            visibleCheckboxes.forEach(cb => {
                const accId = cb.getAttribute('data-account-id');
                if (!accId) return;
                cb.checked = isChecked;
                if (isChecked) {
                    if (!state.selectedAccountIds.includes(accId)) {
                        state.selectedAccountIds.push(accId);
                    }
                } else {
                    state.selectedAccountIds = state.selectedAccountIds.filter(id => id !== accId);
                }
            });
            updateBatchActionBarUI();
        });
    }

    const btnBatchDelete = document.getElementById('btnBatchDeleteAccounts') as HTMLButtonElement | null;
    if (btnBatchDelete) {
        btnBatchDelete.addEventListener('click', async () => {
            const count = state.selectedAccountIds.length;
            if (count === 0) return;

            const dict = i18n[state.currentLanguage] || i18n.zh;
            const confirmMsg = (dict.batchDeleteAccountsConfirm || '确定要删除选中的 {count} 个账号吗？删除后不可恢复！')
                .replace('{count}', String(count));

            const $confirm = (window as any).$confirm;
            let confirmed = false;
            if (typeof $confirm === 'function') {
                confirmed = await $confirm(confirmMsg);
            } else {
                confirmed = confirm(confirmMsg);
            }
            if (!confirmed) return;

            const toDeleteIds = [...state.selectedAccountIds];
            try {
                await ipcRenderer.invoke('accounts:batch-remove', toDeleteIds);
            } catch (err) {
                ipcRenderer.send('accounts:batch-remove', toDeleteIds);
            }

            state.selectedAccountIds = [];
            updateBatchActionBarUI();
            ipcRenderer.send('accounts:get');
        });
    }
}

export function initImportExportEvents(): void {
    const btnExportAccounts = document.getElementById('btnExportAccounts') as HTMLButtonElement | null;
    if (btnExportAccounts) {
        btnExportAccounts.addEventListener('click', async () => {
            const provider = state.currentViewTab || 'antigravity';
            await exportAccountConfig(provider);
        });
    }

    const btnImportAccounts = document.getElementById('btnImportAccounts') as HTMLButtonElement | null;
    if (btnImportAccounts) {
        btnImportAccounts.addEventListener('click', async () => {
            try {
                const res = await ipcRenderer.invoke('accounts:import');
                const payload = (res && typeof res === 'object') ? res : null;
                if (!payload || payload.success === false) return;
                ipcRenderer.send('accounts:get');
            } catch (err) {
                console.error('Failed to import accounts:', err);
            }
        });
    }
}

// 导出到原 accountsController 语义命名(内部使用)
export { exportAccountConfig, setGrokThawButtonVisible, setGrokCheckAuthButtonVisible, setNvidiaBatchAssignIPButtonVisible };