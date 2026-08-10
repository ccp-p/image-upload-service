package main

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFileExists(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "testfile_*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	if !fileExists(tmpFile.Name()) {
		t.Errorf("fileExists(%q) returned false, expected true", tmpFile.Name())
	}

	if fileExists("non_existent_file_12345.txt") {
		t.Errorf("fileExists returned true for a non-existent file")
	}
}

func TestHashFilenameFunctions(t *testing.T) {
	vm := NewVersionManager(Config{HashLength: 8}, false)

	tests := []struct {
		original    string
		hash        string
		hashedName  string
	}{
		{"style.css", "abcdef12", "style.abcdef12.css"},
		{"app.min.js", "12345678", "app.min.12345678.js"},
		{"image.png", "11112222", "image.11112222.png"},
	}

	for _, tt := range tests {
		// Test addHashToFilename
		gotAdd := vm.addHashToFilename(tt.original, tt.hash)
		if gotAdd != tt.hashedName {
			t.Errorf("addHashToFilename(%q, %q) = %q; want %q", tt.original, tt.hash, gotAdd, tt.hashedName)
		}

		// Test removeHashFromFilename
		gotRemove := vm.removeHashFromFilename(tt.hashedName)
		if gotRemove != tt.original {
			t.Errorf("removeHashFromFilename(%q) = %q; want %q", tt.hashedName, gotRemove, tt.original)
		}
	}
}

func TestCalculateFileHash(t *testing.T) {
	content := "test content for hashing"
	
	// Create a stable md5 manually to compare
	hasher := md5.New()
	hasher.Write([]byte(content))
	expectedHash := hex.EncodeToString(hasher.Sum(nil))

	tmpFile, err := os.CreateTemp("", "hash_test_*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(content); err != nil {
		t.Fatalf("Failed to write to temp file: %v", err)
	}
	tmpFile.Close()

	// Test 8 length
	vm := NewVersionManager(Config{HashLength: 8}, false)
	gotHash8, err := vm.calculateFileHash(tmpFile.Name())
	if err != nil {
		t.Fatalf("calculateFileHash failed: %v", err)
	}
	if gotHash8 != expectedHash[:8] {
		t.Errorf("calculateFileHash with length 8 = %q; want %q", gotHash8, expectedHash[:8])
	}

	// Test full length
	vmFull := NewVersionManager(Config{HashLength: 0}, false)
	gotHashFull, err := vmFull.calculateFileHash(tmpFile.Name())
	if err != nil {
		t.Fatalf("calculateFileHash failed: %v", err)
	}
	if gotHashFull != expectedHash {
		t.Errorf("calculateFileHash with full length = %q; want %q", gotHashFull, expectedHash)
	}
	
	// Test getFileHash function
	gotHashDirect, err := getFileHash(tmpFile.Name())
	if err != nil {
		t.Fatalf("getFileHash failed: %v", err)
	}
	if gotHashDirect != expectedHash {
		t.Errorf("getFileHash = %q; want %q", gotHashDirect, expectedHash)
	}
}

func TestShouldProcessComponent(t *testing.T) {
	config := Config{
		IncludeComponents: []string{"button", "modal"},
	}
	vm := NewVersionManager(config, false)

	tests := []struct {
		path     string
		expected bool
	}{
		{"/components/button/style.css", true},
		{"\\components\\modal\\script.js", true},
		{"/components/footer/style.css", false},
		{"button.js", true},
		{"header.js", false},
	}

	for _, tt := range tests {
		got := vm.shouldProcessComponent(tt.path)
		if got != tt.expected {
			t.Errorf("shouldProcessComponent(%q) = %v; want %v", tt.path, got, tt.expected)
		}
	}
}

func TestShouldExcludeFromCDN(t *testing.T) {
	config := Config{
		CDNExcludeFiles: []string{"global.css", "jquery.js"},
	}
	vm := NewVersionManager(config, false)

	tests := []struct {
		path     string
		expected bool
	}{
		{"css/global.css", true},
		{"js/jquery.js", true},
		{"global.css?v=123", true},  // URL parm test
		{"css/main.css", false},
		{"js/app.js", false},
	}

	for _, tt := range tests {
		got := vm.shouldExcludeFromCDN(tt.path)
		if got != tt.expected {
			t.Errorf("shouldExcludeFromCDN(%q) = %v; want %v", tt.path, got, tt.expected)
		}
	}
}

func TestLoadConfig(t *testing.T) {
	configData := `{
		"rootDir": "./src",
		"cdnDomain": "https://cdn.example.com",
		"hashLength": 10,
		"htmlFiles": ["index.html"],
		"excludeDirs": ["node_modules"]
	}`

	tmpFile, err := os.CreateTemp("", "config_test_*.json")
	if err != nil {
		t.Fatalf("Failed to create config file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(configData); err != nil {
		t.Fatalf("Failed to write to config file: %v", err)
	}
	tmpFile.Close()

	config, err := loadConfig(tmpFile.Name())
	if err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}

	if config.RootDir != "./src" {
		t.Errorf("expected rootDir to be './src', got %q", config.RootDir)
	}
	if config.CDNDomain != "https://cdn.example.com" {
		t.Errorf("expected cdnDomain to be 'https://cdn.example.com', got %q", config.CDNDomain)
	}
	if config.HashLength != 10 {
		t.Errorf("expected hashLength to be 10, got %d", config.HashLength)
	}
	if len(config.HTMLFiles) != 1 || config.HTMLFiles[0] != "index.html" {
		t.Errorf("expected htmlFiles to be ['index.html'], got %v", config.HTMLFiles)
	}
}

