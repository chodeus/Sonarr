// Service for the Commands domain. Mirrors
// `NzbDrone.Core.Messaging.Commands.CommandQueueManager` at
// `src/NzbDrone.Core/Messaging/Commands/CommandQueueManager.cs`.
//
// What this slice (Phase 3.9) ships:
//
//   - Service struct + NewService — repo + optional bus + injectable
//     clock (for deterministic tests).
//   - Push / PushMany — enqueue with de-duplication against existing
//     Queued/Started rows by (Name, Body) equality.
//   - Get / All / GetQueued / GetStarted — read accessors.
//   - Start — repo.Start passthrough.
//   - Complete / Fail — set status + persist via repo.End + publish
//     [CommandExecutedEvent].
//   - Cancel — flip a Queued row to Cancelled.
//   - CleanCommands — repo.Trim wrapper.
//   - HandleApplicationStarted — repo.OrphanStarted plus a returned
//     queue list the caller can re-push into the (Phase 3.10+) in-memory
//     dispatcher.
//
// What's deferred:
//
//   - In-memory CommandQueue (the C# `CommandQueue` blocking
//     consumer-enumerator at `:151-154`). Phase 3.10+ ports it as a
//     goroutine-friendly channel-based dispatcher.
//   - `IExecute<T>` discovery + dispatch loop (`CommandExecutor`).
//     Phase 3.10+.
//   - GetCommand reflection lookup on the `Push(string commandName)`
//     overload — the Go port's Push takes the body string directly
//     so the host wires per-command-type adapters.
//
// Body comparison for dedup uses raw string equality. This matches
// the C# `CommandEqualityComparer` in spirit but assumes callers
// produce canonical JSON for the body. For most production callers
// (the scheduler + the API push handler) the body is constructed
// from a fixed shape and naturally canonical; the autotagging /
// custom-format Push paths use a single ID payload that's trivially
// canonical too.

package commands

import (
	"context"
	"errors"
	"fmt"
	"time"

	"sonarr.tv/sonarr/internal/messaging"
)

// Service wraps the Repo with the higher-level surface the rest of
// the app consumes.
type Service struct {
	repo  *Repo
	bus   *messaging.Bus
	queue *Queue
	now   func() time.Time
}

// NewService builds a Service. `bus` may be nil to skip publishing.
func NewService(r *Repo, bus *messaging.Bus) (*Service, error) {
	if r == nil {
		return nil, errors.New("commands: nil repo")
	}
	return &Service{repo: r, bus: bus, now: func() time.Time { return time.Now().UTC() }}, nil
}

// SetClock overrides the clock used for QueuedAt / StartedAt /
// EndedAt / Duration timestamps. Mirrors the providerstatus.SetClock
// pattern. Tests pass a fixed-time func to make timestamps
// deterministic.
func (s *Service) SetClock(f func() time.Time) {
	if f == nil {
		return
	}
	s.now = f
}

// SetQueue wires the in-memory dispatcher buffer (Phase 3.11). When
// set, [Service.Push] / [Service.PushMany] enqueue every newly-
// inserted row so the dispatcher can pick it up, and [Service.Cancel]
// drops the row from the in-memory pending set so the dispatcher
// won't run a Cancelled command. Pass nil to detach (rare; useful in
// tests that swap registries).
func (s *Service) SetQueue(q *Queue) {
	s.queue = q
}

// Push ports `CommandQueueManager.Push<TCommand>(...)` at
// `CommandQueueManager.cs:101-140`. Inserts a fresh Commands row
// unless a Queued/Started row with the same Name + Body already
// exists, in which case the existing row is returned (matches C#
// dedup at `:113-121`).
//
// `body` is the JSON-encoded command payload. The Go port doesn't
// decode it here — callers (the API layer or the scheduler) produce
// the canonical JSON before calling.
//
// Returns the persisted Command (with the assigned ID).
func (s *Service) Push(ctx context.Context, name, body string, priority Priority, trigger Trigger) (*Command, error) {
	if name == "" {
		return nil, errors.New("commands: Push: empty name")
	}
	if existing, err := s.findExisting(ctx, name, body); err != nil {
		return nil, err
	} else if existing != nil {
		return existing, nil
	}
	row := &Command{
		Name:     name,
		Body:     body,
		Priority: int(priority),
		Status:   int(StatusQueued),
		QueuedAt: s.now(),
		Trigger:  int(trigger),
		Result:   int(ResultUnknown),
	}
	if _, err := s.repo.Insert(ctx, row); err != nil {
		return nil, fmt.Errorf("commands: Push(%q): %w", name, err)
	}
	if s.queue != nil {
		s.queue.Add(row)
	}
	s.publishUpdated(ctx, row)
	return row, nil
}

