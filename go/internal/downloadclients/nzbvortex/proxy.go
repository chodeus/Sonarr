package nzbvortex

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
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

// Proxy is the HTTPS client for an NZBVortex endpoint. Mirrors C#
// `NzbVortexProxy` at `NzbVortexProxy.cs:25-218`. Goroutine-safe:
// the in-memory session-ID cache is guarded by a mutex.
type Proxy struct {
	http      HTTPDoer
	log       *slog.Logger
	cnonceGen func() string

	mu       sync.Mutex
	sessions map[string]string // authKey → sessionId
}

// NewProxy constructs a Proxy. http=nil binds an [*http.Client] with
// a permissive TLS config (the typical NZBVortex deploy uses a
// self-signed cert; this matches C# `_httpClient.Execute` which
// also tolerates trust failures via `WebExceptionStatus.TrustFailure`
// at `NzbVortexProxy.cs:168-171`). log=nil binds [slog.Default].
func NewProxy(httpDoer HTTPDoer, log *slog.Logger) *Proxy {
	if httpDoer == nil {
		httpDoer = &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // NZBVortex defaults to a self-signed cert; matches C# trust-failure tolerance at NzbVortexProxy.cs:168-171.
			},
		}
	}
	if log == nil {
		log = slog.Default()
	}
	return &Proxy{
		http:      httpDoer,
		log:       log,
		cnonceGen: defaultCnonce,
		sessions:  map[string]string{},
	}
}

// SetCnonceGenerator overrides the cnonce source. Tests inject a
// deterministic generator so the auth flow's expected hash is
// reproducible.
func (p *Proxy) SetCnonceGenerator(f func() string) { p.cnonceGen = f }

// defaultCnonce generates a 16-byte random hex string. The C# code
// uses `Guid.NewGuid().ToString()` at `NzbVortexProxy.cs:192` — the
// Go port substitutes a 32-hex-char random value (same entropy
// budget, no UUID-shape requirement at the wire).
func defaultCnonce() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// baseURL composes the absolute URL the request POSTs / GETs.
// Mirrors C# `NzbVortexProxy.BuildRequest` at `:116-124`:
//
//	https://<Host>:<Port>[<UrlBase>]/api
//
// UrlBase is trimmed + normalized to leading-slash form. NZBVortex
// has no UseSsl toggle — scheme is always HTTPS.
func baseURL(s Settings) string {
	base := fmt.Sprintf("https://%s:%d", s.Host, s.Port)
	ub := strings.TrimSpace(s.UrlBase)
	if ub != "" && ub != "/" {
		if !strings.HasPrefix(ub, "/") {
			ub = "/" + ub
		}
		base += strings.TrimRight(ub, "/")
	}
	return base + "/api"
}

// authKey identifies a unique cache slot for the in-memory
// sessionId. Two clients pointed at the same host but with
// different ApiKey strings need independent sessions — mirrors C#
// `_authSessionIdCache.Find(authKey)` at `:181` with the C# key
// format `"{baseUrl}:{apiKey}"` at `:179`.
func authKey(s Settings) string { return baseURL(s) + ":" + s.ApiKey }

// responseBase mirrors C# `NzbVortexResponseBase` at
// `Responses/NzbVortexResponseBase.cs`. Embedded by every typed
// response below for the [`result`] field gate.
type responseBase struct {
	Result string `json:"result"`
}

// VersionResponse mirrors C# `NzbVortexVersionResponse` at
// `Responses/NzbVortexVersionResponse.cs`.
type VersionResponse struct {
	responseBase
	Version string `json:"version"`
}

// APIVersionResponse mirrors C# `NzbVortexApiVersionResponse` at
// `Responses/NzbVortexApiVersionResponse.cs`.
type APIVersionResponse struct {
	responseBase
	APILevel string `json:"apiLevel"`
}

// authNonceResponse mirrors C# `NzbVortexAuthNonceResponse` at
// `Responses/NzbVortexAuthNonceResponse.cs`.
type authNonceResponse struct {
	AuthNonce string `json:"authNonce"`
}

// authResponse mirrors C# `NzbVortexAuthResponse` at
// `Responses/NzbVortexAuthResponse.cs`. LoginResult is the
// "Successful" / "Failed" enum the C# Newtonsoft converter
// serialises as a string.
type authResponse struct {
	responseBase
	LoginResult string `json:"loginResult"`
	SessionID   string `json:"sessionId"`
}

