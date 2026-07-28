package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"sync"
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

// Deployer orchestrates the file watcher, remote client, and path mapper.
type Deployer struct {
	client    RemoteClient
	mapper    *PathMapper
	tracker   *ChangeTracker
	autoWatch bool
	mu        sync.Mutex
	logger    *log.Logger
}

func NewDeployer(client RemoteClient, mapper *PathMapper, autoWatch bool, logger *log.Logger) *Deployer {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	return &Deployer{
		client:    client,
		mapper:    mapper,
		tracker:   NewChangeTracker(),
		autoWatch: autoWatch,
		logger:    logger,
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

	if err := d.client.Upload(localPath, remotePath); err != nil {
		return remotePath, fmt.Errorf("upload: %w", err)
	}

	d.tracker.Remove(localPath)
	d.logger.Printf("[OK] %s -> %s", localPath, remotePath)
	return remotePath, nil
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
