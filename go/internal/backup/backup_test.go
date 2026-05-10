package backup_test

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/backup"
)

type stubFS struct {
	folders          map[string]bool
	writable         map[string]bool
	files            map[string][]string // folder -> files
	sizes            map[string]int64
	mtimes           map[string]time.Time
	textWritten      map[string]string
	copies           [][2]string
	moves            [][2]string
	deletedFiles     []string
	deletedFolders   []string
	emptyFolderHits  []string
	ensureFolderHits []string
	ensureErr        error
	emptyErr         error
	copyErr          error
	moveErr          error
}

func newStubFS() *stubFS {
	return &stubFS{
		folders:     map[string]bool{},
		writable:    map[string]bool{},
		files:       map[string][]string{},
		sizes:       map[string]int64{},
		mtimes:      map[string]time.Time{},
		textWritten: map[string]string{},
	}
}

func (s *stubFS) EnsureFolder(p string) error {
	s.ensureFolderHits = append(s.ensureFolderHits, p)
	if s.ensureErr != nil {
		return s.ensureErr
	}
	s.folders[p] = true
	if _, ok := s.writable[p]; !ok {
		s.writable[p] = true
	}
	return nil
}
func (s *stubFS) FolderExists(p string) bool   { return s.folders[p] }
func (s *stubFS) FolderWritable(p string) bool { return s.writable[p] }
func (s *stubFS) EmptyFolder(p string) error {
	s.emptyFolderHits = append(s.emptyFolderHits, p)
	if s.emptyErr != nil {
		return s.emptyErr
	}
	delete(s.files, p)
	return nil
}
func (s *stubFS) GetFiles(p string) ([]string, error) {
	out := append([]string(nil), s.files[p]...)
	sort.Strings(out)
	return out, nil
}
func (s *stubFS) GetFileSize(p string) (int64, error)          { return s.sizes[p], nil }
func (s *stubFS) FileGetLastWrite(p string) (time.Time, error) { return s.mtimes[p], nil }
func (s *stubFS) WriteAllText(p, contents string) error {
	s.textWritten[p] = contents
	return nil
}
func (s *stubFS) CopyFile(src, dst string) error {
	if s.copyErr != nil {
		return s.copyErr
	}
	s.copies = append(s.copies, [2]string{src, dst})
	return nil
}
func (s *stubFS) MoveFile(src, dst string, _ bool) error {
	if s.moveErr != nil {
		return s.moveErr
	}
	s.moves = append(s.moves, [2]string{src, dst})
	return nil
}
func (s *stubFS) DeleteFile(p string) error {
	s.deletedFiles = append(s.deletedFiles, p)
	return nil
}
func (s *stubFS) DeleteFolder(p string, _ bool) error {
	s.deletedFolders = append(s.deletedFolders, p)
	return nil
}

type stubArch struct {
	createdZips [][]string
	createdAt   string
	createErr   error
	extractErr  error
	extractAt   string
	extractFrom string
}

func (s *stubArch) CreateZip(zipPath string, files []string) error {
	if s.createErr != nil {
		return s.createErr
	}
	s.createdAt = zipPath
	s.createdZips = append(s.createdZips, append([]string(nil), files...))
	return nil
}
func (s *stubArch) Extract(zipPath, dest string) error {
	if s.extractErr != nil {
		return s.extractErr
	}
	s.extractFrom = zipPath
	s.extractAt = dest
	return nil
}

type stubDB struct {
	calls  int
	target string
	err    error
}

func (s *stubDB) BackupDatabase(_ context.Context, target string) error {
	s.calls++
	s.target = target
	return s.err
}

type stubCfg struct {
	folder    string
	retention int
}

func (s stubCfg) BackupFolder() string { return s.folder }
func (s stubCfg) BackupRetention() int { return s.retention }

type stubFolders struct {
	app, tmp, cfg, dbRestore string
}

func (s stubFolders) AppDataPath() string         { return s.app }
func (s stubFolders) TempPath() string            { return s.tmp }
func (s stubFolders) ConfigPath() string          { return s.cfg }
func (s stubFolders) DatabaseRestorePath() string { return s.dbRestore }

