package episodefilemoving_test

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/episodefilemoving"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/series"
)

// --- stubs ---------------------------------------------------------

type fsStub struct {
	files       map[string]bool
	folders     map[string]bool
	createPaths []string
	createErr   error
	transfers   []transferCall
	transferErr error
}

type transferCall struct {
	src, dst string
	mode     episodefilemoving.TransferMode
}

func newFS() *fsStub {
	return &fsStub{
		files:   make(map[string]bool),
		folders: make(map[string]bool),
	}
}

func (s *fsStub) FileExists(path string) bool   { return s.files[path] }
func (s *fsStub) FolderExists(path string) bool { return s.folders[path] }
func (s *fsStub) CreateFolder(path string) error {
	s.createPaths = append(s.createPaths, path)
	if s.createErr != nil {
		return s.createErr
	}
	s.folders[path] = true
	return nil
}
func (s *fsStub) TransferFile(src, dst string, mode episodefilemoving.TransferMode) error {
	s.transfers = append(s.transfers, transferCall{src, dst, mode})
	if s.transferErr != nil {
		return s.transferErr
	}
	s.files[dst] = true
	return nil
}

type namesStub struct {
	pathOut string
	pathErr error
	seasons map[int]string
	calls   int
}

func (s *namesStub) BuildFilePath(_ []episodes.Episode, _ *series.Series, _ *episodefiles.File, _ string, _ *episodeimport.LocalEpisode) (string, error) {
	s.calls++
	return s.pathOut, s.pathErr
}
func (s *namesStub) BuildSeasonPath(_ *series.Series, n int) string {
	if v, ok := s.seasons[n]; ok {
		return v
	}
	return "/library/Show/Season 01"
}

type rootsStub struct {
	out string
}

func (s rootsStub) GetBestRootFolderPath(_ context.Context, _ string) string { return s.out }

type cfgStub struct{ hardlinks bool }

func (s cfgStub) CopyUsingHardlinks() bool { return s.hardlinks }

type finderStub struct {
	out []episodes.Episode
	err error
}

func (s finderStub) GetEpisodesByFileID(_ context.Context, _ int) ([]episodes.Episode, error) {
	return s.out, s.err
}

type datesStub struct {
	calls int
	err   error
}

func (s *datesStub) ChangeFileDateForFile(*episodefiles.File, *series.Series, []episodes.Episode) error {
	s.calls++
	return s.err
}

type attrsStub struct {
	folderTimes []string
	filePerms   []string
	folderPerms []string
}

func (s *attrsStub) SetFolderLastWriteTime(path string, _ time.Time) error {
	s.folderTimes = append(s.folderTimes, path)
	return nil
}
func (s *attrsStub) SetFilePermissions(path string) error {
	s.filePerms = append(s.filePerms, path)
	return nil
}
func (s *attrsStub) SetFolderPermissions(path string) error {
	s.folderPerms = append(s.folderPerms, path)
	return nil
}

type scriptsStub struct {
	verdict episodefilemoving.ScriptImportDecision
}

func (s scriptsStub) TryImport(_, _ string, _ *episodeimport.LocalEpisode, _ *episodefiles.File, _ episodefilemoving.TransferMode) episodefilemoving.ScriptImportDecision {
	return s.verdict
}

type eventsStub struct {
	got []any
}

func (s *eventsStub) Publish(e any) { s.got = append(s.got, e) }

