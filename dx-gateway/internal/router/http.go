// Package router implements DX-Gateway's traffic routing. For v0.1 only the
// HTTP (host+path) router is implemented — see tcp.go and tls.go for the
// deliberately unimplemented stubs reserved for a later phase.
package router

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"dx-gateway/internal/model"
)

// Route is one entry DX-Gateway proxies to: match on Host (optional — empty
// means "any host") and a path prefix, forward to Target (127.0.0.1:port).
type Route struct {
	Key    model.RouteKey
	Target string // e.g. "127.0.0.1:443"
	Remark string // for logging only
}

// compiledRoute pairs a Route with its pre-built reverse proxy so ServeHTTP
// never allocates a new proxy per request.
type compiledRoute struct {
	route Route
	proxy *httputil.ReverseProxy
}

// HTTPRouter is an http.Handler that dispatches by Host+path-prefix to one
// of several local backends. The active route table is swapped atomically —
// in-flight requests on unaffected routes are never interrupted by a
// SetRoutes call (no server restart, no lock held across a full request).
type HTTPRouter struct {
	// table holds *[]compiledRoute, sorted by path length descending so the
	// most specific path prefix always wins. Read via Load() on every
	// request; replaced wholesale (never mutated in place) by SetRoutes.
	table atomic.Pointer[[]compiledRoute]

	logMu sync.Mutex // serializes our own log lines only, not routing
}

// NewHTTPRouter returns a router with an empty route table (every request
// will 404 until the first SetRoutes call).
func NewHTTPRouter() *HTTPRouter {
	r := &HTTPRouter{}
	empty := []compiledRoute{}
	r.table.Store(&empty)
	return r
}

// SetRoutes atomically replaces the entire route table. Pass the full
// desired set every time (the watcher recomputes it from scratch each sync
// tick) — SetRoutes does not merge with the previous table. Returns the set
// of routes that were added and removed relative to the previous table, so
// the caller (watcher.Sync) can log a clean diff without keeping its own
// duplicate bookkeeping.
func (r *HTTPRouter) SetRoutes(routes []Route) (added, removed []Route) {
	prevPtr := r.table.Load()
	prev := map[model.RouteKey]Route{}
	if prevPtr != nil {
		for _, cr := range *prevPtr {
			prev[cr.route.Key] = cr.route
		}
	}

	next := make([]compiledRoute, 0, len(routes))
	seen := map[model.RouteKey]bool{}
	for _, rt := range routes {
		seen[rt.Key] = true
		if old, ok := prev[rt.Key]; !ok || old.Target != rt.Target {
			added = append(added, rt)
		}
		next = append(next, compiledRoute{route: rt, proxy: newReverseProxy(rt.Target)})
	}
	for key, old := range prev {
		if !seen[key] {
			removed = append(removed, old)
		}
	}

	// Longest path prefix first, so "/abc/def" is tried before "/abc"
	// before "" (any path) for the same host.
	sort.Slice(next, func(i, j int) bool {
		return len(next[i].route.Key.Path) > len(next[j].route.Key.Path)
	})

	r.table.Store(&next)
	return added, removed
}

func newReverseProxy(target string) *httputil.ReverseProxy {
	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = "http"
			req.URL.Host = target
			// Preserve the original Host header so x-ui's own inbound
			// (and any wsSettings.host check inside Xray) sees the same
			// Host the client sent, not the internal target address.
		},
		ErrorLog: log.Default(),
	}
	// WebSocket upgrade headers (Connection/Upgrade) pass through
	// unmodified by default via ReverseProxy in Go 1.12+; no extra
	// wiring needed as long as we don't strip them in Director.
	return proxy
}

// ServeHTTP matches the incoming request against the route table (longest
// path prefix wins for a matching host; a route with an empty Host matches
// any host) and proxies it. No match -> explicit 404, never a silent
// fallback to some default backend.
func (r *HTTPRouter) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	tablePtr := r.table.Load()
	if tablePtr == nil {
		http.NotFound(w, req)
		return
	}

	host := stripPort(req.Host)
	path := req.URL.Path

	for _, cr := range *tablePtr {
		key := cr.route.Key
		if key.Host != "" && !strings.EqualFold(key.Host, host) {
			continue
		}
		if key.Path != "" && !strings.HasPrefix(path, key.Path) {
			continue
		}
		cr.proxy.ServeHTTP(w, req)
		return
	}

	r.logMu.Lock()
	log.Printf("[ROUTER] 404 no match for host=%s path=%s", host, path)
	r.logMu.Unlock()
	http.NotFound(w, req)
}

func stripPort(hostport string) string {
	if u, err := url.Parse("//" + hostport); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	return hostport
}
