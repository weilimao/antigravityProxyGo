// mergeRetryRows.ts 的纯逻辑冒烟测试(无 DOM 依赖;dashboardLogs.ts 用 esbuild 打包时,
// import state from './dashboardState' 会触发 DOM/localStorage 存取,本文件用 stub 替换
// dashboardLogs.ts 依赖,仅提取 mergeRetryRows 纯函数源码执行,跑 5 个用例断言通过即 OK)。
//
// 运行: node frontend/src/ui/mergeRetryRows.test.mjs

import { build } from 'esbuild';
import { fileURLToPath } from 'node:url';
import { dirname, resolve as pathResolve } from 'node:path';
import { writeFileSync, mkdtempSync } from 'node:fs';
import { pathToFileURL } from 'node:url';
import { tmpdir } from 'node:os';

const __dirname = dirname(fileURLToPath(import.meta.url));
const SRC = pathResolve(__dirname, 'dashboardLogs.ts');

// 用 esbuild 打包 dashboardLogs.ts,stub 掉两个有 DOM 副作用的 import,仅保留 mergeRetryRows。
const result = await build({
    entryPoints: [SRC],
    bundle: true,
    format: 'esm',
    write: false,
    platform: 'browser',
    logLevel: 'silent',
    plugins: [{
        name: 'stub-dom-deps',
        setup(b) {
            // dashboardState / dashboardUtils 都有 DOM/localStorage 副作用,stub 掉。
            // 统一 namespace=stub,单个 onLoad 按 path 分发,避免多个 onLoad 因 filter=/.*/
            // 互相覆盖(后者会吞掉 formatDuration 的导出)。
            const stubs = {
                dashboardState: 'export default { currentLanguage: "zh" };',
                dashboardUtils: 'export function formatDuration(m){return (m===undefined||m===null||typeof m!=="number"||isNaN(m)||m<0)?"-":(m<1000?m+"ms":(m/1000).toFixed(2)+"s")}; export function formatDisplayModel(m, r){return !m?"-":(r&&r!=="none"?`${m}(${r})`:m);};',
            };
            b.onResolve({ filter: /^\.\/dashboard(State|Utils)$/ }, args => ({
                path: args.path, namespace: 'stub',
            }));
            b.onLoad({ filter: /.*/, namespace: 'stub' }, args => {
                const key = args.path.endsWith('dashboardState') ? 'dashboardState'
                    : args.path.endsWith('dashboardUtils') ? 'dashboardUtils' : null;
                return { contents: key ? stubs[key] : 'export default {}', loader: 'ts' };
            });
        },
    }],
});

// 落盘到 tmp,以 ESM import 注入。
const tmpDir = mkdtempSync(pathResolve(tmpdir(), 'retry-test-'));
const outPath = pathResolve(tmpDir, 'bundle.mjs');
writeFileSync(outPath, result.outputFiles[0].text);
const { mergeRetryRows } = await import(pathToFileURL(outPath).href);

let pass = 0, fail = 0;
function eq(name, got, want) {
    const g = JSON.stringify(got), w = JSON.stringify(want);
    if (g === w) { pass++; console.log(`  ✓ ${name}`); }
    else { fail++; console.error(`  ✗ ${name}\n    got:  ${g}\n    want: ${w}`); }
}

const base = {
    method: 'POST', host: 'daily-cloudcode-pa.googleapis.com',
    path: '/v1internal:streamGenerateContent', model: 'gemini-3.6-flash-high',
    sessionId: 'auth:acc:4c91d49', inTokens: 164285, outTokens: 357,
    cost: 0.070777, statusCode: 200, cachedTokens: 158699,
};
function mk(over) { return { ...base, ...over, id: Math.random().toString(36).slice(2) }; }

console.log('mergeRetryRows 冒烟测试:');

