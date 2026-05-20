package deluge

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
	"sync/atomic"

	"sonarr.tv/sonarr/internal/downloadclients"
)

// HTTPDoer is the narrow http-client interface [Proxy] consumes. The
// production binding is `*http.Client` (or the project's
// `*httpclient.Client`). Mirrors the family pattern at
// `internal/downloadclients/qbittorrent/proxy.go:21-23` /
// `transmission/proxy.go:21-23`.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Proxy is the Deluge deluge-web JSON-RPC wrapper. Ports C#
// `DelugeProxy` at
// `src/NzbDrone.Core/Download/Clients/Deluge/DelugeProxy.cs:14-282`.
//
// Deluge speaks a thin JSON-RPC dialect over HTTP at the `<base>/json`
// endpoint: every call posts `{"method":"<name>","params":[...],"id":N}`
// and reads back `{"result":<any>,"error":<{code,message}|null>,"id":N}`.
// Auth is a single `auth.login` call taking the password as its only
// positional argument; success returns a `_session_id` cookie that
// subsequent calls replay.
//
// The error-code 1 (auth required) / 2 (session expired) arm triggers
// a reauth-then-retry-once flow that mirrors the C# `ProcessRequest`
// branch at `DelugeProxy.cs:213-227`.
//
// The session cookie cache is keyed on (baseURL, password) to mirror
// the C# cache key at `DelugeProxy.cs:325`.
type Proxy struct {
	doer HTTPDoer
	log  *slog.Logger

	mu      sync.RWMutex
	cookies map[string]string // authKey → _session_id cookie value

	nextID atomic.Int64
}

// Auth-flow + transport sentinel errors. Wrapping these via
// fmt.Errorf("%w: …", …) lets callers `errors.Is` against the typed
// arm without losing the human-readable context.
var (
	// ErrAuthFailed surfaces an auth.login round-trip that returned
	// result=false in the JSON-RPC body, or a retry-after-reauth
	// that still hit the auth-required error arm. Mirrors C#
	// `DownloadClientAuthenticationException` at `DelugeProxy.cs:335,225`.
	ErrAuthFailed = errors.New("deluge: authentication failed")

	// ErrUnavailable surfaces a transport-level failure (connection
	// refused, TLS handshake, etc.). Mirrors C#
	// `DownloadClientUnavailableException` at `:272,277`.
	ErrUnavailable = errors.New("deluge: unavailable")

	// ErrUnexpected surfaces a 5xx / unknown-status / malformed
	// response.
	ErrUnexpected = errors.New("deluge: unexpected response")

	// ErrRPCFailure surfaces a JSON-RPC method that returned a
	// non-auth `error` object (code != 1 && code != 2). Mirrors C#
	// `DelugeException` at `:228,309`.
	ErrRPCFailure = errors.New("deluge: RPC failure")

	// ErrAddFailed surfaces a core.add_torrent_* reply of JSON
	// null / empty string — deluge's add-error sentinel. Mirrors
	// the C# `DownloadClientException` thrown at
	// `Deluge.cs:64,90`.
	ErrAddFailed = errors.New("deluge: torrent add failed")
)

// Error-code constants the auth-retry branch keys on.
const (
	// ErrorCodeAuthRequired is the JSON-RPC error.code value deluge-web
	// returns when the session cookie is missing.
	ErrorCodeAuthRequired = 1
	// ErrorCodeSessionExpired is the JSON-RPC error.code value deluge-web
	// returns when the session cookie was valid but has timed out.
	ErrorCodeSessionExpired = 2
)

// NewProxy binds the deps. doer is REQUIRED; log defaults to
// slog.Default() when nil. Mirrors qbittorrent's [NewProxy].
func NewProxy(doer HTTPDoer, log *slog.Logger) (*Proxy, error) {
	if doer == nil {
		return nil, errors.New("deluge: NewProxy: nil HTTPDoer")
	}
	if log == nil {
		log = slog.Default()
	}
	return &Proxy{doer: doer, log: log, cookies: map[string]string{}}, nil
}

