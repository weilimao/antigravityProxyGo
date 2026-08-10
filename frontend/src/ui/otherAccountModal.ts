/**
 * Other 账号录入/编辑 Modal 控制逻辑：从 accountsController.ts 抽离的独立模块。
 *
 * 高内聚：承担 Other 号池账号的「添加 / 编辑 / 保存 / 拉取远端模型清单」弹窗生命周期；
 * 另含「选择已有组」下拉的填充(populateOtherGroupSelect)与选中自动填充表单(onOtherGroupSelectChange)。
 * 自带 8 个 DOM handle(groupSelectOther + 7 otherModal 句柄)、1 个模块级编辑态标记(otherEditId)、
 * 8 个函数,由 accountsController.initAccountsEvents 委托 initOtherAccountModalEvents() 完成句柄赋值与事件绑定。
 * 依赖：ipcRenderer、state(lastBackendData.otherGroups / currentAccountsList)、otherRevealAccountId(revealKeyState)。
 */
import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';
import { otherRevealAccountId } from '../shared/revealKeyState';
import i18n from '../shared/i18n';

let groupSelectOther: HTMLSelectElement | null;
let otherAccountModal: HTMLDivElement | null;
let otherAccountModalContainer: HTMLDivElement | null;
let btnOtherModalSave: HTMLButtonElement | null;
let btnOtherModalCancel: HTMLButtonElement | null;
let btnOtherModalClose: HTMLButtonElement | null;
let btnOtherFetchModels: HTMLButtonElement | null;
let otherModalError: HTMLDivElement | null;

// populateOtherGroupSelect:打开 Other 账号 Modal 时,若号池已有组则填充右侧「选择已有组」下拉。
// 0 组时隐藏下拉。每次打开不预选任何组(避免误覆盖用户手动输入)。
function populateOtherGroupSelect() {
    if (!groupSelectOther) {
        groupSelectOther = document.getElementById('selectOtherGroup') as HTMLSelectElement | null;
    }
    if (!groupSelectOther) return;
    const groups = (state.lastBackendData && Array.isArray(state.lastBackendData.otherGroups))
        ? state.lastBackendData.otherGroups
        : [];
    if (groups.length === 0) {
        groupSelectOther.classList.add('hidden');
        groupSelectOther.innerHTML = '<option value="" data-i18n="otherGroupSelectPlaceholder">选择已有组...</option>';
        groupSelectOther.value = '';
        return;
    }
    groupSelectOther.innerHTML = '<option value="" data-i18n="otherGroupSelectPlaceholder">选择已有组...</option>';
    for (const g of groups) {
        const gid = String(g.groupId || g.groupID || g.id || '');
        if (!gid) continue;
        const gname = String(g.groupName || g.groupId || '');
        const count = Number(g.accountCount) || 0;
        const opt = document.createElement('option');
        opt.value = gid;
        opt.textContent = `${gname} (${count})`;
        groupSelectOther.appendChild(opt);
    }
    groupSelectOther.value = '';
    groupSelectOther.classList.remove('hidden');
}

// onOtherGroupSelectChange:选中已有组时自动填充 groupId/groupName/baseUrl/formats/默认模型,
// 仅留 API Key 与展示名给用户填写。切回占位项("")不清空(避免误触抹掉输入)。
function onOtherGroupSelectChange() {
    if (!groupSelectOther || !otherModalError) return;
    const gid = groupSelectOther.value;
    otherModalError.classList.add('hidden');
    otherModalError.textContent = '';
    if (!gid) return;

    const groups = (state.lastBackendData && Array.isArray(state.lastBackendData.otherGroups))
        ? state.lastBackendData.otherGroups
        : [];
    const g = groups.find((x: any) => String(x.groupId || x.groupID || x.id || '') === gid);
    if (!g) return;

    const inputGroupId = document.getElementById('inputOtherGroupId') as HTMLInputElement | null;
    const inputGroupName = document.getElementById('inputOtherGroupName') as HTMLInputElement | null;
    const inputBaseUrl = document.getElementById('inputOtherBaseUrl') as HTMLInputElement | null;
    const inputModelDefault = document.getElementById('inputOtherModelDefault') as HTMLInputElement | null;
    const chkFmtOpenai = document.getElementById('chkOtherFmtOpenai') as HTMLInputElement | null;
    const chkFmtAnthropic = document.getElementById('chkOtherFmtAnthropic') as HTMLInputElement | null;

    if (inputGroupId) inputGroupId.value = gid;
    if (inputGroupName) inputGroupName.value = String(g.groupName || g.groupId || '');
    if (inputBaseUrl) inputBaseUrl.value = String(g.baseUrl || '');

    // 据组 Formats 勾选协议 checkbox(openai 含则勾,否则取消;anthropic 同理)。
    const fmts: string[] = Array.isArray(g.formats) ? g.formats.map((f: any) => String(f).toLowerCase()) : [];
    if (chkFmtOpenai) chkFmtOpenai.checked = fmts.includes('openai');
    if (chkFmtAnthropic) chkFmtAnthropic.checked = fmts.includes('anthropic');

    // 默认模型:从该组首个带 defaultModel 的账号取;取不到留空(不强制)。
    let defModel = '';
    if (state.currentAccountsList) {
        const hit = state.currentAccountsList.find(a =>
            a && a.groupId === gid && a.defaultModel
        );
        if (hit) defModel = String(hit.defaultModel);
    }
    if (inputModelDefault) inputModelDefault.value = defModel;
    // 不填 API Key / 展示名(用户自行填写);groupId 保持可编辑(不设 readonly)。
}

