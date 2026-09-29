package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthcheckCommand(t *testing.T) {
	status := http.StatusOK
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
	}))
	defer ts.Close()
	t.Setenv("LISTEN_ADDR", strings.TrimPrefix(ts.URL, "http://"))

	if code := healthcheck(); code != 0 {
		t.Errorf("healthy server: exit code %d, want 0", code)
	}
	status = http.StatusServiceUnavailable
	if code := healthcheck(); code != 1 {
		t.Errorf("unhealthy server: exit code %d, want 1", code)
	}
	t.Setenv("LISTEN_ADDR", "not-an-addr")
	if code := healthcheck(); code != 1 {
		t.Errorf("invalid LISTEN_ADDR: exit code %d, want 1", code)
	}
}
