# DX-Gateway v0.1

A standalone reverse proxy that sits between Cloudflare Tunnel and an
**unmodified** [3x-ui](https://github.com/mhsanaei/3x-ui) panel, so every
WS/XHTTP inbound you create in the panel becomes reachable through Cloudflare
Tunnel automatically — no manual Cloudflare ingress edits, no x-ui source
changes, no restarts.

```
Cloudflare Tunnel
        |
        |  http://localhost:10000  (single ingress rule, never changes)
        v
   DX-Gateway  <-- discovers inbounds via x-ui's own API (read-only)
        |
        +--> 127.0.0.1:443    (inbound "test-ws",  Host: test.example.com)
        +--> 127.0.0.1:8443   (inbound "xhttp-1",  Host: files.example.com)
        +--> 127.0.0.1:45678  (inbound "new-one",  Host: new.example.com)
```

## What this is NOT (v0.1 scope)

Only inbounds with `network = ws`, `xhttp`, or `http` are routed. Raw TCP
(`security=none`) and TLS/REALITY inbounds are **not implemented yet** — see
`internal/router/tcp.go` and `internal/router/tls.go` for exactly why and
what's needed before they can be (short version: Cloudflare's free/Pro tiers
only forward a fixed list of HTTP(S) ports to a Tunnel at all; raw TCP needs
Cloudflare Spectrum, which must be confirmed available before that work is
even testable).

## How it works

1. **Discovery** (`internal/discovery`): every `SYNC_INTERVAL_SECONDS`,
   DX-Gateway calls x-ui's own `GET {webBasePath}/panel/api/inbounds/list/slim`
   endpoint using a Bearer token (create one in x-ui: **Settings → API
   Tokens → Create** — do not use your admin password here). If the API
   fails `DISCOVERY_API_FAILURE_THRESHOLD` times in a row, it falls back to
   reading x-ui's SQLite file directly, read-only, via the `sqlite3` CLI
   (no Go SQLite driver, no CGO). It switches back to the API automatically
   the moment it's reachable again.
2. **Routing** (`internal/router`): builds a Host+path-prefix lookup table
   from whatever was discovered and swaps it in atomically — in-flight
   requests on unrelated routes are never interrupted. No match → explicit
   HTTP 404, never a silent fallback to some default backend.
3. **main.go**: wires the above together, listens on `LISTEN_ADDR`
   (`0.0.0.0:10000` by default — point your Cloudflare Tunnel ingress rule
   here), and shuts down gracefully on SIGTERM/SIGINT.

DX-Gateway never writes to x-ui's database and never touches x-ui's source
files — it only reads x-ui's public API (or its SQLite file, read-only).

## Running it

### 1. Create an x-ui API token

In the x-ui panel: **Settings → API Tokens → Create**. Copy the token value.

### 2. Configure

```bash
cp .env.example .env
# edit .env: set XUI_URL and XUI_API_TOKEN at minimum
```

### 3. Build and run (needs Go 1.22+ and network access to fetch nothing —
this module has zero external dependencies, so `go build` works offline
once the Go toolchain itself is installed)

```bash
go build -o dx-gateway ./cmd/gateway
set -a; source .env; set +a
./dx-gateway
```

### 4. Or run in Docker

```bash
docker build -t dx-gateway .
docker run --rm --network host --env-file .env dx-gateway
```

`--network host` (or an equivalent) is needed so DX-Gateway can reach both
x-ui's `127.0.0.1:<panel_port>` and every inbound's `127.0.0.1:<port>` — all
of these are loopback-only in the intended deployment.

### 5. Point Cloudflare Tunnel at it

In your `ingress` config (or the Cloudflare API call that creates it),
change the panel's direct port to DX-Gateway's port:

```yaml
ingress:
  - hostname: ${TUNNEL_HOSTNAME}
    service: http://localhost:10000   # <-- DX-Gateway, not the panel directly
  - service: http_status:404
```

### 6. Manual smoke test without Cloudflare at all

```bash
# assumes an inbound exists in x-ui with wsSettings.host = test.example.com
# listening on 127.0.0.1:443
curl -v -H "Host: test.example.com" http://127.0.0.1:10000/
```

Watch the log for a line like:

```
[ROUTER] registered host=test.example.com path=/ -> 127.0.0.1:443 (VLESS-WS)
```

Create a new inbound in the live panel and, within `SYNC_INTERVAL_SECONDS`,
you should see a new `[ROUTER] registered ...` line appear with no restart.

## Testing the fallback path

```bash
# temporarily point XUI_URL at a port nothing is listening on
XUI_URL=http://127.0.0.1:1 ./dx-gateway
```

After `DISCOVERY_API_FAILURE_THRESHOLD` ticks you should see:

```
[DISCOVERY] api unreachable (3/3), falling back to sqlite readonly
```

and routing should keep working from the SQLite-derived data (requires
`XUI_DB_PATH` to point at a real, readable `x-ui.db`).

## Unit tests

```bash
go test ./...
```

Covers the router's Host+path matching, longest-prefix-wins behavior, the
explicit-404-never-silent-fallback guarantee, and the added/removed diff
reporting used for logging.

## Project layout

```
cmd/gateway/main.go          entry point, wiring, graceful shutdown
internal/config/             env var loading + validation
internal/model/               normalized Inbound type shared by both
                               discovery backends
internal/discovery/
  xui_api.go                  primary: x-ui REST API (Bearer token)
  sqlite_cli.go                fallback: read-only `sqlite3 -json` shell-out
internal/router/
  http.go                      v0.1: Host+path routing (implemented)
  tcp.go                       stub — see file header for why/what's needed
  tls.go                       stub — see file header for why/what's needed
internal/watcher/sync.go      polling loop, API/SQLite failover, applies
                               routes to the router
```

## A known limitation to flag before relying on this in production

`internal/router/http.go`'s "empty Host matches any host" fallback (for
inbounds where `wsSettings.host`/`xhttpSettings.host` was left blank) is
only safe if at most one such inbound shares a given path. DX-Gateway logs a
`[ROUTER] warning: ... falling back to path-only matching` line when this
happens — if you see it for more than one inbound on the same path, set an
explicit Host in that inbound's WS/XHTTP settings in x-ui.