type stubVer struct{ v string }

func (s stubVer) Version() string { return s.v }

func newSvc(t *testing.T, fs backup.Filesystem, arch backup.Archiver, db backup.DatabaseBackup, cfg backup.Config, folders backup.AppFolders, ver backup.VersionInfo, clock func() time.Time) *backup.Service {
	t.Helper()
	opts := []backup.Option{}
	if clock != nil {
		opts = append(opts, backup.WithClock(clock))
	}
	svc, err := backup.NewService(fs, arch, db, cfg, folders, ver, nil, opts...)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

func TestType_String(t *testing.T) {
	t.Parallel()
	cases := []struct {
		t    backup.Type
		want string
	}{
		{backup.TypeScheduled, "scheduled"},
		{backup.TypeManual, "manual"},
		{backup.TypeUpdate, "update"},
		{backup.Type(99), "scheduled"},
	}
	for _, c := range cases {
		if got := c.t.String(); got != c.want {
			t.Errorf("%v: got %q want %q", c.t, got, c.want)
		}
	}
}

func TestNewService_NilGuards(t *testing.T) {
	t.Parallel()
	fs := newStubFS()
	arch := &stubArch{}
	db := &stubDB{}
	cfg := stubCfg{}
	folders := stubFolders{}
	ver := stubVer{}
	if _, err := backup.NewService(nil, arch, db, cfg, folders, ver, nil); err == nil {
		t.Error("nil fs")
	}
	if _, err := backup.NewService(fs, nil, db, cfg, folders, ver, nil); err == nil {
		t.Error("nil arch")
	}
	if _, err := backup.NewService(fs, arch, nil, cfg, folders, ver, nil); err == nil {
		t.Error("nil db")
	}
	if _, err := backup.NewService(fs, arch, db, nil, folders, ver, nil); err == nil {
		t.Error("nil cfg")
	}
	if _, err := backup.NewService(fs, arch, db, cfg, nil, ver, nil); err == nil {
		t.Error("nil folders")
	}
	if _, err := backup.NewService(fs, arch, db, cfg, folders, nil, nil); err == nil {
		t.Error("nil ver")
	}
}

func TestGetBackupFolder_RelativeResolvesAgainstAppData(t *testing.T) {
	t.Parallel()
	folders := stubFolders{app: "/var/sonarr", tmp: "/tmp"}
	svc := newSvc(t, newStubFS(), &stubArch{}, &stubDB{}, stubCfg{folder: "Backups"}, folders, stubVer{}, nil)
	if got, want := svc.GetBackupFolder(), "/var/sonarr/Backups"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestGetBackupFolder_AbsolutePassesThrough(t *testing.T) {
	t.Parallel()
	folders := stubFolders{app: "/var/sonarr", tmp: "/tmp"}
	svc := newSvc(t, newStubFS(), &stubArch{}, &stubDB{}, stubCfg{folder: "/srv/sonarr-bk"}, folders, stubVer{}, nil)
	if got, want := svc.GetBackupFolder(), "/srv/sonarr-bk"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestGetBackupFolderForType(t *testing.T) {
	t.Parallel()
	folders := stubFolders{app: "/var/sonarr", tmp: "/tmp"}
	svc := newSvc(t, newStubFS(), &stubArch{}, &stubDB{}, stubCfg{folder: "Backups"}, folders, stubVer{}, nil)
	cases := map[backup.Type]string{
		backup.TypeScheduled: "/var/sonarr/Backups/scheduled",
		backup.TypeManual:    "/var/sonarr/Backups/manual",
		backup.TypeUpdate:    "/var/sonarr/Backups/update",
	}
	for tp, want := range cases {
		if got := svc.GetBackupFolderForType(tp); got != want {
			t.Errorf("%v: got %q want %q", tp, got, want)
		}
	}
}

func TestBackup_HappyPath(t *testing.T) {
	t.Parallel()
	fs := newStubFS()
	arch := &stubArch{}
	db := &stubDB{}
	cfg := stubCfg{folder: "Backups", retention: 28}
	folders := stubFolders{app: "/var/sonarr", tmp: "/tmp", cfg: "/var/sonarr/config.xml"}
	clock := func() time.Time { return time.Date(2025, 6, 15, 12, 30, 45, 0, time.UTC) }
	svc := newSvc(t, fs, arch, db, cfg, folders, stubVer{v: "4.2.0"}, clock)

	// Pre-stage files in temp folder so the GetFiles call returns them.
	temp := "/tmp/sonarr_backup"
	fs.files[temp] = []string{filepath.Join(temp, "config.xml"), filepath.Join(temp, "sonarr.db"), filepath.Join(temp, "INFO")}

	got, err := svc.Backup(context.Background(), backup.TypeManual)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	wantPath := "/var/sonarr/Backups/manual/sonarr_backup_v4.2.0_2025.06.15_12.30.45.zip"
	if got != wantPath {
		t.Errorf("path: got %q want %q", got, wantPath)
	}
	if arch.createdAt != wantPath {
		t.Errorf("zip path: got %q want %q", arch.createdAt, wantPath)
	}
	// EnsureFolder hits temp and per-type folder.
	if !sliceContains(fs.ensureFolderHits, temp) || !sliceContains(fs.ensureFolderHits, "/var/sonarr/Backups/manual") {
		t.Errorf("ensureFolderHits: %+v", fs.ensureFolderHits)
	}
	// Manual backup must NOT prune (cleanupOldBackups not invoked
	// against the manual folder).
	if db.calls != 1 {
		t.Errorf("db backup calls: %d want 1", db.calls)
	}
	if db.target != temp {
		t.Errorf("db target: %q want %q", db.target, temp)
	}
	// Config copy.
	if len(fs.copies) != 1 || fs.copies[0][0] != "/var/sonarr/config.xml" || fs.copies[0][1] != "/tmp/sonarr_backup/config.xml" {
		t.Errorf("config copy: %+v", fs.copies)
	}
	// INFO file written with version + timestamp lines.
	infoPath := filepath.Join(temp, "INFO")
	got2 := fs.textWritten[infoPath]
	if !strings.Contains(got2, "v4.2.0") || !strings.Contains(got2, "2025-06-15 12:30:45") {
		t.Errorf("INFO contents: %q", got2)
	}
	// Journal stripped.
	if !sliceContains(fs.deletedFiles, filepath.Join(temp, "sonarr.db-journal")) {
		t.Errorf("journal not deleted: %+v", fs.deletedFiles)
	}
}

func TestBackup_FolderUnwritable(t *testing.T) {
	t.Parallel()
	fs := newStubFS()
	fs.writable["/var/sonarr/Backups/scheduled"] = false
	folders := stubFolders{app: "/var/sonarr", tmp: "/tmp", cfg: "/x.xml"}
	svc := newSvc(t, fs, &stubArch{}, &stubDB{}, stubCfg{folder: "Backups", retention: 28}, folders, stubVer{v: "1.0.0"}, nil)
	_, err := svc.Backup(context.Background(), backup.TypeScheduled)
	if err == nil {
		t.Fatal("want error")
	}
	var unwritable *backup.ErrBackupFolderUnwritable
	if !errors.As(err, &unwritable) {
		t.Errorf("want ErrBackupFolderUnwritable, got %v", err)
	}
}

func TestBackup_ScheduledTypePrunesOld(t *testing.T) {
	t.Parallel()
	fs := newStubFS()
	folders := stubFolders{app: "/var/sonarr", tmp: "/tmp", cfg: "/x.xml"}
	clock := func() time.Time { return time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC) }
	cfg := stubCfg{folder: "Backups", retention: 7}
	svc := newSvc(t, fs, &stubArch{}, &stubDB{}, cfg, folders, stubVer{v: "1.0.0"}, clock)

	// Pre-stage two backup files in the scheduled folder — one
	// 30-days old (should prune), one 1-day old (should keep).
	scheduledFolder := "/var/sonarr/Backups/scheduled"
	fs.folders[scheduledFolder] = true
	old := filepath.Join(scheduledFolder, "sonarr_backup_v0.9_2025.05.16_00.00.00.zip")
	fresh := filepath.Join(scheduledFolder, "sonarr_backup_v0.9_2025.06.14_00.00.00.zip")
	fs.files[scheduledFolder] = []string{old, fresh}
	fs.mtimes[old] = time.Date(2025, 5, 16, 0, 0, 0, 0, time.UTC)
	fs.mtimes[fresh] = time.Date(2025, 6, 14, 0, 0, 0, 0, time.UTC)

	if _, err := svc.Backup(context.Background(), backup.TypeScheduled); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if !sliceContains(fs.deletedFiles, old) {
		t.Errorf("old not pruned: %+v", fs.deletedFiles)
	}
	if sliceContains(fs.deletedFiles, fresh) {
		t.Errorf("fresh wrongly pruned: %+v", fs.deletedFiles)
	}
}

func TestBackup_ManualTypeSkipsPrune(t *testing.T) {
	t.Parallel()
	fs := newStubFS()
	folders := stubFolders{app: "/var/sonarr", tmp: "/tmp", cfg: "/x.xml"}
	clock := func() time.Time { return time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC) }
	cfg := stubCfg{folder: "Backups", retention: 7}
	svc := newSvc(t, fs, &stubArch{}, &stubDB{}, cfg, folders, stubVer{v: "1.0.0"}, clock)

	// Old backup in manual folder — should NOT prune.
	manualFolder := "/var/sonarr/Backups/manual"
	old := filepath.Join(manualFolder, "sonarr_backup_v0.9_2024.01.01_00.00.00.zip")
	fs.folders[manualFolder] = true
	fs.files[manualFolder] = []string{old}
	fs.mtimes[old] = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	if _, err := svc.Backup(context.Background(), backup.TypeManual); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if sliceContains(fs.deletedFiles, old) {
		t.Errorf("manual: ancient backup wrongly pruned: %+v", fs.deletedFiles)
	}
}

func TestGetBackups_AggregatesAcrossTypesSorted(t *testing.T) {
	t.Parallel()
	fs := newStubFS()
	folders := stubFolders{app: "/var/sonarr", tmp: "/tmp"}
	cfg := stubCfg{folder: "Backups", retention: 28}
	svc := newSvc(t, fs, &stubArch{}, &stubDB{}, cfg, folders, stubVer{v: "1.0"}, nil)

	scheduledFolder := "/var/sonarr/Backups/scheduled"
	manualFolder := "/var/sonarr/Backups/manual"
	a := filepath.Join(scheduledFolder, "sonarr_backup_2025.01.01_00.00.00.zip")
	b := filepath.Join(manualFolder, "sonarr_backup_2025.06.15_00.00.00.zip")
	c := filepath.Join(manualFolder, "not_a_backup.zip") // ignored by regex
	fs.folders[scheduledFolder] = true
	fs.folders[manualFolder] = true
	fs.files[scheduledFolder] = []string{a}
	fs.files[manualFolder] = []string{b, c}
	fs.sizes[a] = 100
	fs.sizes[b] = 200
	fs.mtimes[a] = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	fs.mtimes[b] = time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC)

	got, err := svc.GetBackups()
	if err != nil {
		t.Fatalf("GetBackups: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 backups, got %d: %+v", len(got), got)
	}
	if got[0].Name != "sonarr_backup_2025.01.01_00.00.00.zip" || got[0].Type != backup.TypeScheduled {
		t.Errorf("[0]: %+v", got[0])
	}
	if got[1].Name != "sonarr_backup_2025.06.15_00.00.00.zip" || got[1].Type != backup.TypeManual {
		t.Errorf("[1]: %+v", got[1])
	}
}

