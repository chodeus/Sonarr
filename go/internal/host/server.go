// Package host owns the HTTP server that fronts the Go port's REST API,
// SignalR hub, and static frontend bundle. Phase 5 builds this surface
// incrementally; the first slice ships the listener shell and the
// `/ping` endpoint so the contract-test replayer has a base URL to
// point at.
//
// C# parity: combines the wiring that lives in
// `src/NzbDrone.Host/Bootstrap.cs:CreateConsoleHostBuilder`
// (URL build + Kestrel) and `src/NzbDrone.Host/Startup.cs:Configure`
// (route registration). The Go port collapses Kestrel + Startup into a
// single `*http.Server` because there is no DryIoc/ASP.NET separation
// between hosting and routing.
//
// The router is stdlib `net/http.ServeMux`; Go 1.22+ supports
// method-scoped patterns (`"GET /ping"`) which covers Phase 5's first
// few slices. Chi (the originally planned router) is deferred until
// middleware composition demands it — adopting a new dependency is
// itself a go.mod gate.
package host

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Config carries the listener parameters resolved from the
// `configfile.Provider`. The host package does not depend on the
// provider directly so it stays testable without a config XML.
//
// Mirrors the values read in `Bootstrap.cs:144-153`:
// `BindAddress` (default "*") + `Port` (default 8989) are combined
// into a `scheme://host:port` URL. The Go port honours the same
// "*" → wildcard convention by translating to an empty host so
// `net.JoinHostPort` produces ":8989", which makes the listener
// bind to all interfaces.
type Config struct {
	BindAddress string
	Port        int
}

// Listen returns the host:port pair the *http.Server binds to. The
// C# code builds `http://*:8989` and hands it to Kestrel, which then
// strips the scheme + maps `*` to all-interfaces internally. Go's
// `net.Listen("tcp", addr)` is the equivalent layer; an empty host
// means all-interfaces.
func (c Config) Listen() string {
	host := strings.TrimSpace(c.BindAddress)
	if host == "*" || host == "" {
		host = ""
	}
	return net.JoinHostPort(host, strconv.Itoa(c.Port))
}

// Route is a single mountable endpoint contributed by a controller
// package. Anonymous routes bypass the requireAPIKey middleware;
// every C# controller without `[AllowAnonymous]` should leave it
// false so the auth wrap mirrors the Startup FallbackPolicy.
//
// Pattern follows `net/http.ServeMux` semantics — method prefix is
// optional (`"GET /api/v3/language"`) and path wildcards (`"{id}"`)
// are supported on Go 1.22+. Keep the Method field explicit even
// when it duplicates the prefix; downstream tooling (the future
// `/api/v3/system/routes` controller) reads it without parsing the
// pattern.
type Route struct {
	Method    string
	Path      string
	Handler   http.Handler
	Anonymous bool
}

// Server is the HTTP host. Construct via New and drive via Run.
// Run blocks until the caller's context is cancelled, then drains
// in-flight requests with shutdownTimeout before returning.
type Server struct {
	cfg    Config
	logger *slog.Logger
	probe  ConfigProbe
	apiKey APIKeyResolver
	routes []Route
	now    func() time.Time

	shutdownTimeout time.Duration

	// staticDir is the optional frontend-bundle root mounted under
	// "/" after the per-method API routes. Empty means no static
	// surface — non-API paths get 404. Set via WithStaticDir.
	staticDir string

	// urlBase is the optional sub-path the server runs under
	// (e.g. "/sonarr"). When non-empty, every incoming request's
	// URL.Path has this prefix stripped BEFORE dispatching, and
	// requests outside the prefix 404. Set via WithURLBase.
	urlBase string

	// staticFS is the optional fs.FS-backed frontend-bundle root.
	// Mutually-exclusive with staticDir at boot — set ONE, not
	// both. Used for single-binary distribution via go:embed.
	staticFS fs.FS

	// theme is the configured UI theme value ("auto" / "light" /
	// "dark") that the J205 HTML template substitution replaces the
	// `_THEME_` token with. Empty defaults to "auto" at substitution
	// time so the index.html's downstream prefers-color-scheme
	// detection runs.
	theme string

	// listenerOverride lets tests inject a pre-bound listener so
	// they can run against a randomly assigned port. nil means
	// `net.Listen("tcp", cfg.Listen())` is called inside Run.
	listenerOverride net.Listener
}

