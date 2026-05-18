package host

import (
	"log/slog"
	"net/http"

	"sonarr.tv/sonarr/internal/httperr"
)

// injectLogger wraps next in a middleware that stashes logger into
// the request context via [httperr.WithLogger]. Handlers downstream
// retrieve it via [httperr.LoggerFromRequest], or simply pass
// `logger=nil` to [httperr.WriteErr] which will fall back to the
// context-injected logger automatically.
//
// Closes the cross-cutting "zero handler-side logging" gap surfaced
// by the Phase 5 audit. Wrapped at the route registration site in
// [Server.buildMux] so every API route (auth-gated or anonymous)
// gains observability uniformly.
//
// Nil logger short-circuits: the wrap still happens but
// httperr.WithLogger no-ops, so the request flows through with no
// context change. Test handlers that don't want logging continue to
// work — they instantiate a Server with a discard logger or use the
// host.Route directly without buildMux.
func injectLogger(logger *slog.Logger, next http.Handler) http.Handler {
	if next == nil {
		return nil
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, httperr.WithLogger(r, logger))
	})
}
