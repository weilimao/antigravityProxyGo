// dashboardModelStats.test.ts: 针对控制台模型统计虚拟列表与数据排序的局部自动化单元测试
// 运行命令: npx tsx frontend/src/ui/__tests__/dashboardModelStats.test.ts

import assert from 'node:assert';

// Node.js 环境垫片: 避免 ipc.ts 与 DOM 解析器在无头 Node 环境下抛出 ReferenceError
const originalWindow = (globalThis as any).window;
const originalDocument = (globalThis as any).document;
const originalLocalStorage = (globalThis as any).localStorage;

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
if (typeof (globalThis as any).document === 'undefined') {
  (globalThis as any).document = {
    querySelector: () => null,
    getElementById: () => null,
    createElement: (tag: string) => ({
      tagName: tag.toUpperCase(),
      style: {},
      classList: {
        add: () => {},
        remove: () => {},
        toggle: () => {},
      },
      appendChild: () => {},
      setAttribute: () => {},
      hasAttribute: () => false,
      addEventListener: () => {},
    }),
    createDocumentFragment: () => ({
      appendChild: () => {},
    }),
  };
}

// 统一 Teardown 清理钩子
const cleanups: (() => void)[] = [];
function addCleanup(fn: () => void) {
  cleanups.push(fn);
}

// 注册清理环境垫片钩子
addCleanup(() => {
  if (originalWindow === undefined) {
    delete (globalThis as any).window;
  } else {
    (globalThis as any).window = originalWindow;
  }
  if (originalDocument === undefined) {
    delete (globalThis as any).document;
  } else {
    (globalThis as any).document = originalDocument;
  }
  if (originalLocalStorage === undefined) {
    delete (globalThis as any).localStorage;
  } else {
    (globalThis as any).localStorage = originalLocalStorage;
  }
});