func TestUpdateCSSImageReferences(t *testing.T) {
	cssContent := `
		.bg1 { background: url('../images/bg.png'); }
		.bg2 { background-image: url("img/icon.svg?v=1"); }
	`
	tmpFile, err := os.CreateTemp("", "style_*.css")
	if err != nil {
		t.Fatalf("Failed to create CSS file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(cssContent); err != nil {
		t.Fatalf("Failed to write to CSS file: %v", err)
	}
	tmpFile.Close()

	vm := NewVersionManager(Config{}, true)
	imageMap := map[string]string{
		"../images/bg.png": "bg.abcdef12.png",
		"img/icon.svg":     "icon.12345678.svg",
	}

	err = vm.updateCSSImageReferences(tmpFile.Name(), imageMap)
	if err != nil {
		t.Fatalf("updateCSSImageReferences failed: %v", err)
	}

	updatedContent, err := os.ReadFile(tmpFile.Name())
	if err != nil {
		t.Fatalf("Failed to read updated CSS file: %v", err)
	}
	strContent := string(updatedContent)

	if !strings.Contains(strContent, "url('../images/bg.abcdef12.png')") {
		t.Errorf("Expected CSS to contain updated bg.png, got: %s", strContent)
	}
	if !strings.Contains(strContent, "url(\"img/icon.12345678.svg\")") {
		t.Errorf("Expected CSS to contain updated icon.svg, got: %s", strContent)
	}
}

func TestCollectImagesFromCSS(t *testing.T) {
	cssContent := `
		.test1 { background: url('test1.png'); }
		.test2 { background: url("test2.jpg"); }
		.test3 { background: url(http://external.com/test3.jpg); }
		.test4 { background: url(data:image/png;base64,.....); }
	`
	// Create a temp directory for the test
	tmpDir, err := os.MkdirTemp("", "css_img_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cssPath := filepath.Join(tmpDir, "style.css")
	if err := os.WriteFile(cssPath, []byte(cssContent), 0644); err != nil {
		t.Fatalf("Failed to write CSS file: %v", err)
	}

	// Create dummy image files so they technically "exist" as the function checks fileExists
	os.WriteFile(filepath.Join(tmpDir, "test1.png"), []byte("image_data"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "test2.jpg"), []byte("image_data"), 0644)

	vm := NewVersionManager(Config{}, false)
	images, err := vm.collectImagesFromCSS(cssPath)
	if err != nil {
		t.Fatalf("collectImagesFromCSS failed: %v", err)
	}

	if len(images) != 2 {
		t.Errorf("Expected to collect 2 images, got %d", len(images))
	}

	foundTest1, foundTest2 := false, false
	for _, img := range images {
		if img.OriginalPath == "test1.png" {
			foundTest1 = true
		}
		if img.OriginalPath == "test2.jpg" {
			foundTest2 = true
		}
	}

	if !foundTest1 || !foundTest2 {
		t.Errorf("Did not find expected images. Found: %v", images)
	}
}

func TestDeployCache(t *testing.T) {
	// 创建临时测试文件
	tmpDir := t.TempDir()
	srcFile := filepath.Join(tmpDir, "test.png")
	os.WriteFile(srcFile, []byte("hello world"), 0644)

	// 缓存文件路径
	cacheFile := filepath.Join(tmpDir, ".deploy-cache.json")

	// 1. 加载不存在的缓存文件，应为空
	dc := loadDeployCache(cacheFile)
	if len(dc.Files) != 0 {
		t.Errorf("Expected empty cache, got %d entries", len(dc.Files))
	}

	// 2. 第一次计算 hash（缓存未命中）
	hash1, err := dc.getCachedHash(srcFile)
	if err != nil {
		t.Fatalf("getCachedHash failed: %v", err)
	}
	if hash1 == "" {
		t.Error("Expected non-empty hash")
	}
	if len(dc.Files) != 1 {
		t.Errorf("Expected 1 cache entry, got %d", len(dc.Files))
	}

	// 3. 第二次计算（缓存命中，应返回相同 hash）
	hash2, err := dc.getCachedHash(srcFile)
	if err != nil {
		t.Fatalf("getCachedHash failed: %v", err)
	}
	if hash1 != hash2 {
		t.Errorf("Expected same hash, got %s vs %s", hash1, hash2)
	}

	// 4. 修改文件内容，modTime 变化，缓存应失效
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(srcFile, []byte("hello world modified"), 0644)
	hash3, err := dc.getCachedHash(srcFile)
	if err != nil {
		t.Fatalf("getCachedHash failed: %v", err)
	}
	if hash3 == hash1 {
		t.Error("Expected different hash after file modification")
	}

	// 5. 保存缓存并重新加载
	if err := dc.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	if !fileExists(cacheFile) {
		t.Error("Cache file was not created")
	}

	dc2 := loadDeployCache(cacheFile)
	if len(dc2.Files) != 1 {
		t.Errorf("Expected 1 entry in reloaded cache, got %d", len(dc2.Files))
	}

	// 6. 重新加载后，文件未变，应命中缓存
	hash4, err := dc2.getCachedHash(srcFile)
	if err != nil {
		t.Fatalf("getCachedHash failed: %v", err)
	}
	if hash4 != hash3 {
		t.Errorf("Expected same hash after reload, got %s vs %s", hash4, hash3)
	}

	// 7. 验证 updateCache 方法
	dstFile := filepath.Join(tmpDir, "copied.png")
	os.WriteFile(dstFile, []byte("hello world modified"), 0644)
	dstInfo, _ := os.Stat(dstFile)
	dc2.updateCache(dstFile, hash3, dstInfo.Size(), dstInfo.ModTime().UnixNano())
	if len(dc2.Files) != 2 {
		t.Errorf("Expected 2 entries after updateCache, got %d", len(dc2.Files))
	}
}

// ==================== 补齐的测试用例 ====================

func TestCopyFile(t *testing.T) {
	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "source.txt")
	dst := filepath.Join(tmpDir, "dest.txt")
	content := []byte("file copy test content")
	if err := os.WriteFile(src, content, 0644); err != nil {
		t.Fatalf("Failed to write source: %v", err)
	}
	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copyFile failed: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("Failed to read dest: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("copied content mismatch: got %q, want %q", got, content)
	}
}

func TestIsJSOrCSS(t *testing.T) {
	tests := []struct {
		filename string
		expected bool
	}{
		{"style.css", true},
		{"app.js", true},
		{"STYLE.CSS", true},
		{"APP.JS", true},
		{"app.min.js", true},
		{"image.png", false},
		{"data.json", false},
		{"noext", false},
	}
	for _, tt := range tests {
		if got := isJSOrCSS(tt.filename); got != tt.expected {
			t.Errorf("isJSOrCSS(%q) = %v; want %v", tt.filename, got, tt.expected)
		}
	}
}

func TestIsHomeEnv(t *testing.T) {
	original := os.Getenv("IS_HOME")
	defer os.Setenv("IS_HOME", original)

	os.Setenv("IS_HOME", "1")
	if !isHomeEnv() {
		t.Error("isHomeEnv() returned false with IS_HOME=1")
	}
	os.Setenv("IS_HOME", "0")
	if isHomeEnv() {
		t.Error("isHomeEnv() returned true with IS_HOME=0")
	}
	os.Setenv("IS_HOME", "")
	if isHomeEnv() {
		t.Error("isHomeEnv() returned true with IS_HOME empty")
	}
}

func TestGetRegex(t *testing.T) {
	re1 := getRegex(`^[a-f0-9]{8}$`)
	re2 := getRegex(`^[a-f0-9]{8}$`)
	// Same pattern should return the same cached regex (pointer equality)
	if re1 != re2 {
		t.Error("getRegex should return cached regex for same pattern")
	}
	if !re1.MatchString("abcdef12") {
		t.Error("regex did not match expected hex string")
	}
	if re1.MatchString("xyz12345") {
		t.Error("regex matched non-hex string")
	}
}

func TestAddHashToFilenameEdge(t *testing.T) {
	vm := NewVersionManager(Config{HashLength: 8}, false)
	tests := []struct {
		original string
		hash     string
		expected string
	}{
		{"style.css", "abcd1234", "style.abcd1234.css"},
		{"app.min.js", "aabbccdd", "app.min.aabbccdd.js"},
		// re-add hash: old hash should be replaced
		{"style.aaaabbbb.css", "ccccdddd", "style.ccccdddd.css"},
		// no extension
		{"Makefile", "aabbccdd", "Makefile.aabbccdd"},
	}
	for _, tt := range tests {
		got := vm.addHashToFilename(tt.original, tt.hash)
		if got != tt.expected {
			t.Errorf("addHashToFilename(%q, %q) = %q; want %q", tt.original, tt.hash, got, tt.expected)
		}
	}
}

func TestRemoveHashFromFilenameEdge(t *testing.T) {
	vm := NewVersionManager(Config{HashLength: 8}, false)
	tests := []struct {
		input    string
		expected string
	}{
		{"style.abcdef12.css", "style.css"},
		{"app.min.12345678.js", "app.min.js"},
		// no hash present, should return original
		{"style.css", "style.css"},
		// hash too short (< 4 hex chars), should return original
		{"style.abc.css", "style.abc.css"},
	}
	for _, tt := range tests {
		got := vm.removeHashFromFilename(tt.input)
		if got != tt.expected {
			t.Errorf("removeHashFromFilename(%q) = %q; want %q", tt.input, got, tt.expected)
		}
	}
}

func TestFindFile(t *testing.T) {
	tmpDir := t.TempDir()
	vm := NewVersionManager(Config{HashLength: 8}, false)

	// Create a plain file
	plainPath := filepath.Join(tmpDir, "style.css")
	os.WriteFile(plainPath, []byte("css"), 0644)

	// Should find the plain file directly
	if got := vm.findFile(plainPath); got != plainPath {
		t.Errorf("findFile(plain) = %q; want %q", got, plainPath)
	}

	// Create a hashed version and remove the plain one
	os.Remove(plainPath)
	hashedPath := filepath.Join(tmpDir, "style.abcd1234.css")
	os.WriteFile(hashedPath, []byte("css"), 0644)

	// Should find the hashed version when plain doesn't exist
	got := vm.findFile(plainPath)
	if got == "" {
		t.Error("findFile did not find hashed version")
	}
	if filepath.Base(got) != "style.abcd1234.css" {
		t.Errorf("findFile found %q; want style.abcd1234.css", filepath.Base(got))
	}

	// Non-existent directory
	if got := vm.findFile(filepath.Join(tmpDir, "nonexistent", "style.css")); got != "" {
		t.Errorf("findFile in nonexistent dir should return empty, got %q", got)
	}
}

func TestFindAndDeleteOldHashFiles(t *testing.T) {
	tmpDir := t.TempDir()
	vm := NewVersionManager(Config{HashLength: 8}, false)

	basename := "style"
	ext := ".css"
	currentHash := "aaaabbbb"

	// Create current hash file, an old hash file, and an unrelated file
	os.WriteFile(filepath.Join(tmpDir, "style.aaaabbbb.css"), []byte("current"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "style.ccccdddd.css"), []byte("old"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "style.eeeeffff.css"), []byte("older"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "other.css"), []byte("unrelated"), 0644)

	if err := vm.findAndDeleteOldHashFiles(tmpDir, basename, ext, currentHash); err != nil {
		t.Fatalf("findAndDeleteOldHashFiles failed: %v", err)
	}

	// Current hash file should survive
	if !fileExists(filepath.Join(tmpDir, "style.aaaabbbb.css")) {
		t.Error("current hash file was deleted")
	}
	// Old hash files should be deleted
	if fileExists(filepath.Join(tmpDir, "style.ccccdddd.css")) {
		t.Error("old hash file was not deleted")
	}
	if fileExists(filepath.Join(tmpDir, "style.eeeeffff.css")) {
		t.Error("older hash file was not deleted")
	}
	// Unrelated file should survive
	if !fileExists(filepath.Join(tmpDir, "other.css")) {
		t.Error("unrelated file was deleted")
	}
}

func TestRenameFileWithHash(t *testing.T) {
	tmpDir := t.TempDir()
	vm := NewVersionManager(Config{HashLength: 8}, false)

	srcPath := filepath.Join(tmpDir, "app.js")
	os.WriteFile(srcPath, []byte("console.log(1)"), 0644)

	info, err := vm.renameFileWithHash(srcPath)
	if err != nil {
		t.Fatalf("renameFileWithHash failed: %v", err)
	}

	// Hashed file should exist
	if !fileExists(info.HashedPath) {
		t.Errorf("hashed file not created: %s", info.HashedPath)
	}
	// Original should still exist (copy, not move)
	if !fileExists(srcPath) {
		t.Error("original file was removed")
	}
	// Hash should be 8 chars
	if len(info.Hash) != 8 {
		t.Errorf("hash length = %d; want 8", len(info.Hash))
	}
	// Hashed filename should contain the hash
	if !strings.Contains(filepath.Base(info.HashedPath), info.Hash) {
		t.Errorf("hashed filename %q does not contain hash %q", filepath.Base(info.HashedPath), info.Hash)
	}
}

func TestCollectResourcesFromHTML(t *testing.T) {
	tmpDir := t.TempDir()
	htmlContent := `<!DOCTYPE html>
<html>
<head>
	<link rel="stylesheet" href="css/index.css">
	<link rel="stylesheet" href="components/button/button.css">
	<link rel="stylesheet" href="https://cdn.example.com/components/modal/modal.css">
</head>
<body>
	<script src="js/index.js"></script>
	<script src="components/button/button.js"></script>
	<script src="https://cdn.example.com/components/modal/modal.js"></script>
</body>
</html>`
	htmlPath := filepath.Join(tmpDir, "index.html")
	os.WriteFile(htmlPath, []byte(htmlContent), 0644)

	vm := NewVersionManager(Config{
		IncludeComponents: []string{"button", "modal"},
	}, false)

	resources, err := vm.collectResourcesFromHTML(htmlPath)
	if err != nil {
		t.Fatalf("collectResourcesFromHTML failed: %v", err)
	}

	// Should collect component CSS (local + CDN with "components")
	cssFound := false
	for _, css := range resources["css"] {
		if strings.Contains(css, "button/button.css") {
			cssFound = true
		}
	}
	if !cssFound {
		t.Errorf("did not collect component button CSS, got: %v", resources["css"])
	}

	// Should collect component JS
	jsFound := false
	for _, js := range resources["js"] {
		if strings.Contains(js, "button/button.js") {
			jsFound = true
		}
	}
	if !jsFound {
		t.Errorf("did not collect component button JS, got: %v", resources["js"])
	}
}

func TestUpdateHTMLContent(t *testing.T) {
	tmpDir := t.TempDir()
	htmlContent := `<link rel="stylesheet" href="css/style.css">
<script src="js/app.js"></script>`
	htmlPath := filepath.Join(tmpDir, "index.html")
	os.WriteFile(htmlPath, []byte(htmlContent), 0644)

	// Create the actual resource files so renaming works
	os.MkdirAll(filepath.Join(tmpDir, "css"), 0755)
	os.MkdirAll(filepath.Join(tmpDir, "js"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "css", "style.css"), []byte("body{}"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "js", "app.js"), []byte("1"), 0644)

	vm := NewVersionManager(Config{HashLength: 8}, false)

	resources := map[string]map[string]string{
		"css": {"css/style.css": "css/style.aaaabbbb.css"},
		"js":  {"js/app.js": "js/app.ccccdddd.js"},
	}

	if err := vm.updateHTMLContent(htmlPath, resources); err != nil {
		t.Fatalf("updateHTMLContent failed: %v", err)
	}

	updated, _ := os.ReadFile(htmlPath)
	strContent := string(updated)
	if !strings.Contains(strContent, "style.aaaabbbb.css") {
		t.Errorf("HTML not updated with hashed CSS, got: %s", strContent)
	}
	if !strings.Contains(strContent, "app.ccccdddd.js") {
		t.Errorf("HTML not updated with hashed JS, got: %s", strContent)
	}
}

func TestFindAllHTMLFiles(t *testing.T) {
	tmpDir := t.TempDir()

	// Create HTML files in various locations
	os.WriteFile(filepath.Join(tmpDir, "index.html"), []byte("1"), 0644)
	os.MkdirAll(filepath.Join(tmpDir, "sub"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "sub", "page.html"), []byte("2"), 0644)
	// Exclude dir should be skipped
	os.MkdirAll(filepath.Join(tmpDir, "node_modules"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "node_modules", "lib.html"), []byte("3"), 0644)
	// Non-HTML file
	os.WriteFile(filepath.Join(tmpDir, "readme.txt"), []byte("4"), 0644)

	vm := NewVersionManager(Config{
		RootDir:     tmpDir,
		ExcludeDirs: []string{"node_modules"},
	}, false)

	files := vm.findAllHTMLFiles()
	if len(files) != 2 {
		t.Errorf("expected 2 HTML files, got %d: %v", len(files), files)
	}

	for _, f := range files {
		if strings.Contains(f, "node_modules") {
			t.Errorf("excluded dir not skipped: %s", f)
		}
	}
}

func TestValidateCDNResources(t *testing.T) {
	tmpDir := t.TempDir()
	cdnDomain := "https://cdn.example.com"

	htmlContent := fmt.Sprintf(`<link href="%s/css/style.css">
<script src="%s/js/app.js"></script>`, cdnDomain, cdnDomain)
	htmlPath := filepath.Join(tmpDir, "index.html")
	os.WriteFile(htmlPath, []byte(htmlContent), 0644)

	// Create dest files so validation passes
	os.MkdirAll(filepath.Join(tmpDir, "dest", "css"), 0755)
	os.MkdirAll(filepath.Join(tmpDir, "dest", "js"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "dest", "css", "style.css"), []byte("body{}"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "dest", "js", "app.js"), []byte("1"), 0644)

	dm := &DeployManager{
		config:    DeployConfig{},
		destPath:  filepath.Join(tmpDir, "dest"),
		debugMode: false,
		cache:     loadDeployCache(filepath.Join(tmpDir, ".deploy-cache.json")),
	}

	if err := dm.validateCDNResources(htmlPath, cdnDomain); err != nil {
		t.Errorf("validateCDNResources should pass when files exist, got: %v", err)
	}

	// Remove a file, validation should fail
	os.Remove(filepath.Join(tmpDir, "dest", "css", "style.css"))
	if err := dm.validateCDNResources(htmlPath, cdnDomain); err == nil {
		t.Error("validateCDNResources should fail when a file is missing")
	}
}

func TestCleanHashFiles(t *testing.T) {
	tmpDir := t.TempDir()
	os.MkdirAll(tmpDir, 0755)

	// Create files: keep, old hash, unrelated
	os.WriteFile(filepath.Join(tmpDir, "style.aaaabbbb.css"), []byte("keep"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "style.ccccdddd.css"), []byte("old"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "style.css"), []byte("base"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "other.css"), []byte("unrelated"), 0644)

	dm := &DeployManager{
		config:    DeployConfig{},
		destPath:  tmpDir,
		debugMode: false,
		cache:     loadDeployCache(filepath.Join(tmpDir, ".deploy-cache.json")),
	}

	destPath := filepath.Join(tmpDir, "style.css")
	deleted := dm.cleanHashFiles(destPath, "style.aaaabbbb.css")

	if deleted != 1 {
		t.Errorf("expected 1 deleted, got %d", deleted)
	}
	if !fileExists(filepath.Join(tmpDir, "style.aaaabbbb.css")) {
		t.Error("keep file was deleted")
	}
	if fileExists(filepath.Join(tmpDir, "style.ccccdddd.css")) {
		t.Error("old hash file was not deleted")
	}
	if !fileExists(filepath.Join(tmpDir, "style.css")) {
		t.Error("base file was deleted")
	}
	if !fileExists(filepath.Join(tmpDir, "other.css")) {
		t.Error("unrelated file was deleted")
	}
}

func TestCopyFileWithVersions(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	// Create source: base file + one hash version
	os.WriteFile(filepath.Join(srcDir, "app.js"), []byte("console.log(1)"), 0644)
	os.WriteFile(filepath.Join(srcDir, "app.aaaabbbb.js"), []byte("console.log(1)"), 0644)

	dm := &DeployManager{
		config:     DeployConfig{},
		sourcePath: srcDir,
		destPath:   dstDir,
		debugMode:  false,
		cache:      loadDeployCache(filepath.Join(dstDir, ".deploy-cache.json")),
	}

	copied, skipped, err := dm.copyFileWithVersions("app.js", filepath.Join(dstDir, "app.js"))
	if err != nil {
		t.Fatalf("copyFileWithVersions failed: %v", err)
	}
	if copied == 0 {
		t.Error("expected at least 1 file copied")
	}
	_ = skipped

	// Base file should exist in dest
	if !fileExists(filepath.Join(dstDir, "app.js")) {
		t.Error("base file not copied to dest")
	}
	// Hash version should exist in dest
	if !fileExists(filepath.Join(dstDir, "app.aaaabbbb.js")) {
		t.Error("hash file not copied to dest")
	}
}

func TestRevertSrcGitErrorsOnEmptySource(t *testing.T) {
	dm := &DeployManager{
		config:     DeployConfig{},
		sourcePath: "",
		destPath:   "",
		debugMode:  false,
		cache:      loadDeployCache(""),
	}
	err := dm.revertSrcGit()
	if err == nil {
		t.Error("empty source path should return error")
	}
}

func TestRevertSrcGitCleansWorkspace(t *testing.T) {
	// 集成测试：需要 git 可执行文件。创建临时仓库，弄脏后验证 revertSrcGit 还原工作区
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	dir := t.TempDir()

	runGit := func(args ...string) []byte {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
		return out
	}

	runGit("init")
	runGit("config", "user.name", "test")
	runGit("config", "user.email", "test@test.com")

	// 初始提交一个已跟踪文件
	os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("v1"), 0644)
	runGit("add", "tracked.txt")
	runGit("commit", "-m", "init")

	// 弄脏工作区：修改已跟踪文件 + 添加未跟踪文件和目录（模拟部署产生的 hash 文件）
	os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("modified"), 0644)
	os.WriteFile(filepath.Join(dir, "untracked.688db72b.css"), []byte("u"), 0644)
	os.MkdirAll(filepath.Join(dir, "untracked_dir"), 0755)
	os.WriteFile(filepath.Join(dir, "untracked_dir", "inside.png"), []byte("i"), 0644)

	dm := &DeployManager{
		config:     DeployConfig{},
		sourcePath: dir,
		destPath:   "",
		debugMode:  false,
		cache:      loadDeployCache(filepath.Join(dir, ".deploy-cache.json")),
	}
	if err := dm.revertSrcGit(); err != nil {
		t.Fatalf("revertSrcGit failed: %v", err)
	}

	// 已跟踪文件应恢复到提交版本
	content, err := os.ReadFile(filepath.Join(dir, "tracked.txt"))
	if err != nil {
		t.Fatalf("failed to read tracked.txt: %v", err)
	}
	if string(content) != "v1" {
		t.Errorf("tracked modification not reverted, got %q want %q", string(content), "v1")
	}

	// 未跟踪文件和目录应被移除
	if fileExists(filepath.Join(dir, "untracked.688db72b.css")) {
		t.Error("untracked file should be removed")
	}
	if fileExists(filepath.Join(dir, "untracked_dir")) {
		t.Error("untracked dir should be removed")
	}

	// 工作区应为干净状态
	statusOut := runGit("status", "--porcelain")
	if strings.TrimSpace(string(statusOut)) != "" {
		t.Errorf("workspace should be clean, got: %s", statusOut)
	}
}

