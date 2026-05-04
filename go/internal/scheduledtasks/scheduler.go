// Scheduler ticker. Ports
// `NzbDrone.Core.Jobs.Scheduler` at
// `src/NzbDrone.Core/Jobs/Scheduler.cs:1-71`.
//
// The C# Scheduler is a 30-second `System.Timers.Timer` that polls
// `TaskManager.GetPending` and pushes every due task into the command
// queue with `CommandTrigger.Scheduled`. The Go port collapses the
// timer into a `time.Ticker` driving [Scheduler.Tick] on each tick.
//
// What this slice (Phase 3.12) ships:
//
//   - Scheduler struct + NewScheduler.
//   - Tick(ctx) — one polling pass; public so tests can drive it
//     deterministically without sleeping for the ticker.
//   - Run(ctx) — ticker loop until ctx cancellation. Returns nil on
//     clean shutdown; tick errors are logged but never break the loop.
//   - Configurable poll interval (default 30s, matches `Scheduler.cs:57`).
//
// What's deferred:
//
//   - Per-task body. The C# Scheduler push goes through the reflection
//     overload `Push(string, DateTime?, DateTime?, ...)` which
//     instantiates a default Command subclass and stamps
//     LastExecutionTime + LastStartTime onto it. The Go port pushes
//     `"{}"` for now — most scheduled commands don't read those
//     fields. The first handler that does (likely BackupCommand,
//     which logs the last-backup timestamp) drives the body shape.
//   - Per-task priority. The C# IScheduledTask carries a Priority
//     property hardcoded per implementation; the Go-port Task struct
//     doesn't persist it (the C# value isn't persisted either —
//     `analysis/subsystems/system.md` documents PriorityLow as the
//     universal default for the 11 scheduled commands). The Go port
//     pushes at PriorityLow; a host-supplied per-task override is a
//     trivial future extension when a task needs different priority.

package scheduledtasks

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"sonarr.tv/sonarr/internal/commands"
)

// DefaultSchedulerInterval matches `Scheduler.cs:57` (`Timer.Interval =
// 1000 * 30`). Override via [Scheduler.SetInterval] in tests.
const DefaultSchedulerInterval = 30 * time.Second

// Scheduler bridges scheduledtasks.Service.GetPending into
// commands.Service.Push. One Scheduler per process; the host calls
// [Scheduler.Run] on its own goroutine.
type Scheduler struct {
	tasks    *Service
	commands *commands.Service
	log      *slog.Logger
	interval time.Duration
}

// NewScheduler builds a Scheduler. tasks and cmds must be non-nil;
// log defaults to slog.Default when nil. The poll interval defaults
// to [DefaultSchedulerInterval]; override via [Scheduler.SetInterval].
func NewScheduler(tasks *Service, cmds *commands.Service, log *slog.Logger) (*Scheduler, error) {
	if tasks == nil {
		return nil, errors.New("scheduledtasks: NewScheduler: nil task service")
	}
	if cmds == nil {
		return nil, errors.New("scheduledtasks: NewScheduler: nil command service")
	}
	if log == nil {
		log = slog.Default()
	}
	return &Scheduler{
		tasks:    tasks,
		commands: cmds,
		log:      log,
		interval: DefaultSchedulerInterval,
	}, nil
}

// SetInterval overrides the ticker interval used by [Scheduler.Run].
// Must be > 0; ignored otherwise. Tests typically set a small value
// or drive [Scheduler.Tick] directly.
func (s *Scheduler) SetInterval(d time.Duration) {
	if d <= 0 {
		return
	}
	s.interval = d
}

// Tick runs one polling pass: read GetPending, push each due task
// onto the command queue with TriggerScheduled. Returns the number
// of pushes attempted plus any push that errored — the C# Scheduler
// catches and logs per-iteration errors so a single bad task can't
// stop the loop; the Go port follows the same pattern but surfaces
// the count for observability.
func (s *Scheduler) Tick(ctx context.Context) (pushed int, err error) {
	pending := s.tasks.GetPending(ctx)
	if len(pending) == 0 {
		return 0, nil
	}
	s.log.LogAttrs(ctx, slog.LevelDebug, "scheduledtasks: pending tasks",
		slog.Int("count", len(pending)))
	var firstErr error
	for _, t := range pending {
		_, perr := s.commands.Push(ctx, t.TypeName, "{}", commands.PriorityLow, commands.TriggerScheduled)
		if perr != nil {
			s.log.LogAttrs(ctx, slog.LevelError, "scheduledtasks: push failed",
				slog.String("typeName", t.TypeName),
				slog.Any("err", perr))
			if firstErr == nil {
				firstErr = perr
			}
			continue
		}
		pushed++
	}
	return pushed, firstErr
}

// Run drives [Scheduler.Tick] on a [time.Ticker] until ctx is
// cancelled. Returns nil on clean shutdown. Per-tick errors are
// logged but never returned — matches the C# `LogExceptions()` shape
// at `Scheduler.cs:59`.
func (s *Scheduler) Run(ctx context.Context) error {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if _, err := s.Tick(ctx); err != nil {
				// Already logged inside Tick; swallow so the loop
				// keeps polling. The C# tree behaves the same way
				// via Task.LogExceptions().
				_ = err
			}
		}
	}
}