// resultNotLoggedIn / resultOk are the NzbVortexResultType string
// values. C# defines them as an enum but the wire shape is a
// string via the converter at `Responses/NzbVortexResponseBase.cs`.
const (
	resultOk           = "Ok"
	resultNotLoggedIn  = "NotLoggedIn"
	resultUnknown      = "UnknownCommand"
	loginResultSuccess = "Successful"
	loginResultFailed  = "Failed"
)

// get fires one HTTP GET and unmarshals into out. The
// `requiresAuth` arm runs the auth flow first + appends
// `?sessionid=...` to the URL; on the "NotLoggedIn" result the
// session is invalidated and the request is retried exactly once
// (mirrors C# `ProcessRequest` at `:134-154`).
//
// out must embed [responseBase] so the result-gate check has
// something to read. The Go port keys off the embedded `result`
// field string; nil out is rejected (every typed response carries
// the gate field).
func (p *Proxy) get(ctx context.Context, s Settings, resource string, query url.Values, requiresAuth bool, out interface{ resultField() string }) error {
	if out == nil {
		return errors.New("nzbvortex: get: nil out")
	}
	err := p.doGet(ctx, s, resource, query, requiresAuth, out)
	if err != nil {
		return err
	}
	if out.resultField() == resultNotLoggedIn && requiresAuth {
		p.log.Debug("nzbvortex: NotLoggedIn → reauth + retry", "resource", resource)
		p.invalidateSession(s)
		err = p.doGet(ctx, s, resource, query, requiresAuth, out)
		if err != nil {
			return err
		}
		if out.resultField() == resultNotLoggedIn {
			return fmt.Errorf("%w: %s", ErrSessionLost, resource)
		}
	}
	if out.resultField() == resultUnknown {
		return fmt.Errorf("%w: %s", ErrRPC, resource)
	}
	return nil
}

// doGet builds + fires one HTTP GET — no retry. Used directly by
// the auth flow (which bypasses [Proxy.get]'s retry to avoid an
// infinite loop) and indirectly by [Proxy.get].
func (p *Proxy) doGet(ctx context.Context, s Settings, resource string, query url.Values, requiresAuth bool, out any) error {
	if requiresAuth {
		sid, err := p.ensureSession(ctx, s, false)
		if err != nil {
			return err
		}
		if query == nil {
			query = url.Values{}
		}
		query.Set("sessionid", sid)
	}
	u := baseURL(s) + "/" + strings.TrimLeft(resource, "/")
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("nzbvortex: build request %s: %w", resource, err)
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrUnavailable, resource, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return fmt.Errorf("nzbvortex: %s read body: %w", resource, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: %s status %d: %s", ErrUnavailable, resource, resp.StatusCode,
			strings.TrimSpace(truncate(string(body), 256)))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("nzbvortex: %s unmarshal: %w", resource, err)
	}
	return nil
}

// ensureSession returns a cached sessionId for s or runs the
// nonce → login flow when no cache hit exists / force=true.
// Mirrors C# `AuthenticateClient` at `NzbVortexProxy.cs:177-216`.
//
// Auth flow:
//
//  1. GET /api/auth/nonce → {authNonce}
//  2. cnonce = uuid
//  3. hash = base64( sha256( "<nonce>:<cnonce>:<apikey>" ) )
//  4. GET /api/auth/login?nonce=&cnonce=&hash= → {loginResult, sessionId}
//  5. cache + return sessionId
//
// C# divergence: the C# `SHA256Hash().HexToByteArray()` is a hex →
// bytes round-trip; the Go port computes the raw SHA-256 bytes
// directly (HexToByteArray would just be the inverse of the
// .NET extension's hex-encoding step). Net base64 output is
// byte-identical.
func (p *Proxy) ensureSession(ctx context.Context, s Settings, force bool) (string, error) {
	key := authKey(s)

	if !force {
		p.mu.Lock()
		if sid, ok := p.sessions[key]; ok {
			p.mu.Unlock()
			return sid, nil
		}
		p.mu.Unlock()
	} else {
		p.mu.Lock()
		delete(p.sessions, key)
		p.mu.Unlock()
	}

	// Step 1: nonce
	var nonce authNonceResponse
	if err := p.doGet(ctx, s, "auth/nonce", nil, false, &nonce); err != nil {
		return "", fmt.Errorf("nzbvortex: auth/nonce: %w", err)
	}
	if strings.TrimSpace(nonce.AuthNonce) == "" {
		return "", fmt.Errorf("%w: auth/nonce returned empty authNonce", ErrAuth)
	}

	// Step 2-3: cnonce + hash
	cnonce := p.cnonceGen()
	hashInput := fmt.Sprintf("%s:%s:%s", nonce.AuthNonce, cnonce, s.ApiKey)
	sum := sha256.Sum256([]byte(hashInput))
	hash := base64.StdEncoding.EncodeToString(sum[:])

	// Step 4: login
	q := url.Values{}
	q.Set("nonce", nonce.AuthNonce)
	q.Set("cnonce", cnonce)
	q.Set("hash", hash)
	var login authResponse
	if err := p.doGet(ctx, s, "auth/login", q, false, &login); err != nil {
		return "", fmt.Errorf("nzbvortex: auth/login: %w", err)
	}
	if login.LoginResult == loginResultFailed {
		return "", fmt.Errorf("%w: auth/login rejected the ApiKey", ErrAuth)
	}
	if strings.TrimSpace(login.SessionID) == "" {
		return "", fmt.Errorf("%w: auth/login returned empty sessionId", ErrAuth)
	}

	p.mu.Lock()
	p.sessions[key] = login.SessionID
	p.mu.Unlock()
	return login.SessionID, nil
}

