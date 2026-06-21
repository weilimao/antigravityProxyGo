# API 接口文档：CloudCode 配额服务

## 1. 接口文档整体概览

| 序号 | 方法 | 路径 | 接口名称说明 |
| :--- | :--- | :--- | :--- |
| 1 | POST | `/v1internal:retrieveUserQuotaSummary` | 获取用户云端模型调用配额概览 |

---

## 2. 详细接口定义

### 获取用户云端模型调用配额概览

*   **接口说明**：该接口用于查询当前用户在指定项目下的模型资源使用配额情况，涵盖了不同模型组（如 Gemini 系列、Claude/GPT 系列）的周期性限制（如 5 小时内、每周）及剩余可用额度。
*   **请求方法 (Method)**: `POST`
*   **请求完整 URL 路径**: `https://daily-cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary`

#### 请求 Headers 说明

| 字段 | 说明 | 示例 |
| :--- | :--- | :--- |
| `Accept-Encoding` | 客户端支持的压缩格式 | `gzip` |
| `Authorization` | 身份验证令牌，用于标识当前登录用户 | `Bearer ya29.a0...` |
| `Content-Length` | 请求体字节长度 | `32` |
| `Content-Type` | 请求内容的媒体类型 | `application/json` |
| `User-Agent` | 客户端标识，用于追踪来源平台与版本 | `antigravity/hub/2.1.4 windows/amd64` |

#### 请求 Body 参数说明

| 字段路径 | 字段名称 | 类型 | 必填 | 详细含义说明 | 示例 |
| :--- | :--- | :--- | :--- | :--- | :--- |
| `project` | 项目标识符 | String | 是 | 关联用户的云端项目唯一 ID，用于确定查询范围 | `"nodal-tenure-x6tnh"` |

#### 响应 Body 参数说明

| 字段路径 | 字段名称 | 类型 | 详细含义说明 | 示例 |
| :--- | :--- | :--- | :--- | :--- |
| `description` | 总体描述 | String | 关于资源配额机制（如比例消耗逻辑）的全局性说明 | `"Within each group..."` |
| `groups` | 分组列表 | Array | 按照模型类别划分的资源池集合 | `[...]` |
| `groups[]` | 分组对象 | Object | 单个模型分类的资源配额信息容器 | `{...}` |
| `groups[].description` | 分组详细说明 | String | 该组包含的具体模型系列说明 | `"Models within this group..."` |
| `groups[].displayName` | 分组显示名称 | String | 用户界面展示的分组标题 | `"Gemini Models"` |
| `groups[].buckets` | 配额桶列表 | Array | 该组下不同时间窗口的资源限制维度 | `[...]` |
| `groups[].buckets[]` | 配额桶对象 | Object | 具体的时间窗资源配额状态 | `{...}` |
| `groups[].buckets[].bucketId` | 桶唯一标识 | String | 区分不同频率策略的 ID，如 `gemini-weekly` 代表 Gemini 的周限制，`5h` 代表滑动窗口限制 | `"gemini-weekly"` |
| `groups[].buckets[].displayName` | 桶显示名称 | String | 对应时间维度的友好名称，如“每周限制” | `"Weekly Limit"` |
| `groups[].buckets[].remainingFraction` | 剩余比例 | Float | 当前可用的配额比例（0.0-1.0），用于进度条渲染 | `0.9885186` |
| `groups[].buckets[].resetTime` | 重置时间 | String | 资源额度恢复/重置的 ISO8601 时间戳 | `"2026-06-28T13:55:45Z"` |
| `groups[].buckets[].window` | 时间窗口定义 | String | 配额重置的计算周期维度，枚举：`weekly`（每周）、`5h`（5小时滚动） | `"weekly"` |

#### 请求与响应示例

**请求 JSON:**
```json
{
  "project": "nodal-tenure-x6tnh"
}
```

**响应 JSON:**
```json
{
  "description": "Within each group, models share a weekly limit and a 5-hour limit. Quota is consumed proportionally to the cost of the t...",
  "groups": [
    {
      "buckets": [
        {
          "bucketId": "gemini-weekly",
          "description": "You have used some of your weekly limit, it will fully refresh in 6 days, 23 hours.",
          "displayName": "Weekly Limit",
          "remainingFraction": 0.9885186,
          "resetTime": "2026-06-28T13:55:45Z",
          "window": "weekly"
        },
        {
          "bucketId": "gemini-5h",
          "description": "You have used some of your 5-hour limit, it will fully refresh in 4 hours, 19 minutes.",
          "displayName": "Five Hour Limit",
          "remainingFraction": 0.9811117,
          "resetTime": "2026-06-21T18:55:45Z",
          "window": "5h"
        }
      ],
      "description": "Models within this group: Gemini Flash, Gemini Pro",
      "displayName": "Gemini Models"
    },
    {
      "buckets": [
        {
          "bucketId": "3p-weekly",
          "displayName": "Weekly Limit",
          "remainingFraction": 1,
          "resetTime": "2026-06-23T01:36:33Z",
          "window": "weekly"
        },
        {
          "bucketId": "3p-5h",
          "displayName": "Five Hour Limit",
          "remainingFraction": 1,
          "resetTime": "2026-06-21T19:36:01Z",
          "window": "5h"
        }
      ],
      "description": "Models within this group: Claude Opus, Claude Sonnet, GPT-OSS",
      "displayName": "Claude and GPT models"
    }
  ]
}
```