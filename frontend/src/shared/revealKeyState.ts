import { ref } from 'vue';
import { ipcRenderer } from './ipc';

// revealKeyState.ts: 号池账号编辑「明文查看 API Key(眼睛图标)」的共享取明文工具。
//
// 职责单一:
// 1) 保存「当前处于编辑态的账号 id」响应式 ref(NVIDIA/Other 各一全局队列),供 Modal 的
//    <PasswordInput> 以 revealProvider 标识池类型、账号 id 由 accountsController 写入对应 ref:
//    编辑态点眼睛走 IPC 取明文,添加态(id 为 null)退化为纯视觉切换,绝不发起后端请求。
// 2) revealFromBackend 执行真实 IPC(account:reveal-key)并把明文回填真实 DOM input 的 value:
//    accountsController 的 submit 走 document.getElementById(inputId).value 读取,
//    PasswordInput 渲染的 input 带同样 inputId id,故回填即被 submit 读到,无需额外桥接。
// 3) 失败提示走「模态框级安全提示」:由打开编辑弹窗的 accountsController 注册一次
//    setRevealKeyWarning,复用 Modal 的 error 红条区,避免侵入 PasswordInput 布局。

export const nvidiaRevealAccountId = ref<string | null>(null);
export const otherRevealAccountId = ref<string | null>(null);

export async function revealFromBackend(channel: string, provider: string, accountId: string, inputEl: HTMLInputElement): Promise<boolean> {
  try {
    const res = await ipcRenderer.invoke(channel, accountId, provider);
    if (res && res.success && typeof res.apiKey === 'string' && res.apiKey !== '') {
      inputEl.value = res.apiKey;
      return true;
    }
    const msg = (res?.error || '无法获取明文 Key');
    const warn = getRevealKeyWarning();
    if (warn) warn(msg);
    console.warn('[reveal-key]', msg);
  } catch (err: any) {
    const msg = (err?.message || '无法获取明文 Key');
    const warn = getRevealKeyWarning();
    if (warn) warn(msg);
    console.warn('[reveal-key]', msg);
  }
  return false;
}

let warnHandler: ((msg: string) => void) | null = null;

/** 注册取明文失败提示(模态框 error 红条)。只应存在一个打开中的模态框,故单例即可。 */
export function setRevealKeyWarning(handler: ((msg: string) => void) | null) {
  warnHandler = handler;
}

export function getRevealKeyWarning(): ((msg: string) => void) | null {
  return warnHandler;
}