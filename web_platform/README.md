# Antigravity Web Platform —— 企业级大模型算力运营与订阅分发系统

> **Antigravity Web Platform** 是一套开箱即用、现代化的独立全栈 Web 运营平台，专为独立开发者与跨境 AI 业务打造。
> 具备用户自主注册登录、商业化套餐订阅、**极客工坊（GeekTools）收银切单支付**、**模型白名单精确授权**、**桌面端同款模型路由映射**、**Auto 并发竞速**、**全局 OCR 图像自愈降级** 与 **用户 API Key 凭证发放** 等全套能力。

---

## 🌟 核心功能特性

1. 🔐 **独立用户鉴权与角色隔离**：
   - 完备的自主注册、登录、修改密码（Bcrypt 高强度哈希），采用 JWT 无状态鉴权。
   - 严格的 RBAC 权限控制（普通用户 `user` 与超管 `admin` 路由守卫拦截）。
2. 📦 **商业化套餐与订阅管理 (Plan & Subscription)**：
   - 管理员后台自由配置套餐售价（分）、周期（30天/90天/365天/永久有效）、RPM 频次限制。
   - **模型白名单精确授权 (Allowed Models)**：管理员可视化多选勾选套餐所包含的模型（如 `claude-3-7-sonnet`、`gemini-2.5-pro`、`deepseek-chat`、`auto`），严格保障不同价位套餐的模型权益隔离。
   - 分渠道 Quota 额度配置：支持单独设定 Gemini / Claude / NVIDIA / Grok 滑动窗口配额。
3. 💰 **极客工坊（GeekTools）收银切单支付 (对标 ProxySubForClash)**：
   - 采用标准 A 站（Web 平台）与 B 站（极客工坊收银台）收银切单跳转机制。
   - 基于 **HMAC-SHA256 双向通信验签**，自动向极客工坊发起切单并返回高保真收银台地址（`/#/checkout/{order_no}`）。
   - 用户支付成功后，极客工坊 Webhook 异步回调 Web 平台端点（`/api/v1/pay/notify/relay`），自动完成验签、订单状态扭转（`paid`）与套餐秒级顺延激活。
   - 管理后台支持全量订单流水查看与异常漏单**一键手动核销补单**。
4. 🚀 **桌面端高阶核心能力 100% 完整移植**：
   - **模型映射配置 (Model Mapping)**：配置入站模型到上游真实模型及号池（Google / Claude / NVIDIA / Grok / Other / Auto）的分流规则，支持思考等级参数注入与原生多模态三态控制。
   - **Auto 并发竞速模型 (Auto Racing)**：配置虚拟 `auto` 模型并发候选池，支持动态融合控制台测速池，体验首字（TTFT）抢占与败方纳秒级 Cancel。用户亦可在控制台自定义个人专属候选池。
   - **OCR 图像自愈降级 (OCR Engine)**：后台可指定全局生效的 OCR 图像模型（默认 `gemini-2.5-flash`），非多模态上游遇图自动自愈提取描述，杜绝 400 错误。
5. 🔑 **API Key 凭据分发与客户端一键直连**：
   - 用户可自主生成多个 `sk-ant-...` 密钥，密钥自动继承其当前激活套餐的模型白名单。
   - 提供针对 **OpenCode / Claude Code / NextChat / Cursor / Cherry Studio** 的直连指南与 Base URL。

---

## 🚀 极速上手与本地运行

系统采用零外部依赖设计（默认内置纯 Go SQLite 驱动，无 CGO 依赖，开箱即用）。

### ⚡ 方式一：一键启动 (推荐)

- **Windows 用户**：直接双击或终端运行 `web_platform/start.bat`
  - 自动检测 Go & Node 环境并提示
  - 首次启动自动初始化 `config.yaml` 配置文件并安装前端依赖
  - 自动拉起后端（8100）与前端 Vite（6688）独立日志控制台，并自动打开浏览器访问
  - 如需停止所有服务，可在管理面板按 `2` 或双击 `web_platform/stop.bat`
- **Linux / macOS / WSL 用户**：运行 `./start.sh`，按 `Ctrl+C` 即可自动退出并清理所有进程

---

### 🛠️ 方式二：手动分步启动

#### 1. 运行后端服务 (默认端口 8100)

```bash
cd web_platform
# 启动后端服务 (首次启动自动初始化 SQLite 数据库与默认种子数据)
go run cmd/server/main.go
```

启动成功后终端将输出 Banner：
```
=========================================================================
 Antigravity Web Platform (v1.0.0)
 - Web Host : 0.0.0.0:8100
 - Database : sqlite (data/antigravity_web.db)
 - Payment  : GeekTools Relay (http://127.0.0.1:8000/api/v1/relay/create)
 - Gateway  : Go Relay Server (http://127.0.0.1:18444)
 - Health   : http://0.0.0.0:8100/api/health
=========================================================================
```

#### 2. 运行前端工程 (默认端口 6688)

```bash
cd web_platform/web
npm install
npm run dev
```

在浏览器中直接打开：`http://localhost:6688`

---

## 🔑 本地默认测试账号

系统首次启动会自动初始化默认种子用户：

| 角色 | 用户名 | 密码 | 权限与用途 |
| :--- | :--- | :--- | :--- |
| **超级管理员** | `admin` | `admin123` | 具备套餐创建、订单管理、模型映射、OCR 配置与用户管理全部权限 |
| **普通用户** | 用户可在前台点击【新用户注册】自由创建普通账号 | - | 用于体验套餐购买、收银跳转、API Key 生成与个人 Auto 竞速配置 |

---

## 📄 自动化测试套件

本工程具备严格遵循工程规范的全套自动化测试用例，包含沙箱隔离与测试数据自动清理（Teardown）：

```bash
cd web_platform
go test -v ./...
```

测试覆盖内容：
- `TestRelayHMACSign`: 极客工坊切单 HMAC-SHA256 签名生成与双向验签。
- `TestAuthAndUserLifecycle`: 用户注册、Bcrypt 密码校验、登录与 JWT 签发。
- `TestPlanAndModelAuthorization`: 套餐创建、`AllowedModels` 权限下发与 API Key 联动。
- `TestRelayWebhookFulfillment`: 极客工坊切单支付 Webhook 异步回调、订单状态扭转与套餐自动激活。
- `TestHTTPAPIRoutesAndRBAC`: 全接口路由与 Admin RBAC 权限拦截。
