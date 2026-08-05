package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// ---------- extractHTMLReferences ----------

func TestExtractHTMLReferences_CDNAndRelative(t *testing.T) {
	html := `<html>
<head>
  <link rel="stylesheet" href="https://qqt-res.cmicrwx.cn/2016tyjf/xhmqqthy/res/wap/css/reset.css">
  <link rel="stylesheet" href="css/comm.css?09081">
</head>
<body>
  <script src="https://qqt-res.cmicrwx.cn/2016tyjf/xhmqqthy/res/wap/scripts/js/xdrNormal.9554b20f.js"></script>
  <script src="scripts/common/utils_index.js?v=202607104"></script>
  <img src="images/xdrNormal/202505/pic.png">
</body>
</html>`

	refs := extractHTMLReferences(html)
	sort.Strings(refs)

	want := []string{
		"css/comm.css?09081",
		"images/xdrNormal/202505/pic.png",
		"https://qqt-res.cmicrwx.cn/2016tyjf/xhmqqthy/res/wap/css/reset.css",
		"https://qqt-res.cmicrwx.cn/2016tyjf/xhmqqthy/res/wap/scripts/js/xdrNormal.9554b20f.js",
		"scripts/common/utils_index.js?v=202607104",
	}
	sort.Strings(want)

	if len(refs) != len(want) {
		t.Fatalf("got %d refs, want %d:\n  got=%v\n  want=%v", len(refs), len(want), refs, want)
	}
	for i, r := range refs {
		if r != want[i] {
			t.Errorf("refs[%d] = %q, want %q", i, r, want[i])
		}
	}
}

func TestExtractHTMLReferences_SkipsNonResourceRefs(t *testing.T) {
	html := `<a href="javascript:void(0)">click</a>
<a href="javascript:;">no-op</a>
<a href="#">anchor</a>
<a href="mailto:test@example.com">mail</a>
<a href="tel:+8613800138000">call</a>
<img src="data:image/png;base64,xxx">
<img src="images/real.png">
<a href="${item.pic}">tpl</a>`

	refs := extractHTMLReferences(html)

	if len(refs) != 1 {
		t.Fatalf("got %d refs %v, want 1", len(refs), refs)
	}
	if refs[0] != "images/real.png" {
		t.Errorf("refs[0] = %q, want %q", refs[0], "images/real.png")
	}
}

func TestExtractHTMLReferences_IgnoresHTMLComments(t *testing.T) {
	html := `<head>
  <!-- <link rel="stylesheet" href="css/commented.css"> -->
  <link rel="stylesheet" href="css/active.css">
</head>`

	refs := extractHTMLReferences(html)
	if len(refs) != 1 {
		t.Fatalf("got %d refs %v, want 1 (commented reference should be skipped)", len(refs), refs)
	}
	if refs[0] != "css/active.css" {
		t.Errorf("refs[0] = %q, want %q", refs[0], "css/active.css")
	}
}

func TestExtractHTMLReferences_SingleQuoteAndDoubleQuote(t *testing.T) {
	html := `<script src='scripts/a.js'></script>
<link href="css/b.css">
<img src='images/c.png'>`

	refs := extractHTMLReferences(html)
	sort.Strings(refs)
	want := []string{"css/b.css", "images/c.png", "scripts/a.js"}
	sort.Strings(want)

	if len(refs) != len(want) {
		t.Fatalf("got %d refs %v, want %d", len(refs), want, len(want))
	}
	for i, r := range refs {
		if r != want[i] {
			t.Errorf("refs[%d] = %q, want %q", i, r, want[i])
		}
	}
}

// ---------- resolveRefToDest ----------

func TestResolveRefToDest(t *testing.T) {
	destPath := `/project/res/wap`
	htmlDir := filepath.Join(destPath, "components", "lottery")
	cdn := cdnDomain

	tests := []struct {
		name       string
		ref        string
		wantCheck  bool
		wantSuffix string
	}{
		{
			name:       "CDN reference strips prefix",
			ref:        "https://qqt-res.cmicrwx.cn/2016tyjf/xhmqqthy/res/wap/css/reset.css",
			wantCheck:  true,
			wantSuffix: "css/reset.css",
		},
		{
			name:       "CDN reference with query param",
			ref:        "https://qqt-res.cmicrwx.cn/2016tyjf/xhmqqthy/res/wap/scripts/js/xdrNormal.abc123.js?v=1",
			wantCheck:  true,
			wantSuffix: "scripts/js/xdrNormal.abc123.js",
		},
		{
			name:       "relative path resolves against htmlDir",
			ref:        "scripts/common/common.js",
			wantCheck:  true,
			wantSuffix: "components/lottery/scripts/common/common.js",
		},
		{
			name:       "relative path with ./ prefix",
			ref:        "./index.css",
			wantCheck:  true,
			wantSuffix: "components/lottery/index.css",
		},
		{
			name:       "root path resolves against destPath",
			ref:        "/css/reset.css",
			wantCheck:  true,
			wantSuffix: "res/wap/css/reset.css",
		},
		{
			name:      "external http URL is skipped",
			ref:       "https://www.cmpassport.com/h5/js/jssdk.min.js",
			wantCheck: false,
		},
		{
			name:      "protocol-relative URL is skipped",
			ref:       "//res.wx.qq.com/open/js/jweixin-1.6.0.js",
			wantCheck: false,
		},
		{
			name:       "query param stripped from relative path",
			ref:        "css/comm.css?09081",
			wantCheck:  true,
			wantSuffix: "components/lottery/css/comm.css",
		},
		{
			name:       "anchor stripped",
			ref:        "page.html#section",
			wantCheck:  true,
			wantSuffix: "components/lottery/page.html",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPath, gotCheck := resolveRefToDest(tt.ref, htmlDir, destPath, cdn)
			if gotCheck != tt.wantCheck {
				t.Fatalf("shouldCheck = %v, want %v", gotCheck, tt.wantCheck)
			}
			if !tt.wantCheck {
				return
			}
			gotNorm := filepath.ToSlash(gotPath)
			if !strings.HasSuffix(gotNorm, tt.wantSuffix) {
				t.Errorf("resolved path = %q, want suffix %q", gotNorm, tt.wantSuffix)
			}
		})
	}
}

