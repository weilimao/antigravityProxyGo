/**
 * Grok 账号录入/编辑 Modal 控制逻辑：从 accountsController 委托的独立模块。
 *
 * 高内聚：承担 Grok (xAI OpenAI Chat 兼容上游) 号池账号的「添加 / 编辑 / 保存 / 拉取远端模型清单」
 * 弹窗生命周期；自带 7 个 DOM handle、1 个模块级编辑态标记(grokEditId)、6 个函数,由
 * accountsController.initAccountsEvents 委托调用 initGrokAccountModalEvents() 完成句柄赋值与事件绑定。
 * 依赖：ipcRenderer、grokRevealAccountId(来自 revealKeyState,只读写 .value);
 * 无 state / i18n / 跨簇函数依赖(表单字段一律 document.getElementById 局部读取)。
 * IPC 位置参数顺序与 nvidia 对齐:grok:add [baseURL, apiKey, label?, defaultModel?, sonnet?, opus?, haiku?, fable?];
 * grok:update 前置 accountId,后 8 参同 grok:add。apiKey 留空表示保持不变(编辑态)。
 */
import { ipcRenderer } from '../shared/ipc';
import { grokRevealAccountId } from '../shared/revealKeyState';
import i18n from '../shared/i18n';
import state from './dashboardState';

let grokAccountModal: HTMLDivElement | null;
let grokAccountModalContainer: HTMLDivElement | null;
let btnGrokModalSave: HTMLButtonElement | null;
let btnGrokModalCancel: HTMLButtonElement | null;
let btnGrokModalClose: HTMLButtonElement | null;
let grokModalError: HTMLDivElement | null;
let btnGrokFetchModels: HTMLButtonElement | null;
// Grok OAuth 授权双按钮(并列同级,各自独立起流):打开浏览器登录 + 复制链接登录。
let btnGrokOAuthOpenBrowser: HTMLButtonElement | null;
let btnGrokOAuthCopyLink: HTMLButtonElement | null;
let btnGrokOAuthCancel: HTMLButtonElement | null;
let grokOAuthStatus: HTMLDivElement | null;

// OAuth 轮询定时器句柄(模块级,取消/关闭弹窗时清理)。
let grokOAuthTimer: ReturnType<typeof setInterval> | null = null;

// reanchorGrokModalHandles:(HMR 自愈)从活动 DOM 重新锚定 Grok 弹窗全部句柄,并以 onclick 幂等重绑事件。
// 背景:Vue SFC 热替换(HMR)会重建 <GrokAccountModal /> 子树,导致此前一次性 getElementById 拿到的句柄
// 与新 DOM 脱钩、一次性 addEventListener 绑定随旧节点失效 → 表现为「首次能用、改文件触发 HMR 后点不动」。
// 对策:把句柄锚定+事件绑定收敛到本函数,用 onclick(赋值替换式,幂等不累积)替代 addEventListener,
// 在启动初始化与每次 openGrokAccountModal / openEditGrokAccount 前调用 → 对 HMR / 任意重渲染自愈。
function reanchorGrokModalHandles(): void {
    grokAccountModal = document.getElementById('grokAccountModal') as HTMLDivElement | null;
    grokAccountModalContainer = document.getElementById('grokAccountModalContainer') as HTMLDivElement | null;
    btnGrokModalSave = document.getElementById('btnGrokModalSave') as HTMLButtonElement | null;
    btnGrokModalCancel = document.getElementById('btnGrokModalCancel') as HTMLButtonElement | null;
    btnGrokModalClose = document.getElementById('btnGrokModalClose') as HTMLButtonElement | null;
    grokModalError = document.getElementById('grokModalError') as HTMLDivElement | null;
    btnGrokFetchModels = document.getElementById('btnGrokFetchModels') as HTMLButtonElement | null;
    btnGrokOAuthOpenBrowser = document.getElementById('btnGrokOAuthOpenBrowser') as HTMLButtonElement | null;
    btnGrokOAuthCopyLink = document.getElementById('btnGrokOAuthCopyLink') as HTMLButtonElement | null;
    btnGrokOAuthCancel = document.getElementById('btnGrokOAuthCancel') as HTMLButtonElement | null;
    grokOAuthStatus = document.getElementById('grokOAuthStatus') as HTMLDivElement | null;
    // onclick 幂等赋值:每次 reanchor 重绑,替换式不累积,HMR 重建的新节点也能拿到处理器。
    if (btnGrokModalClose) btnGrokModalClose.onclick = closeGrokAccountModal;
    if (btnGrokModalCancel) btnGrokModalCancel.onclick = closeGrokAccountModal;
    if (btnGrokModalSave) btnGrokModalSave.onclick = submitGrokAccount;
    if (btnGrokFetchModels) btnGrokFetchModels.onclick = fetchGrokModels;
    if (btnGrokOAuthOpenBrowser) btnGrokOAuthOpenBrowser.onclick = () => startGrokOAuthLogin(true);
    if (btnGrokOAuthCopyLink) btnGrokOAuthCopyLink.onclick = () => startGrokOAuthLogin(false);
    if (btnGrokOAuthCancel) btnGrokOAuthCancel.onclick = cancelGrokOAuth;
}

