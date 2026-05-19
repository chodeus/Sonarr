package host

import (
	"context"
	"net/http"
	"strings"

	"sonarr.tv/sonarr/internal/auth"
)

// APIKeyResolver returns the configured API key. Called on every
// authed request so a runtime ResetApiKey takes effect immediately,
// matching the C# behaviour where IConfigFileProvider re-reads the
// stored value on demand.
//
// `*configfile.Provider` satisfies this interface via its existing
// `APIKey(ctx) (string, error)` method.
type APIKeyResolver interface {
	APIKey(ctx context.Context) (string, error)
}

// AuthPolicy is the optional extension some APIKeyResolver
// implementations satisfy. When the resolver also exposes these two
// settings, the requireAPIKey middleware applies the C#
// `AuthenticationRequired = DisabledForLocalAddresses` bypass for
// loopback / LAN / (optionally) CGNAT peers — mirroring
// `Sonarr.Http.Authentication.UiAuthorizationHandler` at
// `src/Sonarr.Http/Authentication/UiAuthorizationHandler.cs:25-41`.
//
// Lightweight test fakes (`staticAPIKey`, etc.) that only implement
// `APIKey` simply never trip the bypass — the type assertion falls
// through and the API-key check stays the sole gate.
//
// `*configfile.Provider` satisfies this interface via its existing
// `AuthenticationRequired` and `TrustCgnatIpAddresses` methods.
type AuthPolicy interface {
	AuthenticationRequired(ctx context.Context) (string, error)
	TrustCgnatIpAddresses(ctx context.Context) (bool, error)
}

// requireAPIKey returns middleware that enforces a valid API key.
// Lookup order mirrors `ApiKeyAuthenticationHandler.ParseApiKey`:
//
//  1. `?apikey=` query parameter (REST) / `?access_token=` (SignalR)
//  2. `X-Api-Key` request header
//  3. `Authorization: Bearer <key>` (any "Bearer " substring removed
//     via `strings.ReplaceAll`, matching C# `string.Replace`).
//
// On miss or mismatch the middleware checks the AuthPolicy bypass
// (DisabledForLocalAddresses + local peer) before falling through to
// an empty-body 401 — C#'s HandleChallengeAsync sets
// `Response.StatusCode = 401` and returns without writing a
// Content-Type or body (`ApiKeyAuthenticationHandler.cs:81-85`).
//
// Comparison uses auth.EqualAPIKey (constant-time, length-safe). An
// empty stored key fails every comparison — protects against an
// uninitialised config file accidentally accepting an empty header.
func (s *Server) requireAPIKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stored, err := s.apiKey.APIKey(r.Context())
		presented := parseAPIKey(r)
		if err == nil && presented != "" && auth.EqualAPIKey(stored, presented) {
			next.ServeHTTP(w, r)
			return
		}
		if err != nil {
			s.logger.Warn("apikey resolve failed", "err", err)
		}
		if policy, ok := s.apiKey.(AuthPolicy); ok && allowLocalBypass(r, policy) {
			next.ServeHTTP(w, r)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	})
}

// parseAPIKey reads the key from the request per C# lookup order.
// Exposed (unexported but in-package) so tests can probe without
// going through the middleware.
//
// The query-parameter name differs by route family, matching the two
// C# auth schemes registered at
// `Sonarr.Http/Authentication/AuthenticationBuilderExtensions.cs:57-66`:
//
//   - REST routes (everything other than /signalr/*) use `?apikey=`.
//   - SignalR routes (path prefix /signalr/) use `?access_token=`
//     because the SignalR JS client can't set custom headers on a
//     WebSocket upgrade.
//
// Each C# scheme is mounted on its own path prefix; cross-prefix
// query names are rejected. The Go port matches by selecting the
// query name based on URL.Path. URL.Path has already been
// strip-prefixed by stripURLBase when WithURLBase is in effect, so
// the comparison is against the post-strip form.
func parseAPIKey(r *http.Request) string {
	queryName := "apikey"
	if strings.HasPrefix(r.URL.Path, "/signalr/") {
		queryName = "access_token"
	}
	if v := r.URL.Query().Get(queryName); v != "" {
		return v
	}
	if v := r.Header.Get("X-Api-Key"); v != "" {
		return v
	}
	if v := r.Header.Get("Authorization"); v != "" {
		// C# `Replace("Bearer ", "")` removes *every* occurrence,
		// not just a prefix. strings.ReplaceAll preserves that
		// semantics; bizarrely-shaped headers degrade the same way
		// in both runtimes.
		return strings.ReplaceAll(v, "Bearer ", "")
	}
	return ""
}

// allowLocalBypass replicates the C# UiAuthorizationHandler decision:
// AuthenticationRequired must be DisabledForLocalAddresses, and the
// peer IP must be a local address (loopback / IPv4 private / IPv6
// link-local et al.) — or, when TrustCgnatIpAddresses is set, an
// IPv4 CGNAT (100.64.0.0/10) address.
//
// Returns false if the AuthenticationRequired lookup errors, if the
// remote address can't be parsed, or if the mode is anything other
// than DisabledForLocalAddresses (the default "Enabled" never
// bypasses).
func allowLocalBypass(r *http.Request, policy AuthPolicy) bool {
	ctx := r.Context()
	mode, err := policy.AuthenticationRequired(ctx)
	if err != nil || !strings.EqualFold(strings.TrimSpace(mode), "DisabledForLocalAddresses") {
		return false
	}
	ip := remoteIP(r)
	if ip == nil {
		return false
	}
	if isLocalAddress(ip) {
		return true
	}
	if trust, err := policy.TrustCgnatIpAddresses(ctx); err == nil && trust && isCgnatAddress(ip) {
		return true
	}
	return false
}
