package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// --- toggle flow with scripted server ---

// fakeFlowCDP scripts responses per IPC command name found in the expression.
func fakeFlowCDP(t *testing.T, responses map[string]string) *httptest.Server {
	t.Helper()
	up := wsUpgrader{}
	var state atomic.Value
	state.Store(map[string]bool{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json/list" {
			host := strings.TrimPrefix(r.Host, "http://")
			w.Write([]byte(`[{"type":"page","webSocketDebuggerUrl":"ws://` + host + `/devtools/page/1"}]`))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/devtools/page/") {
			up.serveFlow(w, r, responses, &state)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestToggleFromInternetToIntranet(t *testing.T) {
	// current: not connected (15006). createTunnel -> success.
	srv := fakeFlowCDP(t, map[string]string{
		"checkTunnelIsConnect": `{"code":"-1","msg":"x","errorId":"15006"}`,
		"setBaseRequestData":   `{"code":"0"}`,
		"createTunnel":         `{"code":"0","msg":"ok"}`,
	})
	orig := cdpEndpoint
	cdpEndpoint = srv.URL
	defer func() { cdpEndpoint = orig }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	msg, nowIntranet, err := toggle(ctx)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !nowIntranet {
		t.Fatal("expected intranet=true")
	}
	if msg != "成功连上内网" {
		t.Fatalf("msg=%q", msg)
	}
}

func TestToggleFromIntranetToInternet(t *testing.T) {
	srv := fakeFlowCDP(t, map[string]string{
		"checkTunnelIsConnect": `{"code":"0","msg":"ok"}`,
		"setBaseRequestData":   `{"code":"0"}`,
		"stopTunnel":           `{"code":"0","msg":"成功断开隧道。"}`,
	})
	orig := cdpEndpoint
	cdpEndpoint = srv.URL
	defer func() { cdpEndpoint = orig }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	msg, nowIntranet, err := toggle(ctx)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if nowIntranet {
		t.Fatal("expected intranet=false")
	}
	if msg != "成功断开，当前外网模式" {
		t.Fatalf("msg=%q", msg)
	}
}

func TestToggleCreateFails(t *testing.T) {
	srv := fakeFlowCDP(t, map[string]string{
		"checkTunnelIsConnect": `{"code":"-1","msg":"x","errorId":"15006"}`,
		"setBaseRequestData":   `{"code":"0"}`,
		"createTunnel":         `{"code":"-1","msg":"server reject"}`,
	})
	orig := cdpEndpoint
	cdpEndpoint = srv.URL
	defer func() { cdpEndpoint = orig }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err := toggle(ctx)
	if err == nil {
		t.Fatal("expected error when createTunnel fails")
	}
	if !strings.Contains(err.Error(), "server reject") {
		t.Fatalf("err should contain server msg, got %v", err)
	}
}

func TestToggleUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	orig := cdpEndpoint
	cdpEndpoint = srv.URL
	defer func() { cdpEndpoint = orig }()
	srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, _, err := toggle(ctx); err == nil {
		t.Fatal("expected error when CDP unreachable")
	}
}