// invalidateSession drops the cached sessionId for s — used by the
// NotLoggedIn-retry branch in [Proxy.get].
func (p *Proxy) invalidateSession(s Settings) {
	p.mu.Lock()
	delete(p.sessions, authKey(s))
	p.mu.Unlock()
}

// resultField gives every typed response a uniform accessor for
// the embedded `result` string. Implemented as method-value below.
func (r *responseBase) resultField() string { return r.Result }

// StateType ports C# `NzbVortexStateType` at
// `NzbVortexStateType.cs:3-30`. Numeric values match the C# enum
// for wire-stable JSON deserialisation. The Hadouken-style state
// table in [NzbVortex.GetItems] (5.J69 follow-up) reads these.
type StateType int

// StateType values mirror the C# enum order. The names match the
// (slightly typo'd) C# spellings where applicable so the cross-
// reference at `:6` (QuaedForProcessing) stays one-to-one with
// the wire protocol.
const (
	StateWaiting                  StateType = 0
	StateDownloading              StateType = 1
	StateWaitingForSave           StateType = 2
	StateSaving                   StateType = 3
	StateSaved                    StateType = 4
	StatePasswordRequest          StateType = 5
	StateQuaedForProcessing       StateType = 6
	StateUserWaitForProcessing    StateType = 7
	StateChecking                 StateType = 8
	StateRepairing                StateType = 9
	StateJoining                  StateType = 10
	StateWaitForFurtherProcessing StateType = 11
	StateJoining2                 StateType = 12
	StateWaitForUncompress        StateType = 13
	StateUncompressing            StateType = 14
	StateWaitForCleanup           StateType = 15
	StateCleaningUp               StateType = 16
	StateCleanedUp                StateType = 17
	StateMovingToCompleted        StateType = 18
	StateMoveCompleted            StateType = 19
	StateDone                     StateType = 20
	StateUncompressFailed         StateType = 21
	StateCheckFailedDataCorrupt   StateType = 22
	StateMoveFailed               StateType = 23
	StateBadlyEncoded             StateType = 24
)

// Group ports C# `NzbVortexGroup` at `NzbVortexGroup.cs:3-6`.
type Group struct {
	GroupName string `json:"groupName"`
}

// QueueItem ports C# `NzbVortexQueueItem` at
// `NzbVortexQueueItem.cs:3-22`. Field names match the wire JSON
// (which the Go port maps via struct tags). Hadouken-style numeric
// fields use int64 where the C# code uses `long`.
type QueueItem struct {
	ID                 int       `json:"id"`
	UiTitle            string    `json:"uiTitle"`
	DestinationPath    string    `json:"destinationPath"`
	NzbFilename        string    `json:"nzbFilename"`
	IsPaused           bool      `json:"isPaused"`
	State              StateType `json:"state"`
	StatusText         string    `json:"statusText"`
	TransferedSpeed    int       `json:"transferedSpeed"`
	Progress           float64   `json:"progress"`
	DownloadedSize     int64     `json:"downloadedSize"`
	TotalDownloadSize  int64     `json:"totalDownloadSize"`
	PostDate           int64     `json:"postDate"`
	TotalArticleCount  int       `json:"totalArticleCount"`
	FailedArticleCount int       `json:"failedArticleCount"`
	GroupUUID          string    `json:"groupUUID"`
	AddUUID            string    `json:"addUUID"`
	GroupName          string    `json:"groupName"`
}