// BuildBaseURL composes the deluge-web JSON-RPC endpoint URL
// (scheme + host + port + UrlBase + "/json", no trailing slash).
// Mirrors C# `BuildRequest` at `DelugeProxy.cs:235-244` which feeds
// `JsonRpcRequestBuilder(url).Resource("json")`.
//
// Examples:
//
//	useSsl=false host=localhost port=8112 urlBase=""        → "http://localhost:8112/json"
//	useSsl=true  host=deluge    port=8443 urlBase="/deluge" → "https://deluge:8443/deluge/json"
func (p *Proxy) BuildBaseURL(settings Settings) string {
	scheme := "http"
	if settings.UseSsl {
		scheme = "https"
	}
	base := fmt.Sprintf("%s://%s:%d", scheme, settings.Host, settings.Port)
	ub := strings.TrimSpace(settings.UrlBase)
	if ub != "" && ub != "/" {
		if !strings.HasPrefix(ub, "/") {
			ub = "/" + ub
		}
		base += strings.TrimRight(ub, "/")
	}
	return base + "/json"
}

// rpcRequest is the canonical request envelope. Mirrors C#
// `JsonRpcRequest` at `Common/Http/JsonRpc/JsonRpcRequest.cs`.
type rpcRequest struct {
	Method string `json:"method"`
	Params []any  `json:"params"`
	ID     int64  `json:"id"`
}

// rpcResponse is the canonical response envelope. Mirrors C#
// `JsonRpcResponse<TResult>` at
// `Common/Http/JsonRpc/JsonRpcResponse.cs`. Result and Error are
// raw JSON so per-method callers can deserialise into the typed
// shape they need.
type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error,omitempty"`
	ID     int64           `json:"id"`
}

// rpcError mirrors C# `DelugeError` at `DelugeError.cs`. Surface
// shape is `{"code": int, "message": string}`; the Go port carries
// both so the auth-retry branch can key on Code while the
// human-readable Message rides along in the wrapped error.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// EnsureAuth runs the auth.login flow when needed and returns the
// `_session_id` cookie value the caller should attach to subsequent
// requests. force=true bypasses the cache (used by the
// error-code-1/2 reauth branch in [Proxy.Call]).
//
// Mirrors C# `AuthenticateClient` at `DelugeProxy.cs:317-352`. The
// post-login `ConnectDaemon` call from C# `:347` is intentionally
// omitted at 5.G2b — the connect-flow lands at 5.G2c when Test()
// drives it (the C# tree's ConnectDaemon at `:354-380` reads
// web.get_hosts + picks the 127.0.0.1 entry, which is corner-case
// for the common single-daemon-on-localhost deploy).
func (p *Proxy) EnsureAuth(ctx context.Context, settings Settings, force bool) (string, error) {
	key := p.authKey(settings)

	if !force {
		p.mu.RLock()
		if sid, ok := p.cookies[key]; ok {
			p.mu.RUnlock()
			return sid, nil
		}
		p.mu.RUnlock()
	} else {
		p.mu.Lock()
		delete(p.cookies, key)
		p.mu.Unlock()
	}

	body, err := json.Marshal(rpcRequest{
		Method: "auth.login",
		Params: []any{settings.Password},
		ID:     p.nextID.Add(1),
	})
	if err != nil {
		return "", fmt.Errorf("deluge: marshal auth.login: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.BuildBaseURL(settings), bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("deluge: build auth.login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := p.doer.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return "", fmt.Errorf("deluge: read auth.login body: %w", readErr)
	}

	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return "", fmt.Errorf("%w: HTTP %d", ErrAuthFailed, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: auth.login status=%d body=%s",
			ErrUnexpected, resp.StatusCode, truncate(respBody, 256))
	}

	var parsed rpcResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", fmt.Errorf("deluge: parse auth.login response: %w", err)
	}
	if parsed.Error != nil {
		return "", fmt.Errorf("%w: auth.login error code=%d message=%q",
			ErrAuthFailed, parsed.Error.Code, parsed.Error.Message)
	}
	// Deluge convention: auth.login returns the literal boolean
	// `false` (not an error object) on bad password. Mirrors C#
	// `:332-336`.
	var ok bool
	if err := json.Unmarshal(parsed.Result, &ok); err != nil {
		return "", fmt.Errorf("deluge: parse auth.login result: %w", err)
	}
	if !ok {
		return "", fmt.Errorf("%w: bad password", ErrAuthFailed)
	}

	// Extract the _session_id cookie. Some reverse-proxy deployments
	// strip cookies; surface as auth failure rather than proceeding
	// without one (deluge-web rejects unauthenticated subsequent
	// calls with error-code 1).
	var sid string
	for _, c := range resp.Cookies() {
		if c.Name == "_session_id" {
			sid = c.Value
			break
		}
	}
	if sid == "" {
		return "", fmt.Errorf("%w: auth.login succeeded but no _session_id cookie returned", ErrAuthFailed)
	}

	p.mu.Lock()
	p.cookies[key] = sid
	p.mu.Unlock()
	return sid, nil
}

