package v3

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"sonarr.tv/sonarr/internal/httperr"
)

// SystemLifecycleRunner is the slice of *lifecycle.Service the
// shutdown/restart HTTP handlers reach for. Defined locally so tests
// can stub the publish without standing up a real Bus.
//
// Mirrors C# ILifecycleService (Shutdown / Restart) at
// `src/NzbDrone.Core/Lifecycle/LifecycleService.cs:1-53`.
type SystemLifecycleRunner interface {
	Shutdown(ctx context.Context) error
	Restart(ctx context.Context) error
}

// SystemShutdownHandler returns POST /api/v3/system/shutdown.
// Mirrors `SystemController.Shutdown` at `:116-121`:
//
//	Task.Factory.StartNew(() => _lifecycleService.Shutdown());
//	return new { ShuttingDown = true };
//
// The C# action dispatches Shutdown off-thread so the HTTP response
// is sent before the lifecycle propagation reaches the host's
// shutdown chain. The Go port preserves that by goroutine-dispatching
// the Shutdown call AFTER writing the response (the response write
// blocks the goroutine's start until the body is flushed). The
// messaging.Bus publish itself is async so the goroutine returns
// quickly.
func SystemShutdownHandler(svc SystemLifecycleRunner) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(struct {
			ShuttingDown bool `json:"shuttingDown"`
		}{ShuttingDown: true})
		_, _ = w.Write(buf)
		log := httperr.LoggerFromRequest(r)
		if log == nil {
			log = slog.Default()
		}
		go func() {
			if err := svc.Shutdown(context.Background()); err != nil {
				log.Error("system/shutdown", "err", err)
			}
		}()
	})
}

// SystemRoutesDuplicateHandler returns GET /api/v3/system/routes/duplicate.
// Mirrors `SystemController.DuplicateRoutes` at `:109-114` which
// returns the C# `DuplicateEndpointDetector.GetDuplicateEndpoints`
// output - an empty array unless the ASP.NET route table actually
// contains conflicts. The Go-port route table is statically declared
// in cmd/sonarr/host_wiring.go's v3Routes list + verified at boot, so
// duplicates can't survive registration - this handler emits `[]`
// unconditionally matching the steady-state shape.
func SystemRoutesDuplicateHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	})
}

// SystemRoutesHandler returns GET /api/v3/system/routes. Mirrors
// `SystemController.GetRoutes` at `:97-107` which writes the ASP.NET
// DFA endpoint graph to a StringWriter and returns it as text/plain.
// The Go-port has no DfaGraphWriter equivalent, so this emits empty
// text/plain to pin the wire surface — same approach as the v5
// RoutesHandler (internal/api/v5/system/system.go:203).
func SystemRoutesHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
	})
}

// SystemRestartHandler returns POST /api/v3/system/restart. Mirrors
// `SystemController.Restart` at `:123-128`. Same fire-and-forget
// pattern as Shutdown — the Restart() call sets RestartPending=true
// then publishes ApplicationShutdownRequested{Restarting:true}, which
// the host's shutdown sequence reads to decide whether to chain an
// updater process.
func SystemRestartHandler(svc SystemLifecycleRunner) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(struct {
			Restarting bool `json:"restarting"`
		}{Restarting: true})
		_, _ = w.Write(buf)
		log := httperr.LoggerFromRequest(r)
		if log == nil {
			log = slog.Default()
		}
		go func() {
			if err := svc.Restart(context.Background()); err != nil {
				log.Error("system/restart", "err", err)
			}
		}()
	})
}
