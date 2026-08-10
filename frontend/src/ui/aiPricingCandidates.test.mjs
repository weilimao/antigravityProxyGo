// aiPricingCandidates.ts 的纯逻辑冒烟测试(无 DOM 依赖;aiPricingCandidates.ts
// 自身不 import 任何有副作用的模块,但为与 mergeRetryRows.test.mjs 同款保持 esbuild
// 打包隔离,这里仍走打包→tmp→ESM import 路径提取纯函数执行)。
//
// 运行: node frontend/src/ui/aiPricingCandidates.test.mjs

import { build } from 'esbuild';
import { fileURLToPath } from 'node:url';
import { dirname, resolve as pathResolve } from 'node:path';
import { writeFileSync, mkdtempSync } from 'node:fs';
import { pathToFileURL } from 'node:url';
import { tmpdir } from 'node:os';

const __dirname = dirname(fileURLToPath(import.meta.url));
const SRC = pathResolve(__dirname, 'aiPricingCandidates.ts');

const result = await build({
    entryPoints: [SRC],
    bundle: true,
    format: 'esm',
    write: false,
    platform: 'browser',
    logLevel: 'silent',
});

const tmpDir = mkdtempSync(pathResolve(tmpdir(), 'aipricing-test-'));
const outPath = pathResolve(tmpDir, 'bundle.mjs');
writeFileSync(outPath, result.outputFiles[0].text);
const { computeAiPricingCandidates } = await import(pathToFileURL(outPath).href);

let pass = 0, fail = 0;
function eq(name, got, want) {
    const g = JSON.stringify(got), w = JSON.stringify(want);
    if (g === w) { pass++; console.log(`  ✓ ${name}`); }
    else { fail++; console.error(`  ✗ ${name}\n    got:  ${g}\n    want: ${w}`); }
}

console.log('computeAiPricingCandidates 冒烟测试:');

// 1. z-ai/glm-5.2 已登记 glm-5.2 → 排除
{
    const got = computeAiPricingCandidates(
        { 'z-ai/glm-5.2': {}, 'deepseek-v4-flash': {} },
        { 'glm-5.2': { input: 1.4 } }
    );
    eq('已登记基名前缀模型被排除', got, ['deepseek-v4-flash']);
}

// 2. deepseek-ai/deepseek-v4-flash 未登记 → 产出基名
{
    const got = computeAiPricingCandidates(
        { 'deepseek-ai/deepseek-v4-flash': {}, 'deepseek-v4-flash': {} },
        {}
    );
    eq('带前缀取基名 + 去重', got, ['deepseek-v4-flash']);
}

// 3. unknown → 排除
{
    const got = computeAiPricingCandidates(
        { 'unknown': {}, 'gemini-3-flash': {} },
        {}
    );
    eq('unknown 排除', got, ['gemini-3-flash']);
}

// 4. a/b/x 与 c/b/x → 单个 x
{
    const got = computeAiPricingCandidates(
        { 'a/b/x': {}, 'c/b/x': {} },
        {}
    );
    eq('多层前缀合并去重', got, ['x']);
}

// 5. 已登记键大小写不一 → 小写比对命中后排除
{
    const got = computeAiPricingCandidates(
        { 'GLM-5.2': {} },
        { 'glm-5.2': { input: 1.4 } }
    );
    eq('登记键大小写差异小写比对命中', got, []);
}

// 6. 空统计表 → 空数组
{
    const got = computeAiPricingCandidates(null, {});
    eq('null statsModels 返回空', got, []);
    const got2 = computeAiPricingCandidates({}, null);
    eq('null pricing 返回空', got2, []);
}

// 7. 字典序排序
{
    const got = computeAiPricingCandidates(
        { 'zeta': {}, 'alpha': {}, 'mid': {} },
        {}
    );
    eq('字典序排序', got, ['alpha', 'mid', 'zeta']);
}

console.log(`\n${pass} passed, ${fail} failed`);
if (fail > 0) process.exit(1);
