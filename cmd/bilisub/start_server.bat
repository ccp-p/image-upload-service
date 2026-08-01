@echo off
chcp 65001 >nul 2>&1
title Bilibili Subtitle Server

:: Kill any process already using port 9876
for /f "tokens=5" %%a in ('netstat -ano ^| findstr ":9876.*LISTENING"') do (
    taskkill /F /PID %%a >nul 2>&1
)

echo ============================================
echo   Bilibili Subtitle Server
echo ============================================
echo.

:: exe is in the same dir as this bat
set "EXE_PATH=%~dp0bilisub.exe"
if not exist "%EXE_PATH%" (
    echo [error] bilisub.exe not found next to this bat
    echo please run: go build -o cmd\bilisub\bilisub.exe ./cmd/bilisub/
    pause
    exit /b 1
)

echo server: http://localhost:9876
echo output: %~dp0subtitles
echo.
echo open a bilibili video page in browser
echo press Ctrl+C to stop
echo ============================================
echo.

"%EXE_PATH%" -serve -o "%~dp0subtitles"

pause
