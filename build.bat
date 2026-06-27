@echo off
:: Use %~dp0 for absolute directory path resolving to avoid issues when invoked from other paths
set "PROJECT_DIR=%~dp0"
if not exist "%PROJECT_DIR%.wails_temp" mkdir "%PROJECT_DIR%.wails_temp"
set "TEMP=%PROJECT_DIR%.wails_temp"
set "TMP=%PROJECT_DIR%.wails_temp"

:: Detect NSIS installation path and append it to local PATH
if exist "C:\Program Files (x86)\NSIS" (
    set "PATH=%PATH%;C:\Program Files (x86)\NSIS"
) else if exist "C:\Program Files\NSIS" (
    set "PATH=%PATH%;C:\Program Files\NSIS"
)

:: Check if arguments are provided. If %1 is empty, default to -nsis
if "%~1"=="" (
    echo [Build] No arguments provided, defaulting to: wails build -nsis
    wails build -nsis
) else (
    echo [Build] Executing: wails build %*
    wails build %*
)
