# Antigravity Relay 中继服务 v1internal 接口文档

中继服务（默认运行在 `18444` 端口）提供了一套与 Google Cloud Code 内置云助手兼容的 `v1internal` 私有接口，支持**非流式生成**与**流式 SSE 传输**。本接口会自动在后台进行负载均衡、权限自愈以及模型映射。

---

## 1. 接口概述

| 特性 | 非流式接口 | 流式（SSE）接口 |
| :--- | :--- | :--- |
| **请求方法** | `POST` | `POST` |
| **接口路径** | `/v1internal:generateContent` | `/v1internal:streamGenerateContent` 或 `/v1internal:generateContent?alt=sse` |
| **Content-Type** | `application/json` | `application/json` |
| **流式支持** | 否 | 是（返回 `text/event-stream` 格式数据） |

---

## 2. 鉴权说明

请求时必须在 `Header` 头部携带您的中继 API Key 进行授权：

```http
Authorization: Bearer <您的中继API_KEY>
```
*示例 Key*：`sk-ant-9cd5fdbf9bf4109539272cf14ea643bb`

---

## 3. 请求体参数定义 (Request Body)

请求包体为 JSON 结构，主要字段定义如下：

```json
{
  "project": "favorable-synapse-ttvcb",
  "requestId": "chat/1688900000-123456",
  "model": "gemini-3.5-flash-extra-low",
  "request": {
    "contents": [
      {
        "role": "user",
        "parts": [
          {
            "text": "Hello, respond with success if you can read this."
          }
        ]
      }
    ],
    "generationConfig": {
      "maxOutputTokens": 1024,
      "temperature": 0.2
    }
  }
}
```

### 字段详解

| 字段名 | 类型 | 是否必填 | 说明 |
| :--- | :--- | :--- | :--- |
| **`project`** | `string` | 否 | 云助手项目 ID。若缺省，中继端会自动补充默认值作为占位符进行自愈。 |
| **`requestId`** | `string` | 否 | 唯一请求标识符。若缺省，中继端会自动生成唯一 ID 进行重试和排重。 |
| **`model`** | `string` | **是** | 请求的目标模型名，直接传入**简写名称**即可。例如：`"gemini-3.5-flash-extra-low"`, `"gemini-2.5-flash"`, `"gemini-1.5-pro"`。 |
| **`request`** | `object` | **是** | 核心的 Gemini 风格对话配置对象。详见下表。 |

### `request` 核心对象字段

| 子字段名 | 类型 | 是否必填 | 说明 |
| :--- | :--- | :--- | :--- |
| **`contents`** | `array` | **是** | 历史对话记录上下文。多轮对话中，数组元素交替出现 `"role": "user"` 与 `"role": "model"`。 |
| **`contents[].role`** | `string` | **是** | 发言角色，取值限定为 `"user"`（代表用户）或 `"model"`（代表 AI）。 |
| **`contents[].parts`** | `array` | **是** | 消息具体片段，每个元素通常为 `{"text": "消息内容"}`。 |
| **`generationConfig`** | `object` | 否 | 生成配置项，例如 `"maxOutputTokens": 1024`（最大生成 Token 数）或 `"temperature": 0.2`（随机温度）。 |

---

## 4. 响应体结构定义 (Response Body)

### 4.1 非流式响应结果 (200 OK)

接口响应直接返回谷歌原生格式：

```json
{
  "response": {
    "candidates": [
      {
        "content": {
          "role": "model",
          "parts": [
            {
              "thoughtSignature": "EpAECo0...",
              "text": "Success"
            }
          ]
        },
        "finishReason": "STOP"
      }
    ],
    "usageMetadata": {
      "promptTokenCount": 12,
      "candidatesTokenCount": 1,
      "totalTokenCount": 131,
      "thoughtsTokenCount": 118
    },
    "modelVersion": "gemini-default",
    "responseId": "orpPavT2KouPjrEPqPbj-Qc"
  },
  "traceId": "cf4d6ec41b21f508",
  "metadata": {}
}
```

### 响应字段解释

*   **`candidates[0].content.parts[0].text`**：AI 输出的实际正文回复内容。
*   **`candidates[0].content.parts[0].thoughtSignature`**：大模型思维链（Thinking）的内部签名（若模型开启了推理思考机制）。
*   **`candidates[0].finishReason`**：生成结束原因。`"STOP"` 代表自然回答结束；`"MAX_TOKENS"` 代表触发了生成上限被截断。
*   **`usageMetadata`**：Token 消耗统计数据：
    *   `promptTokenCount`：输入提示词消耗；
    *   `candidatesTokenCount`：输出正文消耗；
    *   `thoughtsTokenCount`：深度思考（Thinking）过程消耗；
    *   `totalTokenCount`：总消耗 Token 数。

---

## 5. 流式响应格式 (Server-Sent Events)

当调用 `/v1internal:streamGenerateContent` 时，接口会以 `text/event-stream` 格式持续推送事件流数据块。

### 事件流数据包格式示例：

```text
data: {"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"你好"}]}}]},"traceId":"..."}

data: {"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"！"}]}}]},"traceId":"..."}

data: {"response":{"candidates":[{"content":{"role":"model","parts":[]},"finishReason":"STOP"}]},"traceId":"..."}
```
*注：客户端在解析 SSE 数据时，只需提取出每一行 `data: ` 之后的 JSON，拼装每一片 `parts[0].text` 即可展现完整的打字机效果。*
