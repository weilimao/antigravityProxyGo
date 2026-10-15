// relayModelMappingDedupe.test.ts: 验证中继模型映射拉取号池模型时的按 Tab 作用域去重逻辑。
// 复用 npx tsx 独立运行范式,验证跨号池同名模型不会被误判跳过。

import assert from 'node:assert';

function norm(s: string): string {
    return (s || '').trim().toLowerCase();
}

function isGoogleProviderKind(p: string): boolean {
    const c = (p || '').trim().toLowerCase();
    return c === 'google' || c === 'gcp' || c === 'antigravity' || c === 'gemini-cli' || c === '';
}

interface MappingEntry {
    clientModel: string;
    targetModel: string;
    targetProvider: string;
    expose: boolean;
    ownedBy: string;
    injectChatTemplateKwargs?: boolean;
}

function getMappingTab(m: MappingEntry): string {
    if (m.ownedBy) return m.ownedBy;
    const modelName = (m.clientModel || m.targetModel || '').toLowerCase();
    if (modelName.startsWith('other/')) return 'other';
    if (modelName.startsWith('nvidia/') || modelName.endsWith('-nemotron')) return 'nvidia';
    if (modelName.startsWith('grok/')) return 'grok';
    if (modelName.startsWith('deepseek')) return 'deepseek';
    if (modelName.startsWith('qwen')) return 'qwen';
    if (modelName.startsWith('claude')) return 'anthropic';
    return 'google';
}

function makeMappingEntry(clientModel: string, targetModel: string, provider: string, expose: boolean): MappingEntry {
    return {
        clientModel,
        targetModel,
        targetProvider: provider,
        expose,
        ownedBy: '',
        injectChatTemplateKwargs: provider !== 'other',
    };
}

// 模拟生产环境 useModelMapping.ts 中 fetchChannelModels 的去重与映射补全纯逻辑
function simulateFetchChannelModelsDedupe(
    currentTab: { id: string; targetProvider: string },
    allMappings: MappingEntry[],
    remoteModels: string[],
    wbDetails?: { domModels?: string[]; intlModels?: string[]; hasDomestic?: boolean; hasIntl?: boolean }
): MappingEntry[] {
    const provider = (currentTab.targetProvider || currentTab.id || '').trim();
    const tabId = currentTab.id;

    const existingClientSet = new Set<string>();
    for (const m of allMappings) {
        const cm = (m.clientModel || '').trim();
        if (cm) existingClientSet.add(cm.toLowerCase());
    }

    // 按当前 Tab 作用域去重
    const existingTargetSet = new Set<string>();
    for (const m of allMappings) {
        if (getMappingTab(m) === tabId) {
            const tm = (m.targetModel || '').trim();
            if (tm) existingTargetSet.add(tm.toLowerCase());
        }
    }

    const newEntries: MappingEntry[] = [];
    const isWorkBuddy = provider === 'workbuddy';
    const hasWbDetails = isWorkBuddy && wbDetails && (Array.isArray(wbDetails.domModels) || Array.isArray(wbDetails.intlModels));

    if (hasWbDetails) {
        const domList = wbDetails.domModels || [];
        const intlList = wbDetails.intlModels || [];
        const hasDom = Boolean(wbDetails.hasDomestic);
        const hasIntl = Boolean(wbDetails.hasIntl);

        // 1. 国内版账号支持的模型：自动拼接 workbuddy/cn/{model}
        if (hasDom && domList.length > 0) {
            for (const modelRaw of domList) {
                const model = (modelRaw || '').trim();
                if (!model) continue;
                const prefixed = `workbuddy/cn/${model}`;
                if (!existingClientSet.has(prefixed.toLowerCase())) {
                    newEntries.push(makeMappingEntry(prefixed, model, provider, true));
                    existingClientSet.add(prefixed.toLowerCase());
                }
            }
        }

        // 2. 国际版账号支持的模型：自动拼接 workbuddy/intl/{model}
        if (hasIntl && intlList.length > 0) {
            for (const modelRaw of intlList) {
                const model = (modelRaw || '').trim();
                if (!model) continue;
                const prefixed = `workbuddy/intl/${model}`;
                if (!existingClientSet.has(prefixed.toLowerCase())) {
                    newEntries.push(makeMappingEntry(prefixed, model, provider, true));
                    existingClientSet.add(prefixed.toLowerCase());
                }
            }
        }

        // 3. 通用默认条目 (workbuddy/{model})
        for (const modelRaw of remoteModels) {
            const model = (modelRaw || '').trim();
            if (!model) continue;
            const prefixed = `workbuddy/${model}`;
            if (!existingClientSet.has(prefixed.toLowerCase())) {
                newEntries.push(makeMappingEntry(prefixed, model, provider, true));
                existingClientSet.add(prefixed.toLowerCase());
            }
        }
    } else {
        for (const modelRaw of remoteModels) {
            const model = (modelRaw || '').trim();
            if (!model) continue;
            if (existingTargetSet.has(model.toLowerCase())) continue;

            const isGoogle = isGoogleProviderKind(provider);
            if (isGoogle) {
                if (!existingClientSet.has(model.toLowerCase())) {
                    newEntries.push(makeMappingEntry(model, model, provider, true));
                    existingClientSet.add(model.toLowerCase());
                }
                const prefixed = `${provider}/${model}`;
                if (!existingClientSet.has(prefixed.toLowerCase())) {
                    newEntries.push(makeMappingEntry(prefixed, model, provider, true));
                    existingClientSet.add(prefixed.toLowerCase());
                }
            } else {
                const prefixed = `${provider}/${model}`;
                if (!existingClientSet.has(prefixed.toLowerCase())) {
                    newEntries.push(makeMappingEntry(prefixed, model, provider, true));
                    existingClientSet.add(prefixed.toLowerCase());
                }
            }
            existingTargetSet.add(model.toLowerCase());
        }
    }

    for (const ne of newEntries) {
        ne.ownedBy = tabId;
    }
    return newEntries;
}

