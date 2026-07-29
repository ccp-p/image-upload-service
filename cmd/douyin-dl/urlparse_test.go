package main

import "testing"

func TestExtractVideoID(t *testing.T) {
	cases := map[string]string{
		"https://www.douyin.com/video/7380308675841297704":            "7380308675841297704",
		"https://www.douyin.com/discover?modal_id=7380308675841297704": "7380308675841297704",
		"https://www.douyin.com/user/xxx":                              "",
	}
	for in, want := range cases {
		if got := extractVideoID(in); got != want {
			t.Errorf("extractVideoID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsShortURL(t *testing.T) {
	if !isShortURL("https://v.douyin.com/iABC123/") {
		t.Error("v.douyin.com should be short")
	}
	if isShortURL("https://www.douyin.com/video/123") {
		t.Error("www.douyin.com/video should not be short")
	}
}

func TestParseURLType(t *testing.T) {
	if parseURLType("https://www.douyin.com/video/123") != "video" {
		t.Error("video url not detected")
	}
	if parseURLType("https://v.douyin.com/iABC/") != "short" {
		t.Error("short url not detected")
	}
	if parseURLType("https://example.com/") != "" {
		t.Error("non-douyin should be empty")
	}
}

func TestExtractURLFromShareText(t *testing.T) {
	share := "7.38 复制打开抖音，看看【作者】的作品 https://v.douyin.com/iABC123/"
	got := extractURLFromShareText(share)
	if got != "https://v.douyin.com/iABC123/" {
		t.Errorf("got %q", got)
	}
}
