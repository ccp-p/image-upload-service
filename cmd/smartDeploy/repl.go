package main

import (
	"bufio"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
)

// REPL provides an interactive command interface for the deployer.
type REPL struct {
	deployer *Deployer
	client   RemoteClient
	otpStore *OTPStore // nil when OTP auth is not configured
	reader   io.Reader
	writer   io.Writer
	writeMu  sync.Mutex
}

func NewREPL(d *Deployer, c RemoteClient, otp *OTPStore, r io.Reader, w io.Writer) *REPL {
	return &REPL{deployer: d, client: c, otpStore: otp, reader: r, writer: w}
}

// Run reads commands until quit. Returns when input is exhausted or quit.
func (r *REPL) Run() {
	r.printf("SmartDeploy ready - type 'h' for help\n")
	scanner := bufio.NewScanner(r.reader)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if r.handle(line) {
			break
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
	case "c", "clear":
		r.deployer.ClearPending()
		r.printf("Pending cleared.\n")
	case "r", "reconnect":
		r.reconnect()
	case "otp":
		r.handleOTP(args)
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
  w, watch [on|off]  Toggle or set auto-watch mode
  p, push <path>     Upload a specific file
  pa, pushall        Upload all pending files
  l, list            List pending files
  c, clear           Clear pending queue
  r, reconnect       Reconnect to server (async, waits for OTP)
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
	if err := r.deployer.UploadFile(abs); err != nil {
		r.printf("[ERR] %v\n", err)
	} else {
		r.printf("[OK] uploaded\n")
	}
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

func (r *REPL) reconnect() {
	r.printf("Reconnecting... (copy OTP to clipboard or type 'otp <code>')\n")
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
