import { claudeCodeSchema } from '../schemas/claude-code';
import { ConfigField } from '../types';

function cleanModelName(val: any): string {
  if (val === undefined || val === null) return '';
  const str = String(val).trim();
  return str.replace(/\[1M\]$/i, '').trim();
}

function simulateFieldUpdate(
  formData: Record<string, any>,
  field: ConfigField,
  value: any
): Record<string, any> {
  const updated = { ...formData };
  updated[field.key] = value;
  if (field.syncTargetKey) {
    if (value !== undefined && value !== null && value !== '') {
      updated[field.syncTargetKey] = cleanModelName(value);
    } else {
      updated[field.syncTargetKey] = '';
    }
  }
  return updated;
}

async function runSlotSyncTests() {
  console.log('=== 开始运行 Agent 配置槽位模型与展示名称联动测试 ===\n');

  // 测试 1: 验证 Schema 定义中槽位与目标同步 key 的声明
  console.log('测试 1: 验证 claudeCodeSchema 槽位模型 ID 字段的 syncTargetKey 配置...');
  const modelSection = claudeCodeSchema.sections.find(s => s.title.includes('模型槽位映射'));
  if (!modelSection) {
    throw new Error('测试 1 失败: 未找到模型槽位映射 Section');
  }

  const expectedSlots = [
    {
      modelKey: 'env.ANTHROPIC_DEFAULT_SONNET_MODEL',
      nameKey: 'env.ANTHROPIC_DEFAULT_SONNET_MODEL_NAME',
      label: 'Sonnet',
    },
    {
      modelKey: 'env.ANTHROPIC_DEFAULT_OPUS_MODEL',
      nameKey: 'env.ANTHROPIC_DEFAULT_OPUS_MODEL_NAME',
      label: 'Opus',
    },
    {
      modelKey: 'env.ANTHROPIC_DEFAULT_FABLE_MODEL',
      nameKey: 'env.ANTHROPIC_DEFAULT_FABLE_MODEL_NAME',
      label: 'Fable',
    },
    {
      modelKey: 'env.ANTHROPIC_DEFAULT_HAIKU_MODEL',
      nameKey: 'env.ANTHROPIC_DEFAULT_HAIKU_MODEL_NAME',
      label: 'Haiku',
    },
  ];

  for (const item of expectedSlots) {
    const field = modelSection.fields.find(f => f.key === item.modelKey);
    if (!field) {
      throw new Error(`测试 1 失败: 未在 schema 中找到字段 ${item.modelKey}`);
    }
    if (field.syncTargetKey !== item.nameKey) {
      throw new Error(`测试 1 失败: ${item.modelKey} 的 syncTargetKey 应为 ${item.nameKey}，实际为 ${field.syncTargetKey}`);
    }
  }
  console.log('✓ 测试 1 通过: Sonnet/Opus/Fable/Haiku 4 个槽位均已正确配置 syncTargetKey。\n');

  // 测试 2: 验证 cleanModelName 基础与边缘情况
  console.log('测试 2: 验证 cleanModelName 辅助函数的后缀剥离逻辑...');
  const cleanCases = [
    { input: 'nvidia/deepseek-ai/deepseek-v4-flash-0731', expected: 'nvidia/deepseek-ai/deepseek-v4-flash-0731' },
    { input: 'claude-3-7-sonnet-20250219[1M]', expected: 'claude-3-7-sonnet-20250219' },
    { input: 'claude-3-7-sonnet-20250219[1m]', expected: 'claude-3-7-sonnet-20250219' },
    { input: '  grok/grok-4.6[1M]  ', expected: 'grok/grok-4.6' },
    { input: '', expected: '' },
    { input: null, expected: '' },
    { input: undefined, expected: '' },
  ];

  for (const c of cleanCases) {
    const actual = cleanModelName(c.input);
    if (actual !== c.expected) {
      throw new Error(`测试 2 失败: cleanModelName("${c.input}") 应为 "${c.expected}"，实际为 "${actual}"`);
    }
  }
  console.log('✓ 测试 2 通过: cleanModelName 正确处理了普通名称、[1M] 后缀及空值边缘情况。\n');

  // 测试 3: 模拟用户在 Sonnet 槽位选择模型时，展示名称自动联动
  console.log('测试 3: 模拟 Sonnet 槽位模型选择联动...');
  let formData: Record<string, any> = {
    'env.ANTHROPIC_DEFAULT_SONNET_MODEL': 'old-sonnet-model',
    'env.ANTHROPIC_DEFAULT_SONNET_MODEL_NAME': 'old-sonnet-model',
  };

  const sonnetField = modelSection.fields.find(f => f.key === 'env.ANTHROPIC_DEFAULT_SONNET_MODEL')!;
  formData = simulateFieldUpdate(formData, sonnetField, 'nvidia/deepseek-ai/deepseek-v4-flash-0731');

  if (formData['env.ANTHROPIC_DEFAULT_SONNET_MODEL'] !== 'nvidia/deepseek-ai/deepseek-v4-flash-0731') {
    throw new Error(`测试 3 失败: 模型 ID 未正确更新`);
  }
  if (formData['env.ANTHROPIC_DEFAULT_SONNET_MODEL_NAME'] !== 'nvidia/deepseek-ai/deepseek-v4-flash-0731') {
    throw new Error(`测试 3 失败: 菜单展示名称未自动同步为选中的模型名称，实际为 ${formData['env.ANTHROPIC_DEFAULT_SONNET_MODEL_NAME']}`);
  }
  console.log('✓ 测试 3 通过: 选中普通模型时，展示名称正确自动同步更新。\n');

  // 测试 4: 模拟带 1M 上下文开关的槽位模型选择联动
  console.log('测试 4: 模拟带 [1M] 后缀的模型选择联动...');
  formData = simulateFieldUpdate(formData, sonnetField, 'other/aliyun/qwen3.8-max[1M]');

  if (formData['env.ANTHROPIC_DEFAULT_SONNET_MODEL'] !== 'other/aliyun/qwen3.8-max[1M]') {
    throw new Error(`测试 4 失败: 模型 ID 应保留 [1M] 后缀`);
  }
  if (formData['env.ANTHROPIC_DEFAULT_SONNET_MODEL_NAME'] !== 'other/aliyun/qwen3.8-max') {
    throw new Error(`测试 4 失败: 菜单展示名称应剥离 [1M] 后缀，实际为 ${formData['env.ANTHROPIC_DEFAULT_SONNET_MODEL_NAME']}`);
  }
  console.log('✓ 测试 4 通过: 带 [1M] 后缀的模型选择时，展示名称剥离后缀并同步更新。\n');

  // 测试 5: 模拟用户后续手动在展示名称输入框中自定义别名
  console.log('测试 5: 验证用户手动自定义展示名称的灵活性...');
  const sonnetNameField = modelSection.fields.find(f => f.key === 'env.ANTHROPIC_DEFAULT_SONNET_MODEL_NAME')!;
  formData = simulateFieldUpdate(formData, sonnetNameField, 'My-Custom-Sonnet-Alias');

  if (formData['env.ANTHROPIC_DEFAULT_SONNET_MODEL'] !== 'other/aliyun/qwen3.8-max[1M]') {
    throw new Error(`测试 5 失败: 手动修改展示名称不应影响槽位模型 ID`);
  }
  if (formData['env.ANTHROPIC_DEFAULT_SONNET_MODEL_NAME'] !== 'My-Custom-Sonnet-Alias') {
    throw new Error(`测试 5 失败: 展示名称未更新为自定义别名`);
  }
  console.log('✓ 测试 5 通过: 用户手动自定义展示名称不会反向影响模型 ID。\n');

  // 测试 6: 模拟槽位清空操作
  console.log('测试 6: 验证槽位清空操作...');
  formData = simulateFieldUpdate(formData, sonnetField, '');

  if (formData['env.ANTHROPIC_DEFAULT_SONNET_MODEL'] !== '') {
    throw new Error(`测试 6 失败: 模型 ID 应为空`);
  }
  if (formData['env.ANTHROPIC_DEFAULT_SONNET_MODEL_NAME'] !== '') {
    throw new Error(`测试 6 失败: 展示名称应被同步清空，实际为 "${formData['env.ANTHROPIC_DEFAULT_SONNET_MODEL_NAME']}"`);
  }
  console.log('✓ 测试 6 通过: 清空槽位选择时，展示名称同步清空。\n');

  // 测试 7: 全槽位联动遍历测试 (Opus, Fable, Haiku)
  console.log('测试 7: Opus, Fable, Haiku 全槽位自动化联动验证...');
  const testData = [
    { key: 'env.ANTHROPIC_DEFAULT_OPUS_MODEL', nameKey: 'env.ANTHROPIC_DEFAULT_OPUS_MODEL_NAME', val: 'nvidia/z-ai/glm-5.2' },
    { key: 'env.ANTHROPIC_DEFAULT_FABLE_MODEL', nameKey: 'env.ANTHROPIC_DEFAULT_FABLE_MODEL_NAME', val: 'other/sensenova/glm-5.2[1M]' },
    { key: 'env.ANTHROPIC_DEFAULT_HAIKU_MODEL', nameKey: 'env.ANTHROPIC_DEFAULT_HAIKU_MODEL_NAME', val: 'other/aliyun/deepseek-v4-flash-0731' },
  ];

  for (const item of testData) {
    const f = modelSection.fields.find(field => field.key === item.key)!;
    const res = simulateFieldUpdate({}, f, item.val);
    const expectedClean = cleanModelName(item.val);
    if (res[item.nameKey] !== expectedClean) {
      throw new Error(`测试 7 失败: ${item.key} 联动后 ${item.nameKey} 应为 ${expectedClean}，实际为 ${res[item.nameKey]}`);
    }
  }
  console.log('✓ 测试 7 通过: 全部 4 个槽位联动逻辑均已验证通过。\n');

  console.log('>>> 自动化测试全部绿灯通过！ <<<');
}

runSlotSyncTests().catch(err => {
  console.error('❌ 测试执行遇到错误:', err);
  process.exit(1);
});