// groupResponse mirrors C# `NzbVortexGroupResponse` at
// `Responses/NzbVortexGroupResponse.cs`.
type groupResponse struct {
	responseBase
	Groups []*Group `json:"groups"`
}

// queueResponse mirrors C# `NzbVortexQueueResponse` at
// `Responses/NzbVortexQueueResponse.cs`. Note the C# `JsonProperty`
// at `:8` renames the field to `nzbs` on the wire — the Go tag
// matches.
type queueResponse struct {
	responseBase
	Items []*QueueItem `json:"nzbs"`
}

// AddResponse mirrors C# `NzbVortexAddResponse` at
// `Responses/NzbVortexAddResponse.cs`. The C# `JsonProperty("add_uuid")`
// at `:6` renames Id to `add_uuid` on the wire — the Go tag matches.
type AddResponse struct {
	responseBase
	ID string `json:"add_uuid"`
}

// DownloadNzb fires a multipart POST to `nzb/add` with the .nzb
// bytes attached as the `name` form-file (filename + body +
// `application/x-nzb` content-type). Mirrors C#
// `NzbVortexProxy.DownloadNzb` at `:40-56`.
//
// Query params:
//
//   - `priority=<int>` — always emitted (C# `:44`).
//   - `groupname=<TvCategory>` — emitted only when Settings.TvCategory
//     is non-empty (C# `:46-49`).
//   - `sessionid=<sid>` — appended by the auth path inside [Proxy.postMultipart].
//
// Returns the add_uuid string the server assigns to the new
// download. Errors propagate the proxy's sentinel errors.
func (p *Proxy) DownloadNzb(ctx context.Context, s Settings, contents []byte, filename string, priority int) (string, error) {
	q := url.Values{}
	q.Set("priority", fmt.Sprintf("%d", priority))
	if cat := strings.TrimSpace(s.TvCategory); cat != "" {
		q.Set("groupname", cat)
	}

	body, contentType, err := buildMultipart("name", filename, "application/x-nzb", contents)
	if err != nil {
		return "", fmt.Errorf("nzbvortex: build multipart: %w", err)
	}

	var out AddResponse
	if err := p.postMultipart(ctx, s, "nzb/add", q, body, contentType, &out); err != nil {
		return "", err
	}
	if strings.TrimSpace(out.ID) == "" {
		return "", fmt.Errorf("%w: nzb/add returned empty add_uuid", ErrRPC)
	}
	return out.ID, nil
}

// buildMultipart constructs a multipart/form-data body with a
// single file part: `name="<fieldName>"; filename="<filename>"`,
// Content-Type=contentType, payload=contents. Returns the encoded
// body + the boundary-bearing Content-Type header value.
//
// Mirrors C# `HttpRequestBuilder.AddFormUpload(name, filename,
// fileData, contentType)` at NzbVortexProxy.cs:51. The C# code
// emits identical field shape; the Go port uses
// [mime/multipart.Writer] for the well-formed encoding.
func buildMultipart(fieldName, filename, contentType string, contents []byte) (*bytes.Buffer, string, error) {
	buf := &bytes.Buffer{}
	mw := multipart.NewWriter(buf)
	hdr := textproto.MIMEHeader{}
	hdr.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, fieldName, filename))
	hdr.Set("Content-Type", contentType)
	part, err := mw.CreatePart(hdr)
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(contents); err != nil {
		return nil, "", err
	}
	if err := mw.Close(); err != nil {
		return nil, "", err
	}
	return buf, mw.FormDataContentType(), nil
}

// postMultipart fires one HTTP POST with a multipart/form-data
// body. Auth is required (the sessionid query param is appended
// by the auth path); the NotLoggedIn-retry branch is NOT exercised
// here because the C# code calls ProcessRequest with the standard
// auth-retry path. The Go port mirrors that — re-fires the same
// multipart body on NotLoggedIn.
//
// The retry needs a fresh body each time since the first POST
// consumed the buffer. We hold the bytes (post-encode) and rebuild
// the bytes.Reader per attempt.
func (p *Proxy) postMultipart(ctx context.Context, s Settings, resource string, query url.Values, body *bytes.Buffer, contentType string, out *AddResponse) error {
	payload := body.Bytes()
	if err := p.doPostMultipart(ctx, s, resource, query, payload, contentType, out); err != nil {
		return err
	}
	if out.resultField() == resultNotLoggedIn {
		p.log.Debug("nzbvortex: nzb/add NotLoggedIn → reauth + retry")
		p.invalidateSession(s)
		if err := p.doPostMultipart(ctx, s, resource, query, payload, contentType, out); err != nil {
			return err
		}
		if out.resultField() == resultNotLoggedIn {
			return fmt.Errorf("%w: %s", ErrSessionLost, resource)
		}
	}
	if out.resultField() == resultUnknown {
		return fmt.Errorf("%w: %s", ErrRPC, resource)
	}
	return nil
}

