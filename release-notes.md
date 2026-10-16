### v1.7.3 更新日志

- **全局模型强制覆写请求体改写（Payload Rewrite）**：
  - 增强代理转发路由核心（`RewriteModelInPayload`）：当用户启用“全局模型覆写”时，自动扫描并同步改写 JSON 请求体中的 `model` 字段（包括顶层标准格式与嵌套 `request.model` 包装格式）；
  - 保留并对齐 `models/` 前缀形态，确保发送至上游的模型标识与请求路径完全一致，杜绝上游因请求体内部模型不匹配引发的调用异常；
  - 补充完备的单元测试，涵盖根层级、嵌套层级、形态一致性、空载荷与非 JSON 载荷边界防御。

- **上游多端点配额融合与 5 小时滚动重置时间提取**：
  - 深度适配上游 `retrieveUserQuotaSummary` 新版本仅返回 `Claude and GPT models` 的最新特征；
  - 自动协同嗅探 `retrieveUserQuota` 端点，提取真实 Gemini 系列模型实时剩余百分比与 5 小时动态重置时间（`ResetTime`）；
  - 优化配额展示结构（`MergeQuotaBuckets`），实现 Gemini Models 优先置顶与 Claude Models 结构化分层展示；
  - 完善通用渠道与 gemini-cli 分组归类逻辑，提供高可用降级容错。

- **WorkBuddy 客户端版本自适应探测与双端路由前缀隔离**：
  - 支持 WorkBuddy 客户端模拟版本自动探测与管理，可读取 Windows 注册表与安装清单，默认回退稳定基线 `5.7.7`，彻底解决旧版本授权被上游拦截的问题；
  - 重构模型拉取逻辑，国内与国际双端模型统一采用 `workbuddy/cn/*` 与 `workbuddy/intl/*` 细化前缀隔离，杜绝同名模型混淆与路由冲突；
  - 暴露对应 IPC 通道并在前端支持版本一键探测与参数持久化落盘。


