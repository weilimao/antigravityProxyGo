// schemas/opencode.ts: OpenCode 配置项 schema 定义。
// 基于 https://opencode.ai/config.json 官方 JSON Schema 提取的 UI 可视化字段。
// 每个 section 对应 opencode.json 的一个顶层配置区域，repeatable 的 section
// 用 {name} 占位符表示用户可自由命名的 map key（如 provider 名称、mcp 名称）。

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
      default: 'https://opencode.ai/config.json',
      description: '用于编辑器校验与自动补全的 JSON Schema 引用',
    },
    {
      key: 'model',
      label: '默认模型',
      type: 'model-select',
      description: '默认使用的模型，选项来自中继服务模型映射',
    },
    {
      key: 'small_model',
      label: '小型模型',
      type: 'model-select',
      description: '用于标题生成等轻量任务的模型，选项来自中继服务模型映射',
    },
    {
      key: 'default_agent',
      label: '默认 Agent',
      type: 'string',
      placeholder: 'build',
      description: '未指定时使用的默认 Agent，必须为 primary agent',
    },
    {
      key: 'subagent_depth',
      label: '子 Agent 嵌套深度',
      type: 'number',
      default: 1,
      description: '子 Agent 调用子 Agent 的最大嵌套深度，默认 1',
    },
    {
      key: 'shell',
      label: '默认 Shell',
      type: 'string',
      placeholder: 'pwsh',
      description: '终端和 bash 工具使用的默认 shell',
    },
    {
      key: 'username',
      label: '用户名',
      type: 'string',
      description: '自定义对话中显示的用户名（替代系统用户名）',
    },
  ],
};

// ===== 自动更新 =====
const autoupdate: ConfigSection = {
  title: '自动更新',
  icon: 'upgrade',
  fields: [
    {
      key: 'autoupdate',
      label: '自动更新',
      type: 'select',
      options: ['true', 'false', 'notify'],
      default: 'true',
      description: 'true=自动更新, false=禁用, notify=仅通知',
    },
  ],
};

// ===== 快照 =====
const snapshot: ConfigSection = {
  title: '文件快照',
  icon: 'history',
  fields: [
    {
      key: 'snapshot',
      label: '启用快照追踪',
      type: 'boolean',
      default: true,
      description: '启用后可撤销/回滚 Agent 对文件的修改',
    },
  ],
};

// ===== 共享 =====
const share: ConfigSection = {
  title: '会话共享',
  icon: 'share',
  fields: [
    {
      key: 'share',
      label: '共享模式',
      type: 'select',
      options: ['manual', 'auto', 'disabled'],
      default: 'manual',
      description: 'manual=手动共享, auto=自动共享, disabled=禁用',
    },
  ],
};

// ===== 服务器配置 =====
const server: ConfigSection = {
  title: '服务器配置',
  icon: 'dns',
  fields: [
    {
      key: 'server.port',
      label: '监听端口',
      type: 'number',
      placeholder: '4096',
      description: 'opencode serve / web 的监听端口',
    },
    {
      key: 'server.hostname',
      label: '监听主机名',
      type: 'string',
      placeholder: '0.0.0.0',
      description: '监听主机名',
    },
    {
      key: 'server.mdns',
      label: '启用 mDNS 发现',
      type: 'boolean',
      default: false,
      description: '启用 mDNS 服务发现，允许局域网设备发现此服务器',
    },
    {
      key: 'server.mdnsDomain',
      label: 'mDNS 域名',
      type: 'string',
      placeholder: 'opencode.local',
      description: 'mDNS 自定义域名',
    },
    {
      key: 'server.cors',
      label: 'CORS 允许域名',
      type: 'array',
      description: 'HTTP 服务器允许的额外 CORS 来源（每行一个）',
    },
  ],
};

// ===== 图片附件 =====
const attachment: ConfigSection = {
  title: '图片附件',
  icon: 'image',
  fields: [
    {
      key: 'attachment.image.auto_resize',
      label: '自动缩放',
      type: 'boolean',
      default: true,
      description: '超过尺寸限制时自动缩放图片',
    },
    {
      key: 'attachment.image.max_width',
      label: '最大宽度(px)',
      type: 'number',
      default: 2000,
      description: '缩放或拒绝前的最大图片宽度',
    },
    {
      key: 'attachment.image.max_height',
      label: '最大高度(px)',
      type: 'number',
      default: 2000,
      description: '缩放或拒绝前的最大图片高度',
    },
    {
      key: 'attachment.image.max_base64_bytes',
      label: '最大 Base64 字节数',
      type: 'number',
      default: 5242880,
      description: '图片 base64 编码后的最大字节数',
    },
  ],
};

