package router

// TCPRouter is intentionally NOT implemented in v0.1.
//
// Why: raw TCP inbounds (VLESS-TCP with security=none, in particular) carry
// no metadata a gateway can use to pick a destination — no Host header, no
// TLS ClientHello/SNI. The only viable signal is the public port the client
// connected on, which in turn requires Cloudflare to forward that exact
// port to this Tunnel at all. On Cloudflare's free/Pro tiers, a *proxied*
// hostname only forwards a fixed set of HTTP(S) ports
// (80/8080/8880/2052/2082/2086/2095 and 443/2053/2083/2087/2096/8443) to the
// Tunnel — arbitrary ports like 24248 never reach cloudflared at all,
// regardless of anything implemented here. Raw TCP passthrough for
// arbitrary ports requires Cloudflare Spectrum (a separate paid product),
// which must be confirmed available on the account BEFORE this router is
// built, or the work is untestable.
//
// When that's confirmed, implement a TCPRouter here following the same
// pattern as HTTPRouter (Route table behind an atomic.Pointer swapped by
// watcher.Sync), but keyed by ListenPort instead of Host+Path, i.e.:
//
//   type TCPRoute struct {
//       ListenPort int    // the port Cloudflare Spectrum forwards
//       Target     string // 127.0.0.1:<real xray port>
//   }
//
// Each TCPRoute needs its own net.Listener (unlike the HTTP router, which
// shares one listener and dispatches by Host/Path) since raw TCP has no way
// to multiplex several destinations behind a single accept loop without
// protocol-level metadata.
