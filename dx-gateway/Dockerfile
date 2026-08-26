# syntax=docker/dockerfile:1

# --- build stage -------------------------------------------------------
FROM golang:1.22-alpine AS build
WORKDIR /src

# Pure stdlib module, no go.sum to worry about — but keep the cache-friendly
# layer split in case dependencies are ever added later.
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal

# CGO_ENABLED=0: no SQLite driver is linked in-process (we shell out to the
# sqlite3 CLI at runtime instead — see internal/discovery/sqlite_cli.go), so
# the binary is fully static and needs nothing from the build image at
# runtime.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/dx-gateway ./cmd/gateway

# --- runtime stage -------------------------------------------------------
# alpine (not distroless/scratch) because the SQLite fallback path needs the
# `sqlite3` CLI binary on PATH. If you never expect to need the fallback
# (e.g. x-ui's API is always reachable in your deployment), you can switch
# this to gcr.io/distroless/static and drop the "apk add sqlite" line —
# discovery will simply log sqlite errors and never route until the API
# comes back, per internal/watcher/sync.go.
FROM alpine:3.20
RUN apk add --no-cache sqlite ca-certificates
COPY --from=build /out/dx-gateway /usr/local/bin/dx-gateway

# Values here are placeholders — override all of these at `docker run` time
# or in your compose/Railway/systemd env. See .env.example for the full list
# and comments.
ENV LISTEN_ADDR=0.0.0.0:10000 \
    XUI_DB_PATH=/etc/x-ui/x-ui.db \
    SYNC_INTERVAL_SECONDS=5 \
    DISCOVERY_API_FAILURE_THRESHOLD=3 \
    HTTP_REQUEST_TIMEOUT_SECONDS=10 \
    LOG_LEVEL=info

EXPOSE 10000
ENTRYPOINT ["/usr/local/bin/dx-gateway"]
