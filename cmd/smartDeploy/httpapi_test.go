package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// startTestAPIServer creates a mock-backed API server on a random port.
// Returns the base URL and a cleanup function.
func startTestAPIServer(t *testing.T, localBase string) (string, *Deployer, *mockClient, func()) {
	t.Helper()
	mc := newMockClient()
	mapper := NewPathMapper(localBase, "/remote", "")
	deployer := NewDeployer(mc, mapper, false, log.New(io.Discard, "", 0))
	api := NewAPIServer(deployer, mc, log.New(io.Discard, "", 0))

	addr, err := api.Start(0) // port 0 = auto-assign
	if err != nil {
		t.Fatalf("API start: %v", err)
	}

	// Extract the host:port from the returned address.
	baseURL := "http://" + addr

	cleanup := func() {
		api.Close()
	}
	return baseURL, deployer, mc, cleanup
}

// waitForServer polls until the API responds or times out.
func waitForServer(t *testing.T, baseURL string) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		resp, err := http.Get(baseURL + "/status")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return
			}
		}
		select {
		case <-deadline:
			t.Fatal("API server did not start in time")
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func TestAPI_Status(t *testing.T) {
	baseURL, deployer, mc, cleanup := startTestAPIServer(t, "/test")
	defer cleanup()
	waitForServer(t, baseURL)

	_ = deployer
	_ = mc

	resp, err := http.Get(baseURL + "/status")
	if err != nil {
		t.Fatalf("GET /status: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("status code = %d, want 200", resp.StatusCode)
	}

	var status StatusResponse
	json.NewDecoder(resp.Body).Decode(&status)

	if !status.Connected {
		t.Error("status should show connected (mockClient starts connected)")
	}
	if status.AutoWatch {
		t.Error("autoWatch should be off in this test")
	}
}

func TestAPI_Status_NotConnected(t *testing.T) {
	mc := newMockClient()
	mc.connected = false
	mapper := NewPathMapper("/test", "/remote", "")
	deployer := NewDeployer(mc, mapper, false, log.New(io.Discard, "", 0))
	api := NewAPIServer(deployer, mc, log.New(io.Discard, "", 0))

	addr, _ := api.Start(0)
	defer api.Close()
	baseURL := "http://" + addr
	waitForServer(t, baseURL)

	resp, err := http.Get(baseURL + "/status")
	if err != nil {
		t.Fatalf("GET /status: %v", err)
	}
	defer resp.Body.Close()

	var status StatusResponse
	json.NewDecoder(resp.Body).Decode(&status)
	if status.Connected {
		t.Error("status should show not connected")
	}
}

func TestAPI_Status_ShowsLastUpload(t *testing.T) {
	dir := t.TempDir()
	baseURL, deployer, _, cleanup := startTestAPIServer(t, dir)
	defer cleanup()
	waitForServer(t, baseURL)

	// Upload a file to populate history.
	f := filepath.Join(dir, "test.css")
	os.WriteFile(f, []byte("body{}"), 0644)
	deployer.UploadFile(f)

	resp, err := http.Get(baseURL + "/status")
	if err != nil {
		t.Fatalf("GET /status: %v", err)
	}
	defer resp.Body.Close()

	var status StatusResponse
	json.NewDecoder(resp.Body).Decode(&status)
	if status.LastUpload != "test.css" {
		t.Errorf("LastUpload = %q, want 'test.css'", status.LastUpload)
	}
}

func TestAPI_Upload_SingleFile(t *testing.T) {
	dir := t.TempDir()
	baseURL, deployer, mc, cleanup := startTestAPIServer(t, dir)
	defer cleanup()
	waitForServer(t, baseURL)

	f := filepath.Join(dir, "style.css")
	os.WriteFile(f, []byte("body{}"), 0644)

	body, _ := json.Marshal(UploadRequest{Path: f})
	resp, err := http.Post(baseURL+"/upload", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /upload: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("status code = %d, want 200", resp.StatusCode)
	}

	var result UploadResponse
	json.NewDecoder(resp.Body).Decode(&result)
	if result.Uploaded != 1 {
		t.Errorf("uploaded = %d, want 1", result.Uploaded)
	}
	if result.Failed != 0 {
		t.Errorf("failed = %d, want 0", result.Failed)
	}
	uploads := mc.getUploads()
	if len(uploads) != 1 {
		t.Fatalf("mock uploads = %d, want 1", len(uploads))
	}
	_ = deployer
}

func TestAPI_Upload_MultipleFiles(t *testing.T) {
	dir := t.TempDir()
	baseURL, _, mc, cleanup := startTestAPIServer(t, dir)
	defer cleanup()
	waitForServer(t, baseURL)

	f1 := filepath.Join(dir, "a.css")
	f2 := filepath.Join(dir, "b.css")
	os.WriteFile(f1, []byte("a"), 0644)
	os.WriteFile(f2, []byte("b"), 0644)

	body, _ := json.Marshal(UploadRequest{Paths: []string{f1, f2}})
	resp, err := http.Post(baseURL+"/upload", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /upload: %v", err)
	}
	defer resp.Body.Close()

	var result UploadResponse
	json.NewDecoder(resp.Body).Decode(&result)
	if result.Uploaded != 2 {
		t.Errorf("uploaded = %d, want 2", result.Uploaded)
	}
	uploads := mc.getUploads()
	if len(uploads) != 2 {
		t.Fatalf("mock uploads = %d, want 2", len(uploads))
	}
}

func TestAPI_Upload_NotConnected(t *testing.T) {
	dir := t.TempDir()

	mc := newMockClient()
	mc.connected = false
	mapper := NewPathMapper(dir, "/remote", "")
	deployer := NewDeployer(mc, mapper, false, log.New(io.Discard, "", 0))
	api := NewAPIServer(deployer, mc, log.New(io.Discard, "", 0))
	addr, _ := api.Start(0)
	defer api.Close()
	baseURL := "http://" + addr
	waitForServer(t, baseURL)

	f := filepath.Join(dir, "test.css")
	os.WriteFile(f, []byte("body{}"), 0644)

	body, _ := json.Marshal(UploadRequest{Path: f})
	resp, err := http.Post(baseURL+"/upload", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /upload: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status code = %d, want 503", resp.StatusCode)
	}
}

func TestAPI_Upload_NonExistentFile(t *testing.T) {
	dir := t.TempDir()
	baseURL, _, _, cleanup := startTestAPIServer(t, dir)
	defer cleanup()
	waitForServer(t, baseURL)

	body, _ := json.Marshal(UploadRequest{Path: "/nonexistent/file.css"})
	resp, err := http.Post(baseURL+"/upload", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /upload: %v", err)
	}
	defer resp.Body.Close()

	var result UploadResponse
	json.NewDecoder(resp.Body).Decode(&result)
	if result.Uploaded != 0 {
		t.Errorf("uploaded = %d, want 0", result.Uploaded)
	}
	if result.Failed != 1 {
		t.Errorf("failed = %d, want 1", result.Failed)
	}
}

func TestAPI_Upload_NoPaths(t *testing.T) {
	dir := t.TempDir()
	baseURL, _, _, cleanup := startTestAPIServer(t, dir)
	defer cleanup()
	waitForServer(t, baseURL)

	body, _ := json.Marshal(UploadRequest{})
	resp, err := http.Post(baseURL+"/upload", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /upload: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status code = %d, want 400", resp.StatusCode)
	}
}

func TestAPI_Upload_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	baseURL, _, _, cleanup := startTestAPIServer(t, dir)
	defer cleanup()
	waitForServer(t, baseURL)

	resp, err := http.Post(baseURL+"/upload", "application/json", bytes.NewReader([]byte("not json")))
	if err != nil {
		t.Fatalf("POST /upload: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status code = %d, want 400", resp.StatusCode)
	}
}

func TestAPI_Upload_RelativePath(t *testing.T) {
	dir := t.TempDir()

	mc := newMockClient()
	mapper := NewPathMapper(dir, "/remote", "")
	deployer := NewDeployer(mc, mapper, false, log.New(io.Discard, "", 0))
	api := NewAPIServer(deployer, mc, log.New(io.Discard, "", 0))
	addr, _ := api.Start(0)
	defer api.Close()
	baseURL := "http://" + addr
	waitForServer(t, baseURL)

	f := filepath.Join(dir, "rel.css")
	os.WriteFile(f, []byte("x"), 0644)

	// Change to the dir so the relative path resolves.
	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(dir)

	body, _ := json.Marshal(UploadRequest{Path: "rel.css"})
	resp, err := http.Post(baseURL+"/upload", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /upload: %v", err)
	}
	defer resp.Body.Close()

	var result UploadResponse
	json.NewDecoder(resp.Body).Decode(&result)
	if result.Uploaded != 1 {
		t.Errorf("uploaded = %d, want 1", result.Uploaded)
	}
	abs, _ := filepath.Abs("rel.css")
	uploads := mc.getUploads()
	if len(uploads) != 1 || uploads[0].Local != abs {
		t.Errorf("upload local = %v, want %q", uploads, abs)
	}
}

func TestAPI_Status_MethodNotAllowed(t *testing.T) {
	dir := t.TempDir()
	baseURL, _, _, cleanup := startTestAPIServer(t, dir)
	defer cleanup()
	waitForServer(t, baseURL)

	resp, err := http.Post(baseURL+"/status", "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		t.Fatalf("POST /status: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status code = %d, want 405", resp.StatusCode)
	}
}

func TestAPI_Upload_MethodNotAllowed(t *testing.T) {
	dir := t.TempDir()
	baseURL, _, _, cleanup := startTestAPIServer(t, dir)
	defer cleanup()
	waitForServer(t, baseURL)

	resp, err := http.Get(baseURL + "/upload")
	if err != nil {
		t.Fatalf("GET /upload: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status code = %d, want 405", resp.StatusCode)
	}
}

func TestAPI_Upload_MixedResults(t *testing.T) {
	dir := t.TempDir()
	baseURL, _, _, cleanup := startTestAPIServer(t, dir)
	defer cleanup()
	waitForServer(t, baseURL)

	goodFile := filepath.Join(dir, "exists.css")
	os.WriteFile(goodFile, []byte("body{}"), 0644)

	// Upload one good file and one nonexistent file.
	body, _ := json.Marshal(UploadRequest{Paths: []string{goodFile, "/nonexistent/missing.css"}})
	resp, err := http.Post(baseURL+"/upload", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /upload: %v", err)
	}
	defer resp.Body.Close()

	var result UploadResponse
	json.NewDecoder(resp.Body).Decode(&result)
	if result.Uploaded != 1 {
		t.Errorf("uploaded = %d, want 1", result.Uploaded)
	}
	if result.Failed != 1 {
		t.Errorf("failed = %d, want 1", result.Failed)
	}
}

func TestAPI_Start_PortInUse(t *testing.T) {
	// Start a server on port 0 to get a free port, then try to start
	// another on the same port.
	mc := newMockClient()
	mapper := NewPathMapper("/test", "/remote", "")
	deployer := NewDeployer(mc, mapper, false, log.New(io.Discard, "", 0))
	api1 := NewAPIServer(deployer, mc, log.New(io.Discard, "", 0))
	addr, err := api1.Start(0)
	if err != nil {
		t.Fatal(err)
	}
	defer api1.Close()

	// Extract the port from addr.
	_, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	api2 := NewAPIServer(deployer, mc, log.New(io.Discard, "", 0))
	_, err = api2.Start(port)
	if err == nil {
		api2.Close()
		t.Error("expected error when port is already in use")
	}
}

// --- expandPaths tests ---

func TestExpandPaths_SingleFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "test.css")
	os.WriteFile(f, []byte("body{}"), 0644)

	result := expandPaths([]string{f})
	if len(result) != 1 {
		t.Fatalf("result len = %d, want 1", len(result))
	}
	if result[0] != f {
		t.Errorf("result[0] = %q, want %q", result[0], f)
	}
}

