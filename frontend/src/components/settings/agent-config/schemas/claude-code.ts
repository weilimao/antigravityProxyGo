// schemas/claude-code.ts: Claude Code 配置项 schema 定义。
// 基于 https://json.schemastore.org/claude-code-settings.json 官方 JSON Schema 与 CC Switch 规范提取的 UI 可视化字段。
// 对应 ~/.claude/settings.json 配置文件。

import { AgentSchema, ConfigSection } from '../types';

// ===== 基础设置 =====
const basicSettings: ConfigSection = {
  title: '基础设置',
  icon: 'settings',
  fields: [
    {
      key: '$schema',
      label: 'JSON Schema 地址',
      type: 'string',
      default: 'https://json.schemastore.org/claude-code-settings.json',
      description: '用于编辑器校验与自动补全的 JSON Schema 引用',
    },
    {
      key: 'model',
      label: '默认激活档位',
      type: 'select',
      options: ['opus', 'sonnet', 'haiku', 'fable'],
      default: 'opus',
      description: 'Claude Code 启动时默认激活的模型槽位（对应下方映射）',
    },
    {
      key: 'alwaysThinkingEnabled',
      label: '始终启用扩展思考',
      type: 'boolean',
      default: true,
      description: '是否全局默认常驻启用 Extended Thinking 思考推理能力',
    },
    {
      key: 'effortLevel',
      label: '顶层思考强度',
      type: 'select',
      options: ['low', 'medium', 'high', 'xhigh', 'max'],
      default: 'xhigh',
      description: '顶层配置的思考推理强度分档（环境变量 CLAUDE_CODE_EFFORT_LEVEL 覆盖此值）',
    },
    {
      key: 'skipDangerousModePermissionPrompt',
      label: '跳过危险模式权限确认',
      type: 'boolean',
      default: true,
      description: '在无沙箱或危险模式下执行命令时跳过二次弹窗确认',
    },
    {
      key: 'autoCompactEnabled',
      label: '自动压缩对话',
      type: 'boolean',
      default: true,
      description: '上下文逼近上限时自动压缩对话（关闭后需手动 /compact）',
    },
    {
      key: 'includeCoAuthoredBy',
      label: 'Git 提交附加 Co-authored-by',
      type: 'boolean',
      default: true,
      description: 'Git commit / PR 是否追加 Claude Code 联合作者署名',
    },
    {
      key: 'outputStyle',
      label: '输出风格',
      type: 'select',
      options: ['', 'default', 'explanatory', 'learning'],
      default: '',
      description: 'Claude Code 的输出风格预设（留空=默认, explanatory=详细解释, learning=教学式）',
    },
    {
      key: 'autoUpdatesChannel',
      label: '自动更新渠道',
      type: 'select',
      options: ['latest', 'stable'],
      default: 'latest',
      description: 'latest=最新发布版, stable=稳定版（滞后约一周并跳过回归）',
    },
    {
      key: 'autoMemoryEnabled',
      label: '自动上下文记忆',
      type: 'boolean',
      default: true,
      description: '启用后自动将有用上下文保存至 ~/.claude/projects/ 记忆库',
    },
    {
      key: 'cleanupPeriodDays',
      label: '数据保留天数',
      type: 'number',
      default: 30,
      description: '会话、子代理工作树、任务和快照的保留天数（默认 30 天）',
    },
    {
      key: 'apiKeyHelper',
      label: '认证脚本路径',
      type: 'string',
      placeholder: '/bin/generate_temp_api_key.sh',
      toggleable: true,
      description: '输出临时认证 API Key 的外部脚本绝对路径',
    },
  ],
};

// ===== 中继端点与认证 =====
const relaySettings: ConfigSection = {
  title: '中继端点与认证 (env)',
  icon: 'dns',
  fields: [
    {
      key: 'env.ANTHROPIC_BASE_URL',
      label: '中继代理基础 URL (ANTHROPIC_BASE_URL)',
      type: 'string',
      placeholder: 'http://127.0.0.1:18444/route',
      description: '自定义 API 代理基础地址，填入本地中继服务地址即可直连转发',
    },
    {
      key: 'env.ANTHROPIC_API_KEY',
      label: 'API 认证密钥 (ANTHROPIC_API_KEY)',
      type: 'string',
      secret: true,
      toggleable: true,
      placeholder: 'sk-ant-...',
      description: 'Anthropic API 认证密钥或中继鉴权 Key',
    },
    {
      key: 'env.ANTHROPIC_AUTH_TOKEN',
      label: 'Bearer 令牌 (ANTHROPIC_AUTH_TOKEN)',
      type: 'string',
      secret: true,
      toggleable: true,
      description: '自定义 Authorization 请求头 Bearer Token',
    },
    {
      key: 'env.API_TIMEOUT_MS',
      label: '请求超时时间 (ms)',
      type: 'string',
      placeholder: '600000',
      description: 'API 请求超时时间（毫秒，默认 600000 即 10 分钟）',
    },
    {
      key: 'env.CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC',
      label: '禁用非必要网络流量',
      type: 'select',
      options: ['0', '1'],
      description: '禁用遥测、自动更新探测与用户调查（1=禁用, 0=启用）',
    },
    {
      key: 'env.ENABLE_TOOL_SEARCH',
      label: '恢复工具搜索 (ENABLE_TOOL_SEARCH)',
      type: 'boolean',
      default: false,
      description: '当 ANTHROPIC_BASE_URL 指向非官方地址时，Tool Search 会被默认禁用；如中继转发 tool_reference 块，应开启此项以恢复',
    },
  ],
};

