package upgrademediafiles_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/series"
	"sonarr.tv/sonarr/internal/upgrademediafiles"
)

// --- stubs ---------------------------------------------------------

type stubRecycle struct {
	out  string
	err  error
	last struct {
		path, subfolder string
	}
	calls int
}

func (s *stubRecycle) DeleteFile(path, subfolder string) (string, error) {
	s.calls++
	s.last.path = path
	s.last.subfolder = subfolder
	return s.out, s.err
}

type stubDeleter struct {
	deleted []int
	reasons []episodefiles.DeleteMediaFileReason
	err     error
}

func (s *stubDeleter) Delete(_ context.Context, file *episodefiles.File, reason episodefiles.DeleteMediaFileReason) error {
	s.deleted = append(s.deleted, file.ID)
	s.reasons = append(s.reasons, reason)
	return s.err
}

type stubMover struct {
	moveCalls int
	copyCalls int
	moveErr   error
	copyErr   error
}

func (s *stubMover) MoveEpisodeFileWithLocal(_ context.Context, file *episodefiles.File, _ *episodeimport.LocalEpisode) (*episodefiles.File, error) {
	s.moveCalls++
	return file, s.moveErr
}

func (s *stubMover) CopyEpisodeFile(_ context.Context, file *episodefiles.File, _ *episodeimport.LocalEpisode) (*episodefiles.File, error) {
	s.copyCalls++
	return file, s.copyErr
}

type stubFiles struct {
	byID map[int]*episodefiles.File
	err  error
}

func (s *stubFiles) GetFile(_ context.Context, id int) (*episodefiles.File, error) {
	if s.err != nil {
		return nil, s.err
	}
	if v, ok := s.byID[id]; ok {
		return v, nil
	}
	return nil, nil
}

type stubFS struct {
	folders map[string]bool
	files   map[string]bool
}

func (s stubFS) FolderExists(p string) bool { return s.folders[p] }
func (s stubFS) FileExists(p string) bool   { return s.files[p] }

// helpers -----------------------------------------------

func ptr(i int) *int { return &i }

