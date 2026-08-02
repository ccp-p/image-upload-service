@echo off
chcp 65001 >nul 2>&1
title Twitter/X User Monitor
cd /d "%~dp0"

:MENU
cls
echo ============================================
echo        Twitter/X User Monitor
echo ============================================
echo.
echo   Monitor: @1Ylik (https://x.com/1Ylik)
echo   Notify:  PushPlus
echo.
echo   Select option:
echo.
echo     [1] Start monitoring (default 10 min interval)
echo     [2] Start with custom interval
echo     [3] Check once (single check)
echo     [4] Send test notification
echo     [5] Reset state (clear state.json)
echo     [0] Exit
echo.
echo ============================================
set /p choice=Enter option: 

if "%choice%"=="1" goto :START_DEFAULT
if "%choice%"=="2" goto :CUSTOM_INTERVAL
if "%choice%"=="3" goto :ONCE
if "%choice%"=="4" goto :TEST
if "%choice%"=="5" goto :RESET
if "%choice%"=="0" exit /b 0

echo.
echo Invalid option, please try again...
timeout /t 2 >nul
goto :MENU

:START_DEFAULT
echo.
echo Starting monitor (10 min interval)...
echo ============================================
twimon.exe -user=1Ylik -interval=10
goto :END

:CUSTOM_INTERVAL
cls
echo ============================================
echo          Custom Interval (minutes)
echo ============================================
echo.
set /p INTERVAL=Enter interval in minutes: 
echo.
echo Starting monitor (%INTERVAL% min interval)...
echo ============================================
twimon.exe -user=1Ylik -interval=%INTERVAL%
goto :END

:ONCE
echo.
echo Running single check...
echo ============================================
twimon.exe -user=1Ylik -once
echo.
echo ============================================
echo   Check complete
echo ============================================
pause
goto :MENU

:TEST
echo.
echo Sending test notification...
echo ============================================
twimon.exe -user=1Ylik -test
echo.
pause
goto :MENU

:RESET
if exist state.json (
    del state.json
    echo State file deleted. Next run will reinitialize.
) else (
    echo No state file found.
)
timeout /t 2 >nul
goto :MENU

:END
echo.
echo ============================================
echo   Process exited
echo ============================================
pause
goto :MENU