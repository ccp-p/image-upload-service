#requires -Version 5.1
<#
.SYNOPSIS
  Cloudflare quick tunnel watchdog.

.DESCRIPTION
  Records the last successful quick tunnel URL (written by cloudflared-tunnel.ps1
  into tunnel-state.json), periodically health-checks that public URL, and when
  it becomes unreachable re-runs cloudflared-tunnel.bat to establish a fresh
  quick tunnel. A local-target probe guards against pointless restarts when the
  origin itself is down.
#>

# ---------------- Configuration ----------------
# Local service that cloudflared proxies to (used to classify failures).
$TargetUrl          = 'http://192.168.241.10:3000'
# Path appended to a URL when probing health (e.g. '/health'). '/' probes the root.
$HealthPath         = '/'
# Seconds between health checks of the public tunnel URL.
$CheckIntervalSec   = 60
# Consecutive failures required before restarting the tunnel.
$FailureThreshold   = 3
# Per-request HTTP timeout for health checks.
$RequestTimeoutSec  = 15
# How long to wait for a fresh tunnel URL after launching the bat.
$StartTimeoutSec    = 90
# Cool-down after a restart before resuming health checks.
$RestartCooldownSec = 30
# When true, probe the local target before restarting so we don't churn URLs
# when the origin (not the tunnel) is the problem.
$CheckLocalTarget   = $true

# ---------------- Derived paths ----------------
$ScriptDir = $PSScriptRoot
$StateFile = Join-Path $ScriptDir 'tunnel-state.json'
$LogFile   = Join-Path $ScriptDir 'cloudflared-watchdog.log'
$LockFile  = Join-Path $ScriptDir 'cloudflared-watchdog.lock'
$TunnelBat = Join-Path $ScriptDir 'cloudflared-tunnel.bat'

