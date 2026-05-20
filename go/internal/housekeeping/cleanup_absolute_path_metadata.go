// CleanupAbsolutePathMetadataFiles housekeeper. Ports the C# task at
// `src/NzbDrone.Core/Housekeeping/Housekeepers/CleanupAbsolutePathMetadataFiles.cs`.
//
// Background: very old Sonarr installs (pre-v3) used to store
// MetadataFiles.RelativePath as an absolute path on some metadata
// consumers — a bug. The migration to relative paths left those rows
// in place; this housekeeper sweeps them on every pass so the
// metadata-write paths don't trip over them. Patterns matched:
//
//   - `_:\...`   — Windows drive-letter form (e.g. `C:\foo`).
//   - `\...`     — Windows UNC / root-anchored form.
//   - `/...`     — POSIX absolute form.
//
// The C# tree ships dialect-specific SQL because Postgres LIKE
// interprets `\` as the pattern-escape character (RFC `LIKE`
// semantics) and SQLite does NOT. So the C# code double-escapes the
// backslash on Postgres (`'_:\\%'`) and single-escapes on SQLite
// (`'_:\%'`). The Go port preserves both dialect forms verbatim;
// changing the escape posture would break LIKE matching on either
// engine.

package housekeeping

import (
	"context"
	"fmt"
	"log/slog"

	"sonarr.tv/sonarr/internal/datastore"
)

// NewCleanupAbsolutePathMetadataFiles ports
// `Housekeepers/CleanupAbsolutePathMetadataFiles.cs:1-46`. Sweeps
// MetadataFiles rows whose RelativePath was persisted as an absolute
// path (Windows drive-letter, Windows UNC, or POSIX rooted).
//
// Dialect-aware: emits the Postgres-flavoured `WHERE Id = ANY (...)`
// with double-escaped backslash on EnginePostgres, and the SQLite
// `WHERE Id IN (...)` with single-escaped backslash otherwise. The
// inner SELECT is identical between dialects modulo the escape
// posture.
func NewCleanupAbsolutePathMetadataFiles(db *datastore.DB, log *slog.Logger) (Task, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	if log == nil {
		log = slog.Default()
	}
	return &cleanupAbsolutePathMetadataFiles{db: db, log: log}, nil
}

type cleanupAbsolutePathMetadataFiles struct {
	db  *datastore.DB
	log *slog.Logger
}

// Name implements Task.
func (t *cleanupAbsolutePathMetadataFiles) Name() string {
	return "CleanupAbsolutePathMetadataFiles"
}

// Clean implements Task. Single DELETE per pass; the dialect branch
// only changes the WHERE-Id projection (ANY vs IN) and the escape
// posture on the LIKE patterns.
func (t *cleanupAbsolutePathMetadataFiles) Clean(ctx context.Context) error {
	var q string
	if t.db.Engine == datastore.EnginePostgres {
		q = `DELETE FROM "MetadataFiles" WHERE "Id" = ANY (
			SELECT "Id" FROM "MetadataFiles"
			WHERE "RelativePath" LIKE '_:\\%'
			OR "RelativePath" LIKE '\\%'
			OR "RelativePath" LIKE '/%')`
	} else {
		q = `DELETE FROM "MetadataFiles" WHERE "Id" IN (
			SELECT "Id" FROM "MetadataFiles"
			WHERE "RelativePath" LIKE '_:\%'
			OR "RelativePath" LIKE '\%'
			OR "RelativePath" LIKE '/%')`
	}
	res, err := t.db.SQL.ExecContext(ctx, q)
	if err != nil {
		return fmt.Errorf("housekeeping: CleanupAbsolutePathMetadataFiles: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("housekeeping: CleanupAbsolutePathMetadataFiles: rowsAffected: %w", err)
	}
	if n > 0 {
		t.log.LogAttrs(ctx, slog.LevelInfo,
			"housekeeping: deleted MetadataFiles rows with absolute RelativePath",
			slog.Int64("rows", n))
	}
	return nil
}
