/**
 * Other 号池「组级自定义冷却策略」Modal 控制逻辑:从 otherGroupTabs.ts 行内控件迁移而来。
 *
 * 高内聚:承担冷却规则弹窗的生命周期(打开回填 → 校验 → 保存下发)、「获取模型」按钮拉取
 * 上游模型清单填充多选候选,以及工具栏入口按钮(btnOtherCooldownConfig)的状态渲染(启用中高亮)。
 * 状态码解析(parseOtherCooldownCodes)随迁至此;模型过滤为 ModelSearchSelect 多选
 * (v-model:modelIds),chips 即规则白名单数组,自定义通配项经 allow-custom 加入。
 * 自带 6 个 DOM handle + 4 个导出 ref(enabled/组显示名/已选模型/候选模型,供 Vue 组件绑定)
 * + 6 个函数,由 accountsController.initAccountsEvents 委托 initOtherCooldownModalEvents()。
 * 依赖:ipcRenderer、state(lastBackendData.otherGroups / otherGroupFilter)、i18n。
 */
import { ref } from 'vue';
import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';
import i18n from '../shared/i18n';

// 弹窗共享状态:启用勾选态(v-model)、组显示名(只读展示)、模型过滤多选数组与候选模型列表,
// 导出给 OtherCooldownConfigModal.vue。候选列表由「获取模型」按钮经 other:fetch-models 填充。
export const otherCooldownEnabled = ref(false);
export const otherCooldownTargetGroupDisplay = ref('');
export const otherCooldownSelectedModels = ref<string[]>([]);
export const otherCooldownModelOptions = ref<string[]>([]);

// 模型候选列表按组缓存:同组重复打开弹窗不丢已拉取列表,换组才清空重拉。
let cooldownModelsCachedGroupId = '';

let btnOtherCooldownConfig: HTMLButtonElement | null;
let btnOtherCooldownFetchModels: HTMLButtonElement | null;
let otherCooldownModal: HTMLDivElement | null;
let otherCooldownModalContainer: HTMLDivElement | null;
let btnOtherCooldownModalSave: HTMLButtonElement | null;
let otherCooldownModalError: HTMLDivElement | null;

// 正在编辑的组 id(打开弹窗时锁定,避免保存瞬间 otherGroupFilter 被切走写错组)。
let cooldownEditGroupId = '';

// parseOtherCooldownCodes 解析状态码输入为合法码数组:支持逗号/分号/空格分隔,单项为
// 100-599 整数或 {d}xx 整段写法(如 "5xx" → 500..599);非法/越界项静默剔除。
export function parseOtherCooldownCodes(raw: string): number[] {
    const out = new Set<number>();
    for (const tok of String(raw || '').split(/[,;，；\s]+/)) {
        const t = tok.trim();
        if (!t) continue;
        const rangeMatch = t.match(/^([1-5])xx$/i);
        if (rangeMatch) {
            const base = Number(rangeMatch[1]) * 100;
            for (let c = base; c < base + 100; c++) out.add(c);
            continue;
        }
        const n = Number(t);
        if (Number.isInteger(n) && n >= 100 && n <= 599) out.add(n);
    }
    return Array.from(out).sort((a, b) => a - b);
}

// parseOtherCooldownModels 解析模型过滤输入:逗号分隔,去空格去重(大小写不敏感),保留原始大小写。
export function parseOtherCooldownModels(raw: string): string[] {
    const seen = new Set<string>();
    const out: string[] = [];
    for (const tok of String(raw || '').split(/[,，]+/)) {
        const t = tok.trim();
        if (!t) continue;
        const k = t.toLowerCase();
        if (seen.has(k)) continue;
        seen.add(k);
        out.push(t);
    }
    return out;
}

// writeOtherCooldownModalError 在弹窗内红条展示校验/保存错误。
function writeOtherCooldownModalError(msg: string): void {
    if (otherCooldownModalError) {
        otherCooldownModalError.textContent = msg;
        otherCooldownModalError.classList.remove('hidden');
    }
}

function clearOtherCooldownModalError(): void {
    if (otherCooldownModalError) {
        otherCooldownModalError.classList.add('hidden');
        otherCooldownModalError.textContent = '';
    }
}

