package freebox

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // Freebox auth protocol mandates HMAC-SHA1 (NOT a security primitive choice — wire spec).
	"encoding/base64"
	"encoding/hex"
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
)

// HTTPDoer is the [*http.Client] slice the [Proxy] depends on.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// Proxy is the HTTPS client for a Freebox Download endpoint.
// Mirrors C# `FreeboxDownloadProxy` at `FreeboxDownloadProxy.cs:24-275`.
// Goroutine-safe: the per-(host,appId,appToken) session-token cache
// is guarded by a mutex.
type Proxy struct {
	http HTTPDoer
	log  *slog.Logger

	mu       sync.Mutex
	sessions map[string]string // authKey → session_token
}

// NewProxy constructs a Proxy. http=nil binds [http.DefaultClient];
// log=nil binds [slog.Default].
func NewProxy(httpDoer HTTPDoer, log *slog.Logger) *Proxy {
	if httpDoer == nil {
		httpDoer = http.DefaultClient
	}
	if log == nil {
		log = slog.Default()
	}
	return &Proxy{http: httpDoer, log: log, sessions: map[string]string{}}
}

// baseURL composes the Freebox API base. Mirrors C# `BuildRequest`
// at `:205-220` — `<scheme>://<host>:<port><ApiUrl>`. ApiUrl is
// expected to start with `/` and end with `/` (the default
// `/api/v1/` shape); the proxy normalises both ends.
func baseURL(s Settings) string {
	scheme := "http"
	if s.UseSsl {
		scheme = "https"
	}
	api := strings.TrimSpace(s.ApiUrl)
	if api == "" {
		api = "/"
	}
	if !strings.HasPrefix(api, "/") {
		api = "/" + api
	}
	if !strings.HasSuffix(api, "/") {
		api += "/"
	}
	return fmt.Sprintf("%s://%s:%d%s", scheme, s.Host, s.Port, strings.TrimSuffix(api, "/"))
}

// authKey identifies the per-(host,appId,appToken) session cache
// slot. Mirrors C# `BuildCachedHeaderKey` at `:116-119`.
func authKey(s Settings) string {
	return fmt.Sprintf("%s:%s:%s", s.Host, s.AppId, s.AppToken)
}

// Response is the canonical Freebox API envelope. Mirrors C#
// `FreeboxResponse<T>` at `Responses/FreeboxResponse.cs:6-86`.
// Result is `json.RawMessage` so per-call sites unmarshal into
// their typed shape.
type Response struct {
	Success   bool            `json:"success"`
	Message   string          `json:"msg"`
	ErrorCode string          `json:"error_code"`
	Result    json.RawMessage `json:"result"`
}

// ErrorDescription mirrors C# `GetErrorDescription` at
// `FreeboxResponse.cs:72-80` — returns the human-readable string
// for the ErrorCode, falling back to "<code> - Unknown error".
func (r *Response) ErrorDescription() string {
	if r == nil {
		return ""
	}
	if msg, ok := errorDescriptions[r.ErrorCode]; ok {
		return msg
	}
	return fmt.Sprintf("%s - Unknown error", r.ErrorCode)
}

// errorDescriptions ports the C# message-table at
// `FreeboxResponse.cs:20-66` verbatim.
var errorDescriptions = map[string]string{
	// Common
	"invalid_request":     "Your request is invalid.",
	"invalid_api_version": "Invalid API base url or unknown API version.",
	"internal_error":      "Internal error.",
	// Login
	"auth_required":           "Invalid session token, or no session token sent.",
	"invalid_token":           "The app token you are trying to use is invalid or has been revoked.",
	"pending_token":           "The app token you are trying to use has not been validated by user yet.",
	"insufficient_rights":     "Your app permissions does not allow accessing this API.",
	"denied_from_external_ip": "You are trying to get an app_token from a remote IP.",
	"ratelimited":             "Too many auth error have been made from your IP.",
	"new_apps_denied":         "New application token request has been disabled.",
	"apps_denied":             "API access from apps has been disabled.",
	// Download
	"task_not_found":       "No task was found with the given id.",
	"invalid_operation":    "Attempt to perform an invalid operation.",
	"invalid_file":         "Error with the download file (invalid format ?).",
	"invalid_url":          "URL is invalid.",
	"not_implemented":      "Method not implemented.",
	"out_of_memory":        "No more memory available to perform the requested action.",
	"invalid_task_type":    "The task type is invalid.",
	"hibernating":          "The downloader is hibernating.",
	"need_bt_stopped_done": "This action is only valid for Bittorrent task in stopped or done state.",
	"bt_tracker_not_found": "Attempt to access an invalid tracker object.",
	"too_many_tasks":       "Too many tasks.",
	"invalid_address":      "Invalid peer address.",
	"port_conflict":        "Port conflict when setting config.",
	"invalid_priority":     "Invalid priority.",
	"ctx_file_error":       "Failed to initialize task context file (need to check disk).",
	"exists":               "Same task already exists.",
	"port_outside_range":   "Incoming port is not available for this customer.",
}

