package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// urlRe matches url(...) references, with optional single/double quotes.
// Example captures: url(../images/a.png), url('../images/a.png'), url("../a.png")
var urlRe = regexp.MustCompile(`url\(\s*['"]?([^'")]+?)['"]?\s*\)`)

type blockType int

const (
	blockRule   blockType = iota // a normal selector { ... } rule
	blockAtrule                  // @media / @font-face / @import ...
	blockComment
	blockText // trailing non-empty text without a closing brace
)

type block struct {
	typ       blockType
	selector  string
	body      string // text between { and } (empty for statement at-rules like @import)
	bodyStart int    // byte offset of body within the original css
	full      string
	start     int
	end       int
}

// urlRef is a single resolved url() reference found in a CSS file.
type urlRef struct {
	raw      string // raw url content as written in css, e.g. ../images/xdrNormal/202505/foo.png
	resolved string // absolute, cleaned filesystem path
	exists   bool
	cssFile  string
	selector string
	line     int
}

// parseBlocks splits CSS text into top-level blocks via brace matching.
// It is a deliberately small parser: it does not understand string-literal
// braces (e.g. content: "}"), which is fine for background-image heavy CSS.
func parseBlocks(css string) []block {
	var blocks []block
	i := 0
	n := len(css)
	for i < n {
		c := css[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			i++
			continue
		}
		// comment
		if c == '/' && i+1 < n && css[i+1] == '*' {
			end := strings.Index(css[i+2:], "*/")
			var e int
			if end == -1 {
				e = n
			} else {
				e = i + 2 + end + 2
			}
			blocks = append(blocks, block{typ: blockComment, full: css[i:e], start: i, end: e})
			i = e
			continue
		}
		// at-rule
		if c == '@' {
			brace := strings.Index(css[i:], "{")
			if brace == -1 {
				// statement at-rule, e.g. @import url(...) ;
				lineEnd := strings.Index(css[i:], "\n")
				var e int
				if lineEnd == -1 {
					e = n
				} else {
					e = i + lineEnd
				}
				blocks = append(blocks, block{typ: blockAtrule, selector: css[i:e], full: css[i:e], start: i, end: e})
				i = e
				continue
			}
			braceStart := i + brace
			j := matchCloseBrace(css, braceStart)
			blocks = append(blocks, block{
				typ:       blockAtrule,
				selector:  strings.TrimSpace(css[i:braceStart]),
				body:      css[braceStart+1 : j-1],
				bodyStart: braceStart + 1,
				full:      css[i:j],
				start:     i,
				end:       j,
			})
			i = j
			continue
		}
		// normal rule
		brace := strings.Index(css[i:], "{")
		if brace == -1 {
			rem := strings.TrimSpace(css[i:])
			if rem != "" {
				blocks = append(blocks, block{typ: blockText, full: css[i:n], start: i, end: n})
			}
			break
		}
		braceStart := i + brace
		j := matchCloseBrace(css, braceStart)
		blocks = append(blocks, block{
			typ:       blockRule,
			selector:  strings.TrimSpace(css[i:braceStart]),
			body:      css[braceStart+1 : j-1],
			bodyStart: braceStart + 1,
			full:      css[i:j],
			start:     i,
			end:       j,
		})
		i = j
	}
	return blocks
}

// matchCloseBrace returns the index just past the '}' that closes the '{' at open.
func matchCloseBrace(css string, open int) int {
	depth := 0
	for j := open; j < len(css); j++ {
		switch css[j] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return j + 1
			}
		}
	}
	return len(css)
}

// isLocalURL reports whether a url() payload points at a local file
// (skips http(s)://, data:, protocol-relative //, and fragments).
func isLocalURL(u string) bool {
	if u == "" {
		return false
	}
	switch {
	case strings.HasPrefix(u, "http://"), strings.HasPrefix(u, "https://"):
		return false
	case strings.HasPrefix(u, "data:"), strings.HasPrefix(u, "//"), strings.HasPrefix(u, "#"):
		return false
	}
	return true
}

// resolveURL turns a url() payload into an absolute, cleaned filesystem path,
// resolving relative paths against the directory of cssFile.
func resolveURL(cssFile, raw string) string {
	clean := strings.TrimSpace(raw)
	// trim query / fragment
	if i := strings.IndexAny(clean, "?#"); i >= 0 {
		clean = clean[:i]
	}
	if clean == "" {
		return ""
	}
	if filepath.IsAbs(clean) {
		return filepath.Clean(clean)
	}
	base := filepath.Dir(cssFile)
	return filepath.Clean(filepath.Join(base, clean))
}

