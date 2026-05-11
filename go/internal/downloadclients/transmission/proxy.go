package transmission

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"sonarr.tv/sonarr/internal/downloadclients"
)

// HTTPDoer is the narrow http-client interface [Proxy] consumes. The
// production binding is `*http.Client` (or the project's
// `*httpclient.Client`).
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Proxy is the Transmission RPC wrapper. Mirrors C#
// `TransmissionProxy` at `:31-375` but ships only the endpoints
// sub-arc 14b's Provider consumes (torrent-add + session-get for
// the version probe + torrent-set for seeding configuration).
//
// Session-ID auth is the Transmission-specific quirk: the first
// request without a session ID gets a 409 Conflict carrying
// X-Transmission-Session-Id; subsequent requests echo that header.
// On 409 the proxy re-fetches the session ID and replays the
// request. Matches C# `:263-305` + `:332-339`.
//
// The session-ID cache is keyed on baseURL+password to mirror C#
// `:265`. Goroutine-safe via sync.RWMutex.
type Proxy struct {
	doer HTTPDoer
	log  *slog.Logger

	mu        sync.RWMutex
	sessionID map[string]string
}

// NewProxy binds the deps. doer is REQUIRED; log defaults to
// slog.Default() when nil.
func NewProxy(doer HTTPDoer, log *slog.Logger) (*Proxy, error) {
	if doer == nil {
		return nil, errors.New("transmission: NewProxy: nil HTTPDoer")
	}
	if log == nil {
		log = slog.Default()
	}
	return &Proxy{doer: doer, log: log, sessionID: map[string]string{}}, nil
}

// BuildBaseURL composes the absolute Transmission RPC URL. Mirrors
// `TransmissionProxy.GetBaseUrl` at `:245-248`.
func (p *Proxy) BuildBaseURL(settings Settings) string {
	scheme := "http"
	if settings.UseSsl {
		scheme = "https"
	}
	base := fmt.Sprintf("%s://%s:%d", scheme, settings.Host, settings.Port)
	ub := strings.TrimSpace(settings.UrlBase)
	if ub != "" {
		if !strings.HasPrefix(ub, "/") {
			ub = "/" + ub
		}
		base += strings.TrimRight(ub, "/")
	}
	return base + "/rpc"
}

// Response is the canonical Transmission RPC reply shape. Mirrors C#
// `TransmissionResponse.cs:1-10`. Arguments is left as RawMessage so
// per-endpoint callers can deserialise into the typed shape they
// need.
type Response struct {
	Result    string          `json:"result"`
	Tag       int             `json:"tag"`
	Arguments json.RawMessage `json:"arguments"`
}

// rpcRequest is the canonical request envelope.
type rpcRequest struct {
	Method    string      `json:"method"`
	Arguments interface{} `json:"arguments,omitempty"`
	Tag       int         `json:"tag,omitempty"`
}

// AddTorrentFromURL fires `torrent-add` with a magnet or HTTP(S) URL
// as the `filename` argument. Mirrors C# `:57-76`. Returns the
// added torrent's hashString (the canonical DownloadID Sonarr
// tracks against).
//
// When downloadDirectory is empty, Transmission falls back to the
// configured RPC-server download-dir (and the label-based
// per-category subfolder if configured).
func (p *Proxy) AddTorrentFromURL(ctx context.Context, torrentURL, downloadDirectory string, settings Settings) (string, error) {
	args := map[string]any{
		"filename": torrentURL,
		"paused":   settings.AddPaused,
	}
	if downloadDirectory != "" {
		args["download-dir"] = downloadDirectory
	}
	if strings.TrimSpace(settings.TvCategory) != "" {
		args["labels"] = []string{settings.TvCategory}
	}
	return p.addTorrent(ctx, args, settings)
}

// AddTorrentFromData fires `torrent-add` with a base64-encoded
// .torrent payload. Mirrors C# `:78-97`.
func (p *Proxy) AddTorrentFromData(ctx context.Context, torrentData []byte, downloadDirectory string, settings Settings) (string, error) {
	args := map[string]any{
		"metainfo": base64.StdEncoding.EncodeToString(torrentData),
		"paused":   settings.AddPaused,
	}
	if downloadDirectory != "" {
		args["download-dir"] = downloadDirectory
	}
	if strings.TrimSpace(settings.TvCategory) != "" {
		args["labels"] = []string{settings.TvCategory}
	}
	return p.addTorrent(ctx, args, settings)
}

