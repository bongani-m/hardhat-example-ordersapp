package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStaticIndex(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	staticFiles().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Orders") {
		t.Fatalf("body %q", rr.Body.String())
	}
}

func TestRoutePrecedence(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /up", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("up"))
	})
	mux.HandleFunc("GET /orders", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("list"))
	})
	mux.HandleFunc("GET /orders/{id}", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.PathValue("id")))
	})
	mux.HandleFunc("POST /orders", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("created"))
	})
	mux.Handle("GET /", staticFiles())

	checks := []struct {
		method string
		path   string
		want   string
	}{
		{http.MethodGet, "/up", "up"},
		{http.MethodGet, "/orders", "list"},
		{http.MethodGet, "/orders/4", "4"},
		{http.MethodPost, "/orders", "created"},
		{http.MethodGet, "/", "Orders"},
	}
	for _, check := range checks {
		req := httptest.NewRequest(check.method, check.path, nil)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), check.want) {
			t.Fatalf("%s %s: status %d body %q", check.method, check.path, rr.Code, rr.Body.String())
		}
	}
}

func TestStaticMissingAsset(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/assets/missing.js", nil)
	rr := httptest.NewRecorder()
	staticFiles().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status %d", rr.Code)
	}
}