// doPostMultipart builds + fires one POST — no retry. The auth
// flow runs first so sessionid is appended to the query string.
func (p *Proxy) doPostMultipart(ctx context.Context, s Settings, resource string, query url.Values, payload []byte, contentType string, out *AddResponse) error {
	sid, err := p.ensureSession(ctx, s, false)
	if err != nil {
		return err
	}
	q := url.Values{}
	for k, vs := range query {
		for _, v := range vs {
			q.Add(k, v)
		}
	}
	q.Set("sessionid", sid)

	u := baseURL(s) + "/" + strings.TrimLeft(resource, "/") + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("nzbvortex: build %s request: %w", resource, err)
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := p.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrUnavailable, resource, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return fmt.Errorf("nzbvortex: %s read body: %w", resource, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: %s status %d: %s", ErrUnavailable, resource, resp.StatusCode,
			strings.TrimSpace(truncate(string(respBody), 256)))
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("nzbvortex: %s unmarshal: %w", resource, err)
	}
	return nil
}

// GetGroups fires `group`. Auth required. Mirrors C#
// `NzbVortexProxy.GetGroups` at `:83-89`.
func (p *Proxy) GetGroups(ctx context.Context, s Settings) ([]*Group, error) {
	var out groupResponse
	if err := p.get(ctx, s, "group", nil, true, &out); err != nil {
		return nil, err
	}
	return out.Groups, nil
}

// GetQueue fires `nzb` with the optional `groupName` filter +
// the `limitDone` cap. Auth required. Mirrors C#
// `NzbVortexProxy.GetQueue` at `:91-105`. doneLimit caps how
// many completed items the server returns — Sonarr's GetItems
// path uses 30, Test uses 1.
func (p *Proxy) GetQueue(ctx context.Context, s Settings, doneLimit int) ([]*QueueItem, error) {
	q := url.Values{}
	if cat := strings.TrimSpace(s.TvCategory); cat != "" {
		q.Set("groupName", cat)
	}
	q.Set("limitDone", fmt.Sprintf("%d", doneLimit))
	var out queueResponse
	if err := p.get(ctx, s, "nzb", q, true, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// GetVersion fires `app/appversion`. No auth required. Mirrors C#
// `NzbVortexProxy.GetVersion` at `:65-72`.
func (p *Proxy) GetVersion(ctx context.Context, s Settings) (*VersionResponse, error) {
	var out VersionResponse
	if err := p.get(ctx, s, "app/appversion", nil, false, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetAPIVersion fires `app/apilevel`. No auth required. Mirrors C#
// `NzbVortexProxy.GetApiVersion` at `:74-81`. Returns the response
// whose APILevel field carries the dotted-numeric version string.
func (p *Proxy) GetAPIVersion(ctx context.Context, s Settings) (*APIVersionResponse, error) {
	var out APIVersionResponse
	if err := p.get(ctx, s, "app/apilevel", nil, false, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// truncate clips s to n with an ellipsis for error-message bodies.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ErrUnavailable wraps every transport-level failure (network
// errors, non-2xx responses). Mirrors C# `DownloadClientUnavailableException`
// at `:170,173`.
var ErrUnavailable = errors.New("nzbvortex: unavailable")

// ErrAuth wraps an explicit auth flow failure (empty nonce, empty
// session, or LoginResult=Failed). Mirrors C# `NzbVortexAuthenticationException`
// at `:207`.
var ErrAuth = errors.New("nzbvortex: authentication failed")

// ErrRPC wraps a server-side "UnknownCommand" result. Mirrors the
// C# DownloadClientException raised on JSON deserialisation
// failure at `:160` (when the server returns a payload that
// doesn't match the expected shape — UnknownCommand falls under
// the same generic-failure umbrella).
var ErrRPC = errors.New("nzbvortex: rpc error")

// ErrSessionLost is the post-reauth-retry "still NotLoggedIn"
// failure. Mirrors C# `DownloadClientException("Unable to
// connect to remain authenticated to NzbVortex")` at `:152`.
var ErrSessionLost = errors.New("nzbvortex: session lost after reauth")