// ===== 上下文压缩 =====
const compaction: ConfigSection = {
  title: '上下文压缩',
  icon: 'compress',
  fields: [
    {
      key: 'compaction.auto',
      label: '自动压缩',
      type: 'boolean',
      default: true,
      description: '上下文满时自动压缩会话',
    },
    {
      key: 'compaction.prune',
      label: '修剪旧工具输出',
      type: 'boolean',
      default: false,
      description: '修剪旧工具输出以节省 token',
    },
    {
      key: 'compaction.tail_turns',
      label: '保留尾部对话轮数',
      type: 'number',
      description: '压缩时逐字保留的最近用户轮次数',
    },
    {
      key: 'compaction.preserve_recent_tokens',
      label: '保留最近 Token 数',
      type: 'number',
      description: '压缩后逐字保留的最近 token 数上限',
    },
    {
      key: 'compaction.reserved',
      label: '预留 Token 缓冲',
      type: 'number',
      description: '压缩时的 token 缓冲区，避免压缩过程中溢出',
    },
  ],
};

// ===== 文件监听 =====
const watcher: ConfigSection = {
  title: '文件监听',
  icon: 'visibility',
  fields: [
    {
      key: 'watcher.ignore',
      label: '忽略模式',
      type: 'array',
      description: '文件监听忽略的 glob 模式列表（每行一个）',
    },
  ],
};

// ===== 工具输出 =====
const toolOutput: ConfigSection = {
  title: '工具输出限制',
  icon: 'output',
  fields: [
    {
      key: 'tool_output.max_lines',
      label: '最大行数',
      type: 'number',
      default: 2000,
      description: '工具输出超过此行数将被截断并保存到磁盘',
    },
    {
      key: 'tool_output.max_bytes',
      label: '最大字节数',
      type: 'number',
      default: 51200,
      description: '工具输出超过此字节数将被截断并保存到磁盘',
    },
  ],
};

