package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsLocalURL(t *testing.T) {
	cases := map[string]bool{
		"../images/a.png":         true,
		"./a.png":                 true,
		"a/b/c.jpg":               true,
		"http://x.com/a.png":      false,
		"https://x.com/a.png":     false,
		"data:image/png;base64,x": false,
		"//cdn.example.com/a.png": false,
		"#fragment":               false,
	}
	for in, want := range cases {
		if got := isLocalURL(in); got != want {
			t.Errorf("isLocalURL(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestResolveURL(t *testing.T) {
	cssFile := filepath.Join("D:", "proj", "wap", "css", "xdrNormal.css")
	cases := []struct {
		raw  string
		want string
	}{
		{"../images/xdrNormal/202505/foo.png", filepath.Join("D:", "proj", "wap", "images", "xdrNormal", "202505", "foo.png")},
		// resolveURL receives already-unquoted content (the url() regex strips quotes)
		{"../images/a.png", filepath.Join("D:", "proj", "wap", "images", "a.png")},
		{"./icons/i.svg?v=2", filepath.Join("D:", "proj", "wap", "css", "icons", "i.svg")},
	}
	for _, c := range cases {
		got := resolveURL(cssFile, c.raw)
		if got != c.want {
			t.Errorf("resolveURL(%q) = %q, want %q", c.raw, got, c.want)
		}
	}
}

func TestCollectRefsSkipsExternalAndData(t *testing.T) {
	dir := t.TempDir()
	cssPath := filepath.Join(dir, "style.css")
	// create a referenced-but-missing local image to ensure it is reported
	css := ".a { background-image: url('img/used.png'); }\n" +
		".b { background: url('https://x/remote.png'); }\n" +
		".c { background: url('data:image/png;base64,xx'); }\n" +
		".d { background-image: url('img/missing.png'); }\n"
	refs := collectRefs(cssPath, css)
	if len(refs) != 2 {
		t.Fatalf("expected 2 local refs, got %d: %+v", len(refs), refs)
	}
	for _, r := range refs {
		if !r.exists {
			continue // missing.png and used.png both absent here
		}
	}
}

func TestCleanCSSTextRemovesMissingRules(t *testing.T) {
	dir := t.TempDir()
	cssPath := filepath.Join(dir, "style.css")
	imgDir := filepath.Join(dir, "img")
	os.MkdirAll(imgDir, 0755)
	// existing image
	used := filepath.Join(imgDir, "used.png")
	os.WriteFile(used, []byte("x"), 0644)

	css := ".keep { background-image: url('img/used.png'); }\n" +
		".drop { background-image: url('img/gone.png'); }\n"
	newText, removed, selectors := cleanCSSText(cssPath, css)
	if removed != 1 {
		t.Fatalf("expected 1 removed rule, got %d", removed)
	}
	if !contains(selectors, ".drop") {
		t.Errorf("expected .drop in removed selectors, got %v", selectors)
	}
	if !strings.Contains(newText, ".keep") {
		t.Errorf("expected .keep to survive, got: %s", newText)
	}
	if strings.Contains(newText, ".drop") {
		t.Errorf("expected .drop removed, got: %s", newText)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func TestIsHashImageFileName(t *testing.T) {
	cases := map[string]bool{
		"foo.88ade0f6.png":               true, // 8-hex hash (default hashLength=8)
		"afterGetEquityPop.88ade0f6.png": true,
		"foo.abc12345.css":               true,  // ext-agnostic; css hash too
		"foo.1234567.png":                true,  // 7 hex
		"foo.png":                        false, // no hash segment
		"foo.bar.png":                    false, // bar not hex
		"level-sign-prize.png":           false, // no dot-separated hex segment
		"foo.123.png":                    false, // 3 hex < 4 floor
		"20250501.png":                   false, // whole-name, no segment
	}
	for name, want := range cases {
		if got := isHashImageFileName(name); got != want {
			t.Errorf("isHashImageFileName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestFindOrphansSkipsHashImages(t *testing.T) {
	dir := t.TempDir()
	// referenced original image
	referenced := map[string]bool{norm(filepath.Join(dir, "used.png")): true}
	mustWrite(t, filepath.Join(dir, "used.png"), "x")
	mustWrite(t, filepath.Join(dir, "orphan.png"), "x")
	// hash versions of both referenced and orphan originals
	mustWrite(t, filepath.Join(dir, "used.88ade0f6.png"), "x")
	mustWrite(t, filepath.Join(dir, "orphan.88ade0f6.png"), "x")

	orphans, hashSkipped := findOrphans([]string{dir}, referenced, []string{".png"})

	// only the original orphan is flagged; both hash images are protected
	if len(orphans) != 1 || filepath.Base(orphans[0]) != "orphan.png" {
		t.Fatalf("expected only orphan.png, got %v", orphans)
	}
	if hashSkipped != 2 {
		t.Errorf("expected 2 hash images skipped, got %d", hashSkipped)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
