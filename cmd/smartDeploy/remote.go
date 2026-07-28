package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// RemoteClient abstracts remote file operations for testability.
type RemoteClient interface {
	Connect() error
	Close() error
	Upload(localPath, remotePath string) error
	MkdirAll(remoteDir string) error
	IsConnected() bool
	IsReconnecting() bool
	RemotePWD() (string, error)
	Stat(remotePath string) (string, error)
	ListDir(remotePath string) (string, error)
}

// sshClient implements RemoteClient using SSH (mkdir -p + cat >).
type sshClient struct {
	host           string
	port           int
	username       string
	password       string
	privateKeyPath string
	keyPassphrase  string
	keepAlive      time.Duration
	jailRoot       string

	// OTP / keyboard-interactive auth
	otpStore    *OTPStore
	otpPrompter OTPPrompter
	otpTimeout  time.Duration
	lastOTPUse  time.Time
	clock       func() time.Time

	// Auto-reconnect
	autoReconnect bool

	// Logging
	logger *log.Logger

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc

	mu           sync.Mutex
	client       *ssh.Client
	connected    bool
	reconnecting bool
	remotePWD    string

	// connectMu serialises dial attempts so concurrent callers don't
	// open two SSH connections at the same time.
	connectMu sync.Mutex
}

func NewSSHClient(host string, port int, username, password, privateKeyPath, keyPassphrase string, keepAlive time.Duration) *sshClient {
	ctx, cancel := context.WithCancel(context.Background())
	return &sshClient{
		host:           host,
		port:           port,
		username:       username,
		password:       password,
		privateKeyPath: privateKeyPath,
		keyPassphrase:  keyPassphrase,
		keepAlive:      keepAlive,
		otpTimeout:     5 * time.Minute,
		autoReconnect:  true,
		clock:          time.Now,
		logger:         log.New(io.Discard, "", 0),
		ctx:            ctx,
		cancel:         cancel,
	}
}

// SetOTPStore configures keyboard-interactive (OTP) authentication. When set,
// the SSH client adds a KeyboardInteractive auth method that waits for an OTP
// from the store.
func (c *sshClient) SetOTPStore(s *OTPStore) {
	c.otpStore = s
}

// SetOTPPrompter configures interactive OTP confirmation. When set, the
// keyboard-interactive handler asks the user to confirm each OTP before
// sending it, instead of auto-using the clipboard value.
func (c *sshClient) SetOTPPrompter(p OTPPrompter) {
	c.otpPrompter = p
}

// SetAutoReconnect enables or disables automatic reconnection on disconnect.
func (c *sshClient) SetAutoReconnect(on bool) {
	c.autoReconnect = on
}

// SetJailRoot sets a root directory prefix prepended to all remote paths.
// This is needed when the SSH server chroots or sandboxes sessions to a
// specific directory (e.g., /tmp on a jumpserver). When set, every remote
// path is resolved as path.Join(jailRoot, remotePath).
func (c *sshClient) SetJailRoot(root string) {
	c.jailRoot = path.Clean(root)
}

// resolveRemote prepends the jailRoot to a remote path if one is set.
// If the path already starts with jailRoot it is returned unchanged to
// avoid double-prepending.
func (c *sshClient) resolveRemote(p string) string {
	if c.jailRoot == "" {
		return p
	}
	if p == c.jailRoot || strings.HasPrefix(p, c.jailRoot+"/") {
		return p
	}
	return path.Join(c.jailRoot, p)
}

// SetLogger configures where diagnostic messages go.
func (c *sshClient) SetLogger(l *log.Logger) {
	if l != nil {
		c.logger = l
	}
}

// SetClock overrides the clock used for OTP timestamps (mainly for tests).
func (c *sshClient) SetClock(fn func() time.Time) {
	if fn != nil {
		c.clock = fn
	}
}

// SetOTPTimeout sets how long the keyboard-interactive callback waits for an
// OTP before giving up.
func (c *sshClient) SetOTPTimeout(d time.Duration) {
	c.otpTimeout = d
}

func (c *sshClient) logf(format string, args ...interface{}) {
	c.logger.Printf(format, args...)
}

