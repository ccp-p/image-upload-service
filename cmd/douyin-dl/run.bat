@echo off
chcp 65001 >nul
cd /d "%~dp0"

if "%~1"=="" (
    set /p url="请输入抖音视频链接: "
    douyin-dl.exe -c cookies.txt "%url%"
) else (
    douyin-dl.exe -c cookies.txt %*
)

echo.
pause
