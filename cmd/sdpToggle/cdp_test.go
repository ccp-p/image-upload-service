package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// --- cdpPage ---

func TestCdpPageFindsPageTarget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/list" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`[
			{"type":"service_worker","webSocketDebuggerUrl":"ws://x/sw"},
			{"type":"page","webSocketDebuggerUrl":"ws://127.0.0.1:9222/devtools/page/ABC"},
			{"type":"page","webSocketDebuggerUrl":""}
		]`))
	}))
	defer srv.Close()

	got, err := cdpPage(srv.URL)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != "ws://127.0.0.1:9222/devtools/page/ABC" {
		t.Fatalf("got %q", got)
	}
}

func TestCdpPageNoPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"type":"service_worker","webSocketDebuggerUrl":"ws://x/sw"}]`))
	}))
	defer srv.Close()
	if _, err := cdpPage(srv.URL); err == nil {
		t.Fatal("expected error when no page target")
	}
}

func TestCdpPageServerDown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // shut down immediately
	if _, err := cdpPage(url); err == nil {
		t.Fatal("expected error when server unreachable")
	}
}

func TestCdpPageBadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`not-json`))
	}))
	defer srv.Close()
	if _, err := cdpPage(srv.URL); err == nil {
		t.Fatal("expected error on bad json")
	}
}

// --- ipcCode ---

func TestIpcCode(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{`{"code":"0","msg":"ok"}`, "0"},
		{`{"code":"-1","msg":"err"}`, "-1"},
		{`{"code":0}`, ""},      // numeric code -> not a string, returns ""
		{`not json`, ""},
		{`{}`, ""},
	}
	for _, c := range cases {
		if got := ipcCode(c.in); got != c.want {
			t.Errorf("ipcCode(%q)=%q want %q", c.in, got, c.want)
		}
	}
}
