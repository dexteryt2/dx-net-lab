# DX Net Lab

Ephemeral GitHub-Actions-runner-as-VPN-server, exposed through a stable
Cloudflare Tunnel hostname, with automatic per-inbound routing via
DX-Gateway.

```
.
├── .github/workflows/
│   └── ubuntu-cloudflare.yml   # orchestration: spins up the runner,
│                                 installs x-ui, builds + runs DX-Gateway,
│                                 wires up the Cloudflare Tunnel
└── dx-gateway/                  # the actual routing service — see
                                  # dx-gateway/README.md for how it works
```

## How the two parts relate

`ubuntu-cloudflare.yml` does NOT contain any routing logic itself. Its job is
purely infrastructure: install x-ui, get an x-ui API token automatically
(`x-ui setting -getApiToken true` — no manual secret needed), `go build` the
service in `dx-gateway/`, run the resulting binary, then point a Cloudflare
Tunnel ingress rule at it.

`dx-gateway/` is the actual program: it discovers x-ui's inbounds via its
API and reverse-proxies WS/XHTTP traffic to whichever local port each
inbound is really listening on — see `dx-gateway/README.md` for the full
architecture, scope (v0.1 = WS/XHTTP only, TCP/TLS-SNI are stubbed for
later), and how to run/test it standalone outside of CI.

The workflow expects `dx-gateway/go.mod` to exist at exactly this path in
the repository — if you rename or move the folder, update `DX_GATEWAY_DIR`
in the workflow's `env:` block to match.

## First-time setup

1. Commit this whole repository as-is (workflow + `dx-gateway/` together).
2. Add the required repository secrets: `CLOUDFLARE_API_TOKEN`,
   `CLOUDFLARE_ACCOUNT_ID`, `CLOUDFLARE_ZONE_ID`, `TUNNEL_NAME`,
   `TUNNEL_HOSTNAME`, `ROOT_PASSWORD`, and optionally `TUNNEL_HOSTNAME_VPN`
   / `TUNNEL_HOSTNAME_SSH`.
3. Run the workflow (`workflow_dispatch`). No `XUI_API_TOKEN` secret is
   needed — the workflow generates one automatically each run.
