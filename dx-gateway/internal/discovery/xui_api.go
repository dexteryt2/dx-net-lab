// Package discovery implements the two ways DX-Gateway learns about x-ui's
// current inbounds: the x-ui REST API (primary) and a read-only SQLite
// fallback (see sqlite_cli.go). Neither path ever writes to x-ui's database
// or touches its source code — both are purely read-only observers.
//
// API endpoint and JSON field names below were read directly out of the
// real 3x-ui v3.6.0 source, not guessed:
//   - route:      GET {webBasePath}/panel/api/inbounds/list/slim
//     (see 3x-ui internal/web/controller/inbound.go initRouter + api.go
//     initRouter, mounted at api.Group("/panel/api").Group("/inbounds"))
//   - auth:       Authorization: Bearer <token>, matched by
//     APIController.checkAPIAuth against panel.ApiTokenService.Match(tok).
//     This path sets api_authed=true, which also makes the CSRF middleware
//     skip the request (internal/web/middleware/security.go) — no CSRF
//     token or session cookie needed at all.
//   - response:   {"success": bool, "msg": string, "obj": [...]}, obj items
//     are x-ui's model.Inbound JSON (internal/database/model/model.go),
//     with the "clients" array inside settings trimmed by the slim
//     endpoint but "streamSettings" left completely intact.
//   - streamSettings shape: top-level "network" and "security" fields, plus
//     one of "wsSettings"/"xhttpSettings" (both have flat "path"/"host"
//     string fields — see 3x-ui frontend/src/schemas/protocols/stream/
//     ws.ts and xhttp.ts).
package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"dx-gateway/internal/model"
)

// APIClient discovers inbounds via x-ui's own REST API using a Bearer token.
type APIClient struct {
	baseURL    string // e.g. http://127.0.0.1:37801/MIUS6gT4n83bTmWxI0 (no trailing slash)
	token      string
	httpClient *http.Client
}

// NewAPIClient builds an APIClient. baseURL must include x-ui's webBasePath.
func NewAPIClient(baseURL, token string, timeout time.Duration) *APIClient {
	return &APIClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// apiEnvelope mirrors the {success, msg, obj} shape every x-ui API response
// uses (see 3x-ui internal/web/controller/util.go jsonMsgObj / entity.Msg).
type apiEnvelope struct {
	Success bool            `json:"success"`
	Msg     string          `json:"msg"`
	Obj     json.RawMessage `json:"obj"`
}

// rawInbound mirrors the subset of x-ui's model.Inbound JSON we need.
// Field names/tags copied verbatim from 3x-ui internal/database/model/model.go.
type rawInbound struct {
	ID             int    `json:"id"`
	Enable         bool   `json:"enable"`
	Remark         string `json:"remark"`
	Port           int    `json:"port"`
	Protocol       string `json:"protocol"`
	StreamSettings string `json:"streamSettings"`
}

// rawStreamSettings mirrors the top-level fields of the streamSettings JSON
// blob that DX-Gateway v0.1 cares about (see ws.ts / xhttp.ts schemas).
type rawStreamSettings struct {
	Network       string `json:"network"`
	Security      string `json:"security"`
	WSSettings    *hostPathSettings `json:"wsSettings"`
	XHTTPSettings *hostPathSettings `json:"xhttpSettings"`
}

type hostPathSettings struct {
	Path string `json:"path"`
	Host string `json:"host"`
}

// FetchInbounds calls x-ui's slim inbounds list endpoint and returns the
// normalized inbound set. It returns an error for any transport, HTTP-status,
// or JSON-decoding failure — callers (watcher.Sync) are responsible for
// counting consecutive failures and triggering the SQLite fallback.
func (c *APIClient) FetchInbounds(ctx context.Context) ([]model.Inbound, error) {
	url := c.baseURL + "/panel/api/inbounds/list/slim"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20)) // 16MB sanity cap
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusNotFound {
		// x-ui's checkAPIAuth returns 401 (ajax) or 404 (non-ajax) for a bad
		///missing token — surface this distinctly so operators immediately
		// suspect the token, not a network blip.
		return nil, fmt.Errorf("authentication rejected by x-ui (HTTP %d) — check XUI_API_TOKEN and that the token is enabled in x-ui Settings > API Tokens", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected HTTP status %d from %s", resp.StatusCode, url)
	}

	var env apiEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("decode response envelope: %w", err)
	}
	if !env.Success {
		return nil, fmt.Errorf("x-ui reported failure: %s", env.Msg)
	}

	var raws []rawInbound
	if err := json.Unmarshal(env.Obj, &raws); err != nil {
		return nil, fmt.Errorf("decode inbounds list: %w", err)
	}

	out := make([]model.Inbound, 0, len(raws))
	for _, r := range raws {
		out = append(out, normalizeInbound(r))
	}
	return out, nil
}

// normalizeInbound converts x-ui's raw JSON shape into DX-Gateway's model.Inbound.
// Shared between the API and SQLite discovery paths so routing logic never
// needs to know which backend produced a given Inbound.
func normalizeInbound(r rawInbound) model.Inbound {
	ib := model.Inbound{
		ID:       r.ID,
		Enable:   r.Enable,
		Protocol: r.Protocol,
		Port:     r.Port,
		Remark:   r.Remark,
		Path:     "/",
	}

	if r.StreamSettings == "" {
		return ib
	}

	var ss rawStreamSettings
	if err := json.Unmarshal([]byte(r.StreamSettings), &ss); err != nil {
		// Malformed/unexpected streamSettings — leave Network empty so
		// SupportsHostPathRouting() returns false and this inbound is
		// simply skipped (logged by the watcher), rather than guessed at.
		return ib
	}

	ib.Network = ss.Network
	ib.Security = ss.Security

	switch ss.Network {
	case "ws":
		if ss.WSSettings != nil {
			ib.Host = ss.WSSettings.Host
			if ss.WSSettings.Path != "" {
				ib.Path = ss.WSSettings.Path
			}
		}
	case "xhttp":
		if ss.XHTTPSettings != nil {
			ib.Host = ss.XHTTPSettings.Host
			if ss.XHTTPSettings.Path != "" {
				ib.Path = ss.XHTTPSettings.Path
			}
		}
	}

	return ib
}
