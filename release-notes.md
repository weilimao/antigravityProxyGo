### v1.2.1 更新日志

- **NVIDIA 上游流内 SSE Error 嗅探与无感换号重试**：增加对物理 HTTP 200 流中包含的 `{"error": ...}`（如 `ResourceExhausted` 或 `500 Internal server error`）首帧 Error 嗅探。遇到首帧 Error 时自动冷冻该报错账号，并自动从号池挑选下一个可用 NVIDIA 账号进行物理重试，彻底解决超长上下文或节点繁忙时 Agent 静默画横线终止与 `/compact` 报错 `summarization produced empty response` 的问题。
- **Debugger 调试模式与全量日志落盘**：支持在前端设置中一键开启全量请求 Headers、Body 及 SSE 帧的毫秒级落盘日志，方便精准排查断流与异常；支持自定义日志保存路径，默认状态调整为关闭（false）。

### v1.2.0 更新日志

- **新增 NVIDIA NIM / API 原生中继与账号管理**：全量支持 NVIDIA API 请求结构翻译、响应流式转换、配额探针（quota probe）与调用计数统计；前端新增 NVIDIA 账号添加与集中管理弹窗 (`NvidiaAccountModal.vue`)。
- **前端模态框基础组件化重构 (BaseModal)**：新建 `BaseModal.vue` 组件，统一全站 13+ 个 Modal 弹窗逻辑与样式架构，大幅提升界面一致性与交互流畅度。
- **账号管理与配额面板深度优化**：重构 `accountsController`、`accountsRenderer` 及 `usageDetails`，增强配额视图拆分、双因素认证 (OTP) 流程处理及用量验证脚本。
- **扩展 IPC 与诊断日志服务**：优化 Wails IPC 控制层，集成 `internal/corelog` 日志系统与 `internal/diagserver` 诊断服务端点，提升系统可观测性。
- **单元测试与构建全量通过**：补充 NVIDIA 翻译器、响应转换及计数器单元测试，所有 Go 单元测试与 Vue TypeScript 生产打包均全绿通过。
