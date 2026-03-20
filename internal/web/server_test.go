package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/spectertian/go-bt/internal/store"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	f, err := os.CreateTemp("", "go-bt-web-test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })

	db, err := store.New(f.Name())
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewServer(db, ":0")
}

func TestIndexHandler(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusOK)
	}
	if !strings.Contains(rr.Body.String(), "磁力搜索") {
		t.Error("index page does not contain expected text '磁力搜索'")
	}
}

func TestSearchHandlerEmpty(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/search?q=test", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestSearchHandlerWithResults(t *testing.T) {
	f, err := os.CreateTemp("", "go-bt-web-*.db")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	defer os.Remove(f.Name())

	db, err := store.New(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var ih [20]byte
	copy(ih[:], "abcde12345abcde12345")
	if err := db.Save(ih, "Ubuntu 22.04 LTS", 1024*1024*1024, 1); err != nil {
		t.Fatal(err)
	}

	srv := NewServer(db, ":0")
	req := httptest.NewRequest(http.MethodGet, "/search?q=Ubuntu", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Ubuntu 22.04 LTS") {
		t.Errorf("expected torrent name in response body")
	}
}

func TestSearchHandlerJSON(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/search?q=&format=json", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusOK)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type: got %q, want application/json", ct)
	}
}

func TestDetailHandlerNotFound(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/detail/"+strings.Repeat("a", 40), nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestDetailHandlerBadHash(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/detail/tooshort", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestStatsHandler(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "total_torrents") {
		t.Errorf("expected 'total_torrents' in JSON response")
	}
}

func TestNotFoundHandler(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestFormatSize(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KiB"},
		{1024 * 1024, "1.0 MiB"},
		{1024 * 1024 * 1024, "1.0 GiB"},
	}
	for _, tc := range cases {
		got := formatSize(tc.in)
		if got != tc.want {
			t.Errorf("formatSize(%d): got %q, want %q", tc.in, got, tc.want)
		}
	}
}