// ===== 模型配置 / Provider（可重复添加） =====
const provider: ConfigSection = {
  title: '模型配置',
  icon: 'cloud',
  repeatable: true,
  itemKeyField: 'name',
  itemTemplate: {
    npm: '@ai-sdk/anthropic',
    options: {
      apiKey: '',
      baseURL: '',
      setCacheKey: true,
    },
    models: {},
  },
  fields: [
    {
      key: 'provider.{name}.npm',
      label: 'NPM 包名',
      type: 'string',
      placeholder: '@ai-sdk/anthropic',
      description: 'AI SDK 的 npm 包名',
    },
    {
      key: 'provider.{name}.api',
      label: 'API 标识',
      type: 'string',
      description: 'API 类型标识（如 openai, anthropic）',
    },
    {
      key: 'provider.{name}.id',
      label: 'Provider ID',
      type: 'string',
      description: 'Provider 唯一标识',
    },
    {
      key: 'provider.{name}.env',
      label: '环境变量列表',
      type: 'array',
      description: '此 Provider 需要的环境变量名列表',
    },
    {
      key: 'provider.{name}.whitelist',
      label: '模型白名单',
      type: 'array',
      description: '仅展示这些模型（每行一个）',
    },
    {
      key: 'provider.{name}.blacklist',
      label: '模型黑名单',
      type: 'array',
      description: '隐藏这些模型（每行一个）',
    },
    {
      key: 'provider.{name}.options.apiKey',
      label: 'API Key',
      type: 'string',
      secret: true,
      toggleable: true,
      placeholder: 'sk-...',
      description: 'API 密钥',
    },
    {
      key: 'provider.{name}.options.baseURL',
      label: 'Base URL',
      type: 'string',
      toggleable: true,
      placeholder: 'http://127.0.0.1:18444/route/v1',
      description: '自定义 API 基础地址',
    },
    {
      key: 'provider.{name}.options.setCacheKey',
      label: '启用缓存 Key',
      type: 'boolean',
      default: false,
      description: '确保为此 Provider 设置 promptCacheKey',
    },
    {
      key: 'provider.{name}.options.timeout',
      label: '请求超时(ms)',
      type: 'number',
      placeholder: '300000',
      description: '完整请求超时时间（毫秒），设为 false 禁用',
    },
    {
      key: 'provider.{name}.options.headerTimeout',
      label: '响应头超时(ms)',
      type: 'number',
      description: '等待响应头的超时时间（毫秒）',
    },
    {
      key: 'provider.{name}.options.chunkTimeout',
      label: '流式分块超时(ms)',
      type: 'number',
      description: 'SSE 流式分块之间的超时时间（毫秒）',
    },
    {
      key: 'provider.{name}.models',
      label: '模型列表',
      type: 'repeatable-object',
      childKeyLabel: '模型名',
      childTemplate: {
        name: '',
        attachment: false,
        reasoning: false,
        tool_call: false,
        temperature: false,
      },
      description: '在此添加自定义模型，每个模型可独立配置上下文窗口、输入输出限制、价格、思考等级、多模态等参数',
      children: [
        // ===== 基本属性 =====
        { key: 'name', label: '模型显示名', type: 'string', description: '模型在列表中显示的名称' },
        { key: 'id', label: '模型 ID', type: 'string', toggleable: true, description: '模型唯一标识（覆盖默认 key）' },
        { key: 'family', label: '模型系列', type: 'string', toggleable: true, description: '模型所属系列（如 claude, gemini, gpt）' },
        { key: 'release_date', label: '发布日期', type: 'string', toggleable: true, placeholder: '2025-01-01', description: '模型发布日期' },
        // ===== 能力声明 =====
        { key: 'attachment', label: '支持附件', type: 'boolean', default: false, description: '是否支持图片/文件附件上传' },
        { key: 'reasoning', label: '支持推理', type: 'boolean', default: false, description: '是否支持推理/思考链（extended thinking）' },
        { key: 'tool_call', label: '支持工具调用', type: 'boolean', default: false, description: '是否支持 function calling / tool use' },
        { key: 'temperature', label: '支持温度', type: 'boolean', default: false, description: '是否支持 temperature 参数' },
        { key: 'interleaved', label: '交错输出', type: 'boolean', default: false, toggleable: true, description: '是否支持推理与文本输出交错生成' },
        { key: 'experimental', label: '实验性', type: 'boolean', default: false, toggleable: true, description: '标记为实验性模型，可能不稳定' },
        {
          key: 'status',
          label: '模型状态',
          type: 'select',
          options: ['active', 'alpha', 'beta', 'deprecated'],
          toggleable: true,
          description: '模型生命周期状态',
        },
        // ===== 上下文与输入输出限制 =====
        { key: 'limit.context', label: '上下文窗口', type: 'number', description: '最大上下文 token 数（含输入+输出）' },
        { key: 'limit.output', label: '输出上限', type: 'number', description: '单次最大输出 token 数' },
        { key: 'limit.input', label: '输入上限', type: 'number', toggleable: true, description: '单次最大输入 token 数（不设则等于 context）' },
        // ===== 价格（每百万 token，美元） =====
        { key: 'cost.input', label: '输入价格', type: 'number', description: '每百万输入 token 价格（美元）' },
        { key: 'cost.output', label: '输出价格', type: 'number', description: '每百万输出 token 价格（美元）' },
        { key: 'cost.cache_read', label: '缓存读取价格', type: 'number', toggleable: true, description: '每百万缓存读取 token 价格' },
        { key: 'cost.cache_write', label: '缓存写入价格', type: 'number', toggleable: true, description: '每百万缓存写入 token 价格' },
        // ===== 多模态支持 =====
        { key: 'modalities.input', label: '输入模态', type: 'array', toggleable: true, description: '支持的输入模态（每行一个）' },
        { key: 'modalities.output', label: '输出模态', type: 'array', toggleable: true, description: '支持的输出模态（每行一个）' },
        // ===== 推理/思考等级配置（关键，默认可见） =====
        {
          key: 'options.reasoningEffort',
          label: '思考等级',
          type: 'select',
          options: ['none', 'minimal', 'low', 'medium', 'high', 'xhigh'],
          description: '推理思考强度（OpenAI/Gemini 系模型适用）。none=不思考, minimal=最低, low=低, medium=中, high=高, xhigh=超高',
        },
        {
          key: 'options.textVerbosity',
          label: '文本冗余度',
          type: 'select',
          options: ['low', 'medium', 'high'],
          description: '输出文本详细程度（OpenAI gpt-5 系适用）',
        },
        {
          key: 'options.reasoningSummary',
          label: '推理摘要',
          type: 'select',
          options: ['auto', 'concise', 'detailed', 'none'],
          description: '推理过程的摘要级别（OpenAI 系适用）',
        },
        {
          key: 'options.thinking.type',
          label: '思考模式',
          type: 'select',
          options: ['enabled', 'disabled'],
          description: 'Anthropic 系列模型的扩展思考模式开关',
        },
        { key: 'options.thinking.budgetTokens', label: '思考预算(token)', type: 'number', description: 'Anthropic 系列模型的思考 token 预算上限' },
        { key: 'options.include', label: '包含字段', type: 'array', toggleable: true, description: '请求中包含的额外字段（每行一个，如 reasoning.encrypted_content）' },
        // ===== HTTP 与 Provider 路由 =====
        { key: 'headers', label: '自定义请求头', type: 'kv-list', toggleable: true, description: '发送到此模型时的附加 HTTP 请求头（KEY=VALUE 每行一个）' },
        { key: 'options.provider', label: 'Provider 路由', type: 'object', toggleable: true, description: 'OpenRouter 等聚合器的上游路由配置（请在 JSON 编辑器中配置）' },
        // ===== 思考等级多选（勾哪个自动生成对应名 variant 卡片）=====
        {
          key: '_variantEffortsPicker',
          label: '思考等级（多选自动生成变体）',
          type: 'multiselect-variants',
          options: ['none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max'],
          targetRepeatableKey: 'variants',
          description: '勾选思考等级，自动在下方变体配置生成同名的 variant 卡片（reasoningEffort 等于该档），取消勾选自动删除对应 variant。max=最大思考、xhigh=超高、high=高、medium=中、low=低、minimal=最低、none=不思考',
        },
        // ===== 变体配置（可重复添加，无需 toggleable，直接显示） =====
        {
          key: 'variants',
          label: '变体配置',
          type: 'repeatable-object',
          childKeyLabel: '变体名',
          childTemplate: {
            reasoningEffort: 'high',
          },
          description: '自定义变体（如 high=max思考预算、low=低思考预算），展开配置每个变体的推理参数。上方"思考等级多选"勾哪个自动在此生成一条 variant',
          children: [
            { key: 'disabled', label: '禁用此变体', type: 'boolean', default: false, toggleable: true, description: '设为 true 禁用此变体' },
            {
              key: 'reasoningEffort',
              label: '思考等级',
              type: 'select',
              options: ['none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max'],
              description: '推理思考强度。none=不思考, minimal=最低, low=低, medium=中, high=高, xhigh=超高, max=最大',
            },
            {
              key: 'textVerbosity',
              label: '文本冗余度',
              type: 'select',
              options: ['low', 'medium', 'high'],
              toggleable: true,
              description: '输出文本详细程度',
            },
            {
              key: 'reasoningSummary',
              label: '推理摘要',
              type: 'select',
              options: ['auto', 'concise', 'detailed', 'none'],
              toggleable: true,
              description: '推理过程的摘要级别',
            },
            {
              key: 'thinking.type',
              label: '思考模式(Anthropic)',
              type: 'select',
              options: ['enabled', 'disabled'],
              toggleable: true,
              description: 'Anthropic 系列模型的扩展思考模式开关',
            },
            { key: 'thinking.budgetTokens', label: '思考预算(AI anthropic)', type: 'number', toggleable: true, description: 'Anthropic 系列模型的思考 token 预算上限' },
          ],
        },
      ],
    },
  ],
};