// addTorrent dispatches torrent-add + extracts the hashString from
// either the `torrent-added` or `torrent-duplicate` arms. Mirrors
// the Transmission RPC behaviour at the protocol level (not directly
// surfaced in C# Proxy — the C# version returns void and discards
// the hash; the Go port returns it so the orchestrator can capture
// the DownloadID).
func (p *Proxy) addTorrent(ctx context.Context, args map[string]any, settings Settings) (string, error) {
	resp, err := p.processRequest(ctx, "torrent-add", args, settings)
	if err != nil {
		return "", err
	}
	var payload struct {
		TorrentAdded     *struct{ HashString string } `json:"torrent-added"`
		TorrentDuplicate *struct{ HashString string } `json:"torrent-duplicate"`
	}
	if err := json.Unmarshal(resp.Arguments, &payload); err != nil {
		return "", fmt.Errorf("transmission: parse torrent-add response: %w", err)
	}
	switch {
	case payload.TorrentAdded != nil && payload.TorrentAdded.HashString != "":
		return payload.TorrentAdded.HashString, nil
	case payload.TorrentDuplicate != nil && payload.TorrentDuplicate.HashString != "":
		return payload.TorrentDuplicate.HashString, nil
	}
	return "", ErrNoHashReturned
}

// configResponse is the subset of `session-get` we read for the
// version-floor self-check. Mirrors C# `TransmissionConfig.cs:1-21`.
type configResponse struct {
	Version    string `json:"version"`
	RPCVersion string `json:"rpc-version"`
}

// GetClientVersion fires `session-get` and returns the Transmission
// version string (e.g. "3.00 (bb6b5a062e)"). Mirrors C# `:139-149`.
//
// The C# code caches the version for 6 hours; the Go port leaves
// caching to the caller — the Test path fires this exactly once
// per Test() call so caching here would be premature.
func (p *Proxy) GetClientVersion(ctx context.Context, settings Settings) (string, error) {
	resp, err := p.processRequest(ctx, "session-get", nil, settings)
	if err != nil {
		return "", err
	}
	var cfg configResponse
	if err := json.Unmarshal(resp.Arguments, &cfg); err != nil {
		return "", fmt.Errorf("transmission: parse session-get response: %w", err)
	}
	return cfg.Version, nil
}

// SetTorrentSeedingConfiguration ports C# `:99-130`. No-op when no
// limits are configured. Used post-Download to apply per-grab seed
// caps from a [downloadclients.TorrentSeedConfiguration].
func (p *Proxy) SetTorrentSeedingConfiguration(ctx context.Context, hash string, seed *downloadclients.TorrentSeedConfiguration, settings Settings) error {
	if seed == nil {
		return nil
	}
	args := map[string]any{
		"ids": []string{hash},
	}
	if seed.Ratio != nil {
		args["seedRatioLimit"] = *seed.Ratio
		args["seedRatioMode"] = 1
	}
	if seed.SeedTime != nil {
		args["seedIdleLimit"] = int(seed.SeedTime.Minutes())
		args["seedIdleMode"] = 1
	}
	if len(args) == 1 {
		// Only "ids" — no actual limits configured. Skip the RPC.
		return nil
	}
	if _, err := p.processRequest(ctx, "torrent-set", args, settings); err != nil {
		return err
	}
	return nil
}

// processRequest is the auth-aware RPC dispatcher. Mirrors C#
// `ProcessRequest` at `:307-374` plus the AuthenticateClient flow
// at `:263-305` — the Transmission session-ID exchange.
//
// Flow:
//   - First request without session ID → expect 409 Conflict
//     carrying X-Transmission-Session-Id; cache it.
//   - Replay with the cached session-ID. 409 again → re-auth.
//   - 401 Unauthorized → ErrAuthFailed.
//   - Non-success result → ErrRPCFailure with the result string.
func (p *Proxy) processRequest(ctx context.Context, method string, args any, settings Settings) (*Response, error) {
	reqBody, err := json.Marshal(rpcRequest{Method: method, Arguments: args})
	if err != nil {
		return nil, fmt.Errorf("transmission: marshal %s: %w", method, err)
	}

	// First attempt — fetch or use cached session ID.
	sessionID, _ := p.authenticate(ctx, settings, false)
	resp, body, err := p.doRequest(ctx, reqBody, sessionID, settings)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusConflict {
		// Session ID rotated → force re-auth + replay.
		sessionID, authErr := p.authenticate(ctx, settings, true)
		if authErr != nil {
			return nil, authErr
		}
		resp, body, err = p.doRequest(ctx, reqBody, sessionID, settings)
		if err != nil {
			return nil, err
		}
	}

	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return nil, fmt.Errorf("%w: HTTP 401", ErrAuthFailed)
	case http.StatusForbidden:
		return nil, fmt.Errorf("%w: HTTP 403 (check RPC whitelist)", ErrAuthFailed)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: status=%d body=%s", ErrUnexpected, resp.StatusCode, truncate(body, 256))
	}

	var parsed Response
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("transmission: parse %s response: %w", method, err)
	}
	if parsed.Result != "success" {
		return nil, fmt.Errorf("%w: result=%q", ErrRPCFailure, parsed.Result)
	}
	return &parsed, nil
}

