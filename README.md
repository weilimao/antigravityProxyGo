# Antigravity Proxy (Wails Desktop)

Antigravity Proxy 是一款基于 **Go 后端 (Wails v2) + Web 前端 (Vite + TypeScript)** 重构的高性能本地解密中转、多账号负载均衡与多租户 API 中继分发平台。

它既可以通过高性能 MITM 引擎拦截并优化本机对 Google Cloud APIs（如 Generative Language / Gemini API）的调用，也可以作为一个独立的中继服务器（Relay Server）向外提供支持多租户限额、套餐包计费以及模型映射的二次分发服务。

---

## 1. 核心功能特性

* **高性能 MITM 解密引擎**：基于 Go 原生网络库实现的高并发 HTTPS 拦截与解密，支持系统代理的一键热插拔与自动环境清理，运行轻量稳定。
* **多账号智能负载均衡**：支持账号池统一管理、动态配额自动刷新。具备请求重试与防限流避让机制，并提供智能会话路由器（Session Router）维护请求状态与账号的一对一绑定。
* **多租户 API 中继服务 (Relay Server)**：
  - **多用户管理**：支持添加、开关、备注子用户，并为每个用户单独配置 Gemini/Claude 等模型系列的调用权限与过期时间。
  - **分级配额管理**：支持针对子用户配置每小时、每日或固定的 Token 配额额度限制。
  - **套餐包模板 (Relay Packages)**：支持设定额度套餐模板，方便快速向子用户复用和一键绑定。
  - **网络防 SSRF 安全规则**：内置防 SSRF 网络探测、指定端口屏蔽以及自定义域名白名单过滤，确保公网中继部署的安全。
  - **模型别名映射 (Model Mapping)**：支持定义别名模型并映射至底层真实的大模型 API。
* **自动化触发器 (AutoTrigger)**：
  - 允许针对指定账号和模型列表配置自定义 Prompt 的测试任务包。
  - **配额恢复联动调度**：支持定时任务轮询，并与配额系统智能联动。当检测到账号的配额限制被解除（Quota Restored）时，自动触发并恢复关联的自动化测试任务。
* **2FA/TOTP 安全验证器**：
  - 内置 Base32 两步验证密钥解码与验证码计算引擎。
  - 自动计算并显示多账号的 2FA 实时验证码，并在前端动态展示剩余有效时间，便于配合自动化登录或人工校验。
* **Token 与成本统计**：实时解析输入/输出/缓存命中 Token 数量，自动根据模型价格换算 API 调用成本，并提供 24小时至 30天的多维度数据图表分析。
* **数据存储自主迁移**：可在设置中一键修改所有数据（凭证、计费、历史日志、自动触发任务和 CA 证书）的存储路径，系统将自动进行无缝搬迁。
* **一站式 Google Cloud OAuth 授权**：支持在应用内直接拉起谷歌登录并安全获取 Refresh Token，简化配置流程。
* **前台精细抓包日志**：支持过滤搜索请求头、请求体、状态码等关键通信帧。

---

## 2. 网络端口说明

本地运行后，系统将监听或占用以下端口：
* **`18443`** (`127.0.0.1:18443`)：**本地代理拦截端口 (MITM)**。系统开启拦截后，会自动将本机对 `generativelanguage.googleapis.com` 的流量通过该端口解密中转。
* **`18444`**（默认，可在设置中修改）：**中继分发服务端口 (Relay Server)**。开启后，其他客户端或服务器可通过该端口访问中继分发服务。
* **`38121`**（或动态端口）：**本地 OAuth 回调监听端口**。用于本地进行 Google 授权登录时的浏览器安全回调响应。

---

## 3. 极致性能与低内存调优

