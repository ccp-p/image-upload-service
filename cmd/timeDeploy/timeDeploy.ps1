# ============================================
#        CDN Deploy and Check Tool
# ============================================

$ErrorActionPreference = "Stop"
$Host.UI.RawUI.WindowTitle = "CDN Deploy Tool"

# ✅ 关键修复：切换到脚本所在目录，确保相对路径正确
Set-Location $PSScriptRoot

# 缓存文件路径
$script:CacheFile = Join-Path $PSScriptRoot ".run_cache.json"

# 读取缓存
function Get-Cache {
    if (Test-Path $script:CacheFile) {
        try {
            return Get-Content $script:CacheFile -Raw | ConvertFrom-Json
        } catch {
            return $null
        }
    }
    return $null
}

# 保存缓存
function Save-Cache {
    param($ScheduleChoice, $ScheduleTime, $Mode)
    $cache = @{
        scheduleChoice = $ScheduleChoice
        scheduleTime   = $ScheduleTime
        runMode        = $Mode
    }
    $cache | ConvertTo-Json | Set-Content $script:CacheFile -Encoding UTF8
}

$script:LastCache = Get-Cache

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

    # 显示上次选项提示
    if ($script:LastCache) {
        $lastChoice = $script:LastCache.scheduleChoice
        $lastMode = $script:LastCache.runMode
        $lastTime = $script:LastCache.scheduleTime
        $displayInfo = "choice=$lastChoice, mode=$lastMode"
        if ($lastChoice -eq "5" -and $lastTime) {
            $displayInfo += ", time=$lastTime"
        }
        Write-Host "  [Last] $displayInfo" -ForegroundColor DarkGray
    }

    Write-Host ""
    Write-Host "    [1] 21:00 (actual: 20:55)"
    Write-Host "    [2] 21:30 (actual: 21:25)"
    Write-Host "    [3] 22:00 (actual: 21:55)"
    Write-Host "    [4] 24:00 (actual: 23:55)"
    Write-Host "    [5] Custom time"
    Write-Host "    [6] Run now (--now)"
    Write-Host "    [7] Check update only (no deploy)"
    Write-Host "    [0] Exit"
    Write-Host ""
    Write-Host "============================================"
}

function Get-ScheduleTime {
    while ($true) {
        Show-Menu

        # 构建提示信息
        $promptText = "Enter option"
        if ($script:LastCache) {
            $promptText += " (Enter=reuse last)"
        }

        $choice = Read-Host $promptText

        # 直接回车使用上次缓存的选项
        if ($choice -eq "" -and $script:LastCache) {
            $choice = $script:LastCache.scheduleChoice
            # 如果上次是自定义时间，直接复用
            if ($choice -eq "5" -and $script:LastCache.scheduleTime) {
                return @{ Choice = "5"; Time = $script:LastCache.scheduleTime; Now = $false; CheckOnly = $false }
            }
        }

        switch ($choice) {
            "1" { return @{ Choice = "1"; Time = "2055"; Now = $false; CheckOnly = $false } }
            "2" { return @{ Choice = "2"; Time = "2125"; Now = $false; CheckOnly = $false } }
            "3" { return @{ Choice = "3"; Time = "2155"; Now = $false; CheckOnly = $false } }
            "4" { return @{ Choice = "4"; Time = "2355"; Now = $false; CheckOnly = $false } }
            "5" {
                Clear-Host
                Write-Host "============================================"
                Write-Host "         Custom Time (HHMM format)"
                Write-Host "============================================"
                Write-Host ""
                Write-Host "  Example: 2130 = 21:30, 905 = 09:05"
                Write-Host "  Note: Custom time runs at exact input."

                # 显示上次自定义时间
                if ($script:LastCache -and $script:LastCache.scheduleChoice -eq "5" -and $script:LastCache.scheduleTime) {
                    Write-Host ""
                    Write-Host "  [Last] $($script:LastCache.scheduleTime)" -ForegroundColor DarkGray
                }

                Write-Host ""
                $custom = Read-Host "Enter time"

                # 直接回车使用上次自定义时间
                if ($custom -eq "" -and $script:LastCache -and $script:LastCache.scheduleChoice -eq "5" -and $script:LastCache.scheduleTime) {
                    $custom = $script:LastCache.scheduleTime
                }

                if ($custom -match '^\d{3,4}$') {
                    $numVal = [int]$custom
                    if ($numVal -le 2359) {
                        return @{ Choice = "5"; Time = $custom; Now = $false; CheckOnly = $false }
                    } else {
                        Write-Host "`nTime out of range. Max is 2359." -ForegroundColor Yellow
                        Start-Sleep -Seconds 2
                    }
                } else {
                    Write-Host "`nInvalid format. Please enter 3 or 4 digits." -ForegroundColor Yellow
                    Start-Sleep -Seconds 2
                }
            }
            "6" { return @{ Choice = "6"; Time = "2125"; Now = $true; CheckOnly = $false } }
            "7" { return @{ Choice = "7"; Time = ""; Now = $true; CheckOnly = $true } }
            "0" { exit 0 }
            default {
                Write-Host "`nInvalid option, please try again..." -ForegroundColor Yellow
                Start-Sleep -Seconds 2
            }
        }
    }
}

