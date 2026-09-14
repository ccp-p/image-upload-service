@echo off
chcp 65001 >nul
go build -o cmd\fileShare\fileShare.exe ./cmd/fileShare
cmd\fileShare\fileShare.exe -dir "D:\download" -port "8080"
pause