// LoginResult mirrors C# `FreeboxLogin` at `Responses/FreeboxLogin.cs`.
type LoginResult struct {
	LoggedIn     bool   `json:"logged_in"`
	Challenge    string `json:"challenge"`
	PasswordSalt string `json:"password_salt"`
	PasswordSet  bool   `json:"password_set"`
	SessionToken string `json:"session_token"`
}

// Authenticate runs a GET `/login` and verifies the `logged_in`
// field. Used by Test to confirm the API endpoint is reachable
// regardless of the session-token state. Mirrors C# `Authenticate`
// at `:37-47`.
func (p *Proxy) Authenticate(ctx context.Context, s Settings) error {
	var login LoginResult
	if err := p.doRequest(ctx, s, http.MethodGet, "/login", nil, "", false, &login); err != nil {
		return err
	}
	if !login.LoggedIn {
		// The C# code throws DownloadClientAuthenticationException here.
		// However, "logged_in=false" can occur during the normal nonce
		// challenge handshake too — the Test arm separately drives a
		// real session-token fetch to validate the credentials end-to-end.
		// We still surface a recognisable error so the Test arm can
		// distinguish this from transport failure.
		return fmt.Errorf("%w: /login returned logged_in=false", ErrAuth)
	}
	return nil
}

// DownloadConfiguration ports C# `Responses/FreeboxDownloadConfiguration.cs`.
// `DownloadDirectory` is base64-encoded on the wire — the
// `DecodedDownloadDirectory()` helper returns the UTF-8 plaintext
// the Provider's GetDownloadDirectory helper composes against.
type DownloadConfiguration struct {
	DownloadDirectory string `json:"download_dir"`
}

// DecodedDownloadDirectory returns the base64-decoded UTF-8 form
// of [DownloadConfiguration.DownloadDirectory]. Mirrors C#
// `DecodedDownloadDirectory` getter.
func (c *DownloadConfiguration) DecodedDownloadDirectory() string {
	if c == nil || c.DownloadDirectory == "" {
		return ""
	}
	raw, err := base64.StdEncoding.DecodeString(c.DownloadDirectory)
	if err != nil {
		return ""
	}
	return string(raw)
}

// AddTaskResult is the [Response.Result] envelope content for
// `/downloads/add`. Mirrors C# `FreeboxDownloadTask` reduced to the
// `Id` field the add-flow consumes.
type AddTaskResult struct {
	ID string `json:"id"`
}

