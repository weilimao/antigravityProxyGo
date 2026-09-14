// test-agent-config-binding.mjs: agentConfigController 双向绑定函数的业务级往返测试。
//
// 背景：Codex Agent 配置页的「模型列表 (Catalog)」保存路径此前会损坏
// supported_reasoning_levels（对象数组被 Array.join 成 "[object Object]"）与
// truncation_policy（对象被 String() 成 "[object Object]"），导致 codex-cli 拒绝加载。
//
// 本测试用 esbuild 把 controller（wailsjs 以 stub 代替，无窗口依赖）打包为
// 独立 ESM，在 Node 中完整跑一遍「加载 -> formToJSON / jsonToForm 往返 -> 保存」，
// 断言对象与对象数组无损往返、输出中绝不出现 "[object Object]"。
//
// 运行：cd frontend && npm run test:binding   （或 node scripts/test-agent-config-binding.mjs）
import { build } from 'esbuild';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

// wailsjs 是 Wails 运行时生成代码，Node 中不存在，用空实现 stub 掉。
const wailsStubPlugin = {
  name: 'wails-stub',
  setup(b) {
    b.onResolve({ filter: /wailsjs/ }, (args) => ({ path: args.path, namespace: 'wails-stub' }));
    b.onLoad({ filter: /.*/, namespace: 'wails-stub' }, () => ({
      contents: [
        'export const IPCSend = () => Promise.resolve();',
        "export const IPCInvoke = () => Promise.resolve('{}');",
        'export const OpenPath = () => {};',
        'export const ShowItemInFolder = () => {};',
        'export const EventsOn = () => () => {};',
        'export const EventsOff = () => {};',
        'export const EventsEmit = () => {};',
      ].join('\n'),
      loader: 'js',
    }));
  },
};

const tmp = mkdtempSync(path.join(tmpdir(), 'agent-binding-test-'));
const outfile = path.join(tmp, 'binding-test-bundle.mjs');

try {
  await build({
    entryPoints: [path.resolve('scripts/binding-test-entry.ts')],
    bundle: true,
    format: 'esm',
    platform: 'node',
    outfile,
    plugins: [wailsStubPlugin],
    // shared/ipc.ts 等模块在顶层引用 window（Wails WebView 环境才有），Node 中注入兜底。
    banner: { js: 'globalThis.window = globalThis;' },
    logLevel: 'silent',
  });

  const { formToJSON, jsonToForm, getSchema } = await import(pathToFileURL(outfile).href);
  const schema = getSchema('codex');
  assert.ok(schema, 'codex schema 应已注册');

  // 与真实 catalog 文件同构的模型段：对象数组（5 档思考等级）、对象字段（截断策略）、
  // 模态数组、以及带点号的 slug（验证模型名含点号不被误拆）。
  const models = [
    {
      slug: 'test/glm-5.2',
      display_name: 'GLM Test',
      input_modalities: ['text', 'image'],
      truncation_policy: { limit: 10000, mode: 'bytes' },
      supported_reasoning_levels: [
        { effort: 'none', description: 'Disable Thinking' },
        { effort: 'low', description: 'Low Thinking Effort' },
        { effort: 'medium', description: 'Medium Thinking Effort' },
        { effort: 'high', description: 'High Thinking Effort' },
        { effort: 'max', description: 'Max Thinking Effort' },
      ],
    },
  ];

  // 1) 模拟页面加载：catalog 数组转 slug-keyed 对象（mergeCatalogIntoJson 行为）后回填表单。
  const slugKeyed = { models: {} };
  for (const m of models) slugKeyed.models[m.slug] = m;
  const formData = jsonToForm(JSON.stringify(slugKeyed), schema);

  const levelsText = formData['models.test/glm-5.2.supported_reasoning_levels'];
  const policyText = formData['models.test/glm-5.2.truncation_policy'];
  assert.ok(typeof levelsText === 'string', '思考等级应回填为文本');
  assert.ok(!levelsText.includes('[object Object]'), '回填文本不得包含 [object Object]');
  assert.ok(levelsText.includes('{"effort":"high","description":"High Thinking Effort"}'), '回填文本应以 JSON 对象形式嵌入每档思考等级');
  assert.equal(policyText, '{"limit":10000,"mode":"bytes"}', '截断策略应以 JSON 文本形式回填');

  // 2) 模拟保存：formToJSON 生成 slug-keyed 对象，再转回数组（extractCatalogFromJson 行为）。
  const generated = JSON.parse(formToJSON(formData, schema));
  assert.ok(generated.models && typeof generated.models === 'object' && !Array.isArray(generated.models), '生成结果中 models 应为对象容器');
  const savedModels = Object.values(generated.models);
  assert.equal(savedModels.length, 1, '应恰好保存一个模型');

  const saved = savedModels[0];
  assert.deepEqual(saved.supported_reasoning_levels, models[0].supported_reasoning_levels, '思考等级必须无损往返为对象数组');
  assert.deepEqual(saved.truncation_policy, { limit: 10000, mode: 'bytes' }, '截断策略必须无损往返为对象');
  assert.deepEqual(saved.input_modalities, ['text', 'image'], '输入模态必须无损往返');
  assert.equal(saved.slug, 'test/glm-5.2', '含点号的 slug 必须完整保留');
  assert.ok(!JSON.stringify(generated).includes('[object Object]'), '最终输出不得包含 [object Object]');

  // 3) 反向容错：存量损坏文件里 supported_reasoning_levels 已是 "[object Object]" 字符串（非数组），
  //    jsonToForm 回填为空文本、formToJSON 保存为空数组——旧坏数据在下次保存时被自然清洗，
  //    不会再次变形为更糟的结构；真正的坏值由 Go 端写前校验拦截。
  const legacy = { models: { 'legacy/model': { slug: 'legacy/model', supported_reasoning_levels: '[object Object]\n[object Object]' } } };
  const legacyForm = jsonToForm(JSON.stringify(legacy), schema);
  const legacyOut = JSON.parse(formToJSON(legacyForm, schema));
  const legacySaved = Object.values(legacyOut.models)[0];
  assert.deepEqual(legacySaved.supported_reasoning_levels, [], '存量损坏字符串应被清洗为空数组而非再次变形');

  console.log('OK: agentConfigController 绑定往返测试全部通过（对象/对象数组无损、无 [object Object]）。');
} finally {
  rmSync(tmp, { recursive: true, force: true });
}