// openOtherCooldownModal:打开冷却规则弹窗并回填当前选中组的既有配置。
// 无具体选中组(「全部组」/组列表缺失)时直接忽略(入口按钮在该态下本就隐藏,防御兜底)。
export function openOtherCooldownModal(): void {
    const gid = state.otherGroupFilter;
    if (!gid || gid === 'ALL') return;
    const groups = (state.lastBackendData && Array.isArray(state.lastBackendData.otherGroups))
        ? state.lastBackendData.otherGroups
        : [];
    const g = groups.find((x: any) => String(x.groupId || x.groupID || x.id || '') === gid);
    if (!g) return;

    cooldownEditGroupId = gid;
    otherCooldownTargetGroupDisplay.value = String(g.groupName || gid);

    const cd = (g as any).cooldown || null;
    otherCooldownEnabled.value = !!cd?.enabled;

    // 模型过滤多选回填:chips 即规则白名单数组;候选列表换组才清空(同组保留已拉取结果)。
    otherCooldownSelectedModels.value = cd && Array.isArray(cd.models) ? cd.models.map((m: any) => String(m)) : [];
    if (cooldownModelsCachedGroupId !== gid) {
        otherCooldownModelOptions.value = [];
    }

    const codesInput = document.getElementById('otherCooldownModalCodes') as HTMLInputElement | null;
    const secsInput = document.getElementById('otherCooldownModalSecs') as HTMLInputElement | null;
    if (codesInput) codesInput.value = cd && Array.isArray(cd.statusCodes) ? cd.statusCodes.join(',') : '';
    if (secsInput) secsInput.value = String(cd?.cooldownSecs ?? 60);
    clearOtherCooldownModalError();

    if (!otherCooldownModal || !otherCooldownModalContainer) {
        otherCooldownModal = document.getElementById('otherCooldownModal') as HTMLDivElement | null;
        otherCooldownModalContainer = document.getElementById('otherCooldownModalContainer') as HTMLDivElement | null;
    }
    if (!otherCooldownModal || !otherCooldownModalContainer) return;
    otherCooldownModal.classList.remove('opacity-0', 'pointer-events-none', 'hidden');
    otherCooldownModalContainer.classList.remove('scale-95');
    otherCooldownModalContainer.classList.add('scale-100');
}

// closeOtherCooldownModal:关闭弹窗(opacity 模式,与 OtherAccountModal 同范式)。
export function closeOtherCooldownModal(): void {
    if (!otherCooldownModal || !otherCooldownModalContainer) return;
    otherCooldownModalContainer.classList.remove('scale-100');
    otherCooldownModalContainer.classList.add('scale-95');
    otherCooldownModal.classList.add('opacity-0', 'pointer-events-none');
}

// submitOtherCooldownRule:校验后经 invoke(other:set-cooldown-rule) 下发规则 JSON。
// 启用态必须至少填一个合法状态码;关闭态原样下发已填配置(后端存为禁用态,回显不丢),
// 仅当开关关且状态码/模型全空时后端才清除该组规则。成功后关闭弹窗(accounts-res 由后端广播刷新)。
async function submitOtherCooldownRule(): Promise<void> {
    const codesInput = document.getElementById('otherCooldownModalCodes') as HTMLInputElement | null;
    const secsInput = document.getElementById('otherCooldownModalSecs') as HTMLInputElement | null;

    const codes = parseOtherCooldownCodes(codesInput?.value || '');
    // 模型过滤取多选选择器的 chips 数组;经 parseOtherCooldownModels 统一 trim/去重,
    // 自定义通配项(如 deepseek*)原样保留,交由后端前缀匹配。
    const models = parseOtherCooldownModels(otherCooldownSelectedModels.value.join(','));
    const enabled = otherCooldownEnabled.value;
    if (enabled && codes.length === 0) {
        writeOtherCooldownModalError(
            (i18n[state.currentLanguage] || i18n.zh || {}).otherCooldownNeedCodes || '启用自定义冷却时至少填写一个合法状态码(100-599)'
        );
        return;
    }
    const rule = {
        enabled,
        statusCodes: codes,
        cooldownSecs: Math.max(1, Math.min(604800, Number(secsInput?.value) || 60)),
        models,
    };
    try {
        if (btnOtherCooldownModalSave) btnOtherCooldownModalSave.disabled = true;
        const res = await ipcRenderer.invoke('other:set-cooldown-rule', cooldownEditGroupId, JSON.stringify(rule));
        if (res && res.success) {
            closeOtherCooldownModal();
        } else {
            writeOtherCooldownModalError(res?.error || (i18n[state.currentLanguage] || i18n.zh || {}).otherCooldownSaveFailed || '保存失败,请重试');
        }
    } catch (err: any) {
        writeOtherCooldownModalError(err?.message || '系统错误');
    } finally {
        if (btnOtherCooldownModalSave) btnOtherCooldownModalSave.disabled = false;
    }
}

