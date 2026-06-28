@echo off
setlocal enabledelayedexpansion
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

REM 缓存文件路径
set "cache_file=%~dp0.run_cache.ini"

REM 读取上次缓存的选项（只缓存模式）
set "last_mode=1"
if exist "%cache_file%" (
    for /f "usebackq tokens=1,* delims==" %%a in ("%cache_file%") do (
        if "%%a"=="mode" set "last_mode=%%b"
    )
)

echo.
echo 请选择部署模式:
REM [1] 默认的 copy
REM [2] copy-commit (自动提交svn)
echo [1] copy(常态化的走cdn，不包括盲盒组件)
echo [2] copy-commit（常态化的走cdn，不包括盲盒组件)
echo [3] copy-commit，提交git (HTML回滚后)
echo [4] 保持相对路径(不替换CDN)，然后 copy
echo [5] 保持相对路径(不替换CDN)，然后 copy-commit
echo [6] 保持相对路径(不替换CDN)，然后 copy-commit，提交git
echo [7] copy (盲盒组件走cdn)
echo [8] copy-commit (盲盒组件走cdn)
echo [9] copy-commit，提交git (盲盒组件走cdn，HTML回滚后)
echo [10] 仅部署，不处理hash，然后 copy
echo [11] 仅部署，不处理hash，然后 copy-commit
echo.

echo [上次] 模式=%last_mode%
echo [提示] 直接回车使用上次选项，输入 r 重置所有选项
echo.

set /p mode_input="请输入对应的数字 (上次=%last_mode%，默认=1): "
REM 直接回车使用上次的模式
if "!mode_input!"=="" set mode_input=!last_mode!
REM 输入 r 重置缓存
if /i "!mode_input!"=="r" (
    if exist "!cache_file!" del "!cache_file!"
    echo [信息] 缓存已重置，请重新选择
    echo.
    set /p mode_input="请输入对应的数字 (默认=1): "
    if "!mode_input!"=="" set mode_input=1
)

set "message_flag="
set "custom_message="
REM 模式 2/3/5/6/8/9/11 涉及自动提交，提示用户输入自定义提交信息
if "%mode_input%"=="2" goto ask_message
if "%mode_input%"=="3" goto ask_message
if "%mode_input%"=="5" goto ask_message
if "%mode_input%"=="6" goto ask_message
if "%mode_input%"=="8" goto ask_message
if "%mode_input%"=="9" goto ask_message
if "%mode_input%"=="11" goto ask_message
goto save_cache

:ask_message
set /p custom_message="请输入提交信息 (留空则使用Git最新提交信息): "
if not "!custom_message!"=="" set message_flag=-message "!custom_message!"
goto save_cache

:save_cache
REM 保存本次选项到缓存文件（只缓存模式）
(
    echo mode=!mode_input!
) > "!cache_file!"
goto run

:run
echo.
echo [信息] 读取配置文件: version.config.json，使用模式: %mode_input%

REM 模式 10/11 使用 -deploy/-deploy-commit 参数，不走 -mode
if "%mode_input%"=="10" (
    echo [调试] 完整命令: hashCdn.exe -config=version.config.json -deploy %message_flag%
    echo.
    hashCdn.exe -config=version.config.json -deploy %message_flag%
    goto check_result
)
if "%mode_input%"=="11" (
    echo [调试] 完整命令: hashCdn.exe -config=version.config.json -deploy-commit %message_flag%
    echo.
    hashCdn.exe -config=version.config.json -deploy-commit %message_flag%
    goto check_result
)

echo [调试] mode_input = "%mode_input%"
echo [调试] custom_message = "%custom_message%"
echo [调试] message_flag = "%message_flag%"
echo [调试] 完整命令: go run main.go -config=version.config.json -mode=%mode_input% %message_flag%
echo.

REM 运行程序
hashCdn.exe -config=version.config.json -mode=%mode_input% %message_flag%
@REM go run main.go -config=version.config.json -mode=%mode_input% %message_flag%

:check_result

if %ERRORLEVEL% NEQ 0 (
    echo.
    echo [错误] 程序执行失败，错误代码: %ERRORLEVEL%
)

echo.
echo ========================================
echo    处理完成
echo ========================================
pause