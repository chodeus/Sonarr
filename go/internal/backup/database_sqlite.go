package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
)

// SQLiteBackup is the SQLite-engine [DatabaseBackup] impl. Uses
// SQLite's `VACUUM INTO` SQL command (available since 3.27) to
// produce a consistent on-disk copy of the source database without
// dragging in cgo or external tools. The C# tree calls
// `SQLiteConnection.BackupDatabase` (an API-level backup); the
// Go-port `VACUUM INTO` produces an equivalent dense copy and also
// resets the journal mode to TRUNCATE on the output (the C# version
// fights the WAL-vs-truncate handshake at MakeDatabaseBackup.cs:35).
//
// VACUUM INTO refuses to overwrite an existing file, so any pre-
// existing target is removed first.
type SQLiteBackup struct {
	// SQL is the live database/sql handle for the source SQLite DB.
	// Mirrors the C# `IMainDatabase` injection at
	// `BackupService.cs:31`.
	SQL *sql.DB
	// TargetFilename is the basename to write inside targetFolder.
	// Defaults to "sonarr.db" when empty (matches the C# bundled
	// filename via `Path.GetFileName(backupConnectionStringBuilder.DataSource)`).
	TargetFilename string
}

// NewSQLiteBackup binds the source DB. Required dep — nil-SQL
// returns a typed error.
func NewSQLiteBackup(db *sql.DB) (*SQLiteBackup, error) {
	if db == nil {
		return nil, errors.New("backup.SQLiteBackup: nil source DB")
	}
	return &SQLiteBackup{SQL: db, TargetFilename: "sonarr.db"}, nil
}

// BackupDatabase runs `VACUUM INTO targetFolder/<TargetFilename>`.
// The target path must be on the same filesystem (SQLite restriction);
// callers stage to the temp folder used by [Service].
func (b *SQLiteBackup) BackupDatabase(ctx context.Context, targetFolder string) error {
	if b == nil {
		return errors.New("backup.SQLiteBackup: nil receiver")
	}
	if targetFolder == "" {
		return errors.New("backup.SQLiteBackup: empty targetFolder")
	}
	name := b.TargetFilename
	if name == "" {
		name = "sonarr.db"
	}
	target := filepath.Join(targetFolder, name)
	// VACUUM INTO refuses to overwrite an existing target. Strip
	// any prior copy via the SQLite "remove file" pragma surrogate
	// — the C# code does this implicitly via the
	// `SQLiteConnection.BackupDatabase` call which truncates.
	// Using os.Remove keeps the one-driver dependency.
	if err := removeIfExists(target); err != nil {
		return fmt.Errorf("backup.SQLiteBackup: pre-clean: %w", err)
	}
	if _, err := b.SQL.ExecContext(ctx, "VACUUM INTO ?", target); err != nil {
		return fmt.Errorf("backup.SQLiteBackup: VACUUM INTO %q: %w", target, err)
	}
	return nil
}
