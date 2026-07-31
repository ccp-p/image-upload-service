#requires -Version 5.1
<#
.SYNOPSIS
  Comprehensive test suite for cloudflared-watchdog.ps1
.DESCRIPTION
  Tests state file I/O, URL reachability logic, logging, lock mechanism,
  process detection, and tunnel URL polling. Uses a local TCP-based HTTP
  server for status-code testing without external dependencies.
#>

$ErrorActionPreference = 'Stop'

# ===================== Test Framework =====================
$script:PassCount = 0
$script:FailCount = 0

function Assert-Equal {
    param($Expected, $Actual, [string]$Name)
    if ($Expected -eq $Actual) {
        $script:PassCount++
        Write-Host "  [PASS] $Name" -ForegroundColor Green
    } else {
        $script:FailCount++
        Write-Host "  [FAIL] $Name" -ForegroundColor Red
        Write-Host "    Expected: $Expected"
        Write-Host "    Actual:   $Actual"
    }
}

function Assert-True {
    param($Condition, [string]$Name)
    if ([bool]$Condition) {
        $script:PassCount++
        Write-Host "  [PASS] $Name" -ForegroundColor Green
    } else {
        $script:FailCount++
        Write-Host "  [FAIL] $Name" -ForegroundColor Red
        Write-Host "    Expected: true"
        Write-Host "    Actual:   $Condition"
    }
}

function Assert-False {
    param($Condition, [string]$Name)
    if (-not [bool]$Condition) {
        $script:PassCount++
        Write-Host "  [PASS] $Name" -ForegroundColor Green
    } else {
        $script:FailCount++
        Write-Host "  [FAIL] $Name" -ForegroundColor Red
        Write-Host "    Expected: false"
        Write-Host "    Actual:   $Condition"
    }
}

function Assert-Null {
    param($Value, [string]$Name)
    if ($null -eq $Value) {
        $script:PassCount++
        Write-Host "  [PASS] $Name" -ForegroundColor Green
    } else {
        $script:FailCount++
        Write-Host "  [FAIL] $Name" -ForegroundColor Red
        Write-Host "    Expected: null"
        Write-Host "    Actual:   $Value"
    }
}

function Assert-NotNull {
    param($Value, [string]$Name)
    if ($null -ne $Value) {
        $script:PassCount++
        Write-Host "  [PASS] $Name" -ForegroundColor Green
    } else {
        $script:FailCount++
        Write-Host "  [FAIL] $Name" -ForegroundColor Red
        Write-Host "    Expected: non-null"
    }
}

# ===================== Setup =====================
$watchdogPath = Join-Path $PSScriptRoot 'cloudflared-watchdog.ps1'

# Prevent the watchdog main loop from starting when dot-sourced.
$env:_CLOUDFLARED_WD_TEST = '1'

# Dot-source to load all functions and configuration variables.
. $watchdogPath

# Use a temp directory for test artifacts.
$TestDir = Join-Path $env:TEMP ("wdtest_" + [System.Guid]::NewGuid().ToString('N').Substring(0,8))
New-Item -ItemType Directory -Force -Path $TestDir | Out-Null

# Override path variables so tests don't touch the real files.
$StateFile = Join-Path $TestDir 'tunnel-state.json'
$LogFile   = Join-Path $TestDir 'cloudflared-watchdog.log'
$LockFile  = Join-Path $TestDir 'cloudflared-watchdog.lock'

# Reset script-level mutable state.
$script:CurrentLauncherId = 0
$script:Failures          = 0
$script:OkCount           = 0
$script:LastUrl           = $null

Write-Host "Test directory: $TestDir" -ForegroundColor DarkGray
Write-Host ""

# ===================== Test 0: Dot-Sourcing Guard =====================
Write-Host "=== Test 0: Dot-sourcing guard (main loop must not start) ===" -ForegroundColor Cyan
Assert-False (Test-Path $LockFile) "Dot-sourced: no lock file created (main did not run)"

