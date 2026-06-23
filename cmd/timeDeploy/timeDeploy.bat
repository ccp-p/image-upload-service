@echo off
chcp 65001 >nul 2>&1
setlocal EnableDelayedExpansion
title CDN 自动化部署工具
color 0A

:menu
cls
echo.
echo  +==========================================================+
echo  ^|              CDN 自动化部署工具                           ^|
echo  +==========================================================+
echo  ^|                                                          ^|
echo  ^|  [1] 立即部署 + 检测更新                                 ^|
echo  ^|  [2] 立即部署 (不检测)                                   ^|
echo  ^|  [3] 仅检测 CDN 是否更新                                 ^|
echo  ^|                                                          ^|
echo  ^|  ----------- 定时模式 -----------                        ^|
echo  ^|                                                          ^|
echo  ^|  [4] 定时部署 (09:00 / 09:30 / 12:00)                   ^|
echo  ^|  [5] 定时检测 (09:00 / 09:30 / 12:00)                   ^|
echo  ^|  [6] 定时全部 (09:00 / 09:30 / 12:00)                   ^|
echo  ^|                                                          ^|
echo  ^|  ----------- 其它 -----------                             ^|
echo  ^|                                                          ^|
echo  ^|  [7] 设置环境变量                                        ^|
echo  ^|  [0] 退出                                                ^|
echo  ^|                                                          ^|
echo  +==========================================================+
echo.

:: ---- 显示当前环境变量状态 ----
echo  -- 当前环境 --
if defined PUSH_PLUS (
    echo   PUSH_PLUS = !PUSH_PLUS:~0,6!******
) else (
    echo   PUSH_PLUS = [未设置]
)
if "!IS_HOME!"=="1" (
    echo   IS_HOME   = 1 - 家里
) else (
    echo   IS_HOME   = !IS_HOME! - 公司
)
echo.
set /p choice=请输入数字 [0-7]:

if "%choice%"=="1" goto full_now
if "%choice%"=="2" goto deploy_now
if "%choice%"=="3" goto check_now
if "%choice%"=="4" goto scheduled_deploy
if "%choice%"=="5" goto scheduled_check
if "%choice%"=="6" goto scheduled_full
if "%choice%"=="7" goto set_env
if "%choice%"=="0" goto quit

echo  [错误] 无效选项，请重新输入
timeout /t 2 >nul
goto menu

:: ============================================================
::  立即执行
:: ============================================================

:full_now
cls
echo.
echo  [执行] 立即部署 + 检测更新
echo.
go run main.go --now -mode=full
echo.
pause
goto menu

:deploy_now
cls
echo.
echo  [执行] 立即部署 (不检测)
echo.
go run main.go --now -mode=deploy
echo.
pause
goto menu

:check_now
cls
echo.
echo  [执行] 仅检测 CDN 是否更新
echo.
go run main.go --now -mode=check
echo.
pause
goto menu

:: ============================================================
::  定时模式 — 后台循环等待，到点执行
:: ============================================================

:scheduled_deploy
cls
echo.
echo  [定时部署] 将在 09:00 / 09:30 / 12:00 自动执行部署
echo  按 Ctrl+C 可随时停止
echo.
set "SCHEDULE_MODE=deploy"
goto schedule_loop

:scheduled_check
cls
echo.
echo  [定时检测] 将在 09:00 / 09:30 / 12:00 自动执行检测
echo  按 Ctrl+C 可随时停止
echo.
set "SCHEDULE_MODE=check"
goto schedule_loop

:scheduled_full
cls
echo.
echo  [定时全部] 将在 09:00 / 09:30 / 12:00 自动执行部署+检测
echo  按 Ctrl+C 可随时停止
echo.
set "SCHEDULE_MODE=full"
goto schedule_loop

:schedule_loop
set "NOW_HH=%time:~0,2%"
set "NOW_MM=%time:~3,2%"
set "NOW_HH=%NOW_HH: =0%"
set "NOW=%NOW_HH%:%NOW_MM%"

set "T1=09:00"
set "T2=09:30"
set "T3=12:00"

set "TODAY=%date:~0,4%%date:~5,2%%date:~8,2%"
set "MARKER_DIR=%temp%\cdn_scheduler"
if not exist "!MARKER_DIR!" mkdir "!MARKER_DIR!"

if "!NOW!"=="!T1!" call :check_and_run "!T1!"
if "!NOW!"=="!T2!" call :check_and_run "!T2!"
if "!NOW!"=="!T3!" call :check_and_run "!T3!"

echo  [!date! !time:~0,8!] 等待中... (模式=!SCHEDULE_MODE!) 监控: 09:00 / 09:30 / 12:00
timeout /t 30 /nobreak >nul
goto schedule_loop

:check_and_run
set "TID=%~1"
set "MARKER_FILE=!MARKER_DIR!\!TODAY!_!SCHEDULE_MODE!_!TID::=!.done"
if exist "!MARKER_FILE!" (
    echo  [!date! !time:~0,8!] !TID! 已执行过，跳过
    goto :eof
)
echo  [!date! !time:~0,8!] 触发执行 !SCHEDULE_MODE! ...
echo !TID! > "!MARKER_FILE!"
go run main.go --now -mode=!SCHEDULE_MODE!
echo  [!date! !time:~0,8!] 执行完毕
goto :eof

:: ============================================================
::  设置环境变量
:: ============================================================

:set_env
cls
echo.
echo  +========================================+
echo  ^|           设置环境变量                  ^|
echo  +========================================+
echo  ^|                                        ^|
echo  ^|  [1] 设置为 家里 (IS_HOME=1)           ^|
echo  ^|  [2] 设置为 公司 (IS_HOME=0)           ^|
echo  ^|  [3] 设置 PUSH_PLUS token              ^|
echo  ^|  [4] 返回主菜单                        ^|
echo  ^|                                        ^|
echo  +========================================+
echo.
set /p env_choice=请选择 [1-4]:

if "!env_choice!"=="1" (
    setx IS_HOME "1" >nul
    set "IS_HOME=1"
    echo.
    echo  [OK] 已设置 IS_HOME=1 (家里)
    echo     targetDir : D:\self_project\go_project\image-upload-service\cmd\hashCdn
    echo     svnRoot   : D:\job_project\china_mobile\huidu\xhmqqthy-res
    echo     destPath  : D:\job_project\china_mobile\huidu\xhmqqthy-res
    echo.
    pause
    goto menu
)
if "!env_choice!"=="2" (
    setx IS_HOME "0" >nul
    set "IS_HOME=0"
    echo.
    echo  [OK] 已设置 IS_HOME=0 (公司)
    echo     targetDir : D:\project\my_go_project\image-upload-service\cmd\hashCdn
    echo     svnRoot   : D:\project\cx_project\china_mobile\huidu\xhmqqthy-res
    echo     destPath  : D:\project\cx_project\china_mobile\huidu\xhmqqthy-res
    echo.
    pause
    goto menu
)
if "!env_choice!"=="3" (
    echo.
    set /p pp_token=请输入 PUSH_PLUS token:
    if "!pp_token!"=="" (
        echo  [取消] 未输入 token
    ) else (
        setx PUSH_PLUS "!pp_token!" >nul
        set "PUSH_PLUS=!pp_token!"
        echo.
        echo  [OK] 已设置 PUSH_PLUS
    )
    echo.
    pause
    goto menu
)
if "!env_choice!"=="4" goto menu

echo  [错误] 无效选项
timeout /t 2 >nul
goto set_env

:quit
echo.
echo  再见
timeout /t 1 >nul
endlocal
exit