package backup_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"sonarr.tv/sonarr/internal/backup"
)

func TestSQLiteBackup_NilSourceRejected(t *testing.T) {
	t.Parallel()
	if _, err := backup.NewSQLiteBackup(nil); err == nil {
		t.Fatal("want error")
	}
}

func TestSQLiteBackup_VacuumIntoProducesCopy(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "source.db")
	db, err := sql.Open("sqlite", src)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE x (n INTEGER); INSERT INTO x VALUES (42);`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	bkp, err := backup.NewSQLiteBackup(db)
	if err != nil {
		t.Fatalf("NewSQLiteBackup: %v", err)
	}
	target := filepath.Join(tmp, "out")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := bkp.BackupDatabase(context.Background(), target); err != nil {
		t.Fatalf("BackupDatabase: %v", err)
	}
	// Verify the copy exists and contains our row.
	copyPath := filepath.Join(target, "sonarr.db")
	if _, err := os.Stat(copyPath); err != nil {
		t.Fatalf("backup file missing: %v", err)
	}
	verify, err := sql.Open("sqlite", copyPath)
	if err != nil {
		t.Fatalf("open copy: %v", err)
	}
	defer func() { _ = verify.Close() }()
	var n int
	if err := verify.QueryRow("SELECT n FROM x").Scan(&n); err != nil {
		t.Fatalf("query copy: %v", err)
	}
	if n != 42 {
		t.Errorf("copy data: got %d want 42", n)
	}
}

func TestSQLiteBackup_OverwritesExistingTarget(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "source.db")
	db, err := sql.Open("sqlite", src)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE x (n INTEGER);`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	target := filepath.Join(tmp, "out")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-stage a stale "sonarr.db" — VACUUM INTO would refuse without the
	// pre-clean.
	stale := filepath.Join(target, "sonarr.db")
	if err := os.WriteFile(stale, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	bkp, _ := backup.NewSQLiteBackup(db)
	if err := bkp.BackupDatabase(context.Background(), target); err != nil {
		t.Fatalf("BackupDatabase: %v", err)
	}
}

func TestSQLiteBackup_NilReceiverErr(t *testing.T) {
	t.Parallel()
	var b *backup.SQLiteBackup
	if err := b.BackupDatabase(context.Background(), "/x"); err == nil {
		t.Fatal("want error")
	}
}

func TestSQLiteBackup_EmptyTargetErr(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	db, _ := sql.Open("sqlite", filepath.Join(tmp, "src.db"))
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE x (n INTEGER)`); err != nil {
		t.Fatal(err)
	}
	b, _ := backup.NewSQLiteBackup(db)
	if err := b.BackupDatabase(context.Background(), ""); err == nil {
		t.Fatal("want error")
	}
}

func TestPostgresBackup_NilGuards(t *testing.T) {
	t.Parallel()
	if _, err := backup.NewPostgresBackup("", 0, "u", "", ""); err == nil {
		t.Fatal("empty database should error")
	}
	if _, err := backup.NewPostgresBackup("", 0, "", "", "db"); err == nil {
		t.Fatal("empty user should error")
	}
}

func TestPostgresBackup_NilReceiver(t *testing.T) {
	t.Parallel()
	var b *backup.PostgresBackup
	if err := b.BackupDatabase(context.Background(), "/x"); err == nil {
		t.Fatal("want error")
	}
}

func TestPostgresBackup_EmptyTarget(t *testing.T) {
	t.Parallel()
	b, _ := backup.NewPostgresBackup("h", 5432, "u", "p", "d")
	if err := b.BackupDatabase(context.Background(), ""); err == nil {
		t.Fatal("want error")
	}
}

func TestPostgresBackup_MissingBinaryErrPropagates(t *testing.T) {
	t.Parallel()
	b, _ := backup.NewPostgresBackup("h", 5432, "u", "p", "d")
	b.Binary = "/no/such/binary"
	target := t.TempDir()
	err := b.BackupDatabase(context.Background(), target)
	if err == nil {
		t.Fatal("missing binary should surface as error")
	}
}