// Call dispatches one JSON-RPC method. Routes through [EnsureAuth]
// on the first attempt; on a response carrying error.code 1 or 2 —
// the auth-required / session-expired arm — runs EnsureAuth(force=true)
// and replays the call once.
//
// Returns the raw `result` JSON. Per-method callers unmarshal it
// into the typed shape they need.
//
// Mirrors C# `ProcessRequest` at `DelugeProxy.cs:201-232`.
func (p *Proxy) Call(ctx context.Context, method string, params []any, settings Settings) (json.RawMessage, error) {
	if _, err := p.EnsureAuth(ctx, settings, false); err != nil {
		return nil, err
	}
	result, rpcErr, err := p.fireRPC(ctx, method, params, settings)
	if err != nil {
		return nil, err
	}
	if rpcErr == nil {
		return result, nil
	}
	// Auth-required / session-expired → reauth + retry once.
	if rpcErr.Code == ErrorCodeAuthRequired || rpcErr.Code == ErrorCodeSessionExpired {
		if _, err := p.EnsureAuth(ctx, settings, true); err != nil {
			return nil, err
		}
		result, rpcErr, err = p.fireRPC(ctx, method, params, settings)
		if err != nil {
			return nil, err
		}
		if rpcErr == nil {
			return result, nil
		}
		if rpcErr.Code == ErrorCodeAuthRequired || rpcErr.Code == ErrorCodeSessionExpired {
			return nil, fmt.Errorf("%w: %s (code=%d)", ErrAuthFailed, rpcErr.Message, rpcErr.Code)
		}
	}
	return nil, fmt.Errorf("%w: %s (code=%d, method=%s)", ErrRPCFailure, rpcErr.Message, rpcErr.Code, method)
}

