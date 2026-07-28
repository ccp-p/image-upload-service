package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// --- IgnoreMatcher tests ---

func TestIgnoreMatch_ExactDir(t *testing.T) {
	m := NewIgnoreMatcher([]string{".git", "node_modules"})
	if !m.Match("/project/app/.git/config") {
		t.Error("should match .git in path")
	}
	if !m.Match("/project/app/node_modules/lib/index.js") {
		t.Error("should match node_modules in path")
	}
}

func TestIgnoreMatch_WildcardExtension(t *testing.T) {
	m := NewIgnoreMatcher([]string{"*.log", "*.tmp"})
	if !m.Match("/project/app/debug.log") {
		t.Error("should match *.log")
	}
	if !m.Match("/project/app/cache.tmp") {
		t.Error("should match *.tmp")
	}
}

func TestIgnoreMatch_ExactFile(t *testing.T) {
	m := NewIgnoreMatcher([]string{"Thumbs.db", ".DS_Store"})
	if !m.Match("/project/app/Thumbs.db") {
		t.Error("should match Thumbs.db")
	}
	if !m.Match("/project/app/sub/.DS_Store") {
		t.Error("should match .DS_Store")
	}
}

func TestIgnoreMatch_NoMatch(t *testing.T) {
	m := NewIgnoreMatcher([]string{".git", "*.log"})
	if m.Match("/project/app/src/main.go") {
		t.Error("should not match main.go")
	}
	if m.Match("/project/app/css/style.css") {
		t.Error("should not match style.css")
	}
}

func TestIgnoreMatch_EmptyPatterns(t *testing.T) {
	m := NewIgnoreMatcher(nil)
	if m.Match("/any/path") {
		t.Error("nil patterns should not match")
	}
	m2 := NewIgnoreMatcher([]string{})
	if m2.Match("/any/path") {
		t.Error("empty patterns should not match")
	}
}

func TestIgnoreMatch_NestedPath(t *testing.T) {
	m := NewIgnoreMatcher([]string{"target"})
	if !m.Match("/project/app/src/target/file.class") {
		t.Error("should match target dir at any depth")
	}
	if m.Match("/project/app/src/main/file.go") {
		t.Error("should not match when no ignore component")
	}
}

func TestIgnoreMatch_BackslashPath(t *testing.T) {
	m := NewIgnoreMatcher([]string{".git"})
	if !m.Match(`C:\project\app\.git\config`) {
		t.Error("should match .git in backslash path")
	}
}

// --- debouncer tests ---

func TestDebouncer_BasicDebounce(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	d := newDebouncer(100*time.Millisecond, clock)

	d.add("/app/file.css")

	// immediately, nothing ready
	if got := d.drain(); len(got) != 0 {
		t.Errorf("drain before interval = %v, want empty", got)
	}
	if d.count() != 1 {
		t.Errorf("count = %d, want 1", d.count())
	}

	// after interval, should be ready
	now = now.Add(150 * time.Millisecond)
	got := d.drain()
	if len(got) != 1 || got[0] != "/app/file.css" {
		t.Errorf("drain = %v, want [/app/file.css]", got)
	}
	if d.count() != 0 {
		t.Errorf("count after drain = %d, want 0", d.count())
	}
}

func TestDebouncer_MultipleFiles(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	d := newDebouncer(100*time.Millisecond, clock)

	d.add("/a.css")
	d.add("/b.css")
	d.add("/c.css")

	if d.count() != 3 {
		t.Errorf("count = %d, want 3", d.count())
	}

	now = now.Add(150 * time.Millisecond)
	got := d.drain()
	if len(got) != 3 {
		t.Errorf("drain = %d items, want 3", len(got))
	}
}

