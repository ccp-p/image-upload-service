package main

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
)

// collectGitChangedFiles returns absolute paths of files git reports as
// changed (modified, staged, added, untracked) in the repo at cwd. Deleted
// files are skipped; renames resolve to whichever path still exists.
func collectGitChangedFiles(cwd string) ([]string, error) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return nil, err
	}
	root := cwd
	if out, err := runGitCmd(gitPath, cwd, "rev-parse", "--show-toplevel"); err == nil {
		if trimmed := strings.TrimSpace(out); trimmed != "" {
			root = trimmed
		}
	}
	out, err := runGitCmd(gitPath, root, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	return parsePorcelainV1Z(out, root), nil
}

func runGitCmd(gitPath, cwd string, args ...string) (string, error) {
	cmd := exec.Command(gitPath, args...)
	cmd.Dir = cwd
	out, err := cmd.Output()
	return string(out), err
}

// parsePorcelainV1Z parses `git status --porcelain=v1 -z` output. Each entry
// is "XY <path>" NUL-separated; renames/copies carry a second NUL-separated
// path (the source) which is also considered. Files that no longer exist on
// disk are skipped.
func parsePorcelainV1Z(out, gitRoot string) []string {
	var files []string
	seen := map[string]bool{}
	tokens := strings.Split(out, "\x00")
	for i := 0; i < len(tokens); i++ {
		entry := tokens[i]
		if len(entry) < 3 {
			continue
		}
		// The primary path follows the "XY " status prefix.
		candidates := []string{entry[3:]}
		x, y := entry[0], entry[1]
		if x == 'R' || x == 'C' || y == 'R' || y == 'C' {
			i++
			if i < len(tokens) {
				candidates = append(candidates, tokens[i])
			}
		}
		for _, rel := range candidates {
			if rel == "" {
				continue
			}
			full := rel
			if !filepath.IsAbs(full) {
				full = filepath.Join(gitRoot, rel)
			}
			if seen[full] {
				continue
			}
			if st, err := os.Stat(full); err == nil && !st.IsDir() {
				seen[full] = true
				files = append(files, full)
			}
		}
	}
	return files
}

// UploadGitChangedFiles uploads every git-changed file in the repo containing
// cwd. Best-effort: failures are logged, never returned.
func UploadGitChangedFiles(cwd string, d *Deployer, logger *log.Logger) {
	files, err := collectGitChangedFiles(cwd)
	if err != nil {
		logger.Printf("[GIT] changed-file sync skipped: %v", err)
		return
	}
	if len(files) == 0 {
		logger.Printf("[GIT] no changed files to upload")
		return
	}
	logger.Printf("[GIT] uploading %d changed files...", len(files))
	const maxConcurrent = 4
	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup
	var ok, failed int32
	for _, f := range files {
		wg.Add(1)
		sem <- struct{}{}
		go func(p string) {
			defer wg.Done()
			defer func() { <-sem }()
			if _, err := d.UploadFile(p); err != nil {
				logger.Printf("[GIT] upload failed: %s: %v", p, err)
				atomic.AddInt32(&failed, 1)
				return
			}
			atomic.AddInt32(&ok, 1)
		}(f)
	}
	wg.Wait()
	logger.Printf("[GIT] changed-file sync done: %d uploaded, %d failed", ok, failed)
}
