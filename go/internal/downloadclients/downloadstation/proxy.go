package downloadstation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/textproto"
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
// string the C# `UpdateApiInfo` query uses. The V2 task API
// (`SYNO.DownloadStation2.Task`) is the DSM 7+ surface; the
// V1/V2 selector in [Proxy.taskAPIVersion] prefers V2 when
// advertised, falls back to V1 otherwise.
const (
	apiInfoName             = "SYNO.API.Info"
	apiAuthName             = "SYNO.API.Auth"
	apiDownloadStationName  = "SYNO.DownloadStation.Task"
	apiDownloadStation2Name = "SYNO.DownloadStation2.Task"
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
// apiName + SYNO.API.Auth + the V2 task API (so the V1/V2 selector
// always has both task-API entries cached after the first
// discovery round-trip). Mirrors C# `UpdateApiInfo` at `:209-233`
// with the Go-port extension to fold the V2 task API into the
// initial query.
func (p *Proxy) refreshAPIInfo(ctx context.Context, s Settings, apiName string) error {
	q := url.Values{}
	q.Set("api", infoAPI.Name)
	q.Set("version", fmt.Sprintf("%d", infoAPI.MinVersion))
	q.Set("method", "query")
	// Ask for the V1 + V2 task API entries every time so the V1/V2
	// selector at [Proxy.taskAPIVersion] can dispatch without an
	// extra round-trip.
	queries := []string{apiAuthName}
	if apiName != apiAuthName {
		queries = append(queries, apiName)
	}
	if apiName != apiDownloadStation2Name {
		queries = append(queries, apiDownloadStation2Name)
	}
	if apiName != apiDownloadStationName {
		queries = append(queries, apiDownloadStationName)
	}
	q.Set("query", strings.Join(queries, ","))

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

// TaskStatus ports C# `DownloadStationTaskStatus` at
// `DownloadStationTask.cs:46-60`. The Synology API wire shape sends
// the status as a snake-case string (e.g. `"hash_checking"`); the
// Go-port `Task.Status` field is the typed enum exposed via a
// custom UnmarshalJSON.
type TaskStatus int

// TaskStatus values mirror the C# enum order.
const (
	TaskStatusUnknown            TaskStatus = 0
	TaskStatusWaiting            TaskStatus = 1
	TaskStatusDownloading        TaskStatus = 2
	TaskStatusPaused             TaskStatus = 3
	TaskStatusFinishing          TaskStatus = 4
	TaskStatusFinished           TaskStatus = 5
	TaskStatusHashChecking       TaskStatus = 6
	TaskStatusSeeding            TaskStatus = 7
	TaskStatusFilehostingWaiting TaskStatus = 8
	TaskStatusExtracting         TaskStatus = 9
	TaskStatusError              TaskStatus = 10
	TaskStatusCaptchaNeeded      TaskStatus = 11
)

// taskStatusByName is the snake_case wire-name lookup table. The
// C# code uses `UnderscoreStringEnumConverter` at
// `DownloadStationTask.cs:25` to map snake-case to PascalCase enum
// names; the Go port mirrors with an explicit table.
var taskStatusByName = map[string]TaskStatus{
	"unknown":             TaskStatusUnknown,
	"waiting":             TaskStatusWaiting,
	"downloading":         TaskStatusDownloading,
	"paused":              TaskStatusPaused,
	"finishing":           TaskStatusFinishing,
	"finished":            TaskStatusFinished,
	"hash_checking":       TaskStatusHashChecking,
	"seeding":             TaskStatusSeeding,
	"filehosting_waiting": TaskStatusFilehostingWaiting,
	"extracting":          TaskStatusExtracting,
	"error":               TaskStatusError,
	"captcha_needed":      TaskStatusCaptchaNeeded,
}

// UnmarshalJSON decodes the snake-case status string into the
// typed enum. Unknown strings map to TaskStatusUnknown (matches
// C# `UnderscoreStringEnumConverter` with the default fallback at
// `DownloadStationTask.cs:25`).
func (s *TaskStatus) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		// Fall back to numeric if the wire shape is unexpectedly an int.
		var n int
		if err2 := json.Unmarshal(data, &n); err2 == nil {
			*s = TaskStatus(n)
			return nil
		}
		return err
	}
	if v, ok := taskStatusByName[strings.ToLower(raw)]; ok {
		*s = v
		return nil
	}
	*s = TaskStatusUnknown
	return nil
}

