@echo off
setlocal enabledelayedexpansion
chcp 65001 >nul

echo ========================================
echo    HTML Hash CDN Tool (Rust edition)
echo ========================================
echo.

REM Check environment variable
if "%IS_HOME%"=="" (
    echo [warn] IS_HOME not set, defaulting to Office paths
    echo [tip]  set IS_HOME=1 for home or set IS_HOME=0 for office
    echo.
    cd /d D:\project\my_go_project\image-upload-service\cmd\hashCdn
    set "RS_EXE=D:\project\my_go_project\image-upload-service\target\release\hash-cdn.exe"
) else (
    if "%IS_HOME%"=="1" (
        echo [info] env: Home IS_HOME=1
        cd /d D:\self_project\go_project\image-upload-service\cmd\hashCdn
        set "RS_EXE=D:\self_project\go_project\image-upload-service\target\release\hash-cdn.exe"
    ) else (
        echo [info] env: Office IS_HOME=0
        cd /d D:\project\my_go_project\image-upload-service\cmd\hashCdn
        set "RS_EXE=D:\project\my_go_project\image-upload-service\target\release\hash-cdn.exe"
    )
    echo.
)

if not exist "%RS_EXE%" (
    echo [error] Rust binary not found: %RS_EXE%
    echo [tip]   Run cargo build --release first
    pause
    exit /b 1
)

REM Check config file
if not exist "version.config.json" (
    echo [error] config file not found: version.config.json
    pause
    exit /b 1
)

REM Cache file for last-used mode
set "cache_file=%~dp0.run_cache.ini"

REM Read last cached mode
set "last_mode=1"
if exist "%cache_file%" (
    for /f "usebackq tokens=1,* delims==" %%a in ("%cache_file%") do (
        if "%%a"=="mode" set "last_mode=%%b"
    )
)

echo.
echo Select deploy mode:
echo [1]  copy                       CDN replace, no auto-commit
echo [2]  copy-commit                CDN replace, auto-commit svn
echo [3]  copy-commit + git push    CDN replace, rollback HTML
echo [4]  copy  - no CDN             keep relative paths
echo [5]  copy-commit - no CDN
echo [6]  copy-commit + git - no CDN rollback HTML
echo [7]  copy  dark components to CDN
echo [8]  copy-commit dark components to CDN
echo [9]  copy-commit + git dark components, rollback HTML
echo [10] deploy only, then copy
echo [11] deploy only, then copy-commit
echo [12] revert dest SVN local changes
echo [13] revert dest SVN + revert src git
echo [14] revert src git only
echo.
echo [last] mode=%last_mode%  press Enter to reuse, or input r to reset
echo.

set "mode_input="
set /p mode_input="Enter number default=%last_mode%: "
if "!mode_input!"=="" set mode_input=!last_mode!

REM Trim leading/trailing spaces and CR from mode_input
for /f "tokens=1" %%t in ("!mode_input!") do set "mode_input=%%t"

if /i "!mode_input!"=="r" (
    if exist "!cache_file!" del "!cache_file!"
    echo [info] cache reset, please re-select
    echo.
    set "mode_input="
    set /p mode_input="Enter number default=1: "
    if "!mode_input!"=="" set mode_input=1
    for /f "tokens=1" %%t in ("!mode_input!") do set "mode_input=%%t"
)

set "message_flag="
set "custom_message="
REM Modes involving auto-commit: ask for custom message
if "!mode_input!"=="2" goto ask_message
if "!mode_input!"=="3" goto ask_message
if "!mode_input!"=="5" goto ask_message
if "!mode_input!"=="6" goto ask_message
if "!mode_input!"=="8" goto ask_message
if "!mode_input!"=="9" goto ask_message
if "!mode_input!"=="11" goto ask_message
goto save_cache

:ask_message
set /p custom_message="Commit message? blank = use Git latest: "
if not "!custom_message!"=="" set message_flag=-message "!custom_message!"
goto save_cache

:save_cache
REM Save mode to cache (skip for revert to avoid accidental reuse)
if /i "!mode_input!"=="12" goto run
if /i "!mode_input!"=="13" goto run
if /i "!mode_input!"=="14" goto run
echo mode=!mode_input!> "!cache_file!"
goto run

:run
echo.
echo [info] config: version.config.json, mode: !mode_input!
echo.

REM Modes 10/11 use -deploy/-deploy-commit (no -mode)
if "!mode_input!"=="10" (
    echo [cmd] "%RS_EXE%" -config version.config.json -deploy !message_flag!
    echo.
    "%RS_EXE%" -config version.config.json -deploy !message_flag!
    goto check_result
)
if "!mode_input!"=="11" (
    echo [cmd] "%RS_EXE%" -config version.config.json -deploy-commit !message_flag!
    echo.
    "%RS_EXE%" -config version.config.json -deploy-commit !message_flag!
    goto check_result
)
if "!mode_input!"=="12" (
    echo [cmd] "%RS_EXE%" -config version.config.json -revert-svn
    echo.
    "%RS_EXE%" -config version.config.json -revert-svn
    goto check_result
)
if "!mode_input!"=="13" (
    echo [cmd] "%RS_EXE%" -config version.config.json -revert-svn -revert-git
    echo.
    "%RS_EXE%" -config version.config.json -revert-svn -revert-git
    goto check_result
)
if "!mode_input!"=="14" (
    echo [cmd] "%RS_EXE%" -config version.config.json -revert-git
    echo.
    "%RS_EXE%" -config version.config.json -revert-git
    goto check_result
)

echo [cmd] "%RS_EXE%" -config version.config.json -mode=!mode_input! !message_flag!
echo.

REM Run Rust binary
"%RS_EXE%" -config version.config.json -mode=!mode_input! !message_flag!

:check_result
if !ERRORLEVEL! NEQ 0 (
    echo.
    echo [error] execution failed, exit code: !ERRORLEVEL!
)

echo.
echo ========================================
echo    Done
echo ========================================
pause
