/**
 * 离线断言脚本：验证 usageDetails.ts 新增纯函数的核心逻辑。
 * 不依赖任何测试框架，直接 import 纯函数逐项断言。
 *
 * 运行（必须带 --import preload，否则 dashboardState 顶层访问 localStorage 会失败）：
 *   npx tsx --import ./scripts/verify-usage-tabs.preload.ts scripts/verify-usage-tabs.ts
 */

import {
    normalizeProvider,
    computeProviderCounts,
    filterByTab,
    computeTabSummary,
} from '../src/ui/usageDetails';

let pass = 0;
let fail = 0;
function assert(name: string, actual: unknown, expected: unknown) {
    const a = JSON.stringify(actual);
    const e = JSON.stringify(expected);
    if (a === e) {
        pass++;
        console.log(`  ✓ ${name}`);
    } else {
        fail++;
        console.error(`  ✗ ${name}\n      expected=${e}\n      actual=${a}`);
    }
}

// 样例账号：覆盖 antigravity / project / nvidia / direct（含空 provider 兜底）
const sampleAccounts = [
    { email: 'a@x.com', provider: 'antigravity', requestCount: 10, inputTokens: 100, cachedTokens: 20, cacheHitRequests: 2, totalCost: 1.2 },
    { email: 'b@x.com', provider: 'antigravity', requestCount: 5, inputTokens: 50, cachedTokens: 0, cacheHitRequests: 0, totalCost: 0.6 },
    { email: 'c@x.com', provider: 'project', requestCount: 8, inputTokens: 80, cachedTokens: 40, cacheHitRequests: 4, totalCost: 0.9 },
    { email: 'd@x.com', provider: 'nvidia', requestCount: 3, inputTokens: 30, cachedTokens: 5, cacheHitRequests: 1, totalCost: 0.3 },
    { email: 'direct@x.com', provider: '', requestCount: 2, inputTokens: 20, cachedTokens: 0, cacheHitRequests: 0, totalCost: 0.1 },
    { email: 'weird@x.com', provider: 'unknown', requestCount: 1, inputTokens: 10, cachedTokens: 0, cacheHitRequests: 0, totalCost: 0.05 },
];

console.log('normalizeProvider:');
assert('空串兜底为 direct', normalizeProvider(''), 'direct');
assert('unknown 兜底为 direct', normalizeProvider('unknown'), 'direct');
assert('null 兜底为 direct', normalizeProvider(null), 'direct');
assert('antigravity 保留', normalizeProvider('antigravity'), 'antigravity');
assert('未知 provider 归入 direct', normalizeProvider('gemini-cli'), 'direct');
assert('trim 空白', normalizeProvider('  nvidia  '), 'nvidia');

console.log('computeProviderCounts:');
assert('provider 分布计数', computeProviderCounts(sampleAccounts), {
    antigravity: 2,
    project: 1,
    nvidia: 1,
    direct: 2,
});

console.log('filterByTab:');
assert('all 返回全集', filterByTab(sampleAccounts, 'all').length, 6);
assert('antigravity 仅 2 条', filterByTab(sampleAccounts, 'antigravity').length, 2);
assert('direct 合并空+unknown 共 2 条', filterByTab(sampleAccounts, 'direct').length, 2);
assert('nvidia 仅 1 条', filterByTab(sampleAccounts, 'nvidia').length, 1);

console.log('computeTabSummary:');
const agSummary = computeTabSummary(filterByTab(sampleAccounts, 'antigravity'));
assert('antigravity requestCount 累加', agSummary.requestCount, 15);
assert('antigravity inputTokens 累加', agSummary.inputTokens, 150);
assert('antigravity cachedTokens 累加', agSummary.cachedTokens, 20);
assert('antigravity cacheHitRequests 累加', agSummary.cacheHitRequests, 2);
assert('antigravity totalCost 累加', Math.round(agSummary.totalCost * 1000) / 1000, 1.8);

const directSummary = computeTabSummary(filterByTab(sampleAccounts, 'direct'));
assert('direct requestCount 累加', directSummary.requestCount, 3);
assert('direct totalCost 累加', Math.round(directSummary.totalCost * 1000) / 1000, 0.15);

const emptySummary = computeTabSummary([]);
assert('空集 summary 全 0', emptySummary, {
    requestCount: 0, inputTokens: 0, cachedTokens: 0, cacheHitRequests: 0, totalCost: 0,
});

console.log(`\n总计：${pass} 通过，${fail} 失败。`);
if (fail > 0) process.exit(1);
