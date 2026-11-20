// accountModelsModalPerf.test.ts: 针对账号模型检测弹窗即时开闭、noScale、事件幂等与批量渲染性能的局部测试
// 运行命令: npx tsx frontend/src/ui/__tests__/accountModelsModalPerf.test.ts

import assert from 'node:assert';

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

// 模拟 DocumentFragment
class MockDocumentFragment {
  children: any[] = [];
  appendChild(child: any) {
    this.children.push(child);
    return child;
  }
}

function makeElement(id = '', initialClasses: string[] = []) {
  const children: any[] = [];
  let innerHTMLVal = '';
  const listeners: Record<string, Function[]> = {};

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
    appendChild: (child: any) => {
      if (child instanceof MockDocumentFragment) {
        while (child.children.length > 0) {
          children.push(child.children.shift());
        }
        return child;
      }
      children.push(child);
      return child;
    },
    addEventListener: (event: string, fn: Function) => {
      if (!listeners[event]) listeners[event] = [];
      listeners[event].push(fn);
    },
    dispatchEvent: (event: string, e?: any) => {
      (listeners[event] || []).forEach(fn => fn(e || { target: el }));
    },
    getListenerCount: (event: string) => (listeners[event] || []).length,
    setAttribute: () => {},
    querySelectorAll: () => [],
    closest: () => null,
    get innerHTML() { return innerHTMLVal; },
    set innerHTML(val: string) { innerHTMLVal = val; children.length = 0; },
  };
  return el;
}

// 设置环境 Mock
(globalThis as any).window = globalThis;
(globalThis as any).requestAnimationFrame = (cb: Function) => setTimeout(cb, 0);

const storage = new Map<string, string>();
(globalThis as any).localStorage = {
  getItem: (k: string) => storage.get(k) || null,
  setItem: (k: string, v: string) => storage.set(k, String(v)),
  removeItem: (k: string) => storage.delete(k),
  clear: () => storage.clear(),
};

const domRegistry = new Map<string, any>();
function registerEl(id: string, initialClasses: string[] = []) {
  const el = makeElement(id, initialClasses);
  domRegistry.set(id, el);
  return el;
}

const origDoc = (globalThis as any).document;
const origWindow = (globalThis as any).window;
const origRAF = (globalThis as any).requestAnimationFrame;

(globalThis as any).document = {
  getElementById: (id: string) => domRegistry.get(id) || null,
  createElement: (tag: string) => makeElement(tag),
  createDocumentFragment: () => new MockDocumentFragment(),
};

