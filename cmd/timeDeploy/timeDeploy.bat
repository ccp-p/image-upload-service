@echo off
chcp 65001 >nul 2>&1
title CDN Deploy Tool
cd /d "%~dp0"
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0timeDeploy.ps1"
if %errorlevel% neq 0 pause