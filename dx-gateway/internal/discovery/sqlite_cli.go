package discovery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"dx-gateway/internal/model"
)

// SQLiteClient discovers inbounds by shelling out to the sqlite3 CLI in
// read-only mode. Used only as a fallback when the x-ui API is unreachable
// (see watcher.Sync). Deliberately avoids any Go SQLite driver (CGO or pure-Go)
// so this binary has zero non-stdlib module dependencies — it only requires
// the `sqlite3` binary to be present on PATH (already installed on the
// runner for other purposes; see apt install step in ubuntu-cloudflare.yml).
//
// Table/column names below were read directly from the real 3x-ui v3.6.0
// source (internal/database/model/model.go, struct Inbound): gorm has no
// TableName() override for Inbound, so it pluralizes to "inbounds"; columns
// are gorm's default snake_case of the struct fields (id, enable, port,
// protocol, remark, stream_settings, ...).
type SQLiteClient struct {
	dbPath     string
	sqlite3Bin string
}

// NewSQLiteClient builds a SQLiteClient. dbPath should match x-ui's real
// database file (default /etc/x-ui/x-ui.db per x-ui's own install.sh).
func NewSQLiteClient(dbPath, sqlite3Bin string) *SQLiteClient {
	return &SQLiteClient{dbPath: dbPath, sqlite3Bin: sqlite3Bin}
}

// sqliteRow mirrors one row of the SELECT below. Field order/names match
// the SQL column aliases, not the JSON tags used by the API path — kept as
// a separate type so a schema surprise in one path can't silently corrupt
// the other.
type sqliteRow struct {
	ID             int    `json:"id"`
	Enable         int    `json:"enable"` // SQLite stores bool as 0/1
	Port           int    `json:"port"`
	Protocol       string `json:"protocol"`
	Remark         string `json:"remark"`
	StreamSettings string `json:"stream_settings"`
}

// FetchInbounds opens x-ui's SQLite file read-only and returns the
// normalized inbound set. Never opens the database for writing — a busy
// x-ui write in progress simply causes SQLITE_BUSY, which sqlite3 retries
// internally up to its default busy timeout.
func (c *SQLiteClient) FetchInbounds(ctx context.Context) ([]model.Inbound, error) {
	const query = `SELECT id, enable, port, protocol, remark, stream_settings FROM inbounds;`

	// -readonly: never allows a write, even by accident.
	// -json: structured output we can decode directly, no manual TSV parsing.
	// -bail: stop and return non-zero on the first SQL error instead of
	//        silently continuing (important since we're parsing stdout as JSON).
	args := []string{
		"-readonly",
		"-json",
		"-bail",
		c.dbPath,
		query,
	}

	cmd := exec.CommandContext(ctx, c.sqlite3Bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("sqlite3 query failed: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}

	out := strings.TrimSpace(stdout.String())
	if out == "" {
		// Zero inbounds is a valid (if unusual) state, not an error.
		return []model.Inbound{}, nil
	}

	var rows []sqliteRow
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		return nil, fmt.Errorf("decode sqlite3 -json output: %w", err)
	}

	result := make([]model.Inbound, 0, len(rows))
	for _, row := range rows {
		result = append(result, normalizeInbound(rawInbound{
			ID:             row.ID,
			Enable:         row.Enable != 0,
			Remark:         row.Remark,
			Port:           row.Port,
			Protocol:       row.Protocol,
			StreamSettings: row.StreamSettings,
		}))
	}
	return result, nil
}
