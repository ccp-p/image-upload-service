package main

// CDP (Chrome DevTools Protocol) client for controlling the SDP Electron client.
import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// cdpPage returns the first page target's webSocketDebuggerUrl.
func cdpPage(endpoint string) (string, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(endpoint + "/json/list")
	if err != nil {
		return "", fmt.Errorf("CDP 连接失败（客户端未以调试模式运行？）: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var targets []struct {
		Type string `json:"type"`
		WS   string `json:"webSocketDebuggerUrl"`
	}
	if err := json.Unmarshal(body, &targets); err != nil {
		return "", err
	}
	for _, t := range targets {
		if t.Type == "page" && t.WS != "" {
			return t.WS, nil
		}
	}
	return "", fmt.Errorf("未找到 SDP 页面")
}

// cdpEval evaluates a JS expression in the page and returns the result value.
func cdpEval(ctx context.Context, endpoint, expr string, timeout time.Duration) (json.RawMessage, error) {
	wsURL, err := cdpPage(endpoint)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPClient: &http.Client{Timeout: 5 * time.Second}})
	if err != nil {
		return nil, fmt.Errorf("websocket 连接失败: %w", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	req, _ := json.Marshal(map[string]interface{}{
		"id":     1,
		"method": "Runtime.evaluate",
		"params": map[string]interface{}{
			"expression":   expr,
			"returnByValue": true,
			"awaitPromise":  true,
		},
	})
	if err := conn.Write(ctx, websocket.MessageText, req); err != nil {
		return nil, err
	}
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return nil, err
		}
		var msg struct {
			ID     int `json:"id"`
			Result struct {
				Result struct {
					Value json.RawMessage `json:"value"`
				} `json:"result"`
			} `json:"result"`
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		if msg.ID == 1 {
			return msg.Result.Result.Value, nil
		}
	}
}

// ipcResult invokes an SDP IPC command via the renderer bridge and returns the
// raw JSON string result (never a rejected promise: errors become code -1).
func ipcResult(ctx context.Context, endpoint, cmd, argsJSON string, timeout time.Duration) (string, error) {
	var expr string
	if argsJSON != "" {
		expr = fmt.Sprintf(
			`(async()=>{let r;try{r=await window.electronApi.ipcMessageSync('%s',%s)}catch(e){r={code:'-1',msg:e.message}};return JSON.stringify(r)})()`,
			cmd, argsJSON)
	} else {
		expr = fmt.Sprintf(
			`(async()=>{let r;try{r=await window.electronApi.ipcMessageSync('%s')}catch(e){r={code:'-1',msg:e.message}};return JSON.stringify(r)})()`,
			cmd)
	}
	val, err := cdpEval(ctx, endpoint, expr, timeout)
	if err != nil {
		return "", err
	}
	s := strings.Trim(string(val), `"`)
	s = strings.ReplaceAll(s, `\"`, `"`)
	s = strings.ReplaceAll(s, `\`, `\`)
	return s, nil
}
