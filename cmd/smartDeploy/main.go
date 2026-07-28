package main

import (
	"flag"
	"fmt"
	"log"
	"os"
)

func main() {
	configPath := flag.String("config", "config.json", "path to config file")
	flag.Parse()

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[FATAL] %v\n", err)
		os.Exit(1)
	}

	logger := log.New(os.Stdout, "", log.Ltime)

	// OTP store — shared between the SSH client (keyboard-interactive
	// callback) and the clipboard watcher.
	otpStore := NewOTPStore(nil)

	// Clipboard watcher — polls the clipboard and feeds OTP codes into
	// the store automatically.
	var clipWatcher *ClipboardWatcher
	if cfg.EnableClipboard && cfg.ClipboardPollMs > 0 {
		clipWatcher = NewClipboardWatcher(
			NewClipboardReader(), otpStore, cfg.ClipboardPollDuration(),
		)
		clipWatcher.Start()
		logger.Printf("Clipboard OTP watcher started (poll: %dms)", cfg.ClipboardPollMs)
	}

	mapper := NewPathMapper(cfg.WatchFolder, cfg.RemoteBasePath, cfg.StripPrefix)
	matcher := NewIgnoreMatcher(cfg.IgnorePatterns)
	client := NewSSHClient(
		cfg.Host, cfg.Port, cfg.Username,
		cfg.Password, cfg.PrivateKeyPath, cfg.KeyPassphrase,
		cfg.KeepAliveDuration(),
	)
	client.SetOTPStore(otpStore)
	client.SetAutoReconnect(cfg.AutoReconnect)
	client.SetOTPTimeout(cfg.OTPTimeoutDuration())
	client.SetLogger(logger)

	// Connect asynchronously so the user can paste an OTP into the
	// clipboard while the REPL is already interactive.
	fmt.Printf("Connecting to %s:%d ...\n", cfg.Host, cfg.Port)
	if cfg.EnableClipboard {
		fmt.Println("Copy your OTP code to the clipboard (or type 'otp <code>' in the REPL).")
	}
	go func() {
		if err := client.Connect(); err != nil {
			logger.Printf("[ERR] connection: %v", err)
		} else {
			logger.Printf("Connected.")
		}
	}()

	deployer := NewDeployer(client, mapper, cfg.AutoWatch, logger)

	watcher, err := NewFileWatcher(
		cfg.WatchFolder, matcher, cfg.FileExtensions,
		cfg.DebounceDuration(), deployer.OnFileChange,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[FATAL] watcher: %v\n", err)
		os.Exit(1)
	}
	defer watcher.Close()
	watcher.Start()

	autoMode := "ON"
	if !cfg.AutoWatch {
		autoMode = "OFF"
	}
	fmt.Printf("Watching: %s\n", cfg.WatchFolder)
	fmt.Printf("AutoWatch: %s (debounce: %dms)\n", autoMode, cfg.DebounceMs)
	fmt.Printf("Remote: %s\n", cfg.RemoteBasePath)
	if cfg.StripPrefix != "" {
		fmt.Printf("StripPrefix: %s\n", cfg.StripPrefix)
	}
	if cfg.AutoReconnect {
		fmt.Printf("AutoReconnect: ON (keepalive: %ds)\n", cfg.KeepAliveSec)
	}
	fmt.Println("---")

	repl := NewREPL(deployer, client, otpStore, os.Stdin, os.Stdout)
	repl.Run()

	if clipWatcher != nil {
		clipWatcher.Close()
	}
	watcher.Close()
	client.Close()
	fmt.Println("Goodbye.")
}
