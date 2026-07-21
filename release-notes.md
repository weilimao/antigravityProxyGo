### v1.1.19 更新日志

- **修复发送至 Google 上游的 HTTP 400 INVALID_ARGUMENT 报错**：重写请求 Payload 时，当 `thinkingBudget` 设置为 `-1`（自适应/动态预算）或 `0`（强制关闭）时，代理只拼入相应的 `includeThoughts` 逻辑，不再向发送给谷歌上游的 JSON 包体中拼入 `-1` 等非规范数值，彻底规避谷歌 CloudCode REST/gRPC 网关的参数校验报错。
- **新增 4 大深度思考与 Token 限制控制项**：在代理客户端“设置”卡片及全局模型覆写中完整支持 `supportsThinking`（声明模型具备思维链能力）、`thinkingBudget`（思考预算策略：-1 自适应/0 关闭/>0 固定上限）、`minThinkingBudget`（最小思考 Token 限制，如 32）、`maxOutputTokens`（最大单次输出 Token 上限，如 65536）。同步自动重写与注入 Payload 中的 `generationConfig` 属性。
- **新增自动化测试与全量构建验证**：补全针对思考配置改写、`-1` 自适应字段省略及 Token 上限注入的单元测试，后端 Go 与前端 Vue 打包编译无报错全绿通过。
