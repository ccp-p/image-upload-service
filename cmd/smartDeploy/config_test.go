package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := defaultConfig()
	if cfg.Port != 22 {
		t.Errorf("default port = %d, want 22", cfg.Port)
	}
	if cfg.DebounceMs != 1500 {
		t.Errorf("default debounceMs = %d, want 1500", cfg.DebounceMs)
	}
	if !cfg.AutoWatch {
		t.Error("default autoWatch should be true")
	}
	if cfg.KeepAliveSec != 30 {
		t.Errorf("default keepAliveSec = %d, want 30", cfg.KeepAliveSec)
	}
	if !cfg.EnableClipboard {
		t.Error("default enableClipboard should be true")
	}
	if cfg.ClipboardPollMs != 2000 {
		t.Errorf("default clipboardPollMs = %d, want 2000", cfg.ClipboardPollMs)
	}
	if !cfg.AutoReconnect {
		t.Error("default autoReconnect should be true")
	}
	if cfg.OTPTimeoutSec != 300 {
		t.Errorf("default otpTimeoutSec = %d, want 300", cfg.OTPTimeoutSec)
	}
	if len(cfg.IgnorePatterns) == 0 {
		t.Error("default ignorePatterns should not be empty")
	}
	if len(cfg.FileExtensions) == 0 {
		t.Error("default fileExtensions should not be empty")
	}
}

func TestLoadConfig_Valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{
		"host": "10.0.0.1",
		"port": 2222,
		"username": "testuser",
		"password": "secret",
		"watchFolder": "C:/project/test",
		"remoteBasePath": "/remote/base",
		"stripPrefix": "src/main/webapp",
		"debounceMs": 2000,
		"autoWatch": false
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig error: %v", err)
	}
	if cfg.Host != "10.0.0.1" {
		t.Errorf("host = %q", cfg.Host)
	}
	if cfg.Port != 2222 {
		t.Errorf("port = %d", cfg.Port)
	}
	if cfg.Username != "testuser" {
		t.Errorf("username = %q", cfg.Username)
	}
	if cfg.Password != "secret" {
		t.Errorf("password = %q", cfg.Password)
	}
	if cfg.DebounceMs != 2000 {
		t.Errorf("debounceMs = %d", cfg.DebounceMs)
	}
	if cfg.AutoWatch {
		t.Error("autoWatch should be false")
	}
	if cfg.StripPrefix != "src/main/webapp" {
		t.Errorf("stripPrefix = %q", cfg.StripPrefix)
	}
	// defaults should still be present
	if len(cfg.IgnorePatterns) == 0 {
		t.Error("ignorePatterns should have defaults")
	}
	if len(cfg.FileExtensions) == 0 {
		t.Error("fileExtensions should have defaults")
	}
}

func TestLoadConfig_MissingFile(t *testing.T) {
	_, err := LoadConfig("nonexistent_config_file.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadConfig_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("{invalid json}"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestValidate_MissingHost(t *testing.T) {
	cfg := defaultConfig()
	cfg.Host = ""
	cfg.Password = "pass"
	cfg.WatchFolder = "/tmp"
	cfg.RemoteBasePath = "/remote"
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for missing host")
	}
}

func TestValidate_InvalidPort(t *testing.T) {
	cfg := defaultConfig()
	cfg.Host = "h"
	cfg.Port = 0
	cfg.Password = "pass"
	cfg.WatchFolder = "/tmp"
	cfg.RemoteBasePath = "/remote"
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for port 0")
	}
	cfg.Port = 70000
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for port > 65535")
	}
}

func TestValidate_NoAuth(t *testing.T) {
	cfg := defaultConfig()
	cfg.Host = "h"
	cfg.Port = 22
	cfg.Username = "u"
	cfg.Password = ""
	cfg.PrivateKeyPath = ""
	cfg.WatchFolder = "/tmp"
	cfg.RemoteBasePath = "/remote"
	if err := cfg.Validate(); err == nil {
		t.Error("expected error when no auth provided")
	}
}

func TestValidate_KeyAuth(t *testing.T) {
	cfg := defaultConfig()
	cfg.Host = "h"
	cfg.Port = 22
	cfg.Username = "u"
	cfg.Password = ""
	cfg.PrivateKeyPath = "/path/to/key"
	cfg.WatchFolder = "/tmp"
	cfg.RemoteBasePath = "/remote"
	if err := cfg.Validate(); err != nil {
		t.Errorf("key auth should be valid: %v", err)
	}
}

func TestValidate_MissingWatchFolder(t *testing.T) {
	cfg := defaultConfig()
	cfg.Host = "h"
	cfg.Password = "pass"
	cfg.WatchFolder = ""
	cfg.RemoteBasePath = "/remote"
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for missing watchFolder")
	}
}

func TestValidate_MissingRemoteBasePath(t *testing.T) {
	cfg := defaultConfig()
	cfg.Host = "h"
	cfg.Password = "pass"
	cfg.WatchFolder = "/tmp"
	cfg.RemoteBasePath = ""
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for missing remoteBasePath")
	}
}

