// schemas/index.ts: Schema 注册表，导出 getSchema(agentID)。
// 新增 Agent 只需在此 import 并注册。

import { AgentSchema } from '../types';
import { opencodeSchema } from './opencode';
import { claudeCodeSchema } from './claude-code';
import { codexSchema } from './codex';

const schemaRegistry: Record<string, AgentSchema> = {
  opencode: opencodeSchema,
  'claude-code': claudeCodeSchema,
  codex: codexSchema,
};

export function getSchema(agentId: string): AgentSchema | null {
  return schemaRegistry[agentId] || null;
}

export function getAvailableAgents(): string[] {
  return Object.keys(schemaRegistry);
}
