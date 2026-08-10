package main

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"

	"github.com/tdewolff/minify/v2"
	"github.com/tdewolff/minify/v2/js"
)

// obfuscateJS minifies and mangles JavaScript source using tdewolff/minify.
// It removes comments/whitespace, renames local variables, folds dead code,
// and shortens syntax — equivalent to terser -c -m.
func obfuscateJS(content []byte) (result []byte, err error) {
	// Recover from potential parser panics on unusual JS patterns.
	defer func() {
		if r := recover(); r != nil {
			result = nil
			err = fmt.Errorf("JS minify panic: %v", r)
		}
	}()

	m := minify.New()
	m.AddFunc("text/javascript", js.Minify)

	result, err = m.Bytes("text/javascript", content)
	if err != nil {
		return nil, fmt.Errorf("JS minify failed: %w", err)
	}
	return result, nil
}

// hashFromBytes computes an MD5 hash from in-memory bytes (used when content
// is transformed before writing, so the hash reflects the final output).
func hashFromBytes(data []byte, hashLength int) string {
	h := md5.Sum(data)
	full := hex.EncodeToString(h[:])
	if hashLength > 0 && hashLength < len(full) {
		return full[:hashLength]
	}
	return full
}
