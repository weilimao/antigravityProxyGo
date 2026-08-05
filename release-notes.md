### v1.3.0 更新日志

- **第三方号池配置与通用 API 路由扩展 (Other Accounts Pool)**：
  - 新增对各种第三方 OpenAI / Anthropic 兼容 OpenAPI 上游（如 SiliconFlow, OpenRouter, DeepSeek 官方等）的号池化统一管理支持；
  - 支持多协议 Formats 动态解析、自设 BaseURL、自定义 Request Path 以及账号配置全量 JSON 导出与导入校验；
  - 提供第三方号池专属配置模态框（`OtherAccountModal.vue`）与后端 IPC 管理调度链。

- **跨协议双向转译与 Usage 统计闭环**：
  - 优化 Anthropic ↔ OpenAI 双向流式/非流式响应转译与回写架构；
  - 建立 `passthrough_usage` 统计上下文，精准捕获与落库 Prompt Tokens、Completion Tokens、Cached Tokens、TTFT（首字响应延迟）及 Cache Hit 状态，实现第三方中继与号池大盘数据的全面闭环。

- **NVIDIA 号池思考注入与 DeepSeek-V4-Pro 模型适配**：
  - 支持 NVIDIA 号池独占式 `template_kwargs` / `chat_template_kwargs` 思考参数（Reasoning Effort）灵活配置与请求透传；
  - 深度适配 `deepseek-v4-pro` 等最新推理模型，优化实时思考流（Reasoning Content）的流式渲染打点与空串签名守卫。

- **NVIDIA 蓄流重试与兜底出站代理增强**：
  - 实现 NVIDIA 流式断流 3 周期蓄流重试与周期间 10s 归零退避机制，大幅提升网络不稳定环境下的高可用性；
  - 引入每个重试周期及直连失败后的兜底出站代理（SOCKS5/HTTP）独立 Transport 单例复用，提供安全平滑的故障转移。

- **系统性能与 Dashboard 仪表盘修复**：
  - 修复 `totalCacheEligibleInputTokens` 属性查询及 `hitDenom` 校验逻辑，消除缓存命中率计算异常；
  - 新增 `/vc` 路由别名映射与远程 Key 模态框图标显示优化；
  - 锁定 GitHub Actions 构建工作流中的 Wails CLI 版本至 v2.12.0，保障编译一致性。
