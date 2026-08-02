package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type subtitleItem struct {
	Page     int    `json:"page"`
	Part     string `json:"part"`
	Language string `json:"language"`
	Content  string `json:"content"`
	Format   string `json:"format"`
}

type subtitleBatch struct {
	Title     string         `json:"title"`
	BVID      string         `json:"bvid"`
	Format    string         `json:"format"`
	Subtitles []subtitleItem `json:"subtitles"`
}

func runServer(port int, outDir string) error {
	absOut, _ := filepath.Abs(outDir)
	_ = os.MkdirAll(absOut, 0755)

	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		setCORS(w)
		w.Write([]byte(`{"status":"ok"}`))
	})

	mux.HandleFunc("/api/subtitles", func(w http.ResponseWriter, r *http.Request) {
		setCORS(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		if r.Method != http.MethodPost {
			writeJSON(w, 405, map[string]any{"success": false, "error": "method not allowed"})
			return
		}

		raw, err := io.ReadAll(r.Body)
		if err != nil {
			writeJSON(w, 400, map[string]any{"success": false, "error": err.Error()})
			return
		}

		var batch subtitleBatch
		if err := json.Unmarshal(raw, &batch); err != nil {
			writeJSON(w, 400, map[string]any{"success": false, "error": err.Error()})
			return
		}

		baseName := sanitizeFilename(batch.Title)
		if baseName == "" {
			baseName = batch.BVID
		}
		if baseName == "" {
			baseName = "unknown"
		}

		videoDir := filepath.Join(absOut, baseName)
		os.MkdirAll(videoDir, 0755)

		saved := 0
		for _, sub := range batch.Subtitles {
			ext := sub.Format
			if ext != "srt" && ext != "txt" {
				ext = "vtt"
			}
			var filename string
			if len(batch.Subtitles) > 1 {
				filename = fmt.Sprintf("%s.%s", sanitizeFilename(sub.Part), ext)
			} else {
				filename = fmt.Sprintf("%s.%s", sanitizeFilename(sub.Part), ext)
			}
			path := filepath.Join(videoDir, filename)
			if err := os.WriteFile(path, []byte(sub.Content), 0644); err != nil {
				log.Printf("  save error: %v", err)
				continue
			}
			log.Printf("  saved: %s", path)
			saved++
		}

		writeJSON(w, 200, map[string]any{
			"success":     true,
			"saved_count": saved,
			"dir":         videoDir,
		})
	})

	// AI summarize proxy: receives subtitles text + config, forwards to LLM API
	mux.HandleFunc("/api/summarize", func(w http.ResponseWriter, r *http.Request) {
		setCORS(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		if r.Method != http.MethodPost {
			writeJSON(w, 405, map[string]any{"success": false, "error": "method not allowed"})
			return
		}

		var req struct {
			Text   string `json:"text"`
			APIURL string `json:"api_url"`
			APIKey string `json:"api_key"`
	Model  string `json:"model"`
	SystemPrompt string `json:"system_prompt"`
}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, 400, map[string]any{"success": false, "error": err.Error()})
			return
		}

		if req.Text == "" {
			writeJSON(w, 400, map[string]any{"success": false, "error": "text is empty"})
			return
		}
		if req.APIURL == "" || req.APIKey == "" {
			writeJSON(w, 400, map[string]any{"success": false, "error": "api_url and api_key required"})
			return
		}
		if req.Model == "" {
			req.Model = "glm-5.2"
		}

		// Truncate if too long
		if len(req.Text) > 100000 {
			req.Text = req.Text[:100000]
			log.Printf("  subtitle text truncated to 100000 chars")
		}

		// Build URL: user provides base URL, we append v1/chat/completions
		// Handle trailing slash and existing path
		baseURL := strings.TrimRight(req.APIURL, "/")
		// If user already included the full path, use as-is
		var fullURL string
		if strings.HasSuffix(baseURL, "/chat/completions") {
			fullURL = baseURL
		} else if strings.HasSuffix(baseURL, "/v1") {
			fullURL = baseURL + "/chat/completions"
		} else {
			fullURL = baseURL + "/v1/chat/completions"
		}

	// Build OpenAI-compatible request with stream + thinking
	llmReq := map[string]any{
		"model": req.Model,
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": req.SystemPrompt,
			},
			{
				"role":    "user",
				"content": "请分析以下视频字幕，按照系统提示词的要求，用中文生成可视化的 HTML 要点解析。只返回 body 内的 HTML 内容（不要 <!DOCTYPE>、<html>、<head>、<style>，CSS 由渲染器自动注入），使用系统提示词中定义的组件 class 名。字幕内容如下：\n\n" + req.Text,
			},
			},
			"stream": true,
			"chat_template_kwargs": map[string]bool{
				"thinking": true,
			},
		}

		llmBody, _ := json.Marshal(llmReq)

		httpReq, err := http.NewRequest("POST", fullURL, bytes.NewReader(llmBody))
		if err != nil {
			writeJSON(w, 500, map[string]any{"success": false, "error": err.Error()})
			return
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Authorization", "Bearer "+req.APIKey)

		log.Printf("  AI summarize: url=%s, model=%s, text=%d chars", fullURL, req.Model, len(req.Text))

		client := &http.Client{Timeout: 300 * time.Second}
		resp, err := client.Do(httpReq)
		if err != nil {
			writeJSON(w, 502, map[string]any{"success": false, "error": err.Error()})
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			llmRaw, _ := io.ReadAll(resp.Body)
			writeJSON(w, 502, map[string]any{"success": false, "error": "LLM API returned " + fmt.Sprint(resp.StatusCode), "detail": string(llmRaw)})
			return
		}

		// Parse SSE stream: lines starting with "data: "
		// Accumulate content from choices[0].delta.content
		var summary strings.Builder
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 256*1024), 256*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				break
			}
			var chunk struct {
				Choices []struct {
					Delta struct {
						Content      string `json:"content"`
						ReasoningContent string `json:"reasoning_content"`
					} `json:"delta"`
				} `json:"choices"`
			}
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				continue
			}
			if len(chunk.Choices) > 0 {
				if chunk.Choices[0].Delta.Content != "" {
					summary.WriteString(chunk.Choices[0].Delta.Content)
				}
				// Some APIs put thinking in reasoning_content, skip it for summary
			}
		}

		result := summary.String()
		if result == "" {
			// Fallback: maybe non-stream response
			llmRaw, _ := io.ReadAll(resp.Body)
			var llmResp struct {
				Choices []struct {
					Message struct {
						Content string `json:"content"`
					} `json:"message"`
				} `json:"choices"`
			}
			if err := json.Unmarshal(llmRaw, &llmResp); err == nil && len(llmResp.Choices) > 0 {
				result = llmResp.Choices[0].Message.Content
			} else {
				result = string(llmRaw)
			}
		}

		log.Printf("  AI summarize done: %d chars", len(result))
		writeJSON(w, 200, map[string]any{
			"success": true,
			"summary": result,
		})
	})