console.log('=== 开始运行 relayModelMappingDedupe 单元测试 ===\n');

try {
    // 测试 1: 核心场景 - Other 号池已有 z-ai/glm-5.3 时，NVIDIA 号池拉取不能漏掉 z-ai/glm-5.3
    console.log('测试 1: 跨号池同名模型防护 —— Other 号池已存在 z-ai/glm-5.3，NVIDIA 号池拉取时不被误杀...');
    const initialMappings: MappingEntry[] = [
        {
            clientModel: 'other/openrouter/z-ai/glm-5.3',
            targetModel: 'z-ai/glm-5.3',
            targetProvider: 'other',
            expose: true,
            ownedBy: 'other',
        },
        {
            clientModel: 'nvidia/z-ai/glm-5.3-flash',
            targetModel: 'z-ai/glm-5.3-flash',
            targetProvider: 'nvidia',
            expose: true,
            ownedBy: 'nvidia',
        }
    ];

    const nvidiaRemote = ['z-ai/glm-5.3', 'z-ai/glm-5.3-flash', 'nvidia/llama-3.3-70b-instruct'];
    const addedToNvidia = simulateFetchChannelModelsDedupe(
        { id: 'nvidia', targetProvider: 'nvidia' },
        initialMappings,
        nvidiaRemote
    );

    // 验证: 应新增 2 项: nvidia/z-ai/glm-5.3 与 nvidia/nvidia/llama-3.3-70b-instruct; glm-5.3-flash 已在本 Tab 则去重
    assert.strictEqual(addedToNvidia.length, 2, `预期新增 2 条映射，实际新增 ${addedToNvidia.length}`);
    const glm53 = addedToNvidia.find(m => m.targetModel === 'z-ai/glm-5.3');
    assert.ok(glm53, '必须成功生成 targetModel 为 z-ai/glm-5.3 的映射');
    assert.strictEqual(glm53.clientModel, 'nvidia/z-ai/glm-5.3', '客户端请求模型必须带 nvidia/ 前缀');
    assert.strictEqual(glm53.ownedBy, 'nvidia', '归属号池必须为 nvidia');
    console.log('✓ 测试 1 通过\n');

    // 测试 2: 同 Tab 内部重复模型去重防护
    console.log('测试 2: 同 Tab 内部同名模型正常去重...');
    const allAdded = simulateFetchChannelModelsDedupe(
        { id: 'nvidia', targetProvider: 'nvidia' },
        [...initialMappings, ...addedToNvidia],
        nvidiaRemote
    );
    assert.strictEqual(allAdded.length, 0, '同一号池中已存在的模型不应再次重复添加');
    console.log('✓ 测试 2 通过\n');

    // 测试 3: Google 号池生成裸名与带前缀双条目，且正确去重
    console.log('测试 3: Google 族号池生成与去重...');
    const googleRemote = ['gemini-2.5-flash'];
    const addedToGoogle = simulateFetchChannelModelsDedupe(
        { id: 'google', targetProvider: 'google' },
        [],
        googleRemote
    );
    assert.strictEqual(addedToGoogle.length, 2, 'Google 族号池应生成裸名与带前缀双条目');
    assert.ok(addedToGoogle.some(m => m.clientModel === 'gemini-2.5-flash'), '必须含裸名');
    assert.ok(addedToGoogle.some(m => m.clientModel === 'google/gemini-2.5-flash'), '必须含带前缀名');
    console.log('✓ 测试 3 通过\n');

    // 测试 4: Teardown 验证
    console.log('测试 4: Teardown 沙箱隔离验证...');
    assert.strictEqual(initialMappings.length, 2, '测试执行不污染原数据结构');
    console.log('✓ 测试 4 通过\n');

    // 测试 5: WorkBuddy 国内国际双端模型自动拼接与同名模型共存
    console.log('测试 5: WorkBuddy 国内国际双端模型自动拼接与同名模型共存...');
    const wbTab = { id: 'workbuddy', targetProvider: 'workbuddy' };
    const wbDetails = {
        hasDomestic: true,
        hasIntl: true,
        domModels: ['deepseek-v4.1-flash', 'hunyuan-2.0-instruct'],
        intlModels: ['deepseek-v4.1-flash', 'gpt-5.5'],
    };
    const wbRemote = ['deepseek-v4.1-flash', 'hunyuan-2.0-instruct', 'gpt-5.5'];

    const wbAdded = simulateFetchChannelModelsDedupe(wbTab, [], wbRemote, wbDetails);
    // 预期包含:
    // 1. 国内版: workbuddy/cn/deepseek-v4.1-flash -> deepseek-v4.1-flash
    // 2. 国内版: workbuddy/cn/hunyuan-2.0-instruct -> hunyuan-2.0-instruct
    // 3. 国际版: workbuddy/intl/deepseek-v4.1-flash -> deepseek-v4.1-flash
    // 4. 国际版: workbuddy/intl/gpt-5.5 -> gpt-5.5
    // 5. 通用默认: workbuddy/deepseek-v4.1-flash, workbuddy/hunyuan-2.0-instruct, workbuddy/gpt-5.5
    const cnDs = wbAdded.find(m => m.clientModel === 'workbuddy/cn/deepseek-v4.1-flash');
    assert.ok(cnDs, '必须成功生成国内版 workbuddy/cn/deepseek-v4.1-flash');
    assert.strictEqual(cnDs.targetModel, 'deepseek-v4.1-flash', '国内版目标模型必须为 deepseek-v4.1-flash');

    const intlDs = wbAdded.find(m => m.clientModel === 'workbuddy/intl/deepseek-v4.1-flash');
    assert.ok(intlDs, '必须成功生成国际版 workbuddy/intl/deepseek-v4.1-flash');
    assert.strictEqual(intlDs.targetModel, 'deepseek-v4.1-flash', '国际版目标模型必须为 deepseek-v4.1-flash');

    // 二次拉取去重验证
    const wbSecondFetch = simulateFetchChannelModelsDedupe(wbTab, wbAdded, wbRemote, wbDetails);
    assert.strictEqual(wbSecondFetch.length, 0, '二次获取时全部模型均已存在，不应重复追加');
    console.log('✓ 测试 5 通过\n');

    console.log('>>> relayModelMappingDedupe 单元测试全部通过！ <<<');
} catch (err) {
    console.error('❌ 测试失败:', err);
    process.exit(1);
}