func (c *sshClient) buildConfig() (*ssh.ClientConfig, error) {
	var authMethods []ssh.AuthMethod

	if c.privateKeyPath != "" {
		key, err := os.ReadFile(c.privateKeyPath)
		if err != nil {
			return nil, fmt.Errorf("read private key: %w", err)
		}
		var signer ssh.Signer
		if c.keyPassphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(key, []byte(c.keyPassphrase))
		} else {
			signer, err = ssh.ParsePrivateKey(key)
		}
		if err != nil {
			return nil, fmt.Errorf("parse private key: %w", err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	}

	if c.password != "" {
		authMethods = append(authMethods, ssh.Password(c.password))
	}

	if c.otpStore != nil || c.otpPrompter != nil {
		authMethods = append(authMethods, ssh.KeyboardInteractive(c.keyboardInteractiveHandler))
	}

	if len(authMethods) == 0 {
		return nil, fmt.Errorf("no authentication method available")
	}

	return &ssh.ClientConfig{
		User:            c.username,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         30 * time.Second,
	}, nil
}

// keyboardInteractiveHandler is called by the SSH library during
// keyboard-interactive auth. If an OTPPrompter is set it asks the user to
// confirm each code interactively; otherwise it falls back to the OTPStore.
func (c *sshClient) keyboardInteractiveHandler(name, instruction string, questions []string, echos []bool) ([]string, error) {
	answers := make([]string, len(questions))
	for i := range questions {
		// Use interactive prompter if available — asks the user to confirm.
		if c.otpPrompter != nil {
			c.logf("[OTP] Server requesting authentication code. Waiting for user confirmation...")
			ctx, cancel := context.WithTimeout(c.ctx, c.otpTimeout)
			otp, err := c.otpPrompter.PromptOTP(ctx)
			cancel()
			if err != nil {
				return nil, fmt.Errorf("OTP prompt: %w", err)
			}
			c.mu.Lock()
			c.lastOTPUse = c.clock()
			c.mu.Unlock()
			if c.otpStore != nil {
				c.otpStore.Set(otp)
			}
			c.logf("[OTP] Code confirmed and sent to server.")
			answers[i] = otp
			continue
		}

		// Fallback: non-interactive OTP store.
		c.mu.Lock()
		notBefore := c.lastOTPUse
		c.mu.Unlock()

		c.logf("[OTP] Server requesting authentication code. Waiting for clipboard or manual input...")
		ctx, cancel := context.WithTimeout(c.ctx, c.otpTimeout)
		otp, err := c.otpStore.WaitForOTP(ctx, notBefore)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("waiting for OTP: %w", err)
		}

		c.mu.Lock()
		c.lastOTPUse = c.clock()
		c.mu.Unlock()

		c.logf("[OTP] Code received, sending to server.")
		answers[i] = otp
	}
	return answers, nil
}

func (c *sshClient) Connect() error {
	c.connectMu.Lock()
	defer c.connectMu.Unlock()
	return c.connectInternal()
}

// connectInternal performs the dial without acquiring connectMu. This is used
// by Connect (which holds connectMu) and by autoReconnectLoop (which also
// holds connectMu).
func (c *sshClient) connectInternal() error {
	c.mu.Lock()
	if c.connected && c.client != nil {
		c.mu.Unlock()
		return nil
	}
	if c.reconnecting {
		c.mu.Unlock()
		return fmt.Errorf("reconnection already in progress")
	}
	cfg, err := c.buildConfig()
	if err != nil {
		c.mu.Unlock()
		return err
	}
	c.mu.Unlock()

	addr := fmt.Sprintf("%s:%d", c.host, c.port)
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return fmt.Errorf("ssh dial %s: %w", addr, err)
	}

	c.mu.Lock()
	c.client = client
	c.connected = true
	c.mu.Unlock()

	c.postConnect()
	return nil
}

func (c *sshClient) keepalive() {
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-time.After(c.keepAlive):
		}

		c.mu.Lock()
		if !c.connected || c.client == nil || c.reconnecting {
			c.mu.Unlock()
			return
		}
		client := c.client
		c.mu.Unlock()

		_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
		if err != nil {
			c.handleDisconnect()
			return
		}
	}
}

// handleDisconnect marks the client as disconnected and kicks off
// auto-reconnect if enabled.
func (c *sshClient) handleDisconnect() {
	c.mu.Lock()
	if c.client != nil {
		c.client.Close()
		c.client = nil
	}
	wasReconnecting := c.reconnecting
	c.connected = false
	c.mu.Unlock()

	c.logf("[WARN] SSH connection lost.")

	if c.autoReconnect && !wasReconnecting {
		go c.autoReconnectLoop()
	}
}

