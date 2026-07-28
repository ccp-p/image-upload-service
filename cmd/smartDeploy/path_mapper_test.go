package main

import (
	"path/filepath"
	"testing"
)

func TestMap_BasicPath(t *testing.T) {
	m := NewPathMapper("/project/app", "/remote/app", "")
	got, err := m.Map("/project/app/css/style.css")
	if err != nil {
		t.Fatalf("Map error: %v", err)
	}
	want := "/remote/app/css/style.css"
	if got != want {
		t.Errorf("Map = %q, want %q", got, want)
	}
}

func TestMap_WithStripPrefix(t *testing.T) {
	m := NewPathMapper("/project/app", "/remote/app", "src/main/webapp/res/wap")
	got, err := m.Map("/project/app/src/main/webapp/res/wap/css/style.css")
	if err != nil {
		t.Fatalf("Map error: %v", err)
	}
	want := "/remote/app/css/style.css"
	if got != want {
		t.Errorf("Map = %q, want %q", got, want)
	}
}

func TestMap_StripPrefix_FileAtStripBoundary(t *testing.T) {
	m := NewPathMapper("/project/app", "/remote", "src/main/webapp/res/wap")
	got, err := m.Map("/project/app/src/main/webapp/res/wap")
	if err != nil {
		t.Fatalf("Map error: %v", err)
	}
	want := "/remote"
	if got != want {
		t.Errorf("Map = %q, want %q", got, want)
	}
}

func TestMap_StripPrefix_PartialMatch(t *testing.T) {
	m := NewPathMapper("/project/app", "/remote", "src/main/webapp/res/wap")
	// "src/main/webapp/res/wap123" should NOT match strip prefix
	got, err := m.Map("/project/app/src/main/webapp/res/wap123/file.css")
	if err != nil {
		t.Fatalf("Map error: %v", err)
	}
	want := "/remote/src/main/webapp/res/wap123/file.css"
	if got != want {
		t.Errorf("Map = %q, want %q", got, want)
	}
}

func TestMap_NoStripPrefix(t *testing.T) {
	m := NewPathMapper("/project/app", "/remote/app", "")
	got, err := m.Map("/project/app/src/main/webapp/index.html")
	if err != nil {
		t.Fatalf("Map error: %v", err)
	}
	want := "/remote/app/src/main/webapp/index.html"
	if got != want {
		t.Errorf("Map = %q, want %q", got, want)
	}
}

func TestMap_OutsideWatchFolder(t *testing.T) {
	m := NewPathMapper("/project/app", "/remote", "")
	_, err := m.Map("/other/project/file.css")
	if err == nil {
		t.Error("expected error for path outside watch folder")
	}
}

func TestMap_WindowsBackslashes(t *testing.T) {
	m := NewPathMapper(`C:\project\app`, "/remote/app", "")
	got, err := m.Map(`C:\project\app\css\style.css`)
	if err != nil {
		t.Fatalf("Map error: %v", err)
	}
	want := "/remote/app/css/style.css"
	if got != want {
		t.Errorf("Map = %q, want %q", got, want)
	}
}

func TestMap_WindowsBackslashes_WithStrip(t *testing.T) {
	m := NewPathMapper(`C:\project\app`, "/remote/app", "src/main/webapp/res/wap")
	got, err := m.Map(`C:\project\app\src\main\webapp\res\wap\js\app.js`)
	if err != nil {
		t.Fatalf("Map error: %v", err)
	}
	want := "/remote/app/js/app.js"
	if got != want {
		t.Errorf("Map = %q, want %q", got, want)
	}
}

func TestMap_NestedPath(t *testing.T) {
	m := NewPathMapper("/project/app", "/remote", "")
	got, err := m.Map("/project/app/a/b/c/d/e/file.txt")
	if err != nil {
		t.Fatalf("Map error: %v", err)
	}
	want := "/remote/a/b/c/d/e/file.txt"
	if got != want {
		t.Errorf("Map = %q, want %q", got, want)
	}
}

func TestMap_EmptyStripPrefix(t *testing.T) {
	m := NewPathMapper("/app", "/remote", "")
	got, err := m.Map("/app/file.css")
	if err != nil {
		t.Fatalf("Map error: %v", err)
	}
	if got != "/remote/file.css" {
		t.Errorf("Map = %q, want /remote/file.css", got)
	}
}

func TestMap_DotStripPrefix(t *testing.T) {
	m := NewPathMapper("/app", "/remote", ".")
	got, err := m.Map("/app/file.css")
	if err != nil {
		t.Fatalf("Map error: %v", err)
	}
	if got != "/remote/file.css" {
		t.Errorf("Map = %q, want /remote/file.css", got)
	}
}

func TestMap_RemoteBaseWithoutLeadingSlash(t *testing.T) {
	m := NewPathMapper("/app", "remote/base", "")
	got, err := m.Map("/app/file.css")
	if err != nil {
		t.Fatalf("Map error: %v", err)
	}
	if !filepath.IsAbs(filepath.FromSlash(got)) == false {
		// On all platforms, remote path should start with /
	}
	if got[:1] != "/" {
		t.Errorf("remote path should start with /, got %q", got)
	}
}

func TestMap_RemoteBaseWithTrailingSlash(t *testing.T) {
	m := NewPathMapper("/app", "/remote/", "")
	got, err := m.Map("/app/file.css")
	if err != nil {
		t.Fatalf("Map error: %v", err)
	}
	// path.Clean removes trailing slash, so result is /remote/file.css
	if got != "/remote/file.css" {
		t.Errorf("Map = %q, want /remote/file.css", got)
	}
}

func TestMap_FileDirectlyInWatchFolder(t *testing.T) {
	m := NewPathMapper("/project/app", "/remote", "")
	got, err := m.Map("/project/app/index.html")
	if err != nil {
		t.Fatalf("Map error: %v", err)
	}
	if got != "/remote/index.html" {
		t.Errorf("Map = %q, want /remote/index.html", got)
	}
}

func TestMap_RelativePathCleaning(t *testing.T) {
	m := NewPathMapper("/project/app", "/remote", "")
	// path with redundant separators
	got, err := m.Map(filepath.Clean("/project/app/./css/../css/style.css"))
	if err != nil {
		t.Fatalf("Map error: %v", err)
	}
	if got != "/remote/css/style.css" {
		t.Errorf("Map = %q, want /remote/css/style.css", got)
	}
}

func TestMap_StripPrefixWithTrailingSlash(t *testing.T) {
	// stripPrefix with trailing slash should be cleaned
	m := NewPathMapper("/app", "/remote", "src/main/webapp/res/wap/")
	got, err := m.Map("/app/src/main/webapp/res/wap/js/app.js")
	if err != nil {
		t.Fatalf("Map error: %v", err)
	}
	if got != "/remote/js/app.js" {
		t.Errorf("Map = %q, want /remote/js/app.js", got)
	}
}
