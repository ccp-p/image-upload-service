package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// --- resolveQueuedPaths tests ---

func TestResolveQueuedPaths_Empty(t *testing.T) {
	result := resolveQueuedPaths(nil)
	if result != nil {
		t.Errorf("nil args should return nil, got %v", result)
	}
	result = resolveQueuedPaths([]string{})
	if result != nil {
		t.Errorf("empty args should return nil, got %v", result)
	}
}

func TestResolveQueuedPaths_ExistingFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "test.css")
	os.WriteFile(f, []byte("body{}"), 0644)

	result := resolveQueuedPaths([]string{f})
	if len(result) != 1 {
		t.Fatalf("result len = %d, want 1", len(result))
	}
	if result[0].AbsPath != f {
		t.Errorf("AbsPath = %q, want %q", result[0].AbsPath, f)
	}
	if !result[0].Exists {
		t.Error("Exists should be true for existing file")
	}
}

func TestResolveQueuedPaths_NonExistentFile(t *testing.T) {
	result := resolveQueuedPaths([]string{"/nonexistent/file.css"})
	if len(result) != 1 {
		t.Fatalf("result len = %d, want 1", len(result))
	}
	if result[0].Exists {
		t.Error("Exists should be false for nonexistent file")
	}
}

func TestResolveQueuedPaths_RelativePath(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "style.css")
	os.WriteFile(f, []byte("body{}"), 0644)

	// Change to the temp dir so relative path resolves correctly.
	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(dir)

	result := resolveQueuedPaths([]string{"style.css"})
	if len(result) != 1 {
		t.Fatalf("result len = %d, want 1", len(result))
	}
	if result[0].AbsPath != f {
		t.Errorf("AbsPath = %q, want %q", result[0].AbsPath, f)
	}
	if !result[0].Exists {
		t.Error("Exists should be true for relative path to existing file")
	}
}

func TestResolveQueuedPaths_Mixed(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "exists.css")
	os.WriteFile(f, []byte("body{}"), 0644)

	result := resolveQueuedPaths([]string{f, "/nonexistent/missing.css"})
	if len(result) != 2 {
		t.Fatalf("result len = %d, want 2", len(result))
	}
	if !result[0].Exists {
		t.Error("first file should exist")
	}
	if result[1].Exists {
		t.Error("second file should not exist")
	}
}

func TestResolveQueuedPaths_MultipleFiles(t *testing.T) {
	dir := t.TempDir()
	f1 := filepath.Join(dir, "a.css")
	f2 := filepath.Join(dir, "b.js")
	os.WriteFile(f1, []byte("a"), 0644)
	os.WriteFile(f2, []byte("b"), 0644)

	result := resolveQueuedPaths([]string{f1, f2})
	if len(result) != 2 {
		t.Fatalf("result len = %d, want 2", len(result))
	}
	if result[0].AbsPath != f1 || result[1].AbsPath != f2 {
		t.Errorf("paths = %q, %q, want %q, %q", result[0].AbsPath, result[1].AbsPath, f1, f2)
	}
}

// --- uploadQueuedFiles tests ---

func TestUploadQueuedFiles_AfterConnection(t *testing.T) {
	rootDir := t.TempDir()
	addr, cleanup := startTestSSHServer(t, rootDir)
	defer cleanup()

	host, port, _ := net.SplitHostPort(addr)
	portInt := 22
	fmt.Sscanf(port, "%d", &portInt)
	client := NewSSHClient(host, portInt, "testuser", "testpass", "", "", 0)
	logger := log.New(io.Discard, "", 0)

	localDir := t.TempDir()
	mapper := NewPathMapper(localDir, "/remote", "")
	deployer := NewDeployer(client, mapper, false, logger)

	f := filepath.Join(localDir, "queued.css")
	os.WriteFile(f, []byte("body{}"), 0644)

	queued := []QueuedFile{
		{AbsPath: f, FilePath: f, Exists: true},
	}

	// Connect first, then call uploadQueuedFiles.
	if err := client.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	uploadQueuedFiles(client, deployer, queued, logger, 5*time.Second)

	// File should have been uploaded.
	history := deployer.History()
	if len(history) != 1 {
		t.Fatalf("history len = %d, want 1", len(history))
	}
	if history[0].LocalPath != f {
		t.Errorf("history[0].LocalPath = %q, want %q", history[0].LocalPath, f)
	}
	if !history[0].Success {
		t.Error("upload should have succeeded")
	}
}

