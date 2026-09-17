<#
.SYNOPSIS
  MAX API 商业化管理平台（Web Platform）一键跨平台打包脚本
.DESCRIPTION
  自动执行前端生产构建、标准 Linux tar.gz 规范打包、以及 Go 后端 Linux-AMD64 交叉编译。
  使用 Windows 原生 tar 命令，严格遵循 POSIX 路径分隔符规范，彻底杜绝反斜杠解压问题。
#>

$ErrorActionPreference = "Stop"
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path

Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host " 🚀 正在开始 MAX API Web 平台生产环境标准发布打包..." -ForegroundColor Cyan
Write-Host "==========================================================" -ForegroundColor Cyan

# 1. 前端构建
Write-Host "`n[1/3] 📦 正在编译前端 Vue 3 生产静态资源..." -ForegroundColor Yellow
Set-Location -Path "$ScriptDir\web"
cmd.exe /c "npm run build"
if ($LASTEXITCODE -ne 0) {
    Write-Host "❌ 前端构建失败，请检查前端代码与依赖！" -ForegroundColor Red
    exit 1
}

# 2. 前端规范打包为 tar.gz (遵循 POSIX 正斜杠规范)
Write-Host "`n[2/3] 🗜️ 正在使用标准 POSIX 规范打包前端静态资源 (dist.tar.gz)..." -ForegroundColor Yellow
Set-Location -Path $ScriptDir
if (Test-Path "$ScriptDir\dist.tar.gz") {
    Remove-Item "$ScriptDir\dist.tar.gz" -Force
}
tar.exe -czf "$ScriptDir\dist.tar.gz" -C "$ScriptDir\web\dist" .
Write-Host "✅ 前端包已生成: $ScriptDir\dist.tar.gz" -ForegroundColor Green

# 3. 后端交叉编译为 Linux AMD64
Write-Host "`n[3/3] ⚙️ 正在交叉编译 Go 后端 Linux 二进制 (antigravity-web-platform)..." -ForegroundColor Yellow
$env:GOOS = "linux"
$env:GOARCH = "amd64"
go build -ldflags="-s -w" -o "$ScriptDir\antigravity-web-platform" "$ScriptDir\cmd\server\main.go"
if ($LASTEXITCODE -ne 0) {
    Write-Host "❌ 后端编译失败！" -ForegroundColor Red
    exit 1
}
Write-Host "✅ Linux 后端可执行文件已就绪: $ScriptDir\antigravity-web-platform" -ForegroundColor Green

Write-Host "`n==========================================================" -ForegroundColor Cyan
Write-Host " 🎉 打包全部完成！已生成以下标准生产部署文件：" -ForegroundColor Green
Write-Host "  1. dist.tar.gz                (前端静态资源标准包)" -ForegroundColor White
Write-Host "  2. antigravity-web-platform   (Linux 生产可执行程序)" -ForegroundColor White
Write-Host "`n💡 服务器部署只需执行以下标准两步：" -ForegroundColor Yellow
Write-Host "  scp antigravity-web-platform dist.tar.gz root@192.255.160.69:/opt/antigravity-web-platform/" -ForegroundColor Gray
Write-Host "  ssh root@192.255.160.69 'tar -xzf /opt/antigravity-web-platform/dist.tar.gz -C /opt/antigravity-web-platform/dist && systemctl restart antigravity-web-platform'" -ForegroundColor Gray
Write-Host "==========================================================" -ForegroundColor Cyan