// AI config stored on server side
	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		setCORS(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}

		configPath := filepath.Join(absOut, ".ai_config.json")

		if r.Method == http.MethodGet {
			data, err := os.ReadFile(configPath)
			if err != nil {
				writeJSON(w, 200, map[string]any{"api_url": "", "api_key": "", "model": ""})
				return
			}
			var cfg struct {
				APIURL string `json:"api_url"`
				APIKey string `json:"api_key"`
				Model  string `json:"model"`
			}
			json.Unmarshal(data, &cfg)
			// Mask key for security
			if len(cfg.APIKey) > 8 {
				cfg.APIKey = cfg.APIKey[:4] + "..." + cfg.APIKey[len(cfg.APIKey)-4:]
			}
			writeJSON(w, 200, cfg)
			return
		}

		if r.Method == http.MethodPost {
			var cfg struct {
				APIURL string `json:"api_url"`
				APIKey string `json:"api_key"`
				Model  string `json:"model"`
			}
			if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
				writeJSON(w, 400, map[string]any{"success": false, "error": err.Error()})
				return
			}
			data, _ := json.Marshal(cfg)
			os.WriteFile(configPath, data, 0600)
			writeJSON(w, 200, map[string]any{"success": true})
			return
		}

		writeJSON(w, 405, map[string]any{"success": false, "error": "method not allowed"})
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		setCORS(w)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "<html><body><h2>Bilibili Subtitle Server</h2><p>Server is running on port %d.</p><p>Install the userscript and open a Bilibili video page.</p><p>Output directory: %s</p></body></html>", port, absOut)
	})

	addr := fmt.Sprintf(":%d", port)
	log.Printf("Subtitle server listening on http://localhost:%d", port)
	log.Printf("Output directory: %s", absOut)
	return http.ListenAndServe(addr, mux)
}

func setCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

func writeJSON(w http.ResponseWriter, code int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(data)
}
