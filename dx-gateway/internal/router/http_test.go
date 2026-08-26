package router

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"dx-gateway/internal/model"
)

// backend spins up a tiny httptest.Server that just echoes a fixed label,
// and returns its "127.0.0.1:port" target string for use in a Route.
func backend(t *testing.T, label string) (target string, closeFn func()) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(label))
	}))
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse backend URL: %v", err)
	}
	return u.Host, srv.Close
}

func TestHTTPRouter_RoutesByHostAndPath(t *testing.T) {
	targetA, closeA := backend(t, "backend-a")
	defer closeA()
	targetB, closeB := backend(t, "backend-b")
	defer closeB()

	r := NewHTTPRouter()
	r.SetRoutes([]Route{
		{Key: model.RouteKey{Host: "a.example.com", Path: "/"}, Target: targetA, Remark: "A"},
		{Key: model.RouteKey{Host: "b.example.com", Path: "/"}, Target: targetB, Remark: "B"},
	})

	body := doRequest(t, r, "a.example.com", "/anything")
	if body != "backend-a" {
		t.Errorf("expected backend-a, got %q", body)
	}

	body = doRequest(t, r, "b.example.com", "/anything")
	if body != "backend-b" {
		t.Errorf("expected backend-b, got %q", body)
	}
}

func TestHTTPRouter_LongestPathPrefixWins(t *testing.T) {
	targetRoot, closeRoot := backend(t, "root")
	defer closeRoot()
	targetSpecific, closeSpecific := backend(t, "specific")
	defer closeSpecific()

	r := NewHTTPRouter()
	r.SetRoutes([]Route{
		{Key: model.RouteKey{Host: "test.example.com", Path: "/"}, Target: targetRoot, Remark: "root"},
		{Key: model.RouteKey{Host: "test.example.com", Path: "/ws-special"}, Target: targetSpecific, Remark: "specific"},
	})

	if got := doRequest(t, r, "test.example.com", "/ws-special/x"); got != "specific" {
		t.Errorf("expected the more specific /ws-special route to win, got %q", got)
	}
	if got := doRequest(t, r, "test.example.com", "/other"); got != "root" {
		t.Errorf("expected the catch-all / route for an unrelated path, got %q", got)
	}
}

func TestHTTPRouter_NoMatchReturns404_NeverFallsBackSilently(t *testing.T) {
	target, closeSrv := backend(t, "should-not-be-hit")
	defer closeSrv()

	r := NewHTTPRouter()
	r.SetRoutes([]Route{
		{Key: model.RouteKey{Host: "known.example.com", Path: "/"}, Target: target, Remark: "known"},
	})

	resp := doRequestRaw(t, r, "unknown.example.com", "/")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for unknown host, got %d", resp.StatusCode)
	}
}

func TestHTTPRouter_EmptyHostMatchesAnyHost(t *testing.T) {
	target, closeSrv := backend(t, "path-only-backend")
	defer closeSrv()

	r := NewHTTPRouter()
	// Simulates an inbound with no wsSettings.host set — the watcher logs a
	// warning about this (see watcher/sync.go), but the router itself must
	// still route it correctly on path alone.
	r.SetRoutes([]Route{
		{Key: model.RouteKey{Host: "", Path: "/anyhost-path"}, Target: target, Remark: "no-host-set"},
	})

	if got := doRequest(t, r, "totally-random-domain.com", "/anyhost-path"); got != "path-only-backend" {
		t.Errorf("expected path-only match to succeed regardless of host, got %q", got)
	}
}

func TestHTTPRouter_SetRoutes_ReportsAddedAndRemoved(t *testing.T) {
	targetA, closeA := backend(t, "a")
	defer closeA()
	targetB, closeB := backend(t, "b")
	defer closeB()

	r := NewHTTPRouter()
	added, removed := r.SetRoutes([]Route{
		{Key: model.RouteKey{Host: "a.example.com", Path: "/"}, Target: targetA},
	})
	if len(added) != 1 || len(removed) != 0 {
		t.Fatalf("first SetRoutes: expected 1 added/0 removed, got %d/%d", len(added), len(removed))
	}

	added, removed = r.SetRoutes([]Route{
		{Key: model.RouteKey{Host: "b.example.com", Path: "/"}, Target: targetB},
	})
	if len(added) != 1 || len(removed) != 1 {
		t.Fatalf("second SetRoutes: expected 1 added/1 removed, got %d/%d", len(added), len(removed))
	}
}

func doRequest(t *testing.T, r *HTTPRouter, host, path string) string {
	t.Helper()
	resp := doRequestRaw(t, r, host, path)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return string(b)
}

func doRequestRaw(t *testing.T, r *HTTPRouter, host, path string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = host
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec.Result()
}
