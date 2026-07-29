@echo off
chcp 65001 >nul
title Cloudflared Tunnel
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0cloudflared-tunnel.ps1"
pause
