package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func get(t *testing.T, srv *httptest.Server, path string) (int, string) {
	t.Helper()
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestEndpoints(t *testing.T) {
	srv := httptest.NewServer(newMux(newRegistry()))
	defer srv.Close()

	if code, body := get(t, srv, "/"); code != 200 || !strings.HasPrefix(body, "Hello from ") {
		t.Errorf("/: got %d %q", code, body)
	}
	if code, body := get(t, srv, "/healthz"); code != 200 || body != "ok\n" {
		t.Errorf("/healthz: got %d %q", code, body)
	}
	if code, _ := get(t, srv, "/nope"); code != 404 {
		t.Errorf("/nope: got %d, want 404", code)
	}

	code, body := get(t, srv, "/metrics")
	if code != 200 {
		t.Fatalf("/metrics: got %d", code)
	}
	for _, want := range []string{
		`http_requests_total{code="200",path="/"} 1`,
		`http_requests_total{code="200",path="/healthz"} 1`,
		`http_requests_total{code="404",path="/"} 1`,
		`http_request_duration_seconds_count{path="/"} 2`,
		`go_goroutines`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics missing %q", want)
		}
	}
}
