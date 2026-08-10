/**
 * NVIDIA 账号录入/编辑 Modal 控制逻辑：从 accountsController.ts 抽离的独立模块。
 *
 * 高内聚：承担 NVIDIA 号池账号的「添加 / 编辑 / 保存 / 拉取远端模型清单」弹窗生命周期；
 * 自带 7 个 DOM handle、1 个模块级编辑态标记(nvidiaEditId)、6 个函数,由
 * accountsController.initAccountsEvents 委托调用 initNvidiaAccountModalEvents() 完成句柄赋值与事件绑定。
 * 依赖：ipcRenderer、nvidiaRevealAccountId(来自 revealKeyState,只读写 .value);
 * 无 state / i18n / 跨簇函数依赖(表单字段一律 document.getElementById 局部读取)。
 */
import { ipcRenderer } from '../shared/ipc';
import { nvidiaRevealAccountId } from '../shared/revealKeyState';
import i18n from '../shared/i18n';
import state from './dashboardState';

let nvidiaAccountModal: HTMLDivElement | null;
let nvidiaAccountModalContainer: HTMLDivElement | null;
let btnNvidiaModalSave: HTMLButtonElement | null;
let btnNvidiaModalCancel: HTMLButtonElement | null;
let btnNvidiaModalClose: HTMLButtonElement | null;
let nvidiaModalError: HTMLDivElement | null;
let btnNvidiaFetchModels: HTMLButtonElement | null;

// 句柄赋值 + 事件绑定（由 accountsController.initAccountsEvents 委托调用）
export function initNvidiaAccountModalEvents(): void {
    nvidiaAccountModal = document.getElementById('nvidiaAccountModal') as HTMLDivElement | null;
    nvidiaAccountModalContainer = document.getElementById('nvidiaAccountModalContainer') as HTMLDivElement | null;
    btnNvidiaModalSave = document.getElementById('btnNvidiaModalSave') as HTMLButtonElement | null;
    btnNvidiaModalCancel = document.getElementById('btnNvidiaModalCancel') as HTMLButtonElement | null;
    btnNvidiaModalClose = document.getElementById('btnNvidiaModalClose') as HTMLButtonElement | null;
    nvidiaModalError = document.getElementById('nvidiaModalError') as HTMLDivElement | null;
    btnNvidiaFetchModels = document.getElementById('btnNvidiaFetchModels') as HTMLButtonElement | null;
    // NVIDIA 账号模态：关闭/取消/保存
    if (btnNvidiaModalClose) btnNvidiaModalClose.addEventListener('click', closeNvidiaAccountModal);
    if (btnNvidiaModalCancel) btnNvidiaModalCancel.addEventListener('click', closeNvidiaAccountModal);
    if (btnNvidiaModalSave) btnNvidiaModalSave.addEventListener('click', submitNvidiaAccount);
    if (btnNvidiaFetchModels) btnNvidiaFetchModels.addEventListener('click', fetchNvidiaModels);
}

// writeNvidiaModalError:setRevealKeyWarning 单例 handler 被注入时,controller 需要把
// 取明文失败提示写到本模块的 nvidia 红条;句柄已随簇迁出,提供本出口供 controller 间接写。
export function writeNvidiaModalError(msg: string): void {
    if (nvidiaModalError) {
        nvidiaModalError.textContent = msg;
        nvidiaModalError.classList.remove('hidden');
    }
}