// mkSvc builds a Service from common defaults plus overrides.
func mkSvc(t *testing.T, override func(*svcDeps)) *episodefilemoving.Service {
	t.Helper()
	d := &svcDeps{
		fs:      newFS(),
		names:   &namesStub{pathOut: "/library/Show/Season 01/renamed.mkv"},
		roots:   rootsStub{out: "/library"},
		cfg:     cfgStub{},
		finder:  finderStub{},
		dates:   nil,
		attrs:   nil,
		scripts: nil,
		events:  nil,
	}
	if override != nil {
		override(d)
	}
	svc, err := episodefilemoving.NewService(d.fs, d.names, d.roots, d.cfg, d.finder, d.dates, d.attrs, d.scripts, d.events, slog.Default())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

type svcDeps struct {
	fs      episodefilemoving.Filesystem
	names   episodefilemoving.FileNameBuilder
	roots   episodefilemoving.RootFolderFinder
	cfg     episodefilemoving.Config
	finder  episodefilemoving.EpisodeFinder
	dates   episodefilemoving.FileDateUpdater
	attrs   episodefilemoving.AttributeWriter
	scripts episodefilemoving.ImportScriptRunner
	events  episodefilemoving.EventPublisher
}

// helpers --------------------------------------------------

func ts() *series.Series {
	return &series.Series{ID: 7, Title: "Test", Path: "/library/Show", SeasonFolder: true}
}

func mkLE(seasonNumber, episodeNumber int) *episodeimport.LocalEpisode {
	le := episodeimport.NewLocalEpisode()
	le.Series = ts()
	le.Path = "/in/raw.mkv"
	le.Episodes = []episodes.Episode{
		{ID: 11, SeasonNumber: seasonNumber, EpisodeNumber: episodeNumber},
	}
	return le
}

// --- enum String -------------------------------------------------

func TestTransferModeString(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   episodefilemoving.TransferMode
		want string
	}{
		{episodefilemoving.TransferModeMove, "Move"},
		{episodefilemoving.TransferModeCopy, "Copy"},
		{episodefilemoving.TransferModeHardLinkOrCopy, "HardLinkOrCopy"},
	}
	for _, tc := range cases {
		if got := tc.in.String(); got != tc.want {
			t.Errorf("got %q, want %q", got, tc.want)
		}
	}
	if got := episodefilemoving.TransferMode(99).String(); got != "TransferMode(99)" {
		t.Errorf("out-of-range: %q", got)
	}
}

// --- constructor -------------------------------------------------

func TestNewServiceRejectsRequiredNils(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		want string
		args func() (fs episodefilemoving.Filesystem, names episodefilemoving.FileNameBuilder, roots episodefilemoving.RootFolderFinder, cfg episodefilemoving.Config, finder episodefilemoving.EpisodeFinder)
	}{
		{
			name: "nil Filesystem",
			want: "nil Filesystem",
			args: func() (episodefilemoving.Filesystem, episodefilemoving.FileNameBuilder, episodefilemoving.RootFolderFinder, episodefilemoving.Config, episodefilemoving.EpisodeFinder) {
				return nil, &namesStub{}, rootsStub{}, cfgStub{}, finderStub{}
			},
		},
		{
			name: "nil RootFolderFinder",
			want: "nil RootFolderFinder",
			args: func() (episodefilemoving.Filesystem, episodefilemoving.FileNameBuilder, episodefilemoving.RootFolderFinder, episodefilemoving.Config, episodefilemoving.EpisodeFinder) {
				return newFS(), &namesStub{}, nil, cfgStub{}, finderStub{}
			},
		},
		{
			name: "nil Config",
			want: "nil Config",
			args: func() (episodefilemoving.Filesystem, episodefilemoving.FileNameBuilder, episodefilemoving.RootFolderFinder, episodefilemoving.Config, episodefilemoving.EpisodeFinder) {
				return newFS(), &namesStub{}, rootsStub{}, nil, finderStub{}
			},
		},
		{
			name: "nil EpisodeFinder",
			want: "nil EpisodeFinder",
			args: func() (episodefilemoving.Filesystem, episodefilemoving.FileNameBuilder, episodefilemoving.RootFolderFinder, episodefilemoving.Config, episodefilemoving.EpisodeFinder) {
				return newFS(), &namesStub{}, rootsStub{}, cfgStub{}, nil
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs, names, roots, cfg, finder := tc.args()
			_, err := episodefilemoving.NewService(fs, names, roots, cfg, finder, nil, nil, nil, nil, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want %q", err, tc.want)
			}
		})
	}
}

