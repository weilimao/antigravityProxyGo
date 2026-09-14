// schemas/codex.ts: OpenAI Codex 配置项 schema 定义。
// 基于 https://developers.openai.com/codex/config-schema.json 官方 JSON Schema 提取的 UI 可视化字段。
// 对应 ~/.codex/config.json 配置文件。

import { AgentSchema, ConfigSection } from '../types';

// ===== 中继与网关认证 =====
const authSettings: ConfigSection = {
  title: '中继与网关认证 (Auth)',
  icon: 'key',
  fields: [
    {
      key: 'auth.OPENAI_API_KEY',
      label: '网关 API Key (OPENAI_API_KEY)',
      type: 'string',
      secret: true,
      placeholder: 'sk-ant-...',
      description: 'Codex 请求中继网关服务时使用的访问认证密钥，保存后自动同步至 ~/.codex/auth.json',
    },
  ],
};

// ===== 基础与模型设置 =====
const basicSettings: ConfigSection = {
  title: '基础与模型设置',
  icon: 'settings',
  fields: [
    {
      key: 'model',
      label: '默认模型',
      type: 'model-select',
      description: '默认使用的模型标识（如 gpt-4o, o3-mini, codex-mini），选项来自中继服务模型映射',
    },
    {
      key: 'model_provider',
      label: '默认提供商 (model_provider)',
      type: 'string',
      default: 'openai',
      placeholder: 'openai',
      description: '默认使用的模型提供商标识（对应下方 model_providers 中的名称）',
    },
    {
      key: 'openai_base_url',
      label: '内置 OpenAI 端点覆盖 (openai_base_url)',
      type: 'string',
      toggleable: true,
      placeholder: 'http://127.0.0.1:18444/v1',
      description: '覆盖内置 openai 提供商的 Base URL（填入本地中继服务地址即可直连）',
    },
    {
      key: 'model_reasoning_effort',
      label: '思考等级 (reasoning_effort)',
      type: 'select',
      allowCustom: true,
      options: ['none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max'],
      description: '推理思考强度（适用于 o1, o3, o4 等推理模型，支持预设档位或自定义输入如 32768/max 等）',
    },
    {
      key: 'model_reasoning_summary',
      label: '推理摘要 (reasoning_summary)',
      type: 'select',
      options: ['auto', 'concise', 'detailed', 'none'],
      description: '推理思考过程的摘要输出详细级别',
    },
    {
      key: 'model_verbosity',
      label: '文本详细度 (model_verbosity)',
      type: 'select',
      options: ['low', 'medium', 'high'],
      description: '模型生成文本内容的冗余与详细程度',
    },
    {
      key: 'review_model',
      label: '代码审查专用模型 (review_model)',
      type: 'model-select',
      description: '执行 /review 代码审查功能时专用的模型',
    },
    {
      key: 'service_tier',
      label: '服务分级 (service_tier)',
      type: 'select',
      options: ['default', 'priority', 'flex'],
      description: 'OpenAI API 调用的服务请求优先级分档',
    },
  ],
};

// ===== 模型提供商配置（可重复添加） =====
const modelProviders: ConfigSection = {
  title: '模型提供商 (model_providers)',
  icon: 'cloud',
  repeatable: true,
  itemKeyField: 'name',
  itemTemplate: {
    name: '',
    base_url: 'http://127.0.0.1:18444/v1',
    env_key: 'OPENAI_API_KEY',
    requires_openai_auth: true,
    wire_api: 'responses',
  },
  fields: [
    {
      key: 'model_providers.{name}.name',
      label: '提供商名称',
      type: 'string',
      placeholder: 'Local Relay',
      description: '此提供商的显示名称',
    },
    {
      key: 'model_providers.{name}.base_url',
      label: 'API 基础地址 (Base URL)',
      type: 'string',
      placeholder: 'http://127.0.0.1:18444/v1',
      description: '该提供商的 API 基础端点地址',
    },
    {
      key: 'model_providers.{name}.requires_openai_auth',
      label: '携带网关认证 (requires_openai_auth)',
      type: 'boolean',
      default: true,
      description: '启用后，Codex 请求该提供商时将自动携带上述网关 API Key 进行身份认证',
    },
    {
      key: 'model_providers.{name}.env_key',
      label: '密钥环境变量名 (env_key)',
      type: 'string',
      placeholder: 'OPENAI_API_KEY',
      description: '用于读取该提供商 API Key 的环境变量名称',
    },
    {
      key: 'model_providers.{name}.wire_api',
      label: '通信协议 (wire_api)',
      type: 'select',
      options: ['responses', 'chat'],
      default: 'responses',
      description: '通信协议格式（responses 为 OpenAI 现代规范，chat 为传统兼容格式）',
    },
    {
      key: 'model_providers.{name}.headers',
      label: '自定义请求头',
      type: 'kv-list',
      toggleable: true,
      description: '附加到 HTTP 请求中的自定义请求头（KEY=VALUE 每行一个）',
    },
    {
      key: 'model_providers.{name}.query_params',
      label: '自定义查询参数',
      type: 'kv-list',
      toggleable: true,
      description: '附加到 API 请求 URL 中的 Query 参数（KEY=VALUE 每行一个）',
    },
  ],
};