// 句柄赋值 + 事件绑定（由 accountsController.initAccountsEvents 委托调用）
export function initGrokAccountModalEvents(): void {
    reanchorGrokModalHandles();
}

// writeGrokModalError:setRevealKeyWarning 单例 handler 被注入时,controller 需要把
// 取明文失败提示写到本模块的 grok 红条;句柄已随簇迁出,提供本出口供 controller 间接写。
export function writeGrokModalError(msg: string): void {
    if (grokModalError) {
        grokModalError.textContent = msg;
        grokModalError.classList.remove('hidden');
    }
}

export function openGrokAccountModal() {
    // HMR 自愈:每次开弹窗前从活动 DOM 重新锚定句柄并 onclick 幂等重绑。
    // Vue SFC 热替换(及任意重渲染)会重建 <GrokAccountModal /> 子树,此前一次性
    // getElementById 拿到的句柄与新 DOM 脱钩、一次性 addEventListener 随旧节点失效 →
    // 「首次能用、HMR/重渲染后点不动」。reanchor 用 onclick 赋值替换式重绑,新节点也能拿到处理器。
    reanchorGrokModalHandles();
    if (!grokAccountModal || !grokAccountModalContainer) return;

    // 添加态:清空编辑态标记
    grokEditId = null;
    // 复位明文查看目标:添加态不允许从后端取回明文 Key。
    grokRevealAccountId.value = null;

    // 标题与保存按钮文案复位为添加态
    const titleEl = grokAccountModal.querySelector('[data-i18n="grokAddModalTitle"]') as HTMLElement | null;
    if (titleEl) {
        const dict = i18n[state.currentLanguage] || i18n.zh;
        titleEl.textContent = dict.grokAddModalTitle || '添加 Grok 号池账号';
    }
    if (btnGrokModalSave) {
        btnGrokModalSave.textContent = '添加账号';
    }

    const inputBaseUrl = document.getElementById('inputGrokBaseUrl') as HTMLInputElement;
    const inputApiKey = document.getElementById('inputGrokApiKey') as HTMLInputElement;
    const inputLabel = document.getElementById('inputGrokLabel') as HTMLInputElement;
    const inputModelSonnet = document.getElementById('inputGrokModelSonnet') as HTMLInputElement;
    const inputModelOpus = document.getElementById('inputGrokModelOpus') as HTMLInputElement;
    const inputModelHaiku = document.getElementById('inputGrokModelHaiku') as HTMLInputElement;
    const inputModelFable = document.getElementById('inputGrokModelFable') as HTMLInputElement;
    const inputModelDefault = document.getElementById('inputGrokModelDefault') as HTMLInputElement;

    if (inputBaseUrl) inputBaseUrl.value = '';
    if (inputApiKey) inputApiKey.value = '';
    if (inputLabel) inputLabel.value = '';
    if (inputModelSonnet) inputModelSonnet.value = '';
    if (inputModelOpus) inputModelOpus.value = '';
    if (inputModelHaiku) inputModelHaiku.value = '';
    if (inputModelFable) inputModelFable.value = '';
    if (inputModelDefault) inputModelDefault.value = '';

    if (grokModalError) {
        grokModalError.classList.add('hidden');
        grokModalError.textContent = '';
    }

    const selectIds = [
        'selectGrokModelSonnet',
        'selectGrokModelOpus',
        'selectGrokModelHaiku',
        'selectGrokModelFable',
        'selectGrokModelDefault'
    ];
    selectIds.forEach(id => {
        const selectEl = document.getElementById(id) as HTMLSelectElement | null;
        if (selectEl) {
            selectEl.classList.add('hidden');
            selectEl.innerHTML = '<option value="">选择模型...</option>';
        }
    });

    // 复位 OAuth 状态区并取消任何未完成的授权轮询。
    cancelRunningGrokOAuth();
    // 添加态:恢复显示授权双按钮(并列,各自独立起流)。手动表单已平铺永久显示,无需展开。
    resetGrokOAuthButtons();

    grokAccountModal.classList.remove('opacity-0', 'pointer-events-none', 'hidden');
    grokAccountModalContainer.classList.remove('scale-95');
    grokAccountModalContainer.classList.add('scale-100');
}

