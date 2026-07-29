package main

import (
	"crypto/rand"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	baseURL    = "https://www.douyin.com"
	defaultUA  = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/139.0.0.0 Safari/537.36"
	msTokenLen = 182
)

// Client wraps Douyin web API access: cookie jar, msToken, and ABogus-signed
// requests. Mirrors core/api_client.py (DouyinAPIClient).
type Client struct {
	http      *http.Client
	userAgent string
	cookies   map[string]string
	msToken   string
}

// NewClient builds a client from a raw cookie string ("k1=v1; k2=v2").
func NewClient(cookieStr string) *Client {
	cookies := parseCookieStr(cookieStr)
	c := &Client{userAgent: defaultUA, cookies: cookies}
	c.msToken = strings.TrimSpace(cookies["msToken"])
	if c.msToken == "" {
		c.msToken = genFakeMsToken()
	}
	c.http = &http.Client{Timeout: 30 * time.Second}
	return c
}

func parseCookieStr(s string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(s, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if eq := strings.IndexByte(part, '='); eq > 0 {
			out[part[:eq]] = part[eq+1:]
		}
	}
	return out
}

// genFakeMsToken mirrors MsTokenManager.gen_false_ms_token (182 alnum + "==").
func genFakeMsToken() string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	var sb strings.Builder
	for i := 0; i < msTokenLen; i++ {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		sb.WriteByte(chars[n.Int64()])
	}
	return sb.String() + "=="
}

// defaultQuery mirrors DouyinAPIClient._default_query (ordered like Python).
func (c *Client) defaultQuery() []kv {
	return []kv{
		{"device_platform", "webapp"}, {"aid", "6383"}, {"channel", "channel_pc_web"},
		{"update_version_code", "170400"}, {"pc_client_type", "1"}, {"pc_libra_divert", "Windows"},
		{"version_code", "290100"}, {"version_name", "29.1.0"}, {"cookie_enabled", "true"},
		{"screen_width", "1536"}, {"screen_height", "864"}, {"browser_language", "zh-CN"},
		{"browser_platform", "Win32"}, {"browser_name", "Chrome"}, {"browser_version", "139.0.0.0"},
		{"browser_online", "true"}, {"engine_name", "Blink"}, {"engine_version", "139.0.0.0"},
		{"os_name", "Windows"}, {"os_version", "10"}, {"cpu_core_num", "16"}, {"device_memory", "8"},
		{"platform", "PC"}, {"downlink", "10"}, {"effective_type", "4g"}, {"round_trip_time", "200"},
		{"support_h265", "1"}, {"support_dash", "1"}, {"uifid", ""}, {"msToken", c.msToken},
	}
}

type kv struct{ k, v string }

func encodeParams(params []kv) string {
	parts := make([]string, 0, len(params))
	for _, p := range params {
		parts = append(parts, url.QueryEscape(p.k)+"="+url.QueryEscape(p.v))
	}
	return strings.Join(parts, "&")
}

// signedURL builds and ABogus-signs a full request URL for path + params.
// Mirrors build_signed_path + _build_abogus_url.
func (c *Client) signedURL(path string, params []kv) (string, string) {
	query := encodeParams(params)
	fp := generateChromeFingerprint()
	signer := newABogus(c.userAgent, fp)
	signed, _, ua := signer.generate(query, "")
	return baseURL + path + "?" + signed, ua
}

// signPlayURL signs a bare /aweme/v1/play/ URL that Douyin returns without
// X-Bogus (mirrors _sign_play_candidate / sign_url).
func (c *Client) signPlayURL(rawURL string) (string, string) {
	if strings.Contains(rawURL, "X-Bogus=") {
		return rawURL, c.userAgent
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL, c.userAgent
	}
	var params []kv
	for k, vs := range parsed.Query() {
		for _, v := range vs {
			params = append(params, kv{k, v})
		}
	}
	query := encodeParams(params)
	fp := generateChromeFingerprint()
	signer := newABogus(c.userAgent, fp)
	signed, _, ua := signer.generate(query, "")
	return parsed.Scheme + "://" + parsed.Host + parsed.Path + "?" + signed, ua
}

// getJSON performs a signed GET and returns the raw response body.
func (c *Client) getJSON(signedURL, ua string) ([]byte, error) {
	req, err := http.NewRequest("GET", signedURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Referer", "https://www.douyin.com/?recommend=1")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en-US;q=0.8,en;q=0.7")
	for k, v := range c.cookies {
		req.AddCookie(&http.Cookie{Name: k, Value: v})
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return body, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return body, nil
}

// resolveShortURL follows redirects on a v.douyin.com short link and returns
// the final URL. Mirrors DouyinAPIClient.resolve_short_url.
func (c *Client) resolveShortURL(shortURL string) (string, error) {
	checkClient := &http.Client{Timeout: 15 * time.Second}
	req, _ := http.NewRequest("GET", shortURL, nil)
	req.Header.Set("User-Agent", c.userAgent)
	resp, err := checkClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.Request.URL.String(), nil
}
