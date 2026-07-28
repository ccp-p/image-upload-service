package main

import (
	"bytes"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestREPL(autoWatch bool) (*REPL, *bytes.Buffer, *Deployer, *mockClient) {
	mc := newMockClient()
	mapper := NewPathMapper("/project/app", "/remote/app", "src/main/webapp/res/wap")
	d := NewDeployer(mc, mapper, autoWatch, log.New(io.Discard, "", 0))
	buf := &bytes.Buffer{}
	r := NewREPL(d, mc, nil, strings.NewReader(""), buf)
	return r, buf, d, mc
}

func runREPL(t *testing.T, input string, autoWatch bool) (*REPL, *bytes.Buffer, *Deployer, *mockClient) {
	t.Helper()
	mc := newMockClient()
	mapper := NewPathMapper("/project/app", "/remote/app", "src/main/webapp/res/wap")
	d := NewDeployer(mc, mapper, autoWatch, log.New(io.Discard, "", 0))
	buf := &bytes.Buffer{}
	r := NewREPL(d, mc, nil, strings.NewReader(input), buf)
	r.Run()
	return r, buf, d, mc
}

func runREPLWithOTP(t *testing.T, input string, autoWatch bool, otp *OTPStore) (*REPL, *bytes.Buffer, *Deployer, *mockClient) {
	t.Helper()
	mc := newMockClient()
	mapper := NewPathMapper("/project/app", "/remote/app", "src/main/webapp/res/wap")
	d := NewDeployer(mc, mapper, autoWatch, log.New(io.Discard, "", 0))
	buf := &bytes.Buffer{}
	r := NewREPL(d, mc, otp, strings.NewReader(input), buf)
	r.Run()
	return r, buf, d, mc
}

func TestREPL_Status(t *testing.T) {
	_, buf, _, _ := runREPL(t, "s\n", true)
	out := buf.String()
	if !strings.Contains(out, "connected") {
		t.Errorf("status output should contain 'connected': %q", out)
	}
	if !strings.Contains(out, "AutoWatch: ON") {
		t.Errorf("status output should contain AutoWatch ON: %q", out)
	}
}

func TestREPL_Status_AutoWatchOff(t *testing.T) {
	_, buf, _, _ := runREPL(t, "status\n", false)
	out := buf.String()
	if !strings.Contains(out, "AutoWatch: OFF") {
		t.Errorf("status should show AutoWatch OFF: %q", out)
	}
}

func TestREPL_Status_Disconnected(t *testing.T) {
	mc := newMockClient()
	mc.connected = false
	mapper := NewPathMapper("/app", "/remote", "")
	d := NewDeployer(mc, mapper, true, log.New(io.Discard, "", 0))
	buf := &bytes.Buffer{}
	r := NewREPL(d, mc, nil, strings.NewReader("s\n"), buf)
	r.Run()
	if !strings.Contains(buf.String(), "disconnected") {
		t.Errorf("status should show disconnected")
	}
}

func TestREPL_Help(t *testing.T) {
	_, buf, _, _ := runREPL(t, "h\n", true)
	out := buf.String()
	for _, cmd := range []string{"status", "watch", "push", "pushall", "list", "reconnect", "quit", "otp"} {
		if !strings.Contains(out, cmd) {
			t.Errorf("help output should mention %q: %q", cmd, out)
		}
	}
}

func TestREPL_UnknownCommand(t *testing.T) {
	_, buf, _, _ := runREPL(t, "xyz\n", true)
	out := buf.String()
	if !strings.Contains(out, "Unknown command") {
		t.Errorf("should report unknown command: %q", out)
	}
}

func TestREPL_Quit(t *testing.T) {
	_, buf, _, _ := runREPL(t, "q\n", true)
	out := buf.String()
	if !strings.Contains(out, "Bye") {
		t.Errorf("quit should print Bye: %q", out)
	}
}

func TestREPL_QuitAlias(t *testing.T) {
	_, buf, _, _ := runREPL(t, "quit\n", true)
	if !strings.Contains(buf.String(), "Bye") {
		t.Errorf("quit alias should print Bye")
	}
}

func TestREPL_ExitAlias(t *testing.T) {
	_, buf, _, _ := runREPL(t, "exit\n", true)
	if !strings.Contains(buf.String(), "Bye") {
		t.Errorf("exit alias should print Bye")
	}
}

func TestREPL_EmptyLine(t *testing.T) {
	_, buf, _, _ := runREPL(t, "\n", true)
	out := buf.String()
	if !strings.Contains(out, "Status:") {
		t.Errorf("empty line should show status: %q", out)
	}
}

func TestREPL_PushFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "test.css")
	os.WriteFile(f, []byte("body{}"), 0644)

	mc := newMockClient()
	mapper := NewPathMapper(dir, "/remote", "")
	d := NewDeployer(mc, mapper, false, log.New(io.Discard, "", 0))
	buf := &bytes.Buffer{}
	r := NewREPL(d, mc, nil, strings.NewReader("push "+f+"\n"), buf)
	r.Run()

	if !strings.Contains(buf.String(), "OK") {
		t.Errorf("push should report OK: %q", buf.String())
	}
	uploads := mc.getUploads()
	if len(uploads) != 1 {
		t.Fatalf("uploads = %d, want 1", len(uploads))
	}
}