// Option mutates a *Server after construction. Currently used by
// tests to inject a listener and to override the clock for cache TTL
// checks once the ping cache lands in a later slice.
type Option func(*Server)

// WithListener injects a pre-bound listener. Used by tests that need
// a deterministic port assignment.
func WithListener(l net.Listener) Option {
	return func(s *Server) { s.listenerOverride = l }
}

// WithClock replaces the time source. Reserved for later slices that
// add response caches with TTLs.
func WithClock(now func() time.Time) Option {
	return func(s *Server) { s.now = now }
}

// WithShutdownTimeout overrides the graceful-drain budget. Default
// is 5 seconds, matching the implicit Kestrel default.
func WithShutdownTimeout(d time.Duration) Option {
	return func(s *Server) { s.shutdownTimeout = d }
}

// WithRoutes registers additional routes. Called once at boot from
// the cmd/sonarr wiring layer; the host package owns no Route values
// itself so each per-controller package is fully decoupled.
func WithRoutes(routes ...Route) Option {
	return func(s *Server) { s.routes = append(s.routes, routes...) }
}

// WithTheme pins the UI theme value injected into the HTML bundle's
// `_THEME_` placeholder by the J205 template-substitution middleware.
// Empty defaults to "auto" at substitution time (the React bundle
// then resolves to light/dark via the browser's prefers-color-scheme
// at render). Set from configfile.Provider.Theme() at boot.
func WithTheme(theme string) Option {
	return func(s *Server) { s.theme = theme }
}

// WithURLBase pins the sub-path the host runs under. Mirrors
// `ConfigFile.UrlBase` (e.g. `/sonarr` to expose the UI at
// `https://host/sonarr/`). The value is normalised the same way
// configfile.Provider.URLBase normalises the stored XML value:
// trim trailing slashes, ensure leading slash. Empty disables
// sub-path serving and runs at root.
//
// Implementation: a thin middleware in buildMux strips the
// configured prefix from every incoming request's URL.Path
// BEFORE the ServeMux dispatches — so route registrations stay
// prefix-free (`/api/v3/...`, not `/<base>/api/v3/...`). Requests
// that don't carry the prefix return 404 (matches C# behaviour at
// `Startup.cs` where the host's UsePathBase rejects out-of-base
// requests once `UrlBase` is set).
func WithURLBase(base string) Option {
	return func(s *Server) { s.urlBase = normaliseURLBase(base) }
}

// normaliseURLBase mirrors configfile.Provider.URLBase normalisation
// — trim slashes from both ends, prepend a single leading slash.
// Empty / "/" → "" (no prefix).
func normaliseURLBase(b string) string {
	b = strings.Trim(b, "/")
	if b == "" {
		return ""
	}
	return "/" + b
}

// WithStaticFS mounts an fs.FS as the frontend bundle root. The
// fs.FS argument MUST contain index.html at its root (typically
// obtained via fs.Sub on a //go:embed all:<path>/build directive).
// Mutually-exclusive with WithStaticDir at boot — if both are set,
// WithStaticDir wins (the disk-backed mount is more useful for
// development iteration; go:embed is for production binaries).
//
// Use case: single-binary distribution. cmd/sonarr can compile the
// React build into the executable + mount it without a runtime
// --frontenddir flag.
func WithStaticFS(filesystem fs.FS) Option {
	return func(s *Server) { s.staticFS = filesystem }
}

// WithStaticDir mounts a directory as the frontend bundle root.
// The handler serves files under the path; missing files fall back
// to <root>/index.html so the SPA's client-side router can resolve
// the requested route. Empty path disables static serving (default).
//
// API routes (`/api/...`, `/signalr/...`, `/ping`) are registered
// FIRST in the ServeMux, so they take precedence over the static
// fallback even though the static handler nominally matches every
// path.
func WithStaticDir(root string) Option {
	return func(s *Server) { s.staticDir = root }
}

