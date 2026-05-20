package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Session is the validated form-auth session derived from a cookie.
// Identifier is the User.Identifier (UUID) the cookie was issued
// against; IssuedAt is the cookie's issue time (unix seconds).
type Session struct {
	Identifier string
	IssuedAt   time.Time
	Persistent bool
}

// SessionMaxAge mirrors C# `CookieAuthenticationOptions.ExpireTimeSpan
// = TimeSpan.FromDays(7)` at `AuthenticationBuilderExtensions.cs:46`.
const SessionMaxAge = 7 * 24 * time.Hour

// ErrInvalidSession is returned by VerifySessionCookie when the
// cookie value is malformed, the signature doesn't verify, or the
// cookie has expired. Constant value so callers don't need to
// branch on the failure type.
var ErrInvalidSession = errors.New("auth: invalid session cookie")

// SignSessionCookie returns the cookie-value string for `identifier`,
// signed with HMAC-SHA256 using `secret` as the key. The cookie
// value is `<base64(identifier+issuedUnix+persistent)>.<base64(mac)>` —
// two base64-url-no-pad segments separated by a `.`.
//
// Production wiring derives `secret` from the live API key
// (`<apiKey>:session`); the API key is already a per-install secret
// guaranteed unique per install. Rotating the API key invalidates
// every existing session, which matches the C# ResetApiKey
// behaviour where outstanding cookies stop validating.
//
// `persistent` is the C# `IsPersistent` flag — when true the cookie's
// Max-Age is set on the response so the browser persists it across
// restarts; when false the cookie is session-only (no Max-Age).
func SignSessionCookie(identifier string, persistent bool, issuedAt time.Time, secret []byte) string {
	persistFlag := "0"
	if persistent {
		persistFlag = "1"
	}
	payload := identifier + "|" + strconv.FormatInt(issuedAt.Unix(), 10) + "|" + persistFlag
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	sig := mac.Sum(nil)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) +
		"." +
		base64.RawURLEncoding.EncodeToString(sig)
}

// VerifySessionCookie inverts SignSessionCookie. Returns the parsed
// Session on success or ErrInvalidSession on any failure. Performs
// constant-time signature comparison to avoid leaking signing-key
// material through timing attacks.
//
// Expiry is enforced — issuedAt + SessionMaxAge < now → ErrInvalidSession.
func VerifySessionCookie(value string, secret []byte, now time.Time) (*Session, error) {
	parts := strings.SplitN(value, ".", 2)
	if len(parts) != 2 {
		return nil, ErrInvalidSession
	}
	payloadB, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, ErrInvalidSession
	}
	gotSig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrInvalidSession
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(payloadB)
	wantSig := mac.Sum(nil)
	if subtle.ConstantTimeCompare(gotSig, wantSig) != 1 {
		return nil, ErrInvalidSession
	}
	fields := strings.Split(string(payloadB), "|")
	if len(fields) != 3 {
		return nil, ErrInvalidSession
	}
	identifier := fields[0]
	issuedUnix, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return nil, ErrInvalidSession
	}
	issuedAt := time.Unix(issuedUnix, 0).UTC()
	if now.Sub(issuedAt) > SessionMaxAge {
		return nil, ErrInvalidSession
	}
	persistent := fields[2] == "1"
	return &Session{
		Identifier: identifier,
		IssuedAt:   issuedAt,
		Persistent: persistent,
	}, nil
}

// SessionSecret derives the signing key from the configured API key.
// Returns a non-empty byte slice. An empty API key yields a slice
// with the marker prefix only — sign-then-verify against that secret
// still works deterministically; tests that don't need the real key
// can pass an empty string. Production wiring passes the live
// configfile.Provider.APIKey result.
func SessionSecret(apiKey string) []byte {
	// Domain-separate the API key so the same key can't be used to
	// forge other HMACs elsewhere in the codebase (defensive — the
	// API key is currently only used for header / query auth + this
	// signer).
	const domain = "sonarr.session.v1:"
	out := make([]byte, 0, len(domain)+len(apiKey))
	out = append(out, domain...)
	out = append(out, apiKey...)
	return out
}

// NewSessionCookie returns a *fmt.Stringer-style assembled cookie
// value for the given identifier + persistence flag + clock. The
// caller stuffs the value into an `http.Cookie` and sets it on the
// response. Convenience over SignSessionCookie for the common path
// where issuedAt = now.
func NewSessionCookie(identifier string, persistent bool, secret []byte) string {
	return SignSessionCookie(identifier, persistent, time.Now().UTC(), secret)
}

// SessionCookieAttributes is a small bundle the API handlers use to
// set the response cookie. Path mirrors the URL base when set
// (cookie scoped to the Sonarr install); MaxAge is zero for session-
// only and SessionMaxAge for persistent.
type SessionCookieAttributes struct {
	Name       string
	Path       string
	MaxAge     int
	SameSite   string // "lax" / "strict" / "none"
	HTTPOnly   bool
	Secure     bool
	Persistent bool
}

// DefaultSessionAttributes returns sensible defaults: HttpOnly,
// SameSite=Lax, Secure when TLS is in effect (caller passes the
// flag), Path defaults to URL base + "/" or just "/".
func DefaultSessionAttributes(cookieName, urlBase string, secure, persistent bool) SessionCookieAttributes {
	if cookieName == "" {
		cookieName = "SonarrAuth"
	}
	path := "/"
	if urlBase != "" {
		path = "/" + strings.Trim(urlBase, "/") + "/"
	}
	maxAge := 0
	if persistent {
		maxAge = int(SessionMaxAge.Seconds())
	}
	return SessionCookieAttributes{
		Name:       cookieName,
		Path:       path,
		MaxAge:     maxAge,
		SameSite:   "lax",
		HTTPOnly:   true,
		Secure:     secure,
		Persistent: persistent,
	}
}

// String renders the cookie as the canonical `Set-Cookie` header
// value (no `Set-Cookie:` prefix). The caller sets the response
// header directly. Used by the v3 login handler; the host
// middleware reads back via stdlib `http.Cookie`.
func (a SessionCookieAttributes) String(value string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s=%s", a.Name, value)
	if a.Path != "" {
		fmt.Fprintf(&b, "; Path=%s", a.Path)
	}
	if a.MaxAge > 0 {
		fmt.Fprintf(&b, "; Max-Age=%d", a.MaxAge)
	}
	if a.SameSite != "" {
		fmt.Fprintf(&b, "; SameSite=%s", strings.Title(a.SameSite)) //nolint:staticcheck // strings.Title is fine for ASCII enum values
	}
	if a.HTTPOnly {
		b.WriteString("; HttpOnly")
	}
	if a.Secure {
		b.WriteString("; Secure")
	}
	return b.String()
}
