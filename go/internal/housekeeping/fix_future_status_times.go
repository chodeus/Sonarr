// FixFutureProviderStatusTimes family. Ports
// `src/NzbDrone.Core/Housekeeping/Housekeepers/FixFutureProviderStatusTimes.cs:1-57`
// plus the four wrapper subclasses
// (`FixFuture{Indexer,DownloadClient,ImportList,Notification}StatusTimes.cs`).
//
// Why it exists in C# (and here): provider-status rows record three
// failure-clock columns — DisabledTill, InitialFailure, MostRecentFailure
// — that can land in the future after a clock jump (NTP correction or
// manual fix). DisabledTill is also bounded by the EscalationLevel-driven
// BackOff period: a row whose EscalationLevel is 3 should never have
// DisabledTill > now + Periods[3] = now + 15min. This task clamps each
// of the three columns back to a sane upper bound so the
// ProviderStatusBase escalation logic doesn't keep providers disabled
// forever after a clock anomaly.
//
// Implementation:
//
// The C# version reads every row, mutates in place, and bulk-updates
// the affected rows. The Go port matches that shape — the per-row
// CASE-based UPDATE would either need engine-specific date arithmetic
// (sqlite `datetime(?, '+N seconds')` vs Postgres `? + INTERVAL`) or a
// hard-coded SQL CASE table that drifts from BackOffPeriodSeconds.
// Reading + computing in Go keeps the BackOffPeriods table as the
// single source of truth and works on both engines.

package housekeeping

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/providerstatus"
)

// FixFutureProviderStatusTimes is the shared scaffold for the four
// per-status-table housekeepers. The C# generic
// `FixFutureProviderStatusTimes<TModel>` is parameterised on the model
// type; the Go port parameterises on the table name (since all four
// status tables share the four columns this housekeeper touches).
//
// Constructed only via the four `New*` constructors below — the table
// name + canonical task name are wired there.
type FixFutureProviderStatusTimes struct {
	name  string
	table string
	db    *datastore.DB
	log   *slog.Logger
	now   func() time.Time
}

// SetClock overrides the clock used to compute the upper bounds.
// Tests pin a deterministic value; production leaves the default.
func (t *FixFutureProviderStatusTimes) SetClock(f func() time.Time) {
	if f == nil {
		return
	}
	t.now = f
}

// Name implements Task.
func (t *FixFutureProviderStatusTimes) Name() string { return t.name }

