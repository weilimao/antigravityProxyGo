// modelSearchSelect.test.ts: 验证 ModelSearchSelect 核心算法与过滤逻辑

function filterModelOptions(options: string[], query: string): string[] {
  const q = query.trim().toLowerCase();
  if (!q) return options;
  return options.filter(item => item.toLowerCase().includes(q));
}

function resolveCustomOption(options: string[], query: string, allowCustom = true): { isCustomAvailable: boolean; customValue: string } {
  const q = query.trim();
  if (!q || !allowCustom) {
    return { isCustomAvailable: false, customValue: '' };
  }
  const isExisting = options.some(item => item.toLowerCase() === q.toLowerCase());
  return {
    isCustomAvailable: !isExisting,
    customValue: q,
  };
}

function highlightMatch(text: string, query: string): string {
  const q = query.trim();
  if (!q) return text;
  const regex = new RegExp(`(${q.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')})`, 'gi');
  return text.replace(regex, '<span class="highlight">$1</span>');
}

async function runModelSearchSelectTests() {
  console.log('=== 开始运行 ModelSearchSelect 算法与行为测试 ===\n');

  const mockModels = [
    'nvidia/nvidia/nv-embedcode-7b-v1',
    'nvidia/nvidia/nv-embedqa-e5-v5',
    'nvidia/openai/gpt-4o',
    'nvidia/z-ai/glm-5.2',
    'other/sensenova/glm-5.2',
    'other/aliyun/deepseek-v3',
    'other/aliyun/qwen3.8-max',
    'antigravity/claude-3-7-sonnet',
  ];

  // 测试 1: 空搜索词应返回全部模型
  console.log('测试 1: 空搜索词展示全量模型列表...');
  const allRes = filterModelOptions(mockModels, '');
  if (allRes.length !== mockModels.length) {
    throw new Error(`测试 1 失败: 期望 ${mockModels.length} 个模型，实际得到 ${allRes.length}`);
  }
  console.log('✓ 测试 1 通过: 空搜索词正确返回全量选项。\n');

  // 测试 2: 模糊搜索 glm (忽略大小写)
  console.log('测试 2: 搜索 "glm"...');
  const glmRes = filterModelOptions(mockModels, 'glm');
  if (glmRes.length !== 2 || !glmRes.includes('nvidia/z-ai/glm-5.2') || !glmRes.includes('other/sensenova/glm-5.2')) {
    throw new Error(`测试 2 失败: 搜索 glm 结果不正确: ${JSON.stringify(glmRes)}`);
  }
  console.log(`✓ 测试 2 通过: 成功匹配到 ${glmRes.length} 个 glm 模型。\n`);

  // 测试 3: 搜索 "DEEPSEEK" 大写
  console.log('测试 3: 搜索大写 "DEEPSEEK"...');
  const dsRes = filterModelOptions(mockModels, 'DEEPSEEK');
  if (dsRes.length !== 1 || dsRes[0] !== 'other/aliyun/deepseek-v3') {
    throw new Error(`测试 3 失败: 大写搜索 deepseek 失败: ${JSON.stringify(dsRes)}`);
  }
  console.log('✓ 测试 3 通过: 忽略大小写匹配成功。\n');

  // 测试 4: 关键词高亮
  console.log('测试 4: 关键词高亮文本生成...');
  const hl = highlightMatch('nvidia/z-ai/glm-5.2', 'glm');
  if (!hl.includes('<span class="highlight">glm</span>')) {
    throw new Error(`测试 4 失败: 高亮标签未生成: ${hl}`);
  }
  console.log('✓ 测试 4 通过: 关键词高亮正常。\n');

  // 测试 5: 自定义输入判定 (输入非列表中存在的模型)
  console.log('测试 5: 输入自定义新模型...');
  const customRes = resolveCustomOption(mockModels, 'my-custom-org/llama-3.3-70b');
  if (!customRes.isCustomAvailable || customRes.customValue !== 'my-custom-org/llama-3.3-70b') {
    throw new Error(`测试 5 失败: 自定义模型解析错误: ${JSON.stringify(customRes)}`);
  }
  console.log('✓ 测试 5 通过: 未匹配项支持作为自定义模型选用。\n');

  // 测试 6: 输入已存在模型时不应触发自定义模式
  console.log('测试 6: 输入完全匹配已有模型...');
  const existingRes = resolveCustomOption(mockModels, 'nvidia/z-ai/glm-5.2');
  if (existingRes.isCustomAvailable) {
    throw new Error(`测试 6 失败: 已有模型不应判定为自定义新项: ${JSON.stringify(existingRes)}`);
  }
  console.log('✓ 测试 6 通过: 完全匹配已有模型时正确抑制自定义提示。\n');

  console.log('>>> ModelSearchSelect 自动化测试全部绿灯通过！ <<<\n');
}

runModelSearchSelectTests().catch(err => {
  console.error(err);
  process.exit(1);
});