export function openNvidiaAccountModal() {
    if (!nvidiaAccountModal || !nvidiaAccountModalContainer) return;

    // 添加态:清空编辑态标记
    nvidiaEditId = null;
    // 复位明文查看目标:添加态不允许从后端取回明文 Key。
    nvidiaRevealAccountId.value = null;

    // 标题与保存按钮文案复位为添加态
    const titleEl = nvidiaAccountModal.querySelector('[data-i18n="nvidiaAddModalTitle"]') as HTMLElement | null;
    if (titleEl) {
        const dict = i18n[state.currentLanguage] || i18n.zh;
        titleEl.textContent = dict.nvidiaAddModalTitle || '添加 NVIDIA 号池账号';
    }
    if (btnNvidiaModalSave) {
        btnNvidiaModalSave.textContent = '添加账号';
    }

    // Clear previous inputs
    const inputBaseUrl = document.getElementById('inputNvidiaBaseUrl') as HTMLInputElement;
    const inputApiKey = document.getElementById('inputNvidiaApiKey') as HTMLInputElement;
    const inputLabel = document.getElementById('inputNvidiaLabel') as HTMLInputElement;
    const inputModelSonnet = document.getElementById('inputNvidiaModelSonnet') as HTMLInputElement;
    const inputModelOpus = document.getElementById('inputNvidiaModelOpus') as HTMLInputElement;
    const inputModelHaiku = document.getElementById('inputNvidiaModelHaiku') as HTMLInputElement;
    const inputModelFable = document.getElementById('inputNvidiaModelFable') as HTMLInputElement;
    const inputModelDefault = document.getElementById('inputNvidiaModelDefault') as HTMLInputElement;

    if (inputBaseUrl) inputBaseUrl.value = '';
    if (inputApiKey) inputApiKey.value = '';
    if (inputLabel) inputLabel.value = '';
    if (inputModelSonnet) inputModelSonnet.value = '';
    if (inputModelOpus) inputModelOpus.value = '';
    if (inputModelHaiku) inputModelHaiku.value = '';
    if (inputModelFable) inputModelFable.value = '';
    if (inputModelDefault) inputModelDefault.value = '';

    if (nvidiaModalError) {
        nvidiaModalError.classList.add('hidden');
        nvidiaModalError.textContent = '';
    }

    const selectIds = [
        'selectNvidiaModelSonnet',
        'selectNvidiaModelOpus',
        'selectNvidiaModelHaiku',
        'selectNvidiaModelFable',
        'selectNvidiaModelDefault'
    ];
    selectIds.forEach(id => {
        const selectEl = document.getElementById(id) as HTMLSelectElement | null;
        if (selectEl) {
            selectEl.classList.add('hidden');
            selectEl.innerHTML = '<option value="">选择模型...</option>';
        }
    });

    nvidiaAccountModal.classList.remove('opacity-0', 'pointer-events-none', 'hidden');
    nvidiaAccountModalContainer.classList.remove('scale-95');
    nvidiaAccountModalContainer.classList.add('scale-100');
}

function closeNvidiaAccountModal() {
    if (!nvidiaAccountModal || !nvidiaAccountModalContainer) return;
    nvidiaAccountModalContainer.classList.remove('scale-100');
    nvidiaAccountModalContainer.classList.add('scale-95');
    nvidiaAccountModal.classList.add('opacity-0', 'pointer-events-none');
    // 复位明文查看目标:确保「关闭→重新编辑同一账号」时 PasswordInput 的 watch
    // 能被 null→acc.id 的变化触发(否则 revealed/show 不复位,而输入框已被 openEdit 清空,
    // 眼睛会卡在「已取回」分支跳过 IPC,导致永远取不回明文)。
    nvidiaRevealAccountId.value = null;
}

// 当前处于编辑态的 NVIDIA 账号 id(添加态为 null),供 submitNvidiaAccount 区分 add/update。
let nvidiaEditId: string | null = null;

// openEditNvidiaAccount:预填 NVIDIA 账号模态框已有字段并切入编辑态。
// 账号展示名 = acc.Email;API Key 因后端不下发明文,编辑框留空,留空表示保持不变。
export function openEditNvidiaAccount(acc: any) {
    if (!nvidiaAccountModal || !nvidiaAccountModalContainer) return;

    // 查找最新账号数据快照,防止旧卡片闭包数据过时
    const latestAcc = state.currentAccountsList?.find((a: any) => a.id === acc.id) || acc;

    nvidiaEditId = latestAcc.id;
    // 明文查看:把当前编辑账号 id 注入 PasswordInput(revealProvider="nvidia" 时据此取明文)。
    nvidiaRevealAccountId.value = latestAcc.id;

    const inputBaseUrl = document.getElementById('inputNvidiaBaseUrl') as HTMLInputElement;
    const inputApiKey = document.getElementById('inputNvidiaApiKey') as HTMLInputElement;
    const inputLabel = document.getElementById('inputNvidiaLabel') as HTMLInputElement;
    const inputModelSonnet = document.getElementById('inputNvidiaModelSonnet') as HTMLInputElement;
    const inputModelOpus = document.getElementById('inputNvidiaModelOpus') as HTMLInputElement;
    const inputModelHaiku = document.getElementById('inputNvidiaModelHaiku') as HTMLInputElement;
    const inputModelFable = document.getElementById('inputNvidiaModelFable') as HTMLInputElement;
    const inputModelDefault = document.getElementById('inputNvidiaModelDefault') as HTMLInputElement;

    if (inputBaseUrl) inputBaseUrl.value = latestAcc.baseUrl || '';
    if (inputApiKey) {
        inputApiKey.value = ''; // 不预填明文 Key,留空保持不变
        inputApiKey.placeholder = latestAcc.maskedKey || 'nvapi-... (留空保持不变)';
    }
    if (inputLabel) inputLabel.value = latestAcc.email || '';
    if (inputModelSonnet) inputModelSonnet.value = latestAcc.modelSonnet || '';
    if (inputModelOpus) inputModelOpus.value = latestAcc.modelOpus || '';
    if (inputModelHaiku) inputModelHaiku.value = latestAcc.modelHaiku || '';
    if (inputModelFable) inputModelFable.value = latestAcc.modelFable || '';
    if (inputModelDefault) inputModelDefault.value = latestAcc.defaultModel || '';

    // 隐藏模型选择下拉(编辑态不自动拉远端模型,保持手填;用户可点"获取模型")。
    const selectIds = [
        'selectNvidiaModelSonnet',
        'selectNvidiaModelOpus',
        'selectNvidiaModelHaiku',
        'selectNvidiaModelFable',
        'selectNvidiaModelDefault'
    ];
    selectIds.forEach(id => {
        const selectEl = document.getElementById(id) as HTMLSelectElement | null;
        if (selectEl) {
            selectEl.classList.add('hidden');
            selectEl.innerHTML = '<option value="">选择模型...</option>';
        }
    });

    if (nvidiaModalError) {
        nvidiaModalError.classList.add('hidden');
        nvidiaModalError.textContent = '';
    }

    // 标题与保存按钮文案切入编辑态。
    const titleEl = nvidiaAccountModal.querySelector('[data-i18n="nvidiaAddModalTitle"]') as HTMLElement | null;
    if (titleEl) titleEl.textContent = '编辑 NVIDIA 号池账号';
    if (btnNvidiaModalSave) {
        btnNvidiaModalSave.textContent = '保存修改';
    }

    nvidiaAccountModal.classList.remove('opacity-0', 'pointer-events-none', 'hidden');
    nvidiaAccountModalContainer.classList.remove('scale-95');
    nvidiaAccountModalContainer.classList.add('scale-100');
}

