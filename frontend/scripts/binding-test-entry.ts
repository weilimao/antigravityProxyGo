// binding-test-entry.ts: 序列化往返测试的编译入口。
// 仅由 scripts/test-agent-config-binding.mjs 在测试时 esbuild 打包引用，
// 不参与应用构建（tsconfig exclude 已排除 src/**/*.test.ts，本文件亦在 src 外）。
import { formToJSON, jsonToForm } from '../src/ui/agentConfigController';
import { getSchema } from '../src/components/settings/agent-config/schemas';

export { formToJSON, jsonToForm, getSchema };