// fireRPC dispatches a single JSON-RPC request with the supplied
// cached cookie. The caller drives the auth flow.
func (p *Proxy) fireRPC(ctx context.Context, method string, params []any, settings Settings) (json.RawMessage, *rpcError, error) {
	if params == nil {
		params = []any{}
	}
	body, err := json.Marshal(rpcRequest{
		Method: method,
		Params: params,
		ID:     p.nextID.Add(1),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("deluge: marshal %s: %w", method, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.BuildBaseURL(settings), bytes.NewReader(body))
	if err != nil {
		return nil, nil, fmt.Errorf("deluge: build %s request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	// Attach the cached cookie. force=false EnsureAuth call from
	// Call() already populated the cache; the lookup here is to
	// re-read after reauth replay.
	p.mu.RLock()
	sid := p.cookies[p.authKey(settings)]
	p.mu.RUnlock()
	if sid != "" {
		req.AddCookie(&http.Cookie{Name: "_session_id", Value: sid})
	}

	resp, err := p.doer.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, nil, fmt.Errorf("deluge: read %s response: %w", method, readErr)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("%w: %s status=%d body=%s",
			ErrUnexpected, method, resp.StatusCode, truncate(respBody, 256))
	}

	var parsed rpcResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, nil, fmt.Errorf("deluge: parse %s response: %w", method, err)
	}
	return parsed.Result, parsed.Error, nil
}

// GetTorrents fires web.update_ui with the canonical
// [requiredProperties] field-list and the optional category-filter
// arm. Mirrors C# `DelugeProxy.GetTorrents` at `:68-94` (and the
// label-filter variant at `:84-92`).
//
// When settings.TvCategory is non-empty, the filter parameter
// carries `{"label": "<tvCategory>"}` so deluge-web returns only
// matching torrents server-side — matches the C# `GetTorrentsByLabel`
// path that Deluge.GetItems chooses at `Deluge.cs:118-121`.
//
// Returns the projected slice (empty, never nil) with each torrent's
// Hash field back-filled from the map key when missing (some deluge
// builds omit the in-value hash field).
func (p *Proxy) GetTorrents(ctx context.Context, settings Settings) ([]DelugeTorrent, error) {
	filter := map[string]any{}
	if cat := strings.TrimSpace(settings.TvCategory); cat != "" {
		filter["label"] = cat
	}
	raw, err := p.Call(ctx, "web.update_ui", []any{requiredProperties, filter}, settings)
	if err != nil {
		return nil, err
	}
	var envelope DelugeUpdateUIResult
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("deluge: parse web.update_ui response: %w", err)
	}
	out := make([]DelugeTorrent, 0, len(envelope.Torrents))
	for k, v := range envelope.Torrents {
		if v.Hash == "" {
			v.Hash = k
		}
		out = append(out, v)
	}
	return out, nil
}

// AddTorrentFromMagnet fires core.add_torrent_magnet. Mirrors C#
// `DelugeProxy.AddTorrentFromMagnet` at `:103-125`. Returns the
// added torrent's hash (the literal `result` string from deluge).
//
// Options object: add_paused / remove_at_ratio (always false to
// match C# `:108`) / download_location / move_completed +
// move_completed_path. Built via [buildAddOptions] so the add+file
// path shares the assembly.
func (p *Proxy) AddTorrentFromMagnet(ctx context.Context, magnetURL string, settings Settings) (string, error) {
	options := buildAddOptions(settings)
	raw, err := p.Call(ctx, "core.add_torrent_magnet", []any{magnetURL, options}, settings)
	if err != nil {
		return "", err
	}
	return parseAddResult(raw)
}

// AddTorrentFromFile fires core.add_torrent_file. Mirrors C#
// `DelugeProxy.AddTorrentFromFile` at `:127-150`. The filename is
// the wire-format display name only; deluge stores files under
// download_location. fileContent is base64-encoded — deluge's
// JSON-RPC accepts the .torrent bytes only as a base64 string.
func (p *Proxy) AddTorrentFromFile(ctx context.Context, filename string, fileContent []byte, settings Settings) (string, error) {
	options := buildAddOptions(settings)
	encoded := base64.StdEncoding.EncodeToString(fileContent)
	raw, err := p.Call(ctx, "core.add_torrent_file", []any{filename, encoded, options}, settings)
	if err != nil {
		return "", err
	}
	return parseAddResult(raw)
}

// parseAddResult unmarshals the add-torrent result. Deluge returns
// the torrent's hash as a JSON string on success, or JSON null on
// failure (the C# tree at `Deluge.cs:62-65,89-92` treats empty as
// failure). The Go port surfaces failure as [ErrAddFailed].
func parseAddResult(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", ErrAddFailed
	}
	var hash string
	if err := json.Unmarshal(raw, &hash); err != nil {
		return "", fmt.Errorf("deluge: parse add-torrent result: %w", err)
	}
	if strings.TrimSpace(hash) == "" {
		return "", ErrAddFailed
	}
	return hash, nil
}

