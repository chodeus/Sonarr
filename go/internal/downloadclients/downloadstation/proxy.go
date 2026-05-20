package downloadstation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// HTTPDoer is the [*http.Client] slice the [Proxy] depends on.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// sessionTTL is the cache TTL for the SID returned by SYNO.API.Auth
// login. Mirrors C# `DiskStationProxyBase` at `:177,181,193`'s
// `TimeSpan.FromHours(6)`.
const sessionTTL = 6 * time.Hour

// apiInfoTTL mirrors C# `:230`'s `TimeSpan.FromHours(1)` cache TTL
// for the SYNO.API.Info query results.
const apiInfoTTL = time.Hour

// API names ported from the C# enum at
// `DiskStationApi.cs:3-12` paired with the per-API SYNO.API.<X>
// string the C# `UpdateApiInfo` query uses.
const (
	apiInfoName            = "SYNO.API.Info"
	apiAuthName            = "SYNO.API.Auth"
	apiDownloadStationName = "SYNO.DownloadStation.Task"
)

// APIInfo ports C# `DiskStationApiInfo` at `:5-32`. The Path setter
// in C# trims leading `/` and `\\`; the Go port does the same at
// Unmarshal time via the [APIInfo.UnmarshalJSON] hook.
type APIInfo struct {
	Name       string `json:"-"`
	Path       string `json:"path"`
	MaxVersion int    `json:"maxVersion"`
	MinVersion int    `json:"minVersion"`

	// NeedsAuthentication is set by the proxy after API discovery;
	// the SYNO.API.Info wire payload doesn't carry this bit. Auth
	// itself doesn't require a SID (chicken-and-egg); every other
	// API does.
	NeedsAuthentication bool `json:"-"`
}

// UnmarshalJSON normalises the Path field to drop any leading
// separator, matching the C# `Path` setter at
// `DiskStationApiInfo.cs:20-29`.
func (a *APIInfo) UnmarshalJSON(data []byte) error {
	type raw struct {
		Path       string `json:"path"`
		MaxVersion int    `json:"maxVersion"`
		MinVersion int    `json:"minVersion"`
	}
	var r raw
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	a.Path = strings.TrimLeft(r.Path, "/\\")
	a.MaxVersion = r.MaxVersion
	a.MinVersion = r.MinVersion
	return nil
}

// infoAPI is the bootstrap API info used to discover all other
// endpoints. Mirrors C# `_apiInfo` static at
// `DiskStationProxyBase.cs:28-41`.
var infoAPI = APIInfo{
	Name:                apiInfoName,
	Path:                "query.cgi",
	MaxVersion:          1,
	MinVersion:          1,
	NeedsAuthentication: false,
}

// Response wraps the standard `{success, data, error}` envelope
// every Synology API call returns. Mirrors C# `DiskStationResponse<T>`
// at `Responses/DiskStationResponse.cs`. Result is `json.RawMessage`
// so per-call sites unmarshal into their typed shape.
type Response struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   *APIError       `json:"error,omitempty"`
}

// APIError ports C# `DiskStationError` at `Responses/DiskStationError.cs`.
// The C# message-table lookup is preserved as [APIError.Message].
type APIError struct {
	Code int `json:"code"`
}

// IsSession reports whether the error code maps to a stale-session
// condition (matches C# `SessionError` at `:80`).
func (e *APIError) IsSession() bool {
	return e.Code == 105 || e.Code == 106 || e.Code == 107 || e.Code == 119
}

// Message resolves the human-readable error string. The lookup
// order matches C# `GetMessage(api)` at `:84-105`: api-specific
// table first, falling back to the common table. apiName is the
// SYNO.API.<X> string for the call that produced the error;
// unknown codes return a "Code N - Unknown error" sentinel.
func (e *APIError) Message(apiName string) string {
	if e == nil {
		return ""
	}
	if apiName == apiAuthName {
		if msg, ok := authMessages[e.Code]; ok {
			return msg
		}
	}
	if apiName == apiDownloadStationName {
		if msg, ok := taskMessages[e.Code]; ok {
			return msg
		}
	}
	if msg, ok := commonMessages[e.Code]; ok {
		return msg
	}
	return fmt.Sprintf("%d - Unknown error", e.Code)
}

// Common error messages ported from C# `DiskStationError.cs:14-23`.
var commonMessages = map[int]string{
	100: "Unknown error",
	101: "Invalid parameter",
	102: "The requested API does not exist",
	103: "The requested method does not exist",
	104: "The requested version does not support the functionality",
	105: "The logged in session does not have permission",
	106: "Session timeout",
	107: "Session interrupted by duplicate login",
	119: "SID not found",
}

// Auth-specific error messages ported from C# `:25-37`.
var authMessages = map[int]string{
	400: "No such account or incorrect password",
	401: "Disabled account",
	402: "Denied permission",
	403: "2-step authentication code required",
	404: "Failed to authenticate 2-step authentication code",
	406: "Enforce to authenticate with 2-factor authentication code",
	407: "Blocked IP source",
	408: "Expired password cannot change",
	409: "Expired password",
	410: "Password must be changed",
}