// 句柄赋值 + 事件绑定（由 accountsController.initAccountsEvents 委托调用）
export function initOtherAccountModalEvents(): void {
    otherAccountModal = document.getElementById('otherAccountModal') as HTMLDivElement | null;
    otherAccountModalContainer = document.getElementById('otherAccountModalContainer') as HTMLDivElement | null;
    btnOtherModalSave = document.getElementById('btnOtherModalSave') as HTMLButtonElement | null;
    btnOtherModalCancel = document.getElementById('btnOtherModalCancel') as HTMLButtonElement | null;
    btnOtherModalClose = document.getElementById('btnOtherModalClose') as HTMLButtonElement | null;
    btnOtherFetchModels = document.getElementById('btnOtherFetchModels') as HTMLButtonElement | null;
    otherModalError = document.getElementById('otherModalError') as HTMLDivElement | null;
    // Other 账号模态:关闭/取消/保存/获取模型
    if (btnOtherModalClose) btnOtherModalClose.addEventListener('click', closeOtherAccountModal);
    if (btnOtherModalCancel) btnOtherModalCancel.addEventListener('click', closeOtherAccountModal);
    if (btnOtherModalSave) btnOtherModalSave.addEventListener('click', submitOtherAccount);
    if (btnOtherFetchModels) btnOtherFetchModels.addEventListener('click', fetchOtherModels);
    if (groupSelectOther) groupSelectOther.addEventListener('change', onOtherGroupSelectChange);
}

// writeOtherModalError:setRevealKeyWarning 单例 handler 被注入时,controller 需要把取明文失败提示
// 写到本模块的 otherModal 红条;句柄已随簇迁出,提供本出口供 controller 间接写(与 NVIDIA 簇对称)。
export function writeOtherModalError(msg: string): void {
    if (otherModalError) {
        otherModalError.textContent = msg;
        otherModalError.classList.remove('hidden');
    }
}