// ---------- runCheck（只校验 dest 根目录下的 xdrNormal.html）----------

func writeXdrNormal(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "xdrNormal.html"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestRunCheck_AllReferencesExist(t *testing.T) {
	dir := t.TempDir()

	html := `<html>
<head>
  <link rel="stylesheet" href="css/style.css">
  <link rel="stylesheet" href="https://qqt-res.cmicrwx.cn/2016tyjf/xhmqqthy/res/wap/css/reset.css">
</head>
<body>
  <script src="scripts/js/app.js"></script>
  <script src="https://qqt-res.cmicrwx.cn/2016tyjf/xhmqqthy/res/wap/scripts/js/lib.js"></script>
  <img src="images/logo.png">
</body>
</html>`
	writeXdrNormal(t, dir, html)

	for _, f := range []string{
		"css/style.css", "css/reset.css",
		"scripts/js/app.js", "scripts/js/lib.js",
		"images/logo.png",
	} {
		full := filepath.Join(dir, filepath.FromSlash(f))
		os.MkdirAll(filepath.Dir(full), 0755)
		os.WriteFile(full, []byte("x"), 0644)
	}

	result := runCheck(EnvConfig{DestPath: dir, Label: "test"})

	if !result.Found {
		t.Errorf("Found = false, want true; missing: %v", result.MissingFiles)
	}
	if result.TotalRefs != 5 {
		t.Errorf("TotalRefs = %d, want 5", result.TotalRefs)
	}
	if len(result.MissingFiles) != 0 {
		t.Errorf("MissingFiles = %v, want empty", result.MissingFiles)
	}
}

func TestRunCheck_MissingReference(t *testing.T) {
	dir := t.TempDir()

	html := `<html>
<head>
  <link rel="stylesheet" href="css/exists.css">
  <link rel="stylesheet" href="css/missing.css">
</head>
<body>
  <script src="scripts/js/app.js"></script>
  <script src="https://www.cmpassport.com/h5/js/external.js"></script>
</body>
</html>`
	writeXdrNormal(t, dir, html)

	os.MkdirAll(filepath.Join(dir, "css"), 0755)
	os.WriteFile(filepath.Join(dir, "css", "exists.css"), []byte("css"), 0644)
	// css/missing.css 和 scripts/js/app.js 不创建 -> 缺失

	result := runCheck(EnvConfig{DestPath: dir, Label: "test"})

	if result.Found {
		t.Error("Found = true, want false")
	}
	// 外部 URL 不计入，TotalRefs = 3
	if result.TotalRefs != 3 {
		t.Errorf("TotalRefs = %d, want 3", result.TotalRefs)
	}
	if len(result.MissingFiles) != 2 {
		t.Errorf("MissingFiles count = %d, want 2:\n  %v", len(result.MissingFiles), result.MissingFiles)
	}
}

func TestRunCheck_RelativePathResolvesAgainstDestRoot(t *testing.T) {
	dir := t.TempDir()

	// xdrNormal.html 在 dest 根目录，相对路径应相对 dest 根目录解析
	html := `<html><body>
<link href="css/style.css">
<script src="scripts/js/app.js"></script>
</body></html>`
	writeXdrNormal(t, dir, html)

	os.MkdirAll(filepath.Join(dir, "css"), 0755)
	os.WriteFile(filepath.Join(dir, "css", "style.css"), []byte("css"), 0644)
	os.MkdirAll(filepath.Join(dir, "scripts", "js"), 0755)
	os.WriteFile(filepath.Join(dir, "scripts", "js", "app.js"), []byte("js"), 0644)

	result := runCheck(EnvConfig{DestPath: dir, Label: "test"})

	if !result.Found {
		t.Errorf("Found = false, want true; missing: %v", result.MissingFiles)
	}
	if result.TotalRefs != 2 {
		t.Errorf("TotalRefs = %d, want 2", result.TotalRefs)
	}
}

func TestRunCheck_NoXdrNormalFile(t *testing.T) {
	dir := t.TempDir()
	// 不创建 xdrNormal.html

	result := runCheck(EnvConfig{DestPath: dir, Label: "test"})

	if result.Found {
		t.Error("Found = true, want false when xdrNormal.html is missing")
	}
	if result.TotalRefs != 0 {
		t.Errorf("TotalRefs = %d, want 0", result.TotalRefs)
	}
	if len(result.MissingFiles) != 1 {
		t.Errorf("MissingFiles count = %d, want 1 (the xdrNormal.html itself)", len(result.MissingFiles))
	}
}

func TestRunCheck_NonExistentDir(t *testing.T) {
	cfg := EnvConfig{DestPath: filepath.Join(t.TempDir(), "nonexistent"), Label: "test"}
	result := runCheck(cfg)

	if result.Found {
		t.Error("Found = true, want false for non-existent directory")
	}
}

func TestRunCheck_QueryParamsAndAnchorsIgnored(t *testing.T) {
	dir := t.TempDir()

	html := `<html><body>
<link href="css/style.css?v=20260804">
<script src="js/app.js#bundle">
</body></html>`
	writeXdrNormal(t, dir, html)

	os.MkdirAll(filepath.Join(dir, "css"), 0755)
	os.WriteFile(filepath.Join(dir, "css", "style.css"), []byte("css"), 0644)
	os.MkdirAll(filepath.Join(dir, "js"), 0755)
	os.WriteFile(filepath.Join(dir, "js", "app.js"), []byte("js"), 0644)

	result := runCheck(EnvConfig{DestPath: dir, Label: "test"})

	if !result.Found {
		t.Errorf("Found = false, want true; query/anchor should be stripped, missing: %v", result.MissingFiles)
	}
}

func TestRunCheck_IgnoresOtherHTMLFiles(t *testing.T) {
	dir := t.TempDir()

	// xdrNormal.html 引用存在的文件
	writeXdrNormal(t, dir, `<html><body><script src="js/app.js"></script></body></html>`)
	os.MkdirAll(filepath.Join(dir, "js"), 0755)
	os.WriteFile(filepath.Join(dir, "js", "app.js"), []byte("js"), 0644)

	// 另一个 html 引用不存在的文件，但不应被检查
	os.WriteFile(filepath.Join(dir, "other.html"),
		[]byte(`<script src="nope/missing.js"></script>`), 0644)

	result := runCheck(EnvConfig{DestPath: dir, Label: "test"})

	if !result.Found {
		t.Errorf("Found = false, want true; other.html should be ignored, missing: %v", result.MissingFiles)
	}
	if result.TotalRefs != 1 {
		t.Errorf("TotalRefs = %d, want 1 (only xdrNormal.html refs)", result.TotalRefs)
	}
}

// ---------- 既有函数的回归测试 ----------

func TestParseScheduleTime(t *testing.T) {
	tests := []struct {
		input     string
		wantHour  int
		wantMin   int
		wantError bool
	}{
		{"2130", 21, 30, false},
		{"905", 9, 5, false},
		{"0000", 0, 0, false},
		{"2359", 23, 59, false},
		{"", 0, 0, true},
		{"12345", 0, 0, true},
		{"24", 0, 0, true},
		{"2400", 0, 0, true},
		{"1261", 0, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			h, m, err := parseScheduleTime(tt.input)
			if tt.wantError {
				if err == nil {
					t.Errorf("expected error for input %q, got nil", tt.input)
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error for %q: %v", tt.input, err)
				return
			}
			if h != tt.wantHour || m != tt.wantMin {
				t.Errorf("parseScheduleTime(%q) = (%d,%d), want (%d,%d)", tt.input, h, m, tt.wantHour, tt.wantMin)
			}
		})
	}
}

func TestNowStr(t *testing.T) {
	s := nowStr()
	if len(s) != 19 {
		t.Errorf("nowStr() = %q, want 19-char format YYYY-MM-DD HH:MM:SS", s)
	}
}

func TestBuildCheckNotifyContent(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		result := CheckResult{Found: true, TotalRefs: 100}
		title, content := buildCheckNotifyContent(result, "test")

		if title == "" {
			t.Error("title should not be empty")
		}
		if !strings.Contains(content, "✅") {
			t.Errorf("content should contain success icon, got: %s", content)
		}
		if !strings.Contains(content, "100") {
			t.Errorf("content should contain TotalRefs, got: %s", content)
		}
	})

	t.Run("missing", func(t *testing.T) {
		result := CheckResult{
			Found:        false,
			TotalRefs:    50,
			MissingFiles: []string{"/path/missing.js"},
		}
		title, content := buildCheckNotifyContent(result, "test")

		if title == "" {
			t.Error("title should not be empty")
		}
		if !strings.Contains(content, "❌") {
			t.Errorf("content should contain failure icon, got: %s", content)
		}
		if !strings.Contains(content, "missing.js") {
			t.Errorf("content should contain missing file path, got: %s", content)
		}
	})
}
