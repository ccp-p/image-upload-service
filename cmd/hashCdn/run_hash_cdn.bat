@echo off
REM filepath: d:\project\my_go_project\image-upload-service\cmd\hashCdn\run_hash_cdn.bat
@chcp 65001 >nul
echo ========================================
echo    HTML Hash CDN 工具
echo ========================================
echo.

REM 检查环境变量
if "%IS_HOME%"=="" (
    echo [警告] 未设置环境变量 IS_HOME
    echo [提示] 默认使用公司电脑路径
    echo [提示] 设置方法: set IS_HOME=1 ^(家里^) 或 set IS_HOME=0 ^(公司^)
    echo.
    cd /d D:\project\my_go_project\image-upload-service\cmd\hashCdn
) else (
    if "%IS_HOME%"=="1" (
        echo [信息] 当前环境: 家里电脑 ^(IS_HOME=1^)
        cd /d D:\self_project\go_project\image-upload-service\cmd\hashCdn
    ) else (
        echo [信息] 当前环境: 公司电脑 ^(IS_HOME=0^)
        cd /d D:\project\my_go_project\image-upload-service\cmd\hashCdn
    )
    echo.
)

REM 检查配置文件是否存在
if not exist "version.config.json" (
    echo [错误] 未找到配置文件 version.config.json
    echo 请先创建配置文件！
    pause
    exit /b 1
)

echo.
echo 请选择部署模式:
REM [1] 默认的 copy
REM [2] copy-commit (自动提交svn)
echo [1] 执行前置脚本，然后 copy
echo [2] 执行前置脚本，然后 copy-commit
echo [3] 执行前置脚本，然后 copy-commit，提交git (HTML回滚后)
echo [4] 保持相对路径(不替换CDN)，然后 copy
echo [5] 保持相对路径(不替换CDN)，然后 copy-commit
echo [6] 保持相对路径(不替换CDN)，然后 copy-commit，提交git
echo.

set /p mode_input="请输入对应的数字 (默认=1): "
if "%mode_input%"=="" set mode_input=1

set "message_flag="
REM 模式 2/3/5/6 涉及自动提交，提示用户输入自定义提交信息
if "%mode_input%"=="2" goto ask_message
if "%mode_input%"=="3" goto ask_message
if "%mode_input%"=="5" goto ask_message
if "%mode_input%"=="6" goto ask_message
goto run

:ask_message
set /p custom_message="请输入提交信息 (留空则使用Git最新提交信息): "
if not "%custom_message%"=="" set message_flag=-message "%custom_message%"
goto run

:run
echo.
echo [信息] 读取配置文件: version.config.json，使用模式: %mode_input%
echo.

REM 运行程序
go run main.go -config=version.config.json -mode=%mode_input% %message_flag%

if %ERRORLEVEL% NEQ 0 (
    echo.
    echo [错误] 程序执行失败，错误代码: %ERRORLEVEL%
)

echo.
echo ========================================
echo    处理完成
echo ========================================
pause