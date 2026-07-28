package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func newTestDeployer(autoWatch bool) (*Deployer, *mockClient, *PathMapper) {
	mc := newMockClient()
	mapper := NewPathMapper("/project/app", "/remote/app", "src/main/webapp/res/wap")
	d := NewDeployer(mc, mapper, autoWatch, log.New(io.Discard, "", 0))
	return d, mc, mapper
}

func newTestDeployerWithDir(autoWatch bool, localBase string) (*Deployer, *mockClient, *PathMapper) {
	mc := newMockClient()
	mapper := NewPathMapper(localBase, "/remote/app", "")
	d := NewDeployer(mc, mapper, autoWatch, log.New(io.Discard, "", 0))
	return d, mc, mapper
}

// --- ChangeTracker tests ---

func TestChangeTracker_AddRemove(t *testing.T) {
	tr := NewChangeTracker()
	tr.Add("/a.css")
	tr.Add("/b.css")
	if tr.Count() != 2 {
		t.Errorf("count = %d, want 2", tr.Count())
	}
	tr.Remove("/a.css")
	if tr.Count() != 1 {
		t.Errorf("count after remove = %d, want 1", tr.Count())
	}
}

func TestChangeTracker_List(t *testing.T) {
	tr := NewChangeTracker()
	tr.Add("/x.css")
	tr.Add("/y.css")
	tr.Add("/z.css")
	list := tr.List()
	sort.Strings(list)
	want := []string{"/x.css", "/y.css", "/z.css"}
	sort.Strings(want)
	if len(list) != 3 {
		t.Fatalf("list len = %d, want 3", len(list))
	}
	for i := range want {
		if list[i] != want[i] {
			t.Errorf("list[%d] = %q, want %q", i, list[i], want[i])
		}
	}
}

func TestChangeTracker_Clear(t *testing.T) {
	tr := NewChangeTracker()
	tr.Add("/a.css")
	tr.Add("/b.css")
	tr.Clear()
	if tr.Count() != 0 {
		t.Errorf("count after clear = %d, want 0", tr.Count())
	}
}

func TestChangeTracker_DuplicateAdd(t *testing.T) {
	tr := NewChangeTracker()
	tr.Add("/a.css")
	tr.Add("/a.css")
	if tr.Count() != 1 {
		t.Errorf("count after duplicate add = %d, want 1", tr.Count())
	}
}

func TestChangeTracker_RemoveNonExistent(t *testing.T) {
	tr := NewChangeTracker()
	tr.Remove("/nonexistent")
	if tr.Count() != 0 {
		t.Errorf("count after removing nonexistent = %d, want 0", tr.Count())
	}
}

// --- Deployer tests ---

func TestDeployer_AutoWatch_UploadsImmediately(t *testing.T) {
	dir := t.TempDir()
	d, mc, _ := newTestDeployerWithDir(true, dir)

	f := filepath.Join(dir, "style.css")
	os.WriteFile(f, []byte("body{}"), 0644)

	d.OnFileChange(f)

	uploads := mc.getUploads()
	if len(uploads) != 1 {
		t.Fatalf("uploads = %d, want 1", len(uploads))
	}
	if uploads[0].Local != f {
		t.Errorf("upload local = %q, want %q", uploads[0].Local, f)
	}
}

func TestDeployer_ManualWatch_QueuesFiles(t *testing.T) {
	d, mc, _ := newTestDeployer(false)

	d.OnFileChange("/project/app/css/a.css")
	d.OnFileChange("/project/app/css/b.css")

	if len(mc.getUploads()) != 0 {
		t.Error("should not upload when autoWatch is off")
	}
	if d.PendingCount() != 2 {
		t.Errorf("pending = %d, want 2", d.PendingCount())
	}
}

func TestDeployer_UploadFile_Success(t *testing.T) {
	dir := t.TempDir()
	d, mc, _ := newTestDeployerWithDir(false, dir)

	f := filepath.Join(dir, "style.css")
	os.WriteFile(f, []byte("body{}"), 0644)

	if _, err := d.UploadFile(f); err != nil {
		t.Fatalf("UploadFile error: %v", err)
	}

	uploads := mc.getUploads()
	if len(uploads) != 1 {
		t.Fatalf("uploads = %d, want 1", len(uploads))
	}
}