// openOtherAccountModal:打开 Other 账号录入模态并清空表单。
export function openOtherAccountModal() {
    if (!otherAccountModal || !otherAccountModalContainer) return;

    // 添加态:清空编辑态标记。
    otherEditId = null;
    // 复位明文查看目标:添加态不允许从后端取回明文 Key。
    otherRevealAccountId.value = null;

    // 标题与保存按钮文案复位为添加态
    const titleEl = otherAccountModal.querySelector('[data-i18n="otherAddModalTitle"]') as HTMLElement | null;
    if (titleEl) {
        const dict = i18n[state.currentLanguage] || i18n.zh;
        titleEl.textContent = dict.otherAddModalTitle || '添加 Other 自定义号池账号';
    }
    if (btnOtherModalSave) btnOtherModalSave.textContent = '添加账号';

    const inputGroupId = document.getElementById('inputOtherGroupId') as HTMLInputElement | null;
    const inputGroupName = document.getElementById('inputOtherGroupName') as HTMLInputElement | null;
    const inputBaseUrl = document.getElementById('inputOtherBaseUrl') as HTMLInputElement | null;
    const inputApiKey = document.getElementById('inputOtherApiKey') as HTMLInputElement | null;
    const inputLabel = document.getElementById('inputOtherLabel') as HTMLInputElement | null;
    const inputModelDefault = document.getElementById('inputOtherModelDefault') as HTMLInputElement | null;
    const selectModelDefault = document.getElementById('selectOtherModelDefault') as HTMLSelectElement | null;
    const chkFmtOpenai = document.getElementById('chkOtherFmtOpenai') as HTMLInputElement | null;
    const chkFmtAnthropic = document.getElementById('chkOtherFmtAnthropic') as HTMLInputElement | null;

    if (inputGroupId) inputGroupId.value = '';
    if (inputGroupName) inputGroupName.value = '';
    if (inputBaseUrl) inputBaseUrl.value = '';
    if (inputApiKey) inputApiKey.value = '';
    if (inputLabel) inputLabel.value = '';
    if (inputModelDefault) inputModelDefault.value = '';
    if (selectModelDefault) {
        selectModelDefault.classList.add('hidden');
        selectModelDefault.innerHTML = '<option value="">选择模型...</option>';
    }
    // 默认勾选 OpenAI 格式,与新建态初始 checked 一致。
    if (chkFmtOpenai) chkFmtOpenai.checked = true;
    if (chkFmtAnthropic) chkFmtAnthropic.checked = false;

    if (otherModalError) {
        otherModalError.classList.add('hidden');
        otherModalError.textContent = '';
    }

    // 号池已有组则填充右侧「选择已有组」下拉(0 组时隐藏)。
    populateOtherGroupSelect();

    otherAccountModal.classList.remove('opacity-0', 'pointer-events-none', 'hidden');
    otherAccountModalContainer.classList.remove('scale-95');
    otherAccountModalContainer.classList.add('scale-100');
}

function closeOtherAccountModal() {
    if (!otherAccountModal || !otherAccountModalContainer) return;
    otherAccountModalContainer.classList.remove('scale-100');
    otherAccountModalContainer.classList.add('scale-95');
    otherAccountModal.classList.add('opacity-0', 'pointer-events-none');
    // 复位明文查看目标:同 closeNvidiaAccountModal,保证「关闭→重新编辑同一账号」眼睛可再次取明文。
    otherRevealAccountId.value = null;
}

// 当前处于编辑态的 Other 账号 id(添加态为 null),供 submitOtherAccount 区分 add/update。
let otherEditId: string | null = null;

// openEditOtherAccount:预填 Other 账号模态框已有字段并切入编辑态。
// 展示名 = acc.email;API Key 因后端不下发明文,编辑框留空,留空表示保持不变。
export function openEditOtherAccount(acc: any) {
    if (!otherAccountModal || !otherAccountModalContainer) return;

    // 查找最新账号数据快照,防止旧卡片闭包数据过时
    const latestAcc = state.currentAccountsList?.find((a: any) => a.id === acc.id) || acc;

    otherEditId = latestAcc.id;
    // 明文查看:把当前编辑账号 id 注入 PasswordInput(revealProvider="other" 时据此取明文)。
    otherRevealAccountId.value = latestAcc.id;

    const inputGroupId = document.getElementById('inputOtherGroupId') as HTMLInputElement | null;
    const inputGroupName = document.getElementById('inputOtherGroupName') as HTMLInputElement | null;
    const inputBaseUrl = document.getElementById('inputOtherBaseUrl') as HTMLInputElement | null;
    const inputApiKey = document.getElementById('inputOtherApiKey') as HTMLInputElement | null;
    const inputLabel = document.getElementById('inputOtherLabel') as HTMLInputElement | null;
    const inputModelDefault = document.getElementById('inputOtherModelDefault') as HTMLInputElement | null;
    const selectModelDefault = document.getElementById('selectOtherModelDefault') as HTMLSelectElement | null;
    const chkFmtOpenai = document.getElementById('chkOtherFmtOpenai') as HTMLInputElement | null;
    const chkFmtAnthropic = document.getElementById('chkOtherFmtAnthropic') as HTMLInputElement | null;

    if (inputGroupId) inputGroupId.value = latestAcc.groupId || '';
    if (inputGroupName) inputGroupName.value = latestAcc.groupName || '';
    if (inputBaseUrl) inputBaseUrl.value = latestAcc.baseUrl || '';
    // 编辑态不预填明文 Key:留空表示保持不变;用脱敏掩码当 placeholder 提示已配置 Key。
    if (inputApiKey) {
        inputApiKey.value = '';
        inputApiKey.placeholder = latestAcc.maskedKey || 'sk-... (留空保持不变)';
    }
    if (inputLabel) inputLabel.value = latestAcc.email || '';
    if (inputModelDefault) inputModelDefault.value = latestAcc.defaultModel || '';
    if (selectModelDefault) {
        selectModelDefault.classList.add('hidden');
        selectModelDefault.innerHTML = '<option value="">选择模型...</option>';
    }

    // 据账号 Formats 勾选协议 checkbox(openai 含则勾,否则取消;anthropic 同理)。
    const fmts: string[] = Array.isArray(latestAcc.formats) ? latestAcc.formats.map((f: any) => String(f).toLowerCase()) : [];
    if (chkFmtOpenai) chkFmtOpenai.checked = fmts.includes('openai');
    if (chkFmtAnthropic) chkFmtAnthropic.checked = fmts.includes('anthropic');
    // 若两者都未开(异常数据或无 formats),默认勾 OpenAI 兜底。
    if (!chkFmtOpenai?.checked && !chkFmtAnthropic?.checked && chkFmtOpenai) chkFmtOpenai.checked = true;

    if (otherModalError) {
        otherModalError.classList.add('hidden');
        otherModalError.textContent = '';
    }

    // 编辑态不展示「选择已有组」下拉(避免误改已选账号所属组身份)。
    if (groupSelectOther) groupSelectOther.classList.add('hidden');

    // 标题与保存按钮文案切入编辑态。
    const titleEl = otherAccountModal.querySelector('[data-i18n="otherAddModalTitle"]') as HTMLElement | null;
    if (titleEl) titleEl.textContent = '编辑 Other 号池账号';
    if (btnOtherModalSave) btnOtherModalSave.textContent = '保存修改';

    otherAccountModal.classList.remove('opacity-0', 'pointer-events-none', 'hidden');
    otherAccountModalContainer.classList.remove('scale-95');
    otherAccountModalContainer.classList.add('scale-100');
}

