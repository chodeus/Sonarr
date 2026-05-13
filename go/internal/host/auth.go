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

// requireAPIKey returns middleware that enforces a valid API key.
// Lookup order mirrors `ApiKeyAuthenticationHandler.ParseApiKey`:
//
//  1. `?apikey=` query parameter
//  2. `X-Api-Key` request header
//  3. `Authorization: Bearer <key>` (any "Bearer " substring removed
//     via `strings.ReplaceAll`, matching C# `string.Replace`).
//
// On miss or mismatch the response is an empty-body 401 — C#'s
// HandleChallengeAsync sets `Response.StatusCode = 401` and returns
// without writing a Content-Type or body
// (`ApiKeyAuthenticationHandler.cs:81-85`).
//
// Comparison uses auth.EqualAPIKey (constant-time, length-safe). An
// empty stored key fails every comparison — protects against an
// uninitialised config file accidentally accepting an empty header.
func (s *Server) requireAPIKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stored, err := s.apiKey.APIKey(r.Context())
		if err != nil {
			s.logger.Warn("apikey resolve failed", "err", err)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		presented := parseAPIKey(r)
		if presented == "" || !auth.EqualAPIKey(stored, presented) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// parseAPIKey reads the key from the request per C# lookup order.
// Exposed (unexported but in-package) so tests can probe without
// going through the middleware.
func parseAPIKey(r *http.Request) string {
	if v := r.URL.Query().Get("apikey"); v != "" {
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
