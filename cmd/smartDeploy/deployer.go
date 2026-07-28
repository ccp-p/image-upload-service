package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"time"
)

// ChangeTracker tracks files that have changed but not yet been uploaded.
type ChangeTracker struct {
	mu      sync.Mutex
	pending map[string]bool
}

func NewChangeTracker() *ChangeTracker {
	return &ChangeTracker{pending: make(map[string]bool)}
}

func (t *ChangeTracker) Add(p string) {
	t.mu.Lock()
	t.pending[p] = true
	t.mu.Unlock()
}

func (t *ChangeTracker) Remove(p string) {
	t.mu.Lock()
	delete(t.pending, p)
	t.mu.Unlock()
}

func (t *ChangeTracker) List() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := make([]string, 0, len(t.pending))
	for p := range t.pending {
		result = append(result, p)
	}
	return result
}

func (t *ChangeTracker) Count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.pending)
}

func (t *ChangeTracker) Clear() {
	t.mu.Lock()
	t.pending = make(map[string]bool)
	t.mu.Unlock()
}

// UploadEntry records a single upload attempt for history display.
type UploadEntry struct {
	LocalPath  string
	RemotePath string
	Time       time.Time
	Success    bool
}

// Deployer orchestrates the file watcher, remote client, and path mapper.
type Deployer struct {
	client    RemoteClient
	mapper    *PathMapper
	tracker   *ChangeTracker
	autoWatch bool
	mu        sync.Mutex
	logger    *log.Logger

	// Upload history (most recent first, capped at maxHistory).
	historyMu  sync.Mutex
	history    []UploadEntry
	maxHistory int
}

func NewDeployer(client RemoteClient, mapper *PathMapper, autoWatch bool, logger *log.Logger) *Deployer {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	return &Deployer{
		client:     client,
		mapper:     mapper,
		tracker:    NewChangeTracker(),
		autoWatch:  autoWatch,
		logger:     logger,
		maxHistory: 20,
	}
}

// OnFileChange is called by the watcher after debounce settles.
func (d *Deployer) OnFileChange(localPath string) {
	if d.IsAutoWatch() {
		if _, err := d.UploadFile(localPath); err != nil {
			d.logger.Printf("[ERR] auto-upload %s: %v", localPath, err)
			d.tracker.Add(localPath)
		}
	} else {
		d.tracker.Add(localPath)
		d.logger.Printf("[INFO] queued: %s (pending: %d)", localPath, d.tracker.Count())
	}
}

// UploadFile maps the local path and uploads a single file.
func (d *Deployer) UploadFile(localPath string) (string, error) {
	if _, err := os.Stat(localPath); os.IsNotExist(err) {
		d.tracker.Remove(localPath)
		return "", fmt.Errorf("file does not exist: %s", localPath)
	}

	remotePath, err := d.mapper.Map(localPath)
	if err != nil {
		return "", fmt.Errorf("map path: %w", err)
	}

	uploadErr := d.client.Upload(localPath, remotePath)
	d.recordHistory(localPath, remotePath, uploadErr == nil)
	if uploadErr != nil {
		return remotePath, fmt.Errorf("upload: %w", uploadErr)
	}

	d.tracker.Remove(localPath)
	d.logger.Printf("[OK] %s -> %s", localPath, remotePath)
	return remotePath, nil
}

// recordHistory prepends an upload entry, capping at maxHistory.
func (d *Deployer) recordHistory(local, remote string, success bool) {
	d.historyMu.Lock()
	defer d.historyMu.Unlock()
	entry := UploadEntry{
		LocalPath:  local,
		RemotePath: remote,
		Time:       time.Now(),
		Success:    success,
	}
	d.history = append([]UploadEntry{entry}, d.history...)
	if len(d.history) > d.maxHistory {
		d.history = d.history[:d.maxHistory]
	}
}

// History returns a copy of recent upload entries (most recent first).
func (d *Deployer) History() []UploadEntry {
	d.historyMu.Lock()
	defer d.historyMu.Unlock()
	cp := make([]UploadEntry, len(d.history))
	copy(cp, d.history)
	return cp
}

// LastUpload returns the most recent upload entry, or false if none.
func (d *Deployer) LastUpload() (UploadEntry, bool) {
	d.historyMu.Lock()
	defer d.historyMu.Unlock()
	if len(d.history) == 0 {
		return UploadEntry{}, false
	}
	return d.history[0], true
}

// UploadAll uploads all pending files, returning success and failure counts.
func (d *Deployer) UploadAll() (success, failed int, err error) {
	files := d.tracker.List()
	for _, f := range files {
		if _, err := d.UploadFile(f); err != nil {
			d.logger.Printf("[ERR] %s: %v", f, err)
			failed++
		} else {
			success++
		}
	}
	return success, failed, nil
}

func (d *Deployer) SetAutoWatch(on bool) {
	d.mu.Lock()
	d.autoWatch = on
	d.mu.Unlock()
}

func (d *Deployer) IsAutoWatch() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.autoWatch
}

func (d *Deployer) PendingCount() int {
	return d.tracker.Count()
}

func (d *Deployer) PendingFiles() []string {
	return d.tracker.List()
}

func (d *Deployer) ClearPending() {
	d.tracker.Clear()
}