// autoReconnectLoop repeatedly tries to reconnect with exponential backoff
// until it succeeds or the client is closed.
func (c *sshClient) autoReconnectLoop() {
	c.mu.Lock()
	if c.reconnecting {
		c.mu.Unlock()
		return
	}
	c.reconnecting = true
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		c.reconnecting = false
		c.mu.Unlock()
	}()

	c.connectMu.Lock()
	defer c.connectMu.Unlock()

	backoff := time.Second
	for {
		select {
		case <-c.ctx.Done():
			c.logf("[RECONNECT] Cancelled.")
			return
		default:
		}

		c.logf("[RECONNECT] Attempting to reconnect...")
		err := c.dialLocked()
		if err == nil {
			c.logf("[RECONNECT] Reconnected successfully.")
			return
		}
		c.logf("[RECONNECT] Failed: %v. Retrying in %v...", err, backoff)
		select {
		case <-c.ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

// dialLocked is the innermost dial — it assumes connectMu is already held and
// performs the SSH dial without additional locking guards.
func (c *sshClient) dialLocked() error {
	c.mu.Lock()
	if c.connected && c.client != nil {
		c.mu.Unlock()
		return nil
	}
	cfg, err := c.buildConfig()
	if err != nil {
		c.mu.Unlock()
		return err
	}
	c.mu.Unlock()

	addr := fmt.Sprintf("%s:%d", c.host, c.port)
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return fmt.Errorf("ssh dial %s: %w", addr, err)
	}

	c.mu.Lock()
	c.client = client
	c.connected = true
	c.mu.Unlock()

	c.postConnect()
	return nil
}

// ensureConnected waits up to ctx for the client to become connected. If
// auto-reconnect is enabled and no reconnect is running it starts one.
func (c *sshClient) ensureConnected(ctx context.Context) error {
	c.mu.Lock()
	if c.connected && c.client != nil {
		c.mu.Unlock()
		return nil
	}
	reconnecting := c.reconnecting
	c.mu.Unlock()

	if !reconnecting && c.autoReconnect {
		go c.autoReconnectLoop()
	} else if !reconnecting {
		return fmt.Errorf("not connected and auto-reconnect is disabled")
	}

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			c.mu.Lock()
			ok := c.connected && c.client != nil
			c.mu.Unlock()
			if ok {
				return nil
			}
		}
	}
}

func (c *sshClient) Close() error {
	c.cancel()
	c.connectMu.Lock()
	defer c.connectMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client != nil {
		err := c.client.Close()
		c.client = nil
		c.connected = false
		c.reconnecting = false
		return err
	}
	return nil
}

func (c *sshClient) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

func (c *sshClient) IsReconnecting() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reconnecting
}

func (c *sshClient) getClient() (*ssh.Client, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.connected || c.client == nil {
		return nil, fmt.Errorf("not connected")
	}
	return c.client, nil
}

// RemotePWD runs pwd on the remote server and caches the result.
func (c *sshClient) RemotePWD() (string, error) {
	client, err := c.getClient()
	if err != nil {
		return "", err
	}
	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("ssh session: %w", err)
	}
	defer session.Close()
	var stdout bytes.Buffer
	session.Stdout = &stdout
	if err := session.Run("pwd"); err != nil {
		return "", fmt.Errorf("pwd: %w", err)
	}
	pwd := strings.TrimSpace(stdout.String())
	c.mu.Lock()
	c.remotePWD = pwd
	c.mu.Unlock()
	return pwd, nil
}

// postConnect runs after a successful dial: logs the remote working
// directory and starts the keepalive goroutine.
func (c *sshClient) postConnect() {
	if pwd, err := c.RemotePWD(); err == nil {
		c.logf("Remote working directory: %s", pwd)
	} else {
		c.logf("[WARN] could not determine remote working directory: %v", err)
	}
	if c.keepAlive > 0 {
		go c.keepalive()
	}
}

func (c *sshClient) MkdirAll(remoteDir string) error {
	waitCtx, cancel := context.WithTimeout(c.ctx, 30*time.Second)
	defer cancel()
	if err := c.ensureConnected(waitCtx); err != nil {
		return fmt.Errorf("not connected: %w", err)
	}

	client, err := c.getClient()
	if err != nil {
		return err
	}
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("ssh session: %w", err)
	}
	defer session.Close()
	physicalDir := c.resolveRemote(remoteDir)
	return session.Run(fmt.Sprintf("mkdir -p %s", shellQuote(physicalDir)))
}

