@echo off
rem --- SmartDeploy Quick Upload ---
rem Drag a file onto this bat, or run: upload.bat <file path>
rem The file will be queued and uploaded automatically after connection.

title SmartDeploy - Quick Upload
cd /d "%~dp0"

if "%~1"=="" (
    echo Usage: Drag a file onto this bat, or run: upload.bat <file path>
    pause
    exit /b 1
)

rem --- Check for config file ---
if not exist "config.json" (
    echo [ERROR] No config.json found in %CD%
    echo Please create one first (copy config.example.json to config.json and edit).
    pause
    exit /b 1
)

rem --- Build the binary if needed ---
if not exist "smartDeploy.exe" (
    echo [INFO] Building smartDeploy.exe...
    go build -o smartDeploy.exe .
    if %errorlevel% neq 0 (
        echo [ERROR] Build failed.
        pause
        exit /b 1
    )
)

echo ============================================
echo   SmartDeploy - Quick Upload
echo ============================================
echo.
echo [INFO] File to upload: %~1
echo [INFO] Starting SmartDeploy... Copy your OTP to clipboard when prompted.
echo.
smartDeploy.exe -config config.json "%~1"
echo.
echo ============================================
echo   SmartDeploy exited.
echo ============================================
pause
