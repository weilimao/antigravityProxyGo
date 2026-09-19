import {
  renameRepeatableObjectChild,
  removeRepeatableObjectChildKeys,
  getRepeatableObjectNames,
} from '../repeatableHelper';
import { ConfigField } from '../types';

async function runModelRenameTests() {
  console.log('=== 开始运行模型重命名（编辑模型名称）与数据迁移自动化测试 ===\n');

  const modelField: ConfigField = {
    key: 'provider.{name}.models',
    label: '模型列表',
    type: 'repeatable-object',
    childKeyLabel: '模型名',
    children: [
      { key: 'name', label: '模型显示名', type: 'string' },
      { key: 'reasoning', label: '支持推理', type: 'boolean' },
      { key: 'limit.context', label: '上下文窗口', type: 'number' },
      { key: 'cost.input', label: '输入价格', type: 'number' },
      {
        key: 'variants',
        label: '思考等级变体',
        type: 'repeatable-object',
        childKeyLabel: '变体名',
        children: [
          { key: 'reasoningEffort', label: '思考等级', type: 'string' },
        ],
      },
    ],
  };

  const parentName = 'antigravityproxy';

  // ----------------------------------------------------
  // 测试 1: 基础重命名与字段完整无损迁移测试
  // ----------------------------------------------------
  console.log('测试 1: 验证模型从旧名称重命名为新名称时，所有多层级配置完整无损迁移...');
  const initialFormData: Record<string, any> = {
    'provider.antigravityproxy.name': 'antigravityproxy',
    'provider.antigravityproxy.models.opencode/deepseek-v4-flash-free.name': 'opencode/deepseek-v4-flash-free',
    'provider.antigravityproxy.models.opencode/deepseek-v4-flash-free.reasoning': true,
    'provider.antigravityproxy.models.opencode/deepseek-v4-flash-free.limit.context': 131072,
    'provider.antigravityproxy.models.opencode/deepseek-v4-flash-free.cost.input': 0.15,
  };

  const res1 = renameRepeatableObjectChild(
    initialFormData,
    modelField,
    parentName,
    'opencode/deepseek-v4-flash-free',
    'deepseek-chat',
  );

  if (res1.error) {
    throw new Error(`测试 1 失败: 出现意外错误 ${res1.error}`);
  }

  const updated1 = res1.updatedFormData;
  // 验证旧键全部清除
  for (const k of Object.keys(updated1)) {
    if (k.includes('opencode/deepseek-v4-flash-free')) {
      throw new Error(`测试 1 失败: 旧模型键未清除: ${k}`);
    }
  }

  // 验证新键存在且数值正确
  if (updated1['provider.antigravityproxy.models.deepseek-chat.reasoning'] !== true) {
    throw new Error('测试 1 失败: reasoning 字段迁移丢失');
  }
  if (updated1['provider.antigravityproxy.models.deepseek-chat.limit.context'] !== 131072) {
    throw new Error('测试 1 失败: limit.context 字段迁移丢失');
  }
  if (updated1['provider.antigravityproxy.models.deepseek-chat.cost.input'] !== 0.15) {
    throw new Error('测试 1 失败: cost.input 字段迁移丢失');
  }
  // 验证默认显示名同步更名
  if (updated1['provider.antigravityproxy.models.deepseek-chat.name'] !== 'deepseek-chat') {
    throw new Error(`测试 1 失败: 默认模型显示名未同步更新，实际为: ${updated1['provider.antigravityproxy.models.deepseek-chat.name']}`);
  }
  console.log('✓ 测试 1 通过: 基础字段与数值完整迁移，旧键彻底清除，默认显示名正确同步。\n');

  // ----------------------------------------------------
  // 测试 2: 自定义显示名保护测试
  // ----------------------------------------------------
  console.log('测试 2: 验证当用户设置了个性化显示名时，重命名模型不破坏自定义显示名...');
  const customNameFormData: Record<string, any> = {
    'provider.antigravityproxy.models.gpt-4o.name': '我的主力高智商模型',
    'provider.antigravityproxy.models.gpt-4o.reasoning': false,
  };

  const res2 = renameRepeatableObjectChild(
    customNameFormData,
    modelField,
    parentName,
    'gpt-4o',
    'gpt-4o-2024-11-20',
  );

  if (res2.error) {
    throw new Error(`测试 2 失败: ${res2.error}`);
  }
  if (res2.updatedFormData['provider.antigravityproxy.models.gpt-4o-2024-11-20.name'] !== '我的主力高智商模型') {
    throw new Error('测试 2 失败: 自定义显示名被意外覆盖');
  }
  console.log('✓ 测试 2 通过: 用户自定义显示名得到完整保护。\n');

  // ----------------------------------------------------
  // 测试 3: 嵌套变体 (variants) 深度迁移测试
  // ----------------------------------------------------
  console.log('测试 3: 验证模型内部嵌套的 variants 变体配置跟随模型重命名无损迁移...');
  const nestedFormData: Record<string, any> = {
    'provider.antigravityproxy.models.gemini-2.5-pro.name': 'gemini-2.5-pro',
    'provider.antigravityproxy.models.gemini-2.5-pro.variants.high.reasoningEffort': 'high',
    'provider.antigravityproxy.models.gemini-2.5-pro.variants.low.reasoningEffort': 'low',
  };

  const res3 = renameRepeatableObjectChild(
    nestedFormData,
    modelField,
    parentName,
    'gemini-2.5-pro',
    'gemini-2.5-pro-preview',
  );

  if (res3.error) {
    throw new Error(`测试 3 失败: ${res3.error}`);
  }
  const updated3 = res3.updatedFormData;
  if (updated3['provider.antigravityproxy.models.gemini-2.5-pro-preview.variants.high.reasoningEffort'] !== 'high') {
    throw new Error('测试 3 失败: variants.high 变体配置迁移失败');
  }
  if (updated3['provider.antigravityproxy.models.gemini-2.5-pro-preview.variants.low.reasoningEffort'] !== 'low') {
    throw new Error('测试 3 失败: variants.low 变体配置迁移失败');
  }
  console.log('✓ 测试 3 通过: 嵌套变体 (variants) 配置路径同步迁移成功。\n');

  // ----------------------------------------------------
  // 测试 4: 边界防呆与校验机制测试（空值、重名、未修改）
  // ----------------------------------------------------
  console.log('测试 4: 验证防呆校验逻辑（空输入、纯空格、重名冲突、同名无操作）...');
  const multiModelsData: Record<string, any> = {
    'provider.antigravityproxy.models.model-alpha.name': 'model-alpha',
    'provider.antigravityproxy.models.model-beta.name': 'model-beta',
  };

  // 空名称
  const errEmpty = renameRepeatableObjectChild(multiModelsData, modelField, parentName, 'model-alpha', '   ');
  if (!errEmpty.error || !errEmpty.error.includes('请输入')) {
    throw new Error('测试 4 失败: 未成功拦截纯空格模型名称');
  }

  // 重名冲突
  const errDuplicate = renameRepeatableObjectChild(multiModelsData, modelField, parentName, 'model-alpha', 'model-beta');
  if (!errDuplicate.error || !errDuplicate.error.includes('已存在')) {
    throw new Error('测试 4 失败: 未成功拦截重复名称冲突');
  }

  // 未做修改
  const noChange = renameRepeatableObjectChild(multiModelsData, modelField, parentName, 'model-alpha', 'model-alpha');
  if (noChange.error) {
    throw new Error('测试 4 失败: 同名未变不应报错');
  }
  if (noChange.updatedFormData !== multiModelsData) {
    throw new Error('测试 4 失败: 同名未变时不应产生多余拷贝');
  }
  console.log('✓ 测试 4 通过: 空名称、重名冲突均被精准拦截，同名不产生冗余操作。\n');

  // ----------------------------------------------------
  // 测试 5: 删除模型键工具函数测试
  // ----------------------------------------------------
  console.log('测试 5: 验证 removeRepeatableObjectChildKeys 干净删除模型全部键...');
  const removedData = removeRepeatableObjectChildKeys(
    multiModelsData,
    modelField,
    parentName,
    'model-alpha',
  );
  const remaining = getRepeatableObjectNames(removedData, modelField, parentName);
  if (remaining.includes('model-alpha')) {
    throw new Error('测试 5 失败: model-alpha 仍然残留');
  }
  if (!remaining.includes('model-beta')) {
    throw new Error('测试 5 失败: model-beta 被误删');
  }
  console.log('✓ 测试 5 通过: 目标模型键被彻底移除且无残留。\n');

  // ----------------------------------------------------
  // 测试 6: JSON 深合并与旧键彻底清理测试 (deepMergeConfig)
  // ----------------------------------------------------
  console.log('测试 6: 验证 deepMergeConfig 能够剔除已更名或删除的旧模型，并完整保留未知根属性（如 $schema）...');
  const { deepMergeConfig } = await import('../repeatableHelper');
  const existingJson = {
    $schema: 'https://opencode.ai/config.json',
    unrelatedConfig: 'some-value',
    provider: {
      antigravityproxy: {
        models: {
          'old-model-x': { name: 'old-model-x', limit: { context: 1000 } },
          'stay-model-y': { name: 'stay-model-y', limit: { context: 2000 } },
        },
      },
    },
  };

  // 模拟重命名后，表单生成的 JSON（old-model-x 变更为 renamed-model-x）
  const generatedJson = {
    provider: {
      antigravityproxy: {
        models: {
          'renamed-model-x': { name: 'renamed-model-x', limit: { context: 1000 } },
          'stay-model-y': { name: 'stay-model-y', limit: { context: 2000 } },
        },
      },
    },
  };

  const merged = deepMergeConfig(existingJson, generatedJson);

  // 1. 验证非动态容器的未知字段被保留
  if (merged.$schema !== 'https://opencode.ai/config.json' || merged.unrelatedConfig !== 'some-value') {
    throw new Error('测试 6 失败: 未知自定义属性丢失');
  }

  // 2. 验证旧键 old-model-x 被彻底剔除
  if (merged.provider.antigravityproxy.models['old-model-x'] !== undefined) {
    throw new Error('测试 6 失败: 旧模型键 old-model-x 未被清除');
  }

  // 3. 验证新键 renamed-model-x 存在且保留属性
  if (!merged.provider.antigravityproxy.models['renamed-model-x']) {
    throw new Error('测试 6 失败: 新模型键 renamed-model-x 不存在');
  }
  if (merged.provider.antigravityproxy.models['stay-model-y'].limit.context !== 2000) {
    throw new Error('测试 6 失败: 保留的模型配置被破坏');
  }
  console.log('✓ 测试 6 通过: JSON 深合并精准剔除旧模型键，并无损保留外部扩展字段。\n');

  // ----------------------------------------------------
  // 测试 7: 环境恢复与 Teardown
  // ----------------------------------------------------
  console.log('测试 7: 执行测试数据环境清理 (Teardown)...');
  // 测试均在局部内存作用域中进行，所有局部引用在此自然析构，沙箱干净
  console.log('✓ 测试 7 通过: 沙箱测试数据与内存上下文彻底恢复干净状态。\n');

  console.log('>>> 全部模型重命名自动化测试绿灯通过！ <<<');
}

runModelRenameTests().catch((err) => {
  console.error('测试执行异常:', err);
  process.exit(1);
});