func TestDeployer_UploadFile_FileNotExist(t *testing.T) {
	d, mc, _ := newTestDeployer(false)

	// add to pending first
	d.tracker.Add("/nonexistent/file.css")

	_, err := d.UploadFile("/nonexistent/file.css")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
	// should be removed from pending
	if d.PendingCount() != 0 {
		t.Errorf("pending after failed upload = %d, want 0", d.PendingCount())
	}
	// should not have uploaded
	if len(mc.getUploads()) != 0 {
		t.Error("should not upload nonexistent file")
	}
}

func TestDeployer_UploadFile_MapError(t *testing.T) {
	d, mc, _ := newTestDeployer(false)

	// path outside watch folder -> map error
	dir := t.TempDir()
	f := filepath.Join(dir, "outside.css")
	os.WriteFile(f, []byte("x"), 0644)

	_, err := d.UploadFile(f)
	if err == nil {
		t.Error("expected map error for path outside watch folder")
	}
	if len(mc.getUploads()) != 0 {
		t.Error("should not upload on map error")
	}
}

func TestDeployer_UploadAll(t *testing.T) {
	dir := t.TempDir()
	d, mc, _ := newTestDeployerWithDir(false, dir)

	f1 := filepath.Join(dir, "a.css")
	f2 := filepath.Join(dir, "b.css")
	os.WriteFile(f1, []byte("a"), 0644)
	os.WriteFile(f2, []byte("b"), 0644)

	d.tracker.Add(f1)
	d.tracker.Add(f2)

	success, failed, _ := d.UploadAll()
	if success != 2 {
		t.Errorf("success = %d, want 2", success)
	}
	if failed != 0 {
		t.Errorf("failed = %d, want 0", failed)
	}
	if len(mc.getUploads()) != 2 {
		t.Errorf("uploads = %d, want 2", len(mc.getUploads()))
	}
	if d.PendingCount() != 0 {
		t.Errorf("pending after pushall = %d, want 0", d.PendingCount())
	}
}

func TestDeployer_UploadAll_PartialFailure(t *testing.T) {
	dir := t.TempDir()
	d, mc, mapper := newTestDeployerWithDir(false, dir)

	f1 := filepath.Join(dir, "a.css")
	f2 := filepath.Join(dir, "b.css")
	os.WriteFile(f1, []byte("a"), 0644)
	os.WriteFile(f2, []byte("b"), 0644)

	// make one upload fail
	mc.uploadErr = os.ErrNotExist
	d.tracker.Add(f1)
	d.tracker.Add(f2)

	success, failed, _ := d.UploadAll()
	_ = mapper // keep mapper referenced
	if success != 0 {
		t.Errorf("success = %d, want 0", success)
	}
	if failed != 2 {
		t.Errorf("failed = %d, want 2", failed)
	}
	// failed files should remain in pending
	if d.PendingCount() != 2 {
		t.Errorf("pending after partial failure = %d, want 2", d.PendingCount())
	}
}

func TestDeployer_ToggleAutoWatch(t *testing.T) {
	d, _, _ := newTestDeployer(false)
	if d.IsAutoWatch() {
		t.Error("should start with autoWatch off")
	}
	d.SetAutoWatch(true)
	if !d.IsAutoWatch() {
		t.Error("should be on after SetAutoWatch(true)")
	}
	d.SetAutoWatch(false)
	if d.IsAutoWatch() {
		t.Error("should be off after SetAutoWatch(false)")
	}
}

func TestDeployer_AutoWatch_FailureQueued(t *testing.T) {
	d, mc, _ := newTestDeployer(true)
	mc.uploadErr = os.ErrInvalid

	dir := t.TempDir()
	f := filepath.Join(dir, "style.css")
	os.WriteFile(f, []byte("body{}"), 0644)

	d.OnFileChange(f)

	// upload failed, should be in pending for retry
	if d.PendingCount() != 1 {
		t.Errorf("pending after failed auto-upload = %d, want 1", d.PendingCount())
	}
}

func TestDeployer_ClearPending(t *testing.T) {
	d, _, _ := newTestDeployer(false)
	d.tracker.Add("/a.css")
	d.tracker.Add("/b.css")
	d.ClearPending()
	if d.PendingCount() != 0 {
		t.Errorf("pending after clear = %d, want 0", d.PendingCount())
	}
}

