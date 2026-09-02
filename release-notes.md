### v1.6.1 更新日志

- **API Key 模型授权白名单**：
  - 中继 API Key 新增 `AllowedModels` 模型授权白名单（精确匹配）：空列表 = 不限制（全部授权，兼容旧数据）；非空 = 仅允许列表中的模型名完全一致时调用。校验在模型映射改写之前拦截客户端原始请求模型名，语义清晰；
  - 授权校验覆盖全部号池一级入口（NVIDIA / Grok / OpenAI-Compat / Anthropic-Compat）及路由转发前置 `handleRoutedForward`，route 链路用客户端原始 `inModel` 校验、直连链路在各 handler 内校验，两条路径同口径不漏不重；
  - 远程 IPC 扩展：`update-quota` 通道新增 `allowedModels` 参数透传至远端 `/api/keys/update-quota`；新增 `remote:get-key-models` 通道与远端 `/api/keys/models` 接口，返回当前中继对外暴露的模型清单（映射表 `Expose==true` 的 ClientModel 去重排序），供前端编辑授权模型时作为候选下拉；
  - 前端「远程密钥」弹窗新增授权模型多选编辑入口，与候选模型下拉联动。

- **NVIDIA SSE 工具调用延迟开块修复**：
  - 修复 OpenAI→Anthropic SSE 转换中，工具名晚于参数分片到达时 `content_block_start` 未发出却已缓存 `input_json_delta`，导致客户端收到残缺 JSON 的 tool_use 块（ZCode: "Tool call ended without a terminal event"）；
  - `sseBlock` 新增 `pendingArgs` 暂存「名字未到达」期间的 arguments 分片，工具名到达补发 `content_block_start` 后一次性 flush；`toolKeyNS` 命名空间将 tool 块 map key 与 thinking/text 块隔离，杜绝漂移后命中文本块把工具参数写进文本块；
  - `nextFreeIndex` 改为以「已实际开块」的 index 占位为准分配 Anthropic index（严格单调递增不复用），延迟未开块的工具块不占位；`closeAll` 重写为按 index 升序闭合、丢弃名字从未到达的未开块工具块，避免输出无名工具块或孤立 `input_json_delta`；
  - `determineStopReason`：无名工具块被丢弃后即便上游 `finish_reason=tool_calls` 也降级为 `end_turn`，避免客户端进入「该执行工具却没有调用」的异常路径。

- **NVIDIA 流式首字节等待计时修复**：
  - `nvidia.go` 在流式分支用 `bufio.NewReader(resp.Body).Peek(1024)` 预读嗅探错误帧，Peek 把最多 1024 字节预读进 bufio 内存缓冲；下游再建的 `upstreamTimingReader` 包装的是已被缓冲的 reader，首次 Read 直接从内存返回，导致 `FirstByteWait` 恒≈0；
  - 新增 `bodyWithTiming` 把 `upstreamTimingReader` 插在 `resp.Body` 与 `bufReader` 之间，Peek(1024) 触发真实网络 Read 正确记录首字节等待，下游通过 type-assert 提取该值与下游完整 timing 组合。

- **Anthropic 透传 SSE 输入 token 统计修复**：
  - `message_start.message.usage.input_tokens` 是 Anthropic 协议中输入 token 的权威来源（`message_delta` 通常只带 output_tokens）；此前注释声明「兜底读取」但未实现，导致遵循标准协议的第三方镜像（如 api.radium.cloud）输入 token 统计恒为 0；
  - 现嗅探 `message_start` 的 input_tokens 作初值，`message_delta` 仅当字段 >0 时才覆盖（避免标准 Anthropic 的空 message_delta 把已设的 inUsage 清零）；流末仍为 0 时按入站请求体 `EnsureInputTokens` 估算兜底，与非流式分支同口径。
