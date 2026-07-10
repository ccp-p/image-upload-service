@echo off
chcp 65001 >nul 2>&1
setlocal EnableDelayedExpansion

set "SCRIPT_DIR=%~dp0"
set "CONFIG_FILE=%SCRIPT_DIR%.dlcodeimg_last_path"

rem Manually define paths (sync with dlcodeImg.go)
set "PATH_1=../images/xdrNormal/202505/new/"
set "PATH_2=../../components/xdrsignNew/static/"
set "PATH_3=~@/assets/img/farm/"
set "PATH_4=~@/assets/img/nft/"
set "IDX=4"

rem Read last used path
set "LAST_PATH="
if exist "%CONFIG_FILE%" (
    set /p LAST_PATH=<"%CONFIG_FILE%"
)

rem Show menu
echo.
echo ============================================
echo   dlcodeImg.go - Select Base Path
echo ============================================
echo.

set "DEFAULT_IDX="
for /L %%I in (1,1,!IDX!) do (
    set "TAG= "
    if "!PATH_%%I!"=="!LAST_PATH!" (
        set "TAG=*"
        set "DEFAULT_IDX=%%I"
    )
    echo   %%I^) [!TAG!] !PATH_%%I!
)

echo.
if defined DEFAULT_IDX (
    echo   * = last used ^(!LAST_PATH!^)
    echo.
    set /p "CHOICE=Select [1-!IDX!] (Enter = last !DEFAULT_IDX!): "
) else (
    echo.
    set /p "CHOICE=Select [1-!IDX!]: "
)

rem Handle input
if "!CHOICE!"=="" (
    if defined DEFAULT_IDX (
        set "CHOICE=!DEFAULT_IDX!"
    ) else (
        echo [INFO] No input, using #1
        set "CHOICE=1"
    )
)

if !CHOICE! LSS 1 (
    echo [ERROR] Invalid selection
    pause
    exit /b 1
)
if !CHOICE! GTR !IDX! (
    echo [ERROR] Invalid selection
    pause
    exit /b 1
)

set "SELECTED_PATH=!PATH_%CHOICE%!"
echo.
echo Selected: !SELECTED_PATH!

rem Save selection
(echo !SELECTED_PATH!) > "%CONFIG_FILE%"

rem Run Go script
echo.
echo Running main.go ...
echo ============================================
cd /d "%SCRIPT_DIR%"
autoDlcode.exe "!SELECTED_PATH!"

echo.
echo ============================================
echo Done. Press any key to exit...
pause >nul
