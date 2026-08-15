const mockLocalStorage = {
    getItem: () => null,
    setItem: () => {},
    removeItem: () => {},
    clear: () => {}
};

// Mock the window object for Node.js environment before importing chartRenderer.ts
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
    const { formatTokenCount } = await import('./chartRenderer');
    console.log("=== 开始运行 formatTokenCount 单元测试 ===");

    // 1. 中文测试用例
    assertEqual(formatTokenCount(14991576841, { lang: 'zh' }), "149.92 亿", "中文模式: 149.91亿+ 格式化");
    assertEqual(formatTokenCount(100000000, { lang: 'zh' }), "1 亿", "中文模式: 恰好1亿 (去除末尾.00)");
    assertEqual(formatTokenCount(150000000, { lang: 'zh' }), "1.5 亿", "中文模式: 1.5亿 (去除末尾多余0)");
    assertEqual(formatTokenCount(45678900, { lang: 'zh' }), "4567.89 万", "中文模式: 4567.89万");
    assertEqual(formatTokenCount(520000, { lang: 'zh' }), "52 万", "中文模式: 52万");
    assertEqual(formatTokenCount(10000, { lang: 'zh' }), "1 万", "中文模式: 恰好1万");
    assertEqual(formatTokenCount(8923, { lang: 'zh' }), "8,923", "中文模式: 小于1万显示千分位");
    assertEqual(formatTokenCount(0, { lang: 'zh' }), "0", "中文模式: 0值");

    // 2. 英文测试用例
    assertEqual(formatTokenCount(14991576841, { lang: 'en' }), "14.99 B", "英文模式: 14.99B 格式化");
    assertEqual(formatTokenCount(1000000000, { lang: 'en' }), "1 B", "英文模式: 恰好1B");
    assertEqual(formatTokenCount(150000000, { lang: 'en' }), "150 M", "英文模式: 150M");
    assertEqual(formatTokenCount(45678900, { lang: 'en' }), "45.68 M", "英文模式: 45.68M");
    assertEqual(formatTokenCount(520000, { lang: 'en' }), "520 k", "英文模式: 520k");
    assertEqual(formatTokenCount(1000, { lang: 'en' }), "1 k", "英文模式: 恰好1k");
    assertEqual(formatTokenCount(823, { lang: 'en' }), "823", "英文模式: 小于1k显示普通整数");
    assertEqual(formatTokenCount(0, { lang: 'en' }), "0", "英文模式: 0值");

    // 3. 边界异常值测试
    assertEqual(formatTokenCount(-100 as any), "0", "异常值: 负数兜底返回0");
    assertEqual(formatTokenCount(NaN as any), "0", "异常值: NaN兜底返回0");
    assertEqual(formatTokenCount(null as any), "0", "异常值: null兜底返回0");

    console.log("=== 所有 formatTokenCount 测试用例全部通过！ ===");
}

runTests().catch(err => {
    console.error(err);
    process.exit(1);
});
