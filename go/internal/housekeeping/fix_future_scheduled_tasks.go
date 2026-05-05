// FixFutureRunScheduledTasks. Ports
// `src/NzbDrone.Core/Housekeeping/Housekeepers/FixFutureRunScheduledTasks.cs:1-35`.
//
// Why it exists in C# (and here): if the system clock runs ahead and
// then jumps back (NTP correction, manual time fix), some
// ScheduledTasks rows can end up with LastExecution in the future.
// GetPending compares `LastExecution + Interval < now`, so a future
// LastExecution masks a task as "not due yet" forever. This task
// clamps any LastExecution > now back to now so the scheduler can
// pick the task up again.

package housekeeping

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
)

// FixFutureRunScheduledTasks is the housekeeper that clamps
// ScheduledTasks.LastExecution to now when it's in the future.
type FixFutureRunScheduledTasks struct {
	db  *datastore.DB
	log *slog.Logger
	now func() time.Time
}

// NewFixFutureRunScheduledTasks builds the task. db must be non-nil;
// log defaults to slog.Default. Tests pin a fixed-time clock via
// SetClock for deterministic UPDATE bounds.
func NewFixFutureRunScheduledTasks(db *datastore.DB, log *slog.Logger) (*FixFutureRunScheduledTasks, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	if log == nil {
		log = slog.Default()
	}
	return &FixFutureRunScheduledTasks{
		db:  db,
		log: log,
		now: func() time.Time { return time.Now().UTC() },
	}, nil
}

// SetClock overrides the clock used to compute the UPDATE's `time`
// parameter. Tests pin a deterministic value; production leaves the
// default.
func (t *FixFutureRunScheduledTasks) SetClock(f func() time.Time) {
	if f == nil {
		return
	}
	t.now = f
}

// Name implements Task.
func (t *FixFutureRunScheduledTasks) Name() string {
	return "FixFutureRunScheduledTasks"
}

// Clean implements Task. Issues a single UPDATE on rows where
// LastExecution > now, setting LastExecution = now.
//
// The C# version runs the UPDATE unconditionally; the BuildInfo.IsDebug
// log line at `:22-25` is cosmetic — the UPDATE still fires. The Go
// port preserves the unconditional UPDATE and skips the cosmetic log.
func (t *FixFutureRunScheduledTasks) Clean(ctx context.Context) error {
	now := t.now()
	q := fmt.Sprintf(
		`UPDATE "ScheduledTasks" SET "LastExecution" = %s WHERE "LastExecution" > %s`,
		datastore.Placeholder(t.db.Engine, 1),
		datastore.Placeholder(t.db.Engine, 2),
	)
	res, err := t.db.SQL.ExecContext(ctx, q, now, now)
	if err != nil {
		return fmt.Errorf("FixFutureRunScheduledTasks: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("FixFutureRunScheduledTasks: rowsAffected: %w", err)
	}
	if n > 0 {
		t.log.LogAttrs(ctx, slog.LevelInfo,
			"housekeeping: clamped future ScheduledTasks.LastExecution",
			slog.Int64("rows", n))
	}
	return nil
}
