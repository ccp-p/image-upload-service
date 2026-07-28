package main

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// PathMapper converts local file paths to remote upload paths.
type PathMapper struct {
	LocalBase   string
	RemoteBase  string
	StripPrefix string
}

func NewPathMapper(localBase, remoteBase, stripPrefix string) *PathMapper {
	sp := filepath.ToSlash(strings.TrimSpace(stripPrefix))
	sp = path.Clean(sp)
	return &PathMapper{
		LocalBase:   filepath.Clean(localBase),
		RemoteBase:  path.Clean(remoteBase),
		StripPrefix: sp,
	}
}

// Map converts a local absolute path to a remote upload path.
// It computes the path relative to LocalBase, optionally strips a prefix,
// then joins with RemoteBase.
func (m *PathMapper) Map(localPath string) (string, error) {
	cleanLocal := filepath.Clean(localPath)
	rel, err := filepath.Rel(m.LocalBase, cleanLocal)
	if err != nil {
		return "", fmt.Errorf("compute relative path: %w", err)
	}
	rel = filepath.ToSlash(rel)

	if strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("path %s is outside watch folder %s", localPath, m.LocalBase)
	}

	if m.StripPrefix != "" && m.StripPrefix != "." {
		strip := strings.TrimSuffix(m.StripPrefix, "/")
		if strings.HasPrefix(rel, strip+"/") {
			rel = strings.TrimPrefix(rel, strip+"/")
		} else if rel == strip {
			rel = ""
		}
	}

	remotePath := path.Join(m.RemoteBase, rel)
	if !strings.HasPrefix(remotePath, "/") {
		remotePath = "/" + remotePath
	}
	return remotePath, nil
}
