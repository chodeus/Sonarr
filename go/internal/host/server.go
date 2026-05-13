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
		Handler:           mux,
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
func (s *Server) Handler() http.Handler {
	return s.buildMux()
}

func (s *Server) buildMux() *http.ServeMux {
	mux := http.NewServeMux()
	ping := s.pingHandler()
	// C# `PingController.GetStatus` binds both `[HttpGet("/ping")]`
	// and `[HttpHead("/ping")]`; ServeMux requires explicit
	// per-method registration. /ping is [AllowAnonymous] in C# —
	// no requireAPIKey wrapping.
	mux.Handle("GET /ping", ping)
	mux.Handle("HEAD /ping", ping)

	// /api: C# does not declare [AllowAnonymous] on
	// ApiInfoController, so the Startup fallback policy enforces
	// API-key auth. Wrap explicitly here.
	mux.Handle("GET /api", s.requireAPIKey(s.apiInfoHandler()))

	for _, r := range s.routes {
		h := r.Handler
		if !r.Anonymous {
			h = s.requireAPIKey(h)
		}
		pattern := r.Path
		if r.Method != "" {
			pattern = r.Method + " " + r.Path
		}
		mux.Handle(pattern, h)
	}
	return mux
}