// Clean implements Task. Reads every row in the target status table,
// computes per-row upper bounds for the three time columns, and issues
// a per-row UPDATE for any row that needed clamping. Mirrors the C#
// per-row mutate-then-UpdateMany shape at
// `FixFutureProviderStatusTimes.cs:19-55`.
func (t *FixFutureProviderStatusTimes) Clean(ctx context.Context) error {
	now := t.now()

	q := fmt.Sprintf(
		`SELECT "Id","EscalationLevel","DisabledTill","InitialFailure","MostRecentFailure" FROM %q`,
		t.table,
	)
	rows, err := t.db.SQL.QueryContext(ctx, q)
	if err != nil {
		return fmt.Errorf("housekeeping: %s: select: %w", t.name, err)
	}
	defer func() { _ = rows.Close() }()

	type pending struct {
		id                int
		disabledTill      sql.NullTime
		initialFailure    sql.NullTime
		mostRecentFailure sql.NullTime
	}
	var updates []pending

	for rows.Next() {
		var (
			id                int
			level             int
			disabledTill      sql.NullTime
			initialFailure    sql.NullTime
			mostRecentFailure sql.NullTime
		)
		if err := rows.Scan(&id, &level, &disabledTill, &initialFailure, &mostRecentFailure); err != nil {
			return fmt.Errorf("housekeeping: %s: scan: %w", t.name, err)
		}

		// Cap EscalationLevel at the BackOffPeriods bounds. The C#
		// version subscripts directly and would IndexOutOfRange on a
		// rogue row; the Go port clamps defensively (a value < 0 or
		// >= len pins to the nearest valid index).
		idx := level
		if idx < 0 {
			idx = 0
		}
		if idx >= len(providerstatus.BackOffPeriodSeconds) {
			idx = len(providerstatus.BackOffPeriodSeconds) - 1
		}
		escalationDelay := time.Duration(providerstatus.BackOffPeriodSeconds[idx]) * time.Second
		disabledMax := now.Add(escalationDelay)

		updated := false
		if disabledTill.Valid && disabledTill.Time.After(disabledMax) {
			disabledTill = sql.NullTime{Time: disabledMax.UTC(), Valid: true}
			updated = true
		}
		if initialFailure.Valid && initialFailure.Time.After(now) {
			initialFailure = sql.NullTime{Time: now.UTC(), Valid: true}
			updated = true
		}
		if mostRecentFailure.Valid && mostRecentFailure.Time.After(now) {
			mostRecentFailure = sql.NullTime{Time: now.UTC(), Valid: true}
			updated = true
		}
		if !updated {
			continue
		}
		updates = append(updates, pending{
			id:                id,
			disabledTill:      disabledTill,
			initialFailure:    initialFailure,
			mostRecentFailure: mostRecentFailure,
		})
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("housekeeping: %s: rows: %w", t.name, err)
	}
	// Close before issuing UPDATEs; SQLite holds a shared lock on the
	// statement that blocks writes on the same connection.
	_ = rows.Close()

	if len(updates) == 0 {
		return nil
	}

	updateSQL := fmt.Sprintf(
		`UPDATE %q SET "DisabledTill" = %s, "InitialFailure" = %s, "MostRecentFailure" = %s WHERE "Id" = %s`,
		t.table,
		datastore.Placeholder(t.db.Engine, 1),
		datastore.Placeholder(t.db.Engine, 2),
		datastore.Placeholder(t.db.Engine, 3),
		datastore.Placeholder(t.db.Engine, 4),
	)
	for _, u := range updates {
		if _, err := t.db.SQL.ExecContext(ctx, updateSQL,
			u.disabledTill, u.initialFailure, u.mostRecentFailure, u.id,
		); err != nil {
			return fmt.Errorf("housekeeping: %s: update id=%d: %w", t.name, u.id, err)
		}
	}
	t.log.LogAttrs(ctx, slog.LevelInfo,
		"housekeeping: clamped future provider-status times",
		slog.String("task", t.name),
		slog.String("table", t.table),
		slog.Int("rows", len(updates)))
	return nil
}

func newFixFutureStatusTimes(name, table string, db *datastore.DB, log *slog.Logger) (*FixFutureProviderStatusTimes, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	if log == nil {
		log = slog.Default()
	}
	return &FixFutureProviderStatusTimes{
		name:  name,
		table: table,
		db:    db,
		log:   log,
		now:   func() time.Time { return time.Now().UTC() },
	}, nil
}

// NewFixFutureIndexerStatusTimes ports
// `Housekeepers/FixFutureIndexerStatusTimes.cs:1-13`. Clamps the three
// failure-clock columns on the IndexerStatus table.
func NewFixFutureIndexerStatusTimes(db *datastore.DB, log *slog.Logger) (*FixFutureProviderStatusTimes, error) {
	return newFixFutureStatusTimes("FixFutureIndexerStatusTimes", "IndexerStatus", db, log)
}

// NewFixFutureDownloadClientStatusTimes ports
// `Housekeepers/FixFutureDownloadClientStatusTimes.cs:1-13`. Clamps the
// three failure-clock columns on the DownloadClientStatus table.
func NewFixFutureDownloadClientStatusTimes(db *datastore.DB, log *slog.Logger) (*FixFutureProviderStatusTimes, error) {
	return newFixFutureStatusTimes("FixFutureDownloadClientStatusTimes", "DownloadClientStatus", db, log)
}

// NewFixFutureImportListStatusTimes ports
// `Housekeepers/FixFutureImportListStatusTimes.cs:1-13`. Clamps the
// three failure-clock columns on the ImportListStatus table.
func NewFixFutureImportListStatusTimes(db *datastore.DB, log *slog.Logger) (*FixFutureProviderStatusTimes, error) {
	return newFixFutureStatusTimes("FixFutureImportListStatusTimes", "ImportListStatus", db, log)
}

// NewFixFutureNotificationStatusTimes ports
// `Housekeepers/FixFutureNotificationStatusTimes.cs:1-13`. Clamps the
// three failure-clock columns on the NotificationStatus table.
func NewFixFutureNotificationStatusTimes(db *datastore.DB, log *slog.Logger) (*FixFutureProviderStatusTimes, error) {
	return newFixFutureStatusTimes("FixFutureNotificationStatusTimes", "NotificationStatus", db, log)
}