// submitOtherAccount:校验后调 other:add(JSON 对象入参),成功关闭模态并同步账号列表。
async function submitOtherAccount() {
    const inputGroupId = document.getElementById('inputOtherGroupId') as HTMLInputElement | null;
    const inputGroupName = document.getElementById('inputOtherGroupName') as HTMLInputElement | null;
    const inputBaseUrl = document.getElementById('inputOtherBaseUrl') as HTMLInputElement | null;
    const inputApiKey = document.getElementById('inputOtherApiKey') as HTMLInputElement | null;
    const inputLabel = document.getElementById('inputOtherLabel') as HTMLInputElement | null;
    const inputModelDefault = document.getElementById('inputOtherModelDefault') as HTMLInputElement | null;
    const chkFmtOpenai = document.getElementById('chkOtherFmtOpenai') as HTMLInputElement | null;
    const chkFmtAnthropic = document.getElementById('chkOtherFmtAnthropic') as HTMLInputElement | null;

    if (!inputGroupId || !inputBaseUrl || !inputApiKey) return;

    const groupId = inputGroupId.value.trim();
    const baseUrl = inputBaseUrl.value.trim();
    const apiKey = inputApiKey.value.trim();

    if (!groupId) {
        if (otherModalError) {
            otherModalError.textContent = '请填写组 ID';
            otherModalError.classList.remove('hidden');
        }
        return;
    }
    if (!apiKey && !otherEditId) {
        if (otherModalError) {
            otherModalError.textContent = '请输入 API Key';
            otherModalError.classList.remove('hidden');
        }
        return;
    }

    const formats: string[] = [];
    if (chkFmtOpenai && chkFmtOpenai.checked) formats.push('openai');
    if (chkFmtAnthropic && chkFmtAnthropic.checked) formats.push('anthropic');
    if (formats.length === 0) {
        if (otherModalError) {
            otherModalError.textContent = '至少勾选一种协议格式';
            otherModalError.classList.remove('hidden');
        }
        return;
    }

    try {
        if (btnOtherModalSave) {
            btnOtherModalSave.disabled = true;
            btnOtherModalSave.textContent = otherEditId ? '正在保存...' : '正在添加...';
        }

        let res;
        const payloadObj: any = {
            groupId,
            groupName: inputGroupName?.value.trim() || '',
            baseUrl,
            apiKey,
            formats,
            label: inputLabel?.value.trim() || '',
            defaultModel: inputModelDefault?.value.trim() || ''
        };
        if (otherEditId) {
            // 编辑态:以 accountId 定位既有账号;apiKey 留空保持不变。
            payloadObj.accountId = otherEditId;
            res = await ipcRenderer.invoke('other:update', JSON.stringify(payloadObj));
        } else {
            // 采用单对象 JSON 入参形态(后端 parseOtherInputFromArgs 优先按 JSON 解析)。
            res = await ipcRenderer.invoke('other:add', JSON.stringify(payloadObj));
        }

        if (res && res.success) {
            closeOtherAccountModal();
            // accounts-res 由后端 emitAccountsRes 主动广播,前端 global listener 会自动 renderAccounts,
            // 此处不再手动 invoke('accounts:get') 以免与广播竞态重复刷新。
        } else {
            if (otherModalError) {
                otherModalError.textContent = res?.error || (otherEditId ? '保存失败' : '添加失败');
                otherModalError.classList.remove('hidden');
            }
        }
    } catch (err: any) {
        if (otherModalError) {
            otherModalError.textContent = err.message || '系统错误';
            otherModalError.classList.remove('hidden');
        }
    } finally {
        if (btnOtherModalSave) {
            btnOtherModalSave.disabled = false;
            btnOtherModalSave.textContent = otherEditId ? '保存修改' : '添加账号';
        }
    }
}