// GetDownloadConfiguration fires GET `/downloads/config/`. Auth-
// required. Mirrors C# `FreeboxDownloadProxy.GetDownloadConfiguration`
// at `:102-107`. Used by the Provider's GetDownloadDirectory helper
// when DestinationDirectory is blank.
func (p *Proxy) GetDownloadConfiguration(ctx context.Context, s Settings) (*DownloadConfiguration, error) {
	var cfg DownloadConfiguration
	if err := p.doRequest(ctx, s, http.MethodGet, "/downloads/config/", nil, "", true, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// AddTaskFromURL fires POST `/downloads/add` with the URL as a
// `download_url` form parameter. Mirrors C#
// `FreeboxDownloadProxy.AddTaskFromUrl` at `:49-66`. After adding
// the task, calls [SetTorrentSettings] when any of addPaused /
// addFirst / seedRatio is non-zero (matches C# `:63`).
//
// downloadDir is the base64-encoded destination directory (the
// Provider's GetDownloadDirectory helper does the encoding).
// addPaused, addFirst flip the queue + status; seedRatio is
// non-nil → sets stop_ratio on the new task. Returns the new task
// ID.
func (p *Proxy) AddTaskFromURL(ctx context.Context, s Settings, downloadURL, downloadDir string, addPaused, addFirst bool, seedRatio *float64) (string, error) {
	form := url.Values{}
	form.Set("download_url", downloadURL)
	if downloadDir != "" {
		form.Set("download_dir", downloadDir)
	}
	body := []byte(form.Encode())

	var task AddTaskResult
	if err := p.doRequest(ctx, s, http.MethodPost, "/downloads/add", body, "application/x-www-form-urlencoded", true, &task); err != nil {
		return "", err
	}
	if strings.TrimSpace(task.ID) == "" {
		return "", fmt.Errorf("%w: /downloads/add returned empty id", ErrRPC)
	}
	if err := p.SetTorrentSettings(ctx, s, task.ID, addPaused, addFirst, seedRatio); err != nil {
		return "", err
	}
	return task.ID, nil
}

// AddTaskFromFile fires POST `/downloads/add` with the .torrent
// bytes as a `download_file` multipart-upload part. Mirrors C#
// `FreeboxDownloadProxy.AddTaskFromFile` at `:68-84`. After adding
// the task, calls [SetTorrentSettings] for the post-add flags.
func (p *Proxy) AddTaskFromFile(ctx context.Context, s Settings, filename string, contents []byte, downloadDir string, addPaused, addFirst bool, seedRatio *float64) (string, error) {
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	if downloadDir != "" {
		hdr := textproto.MIMEHeader{}
		hdr.Set("Content-Disposition", `form-data; name="download_dir"`)
		part, err := mw.CreatePart(hdr)
		if err != nil {
			return "", fmt.Errorf("freebox: multipart download_dir field: %w", err)
		}
		if _, err := part.Write([]byte(downloadDir)); err != nil {
			return "", fmt.Errorf("freebox: write download_dir field: %w", err)
		}
	}
	fileHdr := textproto.MIMEHeader{}
	fileHdr.Set("Content-Disposition", fmt.Sprintf(`form-data; name="download_file"; filename=%q`, filename))
	fileHdr.Set("Content-Type", "application/octet-stream")
	part, err := mw.CreatePart(fileHdr)
	if err != nil {
		return "", fmt.Errorf("freebox: multipart download_file part: %w", err)
	}
	if _, err := part.Write(contents); err != nil {
		return "", fmt.Errorf("freebox: write download_file part: %w", err)
	}
	if err := mw.Close(); err != nil {
		return "", fmt.Errorf("freebox: close multipart writer: %w", err)
	}

	var task AddTaskResult
	if err := p.doRequest(ctx, s, http.MethodPost, "/downloads/add", body.Bytes(), mw.FormDataContentType(), true, &task); err != nil {
		return "", err
	}
	if strings.TrimSpace(task.ID) == "" {
		return "", fmt.Errorf("%w: /downloads/add returned empty id", ErrRPC)
	}
	if err := p.SetTorrentSettings(ctx, s, task.ID, addPaused, addFirst, seedRatio); err != nil {
		return "", err
	}
	return task.ID, nil
}

// SetTorrentSettings fires PUT `/downloads/<id>` with a body that
// can include any combination of {status:"stopped", queue_pos:"1",
// stop_ratio:<float>}. No-op when all three knobs are at their
// neutral defaults. Mirrors C# `SetTorrentSettings` at `:121-153`.
func (p *Proxy) SetTorrentSettings(ctx context.Context, s Settings, id string, addPaused, addFirst bool, seedRatio *float64) error {
	body := map[string]any{}
	if addPaused {
		body["status"] = "stopped"
	}
	if addFirst {
		body["queue_pos"] = "1"
	}
	if seedRatio != nil {
		body["stop_ratio"] = *seedRatio
	}
	if len(body) == 0 {
		return nil
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("freebox: marshal settings body: %w", err)
	}
	return p.doRequest(ctx, s, http.MethodPut, "/downloads/"+id, raw, "application/json", true, nil)
}

// GetSessionToken runs the HMAC-SHA1 challenge → session-token
// flow. Mirrors C# `GetSessionToken` at `:155-203`. force=true
// bypasses the cache.
//
// Auth flow:
//
//  1. GET `/login` → {challenge, ...}
//  2. password = lowercase-hex(HMAC-SHA1(challenge, AppToken))
//  3. POST `/login/session` body {app_id, password} → {session_token}
//  4. cache + return session_token under [authKey].
func (p *Proxy) GetSessionToken(ctx context.Context, s Settings, force bool) (string, error) {
	key := authKey(s)
	if !force {
		p.mu.Lock()
		if tok, ok := p.sessions[key]; ok {
			p.mu.Unlock()
			return tok, nil
		}
		p.mu.Unlock()
	} else {
		p.mu.Lock()
		delete(p.sessions, key)
		p.mu.Unlock()
	}

	// Step 1: fetch challenge.
	var login LoginResult
	if err := p.doRequest(ctx, s, http.MethodGet, "/login", nil, "", false, &login); err != nil {
		return "", err
	}
	if strings.TrimSpace(login.Challenge) == "" {
		return "", fmt.Errorf("%w: /login returned empty challenge", ErrAuth)
	}

	// Step 2: HMAC-SHA1 the challenge under the AppToken.
	mac := hmac.New(sha1.New, []byte(s.AppToken))
	mac.Write([]byte(login.Challenge))
	password := strings.ToLower(hex.EncodeToString(mac.Sum(nil)))

	// Step 3: exchange for a session_token.
	body, err := json.Marshal(map[string]string{
		"app_id":   s.AppId,
		"password": password,
	})
	if err != nil {
		return "", fmt.Errorf("freebox: marshal session body: %w", err)
	}
	var session LoginResult
	if err := p.doRequest(ctx, s, http.MethodPost, "/login/session", body, "application/json", false, &session); err != nil {
		return "", err
	}
	if strings.TrimSpace(session.SessionToken) == "" {
		return "", fmt.Errorf("%w: /login/session returned empty session_token", ErrAuth)
	}

	p.mu.Lock()
	p.sessions[key] = session.SessionToken
	p.mu.Unlock()
	return session.SessionToken, nil
}

// doRequest builds + fires one HTTP request, decodes the
// [Response] envelope, and unmarshals the `result` field into out.
// withAuth=true attaches the `X-Fbx-App-Auth: <session_token>`
// header (via [Proxy.GetSessionToken] auth flow).
//
// Status-code handling matches C# `ProcessRequest` at `:222-275`:
// 401/403 → invalidate cache + ErrAuth; 404 → ErrAPI (the C#
// FreeboxDownloadException at `:254` — usually a bad ApiUrl);
// non-success body → ErrRPC carrying the decoded error_code msg.
func (p *Proxy) doRequest(ctx context.Context, s Settings, method, resource string, body []byte, contentType string, withAuth bool, out any) error {
	if withAuth {
		token, err := p.GetSessionToken(ctx, s, false)
		if err != nil {
			return err
		}
		return p.fireOnce(ctx, s, method, resource, body, contentType, token, out)
	}
	return p.fireOnce(ctx, s, method, resource, body, contentType, "", out)
}

// fireOnce issues a single request — used by [doRequest] for the
// no-retry path and by [GetSessionToken] for the bootstrap auth
// calls (which can't recurse through doRequest).
func (p *Proxy) fireOnce(ctx context.Context, s Settings, method, resource string, body []byte, contentType, sessionToken string, out any) error {
	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewReader(body)
	}
	// Preserve the resource path verbatim — Freebox's
	// `/downloads/config/` endpoint requires the trailing slash,
	// and bootstrap auth paths (`/login`, `/login/session`) work
	// regardless.
	full := baseURL(s) + resource
	req, err := http.NewRequestWithContext(ctx, method, full, reqBody)
	if err != nil {
		return fmt.Errorf("freebox: build %s %s: %w", method, resource, err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")
	if sessionToken != "" {
		req.Header.Set("X-Fbx-App-Auth", sessionToken)
	}

	resp, err := p.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %s %s: %w", ErrUnavailable, method, resource, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return fmt.Errorf("freebox: %s %s read body: %w", method, resource, err)
	}

	switch {
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized:
		p.mu.Lock()
		delete(p.sessions, authKey(s))
		p.mu.Unlock()
		var env Response
		_ = json.Unmarshal(respBody, &env)
		return fmt.Errorf("%w: %s %s: %s", ErrAuth, method, resource, env.ErrorDescription())
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("%w: %s %s: check ApiUrl", ErrAPI, method, resource)
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return fmt.Errorf("%w: %s %s: status %d", ErrUnavailable, method, resource, resp.StatusCode)
	}

	var env Response
	if err := json.Unmarshal(respBody, &env); err != nil {
		return fmt.Errorf("freebox: %s %s unmarshal envelope: %w", method, resource, err)
	}
	if !env.Success {
		return fmt.Errorf("%w: %s %s: %s", ErrRPC, method, resource, env.ErrorDescription())
	}
	if out != nil && len(env.Result) > 0 {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return fmt.Errorf("freebox: %s %s unmarshal result: %w", method, resource, err)
		}
	}
	return nil
}

// ErrUnavailable wraps every transport-level failure (network
// errors + non-2xx that isn't 401/403/404). Mirrors C#
// `DownloadClientUnavailableException` at `:235,239`.
var ErrUnavailable = errors.New("freebox: unavailable")

// ErrAuth wraps 401/403 + empty challenge/session-token responses.
// Mirrors C# `DownloadClientAuthenticationException` at `:45,250`.
var ErrAuth = errors.New("freebox: authentication failed")

// ErrAPI wraps a 404 status code (typically a bad ApiUrl).
// Mirrors C# `FreeboxDownloadException` at `:254`.
var ErrAPI = errors.New("freebox: api endpoint not found")

// ErrRPC wraps every server-side {success:false} response.
// Mirrors C# `DownloadClientException` at `:268`.
var ErrRPC = errors.New("freebox: rpc error")
