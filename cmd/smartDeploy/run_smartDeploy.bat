@echo off
chcp 65001 >nul 2>&1
title SmartDeploy - SSH File Sync Tool
cd /d "%~dp0"

rem --- Check for config file ---
if not exist "config.json" (
    if exist "config.example.json" (
        echo [INFO] config.json not found. Copying from config.example.json...
        copy "config.example.json" "config.json" >nul
        echo [INFO] Please edit config.json with your server settings, then re-run.
        pause
        exit /b 1
    ) else (
        echo [ERROR] No config.json found. Please create one first.
        pause
        exit /b 1
    )
)

rem --- Kill any previous smartDeploy instance to free port 9721 ---
taskkill /F /IM smartDeploy.exe >nul 2>&1
if %errorlevel% equ 0 (
    echo [INFO] Previous SmartDeploy instance terminated.
    timeout /t 1 /nobreak >nul
)

rem --- Rebuild only when a .go file is newer than smartDeploy.exe ---
set NEED_BUILD=1
if exist "smartDeploy.exe" (
    set NEED_BUILD=0
    for /f %%i in ('powershell -NoProfile -Command "(Get-ChildItem -Filter *.go | Sort-Object LastWriteTime -Descending | Select-Object -First 1).LastWriteTime -le (Get-Item smartDeploy.exe).LastWriteTime"') do (
        if /i not "%%i"=="True" set NEED_BUILD=1
    )
)
if %NEED_BUILD% equ 1 (
    echo [INFO] Source changed - rebuilding smartDeploy.exe...
    go build -o smartDeploy.exe .
    if errorlevel 1 (
        echo [ERROR] Build failed.
        pause
        exit /b 1
    )
) else (
    echo [INFO] smartDeploy.exe is up to date - skipping build.
)

rem --- Launch ---
echo ============================================
echo   SmartDeploy - SSH File Sync Tool
echo ============================================
echo.
echo [INFO] Starting... Copy your OTP to clipboard when prompted.
echo [INFO] When prompted for OTP, copy the code then press Enter to confirm.
echo.
smartDeploy.exe -config config.json

echo.
echo ============================================
echo   SmartDeploy exited.
echo ============================================
pause
