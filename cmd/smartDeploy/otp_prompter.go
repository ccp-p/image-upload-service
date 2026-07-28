package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// OTPPrompter interactively asks the user to confirm an OTP code before it is
// sent to the SSH server. The implementation polls the clipboard in real time
// and displays the latest code, then waits for the user to press Enter (or
// type a code manually) before returning.
type OTPPrompter interface {
	PromptOTP(ctx context.Context) (string, error)
}

// interactiveOTPPrompter implements OTPPrompter using a SharedLineReader for
// stdin and a ClipboardReader for clipboard polling.
type interactiveOTPPrompter struct {
	reader       *SharedLineReader
	writer       io.Writer
	clip         ClipboardReader
	pollInterval time.Duration
	active       *atomic.Bool // shared with REPL — when true the REPL yields stdin
	writeMu      *sync.Mutex  // shared with REPL for interleaving-safe output

	// autoConfirmFirstOTP: when true, the first PromptOTP call that finds
	// a valid clipboard code sends it immediately without waiting for
	// Enter. This lets SmartDeploy auto-authenticate on startup if the
	// user already has a fresh OTP in their clipboard. After the first
	// use, it reverts to interactive confirmation (codes expire).
	autoConfirmFirstOTP bool
}

// NewInteractiveOTPPrompter creates a prompter that polls the clipboard every
// pollInterval and reads confirmation from the shared line reader.
func NewInteractiveOTPPrompter(
	reader *SharedLineReader,
	writer io.Writer,
	clip ClipboardReader,
	active *atomic.Bool,
	writeMu *sync.Mutex,
) *interactiveOTPPrompter {
	if active == nil {
		active = new(atomic.Bool)
	}
	if writeMu == nil {
		writeMu = &sync.Mutex{}
	}
	return &interactiveOTPPrompter{
		reader:              reader,
		writer:              writer,
		clip:                clip,
		pollInterval:        500 * time.Millisecond,
		active:              active,
		writeMu:             writeMu,
		autoConfirmFirstOTP: false,
	}
}

// SetPollInterval overrides the clipboard poll interval (mainly for tests).
func (p *interactiveOTPPrompter) SetPollInterval(d time.Duration) {
	if d > 0 {
		p.pollInterval = d
	}
}

// SetAutoConfirmFirstOTP enables auto-send on the first OTP prompt: if the
// clipboard already contains a valid code when the SSH server requests
// authentication, it is sent immediately without waiting for Enter.
func (p *interactiveOTPPrompter) SetAutoConfirmFirstOTP(on bool) {
	p.autoConfirmFirstOTP = on
}

// PromptOTP displays the current clipboard OTP in real time and waits for the
// user to press Enter to confirm (or type a code). Returns the confirmed OTP.
func (p *interactiveOTPPrompter) PromptOTP(ctx context.Context) (string, error) {
	p.active.Store(true)
	defer p.active.Store(false)

	// If auto-confirm is enabled for this call, do a quick clipboard poll.
	// If a valid OTP is found, send it immediately without user interaction.
	if p.autoConfirmFirstOTP {
		p.autoConfirmFirstOTP = false // one-shot: only the first call auto-confirms
		if p.clip != nil {
			content, err := p.clip.Read()
			if err == nil {
				if otp, ok := extractOTP(content); ok {
					p.printf("[OTP] Auto-confirmed from clipboard: %s\n", otp)
					return otp, nil
				}
			}
		}
		// No clipboard code found -- fall through to interactive mode.
	}

	p.printf("\n")
	p.printf("========================================\n")
	p.printf("  OTP REQUIRED - press Enter to confirm\n")
	p.printf("  Copy a fresh code to clipboard first,\n")
	p.printf("  or type the code and press Enter.\n")
	p.printf("========================================\n")

	var lastOTP string
	ticker := time.NewTicker(p.pollInterval)
	defer ticker.Stop()

	// Initial clipboard poll so the user sees a code immediately if one exists.
	p.pollAndDisplay(&lastOTP)

	for {
		select {
		case <-ctx.Done():
			p.printf("\n[OTP] Prompt cancelled.\n")
			return "", ctx.Err()
		case <-ticker.C:
			p.pollAndDisplay(&lastOTP)
		case line, ok := <-p.reader.Lines():
			if !ok {
				return "", fmt.Errorf("input closed during OTP prompt")
			}
			line = strings.TrimSpace(line)
			if line == "" {
				// Enter pressed — use the clipboard OTP.
				if lastOTP != "" {
					p.printf("[OTP] Confirmed: %s\n", lastOTP)
					return lastOTP, nil
				}
				p.printf("[OTP] No code in clipboard. Copy one or type a code.\n")
				continue
			}
			// User typed a code — validate and use it.
			if otp, ok := extractOTP(line); ok {
				p.printf("[OTP] Confirmed: %s\n", otp)
				return otp, nil
			}
			p.printf("[OTP] '%s' is not a valid code (4-8 digits). Try again.\n", line)
		}
	}
}

// pollAndDisplay reads the clipboard and prints the OTP if it changed.
func (p *interactiveOTPPrompter) pollAndDisplay(lastOTP *string) {
	if p.clip == nil {
		return
	}
	content, err := p.clip.Read()
	if err != nil {
		return
	}
	otp, ok := extractOTP(content)
	if !ok {
		return
	}
	if otp != *lastOTP {
		*lastOTP = otp
		p.printf("  Clipboard OTP: %s  (press Enter to use)\n", otp)
	}
}

func (p *interactiveOTPPrompter) printf(format string, args ...interface{}) {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	fmt.Fprintf(p.writer, format, args...)
}

// noopOTPPrompter returns a fixed OTP without any user interaction. Used in
// tests where interactive prompting is not needed.
type noopOTPPrompter struct {
	code string
	err  error
}

func newNoopOTPPrompter(code string) *noopOTPPrompter {
	return &noopOTPPrompter{code: code}
}

func (n *noopOTPPrompter) PromptOTP(ctx context.Context) (string, error) {
	if n.err != nil {
		return "", n.err
	}
	return n.code, nil
}