// fetchOtherCooldownModels:按当前编辑组调 other:fetch-models 拉上游模型清单,填入多选候选列表。
// 组已在号池内,只传 groupId(后端自动回退该组首个可用账号的 baseURL/apiKey 探测)。
// 上游无 /v1/models 端点时后端返回 allowManualInput,提示改为手输通配项(选择器 allow-custom 兜底)。
async function fetchOtherCooldownModels(): Promise<void> {
    if (!btnOtherCooldownFetchModels || !cooldownEditGroupId) return;
    clearOtherCooldownModalError();

    const origHTML = btnOtherCooldownFetchModels.innerHTML;
    btnOtherCooldownFetchModels.disabled = true;
    btnOtherCooldownFetchModels.innerHTML = `<span class="material-symbols-outlined text-[14px] animate-spin">refresh</span><span>获取中...</span>`;

    try {
        const res = await ipcRenderer.invoke('other:fetch-models', cooldownEditGroupId);
        if (res && res.success && Array.isArray(res.models) && res.models.length > 0) {
            otherCooldownModelOptions.value = res.models.map((m: any) => String(m));
            cooldownModelsCachedGroupId = cooldownEditGroupId;
        } else {
            const dict = i18n[state.currentLanguage] || i18n.zh || {};
            writeOtherCooldownModalError(
                (res && res.allowManualInput)
                    ? (res?.error || dict.otherManualInputAllowed || '上游暂不支持模型列表,请手动输入模型名或通配项')
                    : (res?.error || dict.otherCooldownFetchFailed || '获取模型列表失败')
            );
        }
    } catch (err: any) {
        writeOtherCooldownModalError(err?.message || '系统错误');
    } finally {
        if (btnOtherCooldownFetchModels) {
            btnOtherCooldownFetchModels.disabled = false;
            btnOtherCooldownFetchModels.innerHTML = origHTML;
        }
    }
}

// 句柄赋值 + 事件绑定(由 accountsController.initAccountsEvents 委托调用)。
export function initOtherCooldownModalEvents(): void {
    btnOtherCooldownConfig = document.getElementById('btnOtherCooldownConfig') as HTMLButtonElement | null;
    btnOtherCooldownFetchModels = document.getElementById('btnOtherCooldownFetchModels') as HTMLButtonElement | null;
    otherCooldownModal = document.getElementById('otherCooldownModal') as HTMLDivElement | null;
    otherCooldownModalContainer = document.getElementById('otherCooldownModalContainer') as HTMLDivElement | null;
    btnOtherCooldownModalSave = document.getElementById('btnOtherCooldownModalSave') as HTMLButtonElement | null;
    otherCooldownModalError = document.getElementById('otherCooldownModalError') as HTMLDivElement | null;

    if (btnOtherCooldownConfig) {
        btnOtherCooldownConfig.addEventListener('click', openOtherCooldownModal);
        btnOtherCooldownConfig.addEventListener('click', (e) => e.stopPropagation());
    }
    if (btnOtherCooldownFetchModels) btnOtherCooldownFetchModels.addEventListener('click', fetchOtherCooldownModels);
    if (btnOtherCooldownModalSave) btnOtherCooldownModalSave.addEventListener('click', submitOtherCooldownRule);
    const btnCancel = document.getElementById('btnOtherCooldownModalCancel') as HTMLButtonElement | null;
    if (btnCancel) btnCancel.addEventListener('click', closeOtherCooldownModal);
    const btnClose = document.getElementById('btnOtherCooldownModalClose') as HTMLButtonElement | null;
    if (btnClose) btnClose.addEventListener('click', closeOtherCooldownModal);
}

// renderOtherCooldownEntry:渲染工具栏冷却入口按钮状态——组级规则启用中时高亮(冰蓝)并在
// 文案后追加状态点;未启用回落普通 outline 色。由 renderOtherLBMode 在组切换/广播刷新时调用。
export function renderOtherCooldownEntry(): void {
    if (!btnOtherCooldownConfig) {
        btnOtherCooldownConfig = document.getElementById('btnOtherCooldownConfig') as HTMLButtonElement | null;
    }
    if (!btnOtherCooldownConfig) return;
    const groups = (state.lastBackendData && Array.isArray(state.lastBackendData.otherGroups))
        ? state.lastBackendData.otherGroups
        : [];
    const g = groups.find((x: any) => String(x.groupId || x.groupID || x.id || '') === state.otherGroupFilter);
    const active = !!(g as any)?.cooldown?.enabled;
    if (active) {
        btnOtherCooldownConfig.classList.remove('text-outline', 'dark:text-outline-variant', 'bg-outline-variant/10', 'border-outline-variant/20');
        btnOtherCooldownConfig.classList.add('text-sky-500', 'dark:text-sky-400', 'bg-sky-500/10', 'border-sky-500/30');
    } else {
        btnOtherCooldownConfig.classList.remove('text-sky-500', 'dark:text-sky-400', 'bg-sky-500/10', 'border-sky-500/30');
        btnOtherCooldownConfig.classList.add('text-outline', 'dark:text-outline-variant', 'bg-outline-variant/10', 'border-outline-variant/20');
    }
}
