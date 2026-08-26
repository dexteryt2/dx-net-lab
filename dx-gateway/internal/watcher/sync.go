// Package watcher runs the periodic discovery -> diff -> route-table-update
// loop that makes DX-Gateway "hot reload" whenever an inbound is created,
// edited, enabled/disabled, or deleted in x-ui — no gateway restart, no x-ui
// restart, no manual step.
package watcher

import (
	"context"
	"log"
	"strconv"
	"time"

	"dx-gateway/internal/model"
	"dx-gateway/internal/router"
)

// discoverer is satisfied by both discovery.APIClient and
// discovery.SQLiteClient, so Sync doesn't need to know which one it's
// calling — only whether the call succeeded.
type discoverer interface {
	FetchInbounds(ctx context.Context) ([]model.Inbound, error)
}

// Sync owns the polling loop: primary API discovery, with automatic
// fallback to the SQLite reader after apiFailureThreshold consecutive API
// failures. Falls back to SQLite for every subsequent tick until the API
// succeeds again (at which point it switches back and logs that too).
type Sync struct {
	api      discoverer
	sqlite   discoverer
	http     *router.HTTPRouter
	interval time.Duration

	apiFailureThreshold int
	consecutiveAPIFails int
	usingFallback       bool
}

// New builds a Sync loop. httpRouter is the route table Sync keeps up to date.
func New(api, sqlite discoverer, httpRouter *router.HTTPRouter, interval time.Duration, apiFailureThreshold int) *Sync {
	return &Sync{
		api:                 api,
		sqlite:              sqlite,
		http:                httpRouter,
		interval:            interval,
		apiFailureThreshold: apiFailureThreshold,
	}
}

// Run blocks, ticking every s.interval, until ctx is cancelled. Call it in
// its own goroutine from main().
func (s *Sync) Run(ctx context.Context) {
	// Run once immediately on startup rather than waiting a full interval
	// for the first route table.
	s.tick(ctx)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func (s *Sync) tick(ctx context.Context) {
	inbounds, source, ok := s.discover(ctx)
	if !ok {
		// Both sources failed (or API failed but hasn't hit the fallback
		// threshold yet) — keep serving the last known-good route table
		// rather than clearing it. Already logged inside discover().
		return
	}

	routes, skipped := buildRoutes(inbounds)
	for _, sk := range skipped {
		log.Printf("[DISCOVERY] skipping inbound id=%d remark=%q: %s", sk.ID, sk.Remark, skipReason(sk))
	}

	added, removed := s.http.SetRoutes(routes)
	for _, rt := range added {
		log.Printf("[ROUTER] registered %s -> %s (%s)", rt.Key.String(), rt.Target, rt.Remark)
	}
	for _, rt := range removed {
		log.Printf("[ROUTER] removed %s -> %s (inbound disabled/deleted or changed)", rt.Key.String(), rt.Target)
	}

	if len(added) == 0 && len(removed) == 0 {
		return // quiet tick, nothing changed — don't spam logs every 5s
	}
	log.Printf("[DISCOVERY] sync complete via %s: %d routable inbound(s), %d added, %d removed", source, len(routes), len(added), len(removed))
}

// discover always tries the API first (this is also how DX-Gateway
// self-heals: the very next tick after x-ui's API becomes reachable again,
// this call simply succeeds and we're back to normal — no separate recovery
// path needed). Only after apiFailureThreshold *consecutive* API failures
// does it make a single SQLite fallback attempt for that tick.
func (s *Sync) discover(ctx context.Context) (inbounds []model.Inbound, source string, ok bool) {
	apiInbounds, err := s.api.FetchInbounds(ctx)
	if err == nil {
		if s.usingFallback {
			log.Printf("[DISCOVERY] api reachable again, switching back from sqlite fallback")
		}
		s.consecutiveAPIFails = 0
		s.usingFallback = false
		return apiInbounds, "api", true
	}

	s.consecutiveAPIFails++
	log.Printf("[DISCOVERY] api call failed (%d/%d): %v", s.consecutiveAPIFails, s.apiFailureThreshold, err)
	if s.consecutiveAPIFails < s.apiFailureThreshold {
		return nil, "", false
	}

	if !s.usingFallback {
		s.usingFallback = true
		log.Printf("[DISCOVERY] api unreachable (%d/%d), falling back to sqlite readonly", s.consecutiveAPIFails, s.apiFailureThreshold)
	}

	sqliteInbounds, err := s.sqlite.FetchInbounds(ctx)
	if err != nil {
		log.Printf("[DISCOVERY] sqlite fallback also failed: %v", err)
		return nil, "", false
	}
	return sqliteInbounds, "sqlite", true
}

type skippedInbound struct {
	ID      int
	Remark  string
	Enabled bool
	Network string
}

func skipReason(sk skippedInbound) string {
	if !sk.Enabled {
		return "disabled in x-ui"
	}
	return "network=" + sk.Network + " not supported by DX-Gateway v0.1 (only ws/xhttp/http; see router/tcp.go and router/tls.go)"
}

// buildRoutes converts discovered inbounds into router.Route entries,
// keeping only enabled, host-path-routable inbounds. Everything else is
// returned in skipped for logging (never silently dropped).
func buildRoutes(inbounds []model.Inbound) (routes []router.Route, skipped []skippedInbound) {
	for _, ib := range inbounds {
		if !ib.Enable {
			skipped = append(skipped, skippedInbound{ID: ib.ID, Remark: ib.Remark, Enabled: false, Network: ib.Network})
			continue
		}
		if !ib.SupportsHostPathRouting() {
			skipped = append(skipped, skippedInbound{ID: ib.ID, Remark: ib.Remark, Enabled: true, Network: ib.Network})
			continue
		}
		if ib.Host == "" {
			log.Printf("[ROUTER] warning: inbound id=%d remark=%q has no wsSettings/xhttpSettings host set — falling back to path-only matching on %q; this is only safe if no other inbound shares this path", ib.ID, ib.Remark, ib.Path)
		}
		routes = append(routes, router.Route{
			Key:    model.RouteKey{Host: ib.Host, Path: ib.Path},
			Target: "127.0.0.1:" + strconv.Itoa(ib.Port),
			Remark: ib.Remark,
		})
	}
	return routes, skipped
}
