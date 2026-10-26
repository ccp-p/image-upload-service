package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeCDP spins up a fake /json/list + WebSocket CDP server that responds to
// Runtime.evaluate with the given value for any expression containing `match`.
func fakeCDP(t *testing.T, match, value string) *httptest.Server {
	t.Helper()
	upgrader := wsUpgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json/list" {
			host := strings.TrimPrefix(r.Host, "http://")
			w.Write([]byte(`[{"type":"page","webSocketDebuggerUrl":"ws://` + host + `/devtools/page/1"}]`))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/devtools/page/") {
			upgrader.serveEval(w, r, match, value)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestIpcResultSuccess(t *testing.T) {
	srv := fakeCDP(t, "checkTunnelIsConnect", `{"code":"0","msg":"ok"}`)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := ipcResult(ctx, srv.URL, "checkTunnelIsConnect", "", time.Second)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != `{"code":"0","msg":"ok"}` {
		t.Fatalf("got %q", got)
	}
}

func TestIpcResultWithArgs(t *testing.T) {
	srv := fakeCDP(t, "createTunnel", `{"code":"0","msg":"ok"}`)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := ipcResult(ctx, srv.URL, "createTunnel", `{"accessPolicy":"need"}`, time.Second)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != `{"code":"0","msg":"ok"}` {
		t.Fatalf("got %q", got)
	}
}

func TestIpcResultErrorBecomesCodeMinus1(t *testing.T) {
	// expression that throws: our generated expr catches and returns code -1
	srv := fakeCDP(t, "neverMatch", `{"code":"-1","msg":"caught"}`)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// This test verifies wiring, not the catch: send a cmd the fake answers anyway
	got, err := ipcResult(ctx, srv.URL, "neverMatch", "", time.Second)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != `{"code":"-1","msg":"caught"}` {
		t.Fatalf("got %q", got)
	}
}

func TestIsConnectedTrue(t *testing.T) {
	srv := fakeCDP(t, "checkTunnelIsConnect", `{"code":"0","msg":"ok","data":{"peer":"1"}}`)
	orig := cdpEndpoint
	cdpEndpoint = srv.URL
	defer func() { cdpEndpoint = orig }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := isConnected(ctx)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !got {
		t.Fatal("expected connected")
	}
}

func TestIsConnectedFalseOn15006(t *testing.T) {
	srv := fakeCDP(t, "checkTunnelIsConnect", `{"code":"-1","msg":"虚拟网卡不存在","data":{},"errorId":"15006"}`)
	orig := cdpEndpoint
	cdpEndpoint = srv.URL
	defer func() { cdpEndpoint = orig }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := isConnected(ctx)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got {
		t.Fatal("expected not connected for 15006")
	}
}
