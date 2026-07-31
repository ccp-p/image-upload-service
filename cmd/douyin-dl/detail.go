package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// detail.go ports core/api_client.py get_video_detail and the play-URL
// extraction from core/downloader_base.py (_build_video_url_candidates,
// _pick_preferred_play_addr, _partition_video_candidates).

var detailAIDCandidates = []string{"6383", "1128"}

// fetchVideoDetail calls /aweme/v1/web/aweme/detail/ for awemeID, trying the
// aid candidates in order (mirrors get_video_detail).
func fetchVideoDetail(c *Client, awemeID string) (map[string]interface{}, error) {
	for _, aid := range detailAIDCandidates {
		params := append(c.defaultQuery(),
			kv{"aweme_id", awemeID},
			kv{"aid", aid},
		)
		signed, ua := c.signedURL("/aweme/v1/web/aweme/detail/", params)
		body, err := c.getJSON(signed, ua)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [aid=%s] request error: %v\n", aid, err)
			if len(body) > 0 {
				preview := body
				if len(preview) > 300 {
					preview = preview[:300]
				}
				fmt.Fprintf(os.Stderr, "  [aid=%s] response body: %s\n", aid, string(preview))
			}
			continue
		}
		var raw map[string]interface{}
		if err := json.Unmarshal(body, &raw); err != nil {
			fmt.Fprintf(os.Stderr, "  [aid=%s] json parse error: %v\n", aid, err)
			continue
		}
		if detail, ok := raw["aweme_detail"].(map[string]interface{}); ok && detail != nil {
			return detail, nil
		}
		fmt.Fprintf(os.Stderr, "  [aid=%s] no aweme_detail (status_code=%v)\n", aid, raw["status_code"])
	}
	return nil, fmt.Errorf("no aweme_detail for %s", awemeID)
}

// pickPreferredPlayAddr selects the best play_addr from video.bit_rate[],
// falling back to video.play_addr (mirrors _pick_preferred_play_addr,
// "highest" quality).
func pickPreferredPlayAddr(video map[string]interface{}) map[string]interface{} {
	if bitRates, ok := video["bit_rate"].([]interface{}); ok {
		bestPixels := 0
		bestBR := 0
		var best map[string]interface{}
		for _, e := range bitRates {
			entry, ok := e.(map[string]interface{})
			if !ok {
				continue
			}
			pa, ok := entry["play_addr"].(map[string]interface{})
			if !ok || pa == nil {
				continue
			}
			br := int(getNum(entry, "bit_rate"))
			w := int(getNum(pa, "width"))
			h := int(getNum(pa, "height"))
			pixels := w * h
			if pixels > bestPixels || (pixels == bestPixels && br > bestBR) {
				bestPixels = pixels
				bestBR = br
				best = pa
			}
		}
		if best != nil {
			return best
		}
	}
	for _, key := range []string{"play_addr_h264", "play_addr_265", "play_addr_256", "play_addr"} {
		if pa, ok := video[key].(map[string]interface{}); ok && pa != nil {
			return pa
		}
	}
	return map[string]interface{}{}
}

// isWatermarked mirrors _is_watermarked_media_url.
func isWatermarked(u string) bool {
	l := strings.ToLower(u)
	for _, h := range []string{"tplv-dy-water", "dy-water", "owner_watermark", "watermark_image", "watermark=1", "playwm"} {
		if strings.Contains(l, h) {
			return true
		}
	}
	return false
}

// extractPlayURLs returns candidate download URLs, watermark-free direct CDN
// mirrors first, then a signed /aweme/v1/play/ fallback.
func extractPlayURLs(c *Client, detail map[string]interface{}) []string {
	video, _ := detail["video"].(map[string]interface{})
	if video == nil {
		return nil
	}
	pa := pickPreferredPlayAddr(video)
	var direct, watermarked []string
	for _, u := range getStrList(pa, "url_list") {
		if isWatermarked(u) {
			watermarked = append(watermarked, u)
			continue
		}
		// douyin.com play endpoints without X-Bogus need signing.
		if strings.Contains(u, "douyin.com") && !strings.Contains(u, "X-Bogus=") {
			signed, _ := c.signPlayURL(u)
			direct = append(direct, signed)
		} else {
			direct = append(direct, u)
		}
	}
	out := append([]string{}, direct...)
	// signed /aweme/v1/play/ fallback when no direct watermark-free URL exists.
	if len(direct) == 0 {
		uri := getStr(pa, "uri")
		if uri == "" {
			uri = getStr(video, "vid")
		}
		if uri == "" {
			if da, ok := video["download_addr"].(map[string]interface{}); ok {
				uri = getStr(da, "uri")
			}
		}
		if uri != "" {
			params := []kv{
				{"video_id", uri}, {"ratio", "1080p"}, {"line", "0"},
				{"is_play_url", "1"}, {"watermark", "0"}, {"source", "PackSourceEnum_PUBLISH"},
			}
			signed, _ := c.signedURL("/aweme/v1/play/", params)
			out = append(out, signed)
		}
	}
	out = append(out, watermarked...)
	return out
}

// --- JSON helpers ---

func getStr(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func getNum(m map[string]interface{}, key string) float64 {
	switch v := m[key].(type) {
	case float64:
		return v
	case json.Number:
		f, _ := v.Float64()
		return f
	}
	return 0
}

func getStrList(m map[string]interface{}, key string) []string {
	arr, ok := m[key].([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		if s, ok := e.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}