// DownloadStation.Task-specific messages ported from C# `:39-49`.
var taskMessages = map[int]string{
	400: "File upload failed",
	401: "Max number of tasks reached",
	402: "Destination denied",
	403: "Destination does not exist",
	404: "Invalid task id",
	405: "Invalid task action",
	406: "No default destination",
	407: "Set destination failed",
	408: "File does not exist",
}

// Proxy is the HTTP client for a Synology DiskStation. Mirrors C#
// `DiskStationProxyBase` at `Proxies/DiskStationProxyBase.cs:18-271`.
// Goroutine-safe: the in-memory API-info + session caches are
// guarded by a mutex.
type Proxy struct {
	http  HTTPDoer
	log   *slog.Logger
	clock func() time.Time

	mu       sync.Mutex
	apis     map[string]apiInfoCacheEntry // (host:port → apiName) → APIInfo
	sessions map[string]sessionCacheEntry // (user@host:port) → SID
}

type apiInfoCacheEntry struct {
	info    APIInfo
	expires time.Time
}

type sessionCacheEntry struct {
	sid     string
	expires time.Time
}

// NewProxy constructs a Proxy. http=nil binds [http.DefaultClient];
// log=nil binds [slog.Default]; clock=nil binds [time.Now].
func NewProxy(httpDoer HTTPDoer, log *slog.Logger, clock func() time.Time) *Proxy {
	if httpDoer == nil {
		httpDoer = http.DefaultClient
	}
	if log == nil {
		log = slog.Default()
	}
	if clock == nil {
		clock = time.Now
	}
	return &Proxy{
		http:     httpDoer,
		log:      log,
		clock:    clock,
		apis:     map[string]apiInfoCacheEntry{},
		sessions: map[string]sessionCacheEntry{},
	}
}

// baseURL composes the DSM web-API base. Mirrors C#
// `new HttpRequestBuilder(useSsl, host, port).Resource("webapi/...")`
// at `DiskStationProxyBase.cs:159`.
func baseURL(s Settings) string {
	scheme := "http"
	if s.UseSsl {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s:%d/webapi", scheme, s.Host, s.Port)
}

// sessionKey identifies the per-(user,host,port) session cache slot.
// Mirrors C# `GenerateSessionCacheKey` at `:57-60`.
func sessionKey(s Settings) string {
	return fmt.Sprintf("%s@%s:%d", s.Username, s.Host, s.Port)
}

// apiKey identifies the per-(host,port,apiName) API-info cache slot.
// Mirrors C# `GenerateInfoCacheKey` at `:204-207`.
func apiKey(s Settings, apiName string) string {
	return fmt.Sprintf("%s:%d->%s", s.Host, s.Port, apiName)
}

// GetAPIInfo discovers the per-API path + version range via the
// SYNO.API.Info query endpoint. Mirrors C# `UpdateApiInfo` at
// `DiskStationProxyBase.cs:209-233`. Returns the typed [APIInfo]
// the caller composes per-call URLs from. apiName="SYNO.API.Info"
// short-circuits to the bootstrap [infoAPI].
//
// Discovered entries cache for [apiInfoTTL] (1h, matches C#).
func (p *Proxy) GetAPIInfo(ctx context.Context, s Settings, apiName string) (*APIInfo, error) {
	if apiName == apiInfoName {
		info := infoAPI
		return &info, nil
	}
	p.mu.Lock()
	if entry, ok := p.apis[apiKey(s, apiName)]; ok && p.clock().Before(entry.expires) {
		info := entry.info
		p.mu.Unlock()
		return &info, nil
	}
	p.mu.Unlock()
	if err := p.refreshAPIInfo(ctx, s, apiName); err != nil {
		return nil, err
	}
	p.mu.Lock()
	entry, ok := p.apis[apiKey(s, apiName)]
	p.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("%w: API %q not advertised by DSM", ErrUnavailable, apiName)
	}
	info := entry.info
	return &info, nil
}

// refreshAPIInfo fires the SYNO.API.Info query for the given
// apiName + SYNO.API.Auth (the auth endpoint discovery is folded
// in so the login flow doesn't need a separate round-trip).
// Mirrors C# `UpdateApiInfo` at `:209-233`.
func (p *Proxy) refreshAPIInfo(ctx context.Context, s Settings, apiName string) error {
	q := url.Values{}
	q.Set("api", infoAPI.Name)
	q.Set("version", fmt.Sprintf("%d", infoAPI.MinVersion))
	q.Set("method", "query")
	q.Set("query", apiAuthName+","+apiName)

	full := fmt.Sprintf("%s/%s?%s", baseURL(s), infoAPI.Path, q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		return fmt.Errorf("downloadstation: build SYNO.API.Info request: %w", err)
	}
	body, err := p.execute(req, "SYNO.API.Info", infoAPI.Name)
	if err != nil {
		return err
	}
	var discovered map[string]APIInfo
	if err := json.Unmarshal(body, &discovered); err != nil {
		return fmt.Errorf("downloadstation: unmarshal SYNO.API.Info: %w", err)
	}
	expiry := p.clock().Add(apiInfoTTL)
	p.mu.Lock()
	for name, info := range discovered {
		info.Name = name
		info.NeedsAuthentication = name != apiAuthName
		p.apis[apiKey(s, name)] = apiInfoCacheEntry{info: info, expires: expiry}
	}
	p.mu.Unlock()
	return nil
}

