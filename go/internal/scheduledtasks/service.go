// Service for the ScheduledTasks domain. Mirrors
// `NzbDrone.Core.Jobs.TaskManager` at
// `src/NzbDrone.Core/Jobs/TaskManager.cs`.
//
// What this slice (Phase 3.10) ships:
//
//   - Service struct + NewService — repo + optional bus + injectable
//     clock + RWMutex-protected map[string]*Task cache (matches C#
//     `ICached<ScheduledTask>` at `TaskManager.cs:36`).
//   - RegisterDefaults(ctx, defaults) — ports
//     `Handle(ApplicationStartedEvent)` at `:65-166`. Reconciles the
//     persisted ScheduledTasks rows against the supplied default
//     list, deleting orphans, upserting defaults, and populating the
//     cache. New rows get LastExecution = now (matches C# `:158`).
//   - GetPending(ctx) — `:46-51` cache filter on (Interval > 0 AND
//     LastExecution + Interval < now).
//   - GetAll(ctx) — `:53-56` cache values copy.
//   - GetNextExecution(ctx, typeName) — `:58-63` cache lookup; returns
//     ErrNotFound when typeName isn't registered.
//   - HandleCommandExecuted(ctx, ev) — ports `Handle(CommandExecutedEvent)`
//     at `:202-220`. Looks up the scheduled task by Command.Name, and
//     when a host-supplied predicate (or the default — every
//     registered TypeName updates on execution) returns true, calls
//     `repo.SetLastExecutionTime` and refreshes the cache entry.
//
// What's deferred:
//
//   - The 11 hardcoded default tasks at `:67-135` (RefreshMonitoredDownloads
//     / MessagingCleanup / ApplicationUpdateCheck / UpdateSceneMapping /
//     CheckHealth / RefreshSeries / Housekeeping / CleanUpRecycleBin /
//     ImportListSync / Backup / RssSync). Most of those commands don't
//     exist in the Go port yet; the host wires whichever subset has
//     shipped via Service.RegisterDefaults at boot.
//   - GetBackupInterval / GetRssSyncInterval — depend on ConfigService
//     (Phase 4 territory).
//   - HandleAsync(ConfigSavedEvent) — depends on the two interval
//     getters; ports alongside them.
//   - The C# `UpdateScheduledTask` boolean property on Command bodies
//     is replaced by a per-task `ShouldUpdateOnExecuted` predicate
//     the host registers via `Service.SetShouldUpdatePredicate`. The
//     default (no predicate set) treats every registered TypeName as
//     update-on-executed, which matches the C# default for the 11
//     scheduled commands (all set `UpdateScheduledTask = true`).

package scheduledtasks

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"sonarr.tv/sonarr/internal/commands"
)

// Service wraps the Repo with the higher-level surface the rest of
// the app consumes.
type Service struct {
	repo *Repo
	now  func() time.Time

	mu           sync.RWMutex
	cache        map[string]*Task
	shouldUpdate func(typeName string) bool
}

// NewService builds a Service. The cache is empty until
// [Service.RegisterDefaults] is called at boot.
func NewService(r *Repo) (*Service, error) {
	if r == nil {
		return nil, errors.New("scheduledtasks: nil repo")
	}
	return &Service{
		repo:  r,
		now:   func() time.Time { return time.Now().UTC() },
		cache: map[string]*Task{},
	}, nil
}

// SetClock overrides the clock used for "is this task due" computations
// and for stamping LastExecution on freshly-inserted defaults. Tests
// pin a fixed-time func for determinism.
func (s *Service) SetClock(f func() time.Time) {
	if f == nil {
		return
	}
	s.now = f
}

// SetShouldUpdatePredicate registers a host-supplied filter that
// gates `HandleCommandExecuted`'s LastExecution write. When unset
// (the default), every registered TypeName is treated as
// update-on-executed — matching the C# default for the 11 scheduled
// commands.
func (s *Service) SetShouldUpdatePredicate(f func(typeName string) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.shouldUpdate = f
}

