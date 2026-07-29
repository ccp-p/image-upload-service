package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	cookieFlag := flag.String("c", "", "cookie string 'k=v; k2=v2', or path to a cookies.txt file")
	flag.Parse()

	input := strings.Join(flag.Args(), " ")
	if strings.TrimSpace(input) == "" {
		// Read a URL (or full share text) from stdin when no arg is given.
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			input = scanner.Text()
		}
	}
	if strings.TrimSpace(input) == "" {
		fmt.Fprintln(os.Stderr, "usage: douyin-dl [-c cookies] <url-or-share-text>")
		os.Exit(2)
	}

	cookies := loadCookies(*cookieFlag)
	client := NewClient(cookies)

	rawURL := extractURLFromShareText(input)
	if isShortURL(rawURL) {
		fmt.Fprintf(os.Stderr, "resolving short link: %s\n", rawURL)
		if final, err := client.resolveShortURL(rawURL); err == nil && final != "" {
			rawURL = final
			fmt.Fprintf(os.Stderr, "resolved to: %s\n", rawURL)
		} else if err != nil {
			fmt.Fprintf(os.Stderr, "short link resolve failed: %v\n", err)
		}
	}

	awemeID := extractVideoID(rawURL)
	if awemeID == "" {
		fmt.Fprintf(os.Stderr, "could not extract video id from: %s\n", rawURL)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "aweme_id: %s\n", awemeID)

	detail, err := fetchVideoDetail(client, awemeID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fetch detail failed: %v\n", err)
		os.Exit(1)
	}

	printMeta(detail)
	urls := extractPlayURLs(client, detail)
	if len(urls) == 0 {
		fmt.Fprintln(os.Stderr, "no download url found (the video may require login / cookies)")
		os.Exit(1)
	}
	fmt.Printf("\n--- download links (%d) ---\n", len(urls))
	for i, u := range urls {
		fmt.Printf("[%d] %s\n", i+1, u)
	}
}

// loadCookies resolves the cookie string: a -c value that points to an
// existing file is read; otherwise it is treated as a literal cookie string.
// With no -c, cookies.txt in the current dir is used if present.
func loadCookies(c string) string {
	if c != "" {
		if b, err := os.ReadFile(c); err == nil {
			return strings.TrimSpace(string(b))
		}
		return c
	}
	if b, err := os.ReadFile("cookies.txt"); err == nil {
		return strings.TrimSpace(string(b))
	}
	return ""
}

func printMeta(detail map[string]interface{}) {
	if desc := getStr(detail, "desc"); desc != "" {
		fmt.Fprintf(os.Stderr, "title: %s\n", desc)
	}
	if author, ok := detail["author"].(map[string]interface{}); ok {
		if nick := getStr(author, "nickname"); nick != "" {
			fmt.Fprintf(os.Stderr, "author: %s\n", nick)
		}
	}
	if stats, ok := detail["statistics"].(map[string]interface{}); ok {
		fmt.Fprintf(os.Stderr, "digg: %v  comment: %v\n", stats["digg_count"], stats["comment_count"])
	}
}
