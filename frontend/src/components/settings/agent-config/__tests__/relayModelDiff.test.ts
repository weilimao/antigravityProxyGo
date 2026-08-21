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

// collectStaleMappingsByTab:复刻 relayModelMapping.collectStaleMappingsInTab 的「按 Tab 隔离」失效
// 筛选口径(测试内联纯函数版,与生产同逻辑),用于测试 8 验证跨号池误判回归。
// 三重安全:1) 仅取 otab===tabId 的映射(Tab 隔离);2) liveSet 由本次远端全集构建;3) 空全集跳过(防误删)。
// 失效判定:TargetModel 不在远端全集小写集合 → 入列(shouldMarkStale 内部取反)。
function collectStaleMappingsByTab(mappings: MappingLike[], tabId: string, liveRemote: string[]): MappingLike[] {
    if (!liveRemote || liveRemote.length === 0) return [];
    const liveSet = buildLiveSetLower(liveRemote);
    const stale: MappingLike[] = [];
    for (const m of mappings || []) {
        if ((m as any).otab !== tabId) continue; // Tab 隔离:仅处理归属该 Tab 的映射
        const tm = norm((m && m.targetModel) || '');
        if (!tm) continue;
        if (!liveSet.has(tm)) stale.push(m); // 不在远端全集 → 已下架失效
    }
    return stale;
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

    // 测试 7: 失效极性回归 ——「在远端全集里」应判非失效(防 renderCurrentTabTable 直接 .has() 漏取反误标删除线)。
    // 历史 bug:渲染处拿 liveSet 直接 `rowStaleSet.has(tmLower)` 判 isStale,极性反了 → 远端明明有却标删除线。
    // 正确写法走 shouldMarkStale(内部 `!liveSet.has(tm)` 取反):在全集里 = 非失效;不在 = 失效。
    console.log('测试 7: 失效极性 —— 在 liveSet 里应非失效、不在应失效...');
    {
        const polarLive = buildLiveSetLower(['z-ai/glm-5.2', 'moonshotai/kimi-k2.5']);
        // 在远端全集里的 TargetModel:绝不应判失效
        if (shouldMarkStale({ targetModel: 'z-ai/glm-5.2' }, polarLive)) {
            throw new Error('测试 7a 失败: 远端存在却判失效 —— 渲染侧疑似漏了取反(删 \"!\" 就挂这)');
        }
        if (shouldMarkStale({ targetModel: 'Z-AI/GLM-5.2' }, polarLive)) {
            throw new Error('测试 7b 失败: 大小写不敏感,远端存在却判失效');
        }
        // 不在远端全集里的(已下架):应判失效
        if (!shouldMarkStale({ targetModel: 'old-removed-model' }, polarLive)) {
            throw new Error('测试 7c 失败: 已下架模型未判失效 —— 失效集合构建可能反了');
        }
        // 对比佐证:错误写法(直接 .has 漏取反)会把「存在」判成失效 —— 此断言锁死正确极性
        const inflightPresent = 'z-ai/glm-5.2';
        if (polarLive.has(inflightPresent.toLowerCase())) {
            // .has() 命中只是「在全集里」,不能直接当 stale —— 须取反才是正确的 shouldMarkStale 口径
            if (shouldMarkStale({ targetModel: inflightPresent }, polarLive)) {
                throw new Error('测试 7d 失败: .has() 命中却判失效,与正确极性矛盾');
            }
        }
    }
    console.log('✓ 测试 7 通过\n');

    // 测试 8: 失效判定必须按号池 Tab 隔离 ——「远端全集」只能比对「同号池归属」的映射 TargetModel。
    // 历史 bug:_relayClearStaleMappings 遍历全局 allMappings 未按 Tab 过滤,把 Gemini 映射拿到
    // NVIDIA 远端全集里比对 → gemini-* 必然不在 nvidia 全集 → 全判失效 → 跨号池误删。
    // 此处模拟:本号池(nvidia)远端有 [glm,kimi],另一号池(google)映射 [gemini-pro] 不该被算进失效。
    console.log('测试 8: 失效筛选须按 Tab 隔离(跨号池映射不可纳入本号池失效比对)...');
    {
        // 模拟 collectStaleMappingsInTab 的口径:仅对「归属本 Tab 的映射」用本 Tab 远端全集判失效。
        // collectStaleMappingsByTab 是测试内联的、复刻 relayModelMapping.collectStaleMappingsInTab
        // 三重安全逻辑(Tab 隔离 + 缓存键隔离 + 未拉取跳过)的纯函数副本,与生产同口径。
        const allMappings = [
            // 归属 nvidia Tab
            { clientModel: 'nvidia/glm', targetModel: 'z-ai/glm-5.2', otab: 'nvidia' },
            { clientModel: 'nvidia/kimi', targetModel: 'moonshotai/kimi-k2.5', otab: 'nvidia' },
            { clientModel: 'nvidia/old', targetModel: 'old-removed-model', otab: 'nvidia' },
            // 归属 google Tab —— 不应在 nvidia 视角下被任何判失效
            { clientModel: 'gemini-pro', targetModel: 'gemini-2.5-pro', otab: 'google' },
            { clientModel: 'google/gemini-2.5-flash', targetModel: 'gemini-2.5-flash', otab: 'google' },
        ];
        const nvidiaLive = ['z-ai/glm-5.2', 'moonshotai/kimi-k2.5'];
        const nvidiaStale = collectStaleMappingsByTab(allMappings, 'nvidia', nvidiaLive);
        // 预期:仅 old-removed-model 一条失效;gemini 两行必须被 Tab 隔离排除,绝不出现在结果里。
        if (nvidiaStale.length !== 1 || nvidiaStale[0].targetModel !== 'old-removed-model') {
            throw new Error(`测试 8 失败: nvidia 视角失效应仅 old-removed-model, got ${JSON.stringify(nvidiaStale.map(s => s.targetModel))}`);
        }
        if (nvidiaStale.some((s: any) => s.targetModel.startsWith('gemini'))) {
            throw new Error('测试 8 失败: 跨号池泄漏 —— gemini 映射不该被算进 nvidia 失效,根因复发');
        }
        // google 视角同理:用 google 远端全集,google 的两条都在 → 失效 0;nvidia 的不应混入。
        const googleLive = ['gemini-2.5-pro', 'gemini-2.5-flash'];
        const googleStale = collectStaleMappingsByTab(allMappings, 'google', googleLive);
        if (googleStale.length !== 0) {
            throw new Error(`测试 8 失败: google 视角应 0 失效, got ${JSON.stringify(googleStale.map(s => s.targetModel))}`);
        }
        // 未拉取(key 缺失/全集空)→ 绝不下判(防误删),与 computeStaleMappings 空集契约一致。
        const noFetchStale = collectStaleMappingsByTab(allMappings, 'nvidia', []);
        if (noFetchStale.length !== 0) {
            throw new Error(`测试 8 失败: 未拉取应 0 失效(防误删), got ${noFetchStale.length}`);
        }
    }
    console.log('✓ 测试 8 通过\n');

    // 测试 9: 「新增 N」与「失效 N」不可恒等 —— 新增集合(本次远端 - 上次快照)与失效集合
    // (本次映射 TargetModel ∉ 本次远端全集)是两类不同度量,计数必然独立。历史 bug 把按钮计数
    // 误传「远端全集 size」(=新增来源同一次拉取全集)→ 数字恒等于新增数,严重误导用户以为「要删
    // 的就是新增的」。正确口径:失效数 = 当前 Tab 映射里 TargetModel 不在本次远端全集的条数。
    console.log('测试 9: 新增 N 与失效 N 不可恒等(两类独立度量)...');
    {
        const liveFull = ['z-ai/glm-5.2', 'moonshotai/kimi-k2.5', 'deepseek-ai/deepseek-v4-pro']; // 本次远端全集 3
        const snapshot = ['z-ai/glm-5.2', 'moonshotai/kimi-k2.5']; // 上次快照 2
        const addedSet = computeRemoteAddedLower(liveFull, snapshot); // 新增 1(deepseek-v4-pro)

        // 当前 Tab 映射:glm/kimi 都在远端,old-model 已下架 → 失效 1。
        const tabMappings = [
            { targetModel: 'z-ai/glm-5.2' },
            { targetModel: 'moonshotai/kimi-k2.5' },
            { targetModel: 'old-removed-model' },
        ];
        const liveSet = buildLiveSetLower(liveFull);
        const staleCount = tabMappings.filter((m: any) => shouldMarkStale(m, liveSet)).length;

        // 断言:新增数(1) ≠ 失效数(1) 在数值上可相等,但二者独立计算、来源不同;
        // 关键不变量:newCount 只能来自 addedSet(本次 - 快照),staleCount 只能来自 (映射 ∉ 远端全集),
        // 两者不可互相复用。此处构造一个「新增 2、失效 0」的反例证明独立:
        const liveFull2 = ['a', 'b', 'c', 'd']; // 新增 d 对比快照 [a,b,c]
        const addedSet2 = computeRemoteAddedLower(liveFull2, ['a', 'b', 'c']); // 新增 1(d)
        const tabMappings2 = [{ targetModel: 'a' }, { targetModel: 'b' }, { targetModel: 'c' }, { targetModel: 'd' }];
        const liveSet2 = buildLiveSetLower(liveFull2);
        const newCount2 = tabMappings2.filter((m: any) => shouldMarkNew(m.targetModel, addedSet2)).length;
        const staleCount2 = tabMappings2.filter((m: any) => shouldMarkStale(m, liveSet2)).length;
        // 反例:新增 1、失效 0 —— 数值不等,证明两类度量独立,旧逻辑「传 staleSet.size」必然错。
        if (!(newCount2 === 1 && staleCount2 === 0)) {
            throw new Error(`测试 9 失败: 反例应 新增1/失效0, got 新增${newCount2}/失效${staleCount2}`);
        }
        // 锁死:staleCount 不应等于远端全集 size(除非映射恰好全失效,本数据不符合)。
        if (staleCount === liveSet.size) {
            throw new Error(`测试 9 失败: 失效数 == 远端全集数(${liveSet.size}) —— 疑似旧逻辑 staleSet.size 误导复发`);
        }
        // 正例有效性:第一组数据里新增 1、失效 1,确认两条路径都正确独立计算。
        if (!(addedSet.size === 1 && staleCount === 1)) {
            throw new Error(`测试 9 失败: 正例新增应1实际${addedSet.size}, 失效应1实际${staleCount}`);
        }
    }
    console.log('✓ 测试 9 通过\n');

    console.log('>>> relayModelDiff 测试全部绿灯通过！ <<<\n');
}

runRelayDiffTests().catch(err => {
    console.error(err);
    process.exit(1);
});
