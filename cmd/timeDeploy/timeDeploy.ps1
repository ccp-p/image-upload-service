# ============================================
#        CDN Deploy and Check Tool
# ============================================

$ErrorActionPreference = "Stop"
$Host.UI.RawUI.WindowTitle = "CDN Deploy Tool"

# ✅ 关键修复：切换到脚本所在目录，确保相对路径正确
Set-Location $PSScriptRoot

# 检查前置条件
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    Write-Host "[ERROR] 'go' command not found in PATH." -ForegroundColor Red
    Read-Host "Press Enter to exit"
    exit 1
}

if (-not (Test-Path ".\main.go")) {
    Write-Host "[ERROR] main.go not found in: $PSScriptRoot" -ForegroundColor Red
    Read-Host "Press Enter to exit"
    exit 1
}

function Show-Menu {
    Clear-Host
    Write-Host "============================================"
    Write-Host "       CDN Deploy and Check Tool"
    Write-Host "============================================"
    Write-Host ""
    Write-Host "  Select schedule time:"
    Write-Host ""
    Write-Host "    [1] 21:30 (default)"
    Write-Host "    [2] 22:00"
    Write-Host "    [3] 23:00"
    Write-Host "    [4] 09:00 (next morning)"
    Write-Host "    [5] 14:00 (afternoon)"
    Write-Host "    [6] Custom time"
    Write-Host "    [7] Run now (--now)"
    Write-Host "    [0] Exit"
    Write-Host ""
    Write-Host "============================================"
}

function Get-ScheduleTime {
    while ($true) {
        Show-Menu
        $choice = Read-Host "Enter option"

        switch ($choice) {
            "1" { return @{ Time = "2130"; Now = $false } }
            "2" { return @{ Time = "2200"; Now = $false } }
            "3" { return @{ Time = "2300"; Now = $false } }
            "4" { return @{ Time = "900";  Now = $false } }
            "5" { return @{ Time = "1400"; Now = $false } }
            "6" {
                Clear-Host
                Write-Host "============================================"
                Write-Host "         Custom Time (HHMM format)"
                Write-Host "============================================"
                Write-Host ""
                Write-Host "  Example: 2130 = 21:30, 905 = 09:05"
                Write-Host ""
                $custom = Read-Host "Enter time"

                if ($custom -match '^\d{3,4}$') {
                    $numVal = [int]$custom
                    if ($numVal -le 2359) {
                        return @{ Time = $custom; Now = $false }
                    } else {
                        Write-Host "`nTime out of range. Max is 2359." -ForegroundColor Yellow
                        Start-Sleep -Seconds 2
                    }
                } else {
                    Write-Host "`nInvalid format. Please enter 3 or 4 digits." -ForegroundColor Yellow
                    Start-Sleep -Seconds 2
                }
            }
            "7" { return @{ Time = "2130"; Now = $true } }
            "0" { exit 0 }
            default {
                Write-Host "`nInvalid option, please try again..." -ForegroundColor Yellow
                Start-Sleep -Seconds 2
            }
        }
    }
}

function Get-RunMode {
    Clear-Host
    Write-Host "============================================"
    Write-Host "           Select Run Mode"
    Write-Host "============================================"
    Write-Host ""
    Write-Host "    [1] full   - deploy + check (default)"
    Write-Host "    [2] deploy - deploy only"
    Write-Host "    [3] check  - check only"
    Write-Host ""

    $modeChoice = Read-Host "Enter option (press Enter for full)"
    switch ($modeChoice) {
        ""      { return "full" }
        "1"     { return "full" }
        "2"     { return "deploy" }
        "3"     { return "check" }
        default {
            Write-Host "Invalid option, using default full mode" -ForegroundColor Yellow
            Start-Sleep -Seconds 2
            return "full"
        }
    }
}

# ===================== Main Loop =====================
while ($true) {
    $schedule = Get-ScheduleTime
    $mode = Get-RunMode

    # Confirm
    Clear-Host
    Write-Host "============================================"
    Write-Host "             Confirm Start"
    Write-Host "============================================"
    Write-Host ""
    if ($schedule.Now) {
        Write-Host "  Mode: Run Now ($mode)" -ForegroundColor Cyan
    } else {
        $timeStr = $schedule.Time.PadLeft(4, '0')
        $displayTime = "$($timeStr.Substring(0,2)):$($timeStr.Substring(2,2))"
        Write-Host "  Time: $displayTime" -ForegroundColor Cyan
        Write-Host "  Mode: $mode" -ForegroundColor Cyan
    }
    Write-Host ""

    $confirm = Read-Host "Confirm? (Y/n)"
    if ($confirm -eq 'n' -or $confirm -eq 'N') {
        continue
    }

    Write-Host "`nStarting..." -ForegroundColor Green
    Write-Host "============================================"

    # Build arguments
    $goArgs = @("run", "main.go", "-mode=$mode", "-time=$($schedule.Time)")
    if ($schedule.Now) {
        $goArgs += "--now"
    }

    # Execute go command
    & go @goArgs

    Write-Host ""
    Write-Host "============================================"
    Write-Host "  Process exited with code: $LASTEXITCODE"
    Write-Host "============================================"
    Read-Host "Press Enter to return to menu"
}