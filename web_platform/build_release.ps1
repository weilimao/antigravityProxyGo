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
Write-Host " [*] Starting MAX API Web Platform release build..." -ForegroundColor Cyan
Write-Host "==========================================================" -ForegroundColor Cyan

# 1. 前端构建
Write-Host "`n[1/3] Compiling frontend Vue 3 static assets..." -ForegroundColor Yellow
Set-Location -Path "$ScriptDir\web"
cmd.exe /c "npm run build"
if ($LASTEXITCODE -ne 0) {
    Write-Host "[X] Frontend build failed!" -ForegroundColor Red
    exit 1
}

# 2. 前端规范打包为 tar.gz (遵循 POSIX 正斜杠规范)
Write-Host "`n[2/3] Packaging frontend static assets to dist.tar.gz..." -ForegroundColor Yellow
Set-Location -Path $ScriptDir
if (Test-Path "$ScriptDir\dist.tar.gz") {
    Remove-Item "$ScriptDir\dist.tar.gz" -Force
}
tar.exe -czf "$ScriptDir\dist.tar.gz" -C "$ScriptDir\web\dist" .
Write-Host "[OK] dist.tar.gz generated" -ForegroundColor Green

# 3. 后端交叉编译为 Linux AMD64
Write-Host "`n[3/3] Cross-compiling Go backend for Linux AMD64..." -ForegroundColor Yellow
$env:GOOS = "linux"
$env:GOARCH = "amd64"
go build -ldflags="-s -w" -o "$ScriptDir\antigravity-web-platform" "$ScriptDir\cmd\server\main.go"
if ($LASTEXITCODE -ne 0) {
    Write-Host "[X] Backend compile failed!" -ForegroundColor Red
    exit 1
}
Write-Host "[OK] Linux binary ready: $ScriptDir\antigravity-web-platform" -ForegroundColor Green

Write-Host "`n==========================================================" -ForegroundColor Cyan
Write-Host " [SUCCESS] Build completed! Release artifacts ready:" -ForegroundColor Green
Write-Host "  1. dist.tar.gz" -ForegroundColor White
Write-Host "  2. antigravity-web-platform" -ForegroundColor White
Write-Host "`n[Deploy] Server deployment steps:" -ForegroundColor Yellow
Write-Host "  scp antigravity-web-platform dist.tar.gz root@192.255.160.69:/opt/antigravity-web-platform/" -ForegroundColor Gray
Write-Host "  ssh root@192.255.160.69 'tar -xzf /opt/antigravity-web-platform/dist.tar.gz -C /opt/antigravity-web-platform/dist && systemctl restart antigravity-web-platform'" -ForegroundColor Gray
Write-Host "==========================================================" -ForegroundColor Cyan
