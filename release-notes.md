### v1.4.1 更新日志

- **NVIDIA 中继断流自动恢复与流式转换升级**：
  - **断流自动嗅探与重连恢复 (Resume Sink)**：针对长思考模型（如 Kimi-k3 / DeepSeek-R1）在超长推理及工具调用临界点偶发遭遇的 30s 网络静默与 `unexpected EOF` 中断，重构流式断流恢复机制，自动平滑重试并完成上下文拼接；
  - **双向无损分流与 SSE 转换 (Tee Sink & Stream Translator)**：重构流式响应 Tee 架构，实现 `reasoning_content` 思考流、`tool_calls` 参数流与正文内容的多路无损分流及 Anthropic / OpenAI 协议透明转换；
  - **断流恢复全量回归测试套件**：新增 10+ 个端到端流式断流断点恢复与拼接测试（`nvidia_resume_test.go`），保障高并发流式中继零丢字。

- **中继模型与 NVIDIA 优选模型变更对比与快照管理系统**：
  - **映射状态智能比对与失效告警**：支持对当前配置的中继映射模型与 NVIDIA 官方最新上线/下线模型进行全量快照比对，自动标记已失效（Stale）与新上线（New）模型；
  - **快照持久化与 Tab 隔离比对**：支持多号池独立对比与快照持久化，提供纯函数比对算法单元测试套件（`relayModelDiff.test.ts`）。

- **Agent 配置管理与智能模型搜索选择器**：
  - **ModelSearchSelect 智能模糊搜索选择器**：替换原生下拉框，支持拼音/英文多关键词模糊过滤、关键词高亮与自定义新模型自由录入；
  - **模型选择与展示名称自动联动**：选择槽位模型时自动剥离后缀并同步填入展示名称，同时保留手动自定义修改的自由度。

- **全景动态壁纸管理系统 (Antigravity Background)**：
  - 新增全景动态背景组件与配置持久化，支持 Agent 工作区与系统主题全局联动。

- **系统性能与监控增强**：
  - **Windows 内存监控优化**：修复 Windows 环境下进程工作集与物理内存采样统计准确性；
  - **WebView2 渲染器参数调优**：注入 GPU 命令缓冲区定期清理参数，防止长周期运行下的渲染进程内存膨胀；
  - **新增深度慢思考与工具调用探针工具**：内置 `scripts/nvidia_tool_call_probe`，支持实时流式打字刷屏与复杂并发推演时序量化。

### v1.4.0 更新日志

- **全新功能：Agent 配置管理系统 (Claude Code / Codex / OpenCode)**：
  - **多 Agent 可视化与源码双向联动**：支持自动发现、读取与可视化编辑 Claude Code、Codex、OpenCode 等客户端配置文件，支持表单与原始 JSON 双向热同步与校验，修改保存前自动创建 `.bak` 安全备份；
  - **Claude Code 深度集成**：支持 Sonnet / Opus / Haiku 模型槽位与行内 `[1M]` 超大上下文后缀自由勾选；支持默认兜底模型配置；实现与 `~/.claude.json` 全局 MCP 服务的双向联动读取与安全同步；
  - **Codex 模型 Catalog 目录联动**：支持与 Codex 外部 `cc-switch-model-catalog.json` 深度打通，采用点号免疫的精确反向匹配提取算法，完美支持带版本号（如 `qwen3.8`、`glm-5.2`、`gemini-3.6`）的模型目录解析与展示；
  - **OpenCode 树形层级修复**：重构嵌套 `repeatable-object` 路径归属逻辑，解决模型参数误拆为顶层 Provider 的问题，恢复清晰的提供商/模型层级。

- **中继转发与 UI 交互体验优化**：
  - **中继模型映射表**：移除冗余的「可选思考等级 (Variants)」列，界面更聚焦简洁；
  - **配置面板**：Agent 配置管理支持自动激活首个 Agent，消除初次进入空白的问题。

- **NVIDIA 出站代理与高并发稳定性增强**：
  - 新增 Dedicated Proxy 专线出站代理机制与并发互斥保护；
  - OCR 批处理能力增强与全量单元测试覆盖。

### v1.3.3 更新日志

- **关键修复：账号管理器自死锁致高并发下请求全链路卡死**：
  - 修复 `sync.RWMutex` 不可重入导致的致命自死锁：冷却期监控与 Token 同步刷新在持写锁/读锁临界区内再次调用 `GetAccountByID`（内部重复 `RLock`），运行时既不报错也不让步，整把账号管理器锁被永久焊死，所有选号热路径（`GetAvailableAccountsForChannel` 等）排队等锁，表现为运行一段时间后入站请求全部卡在路由转发后、上游中继前的死锁状态；
  - 抽出无锁内查 `getAccountByIDLocked()` 供已持锁临界区使用，消除 `CooldownMonitor` 刷新配额失败分支与 `RefreshAccountTokenSync` 两处自死锁点，并补充切片 nil 指针守卫防潜在 panic；
  - 新增死锁回归测试套件（`TestCooldownMonitor_FetchQuotaError_NoDeadlock` / `TestRefreshAccountTokenSync_Concurrent_NoDeadlock`），以超时守卫钉死两类死锁场景，防止未来回归。

### v1.3.2 更新日志

- **账号体系与号池持久化升级**：
  - 支持账号数据 Provider 分区持久化与定向增量保存，将单一 `accounts.json` 拆分为 7 个独立分区 JSON，消解数千账号高并发场景下的写磁盘 I/O 卡顿瓶颈；
  - 新增账号列表批量勾选删除及二次确认弹窗功能；
  - 支持账号号池面板自定义布局与排序偏好的后端持久化（`accounts_pool.json`）。

- **Grok (xAI) 号池深度支持与稳定性优化**：
  - 新增 Grok 用量统计独立 Tab 与 Token 限额控制体系；
  - 支持 Grok 账号一键解除冷静期，优化 OAuth Token 刷新抗抖动策略（防止批量并发刷新触发风控及 `invalid_grant` 误停用）；
  - 新增 Grok 专属 1 小时授权过期检查与后台静默刷新监控。

- **中继转发与协议增强 (Relay & Protocol)**：
  - `/route` 智能路由转发入口支持 `/v1/messages/count_tokens` 本地字符级粗估端点（零上游消耗、不计费），避免 Claude Code 产生额外推理请求与号池消耗；
  - 深度优化 Anthropic 协议思考块（Extended Thinking）兼容性（开块携带标准 `signature` 空串占位，解决 Claude Code `MessageAccumulator` 丢弃思考正文的问题）；
  - 优化思考等级（`reasoning_effort`）映射逻辑，支持阿里云 MaaS / DeepSeek 等上游端点顶格 `max` 档位 1:1 透传。

- **前端交互与国际化体验提升**：
  - 补齐多模块弹窗的中英双语国际化词条；
  - 修复 Grok / NVIDIA / Other 号池编辑弹窗偶发无法打开的问题。
