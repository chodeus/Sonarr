package downloadprocessing_test

import (
	"context"
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadprocessing"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/history"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/ospath"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

type fakeManualPub struct {
	evs []trackeddownloads.ManualInteractionRequiredEvent
	err error
}

func (f *fakeManualPub) PublishManualInteractionRequired(_ context.Context, ev trackeddownloads.ManualInteractionRequiredEvent) error {
	if f.err != nil {
		return f.err
	}
	f.evs = append(f.evs, ev)
	return nil
}

type fakeHistoryReader struct {
	entries []history.Entry
	err     error
}

func (f fakeHistoryReader) AllByDownloadID(context.Context, string) ([]history.Entry, error) {
	return f.entries, f.err
}

func rejectedResultWithPath(path string, reason episodeimport.ImportRejectionReason) *episodeimport.ImportResult {
	le := episodeimport.NewLocalEpisode()
	le.Path = path
	return &episodeimport.ImportResult{
		Decision: episodeimport.NewImportDecision(le, episodeimport.NewImportRejection(reason, "rejected")),
		Errors:   []string{"rejected"},
	}
}

// matchedTD builds a completed+matched tracked download with a release
// (so RejectedImportService can resolve the indexer) + episodes.
func matchedTD(id string, episodeIDs ...int) *trackeddownloads.TrackedDownload {
	eps := make([]episodes.Episode, len(episodeIDs))
	for i, eid := range episodeIDs {
		eps[i] = episodes.Episode{ID: eid}
	}
	return &trackeddownloads.TrackedDownload{
		DownloadID:   id,
		State:        trackeddownloads.StateDownloading,
		DownloadItem: &downloadclients.Item{DownloadID: id, Title: "Show.S01E01", Status: downloadclients.DownloadItemStatusCompleted, OutputPath: ospath.New("/dl/" + id)},
		RemoteEpisode: &decisionengine.Subject{
			Series:   &series.Series{ID: 1},
			Episodes: eps,
			Release:  &parser.ReleaseInfo{IndexerID: 7},
		},
	}
}

func wireImportBlocked(s *downloadprocessing.Service, pub downloadprocessing.ManualInteractionPublisher, hist downloadprocessing.HistoryByDownloadReader) {
	rej := downloadprocessing.NewRejectedImportService(nil, slog.New(slog.DiscardHandler))
	s.SetImportBlockedDeps(hist, rej, pub)
}

func TestImportBlocked_PartialImport_BlocksAndNotifies_J517(t *testing.T) {
	t.Parallel()
	td := matchedTD("p1", 1, 2)
	imp := &fakeImporter{results: []*episodeimport.ImportResult{
		importedResult(),
		rejectedResultWithPath("/dl/p1/b.mkv", episodeimport.ImportRejectionUnknown),
	}}
	svc := newSvc(t, fakeStore{tds: []*trackeddownloads.TrackedDownload{td}}, imp)
	pub := &fakeManualPub{}
	wireImportBlocked(svc, pub, nil)

	svc.ProcessTrackedDownloads(context.Background())

	if td.State != trackeddownloads.StateImportBlocked {
		t.Errorf("State = %v, want ImportBlocked", td.State)
	}
	if td.Status != trackeddownloads.StatusWarning {
		t.Errorf("Status = %v, want Warning", td.Status)
	}
	if !td.HasNotifiedManualInteractionRequired {
		t.Error("HasNotifiedManualInteractionRequired should be set")
	}
	if len(pub.evs) != 1 {
		t.Fatalf("published %d manual-interaction events, want 1", len(pub.evs))
	}
	if pub.evs[0].TrackedDownload != td {
		t.Error("event should carry the tracked download")
	}
}

func TestImportBlocked_NotifiesOnlyOnce_J517(t *testing.T) {
	t.Parallel()
	td := matchedTD("p2", 1)
	td.HasNotifiedManualInteractionRequired = true // already notified
	imp := &fakeImporter{results: []*episodeimport.ImportResult{
		rejectedResultWithPath("/dl/p2/a.mkv", episodeimport.ImportRejectionUnknown),
		importedResult(),
	}}
	svc := newSvc(t, fakeStore{tds: []*trackeddownloads.TrackedDownload{td}}, imp)
	pub := &fakeManualPub{}
	wireImportBlocked(svc, pub, nil)

	svc.ProcessTrackedDownloads(context.Background())

	if td.State != trackeddownloads.StateImportBlocked {
		t.Errorf("State = %v, want ImportBlocked", td.State)
	}
	if len(pub.evs) != 0 {
		t.Errorf("already-notified download must not re-publish, got %d", len(pub.evs))
	}
}

func TestImportBlocked_EmptyResults_PendingNotBlocked_J517(t *testing.T) {
	t.Parallel()
	td := matchedTD("e1", 1)
	imp := &fakeImporter{results: nil} // no eligible files
	svc := newSvc(t, fakeStore{tds: []*trackeddownloads.TrackedDownload{td}}, imp)
	pub := &fakeManualPub{}
	wireImportBlocked(svc, pub, nil)

	svc.ProcessTrackedDownloads(context.Background())

	if td.State != trackeddownloads.StateImportPending {
		t.Errorf("State = %v, want ImportPending (empty results aren't blocked)", td.State)
	}
	if len(pub.evs) != 0 {
		t.Errorf("empty results must not notify, got %d", len(pub.evs))
	}
	if td.Status != trackeddownloads.StatusWarning || len(td.StatusMessages) == 0 {
		t.Error("empty results should warn")
	}
}

func TestImportBlocked_SingleRejectedFailsViaIndexer_J517(t *testing.T) {
	t.Parallel()
	td := matchedTD("f1", 1)
	imp := &fakeImporter{results: []*episodeimport.ImportResult{
		rejectedResultWithPath("/dl/f1/evil.exe", episodeimport.ImportRejectionExecutableFile),
	}}
	svc := newSvc(t, fakeStore{tds: []*trackeddownloads.TrackedDownload{td}}, imp)
	pub := &fakeManualPub{}
	// RejectedImportService with an indexer that fails executables.
	rej := downloadprocessing.NewRejectedImportService(
		stubSettings{settings: &indexers.CachedIndexerSettings{FailDownloads: []indexers.FailDownloads{indexers.FailDownloadsExecutables}}},
		slog.New(slog.DiscardHandler))
	svc.SetImportBlockedDeps(nil, rej, pub)

	svc.ProcessTrackedDownloads(context.Background())

	// Hard-failed → FailedPending, NOT blocked, NO manual-interaction event.
	if td.State != trackeddownloads.StateFailedPending {
		t.Errorf("State = %v, want FailedPending", td.State)
	}
	if len(pub.evs) != 0 {
		t.Errorf("hard-failed download must not fire manual-interaction, got %d", len(pub.evs))
	}
}

func TestImportBlocked_AlreadyImportedInHistory_MarksImported_J517(t *testing.T) {
	t.Parallel()
	td := matchedTD("h1", 1, 2)
	// This run imports nothing (all skipped/rejected), but history shows
	// both episodes already DownloadFolderImported.
	imp := &fakeImporter{results: []*episodeimport.ImportResult{
		rejectedResultWithPath("/dl/h1/a.mkv", episodeimport.ImportRejectionUnknown),
	}}
	imported := int(history.EventTypeDownloadFolderImported)
	hist := fakeHistoryReader{entries: []history.Entry{
		{EpisodeID: 1, EventType: &imported},
		{EpisodeID: 2, EventType: &imported},
	}}
	svc := newSvc(t, fakeStore{tds: []*trackeddownloads.TrackedDownload{td}}, imp)
	pub := &fakeManualPub{}
	wireImportBlocked(svc, pub, hist)

	svc.ProcessTrackedDownloads(context.Background())

	if td.State != trackeddownloads.StateImported {
		t.Errorf("State = %v, want Imported (history double-check)", td.State)
	}
	if len(pub.evs) != 0 {
		t.Errorf("already-imported-in-history must not block/notify, got %d", len(pub.evs))
	}
}

func TestImportBlocked_HistoryGrabbedOnly_DoesNotMarkImported_J517(t *testing.T) {
	t.Parallel()
	td := matchedTD("h2", 1)
	imp := &fakeImporter{results: []*episodeimport.ImportResult{
		rejectedResultWithPath("/dl/h2/a.mkv", episodeimport.ImportRejectionUnknown),
	}}
	grabbed := int(history.EventTypeGrabbed)
	hist := fakeHistoryReader{entries: []history.Entry{{EpisodeID: 1, EventType: &grabbed}}}
	svc := newSvc(t, fakeStore{tds: []*trackeddownloads.TrackedDownload{td}}, imp)
	pub := &fakeManualPub{}
	wireImportBlocked(svc, pub, hist)

	svc.ProcessTrackedDownloads(context.Background())

	// Only grabbed (not imported) in history → blocks.
	if td.State != trackeddownloads.StateImportBlocked {
		t.Errorf("State = %v, want ImportBlocked", td.State)
	}
}

func TestImportBlocked_Unwired_LegacyRetry_J517(t *testing.T) {
	t.Parallel()
	td := matchedTD("l1", 1)
	imp := &fakeImporter{results: []*episodeimport.ImportResult{
		rejectedResultWithPath("/dl/l1/a.mkv", episodeimport.ImportRejectionUnknown),
	}}
	svc := newSvc(t, fakeStore{tds: []*trackeddownloads.TrackedDownload{td}}, imp)
	// No SetImportBlockedDeps → legacy behaviour.
	svc.ProcessTrackedDownloads(context.Background())
	if td.State != trackeddownloads.StateDownloading {
		t.Errorf("State = %v, want Downloading (legacy retry when unwired)", td.State)
	}
}