func TestGetBackups_LegacyNzbDronePrefix(t *testing.T) {
	t.Parallel()
	fs := newStubFS()
	folders := stubFolders{app: "/var/sonarr", tmp: "/tmp"}
	cfg := stubCfg{folder: "Backups", retention: 28}
	svc := newSvc(t, fs, &stubArch{}, &stubDB{}, cfg, folders, stubVer{v: "1.0"}, nil)
	folder := "/var/sonarr/Backups/scheduled"
	legacy := filepath.Join(folder, "nzbdrone_backup_2018.01.01_00.00.00.zip")
	fs.folders[folder] = true
	fs.files[folder] = []string{legacy}
	fs.mtimes[legacy] = time.Date(2018, 1, 1, 0, 0, 0, 0, time.UTC)

	got, err := svc.GetBackups()
	if err != nil {
		t.Fatalf("GetBackups: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("legacy nzbdrone_ prefix should match: %+v", got)
	}
}

func TestRestore_RawDbMoves(t *testing.T) {
	t.Parallel()
	fs := newStubFS()
	folders := stubFolders{tmp: "/tmp", dbRestore: "/var/sonarr/sonarr.db.restore"}
	svc := newSvc(t, fs, &stubArch{}, &stubDB{}, stubCfg{folder: "Backups", retention: 28}, folders, stubVer{}, nil)
	if err := svc.Restore("/some/path/sonarr.db"); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if len(fs.moves) != 1 || fs.moves[0][0] != "/some/path/sonarr.db" || fs.moves[0][1] != "/var/sonarr/sonarr.db.restore" {
		t.Errorf("moves: %+v", fs.moves)
	}
}

func TestRestore_ZipExtractsAndMovesPayloads(t *testing.T) {
	t.Parallel()
	fs := newStubFS()
	arch := &stubArch{}
	folders := stubFolders{tmp: "/tmp", cfg: "/var/sonarr/config.xml", dbRestore: "/var/sonarr/sonarr.db.restore"}
	svc := newSvc(t, fs, arch, &stubDB{}, stubCfg{folder: "Backups", retention: 28}, folders, stubVer{}, nil)

	// Pre-stage files the archiver would have extracted.
	tempPath := "/tmp/sonarr_backup_restore"
	fs.files[tempPath] = []string{
		filepath.Join(tempPath, "Config.xml"),
		filepath.Join(tempPath, "sonarr.db"),
		filepath.Join(tempPath, "leftover.txt"),
	}

	if err := svc.Restore("/some/backup.zip"); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if arch.extractFrom != "/some/backup.zip" || arch.extractAt != tempPath {
		t.Errorf("extract args: from=%q at=%q", arch.extractFrom, arch.extractAt)
	}
	if len(fs.moves) != 2 {
		t.Errorf("want 2 moves (config + db), got %+v", fs.moves)
	}
	if !sliceContains(fs.deletedFolders, tempPath) {
		t.Errorf("temp folder not deleted: %+v", fs.deletedFolders)
	}
}

func TestRestore_NoRecognisedPayload(t *testing.T) {
	t.Parallel()
	fs := newStubFS()
	arch := &stubArch{}
	folders := stubFolders{tmp: "/tmp", dbRestore: "/x/db.restore"}
	svc := newSvc(t, fs, arch, &stubDB{}, stubCfg{folder: "Backups"}, folders, stubVer{}, nil)
	tempPath := "/tmp/sonarr_backup_restore"
	fs.files[tempPath] = []string{filepath.Join(tempPath, "garbage.txt")}
	err := svc.Restore("/x/backup.zip")
	if err == nil {
		t.Fatal("want error")
	}
	var rerr *backup.ErrRestoreFailed
	if !errors.As(err, &rerr) {
		t.Errorf("want ErrRestoreFailed, got %v", err)
	}
}

func TestService_NilReceiver(t *testing.T) {
	t.Parallel()
	var s *backup.Service
	if got := s.GetBackupFolder(); got != "" {
		t.Errorf("nil GetBackupFolder: %q", got)
	}
	if _, err := s.Backup(context.Background(), backup.TypeManual); err == nil {
		t.Error("nil Backup")
	}
	if _, err := s.GetBackups(); err == nil {
		t.Error("nil GetBackups")
	}
	if err := s.Restore("/x"); err == nil {
		t.Error("nil Restore")
	}
}

func sliceContains(haystack []string, needle string) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}
