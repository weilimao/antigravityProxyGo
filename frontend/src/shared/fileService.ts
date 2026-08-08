// fileService.ts — 前端统一文件转入/转出服务。
//
// 设计目标（低耦合）：
//   - 所有「保存导出数据 / 打开导入文件 / 打开日志目录」的前端入口统一走本模块，
//     不再散落各处。后端负责对话框、目录记忆与「保存成功后自动打开文件夹」；
//     前端只负责编排传参 + 导出成功后弹出成功提示。
//   - 彻底解耦：调用方不关心后端 channel 名与参数装载方式。

import { ipcRenderer } from './ipc';
import state from '../ui/dashboardState';

type SaveTextResult = 'saved' | 'cancelled' | 'error';

/**
 * 以文本内容发起一次「另存为」导出。
 * 后端 Save 对话框会：记忆用户选择的目录 + 保存成功后自动打开文件夹定位文件。
 *
 * @param payload { channel, args } 后端已注册的导出 IPC channel 与参数。
 * @param successHint 成功时弹出的提示文案（可 i18n）。
 * @param errorHint   失败时弹出的提示文案前缀。
 * @returns 'saved' | 'cancelled' | 'error'
 */
export async function saveText(
    payload: { channel: string; args: any[] },
    successHint: string,
    errorHint: string,
): Promise<SaveTextResult> {
    try {
        const success = await ipcRenderer.invoke(payload.channel, ...payload.args);
        if (success === false) {
            // 后端表示用户取消（或未写文件）
            return 'cancelled';
        }
        alert(successHint);
        return 'saved';
    } catch (err: any) {
        alert(errorHint + (err?.message || err));
        return 'error';
    }
}

/**
 * 以文本内容发起一次「另存为」导出（非阻塞式，适用于 UI 无需等待结果的场景，
 * 例如通过 ipcRenderer.send 单向触发）。沿用 saveText 的语义。
 */
export async function saveTextFireAndForget(
    channel: string,
    args: any[],
): Promise<void> {
    try {
        await ipcRenderer.invoke(channel, ...args);
    } catch (err) {
        console.error(`[fileService] ${channel} failed:`, err);
    }
}

/**
 * 从后端打开一个文件。复用后端 Open 对话框（含目录记忆）。
 */
export async function openExistingFile(
    payload: { channel: string; args: any[] },
): Promise<any> {
    return ipcRenderer.invoke(payload.channel, ...payload.args);
}

/**
 * 调用 shell 打开一个本地目录（文件管理器定位文件）。
 * 供「导入成功后打开该目录」等场景使用。
 */
export function openLocalFolder(path: string): void {
    if (!path) return;
    ipcRenderer.send('settings:open-folder', path);
}

export function isZh(): boolean {
    return state.currentLanguage === 'zh';
}