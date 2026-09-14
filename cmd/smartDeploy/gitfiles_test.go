package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParsePorcelainV1Z_Basic(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	b := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(a, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Modified a.txt (staged), untracked b.txt.
	out := "M  a.txt\x00?? b.txt\x00"
	files := parsePorcelainV1Z(out, dir)
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d: %v", len(files), files)
	}
	if files[0] != a || files[1] != b {
		t.Errorf("unexpected paths: %v", files)
	}
}

func TestParsePorcelainV1Z_Rename(t *testing.T) {
	dir := t.TempDir()
	newPath := filepath.Join(dir, "new.txt")
	if err := os.WriteFile(newPath, []byte("n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Rename: destination first, then NUL, then source. Source no longer
	// exists on disk so it is skipped.
	out := "R  new.txt\x00old.txt\x00"
	files := parsePorcelainV1Z(out, dir)
	if len(files) != 1 || files[0] != newPath {
		t.Fatalf("expected [%s], got %v", newPath, files)
	}
}

func TestParsePorcelainV1Z_DeletedSkipped(t *testing.T) {
	dir := t.TempDir()
	// Deleted file (D status) does not exist on disk - must be skipped.
	out := "D  gone.txt\x00"
	if files := parsePorcelainV1Z(out, dir); len(files) != 0 {
		t.Fatalf("expected no files, got %v", files)
	}
}

func TestParsePorcelainV1Z_DedupesAndSkipsDirs(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(sub, "x.txt")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Duplicate entry for the same file, plus a directory entry.
	out := "M  sub/x.txt\x00M  sub/x.txt\x00 M sub/\x00"
	files := parsePorcelainV1Z(out, dir)
	if len(files) != 1 || files[0] != f {
		t.Fatalf("expected [%s], got %v", f, files)
	}
}

func TestCollectGitChangedFiles_NotARepo(t *testing.T) {
	// A temp dir outside any repo: git status fails, error is returned.
	if _, err := collectGitChangedFiles(t.TempDir()); err == nil {
		t.Fatal("expected error outside a git repo")
	} else if !strings.Contains(err.Error(), "git") && !strings.Contains(err.Error(), "not a git") {
		// Acceptable either way; just log for diagnosis.
		t.Logf("error: %v", err)
	}
}