function Get-RunMode {
    param([bool]$CheckOnly)

    # 如果是纯检测模式，直接返回 check，跳过菜单
    if ($CheckOnly) {
        return "check"
    }

    # 获取上次缓存的模式
    $lastMode = $null
    if ($script:LastCache -and $script:LastCache.runMode) {
        $lastMode = $script:LastCache.runMode
    }

    Clear-Host
    Write-Host "============================================"
    Write-Host "           Select Run Mode"
    Write-Host "============================================"
    Write-Host ""
    Write-Host "    [1] full   - deploy + check (default)"
    Write-Host "    [2] deploy - deploy only"
    Write-Host "    [3] check  - check only"
    Write-Host ""

    # 构建提示信息
    $promptText = "Enter option"
    if ($lastMode) {
        $promptText += " (Enter=reuse last '$lastMode')"
    } else {
        $promptText += " (press Enter for full)"
    }

    $modeChoice = Read-Host $promptText

    # 直接回车：有缓存用缓存，没缓存用 full
    if ($modeChoice -eq "") {
        if ($lastMode) { return $lastMode }
        return "full"
    }

    switch ($modeChoice) {
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

# ===================== 预编译 =====================
$exePath = Join-Path $PSScriptRoot "timeDeploy.exe"
$srcPath = Join-Path $PSScriptRoot "main.go"

function Build-IfNeeded {
    # 比较 exe 和 main.go 的修改时间，仅在源码变化时重新编译
    $needBuild = $false
    if (-not (Test-Path $exePath)) {
        $needBuild = $true
    } else {
        $exeTime = (Get-Item $exePath).LastWriteTime
        $srcTime = (Get-Item $srcPath).LastWriteTime
        if ($srcTime -gt $exeTime) {
            $needBuild = $true
        }
    }

    if ($needBuild) {
        Write-Host "[Build] Compiling timeDeploy.exe ..." -ForegroundColor Yellow
        & go build -o $exePath main.go
        if ($LASTEXITCODE -ne 0) {
            Write-Host "[ERROR] Build failed!" -ForegroundColor Red
            Read-Host "Press Enter to exit"
            exit 1
        }
        Write-Host "[Build] Done." -ForegroundColor Green
    }
}

Build-IfNeeded

# ===================== Main Loop =====================
while ($true) {
    $schedule = Get-ScheduleTime
    $mode = Get-RunMode -CheckOnly $schedule.CheckOnly

    # Confirm
    Clear-Host
    Write-Host "============================================"
    Write-Host "             Confirm Start"
    Write-Host "============================================"
    Write-Host ""
    if ($schedule.CheckOnly) {
        Write-Host "  Mode: Check Update Only (Run Now)" -ForegroundColor Cyan
    } elseif ($schedule.Now) {
        Write-Host "  Mode: Run Now ($mode)" -ForegroundColor Cyan
    } else {
        $timeStr = $schedule.Time.PadLeft(4, '0')
        $displayTime = "$($timeStr.Substring(0,2)):$($timeStr.Substring(2,2))"
        Write-Host "  Scheduled Time: $displayTime" -ForegroundColor Cyan
        Write-Host "  Mode: $mode" -ForegroundColor Cyan
    }
    Write-Host ""

    $confirm = Read-Host "Confirm? (Y/n)"
    if ($confirm -eq 'n' -or $confirm -eq 'N') {
        continue
    }

    Write-Host "`nStarting..." -ForegroundColor Green
    Write-Host "============================================"

    # 保存本次选项到缓存
    Save-Cache -ScheduleChoice $schedule.Choice -ScheduleTime $schedule.Time -Mode $mode

    # Build arguments — 直接运行预编译的 exe，不再用 go run
    $runArgs = @("-mode=$mode")

    if ($schedule.CheckOnly) {
        $runArgs += "--now"
    } else {
        $runArgs += "-time=$($schedule.Time)"
        if ($schedule.Now) {
            $runArgs += "--now"
        }
    }

    # Execute
    & $exePath @runArgs

    Write-Host ""
    Write-Host "============================================"
    Write-Host "  Process exited with code: $LASTEXITCODE"
    Write-Host "============================================"
    Read-Host "Press Enter to return to menu"
}