// ===== 模型列表（Catalog 可视化编辑，仅 Codex） =====
// 对应 cc-switch-model-catalog.json 文件中的 models 数组。
// 页面加载时会把数组转为以 slug 为 key 的 object 回填表单，保存时再转回数组。
const modelCatalog: ConfigSection = {
  title: '模型列表 (Catalog)',
  icon: 'view_list',
  repeatable: true,
  isModelList: true,
  itemKeyField: 'slug',
  itemTemplate: {
    display_name: '',
    description: '',
    context_window: 128000,
    max_context_window: 128000,
    default_reasoning_level: 'high',
    input_modalities: ['text'],
    supports_reasoning_summaries: true,
    supports_parallel_tool_calls: false,
    supports_search_tool: false,
    support_verbosity: false,
    supports_image_detail_original: false,
    visibility: 'list',
    priority: 1000,
    truncation_policy: { limit: 10000, mode: 'bytes' },
    supported_reasoning_levels: [
      { effort: 'none', description: 'Disable Thinking' },
      { effort: 'low', description: 'Low Thinking Effort' },
      { effort: 'medium', description: 'Medium Thinking Effort' },
      { effort: 'high', description: 'High Thinking Effort' },
      { effort: 'max', description: 'Max Thinking Effort' },
    ],
    base_instructions: "You are Codex, a coding agent. You and the user share the same workspace and collaborate to achieve the user's goals.",
    additional_speed_tiers: [],
    availability_nux: null,
    default_reasoning_summary: 'none',
    experimental_supported_tools: [],
    service_tiers: [],
    upgrade: null,
    shell_type: 'shell_command',
    supported_in_api: true,
    effective_context_window_percent: 95,
  },
  fields: [
    {
      key: 'models.{name}.slug',
      label: '模型标识 (slug)',
      type: 'string',
      placeholder: 'nvidia/z-ai/glm-5.2',
      description: '在 /model 菜单中显示的模型唯一标识，也是实际请求时发送的模型名',
    },
    {
      key: 'models.{name}.display_name',
      label: '显示名称',
      type: 'string',
      placeholder: 'deepseek-ai/deepseek-v4-flash',
      description: '在模型选择列表右侧展示的友好名称',
    },
    {
      key: 'models.{name}.description',
      label: '模型描述',
      type: 'string',
      toggleable: true,
      description: '模型的附加描述信息（通常与 display_name 相同）',
    },
    {
      key: 'models.{name}.context_window',
      label: '上下文窗口',
      type: 'number',
      default: 128000,
      description: '模型可用上下文 token 数',
    },
    {
      key: 'models.{name}.max_context_window',
      label: '最大上下文窗口',
      type: 'number',
      default: 128000,
      description: '模型支持的最大上下文 token 数',
    },
    {
      key: 'models.{name}.effective_context_window_percent',
      label: '有效上下文百分比',
      type: 'number',
      default: 95,
      description: '实际可使用的上下文占 context_window 的百分比',
    },
    {
      key: 'models.{name}.default_reasoning_level',
      label: '默认思考等级',
      type: 'select',
      options: ['none', 'low', 'medium', 'high', 'max'],
      default: 'high',
      description: '模型默认启动时使用的思考强度',
    },
    {
      key: 'models.{name}.default_reasoning_summary',
      label: '默认推理摘要',
      type: 'select',
      options: ['none', 'auto', 'concise', 'detailed'],
      default: 'none',
      description: '推理思考过程的摘要输出默认级别',
    },
    {
      key: 'models.{name}.input_modalities',
      label: '输入模态',
      type: 'array',
      description: '模型支持的输入模态列表（每行一个：text / image / audio；多个模态分多行填写，不要在同一行用逗号分隔）',
    },
    {
      key: 'models.{name}.supports_reasoning_summaries',
      label: '支持推理摘要',
      type: 'boolean',
      default: true,
      description: '是否支持输出推理思考过程摘要',
    },
    {
      key: 'models.{name}.supports_parallel_tool_calls',
      label: '支持并行工具调用',
      type: 'boolean',
      default: false,
      description: '是否支持在单次请求中并行调用多个工具',
    },
    {
      key: 'models.{name}.supports_search_tool',
      label: '支持搜索工具',
      type: 'boolean',
      default: false,
      description: '是否支持内置搜索工具',
    },
    {
      key: 'models.{name}.support_verbosity',
      label: '支持详细度控制',
      type: 'boolean',
      default: false,
      description: '是否支持 model_verbosity 参数',
    },
    {
      key: 'models.{name}.supports_image_detail_original',
      label: '保留原始图片分辨率',
      type: 'boolean',
      default: false,
      description: '是否在传入图片时保留原始分辨率（不压缩）',
    },
    {
      key: 'models.{name}.supported_in_api',
      label: 'API 可用',
      type: 'boolean',
      default: true,
      description: '是否在 API 中可调用',
    },
    {
      key: 'models.{name}.visibility',
      label: '可见性',
      type: 'select',
      options: ['list', 'hidden'],
      default: 'list',
      description: 'list=显示在模型选择列表中, hidden=隐藏但可通过 -m 指定',
    },
    {
      key: 'models.{name}.priority',
      label: '排序优先级',
      type: 'number',
      default: 1000,
      description: '在模型列表中的排序权重（数字越小越靠前）',
    },
    {
      key: 'models.{name}.shell_type',
      label: 'Shell 类型',
      type: 'string',
      default: 'shell_command',
      description: '代码执行时使用的 shell 类型',
    },
    {
      key: 'models.{name}.base_instructions',
      label: '系统基础指令',
      type: 'string',
      toggleable: true,
      description: '注入到对话的系统基础指令文本',
    },
    {
      key: 'models.{name}.truncation_policy',
      label: '截断策略',
      type: 'object',
      toggleable: true,
      description: '上下文截断策略配置（请在右侧 JSON 编辑器中设定 limit 和 mode）',
    },
    {
      key: 'models.{name}.supported_reasoning_levels',
      label: '支持的思考等级',
      type: 'array',
      description: '模型支持的思考等级列表（每行一个 JSON 对象，如 {"effort": "high", "description": "High Thinking Effort"}）',
    },
  ],
};

