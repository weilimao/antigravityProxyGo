import { build } from 'esbuild';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

// Node 环境提供基础浏览器 mock，避免第三方依赖报错
globalThis.localStorage = {
  getItem: () => null,
  setItem: () => {},
  removeItem: () => {},
};

const wailsStubPlugin = {
  name: 'wails-stub',
  setup(b) {
    b.onResolve({ filter: /wailsjs/ }, (args) => ({ path: args.path, namespace: 'wails-stub' }));
    b.onLoad({ filter: /.*/, namespace: 'wails-stub' }, () => ({
      contents: [
        'export const IPCSend = () => Promise.resolve();',
        "export const IPCInvoke = () => Promise.resolve('{}');",
        'export const OpenPath = () => {};',
        'export const ShowItemInFolder = () => {};',
        'export const EventsOn = () => () => {};',
        'export const EventsOff = () => {};',
        'export const EventsEmit = () => {};',
      ].join('\n'),
      loader: 'js',
    }));
  },
};

const tmp = mkdtempSync(path.join(tmpdir(), 'account-stats-test-'));
const outfile = path.join(tmp, 'account-stats-bundle.mjs');

try {
  await build({
    entryPoints: [path.resolve('src/ui/dashboardAccountStats.ts')],
    bundle: true,
    format: 'esm',
    platform: 'node',
    outfile,
    plugins: [wailsStubPlugin],
    define: {
      'window': 'globalThis',
      'document': 'globalThis.__mockDocument',
    },
  });

  // Mock DOM 视口与表格容器
  const mockTableBody = {
    innerHTML: '',
    appendChild: () => {},
    addEventListener: () => {},
    setAttribute: () => {},
    hasAttribute: () => false,
  };
  const mockContainer = {
    scrollTop: 0,
    clientHeight: 440,
    addEventListener: () => {},
    scrollTo: () => {},
  };
  globalThis.__mockDocument = {
    querySelector: () => mockTableBody,
    getElementById: (id) => {
      if (id === 'accountsTableScrollContainer') return mockContainer;
      if (id === 'accountsTable') return mockTableBody;
      return {
        textContent: '',
        classList: { add: () => {}, remove: () => {} },
        setAttribute: () => {},
        hasAttribute: () => false,
        addEventListener: () => {},
      };
    },
    createElement: () => ({
      className: '',
      style: {},
      innerHTML: '',
      setAttribute: () => {},
      appendChild: () => {},
    }),
    createDocumentFragment: () => ({
      appendChild: () => {},
    }),
  };

  const mod = await import(pathToFileURL(outfile).href);

  // 1. 构造测试数据：模拟真实环境 229 个账号，累计大量模型项
  const mockAccountsData = {};
  for (let i = 1; i <= 229; i++) {
    const accEmail = `user${i}@example.com`;
    const modelCount = (i === 1) ? 27 : 5; // 第一个账号拥有 27 个模型，其他 5 个
    const models = {};
    for (let m = 1; m <= modelCount; m++) {
      models[`model-v${m}`] = {
        reqs: 10 * m,
        inTokens: 1000 * m,
        outTokens: 200 * m,
        cachedTokens: 100 * m,
        cost: 0.05 * m,
      };
    }
    mockAccountsData[accEmail] = {
      accountId: `acc_${i}`,
      email: accEmail,
      provider: 'openai',
      requestCount: 50 * modelCount,
      inputTokens: 5000 * modelCount,
      outputTokens: 1000 * modelCount,
      cachedTokens: 500 * modelCount,
      totalCost: 0.25 * modelCount,
      models,
    };
  }

  // 2. 测试 flattenAccountEntries 默认折叠机制 (Default Collapsed)
  const expandedSet = new Set(); // 默认无展开
  const collapsedRes = mod.flattenAccountEntries(mockAccountsData, expandedSet);

  assert.equal(collapsedRes.totalAccounts, 229, '总账号数必须为 229');
  assert.equal(collapsedRes.flatRows.length, 229, '默认全部折叠时，平铺可见行必须精准等于 229 行（仅包含账号汇总主行）');
  assert.equal(collapsedRes.flatRows[0].type, 'account', '首行必须为账号汇总类型');
  assert.equal(collapsedRes.flatRows[0].isExpanded, false, '首行展开状态必须为 false');

  // 3. 测试单账号展开 (Expand Single Account)
  // 第一个账号按 tokens 倒序排序后排在最前
  const firstAccName = collapsedRes.flatRows[0].displayName;
  expandedSet.add(firstAccName);

  const singleExpandRes = mod.flattenAccountEntries(mockAccountsData, expandedSet);
  // 原有 229 行 + 第一个账号名下的 27 个模型行 = 256 行
  assert.equal(singleExpandRes.flatRows.length, 229 + 27, '展开包含 27 个模型的账号后，平铺行必须精准变为 256 行');
  assert.equal(singleExpandRes.flatRows[0].isExpanded, true, '被展开账号标记必须为 isExpanded: true');
  assert.equal(singleExpandRes.flatRows[1].type, 'model', '第二行必须是该账号名下的模型行');
  assert.equal(singleExpandRes.flatRows[1].accountDisplayName, firstAccName, '模型行的归属账号必须匹配');
  assert.equal(singleExpandRes.flatRows[27].type, 'model', '第 27 个模型行仍然是 model');
  assert.equal(singleExpandRes.flatRows[27].isLast, true, '最后一个模型项的 isLast 标记必须为 true');
  assert.equal(singleExpandRes.flatRows[28].type, 'account', '第 28 行必须恢复为下一个账号汇总主行');

  // 4. 测试全量展开与虚拟窗口切片 (Virtual Window Slicing under Full Expand)
  // 全部展开：将所有 229 个账号加入 expandedSet
  for (let i = 1; i <= 229; i++) {
    expandedSet.add(`user${i}@example.com`);
  }
  const allExpandRes = mod.flattenAccountEntries(mockAccountsData, expandedSet);
  // 总行数 = 229 个账号 + (27 + 228 * 5 = 1167 个模型) = 1396 行
  assert.equal(allExpandRes.flatRows.length, 229 + (27 + 228 * 5), '全部展开后总行数计算正确');

  // 关键测试：在 1396 行满载数据下，虚拟列表切片计算
  const win = mod.calculateVirtualWindow(
    allExpandRes.flatRows.length, // totalCount = 1396
    0, // scrollTop = 0 (顶部)
    440, // viewportHeight = 440px
    mod.ACCOUNT_ITEM_HEIGHT, // 44px
    mod.ACCOUNT_BUFFER_ROWS // 5
  );

  assert.equal(win.startIndex, 0, '顶部起始索引必须为 0');
  // 可见行: ceil(440 / 44) = 10，加上 buffer = 5 -> endIndex = 15
  assert.equal(win.endIndex, 15, '虚拟列表在视口中仅切出 15 行，严控在 20~25 行之内');
  assert.equal(win.topSpacerHeight, 0, '顶部占位高度为 0');
  assert.equal(win.bottomSpacerHeight, (1396 - 15) * 44, '底部占位高度准确支撑完整滚动条');

  // 模拟滚动到中间位置 (例如 scrollTop = 2200px -> 第 50 行附近)
  const winScrolled = mod.calculateVirtualWindow(
    allExpandRes.flatRows.length,
    2200,
    440,
    mod.ACCOUNT_ITEM_HEIGHT,
    mod.ACCOUNT_BUFFER_ROWS
  );
  assert.ok(winScrolled.startIndex > 0, '向下滚动后顶部必须产生虚拟索引与 Spacer');
  assert.ok(winScrolled.topSpacerHeight > 0, '顶部占位高度大于 0');
  const renderedRowCount = winScrolled.endIndex - winScrolled.startIndex;
  assert.ok(renderedRowCount <= 25, `切片渲染行数 (${renderedRowCount}) 必须严格小于等于 25 行`);

  // 5. 测试全部折叠重置 (Collapse All Reset)
  expandedSet.clear();
  const resetRes = mod.flattenAccountEntries(mockAccountsData, expandedSet);
  assert.equal(resetRes.flatRows.length, 229, '全部折叠后平铺行立即恢复为 229 行');

  // 6. 测试 Teardown 与 DOM 彻底清理
  mod.clearAccountsVirtualTable();
  assert.equal(mockTableBody.innerHTML, '', 'clearAccountsVirtualTable 必须彻底清空 DOM');

  console.log('PASS: dashboardAccountStats 虚拟列表平铺、折叠状态流转与内存控制测试全部通过！');
} finally {
  // 清理临时产物
  rmSync(tmp, { recursive: true, force: true });
}