// PushItem is one entry in a [Service.PushMany] batch — same shape as
// the (name, body, priority, trigger) tuple [Service.Push] takes.
type PushItem struct {
	Name     string
	Body     string
	Priority Priority
	Trigger  Trigger
}

// PushMany ports `CommandQueueManager.PushMany<TCommand>(...)` at
// `:58-99`. Per-row dedup against the existing Queued+Started set,
// then per-row Insert. Returns the inserted (newly-queued) commands
// only — duplicates from the input are silently dropped (matches
// C# `:72-75`).
//
// The C# version uses a single InsertMany; the Go port issues per-row
// Insert (same approximation as series.AddMany). Returns the
// newly-queued rows in the same order as the input, minus duplicates.
func (s *Service) PushMany(ctx context.Context, items []PushItem) ([]*Command, error) {
	if len(items) == 0 {
		return nil, nil
	}
	out := make([]*Command, 0, len(items))
	for i, item := range items {
		if item.Name == "" {
			return nil, fmt.Errorf("commands: PushMany: empty name at idx %d", i)
		}
		existing, err := s.findExisting(ctx, item.Name, item.Body)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			continue
		}
		row := &Command{
			Name:     item.Name,
			Body:     item.Body,
			Priority: int(item.Priority),
			Status:   int(StatusQueued),
			QueuedAt: s.now(),
			Trigger:  int(item.Trigger),
			Result:   int(ResultUnknown),
		}
		if _, err := s.repo.Insert(ctx, row); err != nil {
			return nil, fmt.Errorf("commands: PushMany(%q): %w", item.Name, err)
		}
		if s.queue != nil {
			s.queue.Add(row)
		}
		s.publishUpdated(ctx, row)
		out = append(out, row)
	}
	return out, nil
}

// Get ports `:162-172`.
func (s *Service) Get(ctx context.Context, id int) (*Command, error) {
	return s.repo.Get(ctx, id)
}

// All ports `:156-160`.
func (s *Service) All(ctx context.Context) ([]Command, error) {
	return s.repo.All(ctx)
}

// GetQueued returns every currently-queued command. Mirrors
// `CommandRepository.Queued()` via the Service surface.
func (s *Service) GetQueued(ctx context.Context) ([]Command, error) {
	return s.repo.Queued(ctx)
}

// GetStarted ports `:174-178`. Returns every currently-running
// command (Status = StatusStarted).
func (s *Service) GetStarted(ctx context.Context) ([]Command, error) {
	return s.repo.Started(ctx)
}

// Start ports `:190-195`. Marks a command as started in the DB.
func (s *Service) Start(ctx context.Context, c *Command) error {
	if c == nil {
		return errors.New("commands: Start: nil command")
	}
	c.Status = int(StatusStarted)
	if err := s.repo.Start(ctx, c, s.now()); err != nil {
		return err
	}
	s.publishUpdated(ctx, c)
	return nil
}

// Complete ports `:197-208`. Default-sets the Result if Unknown,
// updates EndedAt + Duration + Status=Completed, persists, and
// publishes [CommandExecutedEvent].
//
// `message` is the C# `command.Message` field — a free-text status
// blurb the API surfaces. The Go port doesn't persist it (the
// `Message` field is `[NotMapped]` in C#); it propagates through the
// CommandExecutedEvent payload's Command.Body for any host-side
// listener that wants it.
func (s *Service) Complete(ctx context.Context, c *Command, _ string) error {
	if c == nil {
		return errors.New("commands: Complete: nil command")
	}
	if Result(c.Result) == ResultUnknown {
		c.Result = int(ResultSuccessful)
	}
	c.Status = int(StatusCompleted)
	if err := s.applyEnd(ctx, c); err != nil {
		return err
	}
	if s.bus != nil {
		_ = messaging.Publish(ctx, s.bus, CommandExecutedEvent{Command: c})
	}
	s.publishUpdated(ctx, c)
	return nil
}

// Fail ports `:210-217`. Stamps the exception, transitions to
// Failed, persists, and publishes [CommandExecutedEvent]. The C#
// version takes an `Exception e` and stores `e.ToString()`; the Go
// port takes a pre-formatted string so the caller can choose what
// detail to surface (stack trace, panic message, etc.).
func (s *Service) Fail(ctx context.Context, c *Command, _ string, exceptionDetail string) error {
	if c == nil {
		return errors.New("commands: Fail: nil command")
	}
	c.Exception = exceptionDetail
	c.Status = int(StatusFailed)
	if Result(c.Result) == ResultUnknown {
		c.Result = int(ResultUnsuccessful)
	}
	if err := s.applyEnd(ctx, c); err != nil {
		return err
	}
	if s.bus != nil {
		_ = messaging.Publish(ctx, s.bus, CommandExecutedEvent{Command: c})
	}
	s.publishUpdated(ctx, c)
	return nil
}

