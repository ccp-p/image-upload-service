@echo off
REM filepath: d:\project\my_go_project\image-upload-service\cmd\hashCdn\run_hash_cdn.bat
@chcp 65001 >nul
echo ========================================
echo    HTML 替换图片 CDN 工具
echo ========================================
echo.

REM 检查环境变量
if "%IS_HOME%"=="" (
    echo [警告] 未设置环境变量 IS_HOME
    echo [提示] 默认使用公司电脑路径
    echo [提示] 设置方法: set IS_HOME=1 ^(家里^) 或 set IS_HOME=0 ^(公司^)
    echo.
    cd /d D:\project\my_go_project\image-upload-service\cmd\checkFile
) else (
    if "%IS_HOME%"=="1" (
        echo [信息] 当前环境: 家里电脑 ^(IS_HOME=1^)
        cd /d D:\self_project\go_project\image-upload-service\cmd\checkFile
    ) else (
        echo [信息] 当前环境: 公司电脑 ^(IS_HOME=0^)
        cd /d D:\project\my_go_project\image-upload-service\cmd\checkFile
    )
    echo.
)



REM 运行程序
go run main.go 

if %ERRORLEVEL% NEQ 0 (
    echo.
    echo [错误] 程序执行失败，错误代码: %ERRORLEVEL%
)

echo.
echo ========================================
echo    处理完成
echo ========================================
pause