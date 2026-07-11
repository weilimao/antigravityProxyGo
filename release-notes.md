### v1.1.15 更新日志

- **修复 Claude Code 工具调用 HTTP 400 校验错误**：在将 Anthropic 工具翻译为 Gemini 协议时去除了冗余的 `parametersJsonSchema` 字段，彻底规避了 Google API 报 `parameters_json_schema must not be set when parameters is set` 的参数冲突，使得 Claude Code 的 Tool Use 交互能够正常响应。
- **支持服务端中继请求日志实时展示**：在服务端桌面的“请求日志”表格中打通了中继流量的展示通路，通过新增的只追加内存的日志通道（AddRequestLogInMemoryOnly）来实时刷新中继客户端的流量记录，且有效避免了在数据库中引起数据重复。
- **优化中继日志账号展示归属**：将中继日志中模型下方的 `Account` 列由先前的中继用户哈希 ID 修正为具体负载均衡分发到的 Google 官方邮箱账号（如 `weilimao0714@gmail.com`），消除了配额扣减归属不明的误解。