// TestProcessHTMLFileExtraHashResources verifies that a shared script (e.g.
// utils_index.js) referenced via a relative path with a ?query, configured
// under ExtraHashResources, is hashed and its HTML reference updated to a CDN
// URL — even though the path does not contain "components".
func TestProcessHTMLFileExtraHashResources(t *testing.T) {
	tmpDir := t.TempDir()

	// Create scripts/common/utils_index.js
	commonDir := filepath.Join(tmpDir, "scripts", "common")
	if err := os.MkdirAll(commonDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	utilsPath := filepath.Join(commonDir, "utils_index.js")
	if err := os.WriteFile(utilsPath, []byte("console.log('utils');"), 0644); err != nil {
		t.Fatalf("write utils_index.js failed: %v", err)
	}

	htmlContent := `<!DOCTYPE html>
<html>
<head></head>
<body>
<script type="text/javascript" src="./scripts/common/utils_index.js?2505141"></script>
</body>
</html>`
	htmlPath := filepath.Join(tmpDir, "page.html")
	if err := os.WriteFile(htmlPath, []byte(htmlContent), 0644); err != nil {
		t.Fatalf("write html failed: %v", err)
	}

	cdn := "https://cdn.example.com"
	vm := NewVersionManager(Config{
		HashLength:         8,
		CDNDomain:          cdn,
		ProcessMainResources: []string{"page"},
		ExtraHashResources: []string{"scripts/common/utils_index.js"},
	}, false)

	if err := vm.processHTMLFile(htmlPath); err != nil {
		t.Fatalf("processHTMLFile failed: %v", err)
	}

	result, err := os.ReadFile(htmlPath)
	if err != nil {
		t.Fatalf("read html failed: %v", err)
	}
	strResult := string(result)

	// The old ?query reference must be gone.
	if strings.Contains(strResult, "utils_index.js?2505141") {
		t.Errorf("old ?query reference still present: %s", strResult)
	}

	// Must contain a CDN-prefixed hashed reference.
	if !strings.Contains(strResult, "https://cdn.example.com/scripts/common/utils_index.") ||
		!strings.Contains(strResult, ".js") {
		t.Errorf("expected CDN-prefixed hashed utils_index URL, got: %s", strResult)
	}

	// The hashed file must exist on disk.
	entries, err := os.ReadDir(commonDir)
	if err != nil {
		t.Fatalf("read dir failed: %v", err)
	}
	hasHashed := false
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "utils_index.") && strings.HasSuffix(name, ".js") && name != "utils_index.js" {
			hasHashed = true
			break
		}
	}
	if !hasHashed {
		t.Errorf("hashed utils_index.*.js not found in %s", commonDir)
	}
}

