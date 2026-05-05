// Singleton-table housekeepers. Some tables are designed to hold
// exactly one row (NamingConfig, Users); a duplicate-insert bug or a
// failed migration can leave them with multiple rows. These
// housekeepers run a `DELETE FROM <table> WHERE Id NOT IN (SELECT Id
// FROM <table> LIMIT 1)` to keep one row arbitrarily and drop the
// rest.
//
// `LIMIT 1` without `ORDER BY` is intentionally non-deterministic
// (matches the C# version) — in practice the table never has more
// than one row, and when it does any survivor is acceptable.

package housekeeping

import (
	"context"
	"fmt"
	"log/slog"

	"sonarr.tv/sonarr/internal/datastore"
)

// singletonTableTask is a Task that keeps one row of `table` and
// drops the rest.
type singletonTableTask struct {
	name  string
	table string
	db    *datastore.DB
	log   *slog.Logger
}

// Name implements Task.
func (t *singletonTableTask) Name() string { return t.name }

// Clean implements Task.
func (t *singletonTableTask) Clean(ctx context.Context) error {
	q := fmt.Sprintf(
		`DELETE FROM %q WHERE "Id" NOT IN (SELECT "Id" FROM %q LIMIT 1)`,
		t.table, t.table,
	)
	res, err := t.db.SQL.ExecContext(ctx, q)
	if err != nil {
		return fmt.Errorf("housekeeping: %s: %w", t.name, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("housekeeping: %s: rowsAffected: %w", t.name, err)
	}
	if n > 0 {
		t.log.LogAttrs(ctx, slog.LevelInfo,
			"housekeeping: dropped duplicate singleton rows",
			slog.String("task", t.name),
			slog.String("table", t.table),
			slog.Int64("rows", n))
	}
	return nil
}

func newSingletonTableTask(name, table string, db *datastore.DB, log *slog.Logger) (*singletonTableTask, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	if log == nil {
		log = slog.Default()
	}
	return &singletonTableTask{name: name, table: table, db: db, log: log}, nil
}

// NewCleanupAdditionalNamingSpecs ports
// `Housekeepers/CleanupAdditionalNamingSpecs.cs:1-24`. Drops every
// NamingConfig row except an arbitrary one.
func NewCleanupAdditionalNamingSpecs(db *datastore.DB, log *slog.Logger) (Task, error) {
	return newSingletonTableTask("CleanupAdditionalNamingSpecs", "NamingConfig", db, log)
}

// NewCleanupAdditionalUsers ports
// `Housekeepers/CleanupAdditionalUsers.cs:1-24`. Drops every Users
// row except an arbitrary one.
func NewCleanupAdditionalUsers(db *datastore.DB, log *slog.Logger) (Task, error) {
	return newSingletonTableTask("CleanupAdditionalUsers", "Users", db, log)
}
