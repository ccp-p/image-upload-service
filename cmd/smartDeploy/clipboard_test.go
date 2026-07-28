package main

import (
	"sync"
	"testing"
	"time"
)

var errReadFail = errReadFailErr{}

type errReadFailErr struct{}

func (e errReadFailErr) Error() string { return "simulated clipboard read error" }

func (e errReadFailErr) Unwrap() error { return nil }

// mockClipboardReader is a controllable ClipboardReader for tests.
type mockClipboardReader struct {
	mu      sync.Mutex
	content string
	err     error
}

func (m *mockClipboardReader) Read() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return "", m.err
	}
	return m.content, nil
}

func (m *mockClipboardReader) set(content string) {
	m.mu.Lock()
	m.content = content
	m.mu.Unlock()
}

func newMockClipboardReader(content string) *mockClipboardReader {
	return &mockClipboardReader{content: content}
}

// --- ClipboardWatcher tests ---

func TestClipboardWatcher_DetectsOTP(t *testing.T) {
	reader := newMockClipboardReader("nothing")
	store := NewOTPStore(nil)
	w := NewClipboardWatcher(reader, store, 20*time.Millisecond)
	w.Start()
	defer w.Close()

	// Change clipboard to an OTP
	reader.set("482917")

	// Wait for the watcher to poll and store
	deadline := time.After(2 * time.Second)
	for {
		otp, _, ok := store.Latest()
		if ok && otp == "482917" {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("OTP not detected. Latest: ok=%v", ok)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestClipboardWatcher_DetectsEmbeddedOTP(t *testing.T) {
	reader := newMockClipboardReader("")
	store := NewOTPStore(nil)
	w := NewClipboardWatcher(reader, store, 20*time.Millisecond)
	w.Start()
	defer w.Close()

	reader.set("Your code is 730192. Do not share it.")

	deadline := time.After(2 * time.Second)
	for {
		otp, _, ok := store.Latest()
		if ok && otp == "730192" {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("embedded OTP not detected. ok=%v", ok)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestClipboardWatcher_IgnoresNonOTP(t *testing.T) {
	reader := newMockClipboardReader("")
	store := NewOTPStore(nil)
	w := NewClipboardWatcher(reader, store, 20*time.Millisecond)
	w.Start()
	defer w.Close()

	reader.set("hello world this is not an otp")

	time.Sleep(200 * time.Millisecond)

	_, _, ok := store.Latest()
	if ok {
		t.Error("non-OTP clipboard content should not be stored")
	}
}

func TestClipboardWatcher_DeduplicatesSameContent(t *testing.T) {
	reader := newMockClipboardReader("555444")
	store := NewOTPStore(nil)
	store.Set("555444") // pre-existing
	store.Clear()       // clear so Latest returns false

	w := NewClipboardWatcher(reader, store, 20*time.Millisecond)
	w.Start()

	// The initial poll will detect "555444" and store it.
	// Subsequent polls should not re-store the same content.
	time.Sleep(200 * time.Millisecond)

	otp, _, ok := store.Latest()
	if !ok || otp != "555444" {
		t.Errorf("should have stored the OTP once: %q, ok=%v", otp, ok)
	}

	// Change to a different OTP
	reader.set("666777")
	deadline := time.After(2 * time.Second)
	for {
		otp, _, ok := store.Latest()
		if ok && otp == "666777" {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("second OTP not detected")
		case <-time.After(10 * time.Millisecond):
		}
	}

	w.Close()
}

func TestClipboardWatcher_CloseStopsPolling(t *testing.T) {
	reader := newMockClipboardReader("init")
	store := NewOTPStore(nil)
	w := NewClipboardWatcher(reader, store, 20*time.Millisecond)
	w.Start()

	time.Sleep(50 * time.Millisecond)
	w.Close()

	// After Close, changing the clipboard should not produce new OTPs
	reader.set("123456")
	time.Sleep(200 * time.Millisecond)

	_, _, ok := store.Latest()
	if ok {
		t.Error("watcher should not poll after Close")
	}
}

func TestClipboardWatcher_CloseIsIdempotent(t *testing.T) {
	reader := newMockClipboardReader("")
	store := NewOTPStore(nil)
	w := NewClipboardWatcher(reader, store, 20*time.Millisecond)
	w.Start()
	w.Close()

	// Should not panic
	w.Close()
}

func TestClipboardWatcher_LastRead(t *testing.T) {
	reader := newMockClipboardReader("123456")
	store := NewOTPStore(nil)
	w := NewClipboardWatcher(reader, store, 20*time.Millisecond)
	w.Start()

	deadline := time.After(2 * time.Second)
	for {
		last := w.LastRead()
		if last == "123456" {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("LastRead never updated. got %q", w.LastRead())
		case <-time.After(10 * time.Millisecond):
		}
	}

	w.Close()
}

func TestClipboardWatcher_HandlesReadError(t *testing.T) {
	reader := &mockClipboardReader{err: errReadFail}
	store := NewOTPStore(nil)
	w := NewClipboardWatcher(reader, store, 20*time.Millisecond)
	w.Start()
	defer w.Close()

	time.Sleep(200 * time.Millisecond)
	_, _, ok := store.Latest()
	if ok {
		t.Error("read error should not produce an OTP")
	}
}

func TestClipboardWatcher_FastPollInterval(t *testing.T) {
	reader := newMockClipboardReader("")
	store := NewOTPStore(nil)
	// Very fast poll to ensure the loop handles small intervals
	w := NewClipboardWatcher(reader, store, 5*time.Millisecond)
	w.Start()

	reader.set("876543")

	deadline := time.After(2 * time.Second)
	for {
		otp, _, ok := store.Latest()
		if ok && otp == "876543" {
			break
		}
		select {
		case <-deadline:
			t.Fatal("fast poll did not detect OTP")
		case <-time.After(5 * time.Millisecond):
		}
	}

	w.Close()
}