// TestProcessHTMLFileExtraHashResourcesMultiple verifies that multiple
// ExtraHashResources (e.g. utils_index.js and loginxdrNew.js, both under
// scripts/common/) are hashed together and each HTML reference updated to a
// CDN URL — mirroring the real version.config.json setup where both shared
// scripts are listed together.
func TestProcessHTMLFileExtraHashResourcesMultiple(t *testing.T) {
	tmpDir := t.TempDir()

	commonDir := filepath.Join(tmpDir, "scripts", "common")
	if err := os.MkdirAll(commonDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	sources := map[string]string{
		"utils_index.js": "console.log('utils');",
		"loginxdrNew.js": "console.log('login');",
	}
	for name, content := range sources {
		if err := os.WriteFile(filepath.Join(commonDir, name), []byte(content), 0644); err != nil {
			t.Fatalf("write %s failed: %v", name, err)
		}
	}

	// Both scripts referenced via a relative path with a ?query, as in production.
	htmlContent := `<!DOCTYPE html>
<html>
<head></head>
<body>
<script type="text/javascript" src="./scripts/common/utils_index.js?2505141"></script>
<script type="text/javascript" src="./scripts/common/loginxdrNew.js?v=202607104"></script>
</body>
</html>`
	htmlPath := filepath.Join(tmpDir, "page.html")
	if err := os.WriteFile(htmlPath, []byte(htmlContent), 0644); err != nil {
		t.Fatalf("write html failed: %v", err)
	}

	cdn := "https://cdn.example.com"
	vm := NewVersionManager(Config{
		HashLength:           8,
		CDNDomain:            cdn,
		ProcessMainResources: []string{"page"},
		ExtraHashResources: []string{
			"scripts/common/utils_index.js",
			"scripts/common/loginxdrNew.js",
		},
	}, false)

	if err := vm.processHTMLFile(htmlPath); err != nil {
		t.Fatalf("processHTMLFile failed: %v", err)
	}

	result, err := os.ReadFile(htmlPath)
	if err != nil {
		t.Fatalf("read html failed: %v", err)
	}
	strResult := string(result)

	// Both old ?query references must be gone.
	if strings.Contains(strResult, "utils_index.js?2505141") {
		t.Errorf("old utils_index ?query still present: %s", strResult)
	}
	if strings.Contains(strResult, "loginxdrNew.js?v=202607104") {
		t.Errorf("old loginxdrNew ?query still present: %s", strResult)
	}

	// Both must have CDN-prefixed hashed references.
	for _, base := range []string{"utils_index", "loginxdrNew"} {
		prefix := cdn + "/scripts/common/" + base + "."
		if !strings.Contains(strResult, prefix) || !strings.Contains(strResult, ".js") {
			t.Errorf("expected CDN-prefixed hashed %s URL, got: %s", base, strResult)
		}
	}

	// Both hashed files must exist on disk and be distinct.
	hashed := map[string]bool{}
	entries, err := os.ReadDir(commonDir)
	if err != nil {
		t.Fatalf("read dir failed: %v", err)
	}
	for _, base := range []string{"utils_index", "loginxdrNew"} {
		found := false
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, base+".") && strings.HasSuffix(name, ".js") && name != base+".js" {
				found = true
				hashed[name] = true
			}
		}
		if !found {
			t.Errorf("hashed %s.*.js not found in %s", base, commonDir)
		}
	}
	if len(hashed) != 2 {
		t.Errorf("expected 2 distinct hashed files, got %d (%v)", len(hashed), hashed)
	}
}