func TestREPL_PushFile_NoArg(t *testing.T) {
	_, buf, _, _ := runREPL(t, "push\n", true)
	if !strings.Contains(buf.String(), "Usage:") {
		t.Errorf("push with no arg should show usage: %q", buf.String())
	}
}

func TestREPL_PushAll_Empty(t *testing.T) {
	_, buf, _, _ := runREPL(t, "pa\n", false)
	if !strings.Contains(buf.String(), "No pending") {
		t.Errorf("pushall with no pending should say so: %q", buf.String())
	}
}

func TestREPL_PushAll_WithFiles(t *testing.T) {
	dir := t.TempDir()
	f1 := filepath.Join(dir, "a.css")
	f2 := filepath.Join(dir, "b.css")
	os.WriteFile(f1, []byte("a"), 0644)
	os.WriteFile(f2, []byte("b"), 0644)

	mc := newMockClient()
	mapper := NewPathMapper(dir, "/remote", "")
	d := NewDeployer(mc, mapper, false, log.New(io.Discard, "", 0))
	d.tracker.Add(f1)
	d.tracker.Add(f2)

	buf := &bytes.Buffer{}
	r := NewREPL(d, mc, nil, strings.NewReader("pushall\n"), buf)
	r.Run()

	out := buf.String()
	if !strings.Contains(out, "2 success") {
		t.Errorf("pushall should report 2 success: %q", out)
	}
}

func TestREPL_ListPending_Empty(t *testing.T) {
	_, buf, _, _ := runREPL(t, "l\n", false)
	if !strings.Contains(buf.String(), "No pending") {
		t.Errorf("list with no pending should say so: %q", buf.String())
	}
}

func TestREPL_ListPending_WithFiles(t *testing.T) {
	mc := newMockClient()
	mapper := NewPathMapper("/app", "/remote", "")
	d := NewDeployer(mc, mapper, false, log.New(io.Discard, "", 0))
	d.tracker.Add("/app/a.css")
	d.tracker.Add("/app/b.css")

	buf := &bytes.Buffer{}
	r := NewREPL(d, mc, nil, strings.NewReader("list\n"), buf)
	r.Run()

	out := buf.String()
	if !strings.Contains(out, "Pending files (2)") {
		t.Errorf("list should show count 2: %q", out)
	}
	if !strings.Contains(out, "a.css") || !strings.Contains(out, "b.css") {
		t.Errorf("list should show both files: %q", out)
	}
}

