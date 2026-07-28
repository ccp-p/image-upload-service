package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path"
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

	// OTP / keyboard-interactive auth
	otpStore   *OTPStore
	otpTimeout time.Duration
	lastOTPUse time.Time
	clock      func() time.Time

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

// SetAutoReconnect enables or disables automatic reconnection on disconnect.
func (c *sshClient) SetAutoReconnect(on bool) {
	c.autoReconnect = on
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

	if c.otpStore != nil {
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
// keyboard-interactive auth. It waits for an OTP from the OTPStore that is
// newer than the last one used (so reconnects require a fresh code).
func (c *sshClient) keyboardInteractiveHandler(name, instruction string, questions []string, echos []bool) ([]string, error) {
	answers := make([]string, len(questions))
	for i := range questions {
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

	if c.keepAlive > 0 {
		go c.keepalive()
	}
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

	if c.keepAlive > 0 {
		go c.keepalive()
	}
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
	return session.Run(fmt.Sprintf("mkdir -p %s", shellQuote(remoteDir)))
}

func (c *sshClient) Upload(localPath, remotePath string) error {
	waitCtx, cancel := context.WithTimeout(c.ctx, 30*time.Second)
	defer cancel()
	if err := c.ensureConnected(waitCtx); err != nil {
		return fmt.Errorf("not connected: %w", err)
	}

	data, err := os.ReadFile(localPath)
	if err != nil {
		return fmt.Errorf("read local file: %w", err)
	}

	remoteDir := path.Dir(remotePath)
	if err := c.MkdirAll(remoteDir); err != nil {
		return fmt.Errorf("create remote dir: %w", err)
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

	var stderr bytes.Buffer
	session.Stdin = bytes.NewReader(data)
	session.Stderr = &stderr

	cmd := fmt.Sprintf("cat > %s", shellQuote(remotePath))
	if err := session.Run(cmd); err != nil {
		return fmt.Errorf("upload %s: %w (stderr: %s)", remotePath, err, stderr.String())
	}
	return nil
}

// shellQuote wraps a string in single quotes for safe shell usage.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