// ===== 模型槽位映射与 1M 后缀 =====
const modelSlots: ConfigSection = {
  title: '模型槽位映射 (Model Slots)',
  icon: 'swap_horiz',
  fields: [
    // 默认兜底与推理
    {
      key: 'env.ANTHROPIC_MODEL',
      label: '默认兜底模型 (ANTHROPIC_MODEL)',
      type: 'model-select',
      with1mSuffix: true,
      description: '用于未明确匹配 Sonnet, Opus, Fable, Haiku 角色的请求兜底，右侧开关可声明 [1M] 长上下文',
    },
    {
      key: 'env.ANTHROPIC_REASONING_MODEL',
      label: '推理增强模型 (ANTHROPIC_REASONING_MODEL)',
      type: 'model-select',
      toggleable: true,
      with1mSuffix: false,
      description: '显式指定的思考推理模型名称（如 glm-5.1）',
    },
    {
      key: 'env.CLAUDE_CODE_SUBAGENT_MODEL',
      label: '子 Agent 模型 (CLAUDE_CODE_SUBAGENT_MODEL)',
      type: 'model-select',
      with1mSuffix: true,
      description: '子代理任务分配的独立模型（不显示在 /model 菜单中，右侧可声明 1M）',
    },
    // Sonnet 槽位
    {
      key: 'env.ANTHROPIC_DEFAULT_SONNET_MODEL',
      label: 'Sonnet 槽位模型 ID',
      type: 'model-select',
      with1mSuffix: true,
      description: '日常编码主力 Sonnet 槽位对应的实际模型（右侧可声明 1M 上下文）',
    },
    {
      key: 'env.ANTHROPIC_DEFAULT_SONNET_MODEL_NAME',
      label: 'Sonnet 菜单展示名称',
      type: 'string',
      placeholder: 'grok/grok-4.6',
      description: '在 Claude Code 终端 /model 切换列表中显示的名称',
    },
    {
      key: 'env.ANTHROPIC_DEFAULT_SONNET_MODEL_SUPPORTED_CAPABILITIES',
      label: 'Sonnet 支持能力',
      type: 'string',
      placeholder: 'effort,thinking',
      description: 'Sonnet 槽位模型声明支持的能力（逗号分隔：effort, thinking 等）',
      toggleable: true,
    },
    // Opus 槽位
    {
      key: 'env.ANTHROPIC_DEFAULT_OPUS_MODEL',
      label: 'Opus 槽位模型 ID',
      type: 'model-select',
      with1mSuffix: true,
      description: '复杂推理 Opus 槽位对应的实际模型（右侧可声明 1M 上下文）',
    },
    {
      key: 'env.ANTHROPIC_DEFAULT_OPUS_MODEL_NAME',
      label: 'Opus 菜单展示名称',
      type: 'string',
      placeholder: 'nvidia/z-ai/glm-5.2',
      description: '在 Claude Code 终端 /model 切换列表中显示的名称',
    },
    {
      key: 'env.ANTHROPIC_DEFAULT_OPUS_MODEL_SUPPORTED_CAPABILITIES',
      label: 'Opus 支持能力',
      type: 'string',
      placeholder: 'effort,thinking',
      description: 'Opus 槽位模型声明支持的能力（逗号分隔：effort, thinking 等）',
      toggleable: true,
    },
    // Fable 槽位
    {
      key: 'env.ANTHROPIC_DEFAULT_FABLE_MODEL',
      label: 'Fable 槽位模型 ID',
      type: 'model-select',
      with1mSuffix: true,
      description: 'Fable 槽位对应的实际模型（右侧可声明 1M 上下文）',
    },
    {
      key: 'env.ANTHROPIC_DEFAULT_FABLE_MODEL_NAME',
      label: 'Fable 菜单展示名称',
      type: 'string',
      placeholder: 'other/sensenova/glm-5.2',
      description: '在 Claude Code 终端 /model 切换列表中显示的名称',
    },
    {
      key: 'env.ANTHROPIC_DEFAULT_FABLE_MODEL_SUPPORTED_CAPABILITIES',
      label: 'Fable 支持能力',
      type: 'string',
      placeholder: 'effort,thinking',
      description: 'Fable 槽位模型声明支持的能力（逗号分隔：effort, thinking 等）',
      toggleable: true,
    },
    // Haiku 槽位
    {
      key: 'env.ANTHROPIC_DEFAULT_HAIKU_MODEL',
      label: 'Haiku 槽位模型 ID',
      type: 'model-select',
      with1mSuffix: true,
      description: '轻量/低延迟 Haiku 槽位对应的实际模型（右侧可声明 1M 上下文）',
    },
    {
      key: 'env.ANTHROPIC_DEFAULT_HAIKU_MODEL_NAME',
      label: 'Haiku 菜单展示名称',
      type: 'string',
      placeholder: 'other/aliyun/deepseek-v4-flash-0731',
      description: '在 Claude Code 终端 /model 切换列表中显示的名称',
    },
    {
      key: 'env.ANTHROPIC_DEFAULT_HAIKU_MODEL_SUPPORTED_CAPABILITIES',
      label: 'Haiku 支持能力',
      type: 'string',
      placeholder: 'effort,thinking',
      description: 'Haiku 槽位模型声明支持的能力（逗号分隔：effort, thinking 等）',
      toggleable: true,
    },
    // 环境变量思考强度
    {
      key: 'env.CLAUDE_CODE_EFFORT_LEVEL',
      label: '思考强度覆盖 (EFFORT_LEVEL)',
      type: 'select',
      options: ['low', 'medium', 'high', 'xhigh', 'max', 'auto'],
      default: 'max',
      description: '环境变量层级的思考推理强度覆盖（low=低, medium=中, high=高, xhigh=超高, max=最大）',
    },
  ],
};

