// Package housekeeping is the Go port of NzbDrone.Core.Housekeeping.
// It hosts:
//
//   - The Task interface (the C# IHousekeepingTask shape).
//   - Service.Clean — iterates every registered Task with per-task
//     error swallowing so a single bad task can't halt the pass.
//   - Concrete Task implementations under this package or sibling
//     packages (large ones may live next to their target tables).
//
// What this slice (Phase 3.16) ships:
//
//   - Service struct + NewService (variadic []Task, log injectable).
//   - Service.Clean(ctx) error — runs every task in registration
//     order, logs per-task failures, returns the first error so
//     callers can surface a non-fatal warning. The C# version returns
//     void; the Go port returns the first error for testability and
//     because the dispatcher's command handler shape demands `error`.
//   - FixFutureRunScheduledTasks — the simplest housekeeper, ports
//     `src/NzbDrone.Core/Housekeeping/Housekeepers/FixFutureRunScheduledTasks.cs:1-35`.
//     Direct UPDATE on ScheduledTasks rows whose LastExecution is in
//     the future (clock skew / clock-rollback). Lives in this package
//     because the entire C# tree colocates housekeepers under
//     `Housekeepers/` and the scheduledtasks package shouldn't accrete
//     cross-domain logic.
//
// What's deferred:
//
//   - The remaining ~25 C# housekeepers. Each is a one-screen SQL
//     statement; ports as a separate slice once the Task slot it
//     targets is genuinely needed at runtime. Sequencing favors the
//     ones that touch tables already ported (CleanupOrphanedEpisodes,
//     CleanupOrphanedEpisodeFiles, CleanupOrphanedHistoryItems, etc.)
//     before the ones that need new infrastructure
//     (CleanupAdditionalNamingSpecs, TrimLogDatabase).
//   - BuildInfo.IsDebug short-circuit — the C# FixFutureRunScheduledTasks
//     has a debug-mode log line at `:22-25` then continues to run the
//     UPDATE anyway, so it's effectively cosmetic. Omitted (the Go
//     port will surface a slog log line at the call site if debug
//     mode is later wired).
package housekeeping

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// Task ports `NzbDrone.Core.Housekeeping.IHousekeepingTask` at
// `src/NzbDrone.Core/Housekeeping/IHousekeepingTask.cs`. The C#
// interface has a nullary `void Clean()`; the Go port adds a `ctx`
// for cancellation and an `error` return for logging.
//
// Task.Name is used in log lines and in the per-task error wrapper.
// Mirrors the C# `task.GetType().Name` reflection at
// `HousekeepingService.cs:30`.
type Task interface {
	Name() string
	Clean(ctx context.Context) error
}

// Service is the housekeeping orchestrator. Ports
// `NzbDrone.Core.Housekeeping.HousekeepingService` at
// `HousekeepingService.cs:1-50`.
type Service struct {
	tasks []Task
	log   *slog.Logger
}

// NewService builds a Service with the supplied task list. Tasks run
// in the order supplied. Nil tasks are rejected at construction so
// runtime can assume a clean list. log defaults to slog.Default.
func NewService(tasks []Task, log *slog.Logger) (*Service, error) {
	for i, t := range tasks {
		if t == nil {
			return nil, fmt.Errorf("housekeeping: NewService: nil task at idx %d", i)
		}
	}
	if log == nil {
		log = slog.Default()
	}
	cp := make([]Task, len(tasks))
	copy(cp, tasks)
	return &Service{tasks: cp, log: log}, nil
}

// Clean iterates every registered Task. Errors are logged; the first
// error encountered is returned (after the loop completes) so the
// caller can surface "housekeeping had problems" without losing the
// per-task structure.
//
// Mirrors `HousekeepingService.Clean` at `:22-43` minus the `Vacuum`
// step at the end (the Go port's datastore layer doesn't expose
// VACUUM yet — ports as a separate Task once it does).
func (s *Service) Clean(ctx context.Context) error {
	if len(s.tasks) == 0 {
		s.log.LogAttrs(ctx, slog.LevelDebug, "housekeeping: no tasks registered")
		return nil
	}
	s.log.LogAttrs(ctx, slog.LevelInfo, "housekeeping: running tasks",
		slog.Int("count", len(s.tasks)))
	var first error
	for _, task := range s.tasks {
		s.log.LogAttrs(ctx, slog.LevelDebug, "housekeeping: starting task",
			slog.String("task", task.Name()))
		if err := task.Clean(ctx); err != nil {
			s.log.LogAttrs(ctx, slog.LevelError, "housekeeping: task failed",
				slog.String("task", task.Name()),
				slog.Any("err", err))
			if first == nil {
				first = fmt.Errorf("housekeeping: %s: %w", task.Name(), err)
			}
			continue
		}
		s.log.LogAttrs(ctx, slog.LevelDebug, "housekeeping: completed task",
			slog.String("task", task.Name()))
	}
	if first != nil {
		return first
	}
	return nil
}

// ErrNilDB is returned by the constructor of any Task that takes a
// *datastore.DB when nil is supplied.
var ErrNilDB = errors.New("housekeeping: nil db")
