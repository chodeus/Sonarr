package main

import (
	"context"
	"fmt"

	"sonarr.tv/sonarr/internal/auth"
	"sonarr.tv/sonarr/internal/configfile"
	"sonarr.tv/sonarr/internal/host"
)

// authAdapter bundles the configfile.Provider (which already
// satisfies host.APIKeyResolver + host.AuthPolicy) with the
// *auth.Service so the host's requireAPIKey middleware can also
// validate Forms-mode session cookies and recognise External-mode
// via the AuthMethodResolver interface.
//
// Embedding *configfile.Provider promotes its APIKey,
// AuthenticationRequired, TrustCgnatIpAddresses, and
// AuthenticationMethod methods unchanged. The new
// SessionCookieName / SessionSecret / VerifySession methods round
// out host.SessionVerifier; SessionCookieName derives from the
// InstanceName, SessionSecret from the live API key, and
// VerifySession round-trips through auth.VerifySessionCookie +
// auth.Service.FindByIdentifier so a deleted user invalidates
// outstanding cookies.
//
// Audit fix B.9.
type authAdapter struct {
	*configfile.Provider
	svc *auth.Service
}

func newAuthAdapter(cfg *configfile.Provider, svc *auth.Service) *authAdapter {
	return &authAdapter{Provider: cfg, svc: svc}
}

// SessionCookieName derives the cookie key from the InstanceName.
// Mirrors C# `AuthenticationBuilderExtensions.AddAppAuthentication`
// at `:33-44`.
func (a *authAdapter) SessionCookieName(ctx context.Context) (string, error) {
	name, err := a.InstanceName(ctx)
	if err != nil {
		return "", fmt.Errorf("authAdapter: session cookie name: %w", err)
	}
	return auth.DeriveCookieName(name), nil
}

// SessionSecret returns the HMAC signing key derived from the
// configured API key. Rotating the API key invalidates outstanding
// session cookies.
func (a *authAdapter) SessionSecret(ctx context.Context) ([]byte, error) {
	key, err := a.APIKey(ctx)
	if err != nil {
		return nil, fmt.Errorf("authAdapter: session secret: %w", err)
	}
	return auth.SessionSecret(key), nil
}

// VerifySession verifies the cookie value and confirms the user row
// still exists. Returns the identifier on success or an empty
// string on any failure.
func (a *authAdapter) VerifySession(ctx context.Context, cookieValue string) (string, error) {
	if a.svc == nil {
		return "", fmt.Errorf("authAdapter: nil auth service")
	}
	secret, err := a.SessionSecret(ctx)
	if err != nil {
		return "", err
	}
	return auth.VerifySessionFromService(ctx, a.svc, secret, cookieValue)
}

// URLBase returns the configured URL base — required by
// auth.CookieSetter so the login handler can scope the cookie Path
// to the Sonarr install.
func (a *authAdapter) URLBase(ctx context.Context) (string, error) {
	return a.Provider.URLBase(ctx)
}

// Compile-time guards.
var (
	_ host.APIKeyResolver     = (*authAdapter)(nil)
	_ host.AuthPolicy         = (*authAdapter)(nil)
	_ host.AuthMethodResolver = (*authAdapter)(nil)
	_ host.SessionVerifier    = (*authAdapter)(nil)
	_ auth.CookieSetter       = (*authAdapter)(nil)
)
