package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	utls "github.com/refraction-networking/utls"
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
	jar, _ := cookiejar.New(nil)
	c.http = &http.Client{
		Timeout:   30 * time.Second,
		Transport: newChromeTransport(),
		Jar:       jar,
	}
	// Seed the jar for both Douyin domains so redirects carry cookies.
	for _, host := range []string{"https://www.douyin.com", "https://v.douyin.com"} {
		u, _ := url.Parse(host)
		var cs []*http.Cookie
		for k, v := range cookies {
			cs = append(cs, &http.Cookie{Name: k, Value: v})
		}
		jar.SetCookies(u, cs)
	}
	return c
}

// newChromeTransport returns an *http.Transport whose TLS handshakes use
// uTLS to mimic Chrome's JA3 fingerprint. Douyin's CDN drops connections
// from Go's default crypto/tls ClientHello, so this is essential.
func newChromeTransport() *http.Transport {
	tr := &http.Transport{
		MaxIdleConns:        20,
		IdleConnTimeout:     30 * time.Second,
		TLSHandshakeTimeout: 15 * time.Second,
		ForceAttemptHTTP2:   false,
	}
	tr.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, _ := net.SplitHostPort(addr)
		dialer := &net.Dialer{Timeout: 15 * time.Second}
		rawConn, err := dialer.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		config := &utls.Config{ServerName: host}
		uconn := utls.UClient(rawConn, config, utls.HelloChrome_Auto)
		if err := uconn.HandshakeContext(ctx); err != nil {
			rawConn.Close()
			return nil, err
		}
		return uconn, nil
	}
	return tr
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

// setBrowserHeaders sets common Chrome navigation headers on a request.
func setBrowserHeaders(req *http.Request, ua string) {
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Referer", "https://www.douyin.com/?recommend=1")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en-US;q=0.8,en;q=0.7")
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	req.Header.Set("Sec-Ch-Ua", `"Chromium";v="139", "Not?A_Brand";v="24", "Google Chrome";v="139"`)
	req.Header.Set("Sec-Ch-Ua-Mobile", "?0")
	req.Header.Set("Sec-Ch-Ua-Platform", `"Windows"`)
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
}

// getJSON performs a signed GET and returns the raw response body.
// Cookies are sent automatically via the client's cookie jar.
func (c *Client) getJSON(signedURL, ua string) ([]byte, error) {
	req, err := http.NewRequest("GET", signedURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Referer", "https://www.douyin.com/?recommend=1")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en-US;q=0.8,en;q=0.7")
	resp, err := c.http.Do(req)
	if err != nil {
		// Fallback to PowerShell when Go's TLS is blocked by the CDN.
		fmt.Fprintf(os.Stderr, "  Go HTTP failed (%v), trying PowerShell...\n", err)
		return c.getJSONViaPowerShell(signedURL, ua)
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

// cookieString reconstructs the cookie header from the parsed map.
func (c *Client) cookieString() string {
	var parts []string
	for k, v := range c.cookies {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, "; ")
}

// getJSONViaPowerShell uses PowerShell's Invoke-WebRequest as a fallback when
// Go's TLS connections are blocked by Douyin's CDN.
func (c *Client) getJSONViaPowerShell(signedURL, ua string) ([]byte, error) {
	ck := strings.ReplaceAll(c.cookieString(), "'", "''")
	u := strings.ReplaceAll(signedURL, "'", "''")
	a := strings.ReplaceAll(ua, "'", "''")
	ps := fmt.Sprintf(
		`$ErrorActionPreference='Stop';$s=New-Object Microsoft.PowerShell.Commands.WebRequestSession;$ck='%s';foreach($c in $ck.Split(';')|%%{$_.Trim()}){if($c -match '^([^=]+)=(.*)$'){$s.Cookies.Add((New-Object System.Net.Cookie($Matches[1],$Matches[2],'/','.douyin.com')))}};$r=Invoke-WebRequest -Uri '%s' -WebSession $s -UseBasicParsing -TimeoutSec 20 -Headers @{'User-Agent'='%s';'Referer'='https://www.douyin.com/?recommend=1';'Accept'='*/*'};[Console]::Write($r.Content)`,
		ck, u, a,
	)
	cmd := exec.Command("powershell", "-NoProfile", "-Command", ps)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("powershell: %s", string(ee.Stderr))
		}
		return nil, fmt.Errorf("powershell: %w", err)
	}
	return out, nil
}

// resolveShortURL follows redirects on a v.douyin.com short link and returns
// the final URL. Tries the Go HTTP client (with uTLS) first, then falls back
// to PowerShell's Invoke-WebRequest on Windows if the connection is blocked.
func (c *Client) resolveShortURL(shortURL string) (string, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * time.Second)
		}
		req, err := http.NewRequest("GET", shortURL, nil)
		if err != nil {
			return "", err
		}
		setBrowserHeaders(req, c.userAgent)
		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= 400 {
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			continue
		}
		return resp.Request.URL.String(), nil
	}
	if lastErr != nil {
		fmt.Fprintf(os.Stderr, "  Go HTTP failed (%v), trying PowerShell fallback...\n", lastErr)
	}
	return c.resolveShortURLViaPowerShell(shortURL)
}

// resolveShortURLViaPowerShell uses PowerShell's Invoke-WebRequest to follow
// redirects. .NET's HTTP stack sometimes succeeds where Go's TLS fails.
func (c *Client) resolveShortURLViaPowerShell(shortURL string) (string, error) {
	cmd := exec.Command("powershell", "-NoProfile", "-Command",
		fmt.Sprintf(
			`try { $r = Invoke-WebRequest -Uri '%s' -MaximumRedirection 10 -UseBasicParsing -TimeoutSec 15; Write-Output $r.BaseResponse.ResponseUri.AbsoluteUri } catch { Write-Error $_.Exception.Message; exit 1 }`,
			shortURL,
		),
	)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("powershell fallback failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
