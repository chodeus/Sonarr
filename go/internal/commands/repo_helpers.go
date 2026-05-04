package commands

import (
	"context"
	"fmt"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
)

// Queued ports `CommandRepository.Queued()` at
// `src/NzbDrone.Core/Messaging/Commands/CommandRepository.cs:50-53`.
// Returns every Command whose Status is StatusQueued, ordered by
// QueuedAt ascending. The dispatcher loop drains this list.
func (r *Repo) Queued(ctx context.Context) ([]Command, error) {
	return r.AllByStatus(ctx, int(StatusQueued))
}

// Started ports the equivalent of `Queued` for StatusStarted —
// returns every in-flight command. Used by CommandQueueManager.GetStarted
// (`CommandQueueManager.cs:174-178`).
func (r *Repo) Started(ctx context.Context) ([]Command, error) {
	return r.AllByStatus(ctx, int(StatusStarted))
}

// Trim ports `CommandRepository.Trim()` at `:25-30`. Deletes every
// command whose EndedAt is older than `cutoff`. The C# version
// hard-codes `DateTime.UtcNow.AddDays(-1)`; the Go port lets the
// caller pass the cutoff so the cleanup-housekeeper can drive it
// from config (and tests can pin deterministic cutoffs).
//
// Returns the number of rows deleted.
func (r *Repo) Trim(ctx context.Context, cutoff time.Time) (int64, error) {
	db := r.DB()
	q := fmt.Sprintf(
		`DELETE FROM "Commands" WHERE "EndedAt" < %s`,
		datastore.Placeholder(db.Engine, 1),
	)
	res, err := db.SQL.ExecContext(ctx, q, cutoff.UTC())
	if err != nil {
		return 0, fmt.Errorf("commands: Trim: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("commands: Trim: rowsAffected: %w", err)
	}
	return n, nil
}

// OrphanStarted ports `CommandRepository.OrphanStarted()` at
// `:32-46`. On Sonarr restart any command that was StatusStarted is
// transitioned to StatusOrphaned with EndedAt = now — the dispatcher
// crashed mid-run and the work needs to be re-queued (or surfaced to
// the user as failed).
//
// Returns the number of rows updated.
func (r *Repo) OrphanStarted(ctx context.Context) (int64, error) {
	db := r.DB()
	now := time.Now().UTC()
	q := fmt.Sprintf(
		`UPDATE "Commands" SET "Status" = %s, "EndedAt" = %s WHERE "Status" = %s`,
		datastore.Placeholder(db.Engine, 1),
		datastore.Placeholder(db.Engine, 2),
		datastore.Placeholder(db.Engine, 3),
	)
	res, err := db.SQL.ExecContext(ctx, q, int(StatusOrphaned), now, int(StatusStarted))
	if err != nil {
		return 0, fmt.Errorf("commands: OrphanStarted: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("commands: OrphanStarted: rowsAffected: %w", err)
	}
	return n, nil
}

// Start ports `CommandRepository.Start(CommandModel)` at `:55-58`.
// Targeted UPDATE on (StartedAt, Status) only — the C# version uses
// `SetFields(c => c.StartedAt, c => c.Status)`. In-place mutation is
// intentional so the caller's Command pointer stays current with the
// persisted shape (matches the C# `SetFields` semantic).
//
// `started` is forced to UTC. Caller is responsible for setting
// `c.Status` (typically StatusStarted) before calling.
func (r *Repo) Start(ctx context.Context, c *Command, started time.Time) error {
	if c == nil {
		return fmt.Errorf("commands: Start: nil command")
	}
	startUTC := started.UTC()
	db := r.DB()
	q := fmt.Sprintf(
		`UPDATE "Commands" SET "StartedAt" = %s, "Status" = %s WHERE "Id" = %s`,
		datastore.Placeholder(db.Engine, 1),
		datastore.Placeholder(db.Engine, 2),
		datastore.Placeholder(db.Engine, 3),
	)
	res, err := db.SQL.ExecContext(ctx, q, startUTC, c.Status, c.ID)
	if err != nil {
		return fmt.Errorf("commands: Start(%d): %w", c.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("commands: Start: rowsAffected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	c.StartedAt = &startUTC
	return nil
}

// End ports `CommandRepository.End(CommandModel)` at `:60-63`.
// Targeted UPDATE on (EndedAt, Status, Duration, Exception) — the C#
// version uses `SetFields(c => c.EndedAt, c => c.Status, c => c.Duration,
// c => c.Exception)`.
//
// `ended` is forced to UTC. `c.Duration` and `c.Exception` are written
// from the struct. Caller sets c.Status (typically StatusCompleted /
// StatusFailed) before calling.
func (r *Repo) End(ctx context.Context, c *Command, ended time.Time) error {
	if c == nil {
		return fmt.Errorf("commands: End: nil command")
	}
	endUTC := ended.UTC()
	db := r.DB()
	q := fmt.Sprintf(
		`UPDATE "Commands" SET "EndedAt" = %s, "Status" = %s, "Duration" = %s, "Exception" = %s WHERE "Id" = %s`,
		datastore.Placeholder(db.Engine, 1),
		datastore.Placeholder(db.Engine, 2),
		datastore.Placeholder(db.Engine, 3),
		datastore.Placeholder(db.Engine, 4),
		datastore.Placeholder(db.Engine, 5),
	)
	res, err := db.SQL.ExecContext(ctx, q, endUTC, c.Status, nullableDuration(c.Duration), nullableStringEmpty(c.Exception), c.ID)
	if err != nil {
		return fmt.Errorf("commands: End(%d): %w", c.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("commands: End: rowsAffected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	c.EndedAt = &endUTC
	return nil
}

// FindByName ports the name-based query the dispatcher uses to detect
// duplicate enqueues. Returns every Command whose Name matches `name`,
// ordered by QueuedAt descending so the most recent entry comes first.
//
// CommandQueueManager uses this in the de-duplication path:
//
//	`Push<TCommand>` checks for an existing Queued/Started command with
//	the same Name + Body and returns the existing row instead of
//	queuing a duplicate (`CommandQueueManager.cs:101-140`).
//
// The Go-port version defers the Body comparison to the caller — the
// repo just returns rows by name; the Service layer compares Body
// JSON.
func (r *Repo) FindByName(ctx context.Context, name string) ([]Command, error) {
	db := r.DB()
	q := fmt.Sprintf(
		`SELECT "Id","Name","Body","Priority","Status","QueuedAt","StartedAt","EndedAt","Duration","Exception","Trigger","Result" FROM "Commands" WHERE "Name" = %s ORDER BY "QueuedAt" DESC`,
		datastore.Placeholder(db.Engine, 1),
	)
	rows, err := db.SQL.QueryContext(ctx, q, name)
	if err != nil {
		return nil, fmt.Errorf("commands: FindByName(%q): %w", name, err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]Command, 0)
	for rows.Next() {
		var c Command
		if err := mapper.Scan(&c, rows); err != nil {
			return nil, fmt.Errorf("commands: FindByName scan: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("commands: FindByName rows: %w", err)
	}
	return out, nil
}
