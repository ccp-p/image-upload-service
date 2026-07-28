package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"sync"
	"sync/atomic"
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

	// Shared line reader — distributes stdin lines between the REPL and
	// the OTP prompter so they never compete for the same input.
	lineReader := NewSharedLineReader(os.Stdin)
	defer lineReader.Close()

	// Shared coordination state between REPL and OTP prompter.
	var otpActive atomic.Bool
	writeMu := &sync.Mutex{}

	// OTP store — shared between the SSH client (keyboard-interactive
	// callback) and the clipboard watcher.
	otpStore := NewOTPStore(nil)

	// Clipboard watcher — polls the clipboard and feeds OTP codes into
	// the store automatically (used by the 'otp' REPL command for status).
	var clipWatcher *ClipboardWatcher
	var clipReader ClipboardReader
	if cfg.EnableClipboard && cfg.ClipboardPollMs > 0 {
		clipReader = NewClipboardReader()
		clipWatcher = NewClipboardWatcher(
			clipReader, otpStore, cfg.ClipboardPollDuration(),
		)
		clipWatcher.Start()
		logger.Printf("Clipboard OTP watcher started (poll: %dms)", cfg.ClipboardPollMs)
	}

	// Interactive OTP prompter — polls the clipboard in real time and asks
	// the user to press Enter to confirm before sending the code.
	var prompter OTPPrompter
	if clipReader != nil {
		p := NewInteractiveOTPPrompter(lineReader, os.Stdout, clipReader, &otpActive, writeMu)
		prompter = p
	}

	mapper := NewPathMapper(cfg.WatchFolder, cfg.RemoteBasePath, cfg.StripPrefix)
	matcher := NewIgnoreMatcher(cfg.IgnorePatterns)
	client := NewSSHClient(
		cfg.Host, cfg.Port, cfg.Username,
		cfg.Password, cfg.PrivateKeyPath, cfg.KeyPassphrase,
		cfg.KeepAliveDuration(),
	)
	client.SetOTPStore(otpStore)
	if prompter != nil {
		client.SetOTPPrompter(prompter)
	}
	client.SetAutoReconnect(cfg.AutoReconnect)
	client.SetOTPTimeout(cfg.OTPTimeoutDuration())
	client.SetLogger(logger)
	client.SetJailRoot(cfg.JailRoot)

	// Connect asynchronously so the user can confirm an OTP via the
	// interactive prompter while the REPL is already responsive.
	fmt.Printf("Connecting to %s:%d ...\n", cfg.Host, cfg.Port)
	if prompter != nil {
		fmt.Println("When prompted, copy your OTP to clipboard and press Enter to confirm.")
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
	if cfg.JailRoot != "" {
		fmt.Printf("JailRoot: %s (uploads go to %s)\n", cfg.JailRoot, cfg.JailRoot)
	}
	if cfg.StripPrefix != "" {
		fmt.Printf("StripPrefix: %s\n", cfg.StripPrefix)
	}
	if cfg.AutoReconnect {
		fmt.Printf("AutoReconnect: ON (keepalive: %ds)\n", cfg.KeepAliveSec)
	}
	fmt.Println("---")

	repl := NewREPL(deployer, client, otpStore, lineReader, os.Stdout)
	repl.SetOTPActive(&otpActive)
	repl.SetWriteMu(writeMu)
	repl.SetRemoteBasePath(cfg.RemoteBasePath)
	repl.Run()

	if clipWatcher != nil {
		clipWatcher.Close()
	}
	watcher.Close()
	client.Close()
	fmt.Println("Goodbye.")
}
