package datastore

import (
	"context"
	"embed"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/sqlite/main/*.sql
//go:embed migrations/postgres/main/*.sql
//go:embed migrations/sqlite/log/*.sql
//go:embed migrations/postgres/log/*.sql
var migrationsFS embed.FS

// Migrate brings the schema up to head for the given DB. The migration
// ledger is embedded into the binary at build time and selected by dialect.
//
// Goose state is stored in goose's own table ("goose_db_version") in the
// same database. The C# side uses FluentMigrator's "VersionInfo" table
// instead; the Go port doesn't reuse that table because the version
// numbering style differs — see analysis/migrations/ledger.md for the
// actual numbering layout (the Sonarr ledger has 8 missing version
// numbers and one empty no-op file across the 0..230 range).
func Migrate(ctx context.Context, db *DB) error {
	if db == nil || db.SQL == nil {
		return fmt.Errorf("datastore: nil DB")
	}

	dialect, dir, err := migrationDir(db.Engine, db.Role)
	if err != nil {
		return err
	}

	// goose v3's package-global state is the documented API; we wrap it
	// in a *goose.Provider where the v3 API supports it, but for simple
	// "up to head" runs the package-level functions are fine. The
	// Provider variant becomes useful when we need per-DB lock files
	// or out-of-band rollbacks.
	sub, err := fs.Sub(migrationsFS, dir)
	if err != nil {
		return fmt.Errorf("datastore: locate migrations dir %q: %w", dir, err)
	}
	provider, err := goose.NewProvider(dialect, db.SQL, sub)
	if err != nil {
		return fmt.Errorf("datastore: goose provider for %s/%s: %w", db.Engine, db.Role, err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("datastore: migrate up %s/%s: %w", db.Engine, db.Role, err)
	}
	return nil
}

// MigrateTo runs migrations forward to a specific target version. Useful in
// tests for asserting the schema shape at intermediate checkpoints. Calling
// MigrateTo with a version equal to or below the current version is a no-op
// and is NOT a rollback — Sonarr migrations are one-way (every migration's
// Down is "restore from backup"). See analysis/migrations/ledger.md.
func MigrateTo(ctx context.Context, db *DB, version int64) error {
	if db == nil || db.SQL == nil {
		return fmt.Errorf("datastore: nil DB")
	}
	dialect, dir, err := migrationDir(db.Engine, db.Role)
	if err != nil {
		return err
	}
	sub, err := fs.Sub(migrationsFS, dir)
	if err != nil {
		return fmt.Errorf("datastore: locate migrations dir %q: %w", dir, err)
	}
	provider, err := goose.NewProvider(dialect, db.SQL, sub)
	if err != nil {
		return fmt.Errorf("datastore: goose provider for %s/%s: %w", db.Engine, db.Role, err)
	}
	if _, err := provider.UpTo(ctx, version); err != nil {
		return fmt.Errorf("datastore: migrate up to %d %s/%s: %w", version, db.Engine, db.Role, err)
	}
	return nil
}

// CurrentVersion returns the highest migration version recorded by goose.
// Returns 0 if goose hasn't been run yet.
func CurrentVersion(ctx context.Context, db *DB) (int64, error) {
	if db == nil || db.SQL == nil {
		return 0, fmt.Errorf("datastore: nil DB")
	}
	dialect, dir, err := migrationDir(db.Engine, db.Role)
	if err != nil {
		return 0, err
	}
	sub, err := fs.Sub(migrationsFS, dir)
	if err != nil {
		return 0, fmt.Errorf("datastore: locate migrations dir %q: %w", dir, err)
	}
	provider, err := goose.NewProvider(dialect, db.SQL, sub)
	if err != nil {
		return 0, fmt.Errorf("datastore: goose provider for %s/%s: %w", db.Engine, db.Role, err)
	}
	v, err := provider.GetDBVersion(ctx)
	if err != nil {
		return 0, fmt.Errorf("datastore: goose version %s/%s: %w", db.Engine, db.Role, err)
	}
	return v, nil
}

// migrationDir maps an (engine, role) pair to the goose dialect and the
// embed path that holds its migration files. The log role's ledger is
// sparser than main — only the migrations whose C# equivalent overrode
// LogDbUpgrade() (per analysis/migrations/ledger.md: 1, 6, 64, 141, 146,
// 188 — six total). The log DB lives in a separate physical database
// from main; LogDbEnabled=false in C# config skips it entirely (per
// subsystems/system.md), so callers that want to disable logging just
// don't open a log-role DB.
func migrationDir(engine Engine, role Role) (dialect goose.Dialect, dir string, err error) {
	var sub string
	switch role {
	case RoleMain:
		sub = "main"
	case RoleLog:
		sub = "log"
	default:
		return "", "", fmt.Errorf("datastore: unknown role %q", role)
	}
	switch engine {
	case EngineSQLite:
		return goose.DialectSQLite3, "migrations/sqlite/" + sub, nil
	case EnginePostgres:
		return goose.DialectPostgres, "migrations/postgres/" + sub, nil
	default:
		return "", "", fmt.Errorf("datastore: unknown engine %q", engine)
	}
}
