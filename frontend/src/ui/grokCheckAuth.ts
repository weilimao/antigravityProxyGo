/**
 * Grok 号池「检查授权」按钮控制逻辑:从 accountsController 委托的独立模块。
 *
 * 高内聚:承担 Grok 号池「手动触发一次授权过期检查→刷新→失效移除」的前端生命周期。
 * 与 grokThawModal.ts 同源但更轻——无需弹窗列表:点按钮 → 二次 confirm →
 * invoke('grok:check-auth') → 按钮置 disabled + spinner → 拿后端三态汇总 →
 * 弹结果 toast(总数/刷新/跳过/移除/失败)。后端 emitAccountsRes 会广播刷新账号卡片,
 * 此处仅兜底呈递汇总文案。
 *
 * 与后端契约(app_account_ipc.go case "grok:check-auth"):
 *   resp = { success, total, removed, refreshed, skipped, failed, details[] }
 * 移除的号已 RemoveAccount,广播已发;refreshed 的号 token 已更新回写。
 *
 * 依赖:ipcRenderer、i18n、DOM 句柄(btnGrokCheckAuth,模板内置于 Accounts.vue 工具栏)。
 */
import { ipcRenderer } from '../shared/ipc';
import i18n from '../shared/i18n';
import state from './dashboardState';

let btnGrokCheckAuth: HTMLButtonElement | null = null;
let checkAuthIcon: HTMLElement | null = null;
let checkAuthLabel: HTMLElement | null = null;

// 运行态标记:防止运行期间重复点击触发并发检查(后端串行已自带防抖,前端再加一道)。
let isChecking = false;

// 原始按钮文案快照(运行期间被替换为 spinner 文案,结束后回填)。
let originalLabel: string = '';

/**
 * 句柄赋值 + 事件绑定。由 accountsController.initAccountsEvents 委托调用
 * (与 initGrokThawModalEvents 同位置接线)。
 */
export function initGrokCheckAuthEvents(): void {
    btnGrokCheckAuth = document.getElementById('btnGrokCheckAuth') as HTMLButtonElement | null;
    if (btnGrokCheckAuth) {
        // icon span 是第一个子元素,label span 是第二个(与 btnGrokOneClickThaw 同构)。
        checkAuthIcon = btnGrokCheckAuth.querySelector('.material-symbols-outlined');
        checkAuthLabel = btnGrokCheckAuth.querySelector('span:not(.material-symbols-outlined)');
        btnGrokCheckAuth.addEventListener('click', onGrokCheckAuthClick);
    }
}

/**
 * 点击入口:二次确认 → 置运行态 → invoke → 汇总 toast → 复原按钮。
 * confirm 由浏览器原生 window.confirm 承载(与 sessionBindings 等处的轻量二次确认同款,
 * 避免引入额外 Modal DOM)。
 */
async function onGrokCheckAuthClick(): Promise<void> {
    if (isChecking) return;
    const dict = i18n[state.currentLanguage] || i18n.zh;

    // 二次确认:检查会刷新/停用账号,属轻度状态变更动作,需用户显式确认。
    const confirmMsg = dict.grokCheckAuthConfirm ||
        '将检查所有 Grok OAuth 账号授权状态,过期则刷新,刷新令牌失效则自动停用。是否继续?';
    if (!window.confirm(confirmMsg)) return;

    setCheckingUI(true);
    try {
        const res = await ipcRenderer.invoke('grok:check-auth');
        handleCheckAuthResult(res, dict);
    } catch (err: any) {
        const detail = err && err.message ? err.message : String(err);
        window.alert((dict.grokCheckAuthFailed || '检查授权失败: {detail}').replace('{detail}', detail));
    } finally {
        setCheckingUI(false);
    }
}

/**
 * 处理后端返回的三态汇总,呈递给用户。
 * success=false 或 total=0 走「无账号/失败」兜底;否则按 grokCheckAuthDone 模板展示。
 */
function handleCheckAuthResult(res: any, dict: Record<string, string>): void {
    if (!res || res.success !== true) {
        const detail = res && res.error ? String(res.error) : 'unknown';
        window.alert((dict.grokCheckAuthFailed || '检查授权失败: {detail}').replace('{detail}', detail));
        return;
    }
    const total = typeof res.total === 'number' ? res.total : 0;
    if (total === 0) {
        window.alert(dict.grokCheckAuthNoAccounts || '当前没有可检查的 Grok OAuth 账号');
        return;
    }
    const refreshed = typeof res.refreshed === 'number' ? res.refreshed : 0;
    const skipped = typeof res.skipped === 'number' ? res.skipped : 0;
    const disabled = typeof res.disabled === 'number' ? res.disabled : (typeof res.removed === 'number' ? res.removed : 0);
    const failed = typeof res.failed === 'number' ? res.failed : 0;
    const tmpl = dict.grokCheckAuthDone ||
        '检查完成:共 {total} 个 · 刷新 {refreshed} · 跳过 {skipped} · 停用 {disabled} · 失败 {failed}';
    window.alert(
        tmpl
            .replace('{total}', String(total))
            .replace('{refreshed}', String(refreshed))
            .replace('{skipped}', String(skipped))
            .replace('{disabled}', String(disabled))
            .replace('{removed}', String(disabled))
            .replace('{failed}', String(failed))
    );
}

/**
 * 切换运行态 UI:disabled + icon 转 spinner 旋转动画 + label 改为「正在检查…」。
 * 结束后复原原 icon/label(高内聚:文案回填取 i18n 当前语言,适配运行期间切语言)。
 */
function setCheckingUI(checking: boolean): void {
    isChecking = checking;
    if (!btnGrokCheckAuth) return;
    btnGrokCheckAuth.disabled = checking;
    if (checking) {
        // 快照当前 label 文案以便复原(若 label span 不存在则用空串兜底)。
        originalLabel = checkAuthLabel ? checkAuthLabel.textContent || '' : '';
        const dict = i18n[state.currentLanguage] || i18n.zh;
        if (checkAuthIcon) {
            checkAuthIcon.textContent = 'progress_activity';
            checkAuthIcon.classList.add('animate-spin');
        }
        if (checkAuthLabel) {
            checkAuthLabel.textContent = dict.grokCheckAuthRunning || '正在检查授权…';
        }
    } else {
        if (checkAuthIcon) {
            checkAuthIcon.textContent = 'verified_user';
            checkAuthIcon.classList.remove('animate-spin');
        }
        if (checkAuthLabel) {
            const dict = i18n[state.currentLanguage] || i18n.zh;
            // 优先回填 i18n 当前文案(切语言后取最新),否则回快照。
            checkAuthLabel.textContent = dict.grokCheckAuth || originalLabel || '检查授权';
        }
    }
}
