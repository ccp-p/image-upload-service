package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Config holds all deployment tool settings loaded from JSON.
type Config struct {
	Host           string   `json:"host"`
	Port           int      `json:"port"`
	Username       string   `json:"username"`
	Password       string   `json:"password"`
	PrivateKeyPath string   `json:"privateKeyPath"`
	KeyPassphrase  string   `json:"keyPassphrase"`
	WatchFolder    string   `json:"watchFolder"`
	RemoteBasePath string   `json:"remoteBasePath"`
	StripPrefix    string   `json:"stripPrefix"`
	IgnorePatterns []string `json:"ignorePatterns"`
	FileExtensions []string `json:"fileExtensions"`
	DebounceMs     int      `json:"debounceMs"`
	AutoWatch      bool     `json:"autoWatch"`
	KeepAliveSec   int      `json:"keepAliveIntervalSec"`

	// Clipboard OTP monitoring
	EnableClipboard bool `json:"enableClipboard"`
	ClipboardPollMs int  `json:"clipboardPollMs"`

	// Connection resilience
	AutoReconnect bool `json:"autoReconnect"`
	OTPTimeoutSec int  `json:"otpTimeoutSec"`
}

func defaultConfig() Config {
	return Config{
		Port:         22,
		DebounceMs:   1500,
		AutoWatch:    true,
		KeepAliveSec: 30,
		IgnorePatterns: []string{
			".git", "node_modules", "target", ".idea",
			"*.log", "*.tmp", "*.swp", "*.bak",
		},
		FileExtensions: []string{
			".html", ".css", ".js", ".json", ".xml",
			".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg",
		},
		EnableClipboard: true,
		ClipboardPollMs: 2000,
		AutoReconnect:   true,
		OTPTimeoutSec:   300,
	}
}

// LoadConfig reads and validates a JSON config file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	cfg := defaultConfig()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if cfg.Password == "" {
		cfg.Password = os.Getenv("SSH_PASSWORD")
	}
	if cfg.KeyPassphrase == "" {
		cfg.KeyPassphrase = os.Getenv("SSH_KEY_PASSPHRASE")
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) Validate() error {
	if c.Host == "" {
		return fmt.Errorf("config: host is required")
	}
	if c.Port <= 0 || c.Port > 65535 {
		return fmt.Errorf("config: port must be 1-65535, got %d", c.Port)
	}
	if c.Username == "" {
		return fmt.Errorf("config: username is required")
	}
	if c.Password == "" && c.PrivateKeyPath == "" {
		return fmt.Errorf("config: either password or privateKeyPath is required")
	}
	if c.WatchFolder == "" {
		return fmt.Errorf("config: watchFolder is required")
	}
	if c.RemoteBasePath == "" {
		return fmt.Errorf("config: remoteBasePath is required")
	}
	if c.DebounceMs < 0 {
		return fmt.Errorf("config: debounceMs must be >= 0")
	}
	if c.ClipboardPollMs < 0 {
		return fmt.Errorf("config: clipboardPollMs must be >= 0")
	}
	if c.OTPTimeoutSec < 0 {
		return fmt.Errorf("config: otpTimeoutSec must be >= 0")
	}
	return nil
}

func (c *Config) DebounceDuration() time.Duration {
	return time.Duration(c.DebounceMs) * time.Millisecond
}

func (c *Config) KeepAliveDuration() time.Duration {
	return time.Duration(c.KeepAliveSec) * time.Second
}

func (c *Config) ClipboardPollDuration() time.Duration {
	return time.Duration(c.ClipboardPollMs) * time.Millisecond
}

func (c *Config) OTPTimeoutDuration() time.Duration {
	return time.Duration(c.OTPTimeoutSec) * time.Second
}
