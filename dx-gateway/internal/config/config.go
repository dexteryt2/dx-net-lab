// Package config loads DX-Gateway's configuration from environment
// variables only (see .env.example at the repo root for the full list).
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config is DX-Gateway's fully resolved runtime configuration.
type Config struct {
	// ListenAddr is where DX-Gateway itself listens. This is the address
	// Cloudflare Tunnel's ingress rule must point at (e.g. http://localhost:10000).
	ListenAddr string

	// XUIURL is the base URL of the x-ui panel INCLUDING its webBasePath,
	// e.g. "http://127.0.0.1:37801/MIUS6gT4n83bTmWxI0". No trailing slash.
	XUIURL string

	// XUIAPIToken is a Bearer token created in x-ui under
	// Settings -> API Tokens -> Create. Using a token (instead of
	// username/password) skips x-ui's CSRF/session flow entirely — see
	// 3x-ui internal/web/middleware/security.go (api_authed bypasses CSRF)
	// and internal/web/controller/api.go (checkAPIAuth honors "Bearer ...").
	XUIAPIToken string

	// XUIDBPath is the path to x-ui's SQLite file, used only as a fallback
	// when the API is unreachable. Default matches x-ui's own install.sh.
	XUIDBPath string

	// SQLite3Bin is the sqlite3 CLI binary used for the fallback discovery
	// path (read-only). DX-Gateway shells out to it instead of linking a
	// SQLite driver, so the binary has zero non-stdlib Go dependencies.
	SQLite3Bin string

	SyncInterval          time.Duration
	APIFailureThreshold   int
	HTTPRequestTimeout    time.Duration
	LogLevel              string
}

// Load reads and validates configuration from the environment. It returns an
// error naming the first missing/invalid required variable rather than
// panicking, so main() can print a clean, actionable message.
func Load() (Config, error) {
	cfg := Config{
		ListenAddr:          getEnv("LISTEN_ADDR", "0.0.0.0:10000"),
		XUIURL:              os.Getenv("XUI_URL"),
		XUIAPIToken:         os.Getenv("XUI_API_TOKEN"),
		XUIDBPath:           getEnv("XUI_DB_PATH", "/etc/x-ui/x-ui.db"),
		SQLite3Bin:          getEnv("SQLITE3_BIN", "sqlite3"),
		LogLevel:            getEnv("LOG_LEVEL", "info"),
	}

	if cfg.XUIURL == "" {
		return cfg, fmt.Errorf("XUI_URL is required, e.g. http://127.0.0.1:37801/<webBasePath>")
	}
	if cfg.XUIAPIToken == "" {
		return cfg, fmt.Errorf("XUI_API_TOKEN is required (create one in x-ui: Settings -> API Tokens)")
	}

	syncSeconds, err := getEnvInt("SYNC_INTERVAL_SECONDS", 5)
	if err != nil {
		return cfg, err
	}
	if syncSeconds < 1 {
		return cfg, fmt.Errorf("SYNC_INTERVAL_SECONDS must be >= 1, got %d", syncSeconds)
	}
	cfg.SyncInterval = time.Duration(syncSeconds) * time.Second

	threshold, err := getEnvInt("DISCOVERY_API_FAILURE_THRESHOLD", 3)
	if err != nil {
		return cfg, err
	}
	if threshold < 1 {
		return cfg, fmt.Errorf("DISCOVERY_API_FAILURE_THRESHOLD must be >= 1, got %d", threshold)
	}
	cfg.APIFailureThreshold = threshold

	timeoutSeconds, err := getEnvInt("HTTP_REQUEST_TIMEOUT_SECONDS", 10)
	if err != nil {
		return cfg, err
	}
	cfg.HTTPRequestTimeout = time.Duration(timeoutSeconds) * time.Second

	return cfg, nil
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) (int, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return def, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer, got %q: %w", key, raw, err)
	}
	return v, nil
}
