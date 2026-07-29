# cloudflared-tunnel.ps1
# Cloudflare quick tunnel: copy URL to clipboard + send PushPlus notification

$targetUrl   = 'http://192.168.241.10:3000'
$pushPlusUrl = 'https://www.pushplus.plus/send'
$found       = $false

& 'D:\download\cloudflared.exe' tunnel --url $targetUrl 2>&1 | ForEach-Object {
    $line = $_.ToString()
    Write-Host $line
    if (-not $found -and $line -match 'https://[a-z0-9-]+\.trycloudflare\.com') {
        $found = $true
        $url = $matches[0]
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
                    Invoke-RestMethod -Uri $pushPlusUrl -Method Post -ContentType 'application/json; charset=utf-8' -Body $bodyBytes -TimeoutSec 15 | Out-Null
                    Write-Host "PushPlus notification sent: $url" -ForegroundColor Green
                    $sent = $true
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
