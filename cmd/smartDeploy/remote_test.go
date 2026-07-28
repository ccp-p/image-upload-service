package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// --- shellQuote tests ---

func TestShellQuote_SimplePath(t *testing.T) {
	got := shellQuote("/path/to/file.css")
	want := "'/path/to/file.css'"
	if got != want {
		t.Errorf("shellQuote = %q, want %q", got, want)
	}
}

func TestShellQuote_WithSpace(t *testing.T) {
	got := shellQuote("/path/to/my file.css")
	want := "'/path/to/my file.css'"
	if got != want {
		t.Errorf("shellQuote = %q, want %q", got, want)
	}
}

func TestShellQuote_WithSingleQuote(t *testing.T) {
	got := shellQuote("/path/to/file's name.css")
	// Should escape the single quote: '/path/to/file'\''s name.css'
	if !strings.HasPrefix(got, "'") || !strings.HasSuffix(got, "'") {
		t.Errorf("shellQuote should be wrapped in single quotes: %q", got)
	}
	// Verify it can be safely used: unquoting should give back the original
	if !strings.Contains(got, "'\\''") {
		t.Errorf("shellQuote should escape single quotes: %q", got)
	}
}

func TestShellQuote_EmptyString(t *testing.T) {
	got := shellQuote("")
	want := "''"
	if got != want {
		t.Errorf("shellQuote(\"\") = %q, want %q", got, want)
	}
}

// --- sshClient buildConfig tests ---

func TestSSHClient_BuildConfig_PasswordOnly(t *testing.T) {
	c := NewSSHClient("host", 22, "user", "pass", "", "", 0)
	cfg, err := c.buildConfig()
	if err != nil {
		t.Fatalf("buildConfig error: %v", err)
	}
	if cfg.User != "user" {
		t.Errorf("user = %q", cfg.User)
	}
	if len(cfg.Auth) != 1 {
		t.Errorf("auth methods = %d, want 1", len(cfg.Auth))
	}
}

func TestSSHClient_BuildConfig_KeyOnly(t *testing.T) {
	// Generate a test key
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	keyPath := filepath.Join(t.TempDir(), "test_key")
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		t.Fatal(err)
	}

	c := NewSSHClient("host", 22, "user", "", keyPath, "", 0)
	cfg, err := c.buildConfig()
	if err != nil {
		t.Fatalf("buildConfig error: %v", err)
	}
	if len(cfg.Auth) != 1 {
		t.Errorf("auth methods = %d, want 1", len(cfg.Auth))
	}
}

func TestSSHClient_BuildConfig_BothAuth(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	keyPath := filepath.Join(t.TempDir(), "test_key")
	os.WriteFile(keyPath, keyPEM, 0600)

	c := NewSSHClient("host", 22, "user", "pass", keyPath, "", 0)
	cfg, err := c.buildConfig()
	if err != nil {
		t.Fatalf("buildConfig error: %v", err)
	}
	if len(cfg.Auth) != 2 {
		t.Errorf("auth methods = %d, want 2", len(cfg.Auth))
	}
}

func TestSSHClient_BuildConfig_NoAuth(t *testing.T) {
	c := NewSSHClient("host", 22, "user", "", "", "", 0)
	_, err := c.buildConfig()
	if err == nil {
		t.Error("expected error when no auth method provided")
	}
}

func TestSSHClient_BuildConfig_BadKeyPath(t *testing.T) {
	c := NewSSHClient("host", 22, "user", "", "/nonexistent/key/path", "", 0)
	_, err := c.buildConfig()
	if err == nil {
		t.Error("expected error for bad key path")
	}
}

func TestSSHClient_IsConnected(t *testing.T) {
	c := NewSSHClient("host", 22, "user", "pass", "", "", 0)
	if c.IsConnected() {
		t.Error("should not be connected before Connect")
	}
}

// --- Integration test with local SSH server ---

func startTestSSHServer(t *testing.T, rootDir string) (addr string, cleanup func()) {
	t.Helper()

	// Generate server host key
	serverKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(serverKey)
	if err != nil {
		t.Fatal(err)
	}

	config := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if c.User() == "testuser" && string(pass) == "testpass" {
				return nil, nil
			}
			return nil, fmt.Errorf("invalid credentials")
		},
	}
	config.AddHostKey(signer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go handleSSHConnForTest(conn, config, rootDir)
		}
	}()

	cleanup = func() {
		listener.Close()
		close(done)
	}
	return listener.Addr().String(), cleanup
}

