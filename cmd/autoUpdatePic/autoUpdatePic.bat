@echo off
chcp 65001 > nul

REM 检查环境变量
if "%IS_HOME%"=="" (
    echo [信息] 未设置 IS_HOME 环境变量
    echo [信息] 请设置 IS_HOME=1 表示家里电脑，IS_HOME=0 表示公司电脑
    echo [信息] 请在环境变量中设置 IS_HOME
    pause
    exit /b 1
)

REM 设置基础路径
if "%IS_HOME%"=="1" (
    echo [信息] 当前环境: 家里电脑 (IS_HOME=1)
    cd /d D:\project\my_go_project\image-upload-service\cmd\autoUpdatePic
) else (
    if "%IS_HOME%"=="0" (
        echo [信息] 当前环境: 公司电脑 (IS_HOME=0)
        cd /d D:\project\my_go_project\image-upload-service\cmd\autoUpdatePic
    ) else (
        echo [信息] 当前环境: 未知环境
        cd /d D:\project\my_go_project\image-upload-service\cmd\autoUpdatePic
    )
    echo.
)

echo [运行] 正在启动图片自动化更新工具...
go run main.go

echo.
echo [退出] 处理结束，请按任意键退出。
pause