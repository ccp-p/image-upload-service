@echo off
chcp 65001 >nul
title Cloudflared Tunnel Watchdog
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0cloudflared-watchdog.ps1"
pause
