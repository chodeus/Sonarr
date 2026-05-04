package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"sonarr.tv/sonarr/internal/messaging"
)

// Service ports `Lifecycle/LifecycleService.cs:1-53`. Wraps the
// shutdown / restart event publication with a RestartPending flag
// the host can read at shutdown to decide whether to chain an
// updater process.
//
// The C# class also implements `IExecute<ShutdownCommand>` and
// `IExecute<RestartCommand>` — those Command handlers land in
// Phase 3 once the Command bus is wired. The plain method surface
// here is sufficient for callers that want to invoke shutdown /
// restart directly (HTTP handlers in Phase 5, signal handlers in
// the host).
//
// Surface:
//
//   - Shutdown(ctx) — publishes ApplicationShutdownRequested{Restarting: false}.
//   - Restart(ctx) — sets RestartPending=true, then publishes
//     ApplicationShutdownRequested{Restarting: true}.
//   - RestartPending() bool — read by the host at shutdown to know
//     whether to launch an updater. Mirrors C# `_runtimeInfo.RestartPending`.
type Service struct {
	bus *messaging.Bus

	// restartPending mirrors the C# IRuntimeInfo.RestartPending bool,
	// which the C# Restart() handler sets to true before publishing
	// the shutdown event. The host's shutdown sequence reads this to
	// decide whether to chain an updater process.
	restartPending atomic.Bool
}

// NewService binds a Bus into a Lifecycle Service. The Bus must be
// non-nil — Shutdown/Restart cannot do their work without one (they'd
// be no-ops, which would silently break the host's shutdown chain).
func NewService(bus *messaging.Bus) (*Service, error) {
	if bus == nil {
		return nil, errors.New("lifecycle: nil bus")
	}
	return &Service{bus: bus}, nil
}

// Shutdown ports `LifecycleService.Shutdown()` at `:30-34`. Publishes
// ApplicationShutdownRequested with Restarting=false.
//
// The publish error is propagated; the host typically calls this
// from a signal handler and ignores ErrShutdown (the bus may already
// be in shutdown if a prior signal triggered it).
func (s *Service) Shutdown(ctx context.Context) error {
	if err := messaging.Publish(ctx, s.bus, ApplicationShutdownRequested{Restarting: false}); err != nil {
		return fmt.Errorf("lifecycle: shutdown: %w", err)
	}
	return nil
}

// Restart ports `LifecycleService.Restart()` at `:36-41`. Sets
// RestartPending=true (mirrors C# `_runtimeInfo.RestartPending = true`)
// then publishes ApplicationShutdownRequested with Restarting=true.
//
// Order matters: the flag is set BEFORE the publish so subscribers
// that immediately query RestartPending see the correct value.
func (s *Service) Restart(ctx context.Context) error {
	s.restartPending.Store(true)
	if err := messaging.Publish(ctx, s.bus, ApplicationShutdownRequested{Restarting: true}); err != nil {
		return fmt.Errorf("lifecycle: restart: %w", err)
	}
	return nil
}

// RestartPending reports whether Restart has been invoked. The host
// reads this at shutdown completion to decide whether to launch the
// updater process.
func (s *Service) RestartPending() bool {
	return s.restartPending.Load()
}
