/**
 * 会话路由绑定 Modal 控制逻辑：从 accountsController.ts 抽离的独立模块。
 *
 * 高内聚：承担会话路由绑定关系的列表加载、弹窗显隐、批量清空；
 * 自带 7 个 DOM handle、4 个函数，由 accountsController.initAccountsEvents 委托
 * 调用 initSessionBindingsModalEvents() 完成句柄赋值与事件绑定。
 * 依赖：state（当前通道）、ipcRenderer、i18n、全局 $confirm；无跨簇函数依赖。
 */
import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';
import i18n from '../shared/i18n';

let btnShowSessionBindings: HTMLButtonElement | null;
let sessionBindingsModal: HTMLDivElement | null;
let sessionBindingsModalCloseBtn: HTMLButtonElement | null;
let sessionBindingsModalCloseBtnSecondary: HTMLButtonElement | null;
let sessionBindingsTableBody: HTMLTableSectionElement | null;
let sessionBindingsModalClearAllBtn: HTMLButtonElement | null;
let sessionBindingsCount: HTMLSpanElement | null;

// 句柄赋值 + 事件绑定（由 accountsController.initAccountsEvents 委托调用）
export function initSessionBindingsModalEvents(): void {
    btnShowSessionBindings = document.getElementById('btnShowSessionBindings') as HTMLButtonElement | null;
    sessionBindingsModal = document.getElementById('sessionBindingsModal') as HTMLDivElement | null;
    sessionBindingsModalCloseBtn = document.getElementById('sessionBindingsModalCloseBtn') as HTMLButtonElement | null;
    sessionBindingsModalCloseBtnSecondary = document.getElementById('sessionBindingsModalCloseBtnSecondary') as HTMLButtonElement | null;
    sessionBindingsTableBody = document.getElementById('sessionBindingsTableBody') as HTMLTableSectionElement | null;
    sessionBindingsModalClearAllBtn = document.getElementById('sessionBindingsModalClearAllBtn') as HTMLButtonElement | null;
    sessionBindingsCount = document.getElementById('sessionBindingsCount') as HTMLSpanElement | null;

    if (btnShowSessionBindings) {
        btnShowSessionBindings.addEventListener('click', showSessionBindings);
    }
    if (sessionBindingsModalCloseBtn) {
        sessionBindingsModalCloseBtn.addEventListener('click', hideSessionBindings);
    }
    if (sessionBindingsModalCloseBtnSecondary) {
        sessionBindingsModalCloseBtnSecondary.addEventListener('click', hideSessionBindings);
    }
    if (sessionBindingsModalClearAllBtn) {
        sessionBindingsModalClearAllBtn.addEventListener('click', clearAllSessionBindings);
    }
}