// TestProcessHTMLFileObfuscateJS verifies that when ObfuscateJS is enabled,
// JS files are minified (comments removed, whitespace stripped, variables
// renamed) and the hash reflects the obfuscated content. CSS files are
// unaffected.
func TestProcessHTMLFileObfuscateJS(t *testing.T) {
	tmpDir := t.TempDir()

	commonDir := filepath.Join(tmpDir, "scripts", "common")
	if err := os.MkdirAll(commonDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	// JS with comments, whitespace, and descriptive variable names
	jsContent := `// This is a comment
var greetingMessage = function() {
    var descriptiveLocalVariable = "hello world";
    console.log(descriptiveLocalVariable);
};
greetingMessage();
`
	jsPath := filepath.Join(commonDir, "loginxdrNew.js")
	if err := os.WriteFile(jsPath, []byte(jsContent), 0644); err != nil {
		t.Fatalf("write js failed: %v", err)
	}

	htmlContent := `<script src="./scripts/common/loginxdrNew.js?v=1"></script>`
	htmlPath := filepath.Join(tmpDir, "page.html")
	if err := os.WriteFile(htmlPath, []byte(htmlContent), 0644); err != nil {
		t.Fatalf("write html failed: %v", err)
	}

	cdn := "https://cdn.example.com"
	vm := NewVersionManager(Config{
		HashLength:           8,
		CDNDomain:            cdn,
		ProcessMainResources: []string{"page"},
		ExtraHashResources:   []string{"scripts/common/loginxdrNew.js"},
		ObfuscateJS:          true,
	}, false)

	if err := vm.processHTMLFile(htmlPath); err != nil {
		t.Fatalf("processHTMLFile failed: %v", err)
	}

	// Find the hashed JS file on disk
	entries, err := os.ReadDir(commonDir)
	if err != nil {
		t.Fatalf("read dir failed: %v", err)
	}
	var hashedPath string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "loginxdrNew.") && strings.HasSuffix(name, ".js") && name != "loginxdrNew.js" {
			hashedPath = filepath.Join(commonDir, name)
			break
		}
	}
	if hashedPath == "" {
		t.Fatalf("hashed JS file not found in %s", commonDir)
	}

	result, err := os.ReadFile(hashedPath)
	if err != nil {
		t.Fatalf("read hashed js failed: %v", err)
	}
	strResult := string(result)

	// Comments must be stripped
	if strings.Contains(strResult, "This is a comment") {
		t.Errorf("comment not stripped in obfuscated JS")
	}
	// Descriptive variable name should be mangled
	if strings.Contains(strResult, "descriptiveLocalVariable") {
		t.Errorf("variable name not mangled in obfuscated JS")
	}
	// Output should be smaller than input
	if len(result) >= len(jsContent) {
		t.Errorf("obfuscated JS (%d bytes) should be smaller than original (%d bytes)", len(result), len(jsContent))
	}

	// HTML reference must be updated to CDN-prefixed hashed URL
	htmlResult, err := os.ReadFile(htmlPath)
	if err != nil {
		t.Fatalf("read html failed: %v", err)
	}
	if !strings.Contains(string(htmlResult), cdn+"/scripts/common/loginxdrNew.") {
		t.Errorf("HTML reference not updated to CDN hashed URL, got: %s", htmlResult)
	}
}

