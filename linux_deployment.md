# Antigravity Proxy Linux 服务端（Headless Server）部署指南

本文档介绍如何将 Antigravity Proxy 核心后端中继服务独立编译并部署至 Linux 服务器（支持 Ubuntu / Debian / CentOS / Alpine / Rocky Linux 等任意发行版）。

---

## 1. 架构与优势

- **纯静态单文件**：采用纯 Go（基于 `modernc.org/sqlite`），`CGO_ENABLED=0` 静态编译，文件体积仅约 13MB，**无需在 Linux 服务器上安装任何 C 依赖库或 Go 运行环境**。
- **超轻量与低内存**：彻底剥离了桌面 WebView 渲染引擎，服务器常驻物理内存仅约 **20MB ~ 30MB**。
- **全协议兼容**：原生开放 `18444` 端口，提供标准 OpenAI 格式（`/v1/chat/completions`）、Gemini 格式（`/v1internal:...`）以及统一多模型路由（`/route/...`、`/nvidia/...`）。

---

## 2. 本地交叉编译

在 Windows 本地开发机终端（PowerShell 或 CMD）中执行项目内置的构建脚本：

```cmd
scripts\build_linux.bat
```

> **提示**：如果目标 Linux 服务器是 ARM 架构（如 AWS Graviton、树莓派等），可以传入参数：
> ```cmd
> scripts\build_linux.bat arm64
> ```

编译产物将生成在：
`build/bin/antigravity-server-linux-amd64`

---

## 3. 服务器部署与数据迁移

### 3.1 上传二进制文件
通过 `scp` 或 SFTP 将可执行文件上传至服务器：

```bash
scp build/bin/antigravity-server-linux-amd64 user@your-server-ip:/opt/antigravity/antigravity-server
```

### 3.2 迁移本地已有配置与账号（推荐）
如果您在 Windows 桌面端已经配置好了各账号（Google/Gemini、NVIDIA、Grok 等）和 API Key，可以直接将本地数据目录中的配置拷贝到服务器上，实现无缝衔接：

- **Windows 本地数据目录**：
  `%APPDATA%\antigravity-proxy-desktop\`（即 `C:\Users\<用户名>\AppData\Roaming\antigravity-proxy-desktop`）
- **需要拷贝的核心文件**：
  - `config.json`（系统配置、模型映射配置）
  - `accounts_*.json`（包含 `accounts_antigravity.json`、`accounts_nvidia.json`、`accounts_pool.json` 等账号池数据）
  - `pricing.json`（模型费率配置，可选）
  - `antigravity.db`（SQLite 历史统计数据库，可选）

在 Linux 服务器上创建数据目录并上传：
```bash
mkdir -p /opt/antigravity/data
# 将上述配置文件放进 /opt/antigravity/data/
```

### 3.3 赋予执行权限并启动
```bash
cd /opt/antigravity
chmod +x antigravity-server

# 测试运行（指定端口 18444 与数据目录 /opt/antigravity/data）
./antigravity-server -p 18444 -d /opt/antigravity/data
```

启动后将输出如下 Banner：
```
=========================================================================
 Antigravity Headless Server (v1.2.0-headless)
 - Relay Port : 18444
 - Data Dir   : /opt/antigravity/data
 - API Base   : http://0.0.0.0:18444/v1
 - Health URL : http://0.0.0.0:18444/api/health
=========================================================================
```

---

## 4. 生产环境后台守护进程配置（Systemd）

为了让服务在服务器后台常驻、开机自启并具备崩溃自动重启能力，推荐使用 Systemd：

1. 创建服务描述文件：
   ```bash
   sudo nano /etc/systemd/system/antigravity.service
   ```

2. 写入以下内容：
   ```ini
   [Unit]
   Description=Antigravity Proxy Headless Server
   After=network.target

   [Service]
   Type=simple
   User=root
   WorkingDirectory=/opt/antigravity
   ExecStart=/opt/antigravity/antigravity-server -p 18444 -d /opt/antigravity/data
   Restart=always
   RestartSec=5s
   LimitNOFILE=65535

   # 环境变量配置（可选）
   # Environment="ANTIGRAVITY_PORT=18444"
   # Environment="ANTIGRAVITY_DATA_DIR=/opt/antigravity/data"

   [Install]
   WantedBy=multi-user.target
   ```

3. 重载并启动服务：
   ```bash
   sudo systemctl daemon-reload
   sudo systemctl enable antigravity
   sudo systemctl start antigravity

   # 查看运行状态
   sudo systemctl status antigravity

   # 查看实时日志
   sudo journalctl -u antigravity -f
   ```

---

## 5. Docker 容器化部署方案（可选）

如果希望通过 Docker 部署，可使用如下极简 Dockerfile：

```dockerfile
FROM alpine:latest
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY antigravity-server /app/antigravity-server
RUN chmod +x /app/antigravity-server
EXPOSE 18444
ENTRYPOINT ["/app/antigravity-server", "-p", "18444", "-d", "/data"]
```

运行命令：
```bash
docker run -d \
  --name antigravity-server \
  --restart unless-stopped \
  -p 18444:18444 \
  -v /opt/antigravity/data:/data \
  antigravity-server:latest
```

---

## 6. 验证与客户端接入

### 6.1 健康检查与接口探测
在服务器本地或外部测试连通性：
```bash
curl http://127.0.0.1:18444/api/health
# 预期返回: {"status":"ok","timestamp":...}
```

### 6.2 客户端配置（Cline / Cherry Studio / NextChat 等）
在任何第三方客户端中配置如下参数：
- **API 基础路径 (Base URL)**：`http://<服务器IP或域名>:18444/v1`
- **API Key**：在 `config.json` 或 `/api/keys` 中配置的对应中继密钥（例如 `sk-ant-...`）
- **模型名称**：直接填入您在服务端配置的模型名（如 `gemini-2.5-flash`、`claude-3-5-sonnet` 等）
