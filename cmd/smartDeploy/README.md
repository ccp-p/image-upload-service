# SmartDeploy

SSH-based file sync tool with automatic change detection, clipboard OTP
monitoring, and persistent keep-alive connections.

## Quick Start

```bat
run_smartDeploy.bat
```

Or manually:

```
go build -o smartDeploy.exe .
smartDeploy.exe -config config.json
```

## Key Features

### 1. Persistent Connection with Heartbeat Keep-Alive

The SSH connection sends `keepalive@openssh.com` requests every
`keepAliveIntervalSec` seconds (default 30s). This prevents idle timeouts
from intermediate proxies or the SSH server itself.

### 2. Auto-Reconnect on Disconnect

If the connection drops (network blip, server restart), SmartDeploy
automatically attempts to reconnect with exponential backoff. If
keyboard-interactive auth is configured, it will wait for a fresh OTP
before reconnecting. You do not need to type `reconnect` manually.

Uploads that arrive while disconnected are buffered. `ensureConnected`
waits for the reconnection to complete (up to 30s) before proceeding,
so file changes are never lost.

### 3. Clipboard OTP Monitoring

When `enableClipboard` is true (default), SmartDeploy polls the system
clipboard every `clipboardPollMs` milliseconds. If it detects a 4-8
digit numeric code, it feeds it to the SSH keyboard-interactive
callback automatically. Just copy the OTP to your clipboard — no need
to paste it into the terminal.

### 4. Interactive REPL

Commands:

```
  s, status          Show connection status and pending count
  w, watch [on|off]  Toggle or set auto-watch mode
  p, push <path>     Upload a specific file
  pa, pushall        Upload all pending files
  l, list            List pending files
  c, clear           Clear pending queue
  r, reconnect       Reconnect to server (async)
  otp [code]         Set or show current OTP code
  h, help            Show help
  q, quit            Exit
```

## Configuration

See `config.example.json` for all options.

| Field | Default | Description |
|---|---|---|
| `host` | required | SSH server host |
| `port` | 22 | SSH server port |
| `username` | required | SSH username |
| `password` | optional | SSH password |
| `privateKeyPath` | optional | Path to SSH private key |
| `keyPassphrase` | optional | Private key passphrase |
| `watchFolder` | required | Local folder to watch |
| `remoteBasePath` | required | Remote base upload path |
| `stripPrefix` | optional | Path prefix to strip from remote path |
| `ignorePatterns` | defaults | Glob patterns to ignore |
| `fileExtensions` | defaults | File extensions to watch |
| `debounceMs` | 1500 | Debounce interval for file changes |
| `autoWatch` | true | Auto-upload on file change |
| `keepAliveIntervalSec` | 30 | Heartbeat interval |
| `enableClipboard` | true | Monitor clipboard for OTP |
| `clipboardPollMs` | 2000 | Clipboard poll interval |
| `autoReconnect` | true | Reconnect automatically on disconnect |
| `otpTimeoutSec` | 300 | Max wait for OTP during auth |

Environment variables:
  - `SSH_PASSWORD` — fallback if password is empty in config
  - `SSH_KEY_PASSPHRASE` — fallback if keyPassphrase is empty in config
