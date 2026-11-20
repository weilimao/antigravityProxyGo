// aiPricingAccountModels.test.ts: AI 一键生成计费配置中按账号生成联动模型下拉列表的专项局部测试
// 运行命令: npx tsx frontend/src/ui/__tests__/aiPricingAccountModels.test.ts

import assert from 'node:assert';

// 简易 classList 模拟
function createClassList(initial: string[] = []) {
  const set = new Set<string>(initial);
  return {
    add: (...cls: string[]) => cls.forEach((c) => set.add(c)),
    remove: (...cls: string[]) => cls.forEach((c) => set.delete(c)),
    contains: (c: string) => set.has(c),
  };
}

function makeElement(id = '') {
  let innerHTMLVal = '';
  const el: any = {
    id,
    value: '',
    disabled: false,
    classList: createClassList([]),
    listeners: {} as Record<string, Function[]>,
    addEventListener: (evt: string, fn: Function) => {
      if (!el.listeners[evt]) el.listeners[evt] = [];
      el.listeners[evt].push(fn);
    },
    get innerHTML() {
      return innerHTMLVal;
    },
    set innerHTML(val: string) {
      innerHTMLVal = val;
      // 提取首个 option value 作为默认回填
      const match = val.match(/value="([^"]+)"/);
      if (match) {
        el.value = match[1];
      } else {
        el.value = '';
      }
    },
  };
  return el;
}

// 设置环境 mock
(globalThis as any).window = globalThis;
(globalThis as any).requestAnimationFrame = (cb: Function) => setTimeout(cb, 0);

const storage = new Map<string, string>();
(globalThis as any).localStorage = {
  getItem: (k: string) => storage.get(k) || null,
  setItem: (k: string, v: string) => storage.set(k, String(v)),
  removeItem: (k: string) => storage.delete(k),
  clear: () => storage.clear(),
};

const elements: Record<string, any> = {};
elements['aiPricingAccountModelSelect'] = makeElement('aiPricingAccountModelSelect');
elements['aiPricingAccountSelect'] = makeElement('aiPricingAccountSelect');

(globalThis as any).document = {
  getElementById: (id: string) => elements[id] || null,
  createElement: (tag: string) => makeElement(tag),
  createDocumentFragment: () => ({
    children: [] as any[],
    appendChild: function (c: any) { this.children.push(c); return c; },
  }),
  createTextNode: (t: string) => ({ textContent: t }),
  createComment: () => ({}),
  head: makeElement('head'),
  body: makeElement('body'),
};

async function runTests() {
  console.log('🧪 开始执行 AI 计费按账号生成联动模型下拉列表专项测试...');

  // 动态引入并注入 mock state
  const stateMod = await import('../dashboardState');
  const state = stateMod.default;
  const { updateAccountModelsSelect } = await import('../aiPricingController');

  // 1. 准备测试号池数据
  state.currentAccountsList = [
    {
      id: 'acc-pro-argon',
      email: 'argon@gmail.com',
      provider: 'antigravity',
      enabled: true,
      supportedModels: ['claude-opus-5-5-high', 'gemini-4-argon', 'gemini-2.5-flash', 'gemini-2.5-pro'],
    },
    {
      id: 'acc-free-unprobed',
      email: 'unprobed@gmail.com',
      provider: 'antigravity',
      enabled: true,
      supportedModels: [], // 尚未探测
    },
  ];

  // 测试用例 1: 账号包含已探测模型列表
  {
    updateAccountModelsSelect('acc-pro-argon');
    const selectEl = elements['aiPricingAccountModelSelect'];
    assert.strictEqual(selectEl.disabled, false, '有账号时下拉框不应被禁用');
    assert.ok(selectEl.innerHTML.includes('gemini-2.5-flash'), '应包含 gemini-2.5-flash');
    assert.ok(selectEl.innerHTML.includes('claude-opus-5-5-high'), '应包含 claude-opus-5-5-high');
    assert.ok(selectEl.innerHTML.includes('gemini-4-argon'), '应包含 gemini-4-argon');
    // 推荐模型优先级: gemini-2.5-flash 排在最前并默认选中
    assert.strictEqual(selectEl.value, 'gemini-2.5-flash', '应优先默认选中 gemini-2.5-flash');
    console.log('  ✅ 测试用例 1 通过: 已探测账号正确渲染且默认选中推荐模型');
  }

  // 测试用例 2: 账号未探测(supportedModels 为空),使用默认兜底模型
  {
    updateAccountModelsSelect('acc-free-unprobed');
    const selectEl = elements['aiPricingAccountModelSelect'];
    assert.strictEqual(selectEl.disabled, false, '未探测账号下拉框也不应被禁用');
    assert.ok(selectEl.innerHTML.includes('gemini-2.5-flash'), '兜底列表应包含 gemini-2.5-flash');
    assert.ok(selectEl.innerHTML.includes('gemini-2.5-pro'), '兜底列表应包含 gemini-2.5-pro');
    assert.ok(selectEl.innerHTML.includes('gemini-2.5-flash-lite'), '兜底列表应包含 gemini-2.5-flash-lite');
    assert.strictEqual(selectEl.value, 'gemini-2.5-flash', '兜底列表仍优先推荐 gemini-2.5-flash');
    console.log('  ✅ 测试用例 2 通过: 未探测账号成功使用兜底模型列表平滑过渡');
  }

  // 测试用例 3: 空账号 ID 时的防护回退
  {
    updateAccountModelsSelect('');
    const selectEl = elements['aiPricingAccountModelSelect'];
    assert.strictEqual(selectEl.disabled, true, '空账号 ID 时应禁用下拉框');
    assert.strictEqual(selectEl.value, '', '空账号 ID 时选值应为空');
    console.log('  ✅ 测试用例 3 通过: 空账号状态下正确呈现禁用与提示');
  }

  // Teardown: 恢复环境与数据
  state.currentAccountsList = [];
  delete (globalThis as any).document;
  delete (globalThis as any).window;
  delete (globalThis as any).localStorage;
  delete (globalThis as any).requestAnimationFrame;
  console.log('🧹 测试环境与 Mock 数据已彻底清理 (Teardown complete)');
}

runTests().catch((err) => {
  console.error('❌ 测试失败:', err);
  process.exit(1);
});
