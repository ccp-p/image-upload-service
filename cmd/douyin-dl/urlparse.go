package main

import (
	"net/url"
	"regexp"
	"strings"
)

// urlparse.go ports utils/validators.py (parse_url_type) and core/url_parser.py
// (URLParser._extract_video_id) for the single-video use case.

var (
	reVideoID   = regexp.MustCompile(`/video/(\d+)`)
	reModalID   = regexp.MustCompile(`modal_id=(\d+)`)
	reURLInText = regexp.MustCompile(`https?://[^\s]+`)
	reShortHost = regexp.MustCompile(`(?i)^(v\.douyin\.com|v\.iesdouyin\.com)$`)
)

func isShortURL(s string) bool {
	parsed, err := url.Parse(s)
	if err != nil {
		return false
	}
	return reShortHost.MatchString(strings.ToLower(parsed.Hostname()))
}

func isDouyinWebHost(host string) bool {
	host = strings.ToLower(host)
	return host == "www.douyin.com" || host == "douyin.com"
}

// parseURLType mirrors validators.parse_url_type for video URLs.
func parseURLType(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	path := parsed.Path
	if isShortURL(rawURL) {
		return "short"
	}
	if !isDouyinWebHost(host) {
		return ""
	}
	if mids := parsed.Query()["modal_id"]; len(mids) > 0 && strings.TrimSpace(mids[0]) != "" {
		return "video"
	}
	if strings.Contains(path, "/video/") {
		return "video"
	}
	return ""
}

// extractVideoID mirrors URLParser._extract_video_id.
func extractVideoID(rawURL string) string {
	if m := reVideoID.FindStringSubmatch(rawURL); m != nil {
		return m[1]
	}
	if m := reModalID.FindStringSubmatch(rawURL); m != nil {
		return m[1]
	}
	return ""
}

// extractURLFromShareText pulls the first http(s) URL out of a Douyin share
// payload like "7.38 复制打开抖音 ... https://v.douyin.com/xxxx/".
func extractURLFromShareText(text string) string {
	if m := reURLInText.FindString(text); m != "" {
		return strings.TrimRight(m, " \t，。")
	}
	return text
}
