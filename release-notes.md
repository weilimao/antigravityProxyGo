### v1.7.8 更新日志

- **官方 Antigravity 原生流量指纹动态嗅探器与零人工无感热对齐（重点）**：
  - **动态嗅探官方最新指纹**：代理接入层（`handler.go`）新增原生客户端流量嗅探器（`handler_sniffer.go`），在官方白色桌面端或 IDE 通信时自动从 User-Agent 智能提取官方最新 Hub 版本号与 Build CL 构建号；
  - **自动热更新与持久化**：当检测到官方上游版本升级（如通过语义化版本比较 `CompareVersion` 判定新版本），自动反哺更新号池全局指纹并原子落盘，同时通过事件系统广播至前端界面并记录系统日志，彻底实现指纹零人工介入的自愈与自适应；
  - **前端编辑保护防争用**：前端号池 Hub 版本输入框增加编辑聚焦保护与防抖机制，避免后台自动嗅探刷新覆盖用户正在编辑的自定义版本。

- **默认官方指纹升级至 v2.29.1**：
  - 默认 Hub 版本升级为 `2.29.1`，官方构建号对齐最新 `cl=1015941558`；
  - 代理伪装层、配额查询引擎（`quota.go` 与 `quota_template.go`）及测试保活模块全链路同步采用最新官方指纹。

- **号池配置拆分与构建号独立持久化**：
  - 存储层新增 `antigravityBuildCL` 独立字段存储与向后兼容迁移，支持单独配置或通过官方流量自动学习覆盖。

### v1.7.7 更新日志

- **逆向深度对齐官方 Antigravity Desktop (v2.29.0) TLS ClientHello 指纹与协议契约（重点防风控）**：
  - **TLS ClientHello 1:1 精确对齐**：逆向官方二进制程序 `language_server_windows_x64.exe`（`codeassistclient.CreateCloudCodeHTTPClient` @ `0x20f6c60`），将出站隔离传输层配置对齐为 `ForceAttemptHTTP2: false` 与 `ClientSessionCache: nil`，使出站 TLS ClientHello 不发送 `ALPN (0x0010)`、`SessionTicket (0x0023)`、`PSKKeyExchangeModes (0x002d)` 与 `PreSharedKey (0x0029)` 扩展，JA3/JA4 请求指纹与官方桌面端 100% 一致；
  - **User-Agent 构建号全链路动态注入**：User-Agent 升级为包含官方构建号格式 `antigravity/hub/<ver> (aidev_client; os_type=windows; arch=amd64; cl=1013060670)`，默认版本升级为 `2.29.0`，全系统（MITM 代理、中继接口、测试保活、配额刷新）统一实时联动前端界面「Hub版本」配置；
  - **官方 sessionId 协议规范落地**：对话与自动化保活请求对齐官方负十进制整数会话规范（`sessionId: "-..."`），消除字符串/缺失导致的特征识别；
  - **ASAR 补丁幂等与自动去重**：增强 Electron `app.asar` 代理环境注入逻辑，实现智能检测、幂等跳过与历史重复注入块自动剥离收敛。

- **号池可用模型主动探测与受限模型（Argon / Claude 5.5）可视化及智能调度隔离**：
  - 账号管理工具栏新增「检测可用模型」功能与可视化弹窗（`AccountModelsModal.vue`），主动批量探测各账号真实开通模型，并直观罗列受限模型（Argon / Claude 5.5）；
  - 调度器构建三层能力画像与 404 负向记忆学习机制，遇到受限模型时自动规避不支持账号、精准分发至已开通账号，杜绝 404 换号风控。

- **自动化触发任务支持各账号独立指定模型**：
  - 自动化触发任务配置弹窗全面升级，支持为号池中不同账号独立指定触发测试模型，避免因账号权限差异触发 404；
  - 支持账号标签栏便捷切换、基于探测结果一键填充及配置同步。

- **单账号物理隔离连接池与出站项目 ID 动态清洗**：
  - 实现按账号独立的 HTTP 连接池，彻底消除多账号交替复用连接引发的跨账号关联风控；
  - 请求转发时出站 project 字段严格动态替换为目标账号绑定项目 ID，阻断客户端项目信息反向污染。

### v1.7.6 更新日志

