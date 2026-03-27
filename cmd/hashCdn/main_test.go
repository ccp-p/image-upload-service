package main

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
