(
echo @echo off
echo setlocal enabledelayedexpansion
echo title CDN Deploy Tool
echo.
echo :MENU
echo cls
echo echo ============================================
echo echo        CDN Deploy and Check Tool
echo echo ============================================
echo echo.
echo echo   Select schedule time:
echo echo.
echo echo     [1] 21:30 (default)
echo echo     [2] 22:00
echo echo     [3] 23:00
echo echo     [4] 09:00 (next morning)
echo echo     [5] 14:00 (afternoon)
echo echo     [6] Custom time
echo echo     [7] Run now (--now)
echo echo     [0] Exit
echo echo.
echo echo ============================================
echo set /p choice=Enter option: 
echo.
echo if "%%choice%%"=="1" set "SCHED_TIME=2130" ^& goto :SELECT_MODE
echo if "%%choice%%"=="2" set "SCHED_TIME=2200" ^& goto :SELECT_MODE
echo if "%%choice%%"=="3" set "SCHED_TIME=2300" ^& goto :SELECT_MODE
echo if "%%choice%%"=="4" set "SCHED_TIME=900"  ^& goto :SELECT_MODE
echo if "%%choice%%"=="5" set "SCHED_TIME=1400" ^& goto :SELECT_MODE
echo if "%%choice%%"=="6" goto :CUSTOM_TIME
echo if "%%choice%%"=="7" set "SCHED_TIME=2130" ^& set "NOW_FLAG=--now" ^& goto :SELECT_MODE
echo if "%%choice%%"=="0" exit /b 0
echo.
echo echo Invalid option...
echo timeout /t 2 ^>nul
echo goto :MENU
echo.
echo :CUSTOM_TIME
echo cls
echo echo ============================================
echo echo          Custom Time (HHMM format)
echo echo ============================================
echo echo   Example: 2130 = 21:30, 905 = 09:05
echo echo.
echo set /p SCHED_TIME=Enter time: 
echo echo.%%SCHED_TIME%% ^| findstr /r "^[0-9][0-9][0-9]$" ^>nul 2^>^&1
echo if !errorlevel! equ 0 goto :SELECT_MODE
echo echo.%%SCHED_TIME%% ^| findstr /r "^[0-9][0-9][0-9][0-9]$" ^>nul 2^>^&1
echo if !errorlevel! equ 0 goto :SELECT_MODE
echo echo Invalid format...
echo timeout /t 3 ^>nul
echo goto :CUSTOM_TIME
echo.
echo :SELECT_MODE
echo cls
echo echo ============================================
echo echo            Select Run Mode
echo echo ============================================
echo echo     [1] full   - deploy + check (default)
echo echo     [2] deploy - deploy only
echo echo     [3] check  - check only
echo echo.
echo set /p modeChoice=Enter option (Enter for full): 
echo if "%%modeChoice%%"=="" set "RUN_MODE=full" ^& goto :CONFIRM
echo if "%%modeChoice%%"=="1" set "RUN_MODE=full"   ^& goto :CONFIRM
echo if "%%modeChoice%%"=="2" set "RUN_MODE=deploy" ^& goto :CONFIRM
echo if "%%modeChoice%%"=="3" set "RUN_MODE=check"  ^& goto :CONFIRM
echo set "RUN_MODE=full"
echo goto :CONFIRM
echo.
echo :CONFIRM
echo cls
echo echo ============================================
echo echo              Confirm Start
echo echo ============================================
echo if defined NOW_FLAG (
echo     echo   Mode: Run Now (%%RUN_MODE%%)
echo ) else (
echo     echo   Time: %%SCHED_TIME:~0,-2%%:%%SCHED_TIME:~-2%%
echo     echo   Mode: %%RUN_MODE%%
echo )
echo echo.
echo set /p confirm=Confirm? (Y/n): 
echo if /i "%%confirm%%"=="n" goto :MENU
echo echo Starting...
echo if defined NOW_FLAG (
echo     go run main.go -mode=%%RUN_MODE%% -time=%%SCHED_TIME%% --now
echo ) else (
echo     go run main.go -mode=%%RUN_MODE%% -time=%%SCHED_TIME%%
echo )
echo echo Process exited
echo pause
echo goto :MENU
) > start.bat