# Force TLS 1.2 (PS 5.1 defaults to TLS 1.0 which fails on modern HTTPS).
try { [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12 } catch { }

# ---------------- Mutable state ----------------
$script:CurrentLauncherId = 0
$script:Failures          = 0
$script:OkCount           = 0
$script:LastUrl           = $null

# ---------------- Helpers ----------------
function Write-Log {
    param([string]$Message, [string]$Level = 'INFO')
    $ts = Get-Date -Format 'yyyy-MM-dd HH:mm:ss'
    $line = "[$ts] [$Level] $Message"
    Write-Host $line
    try { Add-Content -LiteralPath $LogFile -Value $line -Encoding UTF8 } catch { }
}

function Read-TunnelState {
    if (-not (Test-Path -LiteralPath $StateFile)) { return $null }
    try {
        $raw = [IO.File]::ReadAllText($StateFile).TrimStart([char]0xFEFF)
        if ([string]::IsNullOrWhiteSpace($raw)) { return $null }
        return ($raw | ConvertFrom-Json)
    } catch {
        Write-Log "Failed to read tunnel state: $($_.Exception.Message)" 'WARN'
        return $null
    }
}

# Returns true when the URL answers with a status below 500 (2xx/3xx/4xx),
# i.e. something is actually responding. Only transport errors or 5xx count as down.
function Test-UrlReachable {
    param([string]$Url)
    if ([string]::IsNullOrWhiteSpace($Url)) { return $false }
    $probe = $Url.TrimEnd('/') + $HealthPath
    try {
        $resp = Invoke-WebRequest -Uri $probe -TimeoutSec $RequestTimeoutSec -UseBasicParsing -MaximumRedirection 5
        return ([int]$resp.StatusCode -lt 500)
    } catch {
        $ex = $_.Exception
        switch ($ex.GetType().FullName) {
            'Microsoft.PowerShell.Commands.HttpResponseException' {
                try { return ([int]$ex.Response.StatusCode -lt 500) } catch { return $false }
            }
            'System.Net.WebException' {
                if ($ex.Status -and $ex.Status.ToString() -eq 'ProtocolError') {
                    try { return ([int]$ex.Response.StatusCode -lt 500) } catch { return $false }
                }
                return $false
            }
            default { return $false }
        }
    }
}

function Get-CloudflaredProcesses {
    # Leading comma prevents the pipeline from unwrapping an empty array to $null.
    , @(Get-Process -Name cloudflared -ErrorAction SilentlyContinue)
}

function Stop-ExistingTunnel {
    $procs = Get-CloudflaredProcesses
    if ($procs.Count -gt 0) {
        Write-Log "Stopping cloudflared process(es): $($procs.Id -join ', ')"
        $procs | Stop-Process -Force -ErrorAction SilentlyContinue
        Start-Sleep -Seconds 3
    } else {
        Write-Log 'No running cloudflared process found.'
    }
}

function Stop-Launcher {
    param([int]$LauncherId)
    if (-not $LauncherId) { return }
    try {
        $p = Get-Process -Id $LauncherId -ErrorAction Stop
        Write-Log "Closing stale tunnel launcher window (PID $LauncherId)."
        Stop-Process -Id $LauncherId -Force -ErrorAction SilentlyContinue
    } catch { }
}

function Start-TunnelBat {
    if (-not (Test-Path -LiteralPath $TunnelBat)) {
        Write-Log "cloudflared-tunnel.bat not found at $TunnelBat" 'ERROR'
        return 0
    }
    Write-Log 'Launching cloudflared-tunnel.bat ...'
    $proc = Start-Process -FilePath $TunnelBat -PassThru
    if ($proc -and $proc.Id) {
        Write-Log "Tunnel launched, launcher PID: $($proc.Id)"
        return $proc.Id
    }
    Write-Log 'Failed to launch tunnel bat.' 'ERROR'
    return 0
}

function Wait-ForNewTunnelUrl {
    param([string]$OldUrl)
    Write-Log "Waiting for a fresh tunnel URL (up to $StartTimeoutSec s)..."
    $deadline = (Get-Date).AddSeconds($StartTimeoutSec)
    while ((Get-Date) -lt $deadline) {
        Start-Sleep -Seconds 3
        $s = Read-TunnelState
        if ($s -and -not [string]::IsNullOrWhiteSpace($s.url) -and $s.url -ne $OldUrl) {
            Write-Log "New tunnel URL recorded: $($s.url)" 'OK'
            return $s.url
        }
    }
    Write-Log 'Timed out waiting for a new tunnel URL.' 'ERROR'
    return $null
}

# Kill the dead tunnel and start a fresh one. Returns $true if a new URL appeared.
function Restart-Tunnel {
    param([string]$OldUrl)
    if ($CheckLocalTarget) {
        if (-not (Test-UrlReachable -Url $TargetUrl)) {
            Write-Log "Public tunnel is down, but local target $TargetUrl is also unreachable. This is an origin problem, not a tunnel problem; holding off on restart to avoid churning URLs." 'WARN'
            return $false
        }
        Write-Log "Local target $TargetUrl is reachable but the public tunnel is down. Restarting tunnel."
    } else {
        Write-Log 'Public tunnel unreachable; restarting.'
    }
    Stop-ExistingTunnel
    Stop-Launcher -LauncherId $script:CurrentLauncherId
    Start-Sleep -Seconds 1
    $script:CurrentLauncherId = Start-TunnelBat
    $newUrl = Wait-ForNewTunnelUrl -OldUrl $OldUrl
    if ($newUrl) {
        $script:LastUrl  = $newUrl
        $script:Failures = 0
        Start-Sleep -Seconds $RestartCooldownSec
        return $true
    }
    return $false
}

function Acquire-Lock {
    if (Test-Path -LiteralPath $LockFile) {
        try {
            $oldPid = [int](([IO.File]::ReadAllText($LockFile)).Trim())
            if ($oldPid -gt 0) {
                $alive = Get-Process -Id $oldPid -ErrorAction SilentlyContinue
                if ($alive -and ($alive.ProcessName -ieq 'powershell' -or $alive.ProcessName -ieq 'pwsh')) {
                    Write-Host "Another watchdog instance is running (PID $oldPid). Exiting." -ForegroundColor Yellow
                    exit 2
                }
            }
        } catch { }
    }
    try { [IO.File]::WriteAllText($LockFile, "$PID") } catch { }
}

function Release-Lock {
    try { if (Test-Path -LiteralPath $LockFile) { Remove-Item -LiteralPath $LockFile -Force } } catch { }
}

# ---------------- Main ----------------
# Only run main when the script is executed directly, not when dot-sourced for testing.
if ($MyInvocation.InvocationName -eq '.' -or $env:_CLOUDFLARED_WD_TEST) { return }

Acquire-Lock
Write-Log '====== Cloudflared tunnel watchdog started ======'
Write-Log "State file : $StateFile"
Write-Log "Tunnel bat : $TunnelBat"
Write-Log "Local target: $TargetUrl"
Write-Log "Check every ${CheckIntervalSec}s; restart after $FailureThreshold consecutive failures."

try {
    while ($true) {
        $cloudflaredRunning = (Get-CloudflaredProcesses).Count -gt 0
        $state = Read-TunnelState
        $url   = if ($state) { $state.url } else { $null }

        # No cloudflared process -> ensure a tunnel is running.
        if (-not $cloudflaredRunning) {
            if ($url) {
                Write-Log "cloudflared not running; last recorded URL was $url. Starting a fresh tunnel."
            } else {
                Write-Log 'cloudflared not running and no URL recorded. Starting tunnel.'
            }
            $script:CurrentLauncherId = Start-TunnelBat
            $url = Wait-ForNewTunnelUrl -OldUrl $url
            if ($url) {
                $script:LastUrl  = $url
                $script:Failures = 0
            } else {
                Write-Log 'Tunnel did not come up; will retry next cycle.' 'ERROR'
            }
            Start-Sleep -Seconds $RestartCooldownSec
            continue
        }

        # Process running but URL not recorded yet -> wait for it.
        if ([string]::IsNullOrWhiteSpace($url)) {
            Write-Log 'cloudflared running but tunnel URL not recorded yet; waiting...'
            Start-Sleep -Seconds 5
            continue
        }

        # Health check.
        if (Test-UrlReachable -Url $url) {
            $script:Failures = 0
            if ($script:LastUrl -ne $url) {
                Write-Log "Tunnel healthy: $url" 'OK'
                $script:LastUrl = $url
                $script:OkCount = 1
            } else {
                $script:OkCount++
                if ($script:OkCount % 10 -eq 0) {
                    Write-Log "Tunnel still healthy: $url (check #$($script:OkCount))"
                }
            }
        } else {
            $script:Failures++
            Write-Log "Tunnel health check FAILED ($($script:Failures)/$FailureThreshold): $url" 'WARN'
            if ($script:Failures -ge $FailureThreshold) {
                $restarted = Restart-Tunnel -OldUrl $url
                if (-not $restarted) { Start-Sleep -Seconds $RestartCooldownSec }
                $script:Failures = 0
            }
        }

        Start-Sleep -Seconds $CheckIntervalSec
    }
} finally {
    Release-Lock
    Write-Log 'Cloudflared tunnel watchdog stopped.'
}
