package episodefilemoving_test

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/episodefilemoving"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/mediafiles"
	"sonarr.tv/sonarr/internal/series"
)

type stubTimeMgr struct {
	getCalls  int
	setCalls  int
	getReturn time.Time
	getErr    error
	setErr    error
	setPath   string
	setTime   time.Time
}

func (s *stubTimeMgr) FileGetLastWrite(_ string) (time.Time, error) {
	s.getCalls++
	return s.getReturn, s.getErr
}

func (s *stubTimeMgr) FileSetLastWriteTime(p string, t time.Time) error {
	s.setCalls++
	s.setPath = p
	s.setTime = t
	return s.setErr
}

type stubCfg struct{ value string }

func (s stubCfg) FileDate() string { return s.value }

func newSvc(t *testing.T, mgr episodefilemoving.FileTimeManager, cfg episodefilemoving.FileDateConfigReader) *episodefilemoving.UpdateService {
	t.Helper()
	svc, err := episodefilemoving.NewUpdateService(mgr, cfg, nil)
	if err != nil {
		t.Fatalf("NewUpdateService: %v", err)
	}
	return svc
}

func ptrTime(tm time.Time) *time.Time { return &tm }

func TestParseFileDateType(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want episodefilemoving.FileDateType
	}{
		{"None", episodefilemoving.FileDateNone},
		{"none", episodefilemoving.FileDateNone},
		{"", episodefilemoving.FileDateNone},
		{"unknown", episodefilemoving.FileDateNone},
		{"LocalAirDate", episodefilemoving.FileDateLocalAirDate},
		{"localairdate", episodefilemoving.FileDateLocalAirDate},
		{"UtcAirDate", episodefilemoving.FileDateUtcAirDate},
		{"  utcairdate  ", episodefilemoving.FileDateUtcAirDate},
	}
	for _, tc := range cases {
		if got := episodefilemoving.ParseFileDateType(tc.in); got != tc.want {
			t.Errorf("ParseFileDateType(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestFileDateType_StringRoundTrip(t *testing.T) {
	t.Parallel()
	for _, v := range []episodefilemoving.FileDateType{
		episodefilemoving.FileDateNone,
		episodefilemoving.FileDateLocalAirDate,
		episodefilemoving.FileDateUtcAirDate,
	} {
		if got := episodefilemoving.ParseFileDateType(v.String()); got != v {
			t.Errorf("round-trip %v: got %v", v, got)
		}
	}
}

func TestNewUpdateService_NilDeps(t *testing.T) {
	t.Parallel()
	if _, err := episodefilemoving.NewUpdateService(nil, stubCfg{}, nil); err == nil {
		t.Error("nil mgr: want error")
	}
	if _, err := episodefilemoving.NewUpdateService(&stubTimeMgr{}, nil, nil); err == nil {
		t.Error("nil cfg: want error")
	}
}

func TestChangeFileDateForFile_NilReceiver(t *testing.T) {
	t.Parallel()
	var s *episodefilemoving.UpdateService
	if err := s.ChangeFileDateForFile(&episodefiles.File{}, &series.Series{}, []episodes.Episode{{}}); err == nil {
		t.Error("nil receiver: want error")
	}
}

func TestChangeFileDateForFile_NilGuards(t *testing.T) {
	t.Parallel()
	svc := newSvc(t, &stubTimeMgr{}, stubCfg{value: "LocalAirDate"})
	if err := svc.ChangeFileDateForFile(nil, &series.Series{}, []episodes.Episode{{}}); err == nil {
		t.Error("nil file: want error")
	}
	if err := svc.ChangeFileDateForFile(&episodefiles.File{}, nil, []episodes.Episode{{}}); err == nil {
		t.Error("nil series: want error")
	}
	if err := svc.ChangeFileDateForFile(&episodefiles.File{}, &series.Series{}, nil); err == nil {
		t.Error("empty episodes: want error")
	}
}

func TestChangeFileDateForFile_NilAirDateNoOp(t *testing.T) {
	t.Parallel()
	mgr := &stubTimeMgr{}
	svc := newSvc(t, mgr, stubCfg{value: "LocalAirDate"})
	err := svc.ChangeFileDateForFile(
		&episodefiles.File{RelativePath: "ep.mkv"},
		&series.Series{Path: "/tv/Show"},
		[]episodes.Episode{{AirDateUtc: nil}},
	)
	if err != nil {
		t.Errorf("err: %v", err)
	}
	if mgr.getCalls != 0 {
		t.Errorf("nil air date: should not call FileGetLastWrite")
	}
}

func TestChangeFileDateForFile_FileDateNoneNoOp(t *testing.T) {
	t.Parallel()
	mgr := &stubTimeMgr{}
	svc := newSvc(t, mgr, stubCfg{value: "None"})
	air := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	err := svc.ChangeFileDateForFile(
		&episodefiles.File{RelativePath: "ep.mkv"},
		&series.Series{Path: "/tv/Show"},
		[]episodes.Episode{{AirDateUtc: ptrTime(air)}},
	)
	if err != nil {
		t.Errorf("err: %v", err)
	}
	if mgr.getCalls != 0 || mgr.setCalls != 0 {
		t.Errorf("FileDate=None: should not touch disk; get=%d set=%d", mgr.getCalls, mgr.setCalls)
	}
}

func TestChangeFileDateForFile_LocalAirDate_Writes(t *testing.T) {
	t.Parallel()
	air := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	// Existing mtime: same date but different — make it 2024 to force a change.
	existing := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	mgr := &stubTimeMgr{getReturn: existing}
	svc := newSvc(t, mgr, stubCfg{value: "LocalAirDate"})

	err := svc.ChangeFileDateForFile(
		&episodefiles.File{RelativePath: "ep.mkv"},
		&series.Series{Path: "/tv/Show"},
		[]episodes.Episode{{AirDateUtc: ptrTime(air)}},
	)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if mgr.setCalls != 1 {
		t.Fatalf("set calls: %d", mgr.setCalls)
	}
	wantPath := filepath.Join("/tv/Show", "ep.mkv")
	if mgr.setPath != wantPath {
		t.Errorf("path: got %q want %q", mgr.setPath, wantPath)
	}
	// Set time should be air (in local). Check the wall-clock equality
	// after converting both to UTC.
	if !mgr.setTime.UTC().Truncate(time.Second).Equal(air.UTC().Truncate(time.Second)) {
		t.Errorf("set time: got %v want %v", mgr.setTime.UTC(), air)
	}
}

func TestChangeFileDateForFile_UtcAirDate_TreatsAsLocal(t *testing.T) {
	t.Parallel()
	air := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	existing := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	mgr := &stubTimeMgr{getReturn: existing}
	svc := newSvc(t, mgr, stubCfg{value: "UtcAirDate"})
	err := svc.ChangeFileDateForFile(
		&episodefiles.File{RelativePath: "ep.mkv"},
		&series.Series{Path: "/tv/Show"},
		[]episodes.Episode{{AirDateUtc: ptrTime(air)}},
	)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if mgr.setCalls != 1 {
		t.Fatalf("set calls: %d", mgr.setCalls)
	}
	// Wall-clock components should match air directly (NOT converted).
	got := mgr.setTime
	if got.Year() != air.Year() || got.Month() != air.Month() || got.Day() != air.Day() ||
		got.Hour() != air.Hour() || got.Minute() != air.Minute() {
		t.Errorf("UtcAirDate: wall clock should match air; got %v want %v", got, air)
	}
	if got.Location().String() != time.Local.String() {
		t.Errorf("UtcAirDate: location should be local, got %v", got.Location())
	}
}

func TestChangeFileDateForFile_SameTimeNoWrite(t *testing.T) {
	t.Parallel()
	air := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	// UtcAirDate reinterprets the UTC wall-clock components as local.
	// To match for "no write", existing must hold the same wall-clock
	// in local time (sub-second drift is OK — withoutTicks strips it).
	existing := time.Date(2025, 6, 15, 12, 0, 0, 123_000_000, time.Local)
	mgr := &stubTimeMgr{getReturn: existing}
	svc := newSvc(t, mgr, stubCfg{value: "UtcAirDate"})
	err := svc.ChangeFileDateForFile(
		&episodefiles.File{RelativePath: "ep.mkv"},
		&series.Series{Path: "/tv/Show"},
		[]episodes.Episode{{AirDateUtc: ptrTime(air)}},
	)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if mgr.setCalls != 0 {
		t.Errorf("identical (mod sub-second) times: should not write; calls=%d", mgr.setCalls)
	}
}

func TestChangeFileDateForFile_PreservesSubSeconds(t *testing.T) {
	t.Parallel()
	air := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	existing := time.Date(2024, 1, 1, 0, 0, 0, 555_000_000, time.UTC)
	mgr := &stubTimeMgr{getReturn: existing}
	svc := newSvc(t, mgr, stubCfg{value: "UtcAirDate"})
	err := svc.ChangeFileDateForFile(
		&episodefiles.File{RelativePath: "ep.mkv"},
		&series.Series{Path: "/tv/Show"},
		[]episodes.Episode{{AirDateUtc: ptrTime(air)}},
	)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if mgr.setTime.Nanosecond() != 555_000_000 {
		t.Errorf("sub-second preservation: got %d, want 555_000_000", mgr.setTime.Nanosecond())
	}
}

func TestChangeFileDateForFile_GetWriteErrorPropagates(t *testing.T) {
	t.Parallel()
	getErr := errors.New("boom")
	mgr := &stubTimeMgr{getErr: getErr}
	svc := newSvc(t, mgr, stubCfg{value: "LocalAirDate"})
	err := svc.ChangeFileDateForFile(
		&episodefiles.File{RelativePath: "ep.mkv"},
		&series.Series{Path: "/tv/Show"},
		[]episodes.Episode{{AirDateUtc: ptrTime(time.Now().UTC())}},
	)
	if err == nil {
		t.Fatal("want error from get")
	}
	if !errors.Is(err, getErr) {
		t.Errorf("error chain missing: %v", err)
	}
}

func TestChangeFileDateForFile_SetWriteErrorSwallowed(t *testing.T) {
	t.Parallel()
	mgr := &stubTimeMgr{
		getReturn: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		setErr:    errors.New("permission denied"),
	}
	svc := newSvc(t, mgr, stubCfg{value: "LocalAirDate"})
	err := svc.ChangeFileDateForFile(
		&episodefiles.File{RelativePath: "ep.mkv"},
		&series.Series{Path: "/tv/Show"},
		[]episodes.Episode{{AirDateUtc: ptrTime(time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC))}},
	)
	if err != nil {
		t.Errorf("set error should be swallowed (warn-and-continue), got %v", err)
	}
}

func TestChangeFileDateForFile_PreEpochClamp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("clamp is non-Windows-only")
	}
	t.Parallel()
	// Pre-epoch air date.
	air := time.Date(1969, 6, 15, 12, 0, 0, 0, time.UTC)
	existing := time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
	mgr := &stubTimeMgr{getReturn: existing}
	svc := newSvc(t, mgr, stubCfg{value: "LocalAirDate"})
	err := svc.ChangeFileDateForFile(
		&episodefiles.File{RelativePath: "ep.mkv"},
		&series.Series{Path: "/tv/Show"},
		[]episodes.Episode{{AirDateUtc: ptrTime(air)}},
	)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if mgr.setCalls != 1 {
		t.Fatalf("set calls: %d", mgr.setCalls)
	}
	// Should be clamped to epoch (1970-01-01 UTC), local-converted.
	if !mgr.setTime.UTC().Truncate(time.Second).Equal(time.Unix(0, 0).UTC()) {
		t.Errorf("pre-epoch clamp: got %v want 1970-01-01 UTC", mgr.setTime.UTC())
	}
}