func TestNewServiceNilOptionalsAccepted(t *testing.T) {
	t.Parallel()
	svc, err := episodefilemoving.NewService(newFS(), &namesStub{}, rootsStub{out: "/library"}, cfgStub{}, finderStub{}, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if svc == nil {
		t.Fatalf("nil svc")
	}
}

// --- ensureEpisodeFolder rejection arms (via MoveEpisodeFile) ----

func TestEnsureEpisodeFolderRootMissingErrors(t *testing.T) {
	t.Parallel()
	svc := mkSvc(t, func(d *svcDeps) {
		d.roots = rootsStub{out: ""} // empty → ErrRootFolderNotFound
	})
	_, err := svc.MoveEpisodeFile(context.Background(),
		&episodefiles.File{ID: 1, RelativePath: "old.mkv"}, ts())
	if !errors.Is(err, episodefilemoving.ErrRootFolderNotFound) {
		t.Errorf("got %v, want ErrRootFolderNotFound", err)
	}
}

func TestEnsureEpisodeFolderRootDoesNotExistErrors(t *testing.T) {
	t.Parallel()
	svc := mkSvc(t, func(d *svcDeps) {
		d.fs = newFS() // root /library is NOT in folders
		d.roots = rootsStub{out: "/library"}
	})
	_, err := svc.MoveEpisodeFile(context.Background(),
		&episodefiles.File{ID: 1, RelativePath: "old.mkv"}, ts())
	if !errors.Is(err, episodefilemoving.ErrRootFolderNotFound) {
		t.Errorf("got %v, want ErrRootFolderNotFound", err)
	}
}

// --- transferFile rejection arms -------------------------------

func TestTransferFileMissingSourceErrors(t *testing.T) {
	t.Parallel()
	fs := newFS()
	fs.folders["/library"] = true
	fs.folders["/library/Show"] = true
	// Note: source file path is NOT registered.
	svc := mkSvc(t, func(d *svcDeps) {
		d.fs = fs
		d.names = &namesStub{pathOut: "/library/Show/Season 01/renamed.mkv"}
	})
	_, err := svc.MoveEpisodeFile(context.Background(),
		&episodefiles.File{ID: 1, RelativePath: "old.mkv"}, ts())
	if !errors.Is(err, episodefilemoving.ErrFileNotFound) {
		t.Errorf("got %v, want ErrFileNotFound", err)
	}
}

func TestTransferFileSamePathErrors(t *testing.T) {
	t.Parallel()
	fs := newFS()
	fs.folders["/library"] = true
	fs.folders["/library/Show"] = true
	fs.folders["/library/Show/Season 01"] = true
	fs.files["/library/Show/old.mkv"] = true

	svc := mkSvc(t, func(d *svcDeps) {
		d.fs = fs
		d.names = &namesStub{pathOut: "/library/Show/old.mkv"} // same as src
	})
	_, err := svc.MoveEpisodeFile(context.Background(),
		&episodefiles.File{ID: 1, RelativePath: "old.mkv"}, ts())
	if !errors.Is(err, episodefilemoving.ErrSameFilename) {
		t.Errorf("got %v, want ErrSameFilename", err)
	}
}

// --- happy path: 2-arg MoveEpisodeFile --------------------------

func TestMoveEpisodeFileHappyPath(t *testing.T) {
	t.Parallel()
	fs := newFS()
	fs.folders["/library"] = true
	fs.folders["/library/Show"] = true
	fs.folders["/library/Show/Season 01"] = true
	fs.files["/library/Show/old.mkv"] = true

	finder := finderStub{out: []episodes.Episode{
		{ID: 1, SeasonNumber: 1, EpisodeNumber: 5},
	}}
	dates := &datesStub{}
	attrs := &attrsStub{}
	events := &eventsStub{}
	names := &namesStub{pathOut: "/library/Show/Season 01/renamed.mkv"}

	svc := mkSvc(t, func(d *svcDeps) {
		d.fs = fs
		d.names = names
		d.finder = finder
		d.dates = dates
		d.attrs = attrs
		d.events = events
	})

	got, err := svc.MoveEpisodeFile(context.Background(),
		&episodefiles.File{ID: 1, RelativePath: "old.mkv", DateAdded: time.Now()}, ts())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got == nil {
		t.Fatalf("nil file")
	}

	// Transfer was called with the resolved dst path.
	if len(fs.transfers) != 1 || fs.transfers[0].dst != "/library/Show/Season 01/renamed.mkv" {
		t.Errorf("transfer = %+v, want one move to renamed", fs.transfers)
	}
	if fs.transfers[0].mode != episodefilemoving.TransferModeMove {
		t.Errorf("mode = %v, want Move", fs.transfers[0].mode)
	}
	// File RelativePath updated.
	if got.RelativePath != "Season 01/renamed.mkv" {
		t.Errorf("RelativePath = %q, want Season 01/renamed.mkv", got.RelativePath)
	}
	// Date updater + attrs called.
	if dates.calls != 1 {
		t.Errorf("dates calls = %d, want 1", dates.calls)
	}
	if len(attrs.filePerms) != 1 {
		t.Errorf("filePerms calls = %d, want 1", len(attrs.filePerms))
	}
	// Series + season folder mtime updates (SeasonFolder=true).
	if len(attrs.folderTimes) != 2 {
		t.Errorf("folderTimes = %d, want 2", len(attrs.folderTimes))
	}
	// All folders existed → no event.
	if len(events.got) != 0 {
		t.Errorf("events got %d, want 0 (no folder created)", len(events.got))
	}
}

func TestMoveEpisodeFileCreatesMissingFolders(t *testing.T) {
	t.Parallel()
	fs := newFS()
	fs.folders["/library"] = true
	// Note: /library/Show and /library/Show/Season 01 are missing.
	fs.files["/library/Show/old.mkv"] = true

	events := &eventsStub{}
	svc := mkSvc(t, func(d *svcDeps) {
		d.fs = fs
		d.names = &namesStub{pathOut: "/library/Show/Season 01/renamed.mkv"}
		d.finder = finderStub{out: []episodes.Episode{{SeasonNumber: 1}}}
		d.events = events
	})

	_, err := svc.MoveEpisodeFile(context.Background(),
		&episodefiles.File{ID: 1, RelativePath: "old.mkv"}, ts())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// Series + season folders should have been created.
	if len(fs.createPaths) < 2 {
		t.Errorf("createPaths = %v, want at least series + season", fs.createPaths)
	}
	// One EpisodeFolderCreatedEvent published.
	if len(events.got) != 1 {
		t.Errorf("events = %d, want 1", len(events.got))
	}
	if evt, ok := events.got[0].(episodefilemoving.EpisodeFolderCreatedEvent); !ok || evt.SeriesFolder == "" {
		t.Errorf("event = %+v, want EpisodeFolderCreatedEvent with SeriesFolder set", events.got[0])
	}
}

// --- 3-arg MoveEpisodeFileWithLocal -----------------------------

func TestMoveEpisodeFileWithLocalUsesLocalEpisodeSeasonAndPath(t *testing.T) {
	t.Parallel()
	fs := newFS()
	fs.folders["/library"] = true
	fs.folders["/library/Show"] = true
	fs.folders["/library/Show/Season 02"] = true
	fs.files["/library/Show/old.mkv"] = true

	names := &namesStub{
		pathOut: "/library/Show/Season 02/renamed.mkv",
		seasons: map[int]string{2: "/library/Show/Season 02"},
	}

	svc := mkSvc(t, func(d *svcDeps) {
		d.fs = fs
		d.names = names
	})

	le := mkLE(2, 7)
	got, err := svc.MoveEpisodeFileWithLocal(context.Background(),
		&episodefiles.File{ID: 1, RelativePath: "old.mkv"}, le)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got.RelativePath != "Season 02/renamed.mkv" {
		t.Errorf("got RelativePath %q", got.RelativePath)
	}
	// FileNameBeforeRename mutated on the LocalEpisode.
	if le.FileNameBeforeRename != "Season 02/renamed.mkv" {
		t.Errorf("le.FileNameBeforeRename = %q", le.FileNameBeforeRename)
	}
}

// --- CopyEpisodeFile mode selection ----------------------------

func TestCopyEpisodeFilePlainCopy(t *testing.T) {
	t.Parallel()
	fs := newFS()
	fs.folders["/library"] = true
	fs.folders["/library/Show"] = true
	fs.folders["/library/Show/Season 01"] = true
	fs.files["/library/Show/old.mkv"] = true

	svc := mkSvc(t, func(d *svcDeps) {
		d.fs = fs
		d.cfg = cfgStub{hardlinks: false}
	})

	_, err := svc.CopyEpisodeFile(context.Background(),
		&episodefiles.File{ID: 1, RelativePath: "old.mkv"}, mkLE(1, 5))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(fs.transfers) != 1 || fs.transfers[0].mode != episodefilemoving.TransferModeCopy {
		t.Errorf("mode = %+v, want Copy", fs.transfers)
	}
}

func TestCopyEpisodeFileHardlinkPreferred(t *testing.T) {
	t.Parallel()
	fs := newFS()
	fs.folders["/library"] = true
	fs.folders["/library/Show"] = true
	fs.folders["/library/Show/Season 01"] = true
	fs.files["/library/Show/old.mkv"] = true

	svc := mkSvc(t, func(d *svcDeps) {
		d.fs = fs
		d.cfg = cfgStub{hardlinks: true}
	})

	_, err := svc.CopyEpisodeFile(context.Background(),
		&episodefiles.File{ID: 1, RelativePath: "old.mkv"}, mkLE(1, 5))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if fs.transfers[0].mode != episodefilemoving.TransferModeHardLinkOrCopy {
		t.Errorf("mode = %v, want HardLinkOrCopy", fs.transfers[0].mode)
	}
}

// --- script-import flow ----------------------------------------

func TestScriptMoveCompleteSkipsTransferFile(t *testing.T) {
	t.Parallel()
	fs := newFS()
	fs.folders["/library"] = true
	fs.folders["/library/Show"] = true
	fs.folders["/library/Show/Season 01"] = true
	fs.files["/library/Show/old.mkv"] = true

	svc := mkSvc(t, func(d *svcDeps) {
		d.fs = fs
		d.scripts = scriptsStub{verdict: episodefilemoving.ScriptImportMoveComplete}
	})

	_, err := svc.MoveEpisodeFileWithLocal(context.Background(),
		&episodefiles.File{ID: 1, RelativePath: "old.mkv"}, mkLE(1, 5))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(fs.transfers) != 0 {
		t.Errorf("transfers = %d, want 0 (script handled the move)", len(fs.transfers))
	}
}

func TestScriptDeferMoveRunsTransfer(t *testing.T) {
	t.Parallel()
	fs := newFS()
	fs.folders["/library"] = true
	fs.folders["/library/Show"] = true
	fs.folders["/library/Show/Season 01"] = true
	fs.files["/library/Show/old.mkv"] = true

	svc := mkSvc(t, func(d *svcDeps) {
		d.fs = fs
		d.scripts = scriptsStub{verdict: episodefilemoving.ScriptImportDeferMove}
	})

	_, err := svc.MoveEpisodeFileWithLocal(context.Background(),
		&episodefiles.File{ID: 1, RelativePath: "old.mkv"}, mkLE(1, 5))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(fs.transfers) != 1 {
		t.Errorf("transfers = %d, want 1 (DeferMove → regular transfer)", len(fs.transfers))
	}
}

// --- buildPath / season-folder discovery -----------------------

func TestEnsureEpisodeFolderSkipsExistingFolders(t *testing.T) {
	t.Parallel()
	fs := newFS()
	fs.folders["/library"] = true
	fs.folders["/library/Show"] = true
	fs.folders["/library/Show/Season 01"] = true
	fs.files["/library/Show/old.mkv"] = true

	events := &eventsStub{}
	svc := mkSvc(t, func(d *svcDeps) {
		d.fs = fs
		d.events = events
	})

	_, err := svc.MoveEpisodeFile(context.Background(),
		&episodefiles.File{ID: 1, RelativePath: "old.mkv"}, ts())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// All folders existed → no creates → no event.
	if len(fs.createPaths) != 0 {
		t.Errorf("createPaths = %v, want []", fs.createPaths)
	}
	if len(events.got) != 0 {
		t.Errorf("events = %d, want 0", len(events.got))
	}
}

// --- EpisodeFinder error propagation ---------------------------

func TestMoveEpisodeFileEpisodeFinderErrPropagates(t *testing.T) {
	t.Parallel()
	svc := mkSvc(t, func(d *svcDeps) {
		d.finder = finderStub{err: errors.New("EIO")}
	})
	_, err := svc.MoveEpisodeFile(context.Background(),
		&episodefiles.File{ID: 1}, ts())
	if err == nil || !strings.Contains(err.Error(), "GetEpisodesByFileID") {
		t.Errorf("got %v, want GetEpisodesByFileID error", err)
	}
}

// --- nil-receiver guards ---------------------------------------

func TestPublicMethodsNilReceiver(t *testing.T) {
	t.Parallel()
	var svc *episodefilemoving.Service

	if _, err := svc.MoveEpisodeFile(context.Background(), &episodefiles.File{}, ts()); err == nil {
		t.Errorf("MoveEpisodeFile: nil receiver should error")
	}
	if _, err := svc.MoveEpisodeFileWithLocal(context.Background(), &episodefiles.File{}, mkLE(1, 1)); err == nil {
		t.Errorf("MoveEpisodeFileWithLocal: nil receiver should error")
	}
	if _, err := svc.CopyEpisodeFile(context.Background(), &episodefiles.File{}, mkLE(1, 1)); err == nil {
		t.Errorf("CopyEpisodeFile: nil receiver should error")
	}
}

// --- helpers usage ----------------------------------------------

func TestRelativePathUsesSeriesPathAsBase(t *testing.T) {
	t.Parallel()
	fs := newFS()
	fs.folders["/library"] = true
	fs.folders["/library/Show"] = true
	fs.folders["/library/Show/Season 01"] = true
	fs.files["/library/Show/old.mkv"] = true

	dst := "/library/Show/Season 01/renamed.mkv"
	svc := mkSvc(t, func(d *svcDeps) {
		d.fs = fs
		d.names = &namesStub{pathOut: dst}
	})
	got, err := svc.MoveEpisodeFile(context.Background(),
		&episodefiles.File{ID: 1, RelativePath: "old.mkv"}, ts())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := "Season 01/renamed.mkv"
	// On Windows filepath.Rel produces backslashes; normalise.
	if got.RelativePath != want && filepath.ToSlash(got.RelativePath) != want {
		t.Errorf("RelativePath = %q, want %q", got.RelativePath, want)
	}
}
