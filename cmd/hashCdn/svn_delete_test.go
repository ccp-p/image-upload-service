package main

import (
	"os"
	"path/filepath"
	"testing"
)

// svnDeleteRecorder records every path passed to vcsSvnDelete so tests can
// assert that old hash files are announced to SVN before being removed.
type svnDeleteRecorder struct {
	paths []string
}

func (r *svnDeleteRecorder) call(filePath string, debugMode bool) {
	r.paths = append(r.paths, filePath)
}

// TestVcsSvnDelete_VarIsSwappable verifies the SVN delete hook is a swappable
// var, which is the seam the notification tests rely on.
func TestVcsSvnDelete_VarIsSwappable(t *testing.T) {
	orig := vcsSvnDelete
	called := false
	vcsSvnDelete = func(string, bool) { called = true }
	t.Cleanup(func() { vcsSvnDelete = orig })

	vcsSvnDelete("/nonexistent/file.css", false)
	if !called {
		t.Fatal("swapped vcsSvnDelete was not invoked")
	}
}

// TestFindAndDeleteOldHashFiles_NotifiesSvnDelete verifies that deleting old
// hash files notifies SVN once per old file, and never for the current hash
// file or unrelated files.
func TestFindAndDeleteOldHashFiles_NotifiesSvnDelete(t *testing.T) {
	tmpDir := t.TempDir()
	vm := NewVersionManager(Config{HashLength: 8}, false)

	os.WriteFile(filepath.Join(tmpDir, "style.aaaabbbb.css"), []byte("current"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "style.ccccdddd.css"), []byte("old"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "style.eeeeffff.css"), []byte("older"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "other.css"), []byte("unrelated"), 0644)

	rec := &svnDeleteRecorder{}
	orig := vcsSvnDelete
	vcsSvnDelete = rec.call
	t.Cleanup(func() { vcsSvnDelete = orig })

	if err := vm.findAndDeleteOldHashFiles(tmpDir, "style", ".css", "aaaabbbb"); err != nil {
		t.Fatalf("findAndDeleteOldHashFiles failed: %v", err)
	}

	if len(rec.paths) != 2 {
		t.Fatalf("expected 2 svn delete notifications, got %d: %v", len(rec.paths), rec.paths)
	}
	for _, p := range rec.paths {
		switch filepath.Base(p) {
		case "style.aaaabbbb.css", "other.css":
			t.Errorf("svn delete should not be called for %s", filepath.Base(p))
		}
	}
	if fileExists(filepath.Join(tmpDir, "style.ccccdddd.css")) || fileExists(filepath.Join(tmpDir, "style.eeeeffff.css")) {
		t.Error("old hash files were not removed from disk")
	}
	if !fileExists(filepath.Join(tmpDir, "style.aaaabbbb.css")) {
		t.Error("current hash file was deleted")
	}
}

// TestCleanHashFiles_NotifiesSvnDelete verifies that cleaning old hash files
// from the dest dir notifies SVN for the removed file only.
func TestCleanHashFiles_NotifiesSvnDelete(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "style.aaaabbbb.css"), []byte("keep"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "style.ccccdddd.css"), []byte("old"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "style.css"), []byte("base"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "other.css"), []byte("unrelated"), 0644)

	dm := &DeployManager{
		config:    DeployConfig{},
		destPath:  tmpDir,
		debugMode: false,
		cache:     loadDeployCache(filepath.Join(tmpDir, ".deploy-cache.json")),
	}

	rec := &svnDeleteRecorder{}
	orig := vcsSvnDelete
	vcsSvnDelete = rec.call
	t.Cleanup(func() { vcsSvnDelete = orig })

	deleted := dm.cleanHashFiles(filepath.Join(tmpDir, "style.css"), "style.aaaabbbb.css")
	if deleted != 1 {
		t.Fatalf("expected 1 deleted, got %d", deleted)
	}
	if len(rec.paths) != 1 {
		t.Fatalf("expected 1 svn delete notification, got %d: %v", len(rec.paths), rec.paths)
	}
	if filepath.Base(rec.paths[0]) != "style.ccccdddd.css" {
		t.Errorf("svn delete called on %s; want style.ccccdddd.css", filepath.Base(rec.paths[0]))
	}
	for _, f := range []string{"style.aaaabbbb.css", "style.css", "other.css"} {
		if !fileExists(filepath.Join(tmpDir, f)) {
			t.Errorf("%s was deleted but should have been kept", f)
		}
	}
}