// readStableFile reads a local file and verifies its size is stable.
// Editors may write files in multiple flushes; without this check we could
// upload a partially-written file. It reads the file, waits briefly,
// re-stats, and re-reads if the size changed.
func readStableFile(localPath string, maxRetries int, wait time.Duration) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		data, err := os.ReadFile(localPath)
		if err != nil {
			return nil, err
		}
		if attempt >= maxRetries {
			return data, nil
		}
		time.Sleep(wait)
		info, err := os.Stat(localPath)
		if err != nil {
			return nil, err
		}
		if info.Size() == int64(len(data)) {
			return data, nil
		}
		// Size changed between read and stat; re-read.
	}
}

func (c *sshClient) Upload(localPath, remotePath string) error {
	waitCtx, cancel := context.WithTimeout(c.ctx, 30*time.Second)
	defer cancel()
	if err := c.ensureConnected(waitCtx); err != nil {
		return fmt.Errorf("not connected: %w", err)
	}

	data, err := readStableFile(localPath, 3, 50*time.Millisecond)
	if err != nil {
		return fmt.Errorf("read local file: %w", err)
	}

	physicalPath := c.resolveRemote(remotePath)
	remoteDir := path.Dir(physicalPath)

	if physicalPath != remotePath {
		c.logf("[UPLOAD] %s -> %s (server: %s) (%d bytes)", localPath, remotePath, physicalPath, len(data))
	} else {
		c.logf("[UPLOAD] %s -> %s (%d bytes)", localPath, remotePath, len(data))
	}

	client, err := c.getClient()
	if err != nil {
		return err
	}

	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("ssh session: %w", err)
	}
	defer session.Close()

	var stdout, stderr bytes.Buffer
	session.Stdin = bytes.NewReader(data)
	session.Stdout = &stdout
	session.Stderr = &stderr

	// Combine mkdir, upload, and verify into a single shell session so the
	// directory cannot vanish between mkdir and cat (a real risk with
	// jumpserver/bastion per-session sandboxes). If any step fails the
	// whole command returns non-zero and Upload returns an error, so the
	// caller never sees a false [OK].
	cmd := fmt.Sprintf("mkdir -p %s && cat > %s && ls -ld %s && wc -c < %s",
		shellQuote(remoteDir), shellQuote(physicalPath), shellQuote(physicalPath), shellQuote(physicalPath))
	if err := session.Run(cmd); err != nil {
		return fmt.Errorf("upload %s: %w (stderr: %s)", physicalPath, err, strings.TrimSpace(stderr.String()))
	}

	// Verify uploaded size matches local size. The last line of stdout
	// is the wc -c output (a bare number); earlier lines are ls -ld.
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) > 0 {
		wcLine := strings.TrimSpace(lines[len(lines)-1])
		if remoteBytes, parseErr := strconv.Atoi(wcLine); parseErr == nil {
			if remoteBytes != len(data) {
				return fmt.Errorf("upload %s: size mismatch (local %d, remote %d)",
					physicalPath, len(data), remoteBytes)
			}
			c.logf("[VERIFY] %s (%d bytes confirmed)", physicalPath, remoteBytes)
		} else {
			// wc -c output unparseable; fall back to raw ls -ld detail.
			if detail := strings.TrimSpace(stdout.String()); detail != "" {
				c.logf("[VERIFY] %s", detail)
			}
		}
	}
	return nil
}

// Stat runs ls -ld on a remote path and returns the detail line.
// Used for post-upload verification so the user can confirm the file
// exists at the expected absolute path on the server filesystem.
func (c *sshClient) Stat(remotePath string) (string, error) {
	client, err := c.getClient()
	if err != nil {
		return "", err
	}
	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("ssh session: %w", err)
	}
	defer session.Close()
	var stdout bytes.Buffer
	session.Stdout = &stdout
	physicalPath := c.resolveRemote(remotePath)
	if err := session.Run(fmt.Sprintf("ls -ld %s", shellQuote(physicalPath))); err != nil {
		return "", fmt.Errorf("stat %s: %w", remotePath, err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// ListDir runs ls -la on a remote directory and returns the output.
func (c *sshClient) ListDir(remotePath string) (string, error) {
	client, err := c.getClient()
	if err != nil {
		return "", err
	}
	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("ssh session: %w", err)
	}
	defer session.Close()
	var stdout bytes.Buffer
	session.Stdout = &stdout
	physicalPath := c.resolveRemote(remotePath)
	if err := session.Run(fmt.Sprintf("ls -la %s", shellQuote(physicalPath))); err != nil {
		return "", fmt.Errorf("ls %s: %w", remotePath, err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// shellQuote wraps a string in single quotes for safe shell usage.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
