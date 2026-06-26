# Antigravity Proxy (Wails Desktop)

Antigravity Proxy 是一款基于 **Go 后端 (Wails v2) + Web 前端 (Vite + TypeScript)** 重构的高性能本地解密中转与负载均衡代理软件。

它主要用于拦截并优化本机对 Google Cloud APIs（如 Generative Language / Gemini API）的调用，提供多账号负载均衡、请求重试、Token 用量统计、请求抓包分析及响应缓存等功能。

---

## 1. 核心功能特性

* **高性能 MITM 解密引擎**：基于 Go 原生网络库实现的高并发 HTTPS 拦截与解密，系统代理集成更轻量稳定。
* **多账号智能负载均衡**：支持账号池管理、动态配额查询及防限流的重试避让机制。
* **Token 与成本统计**：实时解析输入/输出/缓存命中 Token 数量，自动换算 API 调用成本，并提供 24小时内到 30天的数据图表。
* **数据存储自主迁移**：可在设置中一键修改所有数据（凭证、计费、历史日志和 CA 证书）的存储路径，系统将自动进行无缝迁移。
* **一站式 Google Cloud OAuth 授权**：支持在应用内直接弹出谷歌登录并安全获取 Refresh Token，简化配置流程。
* **前台精细抓包日志**：支持过滤搜索请求头、请求体、状态码等关键通信帧。

---

## 2. 网络端口说明

在本地运行后，系统将占用/监听以下端口：
* **`18443`** (`127.0.0.1:18443`)：**核心本地代理端口**。系统默认会将本机对 `generativelanguage.googleapis.com` 的流量通过该端口解密中转。
* **`38121`**（或动态端口）：**本地 OAuth 回调监听端口**。用于本地进行 Google 授权登录时的浏览器回调响应。

---

## 3. 本地开发与编译

### 3.1 环境要求
在开始开发或编译前，请确保您的系统已安装：
1. **Go SDK** (建议 v1.20 或更高版本)
2. **Node.js** (建议 v16 或更高版本)
3. **Wails CLI** (执行安装：`go install github.com/wailsapp/wails/v2/cmd/wails@latest`)

### 3.2 运行开发模式（支持热更新）
在项目根目录下，执行以下命令进入开发模式：
```bash
wails dev
```
该命令会自动启动 Vite 前端开发服务器，并绑定 Go 绑定接口。前端资源变动会实时热重载，Go 代码修改后会自动重编译并重启客户端。

### 3.3 编译生产版本二进制
执行以下命令，编译生成无调试控制台、打包压缩前端资源的单文件可执行二进制文件：
```bash
wails build
```
编译产物将输出至：
`build/bin/antigravity-proxy-desktop-go.exe` (Windows 环境下)

### 3.4 编译与打包 NSIS 安装程序（Windows）
如果您希望将软件打包成标准的 Windows 安装程序（带有安装向导和快捷方式，而非双击直接运行的单文件二进制），可按以下步骤进行：

1. **环境准备**：本地必须安装 NSIS 编译器，且将 `makensis` 添加至系统环境变量 `Path` 中。
   * **快捷安装方式（推荐）**：在 PowerShell 中执行 `winget install NSIS.NSIS` 自动下载安装。
   * *注意：安装完成后，请关闭并重新打开您的终端或开发工具 (IDE) 以刷新并加载最新的系统环境变量。*
2. **执行打包命令**：
   ```bash
   wails build -nsis
   ```
3. **打包产物**：
   安装程序将生成并输出至：
   `build/bin/antigravity-proxy-amd64-installer.exe`

### 3.5 Windows 开发者特别说明（中文用户名兼容）
如果您在 Windows 下的系统用户名包含中文、空格或非 ASCII 字符（例如 `C:\Users\张三`），直接执行 `wails dev` 或 `wails build` 可能会因为 Wails 官方 CLI 工具在写临时文件时的编码缺陷，抛出如下致命错误：
* `Error: open C:\Users\...\Temp\wails.json: The system cannot find the file specified`
* `FATAL remove ...res.syso: The system cannot find the file specified`

**解决方案**：
本项目已内置了解决该 Bug 的局部重定向脚本。请使用以下命令代替原生 Wails 命令进行开发与构建：
* **本地开发调试**：执行根目录下的 `dev.bat` 代替 `wails dev`。
* **生产编译与打包**：执行根目录下的 `build.bat` 代替 `wails build`（支持传入参数，如 `build.bat -nsis`）。

*注意：这些脚本会自动在项目根目录下生成一个 `.wails_temp` 临时目录作为构建缓存，且该目录已被列入 `.gitignore`，不会被提交。*

---

## 4. 使用与运行说明

1. **运行程序**：双击启动 `build/bin/antigravity-proxy-desktop-go.exe`。
2. **安装 CA 证书**（关键步骤）：
   - 在程序顶部，证书状态初始显示为 `未信任`。
   - 点击 **`安装证书`** 按钮。这会请求系统管理员权限以向系统根证书存储区导入本地动态生成的 CA 根证书（用于解密本地 HTTPS 流量）。
   - 安装完成后状态将变为 **`🔒 已信任`**。
3. **开启拦截模式**：
   - 切换顶部右侧的 **`拦截模式`** 开关为 **`ON`**。
   - 此时，系统会对 Gemini 等流量进行解密、失败重试及 Token 成本审计。
4. **管理账号池**：
   - 切换至 **`账号池`** (ACCOUNTS) 选项卡。
   - 您可以通过 **`谷歌登录授权`** 进行动态一站式授权绑定，或通过右上角直接导入包含多账号信息的 JSON 凭证包。
   - 可在列表中启用/禁用特定账号，或设置是否开启超额积分扣费。

---

## 5. 项目工程规范说明

本项目经过精心模块化改造，遵循以下工程设计准则：
* **强类型安全**：前端业务逻辑 100% 采用 **TypeScript (ESM)** 实现，杜绝隐式类型报错。
* **高内聚低耦合**：大文件如 `accountsController.js` 和 `dashboard.js` 已按业务边界被重构拆分为独立的 Controller、Renderer 和 Modal 模块，单文件行数严格控制在 **600 行以内**。
* **无侵入补丁**：Go 后端在运行中会自动更新系统网络代理配置（HTTP/HTTPS Proxy 指向 `127.0.0.1:18443`），并在软件安全关闭时自动清除代理还原系统，确保环境不受污染。
