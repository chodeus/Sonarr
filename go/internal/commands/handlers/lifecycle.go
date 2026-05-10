// Lifecycle command handlers — Shutdown + Restart. Port the
// IExecute<ShutdownCommand> + IExecute<RestartCommand> pair from
// `Lifecycle/LifecycleService.cs:15-44`. As of slice 3.BP (queue [18])
// the cmd/sonarr boot site constructs a *lifecycle.Service and passes
// it via [Deps.Lifecycle]; both handlers delegate to that Service so
// the publish-side semantics match the C# tree (Shutdown publishes
// ApplicationShutdownRequested{Restarting:false}; Restart sets the
// RestartPending flag THEN publishes
// ApplicationShutdownRequested{Restarting:true}).
//
// The C# `ShutdownCommand` + `RestartCommand` bodies have no fields —
// the handler ignores the body string entirely.

package handlers

import (
	"context"
	"errors"
	"log/slog"
)

// ShutdownName / RestartName — the registry keys + Commands.Name +
// ScheduledTasks.TypeName. Mirror the C# short-form `ShutdownCommand`
// → `Shutdown`, `RestartCommand` → `Restart`.
const (
	ShutdownName = "Shutdown"
	RestartName  = "Restart"
)

// LifecycleRunner narrows [*lifecycle.Service] to the two methods
// the Shutdown + Restart handlers reach for. Defined locally so
// tests can stub the publish without standing up a real Bus.
type LifecycleRunner interface {
	Shutdown(ctx context.Context) error
	Restart(ctx context.Context) error
}

// Shutdown is the IExecute<ShutdownCommand> port.
type Shutdown struct {
	svc LifecycleRunner
	log *slog.Logger
}

// NewShutdown builds the handler. svc is required (slice 3.BP+); nil
// returns an error so the boot site fails loudly. log defaults to
// slog.Default.
func NewShutdown(svc LifecycleRunner, log *slog.Logger) (*Shutdown, error) {
	if svc == nil {
		return nil, errors.New("commands.handlers.NewShutdown: nil lifecycle runner")
	}
	if log == nil {
		log = slog.Default()
	}
	return &Shutdown{svc: svc, log: log}, nil
}

// Execute delegates to [LifecycleRunner.Shutdown]. Mirrors C#
// `LifecycleService.Execute(ShutdownCommand)` at `:43` which calls
// `Shutdown()`.
func (h *Shutdown) Execute(ctx context.Context, _ string) error {
	h.log.LogAttrs(ctx, slog.LevelInfo,
		"lifecycle: shutdown requested",
		slog.String("command", ShutdownName))
	return h.svc.Shutdown(ctx)
}

// Restart is the IExecute<RestartCommand> port.
type Restart struct {
	svc LifecycleRunner
	log *slog.Logger
}

// NewRestart builds the handler. svc is required; nil returns an
// error. log defaults to slog.Default.
func NewRestart(svc LifecycleRunner, log *slog.Logger) (*Restart, error) {
	if svc == nil {
		return nil, errors.New("commands.handlers.NewRestart: nil lifecycle runner")
	}
	if log == nil {
		log = slog.Default()
	}
	return &Restart{svc: svc, log: log}, nil
}

// Execute delegates to [LifecycleRunner.Restart]. Mirrors C#
// `LifecycleService.Execute(RestartCommand)` at `:48` which calls
// `Restart()`.
func (h *Restart) Execute(ctx context.Context, _ string) error {
	h.log.LogAttrs(ctx, slog.LevelInfo,
		"lifecycle: restart requested",
		slog.String("command", RestartName))
	return h.svc.Restart(ctx)
}