func TestUploadQueuedFiles_SkipsNonExistent(t *testing.T) {
	rootDir := t.TempDir()
	addr, cleanup := startTestSSHServer(t, rootDir)
	defer cleanup()

	host, port, _ := net.SplitHostPort(addr)
	portInt := 22
	fmt.Sscanf(port, "%d", &portInt)
	client := NewSSHClient(host, portInt, "testuser", "testpass", "", "", 0)
	logger := log.New(io.Discard, "", 0)
	mapper := NewPathMapper("/dummy", "/remote", "")
	deployer := NewDeployer(client, mapper, false, logger)

	queued := []QueuedFile{
		{AbsPath: "/nonexistent/file.css", FilePath: "/nonexistent/file.css", Exists: false},
	}

	if err := client.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	uploadQueuedFiles(client, deployer, queued, logger, 5*time.Second)

	// No upload should have been attempted.
	history := deployer.History()
	if len(history) != 0 {
		t.Errorf("history should be empty, got %d entries", len(history))
	}
}

func TestUploadQueuedFiles_TimeoutWhenNotConnected(t *testing.T) {
	mc := newMockClient()
	mc.connected = false
	logger := log.New(io.Discard, "", 0)
	mapper := NewPathMapper("/dummy", "/remote", "")
	deployer := NewDeployer(mc, mapper, false, logger)

	queued := []QueuedFile{
		{AbsPath: "/tmp/test.css", FilePath: "/tmp/test.css", Exists: true},
	}

	// With a very short timeout and no connection, should return quickly.
	start := time.Now()
	uploadQueuedFiles(mc, deployer, queued, logger, 200*time.Millisecond)
	elapsed := time.Since(start)

	if elapsed > 1*time.Second {
		t.Errorf("should timeout quickly, took %v", elapsed)
	}
	// No upload should have happened.
	if len(mc.getUploads()) != 0 {
		t.Errorf("no uploads should have happened when not connected")
	}
}

func TestUploadQueuedFiles_MultipleFiles(t *testing.T) {
	rootDir := t.TempDir()
	addr, cleanup := startTestSSHServer(t, rootDir)
	defer cleanup()

	host, port, _ := net.SplitHostPort(addr)
	portInt := 22
	fmt.Sscanf(port, "%d", &portInt)
	client := NewSSHClient(host, portInt, "testuser", "testpass", "", "", 0)
	logger := log.New(io.Discard, "", 0)

	localDir := t.TempDir()
	mapper := NewPathMapper(localDir, "/remote", "")
	deployer := NewDeployer(client, mapper, false, logger)

	f1 := filepath.Join(localDir, "a.css")
	f2 := filepath.Join(localDir, "b.css")
	os.WriteFile(f1, []byte("a"), 0644)
	os.WriteFile(f2, []byte("b"), 0644)

	queued := []QueuedFile{
		{AbsPath: f1, FilePath: f1, Exists: true},
		{AbsPath: f2, FilePath: f2, Exists: true},
	}

	if err := client.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	uploadQueuedFiles(client, deployer, queued, logger, 5*time.Second)

	history := deployer.History()
	if len(history) != 2 {
		t.Fatalf("history len = %d, want 2", len(history))
	}
	if !history[0].Success || !history[1].Success {
		t.Error("both uploads should succeed")
	}
}