// ===== 工具与特性 =====
const toolsAndFeatures: ConfigSection = {
  title: '工具与特性',
  icon: 'build',
  fields: [
    {
      key: 'web_search',
      label: '联网搜索模式',
      type: 'select',
      options: ['disabled', 'cached', 'indexed', 'live'],
      default: 'live',
      description: '控制 Web 搜索工具模式（live=实时在线搜索, disabled=禁用）',
    },
    {
      key: 'tool_output_token_limit',
      label: '工具输出 Token 上限',
      type: 'number',
      placeholder: '4000',
      description: '将工具/函数执行结果存入上下文时的 Token 预算上限',
    },
    {
      key: 'show_raw_agent_reasoning',
      label: '显示原始推理过程',
      type: 'boolean',
      default: false,
      description: '开启后在终端输出中直接显示模型原始的思考过程事件',
    },
    {
      key: 'suppress_unstable_features_warning',
      label: '屏蔽实验性功能警告',
      type: 'boolean',
      default: false,
      description: '屏蔽关于开发中/不稳定特性的提示信息',
    },
  ],
};

// ===== 沙箱与安全 =====
const sandboxSettings: ConfigSection = {
  title: '沙箱与安全隔离',
  icon: 'security',
  fields: [
    {
      key: 'sandbox_mode',
      label: '沙箱模式 (sandbox_mode)',
      type: 'select',
      options: ['read_only', 'workspace_write', 'full'],
      default: 'workspace_write',
      description: '执行环境沙箱隔离级别（workspace_write 仅允许修改工作区文件）',
    },
    {
      key: 'project_root_markers',
      label: '项目根目录标记',
      type: 'array',
      description: '向上查找识别项目根目录的标记文件/目录名（每行一个，默认 .git）',
    },
    {
      key: 'project_doc_max_bytes',
      label: '项目文档最大读取字节',
      type: 'number',
      default: 32768,
      description: '从 AGENTS.md 提取项目指南的最大字节数（默认 32KB）',
    },
  ],
};