// TaskAdditional ports C# `DownloadStationTaskAdditional` at
// `DownloadStationTaskAdditional.cs:6-14`. Detail + Transfer are
// `map[string]string` slots — Synology emits everything as strings.
type TaskAdditional struct {
	Detail   map[string]string `json:"detail"`
	Transfer map[string]string `json:"transfer"`
}

// Task ports C# `DownloadStationTask` at `DownloadStationTask.cs:7-34`.
// Only the fields Sonarr reads are surfaced.
type Task struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Size        int64             `json:"size"`
	Type        string            `json:"type"`
	Username    string            `json:"username"`
	Status      TaskStatus        `json:"status"`
	StatusExtra map[string]string `json:"status_extra"`
	Additional  TaskAdditional    `json:"additional"`
}

// AddTaskFromData adds a task from raw .torrent/.nzb bytes via a
// multipart POST. Dispatches V1 or V2.
func (p *Proxy) AddTaskFromData(ctx context.Context, s Settings, contents []byte, filename, destination string) error {
	v, err := p.taskAPIVersion(ctx, s)
	if err != nil {
		return err
	}
	if v == 2 {
		return p.addTaskFromDataV2(ctx, s, contents, filename, destination)
	}
	return p.addTaskFromDataV1(ctx, s, contents, filename, destination)
}

// addTaskFromDataV1 fires `SYNO.DownloadStation.Task` create
// method v2 with a multipart POST. Mirrors C#
// `DownloadStationTaskProxyV1.AddTaskFromData` at `:24-36`.
//
// Body shape (multipart form-data, all parameters as form fields):
//
//	api=SYNO.DownloadStation.Task
//	version=2
//	method=create
//	_sid=<SID>
//	destination=<path>       (only when non-empty)
//	file=@<filename> (file part with body bytes)
func (p *Proxy) addTaskFromDataV1(ctx context.Context, s Settings, contents []byte, filename, destination string) error {
	api, err := p.GetAPIInfo(ctx, s, apiDownloadStationName)
	if err != nil {
		return fmt.Errorf("downloadstation: discover %s: %w", apiDownloadStationName, err)
	}
	version := 2
	if version < api.MinVersion || version > api.MaxVersion {
		return fmt.Errorf("%w: %s version %d not supported (server: %d..%d)",
			ErrUnavailable, apiDownloadStationName, version, api.MinVersion, api.MaxVersion)
	}
	sid, err := p.Authenticate(ctx, s, false)
	if err != nil {
		return err
	}

	body, contentType, err := buildV1AddMultipart(api.Name, version, "create", sid, destination, filename, contents)
	if err != nil {
		return err
	}
	full := fmt.Sprintf("%s/%s", baseURL(s), api.Path)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, full, body)
	if err != nil {
		return fmt.Errorf("downloadstation: build %s create POST: %w", apiDownloadStationName, err)
	}
	req.Header.Set("Content-Type", contentType)
	_, err = p.execute(req, fmt.Sprintf("%s create", apiDownloadStationName), apiDownloadStationName)
	return err
}

