// CleanupDownloadClientUnavailablePendingReleases housekeeper. Ports
// the C# task at
// `src/NzbDrone.Core/Housekeeping/Housekeepers/CleanupDownloadClientUnavailablePendingReleases.cs`.
//
// Sweeps PendingReleases rows that have been parked with a transient
// defer-reason for too long. The orchestrator stages a row with
// [pendingreleases.ReasonDownloadClientUnavailable] when no
// download client could accept a grab at decision time, and with
// [pendingreleases.ReasonFallback] for ambiguous queue-state defers;
// both are expected to clear on the next retry tick. If a row's
// Added timestamp is older than 14 days, the underlying defer almost
// certainly stuck because the original release is no longer
// recoverable — the row would otherwise live in the queue forever,
// re-evaluated on every tick.
//
// 14-day TTL pinned against C# `:28` (`DateTime.UtcNow.AddDays(-14)`).
// Rows with [pendingreleases.ReasonDelay] (the DelayProfile-driven
// defer) are NOT swept; they're expected to live until the
// DelayProfile timer fires.

package housekeeping

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/pendingreleases"
)

// NewCleanupDownloadClientUnavailablePendingReleases ports
// `Housekeepers/CleanupDownloadClientUnavailablePendingReleases.cs:1-45`.
// Drops PendingReleases rows older than 14 days whose Reason is
// DownloadClientUnavailable or Fallback.
//
// The Go port uses a single dialect-neutral DELETE via
// [datastore.Placeholder] so Postgres gets `$N` and SQLite gets `?`.
// The C# tree's Postgres branch uses `Reason = ANY (@Reasons)` only
// because Dapper expands array params that way; database/sql doesn't
// have an array type, so the IN-list with literal placeholders is
// the natural Go form. Behaviour is identical — same rows match.
func NewCleanupDownloadClientUnavailablePendingReleases(db *datastore.DB, log *slog.Logger) (Task, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	if log == nil {
		log = slog.Default()
	}
	return &cleanupDCUPending{db: db, log: log, now: time.Now}, nil
}

type cleanupDCUPending struct {
	db  *datastore.DB
	log *slog.Logger
	// now is the clock used to compute the 14-day cutoff. Pinned via
	// the unexported field; the wall clock is wired by the
	// constructor. Tests step over the TTL by seeding rows with an
	// Added timestamp older than 14 days rather than mocking now.
	now func() time.Time
}

// Name implements Task.
func (t *cleanupDCUPending) Name() string {
	return "CleanupDownloadClientUnavailablePendingReleases"
}

// Clean implements Task. Single DELETE; rows older than 14 days
// whose Reason is in the transient-defer set are removed.
func (t *cleanupDCUPending) Clean(ctx context.Context) error {
	cutoff := t.now().UTC().AddDate(0, 0, -14)
	q := fmt.Sprintf(
		`DELETE FROM "PendingReleases" WHERE "Added" < %s AND "Reason" IN (%s, %s)`,
		datastore.Placeholder(t.db.Engine, 1),
		datastore.Placeholder(t.db.Engine, 2),
		datastore.Placeholder(t.db.Engine, 3),
	)
	res, err := t.db.SQL.ExecContext(ctx, q,
		cutoff,
		int(pendingreleases.ReasonDownloadClientUnavailable),
		int(pendingreleases.ReasonFallback),
	)
	if err != nil {
		return fmt.Errorf("housekeeping: CleanupDownloadClientUnavailablePendingReleases: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("housekeeping: CleanupDownloadClientUnavailablePendingReleases: rowsAffected: %w", err)
	}
	if n > 0 {
		t.log.LogAttrs(ctx, slog.LevelInfo,
			"housekeeping: deleted stale PendingReleases rows",
			slog.Int64("rows", n))
	}
	return nil
}
