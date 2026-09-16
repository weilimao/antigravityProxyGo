@echo off
setlocal EnableDelayedExpansion
title Antigravity Web Platform Launcher

set "ROOT_DIR=%~dp0"
cd /d "%ROOT_DIR%"

echo =========================================================================
echo       Antigravity Web Platform - One-Click Launcher
echo =========================================================================
echo.

REM 1. Check Go environment
where go >nul 2>nul
if %errorlevel% neq 0 goto err_go

REM 2. Check Node.js and npm environment
where node >nul 2>nul
if %errorlevel% neq 0 goto err_node

where npm >nul 2>nul
if %errorlevel% neq 0 goto err_npm

echo [1/4] Environment check passed: Go, Node.js, and npm are ready.

REM 3. Check and initialize config.yaml
if exist "%ROOT_DIR%config.yaml" goto cfg_ready
if exist "%ROOT_DIR%config.yaml.example" (
    echo [2/4] config.yaml not found. Initializing from template...
    copy "%ROOT_DIR%config.yaml.example" "%ROOT_DIR%config.yaml" >nul
    echo       config.yaml initialized successfully.
    goto cfg_done
)
:cfg_ready
echo [2/4] Configuration file config.yaml is ready.
:cfg_done

REM 4. Check frontend dependencies
if exist "%ROOT_DIR%web\node_modules\" goto skip_install
echo [3/4] First-time setup: Installing frontend dependencies...
pushd "%ROOT_DIR%web"
call npm install
if %errorlevel% neq 0 (
    popd
    goto err_npm_install
)
popd
echo       Frontend dependencies installed successfully.
:skip_install
echo [3/4] Frontend dependencies are ready.

REM 5. Check port occupation and clean up old instances
set "PORT_BUSY=0"
for /f "tokens=5" %%a in ('netstat -ano ^| findstr :8100 ^| findstr LISTENING 2^>nul') do set "PORT_BUSY=1"
for /f "tokens=5" %%a in ('netstat -ano ^| findstr :6688 ^| findstr LISTENING 2^>nul') do set "PORT_BUSY=1"

if "!PORT_BUSY!"=="0" goto skip_cleanup
echo.
echo [NOTICE] Port 8100 or 6688 is in use. Cleaning up old instances...
call "%ROOT_DIR%stop.bat" >nul 2>&1
ping 127.0.0.1 -n 2 >nul
:skip_cleanup

REM 6. Establish secure SSH Tunnel for remote database if configured
set "NEED_TUNNEL=0"
findstr /i "127.0.0.1:39306" "%ROOT_DIR%config.yaml" >nul 2>&1
if %errorlevel% equ 0 set "NEED_TUNNEL=1"

if "!NEED_TUNNEL!"=="1" (
    set "TUNNEL_READY=0"
    for /f "tokens=5" %%a in ('netstat -ano ^| findstr :39306 ^| findstr LISTENING 2^>nul') do set "TUNNEL_READY=1"
    if "!TUNNEL_READY!"=="0" (
        echo [*] Establishing secure SSH Database Tunnel to 192.255.160.69:39306 in background...
        powershell -Command "Start-Process ssh -ArgumentList '-N -o ExitOnForwardFailure=yes -o StrictHostKeyChecking=no -L 39306:127.0.0.1:39306 root@192.255.160.69' -WindowStyle Hidden"
        ping 127.0.0.1 -n 3 >nul
        echo       SSH Database Tunnel connected successfully.
    ) else (
        echo [*] SSH Database Tunnel on port 39306 is already active.
    )
)

echo.
echo [4/4] Starting backend and frontend services...
echo       (Note: Syncing remote database schema takes ~30-40s on first load)
echo.

REM Start Backend Service (Port 8100)
if exist "%ROOT_DIR%web_platform_server.exe" (
    start "Antigravity-Web-Backend" cmd /k "title Antigravity-Web-Backend [Port 8100] && cd /d "%ROOT_DIR%" && .\web_platform_server.exe"
) else (
    start "Antigravity-Web-Backend" cmd /k "title Antigravity-Web-Backend [Port 8100] && cd /d "%ROOT_DIR%" && echo [*] Starting Go Backend Server on port 8100... && go run cmd/server/main.go"
)

REM Start Frontend Service (Port 6688)
start "Antigravity-Web-Frontend" cmd /k "title Antigravity-Web-Frontend [Port 6688] && cd /d "%ROOT_DIR%web" && echo [*] Starting Vite Frontend Server on port 6688... && call npm run dev"

REM Wait 3 seconds and launch browser
ping 127.0.0.1 -n 3 >nul
start http://localhost:6688

:MENU
cls
echo =========================================================================
echo       Antigravity Web Platform - Service Dashboard
echo =========================================================================
echo.
echo  Access URLs:
echo  - Web Application : http://localhost:6688
echo  - Backend API     : http://localhost:8100
echo  - Health Check    : http://localhost:8100/api/health
echo.
echo  Default Superadmin Credentials:
echo  - Username : admin
echo  - Password : admin123
echo.
echo =========================================================================
echo  Actions (Press key directly):
echo   [1] Open Web Platform in Browser
echo   [2] Stop all services and exit
echo   [3] Keep running in background and close this dashboard
echo =========================================================================
echo.

choice /c 123 /n /m "Please press 1, 2, or 3: "
if errorlevel 3 goto action_exit
if errorlevel 2 goto action_stop
if errorlevel 1 goto action_open
goto MENU

:action_open
start http://localhost:6688
goto MENU

:action_stop
echo.
echo Stopping all services...
call "%ROOT_DIR%stop.bat"
echo Done. Dashboard will close now.
ping 127.0.0.1 -n 2 >nul
exit /b 0

:action_exit
echo.
echo Services are running in background.
echo You can double-click stop.bat anytime to shut them down.
ping 127.0.0.1 -n 2 >nul
exit /b 0

:err_go
echo [ERROR] Go environment not found!
echo Please install Go 1.22+ and configure PATH.
echo.
pause
exit /b 1

:err_node
echo [ERROR] Node.js environment not found!
echo Please install Node.js (v18+ recommended).
echo.
pause
exit /b 1

:err_npm
echo [ERROR] npm command not found! Please check Node.js installation.
echo.
pause
exit /b 1

:err_npm_install
echo [ERROR] Frontend npm install failed! Please check your network.
echo.
pause
exit /b 1
