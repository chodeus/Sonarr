// Package backup is the Go port of `NzbDrone.Core.Backup` —
// `src/NzbDrone.Core/Backup/`. Closes queue [16] (slice 3.BM ships
// the core BackupService surface; sub-arc 16b will land the
// engine-specific MakeDatabaseBackup impls + the BackupCommand
// handler boot wire).
//
// Composition: a [Service] orchestrates four narrow seams —
// [Filesystem] (disk read/write/move/delete + ensure-folder helpers
// on top of [*disk.Provider]), [Archiver] (zip create + extract;
// production binds the package-level [ZipArchiver] using
// [archive/zip] from the Go stdlib), [DatabaseBackup] (engine-
// specific backup-this-DB-to-target-folder; production binding
// deferred to slice 16b), and [Config] (BackupFolder +
// BackupRetention from the config getters added this slice). Each
// is stub-friendly so unit tests don't need real disk, real archive,
// or real DB.
package backup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Type ports `BackupType` at `Backup/BackupCommand.cs:25-30`.
// Ordinals frozen — the wire format used in BackupCommand serializes
// to int.
type Type int

const (
	// TypeScheduled is the cron-driven nightly backup. Subject to
	// retention pruning. Mirrors C# `:27`.
	TypeScheduled Type = 0
	// TypeManual is a user-triggered ad-hoc backup. Exempt from
	// retention pruning (the user wants this one to stick).
	TypeManual Type = 1
	// TypeUpdate is the pre-update auto-backup. Subject to retention
	// pruning.
	TypeUpdate Type = 2
)

// String returns the lowercased enum name C# uses for the on-disk
// folder name (`scheduled` / `manual` / `update`). Mirrors the
// `backupType.ToString().ToLower()` at C# `BackupService.cs:186`.
func (t Type) String() string {
	switch t {
	case TypeManual:
		return "manual"
	case TypeUpdate:
		return "update"
	default:
		return "scheduled"
	}
}

// Backup ports the `Backup` struct at `Backup/Backup.cs:5-11`.
// Returned from [Service.GetBackups] for the API surface.
type Backup struct {
	Name string    `json:"name"`
	Type Type      `json:"type"`
	Size int64     `json:"size"`
	Time time.Time `json:"time"`
}

// FilenameRegex ports the C# `BackupFileRegex` at `BackupService.cs:42`.
// Matches both the legacy `nzbdrone_backup_…` format and the current
// `sonarr_backup_…` form, with optional `vN.N.N` version segment.
// Used by [Service.GetBackups] + [Service.CleanupOldBackups].
var FilenameRegex = regexp.MustCompile(`(?i)(nzbdrone|sonarr)_backup_(v[0-9.]+_)?[._0-9]+\.zip`)

// Filesystem narrows the disk-level operations [Service] needs.
// Production wiring binds an adapter over [*disk.Provider] +
// stdlib helpers for the methods *disk.Provider doesn't yet expose
// (EnsureFolder, EmptyFolder, WriteAllText, MoveFile, DeleteFile).
//
// Directory-walk fields return paths absolute to the supplied root.
type Filesystem interface {
	EnsureFolder(path string) error
	FolderExists(path string) bool
	FolderWritable(path string) bool
	EmptyFolder(path string) error
	GetFiles(path string) ([]string, error)
	GetFileSize(path string) (int64, error)
	FileGetLastWrite(path string) (time.Time, error)
	WriteAllText(path, contents string) error
	CopyFile(src, dst string) error
	MoveFile(src, dst string, overwrite bool) error
	DeleteFile(path string) error
	DeleteFolder(path string, recursive bool) error
}

// Archiver narrows zip create/extract to the surface [Service] uses.
// Production binds [ZipArchiver]; tests pass a stub.
type Archiver interface {
	CreateZip(zipPath string, sourceFiles []string) error
	Extract(zipPath, destFolder string) error
}

