@echo off
chcp 65001 >nul
cd /d %~dp0

if not exist cleanUnused.exe (
    echo [错误] 未找到 cleanUnused.exe
    echo        请先编译: go build -o cleanUnused.exe .
    pause
    exit /b 1
)

if "%~1"=="" (
    echo 用法: run_cleanUnused.bat ^<css文件路径^> [更多css...] [选项...]
    echo 示例:
    echo   run_cleanUnused.bat "D:\proj\res\wap\css\xdrNormal.css"
    echo   run_cleanUnused.bat --clean-css "D:\proj\res\wap\css\xdrNormal.css"
    echo   run_cleanUnused.bat --move .\_unused "D:\proj\res\wap\css\xdrNormal.css"
    echo.
    echo 预览模式（不加任何清理选项）只报告不改动。
    pause
    exit /b 1
)

cleanUnused.exe %*
echo.
pause