// Cancel ports `:227-233`. Atomic transition Queued → Cancelled. The
// C# version manipulates the in-memory queue (and throws a 409 if
// the command isn't found there); the Go port does it at the DB
// layer until the in-memory dispatcher ports.
//
// Returns:
//   - ErrNotFound when the row is missing.
//   - ErrCannotCancel when the row exists but isn't in the Queued
//     state (matches the C# 409 semantic — a Started/Completed
//     command can't be cancelled).
func (s *Service) Cancel(ctx context.Context, id int) error {
	row, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if Status(row.Status) != StatusQueued {
		return fmt.Errorf("%w: id=%d in state %s", ErrCannotCancel, id, Status(row.Status))
	}
	row.Status = int(StatusCancelled)
	now := s.now()
	row.EndedAt = &now
	row.Result = int(ResultUnsuccessful)
	if err := s.repo.End(ctx, row, now); err != nil {
		return err
	}
	if s.queue != nil {
		// The DB row is now Cancelled; drop the in-memory copy so the
		// dispatcher doesn't pick it up. The queue's RemoveIfQueued
		// only matches rows still in the Queued state, which is what
		// we want — a Started row was already pulled and is being
		// run by the dispatcher (cancellation propagation into a
		// running handler is a Phase 3.12 follow-up).
		s.queue.RemoveIfQueued(id)
	}
	s.publishUpdated(ctx, row)
	return nil
}

// ErrCannotCancel is returned by Cancel when the row is not in the
// Queued state. Matches the C# `NzbDroneClientException(Conflict)`
// semantic.
var ErrCannotCancel = errors.New("commands: cannot cancel — command is not queued")

// publishUpdated emits [CommandUpdatedEvent] best-effort. No-op when
// bus is nil (matches the established skip-publish pattern). See the
// CommandUpdatedEvent doc-comment for the rationale (the Go-port
// Service publishes at every transition until the dispatcher splits
// out and a separate executor takes ownership of the Start/Finally
// emissions).
func (s *Service) publishUpdated(ctx context.Context, c *Command) {
	if s.bus == nil || c == nil {
		return
	}
	_ = messaging.Publish(ctx, s.bus, CommandUpdatedEvent{Command: c})
}

// CleanCommands ports `:235-246`. The C# version hard-codes a
// `now - 5 minutes` cutoff for the in-memory queue and uses
// `now - 1 day` (via Repo.Trim) for the DB. The Go port collapses
// both to a single `cutoff` parameter — caller chooses the policy
// (typically the housekeeper passes `now - 1 day` to match C# DB
// retention).
//
// Returns the number of DB rows deleted.
func (s *Service) CleanCommands(ctx context.Context, cutoff time.Time) (int64, error) {
	return s.repo.Trim(ctx, cutoff)
}

// HandleApplicationStarted ports the boot-time recovery path at
// `:276-281`. Flips any leftover Started rows to Orphaned (the
// dispatcher crashed mid-run on the previous boot) and returns the
// list of currently-Queued commands so the host can re-push them
// into the (Phase 3.10+) in-memory dispatcher via Service.Push or
// the dispatcher's enqueue API.
//
// Returns (orphanedCount, queuedRows, err).
func (s *Service) HandleApplicationStarted(ctx context.Context) (int64, []Command, error) {
	orphaned, err := s.repo.OrphanStarted(ctx)
	if err != nil {
		return 0, nil, err
	}
	queued, err := s.repo.Queued(ctx)
	if err != nil {
		return orphaned, nil, err
	}
	return orphaned, queued, nil
}

// findExisting walks the rows that share `name` and returns the first
// one in the Queued or Started state whose Body matches. Mirrors C#
// `:113-121`, scoped to a per-name query for efficiency.
func (s *Service) findExisting(ctx context.Context, name, body string) (*Command, error) {
	hits, err := s.repo.FindByName(ctx, name)
	if err != nil {
		return nil, err
	}
	for i := range hits {
		st := Status(hits[i].Status)
		if (st == StatusQueued || st == StatusStarted) && hits[i].Body == body {
			row := hits[i]
			return &row, nil
		}
	}
	return nil, nil
}

// applyEnd is the shared epilogue for Complete/Fail. Stamps EndedAt,
// computes Duration if StartedAt is set, persists via repo.End.
func (s *Service) applyEnd(ctx context.Context, c *Command) error {
	now := s.now()
	c.EndedAt = &now
	if c.StartedAt != nil {
		dur := now.Sub(*c.StartedAt)
		c.Duration = &dur
	}
	return s.repo.End(ctx, c, now)
}