// ===== MCP 服务（可重复添加） =====
const mcp: ConfigSection = {
  title: 'MCP 服务',
  icon: 'extension',
  repeatable: true,
  itemKeyField: 'name',
  itemTemplate: {
    type: 'local',
    command: [],
    enabled: true,
    environment: {},
  },
  fields: [
    {
      key: 'mcp.{name}.type',
      label: '类型',
      type: 'select',
      options: ['local', 'remote'],
      default: 'local',
      description: 'local=本地进程, remote=远程服务',
    },
    {
      key: 'mcp.{name}.command',
      label: '启动命令',
      type: 'array',
      description: '命令及参数（每行一个）',
    },
    {
      key: 'mcp.{name}.enabled',
      label: '启用',
      type: 'boolean',
      default: true,
      description: '启动时是否加载此 MCP 服务',
    },
    {
      key: 'mcp.{name}.cwd',
      label: '工作目录',
      type: 'string',
      toggleable: true,
      description: 'MCP 进程的工作目录',
    },
    {
      key: 'mcp.{name}.environment',
      label: '环境变量',
      type: 'kv-list',
      description: '键值对，每行一个 KEY=VALUE',
    },
    {
      key: 'mcp.{name}.timeout',
      label: '超时(ms)',
      type: 'number',
      placeholder: '5000',
      description: 'MCP 请求超时时间（毫秒）',
    },
    {
      key: 'mcp.{name}.url',
      label: '远程 URL',
      type: 'string',
      toggleable: true,
      placeholder: 'https://...',
      description: '远程 MCP 服务地址（type=remote 时必填）',
    },
    {
      key: 'mcp.{name}.headers',
      label: '请求头',
      type: 'kv-list',
      toggleable: true,
      description: '远程请求附加头（键值对）',
    },
  ],
};