async function submitNvidiaAccount() {
    const inputBaseUrl = document.getElementById('inputNvidiaBaseUrl') as HTMLInputElement;
    const inputApiKey = document.getElementById('inputNvidiaApiKey') as HTMLInputElement;
    const inputLabel = document.getElementById('inputNvidiaLabel') as HTMLInputElement;
    const inputModelSonnet = document.getElementById('inputNvidiaModelSonnet') as HTMLInputElement;
    const inputModelOpus = document.getElementById('inputNvidiaModelOpus') as HTMLInputElement;
    const inputModelHaiku = document.getElementById('inputNvidiaModelHaiku') as HTMLInputElement;
    const inputModelFable = document.getElementById('inputNvidiaModelFable') as HTMLInputElement;
    const inputModelDefault = document.getElementById('inputNvidiaModelDefault') as HTMLInputElement;

    if (!inputBaseUrl || !inputApiKey) return;

    let baseUrl = inputBaseUrl.value.trim();
    const apiKey = inputApiKey.value.trim();

    if (!baseUrl) {
        baseUrl = 'https://integrate.api.nvidia.com/v1';
    }
    if (!apiKey && !nvidiaEditId) {
        if (nvidiaModalError) {
            nvidiaModalError.textContent = '请输入 API Key';
            nvidiaModalError.classList.remove('hidden');
        }
        return;
    }

    try {
        if (btnNvidiaModalSave) {
            btnNvidiaModalSave.disabled = true;
            btnNvidiaModalSave.textContent = nvidiaEditId ? '正在保存...' : '正在添加...';
        }

        let res;
        if (nvidiaEditId) {
            // 编辑态:定位既有账号;apiKey 留空保持不变。
            res = await ipcRenderer.invoke('nvidia:update',
                nvidiaEditId,
                baseUrl,
                apiKey,
                inputLabel?.value.trim() || '',
                inputModelDefault?.value.trim() || '',
                inputModelSonnet?.value.trim() || '',
                inputModelOpus?.value.trim() || '',
                inputModelHaiku?.value.trim() || '',
                inputModelFable?.value.trim() || ''
            );
        } else {
            res = await ipcRenderer.invoke('nvidia:add',
                baseUrl,
                apiKey,
                inputLabel?.value.trim() || '',
                inputModelDefault?.value.trim() || '',
                inputModelSonnet?.value.trim() || '',
                inputModelOpus?.value.trim() || '',
                inputModelHaiku?.value.trim() || '',
                inputModelFable?.value.trim() || ''
            );
        }

        if (res && res.success) {
            closeNvidiaAccountModal();
            ipcRenderer.send('accounts:get');
        } else {
            if (nvidiaModalError) {
                nvidiaModalError.textContent = res?.error || (nvidiaEditId ? '保存失败' : '添加失败');
                nvidiaModalError.classList.remove('hidden');
            }
        }
    } catch (err: any) {
        if (nvidiaModalError) {
            nvidiaModalError.textContent = err.message || '系统错误';
            nvidiaModalError.classList.remove('hidden');
        }
    } finally {
        if (btnNvidiaModalSave) {
            btnNvidiaModalSave.disabled = false;
            btnNvidiaModalSave.textContent = nvidiaEditId ? '保存修改' : '添加账号';
        }
    }
}