async function runTests() {
  // 动态 import 避免模块提升导致全局 window 垫片未生效
  const {
    filterAndSortModelEntries,
    MODEL_ITEM_HEIGHT,
    MODEL_BUFFER_ROWS,
    DEFAULT_VIEWPORT_HEIGHT,
  } = await import('../dashboardModelStats');
  const { calculateVirtualWindow } = await import('../useVirtualList');

  console.log('=== 开始运行 dashboardModelStats 局部单元与业务逻辑测试 ===\n');

  try {
    // ----------------------------------------------------
    // 测试 1: 模型统计排序与异常数据过滤逻辑
    // ----------------------------------------------------
    console.log('测试 1: 验证 filterAndSortModelEntries 排序与过滤规则...');
    {
      // 空值安全保护
      assert.deepStrictEqual(filterAndSortModelEntries(null), [], 'null 输入应返回空数组');
      assert.deepStrictEqual(filterAndSortModelEntries(undefined), [], 'undefined 输入应返回空数组');
      assert.deepStrictEqual(filterAndSortModelEntries({}), [], '空对象输入应返回空数组');

      // 构造测试数据集
      const mockModels = {
        'unknown': { reqs: 0, inTokens: 0, outTokens: 0, cachedTokens: 0, cost: 0 },
        'claude-3-opus': { reqs: 10, inTokens: 500, outTokens: 500, cachedTokens: 0, cost: 0.1 },      // 总 Tokens = 1000
        'gpt-4o': { reqs: 50, inTokens: 10000, outTokens: 5000, cachedTokens: 1000, cost: 0.5 },       // 总 Tokens = 15000
        'gemini-1.5-pro': { reqs: 100, inTokens: 20000, outTokens: 10000, cachedTokens: 5000, cost: 1.0 }, // 总 Tokens = 30000
        'gemini-flash-tier1': { reqs: 200, inTokens: 1000, outTokens: 1000, cachedTokens: 0, cost: 0.05 }, // 总 Tokens = 2000, reqs = 200
        'gemini-flash-tier2': { reqs: 150, inTokens: 1000, outTokens: 1000, cachedTokens: 0, cost: 0.05 }, // 总 Tokens = 2000, reqs = 150
      };

      const sorted = filterAndSortModelEntries(mockModels);

      // 验证 unknown 且 reqs=0 被完全过滤
      assert.strictEqual(
        sorted.some(([name]) => name === 'unknown'),
        false,
        'unknown 且 reqs=0 的脏数据必须被彻底过滤'
      );
      assert.strictEqual(sorted.length, 5, '有效模型数量应为 5');

      // 验证第一名总 Tokens 最高 (gemini-1.5-pro: 30000)
      assert.strictEqual(sorted[0][0], 'gemini-1.5-pro', '第 1 名应为总 Token 最大的 gemini-1.5-pro');
      // 验证第二名 (gpt-4o: 15000)
      assert.strictEqual(sorted[1][0], 'gpt-4o', '第 2 名应为 gpt-4o');
      // 验证相同 Tokens 时次级按请求次数排序 (gemini-flash-tier1 200 > tier2 150)
      assert.strictEqual(sorted[2][0], 'gemini-flash-tier1', '相同 Token 时请求数较高的排在前');
      assert.strictEqual(sorted[3][0], 'gemini-flash-tier2', '相同 Token 时请求数较低的排在后');
      assert.strictEqual(sorted[4][0], 'claude-3-opus', '第 5 名应为 claude-3-opus');

      console.log('✓ 测试 1 通过: 过滤与按 Token/请求量多级排序符合业务规范');
    }

    // ----------------------------------------------------
    // 测试 2: 虚拟窗口在顶部初始状态的数学计算 (scrollTop = 0)
    // ----------------------------------------------------
    console.log('\n测试 2: 验证虚拟列表顶部初始切片与占位高度守恒...');
    {
      const totalCount = 80; // 模拟 80 个模型
      const itemHeight = MODEL_ITEM_HEIGHT; // 44px
      const viewportHeight = DEFAULT_VIEWPORT_HEIGHT; // 440px (约 10 行视口)
      const buffer = MODEL_BUFFER_ROWS; // 5 行缓冲

      const res = calculateVirtualWindow(totalCount, 0, viewportHeight, itemHeight, buffer);

      assert.strictEqual(res.totalHeight, 80 * 44, '总高度应为 80 * 44 = 3520');
      assert.strictEqual(res.startIndex, 0, '顶部起始索引必须为 0');
      // rawEnd = ceil(440 / 44) = 10; endIndex = min(80, 10 + 5) = 15
      assert.strictEqual(res.endIndex, 15, '结束索引应为 15');
      assert.strictEqual(res.topSpacerHeight, 0, '顶部占位必须严格为 0px');
      assert.strictEqual(res.bottomSpacerHeight, (80 - 15) * 44, '底部占位高度应为 65 * 44 = 2860px');

      const renderedHeight = (res.endIndex - res.startIndex) * itemHeight;
      assert.strictEqual(
        res.topSpacerHeight + renderedHeight + res.bottomSpacerHeight,
        res.totalHeight,
        '顶部占位 + 渲染高度 + 底部占位 必须严格等于总高度 (物理高度守恒)'
      );

      console.log('✓ 测试 2 通过: 顶部初始状态占位与高度完全守恒 (渲染行数=15, 底部占位=2860px)');
    }

    // ----------------------------------------------------
    // 测试 3: 中段滚动切片计算 (scrollTop = 880px)
    // ----------------------------------------------------
    console.log('\n测试 3: 验证滚动中段的视口切片与前后占位撑高...');
    {
      const totalCount = 80;
      const itemHeight = MODEL_ITEM_HEIGHT;
      const viewportHeight = 440;
      const buffer = 5;
      const scrollTop = 880; // 880 / 44 = 20 行

      const res = calculateVirtualWindow(totalCount, scrollTop, viewportHeight, itemHeight, buffer);

      // rawStart = 20 -> startIndex = max(0, 20 - 5) = 15
      // rawEnd = ceil((880 + 440) / 44) = ceil(1320 / 44) = 30 -> endIndex = min(80, 30 + 5) = 35
      assert.strictEqual(res.startIndex, 15, '中段起始索引应为 15');
      assert.strictEqual(res.endIndex, 35, '中段结束索引应为 35');
      assert.strictEqual(res.topSpacerHeight, 15 * 44, '顶部占位高度应为 15 * 44 = 660px');
      assert.strictEqual(res.bottomSpacerHeight, (80 - 35) * 44, '底部占位高度应为 45 * 44 = 1980px');

      const renderedCount = res.endIndex - res.startIndex;
      assert.strictEqual(renderedCount, 20, '渲染行数应为 20 行 (10 可视 + 10 缓冲)');
      assert.strictEqual(
        res.topSpacerHeight + renderedCount * itemHeight + res.bottomSpacerHeight,
        res.totalHeight,
        '中段滚动高度守恒'
      );

      console.log('✓ 测试 3 通过: 滚动中段高度完全守恒 (顶部占位=660px, 渲染行数=20, 底部占位=1980px)');
    }

    // ----------------------------------------------------
    // 测试 4: 触底极限切片计算 (scrollTop 达到最底端)
    // ----------------------------------------------------
    console.log('\n测试 4: 验证触底极限状态下的无越界与底部 0 占位...');
    {
      const totalCount = 80;
      const itemHeight = MODEL_ITEM_HEIGHT;
      const viewportHeight = 440;
      const buffer = 5;
      const maxScroll = totalCount * itemHeight - viewportHeight; // 3520 - 440 = 3080px

      const res = calculateVirtualWindow(totalCount, maxScroll, viewportHeight, itemHeight, buffer);

      // 触底时结束索引必须严格等于 totalCount
      assert.strictEqual(res.endIndex, 80, '触底时结束索引必须严格等于 80');
      assert.strictEqual(res.bottomSpacerHeight, 0, '触底时底部占位高度必须严格为 0');
      // rawStart = 3080 / 44 = 70 -> startIndex = 70 - 5 = 65
      assert.strictEqual(res.startIndex, 65, '触底起始索引应为 65');
      assert.strictEqual(res.topSpacerHeight, 65 * 44, '顶部占位高度应为 65 * 44 = 2860px');

      const renderedCount = res.endIndex - res.startIndex;
      assert.strictEqual(
        res.topSpacerHeight + renderedCount * itemHeight + res.bottomSpacerHeight,
        res.totalHeight,
        '触底高度守恒'
      );

      console.log('✓ 测试 4 通过: 触底切片精准对齐且底部占位为 0');
    }

    // ----------------------------------------------------
    // 测试 5: 边界异常自愈 (0 个模型、少于视口、超范围滚动)
    // ----------------------------------------------------
    console.log('\n测试 5: 验证极端边界保护与超范围滚动自愈...');
    {
      // 0 个模型
      const emptyRes = calculateVirtualWindow(0, 500, 440, 44, 5);
      assert.strictEqual(emptyRes.totalHeight, 0, '0 条时总高应为 0');
      assert.strictEqual(emptyRes.startIndex, 0);
      assert.strictEqual(emptyRes.endIndex, 0);
      assert.strictEqual(emptyRes.topSpacerHeight, 0);
      assert.strictEqual(emptyRes.bottomSpacerHeight, 0);

      // 仅 1 个模型 (远小于视口)
      const singleRes = calculateVirtualWindow(1, 0, 440, 44, 5);
      assert.strictEqual(singleRes.totalHeight, 44);
      assert.strictEqual(singleRes.startIndex, 0);
      assert.strictEqual(singleRes.endIndex, 1);
      assert.strictEqual(singleRes.topSpacerHeight, 0);
      assert.strictEqual(singleRes.bottomSpacerHeight, 0);

      // 超范围极大滚动 (如原在 80 项底部 scrollTop=3000，切换范围后只剩 3 项)
      const overScrollRes = calculateVirtualWindow(3, 99999, 440, 44, 5);
      assert.strictEqual(overScrollRes.startIndex, 0, '超范围滚动必须自愈夹回至安全范围');
      assert.strictEqual(overScrollRes.endIndex, 3, '超范围滚动结束索引不得超过总数 3');
      assert.strictEqual(overScrollRes.topSpacerHeight, 0);
      assert.strictEqual(overScrollRes.bottomSpacerHeight, 0);

      console.log('✓ 测试 5 通过: 0 条、单条、超范围极端场景均能完美自愈，杜绝白屏与越界');
    }

    console.log('\n========================================');
    console.log('🎉 全部局部单元测试绿灯通过！');
    console.log('========================================\n');

  } finally {
    // 统一执行 Teardown 清理钩子
    while (cleanups.length > 0) {
      const cleanup = cleanups.pop();
      try {
        cleanup?.();
      } catch (err) {
        console.error('Teardown 清理失败:', err);
      }
    }
  }
}

runTests().catch((err) => {
  console.error('测试运行异常:', err);
  process.exit(1);
});
