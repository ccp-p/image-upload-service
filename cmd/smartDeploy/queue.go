package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"log"
)

// QueuedFile holds a resolved file path awaiting upload after connection.
type QueuedFile struct {
	AbsPath  string
	FilePath string // original argument as passed
	Exists   bool
}

// resolveQueuedPaths takes raw command-line file arguments and resolves
// each to an absolute path, checking whether the file exists. Files that
// don't exist are still returned (with Exists=false) so the caller can
// warn the user — they are not silently dropped.
func resolveQueuedPaths(args []string) []QueuedFile {
	if len(args) == 0 {
		return nil
	}
	result := make([]QueuedFile, 0, len(args))
	for _, p := range args {
		abs, err := filepath.Abs(p)
		if err != nil {
			result = append(result, QueuedFile{AbsPath: p, FilePath: p, Exists: false})
			continue
		}
		_, statErr := os.Stat(abs)
		result = append(result, QueuedFile{
			AbsPath:  abs,
			FilePath: p,
			Exists:   statErr == nil,
		})
	}
	return result
}

// uploadQueuedFiles waits for the client to connect, then uploads each
// queued file in order. It runs in its own goroutine so it never blocks
// the REPL or the watcher. If connection takes too long (OTP delay),
// it times out after queueTimeout.
func uploadQueuedFiles(client RemoteClient, deployer *Deployer, queued []QueuedFile, logger *log.Logger, queueTimeout time.Duration) {
	deadline := time.After(queueTimeout)
	for {
		if client.IsConnected() {
			break
		}
		select {
		case <-deadline:
			logger.Printf("[ERR] queued uploads: connection timeout after %v", queueTimeout)
			return
		case <-time.After(500 * time.Millisecond):
		}
	}

	for _, qf := range queued {
		if !qf.Exists {
			logger.Printf("[WARN] skipping (not found): %s", qf.AbsPath)
			continue
		}
		logger.Printf("[QUEUE] Uploading: %s", qf.AbsPath)
		remote, err := deployer.UploadFile(qf.AbsPath)
		if err != nil {
			logger.Printf("[ERR] queued upload %s: %v", qf.AbsPath, err)
			continue
		}
		fmt.Printf("[QUEUE] Done: %s -> %s\n", qf.AbsPath, remote)
	}
}
