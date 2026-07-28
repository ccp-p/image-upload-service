# SmartDeploy

SSH-based file sync tool with automatic change detection, clipboard OTP
monitoring with interactive confirmation, and persistent keep-alive
connections.

## Quick Start

    run_smartDeploy.bat

Or manually:

    go build -o smartDeploy.exe .
    smartDeploy.exe -config config.json

## Key Features

### 1. Persistent Connection with Heartbeat Keep-Alive

The SSH connection sends keepalive@openssh.com requests every
keepAliveIntervalSec seconds (default 30s). This prevents idle
timeouts from intermediate proxies or the SSH server itself, so you
stay connected with a single login.

### 2. Auto-Reconnect on Disconnect

If the connection drops (network blip, server restart), SmartDeploy
automatically attempts to reconnect with exponential backoff. If
keyboard-interactive auth is configured, it will wait for a fresh OTP
before reconnecting. You do not need to type reconnect manually.

Uploads that arrive while disconnected are buffered. ensureConnected
waits for the reconnection to complete (up to 30s) before proceeding,
so file changes are never lost.

### 3. Clipboard OTP Monitoring with Interactive Confirmation

When enableClipboard is true (default), SmartDeploy polls the system
clipboard every clipboardPollMs milliseconds. When the SSH server
requests an authentication code, SmartDeploy displays the latest
clipboard OTP in real time and waits for you to press Enter to confirm
it is current before sending. This prevents stale codes from being used.

You can also type a code manually and press Enter if you prefer not to
use the clipboard.

### 4. Remote Path Visibility and jailRoot

SmartDeploy uploads files via SSH exec (mkdir -p + cat > + ls -ld),
NOT via the SFTP subsystem. Each upload runs mkdir, cat, and verify in
a single shell session so that per-session sandboxes (common with
jumpserver/bastion hosts) cannot cause the directory to vanish between
steps. If any step fails, Upload returns an error so the caller never
sees a false [OK].

If your SSH server chroots or sandboxes sessions to a specific directory
(e.g., /tmp on a jumpserver), set the jailRoot config option. When set,
all remote paths are resolved as path.Join(jailRoot, remotePath). For
example, with jailRoot=/tmp and remoteBasePath=/ccp/xhmqqthy/, files
land at /tmp/ccp/xhmqqthy/ on the server. The [UPLOAD] log shows both
the logical path and the physical server path. If the upload command
fails (e.g., permission denied), an error is returned and [OK] is never
logged.

After connecting, SmartDeploy logs the server working directory. The
status and pwd commands show both the remote base path and the
remote working directory. When jailRoot is set, the server (physical)
path is also displayed. Use the ls and stat commands to inspect remote
files at the resolved physical path.

### 5. Interactive REPL

Commands:

  s, status          Show connection status, remote paths, and pending count
  pwd                Show remote working directory and base path
  ls [path]          List remote directory (default: remote base path)
  stat <path>        Show details of a remote file or directory
  w, watch [on|off]  Toggle or set auto-watch mode
  p, push <path>     Upload a specific file
  pa, pushall        Upload all pending files
  l, list            List pending files
  c, clear           Clear pending queue
  r, reconnect       Reconnect to server (waits for OTP confirmation)
  otp [code]         Set or show current OTP code
  h, help            Show help
  q, quit            Exit

## Configuration

See config.example.json for all options.

  host                  required   SSH server host
  port                  22         SSH server port
  username              required   SSH username
  password              optional   SSH password
  privateKeyPath        optional   Path to SSH private key
  keyPassphrase         optional   Private key passphrase
  watchFolder           required   Local folder to watch
  remoteBasePath        required   Remote base upload path
  jailRoot              optional   Root prefix for chrooted/sandboxed servers (e.g., /tmp)
  stripPrefix           optional   Path prefix to strip from remote path
  ignorePatterns        defaults   Glob patterns to ignore
  fileExtensions        defaults   File extensions to watch
  debounceMs            1500       Debounce interval for file changes
  autoWatch             true       Auto-upload on file change
  keepAliveIntervalSec  30         Heartbeat interval
  enableClipboard       true       Monitor clipboard for OTP
  clipboardPollMs       2000       Clipboard poll interval
  autoReconnect         true       Reconnect automatically on disconnect
  otpTimeoutSec         300        Max wait for OTP during auth

Environment variables:
  - SSH_PASSWORD - fallback if password is empty in config
  - SSH_KEY_PASSPHRASE - fallback if keyPassphrase is empty in config
