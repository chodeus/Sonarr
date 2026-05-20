package nzbvortex

import (
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
	"net/http"
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