function closeGrokAccountModal() {
    // HMR 自愈:关闭前同样重新锚定句柄。轮询期间 success 路径会调用本函数,若恰有 DOM
    // 重建(热替换/重渲染),下方的 null 守卫会因脱钩句柄直接 bail → 弹窗不隐藏、
    // resetGrokOAuthButtons 不执行 → 真按钮卡在 hidden+disabled+spinner 态(点不动的根因)。
    // 在 null 守卫之前 reanchor,确保后续 hide/reset 作用于实时 DOM。
    reanchorGrokModalHandles();
    if (!grokAccountModal || !grokAccountModalContainer) return;
    grokAccountModalContainer.classList.remove('scale-100');
    grokAccountModalContainer.classList.add('scale-95');
    grokAccountModal.classList.add('opacity-0', 'pointer-events-none');
    // 关闭弹窗时取消未完成的 OAuth 轮询(后端侧经 auth:cancel-login 取消设备码)。
    cancelRunningGrokOAuth();
    // 复位明文查看目标:确保「关闭→重新编辑同一账号」时 PasswordInput 的 watch
    // 能被 null→acc.id 的变化触发(否则 revealed/show 不复位,而输入框已被 openEdit 清空,
    // 眼睛会卡在「已取回」分支跳过 IPC,导致永远取不回明文)。
    grokRevealAccountId.value = null;
}

// 当前处于编辑态的 Grok 账号 id(添加态为 null),供 submitGrokAccount 区分 add/update。
let grokEditId: string | null = null;

