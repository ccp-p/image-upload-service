package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Bilibili API response types
// ---------------------------------------------------------------------------

// ViewInfo is the response from https://api.bilibili.com/x/web-interface/view
type ViewInfo struct {
	Code int `json:"code"`
	Data struct {
		AID   int64  `json:"aid"`
		BVID  string `json:"bvid"`
		Title string `json:"title"`
		Pages []struct {
			CID  int64  `json:"cid"`
			Page int    `json:"page"`
			Part string `json:"part"`
		} `json:"pages"`
	} `json:"data"`
}

// SubtitleList is the response from https://api.bilibili.com/x/player/wbi/v2
type SubtitleList struct {
	Code int `json:"code"`
	Data struct {
		Subtitle struct {
			Subtitles []struct {
				Language    string `json:"lan_doc"`
				SubtitleURL string `json:"subtitle_url"`
			} `json:"subtitles"`
		} `json:"subtitle"`
	} `json:"data"`
}

// SubtitleBody is the JSON returned by a subtitle_url
type SubtitleBody struct {
	Body []struct {
		From    float64 `json:"from"`
		To      float64 `json:"to"`
		Content string  `json:"content"`
	} `json:"body"`
}

// ---------------------------------------------------------------------------
// HTTP helpers
// ---------------------------------------------------------------------------

var httpClient = &http.Client{Timeout: 30 * time.Second}

func fetchJSON(rawURL string, sessdata string) ([]byte, error) {
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://www.bilibili.com/")
	if sessdata != "" {
		req.Header.Set("Cookie", "SESSDATA="+sessdata)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func fixSubtitleURL(u string) string {
	if u == "" {
		return ""
	}
	if strings.HasPrefix(u, "//") {
		return "https:" + u
	}
	if !strings.HasPrefix(u, "http") {
		return "https://" + u
	}
	return u
}

// ---------------------------------------------------------------------------
// Core API calls
// ---------------------------------------------------------------------------

// getViewInfo fetches video metadata including all multi-P pages.
func getViewInfo(bvid string, sessdata string) (*ViewInfo, error) {
	apiURL := "https://api.bilibili.com/x/web-interface/view?bvid=" + bvid
	data, err := fetchJSON(apiURL, sessdata)
	if err != nil {
		return nil, fmt.Errorf("request view info: %w", err)
	}
	var info ViewInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("parse view info: %w", err)
	}
	if info.Code != 0 {
		return nil, fmt.Errorf("view api error code %d", info.Code)
	}
	return &info, nil
}

// getSubtitleList fetches the available subtitle list for a single page (cid).
func getSubtitleList(aid int64, cid int64, sessdata string) (*SubtitleList, error) {
	apiURL := fmt.Sprintf("https://api.bilibili.com/x/player/wbi/v2?aid=%d&cid=%d", aid, cid)
	data, err := fetchJSON(apiURL, sessdata)
	if err != nil {
		return nil, fmt.Errorf("request subtitle list: %w", err)
	}
	var list SubtitleList
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("parse subtitle list: %w", err)
	}
	if list.Code != 0 {
		return nil, fmt.Errorf("subtitle api error code %d", list.Code)
	}
	return &list, nil
}

// getSubtitleContent fetches and parses the actual subtitle JSON.
func getSubtitleContent(subtitleURL string) (*SubtitleBody, error) {
	subtitleURL = fixSubtitleURL(subtitleURL)
	data, err := fetchJSON(subtitleURL, "")
	if err != nil {
		return nil, fmt.Errorf("request subtitle content: %w", err)
	}
	var body SubtitleBody
	if err := json.Unmarshal(data, &body); err != nil {
		return nil, fmt.Errorf("parse subtitle content: %w", err)
	}
	return &body, nil
}

// ---------------------------------------------------------------------------
// Format conversion
// ---------------------------------------------------------------------------

// formatVTTTime converts seconds to "HH:MM:SS.mmm"
func formatVTTTime(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	h := int(sec) / 3600
	m := (int(sec) % 3600) / 60
	s := int(sec) % 60
	ms := int((sec - float64(int(sec))) * 1000)
	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, s, ms)
}

// toVTT converts Bilibili subtitle body to WebVTT format.
func toVTT(body *SubtitleBody) string {
	var sb strings.Builder
	sb.WriteString("WEBVTT\n\n")
	for _, item := range body.Body {
		sb.WriteString(formatVTTTime(item.From))
		sb.WriteString(" --> ")
		sb.WriteString(formatVTTTime(item.To))
		sb.WriteString("\n")
		sb.WriteString(strings.TrimSpace(item.Content))
		sb.WriteString("\n\n")
	}
	return sb.String()
}

// toSRT converts Bilibili subtitle body to SubRip (SRT) format.
func toSRT(body *SubtitleBody) string {
	var sb strings.Builder
	for i, item := range body.Body {
		sb.WriteString(strconv.Itoa(i + 1))
		sb.WriteString("\n")
		sb.WriteString(formatVTTTime(item.From))
		sb.WriteString(" --> ")
		sb.WriteString(formatVTTTime(item.To))
		sb.WriteString("\n")
		sb.WriteString(strings.TrimSpace(item.Content))
		sb.WriteString("\n\n")
	}
	return sb.String()
}

// ---------------------------------------------------------------------------
// Filename helpers
// ---------------------------------------------------------------------------

var sanitizeRe = regexp.MustCompile(`[\/\\:*?"<>|]+`)

func sanitizeFilename(name string) string {
	return sanitizeRe.ReplaceAllString(name, "_")
}

func buildFilename(videoTitle string, page int, pagePart string, total int, ext string) string {
	base := sanitizeFilename(videoTitle)
	if total > 1 {
		base += fmt.Sprintf("_P%d_%s", page, sanitizeFilename(pagePart))
	}
	return base + "." + ext
}