try {

# ===================== Test 1: State File Round-Trip =====================
Write-Host ""
Write-Host "=== Test 1: State file round-trip (ps1 -> watchdog) ===" -ForegroundColor Cyan

$expectedUrl    = 'https://abc-xyz.trycloudflare.com'
$expectedTarget = 'http://192.168.241.10:3000'
$expectedTime   = '2026-07-31 12:00:00'
$state = [ordered]@{ url = $expectedUrl; targetUrl = $expectedTarget; recordedAt = $expectedTime }
[IO.File]::WriteAllText($StateFile, ($state | ConvertTo-Json))

$obj = Read-TunnelState
Assert-NotNull $obj "Read-TunnelState returns object for valid JSON"
Assert-Equal $expectedUrl    $obj.url        "State url matches"
Assert-Equal $expectedTarget $obj.targetUrl   "State targetUrl matches"
Assert-Equal $expectedTime   $obj.recordedAt  "State recordedAt matches"

# ===================== Test 2: Read-TunnelState Edge Cases =====================
Write-Host ""
Write-Host "=== Test 2: Read-TunnelState edge cases ===" -ForegroundColor Cyan

# Missing file
if (Test-Path $StateFile) { Remove-Item $StateFile -Force }
Assert-Null (Read-TunnelState) "Missing state file -> null"

# Empty file
[IO.File]::WriteAllText($StateFile, '')
Assert-Null (Read-TunnelState) "Empty state file -> null"

# Whitespace-only file
[IO.File]::WriteAllText($StateFile, "   `n  `t  ")
Assert-Null (Read-TunnelState) "Whitespace-only state file -> null"

# Corrupt JSON
[IO.File]::WriteAllText($StateFile, '{ this is not valid json')
Assert-Null (Read-TunnelState) "Corrupt JSON -> null (no exception)"

# BOM-prefixed JSON
$bom  = [byte[]](0xEF, 0xBB, 0xBF)
$json = [Text.Encoding]::UTF8.GetBytes('{"url":"https://bom.trycloudflare.com","targetUrl":"http://localhost:3000","recordedAt":"2026-01-01 00:00:00"}')
[IO.File]::WriteAllBytes($StateFile, $bom + $json)
$objBom = Read-TunnelState
Assert-NotNull $objBom "BOM-prefixed JSON -> object"
Assert-Equal 'https://bom.trycloudflare.com' $objBom.url "BOM JSON url matches"

# JSON with extra fields (should be ignored)
[IO.File]::WriteAllText($StateFile, '{"url":"https://extra.trycloudflare.com","extra":"ignored","targetUrl":"http://x:1","recordedAt":"now"}')
$objExtra = Read-TunnelState
Assert-Equal 'https://extra.trycloudflare.com' $objExtra.url "JSON with extra fields -> url matches"

# ===================== Test 3: Test-UrlReachable =====================
Write-Host ""
Write-Host "=== Test 3: Test-UrlReachable ===" -ForegroundColor Cyan

# Save original HealthPath and use empty for path-specific tests.
$origHealthPath = $HealthPath
$HealthPath = ''

# 3a. Empty/null URL
Assert-False (Test-UrlReachable -Url '')     "Empty URL -> false"
Assert-False (Test-UrlReachable -Url $null)  "Null URL -> false"

# 3b. Unreachable host (closed port)
Assert-False (Test-UrlReachable -Url 'http://127.0.0.1:1/') "Closed port -> false"

# 3c. HTTP status codes via local TCP server
function Get-FreePort {
    $l = New-Object System.Net.Sockets.TcpListener([System.Net.IPAddress]::Loopback, 0)
    $l.Start()
    $p = $l.LocalEndpoint.Port
    $l.Stop()
    return $p
}

$testPort = Get-FreePort
Write-Host "  Starting test HTTP server on port $testPort..." -ForegroundColor DarkGray

# Start a local TCP-based HTTP server in a runspace.
$serverPS = [powershell]::Create()
[void]$serverPS.AddScript({
    param($Port)
    $tcp = New-Object System.Net.Sockets.TcpListener([System.Net.IPAddress]::Loopback, $Port)
    $tcp.Start()
    while ($true) {
        $ar = $tcp.BeginAcceptTcpClient($null, $null)
        if (-not $ar.AsyncWaitHandle.WaitOne(500)) { continue }
        $client = $tcp.EndAcceptTcpClient($ar)
        try {
            $stream = $client.GetStream()
            $stream.ReadTimeout = 5000
            $buf = New-Object byte[] 4096
            $n = $stream.Read($buf, 0, 4096)
            if ($n -eq 0) { continue }
            $req = [Text.Encoding]::ASCII.GetString($buf, 0, $n)
            $path = ($req -split ' ')[1]
            $code = 200; $reason = 'OK'; $body = 'OK'; $extra = ''
            if ($path -match '^/notfound') {
                $code = 404; $reason = 'Not Found'; $body = 'NF'
            } elseif ($path -match '^/error') {
                $code = 500; $reason = 'Internal Server Error'; $body = 'ERR'
            } elseif ($path -match '^/redirect') {
                $code = 301; $reason = 'Moved Permanently'; $body = ''
                $extra = "Location: http://127.0.0.1:$Port/ok`r`n"
            }
            $resp = "HTTP/1.1 $code $reason`r`nContent-Length: $($body.Length)`r`nConnection: close`r`n$extra`r`n$body"
            $bytes = [Text.Encoding]::ASCII.GetBytes($resp)
            $stream.Write($bytes, 0, $bytes.Length)
            $stream.Flush()
        } catch {}
        finally { $client.Close() }
    }
}).AddArgument($testPort)
$serverHandle = $serverPS.BeginInvoke()
Start-Sleep -Milliseconds 300

$baseUrl = "http://127.0.0.1:$testPort"

try {
    Assert-True  (Test-UrlReachable -Url "$baseUrl/ok")       "HTTP 200 -> true"
    Assert-True  (Test-UrlReachable -Url "$baseUrl/notfound") "HTTP 404 -> true (4xx is reachable)"
    Assert-False (Test-UrlReachable -Url "$baseUrl/error")    "HTTP 500 -> false (5xx is down)"
    Assert-True  (Test-UrlReachable -Url "$baseUrl/redirect") "HTTP 301 -> 200 (redirect followed) -> true"
    Assert-True  (Test-UrlReachable -Url $baseUrl)            "Root URL (default 200) -> true"

    # With HealthPath = '/' (default behavior)
    $HealthPath = '/'
    Assert-True (Test-UrlReachable -Url $baseUrl) "Root URL with HealthPath='/' -> true"
    $HealthPath = ''
} finally {
    try { $serverPS.Stop() } catch {}
    try { $serverPS.Dispose() } catch {}
}

$HealthPath = $origHealthPath

# ===================== Test 4: Write-Log =====================
Write-Host ""
Write-Host "=== Test 4: Write-Log ===" -ForegroundColor Cyan

if (Test-Path $LogFile) { Remove-Item $LogFile -Force }

Write-Log 'First message'  'INFO'
Write-Log 'Second message' 'WARN'
Write-Log 'Third message'  'ERROR'

Assert-True (Test-Path $LogFile) "Log file created"
$lines = Get-Content $LogFile
Assert-Equal 3 $lines.Count "Log has 3 lines"
Assert-True ($lines[0] -match '^\[\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\] \[INFO\] First message$')  "Log line 1 format correct"
Assert-True ($lines[1] -match '^\[\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\] \[WARN\] Second message$') "Log line 2 format correct"
Assert-True ($lines[2] -match '^\[\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\] \[ERROR\] Third message$') "Log line 3 format correct"

# Append doesn't overwrite
Write-Log 'Fourth message' 'OK'
$lines2 = Get-Content $LogFile
Assert-Equal 4 $lines2.Count "Log append (not overwrite) -> 4 lines"

# ===================== Test 5: Lock Mechanism =====================
Write-Host ""
Write-Host "=== Test 5: Lock mechanism ===" -ForegroundColor Cyan

# 5a. No lock file -> acquire creates it
if (Test-Path $LockFile) { Remove-Item $LockFile -Force }
Acquire-Lock
Assert-True (Test-Path $LockFile) "Acquire-Lock creates lock file"
$lockPid = [int](([IO.File]::ReadAllText($LockFile)).Trim())
Assert-Equal $PID $lockPid "Lock file contains current PID"

# 5b. Release-Lock removes it
Release-Lock
Assert-False (Test-Path $LockFile) "Release-Lock removes lock file"

# 5c. Lock with dead PID -> overwritten
[IO.File]::WriteAllText($LockFile, '99999')
Acquire-Lock
$lockPid2 = [int](([IO.File]::ReadAllText($LockFile)).Trim())
Assert-Equal $PID $lockPid2 "Lock with dead PID -> overwritten with current PID"
Release-Lock

# 5d. Lock with non-PowerShell PID -> overwritten (PID 4 = System)
$sysProc = Get-Process -Id 4 -ErrorAction SilentlyContinue
if ($sysProc -and $sysProc.ProcessName -ieq 'System') {
    [IO.File]::WriteAllText($LockFile, '4')
    Acquire-Lock
    $lockPid3 = [int](([IO.File]::ReadAllText($LockFile)).Trim())
    Assert-Equal $PID $lockPid3 "Lock with non-PowerShell PID -> overwritten"
    Release-Lock
} else {
    Write-Host "  [SKIP] PID 4 (System) not available for test" -ForegroundColor DarkGray
    $script:PassCount++
}

# 5e. Lock with live PowerShell PID -> child process exits with code 2
[IO.File]::WriteAllText($LockFile, "$PID")
$childScript = @(
    ". '$watchdogPath'"
    "`$LockFile = '$LockFile'"
    'Acquire-Lock'
) -join "`r`n"
$childPath = Join-Path $TestDir 'child-lock-test.ps1'
[IO.File]::WriteAllText($childPath, $childScript)

& powershell -NoProfile -ExecutionPolicy Bypass -File $childPath 2>&1 | Out-Null
Assert-Equal 2 $LASTEXITCODE "Lock with live PowerShell PID -> child exits with code 2"
Release-Lock

# 5f. Release-Lock when no lock file -> no error
if (Test-Path $LockFile) { Remove-Item $LockFile -Force }
Release-Lock
Assert-True $true "Release-Lock with no lock file -> no error"

# ===================== Test 6: Get-CloudflaredProcesses =====================
Write-Host ""
Write-Host "=== Test 6: Get-CloudflaredProcesses ===" -ForegroundColor Cyan

$procs = Get-CloudflaredProcesses
Assert-True ($procs -is [array]) "Returns array type"
Assert-True ($procs.Count -ge 0) "Returns non-negative count (no crash)"
if ($procs.Count -eq 0) {
    Assert-Equal 0 $procs.Count "No cloudflared running -> empty array"
} else {
    Write-Host "  [INFO] cloudflared is currently running (PID: $($procs.Id -join ', '))" -ForegroundColor DarkGray
}

# ===================== Test 7: Wait-ForNewTunnelUrl =====================
Write-Host ""
Write-Host "=== Test 7: Wait-ForNewTunnelUrl ===" -ForegroundColor Cyan

$origStartTimeout = $StartTimeoutSec
$StartTimeoutSec = 3

# 7a. New URL already in state file -> returns URL
[IO.File]::WriteAllText($StateFile, '{"url":"https://new1.trycloudflare.com","targetUrl":"http://x:1","recordedAt":"now"}')
$result = Wait-ForNewTunnelUrl -OldUrl 'https://old.trycloudflare.com'
Assert-Equal 'https://new1.trycloudflare.com' $result "New URL in state file -> returns URL"

# 7b. No state file -> times out, returns null
if (Test-Path $StateFile) { Remove-Item $StateFile -Force }
$result2 = Wait-ForNewTunnelUrl -OldUrl 'https://old.trycloudflare.com'
Assert-Null $result2 "No state file -> timeout -> null"

# 7c. Same URL as OldUrl -> times out, returns null
[IO.File]::WriteAllText($StateFile, '{"url":"https://same.trycloudflare.com","targetUrl":"http://x:1","recordedAt":"now"}')
$result3 = Wait-ForNewTunnelUrl -OldUrl 'https://same.trycloudflare.com'
Assert-Null $result3 "Same URL as OldUrl -> timeout -> null"

$StartTimeoutSec = $origStartTimeout

# ===================== Test 8: Configuration Sanity =====================
Write-Host ""
Write-Host "=== Test 8: Configuration sanity ===" -ForegroundColor Cyan

Assert-True ($CheckIntervalSec   -gt 0) "CheckIntervalSec > 0"
Assert-True ($FailureThreshold   -gt 0) "FailureThreshold > 0"
Assert-True ($RequestTimeoutSec  -gt 0) "RequestTimeoutSec > 0"
Assert-True ($StartTimeoutSec    -gt 0) "StartTimeoutSec > 0"
Assert-True ($RestartCooldownSec -ge 0) "RestartCooldownSec >= 0"
Assert-True ($TargetUrl -match '^https?://') "TargetUrl is HTTP(S) URL"
Assert-True ($HealthPath -match '^/')       "HealthPath starts with /"
Assert-True (Test-Path $TunnelBat)          "TunnelBat file exists"

} finally {
    # ===================== Cleanup =====================
    Remove-Item env:_CLOUDFLARED_WD_TEST -ErrorAction SilentlyContinue
    Remove-Item -Recurse -Force $TestDir -ErrorAction SilentlyContinue
}

# ===================== Summary =====================
Write-Host ""
Write-Host "===== Test Summary =====" -ForegroundColor Cyan
Write-Host "Passed: $script:PassCount" -ForegroundColor Green
Write-Host "Failed: $script:FailCount" -ForegroundColor $(if ($script:FailCount -gt 0) {'Red'} else {'Green'})
Write-Host ""

if ($script:FailCount -gt 0) {
    Write-Host "SOME TESTS FAILED!" -ForegroundColor Red
    exit 1
} else {
    Write-Host "ALL TESTS PASSED!" -ForegroundColor Green
    exit 0
}