func TestDebouncer_ResetOnReAdd(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	d := newDebouncer(100*time.Millisecond, clock)

	d.add("/file.css")
	now = now.Add(80 * time.Millisecond)

	// re-add before interval expires -> resets timer
	d.add("/file.css")
	now = now.Add(80 * time.Millisecond) // 160ms total since first add, 80ms since second

	// should NOT be ready yet (only 80ms since last add)
	if got := d.drain(); len(got) != 0 {
		t.Errorf("drain after re-add = %v, want empty", got)
	}

	now = now.Add(30 * time.Millisecond) // 110ms since last add
	got := d.drain()
	if len(got) != 1 {
		t.Errorf("drain after full interval = %v, want 1 item", got)
	}
}

func TestDebouncer_EmptyDrain(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	d := newDebouncer(50*time.Millisecond, clock)

	got := d.drain()
	if len(got) != 0 {
		t.Errorf("drain on empty = %v, want empty", got)
	}
}

func TestDebouncer_NilClockUsesTimeNow(t *testing.T) {
	d := newDebouncer(1*time.Millisecond, nil)
	d.add("/test.css")
	time.Sleep(20 * time.Millisecond)
	got := d.drain()
	if len(got) != 1 {
		t.Errorf("drain with nil clock = %v, want 1 item", got)
	}
}

// --- debouncePollInterval tests ---

func TestDebouncePollInterval_Quarter(t *testing.T) {
	// At 300ms debounce the poll should be 75ms (quarter)
	got := debouncePollInterval(300 * time.Millisecond)
	want := 75 * time.Millisecond
	if got != want {
		t.Errorf("poll(300ms) = %v, want %v", got, want)
	}
}

func TestDebouncePollInterval_OldDefault(t *testing.T) {
	// At the old 1500ms default the poll should be 375ms
	got := debouncePollInterval(1500 * time.Millisecond)
	want := 375 * time.Millisecond
	if got != want {
		t.Errorf("poll(1500ms) = %v, want %v", got, want)
	}
}

func TestDebouncePollInterval_MinClamp(t *testing.T) {
	// Below the 200ms threshold the poll clamps to 50ms
	got := debouncePollInterval(100 * time.Millisecond)
	want := 50 * time.Millisecond
	if got != want {
		t.Errorf("poll(100ms) = %v, want %v", got, want)
	}
}

func TestDebouncePollInterval_Zero(t *testing.T) {
	got := debouncePollInterval(0)
	want := 50 * time.Millisecond
	if got != want {
		t.Errorf("poll(0) = %v, want %v", got, want)
	}
}

// --- FileWatcher integration tests ---

func TestFileWatcher_AutoUpload(t *testing.T) {
	dir := t.TempDir()
	cssDir := filepath.Join(dir, "css")
	os.MkdirAll(cssDir, 0755)

	var mu sync.Mutex
	var changed []string
	onChange := func(p string) {
		mu.Lock()
		changed = append(changed, p)
		mu.Unlock()
	}

	matcher := NewIgnoreMatcher([]string{".git"})
	w, err := NewFileWatcher(dir, matcher, []string{".css"}, 200*time.Millisecond, onChange)
	if err != nil {
		t.Fatalf("NewFileWatcher error: %v", err)
	}
	defer w.Close()
	w.Start()

	// write a file
	cssFile := filepath.Join(cssDir, "style.css")
	if err := os.WriteFile(cssFile, []byte("body{}"), 0644); err != nil {
		t.Fatal(err)
	}

	// wait for debounce + processing
	deadline := time.After(3 * time.Second)
	for {
		mu.Lock()
		n := len(changed)
		mu.Unlock()
		if n > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timeout waiting for file change event")
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(changed) != 1 {
		t.Fatalf("got %d changes, want 1", len(changed))
	}
	// path may have different separators, compare cleaned
	got := filepath.Clean(changed[0])
	want := filepath.Clean(cssFile)
	if got != want {
		t.Errorf("changed path = %q, want %q", got, want)
	}
}

func TestFileWatcher_IgnorePattern(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, "logs")
	os.MkdirAll(logDir, 0755)

	var mu sync.Mutex
	var changed []string
	onChange := func(p string) {
		mu.Lock()
		changed = append(changed, p)
		mu.Unlock()
	}

	matcher := NewIgnoreMatcher([]string{"logs"})
	w, err := NewFileWatcher(dir, matcher, nil, 100*time.Millisecond, onChange)
	if err != nil {
		t.Fatalf("NewFileWatcher error: %v", err)
	}
	defer w.Close()
	w.Start()

	// write a file in ignored dir
	logFile := filepath.Join(logDir, "app.log")
	os.WriteFile(logFile, []byte("log"), 0644)

	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(changed) != 0 {
		t.Errorf("expected no changes for ignored dir, got %d", len(changed))
	}
}