// Authenticate runs the SYNO.API.Auth `login` flow and caches the
// returned SID. Mirrors C# `AuthenticateClient` at
// `DiskStationProxyBase.cs:131-144`. force=true bypasses the cache.
//
// Account/password are sent as query parameters (matches the C#
// `AddQueryParam` calls at `:136-139`); the wire payload also
// includes `format=sid` + `session=DownloadStation`.
func (p *Proxy) Authenticate(ctx context.Context, s Settings, force bool) (string, error) {
	if !force {
		p.mu.Lock()
		if entry, ok := p.sessions[sessionKey(s)]; ok && p.clock().Before(entry.expires) {
			sid := entry.sid
			p.mu.Unlock()
			return sid, nil
		}
		p.mu.Unlock()
	} else {
		p.mu.Lock()
		delete(p.sessions, sessionKey(s))
		p.mu.Unlock()
	}

	authInfo, err := p.GetAPIInfo(ctx, s, apiAuthName)
	if err != nil {
		return "", fmt.Errorf("downloadstation: discover SYNO.API.Auth: %w", err)
	}
	version := 2
	if authInfo.MaxVersion >= 7 {
		version = 6 // matches C# `:135` — newer DSM uses version 6
	}
	q := url.Values{}
	q.Set("api", apiAuthName)
	q.Set("version", fmt.Sprintf("%d", version))
	q.Set("method", "login")
	q.Set("account", s.Username)
	q.Set("passwd", s.Password)
	q.Set("format", "sid")
	q.Set("session", "DownloadStation")
	full := fmt.Sprintf("%s/%s?%s", baseURL(s), authInfo.Path, q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		return "", fmt.Errorf("downloadstation: build login request: %w", err)
	}
	body, err := p.execute(req, "login", apiAuthName)
	if err != nil {
		return "", err
	}
	var data struct {
		SID string `json:"sid"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return "", fmt.Errorf("downloadstation: unmarshal login: %w", err)
	}
	if strings.TrimSpace(data.SID) == "" {
		return "", fmt.Errorf("%w: login returned empty SID", ErrAuth)
	}
	p.mu.Lock()
	p.sessions[sessionKey(s)] = sessionCacheEntry{sid: data.SID, expires: p.clock().Add(sessionTTL)}
	p.mu.Unlock()
	return data.SID, nil
}

// execute fires one request + decodes the [Response] envelope.
// Returns the raw `data` JSON on success; an [ErrAuth]-wrapped or
// [ErrRPC]-wrapped error otherwise. Stale-session error codes (105
// + 106/107/119) invalidate the cached SID before returning.
//
// apiName is used for the error-message-table lookup.
func (p *Proxy) execute(req *http.Request, operation, apiName string) ([]byte, error) {
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrUnavailable, operation, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("downloadstation: %s read body: %w", operation, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: %s status %d", ErrUnavailable, operation, resp.StatusCode)
	}
	var env Response
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("downloadstation: %s unmarshal envelope: %w", operation, err)
	}
	if !env.Success {
		msg := ""
		if env.Error != nil {
			msg = env.Error.Message(apiName)
		}
		// SYNO.API.Auth code 400 (no such account / wrong password)
		// → ErrAuth; session codes (105/106/107/119) also → ErrAuth
		// since they signal the operator must re-auth.
		if env.Error != nil && (apiName == apiAuthName && env.Error.Code == 400) {
			return nil, fmt.Errorf("%w: %s: %s", ErrAuth, operation, msg)
		}
		if env.Error != nil && env.Error.IsSession() {
			return nil, fmt.Errorf("%w: %s: %s", ErrAuth, operation, msg)
		}
		return nil, fmt.Errorf("%w: %s: %s", ErrRPC, operation, msg)
	}
	return env.Data, nil
}

// ErrUnavailable wraps every transport-level failure (network
// errors + non-2xx). Mirrors C#
// `DownloadClientUnavailableException` raised at `:91,94`.
var ErrUnavailable = errors.New("downloadstation: unavailable")

// ErrAuth wraps SYNO.API.Auth code 400 (bad credentials) + the
// session-error code family (105/106/107/119). Mirrors C#
// `DownloadClientAuthenticationException` at `:118`.
var ErrAuth = errors.New("downloadstation: authentication failed")

// ErrRPC wraps every non-success Synology response that doesn't
// fall into the auth/session families. Mirrors the generic
// C# `DownloadClientException` arm at `:122`.
var ErrRPC = errors.New("downloadstation: rpc error")