// SetTorrentLabel fires label.set_torrent. Mirrors C#
// `DelugeProxy.SetTorrentLabel` at `:189-192`. Used by [Deluge.Download]
// to apply TvCategory + by [Deluge.MarkItemAsImported] to swap to
// TvImportedCategory.
func (p *Proxy) SetTorrentLabel(ctx context.Context, hash, label string, settings Settings) error {
	_, err := p.Call(ctx, "label.set_torrent", []any{strings.ToLower(hash), label}, settings)
	return err
}

// SetTorrentSeedingConfiguration fires core.set_torrent_options with
// stop_ratio + stop_at_ratio fields. Mirrors C#
// `DelugeProxy.SetTorrentSeedingConfiguration` at `:170-186`. nil
// seedConfig → no-op.
func (p *Proxy) SetTorrentSeedingConfiguration(ctx context.Context, hash string, seedConfig *downloadclients.TorrentSeedConfiguration, settings Settings) error {
	if seedConfig == nil {
		return nil
	}
	args := map[string]any{}
	if seedConfig.Ratio != nil {
		args["stop_ratio"] = *seedConfig.Ratio
		args["stop_at_ratio"] = 1
	}
	if len(args) == 0 {
		// No actionable limits — skip the RPC (deluge happily
		// accepts an empty arguments map but it's a wasted round
		// trip).
		return nil
	}
	_, err := p.Call(ctx, "core.set_torrent_options",
		[]any{[]string{strings.ToLower(hash)}, args}, settings)
	return err
}

// GetConfig fires core.get_config. Mirrors C#
// `DelugeProxy.GetConfig` at `:66-72`. Returns the raw preferences
// map — deluge's preferences shape is large and varies across
// versions, so the consumer reads specific keys ("download_location",
// "move_completed", "move_completed_path") rather than unmarshalling
// into a typed struct.
func (p *Proxy) GetConfig(ctx context.Context, settings Settings) (map[string]any, error) {
	raw, err := p.Call(ctx, "core.get_config", nil, settings)
	if err != nil {
		return nil, err
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("deluge: parse core.get_config response: %w", err)
	}
	return cfg, nil
}

// GetLabelOptions fires label.get_options for the named label.
// Mirrors C# `DelugeProxy.GetLabelOptions` at `:159-164`.
//
// Returns (nil, nil) when the label doesn't exist — deluge's
// label.get_options on a missing label tends to surface as either
// an empty result object OR an RPC error; both arms collapse to
// nil so the GetStatus 4-way precedence falls through to the
// next arm cleanly.
func (p *Proxy) GetLabelOptions(ctx context.Context, label string, settings Settings) (*DelugeLabel, error) {
	raw, err := p.Call(ctx, "label.get_options", []any{label}, settings)
	if err != nil {
		// Label plugin missing OR label not found both surface as
		// RPC errors at this layer. The C# tree throws via
		// DelugeException at `:215-228` which the consumer caller
		// in `GetStatus` doesn't catch — so the Go-port mirror would
		// propagate the error too. We propagate via the (nil, err)
		// return; the GetStatus arm handles the err by treating it
		// as "no label override" and falling through.
		return nil, err
	}
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	var opts DelugeLabel
	if err := json.Unmarshal(raw, &opts); err != nil {
		return nil, fmt.Errorf("deluge: parse label.get_options response: %w", err)
	}
	opts.Name = label
	return &opts, nil
}