func mkSvc(t *testing.T, recycle upgrademediafiles.RecycleBinProvider, deleter upgrademediafiles.MediaFileDeleter, mover upgrademediafiles.EpisodeFileMover, files upgrademediafiles.EpisodeFileFinder, fs upgrademediafiles.Filesystem) *upgrademediafiles.Service {
	t.Helper()
	svc, err := upgrademediafiles.NewService(recycle, deleter, mover, files, fs, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

// --- constructor tests --------------------------------

func TestNewServiceRejectsRequiredNils(t *testing.T) {
	t.Parallel()
	if _, err := upgrademediafiles.NewService(nil, nil, &stubMover{}, &stubFiles{}, stubFS{}, nil); err == nil ||
		!strings.Contains(err.Error(), "nil MediaFileDeleter") {
		t.Errorf("nil deleter: %v", err)
	}
	if _, err := upgrademediafiles.NewService(nil, &stubDeleter{}, nil, &stubFiles{}, stubFS{}, nil); err == nil ||
		!strings.Contains(err.Error(), "nil EpisodeFileMover") {
		t.Errorf("nil mover: %v", err)
	}
	if _, err := upgrademediafiles.NewService(nil, &stubDeleter{}, &stubMover{}, nil, stubFS{}, nil); err == nil ||
		!strings.Contains(err.Error(), "nil EpisodeFileFinder") {
		t.Errorf("nil files: %v", err)
	}
	if _, err := upgrademediafiles.NewService(nil, &stubDeleter{}, &stubMover{}, &stubFiles{}, nil, nil); err == nil ||
		!strings.Contains(err.Error(), "nil Filesystem") {
		t.Errorf("nil fs: %v", err)
	}
}

func TestNewServiceNilRecycleAccepted(t *testing.T) {
	t.Parallel()
	svc, err := upgrademediafiles.NewService(nil, &stubDeleter{}, &stubMover{}, &stubFiles{}, stubFS{}, nil)
	if err != nil || svc == nil {
		t.Errorf("got (%+v, %v)", svc, err)
	}
}

func TestUpgradeNilReceiver(t *testing.T) {
	t.Parallel()
	var s *upgrademediafiles.Service
	_, err := s.UpgradeEpisodeFile(context.Background(), &episodefiles.File{}, &episodeimport.LocalEpisode{}, false)
	if err == nil {
		t.Errorf("expected error")
	}
}

func TestUpgradeNilFileOrLEErrors(t *testing.T) {
	t.Parallel()
	svc := mkSvc(t, nil, &stubDeleter{}, &stubMover{}, &stubFiles{}, stubFS{})
	if _, err := svc.UpgradeEpisodeFile(context.Background(), nil, &episodeimport.LocalEpisode{Series: &series.Series{}}, false); err == nil {
		t.Errorf("nil file: expected error")
	}
	if _, err := svc.UpgradeEpisodeFile(context.Background(), &episodefiles.File{}, nil, false); err == nil {
		t.Errorf("nil le: expected error")
	}
}

// --- happy paths --------------------------------------

func mkLE(t *testing.T) *episodeimport.LocalEpisode {
	t.Helper()
	le := episodeimport.NewLocalEpisode()
	le.Series = &series.Series{ID: 7, Path: "/library/Show"}
	le.Path = "/in/raw.mkv"
	le.Episodes = []episodes.Episode{
		{ID: 1, EpisodeFileID: ptr(101)},
		{ID: 2, EpisodeFileID: ptr(102)},
	}
	return le
}

func TestUpgradeNoExistingFilesGoesStraightToMove(t *testing.T) {
	t.Parallel()
	mover := &stubMover{}
	deleter := &stubDeleter{}
	fs := stubFS{folders: map[string]bool{"/library": true}}

	le := episodeimport.NewLocalEpisode()
	le.Series = &series.Series{ID: 7, Path: "/library/Show"}
	le.Path = "/in/x.mkv"
	le.Episodes = []episodes.Episode{
		{ID: 1, EpisodeFileID: nil}, // no existing file
	}

	svc := mkSvc(t, nil, deleter, mover, &stubFiles{}, fs)
	old, err := svc.UpgradeEpisodeFile(context.Background(), &episodefiles.File{}, le, false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(old) != 0 {
		t.Errorf("OldFiles = %d, want 0", len(old))
	}
	if mover.moveCalls != 1 {
		t.Errorf("moveCalls = %d, want 1", mover.moveCalls)
	}
	if mover.copyCalls != 0 {
		t.Errorf("copyCalls = %d, want 0", mover.copyCalls)
	}
}

func TestUpgradeRecyclesAndDeletesExistingFiles(t *testing.T) {
	t.Parallel()
	files := &stubFiles{byID: map[int]*episodefiles.File{
		101: {ID: 101, RelativePath: "Season 01/old1.mkv"},
		102: {ID: 102, RelativePath: "Season 01/old2.mkv"},
	}}
	fs := stubFS{
		folders: map[string]bool{"/library": true},
		files: map[string]bool{
			"/library/Show/Season 01/old1.mkv": true,
			"/library/Show/Season 01/old2.mkv": true,
		},
	}
	recycle := &stubRecycle{out: "/recycle/Show/Season 01/old1.mkv"}
	deleter := &stubDeleter{}
	mover := &stubMover{}

	svc := mkSvc(t, recycle, deleter, mover, files, fs)
	le := mkLE(t)

	old, err := svc.UpgradeEpisodeFile(context.Background(), &episodefiles.File{}, le, false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(old) != 2 {
		t.Fatalf("OldFiles = %d, want 2", len(old))
	}
	if recycle.calls != 2 {
		t.Errorf("recycle.calls = %d, want 2", recycle.calls)
	}
	if len(deleter.deleted) != 2 {
		t.Errorf("deleted = %v, want 2", deleter.deleted)
	}
	for _, r := range deleter.reasons {
		if r != episodefiles.ReasonUpgrade {
			t.Errorf("delete reason = %v, want Upgrade", r)
		}
	}
	// LocalEpisode.OldFiles populated.
	if len(le.OldFiles) != 2 {
		t.Errorf("le.OldFiles = %d, want 2", len(le.OldFiles))
	}
	if mover.moveCalls != 1 {
		t.Errorf("moveCalls = %d, want 1", mover.moveCalls)
	}
}

func TestUpgradeCopyOnlyUsesCopyMover(t *testing.T) {
	t.Parallel()
	files := &stubFiles{}
	fs := stubFS{folders: map[string]bool{"/library": true}}
	mover := &stubMover{}

	svc := mkSvc(t, nil, &stubDeleter{}, mover, files, fs)
	le := episodeimport.NewLocalEpisode()
	le.Series = &series.Series{Path: "/library/Show"}
	le.Path = "/in/x.mkv"

	_, err := svc.UpgradeEpisodeFile(context.Background(), &episodefiles.File{}, le, true)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if mover.copyCalls != 1 || mover.moveCalls != 0 {
		t.Errorf("got copy=%d move=%d, want copy=1 move=0", mover.copyCalls, mover.moveCalls)
	}
}

// --- root-folder check ---------------------------------

func TestUpgradeRootFolderMissingErrors(t *testing.T) {
	t.Parallel()
	files := &stubFiles{byID: map[int]*episodefiles.File{
		101: {ID: 101, RelativePath: "Season 01/old.mkv"},
	}}
	fs := stubFS{} // /library is NOT in folders → root missing
	mover := &stubMover{}

	svc := mkSvc(t, nil, &stubDeleter{}, mover, files, fs)
	le := mkLE(t)

	_, err := svc.UpgradeEpisodeFile(context.Background(), &episodefiles.File{}, le, false)
	if !errors.Is(err, episodeimport.ErrRootFolderNotFound) {
		t.Errorf("got %v, want ErrRootFolderNotFound", err)
	}
	// Mover must NOT have been called when root-folder check fails.
	if mover.moveCalls != 0 || mover.copyCalls != 0 {
		t.Errorf("mover should not have been called: move=%d copy=%d", mover.moveCalls, mover.copyCalls)
	}
}

func TestUpgradeRootFolderOnlyCheckedWhenExistingFiles(t *testing.T) {
	t.Parallel()
	// No existing files → root-folder check skipped → no error
	// despite stubFS missing the root.
	mover := &stubMover{}
	svc := mkSvc(t, nil, &stubDeleter{}, mover, &stubFiles{}, stubFS{})

	le := episodeimport.NewLocalEpisode()
	le.Series = &series.Series{Path: "/library/Show"}
	le.Path = "/in/x.mkv"
	// no episodes with EpisodeFileID set

	_, err := svc.UpgradeEpisodeFile(context.Background(), &episodefiles.File{}, le, false)
	if err != nil {
		t.Errorf("should not error when no existing files: %v", err)
	}
	if mover.moveCalls != 1 {
		t.Errorf("moveCalls = %d, want 1", mover.moveCalls)
	}
}

// --- file-missing-from-disk arm ------------------------

func TestUpgradeMissingFileFromDiskStillDeletesRow(t *testing.T) {
	t.Parallel()
	files := &stubFiles{byID: map[int]*episodefiles.File{
		101: {ID: 101, RelativePath: "Season 01/old.mkv"},
	}}
	fs := stubFS{folders: map[string]bool{"/library": true}} // file NOT in files
	recycle := &stubRecycle{}
	deleter := &stubDeleter{}
	mover := &stubMover{}

	svc := mkSvc(t, recycle, deleter, mover, files, fs)
	le := mkLE(t)
	le.Episodes = []episodes.Episode{
		{ID: 1, EpisodeFileID: ptr(101)},
	}

	old, err := svc.UpgradeEpisodeFile(context.Background(), &episodefiles.File{}, le, false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// File missing → no recycle call.
	if recycle.calls != 0 {
		t.Errorf("recycle.calls = %d, want 0 (file missing)", recycle.calls)
	}
	// But the EpisodeFile row IS still deleted (mirrors C#).
	if len(deleter.deleted) != 1 {
		t.Errorf("deleted = %v, want 1", deleter.deleted)
	}
	// OldFiles populated with empty RecycleBinPath.
	if len(old) != 1 || old[0].RecycleBinPath != "" {
		t.Errorf("old = %+v, want one entry with empty RecycleBinPath", old)
	}
}

// --- duplicate EpisodeFileID dedup ---------------------

func TestUpgradeDeduplicatesByEpisodeFileID(t *testing.T) {
	t.Parallel()
	files := &stubFiles{byID: map[int]*episodefiles.File{
		101: {ID: 101, RelativePath: "old.mkv"},
	}}
	fs := stubFS{
		folders: map[string]bool{"/library": true},
		files:   map[string]bool{"/library/Show/old.mkv": true},
	}
	recycle := &stubRecycle{}
	deleter := &stubDeleter{}
	mover := &stubMover{}

	svc := mkSvc(t, recycle, deleter, mover, files, fs)
	le := episodeimport.NewLocalEpisode()
	le.Series = &series.Series{ID: 7, Path: "/library/Show"}
	le.Path = "/in/x.mkv"
	// Two episodes pointing at the SAME EpisodeFileID → recycled once.
	le.Episodes = []episodes.Episode{
		{ID: 1, EpisodeFileID: ptr(101)},
		{ID: 2, EpisodeFileID: ptr(101)},
	}

	_, err := svc.UpgradeEpisodeFile(context.Background(), &episodefiles.File{}, le, false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if recycle.calls != 1 {
		t.Errorf("recycle.calls = %d, want 1 (deduped)", recycle.calls)
	}
	if len(deleter.deleted) != 1 {
		t.Errorf("deleted = %v, want 1 (deduped)", deleter.deleted)
	}
}

// --- mover error propagation ---------------------------

func TestUpgradeMoverErrPropagates(t *testing.T) {
	t.Parallel()
	mover := &stubMover{moveErr: errors.New("transfer failed")}
	files := &stubFiles{}
	fs := stubFS{folders: map[string]bool{"/library": true}}

	svc := mkSvc(t, nil, &stubDeleter{}, mover, files, fs)
	le := episodeimport.NewLocalEpisode()
	le.Series = &series.Series{Path: "/library/Show"}
	le.Path = "/in/x.mkv"

	_, err := svc.UpgradeEpisodeFile(context.Background(), &episodefiles.File{}, le, false)
	if err == nil || !strings.Contains(err.Error(), "transfer failed") {
		t.Errorf("got %v, want transfer-failed error", err)
	}
}

// --- subfolder derivation ------------------------------

func TestUpgradeRecycleSubfolderRelativeToRoot(t *testing.T) {
	t.Parallel()
	files := &stubFiles{byID: map[int]*episodefiles.File{
		101: {ID: 101, RelativePath: "Season 03/old.mkv"},
	}}
	fs := stubFS{
		folders: map[string]bool{"/library": true},
		files:   map[string]bool{"/library/Show/Season 03/old.mkv": true},
	}
	recycle := &stubRecycle{}

	svc := mkSvc(t, recycle, &stubDeleter{}, &stubMover{}, files, fs)
	le := mkLE(t)
	le.Episodes = []episodes.Episode{{ID: 1, EpisodeFileID: ptr(101)}}

	_, err := svc.UpgradeEpisodeFile(context.Background(), &episodefiles.File{}, le, false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// The recycle subfolder is the file's parent folder relative to
	// the root-folder (/library). Series.Path is /library/Show; the
	// file's parent is /library/Show/Season 03 → relative-to-root
	// is "Show/Season 03".
	if recycle.last.subfolder != "Show/Season 03" {
		t.Errorf("subfolder = %q, want Show/Season 03", recycle.last.subfolder)
	}
}
