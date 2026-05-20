package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// CookieSetter abstracts what the login + logout handlers need to
// set the session cookie. Production wires this against the
// configfile.Provider via a small adapter; the interface keeps the
// handler decoupled from the concrete provider.
type CookieSetter interface {
	// SessionCookieName returns the cookie key (typically derived
	// from InstanceName).
	SessionCookieName(ctx context.Context) (string, error)
	// SessionSecret returns the HMAC signing key.
	SessionSecret(ctx context.Context) ([]byte, error)
	// URLBase returns the configured URL base (used as the cookie
	// Path so the cookie scopes to the Sonarr install).
	URLBase(ctx context.Context) (string, error)
}

// LoginRequest is the wire shape the POST /login handler accepts.
// Form-encoded per the C# `[FromForm] LoginResource` at
// `AuthenticationController.cs:36`.
type LoginRequest struct {
	Username   string `json:"username"`
	Password   string `json:"password"`
	RememberMe string `json:"rememberMe"`
}

// LoginResponse is returned on success. The cookie is set on the
// Set-Cookie header; the JSON body carries the user identifier so
// clients (the React UI) can update their local state.
type LoginResponse struct {
	Username   string `json:"username"`
	Identifier string `json:"identifier"`
}

// LoginHandler returns the POST /login handler. Accepts form-encoded
// `username`/`password`/`rememberMe` (the React UI's existing post
// shape) AND JSON for programmatic clients. On success: sets the
// session cookie and returns 200 + LoginResponse. On failure: 401
// with an empty body (matches C# `Unauthorized()` at
// `AuthenticationController.cs:72`).
//
// Audit fix B.9.
func LoginHandler(svc *Service, cookies CookieSetter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, err := parseLoginRequest(r)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		ctx := r.Context()
		user, err := svc.Authenticate(ctx, req.Username, req.Password)
		if err != nil {
			if errors.Is(err, ErrInvalidCredentials) {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Cookie attributes.
		cookieName, err := cookies.SessionCookieName(ctx)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		secret, err := cookies.SessionSecret(ctx)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		urlBase, _ := cookies.URLBase(ctx)
		persistent := strings.EqualFold(req.RememberMe, "on") || strings.EqualFold(req.RememberMe, "true")

		// Honour the request's TLS posture so the Secure flag is
		// only set on HTTPS (avoids breaking dev HTTP setups).
		secure := r.TLS != nil
		attrs := DefaultSessionAttributes(cookieName, urlBase, secure, persistent)
		cookieValue := SignSessionCookie(user.Identifier, persistent, time.Now().UTC(), secret)
		w.Header().Add("Set-Cookie", attrs.String(cookieValue))

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(LoginResponse{
			Username:   user.Username,
			Identifier: user.Identifier,
		})
	})
}

// LogoutHandler clears the session cookie by setting it to an empty
// value with Max-Age=0. Mirrors C# `AuthenticationController.Logout`
// at `:88-94` but returns 204 instead of redirecting (clients drive
// the post-logout navigation; redirecting from an API endpoint
// blurs the API/UI boundary that the Go port keeps clean).
//
// Audit fix B.9.
func LogoutHandler(cookies CookieSetter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		cookieName, err := cookies.SessionCookieName(ctx)
		if err != nil || cookieName == "" {
			cookieName = "SonarrAuth"
		}
		urlBase, _ := cookies.URLBase(ctx)
		path := "/"
		if urlBase != "" {
			path = "/" + strings.Trim(urlBase, "/") + "/"
		}
		// Setting Max-Age=0 with a blank value tells the browser to
		// delete the cookie immediately.
		w.Header().Add("Set-Cookie",
			fmt.Sprintf("%s=; Path=%s; Max-Age=0; HttpOnly; SameSite=Lax", cookieName, path))
		w.WriteHeader(http.StatusNoContent)
	})
}

// parseLoginRequest accepts both `application/x-www-form-urlencoded`
// (the C# default) and `application/json` (programmatic clients).
// Empty / missing credentials yield a typed error so the handler
// returns 400 rather than 401.
func parseLoginRequest(r *http.Request) (LoginRequest, error) {
	ct := r.Header.Get("Content-Type")
	switch {
	case strings.HasPrefix(ct, "application/json"):
		var req LoginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return LoginRequest{}, err
		}
		if req.Username == "" || req.Password == "" {
			return LoginRequest{}, errors.New("auth: missing credentials")
		}
		return req, nil
	default:
		if err := r.ParseForm(); err != nil {
			return LoginRequest{}, err
		}
		req := LoginRequest{
			Username:   r.FormValue("username"),
			Password:   r.FormValue("password"),
			RememberMe: r.FormValue("rememberMe"),
		}
		if req.Username == "" || req.Password == "" {
			return LoginRequest{}, errors.New("auth: missing credentials")
		}
		return req, nil
	}
}

// VerifySessionFromService is the convenience adapter the host's
// SessionVerifier consumes — given a cookie value + service, returns
// the validated user identifier or "" on any failure. Combines
// VerifySessionCookie (signature check) with Service.FindByIdentifier
// (row still exists) so a stale identifier (deleted user) is also
// rejected.
//
// Audit fix B.9.
func VerifySessionFromService(ctx context.Context, svc *Service, secret []byte, cookieValue string) (string, error) {
	sess, err := VerifySessionCookie(cookieValue, secret, time.Now().UTC())
	if err != nil {
		return "", err
	}
	if _, err := svc.FindByIdentifier(ctx, sess.Identifier); err != nil {
		return "", err
	}
	return sess.Identifier, nil
}
