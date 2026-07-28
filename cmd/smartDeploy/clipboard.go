package main

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// ClipboardReader reads the current text content of the system clipboard.
type ClipboardReader interface {
	Read() (string, error)
}

// commandClipboardReader reads the clipboard via platform-specific commands.
type commandClipboardReader struct{}

// NewClipboardReader returns a ClipboardReader appropriate for the current OS.
func NewClipboardReader() ClipboardReader {
	return &commandClipboardReader{}
}

func (r *commandClipboardReader) Read() (string, error) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", "Get-Clipboard")
	case "darwin":
		cmd = exec.Command("pbpaste")
	default:
		cmd = exec.Command("xclip", "-selection", "clipboard", "-o")
	}
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("read clipboard: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// ClipboardWatcher polls the clipboard at a fixed interval. When the content
// changes and looks like an OTP, it stores the value in the provided OTPStore.
type ClipboardWatcher struct {
	reader   ClipboardReader
	store    *OTPStore
	interval time.Duration

	mu     sync.Mutex
	last   string
	stopCh chan struct{}
	done   chan struct{}
}

// NewClipboardWatcher creates a watcher that polls every interval.
func NewClipboardWatcher(reader ClipboardReader, store *OTPStore, interval time.Duration) *ClipboardWatcher {
	return &ClipboardWatcher{
		reader:   reader,
		store:    store,
		interval: interval,
		stopCh:   make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// Start launches the polling goroutine.
func (w *ClipboardWatcher) Start() {
	go w.run()
}

func (w *ClipboardWatcher) run() {
	defer close(w.done)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-w.stopCh:
			return
		case <-ticker.C:
			w.poll()
		}
	}
}

// poll reads the clipboard once and stores the value if it is a new OTP.
func (w *ClipboardWatcher) poll() {
	content, err := w.reader.Read()
	if err != nil {
		return
	}

	w.mu.Lock()
	if content == w.last {
		w.mu.Unlock()
		return
	}
	w.last = content
	w.mu.Unlock()

	if otp, ok := extractOTP(content); ok {
		w.store.Set(otp)
	}
}

// Close stops the polling goroutine and waits for it to finish.
func (w *ClipboardWatcher) Close() {
	select {
	case <-w.stopCh:
		// already closed
	default:
		close(w.stopCh)
	}
	<-w.done
}

// LastRead returns the last clipboard content observed by the watcher (for
// diagnostics). Returns "" before the first successful poll.
func (w *ClipboardWatcher) LastRead() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.last
}
