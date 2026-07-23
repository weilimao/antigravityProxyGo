### v1.1.21 更新日志

- **修复 HTTP 400 INVALID_ARGUMENT 报错与连续角色合并**：新增 `cleanContentsRoles` 自动清洗逻辑，在请求到达 Google CloudCode (`v1internal:streamGenerateContent`) 上游前合并 `contents` 数组中连续出现的相同 `role`（如连续 `user` 节点），确保上游严格保持角色交替传输。
- **Tab 补全模型思维链隔离保护**：为代码补全模型（包含 `tab` 名称的模型，如 `tab_flash_lite_preview`）增加防护隔离，自动跳过 `thinkingConfig` 与 `65536` `maxOutputTokens` 的拼入与覆写，彻底规避补全交互触发的网关 400 报错。
- **界面默认设置调整为全关**：全局模型覆写（`customModelOverrideEnabled`）、思维链覆写（`customThinkingOverrideEnabled`）及声明思维能力（`customThinkingSupports`）默认开关均调整为全关 (false/0)，规避代理初次启动或重置时的无意覆写。
- **单元测试全量绿灯通过**：新增并补充针对默认配置、`cleanContentsRoles` 角色合并以及 Tab 模型隔离防护的单元测试，确保系统高稳定性运行。

---

### v1.1.20 更新日志

- **思考签名跨包缓存机制（sigcache）**：新增 `sigcache` 模块，为跨包调用/会话思维签名（thoughtSignature）提供全局集中式缓存，彻底修复 Codex CLI 工具调用在特定上下文下的死循环问题。
- **优化思维链（Thinking Stream）与响应清洗**：完善中途重试流式 SSE 拼接及思维链标签清洗逻辑 (`json_schema_clean`)，提升中继兼容性与请求头清理能力。
- **自动化测试全量通过**：补全 `json_schema_clean_test` 与 `thought_sig_strip_test` 单元测试，确保高并发与重试场景下的稳定传输。

---

### v1.1.19 更新日志

- **修复发送至 Google 上游的 HTTP 400 INVALID_ARGUMENT 报错**：重写请求 Payload 时，当 `thinkingBudget` 设置为 `-1`（自适应/动态预算）或 `0`（强制关闭）时，代理只拼入相应的 `includeThoughts` 逻辑，不再向发送给谷歌上游的 JSON 包体中拼入 `-1` 等非规范数值，彻底规避谷歌 CloudCode REST/gRPC 网关的参数校验报错。
- **新增 4 大深度思考与 Token 限制控制项**：在代理客户端“设置”卡片及全局模型覆写中完整支持 `supportsThinking`（声明模型具备思维链能力）、`thinkingBudget`（思考预算策略：-1 自适应/0 关闭/>0 固定上限）、`minThinkingBudget`（最小思考 Token 限制，如 32）、`maxOutputTokens`（最大单次输出 Token 上限，如 65536）。同步自动重写与注入 Payload 中的 `generationConfig` 属性。
- **新增自动化测试与全量构建验证**：补全针对思考配置改写、`-1` 自适应字段省略及 Token 上限注入的单元测试，后端 Go 与前端 Vue 打包编译无报错全绿通过。