async function runTests() {
  console.log('[Test] 开始执行账号可用模型弹窗性能优化（即时开闭与防抖防卡）局部验证...');

  // 动态引入待测模块，确保环境 Mock 先于模块顶层副作用生效
  const {
    openAccountModelsModal,
    hideAccountModelsModal,
    initAccountModelsModalEvents,
  } = await import('../accountModelsModal');
  const { renderProbeResults } = await import('../accountModelsRenderer');

  // 1. 初始化 DOM 树节点
  const modalEl = registerEl('accountModelsModal', ['opacity-0', 'pointer-events-none', 'hidden']);
  const containerEl = registerEl('accountModelsModalContainer', ['scale-95']);
  const tbodyEl = registerEl('accountModelsTableBody');
  const countEl = registerEl('accountModelsSummaryCount');
  const totalAccEl = registerEl('statProbeTotalAccounts');
  const allModelsEl = registerEl('statProbeAllModelsCount');
  const diffModelsEl = registerEl('statProbeDiffModelsCount');
  const covSecEl = registerEl('accountModelsCoverageSection', ['hidden']);
  const covChipsEl = registerEl('accountModelsCoverageChips');

  const btnProbeEl = registerEl('btnProbeAccountModels');
  registerEl('iconProbeModels');
  const btnCloseEl = registerEl('btnAccountModelsModalClose');
  const btnCloseSecEl = registerEl('btnAccountModelsCloseSecondary');
  registerEl('btnAccountModelsReProbe');
  registerEl('iconAccountModelsReProbe');
  registerEl('preferLowTierAccountToggle');
  registerEl('preferLowTierStatusBadge');
  registerEl('accountModelsSearchInput');
  registerEl('accountModelsLoadingBar');

  // Test Case 1: 事件绑定的幂等性（防重复监听与闭包泄漏）
  console.log('-> 测试用例 1: initAccountModelsModalEvents 幂等性验证（多次调用不重复绑定）');
  initAccountModelsModalEvents();
  initAccountModelsModalEvents();
  initAccountModelsModalEvents();
  assert.strictEqual(btnProbeEl.getListenerCount('click'), 1, 'btnProbeAccountModels click 监听器应仅绑定 1 次');
  assert.strictEqual(btnCloseEl.getListenerCount('click'), 1, 'btnClose click 监听器应仅绑定 1 次');
  assert.strictEqual(btnCloseSecEl.getListenerCount('click'), 1, 'btnCloseSecondary click 监听器应仅绑定 1 次');
  console.log('✓ 用例 1 通过: 多次初始化下事件监听器保持单次幂等，彻底根除闭包堆积');

  // Test Case 2: 打开弹窗阶段即时响应（0 延迟，零人造定时器）
  console.log('-> 测试用例 2: 弹窗打开阶段即时响应验证');
  openAccountModelsModal();

  // 验证同步瞬间状态切换
  assert.strictEqual(modalEl.classList.contains('hidden'), false, '打开时必须立即移除 hidden');
  assert.strictEqual(modalEl.classList.contains('opacity-0'), false, '打开时必须立即移除 opacity-0');
  assert.strictEqual(modalEl.classList.contains('pointer-events-none'), false, '打开时必须立即恢复交互');
  assert.strictEqual(containerEl.classList.contains('scale-100'), true, 'container 应立即就绪 scale-100');
  console.log('✓ 用例 2 通过: 打开瞬间即刻就绪，无任何 setTimeout 延迟阻塞');

  // Test Case 3: 关闭弹窗阶段瞬时收起（零粘滞、零拖泥带水）
  console.log('-> 测试用例 3: 弹窗关闭阶段瞬间响应验证');
  hideAccountModelsModal();

  assert.strictEqual(modalEl.classList.contains('opacity-0'), true, '关闭时必须立即加上 opacity-0 瞬时隐藏');
  assert.strictEqual(modalEl.classList.contains('pointer-events-none'), true, '关闭时必须立即禁用点击交互');
  console.log('✓ 用例 3 通过: 关闭动作瞬间执行，无拖沓滞留');

  // Test Case 4: DocumentFragment 批量挂载渲染性能与统计数据
  console.log('-> 测试用例 4: accountModelsRenderer 批量挂载性能与统计完整性');
  const mockProbeData = {
    summary: {
      totalAccounts: 2,
      probedAccounts: 2,
      allPoolModels: ['gemini-2.5-flash', 'claude-3-5-sonnet', 'gemini-4-hydrogen'],
      differentiatedModels: ['gemini-4-hydrogen'],
      familyCoverage: [
        { family: 'gemini-4-hydrogen', isDifferentiated: true, models: ['gemini-4-hydrogen'], supportedCount: 1, totalProbed: 2 },
      ],
    },
    results: [
      {
        id: 'acc_1',
        email: 'user1@example.com',
        tier: 'Pro',
        hasArgonBucket: true,
        probed: true,
        supportedModels: ['gemini-2.5-flash', 'claude-3-5-sonnet', 'gemini-4-hydrogen'],
        missingModels: [],
      },
      {
        id: 'acc_2',
        email: 'user2@example.com',
        tier: 'Free',
        hasArgonBucket: false,
        probed: true,
        supportedModels: ['gemini-2.5-flash'],
        missingModels: ['gemini-4-hydrogen', 'claude-3-5-sonnet'],
      },
    ],
  };

  const refs: ProbeRenderRefs = {
    accountModelsSummaryCount: countEl,
    statProbeTotalAccounts: totalAccEl,
    statProbeAllModelsCount: allModelsEl,
    statProbeDiffModelsCount: diffModelsEl,
    accountModelsCoverageSection: covSecEl,
    accountModelsCoverageChips: covChipsEl,
    accountModelsTableBody: tbodyEl,
  };

  const renderState: ProbeRenderState = {
    activeFamilyFilter: null,
    searchKeyword: '',
    expandedSupportedAccounts: new Set(),
    expandedMissingAccounts: new Set(),
    probingAccountIds: new Set(),
    isGlobalProbing: false,
    singleProbeErrors: {},
  };

  renderProbeResults(mockProbeData, refs, renderState);

  assert.strictEqual(totalAccEl.textContent, '2', '账号总数统计应为 2');
  assert.strictEqual(allModelsEl.textContent, '3', '发现模型数应为 3');
  assert.strictEqual(diffModelsEl.textContent, '1', '差异受限模型数应为 1');
  assert.strictEqual(covSecEl.classList.contains('hidden'), false, '有家族分布时应展示覆盖分布栏');
  assert.strictEqual(tbodyEl.children.length, 2, '通过 DocumentFragment 应一次性挂载 2 个账号 tr 节点');
  console.log('✓ 用例 4 通过: renderProbeResults 离线批量装配正常且统计准确');

  console.log('✅ 所有局部精准测试用例全部执行通过！');
}

runTests()
  .catch((err) => {
    console.error('❌ 测试失败:', err);
    process.exit(1);
  })
  .finally(() => {
    // Teardown: 恢复全局环境并清理测试节点
    domRegistry.clear();
    (globalThis as any).document = origDoc;
    (globalThis as any).window = origWindow;
    (globalThis as any).requestAnimationFrame = origRAF;
  });
