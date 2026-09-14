@echo off
setlocal enabledelayedexpansion

echo ===================================================
echo   Antigravity Proxy - Linux Server Cross Compiler
echo ===================================================

set CGO_ENABLED=0
set GOOS=linux
set TARGET_ARCH=amd64

if not "%1"=="" (
    set TARGET_ARCH=%1
)

set OUTPUT_DIR=build\bin
if not exist "%OUTPUT_DIR%" mkdir "%OUTPUT_DIR%"

set OUTPUT_FILE=%OUTPUT_DIR%\antigravity-server-linux-%TARGET_ARCH%

echo [*] Compiling for Linux/%TARGET_ARCH% (CGO_ENABLED=0)...
set GOARCH=%TARGET_ARCH%
go build -ldflags="-s -w" -o "%OUTPUT_FILE%" ./cmd/server

if %ERRORLEVEL% equ 0 (
    echo [OK] Successfully built: %OUTPUT_FILE%
    echo.
    echo Next steps:
    echo 1. Upload %OUTPUT_FILE% to your Linux server.
    echo 2. Run: chmod +x antigravity-server-linux-%TARGET_ARCH%
    echo 3. Run: ./antigravity-server-linux-%TARGET_ARCH% -p 18444 -d /path/to/data
) else (
    echo [ERROR] Build failed with error code %ERRORLEVEL%
    exit /b %ERRORLEVEL%
)

endlocal
