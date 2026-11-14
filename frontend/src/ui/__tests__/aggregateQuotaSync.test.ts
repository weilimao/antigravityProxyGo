// aggregateQuotaSync.test.ts: 针对首页额度配额条(#aggregate-quota-panel)状态同步与显隐逻辑的局部测试
// 运行命令: npx tsx frontend/src/ui/__tests__/aggregateQuotaSync.test.ts

import assert from 'node:assert';
import { readFileSync } from 'node:fs';
import { resolve as pathResolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const originalWindow = (globalThis as any).window;
const originalDocument = (globalThis as any).document;
const originalLocalStorage = (globalThis as any).localStorage;

// 简易 classList 模拟
function createClassList(initial: string[] = []) {
  const set = new Set<string>(initial);
  return {
    add: (...cls: string[]) => cls.forEach((c) => set.add(c)),
    remove: (...cls: string[]) => cls.forEach((c) => set.delete(c)),
    contains: (c: string) => set.has(c),
    toggle: (c: string, force?: boolean) => {
      if (force === undefined) {
        if (set.has(c)) { set.delete(c); return false; }
        set.add(c); return true;
      }
      if (force) set.add(c); else set.delete(c);
      return force;
    },
    toArray: () => Array.from(set),
  };
}

function makeElement(id = '', initialClasses: string[] = []) {
  const children: any[] = [];
  let innerHTMLVal = '';
  const el: any = {
    id,
    className: initialClasses.join(' '),
    classList: createClassList(initialClasses),
    textContent: '',
    title: '',
    children,
    style: {},
    checked: false,
    disabled: false,
    appendChild: (child: any) => { children.push(child); return child; },
    addEventListener: () => {},
    setAttribute: () => {},
    querySelectorAll: () => [],
    closest: () => null,
    get innerHTML() { return innerHTMLVal; },
    set innerHTML(v: string) {
      innerHTMLVal = v;
      if (v === '') children.length = 0;
    },
  };
  return el;
}

const domMap = new Map<string, any>();

if (typeof (globalThis as any).window === 'undefined') {
  (globalThis as any).window = globalThis;
}
if (typeof (globalThis as any).localStorage === 'undefined') {
  (globalThis as any).localStorage = {
    getItem: () => null,
    setItem: () => {},
    removeItem: () => {},
    clear: () => {},
  };
}
(globalThis as any).document = {
  documentElement: makeElement('html'),
  getElementById: (id: string) => domMap.get(id) || null,
  querySelector: () => null,
  querySelectorAll: () => [],
  createElement: () => makeElement(),
  addEventListener: () => {},
};

const cleanups: (() => void)[] = [];
cleanups.push(() => {
  if (originalWindow === undefined) delete (globalThis as any).window;
  else (globalThis as any).window = originalWindow;
  if (originalDocument === undefined) delete (globalThis as any).document;
  else (globalThis as any).document = originalDocument;
  if (originalLocalStorage === undefined) delete (globalThis as any).localStorage;
  else (globalThis as any).localStorage = originalLocalStorage;
});

async function runTests() {
  const { default: state } = await import('../dashboardState');
  const { updateAggregateQuotaUI } = await import('../aggregateQuotaUI');

  console.log('=== 开始运行 aggregateQuotaSync 局部单元测试 ===\n');

  const panel = makeElement('aggregate-quota-panel', ['hidden', 'glass-card', 'flex-col']);
  const grid = makeElement('aggregate-quota-grid');
  const info = makeElement('aggregate-quota-info');
  domMap.set('aggregate-quota-panel', panel);
  domMap.set('aggregate-quota-grid', grid);
  domMap.set('aggregate-quota-info', info);

  const origStateSnapshot = {
    isRemoteMode: state.isRemoteMode,
    remoteStats: state.remoteStats,
    currentActiveChannel: state.currentActiveChannel,
    lastBackendData: state.lastBackendData,
    currentAccountsList: state.currentAccountsList,
    quotaCache: state.quotaCache,
  };
  cleanups.push(() => {
    Object.assign(state, origStateSnapshot);
    domMap.clear();
  });

  // 用例 1: 停用远程模式(isRemoteMode=false, remoteStats=null) 且 Antigravity 负载均衡开启时，面板必须可见
  state.isRemoteMode = false;
  state.remoteStats = null;
  state.currentActiveChannel = 'antigravity';
  state.lastBackendData = { poolMode: true, activeChannel: 'antigravity' };
  state.currentAccountsList = [
    { id: 'acc-1', email: 'u1@example.com', provider: 'antigravity', enabled: true },
  ];
  state.quotaCache = {};

  updateAggregateQuotaUI();
  assert.strictEqual(panel.classList.contains('hidden'), false, '开启 poolMode 且非远程时应移除 hidden');
  assert.strictEqual(panel.classList.contains('flex'), true, '开启 poolMode 且非远程时应添加 flex');
  assert.strictEqual(grid.children.length, 4, 'Antigravity 通道应渲染 4 个聚合额度条');
  console.log('✓ 用例 1 通过: 本地 Antigravity 负载均衡开启时正确展示 4 栏配额条');

  // 用例 2: poolMode 关闭时应隐藏；重新打开（同步更新 lastBackendData.poolMode=true）后应立即恢复可见
  state.lastBackendData.poolMode = false;
  updateAggregateQuotaUI();
  assert.strictEqual(panel.classList.contains('hidden'), true, 'poolMode=false 时应隐藏面板');

  state.lastBackendData.poolMode = true;
  updateAggregateQuotaUI();
  assert.strictEqual(panel.classList.contains('hidden'), false, 'poolMode 重新开启后应立即恢复显示');
  console.log('✓ 用例 2 通过: poolMode 开关状态变化可实时反映到面板显隐');

  // 用例 3: 处于远程已连接但无 remoteStats 时应隐藏；停用远程后应立即恢复显示本地配额条
  state.isRemoteMode = true;
  state.remoteStats = null;
  updateAggregateQuotaUI();
  assert.strictEqual(panel.classList.contains('hidden'), true, '远程模式未拿到 stats 时应禁止显示本地配额条');

  state.isRemoteMode = false;
  updateAggregateQuotaUI();
  assert.strictEqual(panel.classList.contains('hidden'), false, '停用远程模式后即使 remoteStats=null 也应恢复显示本地配额条');
  console.log('✓ 用例 3 通过: 停用远程模式后正确恢复本地配额条');

  // 用例 4: 静态契约校验 — remoteController 不应再残留错误的 'get-accounts' 通道名
  const __dirname = dirname(fileURLToPath(import.meta.url));
  const remoteSrc = readFileSync(pathResolve(__dirname, '../remoteController.ts'), 'utf8');
  assert.strictEqual(remoteSrc.includes("'get-accounts'"), false, "remoteController.ts 不得再使用无效通道 'get-accounts'");
  assert.strictEqual(remoteSrc.includes("ipcRenderer.send('accounts:get')"), true, "remoteController.ts 应使用正确通道 'accounts:get'");
  console.log("✓ 用例 4 通过: remoteController 账号同步通道已对齐为 'accounts:get'");

  // 用例 5: 静态契约校验 — dashboard.ts 切回控制台与 app_ipc_send_accounts.go 开关均闭合
  const dashboardSrc = readFileSync(pathResolve(__dirname, '../dashboard.ts'), 'utf8');
  assert.ok(
    dashboardSrc.includes("if (viewName === 'dashboard')") &&
      dashboardSrc.includes('state.callbacks.updateAggregateQuotaUI()'),
    'dashboard.ts switchView("dashboard") 应触发 updateAggregateQuotaUI()'
  );
  const goSendSrc = readFileSync(pathResolve(__dirname, '../../../../app_ipc_send_accounts.go'), 'utf8');
  const poolToggleSection = goSendSrc.slice(
    goSendSrc.indexOf('case "pool:toggle":'),
    goSendSrc.indexOf('case "nvidia:set-lb-mode":')
  );
  const emitMatches = poolToggleSection.match(/a\.emitAccountsRes\(\)/g) || [];
  assert.strictEqual(emitMatches.length, 5, 'app_ipc_send_accounts.go 中 5 个 pool:toggle* 分支均应调用 a.emitAccountsRes()');
  console.log('✓ 用例 5 通过: dashboard 切页重绘与 Go 侧 5 路开关广播契约完整');

  console.log('\n>>> aggregateQuotaSync 全部局部测试用例绿灯通过！ <<<');
}

(async () => {
  try {
    await runTests();
  } catch (err) {
    console.error('❌ 测试执行失败:', err);
    process.exitCode = 1;
  } finally {
    for (let i = cleanups.length - 1; i >= 0; i--) {
      cleanups[i]();
    }
  }
})();