// GetMethods fires system.listMethods. Mirrors C#
// `DelugeProxy.GetMethods` at `:96-101`. Used by the label-plugin
// probe in 5.G2h's MarkItemAsImported when AddLabel is needed
// (the label plugin must be loaded for label.* RPCs to work).
func (p *Proxy) GetMethods(ctx context.Context, settings Settings) ([]string, error) {
	raw, err := p.Call(ctx, "system.listMethods", nil, settings)
	if err != nil {
		return nil, err
	}
	var methods []string
	if err := json.Unmarshal(raw, &methods); err != nil {
		return nil, fmt.Errorf("deluge: parse system.listMethods response: %w", err)
	}
	return methods, nil
}

// RemoveTorrent fires core.remove_torrent. Mirrors C#
// `DelugeProxy.RemoveTorrent` at `:152-156`. The `removeData` flag
// tells deluge whether to also delete the on-disk payload.
//
// Deluge returns a boolean `true` on success; the Go port ignores
// the result body since the orchestrator only cares about
// success/failure (any RPC error bubbles up).
func (p *Proxy) RemoveTorrent(ctx context.Context, hash string, removeData bool, settings Settings) error {
	_, err := p.Call(ctx, "core.remove_torrent",
		[]any{strings.ToLower(hash), removeData}, settings)
	return err
}

// MoveTorrentToTopInQueue fires core.queue_top. Mirrors C#
// `DelugeProxy.MoveTorrentToTopInQueue` at `:152-155`. The hash is
// wrapped in a single-element array per deluge's API shape.
func (p *Proxy) MoveTorrentToTopInQueue(ctx context.Context, hash string, settings Settings) error {
	_, err := p.Call(ctx, "core.queue_top",
		[]any{[]string{strings.ToLower(hash)}}, settings)
	return err
}

// AddLabel fires label.add. Mirrors C# `DelugeProxy.AddLabel` at
// `:184-187`. Used by [Deluge.Test] (slice 5.J101) to pre-create the
// TvCategory label when it's missing.
func (p *Proxy) AddLabel(ctx context.Context, label string, settings Settings) error {
	_, err := p.Call(ctx, "label.add", []any{label}, settings)
	return err
}

// GetAvailableLabels fires label.get_labels. Mirrors C#
// `DelugeProxy.GetAvailableLabels` at `:158-163`. Returns the
// labels currently registered with the Deluge `label` plugin
// (empty slice when the plugin reports none). Used by the J101
// TestCategory probe to confirm TvCategory + TvImportedCategory
// exist on the server (with auto-create fallback via [AddLabel]).
func (p *Proxy) GetAvailableLabels(ctx context.Context, settings Settings) ([]string, error) {
	raw, err := p.Call(ctx, "label.get_labels", nil, settings)
	if err != nil {
		return nil, err
	}
	var labels []string
	if err := json.Unmarshal(raw, &labels); err != nil {
		return nil, fmt.Errorf("deluge: parse label.get_labels response: %w", err)
	}
	return labels, nil
}

// buildAddOptions assembles the per-add options dict. Mirrors C#
// `DelugeProxy.AddTorrentFromMagnet/AddTorrentFromFile` arms at
// `:107-122,131-146` exactly.
//
// remove_at_ratio is unconditionally false — the C# tree forces it
// off so Sonarr (not deluge) decides when to delete the torrent.
// move_completed is set true alongside move_completed_path so
// deluge applies the post-completion move per the C# `:115-119`
// arm.
func buildAddOptions(settings Settings) map[string]any {
	opts := map[string]any{
		"add_paused":      settings.AddPaused,
		"remove_at_ratio": false,
	}
	if dl := strings.TrimSpace(settings.DownloadDirectory); dl != "" {
		opts["download_location"] = dl
	}
	if mc := strings.TrimSpace(settings.CompletedDirectory); mc != "" {
		opts["move_completed_path"] = mc
		opts["move_completed"] = true
	}
	return opts
}

// authKey is the cache key (baseURL, password). Mirrors C#
// `DelugeProxy.cs:325` exactly.
func (p *Proxy) authKey(settings Settings) string {
	return p.BuildBaseURL(settings) + ":" + settings.Password
}

// truncate clamps a byte slice to n bytes for log lines.
func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
