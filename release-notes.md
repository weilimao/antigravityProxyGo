### v1.7.4 更新日志

- **WorkBuddy 400 / 11128 渠道校验拦截修复（重点）**：
  - 会话标识规整：新增 `workbuddyConversationID`，把内部会话键（`claude:` / `opencode:` / `acc:` / `auth:acc:` / `sock:acc:` 等带前缀形态）经 SHA-256 派生为确定性 UUID 后写入 `X-Conversation-ID`。此前内部前缀直接外泄，既不符合上游 UUID 格式要求，`claude:` 本身就是「非官方客户端」的最强指纹；派生方案保持同一会话的粘性语义不变；
  - 请求头 1:1 复刻官方桌面端：`applyWorkBuddyChatHeaders` 补齐 `x-codebuddy-request`、`X-Product`、`X-Private-Data`、`X-Agent-Intent`、`X-Agent-Purpose` 等 app-server 安全头，以及 `traceparent` / `b3` / `X-B3-*` / `X-Trace-ID` 链路追踪头；新增 `X-Current-Account-Id`（与 `X-User-Id` 同值）；
  - 提示词指纹清洗：system 消息在通用清洗后再泛化残留 `claude` / `anthropic` 字样，并抹除 `Main branch (you will usually use this for PRs)` 句式指纹（实测单句即可触发 11128），只去掉括号注释，语义不丢；
  - 号池保护策略调整：11128 不再冷冻账号（避免一个坏载荷连坐整池），改为本请求内跳过该账号换号重试；全部账号失败时回传上游原始错误体，客户端看到真实原因而非包装后的 502；被拦载荷与请求头摘要落本机临时目录用于排查。

- **WorkBuddy 客户端版本按区域回退**：
  - 新增 `GetWorkBuddyCliVersionRaw`，返回未回退默认值的配置原文，由 `BuildWorkBuddyHeaders` 按账号区域选择默认版本（国际版 `5.7.6` / 国内版 `5.7.7`）。此前统一传国内默认版本，国际版账号会被顶替成不匹配的版本。

- **请求日志重复记账修复**：
  - 新增 `Tracker.HasRequest` 与同 ID 幂等合并，中继侧 `relayRecordUsage` 与 proxyHandler 记账互斥，消除请求量翻倍与成对重复行；
  - 远端转发日志改用 `AddRequestLogInMemoryOnly`，不再异步二次写入 `mode="local"` 重复记录；
  - 日志 `ReqID` 复用 `X-Antigravity-Req-ID`，贯通去重链路；补齐 `CacheStatus` 字段（HIT / MISS / NONE），前端缓存徽标兼容仅有 `cachedTokens` 的回填历史行。

- **首页顶部广告横幅轮播**：
  - 新增 `AdCarousel` 组件与 `adService` 数据服务，从后端拉取公开免鉴权广告列表；
  - 具备 3 秒超时隔离与多端口回退（8090 → 8000）容错，本地轻量缓存 + 服务不可达时的降级默认宣传项；
  - 支持自动轮播、悬停暂停、手动切换、图片加载失败兜底与中英文标题切换。

- **WorkBuddy 请求区域错位修复**：
  - 路由转发改写请求体模型前，把客户端原始模型名写入 `X-Antigravity-Original-Model`；选号阶段优先用它判断区域亲和性，避免带前缀模型（`workbuddy/cn/*`、`workbuddy/intl/*`）在请求体 model 被映射为裸模型名后丢失区域意图，导致国内请求被分到国际账号。
  - 号池中无匹配区域账号时降级使用全量号池，不影响可用性。
