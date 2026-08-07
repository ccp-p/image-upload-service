# cloudflared-tunnel.ps1
# Cloudflare quick tunnel: copy URL to clipboard + send PushPlus notification

$targetUrl   = 'http://192.168.241.10:3000'
$pushPlusUrl = 'https://www.pushplus.plus/send'
$found       = $false
# State file shared with the watchdog so it can record/pick up the last successful tunnel URL.
$stateFile   = Join-Path $PSScriptRoot 'tunnel-state.json'

& 'D:\download\cloudflared.exe' tunnel --url $targetUrl 2>&1 | ForEach-Object {
    $line = $_.ToString()
    Write-Host $line
    if (-not $found -and $line -match 'https://[a-z0-9-]+\.trycloudflare\.com') {
        $found = $true
        $url = $matches[0]
        try {
            $state = [ordered]@{ url = $url; targetUrl = $targetUrl; recordedAt = (Get-Date -Format 'yyyy-MM-dd HH:mm:ss') }
            [IO.File]::WriteAllText($stateFile, ($state | ConvertTo-Json))
            Write-Host "Tunnel URL saved to state file: $stateFile" -ForegroundColor Cyan
        } catch {
            Write-Host "Failed to save tunnel state: $($_.Exception.Message)" -ForegroundColor Yellow
        }
        Set-Clipboard -Value $url
        Write-Host ''
        Write-Host "Tunnel URL copied to clipboard: $url" -ForegroundColor Green

        # PushPlus notification
        $token = $env:PUSH_PLUS
        if (-not $token) {
            Write-Host 'PUSH_PLUS not set, skipping PushPlus notification' -ForegroundColor Yellow
        } else {
            $ts = Get-Date -Format 'yyyy-MM-dd HH:mm:ss'
            $title = "Cloudflare Tunnel - $ts"
            $content = "## Cloudflare 隧道已创建`n`n**公网地址**: $url`n`n**本地目标**: $targetUrl`n`n**生成时间**: $ts"
            $body = @{ token = $token; title = $title; content = $content; template = 'markdown' } | ConvertTo-Json
            $bodyBytes = [System.Text.Encoding]::UTF8.GetBytes($body)
            $sent = $false
            for ($i = 1; $i -le 3 -and -not $sent; $i++) {
                try {
                    $resp = Invoke-RestMethod -Uri $pushPlusUrl -Method Post -ContentType 'application/json; charset=utf-8' -Body $bodyBytes -TimeoutSec 15
                    if ($resp.code -eq 200) {
                        Write-Host "PushPlus notification sent: $url" -ForegroundColor Green
                        $sent = $true
                    } else {
                        Write-Host "PushPlus attempt $i returned code $($resp.code): $($resp.msg)" -ForegroundColor Yellow
                        if ($i -lt 3) { Start-Sleep -Seconds ($i * 2) }
                    }
                } catch {
                    Write-Host "PushPlus attempt $i failed: $($_.Exception.Message)" -ForegroundColor Yellow
                    if ($i -lt 3) { Start-Sleep -Seconds ($i * 2) }
                }
            }
            if (-not $sent) {
                Write-Host 'PushPlus notification failed after 3 attempts' -ForegroundColor Red
            }
        }
    }
}

if (-not $found) {
    Write-Host ''
    Write-Host 'Tunnel URL was not detected in output.' -ForegroundColor Yellow
}
