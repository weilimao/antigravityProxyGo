### v1.6.0 更新日志

- **模型统计时间范围筛选**：
  - 模型统计表新增「全部 / 今日 / 近三天 / 近七天」时间范围筛选栏：「全部」直接复用全量累计数据（零开销、口径与原视图完全一致）；其余范围由后端按 `request_logs` 时间戳聚合计算（新增 `QueryModelStatsSince` 聚合查询与 `stats:model-range` IPC 通道）；
  - 范围聚合视图在统计心跳刷新时保持冻结，仅在切换筛选时重新拉取，避免聚合视图抖动；
  - 筛选栏支持中英双语国际化词条。

- **Windows 托盘图标点击无响应（幽灵图标）修复**：
  - 托盘消息循环 goroutine 显式 `runtime.LockOSThread()`：Windows 托盘窗口的消息队列存在线程亲和，未锁定的 goroutine 被 runtime 迁移到其他 OS 线程后，点击消息滞留旧线程队列，表现为图标可见但点击无响应；
  - 「显示控制面板」回调中窗口显示与可见状态补偿全部异步化，避免在 systray 消息泵线程上同步阻塞导致托盘假死。

- **Other 号池 Cloudflare Worker 出口代理 404 修复**：
  - Worker 代理地址改写逻辑从「整段覆盖 BaseURL」改为「仅替换目标 URL 的 scheme/host」，完整保留原始 path 与 query，修复裸 Worker 域名被重拼 `/v1/chat/completions` 丢失 `/api` 等前缀导致上游 404 的问题（与 Antigravity 号池同口径）；
  - `X-Target-Upstream` 头改为透传完整原始目标 URL，Worker 侧转发寻址更精确。

- **安装包版本号对齐**：NSIS 安装器版本宏与主程序版本保持同步。