// DatabaseBackup narrows the engine-specific copy-this-DB-to-folder
// method. Slice 3.BM ships an interface-only surface; the production
// binding (sub-arc 16b) provides SQLite + Postgres impls. The C#
// tree uses SQLite's `BackupDatabase` API — the Postgres path will
// shell out to pg_dump in 16b.
//
// targetFolder is an absolute path to a writable scratch directory
// the [Service] has already EnsureFolder'd.
type DatabaseBackup interface {
	BackupDatabase(ctx context.Context, targetFolder string) error
}

// Config narrows [config.Provider] to the two backup keys. Both
// have safe defaults so a fresh deployment doesn't need explicit
// configuration.
type Config interface {
	BackupFolder() string
	BackupRetention() int
}

// AppFolders narrows the [AppFolderInfo] surface to the four paths
// the BackupService reaches for. Production binds an adapter over
// [appenv.AppFolderInfo].
type AppFolders interface {
	// AppDataPath returns the root of Sonarr's app-data folder.
	// Used to resolve a relative [Config.BackupFolder] into an
	// absolute path (mirrors C# `BackupService.cs:181`).
	AppDataPath() string
	// TempPath returns the OS temp folder Sonarr is allowed to use.
	// The backup pipeline scopes a `sonarr_backup` subfolder of this
	// for staging and a `sonarr_backup_restore` subfolder for the
	// extract-on-restore arm.
	TempPath() string
	// ConfigPath returns the absolute path to `config.xml` the
	// [Service.Backup] arm copies into the backup zip. The Restore
	// arm overwrites this file when it sees a `Config.xml` entry in
	// the archive.
	ConfigPath() string
	// DatabaseRestorePath returns the absolute path the Restore arm
	// moves a candidate `.db` blob to so the next startup picks it
	// up. Mirrors C# `IAppFolderInfo.GetDatabaseRestore()`.
	DatabaseRestorePath() string
}

// VersionInfo narrows the [BuildInfo.Version] read C# uses to stamp
// the backup filename + the INFO version file. Matches the
// build-time-stamped semver string ("4.0.16" etc.); production
// binding pulls from [internal/buildinfo].
type VersionInfo interface {
	Version() string
}

// Service ports `BackupService` at `Backup/BackupService.cs:29-260`.
// Goroutine-safe: stateless after construction (the C# tree caches
// `_backupTempFolder` once at construction; the Go-port mirrors).
type Service struct {
	fs       Filesystem
	archiver Archiver
	dbBackup DatabaseBackup
	cfg      Config
	folders  AppFolders
	version  VersionInfo
	clock    func() time.Time
	log      *slog.Logger

	tempFolder string
}

// Option configures a [Service] at construction.
type Option func(*Service)

// WithClock overrides the wall-clock source. Default [time.Now].
// Tests inject a fixed clock so the timestamp encoded in the backup
// filename is reproducible.
func WithClock(c func() time.Time) Option {
	return func(s *Service) { s.clock = c }
}

