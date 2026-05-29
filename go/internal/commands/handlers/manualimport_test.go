package handlers_test

import (
	"context"
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/commands/handlers"
	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/quality"
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

// presettingAggregator stamps DIFFERENT quality/languages/releaseGroup
// onto the LocalEpisode (as a real augmenter would) so a test can prove
// the handler's re-apply pass OVERRIDES the augmented values with the
// user's choices, rather than merely filling empty fields.
type presettingAggregator struct{}

func (presettingAggregator) Augment(le *episodeimport.LocalEpisode, _ *downloadclients.Item) (*episodeimport.LocalEpisode, error) {
	if q, ok := quality.FindByID(1); ok { // SDTV — distinct from the user's id 7
		le.Quality = quality.Model{Quality: q}
	}
	if l, ok := language.FindByID(2); ok { // French — distinct from the user's id 1
		le.Languages = []language.Language{l}
	}
	le.ReleaseGroup = "AUGMENTED"
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
	_, err := handlers.NewManualImport(&stubSeriesGetter{}, &stubEpisodeGetter{}, &passthroughAggregator{}, nil, nil, nil, nil)
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

// Re-apply override: the aggregator stamps SDTV / French / "AUGMENTED",
// but the user's body specifies quality id 7 + language id 1 +
// "SONARR" — the handler's re-apply pass must win (C# `:537-543`).
func TestManualImport_ReapplyOverridesAugmented(t *testing.T) {
	t.Parallel()
	ser := &series.Series{ID: 42, Path: "/tv/Show"}
	imp := &recordingImporter{}
	h := newTestManualImport(t, &stubSeriesGetter{ser: ser}, &stubEpisodeGetter{}, presettingAggregator{}, imp, nil)

	body := `{"importMode":1,"files":[{
		"path":"/downloads/Show.S01E01.mkv",
		"seriesId":42,
		"episodeIds":[100],
		"quality":{"quality":{"id":7},"revision":{"version":1}},
		"languages":[{"id":1,"name":"English"}],
		"releaseGroup":"SONARR"
	}]}`
	if err := h.Execute(t.Context(), body); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	le := imp.gotDecisions[0].LocalEpisode
	if le.Quality.Quality.ID != 7 {
		t.Errorf("quality id = %d, want 7 (user override should beat augmented id 1)", le.Quality.Quality.ID)
	}
	if len(le.Languages) != 1 || le.Languages[0].ID != 1 {
		t.Errorf("languages = %v, want [id:1] (user override should beat augmented id 2)", le.Languages)
	}
	if le.ReleaseGroup != "SONARR" {
		t.Errorf("releaseGroup = %q, want SONARR (user override should beat AUGMENTED)", le.ReleaseGroup)
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
	h, err := handlers.NewManualImport(sg, eg, agg, imp, finder, nil, slog.Default())
	if err != nil {
		t.Fatalf("NewManualImport: %v", err)
	}
	return h
}

// --- J450: completion-event publication --------------------------

// resultImporter returns an Imported result per decision, attaching a
// fresh EpisodeFile (id auto-incremented) whose SeriesId comes from the
// decision's LocalEpisode and whose SeasonNumber is the configured value
// — enough to drive the untracked series/season grouping + the tracked
// all-imported gate.
type resultImporter struct {
	season int
	nextID int
}

func (r *resultImporter) Import(_ context.Context, decisions []*episodeimport.ImportDecision, _ bool, _ *downloadclients.Item, _ episodeimport.ImportMode) ([]*episodeimport.ImportResult, error) {
	var out []*episodeimport.ImportResult
	for _, d := range decisions {
		r.nextID++
		f := &episodefiles.File{ID: r.nextID, SeasonNumber: r.season}
		if d.LocalEpisode != nil && d.LocalEpisode.Series != nil {
			f.SeriesID = d.LocalEpisode.Series.ID
		}
		out = append(out, episodeimport.NewImportedImportResult(d, f))
	}
	return out, nil
}

type fakeManualCompletionPub struct {
	completed []trackeddownloads.DownloadCompletedEvent
	untracked []trackeddownloads.UntrackedDownloadCompletedEvent
}

func (f *fakeManualCompletionPub) PublishDownloadCompleted(_ context.Context, ev trackeddownloads.DownloadCompletedEvent) error {
	f.completed = append(f.completed, ev)
	return nil
}

func (f *fakeManualCompletionPub) PublishUntrackedDownloadCompleted(_ context.Context, ev trackeddownloads.UntrackedDownloadCompletedEvent) error {
	f.untracked = append(f.untracked, ev)
	return nil
}

func newManualImportWithPub(t *testing.T, eg handlers.ManualImportEpisodeGetter, imp handlers.ManualImportImporter, finder handlers.ManualImportTrackedDownloadFinder, pub handlers.ManualImportCompletionPublisher) *handlers.ManualImport {
	t.Helper()
	h, err := handlers.NewManualImport(&stubSeriesGetter{ser: &series.Series{ID: 42, Path: "/tv/Show"}}, eg, &passthroughAggregator{}, imp, finder, pub, slog.Default())
	if err != nil {
		t.Fatalf("NewManualImport: %v", err)
	}
	return h
}

// J450: an untracked manual import publishes an
// UntrackedDownloadCompletedEvent carrying the series, episodes, files,
// and the longest-common source path.
func TestManualImport_Untracked_PublishesUntrackedCompleted_J450(t *testing.T) {
	t.Parallel()
	eg := &stubEpisodeGetter{eps: []episodes.Episode{{ID: 100}}}
	pub := &fakeManualCompletionPub{}
	h := newManualImportWithPub(t, eg, &resultImporter{season: 1}, nil, pub)

	body := `{"importMode":1,"files":[{"path":"/downloads/Show/ep.mkv","seriesId":42,"episodeIds":[100]}]}`
	if err := h.Execute(t.Context(), body); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(pub.completed) != 0 {
		t.Errorf("untracked import published %d DownloadCompletedEvents, want 0", len(pub.completed))
	}
	if len(pub.untracked) != 1 {
		t.Fatalf("published %d UntrackedDownloadCompletedEvents, want 1", len(pub.untracked))
	}
	ev := pub.untracked[0]
	if ev.Series == nil || ev.Series.ID != 42 {
		t.Errorf("event Series = %v, want id 42", ev.Series)
	}
	if len(ev.EpisodeFiles) != 1 || ev.EpisodeFiles[0].SeasonNumber != 1 {
		t.Errorf("event EpisodeFiles = %+v, want 1 season-1 file", ev.EpisodeFiles)
	}
	if len(ev.Episodes) != 1 || ev.Episodes[0].ID != 100 {
		t.Errorf("event Episodes = %+v, want [ep 100]", ev.Episodes)
	}
	if ev.SourcePath != "/downloads/Show" {
		t.Errorf("event SourcePath = %q, want /downloads/Show", ev.SourcePath)
	}
}

// J450: two untracked files of the same series+season collapse into ONE
// grouped UntrackedDownloadCompletedEvent (both files, common source dir).
func TestManualImport_Untracked_GroupsBySeriesSeason_J450(t *testing.T) {
	t.Parallel()
	eg := &stubEpisodeGetter{eps: []episodes.Episode{{ID: 100}}}
	pub := &fakeManualCompletionPub{}
	h := newManualImportWithPub(t, eg, &resultImporter{season: 2}, nil, pub)

	body := `{"importMode":1,"files":[` +
		`{"path":"/downloads/Show/a.mkv","seriesId":42,"episodeIds":[100]},` +
		`{"path":"/downloads/Show/b.mkv","seriesId":42,"episodeIds":[100]}]}`
	if err := h.Execute(t.Context(), body); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(pub.untracked) != 1 {
		t.Fatalf("published %d events, want 1 grouped event", len(pub.untracked))
	}
	ev := pub.untracked[0]
	if len(ev.EpisodeFiles) != 2 {
		t.Errorf("grouped event has %d files, want 2", len(ev.EpisodeFiles))
	}
	if ev.SourcePath != "/downloads/Show" {
		t.Errorf("grouped SourcePath = %q, want /downloads/Show", ev.SourcePath)
	}
}

// J450: a tracked manual import whose every episode imports marks the
// tracked download Imported + publishes a DownloadCompletedEvent.
func TestManualImport_Tracked_PublishesCompleted_J450(t *testing.T) {
	t.Parallel()
	eg := &stubEpisodeGetter{eps: []episodes.Episode{{ID: 100}}}
	td := &trackeddownloads.TrackedDownload{
		State:         trackeddownloads.StateDownloading,
		DownloadItem:  &downloadclients.Item{DownloadID: "abc", Title: "Show.S01E01"},
		RemoteEpisode: &decisionengine.Subject{}, // 0 episodes → expected gate = 1
	}
	pub := &fakeManualCompletionPub{}
	h := newManualImportWithPub(t, eg, &resultImporter{season: 1}, &stubManualFinder{td: td}, pub)

	body := `{"importMode":1,"files":[{"path":"/downloads/Show.S01E01.mkv","seriesId":42,"episodeIds":[100],"downloadId":"abc"}]}`
	if err := h.Execute(t.Context(), body); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(pub.untracked) != 0 {
		t.Errorf("tracked import published %d untracked events, want 0", len(pub.untracked))
	}
	if len(pub.completed) != 1 {
		t.Fatalf("published %d DownloadCompletedEvents, want 1", len(pub.completed))
	}
	ev := pub.completed[0]
	if ev.TrackedDownload != td {
		t.Errorf("event TrackedDownload mismatch")
	}
	if ev.SeriesID != 42 {
		t.Errorf("event SeriesID = %d, want 42", ev.SeriesID)
	}
	if len(ev.EpisodeFiles) != 1 {
		t.Errorf("event EpisodeFiles = %d, want 1", len(ev.EpisodeFiles))
	}
	if td.State != trackeddownloads.StateImported {
		t.Errorf("tracked download state = %v, want Imported", td.State)
	}
}

// J450: a tracked manual import that imports FEWER episodes than the
// release expected fails the all-imported gate — no DownloadCompletedEvent,
// state unchanged.
func TestManualImport_Tracked_PartialGate_NoEvent_J450(t *testing.T) {
	t.Parallel()
	eg := &stubEpisodeGetter{eps: []episodes.Episode{{ID: 100}}} // import 1 episode
	td := &trackeddownloads.TrackedDownload{
		State:        trackeddownloads.StateDownloading,
		DownloadItem: &downloadclients.Item{DownloadID: "abc", Title: "Show.S01"},
		// release expected 2 episodes, only 1 imported → gate fails
		RemoteEpisode: &decisionengine.Subject{Episodes: []episodes.Episode{{ID: 1}, {ID: 2}}},
	}
	pub := &fakeManualCompletionPub{}
	h := newManualImportWithPub(t, eg, &resultImporter{season: 1}, &stubManualFinder{td: td}, pub)

	body := `{"importMode":1,"files":[{"path":"/downloads/Show.S01E01.mkv","seriesId":42,"episodeIds":[100],"downloadId":"abc"}]}`
	if err := h.Execute(t.Context(), body); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(pub.completed) != 0 {
		t.Errorf("published %d DownloadCompletedEvents, want 0 (gate failed)", len(pub.completed))
	}
	if td.State != trackeddownloads.StateDownloading {
		t.Errorf("tracked download state = %v, want still Downloading", td.State)
	}
}
