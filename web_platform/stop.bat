@echo off
setlocal EnableDelayedExpansion
title Antigravity Web Platform - Service Stopper

echo =========================================================================
echo       Antigravity Web Platform - Service Stopper
echo =========================================================================
echo.

set "KILLED=0"

REM 1. Terminate Backend process on port 8100
for /f "tokens=5" %%a in ('netstat -ano ^| findstr :8100 ^| findstr LISTENING 2^>nul') do (
    echo [*] Stopping Backend process on port 8100 [PID: %%a]...
    taskkill /f /t /pid %%a >nul 2>&1
    set /a KILLED+=1
)

REM 2. Terminate Frontend process on port 6688
for /f "tokens=5" %%a in ('netstat -ano ^| findstr :6688 ^| findstr LISTENING 2^>nul') do (
    echo [*] Stopping Frontend process on port 6688 [PID: %%a]...
    taskkill /f /t /pid %%a >nul 2>&1
    set /a KILLED+=1
)

REM 3. Close titled windows if any
taskkill /f /fi "WINDOWTITLE eq Antigravity-Web-Backend*" >nul 2>&1
taskkill /f /fi "WINDOWTITLE eq Antigravity-Web-Frontend*" >nul 2>&1

echo.
if !KILLED! gtr 0 (
    echo [SUCCESS] Services stopped. Ports 8100 and 6688 have been released.
) else (
    echo [INFO] No running services found on port 8100 or 6688.
)

echo.
ping 127.0.0.1 -n 2 >nul