为了将桌面客户端的常驻运行内存控制在最低水平，项目在底层进行了多项深度优化：
1. **单实例保护**：引入进程单实例锁机制（`singleinstance.TryLock`），若检测到程序已运行，自动激活已有窗口并退出新进程，杜绝重复启动和端口冲突。
2. **WebView2 启动参数深度调优**：
   - 强制限制 V8 引擎堆空间最大为 128MB (`--js-flags="--max-old-space-size=128"`)。
   - 禁用无用的音视频沙盒服务 (`--disable-features=AudioServiceSandbox,VideoCaptureService`)。
   - 停用硬件 GPU 渲染加速、静音以及禁用崩溃日志收集与着色器磁盘缓存。
   - 极大地降低了桌面 UI 引擎带来的内存开销，使客户端常驻内存仅需约几十兆。

---

## 4. 本地开发与编译

### 4.1 环境要求
在开始开发或编译前，请确保您的系统已安装：
1. **Go SDK** (建议 v1.20 或更高版本)
2. **Node.js** (建议 v16 或更高版本)
3. **Wails CLI** (执行安装：`go install github.com/wailsapp/wails/v2/cmd/wails@latest`)

### 4.2 运行开发模式（支持热更新）
在项目根目录下，执行以下命令进入开发模式：
```bash
wails dev
```
该命令会自动启动 Vite 前端开发服务器并绑定 Go 的 IPC 接口。前端代码变动会实时热重载，Go 代码修改后会自动重编译并重启客户端。

### 4.3 编译生产版本二进制
执行以下命令，编译生成无调试控制台、打包压缩前端资源的单文件可执行二进制文件：
```bash
wails build
```
编译产物将输出至：
`build/bin/antigravity-proxy-desktop-go.exe` (Windows 环境下)

### 4.4 编译与打包 NSIS 安装程序（Windows）
如果希望将软件打包成标准的 Windows 安装程序（包含安装向导和桌面快捷方式），可按以下步骤进行：

1. **环境准备**：本地必须安装 NSIS 编译器，且将 `makensis` 添加至系统环境变量 `Path` 中。
   - **快捷安装方式（推荐）**：在 PowerShell 中执行 `winget install NSIS.NSIS` 自动下载安装。
   - *注意：安装完成后，请重启您的终端或开发工具 (IDE) 以刷新并加载最新的系统环境变量。*
2. **执行打包命令**：
   ```bash
   wails build -nsis
   ```
3. **打包产物**：
   安装程序将生成并输出至：
   `build/bin/antigravity-proxy-amd64-installer.exe`

### 4.5 Windows 开发者特别说明（中文用户名兼容）
如果您的 Windows 系统用户名包含中文、空格或非 ASCII 字符（例如 `C:\Users\张三`），直接执行 `wails dev` 或 `wails build` 可能会因为 Wails 官方 CLI 在写入临时文件时的编码缺陷，抛出如下致命错误：
* `Error: open C:\Users\...\Temp\wails.json: The system cannot find the file specified`
* `FATAL remove ...res.syso: The system cannot find the file specified`

**解决方案**：
本项目已内置了解决该 Bug 的局部重定向脚本。请使用以下脚本命令代替原生 Wails 命令进行开发与构建：
* **本地开发调试**：执行根目录下的 `dev.bat` 代替 `wails dev`。
* **生产编译与打包**：执行根目录下的 `build.bat` 代替 `wails build`（支持传入参数，如 `build.bat -nsis`）。

*注意：这些脚本会自动在项目根目录下生成一个 `.wails_temp` 临时目录作为构建缓存，该目录已被列入 `.gitignore`，不会被 Git 提交。*

---

## 5. 项目工程规范说明

本项目经过精心模块化设计，遵循以下软件工程设计准则：
* **强类型安全**：前端业务逻辑 100% 采用 **TypeScript (ESM)** 实现，杜绝隐式类型报错。
* **高内聚低耦合**：核心业务模块在后端严格按包拆分（如 `internal/relay`、`internal/autotrigger`、`internal/totp` 等）。前端复杂控制器也按业务边界重构拆分为独立的 Controller、Renderer 和 Modal 模块，单文件行数严格控制在 **600 行以内**。
* **无侵入补丁**：Go 后端在运行中会自动更新系统网络代理配置（将 HTTP/HTTPS Proxy 指向 `127.0.0.1:18443`），并在软件正常关闭时自动清除代理还原系统，确保本机网络环境不被污染。