// NewService binds the seven narrow deps. nil-required-dep returns a
// typed error.
func NewService(
	fs Filesystem,
	archiver Archiver,
	dbBackup DatabaseBackup,
	cfg Config,
	folders AppFolders,
	version VersionInfo,
	log *slog.Logger,
	opts ...Option,
) (*Service, error) {
	if fs == nil {
		return nil, errors.New("backup: NewService: fs is nil")
	}
	if archiver == nil {
		return nil, errors.New("backup: NewService: archiver is nil")
	}
	if dbBackup == nil {
		return nil, errors.New("backup: NewService: dbBackup is nil")
	}
	if cfg == nil {
		return nil, errors.New("backup: NewService: cfg is nil")
	}
	if folders == nil {
		return nil, errors.New("backup: NewService: folders is nil")
	}
	if version == nil {
		return nil, errors.New("backup: NewService: version is nil")
	}
	if log == nil {
		log = slog.Default()
	}
	s := &Service{
		fs:         fs,
		archiver:   archiver,
		dbBackup:   dbBackup,
		cfg:        cfg,
		folders:    folders,
		version:    version,
		clock:      time.Now,
		log:        log,
		tempFolder: filepath.Join(folders.TempPath(), "sonarr_backup"),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

// GetBackupFolder ports the parameterless overload at
// `BackupService.cs:172-182`. Resolves the [Config.BackupFolder]
// string against [AppFolders.AppDataPath] when relative.
func (s *Service) GetBackupFolder() string {
	if s == nil {
		return ""
	}
	folder := s.cfg.BackupFolder()
	if filepath.IsAbs(folder) {
		return folder
	}
	return filepath.Join(s.folders.AppDataPath(), folder)
}

// GetBackupFolderForType ports the `GetBackupFolder(BackupType)`
// overload at `:184-187`. Each type lives in its own subfolder
// keyed by the lowercased enum name (matches the C#
// `backupType.ToString().ToLower()` at `:186`).
//
// Renamed from C# `GetBackupFolder` to avoid Go's no-overload
// limitation. Same behaviour.
func (s *Service) GetBackupFolderForType(t Type) string {
	if s == nil {
		return ""
	}
	return filepath.Join(s.GetBackupFolder(), t.String())
}

// Backup ports `BackupService.Backup(BackupType)` at `:65-102`.
//
//  1. Ensures the per-type backup folder + scratch temp folder both
//     exist (creates with Sonarr's default permissions if missing);
//  2. fails with [ErrBackupFolderUnwritable] if the per-type folder
//     isn't writable (matches C# UnauthorizedAccessException);
//  3. wipes the scratch folder (Cleanup);
//  4. on Scheduled / Update types, prunes backups older than
//     [Config.BackupRetention] days (CleanupOldBackups);
//  5. copies the config file + DB into the scratch folder, writes
//     the INFO file with version + timestamp;
//  6. zips the scratch folder contents into
//     `sonarr_backup_v<ver>_<yyyy.MM.dd_HH.mm.ss>.zip` inside the
//     per-type folder.
//
// Returns the absolute path of the created zip on success.
func (s *Service) Backup(ctx context.Context, t Type) (string, error) {
	if s == nil {
		return "", errors.New("backup: Backup on nil receiver")
	}
	s.log.Info("backup: starting", "type", t.String())

	folder := s.GetBackupFolderForType(t)
	if err := s.fs.EnsureFolder(s.tempFolder); err != nil {
		return "", fmt.Errorf("backup: ensure temp: %w", err)
	}
	if err := s.fs.EnsureFolder(folder); err != nil {
		return "", fmt.Errorf("backup: ensure %s: %w", folder, err)
	}
	if !s.fs.FolderWritable(folder) {
		return "", &ErrBackupFolderUnwritable{Path: folder}
	}

	now := s.clock()
	filename := fmt.Sprintf("sonarr_backup_v%s_%s.zip", s.version.Version(), now.Format("2006.01.02_15.04.05"))
	backupPath := filepath.Join(folder, filename)

	if err := s.cleanupTempFolder(); err != nil {
		return "", err
	}
	if t != TypeManual {
		if err := s.cleanupOldBackups(t); err != nil {
			s.log.Warn("backup: cleanup-old failed", "err", err)
		}
	}
	if err := s.backupConfigFile(); err != nil {
		return "", err
	}
	if err := s.backupDatabase(ctx); err != nil {
		return "", err
	}
	if err := s.createVersionInfo(now); err != nil {
		return "", err
	}

	// Strip the SQLite journal that the DB-backup pass may have
	// left behind. Mirrors C# `:97`.
	_ = s.fs.DeleteFile(filepath.Join(s.tempFolder, "sonarr.db-journal"))

	files, err := s.fs.GetFiles(s.tempFolder)
	if err != nil {
		return "", fmt.Errorf("backup: list temp files: %w", err)
	}
	sort.Strings(files)
	if err := s.archiver.CreateZip(backupPath, files); err != nil {
		return "", fmt.Errorf("backup: create zip: %w", err)
	}
	s.log.Info("backup: complete", "path", backupPath)
	return backupPath, nil
}

// GetBackups ports `:104-125`. Walks each per-type folder looking
// for filenames matching [FilenameRegex]; returns the union sorted
// by Time ascending.
func (s *Service) GetBackups() ([]Backup, error) {
	if s == nil {
		return nil, errors.New("backup: GetBackups on nil receiver")
	}
	out := []Backup{}
	for _, t := range []Type{TypeScheduled, TypeManual, TypeUpdate} {
		folder := s.GetBackupFolderForType(t)
		if !s.fs.FolderExists(folder) {
			continue
		}
		files, err := s.backupFiles(folder)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			size, err := s.fs.GetFileSize(f)
			if err != nil {
				s.log.Debug("backup: GetFileSize failed; skipping", "path", f, "err", err)
				continue
			}
			modTime, err := s.fs.FileGetLastWrite(f)
			if err != nil {
				s.log.Debug("backup: FileGetLastWrite failed; skipping", "path", f, "err", err)
				continue
			}
			out = append(out, Backup{
				Name: filepath.Base(f),
				Type: t,
				Size: size,
				Time: modTime,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Time.Before(out[j].Time)
	})
	return out, nil
}

// Restore ports `:127-170`.
//
// When backupFileName ends in ".zip", extracts to a scratch folder
// and copies/moves the recognised payloads into place:
//   - `Config.xml` (case-insensitive) → [AppFolders.ConfigPath];
//   - `nzbdrone.db` / `sonarr.db` (case-insensitive) →
//     [AppFolders.DatabaseRestorePath].
//
// At least one of those must be present, else returns
// [ErrRestoreFailed]. The scratch folder is recursively deleted on
// success.
//
// When backupFileName does NOT end in ".zip", treats it as a raw
// `.db` file and moves it directly to DatabaseRestorePath
// (mirrors C# `:169`).
func (s *Service) Restore(backupFileName string) error {
	if s == nil {
		return errors.New("backup: Restore on nil receiver")
	}
	if !strings.HasSuffix(strings.ToLower(backupFileName), ".zip") {
		if err := s.fs.MoveFile(backupFileName, s.folders.DatabaseRestorePath(), true); err != nil {
			return fmt.Errorf("backup: restore raw db: %w", err)
		}
		return nil
	}
	tempPath := filepath.Join(s.folders.TempPath(), "sonarr_backup_restore")
	if err := s.fs.EnsureFolder(tempPath); err != nil {
		return fmt.Errorf("backup: ensure restore temp: %w", err)
	}
	if err := s.archiver.Extract(backupFileName, tempPath); err != nil {
		return fmt.Errorf("backup: extract: %w", err)
	}
	files, err := s.fs.GetFiles(tempPath)
	if err != nil {
		return fmt.Errorf("backup: list extracted files: %w", err)
	}
	restoredFile := false
	for _, f := range files {
		base := filepath.Base(f)
		switch strings.ToLower(base) {
		case "config.xml":
			if err := s.fs.MoveFile(f, s.folders.ConfigPath(), true); err != nil {
				return fmt.Errorf("backup: move config.xml: %w", err)
			}
			restoredFile = true
		case "nzbdrone.db", "sonarr.db":
			if err := s.fs.MoveFile(f, s.folders.DatabaseRestorePath(), true); err != nil {
				return fmt.Errorf("backup: move db: %w", err)
			}
			restoredFile = true
		}
	}
	if !restoredFile {
		return &ErrRestoreFailed{Reason: "no recognised payload (Config.xml / sonarr.db / nzbdrone.db) in archive"}
	}
	if err := s.fs.DeleteFolder(tempPath, true); err != nil {
		s.log.Debug("backup: cleanup restore temp failed", "path", tempPath, "err", err)
	}
	return nil
}

// cleanupTempFolder ports the C# `Cleanup` private at `:189-195`.
// EmptyFolder when the temp folder exists; no-op otherwise.
func (s *Service) cleanupTempFolder() error {
	if !s.fs.FolderExists(s.tempFolder) {
		return nil
	}
	if err := s.fs.EmptyFolder(s.tempFolder); err != nil {
		return fmt.Errorf("backup: empty temp folder: %w", err)
	}
	return nil
}

// cleanupOldBackups ports `:228-247`. Deletes any matching backup
// in the per-type folder whose mtime + retention-days has passed.
// retention is read fresh each call so an admin tweaking the
// setting takes effect on the next backup.
func (s *Service) cleanupOldBackups(t Type) error {
	retention := s.cfg.BackupRetention()
	folder := s.GetBackupFolderForType(t)
	if !s.fs.FolderExists(folder) {
		return nil
	}
	files, err := s.backupFiles(folder)
	if err != nil {
		return err
	}
	cutoff := s.clock().UTC().Add(-time.Duration(retention) * 24 * time.Hour)
	for _, f := range files {
		mtime, err := s.fs.FileGetLastWrite(f)
		if err != nil {
			s.log.Debug("backup: cleanup mtime read failed; skipping", "path", f, "err", err)
			continue
		}
		if mtime.UTC().Before(cutoff) {
			s.log.Debug("backup: deleting old", "path", f)
			if err := s.fs.DeleteFile(f); err != nil {
				s.log.Warn("backup: delete old failed", "path", f, "err", err)
			}
		}
	}
	return nil
}

// backupConfigFile ports `:207-215`. TransferFile-copies the live
// config.xml into the scratch folder. The C# tree calls
// IDiskTransferService.TransferFile(Copy); the Go-port collapses
// to [Filesystem.CopyFile].
func (s *Service) backupConfigFile() error {
	src := s.folders.ConfigPath()
	if src == "" {
		return errors.New("backup: empty config path")
	}
	dst := filepath.Join(s.tempFolder, filepath.Base(src))
	if err := s.fs.CopyFile(src, dst); err != nil {
		return fmt.Errorf("backup: copy config: %w", err)
	}
	return nil
}

// backupDatabase ports `:197-205`. Dispatches to the
// engine-specific [DatabaseBackup] binding. The C# tree's
// `if (DatabaseType == SQLite)` gate is collapsed into the
// production binding — the Postgres impl is a no-op (with a
// debug-log) since pg_dump is shipping in slice 16b.
func (s *Service) backupDatabase(ctx context.Context) error {
	return s.dbBackup.BackupDatabase(ctx, s.tempFolder)
}

// createVersionInfo ports `:217-226`. Writes the INFO file with two
// lines — `vX.Y.Z` and `yyyy-MM-dd HH:mm:ss`. Matches the C# layout
// exactly; user-supplied restore tooling parses both lines.
func (s *Service) createVersionInfo(now time.Time) error {
	contents := fmt.Sprintf("v%s\n%s\n", s.version.Version(), now.Format("2006-01-02 15:04:05"))
	return s.fs.WriteAllText(filepath.Join(s.tempFolder, "INFO"), contents)
}

// backupFiles ports `:249-254`. Lists the supplied folder, filters
// to filenames matching [FilenameRegex].
func (s *Service) backupFiles(folder string) ([]string, error) {
	files, err := s.fs.GetFiles(folder)
	if err != nil {
		return nil, fmt.Errorf("backup: list %q: %w", folder, err)
	}
	out := files[:0:0]
	for _, f := range files {
		if FilenameRegex.MatchString(filepath.Base(f)) {
			out = append(out, f)
		}
	}
	return out, nil
}

// ErrBackupFolderUnwritable signals the configured backup folder
// can't be written to (mirrors C# UnauthorizedAccessException at
// `:76`). Surfaced to the caller so the API layer can render a
// 500 with a useful message.
type ErrBackupFolderUnwritable struct {
	Path string
}

func (e *ErrBackupFolderUnwritable) Error() string {
	return fmt.Sprintf("backup: folder %q is not writable", e.Path)
}

// ErrRestoreFailed signals the Restore arm couldn't find any
// recognised payload in the supplied archive. Mirrors the C#
// RestoreBackupFailedException at
// `Backup/RestoreBackupFailedException.cs`.
type ErrRestoreFailed struct {
	Reason string
}

func (e *ErrRestoreFailed) Error() string {
	return fmt.Sprintf("backup: restore failed: %s", e.Reason)
}
