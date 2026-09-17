@echo off
chcp 65001 >nul
title MAX API 商业化管理平台标准打包
echo ==========================================================
echo  MAX API 商业化管理平台生产环境标准发布打包
echo ==========================================================
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0build_release.ps1"
pause
