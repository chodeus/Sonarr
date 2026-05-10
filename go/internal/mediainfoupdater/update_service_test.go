package mediainfoupdater_test

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/mediainfo"
	"sonarr.tv/sonarr/internal/mediainfoupdater"
	"sonarr.tv/sonarr/internal/series"
)

// stubFS feeds a fixed FileExists response keyed by path.
type stubFS struct {
	exists map[string]bool
}

func (s stubFS) FileExists(path string) bool {
	if s.exists == nil {
		return false
	}
	return s.exists[path]
}

// stubFiles records the last Update call + an injectable error.
type stubFiles struct {
	gotUpdate *episodefiles.File
	err       error
	calls     int
}

func (s *stubFiles) Update(_ context.Context, f *episodefiles.File) error {
	s.calls++
	s.gotUpdate = f
	return s.err
}

// stubReader feeds a fixed *Model + optional error.
type stubReader struct {
	model *mediainfo.Model
	err   error
	calls int
	got   string
}

func (s *stubReader) GetMediaInfo(_ context.Context, path string) (*mediainfo.Model, error) {
	s.calls++
	s.got = path
	return s.model, s.err
}

// stubCfg returns a fixed EnableMediaInfo flag.
type stubCfg struct{ enabled bool }

func (s stubCfg) EnableMediaInfo() bool { return s.enabled }

// stubFileReader feeds a fixed []episodefiles.File + optional error,
// captures the seriesID arg.
type stubFileReader struct {
	rows  []episodefiles.File
	err   error
	calls int
	got   int
}

func (s *stubFileReader) GetFilesBySeries(_ context.Context, seriesID int) ([]episodefiles.File, error) {
	s.calls++
	s.got = seriesID
	return s.rows, s.err
}

func newSvc(t *testing.T, fs stubFS, files *stubFiles, reader *stubReader, cfg stubCfg) *mediainfoupdater.UpdateService {
	t.Helper()
	return newSvcWithFileReader(t, fs, files, &stubFileReader{}, reader, cfg)
}

func newSvcWithFileReader(t *testing.T, fs stubFS, files *stubFiles, fileReader *stubFileReader, reader *stubReader, cfg stubCfg) *mediainfoupdater.UpdateService {
	t.Helper()
	svc, err := mediainfoupdater.NewUpdateService(fs, files, fileReader, reader, cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewUpdateService: %v", err)
	}
	return svc
}

func TestNewUpdateService_RejectsNilDeps(t *testing.T) {
	t.Parallel()
	files := &stubFiles{}
	reader := &stubReader{}
	fileReader := &stubFileReader{}
	cases := []struct {
		name string
		fn   func() error
	}{
		{"nil disk", func() error {
			_, err := mediainfoupdater.NewUpdateService(nil, files, fileReader, reader, stubCfg{}, nil)
			return err
		}},
		{"nil files", func() error {
			_, err := mediainfoupdater.NewUpdateService(stubFS{}, nil, fileReader, reader, stubCfg{}, nil)
			return err
		}},
		{"nil fileReader", func() error {
			_, err := mediainfoupdater.NewUpdateService(stubFS{}, files, nil, reader, stubCfg{}, nil)
			return err
		}},
		{"nil reader", func() error {
			_, err := mediainfoupdater.NewUpdateService(stubFS{}, files, fileReader, nil, stubCfg{}, nil)
			return err
		}},
		{"nil cfg", func() error {
			_, err := mediainfoupdater.NewUpdateService(stubFS{}, files, fileReader, reader, nil, nil)
			return err
		}},
	}
	for _, c := range cases {
		if err := c.fn(); err == nil {
			t.Errorf("%s: want error, got nil", c.name)
		}
	}
}

func TestUpdate_NilReceiverErrors(t *testing.T) {
	t.Parallel()
	var s *mediainfoupdater.UpdateService
	if _, err := s.Update(t.Context(), &episodefiles.File{}, &series.Series{}); err == nil {
		t.Errorf("nil receiver: want error")
	}
}

