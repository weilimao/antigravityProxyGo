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

