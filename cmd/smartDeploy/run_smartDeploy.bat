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

rem --- Always rebuild to pick up code changes ---
echo [INFO] Building smartDeploy.exe...
go build -o smartDeploy.exe .
if %errorlevel% neq 0 (
    echo [ERROR] Build failed.
    pause
    exit /b 1
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
