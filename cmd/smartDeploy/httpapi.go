package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// UploadRequest is the JSON body for POST /upload.
type UploadRequest struct {
	Path  string   `json:"path"`
	Paths []string `json:"paths"`
}

// UploadResult records the outcome for a single file in a batch upload.
type UploadResult struct {
	LocalPath  string `json:"localPath"`
	RemotePath string `json:"remotePath,omitempty"`
	Success    bool   `json:"success"`
	Error      string `json:"error,omitempty"`
}

// UploadResponse is the JSON response for POST /upload.
type UploadResponse struct {
	Uploaded int            `json:"uploaded"`
	Failed   int            `json:"failed"`
	Results  []UploadResult `json:"results"`
}

// StatusResponse is the JSON response for GET /status.
type StatusResponse struct {
	Connected  bool   `json:"connected"`
	AutoWatch  bool   `json:"autoWatch"`
	Pending    int    `json:"pending"`
	LastUpload string `json:"lastUpload,omitempty"`
}

// APIServer is a lightweight HTTP server that exposes the Deployer to
// external tools (VSCode extension, scripts, etc.) over localhost.
// It uses the already-connected SSH session, so no re-OTP is needed.
type APIServer struct {
	deployer     *Deployer
	client       RemoteClient
	logger       *log.Logger
	server       *http.Server
	clearCommand string
	syncCommand  string
}

func NewAPIServer(deployer *Deployer, client RemoteClient, logger *log.Logger) *APIServer {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	return &APIServer{
		deployer: deployer,
		client:   client,
		logger:   logger,
	}
}

// SetClearCommand configures the shell command to clear the temp directory.
func (a *APIServer) SetClearCommand(cmd string) {
	a.clearCommand = cmd
}

// SetSyncCommand configures the shell command to rsync temp to webapp.
func (a *APIServer) SetSyncCommand(cmd string) {
	a.syncCommand = cmd
}

// Start binds to localhost:port and begins serving. Returns the actual
// listen address (useful when port 0 is requested for auto-assignment).
func (a *APIServer) Start(port int) (string, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("/upload", a.handleUpload)
	mux.HandleFunc("/status", a.handleStatus)
	mux.HandleFunc("/clear", a.handleClear)
	mux.HandleFunc("/sync", a.handleSync)

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return "", fmt.Errorf("api listen on port %d: %w", port, err)
	}

	a.server = &http.Server{
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
	}

	go func() {
		if err := a.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			a.logger.Printf("[API] server error: %v", err)
		}
	}()

	addr := ln.Addr().String()
	a.logger.Printf("[API] listening on %s", addr)
	return addr, nil
}

func (a *APIServer) Close() error {
	if a.server == nil {
		return nil
	}
	return a.server.Close()
}

func (a *APIServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	lastUpload := ""
	if entry, ok := a.deployer.LastUpload(); ok {
		lastUpload = filepath.Base(entry.LocalPath)
	}

	resp := StatusResponse{
		Connected:  a.client.IsConnected(),
		AutoWatch:  a.deployer.IsAutoWatch(),
		Pending:    a.deployer.PendingCount(),
		LastUpload: lastUpload,
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *APIServer) handleClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if a.clearCommand == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no clearCommand configured"})
		return
	}
	if !a.client.IsConnected() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "not connected to server"})
		return
	}
	a.logger.Printf("[CLEAR] running: %s", a.clearCommand)
	output, err := a.client.RunCommand(a.clearCommand)
	if err != nil {
		a.logger.Printf("[CLEAR] failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error(), "output": output})
		return
	}
	a.logger.Printf("[CLEAR] done")
	writeJSON(w, http.StatusOK, map[string]string{"output": output})
}

func (a *APIServer) handleSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if a.syncCommand == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no syncCommand configured"})
		return
	}
	if !a.client.IsConnected() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "not connected to server"})
		return
	}
	a.logger.Printf("[SYNC] running: %s", a.syncCommand)
	output, err := a.client.RunCommand(a.syncCommand)
	if err != nil {
		a.logger.Printf("[SYNC] failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error(), "output": output})
		return
	}
	a.logger.Printf("[SYNC] done")
	writeJSON(w, http.StatusOK, map[string]string{"output": output})
}

func (a *APIServer) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req UploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}

	// Collect file paths from both single and batch fields.
	var paths []string
	if req.Path != "" {
		paths = append(paths, req.Path)
	}
	paths = append(paths, req.Paths...)

	if len(paths) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no file paths provided"})
		return
	}

	if !a.client.IsConnected() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "not connected to server"})
		return
	}

	// Expand any directories to their contained files.
	filePaths := expandPaths(paths)

	// Upload concurrently (bounded by a semaphore) to avoid serial round-trip
	// latency when many files are sent at once. The SSH client multiplexes
	// multiple sessions over a single connection, so parallel NewSession calls
	// are safe. A modest cap keeps the jumpserver from being overwhelmed.
	const maxConcurrent = 4
	results := make([]UploadResult, len(filePaths))
	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup
	for i, p := range filePaths {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, localPath string) {
			defer wg.Done()
			defer func() { <-sem }()
			remote, err := a.deployer.UploadFile(localPath)
			if err != nil {
				results[idx] = UploadResult{
					LocalPath: localPath,
					Success:   false,
					Error:     err.Error(),
				}
				return
			}
			results[idx] = UploadResult{
				LocalPath:  localPath,
				RemotePath: remote,
				Success:    true,
			}
		}(i, p)
	}
	wg.Wait()

	resp := UploadResponse{Results: results}
	for _, r := range results {
		if r.Success {
			resp.Uploaded++
		} else {
			resp.Failed++
		}
	}
	status := http.StatusOK
	if resp.Uploaded == 0 && resp.Failed > 0 {
		status = http.StatusInternalServerError
	}
	writeJSON(w, status, resp)
}

// expandPaths takes a list of file/directory paths and returns a flat
// list of file paths only. Directories are walked recursively so the
// caller can pass a folder and upload everything inside it.
func expandPaths(paths []string) []string {
	var files []string
	for _, p := range paths {
		abs, err := filepath.Abs(strings.TrimSpace(p))
		if err != nil {
			files = append(files, p)
			continue
		}
		info, err := os.Stat(abs)
		if err != nil {
			files = append(files, abs)
			continue
		}
		if info.IsDir() {
			filepath.WalkDir(abs, func(fp string, d os.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if !d.IsDir() {
					files = append(files, fp)
				}
				return nil
			})
		} else {
			files = append(files, abs)
		}
	}
	return files
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
