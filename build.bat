@echo off
if not exist .wails_temp mkdir .wails_temp
set TEMP=%CD%\.wails_temp
set TMP=%CD%\.wails_temp

:: Detect NSIS installation path and append it to local PATH
if exist "C:\Program Files (x86)\NSIS" (
    set "PATH=%PATH%;C:\Program Files (x86)\NSIS"
) else if exist "C:\Program Files\NSIS" (
    set "PATH=%PATH%;C:\Program Files\NSIS"
)

wails build %*
