package backup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

// PostgresBackup is the Postgres-engine [DatabaseBackup] impl.
// Shells out to `pg_dump` to produce a custom-format dump that
// `pg_restore` can replay end-to-end on a fresh cluster. The C#
// tree only ever ran on SQLite (the `if DatabaseType == SQLite`
// gate at `BackupService.cs:199` short-circuits otherwise); the
// Go-port spike runs on Postgres so backup/restore parity required
// a per-engine impl.
//
// pg_dump is invoked with -F c (custom binary format) so the output
// is single-file + restorable on schema mismatches via pg_restore's
// selective restore. PGPASSWORD is supplied via env when Password
// is set.
type PostgresBackup struct {
	// Binary is the pg_dump binary path. Empty defaults to "pg_dump"
	// (resolved via PATH at run time). Override at construction for
	// non-default installs.
	Binary string

	// Connection details — the consumer passes the same pieces the
	// datastore.Config carries.
	Host     string
	Port     int
	User     string
	Password string
	Database string

	// TargetFilename is the basename to write inside targetFolder.
	// Defaults to "sonarr.dump" when empty.
	TargetFilename string
}

// NewPostgresBackup binds the connection details. Database and
// User are required; Host/Port default to libpq's defaults when
// empty.
func NewPostgresBackup(host string, port int, user, password, database string) (*PostgresBackup, error) {
	if database == "" {
		return nil, errors.New("backup.PostgresBackup: empty database")
	}
	if user == "" {
		return nil, errors.New("backup.PostgresBackup: empty user")
	}
	return &PostgresBackup{
		Host:           host,
		Port:           port,
		User:           user,
		Password:       password,
		Database:       database,
		TargetFilename: "sonarr.dump",
	}, nil
}

// BackupDatabase invokes pg_dump.
func (b *PostgresBackup) BackupDatabase(ctx context.Context, targetFolder string) error {
	if b == nil {
		return errors.New("backup.PostgresBackup: nil receiver")
	}
	if targetFolder == "" {
		return errors.New("backup.PostgresBackup: empty targetFolder")
	}
	name := b.TargetFilename
	if name == "" {
		name = "sonarr.dump"
	}
	target := filepath.Join(targetFolder, name)
	if err := removeIfExists(target); err != nil {
		return fmt.Errorf("backup.PostgresBackup: pre-clean: %w", err)
	}
	binary := b.Binary
	if binary == "" {
		binary = "pg_dump"
	}
	args := []string{
		"-F", "c",
		"-f", target,
		"-d", b.Database,
		"-U", b.User,
	}
	if b.Host != "" {
		args = append(args, "-h", b.Host)
	}
	if b.Port != 0 {
		args = append(args, "-p", strconv.Itoa(b.Port))
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = append(os.Environ(), "PGCLIENTENCODING=UTF8")
	if b.Password != "" {
		cmd.Env = append(cmd.Env, "PGPASSWORD="+b.Password)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("backup.PostgresBackup: pg_dump %v: %w (stderr=%q)", args, err, stderr.String())
	}
	return nil
}

// removeIfExists is shared by the SQLite + Postgres impls. Returns
// nil when the path doesn't exist.
func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