async function loadSessionBindings() {
    const dict = i18n[state.currentLanguage] || {};
    const tableBody = sessionBindingsTableBody;
    if (!tableBody) return;
    tableBody.innerHTML = `
        <tr>
            <td colspan="4" class="p-8 text-center text-outline dark:text-outline-variant italic">
                <span class="inline-block animate-spin mr-2">⏳</span>${dict.sessionBindingsLoading || '正在加载会话绑定数据...'}
            </td>
        </tr>
    `;
    
    try {
        const list = await ipcRenderer.invoke('sessions:get', state.currentViewTab || '') as Array<{
            sessionKey: string;
            accountId: string;
            accountEmail: string;
            provider?: string;
            lastActive: number;
        }>;
        
        if (sessionBindingsCount) {
            const totalText = (dict.sessionBindingsTotal || '共 {count} 条记录').replace('{count}', list.length.toString());
            sessionBindingsCount.textContent = totalText;
        }
        
        if (list.length === 0) {
            tableBody.innerHTML = `
                <tr>
                    <td colspan="4" class="p-8 text-center text-outline dark:text-outline-variant italic">
                        📭 ${dict.sessionBindingsEmpty || '当前暂无会话路由绑定关系'}
                    </td>
                </tr>
            `;
            return;
        }
        
        // 按照最后活跃时间降序排序
        list.sort((a, b) => b.lastActive - a.lastActive);
        
        tableBody.innerHTML = '';
        list.forEach(item => {
            const tr = document.createElement('tr');
            tr.className = 'hover:bg-slate-50 dark:hover:bg-white/5 transition-colors border-b border-outline-variant/10';
            
            // 格式化活跃时间
            const timeStr = new Date(item.lastActive).toLocaleString('zh-CN', {
                hour12: false,
                month: '2-digit',
                day: '2-digit',
                hour: '2-digit',
                minute: '2-digit',
                second: '2-digit'
            });
            
            // 为了美观，给 SessionKey 不同的类型不同的徽章
            let keyBadge = '';
            let channelBadge = '';
            let keyText = item.sessionKey;
            
            const projectLbText = dict.projectLoadBalancing || '项目负载均衡';
            const accountLbText = dict.poolLoadBalance || '账号负载均衡';
            
            if (item.sessionKey.startsWith('auth:prj:')) {
                keyBadge = '<span class="px-1.5 py-0.5 rounded bg-primary/10 text-primary dark:text-primary-fixed-dim text-[10px] font-bold mr-1.5">Bearer</span>';
                channelBadge = `<span class="px-1.5 py-0.5 rounded bg-purple-500/10 text-purple-500 dark:text-purple-300 text-[10px] font-bold mr-1.5">${projectLbText}</span>`;
                keyText = item.sessionKey.substring(9);
            } else if (item.sessionKey.startsWith('auth:acc:')) {
                keyBadge = '<span class="px-1.5 py-0.5 rounded bg-primary/10 text-primary dark:text-primary-fixed-dim text-[10px] font-bold mr-1.5">Bearer</span>';
                channelBadge = `<span class="px-1.5 py-0.5 rounded bg-teal-500/10 text-teal-600 dark:text-teal-300 text-[10px] font-bold mr-1.5">${accountLbText}</span>`;
                keyText = item.sessionKey.substring(9);
            } else if (item.sessionKey.startsWith('sock:prj:')) {
                keyBadge = '<span class="px-1.5 py-0.5 rounded bg-amber-500/10 text-amber-500 text-[10px] font-bold mr-1.5">Socket</span>';
                channelBadge = `<span class="px-1.5 py-0.5 rounded bg-purple-500/10 text-purple-500 dark:text-purple-300 text-[10px] font-bold mr-1.5">${projectLbText}</span>`;
                keyText = item.sessionKey.substring(9);
            } else if (item.sessionKey.startsWith('sock:acc:')) {
                keyBadge = '<span class="px-1.5 py-0.5 rounded bg-amber-500/10 text-amber-500 text-[10px] font-bold mr-1.5">Socket</span>';
                channelBadge = `<span class="px-1.5 py-0.5 rounded bg-teal-500/10 text-teal-600 dark:text-teal-300 text-[10px] font-bold mr-1.5">${accountLbText}</span>`;
                keyText = item.sessionKey.substring(9);
            } else if (item.sessionKey.startsWith('auth:')) {
                keyBadge = '<span class="px-1.5 py-0.5 rounded bg-primary/10 text-primary dark:text-primary-fixed-dim text-[10px] font-bold mr-1.5">Bearer</span>';
                keyText = item.sessionKey.substring(5);
            } else if (item.sessionKey.startsWith('sock:')) {
                keyBadge = '<span class="px-1.5 py-0.5 rounded bg-amber-500/10 text-amber-500 text-[10px] font-bold mr-1.5">Socket</span>';
                keyText = item.sessionKey.substring(5);
            }
            
            tr.innerHTML = `
                <td class="p-3 font-data-mono break-all max-w-[280px]">
                    <div class="flex items-center flex-wrap gap-1">
                        ${keyBadge}
                        ${channelBadge}
                        <span class="text-on-surface dark:text-white font-medium">${keyText}</span>
                    </div>
                </td>
                <td class="p-3 text-outline dark:text-outline-variant font-medium break-all max-w-[200px]">${item.accountEmail}</td>
                <td class="p-3 text-outline/80 dark:text-outline-variant/80 font-data-mono">${timeStr}</td>
                <td class="p-3 text-center">
                    <button class="unbind-btn text-red-500 hover:text-white hover:bg-red-500 active:bg-red-600 px-2 py-1 rounded transition-all text-[11px] font-bold border border-red-500/20" data-key="${item.sessionKey}">
                        ${dict.btnUnbind || '解绑'}
                    </button>
                </td>
            `;
            
            // 绑定解绑事件
            const unbindBtn = tr.querySelector('.unbind-btn');
            if (unbindBtn) {
                unbindBtn.addEventListener('click', async (e) => {
                    const key = (e.currentTarget as HTMLButtonElement).getAttribute('data-key');
                    if (!key) return;
                    
                    (e.currentTarget as HTMLButtonElement).disabled = true;
                    (e.currentTarget as HTMLButtonElement).textContent = dict.btnUnbindProcessing || '处理中...';
                    
                    try {
                        const res = await ipcRenderer.invoke('sessions:unbind', key);
                        if (res && res.success) {
                            loadSessionBindings();
                        } else {
                            alert(dict.unbindFailed || '解绑失败，请重试');
                            (e.currentTarget as HTMLButtonElement).disabled = false;
                            (e.currentTarget as HTMLButtonElement).textContent = dict.btnUnbind || '解绑';
                        }
                    } catch (err) {
                        console.error('Failed to unbind session:', err);
                        alert(dict.unbindRequestFailed || '解绑请求失败');
                        (e.currentTarget as HTMLButtonElement).disabled = false;
                        (e.currentTarget as HTMLButtonElement).textContent = dict.btnUnbind || '解绑';
                    }
                });
            }
            
            tableBody.appendChild(tr);
        });
        
    } catch (err) {
        console.error('Failed to load session bindings:', err);
        const errMsg = (dict.loadBindingsFailed || '获取绑定关系失败: {error}').replace('{error}', (err as Error).message);
        tableBody.innerHTML = `
            <tr>
                <td colspan="4" class="p-8 text-center text-red-500 italic">
                    ❌ ${errMsg}
                </td>
            </tr>
        `;
    }
}