// buildV1AddMultipart composes the V1 add-multipart body —
// extracted so the V1 implementation stays readable + so V2 can
// share the same writer scaffold without sharing the field-shape
// (V2 uses JSON-quoted values; see [buildV2AddMultipart]).
func buildV1AddMultipart(apiName string, version int, method, sid, destination, filename string, contents []byte) (*bytes.Buffer, string, error) {
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	addField := func(name, value string) error {
		hdr := textproto.MIMEHeader{}
		hdr.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q`, name))
		part, err := mw.CreatePart(hdr)
		if err != nil {
			return err
		}
		_, err = part.Write([]byte(value))
		return err
	}
	if err := addField("api", apiName); err != nil {
		return nil, "", fmt.Errorf("downloadstation: multipart api field: %w", err)
	}
	if err := addField("version", fmt.Sprintf("%d", version)); err != nil {
		return nil, "", fmt.Errorf("downloadstation: multipart version field: %w", err)
	}
	if err := addField("method", method); err != nil {
		return nil, "", fmt.Errorf("downloadstation: multipart method field: %w", err)
	}
	if err := addField("_sid", sid); err != nil {
		return nil, "", fmt.Errorf("downloadstation: multipart _sid field: %w", err)
	}
	if strings.TrimSpace(destination) != "" {
		if err := addField("destination", destination); err != nil {
			return nil, "", fmt.Errorf("downloadstation: multipart destination field: %w", err)
		}
	}
	fileHdr := textproto.MIMEHeader{}
	fileHdr.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, "file", filename))
	fileHdr.Set("Content-Type", "application/octet-stream")
	part, err := mw.CreatePart(fileHdr)
	if err != nil {
		return nil, "", fmt.Errorf("downloadstation: multipart file part: %w", err)
	}
	if _, err := part.Write(contents); err != nil {
		return nil, "", fmt.Errorf("downloadstation: write multipart file bytes: %w", err)
	}
	if err := mw.Close(); err != nil {
		return nil, "", fmt.Errorf("downloadstation: close multipart writer: %w", err)
	}
	return body, mw.FormDataContentType(), nil
}

// addTaskFromDataV2 fires `SYNO.DownloadStation2.Task` create
// method v2 with the DSM-7-specific multipart shape. Mirrors C#
// `DownloadStationTaskProxyV2.AddTaskFromData` at `:25-41`.
//
// V2-specific oddities (verbatim from the C# wire):
//
//   - `type` form-parameter value is the JSON-quoted string `"file"`
//     — literal double-quotes around the word file.
//   - `file` form-parameter value is the JSON array `["fileData"]`.
//   - `create_list` form-parameter value is the unquoted string
//     `false`.
//   - `destination` form-parameter value is the JSON-quoted
//     directory `"<dir>"`.
//   - The actual file bytes go as a form-file part with field name
//     `fileData` (matching the `file` array element).
//
// The Synology DSM 7 surface serialises params as JSON-quoted
// values even within a multipart form — preserved verbatim.
func (p *Proxy) addTaskFromDataV2(ctx context.Context, s Settings, contents []byte, filename, destination string) error {
	api, err := p.GetAPIInfo(ctx, s, apiDownloadStation2Name)
	if err != nil {
		return fmt.Errorf("downloadstation: discover %s: %w", apiDownloadStation2Name, err)
	}
	version := 2
	if version < api.MinVersion || version > api.MaxVersion {
		return fmt.Errorf("%w: %s version %d not supported (server: %d..%d)",
			ErrUnavailable, apiDownloadStation2Name, version, api.MinVersion, api.MaxVersion)
	}
	sid, err := p.Authenticate(ctx, s, false)
	if err != nil {
		return err
	}

	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	addField := func(name, value string) error {
		hdr := textproto.MIMEHeader{}
		hdr.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q`, name))
		part, err := mw.CreatePart(hdr)
		if err != nil {
			return err
		}
		_, err = part.Write([]byte(value))
		return err
	}
	for _, kv := range []struct{ k, v string }{
		{"api", api.Name},
		{"version", fmt.Sprintf("%d", version)},
		{"method", "create"},
		{"_sid", sid},
		// V2 JSON-quoted oddities:
		{"type", `"file"`},
		{"file", `["fileData"]`},
		{"create_list", "false"},
	} {
		if err := addField(kv.k, kv.v); err != nil {
			return fmt.Errorf("downloadstation: v2 multipart %s field: %w", kv.k, err)
		}
	}
	if strings.TrimSpace(destination) != "" {
		if err := addField("destination", fmt.Sprintf(`%q`, destination)); err != nil {
			return fmt.Errorf("downloadstation: v2 multipart destination field: %w", err)
		}
	}
	fileHdr := textproto.MIMEHeader{}
	fileHdr.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, "fileData", filename))
	fileHdr.Set("Content-Type", "application/octet-stream")
	part, err := mw.CreatePart(fileHdr)
	if err != nil {
		return fmt.Errorf("downloadstation: v2 multipart fileData part: %w", err)
	}
	if _, err := part.Write(contents); err != nil {
		return fmt.Errorf("downloadstation: v2 write multipart fileData bytes: %w", err)
	}
	if err := mw.Close(); err != nil {
		return fmt.Errorf("downloadstation: v2 close multipart writer: %w", err)
	}

	full := fmt.Sprintf("%s/%s", baseURL(s), api.Path)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, full, body)
	if err != nil {
		return fmt.Errorf("downloadstation: build %s create POST: %w", apiDownloadStation2Name, err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	_, err = p.execute(req, fmt.Sprintf("%s create", apiDownloadStation2Name), apiDownloadStation2Name)
	return err
}

// AddTaskFromURL adds a task by URL/magnet. Dispatches to the V1
// or V2 implementation based on the DSM-advertised task API.
// Mirrors the C# `IDownloadStationTaskProxy.AddTaskFromUrl`
// interface contract at `Proxies/DownloadStationTaskProxySelector.cs:13`.
func (p *Proxy) AddTaskFromURL(ctx context.Context, s Settings, taskURL, destination string) error {
	v, err := p.taskAPIVersion(ctx, s)
	if err != nil {
		return err
	}
	if v == 2 {
		return p.addTaskFromURLV2(ctx, s, taskURL, destination)
	}
	return p.addTaskFromURLV1(ctx, s, taskURL, destination)
}

// addTaskFromURLV1 fires `SYNO.DownloadStation.Task` create method
// (version 3) with the URL as a query param. Mirrors C#
// `DownloadStationTaskProxyV1.AddTaskFromUrl` at `:38-49`.
func (p *Proxy) addTaskFromURLV1(ctx context.Context, s Settings, taskURL, destination string) error {
	q := url.Values{}
	q.Set("uri", taskURL)
	if strings.TrimSpace(destination) != "" {
		q.Set("destination", destination)
	}
	_, err := p.callTaskAPI(ctx, s, apiDownloadStationName, "create", 3, q)
	return err
}

// addTaskFromURLV2 fires `SYNO.DownloadStation2.Task` create
// method (version 2) with `type=url&url=<url>&create_list=false`
// + optional `destination=<dir>` query params. Mirrors C#
// `DownloadStationTaskProxyV2.AddTaskFromUrl` at `:43-57`.
//
// Note: the V2 add-URL path uses plain query params (NOT the
// JSON-quoted string oddity the V2 file-upload uses).
func (p *Proxy) addTaskFromURLV2(ctx context.Context, s Settings, taskURL, destination string) error {
	q := url.Values{}
	q.Set("type", "url")
	q.Set("url", taskURL)
	q.Set("create_list", "false")
	if strings.TrimSpace(destination) != "" {
		q.Set("destination", destination)
	}
	_, err := p.callTaskAPI(ctx, s, apiDownloadStation2Name, "create", 2, q)
	return err
}

// GetTasks lists tasks. Dispatches V1 or V2 based on the advertised
// task API. Both paths return the same [Task] shape so the Provider
// code stays unchanged.
func (p *Proxy) GetTasks(ctx context.Context, s Settings) ([]*Task, error) {
	v, err := p.taskAPIVersion(ctx, s)
	if err != nil {
		// Mirror the V1 catch-and-empty arm: only swallow API/RPC errors.
		if errors.Is(err, ErrRPC) {
			p.log.Debug("downloadstation: taskAPIVersion failed", "err", err)
			return []*Task{}, nil
		}
		return nil, err
	}
	if v == 2 {
		return p.getTasksV2(ctx, s)
	}
	return p.getTasksV1(ctx, s)
}

// getTasksV1 fires `SYNO.DownloadStation.Task` list method
// (version 1) with `additional=detail,transfer`. Mirrors C#
// `DownloadStationTaskProxyV1.GetTasks` at `:51-67`. The C#
// catch-all-and-empty arm at `:62-65` is preserved.
func (p *Proxy) getTasksV1(ctx context.Context, s Settings) ([]*Task, error) {
	q := url.Values{}
	q.Set("additional", "detail,transfer")
	body, err := p.callTaskAPI(ctx, s, apiDownloadStationName, "list", 1, q)
	if err != nil {
		if errors.Is(err, ErrRPC) {
			p.log.Debug("downloadstation: GetTasks (v1) list failed", "err", err)
			return []*Task{}, nil
		}
		return nil, err
	}
	var resp struct {
		Tasks []*Task `json:"tasks"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("downloadstation: unmarshal task list: %w", err)
	}
	return resp.Tasks, nil
}

// getTasksV2 fires TWO `SYNO.DownloadStation2.Task` list calls
// (`additional=detail` then `additional=transfer`) and merges the
// results by ID. Mirrors C# `DownloadStationTaskProxyV2.GetTasks`
// at `:59-108`. The dual-call pattern is a DSM 7 quirk — V2 list
// doesn't accept comma-separated additional values; each
// `additional=...` request returns only that arm's data.
//
// V2 also returns Status as an int (vs the snake-case string V1
// uses). The Task wire shape is the same struct (TaskStatus has a
// custom UnmarshalJSON that tolerates numeric input — see
// proxy.go's `:50-58` numeric fallback).
func (p *Proxy) getTasksV2(ctx context.Context, s Settings) ([]*Task, error) {
	detail, err := p.fetchTasksV2(ctx, s, "detail")
	if err != nil {
		if errors.Is(err, ErrRPC) {
			p.log.Debug("downloadstation: GetTasks (v2) detail failed", "err", err)
			return []*Task{}, nil
		}
		return nil, err
	}
	if len(detail.Task) == 0 {
		return []*Task{}, nil
	}
	transfer, err := p.fetchTasksV2(ctx, s, "transfer")
	if err != nil {
		if errors.Is(err, ErrRPC) {
			// Detail-only is incomplete but better than nothing — the
			// C# code returns empty on this branch via the same
			// catch-and-swallow arm. The Go port surfaces what it has
			// so RemoveCompletedDownloads can still process tasks even
			// if the second call hiccups.
			p.log.Debug("downloadstation: GetTasks (v2) transfer failed; returning detail-only", "err", err)
			return detail.Task, nil
		}
		return nil, err
	}
	// Merge by ID — transfer entry's Additional.Transfer map
	// fills in alongside the detail entry's Additional.Detail.
	transferByID := make(map[string]*Task, len(transfer.Task))
	for _, t := range transfer.Task {
		if t != nil {
			transferByID[t.ID] = t
		}
	}
	for _, t := range detail.Task {
		if t == nil {
			continue
		}
		if tx := transferByID[t.ID]; tx != nil {
			if t.Additional.Transfer == nil {
				t.Additional.Transfer = tx.Additional.Transfer
			}
		}
	}
	return detail.Task, nil
}

// fetchTasksV2 runs ONE V2 list call against the requested
// additional-field set.
func (p *Proxy) fetchTasksV2(ctx context.Context, s Settings, additional string) (*v2TaskList, error) {
	q := url.Values{}
	q.Set("additional", additional)
	body, err := p.callTaskAPI(ctx, s, apiDownloadStation2Name, "list", 1, q)
	if err != nil {
		return nil, err
	}
	var resp v2TaskList
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("downloadstation: unmarshal v2 task list (additional=%s): %w", additional, err)
	}
	return &resp, nil
}

// v2TaskList ports C# `DownloadStation2TaskInfoResponse` at
// `Responses/DownloadStation2TaskInfoResponse.cs`.
type v2TaskList struct {
	Offset int     `json:"offset"`
	Task   []*Task `json:"task"`
	Total  int     `json:"total"`
}

// RemoveTask removes a task. Dispatches V1 or V2.
func (p *Proxy) RemoveTask(ctx context.Context, s Settings, taskID string) error {
	v, err := p.taskAPIVersion(ctx, s)
	if err != nil {
		return err
	}
	if v == 2 {
		return p.removeTaskV2(ctx, s, taskID)
	}
	return p.removeTaskV1(ctx, s, taskID)
}

// removeTaskV1 fires `SYNO.DownloadStation.Task` delete method
// (version 1). Mirrors C# `DownloadStationTaskProxyV1.RemoveTask`
// at `:69-76`.
func (p *Proxy) removeTaskV1(ctx context.Context, s Settings, taskID string) error {
	q := url.Values{}
	q.Set("id", taskID)
	q.Set("force_complete", "false")
	_, err := p.callTaskAPI(ctx, s, apiDownloadStationName, "delete", 1, q)
	return err
}

// removeTaskV2 fires `SYNO.DownloadStation2.Task` delete method
// (version 2). Mirrors C# `DownloadStationTaskProxyV2.RemoveTask`
// at `:110-117` — same query-param shape as V1 but a different
// method version.
func (p *Proxy) removeTaskV2(ctx context.Context, s Settings, taskID string) error {
	q := url.Values{}
	q.Set("id", taskID)
	q.Set("force_complete", "false")
	_, err := p.callTaskAPI(ctx, s, apiDownloadStation2Name, "delete", 2, q)
	return err
}

// taskAPIVersion picks V2 when SYNO.DownloadStation2.Task is
// advertised by the DSM, else V1. Returns 0+error when neither API
// is reachable. Mirrors C# `DownloadStationTaskProxySelector.FetchProxy`
// at `:51-66`'s "V2-or-V1" preference order with the C# `IsApiSupported`
// = "info present in the cache" predicate. Sonarr DSM 7+ deployments
// almost universally land in the V2 arm.
func (p *Proxy) taskAPIVersion(ctx context.Context, s Settings) (int, error) {
	// Triggering V1 discovery refreshes the cache with both task
	// APIs (refreshAPIInfo always queries both); V1 has shipped on
	// every DSM that runs Download Station so it's a safe entry.
	if _, err := p.GetAPIInfo(ctx, s, apiDownloadStationName); err != nil {
		return 0, err
	}
	p.mu.Lock()
	_, hasV2 := p.apis[apiKey(s, apiDownloadStation2Name)]
	p.mu.Unlock()
	if hasV2 {
		return 2, nil
	}
	return 1, nil
}

// callTaskAPI is the shared per-call helper for the SYNO.DownloadStation.Task
// (V1) and SYNO.DownloadStation2.Task (V2) endpoints. Resolves the
// API path + appends the standard `api=...&version=...&method=...&_sid=...`
// params, fires GET, returns the raw `data` envelope.
func (p *Proxy) callTaskAPI(ctx context.Context, s Settings, apiName, method string, version int, extra url.Values) ([]byte, error) {
	api, err := p.GetAPIInfo(ctx, s, apiName)
	if err != nil {
		return nil, fmt.Errorf("downloadstation: discover %s: %w", apiName, err)
	}
	if version < api.MinVersion || version > api.MaxVersion {
		return nil, fmt.Errorf("%w: %s version %d not supported (server: %d..%d)",
			ErrUnavailable, apiName, version, api.MinVersion, api.MaxVersion)
	}
	sid, err := p.Authenticate(ctx, s, false)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	for k, vs := range extra {
		for _, v := range vs {
			q.Add(k, v)
		}
	}
	q.Set("api", api.Name)
	q.Set("version", fmt.Sprintf("%d", version))
	q.Set("method", method)
	q.Set("_sid", sid)
	full := fmt.Sprintf("%s/%s?%s", baseURL(s), api.Path, q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		return nil, fmt.Errorf("downloadstation: build %s %s: %w", apiName, method, err)
	}
	return p.execute(req, fmt.Sprintf("%s %s", apiName, method), apiName)
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
