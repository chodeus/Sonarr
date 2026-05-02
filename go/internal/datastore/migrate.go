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
var migrationsFS embed.FS

// Migrate brings the schema up to head for the given DB. The migration
// ledger is embedded into the binary at build time and selected by dialect.
//
// Goose state is stored in goose's own table ("goose_db_version") in the
// same database. The C# side uses FluentMigrator's "VersionInfo" table
// instead; the Go port doesn't reuse that table because the version
// numbering is contiguous-from-1 in goose vs gappy in FluentMigrator (the
// Sonarr ledger has gaps at 5/15/18/21/57/82). Cross-cite
// analysis/migrations/ledger.md.
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
// embed path that holds its migration files. Today only main is wired;
// log will land alongside the C# log-DB ledger (6 migrations per
// analysis/migrations/ledger.md). New engines or roles must be added here
// AND have a sibling directory under migrations/.
func migrationDir(engine Engine, role Role) (dialect goose.Dialect, dir string, err error) {
	switch role {
	case RoleMain:
	case RoleLog:
		return "", "", fmt.Errorf("datastore: log role not yet wired (Phase 1 follow-up)")
	default:
		return "", "", fmt.Errorf("datastore: unknown role %q", role)
	}
	switch engine {
	case EngineSQLite:
		return goose.DialectSQLite3, "migrations/sqlite/main", nil
	case EnginePostgres:
		return goose.DialectPostgres, "migrations/postgres/main", nil
	default:
		return "", "", fmt.Errorf("datastore: unknown engine %q", engine)
	}
}