// New builds a Server. probe is required so /ping has a DB liveness
// signal; mirrors the `IConfigRepository` argument the C#
// PingController takes (`src/Sonarr.Http/Ping/PingController.cs:14`).
// apiKey is required for the authed routes — C#'s Startup fallback
// policy gates every non-[AllowAnonymous] controller, so the Go port
// requires a resolver up front rather than allowing a nil-deny
// fallback that would silently disable auth.
func New(cfg Config, logger *slog.Logger, probe ConfigProbe, apiKey APIKeyResolver, opts ...Option) *Server {
	s := &Server{
		cfg:             cfg,
		logger:          logger,
		probe:           probe,
		apiKey:          apiKey,
		now:             time.Now,
		shutdownTimeout: 5 * time.Second,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Run binds the listener, serves HTTP until ctx is cancelled, then
// gracefully shuts down. The drain phase uses a fresh context with
// shutdownTimeout — the caller's ctx is typically already cancelled
// by the time Run sees it, which would short-circuit Shutdown.
//
// Returns ctx.Err() on clean shutdown; a wrapped error if the
// listener or Shutdown itself fails. http.ErrServerClosed from Serve
// is expected on shutdown and is not propagated.
func (s *Server) Run(ctx context.Context) error {
	mux := s.buildMux()

	srv := &http.Server{
		Handler:           lowercaseAPIPath(mux),
		ReadHeaderTimeout: 30 * time.Second,
	}

	listener := s.listenerOverride
	if listener == nil {
		l, err := net.Listen("tcp", s.cfg.Listen())
		if err != nil {
			return fmt.Errorf("host listen %s: %w", s.cfg.Listen(), err)
		}
		listener = l
	}

	s.logger.Info("host listening", "addr", listener.Addr().String())

	serveErr := make(chan error, 1)
	go func() {
		err := srv.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveErr <- err
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			s.logger.Warn("host shutdown timed out", "err", err)
			return fmt.Errorf("host shutdown: %w", err)
		}
		<-serveErr
		return ctx.Err()
	case err := <-serveErr:
		if err != nil {
			return fmt.Errorf("host serve: %w", err)
		}
		return nil
	}
}

// Handler returns the http.Handler the listener serves. Exposed so
// callers that don't want a network listener (notably the contract
// harness's `httptest.Server` wrapper) can plug the same routing
// surface in directly.
//
// When urlBase is set, the mux is wrapped in a strip-prefix
// middleware so route registrations stay prefix-free. Requests
// outside the prefix 404 (matches the C# Kestrel UsePathBase
// semantic).
func (s *Server) Handler() http.Handler {
	h := lowercaseAPIPath(s.buildMux())
	if s.urlBase != "" {
		h = stripURLBase(s.urlBase, h)
	}
	return h
}

// stripURLBase is the middleware WithURLBase installs. Strips the
// prefix from r.URL.Path (and RawPath when set so encoded segments
// pass through cleanly), then delegates to next. Requests outside
// the prefix return 404 without invoking next.
func stripURLBase(prefix string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case p == prefix:
			// Bare prefix → SPA root.
			r2 := *r
			u := *r.URL
			u.Path = "/"
			u.RawPath = ""
			r2.URL = &u
			next.ServeHTTP(w, &r2)
			return
		case strings.HasPrefix(p, prefix+"/"):
			r2 := *r
			u := *r.URL
			u.Path = strings.TrimPrefix(p, prefix)
			if u.RawPath != "" {
				u.RawPath = strings.TrimPrefix(u.RawPath, prefix)
			}
			r2.URL = &u
			next.ServeHTTP(w, &r2)
		default:
			http.NotFound(w, r)
		}
	})
}