// A: HIT+MISS 同指纹同秒 → 合成 1 行,HIT 缓存率/耗时,标 retryN=2
{
    const logs = [
        mk({ timestamp: '08/10 11:42:39', cacheStatus: 'HIT', cachedTokens: 158699, durationMs: 444, firstByteMs: 2080 }),
        mk({ timestamp: '08/10 11:42:39', cacheStatus: 'MISS', cachedTokens: 0, durationMs: 2520, firstByteMs: 2080 }),
    ];
    const r = mergeRetryRows(logs);
    eq('A 同步数', r.length, 1);
    eq('A HIT 优先', r[0].cacheStatus, 'HIT');
    eq('A 取命中缓存率字段', r[0].cachedTokens, 158699);
    eq('A 取命中耗时(444ms)', r[0].durationMs, 444);
    eq('A 标记 retryN=2', r[0].__retryN, 2);
}

// B: 仅 MISS → 1 行,无 retryN
{
    const logs = [mk({ timestamp: '08/10 11:42:39', cacheStatus: 'MISS', cachedTokens: 0 })];
    const r = mergeRetryRows(logs);
    eq('B 独条直推', r.length, 1);
    eq('B 无 retryN', r[0].__retryN, undefined);
    eq('B 保持 MISS', r[0].cacheStatus, 'MISS');
}

// C: HIT+HIT 同秒 → 合成 1 行 retryN=2(多次命中重试也合并)
{
    const logs = [
        mk({ timestamp: '08/10 11:42:31', cacheStatus: 'HIT', cachedTokens: 159000, outTokens: 103 }),
        mk({ timestamp: '08/10 11:42:31', cacheStatus: 'HIT', cachedTokens: 159000, outTokens: 103 }),
    ];
    const r = mergeRetryRows(logs);
    eq('C 同步合成', r.length, 1);
    eq('C retryN=2', r[0].__retryN, 2);
    eq('C 仍 HIT', r[0].cacheStatus, 'HIT');
}

// D: 同指纹但时间差 4s(超 ±3s 窗口)→ 2 行不合并
{
    const logs = [
        mk({ timestamp: '08/10 11:42:39', cacheStatus: 'HIT' }),
        mk({ timestamp: '08/10 11:42:35', cacheStatus: 'MISS' }), // 4s 前,超出窗口
    ];
    const r = mergeRetryRows(logs);
    eq('D 超窗口不合并', r.length, 2);
    eq('D 第一条无 retryN', r[0].__retryN, undefined);
}

// E: 不同 sessionId 同秒 HIT/MISS → 2 行不合并
{
    const logs = [
        mk({ timestamp: '08/10 11:42:39', sessionId: 'auth:acc:AAA', cacheStatus: 'HIT' }),
        mk({ timestamp: '08/10 11:42:39', sessionId: 'auth:acc:BBB', cacheStatus: 'MISS', cachedTokens: 0 }),
    ];
    const r = mergeRetryRows(logs);
    eq('E 异指纹不合并', r.length, 2);
}

// F: formatDuration 边缘情况验证(0ms/正常毫秒/秒级/异常值)
console.log('\nformatDuration 纯函数测试:');
const UTILS_SRC = pathResolve(__dirname, 'dashboardUtils.ts');
const utilsRes = await build({
    entryPoints: [UTILS_SRC],
    bundle: true,
    format: 'esm',
    write: false,
    platform: 'browser',
    logLevel: 'silent',
});
const utilsOutPath = pathResolve(tmpDir, 'bundle-utils.mjs');
writeFileSync(utilsOutPath, utilsRes.outputFiles[0].text);
const { formatDuration } = await import(pathToFileURL(utilsOutPath).href);

eq('0ms 正常格式化为 0ms(不吞成横杠)', formatDuration(0), '0ms');
eq('1ms 格式化为 1ms', formatDuration(1), '1ms');
eq('500ms 格式化为 500ms', formatDuration(500), '500ms');
eq('1500ms 格式化为 1.50s', formatDuration(1500), '1.50s');
eq('undefined 兜底为 -', formatDuration(undefined), '-');
eq('null 兜底为 -', formatDuration(null), '-');
eq('负数 兜底为 -', formatDuration(-10), '-');
eq('NaN 兜底为 -', formatDuration(NaN), '-');

console.log(`\n结果: ${pass} 通过 / ${fail} 失败`);
process.exit(fail ? 1 : 0);

