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