// lowercaseAPIPath makes API route matching case-insensitive, mirroring
// the ASP.NET Core routing the React frontend was written against. Go's
// net/http.ServeMux matches paths case-sensitively, so the camelCase
// URLs the UI issues — /api/v3/customFilter, /rootFolder, /episodeFile,
// /autoTagging, /indexerFlag (and their /api/v5 forms) — would 404
// against the lowercase patterns the routes register under, breaking the
// UI's bootstrap (it renders "Failed to load custom filters from API").
//
// Every Sonarr API route literal is lowercase and every path parameter
// is a numeric id, so lowercasing the whole /api/ path matches the same
// route while being a no-op on already-correct requests. Two things are
// deliberately left untouched, where case is load-bearing:
//
//   - The mediacover subtree (/api/vN/mediacover/...) serves on-disk
//     filenames; skipped so a case-sensitive filename round-trips intact.
//   - Non-/api/ paths — the SPA shell and webpack chunk filenames such as
//     frontend_src_Components_Page_Header_fuse_worker_ts.js contain
//     capitals and must reach the static handler verbatim.
//
// Query strings live in r.URL.RawQuery (not Path), so case-sensitive
// query params like ?path=/Foo are unaffected.
func lowercaseAPIPath(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lower := strings.ToLower(r.URL.Path)
		if lower != r.URL.Path &&
			strings.HasPrefix(lower, "/api/") &&
			!strings.Contains(lower, "/mediacover/") {
			r2 := *r
			u := *r.URL
			u.Path = lower
			u.RawPath = "" // Path is canonical now; drop the cased raw form.
			r2.URL = &u
			next.ServeHTTP(w, &r2)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) buildMux() *http.ServeMux {
	mux := http.NewServeMux()
	ping := s.pingHandler()
	// C# `PingController.GetStatus` binds both `[HttpGet("/ping")]`
	// and `[HttpHead("/ping")]`; ServeMux requires explicit
	// per-method registration. /ping is [AllowAnonymous] in C# —
	// no requireAPIKey wrapping.
	mux.Handle("GET /ping", disableCacheHeaders(ping))
	mux.Handle("HEAD /ping", disableCacheHeaders(ping))

	// /api: C# does not declare [AllowAnonymous] on
	// ApiInfoController, so the Startup fallback policy enforces
	// API-key auth. Wrap explicitly here.
	mux.Handle("GET /api", disableCacheHeaders(s.requireAPIKey(s.apiInfoHandler())))

	for _, r := range s.routes {
		h := r.Handler
		if !r.Anonymous {
			h = s.requireAPIKey(h)
		}
		// Inject the server's *slog.Logger into the request context
		// so handlers calling `httperr.WriteErr(..., nil)` can fetch
		// it via `httperr.LoggerFromRequest` and log 500-class errors
		// at slog.Error. Closes the "zero handler-side logging"
		// observability gap surfaced by the Phase 5 audit. Wraps
		// every route uniformly (including anonymous ones) — handlers
		// gain logging access independent of whether they're behind
		// requireAPIKey.
		h = injectLogger(s.logger, h)
		// Audit fix C.1: emit the four "do not cache" headers C#
		// Sonarr applies via `CacheHeaderMiddleware` on every API
		// response. MediaCover routes skip this so the React UI can
		// long-cache series posters (matches C# `CacheableSpecification`
		// EnableCache behaviour on that prefix).
		if !shouldSkipCacheHeaders(r.Path) {
			h = disableCacheHeaders(h)
		}
		pattern := r.Path
		if r.Method != "" {
			pattern = r.Method + " " + r.Path
		}
		mux.Handle(pattern, h)
	}

	// Static frontend bundle mounted under "/". Registered LAST so
	// the API routes above take precedence on path collisions.
	// Anonymous — the frontend bundle is the entry point users hit
	// before they've supplied an API key; auth gates the API
	// surface, not the HTML/CSS/JS shell.
	//
	// Precedence: WithStaticDir wins over WithStaticFS when both
	// are set (disk-backed mount is the development-iteration path
	// and should take priority over the embedded production
	// fallback).
	// Theme default: empty stored value falls back to "auto" so the
	// React bundle's downstream prefers-color-scheme detection runs.
	// Matches C# `IConfigFileProvider.Theme` default (auto).
	theme := s.theme
	if theme == "" {
		theme = "auto"
	}
	// Middleware chain (innermost first): staticHandler →
	// injectHTMLTemplates → injectBaseHref. Order matters:
	// injectHTMLTemplates replaces `__URL_BASE__` with the configured
	// urlBase string and `_THEME_` with the theme value; injectBaseHref
	// then rewrites the `<base href>` tag for client-side routing
	// prefixes. Running templates first keeps the base-href rewrite
	// operating on the post-substitution HTML.
	switch {
	case s.staticDir != "":
		static, err := staticHandler(s.staticDir)
		if err != nil {
			s.logger.Error("static handler init failed", "err", err, "dir", s.staticDir)
		} else {
			// Audit fix C.5 + J205: chain template substitution before
			// the base-href rewrite. No-op on non-text/html responses.
			mux.Handle("/", injectBaseHref(injectHTMLTemplates(static, s.urlBase, theme), s.urlBase))
		}
	case s.staticFS != nil:
		mux.Handle("/", injectBaseHref(injectHTMLTemplates(staticHandlerFS(s.staticFS), s.urlBase, theme), s.urlBase))
	}
	return mux
}