// ===== 权限配置 =====
const permission: ConfigSection = {
  title: '权限配置',
  icon: 'lock',
  fields: [
    {
      key: 'permission',
      label: '全局权限',
      type: 'select',
      options: ['ask', 'allow', 'deny'],
      toggleable: true,
      description: '全局默认权限策略',
    },
    {
      key: 'permission.read',
      label: '文件读取',
      type: 'select',
      options: ['ask', 'allow', 'deny'],
      toggleable: true,
    },
    {
      key: 'permission.edit',
      label: '文件编辑',
      type: 'select',
      options: ['ask', 'allow', 'deny'],
      toggleable: true,
    },
    {
      key: 'permission.bash',
      label: 'Bash 命令',
      type: 'select',
      options: ['ask', 'allow', 'deny'],
      toggleable: true,
    },
    {
      key: 'permission.glob',
      label: '文件搜索',
      type: 'select',
      options: ['ask', 'allow', 'deny'],
      toggleable: true,
    },
    {
      key: 'permission.grep',
      label: '内容搜索',
      type: 'select',
      options: ['ask', 'allow', 'deny'],
      toggleable: true,
    },
    {
      key: 'permission.list',
      label: '目录列表',
      type: 'select',
      options: ['ask', 'allow', 'deny'],
      toggleable: true,
    },
    {
      key: 'permission.task',
      label: '子任务',
      type: 'select',
      options: ['ask', 'allow', 'deny'],
      toggleable: true,
    },
    {
      key: 'permission.webfetch',
      label: '网页抓取',
      type: 'select',
      options: ['ask', 'allow', 'deny'],
      toggleable: true,
    },
    {
      key: 'permission.websearch',
      label: '网页搜索',
      type: 'select',
      options: ['ask', 'allow', 'deny'],
      toggleable: true,
    },
  ],
};

// ===== 实验性功能 =====
const experimental: ConfigSection = {
  title: '实验性功能',
  icon: 'science',
  fields: [
    {
      key: 'experimental.batch_tool',
      label: '批量工具',
      type: 'boolean',
      default: false,
      description: '启用 batch tool',
    },
    {
      key: 'experimental.openTelemetry',
      label: 'OpenTelemetry',
      type: 'boolean',
      default: false,
      description: '为 AI SDK 调用启用 OpenTelemetry span',
    },
    {
      key: 'experimental.continue_loop_on_deny',
      label: '拒绝后继续循环',
      type: 'boolean',
      default: false,
      description: '工具调用被拒绝后继续 Agent 循环',
    },
    {
      key: 'experimental.disable_paste_summary',
      label: '禁用粘贴摘要',
      type: 'boolean',
      default: false,
    },
    {
      key: 'experimental.mcp_timeout',
      label: 'MCP 全局超时(ms)',
      type: 'number',
      description: 'MCP 请求全局超时时间（毫秒）',
    },
  ],
};