// ===== 上下文与压缩调优 =====
const contextCompaction: ConfigSection = {
  title: '上下文与压缩调优',
  icon: 'compress',
  fields: [
    {
      key: 'env.CLAUDE_AUTOCOMPACT_PCT_OVERRIDE',
      label: '自动压缩百分比 (AUTOCOMPACT_PCT)',
      type: 'string',
      placeholder: '90',
      description: '触发上下文自动压缩的容量百分比阈值（1-100，如 90）',
    },
    {
      key: 'env.CLAUDE_CODE_AUTO_COMPACT_WINDOW',
      label: '压缩 Token 窗口大小',
      type: 'string',
      placeholder: '202752',
      description: '用于压缩计算的上下文 Token 容量上限（如 202752）',
    },
    {
      key: 'env.BASH_MAX_OUTPUT_LENGTH',
      label: 'Bash 最大输出字符',
      type: 'string',
      placeholder: '50000',
      description: 'Bash 工具输出被截断前的最大字符数',
    },
    {
      key: 'env.BASH_DEFAULT_TIMEOUT_MS',
      label: 'Bash 默认超时 (ms)',
      type: 'string',
      placeholder: '120000',
      description: 'Bash 命令执行默认超时时间（毫秒，默认 120000 即 2 分钟）',
    },
    {
      key: 'env.BASH_MAX_TIMEOUT_MS',
      label: 'Bash 最大超时上限 (ms)',
      type: 'string',
      placeholder: '600000',
      description: 'Bash 命令超时上限（毫秒，0=不限制，默认 600000 即 10 分钟）',
      toggleable: true,
    },
    {
      key: 'env.MAX_MCP_OUTPUT_TOKENS',
      label: 'MCP 工具输出上限 (tokens)',
      type: 'string',
      placeholder: '25000',
      description: 'MCP 工具返回结果的最大 token 数（超出将告警并截断，默认 25000）',
      toggleable: true,
    },
    {
      key: 'env.MCP_TIMEOUT',
      label: 'MCP 连接超时 (ms)',
      type: 'string',
      placeholder: '10000',
      description: 'MCP 服务启动连接的超时时间（毫秒，默认 10000 即 10 秒）',
      toggleable: true,
    },
  ],
};