func TestUpdate_NilFileErrors(t *testing.T) {
	t.Parallel()
	svc := newSvc(t, stubFS{}, &stubFiles{}, &stubReader{}, stubCfg{enabled: true})
	if _, err := svc.Update(t.Context(), nil, &series.Series{}); err == nil {
		t.Errorf("nil file: want error")
	}
}

func TestUpdate_NilSeriesErrors(t *testing.T) {
	t.Parallel()
	svc := newSvc(t, stubFS{}, &stubFiles{}, &stubReader{}, stubCfg{enabled: true})
	if _, err := svc.Update(t.Context(), &episodefiles.File{}, nil); err == nil {
		t.Errorf("nil series: want error")
	}
}

func TestUpdate_DisabledByConfig(t *testing.T) {
	t.Parallel()
	files := &stubFiles{}
	reader := &stubReader{}
	svc := newSvc(t, stubFS{}, files, reader, stubCfg{enabled: false})
	updated, err := svc.Update(t.Context(),
		&episodefiles.File{ID: 7, RelativePath: "a.mkv"},
		&series.Series{Path: "/tv/Show"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if updated {
		t.Errorf("disabled: want updated=false")
	}
	if files.calls != 0 {
		t.Errorf("disabled: files.Update should not be called; calls=%d", files.calls)
	}
	if reader.calls != 0 {
		t.Errorf("disabled: reader.GetMediaInfo should not be called; calls=%d", reader.calls)
	}
}

func TestUpdate_FileMissingOnDisk(t *testing.T) {
	t.Parallel()
	files := &stubFiles{}
	reader := &stubReader{}
	svc := newSvc(t, stubFS{exists: map[string]bool{}}, files, reader, stubCfg{enabled: true})
	updated, err := svc.Update(t.Context(),
		&episodefiles.File{ID: 7, RelativePath: "a.mkv"},
		&series.Series{Path: "/tv/Show"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if updated {
		t.Errorf("file missing: want updated=false")
	}
	if reader.calls != 0 {
		t.Errorf("file missing: reader should not be called; calls=%d", reader.calls)
	}
}

func TestUpdate_ReaderReturnsNilMediaInfo(t *testing.T) {
	t.Parallel()
	path := filepath.Join("/tv/Show", "a.mkv")
	files := &stubFiles{}
	reader := &stubReader{model: nil}
	svc := newSvc(t, stubFS{exists: map[string]bool{path: true}}, files, reader, stubCfg{enabled: true})
	updated, err := svc.Update(t.Context(),
		&episodefiles.File{ID: 7, RelativePath: "a.mkv"},
		&series.Series{Path: "/tv/Show"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if updated {
		t.Errorf("nil model: want updated=false")
	}
	if files.calls != 0 {
		t.Errorf("nil model: files.Update should not be called; calls=%d", files.calls)
	}
}

func TestUpdate_ReaderErrorTreatedAsNilModel(t *testing.T) {
	t.Parallel()
	path := filepath.Join("/tv/Show", "a.mkv")
	files := &stubFiles{}
	reader := &stubReader{err: errors.New("ffmpeg unavailable")}
	svc := newSvc(t, stubFS{exists: map[string]bool{path: true}}, files, reader, stubCfg{enabled: true})
	updated, err := svc.Update(t.Context(),
		&episodefiles.File{ID: 7, RelativePath: "a.mkv"},
		&series.Series{Path: "/tv/Show"})
	if err != nil {
		t.Fatalf("err should be swallowed, got: %v", err)
	}
	if updated {
		t.Errorf("reader error: want updated=false")
	}
}

func TestUpdate_HappyPathPersistsWhenIDNonZero(t *testing.T) {
	t.Parallel()
	path := filepath.Join("/tv/Show", "Season 01", "ep.mkv")
	files := &stubFiles{}
	reader := &stubReader{model: &mediainfo.Model{SchemaRevision: mediainfoupdater.MinimumSchemaRevision}}
	svc := newSvc(t, stubFS{exists: map[string]bool{path: true}}, files, reader, stubCfg{enabled: true})
	file := &episodefiles.File{ID: 42, RelativePath: "Season 01/ep.mkv"}
	updated, err := svc.Update(t.Context(), file, &series.Series{Path: "/tv/Show"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !updated {
		t.Errorf("happy path: want updated=true")
	}
	if reader.got != path {
		t.Errorf("reader called with %q, want %q", reader.got, path)
	}
	if files.calls != 1 {
		t.Errorf("files.Update should be called once; calls=%d", files.calls)
	}
	if file.MediaInfo == nil || file.MediaInfo.SchemaRevision != mediainfoupdater.MinimumSchemaRevision {
		t.Errorf("file.MediaInfo not stamped: %+v", file.MediaInfo)
	}
}

func TestUpdate_HappyPathSkipsPersistWhenIDZero(t *testing.T) {
	t.Parallel()
	path := filepath.Join("/tv/Show", "ep.mkv")
	files := &stubFiles{}
	reader := &stubReader{model: &mediainfo.Model{SchemaRevision: 14}}
	svc := newSvc(t, stubFS{exists: map[string]bool{path: true}}, files, reader, stubCfg{enabled: true})
	file := &episodefiles.File{ID: 0, RelativePath: "ep.mkv"} // not yet persisted
	updated, err := svc.Update(t.Context(), file, &series.Series{Path: "/tv/Show"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !updated {
		t.Errorf("happy path: want updated=true")
	}
	if files.calls != 0 {
		t.Errorf("ID=0: files.Update should be skipped; calls=%d", files.calls)
	}
	if file.MediaInfo == nil {
		t.Errorf("file.MediaInfo should still be stamped in-memory")
	}
}

func TestUpdate_PersistErrorPropagates(t *testing.T) {
	t.Parallel()
	path := filepath.Join("/tv/Show", "ep.mkv")
	persistErr := errors.New("db down")
	files := &stubFiles{err: persistErr}
	reader := &stubReader{model: &mediainfo.Model{SchemaRevision: 14}}
	svc := newSvc(t, stubFS{exists: map[string]bool{path: true}}, files, reader, stubCfg{enabled: true})
	_, err := svc.Update(t.Context(),
		&episodefiles.File{ID: 7, RelativePath: "ep.mkv"},
		&series.Series{Path: "/tv/Show"})
	if err == nil {
		t.Fatal("want error")
	}
	if !errors.Is(err, persistErr) {
		t.Errorf("error chain missing persist err: %v", err)
	}
}

func TestUpdate_EmptyRelativePathSkips(t *testing.T) {
	t.Parallel()
	files := &stubFiles{}
	reader := &stubReader{}
	svc := newSvc(t, stubFS{}, files, reader, stubCfg{enabled: true})
	updated, err := svc.Update(t.Context(),
		&episodefiles.File{ID: 7, RelativePath: ""},
		&series.Series{Path: "/tv/Show"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if updated {
		t.Errorf("empty RelativePath: want updated=false")
	}
	if reader.calls != 0 {
		t.Errorf("empty path: reader should not be called")
	}
}

func TestUpdateMediaInfo_BypassesEnableCheck(t *testing.T) {
	t.Parallel()
	// UpdateMediaInfo (the lower-level method) does NOT check
	// EnableMediaInfo — callers that bypass Update() are accepting
	// the responsibility. This test pins that contract.
	path := filepath.Join("/tv/Show", "ep.mkv")
	files := &stubFiles{}
	reader := &stubReader{model: &mediainfo.Model{SchemaRevision: 14}}
	svc := newSvc(t, stubFS{exists: map[string]bool{path: true}}, files, reader, stubCfg{enabled: false})
	file := &episodefiles.File{ID: 7, RelativePath: "ep.mkv"}
	updated, err := svc.UpdateMediaInfo(t.Context(), file, &series.Series{Path: "/tv/Show"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !updated {
		t.Errorf("UpdateMediaInfo with EnableMediaInfo=false: want updated=true (no flag check)")
	}
}

func TestMinimumSchemaRevision_Pinned(t *testing.T) {
	t.Parallel()
	if mediainfoupdater.MinimumSchemaRevision != 14 {
		t.Errorf("MinimumSchemaRevision = %d, want 14 (C# parity)", mediainfoupdater.MinimumSchemaRevision)
	}
}