// ===== Provider 白名单/黑名单 =====
const providerFilter: ConfigSection = {
  title: 'Provider 过滤',
  icon: 'filter_alt',
  fields: [
    {
      key: 'enabled_providers',
      label: '启用的 Provider 白名单',
      type: 'array',
      description: '设置后仅启用此列表中的 Provider，其余全部忽略',
    },
    {
      key: 'disabled_providers',
      label: '禁用的 Provider 黑名单',
      type: 'array',
      description: '禁用指定 Provider（优先级高于白名单）',
    },
  ],
};

// ===== 日志级别 =====
const logLevel: ConfigSection = {
  title: '日志',
  icon: 'log',
  fields: [
    {
      key: 'logLevel',
      label: '日志级别',
      type: 'select',
      options: ['DEBUG', 'INFO', 'WARN', 'ERROR'],
      toggleable: true,
      description: '日志输出级别',
    },
  ],
};

// ===== 指令文件 =====
const instructions: ConfigSection = {
  title: '指令文件',
  icon: 'description',
  fields: [
    {
      key: 'instructions',
      label: '指令文件列表',
      type: 'array',
      description: '附加指令文件路径或 glob 模式（每行一个）',
    },
  ],
};

// ===== 插件 =====
const plugin: ConfigSection = {
  title: '插件',
  icon: 'extension',
  fields: [
    {
      key: 'plugin',
      label: '插件列表',
      type: 'array',
      description: '从 npm 加载的插件名列表（每行一个）',
    },
  ],
};

// ===== Formatters =====
const formatter: ConfigSection = {
  title: '代码格式化',
  icon: 'format_align_left',
  fields: [
    {
      key: 'formatter',
      label: '启用格式化',
      type: 'boolean',
      toggleable: true,
      default: false,
      description: '设为 true 启用内置格式化器',
    },
  ],
};

// ===== LSP =====
const lsp: ConfigSection = {
  title: 'LSP 语言服务器',
  icon: 'code',
  fields: [
    {
      key: 'lsp',
      label: '启用 LSP',
      type: 'boolean',
      toggleable: true,
      default: false,
      description: '设为 true 启用内置 LSP 服务器',
    },
  ],
};

// ===== Agent 配置（可重复添加） =====
const agent: ConfigSection = {
  title: 'Agent 配置',
  icon: 'smart_toy',
  repeatable: true,
  itemKeyField: 'name',
  itemTemplate: {
    model: '',
    mode: 'primary',
  },
  fields: [
    {
      key: 'agent.{name}.model',
      label: '模型',
      type: 'model-select',
      description: '此 Agent 使用的模型，选项来自中继服务模型映射',
    },
    {
      key: 'agent.{name}.variant',
      label: '模型变体',
      type: 'string',
      toggleable: true,
      description: '此 Agent 默认使用的模型变体',
    },
    {
      key: 'agent.{name}.temperature',
      label: '温度',
      type: 'number',
      toggleable: true,
      description: '采样温度',
    },
    {
      key: 'agent.{name}.top_p',
      label: 'Top P',
      type: 'number',
      toggleable: true,
      description: '核采样参数',
    },
    {
      key: 'agent.{name}.prompt',
      label: '系统提示词',
      type: 'string',
      toggleable: true,
      description: '此 Agent 的系统提示词',
    },
    {
      key: 'agent.{name}.mode',
      label: 'Agent 模式',
      type: 'select',
      options: ['subagent', 'primary', 'all'],
      default: 'primary',
      description: 'subagent=子代理, primary=主代理, all=全部可用',
    },
    {
      key: 'agent.{name}.steps',
      label: '最大步数',
      type: 'number',
      toggleable: true,
      description: 'Agent 循环最大迭代次数，超过则强制输出纯文本',
    },
    {
      key: 'agent.{name}.disable',
      label: '禁用',
      type: 'boolean',
      default: false,
      description: '禁用此 Agent',
    },
    {
      key: 'agent.{name}.hidden',
      label: '隐藏',
      type: 'boolean',
      default: false,
      description: '在 @ 自动补全菜单中隐藏此 Agent',
    },
    {
      key: 'agent.{name}.description',
      label: '描述',
      type: 'string',
      toggleable: true,
      description: '何时使用此 Agent 的描述',
    },
    {
      key: 'agent.{name}.color',
      label: '颜色标识',
      type: 'string',
      toggleable: true,
      placeholder: '#FF5733 或 primary/severity/error 等',
      description: '十六进制颜色码或主题颜色名',
    },
    {
      key: 'agent.{name}.options',
      label: '额外选项',
      type: 'object',
      toggleable: true,
      description: '请在右侧 JSON 编辑器中直接配置',
    },
  ],
};

