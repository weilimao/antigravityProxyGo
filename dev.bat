@echo off
:: Use %~dp0 for absolute directory path resolving
set "PROJECT_DIR=%~dp0"
if not exist "%PROJECT_DIR%.wails_temp" mkdir "%PROJECT_DIR%.wails_temp"
set "TEMP=%PROJECT_DIR%.wails_temp"
set "TMP=%PROJECT_DIR%.wails_temp"

wails dev