func handleSSHConnForTest(conn net.Conn, config *ssh.ServerConfig, rootDir string) {
	sshConn, chans, reqs, err := ssh.NewServerConn(conn, config)
	if err != nil {
		return
	}
	defer sshConn.Close()
	go ssh.DiscardRequests(reqs)

	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			newChannel.Reject(ssh.UnknownChannelType, "unknown")
			continue
		}
		channel, requests, err := newChannel.Accept()
		if err != nil {
			continue
		}
		go func(ch ssh.Channel, reqs <-chan *ssh.Request) {
			defer ch.Close()
			for req := range reqs {
				if req.Type == "exec" {
					if len(req.Payload) < 4 {
						req.Reply(false, nil)
						continue
					}
					cmdLen := binary.BigEndian.Uint32(req.Payload[:4])
					if int(cmdLen) > len(req.Payload)-4 {
						req.Reply(false, nil)
						continue
					}
					cmd := string(req.Payload[4 : 4+cmdLen])
					req.Reply(true, nil)

					// Read stdin (client writes then closes)
					stdinData, _ := io.ReadAll(ch)

					exitCode := uint32(0)
					if strings.HasPrefix(cmd, "mkdir -p ") {
						dir := strings.TrimPrefix(cmd, "mkdir -p ")
						dir = strings.Trim(dir, "'")
						fullPath := filepath.Join(rootDir, dir)
						if err := os.MkdirAll(fullPath, 0755); err != nil {
							exitCode = 1
						}
					} else if strings.HasPrefix(cmd, "cat > ") {
						path := strings.TrimPrefix(cmd, "cat > ")
						path = strings.Trim(path, "'")
						fullPath := filepath.Join(rootDir, path)
						os.MkdirAll(filepath.Dir(fullPath), 0755)
						if err := os.WriteFile(fullPath, stdinData, 0644); err != nil {
							exitCode = 1
						}
					} else {
						exitCode = 1
					}

					ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Code uint32 }{exitCode}))
					return
				}
				req.Reply(false, nil)
			}
		}(channel, requests)
	}
}

