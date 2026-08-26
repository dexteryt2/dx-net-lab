// Command gateway runs DX-Gateway: a standalone reverse proxy that sits
// between Cloudflare Tunnel and an unmodified x-ui installation, discovering
// x-ui's inbounds via its own REST API (with a read-only SQLite fallback)
// and routing WS/XHTTP traffic to the right local port automatically.
//
// DX-Gateway never modifies x-ui's source code, database schema, or
// configuration files — it only reads x-ui's API/database and proxies
// traffic. See internal/discovery for exactly which x-ui endpoints/tables
// are read.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"dx-gateway/internal/config"
	"dx-gateway/internal/discovery"
	"dx-gateway/internal/router"
	"dx-gateway/internal/watcher"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("[STARTUP] config error: %v", err)
	}

	log.Printf("[STARTUP] DX-Gateway v0.1 starting: listen=%s xui=%s sync_interval=%s",
		cfg.ListenAddr, cfg.XUIURL, cfg.SyncInterval)

	apiClient := discovery.NewAPIClient(cfg.XUIURL, cfg.XUIAPIToken, cfg.HTTPRequestTimeout)
	sqliteClient := discovery.NewSQLiteClient(cfg.XUIDBPath, cfg.SQLite3Bin)

	httpRouter := router.NewHTTPRouter()

	sync := watcher.New(apiClient, sqliteClient, httpRouter, cfg.SyncInterval, cfg.APIFailureThreshold)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go sync.Run(ctx)

	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           httpRouter,
		ReadHeaderTimeout: 10 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		log.Printf("[STARTUP] listening on %s", cfg.ListenAddr)
		serverErr <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErr:
		if err != nil && err != http.ErrServerClosed {
			log.Fatalf("[STARTUP] server error: %v", err)
		}
	case <-ctx.Done():
		log.Println("[SHUTDOWN] signal received, shutting down gracefully...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("[SHUTDOWN] error during shutdown: %v", err)
		}
	}
}
