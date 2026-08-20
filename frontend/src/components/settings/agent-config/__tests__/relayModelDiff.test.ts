// relayModelDiff.test.ts: 验证中继模型映射 diff 纯函数(从 relayModelMapping 抽离的算法层)。
// 复用 npx tsx 独立运行范式,聚焦 added/stale/mark 的纯逻辑契约。

// 内联纯逻辑副本(与 relayModelDiff.ts 同口径),不拉 i18n/state 运行时。
function norm(s: string): string { return (s || '').trim().toLowerCase(); }

function computeRemoteAddedLower(current: string[], snapshot: string[]): Set<string> {
    const out = new Set<string>();
    if (!current || current.length === 0) return out;
    const snapSet = new Set<string>();
    for (const s of snapshot || []) { const k = norm(s); if (k) snapSet.add(k); }
    for (const c of current) { const k = norm(c); if (k && !snapSet.has(k)) out.add(k); }
    return out;
}

interface MappingLike { clientModel?: string; targetModel?: string; [k: string]: any; }

function computeStaleMappings(mappings: MappingLike[], liveRemote: string[]): MappingLike[] {
    if (!liveRemote || liveRemote.length === 0) return [];
    const liveSet = new Set<string>();
    for (const r of liveRemote) { const k = norm(r); if (k) liveSet.add(k); }
    const stale: MappingLike[] = [];
    for (const m of mappings || []) {
        const tm = norm((m && m.targetModel) || '');
        if (!tm) continue;
        if (!liveSet.has(tm)) stale.push(m);
    }
    return stale;
}

function shouldMarkStale(mapping: MappingLike, liveSetLower: Set<string>): boolean {
    if (!liveSetLower || liveSetLower.size === 0) return false;
    const tm = norm((mapping && mapping.targetModel) || '');
    if (!tm) return false;
    return !liveSetLower.has(tm);
}

function shouldMarkNew(targetModel: string, addedSetLower: Set<string>): boolean {
    if (!addedSetLower || addedSetLower.size === 0) return false;
    const tm = norm(targetModel || '');
    if (!tm) return false;
    return addedSetLower.has(tm);
}

function buildLiveSetLower(liveRemote: string[] | null): Set<string> {
    const out = new Set<string>();
    if (!liveRemote) return out;
    for (const r of liveRemote) { const k = norm(r); if (k) out.add(k); }
    return out;
}

async function runRelayDiffTests() {
    console.log('=== 开始运行 relayModelDiff 纯函数测试 ===\n');

    // 测试 1: computeStaleMappings 基础失效
    console.log('测试 1: computeStaleMappings 失效判定...');
    const mappings1 = [
        { clientModel: 'nvidia/glm', targetModel: 'z-ai/glm-5.2' },
        { clientModel: 'nvidia/kimi', targetModel: 'moonshotai/kimi-k2.5' },
        { clientModel: 'nvidia/old', targetModel: 'old-removed-model' },
    ];
    const live1 = ['z-ai/glm-5.2', 'moonshotai/kimi-k2.5'];
    const stale1 = computeStaleMappings(mappings1, live1);
    if (stale1.length !== 1 || stale1[0].targetModel !== 'old-removed-model') {
        throw new Error(`测试 1 失败: 失效应仅 old-removed-model, got ${JSON.stringify(stale1.map(s => s.targetModel))}`);
    }
    console.log('✓ 测试 1 通过\n');

    // 测试 2: 远端全集空 → 不下失效(防误删)
    console.log('测试 2: computeStaleMappings 远端空不判失效...');
    const stale2 = computeStaleMappings(mappings1, []);
    if (stale2.length !== 0) {
        throw new Error(`测试 2 失败: 远端空应空失效(防误删), got ${stale2.length}`);
    }
    console.log('✓ 测试 2 通过\n');

    // 测试 3: 大小写不敏感失效
    console.log('测试 3: computeStaleMappings 大小写不敏感...');
    const mappings3 = [{ clientModel: 'a', targetModel: 'Z-AI/GLM-5.2' }];
    const stale3 = computeStaleMappings(mappings3, ['z-ai/glm-5.2']);
    if (stale3.length !== 0) {
        throw new Error(`测试 3 失败: 大小写不敏感应判非失效, got ${stale3.length}`);
    }
    console.log('✓ 测试 3 通过\n');

    // 测试 4: 空 targetModel 跳过(用户未填不下判)
    console.log('测试 4: 空 targetModel 跳过失效判定...');
    const mappings4 = [{ clientModel: 'a', targetModel: '' }, { clientModel: 'b', targetModel: '   ' }];
    const stale4 = computeStaleMappings(mappings4, ['x']);
    if (stale4.length !== 0) {
        throw new Error(`测试 4 失败: 空 targetModel 不应判失效, got ${stale4.length}`);
    }
    console.log('✓ 测试 4 通过\n');

    // 测试 5: shouldMarkStale / shouldMarkNew 逐条
    console.log('测试 5: shouldMarkStale/shouldMarkNew 逐条判定...');
    const liveSet = buildLiveSetLower(['a', 'b']);
    if (!shouldMarkStale({ targetModel: 'c' }, liveSet)) throw new Error('测试 5a 失败: c 应失效');
    if (shouldMarkStale({ targetModel: 'a' }, liveSet)) throw new Error('测试 5b 失败: a 应非失效');
    if (shouldMarkStale({ targetModel: 'a' }, new Set())) throw new Error('测试 5c 失败: 空集不应失效');
    if (shouldMarkStale({ targetModel: '' }, liveSet)) throw new Error('测试 5d 失败: 空 tm 不应失效');
    const addedSet = computeRemoteAddedLower(['c', 'd'], ['c']); // d 新增
    if (!shouldMarkNew('d', addedSet)) throw new Error('测试 5e 失败: d 应标新增');
    if (shouldMarkNew('c', addedSet)) throw new Error('测试 5f 失败: c 不应标新增');
    if (shouldMarkNew('x', new Set())) throw new Error('测试 5g 失败: 空集不标新增');
    console.log('✓ 测试 5 通过\n');

    // 测试 6: buildLiveSetLower null 安全
    console.log('测试 6: buildLiveSetLower null 安全...');
    if (buildLiveSetLower(null).size !== 0) throw new Error('测试 6 失败: null 应空集');
    console.log('✓ 测试 6 通过\n');

    console.log('>>> relayModelDiff 测试全部绿灯通过！ <<<\n');
}

runRelayDiffTests().catch(err => {
    console.error(err);
    process.exit(1);
});