// TestProcessHTMLFileExtraHashResourcesNoCDN verifies the non-CDN case: the
// HTML reference is updated to the hashed relative path (no CDN prefix).
func TestProcessHTMLFileExtraHashResourcesNoCDN(t *testing.T) {
	tmpDir := t.TempDir()

	commonDir := filepath.Join(tmpDir, "scripts", "common")
	if err := os.MkdirAll(commonDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	utilsPath := filepath.Join(commonDir, "utils_index.js")
	if err := os.WriteFile(utilsPath, []byte("console.log('utils');"), 0644); err != nil {
		t.Fatalf("write utils_index.js failed: %v", err)
	}

	htmlContent := `<script src="scripts/common/utils_index.js"></script>`
	htmlPath := filepath.Join(tmpDir, "page.html")
	if err := os.WriteFile(htmlPath, []byte(htmlContent), 0644); err != nil {
		t.Fatalf("write html failed: %v", err)
	}

	// No CDNDomain set — should produce a relative hashed path, not a CDN URL.
	vm := NewVersionManager(Config{
		HashLength:         8,
		ProcessMainResources: []string{"page"},
		ExtraHashResources: []string{"scripts/common/utils_index.js"},
	}, false)

	if err := vm.processHTMLFile(htmlPath); err != nil {
		t.Fatalf("processHTMLFile failed: %v", err)
	}

	result, err := os.ReadFile(htmlPath)
	if err != nil {
		t.Fatalf("read html failed: %v", err)
	}
	strResult := string(result)

	// Must contain a hashed relative reference (no CDN, no ?query).
	if !strings.Contains(strResult, "scripts/common/utils_index.") || !strings.Contains(strResult, ".js") {
		t.Errorf("expected hashed relative utils_index URL, got: %s", strResult)
	}
	if strings.Contains(strResult, "https://") {
		t.Errorf("should not contain CDN URL in no-CDN mode, got: %s", strResult)
	}
}