// ===== 多智能体协同 =====
const multiAgentSettings: ConfigSection = {
  title: '多智能体协同 (agents)',
  icon: 'groups',
  fields: [
    {
      key: 'agents.enabled',
      label: '启用多智能体协同',
      type: 'boolean',
      default: true,
      description: '是否启用子 Agent 派生与多智能体分工机制',
    },
    {
      key: 'agents.default_subagent_model',
      label: '子 Agent 默认模型',
      type: 'model-select',
      description: '未显式指定时派生子 Agent 所使用的模型',
    },
    {
      key: 'agents.default_subagent_reasoning_effort',
      label: '子 Agent 思考等级',
      type: 'select',
      allowCustom: true,
      options: ['none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max'],
      description: '子 Agent 默认的推理思考强度（支持预设档位或自定义输入）',
    },
    {
      key: 'agents.max_depth',
      label: '子 Agent 最大嵌套深度',
      type: 'number',
      default: 2,
      description: '子 Agent 允许递归调用派生子 Agent 的最大层级',
    },
    {
      key: 'agents.max_concurrent_threads_per_session',
      label: '并发线程数上限',
      type: 'number',
      default: 5,
      description: '单会话中允许同时并发运行的子 Agent 最大线程数',
    },
  ],
};

// ===== MCP 扩展服务（可重复添加） =====
const mcpServers: ConfigSection = {
  title: 'MCP 扩展服务 (mcp_servers)',
  icon: 'extension',
  repeatable: true,
  itemKeyField: 'name',
  itemTemplate: {
    type: 'stdio',
    command: 'node',
    args: [],
    env: {},
  },
  fields: [
    {
      key: 'mcp_servers.{name}.type',
      label: '传输协议类型',
      type: 'select',
      options: ['stdio', 'sse', 'http'],
      default: 'stdio',
      description: 'MCP 服务的通信协议（stdio / sse / http）',
    },
    {
      key: 'mcp_servers.{name}.command',
      label: '启动命令',
      type: 'string',
      placeholder: 'node',
      description: 'MCP 服务的启动可执行命令（如 node, python, npx）',
    },
    {
      key: 'mcp_servers.{name}.args',
      label: '启动参数',
      type: 'array',
      description: '传递给启动命令的参数列表（每行一个）',
    },
    {
      key: 'mcp_servers.{name}.env',
      label: '环境变量',
      type: 'kv-list',
      description: '传递给 MCP 进程的环境变量（KEY=VALUE 每行一个）',
    },
    {
      key: 'mcp_servers.{name}.url',
      label: 'SSE / HTTP 端点',
      type: 'string',
      toggleable: true,
      placeholder: 'http://localhost:8000/sse',
      description: '远程 MCP 服务的 HTTP/SSE 端点地址',
    },
    {
      key: 'mcp_servers.{name}.cwd',
      label: '工作目录 (cwd)',
      type: 'string',
      toggleable: true,
      description: 'MCP 进程启动时的工作目录绝对路径',
    },
  ],
};

// ===== 终端与界面 =====
const tuiSettings: ConfigSection = {
  title: '终端与界面 (TUI)',
  icon: 'terminal',
  fields: [
    {
      key: 'tui.alt_screen_mode',
      label: '屏幕缓冲模式 (alt_screen_mode)',
      type: 'select',
      options: ['auto', 'always', 'never'],
      default: 'auto',
      description: 'auto=自适应, always=始终启用全屏缓冲, never=保留原生终端回滚',
    },
    {
      key: 'tui.theme',
      label: 'TUI 主题',
      type: 'string',
      placeholder: 'dark',
      description: '终端用户界面的颜色主题',
    },
  ],
};

export const codexSchema: AgentSchema = {
  agentId: 'codex',
  sections: [
    authSettings,
    basicSettings,
    modelProviders,
    modelCatalog,
    mcpServers,
    toolsAndFeatures,
    sandboxSettings,
    multiAgentSettings,
    tuiSettings,
  ],
};
