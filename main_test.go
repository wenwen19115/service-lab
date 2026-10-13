package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBrowserURL(t *testing.T) {
	for _, item := range []struct{ addr, want string }{
		{"0.0.0.0:8080", "http://127.0.0.1:8080"},
		{"[::]:9000", "http://127.0.0.1:9000"},
		{"192.168.1.10:8080", "http://192.168.1.10:8080"},
	} {
		got, err := browserURL(item.addr)
		if err != nil || got != item.want {
			t.Fatal(item, got, err)
		}
	}
	if _, err := browserURL("invalid"); err == nil {
		t.Fatal("invalid address accepted")
	}
}

func TestExistingInstanceDetectionAndReuse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/health" {
			t.Error(r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"service":"service-lab"}`))
	}))
	defer server.Close()
	if !runningInstance(server.URL) {
		t.Fatal("instance not recognized")
	}
	if err := run(strings.TrimPrefix(server.URL, "http://"), t.TempDir(), false); err != nil {
		t.Fatal("instance reuse failed", err)
	}
}

func TestOtherServiceIsNotReused(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"service":"another-app"}`)) }))
	defer server.Close()
	if runningInstance(server.URL) {
		t.Fatal("wrong service recognized")
	}
	if err := run(strings.TrimPrefix(server.URL, "http://"), t.TempDir(), false); err == nil {
		t.Fatal("port conflict hidden")
	}
}