func TestREPL_WatchToggle(t *testing.T) {
	_, buf, d, _ := runREPL(t, "w\n", true)
	if d.IsAutoWatch() {
		t.Error("watch toggle should turn off autoWatch")
	}
	if !strings.Contains(buf.String(), "AutoWatch: OFF") {
		t.Errorf("should show OFF: %q", buf.String())
	}

	// toggle back on
	mc2 := newMockClient()
	mapper2 := NewPathMapper("/app", "/remote", "")
	d2 := NewDeployer(mc2, mapper2, false, log.New(io.Discard, "", 0))
	buf2 := &bytes.Buffer{}
	r2 := NewREPL(d2, mc2, nil, strings.NewReader("w\n"), buf2)
	r2.Run()
	if !d2.IsAutoWatch() {
		t.Error("watch toggle should turn on autoWatch")
	}
}

func TestREPL_WatchExplicitOn(t *testing.T) {
	_, buf, d, _ := runREPL(t, "watch on\n", false)
	if !d.IsAutoWatch() {
		t.Error("watch on should enable autoWatch")
	}
	if !strings.Contains(buf.String(), "ON") {
		t.Errorf("should show ON: %q", buf.String())
	}
}

func TestREPL_WatchExplicitOff(t *testing.T) {
	_, buf, d, _ := runREPL(t, "watch off\n", true)
	if d.IsAutoWatch() {
		t.Error("watch off should disable autoWatch")
	}
	if !strings.Contains(buf.String(), "OFF") {
		t.Errorf("should show OFF: %q", buf.String())
	}
}

func TestREPL_WatchInvalidArg(t *testing.T) {
	_, buf, _, _ := runREPL(t, "watch maybe\n", true)
	if !strings.Contains(buf.String(), "Usage:") {
		t.Errorf("invalid watch arg should show usage: %q", buf.String())
	}
}

func TestREPL_Reconnect(t *testing.T) {
	_, buf, _, mc := runREPL(t, "r\n", true)
	out := buf.String()
	if !strings.Contains(out, "Reconnecting") {
		t.Errorf("reconnect should say Reconnecting: %q", out)
	}
	// The reconnect is async; give it a moment
	time.Sleep(100 * time.Millisecond)
	if !mc.IsConnected() {
		t.Error("should be connected after reconnect")
	}
}

func TestREPL_Reconnect_Failure(t *testing.T) {
	mc := newMockClient()
	mc.connectErr = io.EOF
	mapper := NewPathMapper("/app", "/remote", "")
	d := NewDeployer(mc, mapper, true, log.New(io.Discard, "", 0))
	buf := &bytes.Buffer{}
	r := NewREPL(d, mc, nil, strings.NewReader("reconnect\n"), buf)
	r.Run()
	// Reconnect is async — wait for the goroutine to write the error
	time.Sleep(100 * time.Millisecond)
	out := buf.String()
	if !strings.Contains(out, "ERR") {
		t.Errorf("reconnect failure should show ERR: %q", out)
	}
}

func TestREPL_Clear(t *testing.T) {
	mc := newMockClient()
	mapper := NewPathMapper("/app", "/remote", "")
	d := NewDeployer(mc, mapper, false, log.New(io.Discard, "", 0))
	d.tracker.Add("/app/a.css")

	buf := &bytes.Buffer{}
	r := NewREPL(d, mc, nil, strings.NewReader("c\n"), buf)
	r.Run()

	if !strings.Contains(buf.String(), "cleared") {
		t.Errorf("clear should say cleared: %q", buf.String())
	}
	if d.PendingCount() != 0 {
		t.Errorf("pending after clear = %d, want 0", d.PendingCount())
	}
}

func TestREPL_MultipleCommands(t *testing.T) {
	_, buf, d, _ := runREPL(t, "s\nl\nw\ns\nq\n", true)
	out := buf.String()
	if !strings.Contains(out, "Bye") {
		t.Error("should end with Bye")
	}
	if d.IsAutoWatch() {
		t.Error("autoWatch should be toggled off then status checked")
	}
}