// openEditGrokAccount:预填 Grok 账号模态框已有字段并切入编辑态。
// 账号展示名 = acc.email;API Key 因后端不下发明文,编辑框留空,留空表示保持不变。
export function openEditGrokAccount(acc: any) {
    // HMR 自愈:编辑入口同样在开弹窗前重新锚定句柄(理由同 openGrokAccountModal)。
    reanchorGrokModalHandles();
    if (!grokAccountModal || !grokAccountModalContainer) return;

    // 查找最新账号数据快照,防止旧卡片闭包数据过时
    const latestAcc = state.currentAccountsList?.find((a: any) => a.id === acc.id) || acc;

    grokEditId = latestAcc.id;
    // 明文查看:把当前编辑账号 id 注入 PasswordInput(revealProvider="grok" 时据此取明文)。
    grokRevealAccountId.value = latestAcc.id;

    const inputBaseUrl = document.getElementById('inputGrokBaseUrl') as HTMLInputElement;
    const inputApiKey = document.getElementById('inputGrokApiKey') as HTMLInputElement;
    const inputLabel = document.getElementById('inputGrokLabel') as HTMLInputElement;
    const inputModelSonnet = document.getElementById('inputGrokModelSonnet') as HTMLInputElement;
    const inputModelOpus = document.getElementById('inputGrokModelOpus') as HTMLInputElement;
    const inputModelHaiku = document.getElementById('inputGrokModelHaiku') as HTMLInputElement;
    const inputModelFable = document.getElementById('inputGrokModelFable') as HTMLInputElement;
    const inputModelDefault = document.getElementById('inputGrokModelDefault') as HTMLInputElement;

    if (inputBaseUrl) inputBaseUrl.value = latestAcc.baseUrl || '';
    if (inputApiKey) {
        inputApiKey.value = ''; // 不预填明文 Key,留空保持不变
        inputApiKey.placeholder = latestAcc.maskedKey || 'xai-... (留空保持不变)';
    }
    if (inputLabel) inputLabel.value = latestAcc.email || '';
    if (inputModelSonnet) inputModelSonnet.value = latestAcc.modelSonnet || '';
    if (inputModelOpus) inputModelOpus.value = latestAcc.modelOpus || '';
    if (inputModelHaiku) inputModelHaiku.value = latestAcc.modelHaiku || '';
    if (inputModelFable) inputModelFable.value = latestAcc.modelFable || '';
    if (inputModelDefault) inputModelDefault.value = latestAcc.defaultModel || '';

    // 隐藏模型选择下拉(编辑态不自动拉远端模型,保持手填;用户可点"获取模型")。
    const selectIds = [
        'selectGrokModelSonnet',
        'selectGrokModelOpus',
        'selectGrokModelHaiku',
        'selectGrokModelFable',
        'selectGrokModelDefault'
    ];
    selectIds.forEach(id => {
        const selectEl = document.getElementById(id) as HTMLSelectElement | null;
        if (selectEl) {
            selectEl.classList.add('hidden');
            selectEl.innerHTML = '<option value="">选择模型...</option>';
        }
    });

    // 编辑态不适用 OAuth 授权:隐藏授权双按钮/状态区。手动表单已平铺永久显示,无需展开。
    if (btnGrokOAuthOpenBrowser) btnGrokOAuthOpenBrowser.classList.add('hidden');
    if (btnGrokOAuthCopyLink) btnGrokOAuthCopyLink.classList.add('hidden');
    if (grokOAuthStatus) grokOAuthStatus.classList.add('hidden');

    if (grokModalError) {
        grokModalError.classList.add('hidden');
        grokModalError.textContent = '';
    }

    // 标题与保存按钮文案切入编辑态。
    const titleEl = grokAccountModal.querySelector('[data-i18n="grokAddModalTitle"]') as HTMLElement | null;
    if (titleEl) titleEl.textContent = '编辑 Grok 号池账号';
    if (btnGrokModalSave) {
        btnGrokModalSave.textContent = '保存修改';
    }

    grokAccountModal.classList.remove('opacity-0', 'pointer-events-none', 'hidden');
    grokAccountModalContainer.classList.remove('scale-95');
    grokAccountModalContainer.classList.add('scale-100');
}