// fetchOtherModels:调其他组拉模型(透传当前录入框内的 groupId/baseUrl/apiKey 以支持未入库预拉),
// 填入默认模型下拉供选择。
async function fetchOtherModels() {
    const inputGroupId = document.getElementById('inputOtherGroupId') as HTMLInputElement | null;
    const inputBaseUrl = document.getElementById('inputOtherBaseUrl') as HTMLInputElement | null;
    const inputApiKey = document.getElementById('inputOtherApiKey') as HTMLInputElement | null;
    const selectModelDefault = document.getElementById('selectOtherModelDefault') as HTMLSelectElement | null;
    if (!btnOtherFetchModels) return;

    const groupId = inputGroupId ? inputGroupId.value.trim() : '';
    const baseUrl = inputBaseUrl ? inputBaseUrl.value.trim() : '';
    const apiKey = inputApiKey ? inputApiKey.value.trim() : '';

    if (otherModalError) {
        otherModalError.classList.add('hidden');
        otherModalError.textContent = '';
    }

    if (!groupId && !baseUrl) {
        if (otherModalError) {
            otherModalError.textContent = '请先填写组 ID 或 Base URL';
            otherModalError.classList.remove('hidden');
        }
        return;
    }

    const origHTML = btnOtherFetchModels.innerHTML;
    btnOtherFetchModels.disabled = true;
    btnOtherFetchModels.innerHTML = `<span class="material-symbols-outlined text-[14px] animate-spin">refresh</span><span>获取中...</span>`;

    try {
        // 透传 baseUrl/apiKey,便于未入库时按当前表单预拉;后端会优先用透传值,否则查号池该组首个账号。
        const res = await ipcRenderer.invoke('other:fetch-models', JSON.stringify({ groupId, baseUrl, apiKey }));
        if (res && res.success && Array.isArray(res.models)) {
            if (selectModelDefault) {
                selectModelDefault.innerHTML = '<option value="">选择模型...</option>';
                res.models.forEach((m: string) => {
                    const opt = document.createElement('option');
                    opt.value = m;
                    opt.textContent = m;
                    selectModelDefault.appendChild(opt);
                });
                selectModelDefault.classList.remove('hidden');
                selectModelDefault.onchange = () => {
                    if (selectModelDefault.value) {
                        const inputModelDefault = document.getElementById('inputOtherModelDefault') as HTMLInputElement | null;
                        if (inputModelDefault) inputModelDefault.value = selectModelDefault.value;
                    }
                };
            }
        } else {
            // Anthropic-only 上游无 /v1/models 端点会返回 allowManualInput,提示用户手填。
            if (res && res.allowManualInput) {
                if (otherModalError) {
                    otherModalError.textContent = res?.error || '上游暂不支持模型列表,请手动填写模型名';
                    otherModalError.classList.remove('hidden');
                }
                if (selectModelDefault) selectModelDefault.classList.add('hidden');
            } else if (otherModalError) {
                otherModalError.textContent = res?.error || '获取模型列表失败';
                otherModalError.classList.remove('hidden');
            }
        }
    } catch (err: any) {
        if (otherModalError) {
            otherModalError.textContent = err.message || '网络或接口请求出错';
            otherModalError.classList.remove('hidden');
        }
    } finally {
        if (btnOtherFetchModels) {
            btnOtherFetchModels.disabled = false;
            btnOtherFetchModels.innerHTML = origHTML;
        }
    }
}