- **多宿主编辑器（Cursor / Windsurf / Trae / VS Code）环境深度兼容与补丁加固（重点）**：
  - 修复 IDE 启动时 `http.noProxy` 被写入非数组类型导致主进程在 `openInBrowserWindow` 阶段抛出 `TypeError: (noProxy || []).map is not a function` 而无法创建窗口的致命缺陷；
  - 采用强类型字符串数组结构安全合并与回写 `http.noProxy`（`defaultNoProxyHosts`），顺带自动纠正历史坏值；
  - 增强跨平台 AppData 与用户配置目录探测能力，全面适配 Cursor、Windsurf、Trae、VS Code、VSCodium 等多编辑器环境；
  - 实现标准 `cleanJSONC` 过滤引擎，自动剥离配置文件中的单行注释（`//`）、多行注释（`/* */`）及尾随逗号，杜绝 JSON 序列化解析异常。

- **Antigravity 官方原生客户端无损透传与新版 IDE 插件流量智能分流**：
  - 增强官方原生客户端特征识别（白色桌面端、独立 IDE、CLI 等），对原生请求严格保持原始载荷与请求头 100% 无损透传，避免破坏官方通信协议与上下文完整性；
  - 对来自第三方宿主编辑器或新版扩展插件客户端，自动执行 Hub 版本 User-Agent 智能伪装与 Google 内部网关路由适配；
  - 对非原生插件客户端请求体自动执行 JSON Schema 宽松清洗（`cleanAndPrepareGeminiRequest`），剔除不支持的 Schema 字段并注入安全 toolConfig，彻底消除 `MALFORMED_FUNCTION_CALL` 错误与断连问题。

- **配置兜底与中继端口修正**：
  - 补齐号池错误重试规则字段在代理配置（`internal/proxy/config.json`）中的持久化兜底结构；
  - 修正中继配置（`internal/relay/config.json`）中 NVIDIA Worker 代理的默认监听端口。

### v1.7.5 更新日志

- **号池自定义切号与错误重试规则引擎（重点）**：
  - 新增号池自定义重试规则配置（`settings_pool_error_retry.go`），支持根据 HTTP 状态码（如 429、500 等）和错误内容关键字定义切号行为（`SWITCH_NEXT_ACCOUNT` 换号重试、`RETRY_SAME_ACCOUNT` 同号重试）与独立冷静期；
  - 代理转发重试循环深度打通，上游报错命中自定义规则时自动设置临时冷静期并平滑无感切号重试，大幅提升高并发与节点异常时的容灾成功率；
  - 前端控制台设置新增「号池错误重试配置」可视化弹窗（`PoolErrorRetryModal.vue`），支持按状态码及错误模式增删改查重试规则。

- **WorkBuddy Token 后台静默自动刷新与配额兼容**：
  - 新增 WorkBuddy 令牌后台自动刷新机制（`account_workbuddy_refresh.go`），检测 AccessToken 过期失效时利用 RefreshToken 自动静默换发新凭证，杜绝频繁手动重登；
  - 完善国内版（CodeBuddy / 国际 WorkBuddy）双模版本适配与动态配额解析，修正国内版周期重置时间与额度计算。

- **控制台压测模型统计与虚拟滚动性能优化**：
  - 控制台首页新增模型级统计卡片与虚拟列表滚动架构（`dashboardModelStats.ts`），解决海量请求记录下的 DOM 节点堆积与卡顿掉帧问题；
  - 优化压测结果状态码回退提取逻辑（显式状态码与文本错误正则解析双重保障），指标卡片新增首帧延迟与错误状态 Badge 双语高亮渲染。

- **2FA / TOTP 账号批量导出能力**：
  - 新增 TOTP IPC 处理模块（`app_totp_ipc.go`），支持 2FA 账号一键批量导出为 TXT、CSV 与 JSON 格式，具备安全的系统文件保存对话框与导出校验；
  - OTP 界面全面集成导出交互与中英文双语适配。

- **OpenCode 粘性会话与中继流式吞吐性能增强**：
  - 引入高性能 `goccy/go-json`（`fast_json.go`）重构 SSE 增量数据块序列化与解析，大幅降低高频流式传输下的 CPU 反射开销与内存分配；
  - 优化 OpenCode 会话隔离键（`opencode_session.go`），规范上游流式与非流式通道透传；
  - NVIDIA 模块针对 SSE 数据块解析与多会话取消隔离进行深度加固。

