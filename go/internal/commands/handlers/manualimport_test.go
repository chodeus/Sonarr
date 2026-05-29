package handlers_test

import (
	"context"
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/commands/handlers"
	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/series"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

// --- stubs --------------------------------------------------------

type stubSeriesGetter struct {
	ser *series.Series
}

func (s *stubSeriesGetter) GetSeries(_ context.Context, _ int) (*series.Series, error) {
	return s.ser, nil
}

type stubEpisodeGetter struct {
	eps    []episodes.Episode
	gotIDs []int
}

func (s *stubEpisodeGetter) GetEpisodes(_ context.Context, ids []int) ([]episodes.Episode, error) {
	s.gotIDs = ids
	return s.eps, nil
}

// passthroughAggregator is a no-op augmenter: it returns le unchanged
// so the test can assert the handler's re-apply pass overrides nothing
// the aggregator didn't set. (A real aggregator would fill quality /
// languages; the re-apply pass then stamps the user's choices over the
// top — exactly the behaviour under test.)
type passthroughAggregator struct {
	calls int
}

func (a *passthroughAggregator) Augment(le *episodeimport.LocalEpisode, _ *downloadclients.Item) (*episodeimport.LocalEpisode, error) {
	a.calls++
	return le, nil
}

// recordingImporter captures exactly what the handler hands the real
// import pipeline. The real disk move + EpisodeFile insert is exercised
// by the episodeimport package's own temp-dir tests; the handler's job
// is to build a faithful decision and route it with the correct
// newDownload flag + download item, which this records.
type recordingImporter struct {
	calls        int
	gotDecisions []*episodeimport.ImportDecision
	gotNew       bool
	gotItem      *downloadclients.Item
	gotMode      episodeimport.ImportMode
}

func (i *recordingImporter) Import(_ context.Context, decisions []*episodeimport.ImportDecision, newDownload bool, item *downloadclients.Item, mode episodeimport.ImportMode) ([]*episodeimport.ImportResult, error) {
	i.calls++
	i.gotDecisions = decisions
	i.gotNew = newDownload
	i.gotItem = item
	i.gotMode = mode
	return []*episodeimport.ImportResult{}, nil
}

type stubManualFinder struct {
	td *trackeddownloads.TrackedDownload
}

func (s *stubManualFinder) Find(_ string) *trackeddownloads.TrackedDownload { return s.td }

// --- tests --------------------------------------------------------

func TestManualImport_NewWithNilImporter(t *testing.T) {
	t.Parallel()
	_, err := handlers.NewManualImport(&stubSeriesGetter{}, &stubEpisodeGetter{}, &passthroughAggregator{}, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error from nil importer")
	}
}

func TestManualImport_EmptyFiles_ReturnsError(t *testing.T) {
	t.Parallel()
	h := newTestManualImport(t, &stubSeriesGetter{}, &stubEpisodeGetter{}, &passthroughAggregator{}, &recordingImporter{}, nil)
	if err := h.Execute(t.Context(), `{"importMode":1}`); err == nil {
		t.Fatal("expected error on empty files")
	}
}

func TestManualImport_MalformedBody_ReturnsError(t *testing.T) {
	t.Parallel()
	h := newTestManualImport(t, &stubSeriesGetter{}, &stubEpisodeGetter{}, &passthroughAggregator{}, &recordingImporter{}, nil)
	if err := h.Execute(t.Context(), `not-json`); err == nil {
		t.Fatal("expected parse error")
	}
}

// Untracked import: the file lives OUTSIDE the series folder, so
// existingFile=false → newDownload=true. Asserts the decision carries
// the resolved series + episodes + the user's re-applied quality /
// languages / releaseGroup, and that the importer is routed untracked.
func TestManualImport_Untracked_BuildsDecisionAndRoutes(t *testing.T) {
	t.Parallel()
	ser := &series.Series{ID: 42, Path: "/tv/Show"}
	eps := []episodes.Episode{{ID: 100, SeriesID: 42, SeasonNumber: 1, EpisodeNumber: 1}}
	sg := &stubSeriesGetter{ser: ser}
	eg := &stubEpisodeGetter{eps: eps}
	agg := &passthroughAggregator{}
	imp := &recordingImporter{}
	h := newTestManualImport(t, sg, eg, agg, imp, nil)

	body := `{"importMode":1,"files":[{
		"path":"/downloads/Show.S01E01.WEBDL-1080p.mkv",
		"seriesId":42,
		"episodeIds":[100],
		"quality":{"quality":{"id":7},"revision":{"version":1}},
		"languages":[{"id":1,"name":"English"}],
		"releaseGroup":"SONARR",
		"indexerFlags":2,
		"releaseType":"singleEpisode"
	}]}`

	if err := h.Execute(t.Context(), body); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if agg.calls != 1 {
		t.Fatalf("aggregator called %d times, want 1", agg.calls)
	}
	if imp.calls != 1 {
		t.Fatalf("importer called %d times, want 1", imp.calls)
	}
	if !imp.gotNew {
		t.Error("untracked import outside series folder should set newDownload=true")
	}
	if imp.gotItem != nil {
		t.Errorf("untracked import should pass nil download item, got %v", imp.gotItem)
	}
	if imp.gotMode != episodeimport.ImportModeMove {
		t.Errorf("got mode %v, want Move", imp.gotMode)
	}
	if eg.gotIDs == nil || len(eg.gotIDs) != 1 || eg.gotIDs[0] != 100 {
		t.Errorf("episode getter got ids %v, want [100]", eg.gotIDs)
	}
	if len(imp.gotDecisions) != 1 {
		t.Fatalf("got %d decisions, want 1", len(imp.gotDecisions))
	}
	le := imp.gotDecisions[0].LocalEpisode
	if le == nil {
		t.Fatal("decision has nil LocalEpisode")
	}
	if le.Series == nil || le.Series.ID != 42 {
		t.Errorf("decision series = %v, want id 42", le.Series)
	}
	if len(le.Episodes) != 1 || le.Episodes[0].ID != 100 {
		t.Errorf("decision episodes = %v, want [100]", le.Episodes)
	}
	if le.Quality.Quality.ID != 7 {
		t.Errorf("re-applied quality id = %d, want 7", le.Quality.Quality.ID)
	}
	if len(le.Languages) != 1 || le.Languages[0].ID != 1 {
		t.Errorf("re-applied languages = %v, want [id:1]", le.Languages)
	}
	if le.ReleaseGroup != "SONARR" {
		t.Errorf("re-applied releaseGroup = %q, want SONARR", le.ReleaseGroup)
	}
	if int(le.IndexerFlags) != 2 {
		t.Errorf("re-applied indexerFlags = %d, want 2", le.IndexerFlags)
	}
	if le.ExistingFile {
		t.Error("file outside series folder should have ExistingFile=false")
	}
}

// Existing-file import: the file already lives INSIDE the series
// folder → existingFile=true → newDownload=false.
func TestManualImport_ExistingFile_NewDownloadFalse(t *testing.T) {
	t.Parallel()
	ser := &series.Series{ID: 42, Path: "/tv/Show"}
	imp := &recordingImporter{}
	h := newTestManualImport(t, &stubSeriesGetter{ser: ser}, &stubEpisodeGetter{}, &passthroughAggregator{}, imp, nil)

	body := `{"importMode":0,"files":[{"path":"/tv/Show/Season 01/Show.S01E01.mkv","seriesId":42,"episodeIds":[100]}]}`
	if err := h.Execute(t.Context(), body); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if imp.gotNew {
		t.Error("file inside series folder should set newDownload=false")
	}
	if le := imp.gotDecisions[0].LocalEpisode; le == nil || !le.ExistingFile {
		t.Error("file inside series folder should have ExistingFile=true")
	}
}

// Tracked import: a resolvable downloadId routes through the tracked
// path — newDownload=true with the tracked download's item.
func TestManualImport_Tracked_RoutesWithDownloadItem(t *testing.T) {
	t.Parallel()
	ser := &series.Series{ID: 42, Path: "/tv/Show"}
	item := &downloadclients.Item{Title: "Show.S01E01", DownloadID: "abc"}
	td := &trackeddownloads.TrackedDownload{
		DownloadItem:  item,
		RemoteEpisode: &decisionengine.Subject{},
	}
	imp := &recordingImporter{}
	h := newTestManualImport(t, &stubSeriesGetter{ser: ser}, &stubEpisodeGetter{}, &passthroughAggregator{}, imp, &stubManualFinder{td: td})

	body := `{"importMode":2,"files":[{"path":"/downloads/Show.S01E01.mkv","seriesId":42,"episodeIds":[100],"downloadId":"abc"}]}`
	if err := h.Execute(t.Context(), body); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !imp.gotNew {
		t.Error("tracked import should set newDownload=true")
	}
	if imp.gotItem != item {
		t.Errorf("tracked import should pass the tracked download item, got %v", imp.gotItem)
	}
	if imp.gotMode != episodeimport.ImportModeCopy {
		t.Errorf("got mode %v, want Copy", imp.gotMode)
	}
}

func newTestManualImport(
	t *testing.T,
	sg handlers.ManualImportSeriesGetter,
	eg handlers.ManualImportEpisodeGetter,
	agg handlers.ManualImportAggregator,
	imp handlers.ManualImportImporter,
	finder handlers.ManualImportTrackedDownloadFinder,
) *handlers.ManualImport {
	t.Helper()
	h, err := handlers.NewManualImport(sg, eg, agg, imp, finder, slog.Default())
	if err != nil {
		t.Fatalf("NewManualImport: %v", err)
	}
	return h
}
