@echo off
chcp 65001 >nul
cd /d "%~dp0"

if "%~1"=="" (
    set /p url="url: "
    douyin-dl.exe -c \/cookies.txt "%url%"
) else (
    douyin-dl.exe -c cookies.txt %*
)

echo.
pause
