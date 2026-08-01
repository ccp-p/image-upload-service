package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	configPath := flag.String("config", "config.json", "path to config file")
	flag.Parse()

	// Positional args after flags are file paths to upload immediately
	// after connection. This lets the user drag a file onto the exe or
	// use a right-click context menu without typing paths in the REPL.
	queuedFiles := resolveQueuedPaths(flag.Args())

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[FATAL] %v\n", err)
		os.Exit(1)
	}

	logger := log.New(os.Stdout, "", log.Ltime)
	// Pick the watch folder based on the IS_HOME env var. When set to "1"
	// (home machine), use watchFolderHome; otherwise use watchFolder. This
	// lets the same binary/config run on both home and work machines where
	// the project lives under different drives/paths.
	watchFolder := cfg.WatchFolder
	if os.Getenv("IS_HOME") == "1" && cfg.WatchFolderHome != "" {
		watchFolder = cfg.WatchFolderHome
		logger.Printf("IS_HOME=1 -> home watch folder: %s", watchFolder)
	} else {
		logger.Printf("watch folder: %s", watchFolder)
	}

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
		p.SetAutoConfirmFirstOTP(true)
		prompter = p
	}

	mapper := NewPathMapper(watchFolder, cfg.RemoteBasePath, cfg.StripPrefix)
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
		fmt.Println("OTP: auto-confirm on first connect if clipboard has a code.")
	}
	go func() {
		if err := client.Connect(); err != nil {
			logger.Printf("[ERR] connection: %v", err)
		} else {
			logger.Printf("Connected.")
		}
	}()

	deployer := NewDeployer(client, mapper, cfg.AutoWatch, logger)

	// Start local HTTP API for editor integration (VSCode, etc.).
	// Uses the already-connected SSH session, so no re-OTP needed.
	var apiServer *APIServer
	if cfg.APIEnabled && cfg.APIPort > 0 {
		apiServer = NewAPIServer(deployer, client, logger)
		if addr, err := apiServer.Start(cfg.APIPort); err != nil {
			logger.Printf("[WARN] API server: %v", err)
		} else {
			fmt.Printf("API: http://%s\n", addr)
		}
	}
	defer func() {
		if apiServer != nil {
			apiServer.Close()
		}
	}()

	// If file paths were passed on the command line, display them and
	// start a background goroutine that uploads them once connected.
	if len(queuedFiles) > 0 {
		fmt.Printf("Queued for upload (%d):\n", len(queuedFiles))
		for _, qf := range queuedFiles {
			if qf.Exists {
				fmt.Printf("  - %s\n", qf.AbsPath)
			} else {
				fmt.Printf("  - %s [NOT FOUND]\n", qf.FilePath)
			}
		}
		fmt.Println("(will upload automatically after connection)")
		go uploadQueuedFiles(client, deployer, queuedFiles, logger, 5*time.Minute)
	}

	watcher, err := NewFileWatcher(
		watchFolder, matcher, cfg.FileExtensions,
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
	fmt.Printf("Watching: %s\n", watchFolder)
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
