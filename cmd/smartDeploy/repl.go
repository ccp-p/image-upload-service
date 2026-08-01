package main

import (
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// REPL provides an interactive command interface for the deployer.
type REPL struct {
	deployer       *Deployer
	client         RemoteClient
	otpStore       *OTPStore // nil when OTP auth is not configured
	lineReader     *SharedLineReader
	writer         io.Writer
	writeMu        *sync.Mutex
	otpActive      *atomic.Bool // when true the REPL yields stdin to the OTP prompter
	remoteBasePath string
	jailRoot       string
	clearCommand   string
	syncCommand    string
}

func NewREPL(d *Deployer, c RemoteClient, otp *OTPStore, lr *SharedLineReader, w io.Writer) *REPL {
	return &REPL{
		deployer:   d,
		client:     c,
		otpStore:   otp,
		lineReader: lr,
		writer:     w,
		writeMu:    &sync.Mutex{},
		otpActive:  new(atomic.Bool),
	}
}

// SetClearCommand configures the shell command to clear the temp directory.
func (r *REPL) SetClearCommand(cmd string) {
	r.clearCommand = cmd
}

// SetSyncCommand configures the shell command to rsync temp to webapp.
func (r *REPL) SetSyncCommand(cmd string) {
	r.syncCommand = cmd
}

func (r *REPL) clearTemp() {
	if r.clearCommand == "" {
		r.printf("No clearCommand configured.\n")
		return
	}
	if !r.client.IsConnected() {
		r.printf("Not connected.\n")
		return
	}
	r.printf("Clearing temp directory...\n")
	output, err := r.client.RunCommand(r.clearCommand)
	if err != nil {
		r.printf("[ERR] clear: %v\n", err)
		if output != "" {
			r.printf("%s\n", output)
		}
		return
	}
	r.printf("[OK] temp directory cleared\n")
	if output != "" {
		r.printf("%s\n", output)
	}
}

func (r *REPL) syncToWebapp() {
	if r.syncCommand == "" {
		r.printf("No syncCommand configured.\n")
		return
	}
	if !r.client.IsConnected() {
		r.printf("Not connected.\n")
		return
	}
	r.printf("Syncing to webapp...\n")
	output, err := r.client.RunCommand(r.syncCommand)
	if err != nil {
		r.printf("[ERR] sync: %v\n", err)
		if output != "" {
			r.printf("%s\n", output)
		}
		return
	}
	r.printf("[OK] sync done\n")
	if output != "" {
		r.printf("%s\n", output)
	}
}

// SetOTPActive sets the shared flag that signals the REPL to yield stdin.
func (r *REPL) SetOTPActive(flag *atomic.Bool) {
	if flag != nil {
		r.otpActive = flag
	}
}

// SetWriteMu overrides the internal write mutex so output is coordinated
// with the OTP prompter.
func (r *REPL) SetWriteMu(mu *sync.Mutex) {
	if mu != nil {
		r.writeMu = mu
	}
}

// SetRemoteBasePath stores the configured remote base path for status display.
func (r *REPL) SetRemoteBasePath(p string) {
	r.remoteBasePath = p
}

// SetJailRoot stores the jailRoot for display in status output.
func (r *REPL) SetJailRoot(root string) {
	r.jailRoot = root
}

// physicalBasePath returns the full server path for the remote base.
func (r *REPL) physicalBasePath() string {
	if r.jailRoot == "" {
		return r.remoteBasePath
	}
	return path.Join(r.jailRoot, r.remoteBasePath)
}

// Run reads commands until quit. Returns when input is exhausted or quit.
// When the OTP prompter is active (otpActive=true), the REPL yields stdin
// and does not consume input lines.
func (r *REPL) Run() {
	r.printf("SmartDeploy ready - type 'h' for help\n")
	for {
		if r.otpActive.Load() {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		select {
		case line, ok := <-r.lineReader.Lines():
			if !ok {
				return
			}
			if r.handle(strings.TrimSpace(line)) {
				return
			}
		case <-time.After(100 * time.Millisecond):
			// Periodically re-check otpActive so the prompter can take over.
		}
	}
}

func (r *REPL) handle(line string) (quit bool) {
	if line == "" {
		r.printStatus()
		return false
	}

	parts := strings.Fields(line)
	cmd := strings.ToLower(parts[0])
	args := parts[1:]

	switch cmd {
	case "h", "help":
		r.printHelp()
	case "s", "status":
		r.printStatus()
	case "w", "watch":
		r.toggleWatch(args)
	case "p", "push":
		r.pushFile(args)
	case "pa", "pushall":
		r.pushAll()
	case "l", "list":
		r.listPending()
	case "u", "up":
		r.repush(args)
	case "rec", "recent":
		r.showRecent()
	case "c", "clear":
		r.deployer.ClearPending()
		r.printf("Pending cleared.\n")
	case "r", "reconnect":
		r.reconnect()
	case "pwd":
		r.showPWD()
	case "ls":
		r.listRemote(args)
	case "stat":
		r.statRemote(args)
	case "otp":
		r.handleOTP(args)
	case "ct", "cleartemp":
		r.clearTemp()
	case "sy", "sync":
		r.syncToWebapp()
	case "q", "quit", "exit":
		r.printf("Bye.\n")
		return true
	default:
		r.printf("Unknown command: %s (type 'h' for help)\n", cmd)
	}
	return false
}

func (r *REPL) printHelp() {
	r.printf(`Commands:
  s, status          Show connection status and pending count
  pwd                Show remote working directory
  ls [path]          List remote directory (default: remote base)
  stat <path>        Show details of a remote file or directory
  w, watch [on|off]  Toggle or set auto-watch mode
  p, push <path>     Upload a specific file
  pa, pushall        Upload all pending files
  l, list            List pending files
  u, up [N]          Re-upload last file (or Nth from recent history)
  rec, recent        Show recent upload history
  c, clear           Clear pending queue
  r, reconnect       Reconnect to server (async, waits for OTP)
  ct, cleartemp      Clear temp directory on server
  sy, sync           Rsync temp directory to webapp root
  otp [code]         Set or show current OTP code
  h, help            Show this help
  q, quit            Exit
`)
}

func (r *REPL) printStatus() {
	status := "disconnected"
	if r.client.IsConnected() {
		status = "connected"
	} else if r.client.IsReconnecting() {
		status = "reconnecting"
	}
	mode := "OFF"
	if r.deployer.IsAutoWatch() {
		mode = "ON"
	}
	line := fmt.Sprintf("Status: %s | AutoWatch: %s | Pending: %d", status, mode, r.deployer.PendingCount())
	if r.otpStore != nil {
		_, _, ok := r.otpStore.Latest()
		if ok {
			line += " | OTP: ready"
		} else {
			line += " | OTP: waiting"
		}
	}
	r.printf("%s\n", line)

	// Show remote paths when connected for clarity.
	if r.client.IsConnected() {
		if r.remoteBasePath != "" {
			if r.jailRoot != "" {
				r.printf("  Remote base: %s (server: %s)\n", r.remoteBasePath, r.physicalBasePath())
			} else {
				r.printf("  Remote base: %s\n", r.remoteBasePath)
			}
		}
		if pwd, err := r.client.RemotePWD(); err == nil {
			r.printf("  Remote PWD:  %s\n", pwd)
		}
	}

	// Show last uploaded file for quick reference.
	if entry, ok := r.deployer.LastUpload(); ok {
		name := filepath.Base(entry.LocalPath)
		status := "OK"
		if !entry.Success {
			status = "FAIL"
		}
		r.printf("  Last upload: %s [%s] (%s)\n", name, status, entry.Time.Format("15:04:05"))
	}
}

func (r *REPL) showPWD() {
	if !r.client.IsConnected() {
		r.printf("Not connected.\n")
		return
	}
	pwd, err := r.client.RemotePWD()
	if err != nil {
		r.printf("[ERR] pwd: %v\n", err)
		return
	}
	r.printf("Remote working directory: %s\n", pwd)
	if r.remoteBasePath != "" {
		if r.jailRoot != "" {
			r.printf("Remote base path: %s (server: %s)\n", r.remoteBasePath, r.physicalBasePath())
		} else {
			r.printf("Remote base path: %s\n", r.remoteBasePath)
		}
	}
}

func (r *REPL) listRemote(args []string) {
	if !r.client.IsConnected() {
		r.printf("Not connected.\n")
		return
	}
	remotePath := r.remoteBasePath
	if len(args) > 0 {
		remotePath = strings.Trim(args[0], `"'`)
	}
	output, err := r.client.ListDir(remotePath)
	if err != nil {
		r.printf("[ERR] ls %s: %v\n", remotePath, err)
		return
	}
	r.printf("%s\n", output)
}

func (r *REPL) statRemote(args []string) {
	if !r.client.IsConnected() {
		r.printf("Not connected.\n")
		return
	}
	if len(args) == 0 {
		r.printf("Usage: stat <remote path>\n")
		return
	}
	remotePath := strings.Trim(args[0], `"'`)
	output, err := r.client.Stat(remotePath)
	if err != nil {
		r.printf("[ERR] stat %s: %v\n", remotePath, err)
		return
	}
	r.printf("%s\n", output)
}

func (r *REPL) toggleWatch(args []string) {
	if len(args) > 0 {
		switch strings.ToLower(args[0]) {
		case "on":
			r.deployer.SetAutoWatch(true)
		case "off":
			r.deployer.SetAutoWatch(false)
		default:
			r.printf("Usage: watch [on|off]\n")
			return
		}
	} else {
		r.deployer.SetAutoWatch(!r.deployer.IsAutoWatch())
	}
	mode := "OFF"
	if r.deployer.IsAutoWatch() {
		mode = "ON"
	}
	r.printf("AutoWatch: %s\n", mode)
}

func (r *REPL) pushFile(args []string) {
	if len(args) == 0 {
		r.printf("Usage: push <file path>\n")
		return
	}
	p := strings.Trim(args[0], `"'`)
	abs, err := filepath.Abs(p)
	if err != nil {
		r.printf("[ERR] resolve path: %v\n", err)
		return
	}
	remotePath, err := r.deployer.UploadFile(abs)
	if err != nil {
		r.printf("[ERR] %v\n", err)
		return
	}
	r.printf("[OK] %s -> %s\n", abs, remotePath)
}

func (r *REPL) pushAll() {
	if r.deployer.PendingCount() == 0 {
		r.printf("No pending files.\n")
		return
	}
	success, failed, _ := r.deployer.UploadAll()
	r.printf("Pushed: %d success, %d failed\n", success, failed)
}

func (r *REPL) listPending() {
	files := r.deployer.PendingFiles()
	if len(files) == 0 {
		r.printf("No pending files.\n")
		return
	}
	r.printf("Pending files (%d):\n", len(files))
	for i, f := range files {
		r.printf("  %d. %s\n", i+1, f)
	}
}

// repush re-uploads the most recent file from upload history.
// With a numeric argument it re-uploads the Nth entry (1-indexed).
func (r *REPL) repush(args []string) {
	history := r.deployer.History()
	if len(history) == 0 {
		r.printf("No recent uploads. Use 'push <path>' to upload a file.\n")
		return
	}
	if len(args) > 0 {
		n, err := strconv.Atoi(args[0])
		if err != nil || n < 1 || n > len(history) {
			r.printf("Invalid number. Use 'u <1-%d>'.\n", len(history))
			return
		}
		entry := history[n-1]
		r.printf("Re-uploading: %s\n", entry.LocalPath)
		remotePath, err := r.deployer.UploadFile(entry.LocalPath)
		if err != nil {
			r.printf("[ERR] %v\n", err)
			return
		}
		r.printf("[OK] %s -> %s\n", entry.LocalPath, remotePath)
		return
	}
	// No argument: re-push the most recent file.
	entry := history[0]
	r.printf("Re-uploading: %s\n", entry.LocalPath)
	remotePath, err := r.deployer.UploadFile(entry.LocalPath)
	if err != nil {
		r.printf("[ERR] %v\n", err)
		return
	}
	r.printf("[OK] %s -> %s\n", entry.LocalPath, remotePath)
}

// showRecent displays the upload history with indices for repush.
func (r *REPL) showRecent() {
	history := r.deployer.History()
	if len(history) == 0 {
		r.printf("No recent uploads.\n")
		return
	}
	r.printf("Recent uploads (%d):\n", len(history))
	for i, e := range history {
		status := "OK"
		if !e.Success {
			status = "FAIL"
		}
		r.printf("  %d. [%s] %s -> %s (%s)\n", i+1, status, e.LocalPath, e.RemotePath, e.Time.Format("15:04:05"))
	}
}

func (r *REPL) reconnect() {
	r.printf("Reconnecting... (copy OTP to clipboard, then press Enter to confirm)\n")
	go func() {
		_ = r.client.Close()
		if err := r.client.Connect(); err != nil {
			r.printf("[ERR] reconnect: %v\n", err)
		} else {
			r.printf("Reconnected.\n")
		}
	}()
}

func (r *REPL) handleOTP(args []string) {
	if r.otpStore == nil {
		r.printf("OTP not configured for this connection.\n")
		return
	}
	if len(args) == 0 {
		_, _, ok := r.otpStore.Latest()
		if ok {
			r.printf("OTP: ready (code available)\n")
		} else {
			r.printf("OTP: none. Usage: otp <code>\n")
		}
		return
	}
	code := strings.Join(args, " ")
	if extracted, ok := extractOTP(code); ok {
		code = extracted
	} else {
		r.printf("Invalid OTP format. Expected 4-8 digits.\n")
		return
	}
	r.otpStore.Set(code)
	r.printf("OTP set: %s\n", code)
}

func (r *REPL) printf(format string, args ...interface{}) {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	fmt.Fprintf(r.writer, format, args...)
}