func TestSSHClient_Integration_Upload(t *testing.T) {
	rootDir := t.TempDir()
	addr, cleanup := startTestSSHServer(t, rootDir)
	defer cleanup()

	host, port, _ := net.SplitHostPort(addr)
	portInt := 22
	fmt.Sscanf(port, "%d", &portInt)

	client := NewSSHClient(host, portInt, "testuser", "testpass", "", "", 0)

	if err := client.Connect(); err != nil {
		t.Fatalf("Connect error: %v", err)
	}
	defer client.Close()

	if !client.IsConnected() {
		t.Fatal("should be connected after Connect")
	}

	// Create a local test file
	localDir := t.TempDir()
	localFile := filepath.Join(localDir, "test.css")
	content := []byte("body { color: red; }")
	if err := os.WriteFile(localFile, content, 0644); err != nil {
		t.Fatal(err)
	}

	// Upload
	remotePath := "/css/style.css"
	if err := client.Upload(localFile, remotePath); err != nil {
		t.Fatalf("Upload error: %v", err)
	}

	// Verify the file was created on the "remote" (which is rootDir)
	remoteFile := filepath.Join(rootDir, "css", "style.css")
	got, err := os.ReadFile(remoteFile)
	if err != nil {
		t.Fatalf("read remote file error: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("remote content = %q, want %q", got, content)
	}
}

func TestSSHClient_Integration_MkdirAll(t *testing.T) {
	rootDir := t.TempDir()
	addr, cleanup := startTestSSHServer(t, rootDir)
	defer cleanup()

	host, port, _ := net.SplitHostPort(addr)
	portInt := 22
	fmt.Sscanf(port, "%d", &portInt)

	client := NewSSHClient(host, portInt, "testuser", "testpass", "", "", 0)
	if err := client.Connect(); err != nil {
		t.Fatalf("Connect error: %v", err)
	}
	defer client.Close()

	remoteDir := "/deep/nested/dir"
	if err := client.MkdirAll(remoteDir); err != nil {
		t.Fatalf("MkdirAll error: %v", err)
	}

	// Verify directory exists
	fullPath := filepath.Join(rootDir, "deep", "nested", "dir")
	info, err := os.Stat(fullPath)
	if err != nil {
		t.Fatalf("dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("should be a directory")
	}
}

func TestSSHClient_Integration_Close(t *testing.T) {
	rootDir := t.TempDir()
	addr, cleanup := startTestSSHServer(t, rootDir)
	defer cleanup()

	host, port, _ := net.SplitHostPort(addr)
	portInt := 22
	fmt.Sscanf(port, "%d", &portInt)

	client := NewSSHClient(host, portInt, "testuser", "testpass", "", "", 0)
	if err := client.Connect(); err != nil {
		t.Fatalf("Connect error: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Errorf("Close error: %v", err)
	}
	if client.IsConnected() {
		t.Error("should be disconnected after Close")
	}
}

func TestSSHClient_Integration_BadAuth(t *testing.T) {
	rootDir := t.TempDir()
	addr, cleanup := startTestSSHServer(t, rootDir)
	defer cleanup()

	host, port, _ := net.SplitHostPort(addr)
	portInt := 22
	fmt.Sscanf(port, "%d", &portInt)

	client := NewSSHClient(host, portInt, "testuser", "wrongpass", "", "", 0)
	err := client.Connect()
	if err == nil {
		client.Close()
		t.Fatal("expected error for bad credentials")
	}
}

func TestSSHClient_Integration_UploadBinary(t *testing.T) {
	rootDir := t.TempDir()
	addr, cleanup := startTestSSHServer(t, rootDir)
	defer cleanup()

	host, port, _ := net.SplitHostPort(addr)
	portInt := 22
	fmt.Sscanf(port, "%d", &portInt)

	client := NewSSHClient(host, portInt, "testuser", "testpass", "", "", 0)
	if err := client.Connect(); err != nil {
		t.Fatalf("Connect error: %v", err)
	}
	defer client.Close()

	// Create binary content with all byte values
	localDir := t.TempDir()
	localFile := filepath.Join(localDir, "binary.dat")
	binaryContent := make([]byte, 256)
	for i := 0; i < 256; i++ {
		binaryContent[i] = byte(i)
	}
	os.WriteFile(localFile, binaryContent, 0644)

	remotePath := "/data/binary.dat"
	if err := client.Upload(localFile, remotePath); err != nil {
		t.Fatalf("Upload binary error: %v", err)
	}

	remoteFile := filepath.Join(rootDir, "data", "binary.dat")
	got, err := os.ReadFile(remoteFile)
	if err != nil {
		t.Fatalf("read remote binary: %v", err)
	}
	if len(got) != 256 {
		t.Fatalf("remote binary len = %d, want 256", len(got))
	}
	for i := 0; i < 256; i++ {
		if got[i] != byte(i) {
			t.Errorf("byte %d = %d, want %d", i, got[i], i)
			break
		}
	}
}

func TestSSHClient_Integration_UploadLargeFile(t *testing.T) {
	rootDir := t.TempDir()
	addr, cleanup := startTestSSHServer(t, rootDir)
	defer cleanup()

	host, port, _ := net.SplitHostPort(addr)
	portInt := 22
	fmt.Sscanf(port, "%d", &portInt)

	client := NewSSHClient(host, portInt, "testuser", "testpass", "", "", 0)
	if err := client.Connect(); err != nil {
		t.Fatalf("Connect error: %v", err)
	}
	defer client.Close()

	// 1MB file
	localDir := t.TempDir()
	localFile := filepath.Join(localDir, "large.css")
	largeContent := strings.Repeat("body { margin: 0; }\n", 64*1024) // ~1MB
	os.WriteFile(localFile, []byte(largeContent), 0644)

	remotePath := "/css/large.css"
	if err := client.Upload(localFile, remotePath); err != nil {
		t.Fatalf("Upload large error: %v", err)
	}

	remoteFile := filepath.Join(rootDir, "css", "large.css")
	got, err := os.ReadFile(remoteFile)
	if err != nil {
		t.Fatalf("read remote large: %v", err)
	}
	if len(got) != len(largeContent) {
		t.Errorf("remote size = %d, want %d", len(got), len(largeContent))
	}
}

func TestSSHClient_Integration_NotConnected(t *testing.T) {
	client := NewSSHClient("127.0.0.1", 22, "user", "pass", "", "", 0)
	err := client.MkdirAll("/test")
	if err == nil {
		t.Error("expected error when not connected")
	}
	err = client.Upload("/local/file", "/remote/file")
	if err == nil {
		t.Error("expected error when not connected")
	}
}

func TestSSHClient_Integration_MultipleUploads(t *testing.T) {
	rootDir := t.TempDir()
	addr, cleanup := startTestSSHServer(t, rootDir)
	defer cleanup()

	host, port, _ := net.SplitHostPort(addr)
	portInt := 22
	fmt.Sscanf(port, "%d", &portInt)

	client := NewSSHClient(host, portInt, "testuser", "testpass", "", "", 0)
	if err := client.Connect(); err != nil {
		t.Fatalf("Connect error: %v", err)
	}
	defer client.Close()

	localDir := t.TempDir()
	for i := 0; i < 5; i++ {
		f := filepath.Join(localDir, fmt.Sprintf("file%d.css", i))
		os.WriteFile(f, []byte(fmt.Sprintf("content %d", i)), 0644)
		remote := fmt.Sprintf("/dir/file%d.css", i)
		if err := client.Upload(f, remote); err != nil {
			t.Fatalf("upload %d error: %v", i, err)
		}
	}

	for i := 0; i < 5; i++ {
		remoteFile := filepath.Join(rootDir, "dir", fmt.Sprintf("file%d.css", i))
		got, err := os.ReadFile(remoteFile)
		if err != nil {
			t.Errorf("read file %d: %v", i, err)
			continue
		}
		want := fmt.Sprintf("content %d", i)
		if string(got) != want {
			t.Errorf("file %d content = %q, want %q", i, got, want)
		}
	}
}

func TestSSHClient_Integration_Reconnect(t *testing.T) {
	rootDir := t.TempDir()
	addr, cleanup := startTestSSHServer(t, rootDir)
	defer cleanup()

	host, port, _ := net.SplitHostPort(addr)
	portInt := 22
	fmt.Sscanf(port, "%d", &portInt)

	client := NewSSHClient(host, portInt, "testuser", "testpass", "", "", 0)
	if err := client.Connect(); err != nil {
		t.Fatalf("Connect error: %v", err)
	}

	// Close and reconnect
	client.Close()
	if client.IsConnected() {
		t.Error("should be disconnected")
	}

	if err := client.Connect(); err != nil {
		t.Fatalf("reconnect error: %v", err)
	}
	if !client.IsConnected() {
		t.Error("should be connected after reconnect")
	}
	client.Close()
}

func TestSSHClient_Integration_UploadToNestedPath(t *testing.T) {
	rootDir := t.TempDir()
	addr, cleanup := startTestSSHServer(t, rootDir)
	defer cleanup()

	host, port, _ := net.SplitHostPort(addr)
	portInt := 22
	fmt.Sscanf(port, "%d", &portInt)

	client := NewSSHClient(host, portInt, "testuser", "testpass", "", "", 0)
	if err := client.Connect(); err != nil {
		t.Fatalf("Connect error: %v", err)
	}
	defer client.Close()

	localDir := t.TempDir()
	f := filepath.Join(localDir, "deep.css")
	os.WriteFile(f, []byte("deep"), 0644)

	remotePath := "/a/b/c/d/e/deep.css"
	if err := client.Upload(f, remotePath); err != nil {
		t.Fatalf("Upload to nested path error: %v", err)
	}

	remoteFile := filepath.Join(rootDir, "a", "b", "c", "d", "e", "deep.css")
	got, err := os.ReadFile(remoteFile)
	if err != nil {
		t.Fatalf("read nested file: %v", err)
	}
	if string(got) != "deep" {
		t.Errorf("content = %q, want 'deep'", got)
	}
}

func TestSSHClient_Integration_ConnectionTimeout(t *testing.T) {
	// Connect to a port that doesn't exist -- should fail quickly
	client := NewSSHClient("127.0.0.1", 1, "user", "pass", "", "", 0)
	err := client.Connect()
	if err == nil {
		client.Close()
		t.Fatal("expected error connecting to port 1")
	}
	if client.IsConnected() {
		t.Error("should not be connected after failed connect")
	}
}

// --- Keyboard-interactive (OTP) SSH server ---

func startKeyboardInteractiveSSHServer(t *testing.T, expectedOTP string) (addr string, cleanup func()) {
	t.Helper()

	serverKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(serverKey)
	if err != nil {
		t.Fatal(err)
	}

	config := &ssh.ServerConfig{
		KeyboardInteractiveCallback: func(c ssh.ConnMetadata, challenge ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
			answers, err := challenge("", "OTP required", []string{"Enter OTP code: "}, []bool{false})
			if err != nil {
				return nil, err
			}
			if len(answers) == 0 || answers[0] != expectedOTP {
				return nil, fmt.Errorf("invalid OTP")
			}
			return nil, nil
		},
	}
	config.AddHostKey(signer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go handleSSHConnForTest(conn, config, t.TempDir())
		}
	}()

	cleanup = func() {
		listener.Close()
	}
	return listener.Addr().String(), cleanup
}

func TestSSHClient_KeyboardInteractiveAuth(t *testing.T) {
	expectedOTP := "648291"
	addr, cleanup := startKeyboardInteractiveSSHServer(t, expectedOTP)
	defer cleanup()

	host, port, _ := net.SplitHostPort(addr)
	portInt := 22
	fmt.Sscanf(port, "%d", &portInt)

	otpStore := NewOTPStore(nil)
	client := NewSSHClient(host, portInt, "testuser", "", "", "", 0)
	client.SetOTPStore(otpStore)
	client.SetOTPTimeout(5 * time.Second)

	// Pre-set the OTP so the keyboard-interactive callback finds it immediately
	otpStore.Set(expectedOTP)

	if err := client.Connect(); err != nil {
		t.Fatalf("Connect with keyboard-interactive error: %v", err)
	}
	defer client.Close()

	if !client.IsConnected() {
		t.Fatal("should be connected after keyboard-interactive auth")
	}
}

func TestSSHClient_KeyboardInteractiveAuth_DelayedOTP(t *testing.T) {
	expectedOTP := "193847"
	addr, cleanup := startKeyboardInteractiveSSHServer(t, expectedOTP)
	defer cleanup()

	host, port, _ := net.SplitHostPort(addr)
	portInt := 22
	fmt.Sscanf(port, "%d", &portInt)

	otpStore := NewOTPStore(nil)
	client := NewSSHClient(host, portInt, "testuser", "", "", "", 0)
	client.SetOTPStore(otpStore)
	client.SetOTPTimeout(5 * time.Second)

	// Set the OTP after a short delay (simulating clipboard paste)
	go func() {
		time.Sleep(200 * time.Millisecond)
		otpStore.Set(expectedOTP)
	}()

	if err := client.Connect(); err != nil {
		t.Fatalf("Connect with delayed OTP error: %v", err)
	}
	defer client.Close()
}

func TestSSHClient_KeyboardInteractiveAuth_WrongOTP(t *testing.T) {
	addr, cleanup := startKeyboardInteractiveSSHServer(t, "999999")
	defer cleanup()

	host, port, _ := net.SplitHostPort(addr)
	portInt := 22
	fmt.Sscanf(port, "%d", &portInt)

	otpStore := NewOTPStore(nil)
	otpStore.Set("111111") // wrong OTP
	client := NewSSHClient(host, portInt, "testuser", "", "", "", 0)
	client.SetOTPStore(otpStore)
	client.SetOTPTimeout(2 * time.Second)

	err := client.Connect()
	if err == nil {
		client.Close()
		t.Fatal("expected error for wrong OTP")
	}
}

func TestSSHClient_KeyboardInteractiveAuth_Timeout(t *testing.T) {
	addr, cleanup := startKeyboardInteractiveSSHServer(t, "123456")
	defer cleanup()

	host, port, _ := net.SplitHostPort(addr)
	portInt := 22
	fmt.Sscanf(port, "%d", &portInt)

	otpStore := NewOTPStore(nil) // no OTP set
	client := NewSSHClient(host, portInt, "testuser", "", "", "", 0)
	client.SetOTPStore(otpStore)
	client.SetOTPTimeout(500 * time.Millisecond) // very short timeout

	err := client.Connect()
	if err == nil {
		client.Close()
		t.Fatal("expected timeout error when no OTP provided")
	}
}

func TestSSHClient_IsReconnected_False(t *testing.T) {
	client := NewSSHClient("127.0.0.1", 22, "user", "pass", "", "", 0)
	if client.IsReconnecting() {
		t.Error("should not be reconnecting initially")
	}
}

func TestSSHClient_UploadAfterReconnect(t *testing.T) {
	rootDir := t.TempDir()
	addr, cleanup := startTestSSHServer(t, rootDir)
	defer cleanup()

	host, port, _ := net.SplitHostPort(addr)
	portInt := 22
	fmt.Sscanf(port, "%d", &portInt)

	client := NewSSHClient(host, portInt, "testuser", "testpass", "", "", 0)
	client.SetAutoReconnect(true)

	if err := client.Connect(); err != nil {
		t.Fatalf("Connect error: %v", err)
	}

	// Simulate disconnect by closing the underlying client
	client.mu.Lock()
	if client.client != nil {
		client.client.Close()
		client.client = nil
	}
	client.connected = false
	client.mu.Unlock()

	// Trigger reconnect
	go client.autoReconnectLoop()

	// Wait for reconnect
	deadline := time.After(5 * time.Second)
	for {
		if client.IsConnected() {
			break
		}
		select {
		case <-deadline:
			t.Fatal("did not reconnect in time")
		case <-time.After(50 * time.Millisecond):
		}
	}

	// Now try uploading
	localDir := t.TempDir()
	localFile := filepath.Join(localDir, "after_reconnect.css")
	os.WriteFile(localFile, []byte("reconnected"), 0644)

	if err := client.Upload(localFile, "/test/after_reconnect.css"); err != nil {
		t.Fatalf("upload after reconnect error: %v", err)
	}

	remoteFile := filepath.Join(rootDir, "test", "after_reconnect.css")
	got, err := os.ReadFile(remoteFile)
	if err != nil {
		t.Fatalf("read remote file: %v", err)
	}
	if string(got) != "reconnected" {
		t.Errorf("content = %q, want 'reconnected'", got)
	}

	client.Close()
}

func TestSSHClient_HandleDisconnect_TriggersReconnect(t *testing.T) {
	rootDir := t.TempDir()
	addr, cleanup := startTestSSHServer(t, rootDir)
	defer cleanup()

	host, port, _ := net.SplitHostPort(addr)
	portInt := 22
	fmt.Sscanf(port, "%d", &portInt)

	client := NewSSHClient(host, portInt, "testuser", "testpass", "", "", 0)
	client.SetAutoReconnect(true)

	if err := client.Connect(); err != nil {
		t.Fatalf("Connect error: %v", err)
	}

	// Simulate disconnect
	client.handleDisconnect()

	// Should auto-reconnect
	deadline := time.After(5 * time.Second)
	for {
		if client.IsConnected() {
			break
		}
		select {
		case <-deadline:
			t.Fatal("auto-reconnect did not succeed")
		case <-time.After(50 * time.Millisecond):
		}
	}

	client.Close()
}

func TestSSHClient_AutoReconnectDisabled(t *testing.T) {
	rootDir := t.TempDir()
	addr, cleanup := startTestSSHServer(t, rootDir)
	defer cleanup()

	host, port, _ := net.SplitHostPort(addr)
	portInt := 22
	fmt.Sscanf(port, "%d", &portInt)

	client := NewSSHClient(host, portInt, "testuser", "testpass", "", "", 0)
	client.SetAutoReconnect(false)

	if err := client.Connect(); err != nil {
		t.Fatalf("Connect error: %v", err)
	}

	// Simulate disconnect
	client.handleDisconnect()

	// Should NOT auto-reconnect
	time.Sleep(500 * time.Millisecond)
	if client.IsConnected() {
		t.Error("should not auto-reconnect when disabled")
	}
	if client.IsReconnecting() {
		t.Error("should not be in reconnecting state")
	}

	client.Close()
}

func TestSSHClient_EnsureConnected_AlreadyConnected(t *testing.T) {
	rootDir := t.TempDir()
	addr, cleanup := startTestSSHServer(t, rootDir)
	defer cleanup()

	host, port, _ := net.SplitHostPort(addr)
	portInt := 22
	fmt.Sscanf(port, "%d", &portInt)

	client := NewSSHClient(host, portInt, "testuser", "testpass", "", "", 0)
	if err := client.Connect(); err != nil {
		t.Fatalf("Connect error: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	if err := client.ensureConnected(ctx); err != nil {
		t.Errorf("ensureConnected should succeed when already connected: %v", err)
	}
}

func TestSSHClient_EnsureConnected_WaitsForReconnect(t *testing.T) {
	rootDir := t.TempDir()
	addr, cleanup := startTestSSHServer(t, rootDir)
	defer cleanup()

	host, port, _ := net.SplitHostPort(addr)
	portInt := 22
	fmt.Sscanf(port, "%d", &portInt)

	client := NewSSHClient(host, portInt, "testuser", "testpass", "", "", 0)
	client.SetAutoReconnect(true)

	if err := client.Connect(); err != nil {
		t.Fatalf("Connect error: %v", err)
	}

	// Disconnect
	client.mu.Lock()
	if client.client != nil {
		client.client.Close()
		client.client = nil
	}
	client.connected = false
	client.mu.Unlock()

	// ensureConnected should trigger reconnect and wait for it
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.ensureConnected(ctx); err != nil {
		t.Fatalf("ensureConnected error: %v", err)
	}
	if !client.IsConnected() {
		t.Error("should be connected after ensureConnected")
	}

	client.Close()
}

func TestSSHClient_SetOTPStore(t *testing.T) {
	client := NewSSHClient("h", 22, "u", "p", "", "", 0)
	if client.otpStore != nil {
		t.Error("otpStore should be nil initially")
	}
	store := NewOTPStore(nil)
	client.SetOTPStore(store)
	if client.otpStore == nil {
		t.Error("otpStore should be set after SetOTPStore")
	}
}

func TestSSHClient_SetAutoReconnect(t *testing.T) {
	client := NewSSHClient("h", 22, "u", "p", "", "", 0)
	if !client.autoReconnect {
		t.Error("autoReconnect should be true by default")
	}
	client.SetAutoReconnect(false)
	if client.autoReconnect {
		t.Error("autoReconnect should be false after SetAutoReconnect(false)")
	}
}

func TestSSHClient_SetOTPTimeout(t *testing.T) {
	client := NewSSHClient("h", 22, "u", "p", "", "", 0)
	client.SetOTPTimeout(42 * time.Second)
	if client.otpTimeout != 42*time.Second {
		t.Errorf("otpTimeout = %v, want 42s", client.otpTimeout)
	}
}

func TestSSHClient_UploadWhenDisconnected_WaitsForReconnect(t *testing.T) {
	rootDir := t.TempDir()
	addr, cleanup := startTestSSHServer(t, rootDir)
	defer cleanup()

	host, port, _ := net.SplitHostPort(addr)
	portInt := 22
	fmt.Sscanf(port, "%d", &portInt)

	client := NewSSHClient(host, portInt, "testuser", "testpass", "", "", 0)
	client.SetAutoReconnect(true)
	if err := client.Connect(); err != nil {
		t.Fatalf("Connect error: %v", err)
	}

	// Disconnect
	client.mu.Lock()
	if client.client != nil {
		client.client.Close()
		client.client = nil
	}
	client.connected = false
	client.mu.Unlock()

	// Upload should trigger reconnect and succeed
	localDir := t.TempDir()
	localFile := filepath.Join(localDir, "wait.css")
	os.WriteFile(localFile, []byte("waited"), 0644)

	if err := client.Upload(localFile, "/test/wait.css"); err != nil {
		t.Fatalf("upload should succeed after reconnect: %v", err)
	}

	client.Close()
}
