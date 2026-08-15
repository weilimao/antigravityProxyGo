// Test suite for usageDetails.ts token formatting
const mockLocalStorage = {
    getItem: () => null,
    setItem: () => {},
    removeItem: () => {},
    clear: () => {}
};

(global as any).localStorage = mockLocalStorage;
(global as any).window = {
    localStorage: mockLocalStorage,
    wailsConfigCache: {},
    addEventListener: () => {},
    removeEventListener: () => {}
};

function assertEqual(actual: string, expected: string, message: string) {
    if (actual !== expected) {
        throw new Error(`FAIL: ${message} - expected "${expected}", got "${actual}"`);
    }
    console.log(`PASS: ${message} => "${actual}"`);
}

async function runTests() {
    console.log("=== 开始运行 usageDetails.ts Token 格式化单元测试 ===");

    const { formatTokens, formatNumber, renderSummaryChip } = await import('./usageDetails');

    // 1. formatTokens 测试
    assertEqual(formatTokens(14874764784), "148.75 亿", "使用详情: 148.74亿+ 格式化");
    assertEqual(formatTokens(1311333340), "13.11 亿", "使用详情: 13.11亿+ 格式化");
    assertEqual(formatTokens(45678900), "4567.89 万", "使用详情: 4567.89万 格式化");
    assertEqual(formatTokens(8923), "8,923", "使用详情: 小于1万显示千分位");
    assertEqual(formatTokens(0), "0", "使用详情: 0值");

    // 2. renderSummaryChip 带有 title 的测试
    const chipHtml = renderSummaryChip("Token 总数", formatTokens(14874764784), "slate", formatNumber(14874764784));
    if (!chipHtml.includes('title="14,874,764,784"')) {
        throw new Error("FAIL: renderSummaryChip 应包含精确千分位 title");
    }
    if (!chipHtml.includes('148.75 亿')) {
        throw new Error("FAIL: renderSummaryChip 应展示 148.75 亿");
    }
    console.log("PASS: renderSummaryChip title 悬停与紧凑格式渲染正确");

    console.log("=== 所有 usageDetails Token 格式化测试全部通过！ ===");
}

runTests().catch(err => {
    console.error(err);
    process.exit(1);
});
