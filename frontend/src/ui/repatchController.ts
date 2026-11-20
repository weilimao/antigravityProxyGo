/**
 * 顶部导航栏「重新打补丁并重启 Antigravity 桌面端」按钮控制器。
 *
 * 解决 Antigravity 桌面端自动更新覆盖 app.asar 后补丁丢失的问题：
 * 用户无需退出代理程序，点击按钮即可经二次确认后，通知后端完成
 * 「关闭运行中桌面端 -> 重打全量补丁 -> 自动重启桌面端」闭环。
 */
import { ipcRenderer } from '../shared/ipc';
import i18n from '../shared/i18n';
import state from './dashboardState';

let btnRepatchRestart: HTMLButtonElement | null = null;
let repatchIcon: HTMLElement | null = null;
let repatchLabel: HTMLElement | null = null;
let isRepatching = false;

export function initRepatchEvents(): void {
    btnRepatchRestart = document.getElementById('btnRepatchRestart') as HTMLButtonElement | null;
    if (!btnRepatchRestart) return;

    repatchIcon = btnRepatchRestart.querySelector('.material-symbols-outlined');
    repatchLabel = btnRepatchRestart.querySelector('span[data-i18n="repatchRestartBtn"]');
    btnRepatchRestart.addEventListener('click', onRepatchClick);
}

async function onRepatchClick(): Promise<void> {
    if (isRepatching) return;

    const dict = i18n[state.currentLanguage] || i18n.zh;
    const confirmMsg =
        dict.repatchRestartConfirm ||
        '即将重新注入代理补丁并重启 Antigravity 桌面端。\n\n若 Antigravity 桌面端当前正在运行，将会先关闭再自动重新打开，是否继续？';

    const $confirm = (window as any).$confirm;
    const confirmed = typeof $confirm === 'function'
        ? await $confirm(confirmMsg)
        : window.confirm(confirmMsg);
    if (!confirmed) return;

    setRepatchingUI(true);
    try {
        const res = await ipcRenderer.invoke('patch:repatch-and-restart');
        handleRepatchResult(res);
    } catch (err: any) {
        const latestDict = i18n[state.currentLanguage] || i18n.zh;
        const detail = err && err.message ? err.message : String(err);
        window.alert((latestDict.repatchRestartFailed || '重新打补丁失败：{detail}').replace('{detail}', detail));
    } finally {
        setRepatchingUI(false);
    }
}

function handleRepatchResult(res: any): void {
    const dict = i18n[state.currentLanguage] || i18n.zh;
    if (res && res.success === true) {
        if (res.restarted) {
            window.alert(dict.repatchRestartSuccess || '补丁已重新注入，并已自动重启 Antigravity 桌面端！');
        } else {
            window.alert(dict.repatchRestartNoDesktop || res.message || '代理配置与 CLI 补丁已重新注入。');
        }
        return;
    }

    const detail = (res && res.error) ? String(res.error) : 'unknown';
    if (res && res.patched === true && res.restarted === false) {
        window.alert((dict.repatchRestartPatchedOnly || '补丁已重新注入，但自动启动 Antigravity 桌面端失败：{detail}').replace('{detail}', detail));
        return;
    }

    window.alert((dict.repatchRestartFailed || '重新打补丁失败：{detail}').replace('{detail}', detail));
}

function setRepatchingUI(running: boolean): void {
    isRepatching = running;
    if (!btnRepatchRestart) return;

    const dict = i18n[state.currentLanguage] || i18n.zh;
    btnRepatchRestart.disabled = running;

    if (repatchIcon) {
        repatchIcon.textContent = running ? 'progress_activity' : 'build';
        repatchIcon.classList.toggle('animate-spin', running);
    }
    if (repatchLabel) {
        repatchLabel.textContent = running
            ? (dict.repatchRestartRunning || '打补丁中...')
            : (dict.repatchRestartBtn || '重新打补丁');
    }
}
