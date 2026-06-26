@echo off
if not exist .wails_temp mkdir .wails_temp
set TEMP=%CD%\.wails_temp
set TMP=%CD%\.wails_temp
wails dev