func TestDeployer_UploadAll_Empty(t *testing.T) {
	d, mc, _ := newTestDeployer(false)
	success, failed, _ := d.UploadAll()
	if success != 0 || failed != 0 {
		t.Errorf("uploadall on empty: success=%d failed=%d, want 0 0", success, failed)
	}
	if len(mc.getUploads()) != 0 {
		t.Error("should not upload anything")
	}
}

func TestNewDeployer_NilLogger(t *testing.T) {
	mc := newMockClient()
	mapper := NewPathMapper("/app", "/remote", "")
	d := NewDeployer(mc, mapper, true, nil)
	if d.logger == nil {
		t.Error("logger should not be nil even when passed nil")
	}
}

// --- Upload history tests ---

func TestDeployer_History_Empty(t *testing.T) {
	d, _, _ := newTestDeployer(false)
	if history := d.History(); len(history) != 0 {
		t.Errorf("history should be empty initially, got %d", len(history))
	}
	if _, ok := d.LastUpload(); ok {
		t.Error("LastUpload should return false when no history")
	}
}

func TestDeployer_History_AfterUpload(t *testing.T) {
	dir := t.TempDir()
	d, _, _ := newTestDeployerWithDir(false, dir)

	f := filepath.Join(dir, "style.css")
	os.WriteFile(f, []byte("body{}"), 0644)

	d.UploadFile(f)

	history := d.History()
	if len(history) != 1 {
		t.Fatalf("history len = %d, want 1", len(history))
	}
	if history[0].LocalPath != f {
		t.Errorf("history[0].LocalPath = %q, want %q", history[0].LocalPath, f)
	}
	if !history[0].Success {
		t.Error("history[0] should be success")
	}
}

func TestDeployer_History_MostRecentFirst(t *testing.T) {
	dir := t.TempDir()
	d, _, _ := newTestDeployerWithDir(false, dir)

	f1 := filepath.Join(dir, "a.css")
	f2 := filepath.Join(dir, "b.css")
	os.WriteFile(f1, []byte("a"), 0644)
	os.WriteFile(f2, []byte("b"), 0644)

	d.UploadFile(f1)
	d.UploadFile(f2)

	history := d.History()
	if len(history) != 2 {
		t.Fatalf("history len = %d, want 2", len(history))
	}
	if history[0].LocalPath != f2 {
		t.Errorf("history[0] should be most recent (f2), got %q", history[0].LocalPath)
	}
	if history[1].LocalPath != f1 {
		t.Errorf("history[1] should be older (f1), got %q", history[1].LocalPath)
	}
}

func TestDeployer_History_FailedUploadRecorded(t *testing.T) {
	dir := t.TempDir()
	d, mc, _ := newTestDeployerWithDir(true, dir)
	mc.uploadErr = os.ErrInvalid

	f := filepath.Join(dir, "style.css")
	os.WriteFile(f, []byte("body{}"), 0644)

	d.OnFileChange(f)

	history := d.History()
	if len(history) != 1 {
		t.Fatalf("history len = %d, want 1", len(history))
	}
	if history[0].Success {
		t.Error("failed upload should be recorded as Success=false")
	}
}

func TestDeployer_History_CappedAt20(t *testing.T) {
	dir := t.TempDir()
	d, _, _ := newTestDeployerWithDir(false, dir)

	for i := 0; i < 25; i++ {
		f := filepath.Join(dir, fmt.Sprintf("file%d.css", i))
		os.WriteFile(f, []byte("x"), 0644)
		d.UploadFile(f)
	}

	history := d.History()
	if len(history) != 20 {
		t.Errorf("history len = %d, want 20 (capped)", len(history))
	}
	// Most recent should be file24 (last uploaded).
	want := filepath.Join(dir, "file24.css")
	if history[0].LocalPath != want {
		t.Errorf("history[0] = %q, want %q", history[0].LocalPath, want)
	}
}

func TestDeployer_LastUpload(t *testing.T) {
	dir := t.TempDir()
	d, _, _ := newTestDeployerWithDir(false, dir)

	f := filepath.Join(dir, "style.css")
	os.WriteFile(f, []byte("body{}"), 0644)
	d.UploadFile(f)

	entry, ok := d.LastUpload()
	if !ok {
		t.Fatal("LastUpload should return true after upload")
	}
	if entry.LocalPath != f {
		t.Errorf("LastUpload.LocalPath = %q, want %q", entry.LocalPath, f)
	}
	if !entry.Success {
		t.Error("LastUpload should be success")
	}
}
