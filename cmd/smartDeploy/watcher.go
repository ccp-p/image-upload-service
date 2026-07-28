package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// IgnoreMatcher checks file paths against a list of glob patterns.
// A path matches if any single path component matches any pattern.
type IgnoreMatcher struct {
	patterns []string
}

func NewIgnoreMatcher(patterns []string) *IgnoreMatcher {
	return &IgnoreMatcher{patterns: patterns}
}

func (m *IgnoreMatcher) Match(p string) bool {
	if m == nil || len(m.patterns) == 0 {
		return false
	}
	cleanPath := filepath.ToSlash(filepath.Clean(p))
	parts := strings.Split(cleanPath, "/")
	for _, part := range parts {
		for _, pat := range m.patterns {
			if matched, _ := filepath.Match(pat, part); matched {
				return true
			}
		}
	}
	return false
}

// debouncer coalesces rapid file-change events so that each file
// is only emitted once after a quiet period.
type debouncer struct {
	interval time.Duration
	clock    func() time.Time
	mu       sync.Mutex
	pending  map[string]time.Time
}

func newDebouncer(interval time.Duration, clock func() time.Time) *debouncer {
	if clock == nil {
		clock = time.Now
	}
	return &debouncer{
		interval: interval,
		clock:    clock,
		pending:  make(map[string]time.Time),
	}
}

func (d *debouncer) add(p string) {
	d.mu.Lock()
	d.pending[p] = d.clock()
	d.mu.Unlock()
}

func (d *debouncer) drain() []string {
	now := d.clock()
	d.mu.Lock()
	var ready []string
	for p, t := range d.pending {
		if now.Sub(t) >= d.interval {
			ready = append(ready, p)
			delete(d.pending, p)
		}
	}
	d.mu.Unlock()
	return ready
}

func (d *debouncer) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.pending)
}

// FileWatcher recursively watches a directory and calls onChange
// for each file that settles after the debounce interval.
type FileWatcher struct {
	fsw      *fsnotify.Watcher
	root     string
	matcher  *IgnoreMatcher
	exts     map[string]bool
	deb      *debouncer
	onChange func(string)

	mu     sync.Mutex
	closed bool
	stopCh chan struct{}

	// Async upload dispatch: prevents the debounce loop from blocking
	// during slow uploads. Files submitted while one is in flight are
	// coalesced into a single re-upload after the current upload finishes.
	uploadPending map[string]bool
	uploadMu      sync.Mutex
	uploadNotify  chan struct{}
}

func NewFileWatcher(root string, matcher *IgnoreMatcher, exts []string, debounce time.Duration, onChange func(string)) (*FileWatcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("create fsnotify watcher: %w", err)
	}

	extSet := make(map[string]bool)
	for _, ext := range exts {
		extSet[strings.ToLower(ext)] = true
	}

	w := &FileWatcher{
		fsw:           fsw,
		root:          filepath.Clean(root),
		matcher:       matcher,
		exts:          extSet,
		deb:           newDebouncer(debounce, nil),
		onChange:      onChange,
		stopCh:        make(chan struct{}),
		uploadPending: make(map[string]bool),
		uploadNotify:  make(chan struct{}, 1),
	}

	if err := w.addWatches(w.root); err != nil {
		fsw.Close()
		return nil, fmt.Errorf("add watches: %w", err)
	}
	return w, nil
}

func (w *FileWatcher) addWatches(dir string) error {
	return filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if w.matcher.Match(p) {
				return filepath.SkipDir
			}
			return w.fsw.Add(p)
		}
		return nil
	})
}

func (w *FileWatcher) Start() {
	go w.processEvents()
	go w.processDebounce()
	go w.processUploads()
}

func (w *FileWatcher) processEvents() {
	for {
		select {
		case <-w.stopCh:
			return
		case event, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			w.handleEvent(event)
		case <-w.fsw.Errors:
			// ignore
		}
	}
}

func (w *FileWatcher) handleEvent(event fsnotify.Event) {
	p := filepath.Clean(event.Name)

	info, err := os.Stat(p)
	if err != nil {
		return // file deleted or inaccessible
	}

	if info.IsDir() {
		if event.Op&fsnotify.Create != 0 {
			w.fsw.Add(p)
		}
		return
	}

	if w.matcher.Match(p) {
		return
	}
	if !w.extOK(p) {
		return
	}
	if event.Op&(fsnotify.Write|fsnotify.Create) == 0 {
		return
	}

	w.deb.add(p)
}

func (w *FileWatcher) extOK(p string) bool {
	if len(w.exts) == 0 {
		return true
	}
	ext := strings.ToLower(filepath.Ext(p))
	return w.exts[ext]
}

func (w *FileWatcher) processDebounce() {
	ticker := time.NewTicker(debouncePollInterval(w.deb.interval))
	defer ticker.Stop()

	for {
		select {
		case <-w.stopCh:
			return
		case <-ticker.C:
			for _, p := range w.deb.drain() {
				w.dispatchUpload(p)
			}
		}
	}
}

// dispatchUpload adds a file to the async upload queue without blocking.
// If the file is already pending, the submission is a no-op (map dedup).
func (w *FileWatcher) dispatchUpload(p string) {
	w.uploadMu.Lock()
	w.uploadPending[p] = true
	w.uploadMu.Unlock()
	select {
	case w.uploadNotify <- struct{}{}:
	default:
	}
}

// processUploads is the serial worker that drains the pending upload set
// one file at a time. It never blocks the debounce loop; if a file is
// re-added while its upload is in flight, the map entry persists and the
// file is re-uploaded exactly once after the current upload completes.
func (w *FileWatcher) processUploads() {
	for {
		select {
		case <-w.stopCh:
			return
		case <-w.uploadNotify:
			for {
				w.uploadMu.Lock()
				if len(w.uploadPending) == 0 {
					w.uploadMu.Unlock()
					break
				}
				var path string
				for p := range w.uploadPending {
					path = p
					break
				}
				delete(w.uploadPending, path)
				w.uploadMu.Unlock()

				w.onChange(path)
			}
		}
	}
}

// debouncePollInterval calculates how frequently the debouncer should be
// polled. It uses a quarter of the debounce interval (not half) to keep
// worst-case upload latency low: at 300ms debounce the poll fires every
// 75ms, so a settled file is uploaded within ~375ms instead of ~450ms.
// The minimum poll is 50ms to avoid excessive CPU on tiny intervals.
func debouncePollInterval(debounce time.Duration) time.Duration {
	poll := debounce / 4
	if poll < 50*time.Millisecond {
		poll = 50 * time.Millisecond
	}
	return poll
}

func (w *FileWatcher) Close() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	w.mu.Unlock()

	close(w.stopCh)
	return w.fsw.Close()
}
