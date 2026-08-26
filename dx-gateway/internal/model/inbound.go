// Package model holds the normalized, discovery-source-agnostic
// representation of an x-ui inbound. Both discovery backends (the x-ui REST
// API and the read-only SQLite fallback) produce []Inbound, so nothing
// downstream (router, watcher) needs to know or care which one was used.
package model

import "fmt"

// Inbound is DX-Gateway's own normalized view of an x-ui inbound. It is
// deliberately much smaller than x-ui's real model.Inbound (see
// 3x-ui/internal/database/model/model.go) — we only keep what routing needs.
type Inbound struct {
	ID       int    // x-ui inbound primary key (informational, used in logs only)
	Enable   bool   // x-ui "enable" column/field
	Protocol string // vless, vmess, trojan, ...
	Port     int    // the real local port Xray is listening on (127.0.0.1:Port)
	Network  string // parsed from streamSettings.network: ws, xhttp, tcp, grpc, ...
	Security string // parsed from streamSettings.security: none, tls, reality
	Host     string // wsSettings.host / xhttpSettings.host (may be empty)
	Path     string // wsSettings.path / xhttpSettings.path (defaults to "/")
	Remark   string // human label, log/debug only
}

// SupportsHostPathRouting reports whether this inbound is something DX-Gateway
// v0.1 knows how to route (plain HTTP-ish transports). Raw TCP and TLS/REALITY
// inbounds are intentionally out of scope for v0.1 — see router/tcp.go and
// router/tls.go for the (stubbed) home for that logic later.
func (ib Inbound) SupportsHostPathRouting() bool {
	switch ib.Network {
	case "ws", "xhttp", "http":
		return true
	default:
		return false
	}
}

// RouteKey is the lookup key the HTTP router matches incoming requests
// against: (Host header, path prefix).
type RouteKey struct {
	Host string
	Path string
}

func (k RouteKey) String() string {
	host := k.Host
	if host == "" {
		host = "<any-host>"
	}
	return fmt.Sprintf("host=%s path=%s", host, k.Path)
}