func TestFileWatcher_ExtensionFilter(t *testing.T) {
	dir := t.TempDir()

	var mu sync.Mutex
	var changed []string
	onChange := func(p string) {
		mu.Lock()
		changed = append(changed, p)
		mu.Unlock()
	}

	matcher := NewIgnoreMatcher(nil)
	w, err := NewFileWatcher(dir, matcher, []string{".css"}, 100*time.Millisecond, onChange)
	if err != nil {
		t.Fatalf("NewFileWatcher error: %v", err)
	}
	defer w.Close()
	w.Start()

	// write a .js file (should be ignored)
	jsFile := filepath.Join(dir, "app.js")
	os.WriteFile(jsFile, []byte("console.log(1)"), 0644)

	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(changed) != 0 {
		t.Errorf("expected no changes for .js file with .css filter, got %d", len(changed))
	}
}

func TestFileWatcher_DebounceCoalescing(t *testing.T) {
	dir := t.TempDir()

	var mu sync.Mutex
	var changed []string
	onChange := func(p string) {
		mu.Lock()
		changed = append(changed, p)
		mu.Unlock()
	}

	matcher := NewIgnoreMatcher(nil)
	w, err := NewFileWatcher(dir, matcher, []string{".txt"}, 300*time.Millisecond, onChange)
	if err != nil {
		t.Fatalf("NewFileWatcher error: %v", err)
	}
	defer w.Close()
	w.Start()

	// write to the same file multiple times rapidly
	txtFile := filepath.Join(dir, "data.txt")
	for i := 0; i < 5; i++ {
		os.WriteFile(txtFile, []byte("content"), 0644)
		time.Sleep(20 * time.Millisecond)
	}

	// wait for debounce
	time.Sleep(1 * time.Second)

	mu.Lock()
	defer mu.Unlock()
	// should have been coalesced to at most 1 event
	if len(changed) > 1 {
		t.Errorf("expected at most 1 change after debounce, got %d", len(changed))
	}
}

func TestFileWatcher_NewDirWatched(t *testing.T) {
	dir := t.TempDir()

	var mu sync.Mutex
	var changed []string
	onChange := func(p string) {
		mu.Lock()
		changed = append(changed, p)
		mu.Unlock()
	}

	matcher := NewIgnoreMatcher(nil)
	w, err := NewFileWatcher(dir, matcher, []string{".css"}, 100*time.Millisecond, onChange)
	if err != nil {
		t.Fatalf("NewFileWatcher error: %v", err)
	}
	defer w.Close()
	w.Start()

	// create a new subdirectory with a file
	newDir := filepath.Join(dir, "newdir")
	os.MkdirAll(newDir, 0755)
	time.Sleep(200 * time.Millisecond) // let watcher pick up new dir

	cssFile := filepath.Join(newDir, "style.css")
	os.WriteFile(cssFile, []byte("body{}"), 0644)

	deadline := time.After(3 * time.Second)
	for {
		mu.Lock()
		n := len(changed)
		mu.Unlock()
		if n > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timeout waiting for file in new directory")
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}
}