func TestUpdateService_SatisfiesFileDateUpdater(t *testing.T) {
	t.Parallel()
	var _ episodefilemoving.FileDateUpdater = (*episodefilemoving.UpdateService)(nil)
}

// ---- J212 — HandleSeriesScanned tests ----

type stubEpisodesLister struct {
	rows []episodes.Episode
	err  error
}

func (s *stubEpisodesLister) EpisodesWithFiles(_ context.Context, _ int) ([]episodes.Episode, error) {
	return s.rows, s.err
}

type stubFileLookup struct {
	files map[int]*episodefiles.File
	errs  map[int]error
}

func (s *stubFileLookup) GetFile(_ context.Context, id int) (*episodefiles.File, error) {
	if err, ok := s.errs[id]; ok {
		return nil, err
	}
	return s.files[id], nil
}

func ptrInt(v int) *int { return &v }

func TestHandleSeriesScanned_FileDateNone_NoOp(t *testing.T) {
	t.Parallel()
	mgr := &stubTimeMgr{}
	svc := newSvc(t, mgr, stubCfg{value: "None"})
	// Even with deps wired, FileDate=None must short-circuit before
	// any episode lookup fires.
	lister := &stubEpisodesLister{rows: []episodes.Episode{{ID: 1, EpisodeFileID: ptrInt(10)}}}
	files := &stubFileLookup{files: map[int]*episodefiles.File{10: {ID: 10, RelativePath: "ep.mkv"}}}
	svc.SetSeriesScannedDeps(lister, files)
	err := svc.HandleSeriesScanned(t.Context(), mediafiles.SeriesScannedEvent{
		Series: &series.Series{ID: 42, Path: "/tv/Show"},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if mgr.setCalls != 0 {
		t.Errorf("FileDate=None: set should not fire; got %d", mgr.setCalls)
	}
}

func TestHandleSeriesScanned_NoDepsWired_DebugLogsAndSkips(t *testing.T) {
	t.Parallel()
	mgr := &stubTimeMgr{}
	svc := newSvc(t, mgr, stubCfg{value: "LocalAirDate"})
	// Don't call SetSeriesScannedDeps — both deps stay nil.
	err := svc.HandleSeriesScanned(t.Context(), mediafiles.SeriesScannedEvent{
		Series: &series.Series{ID: 42, Path: "/tv/Show"},
	})
	if err != nil {
		t.Fatalf("err: %v (nil deps must be tolerated)", err)
	}
	if mgr.setCalls != 0 {
		t.Errorf("nil deps: set should not fire; got %d", mgr.setCalls)
	}
}

func TestHandleSeriesScanned_NilEvent_NoOp(t *testing.T) {
	t.Parallel()
	mgr := &stubTimeMgr{}
	svc := newSvc(t, mgr, stubCfg{value: "LocalAirDate"})
	svc.SetSeriesScannedDeps(&stubEpisodesLister{}, &stubFileLookup{})
	if err := svc.HandleSeriesScanned(t.Context(), mediafiles.SeriesScannedEvent{Series: nil}); err != nil {
		t.Fatalf("nil Series event: %v", err)
	}
}

func TestHandleSeriesScanned_GroupsByFileID_OneCallPerFile(t *testing.T) {
	t.Parallel()
	air := time.Date(2026, 5, 24, 18, 0, 0, 0, time.UTC)
	existing := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	mgr := &stubTimeMgr{getReturn: existing}
	svc := newSvc(t, mgr, stubCfg{value: "LocalAirDate"})

	// Two episodes pointing at the SAME EpisodeFileID — a two-part
	// story sharing one physical file. C# groups by EpisodeFileId so
	// ChangeFileDate fires once; the Go-port mirrors via the groups map.
	rows := []episodes.Episode{
		{ID: 1, EpisodeFileID: ptrInt(10), AirDateUtc: ptrTime(air)},
		{ID: 2, EpisodeFileID: ptrInt(10), AirDateUtc: ptrTime(air)},
		{ID: 3, EpisodeFileID: ptrInt(11), AirDateUtc: ptrTime(air)},
	}
	svc.SetSeriesScannedDeps(
		&stubEpisodesLister{rows: rows},
		&stubFileLookup{files: map[int]*episodefiles.File{
			10: {ID: 10, RelativePath: "two-parter.mkv"},
			11: {ID: 11, RelativePath: "next.mkv"},
		}},
	)
	err := svc.HandleSeriesScanned(t.Context(), mediafiles.SeriesScannedEvent{
		Series: &series.Series{ID: 42, Path: "/tv/Show"},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// 2 distinct fileIDs → 2 FileSetLastWriteTime calls (one per file,
	// not per episode).
	if mgr.setCalls != 2 {
		t.Errorf("set calls: got %d, want 2 (one per distinct fileID)", mgr.setCalls)
	}
}

func TestHandleSeriesScanned_SkipsEpisodesWithNoFile(t *testing.T) {
	t.Parallel()
	air := time.Date(2026, 5, 24, 18, 0, 0, 0, time.UTC)
	existing := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	mgr := &stubTimeMgr{getReturn: existing}
	svc := newSvc(t, mgr, stubCfg{value: "LocalAirDate"})
	rows := []episodes.Episode{
		{ID: 1, EpisodeFileID: nil, AirDateUtc: ptrTime(air)},
		{ID: 2, EpisodeFileID: ptrInt(0), AirDateUtc: ptrTime(air)}, // zero pointee
		{ID: 3, EpisodeFileID: ptrInt(10), AirDateUtc: ptrTime(air)},
	}
	svc.SetSeriesScannedDeps(
		&stubEpisodesLister{rows: rows},
		&stubFileLookup{files: map[int]*episodefiles.File{10: {ID: 10, RelativePath: "e3.mkv"}}},
	)
	err := svc.HandleSeriesScanned(t.Context(), mediafiles.SeriesScannedEvent{
		Series: &series.Series{ID: 42, Path: "/tv/Show"},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if mgr.setCalls != 1 {
		t.Errorf("set calls: got %d, want 1 (only id=10 should be processed)", mgr.setCalls)
	}
}

func TestHandleSeriesScanned_ListerError_Propagates(t *testing.T) {
	t.Parallel()
	mgr := &stubTimeMgr{}
	svc := newSvc(t, mgr, stubCfg{value: "LocalAirDate"})
	svc.SetSeriesScannedDeps(
		&stubEpisodesLister{err: errors.New("DB exploded")},
		&stubFileLookup{},
	)
	err := svc.HandleSeriesScanned(t.Context(), mediafiles.SeriesScannedEvent{
		Series: &series.Series{ID: 42, Path: "/tv/Show"},
	})
	if err == nil {
		t.Fatal("lister err: want propagated, got nil")
	}
}

func TestHandleSeriesScanned_PerFileLookupError_AbsorbedAndContinues(t *testing.T) {
	t.Parallel()
	air := time.Date(2026, 5, 24, 18, 0, 0, 0, time.UTC)
	existing := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	mgr := &stubTimeMgr{getReturn: existing}
	svc := newSvc(t, mgr, stubCfg{value: "LocalAirDate"})
	rows := []episodes.Episode{
		{ID: 1, EpisodeFileID: ptrInt(10), AirDateUtc: ptrTime(air)},
		{ID: 2, EpisodeFileID: ptrInt(11), AirDateUtc: ptrTime(air)},
	}
	svc.SetSeriesScannedDeps(
		&stubEpisodesLister{rows: rows},
		&stubFileLookup{
			files: map[int]*episodefiles.File{11: {ID: 11, RelativePath: "e2.mkv"}},
			errs:  map[int]error{10: errors.New("missing row")},
		},
	)
	err := svc.HandleSeriesScanned(t.Context(), mediafiles.SeriesScannedEvent{
		Series: &series.Series{ID: 42, Path: "/tv/Show"},
	})
	if err != nil {
		t.Fatalf("per-file lookup err must be absorbed: %v", err)
	}
	if mgr.setCalls != 1 {
		t.Errorf("set calls: got %d, want 1 (id=11 still processed despite id=10 lookup failure)", mgr.setCalls)
	}
}
