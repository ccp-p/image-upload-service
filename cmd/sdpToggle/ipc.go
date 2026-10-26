package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

var cdpEndpoint = "http://127.0.0.1:9222"

// ipcCode extracts the "code" field from an IPC result JSON.
func ipcCode(result string) string {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(result), &m); err != nil {
		return ""
	}
	if c, ok := m["code"].(string); ok {
		return c
	}
	return ""
}

// isConnected reports whether the WireGuard tunnel is up.
// Uses checkTunnelIsConnect (tunnelIsExists false-positives on the daemon process).
func isConnected(ctx context.Context) (bool, error) {
	res, err := ipcResult(ctx, cdpEndpoint, "checkTunnelIsConnect", "", 30*time.Second)
	if err != nil {
		return false, err
	}
	return ipcCode(res) == "0" && !strings.Contains(res, "15006"), nil
}

// ensurePlugin initializes the encrypt plugin so stopTunnel actually works.
func ensurePlugin(ctx context.Context) {
	_, _ = ipcResult(ctx, cdpEndpoint, "setBaseRequestData", "", 30*time.Second)
}

// connectIntranet creates the tunnel (SPA knock + WireGuard wg0).
func connectIntranet(ctx context.Context) (string, error) {
	ensurePlugin(ctx)
	return ipcResult(ctx, cdpEndpoint, "createTunnel", `{"accessPolicy":"need"}`, 150*time.Second)
}

// disconnectInternet stops the tunnel.
func disconnectInternet(ctx context.Context) (string, error) {
	ensurePlugin(ctx)
	return ipcResult(ctx, cdpEndpoint, "stopTunnel", "", 60*time.Second)
}

// toggle switches network mode; returns (message, newIntranetState, error).
func toggle(ctx context.Context) (string, bool, error) {
	cur, err := isConnected(ctx)
	if err != nil {
		return "", false, err
	}
	if cur {
		if _, err := disconnectInternet(ctx); err != nil {
			return "", true, err
		}
		return "成功断开，当前外网模式", false, nil
	}
	res, err := connectIntranet(ctx)
	if err != nil {
		return "", false, err
	}
	_ = res
	if ipcCode(res) != "0" {
		return "", false, fmt.Errorf("连接内网失败: %s", res)
	}
	return "成功连上内网", true, nil
}