// --- async upload dispatch tests ---

// TestFileWatcher_AsyncDispatch verifies that the debounce loop does not
// block during a slow upload. Two files are written while the first
// upload is in flight; both should be uploaded without the second being
// delayed by the first.
func TestFileWatcher_AsyncDispatch(t *testing.T) {
	dir := t.TempDir()

	var mu sync.Mutex
	var uploaded []string
	uploadDone := make(chan string, 10)

	onChange := func(p string) {
		// Simulate a slow upload.
		time.Sleep(200 * time.Millisecond)
		mu.Lock()
		uploaded = append(uploaded, filepath.Base(p))
		mu.Unlock()
		uploadDone <- p
	}

	matcher := NewIgnoreMatcher(nil)
	w, err := NewFileWatcher(dir, matcher, []string{".css"}, 100*time.Millisecond, onChange)
	if err != nil {
		t.Fatalf("NewFileWatcher error: %v", err)
	}
	defer w.Close()
	w.Start()

	// Write file A — triggers upload after debounce.
	f1 := filepath.Join(dir, "a.css")
	os.WriteFile(f1, []byte("a"), 0644)

	// Wait for debounce to fire and the first upload to start.
	time.Sleep(200 * time.Millisecond)

	// Write file B while file A is still uploading.
	f2 := filepath.Join(dir, "b.css")
	os.WriteFile(f2, []byte("b"), 0644)

	// Wait for both uploads to complete.
	deadline := time.After(5 * time.Second)
	for i := 0; i < 2; i++ {
		select {
		case <-uploadDone:
		case <-deadline:
			mu.Lock()
			t.Fatalf("timeout waiting for upload %d, uploaded: %v", i+1, uploaded)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(uploaded) != 2 {
		t.Fatalf("uploaded = %v, want 2 files", uploaded)
	}
}

// TestFileWatcher_CoalescesDuringUpload verifies that when a file is
// modified multiple times while its upload is in flight, the changes are
// coalesced into exactly one re-upload after the current upload completes.
// This is the core fix for the "double refresh" problem.
func TestFileWatcher_CoalescesDuringUpload(t *testing.T) {
	dir := t.TempDir()

	var mu sync.Mutex
	var uploads []string
	var callCount int

	// Gate controls the first upload's duration.
	gate := make(chan struct{})
	started := make(chan struct{}, 1)

	onChange := func(p string) {
		mu.Lock()
		callCount++
		n := callCount
		mu.Unlock()

		started <- struct{}{}
		if n == 1 {
			// First upload: block until gate is closed.
			<-gate
		}
		mu.Lock()
		uploads = append(uploads, p)
		mu.Unlock()
	}

	matcher := NewIgnoreMatcher(nil)
	w, err := NewFileWatcher(dir, matcher, []string{".css"}, 100*time.Millisecond, onChange)
	if err != nil {
		t.Fatalf("NewFileWatcher error: %v", err)
	}
	defer w.Close()
	w.Start()

	// Write file — triggers first upload after debounce.
	f := filepath.Join(dir, "style.css")
	os.WriteFile(f, []byte("v1"), 0644)

	// Wait for first upload to start (it blocks on gate).
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for first upload to start")
	}

	// While the first upload is blocked, modify the file 5 times.
	for i := 0; i < 5; i++ {
		os.WriteFile(f, []byte(fmt.Sprintf("v%d", i+2)), 0644)
		time.Sleep(10 * time.Millisecond)
	}

	// Wait for debounce to settle (all 5 writes coalesced).
	time.Sleep(300 * time.Millisecond)

	// Release the first upload.
	close(gate)

	// Wait for the coalesced re-upload to start.
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for coalesced re-upload")
	}

	// Allow time for any unexpected extra uploads.
	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(uploads) != 2 {
		t.Errorf("uploads = %d, want 2 (initial + 1 coalesced re-upload)", len(uploads))
	}
}