func TestExpandPaths_Directory(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sub"), 0755)
	os.WriteFile(filepath.Join(dir, "a.css"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(dir, "b.js"), []byte("b"), 0644)
	os.WriteFile(filepath.Join(dir, "sub", "c.css"), []byte("c"), 0644)

	result := expandPaths([]string{dir})
	if len(result) != 3 {
		t.Fatalf("result len = %d, want 3", len(result))
	}

	// All results should be files, not directories.
	for _, p := range result {
		info, err := os.Stat(p)
		if err != nil {
			t.Errorf("stat %s: %v", p, err)
			continue
		}
		if info.IsDir() {
			t.Errorf("%s should be a file, not a directory", p)
		}
	}
}

func TestExpandPaths_MixedFilesAndDirs(t *testing.T) {
	root := t.TempDir()
	subdir := filepath.Join(root, "sub")
	os.MkdirAll(subdir, 0755)
	os.WriteFile(filepath.Join(subdir, "a.css"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(subdir, "b.css"), []byte("b"), 0644)

	// standalone is outside the directory so it does not get double-counted.
	standalone := filepath.Join(root, "standalone.js")
	os.WriteFile(standalone, []byte("s"), 0644)

	// Pass both a standalone file and a directory.
	result := expandPaths([]string{standalone, subdir})
	if len(result) != 3 {
		t.Fatalf("result len = %d, want 3 (standalone + sub/a.css + sub/b.css)", len(result))
	}
}

func TestExpandPaths_NonExistentPath(t *testing.T) {
	result := expandPaths([]string{"/nonexistent/file.css"})
	if len(result) != 1 {
		t.Fatalf("result len = %d, want 1 (nonexistent paths are passed through)", len(result))
	}
}

func TestExpandPaths_RelativePath(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "rel.css")
	os.WriteFile(f, []byte("x"), 0644)

	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(dir)

	result := expandPaths([]string{"rel.css"})
	if len(result) != 1 {
		t.Fatalf("result len = %d, want 1", len(result))
	}
	abs, _ := filepath.Abs("rel.css")
	if result[0] != abs {
		t.Errorf("result[0] = %q, want %q", result[0], abs)
	}
}

func TestExpandPaths_Empty(t *testing.T) {
	result := expandPaths(nil)
	if result != nil {
		t.Errorf("nil input should return nil, got %v", result)
	}
	result = expandPaths([]string{})
	if len(result) != 0 {
		t.Errorf("empty input should return empty, got %v", result)
	}
}

// --- API directory upload tests ---

func TestAPI_Upload_DirectoryExpansion(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "css"), 0755)
	os.MkdirAll(filepath.Join(dir, "js"), 0755)
	os.WriteFile(filepath.Join(dir, "css", "style.css"), []byte("body{}"), 0644)
	os.WriteFile(filepath.Join(dir, "css", "theme.css"), []byte("body{}"), 0644)
	os.WriteFile(filepath.Join(dir, "js", "app.js"), []byte("console.log(1)"), 0644)

	mc := newMockClient()
	mapper := NewPathMapper(dir, "/remote", "")
	deployer := NewDeployer(mc, mapper, false, log.New(io.Discard, "", 0))
	api := NewAPIServer(deployer, mc, log.New(io.Discard, "", 0))
	addr, _ := api.Start(0)
	defer api.Close()
	baseURL := "http://" + addr
	waitForServer(t, baseURL)

	// Upload an entire directory.
	body, _ := json.Marshal(UploadRequest{Path: dir})
	resp, err := http.Post(baseURL+"/upload", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /upload: %v", err)
	}
	defer resp.Body.Close()

	var result UploadResponse
	json.NewDecoder(resp.Body).Decode(&result)
	if result.Uploaded != 3 {
		t.Errorf("uploaded = %d, want 3 (all files in directory tree)", result.Uploaded)
	}
	if result.Failed != 0 {
		t.Errorf("failed = %d, want 0", result.Failed)
	}
}

