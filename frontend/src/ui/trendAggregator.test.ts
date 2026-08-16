import assert from 'node:assert';
import {
    getTrendGranularity,
    generateHourlySlots,
    generateTodaySlots,
    generateDailySlots,
    generateCustomSlots,
    getFilteredTrends,
    formatTrendsDayTime,
    formatTrendsHourTime,
    parseTrendsTime,
    TrendItem
} from './trendAggregator';

function runTests() {
    console.log('[Test] Running trendAggregator unit tests...');

    // 1. 粒度判断测试
    assert.strictEqual(getTrendGranularity('today'), 'hour', 'today should be hourly');
    assert.strictEqual(getTrendGranularity('24h'), 'hour', '24h should be hourly');
    assert.strictEqual(getTrendGranularity('3d'), 'hour', '3d should be hourly');
    assert.strictEqual(getTrendGranularity('7d'), 'day', '7d should be daily');
    assert.strictEqual(getTrendGranularity('30d'), 'day', '30d should be daily');

    const nowMs = Date.now();
    const oneDayAgo = nowMs - 24 * 3600 * 1000;
    const tenDaysAgo = nowMs - 10 * 24 * 3600 * 1000;
    assert.strictEqual(getTrendGranularity('custom', oneDayAgo, nowMs), 'hour', 'custom <= 48h should be hourly');
    assert.strictEqual(getTrendGranularity('custom', tenDaysAgo, nowMs), 'day', 'custom > 48h should be daily');
    console.log('✓ Granularity detection passed');

    // 2. 插槽生成测试
    const hourly24 = generateHourlySlots(24);
    assert.strictEqual(hourly24.length, 24, '24h should generate 24 slots');
    assert.match(hourly24[0], /^\d{2}\/\d{2} \d{2}:00$/, 'Hourly slot format should be MM/DD HH:00');

    const daily7 = generateDailySlots(7);
    assert.strictEqual(daily7.length, 7, '7d should generate 7 slots');
    assert.match(daily7[0], /^\d{2}\/\d{2}$/, 'Daily slot format should be MM/DD');

    const daily30 = generateDailySlots(30);
    assert.strictEqual(daily30.length, 30, '30d should generate 30 slots');
    assert.match(daily30[0], /^\d{2}\/\d{2}$/, 'Daily slot format should be MM/DD');
    console.log('✓ Slot generation count and format passed');

    // 3. 数据聚合与求和守恒性测试
    const mockTrends: TrendItem[] = [];
    const testAnchor = new Date(2026, 7, 16, 18, 0, 0); // 2026-08-16 18:00
    let expectedTotalInput = 0;
    let expectedTotalOutput = 0;
    let expectedTotalCached = 0;
    let expectedTotalRequests = 0;
    let expectedTotalCost = 0;

    // 构造过去 720 个小时的部分数据（每隔几小时有请求）
    for (let i = 0; i < 720; i++) {
        if (i % 3 === 0) {
            const t = new Date(testAnchor.getTime() - i * 3600 * 1000);
            const item: TrendItem = {
                time: formatTrendsHourTime(t),
                input: 1000 + i * 10,
                output: 200 + i * 5,
                cached: 100 + i * 2,
                requests: 1 + (i % 4),
                cost: 0.005 + (i * 0.0001),
                inputCost: 0.002,
                outputCost: 0.002,
                cachedCost: 0.001
            };
            mockTrends.push(item);

            // 仅统计在过去 30 天每日 slot 覆盖范围内的数据
            const dayKey = item.time.split(' ')[0];
            if (daily30.includes(dayKey)) {
                expectedTotalInput += item.input;
                expectedTotalOutput += item.output;
                expectedTotalCached += item.cached;
                expectedTotalRequests += item.requests;
                expectedTotalCost = Math.round((expectedTotalCost + item.cost) * 1000000) / 1000000;
            }
        }
    }

    const filtered30d = getFilteredTrends(mockTrends, '30d');
    assert.strictEqual(filtered30d.length, 30, 'Filtered 30d should have exactly 30 items');

    let actualTotalInput = 0;
    let actualTotalOutput = 0;
    let actualTotalCached = 0;
    let actualTotalRequests = 0;
    let actualTotalCost = 0;

    filtered30d.forEach(d => {
        actualTotalInput += d.input;
        actualTotalOutput += d.output;
        actualTotalCached += d.cached;
        actualTotalRequests += d.requests;
        actualTotalCost = Math.round((actualTotalCost + d.cost) * 1000000) / 1000000;
    });

    assert.strictEqual(actualTotalInput, expectedTotalInput, 'Total input tokens must match exactly');
    assert.strictEqual(actualTotalOutput, expectedTotalOutput, 'Total output tokens must match exactly');
    assert.strictEqual(actualTotalCached, expectedTotalCached, 'Total cached tokens must match revival');
    assert.strictEqual(actualTotalRequests, expectedTotalRequests, 'Total requests count must match');
    assert.strictEqual(actualTotalCost, expectedTotalCost, 'Total cost must match exactly');
    console.log('✓ 30d Daily aggregation and mathematical conservation passed');

    // 4. 空数据与补零测试
    const emptyResult = getFilteredTrends([], '30d');
    assert.strictEqual(emptyResult.length, 30, 'Empty input should yield 30 zero-filled items');
    assert.strictEqual(emptyResult[0].requests, 0);
    assert.strictEqual(emptyResult[0].cost, 0);

    const nullResult = getFilteredTrends(null as any, '7d');
    assert.strictEqual(nullResult.length, 7, 'Null input should yield 7 zero-filled items');
    console.log('✓ Empty & null fallback passed');

    // 5. 24h 小时粒度测试
    const filtered24h = getFilteredTrends(mockTrends, '24h');
    assert.strictEqual(filtered24h.length, 24, '24h range should retain 24 hourly bins');
    console.log('✓ 24h hourly retention passed');

    console.log('All trendAggregator tests passed successfully!');
}

runTests();