// authenticate returns a session ID for the (baseURL, password)
// cache key. force=true bypasses the cache. Mirrors C# `:263-305`.
func (p *Proxy) authenticate(ctx context.Context, settings Settings, force bool) (string, error) {
	key := p.BuildBaseURL(settings) + ":" + settings.Password
	if !force {
		p.mu.RLock()
		if id, ok := p.sessionID[key]; ok {
			p.mu.RUnlock()
			return id, nil
		}
		p.mu.RUnlock()
	}
	// Fire a probe request and read the session ID from the 409
	// response header. We use a session-get probe with no body to
	// keep the auth flow side-effect-free.
	probeBody, _ := json.Marshal(rpcRequest{Method: "session-get"})
	resp, _, err := p.doRequest(ctx, probeBody, "", settings)
	if err != nil {
		return "", err
	}
	switch resp.StatusCode {
	case http.StatusMovedPermanently:
		loc := resp.Header.Get("Location")
		return "", fmt.Errorf("%w: redirected to %s", ErrAuthFailed, loc)
	case http.StatusForbidden:
		return "", fmt.Errorf("%w: HTTP 403 (RPC whitelist)", ErrAuthFailed)
	case http.StatusConflict:
		id := resp.Header.Get("X-Transmission-Session-Id")
		if id == "" {
			return "", fmt.Errorf("%w: 409 without X-Transmission-Session-Id", ErrAuthFailed)
		}
		p.mu.Lock()
		p.sessionID[key] = id
		p.mu.Unlock()
		return id, nil
	case http.StatusOK:
		// Some Transmission builds skip the session-id requirement
		// (or it's pre-set elsewhere) and just return 200 on the
		// probe. Cache an empty session ID and proceed.
		return "", nil
	}
	return "", fmt.Errorf("%w: unexpected probe status %d", ErrAuthFailed, resp.StatusCode)
}

// doRequest fires one HTTP request with the supplied session ID. The
// caller drives the auth flow.
//
// Returns (response, body, error). The body is read and closed
// before returning so the caller doesn't have to.
func (p *Proxy) doRequest(ctx context.Context, body []byte, sessionID string, settings Settings) (*http.Response, []byte, error) {
	url := p.BuildBaseURL(settings)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, nil, fmt.Errorf("transmission: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if sessionID != "" {
		req.Header.Set("X-Transmission-Session-Id", sessionID)
	}
	if settings.Username != "" || settings.Password != "" {
		req.SetBasicAuth(settings.Username, settings.Password)
	}
	resp, err := p.doer.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return resp, nil, fmt.Errorf("transmission: read body: %w", readErr)
	}
	return resp, respBody, nil
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}

// Typed sentinels.
var (
	// ErrNoHashReturned — `torrent-add` returned success but no
	// torrent-added/duplicate hashString. Treated as a permanent
	// failure for the orchestrator's grab path.
	ErrNoHashReturned = errors.New("transmission: torrent-add returned no hashString")

	// ErrUnavailable — transport error reaching the Transmission
	// RPC endpoint.
	ErrUnavailable = errors.New("transmission: unavailable")

	// ErrUnexpected — non-2xx HTTP that wasn't a known auth path.
	ErrUnexpected = errors.New("transmission: unexpected response")

	// ErrAuthFailed — 401 / 403 / missing session-ID header.
	ErrAuthFailed = errors.New("transmission: authentication failed")

	// ErrRPCFailure — RPC returned result != "success".
	ErrRPCFailure = errors.New("transmission: RPC failure")
)