// collectRefs walks every block (recursing into @media/@font-face bodies) and
// returns all local url() references with selector and line context.
func collectRefs(cssFile, css string) []urlRef {
	var refs []urlRef
	var walk func(blks []block)
	walk = func(blks []block) {
		for _, b := range blks {
			switch b.typ {
			case blockComment:
				// skip
			case blockRule:
				collectFromText(cssFile, css, b.body, b.bodyStart, b.selector, &refs)
			case blockAtrule:
				if strings.TrimSpace(b.body) == "" {
					// statement at-rule like @import url(...)
					collectFromText(cssFile, css, b.selector, b.start, b.selector, &refs)
					continue
				}
				walk(parseBlocks(b.body))
			case blockText:
				collectFromText(cssFile, css, b.full, b.start, "", &refs)
			}
		}
	}
	walk(parseBlocks(css))
	return refs
}

// collectFromText extracts local url() refs from a slice of css text.
// textBase is the byte offset of text within the full css (for line numbers).
func collectFromText(cssFile, css, text string, textBase int, selector string, refs *[]urlRef) {
	for _, m := range urlRe.FindAllStringSubmatchIndex(text, -1) {
		raw := text[m[2]:m[3]]
		if !isLocalURL(raw) {
			continue
		}
		resolved := resolveURL(cssFile, raw)
		if resolved == "" {
			continue
		}
		line := 1 + strings.Count(css[:textBase+m[2]], "\n")
		*refs = append(*refs, urlRef{
			raw:      raw,
			resolved: resolved,
			exists:   fileExists(resolved),
			cssFile:  cssFile,
			selector: selector,
			line:     line,
		})
	}
}

// ruleHasMissing reports whether a rule body references any local image
// whose file does not exist on disk. Used when rewriting CSS.
func ruleHasMissing(cssFile, body string) bool {
	for _, m := range urlRe.FindAllStringSubmatchIndex(body, -1) {
		raw := body[m[2]:m[3]]
		if !isLocalURL(raw) {
			continue
		}
		p := resolveURL(cssFile, raw)
		if p == "" {
			continue
		}
		if !fileExists(p) {
			return true
		}
	}
	return false
}

// filterBlocks rebuilds CSS, dropping rules that reference missing images.
// @media blocks whose inner rules are all removed are dropped entirely.
func filterBlocks(cssFile string, blks []block) (kept []string, removed []string) {
	for _, b := range blks {
		switch b.typ {
		case blockComment, blockText:
			kept = append(kept, b.full)
		case blockAtrule:
			if strings.TrimSpace(b.body) == "" {
				kept = append(kept, b.full)
				continue
			}
			innerKept, innerRemoved := filterBlocks(cssFile, parseBlocks(b.body))
			removed = append(removed, innerRemoved...)
			if len(innerKept) > 0 {
				kept = append(kept, b.selector+" {\n"+strings.Join(innerKept, "\n")+"\n}")
			}
		case blockRule:
			if ruleHasMissing(cssFile, b.body) {
				removed = append(removed, b.selector)
			} else {
				kept = append(kept, b.full)
			}
		}
	}
	return
}

// cleanCSSText rewrites css, removing rules whose referenced images are missing.
// Returns the new text, the number of removed rules, and their selectors.
func cleanCSSText(cssFile, css string) (string, int, []string) {
	kept, removed := filterBlocks(cssFile, parseBlocks(css))
	return strings.Join(kept, "\n\n"), len(removed), removed
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// hashSegmentRe matches a hashCdn-style content hash segment (4-64 hex chars),
// mirroring cmd/hashCdn's reHashInFilename. hashCdn names hashed files as
// <basename>.<hash>.<ext> (e.g. foo.88ade0f6.png). These are build artifacts
// that hashCdn itself cleans up on rebuild, so cleanUnused always excludes them
// from orphan detection.
var hashSegmentRe = regexp.MustCompile(`^[a-f0-9]{4,64}$`)

// isHashImageFileName reports whether name is a hash-version file:
// <basename>.<hex>.<ext>, e.g. foo.88ade0f6.png. Ext-agnostic so custom
// --ext values are covered too.
func isHashImageFileName(name string) bool {
	ext := filepath.Ext(name)
	if ext == "" {
		return false
	}
	stem := strings.TrimSuffix(name, ext)
	dot := strings.LastIndex(stem, ".")
	if dot <= 0 {
		return false
	}
	return hashSegmentRe.MatchString(stem[dot+1:])
}