function showSessionBindings() {
    if (!sessionBindingsModal) return;
    sessionBindingsModal.classList.remove('pointer-events-none', 'opacity-0');
    sessionBindingsModal.classList.add('opacity-100');
    const container = sessionBindingsModal.querySelector('#sessionBindingsModalContainer');
    if (container) {
        container.classList.remove('scale-95');
        container.classList.add('scale-100');
    }
    loadSessionBindings();
}

function hideSessionBindings() {
    if (!sessionBindingsModal) return;
    sessionBindingsModal.classList.add('opacity-0', 'pointer-events-none');
    sessionBindingsModal.classList.remove('opacity-100');
    const container = sessionBindingsModal.querySelector('#sessionBindingsModalContainer');
    if (container) {
        container.classList.add('scale-95');
        container.classList.remove('scale-100');
    }
}

async function clearAllSessionBindings() {
    const dict = i18n[state.currentLanguage] || {};
    const confirmMsg = dict.btnClearAllBindingsConfirm || '您确定要清空所有的会话路由绑定关系吗？这将会使后续客户端的请求重新在可用账号池中进行轮询或一致性哈希分配。';
    if (!await $confirm(confirmMsg)) {
        return;
    }
    if (sessionBindingsModalClearAllBtn) {
        sessionBindingsModalClearAllBtn.disabled = true;
        const span = sessionBindingsModalClearAllBtn.querySelector('span:last-child');
        if (span) span.textContent = dict.btnClearAllBindingsProcessing || '清空中...';
    }
    try {
        const res = await ipcRenderer.invoke('pool:clear-sessions');
        if (res && res.success) {
            loadSessionBindings();
        }
    } catch (err) {
        console.error('Failed to clear sessions:', err);
    } finally {
        if (sessionBindingsModalClearAllBtn) {
            sessionBindingsModalClearAllBtn.disabled = false;
            const span = sessionBindingsModalClearAllBtn.querySelector('span:last-child');
            if (span) span.textContent = dict.btnClearAllBindings || '清空所有绑定';
        }
    }
}
