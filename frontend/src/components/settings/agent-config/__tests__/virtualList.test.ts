// virtualList.test.ts: 验证模型映射虚拟列表的数学窗口切片算法、边界与响应式切片。
// 运行命令: npx tsx frontend/src/components/settings/agent-config/__tests__/virtualList.test.ts

import assert from 'node:assert';
import { ref, computed } from 'vue';
import { calculateVirtualWindow, useVirtualList } from '../../../../ui/useVirtualList';

console.log('=== 开始运行 virtualList 虚拟列表单元测试 ===\n');

// 统一 Teardown 清理钩子
const cleanups: (() => void)[] = [];
function addCleanup(fn: () => void) {
  cleanups.push(fn);
}

try {
  // ----------------------------------------------------
  // 测试 1: 基础初始切片计算（顶部 scrollTop = 0）
  // ----------------------------------------------------
  console.log('测试 1: 顶部初始状态切片与占位计算...');
  {
    const totalCount = 100;
    const itemHeight = 48;
    const viewportHeight = 420;
    const buffer = 5;

    const res = calculateVirtualWindow(totalCount, 0, viewportHeight, itemHeight, buffer);

    assert.strictEqual(res.totalHeight, 4800, '总高度应为 100 * 48 = 4800');
    assert.strictEqual(res.startIndex, 0, '顶部开始索引应为 0');
    // rawEnd = ceil(420 / 48) = 9; endIndex = min(100, 9 + 5) = 14
    assert.strictEqual(res.endIndex, 14, '结束索引应为 14');
    assert.strictEqual(res.topSpacerHeight, 0, '顶部占位高度应为 0');
    assert.strictEqual(res.bottomSpacerHeight, (100 - 14) * 48, '底部占位高度应为 4128');

    // 核心物理守恒: topSpacer + renderedHeight + bottomSpacer === totalHeight
    const renderedHeight = (res.endIndex - res.startIndex) * itemHeight;
    assert.strictEqual(
      res.topSpacerHeight + renderedHeight + res.bottomSpacerHeight,
      res.totalHeight,
      '顶部占位 + 渲染高度 + 底部占位 必须严格等于总高度'
    );
    console.log('✓ 测试 1 通过 (顶部占位=0px, 渲染行数=14, 底部占位=4128px, 总高=4800px)');
  }

  // ----------------------------------------------------
  // 测试 2: 中段滚动切片计算（scrollTop = 960px）
  // ----------------------------------------------------
  console.log('\n测试 2: 中段滚动切片计算...');
  {
    const totalCount = 100;
    const itemHeight = 48;
    const viewportHeight = 420;
    const buffer = 5;
    const scrollTop = 960; // 960 / 48 = 20 行

    const res = calculateVirtualWindow(totalCount, scrollTop, viewportHeight, itemHeight, buffer);

    // rawStart = 20 -> startIndex = max(0, 20 - 5) = 15
    // rawEnd = ceil((960 + 420) / 48) = ceil(1380 / 48) = 29 -> endIndex = min(100, 29 + 5) = 34
    assert.strictEqual(res.startIndex, 15, '中段起始索引应为 15');
    assert.strictEqual(res.endIndex, 34, '中段结束索引应为 34');
    assert.strictEqual(res.topSpacerHeight, 15 * 48, '顶部占位高度应为 15 * 48 = 720');
    assert.strictEqual(res.bottomSpacerHeight, (100 - 34) * 48, '底部占位高度应为 66 * 48 = 3168');

    const renderedCount = res.endIndex - res.startIndex;
    assert.strictEqual(renderedCount, 19, '渲染行数应为 19 行');
    assert.strictEqual(
      res.topSpacerHeight + renderedCount * itemHeight + res.bottomSpacerHeight,
      res.totalHeight,
      '滚动中段高度守恒验证'
    );
    console.log('✓ 测试 2 通过 (顶部占位=720px, 渲染行数=19, 底部占位=3168px, 高度守恒)');
  }

  // ----------------------------------------------------
  // 测试 3: 触底滚动切片计算（极限底部）
  // ----------------------------------------------------
  console.log('\n测试 3: 触底滚动切片计算（无负数与边界越界）...');
  {
    const totalCount = 100;
    const itemHeight = 48;
    const viewportHeight = 420;
    const buffer = 5;
    const scrollTop = 4800 - 420; // 4380px

    const res = calculateVirtualWindow(totalCount, scrollTop, viewportHeight, itemHeight, buffer);

    // rawStart = floor(4380 / 48) = 91 -> startIndex = 91 - 5 = 86
    // rawEnd = ceil(4800 / 48) = 100 -> endIndex = 100
    assert.strictEqual(res.endIndex, 100, '触底时结束索引必须恰好为 100');
    assert.strictEqual(res.bottomSpacerHeight, 0, '触底时底部占位高度必须严格为 0');
    assert.strictEqual(res.startIndex, 86, '起始索引应为 86');
    assert.strictEqual(res.topSpacerHeight, 86 * 48, '顶部占位应为 86 * 48 = 4128');

    const renderedCount = res.endIndex - res.startIndex;
    assert.strictEqual(
      res.topSpacerHeight + renderedCount * itemHeight + res.bottomSpacerHeight,
      res.totalHeight,
      '触底高度守恒验证'
    );
    console.log('✓ 测试 3 通过 (触底时底部占位严格归零，无越界)');
  }

  // ----------------------------------------------------
  // 测试 4: 边缘情况测试（0条、少量数据、负滚动）
  // ----------------------------------------------------
  console.log('\n测试 4: 边缘情况与容错测试...');
  {
    // 4.1 空数据
    const emptyRes = calculateVirtualWindow(0, 0, 420, 48, 5);
    assert.strictEqual(emptyRes.startIndex, 0);
    assert.strictEqual(emptyRes.endIndex, 0);
    assert.strictEqual(emptyRes.topSpacerHeight, 0);
    assert.strictEqual(emptyRes.bottomSpacerHeight, 0);
    assert.strictEqual(emptyRes.totalHeight, 0);

    // 4.2 少量数据（2条，总高 96px < 视口 420px）
    const smallRes = calculateVirtualWindow(2, 0, 420, 48, 5);
    assert.strictEqual(smallRes.startIndex, 0);
    assert.strictEqual(smallRes.endIndex, 2);
    assert.strictEqual(smallRes.topSpacerHeight, 0);
    assert.strictEqual(smallRes.bottomSpacerHeight, 0);
    assert.strictEqual(smallRes.totalHeight, 96);

    // 4.3 负滚动值 (iOS 或惯性回弹负滚动)
    const negativeRes = calculateVirtualWindow(50, -100, 420, 48, 5);
    assert.strictEqual(negativeRes.startIndex, 0, '负滚动值安全夹回 0');
    assert.strictEqual(negativeRes.topSpacerHeight, 0);

    console.log('✓ 测试 4 通过 (空数据、小数据集、负值越界完全鲁棒)');
  }

  // ----------------------------------------------------
  // 测试 5: 响应式 Composable 实例调度与数据切片测试
  // ----------------------------------------------------
  console.log('\n测试 5: 响应式 useVirtualList 调度与切片测试...');
  {
    const mockList = ref(
      Array.from({ length: 84 }, (_, i) => ({
        clientModel: `opencode/model-${i + 1}`,
        targetModel: `model-${i + 1}`,
        expose: true,
      }))
    );

    const vl = useVirtualList({
      items: mockList,
      itemHeight: 48,
      buffer: 5,
      defaultViewportHeight: 420,
    });

    addCleanup(() => {
      mockList.value = [];
    });

    // 初始状态
    assert.strictEqual(vl.totalHeight.value, 84 * 48, '总高应为 84 * 48 = 4032');
    assert.strictEqual(vl.visibleItems.value.length, 14, '初始切片数量应为 14');
    assert.strictEqual(vl.visibleItems.value[0].clientModel, 'opencode/model-1');
    assert.strictEqual(vl.visibleItems.value[13].clientModel, 'opencode/model-14');

    // 模拟滚动到 480px (跳过 10 行)
    vl.scrollTop.value = 480;
    // rawStart = 10 -> startIndex = 10 - 5 = 5
    // rawEnd = ceil((480 + 420) / 48) = 19 -> endIndex = 19 + 5 = 24
    assert.strictEqual(vl.startIndex.value, 5);
    assert.strictEqual(vl.endIndex.value, 24);
    assert.strictEqual(vl.visibleItems.value[0].clientModel, 'opencode/model-6'); // index 5 -> 第6项

    // 模拟动态数据过滤 (搜索出 3 条)
    mockList.value = [
      { clientModel: 'opencode/free-1', targetModel: 'free-1', expose: true },
      { clientModel: 'opencode/free-2', targetModel: 'free-2', expose: true },
      { clientModel: 'opencode/free-3', targetModel: 'free-3', expose: true },
    ];

    assert.strictEqual(vl.totalHeight.value, 3 * 48);
    assert.strictEqual(vl.visibleItems.value.length, 3);
    assert.strictEqual(vl.topSpacerHeight.value, 0);
    assert.strictEqual(vl.bottomSpacerHeight.value, 0);

    console.log('✓ 测试 5 通过 (响应式切片、动态搜索过滤自适应)');
  }

  // ----------------------------------------------------
  // 测试 6: Teardown 沙箱隔离清理
  // ----------------------------------------------------
  console.log('\n测试 6: Teardown 沙箱隔离与资源回收...');
  {
    while (cleanups.length > 0) {
      const fn = cleanups.pop();
      if (fn) fn();
    }
    assert.strictEqual(cleanups.length, 0, '清理队列必须彻底为空');
    console.log('✓ 测试 6 通过 (测试沙箱数据已全部清理，零污染)');
  }

  console.log('\n>>> virtualList 虚拟列表单元测试全部通过！ <<<');
} catch (err) {
  console.error('\n❌ 单元测试执行失败:', err);
  process.exit(1);
} finally {
  while (cleanups.length > 0) {
    const fn = cleanups.pop();
    if (fn) fn();
  }
}