func TestValidate_NegativeDebounce(t *testing.T) {
	cfg := defaultConfig()
	cfg.Host = "h"
	cfg.Password = "pass"
	cfg.WatchFolder = "/tmp"
	cfg.RemoteBasePath = "/remote"
	cfg.DebounceMs = -1
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for negative debounceMs")
	}
}

func TestLoadConfig_PasswordFromEnv(t *testing.T) {
	t.Setenv("SSH_PASSWORD", "envpass")

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{
		"host": "h",
		"port": 22,
		"username": "u",
		"watchFolder": "/tmp",
		"remoteBasePath": "/remote"
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig error: %v", err)
	}
	if cfg.Password != "envpass" {
		t.Errorf("password = %q, want envpass", cfg.Password)
	}
}

func TestLoadConfig_KeyPassphraseFromEnv(t *testing.T) {
	t.Setenv("SSH_KEY_PASSPHRASE", "envphrase")

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{
		"host": "h",
		"port": 22,
		"username": "u",
		"privateKeyPath": "/key",
		"watchFolder": "/tmp",
		"remoteBasePath": "/remote"
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig error: %v", err)
	}
	if cfg.KeyPassphrase != "envphrase" {
		t.Errorf("keyPassphrase = %q, want envphrase", cfg.KeyPassphrase)
	}
}

func TestDebounceDuration(t *testing.T) {
	cfg := Config{DebounceMs: 2500}
	d := cfg.DebounceDuration()
	if d != 2500*time.Millisecond {
		t.Errorf("debounce duration = %v, want 2500ms", d)
	}
}

func TestKeepAliveDuration(t *testing.T) {
	cfg := Config{KeepAliveSec: 45}
	d := cfg.KeepAliveDuration()
	if d != 45*time.Second {
		t.Errorf("keepalive duration = %v, want 45s", d)
	}
}

func TestLoadConfig_ZeroDebounce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{
		"host": "h",
		"port": 22,
		"username": "u",
		"password": "p",
		"watchFolder": "/tmp",
		"remoteBasePath": "/remote",
		"debounceMs": 0
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("debounceMs=0 should be valid: %v", err)
	}
	if cfg.DebounceDuration() != 0 {
		t.Errorf("debounce duration = %v, want 0", cfg.DebounceDuration())
	}
}

func TestClipboardPollDuration(t *testing.T) {
	cfg := Config{ClipboardPollMs: 3000}
	d := cfg.ClipboardPollDuration()
	if d != 3*time.Second {
		t.Errorf("clipboard poll duration = %v, want 3s", d)
	}
}

func TestOTPTimeoutDuration(t *testing.T) {
	cfg := Config{OTPTimeoutSec: 120}
	d := cfg.OTPTimeoutDuration()
	if d != 2*time.Minute {
		t.Errorf("otp timeout duration = %v, want 2m", d)
	}
}

func TestValidate_NegativeClipboardPollMs(t *testing.T) {
	cfg := defaultConfig()
	cfg.Host = "h"
	cfg.Password = "pass"
	cfg.WatchFolder = "/tmp"
	cfg.RemoteBasePath = "/remote"
	cfg.ClipboardPollMs = -1
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for negative clipboardPollMs")
	}
}

func TestValidate_NegativeOTPTimeoutSec(t *testing.T) {
	cfg := defaultConfig()
	cfg.Host = "h"
	cfg.Password = "pass"
	cfg.WatchFolder = "/tmp"
	cfg.RemoteBasePath = "/remote"
	cfg.OTPTimeoutSec = -1
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for negative otpTimeoutSec")
	}
}

func TestLoadConfig_NewFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{
		"host": "h",
		"port": 22,
		"username": "u",
		"password": "p",
		"watchFolder": "/tmp",
		"remoteBasePath": "/remote",
		"enableClipboard": false,
		"clipboardPollMs": 500,
		"autoReconnect": false,
		"otpTimeoutSec": 60
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig error: %v", err)
	}
	if cfg.EnableClipboard {
		t.Error("enableClipboard should be false")
	}
	if cfg.ClipboardPollMs != 500 {
		t.Errorf("clipboardPollMs = %d, want 500", cfg.ClipboardPollMs)
	}
	if cfg.AutoReconnect {
		t.Error("autoReconnect should be false")
	}
	if cfg.OTPTimeoutSec != 60 {
		t.Errorf("otpTimeoutSec = %d, want 60", cfg.OTPTimeoutSec)
	}
}

func TestLoadConfig_NewFieldsDefaultWhenOmitted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{
		"host": "h",
		"port": 22,
		"username": "u",
		"password": "p",
		"watchFolder": "/tmp",
		"remoteBasePath": "/remote"
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig error: %v", err)
	}
	if !cfg.EnableClipboard {
		t.Error("enableClipboard should default to true")
	}
	if !cfg.AutoReconnect {
		t.Error("autoReconnect should default to true")
	}
	if cfg.ClipboardPollMs != 2000 {
		t.Errorf("clipboardPollMs should default to 2000, got %d", cfg.ClipboardPollMs)
	}
}
