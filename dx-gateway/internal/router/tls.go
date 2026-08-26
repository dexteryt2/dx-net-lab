package router

// TLSRouter (SNI-sniffing router) is intentionally NOT implemented in v0.1.
//
// Scope for later: inbounds with network=tcp and security=tls or
// security=reality (REALITY) do carry a TLS ClientHello with an SNI
// extension, which — unlike plain TCP — IS a usable routing signal without
// needing Cloudflare Spectrum, AS LONG AS the gateway terminates or peeks at
// the TLS layer itself rather than Cloudflare doing it. In DX-Gateway's
// current position behind Cloudflare Tunnel's HTTP ingress, TLS is already
// terminated at Cloudflare's edge before traffic reaches cloudflared/this
// process, so there is no ClientHello left to sniff by the time a request
// gets here — see the note in router/tcp.go about Cloudflare's fixed proxied
// port list, which applies here too.
//
// A future TLSRouter therefore only makes sense once raw TCP passthrough
// (Spectrum, or bypassing Cloudflare Tunnel with a direct exposed port) is
// in place — at that point this file would implement a plain
// net.Listener.Accept() loop, peek the first TLS record without consuming
// it (bufio.Reader + tls.Client/ClientHelloInfo via
// tls.Config{GetConfigForClient: ...} or a manual ClientHello parse), read
// ServerName, and dial the matching backend — then splice the two
// connections (io.Copy both directions) rather than terminating TLS here,
// since Xray itself still needs to see the real ClientHello for REALITY to
// work.
//
// SNI collisions (two inbounds sharing one SNI, as flagged during earlier
// design discussion) mean SNI alone is not a fully general routing key —
// when this is built, keep the routing-policy layer (config.go) able to
// combine SNI with ALPN or a dedicated port per SNI-router entry, rather
// than assuming SNI is always unique.
