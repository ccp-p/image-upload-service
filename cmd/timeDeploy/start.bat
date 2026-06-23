@echo off
setlocal enabledelayedexpansion
title CDN Deploy Tool

:MENU
cls
echo ============================================
echo        CDN Deploy and Check Tool
echo ============================================
echo.
echo   Select schedule time:
echo.
echo     [1] 21:30 (default)
echo     [2] 22:00
echo     [3] 23:00
echo     [4] 09:00 (next morning)
echo     [5] 14:00 (afternoon)
echo     [6] Custom time
echo     [7] Run now (--now)
echo     [0] Exit
echo.
echo ============================================
set /p choice=Enter option: 

if "%choice%"=="1" set "SCHED_TIME=2130" & goto :SELECT_MODE
if "%choice%"=="2" set "SCHED_TIME=2200" & goto :SELECT_MODE
if "%choice%"=="3" set "SCHED_TIME=2300" & goto :SELECT_MODE
if "%choice%"=="4" set "SCHED_TIME=900"  & goto :SELECT_MODE
if "%choice%"=="5" set "SCHED_TIME=1400" & goto :SELECT_MODE
if "%choice%"=="6" goto :CUSTOM_TIME
if "%choice%"=="7" set "SCHED_TIME=2130" & set "NOW_FLAG=--now" & goto :SELECT_MODE
if "%choice%"=="0" exit /b 0

echo.
echo Invalid option, please try again...
timeout /t 2 >nul
goto :MENU

:CUSTOM_TIME
cls
echo ============================================
echo          Custom Time (HHMM format)
echo ============================================
echo.
echo   Example: 2130 = 21:30, 905 = 09:05
echo.
set /p SCHED_TIME=Enter time: 

:: 检查长度是否为 3 或 4
set "LEN_CHECK="
if not "!SCHED_TIME:~3!"=="" if "!SCHED_TIME:~4!"=="" set "LEN_CHECK=OK"
if not "!SCHED_TIME:~2!"=="" if "!SCHED_TIME:~3!"=="" set "LEN_CHECK=OK"

if not defined LEN_CHECK (
    echo.
    echo Invalid length. Please enter 3 or 4 digits...
    timeout /t 3 >nul
    goto :CUSTOM_TIME
)

:: 检查是否为纯数字 (利用 set /a 的特性，非数字会报错或结果不一致)
set /a "NUM_TEST=!SCHED_TIME!+0" 2>nul
if "!NUM_TEST!"=="0" if not "!SCHED_TIME!"=="0" if not "!SCHED_TIME!"=="00" if not "!SCHED_TIME!"=="000" if not "!SCHED_TIME!"=="0000" (
    echo.
    echo Invalid characters. Digits only...
    timeout /t 3 >nul
    goto :CUSTOM_TIME
)

:: 校验时间范围 (0-2359)
if !SCHED_TIME! gtr 2359 (
    echo.
    echo Time out of range. Max is 2359...
    timeout /t 3 >nul
    goto :CUSTOM_TIME
)

goto :SELECT_MODE

:SELECT_MODE
cls
echo ============================================
echo            Select Run Mode
echo ============================================
echo.
echo     [1] full   - deploy + check (default)
echo     [2] deploy - deploy only
echo     [3] check  - check only
echo.
set /p modeChoice=Enter option (press Enter for full): 

if "%modeChoice%"=="" set "RUN_MODE=full" & goto :CONFIRM
if "%modeChoice%"=="1" set "RUN_MODE=full"   & goto :CONFIRM
if "%modeChoice%"=="2" set "RUN_MODE=deploy" & goto :CONFIRM
if "%modeChoice%"=="3" set "RUN_MODE=check"  & goto :CONFIRM

echo Invalid option, using default full mode
set "RUN_MODE=full"
timeout /t 2 >nul
goto :CONFIRM

:CONFIRM
cls
echo ============================================
echo              Confirm Start
echo ============================================
echo.
if defined NOW_FLAG (
    echo   Mode: Run Now (%RUN_MODE%)
) else (
    echo   Time: %SCHED_TIME:~0,-2%:%SCHED_TIME:~-2%
    echo   Mode: %RUN_MODE%
)
echo.
set /p confirm=Confirm? (Y/n): 
if /i "%confirm%"=="n" goto :MENU
if /i "%confirm%"=="N" goto :MENU

echo.
echo Starting...
echo ============================================

if defined NOW_FLAG (
    go run main.go -mode=%RUN_MODE% -time=%SCHED_TIME% --now
) else (
    go run main.go -mode=%RUN_MODE% -time=%SCHED_TIME%
)

echo.
echo ============================================
echo   Process exited
echo ============================================
pause
goto :MENU