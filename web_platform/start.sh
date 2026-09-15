#!/usr/bin/env bash

# Antigravity Web Platform 一键启动脚本 (Linux/macOS/WSL)
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

echo "========================================================================="
echo "      Antigravity Web Platform 运营系统 - 一键启动 (Bash)"
echo "========================================================================="

# 1. 检查 Go
if ! command -v go &> /dev/null; then
    echo "❌ 未检测到 Go 环境，请先安装 Go 1.22+ !"
    exit 1
fi

# 2. 检查 Node / npm
if ! command -v node &> /dev/null || ! command -v npm &> /dev/null; then
    echo "❌ 未检测到 Node.js / npm 环境，请先安装 Node.js (推荐 v18+) !"
    exit 1
fi

# 3. 检查配置
if [ ! -f "config.yaml" ] && [ -f "config.yaml.example" ]; then
    echo "ℹ️ 未发现 config.yaml，正在根据模板创建..."
    cp config.yaml.example config.yaml
fi

# 4. 检查前端依赖
if [ ! -d "web/node_modules" ]; then
    echo "ℹ️ 首次启动，正在安装前端依赖 (npm install)..."
    (cd web && npm install)
fi

echo "🚀 正在启动前后端服务..."

# 启动后端
go run cmd/server/main.go &
BACKEND_PID=$!

# 启动前端
(cd web && npm run dev) &
FRONTEND_PID=$!

cleanup() {
    echo ""
    echo "🛑 收到退出信号，正在关闭前后端服务..."
    kill "$BACKEND_PID" 2>/dev/null || true
    kill "$FRONTEND_PID" 2>/dev/null || true
    wait "$BACKEND_PID" 2>/dev/null || true
    wait "$FRONTEND_PID" 2>/dev/null || true
    echo "✅ 服务已全部停止。"
    exit 0
}

trap cleanup SIGINT SIGTERM EXIT

echo ""
echo "========================================================================="
echo " Antigravity Web Platform 已成功在后台启动！"
echo " - 前台入口: http://localhost:6688"
echo " - 后端接口: http://localhost:8100"
echo " - 默认超管: admin / admin123"
echo " 按 Ctrl+C 即可同时停止前后端服务"
echo "========================================================================="

wait