async function submitGrokAccount() {
    const inputBaseUrl = document.getElementById('inputGrokBaseUrl') as HTMLInputElement;
    const inputApiKey = document.getElementById('inputGrokApiKey') as HTMLInputElement;
    const inputLabel = document.getElementById('inputGrokLabel') as HTMLInputElement;
    const inputModelSonnet = document.getElementById('inputGrokModelSonnet') as HTMLInputElement;
    const inputModelOpus = document.getElementById('inputGrokModelOpus') as HTMLInputElement;
    const inputModelHaiku = document.getElementById('inputGrokModelHaiku') as HTMLInputElement;
    const inputModelFable = document.getElementById('inputGrokModelFable') as HTMLInputElement;
    const inputModelDefault = document.getElementById('inputGrokModelDefault') as HTMLInputElement;

    if (!inputBaseUrl || !inputApiKey) return;

    let baseUrl = inputBaseUrl.value.trim();
    const apiKey = inputApiKey.value.trim();

    if (!baseUrl) {
        baseUrl = 'https://api.x.ai/v1';
    }
    if (!apiKey && !grokEditId) {
        if (grokModalError) {
            grokModalError.textContent = '请输入 API Key';
            grokModalError.classList.remove('hidden');
        }
        return;
    }

    try {
        if (btnGrokModalSave) {
            btnGrokModalSave.disabled = true;
            btnGrokModalSave.textContent = grokEditId ? '正在保存...' : '正在添加...';
        }

        let res;
        if (grokEditId) {
            // 编辑态:定位既有账号;apiKey 留空保持不变。
            // 与 grok:add 位置参数对齐,前置 accountId,后 8 参顺序 [baseURL, apiKey, label, defaultModel, sonnet, opus, haiku, fable]。
            res = await ipcRenderer.invoke('grok:update',
                grokEditId,
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
            // 添加态:8 位置参数 [baseURL, apiKey, label, defaultModel, sonnet, opus, haiku, fable]。
            res = await ipcRenderer.invoke('grok:add',
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
            closeGrokAccountModal();
            ipcRenderer.send('accounts:get');
        } else {
            if (grokModalError) {
                grokModalError.textContent = res?.error || (grokEditId ? '保存失败' : '添加失败');
                grokModalError.classList.remove('hidden');
            }
        }
    } catch (err: any) {
        if (grokModalError) {
            grokModalError.textContent = err.message || '系统错误';
            grokModalError.classList.remove('hidden');
        }
    } finally {
        if (btnGrokModalSave) {
            btnGrokModalSave.disabled = false;
            btnGrokModalSave.textContent = grokEditId ? '保存修改' : '添加账号';
        }
    }
}

async function fetchGrokModels() {
    const inputBaseUrl = document.getElementById('inputGrokBaseUrl') as HTMLInputElement | null;
    const inputApiKey = document.getElementById('inputGrokApiKey') as HTMLInputElement | null;
    if (!btnGrokFetchModels) return;

    const baseUrl = inputBaseUrl ? inputBaseUrl.value.trim() : '';
    let apiKey = inputApiKey ? inputApiKey.value.trim() : '';

    if (grokModalError) {
        grokModalError.classList.add('hidden');
        grokModalError.textContent = '';
    }

    // 编辑态下 API Key 输入框留空(表示保持不变),此时若直接传空 key 探活会导致上游 401
    // ("Invalid or expired credentials")。改用 account:reveal-key 取回明文再探活;
    // 取不到明文则不再发起远端请求,直接红条提示。
    if (!apiKey && grokEditId) {
        try {
            const reveal = await ipcRenderer.invoke('account:reveal-key', grokEditId, 'grok');
            if (reveal && reveal.success && typeof reveal.apiKey === 'string' && reveal.apiKey !== '') {
                apiKey = reveal.apiKey;
            } else {
                if (grokModalError) {
                    grokModalError.textContent = (reveal && reveal.error) ? reveal.error : '无法获取明文 API Key,请先填入 API Key 再获取模型';
                    grokModalError.classList.remove('hidden');
                }
                return;
            }
        } catch (err: any) {
            if (grokModalError) {
                grokModalError.textContent = err.message || '无法获取明文 API Key';
                grokModalError.classList.remove('hidden');
            }
            return;
        }
    }

    const origHTML = btnGrokFetchModels.innerHTML;
    btnGrokFetchModels.disabled = true;
    btnGrokFetchModels.innerHTML = `<span class="material-symbols-outlined text-[14px] animate-spin">refresh</span><span>获取中...</span>`;

    try {
        const res = await ipcRenderer.invoke('grok:fetch-models', baseUrl, apiKey);
        if (res && res.success && Array.isArray(res.models)) {
            populateGrokModelSelects(res.models);
        } else {
            if (grokModalError) {
                grokModalError.textContent = (res && res.error) ? res.error : '获取模型列表失败';
                grokModalError.classList.remove('hidden');
            }
        }
    } catch (err: any) {
        if (grokModalError) {
            grokModalError.textContent = err.message || '网络或接口请求出错';
            grokModalError.classList.remove('hidden');
        }
    } finally {
        if (btnGrokFetchModels) {
            btnGrokFetchModels.disabled = false;
            btnGrokFetchModels.innerHTML = origHTML;
        }
    }
}

function populateGrokModelSelects(models: string[]) {
    const targetMap: Array<{ inputId: string; selectId: string }> = [
        { inputId: 'inputGrokModelSonnet', selectId: 'selectGrokModelSonnet' },
        { inputId: 'inputGrokModelOpus', selectId: 'selectGrokModelOpus' },
        { inputId: 'inputGrokModelHaiku', selectId: 'selectGrokModelHaiku' },
        { inputId: 'inputGrokModelFable', selectId: 'selectGrokModelFable' },
        { inputId: 'inputGrokModelDefault', selectId: 'selectGrokModelDefault' }
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

// ============ Grok OAuth 授权登录(设备码流·并列双按钮) ============

// startGrokOAuthLogin 发起 xAI 设备码授权:调用后端起流拿到 verification_url,
// 随后按 openBrowser 分叉执行,再每 2s 轮询 auth:xai-status 直到成功/失败/取消。
//
// openBrowser=true:[打开浏览器登录] 起流后调默认浏览器打开授权页(verification_uri_complete 自动带 user_code)。
// openBrowser=false:[复制链接登录] 起流后仅把链接复制到剪贴板(不开默认浏览器,配合无痕/目标账号浏览器粘贴,避免串号)。
// 两者都须先触发后端 auth:xai-login 起流一次,无法凭空复制已有链接。
async function startGrokOAuthLogin(openBrowser: boolean) {
    // HMR 自愈:起流前从活动 DOM 重新锚定句柄并重绑 onclick。用户点按钮触达本函数时,
    // 若 DOM 在按钮渲染后被重渲染过,模块级 btnGrokOAuthOpenBrowser/btnGrokOAuthCopyLink
    // 可能指向脱钩的旧节点 → 下行 `!btn...` 守卫静默 bail 表现为「点了没反应」。
    // reanchor 在守卫之前执行,确保 require-guards/disabled/innerHTML 全部作用于实时节点。
    reanchorGrokModalHandles();
    if (!btnGrokOAuthOpenBrowser || !btnGrokOAuthCopyLink) return;

    // 隐藏错误条,双按钮互斥禁用防连点(点一个另一个也禁用,避免并发起流)。
    if (grokModalError) {
        grokModalError.classList.add('hidden');
        grokModalError.textContent = '';
    }
    const spinnerHTML = `<span class="material-symbols-outlined text-[18px] animate-spin">autorenew</span><span>正在启动授权…</span>`;
    btnGrokOAuthOpenBrowser.disabled = true;
    btnGrokOAuthCopyLink.disabled = true;
    btnGrokOAuthOpenBrowser.innerHTML = spinnerHTML;
    btnGrokOAuthCopyLink.innerHTML = spinnerHTML;

    let state = '';
    try {
        const res = await ipcRenderer.invoke('auth:xai-login');
        if (!res || !res.success) {
            throw new Error((res && res.error) ? res.error : '启动授权失败');
        }
        state = res.state || '';
        if (!state) {
            throw new Error('授权会话无效');
        }

        // 展示状态区:授权码 + 只读授权链接(剪贴板 API 失败时选中供手动 Ctrl+C 兜底)。
        const codeEl = document.getElementById('grokOAuthUserCodeValue');
        if (codeEl) codeEl.textContent = res.user_code || '';
        const linkInput = document.getElementById('grokOAuthLinkInput') as HTMLInputElement | null;
        if (linkInput) linkInput.value = res.verification_url || '';
        if (grokOAuthStatus) {
            grokOAuthStatus.classList.remove('hidden');
            if (grokOAuthStatus.classList.contains('flex-col')) {
                grokOAuthStatus.classList.add('flex');
            }
        }

        if (openBrowser) {
            // [打开浏览器登录] 起流成功后调起默认浏览器打开授权页(verification_uri_complete 自动带 user_code)。
            // 走 settings:open-folder,后端对 http URL 分支调 OpenPath → BrowserOpenURL。
            if (res.verification_url) {
                ipcRenderer.send('settings:open-folder', res.verification_url);
            }
        } else {
            // [复制链接登录] 起流成功后仅把链接复制到剪贴板,不开默认浏览器(配合无痕/目标账号浏览器粘贴,避免串号)。
            // 剪贴板 API 失败时选中只读输入框供手动 Ctrl+C 兜底。
            const link = (res.verification_url || '').trim();
            if (link && linkInput) {
                navigator.clipboard.writeText(link).catch(() => {
                    linkInput.select();
                });
            }
        }

        // 起流成功后隐藏双按钮,仅保留状态卡(含取消授权) —— 避免重复触发入口。
        btnGrokOAuthOpenBrowser.classList.add('hidden');
        btnGrokOAuthCopyLink.classList.add('hidden');

        // 轮询授权状态。
        setGrokOAuthTimer(state);
    } catch (err: any) {
        resetGrokOAuthButtons();
        if (grokModalError) {
            grokModalError.textContent = err.message || '启动授权失败';
            grokModalError.classList.remove('hidden');
        }
    }
}

// setGrokOAuthTimer 每 2s 轮询一次授权状态,直到成功/报错/超时。
function setGrokOAuthTimer(state: string) {
    const MAX_WAIT_MS = 30 * 60 * 1000; // 与后端 MaxPollDuration 对齐
    const startedAt = Date.now();
    clearGrokOAuthTimer();
    grokOAuthTimer = setInterval(async () => {
        if (Date.now() - startedAt > MAX_WAIT_MS) {
            // 超时:取消后端会话并提示。
            clearGrokOAuthTimer();
            void ipcRenderer.invoke('auth:cancel-login');
            if (grokOAuthStatus) grokOAuthStatus.classList.add('hidden');
            if (grokModalError) {
                grokModalError.textContent = '授权已超时，请重新发起';
                grokModalError.classList.remove('hidden');
            }
            resetGrokOAuthButtons();
            return;
        }
        try {
            const res = await ipcRenderer.invoke('auth:xai-status', state);
            if (!res || res.success === false) {
                // 会话失效(error)。
                clearGrokOAuthTimer();
                if (grokOAuthStatus) grokOAuthStatus.classList.add('hidden');
                if (grokModalError) {
                    grokModalError.textContent = (res && res.error) ? res.error : '授权失败';
                    grokModalError.classList.remove('hidden');
                }
                resetGrokOAuthButtons();
                return;
            }
            if (res.status === 'success') {
                clearGrokOAuthTimer();
                if (grokOAuthStatus) grokOAuthStatus.classList.add('hidden');
                closeGrokAccountModal();
                ipcRenderer.send('accounts:get');
            } else if (res.status === 'error') {
                clearGrokOAuthTimer();
                if (grokOAuthStatus) grokOAuthStatus.classList.add('hidden');
                if (grokModalError) {
                    grokModalError.textContent = res.error || '授权失败';
                    grokModalError.classList.remove('hidden');
                }
                resetGrokOAuthButtons();
            }
            // status === pending 继续轮询。
        } catch (err: any) {
            clearGrokOAuthTimer();
            if (grokOAuthStatus) grokOAuthStatus.classList.add('hidden');
            if (grokModalError) {
                grokModalError.textContent = err.message || '网络错误';
                grokModalError.classList.remove('hidden');
            }
            resetGrokOAuthButtons();
        }
    }, 2000);
}

// cancelGrokOAuth 取消当前授权(用户点「取消授权」或需中止时)。
function cancelGrokOAuth() {
    clearGrokOAuthTimer();
    void ipcRenderer.invoke('auth:cancel-login');
    if (grokOAuthStatus) grokOAuthStatus.classList.add('hidden');
    resetGrokOAuthButtons();
}

// cancelRunningGrokOAuth 关闭/重开弹窗时中止未完成授权(不触发后端错误条)。
function cancelRunningGrokOAuth() {
    clearGrokOAuthTimer();
    if (grokOAuthStatus) {
        grokOAuthStatus.classList.add('hidden');
    }
    resetGrokOAuthButtons();
}

// clearGrokOAuthTimer 清理轮询定时器。
function clearGrokOAuthTimer() {
    if (grokOAuthTimer !== null) {
        clearInterval(grokOAuthTimer);
        grokOAuthTimer = null;
    }
}

// resetGrokOAuthButtons 恢复授权双按钮为初始态并取消互斥禁用。
// 起流成功时双按钮已隐藏;失败/取消/超时/会话失效等退出路径调用本函数恢复双按钮供重试。
function resetGrokOAuthButtons() {
    if (btnGrokOAuthOpenBrowser) {
        btnGrokOAuthOpenBrowser.classList.remove('hidden');
        btnGrokOAuthOpenBrowser.disabled = false;
        btnGrokOAuthOpenBrowser.innerHTML = `<span class="material-symbols-outlined text-[18px]">open_in_new</span><span>${getGrokOAuthOpenBrowserText()}</span>`;
    }
    if (btnGrokOAuthCopyLink) {
        btnGrokOAuthCopyLink.classList.remove('hidden');
        btnGrokOAuthCopyLink.disabled = false;
        btnGrokOAuthCopyLink.innerHTML = `<span class="material-symbols-outlined text-[18px]">content_copy</span><span>${getGrokOAuthCopyLoginText()}</span>`;
    }
}

// getGrokOAuthOpenBrowserText/getGrokOAuthCopyLoginText:从 i18n 取双按钮文案,无 i18n 时回退中文默认。
function getGrokOAuthOpenBrowserText(): string {
    const dict = i18n[state.currentLanguage] || i18n.zh || {};
    return dict.grokOAuthOpenBrowser || '打开浏览器登录';
}
function getGrokOAuthCopyLoginText(): string {
    const dict = i18n[state.currentLanguage] || i18n.zh || {};
    return dict.grokOAuthCopyLogin || '复制链接登录';
}