func TestAPI_Upload_MultiFileBatch(t *testing.T) {
	dir := t.TempDir()
	baseURL, _, mc, cleanup := startTestAPIServer(t, dir)
	defer cleanup()
	waitForServer(t, baseURL)

	// Create 5 files and upload them all in one request.
	paths := make([]string, 5)
	for i := 0; i < 5; i++ {
		paths[i] = filepath.Join(dir, fmt.Sprintf("file%d.css", i))
		os.WriteFile(paths[i], []byte(fmt.Sprintf("content %d", i)), 0644)
	}

	body, _ := json.Marshal(UploadRequest{Paths: paths})
	resp, err := http.Post(baseURL+"/upload", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /upload: %v", err)
	}
	defer resp.Body.Close()

	var result UploadResponse
	json.NewDecoder(resp.Body).Decode(&result)
	if result.Uploaded != 5 {
		t.Errorf("uploaded = %d, want 5", result.Uploaded)
	}
	if result.Failed != 0 {
		t.Errorf("failed = %d, want 0", result.Failed)
	}
	uploads := mc.getUploads()
	if len(uploads) != 5 {
		t.Errorf("mock uploads = %d, want 5", len(uploads))
	}
}

func TestAPI_Upload_MixedDirAndFile(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sub"), 0755)
	os.WriteFile(filepath.Join(dir, "top.css"), []byte("top"), 0644)
	os.WriteFile(filepath.Join(dir, "sub", "nested.css"), []byte("nested"), 0644)

	mc := newMockClient()
	mapper := NewPathMapper(dir, "/remote", "")
	deployer := NewDeployer(mc, mapper, false, log.New(io.Discard, "", 0))
	api := NewAPIServer(deployer, mc, log.New(io.Discard, "", 0))
	addr, _ := api.Start(0)
	defer api.Close()
	baseURL := "http://" + addr
	waitForServer(t, baseURL)

	// Pass both a directory and a standalone file.
	body, _ := json.Marshal(UploadRequest{Paths: []string{
		filepath.Join(dir, "sub"),
		filepath.Join(dir, "top.css"),
	}})
	resp, err := http.Post(baseURL+"/upload", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /upload: %v", err)
	}
	defer resp.Body.Close()

	var result UploadResponse
	json.NewDecoder(resp.Body).Decode(&result)
	if result.Uploaded != 2 {
		t.Errorf("uploaded = %d, want 2 (sub/nested.css + top.css)", result.Uploaded)
	}
}
