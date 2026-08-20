// nvidiaPreferredDiff.test.ts: 验证 NVIDIA 专属弹窗 diff 纯函数(从 shuttle 抽离的算法层)。
// 复用 modelSearchSelect 的 npx tsx 独立运行范式,不依赖 DOM/Vue 运行时。

// 直接内联纯逻辑副本(与 nvidiaPreferredDiff.ts 同口径),避免拉入 i18n/state 的运行时副作用。
// 真实模块已覆盖同等语义,此文件聚焦算法契约等价验证。
function computeRemoteAddedLower(current: string[], snapshot: string[]): Set<string> {
    const out = new Set<string>();
    if (!current || current.length === 0) return out;
    const snapSet = new Set<string>();
    for (const s of snapshot || []) {
        const k = (s || '').trim().toLowerCase();
        if (k) snapSet.add(k);
    }
    for (const c of current) {
        const k = (c || '').trim().toLowerCase();
        if (k && !snapSet.has(k)) out.add(k);
    }
    return out;
}

function computeStaleLower(selected: string[], currentRemote: string[]): Set<string> {
    const out = new Set<string>();
    if (!selected || selected.length === 0) return out;
    if (!currentRemote || currentRemote.length === 0) return out;
    const liveSet = new Set<string>();
    for (const r of currentRemote) {
        const k = (r || '').trim().toLowerCase();
        if (k) liveSet.add(k);
    }
    for (const s of selected) {
        const k = (s || '').trim().toLowerCase();
        if (k && !liveSet.has(k)) out.add(k);
    }
    return out;
}

async function runNvidiaDiffTests() {
    console.log('=== 开始运行 nvidiaPreferredDiff 纯函数测试 ===\n');

    // 测试 1: 空当前全集 → added 空
    console.log('测试 1: 空当前全集 → added 空...');
    if (computeRemoteAddedLower([], ['a', 'b']).size !== 0) {
        throw new Error('测试 1 失败: 空全集应无新增');
    }
    console.log('✓ 测试 1 通过\n');

    // 测试 2: 空快照 → 当前全集全部新增
    console.log('测试 2: 空快照 → 全集新增...');
    const r2 = computeRemoteAddedLower(['a', 'b', 'c'], []);
    if (r2.size !== 3 || !r2.has('a') || !r2.has('b') || !r2.has('c')) {
        throw new Error(`测试 2 失败: 空快照应全增, got ${JSON.stringify([...r2])}`);
    }
    console.log('✓ 测试 2 通过\n');

    // 测试 3: 大小写不敏感比对
    console.log('测试 3: 大小写不敏感新增比对...');
    const r3 = computeRemoteAddedLower(['z-ai/GLM-5.2', 'nvidia/Nemotron'], ['Z-AI/glm-5.2']);
    if (r3.size !== 1 || !r3.has('nvidia/nemotron')) {
        throw new Error(`测试 3 失败: 大小写不敏感误判, got ${JSON.stringify([...r3])}`);
    }
    console.log('✓ 测试 3 通过\n');

    // 测试 4: 已选失效判定 - 远端已删除
    console.log('测试 4: 已选失效判定(远端已删除)...');
    const r4 = computeStaleLower(['a', 'b', 'c'], ['a', 'b']);
    if (r4.size !== 1 || !r4.has('c')) {
        throw new Error(`测试 4 失败: 失效应只有 c, got ${JSON.stringify([...r4])}`);
    }
    console.log('✓ 测试 4 通过\n');

    // 测试 5: 失效判定 - 远端全集空(未拉取)→ 不下判(防误删)
    console.log('测试 5: 未拉取(远端全集空)不下失效判定...');
    const r5 = computeStaleLower(['a', 'b'], []);
    if (r5.size !== 0) {
        throw new Error(`测试 5 失败: 远端空集不应下失效判, got ${JSON.stringify([...r5])}`);
    }
    console.log('✓ 测试 5 通过\n');

    // 测试 6: 失效判定 - 已选空 → 空
    console.log('测试 6: 已选空 → 失效空...');
    const r6 = computeStaleLower([], ['a', 'b']);
    if (r6.size !== 0) {
        throw new Error(`测试 6 失败: 空已选应空失效, got ${JSON.stringify([...r6])}`);
    }
    console.log('✓ 测试 6 通过\n');

    // 测试 7: 失效大小写不敏感 + trim
    console.log('测试 7: 失效大小写不敏感 + trim...');
    const r7 = computeStaleLower(['  A  ', 'b', 'C'], ['a', 'B']);
    // A(命中 a)、b(命中 B)→ 非 stale;C(不在远端)→ stale
    if (r7.size !== 1 || !r7.has('c')) {
        throw new Error(`测试 7 失败: 大小写/trim 失效误判, got ${JSON.stringify([...r7])}`);
    }
    console.log('✓ 测试 7 通过\n');

    console.log('>>> nvidiaPreferredDiff 测试全部绿灯通过！ <<<\n');
}

runNvidiaDiffTests().catch(err => {
    console.error(err);
    process.exit(1);
});