// ===== 自定义命令（可重复添加） =====
const command: ConfigSection = {
  title: '自定义命令',
  icon: 'terminal',
  repeatable: true,
  itemKeyField: 'name',
  itemTemplate: {
    template: '',
  },
  fields: [
    {
      key: 'command.{name}.template',
      label: '命令模板',
      type: 'string',
      description: '命令的提示词模板',
    },
    {
      key: 'command.{name}.description',
      label: '描述',
      type: 'string',
      toggleable: true,
      description: '命令的描述说明',
    },
    {
      key: 'command.{name}.agent',
      label: '指定 Agent',
      type: 'string',
      toggleable: true,
      description: '执行此命令时使用的 Agent',
    },
    {
      key: 'command.{name}.model',
      label: '指定模型',
      type: 'model-select',
      description: '执行此命令时使用的模型，选项来自中继服务模型映射',
    },
    {
      key: 'command.{name}.variant',
      label: '模型变体',
      type: 'string',
      toggleable: true,
      description: '命令使用的模型变体',
    },
    {
      key: 'command.{name}.subtask',
      label: '子任务模式',
      type: 'boolean',
      default: false,
      description: '以子任务模式执行此命令',
    },
  ],
};

// ===== Skills 扩展 =====
const skills: ConfigSection = {
  title: 'Skills 扩展',
  icon: 'school',
  fields: [
    {
      key: 'skills.paths',
      label: 'Skill 文件夹路径',
      type: 'array',
      description: '额外的 Skill 文件夹路径（每行一个）',
    },
    {
      key: 'skills.urls',
      label: 'Skill URL 列表',
      type: 'array',
      description: '从 URL 拉取 Skill 的地址列表（每行一个，如 https://example.com/.well-known/skills/）',
    },
  ],
};

// ===== 命名引用（可重复添加） =====
const references: ConfigSection = {
  title: '命名引用',
  icon: 'folder',
  repeatable: true,
  itemKeyField: 'name',
  itemTemplate: {
    repository: '',
  },
  fields: [
    {
      key: 'references.{name}.repository',
      label: 'Git 仓库地址',
      type: 'string',
      toggleable: true,
      placeholder: 'https://github.com/user/repo',
      description: 'Git 仓库 URL（Git 引用模式）',
    },
    {
      key: 'references.{name}.branch',
      label: '分支',
      type: 'string',
      toggleable: true,
      description: 'Git 分支名（Git 引用模式）',
    },
    {
      key: 'references.{name}.path',
      label: '本地路径',
      type: 'string',
      toggleable: true,
      placeholder: '/path/to/dir',
      description: '本地目录路径（本地引用模式；与 repository 互斥）',
    },
    {
      key: 'references.{name}.description',
      label: '描述',
      type: 'string',
      toggleable: true,
      description: '引用的描述说明',
    },
    {
      key: 'references.{name}.hidden',
      label: '隐藏',
      type: 'boolean',
      default: false,
      description: '隐藏此引用',
    },
  ],
};

// ===== 企业配置 =====
const enterprise: ConfigSection = {
  title: '企业配置',
  icon: 'business',
  fields: [
    {
      key: 'enterprise.url',
      label: '企业 URL',
      type: 'string',
      toggleable: true,
      placeholder: 'https://enterprise.example.com',
      description: '企业版 URL',
    },
  ],
};

export const opencodeSchema: AgentSchema = {
  agentId: 'opencode',
  sections: [
    basicSettings,
    provider,
    autoupdate,
    snapshot,
    share,
    server,
    attachment,
    compaction,
    watcher,
    toolOutput,
    providerFilter,
    logLevel,
    instructions,
    plugin,
    formatter,
    lsp,
    experimental,
    permission,
    agent,
    command,
    skills,
    references,
    enterprise,
    mcp,
  ],
};