func TestREPL_CommandAliases(t *testing.T) {
	aliases := []string{"s", "h", "l", "q"}
	for _, a := range aliases {
		t.Run(a, func(t *testing.T) {
			_, buf, _, _ := runREPL(t, a+"\n", true)
			if strings.Contains(buf.String(), "Unknown command") {
				t.Errorf("alias %q should not be unknown: %q", a, buf.String())
			}
		})
	}
}

// --- OTP command tests ---

func TestREPL_OTP_SetCode(t *testing.T) {
	otp := NewOTPStore(nil)
	_, buf, _, _ := runREPLWithOTP(t, "otp 654321\n", true, otp)

	if !strings.Contains(buf.String(), "OTP set: 654321") {
		t.Errorf("otp command should confirm: %q", buf.String())
	}
	code, _, ok := otp.Latest()
	if !ok || code != "654321" {
		t.Errorf("OTP store should have 654321, got %q ok=%v", code, ok)
	}
}

func TestREPL_OTP_SetEmbeddedCode(t *testing.T) {
	otp := NewOTPStore(nil)
	_, buf, _, _ := runREPLWithOTP(t, "otp your code is 123456\n", true, otp)

	if !strings.Contains(buf.String(), "OTP set: 123456") {
		t.Errorf("otp command should extract and set 123456: %q", buf.String())
	}
}

func TestREPL_OTP_InvalidFormat(t *testing.T) {
	otp := NewOTPStore(nil)
	_, buf, _, _ := runREPLWithOTP(t, "otp abcdef\n", true, otp)

	if !strings.Contains(buf.String(), "Invalid OTP format") {
		t.Errorf("should reject non-numeric OTP: %q", buf.String())
	}
	_, _, ok := otp.Latest()
	if ok {
		t.Error("invalid OTP should not be stored")
	}
}

func TestREPL_OTP_NoArg_NoValue(t *testing.T) {
	otp := NewOTPStore(nil)
	_, buf, _, _ := runREPLWithOTP(t, "otp\n", true, otp)

	if !strings.Contains(buf.String(), "none") {
		t.Errorf("otp with no arg and no value should say none: %q", buf.String())
	}
}

func TestREPL_OTP_NoArg_HasValue(t *testing.T) {
	otp := NewOTPStore(nil)
	otp.Set("789456")
	_, buf, _, _ := runREPLWithOTP(t, "otp\n", true, otp)

	if !strings.Contains(buf.String(), "ready") {
		t.Errorf("otp with existing value should say ready: %q", buf.String())
	}
}

func TestREPL_OTP_NotConfigured(t *testing.T) {
	// When otpStore is nil, the otp command should say so
	_, buf, _, _ := runREPL(t, "otp 123456\n", true)
	if !strings.Contains(buf.String(), "OTP not configured") {
		t.Errorf("otp command without store should say not configured: %q", buf.String())
	}
}

func TestREPL_Status_ShowsOTPStatus(t *testing.T) {
	otp := NewOTPStore(nil)
	otp.Set("111222")
	_, buf, _, _ := runREPLWithOTP(t, "s\n", true, otp)
	if !strings.Contains(buf.String(), "OTP: ready") {
		t.Errorf("status should show OTP ready: %q", buf.String())
	}
}

func TestREPL_Status_ShowsOTPWaiting(t *testing.T) {
	otp := NewOTPStore(nil)
	_, buf, _, _ := runREPLWithOTP(t, "s\n", true, otp)
	if !strings.Contains(buf.String(), "OTP: waiting") {
		t.Errorf("status should show OTP waiting: %q", buf.String())
	}
}

func TestREPL_Status_NoOTPWhenNotConfigured(t *testing.T) {
	_, buf, _, _ := runREPL(t, "s\n", true)
	if strings.Contains(buf.String(), "OTP:") {
		t.Errorf("status should not show OTP line when not configured: %q", buf.String())
	}
}

// --- helpers ---