// ===== 权限与工具控制 =====
const permissions: ConfigSection = {
  title: '权限控制 (Permissions)',
  icon: 'security',
  fields: [
    {
      key: 'permissions.defaultMode',
      label: '默认权限放行模式',
      type: 'select',
      options: ['bypassPermissions', 'default', 'prompt', 'plan'],
      default: 'bypassPermissions',
      description: 'bypassPermissions=自动放行所有工具操作, prompt=每次确认, default=默认策略',
    },
    {
      key: 'permissions.allow',
      label: '自动放行规则 (allow)',
      type: 'array',
      description: '允许直接执行免确认的工具规则（每行一个，例如 Bash(npm run *), Edit(/src/**), Read(*)）',
    },
    {
      key: 'permissions.deny',
      label: '禁止执行规则 (deny)',
      type: 'array',
      description: '明确拒绝执行的工具规则（每行一个，例如 Read(./.env), Read(./secrets/**)）',
    },
    {
      key: 'permissions.ask',
      label: '弹窗确认规则 (ask)',
      type: 'array',
      description: '执行前必须提示用户确认的工具规则（每行一个）',
    },
    {
      key: 'permissions.additionalDirectories',
      label: '额外允许目录',
      type: 'array',
      description: '授权 Claude Code 跨目录访问的绝对路径列表（每行一个）',
    },
  ],
};

// ===== MCP 扩展服务（可重复添加） =====
const mcpServers: ConfigSection = {
  title: 'MCP 扩展服务 (mcpServers)',
  icon: 'extension',
  repeatable: true,
  itemKeyField: 'name',
  itemTemplate: {
    command: 'npx',
    args: [],
    env: {},
  },
  fields: [
    {
      key: 'mcpServers.{name}.command',
      label: '启动命令',
      type: 'string',
      placeholder: 'npx',
      description: 'MCP 服务的启动可执行命令（如 npx, node, python）',
    },
    {
      key: 'mcpServers.{name}.args',
      label: '启动参数',
      type: 'array',
      description: '传递给启动命令的参数列表（每行一个）',
    },
    {
      key: 'mcpServers.{name}.env',
      label: '环境变量',
      type: 'kv-list',
      description: '传递给 MCP 进程的环境变量（KEY=VALUE 每行一个）',
    },
    {
      key: 'mcpServers.{name}.url',
      label: 'SSE / HTTP 服务端点',
      type: 'string',
      toggleable: true,
      placeholder: 'http://localhost:8000/sse',
      description: '远程 MCP 服务的 HTTP/SSE 端点（设置时优先于本地 command 模式）',
    },
  ],
};

// ===== MCP 项目级审批控制 =====
const mcpApproval: ConfigSection = {
  title: 'MCP 项目级审批 (jsonServers)',
  icon: 'verified_user',
  fields: [
    {
      key: 'enableAllProjectMcpServers',
      label: '自动批准 .mcp.json 内全部 MCP',
      type: 'boolean',
      default: false,
      description: '启用后自动批准项目根目录 .mcp.json 中声明的全部 MCP 服务，跳过逐个审批弹窗',
    },
    {
      key: 'disabledMcpServers',
      label: '已禁用的 MCP 服务列表',
      type: 'array',
      description: '按名称禁用用户级/插件/claude.ai 连接器 MCP 服务（每行一个名称，停止连接但保留配置）',
    },
    {
      key: 'enabledMcpjsonServers',
      label: '已批准的项目 MCP 列表',
      type: 'array',
      description: '逐个批准 .mcp.json 中的 MCP 服务名（每行一个名称，与 enableAllProjectMcpServers 二选一）',
    },
    {
      key: 'disabledMcpjsonServers',
      label: '已拒绝的项目 MCP 列表',
      type: 'array',
      description: '逐个拒绝 .mcp.json 中的 MCP 服务名（每行一个名称，优先级最高）',
    },
  ],
};

// ===== 界面与终端偏好 =====
const preferences: ConfigSection = {
  title: '界面与终端偏好',
  icon: 'palette',
  fields: [
    {
      key: 'theme',
      label: '主题色彩',
      type: 'string',
      placeholder: 'dark',
      description: '终端 TUI 显示主题',
    },
    {
      key: 'preferEditor',
      label: '首选编辑器',
      type: 'string',
      placeholder: 'code',
      description: '交互式编辑文件时首选的编辑器（如 code, cursor, vim）',
    },
    {
      key: 'verbose',
      label: '详细日志输出',
      type: 'boolean',
      default: false,
      description: '开启后在终端显示详细调试日志与跟踪信息',
    },
    {
      key: 'prefersReducedMotion',
      label: '减少界面动效',
      type: 'boolean',
      default: false,
      description: '降低动画和过渡效果以提升终端渲染性能',
    },
    {
      key: 'feedbackSurveyRate',
      label: '反馈调查频率',
      type: 'number',
      default: 0,
      description: '会话结束时弹出体验调查问卷的概率（设为 0 完全禁用）',
    },
  ],
};

export const claudeCodeSchema: AgentSchema = {
  agentId: 'claude-code',
  sections: [
    basicSettings,
    relaySettings,
    modelSlots,
    contextCompaction,
    permissions,
    mcpServers,
    mcpApproval,
    preferences,
  ],
};
