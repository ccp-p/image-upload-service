package main

import (
	"context"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestPrompter(clip ClipboardReader, lr *SharedLineReader) (*interactiveOTPPrompter, *bytesSafeBuffer) {
	buf := &bytesSafeBuffer{}
	active := new(atomic.Bool)
	mu := &sync.Mutex{}
	p := NewInteractiveOTPPrompter(lr, buf, clip, active, mu)
	p.SetPollInterval(20 * time.Millisecond)
	return p, buf
}

// bytesSafeBuffer is a thread-safe bytes.Buffer for concurrent writes.
type bytesSafeBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *bytesSafeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *bytesSafeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestOTPPrompter_ClipboardOTP_EnterConfirms(t *testing.T) {
	clip := newMockClipboardReader("123456")
	lr := NewSharedLineReader(strings.NewReader("\n"))
	defer lr.Close()

	p, buf := newTestPrompter(clip, lr)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	otp, err := p.PromptOTP(ctx)
	if err != nil {
		t.Fatalf("PromptOTP error: %v", err)
	}
	if otp != "123456" {
		t.Errorf("otp = %q, want '123456'", otp)
	}
	if !strings.Contains(buf.String(), "Clipboard OTP: 123456") {
		t.Errorf("output should show clipboard OTP: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "Confirmed: 123456") {
		t.Errorf("output should show confirmation: %q", buf.String())
	}
}

func TestOTPPrompter_EmbeddedClipboardOTP(t *testing.T) {
	clip := newMockClipboardReader("Your verification code is 998877.")
	lr := NewSharedLineReader(strings.NewReader("\n"))
	defer lr.Close()

	p, _ := newTestPrompter(clip, lr)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	otp, err := p.PromptOTP(ctx)
	if err != nil {
		t.Fatalf("PromptOTP error: %v", err)
	}
	if otp != "998877" {
		t.Errorf("otp = %q, want '998877'", otp)
	}
}

func TestOTPPrompter_UserTypesCode(t *testing.T) {
	clip := newMockClipboardReader("no code here")
	lr := NewSharedLineReader(strings.NewReader("456789\n"))
	defer lr.Close()

	p, buf := newTestPrompter(clip, lr)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	otp, err := p.PromptOTP(ctx)
	if err != nil {
		t.Fatalf("PromptOTP error: %v", err)
	}
	if otp != "456789" {
		t.Errorf("otp = %q, want '456789'", otp)
	}
	if !strings.Contains(buf.String(), "Confirmed: 456789") {
		t.Errorf("output should show confirmation: %q", buf.String())
	}
}

func TestOTPPrompter_InvalidTypedCode_Retries(t *testing.T) {
	clip := newMockClipboardReader("")
	lr := NewSharedLineReader(strings.NewReader("abc\n789012\n"))
	defer lr.Close()

	p, buf := newTestPrompter(clip, lr)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	otp, err := p.PromptOTP(ctx)
	if err != nil {
		t.Fatalf("PromptOTP error: %v", err)
	}
	if otp != "789012" {
		t.Errorf("otp = %q, want '789012'", otp)
	}
	if !strings.Contains(buf.String(), "not a valid code") {
		t.Errorf("output should show invalid code message: %q", buf.String())
	}
}

func TestOTPPrompter_EnterWithNoClipboard(t *testing.T) {
	clip := newMockClipboardReader("")
	lr := NewSharedLineReader(strings.NewReader("\n654321\n"))
	defer lr.Close()

	p, buf := newTestPrompter(clip, lr)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	otp, err := p.PromptOTP(ctx)
	if err != nil {
		t.Fatalf("PromptOTP error: %v", err)
	}
	if otp != "654321" {
		t.Errorf("otp = %q, want '654321'", otp)
	}
	if !strings.Contains(buf.String(), "No code in clipboard") {
		t.Errorf("output should say no code in clipboard: %q", buf.String())
	}
}

func TestOTPPrompter_ContextCancelled(t *testing.T) {
	clip := newMockClipboardReader("")
	lr := NewSharedLineReader(strings.NewReader(""))
	defer lr.Close()

	p, _ := newTestPrompter(clip, lr)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := p.PromptOTP(ctx)
	if err == nil {
		t.Error("expected error on context cancellation")
	}
}

func TestOTPPrompter_ClipboardUpdates(t *testing.T) {
	clip := newMockClipboardReader("111111")
	// Use a pipe so we can control when Enter is pressed.
	pr, pw := io.Pipe()
	lr := NewSharedLineReader(pr)
	defer lr.Close()
	defer pw.Close()

	p, buf := newTestPrompter(clip, lr)

	// Change the clipboard after a short delay.
	go func() {
		time.Sleep(100 * time.Millisecond)
		clip.set("222222")
		time.Sleep(100 * time.Millisecond)
		pw.Write([]byte("\n"))
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	otp, err := p.PromptOTP(ctx)
	if err != nil {
		t.Fatalf("PromptOTP error: %v", err)
	}
	if otp != "222222" {
		t.Errorf("otp = %q, want '222222' (latest clipboard)", otp)
	}
	// Should have displayed both the old and new clipboard OTPs.
	out := buf.String()
	if !strings.Contains(out, "Clipboard OTP: 111111") {
		t.Errorf("should show initial clipboard OTP: %q", out)
	}
	if !strings.Contains(out, "Clipboard OTP: 222222") {
		t.Errorf("should show updated clipboard OTP: %q", out)
	}
}

func TestOTPPrompter_SetsActiveFlag(t *testing.T) {
	clip := newMockClipboardReader("123456")
	// Use a pipe so Enter is not available until we write it, keeping
	// PromptOTP blocked (and active=true) until we are ready.
	pr, pw := io.Pipe()
	lr := NewSharedLineReader(pr)
	defer lr.Close()
	defer pw.Close()

	buf := &bytesSafeBuffer{}
	active := new(atomic.Bool)
	mu := &sync.Mutex{}
	p := NewInteractiveOTPPrompter(lr, buf, clip, active, mu)
	p.SetPollInterval(20 * time.Millisecond)

	if active.Load() {
		t.Error("active should be false before PromptOTP")
	}

	done := make(chan struct{})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = p.PromptOTP(ctx)
		close(done)
	}()

	// Wait for active to be set.
	deadline := time.After(1 * time.Second)
	for !active.Load() {
		select {
		case <-deadline:
			t.Fatal("active flag was never set")
		case <-time.After(10 * time.Millisecond):
		}
	}

	// Now send Enter so PromptOTP can complete.
	pw.Write([]byte("\n"))

	<-done

	if active.Load() {
		t.Error("active should be false after PromptOTP returns")
	}
}

func TestNoopOTPPrompter(t *testing.T) {
	n := newNoopOTPPrompter("999888")
	otp, err := n.PromptOTP(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if otp != "999888" {
		t.Errorf("otp = %q, want '999888'", otp)
	}
}

func TestNoopOTPPrompter_Error(t *testing.T) {
	n := newNoopOTPPrompter("")
	n.err = io.EOF
	_, err := n.PromptOTP(context.Background())
	if err == nil {
		t.Error("expected error")
	}
}
