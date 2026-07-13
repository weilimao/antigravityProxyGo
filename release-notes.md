### v1.1.16 更新日志

- **实现证书静默合并与完全无感启动**：在 Windows 平台上，代理程序直接通过 Go 语言底层的 Syscall 调用 Windows 的 `crypt32.dll` API 来读取并导出当前系统所有受信任根证书公钥，从而在无需管理员提权的情况下与代理 CA 证书动态合并为 `ca_combined.pem`，并通过 `SSL_CERT_FILE` 变量注入。这既实现了零弹窗（不产生 Windows 安全警告或 UAC 提权窗）的安全代理配置，又完美解决了普通 CMD 与 PowerShell 命令行工具下 `agy` 命令报证书未知签名（x509 unknown authority）的 SSL 校验错误。
- **恢复原有的 LocalMachine 状态检查机制**：支持 LocalMachine 和 CurrentUser 双重校验逻辑判定。代理在检测到全局证书库已存在 CA 证书时，能够实现完全静默代理开启。

### v1.1.15 更新日志

- **修复 Claude Code 工具调用 HTTP 400 校验错误**：在将 Anthropic 工具翻译为 Gemini 协议时去除了冗余的 `parametersJsonSchema` 字段，彻底规避了 Google API 报 `parameters_json_schema must not be set when parameters is set` 的参数冲突，使得 Claude Code 的 Tool Use 交互能够正常响应。
- **支持服务端中继请求日志实时展示**：在服务端桌面的“请求日志”表格中打通了中继流量的展示通路，通过新增的只追加内存的日志通道（AddRequestLogInMemoryOnly）来实时刷新中继客户端的流量记录，且有效避免了在数据库中引起数据重复。
- **优化中继日志账号展示归属**：将中继日志中模型下方的 `Account` 列由先前的中继用户哈希 ID 修正为具体负载均衡分发到的 Google 官方邮箱账号（如 `weilimao0714@gmail.com`），消除了配额扣减归属不明的误解。