// RegisterDefaults reconciles the persisted ScheduledTasks rows
// against the supplied default list and populates the cache. Ports
// `TaskManager.Handle(ApplicationStartedEvent)` at `:65-166`.
//
// Algorithm (mirrors the C# loop at `:135-165`):
//
//  1. Read every row currently persisted.
//  2. For each persisted row whose TypeName isn't in `defaults`,
//     delete it (C# `:140-147`).
//  3. For each default, find the matching persisted row by TypeName.
//     Reuse the existing Id + LastExecution + LastStartTime when
//     present; for fresh rows, set LastExecution = now (C# `:158`).
//     Always overwrite Interval + Priority from the default.
//  4. Upsert the merged row and store a copy in the cache.
//
// The 11 hard-coded default tasks at C# `:67-135` are NOT instantiated
// here — the host wires whichever subset of commands have ports in
// the current phase. This keeps the Service decoupled from the
// commands package's evolving registry.
func (s *Service) RegisterDefaults(ctx context.Context, defaults []*Task) error {
	defaultByName := make(map[string]*Task, len(defaults))
	for _, d := range defaults {
		if d == nil {
			return errors.New("scheduledtasks: RegisterDefaults: nil default")
		}
		if d.TypeName == "" {
			return errors.New("scheduledtasks: RegisterDefaults: empty TypeName in defaults")
		}
		if _, dup := defaultByName[d.TypeName]; dup {
			return fmt.Errorf("scheduledtasks: RegisterDefaults: duplicate TypeName %q", d.TypeName)
		}
		defaultByName[d.TypeName] = d
	}

	existing, err := s.repo.All(ctx)
	if err != nil {
		return fmt.Errorf("scheduledtasks: RegisterDefaults: %w", err)
	}

	existingByName := make(map[string]Task, len(existing))
	for _, row := range existing {
		if _, keep := defaultByName[row.TypeName]; !keep {
			if err := s.repo.Delete(ctx, row.ID); err != nil {
				return fmt.Errorf("scheduledtasks: RegisterDefaults: delete orphan %q: %w", row.TypeName, err)
			}
			continue
		}
		existingByName[row.TypeName] = row
	}

	now := s.now()
	merged := make(map[string]*Task, len(defaults))
	for _, d := range defaults {
		row := *d
		if prior, ok := existingByName[d.TypeName]; ok {
			row.ID = prior.ID
			row.LastExecution = prior.LastExecution
			row.LastStartTime = prior.LastStartTime
		} else {
			row.LastExecution = now
		}
		if err := s.repo.Upsert(ctx, &row); err != nil {
			return fmt.Errorf("scheduledtasks: RegisterDefaults: upsert %q: %w", d.TypeName, err)
		}
		stored := row
		merged[stored.TypeName] = &stored
	}

	s.mu.Lock()
	s.cache = merged
	s.mu.Unlock()
	return nil
}

// GetPending returns every cached task that is due to run — Interval
// > 0 AND LastExecution + Interval < now. Mirrors `:46-51`.
//
// Returned slice is a fresh copy; mutation is safe.
func (s *Service) GetPending(_ context.Context) []Task {
	now := s.now()
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Task, 0, len(s.cache))
	for _, t := range s.cache {
		if t.Interval <= 0 {
			continue
		}
		next := t.LastExecution.Add(time.Duration(t.Interval) * time.Minute)
		if next.Before(now) {
			out = append(out, *t)
		}
	}
	return out
}

// GetAll returns a copy of every cached task. Mirrors `:53-56`.
func (s *Service) GetAll(_ context.Context) []Task {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Task, 0, len(s.cache))
	for _, t := range s.cache {
		out = append(out, *t)
	}
	return out
}

// GetNextExecution returns the next scheduled run for the given
// TypeName — LastExecution + Interval. Mirrors `:58-63`.
//
// Returns ErrNotFound when the TypeName isn't in the cache (the C#
// version throws on `_cache.Find`'s nil result; the Go port returns a
// typed error).
func (s *Service) GetNextExecution(_ context.Context, typeName string) (time.Time, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.cache[typeName]
	if !ok {
		return time.Time{}, fmt.Errorf("%w: typeName=%q", ErrNotFound, typeName)
	}
	// LastExecution / Interval are mutated by HandleCommandExecuted
	// under the write lock; dereference the pointer while we still
	// hold the read lock to avoid a data race.
	return t.LastExecution.Add(time.Duration(t.Interval) * time.Minute), nil
}

// HandleCommandExecuted ports `TaskManager.Handle(CommandExecutedEvent)`
// at `:202-220`. The host wires this as a subscriber on the
// CommandExecutedEvent topic; on each fired event it looks up the
// scheduled task by Command.Name (which equals the TypeName key the
// host used in [Service.RegisterDefaults]). When the task is
// registered AND the host's `shouldUpdate` predicate (or the default,
// when unset) approves, it calls `repo.SetLastExecutionTime` and
// updates the cache entry's LastExecution / LastStartTime.
//
// Returns nil when:
//   - The command isn't registered as a scheduled task (silent skip).
//   - The predicate returns false (silent skip — matches the C#
//     `UpdateScheduledTask = false` short-circuit at `:206`).
//   - The Command has no StartedAt timestamp (silent skip — the C#
//     reads `message.Command.StartedAt.Value` and would NRE).
func (s *Service) HandleCommandExecuted(ctx context.Context, ev commands.CommandExecutedEvent) error {
	if ev.Command == nil || ev.Command.StartedAt == nil {
		return nil
	}
	typeName := ev.Command.Name
	s.mu.RLock()
	cached, ok := s.cache[typeName]
	pred := s.shouldUpdate
	s.mu.RUnlock()
	if !ok {
		return nil
	}
	if pred != nil && !pred(typeName) {
		return nil
	}
	exec := s.now()
	start := ev.Command.StartedAt.UTC()
	if err := s.repo.SetLastExecutionTime(ctx, cached.ID, exec, start); err != nil {
		return fmt.Errorf("scheduledtasks: HandleCommandExecuted(%q): %w", typeName, err)
	}
	s.mu.Lock()
	if entry, still := s.cache[typeName]; still {
		entry.LastExecution = exec
		entry.LastStartTime = &start
	}
	s.mu.Unlock()
	return nil
}