// ---------------------------------------------------------------------------
// Main flow
// ---------------------------------------------------------------------------

func main() {
	var (
		bvid     string
		sessdata string
		pageNum  int
		format   string
		listMode bool
		outDir   string
		lang     string
		serve    bool
		port     int
	)
	flag.StringVar(&bvid, "bvid", "", "Bilibili video BV id (e.g. BV1xx411x7xx)")
	flag.StringVar(&sessdata, "sessdata", "", "SESSDATA cookie value (for login/HD access)")
	flag.IntVar(&pageNum, "p", 0, "specific page number (0 = all pages)")
	flag.StringVar(&format, "format", "vtt", "output format: vtt or srt")
	flag.BoolVar(&listMode, "list", false, "only list available subtitles, do not download")
	flag.StringVar(&outDir, "o", ".", "output directory")
		flag.StringVar(&lang, "lang", "", "preferred subtitle language code (e.g. zh-CN, ai-zh); default: first available")
	flag.BoolVar(&serve, "serve", false, "run as HTTP server to receive subtitles from browser userscript")
	flag.IntVar(&port, "port", 9876, "HTTP server port (for -serve mode)")
	flag.Parse()

	if serve {
		if err := runServer(port, outDir); err != nil {
			fmt.Fprintln(os.Stderr, "Server error:", err)
			os.Exit(1)
		}
		return
	}

	if bvid == "" && flag.NArg() > 0 {
		bvid = flag.Arg(0)
	}
	// Allow pasting full URL: https://www.bilibili.com/video/BV1xx...
	bvid = extractBVID(bvid)
	if bvid == "" {
		fmt.Fprintln(os.Stderr, "Usage: bilisub [-sessdata <cookie>] [-p <page>] [-format vtt|srt] [-list] [-o <dir>] [-lang <code>] <BV...>")
		os.Exit(1)
	}

	// Step 1: get video info with all pages
	fmt.Printf("Fetching video info for %s ...\n", bvid)
	info, err := getViewInfo(bvid, sessdata)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
	total := len(info.Data.Pages)
	fmt.Printf("Title: %s  (aid=%d, %d page(s))\n", info.Data.Title, info.Data.AID, total)

	// Determine which pages to process
	type pageTask struct {
		page int
		cid  int64
		part string
	}
	var tasks []pageTask
	for _, pg := range info.Data.Pages {
		if pageNum == 0 || pg.Page == pageNum {
			tasks = append(tasks, pageTask{page: pg.Page, cid: pg.CID, part: pg.Part})
		}
	}
	if len(tasks) == 0 {
		fmt.Fprintf(os.Stderr, "Page %d not found (video has %d pages)\n", pageNum, total)
		os.Exit(1)
	}

	// Create output directory
	if outDir != "." {
		_ = os.MkdirAll(outDir, 0755)
	}

	ext := "vtt"
	if format == "srt" {
		ext = "srt"
	} else {
		format = "vtt"
	}

	failed := 0
	for _, task := range tasks {
		fmt.Printf("\n--- P%d: %s (cid=%d) ---\n", task.page, task.part, task.cid)

		// Step 2: get subtitle list for this page
		list, err := getSubtitleList(info.Data.AID, task.cid, sessdata)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  subtitle list: %v\n", err)
			failed++
			continue
		}

		subs := list.Data.Subtitle.Subtitles
		if len(subs) == 0 {
			fmt.Println("  no subtitle available")
			failed++
			continue
		}

		if listMode {
			for i, s := range subs {
				fmt.Printf("  [%d] %s  %s\n", i, s.Language, fixSubtitleURL(s.SubtitleURL))
			}
			continue
		}

		// Pick subtitle: prefer --lang, otherwise first
		chosen := 0
		if lang != "" {
			for i, s := range subs {
				if strings.Contains(s.Language, lang) || strings.Contains(fixSubtitleURL(s.SubtitleURL), lang) {
					chosen = i
					break
				}
			}
		}
		fmt.Printf("  subtitle: %s\n", subs[chosen].Language)

		// Step 3: fetch actual subtitle content
		body, err := getSubtitleContent(subs[chosen].SubtitleURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  subtitle content: %v\n", err)
			failed++
			continue
		}

		// Step 4: convert and save
		var content string
		if format == "srt" {
			content = toSRT(body)
		} else {
			content = toVTT(body)
		}

		filename := buildFilename(info.Data.Title, task.page, task.part, total, ext)
		outPath := filepath.Join(outDir, filename)
		if err := os.WriteFile(outPath, []byte(content), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "  write file: %v\n", err)
			failed++
			continue
		}
		fmt.Printf("  saved: %s\n", outPath)
	}

	fmt.Printf("\nDone: %d/%d succeeded", len(tasks)-failed, len(tasks))
	if failed > 0 {
		fmt.Printf(", %d failed", failed)
	}
	fmt.Println()
}

// extractBVID accepts a raw BV id or a full bilibili URL and returns the BV id.
func extractBVID(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	// Full URL
	if strings.Contains(input, "bilibili.com") || strings.Contains(input, "b23.tv") {
		if u, err := url.Parse(input); err == nil && u.Path != "" {
			parts := strings.Split(strings.Trim(u.Path, "/"), "/")
			for _, p := range parts {
				if strings.HasPrefix(p, "BV") || strings.HasPrefix(p, "bv") {
					return p
				}
			}
		}
	}
	// Try to find BV prefix anywhere in the string
	re := regexp.MustCompile(`(?i)BV[0-9A-Za-z]{10}`)
	if m := re.FindString(input); m != "" {
		return m
	}
	return ""
}