async function fetchNvidiaModels() {
    const inputBaseUrl = document.getElementById('inputNvidiaBaseUrl') as HTMLInputElement | null;
    const inputApiKey = document.getElementById('inputNvidiaApiKey') as HTMLInputElement | null;
    if (!btnNvidiaFetchModels) return;

    const baseUrl = inputBaseUrl ? inputBaseUrl.value.trim() : '';
    let apiKey = inputApiKey ? inputApiKey.value.trim() : '';

    if (nvidiaModalError) {
        nvidiaModalError.classList.add('hidden');
        nvidiaModalError.textContent = '';
    }

    // 编辑态下 API Key 输入框留空(表示保持不变),此时若直接传空 key 探活会导致上游 401。
    // 改用 account:reveal-key 取回明文再探活;取不到明文则不再发起远端请求,直接红条提示。
    if (!apiKey && nvidiaEditId) {
        try {
            const reveal = await ipcRenderer.invoke('account:reveal-key', nvidiaEditId, 'nvidia');
            if (reveal && reveal.success && typeof reveal.apiKey === 'string' && reveal.apiKey !== '') {
                apiKey = reveal.apiKey;
            } else {
                if (nvidiaModalError) {
                    nvidiaModalError.textContent = (reveal && reveal.error) ? reveal.error : '无法获取明文 API Key,请先填入 API Key 再获取模型';
                    nvidiaModalError.classList.remove('hidden');
                }
                return;
            }
        } catch (err: any) {
            if (nvidiaModalError) {
                nvidiaModalError.textContent = err.message || '无法获取明文 API Key';
                nvidiaModalError.classList.remove('hidden');
            }
            return;
        }
    }

    const origHTML = btnNvidiaFetchModels.innerHTML;
    btnNvidiaFetchModels.disabled = true;
    btnNvidiaFetchModels.innerHTML = `<span class="material-symbols-outlined text-[14px] animate-spin">refresh</span><span>获取中...</span>`;

    try {
        const res = await ipcRenderer.invoke('nvidia:fetch-models', baseUrl, apiKey);
        if (res && res.success && Array.isArray(res.models)) {
            populateNvidiaModelSelects(res.models);
        } else {
            if (nvidiaModalError) {
                nvidiaModalError.textContent = (res && res.error) ? res.error : '获取模型列表失败';
                nvidiaModalError.classList.remove('hidden');
            }
        }
    } catch (err: any) {
        if (nvidiaModalError) {
            nvidiaModalError.textContent = err.message || '网络或接口请求出错';
            nvidiaModalError.classList.remove('hidden');
        }
    } finally {
        if (btnNvidiaFetchModels) {
            btnNvidiaFetchModels.disabled = false;
            btnNvidiaFetchModels.innerHTML = origHTML;
        }
    }
}

function populateNvidiaModelSelects(models: string[]) {
    const targetMap: Array<{ inputId: string; selectId: string }> = [
        { inputId: 'inputNvidiaModelSonnet', selectId: 'selectNvidiaModelSonnet' },
        { inputId: 'inputNvidiaModelOpus', selectId: 'selectNvidiaModelOpus' },
        { inputId: 'inputNvidiaModelHaiku', selectId: 'selectNvidiaModelHaiku' },
        { inputId: 'inputNvidiaModelFable', selectId: 'selectNvidiaModelFable' },
        { inputId: 'inputNvidiaModelDefault', selectId: 'selectNvidiaModelDefault' }
    ];

    targetMap.forEach(item => {
        const inputEl = document.getElementById(item.inputId) as HTMLInputElement | null;
        const selectEl = document.getElementById(item.selectId) as HTMLSelectElement | null;
        if (!inputEl || !selectEl) return;

        selectEl.innerHTML = '<option value="">选择模型...</option>';
        models.forEach(m => {
            const opt = document.createElement('option');
            opt.value = m;
            opt.textContent = m;
            selectEl.appendChild(opt);
        });

        selectEl.classList.remove('hidden');

        selectEl.onchange = () => {
            if (selectEl.value) {
                inputEl.value = selectEl.value;
            }
        };
    });
}
