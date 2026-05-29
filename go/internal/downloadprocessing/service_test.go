package downloadprocessing_test

import (
	"context"
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadprocessing"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/ospath"
	"sonarr.tv/sonarr/internal/series"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

type fakeStore struct {
	tds []*trackeddownloads.TrackedDownload
}

func (f fakeStore) GetTrackedDownloads() []*trackeddownloads.TrackedDownload { return f.tds }

type fakeImporter struct {
	calls   int
	gotPath string
	gotSer  *series.Series
	gotItem *downloadclients.Item
	results []*episodeimport.ImportResult
}

func (f *fakeImporter) ProcessPath(path string, _ episodeimport.ImportMode, ser *series.Series, dci *downloadclients.Item) []*episodeimport.ImportResult {
	f.calls++
	f.gotPath = path
	f.gotSer = ser
	f.gotItem = dci
	return f.results
}

func completedTD(id string, ser *series.Series, output string) *trackeddownloads.TrackedDownload {
	td := &trackeddownloads.TrackedDownload{
		DownloadID:   id,
		State:        trackeddownloads.StateDownloading,
		DownloadItem: &downloadclients.Item{DownloadID: id, Status: downloadclients.DownloadItemStatusCompleted, OutputPath: ospath.New(output)},
	}
	if ser != nil {
		td.RemoteEpisode = &decisionengine.Subject{Series: ser}
	}
	return td
}

// importedResult returns a result that ResultType()==Imported (empty
// errors + an approved decision).
func importedResult() *episodeimport.ImportResult {
	le := episodeimport.NewLocalEpisode()
	return &episodeimport.ImportResult{Decision: episodeimport.NewImportDecision(le)}
}

func newSvc(t *testing.T, store downloadprocessing.TrackedDownloadStore, imp downloadprocessing.Importer) *downloadprocessing.Service {
	t.Helper()
	s, err := downloadprocessing.NewService(store, imp, slog.Default())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return s
}

// fakeFailPub captures published DownloadFailedEvents (J400).
type fakeFailPub struct {
	evs []trackeddownloads.DownloadFailedEvent
	err error
}

func (f *fakeFailPub) PublishDownloadFailed(_ context.Context, ev trackeddownloads.DownloadFailedEvent) error {
	if f.err != nil {
		return f.err
	}
	f.evs = append(f.evs, ev)
	return nil
}

// failedTD builds a tracked download the client reports Failed, with an
// optional matched series + parsed grab.
func failedTD(id string, ser *series.Series) *trackeddownloads.TrackedDownload {
	td := &trackeddownloads.TrackedDownload{
		DownloadID:   id,
		State:        trackeddownloads.StateDownloading,
		DownloadItem: &downloadclients.Item{DownloadID: id, Title: "Show.S01E01.720p", Status: downloadclients.DownloadItemStatusFailed, Message: "extraction failed"},
	}
	if ser != nil {
		td.RemoteEpisode = &decisionengine.Subject{Series: ser}
	}
	return td
}

// J399: a completed + matched download imports via ProcessPath and is
// marked Imported when all results imported.
func TestProcess_CompletedMatched_Imports_J399(t *testing.T) {
	t.Parallel()
	ser := &series.Series{ID: 42, Path: "/tv/Show"}
	td := completedTD("dl1", ser, "/dl/Show.S01E01")
	imp := &fakeImporter{results: []*episodeimport.ImportResult{importedResult()}}
	n := newSvc(t, fakeStore{tds: []*trackeddownloads.TrackedDownload{td}}, imp).ProcessTrackedDownloads(context.Background())
	if n != 1 || imp.calls != 1 {
		t.Fatalf("n=%d calls=%d, want 1/1", n, imp.calls)
	}
	if imp.gotPath != "/dl/Show.S01E01" || imp.gotSer != ser || imp.gotItem != td.DownloadItem {
		t.Errorf("ProcessPath args: path=%q ser=%v item=%v", imp.gotPath, imp.gotSer, imp.gotItem)
	}
	if td.State != trackeddownloads.StateImported {
		t.Errorf("state = %v, want Imported", td.State)
	}
}

// J399: a download the client still reports Downloading is skipped.
func TestProcess_NotCompleted_Skipped_J399(t *testing.T) {
	t.Parallel()
	td := completedTD("dl1", &series.Series{ID: 1}, "/dl/x")
	td.DownloadItem.Status = downloadclients.DownloadItemStatusDownloading
	imp := &fakeImporter{}
	if n := newSvc(t, fakeStore{tds: []*trackeddownloads.TrackedDownload{td}}, imp).ProcessTrackedDownloads(context.Background()); n != 0 || imp.calls != 0 {
		t.Errorf("n=%d calls=%d, want 0/0", n, imp.calls)
	}
}

// J399: a completed but unmatched download (no series) is skipped (not
// imported) — the import-blocked state is a documented follow-up.
func TestProcess_CompletedUnmatched_Skipped_J399(t *testing.T) {
	t.Parallel()
	td := completedTD("dl1", nil, "/dl/x")
	imp := &fakeImporter{}
	if n := newSvc(t, fakeStore{tds: []*trackeddownloads.TrackedDownload{td}}, imp).ProcessTrackedDownloads(context.Background()); n != 0 || imp.calls != 0 {
		t.Errorf("n=%d calls=%d, want 0/0", n, imp.calls)
	}
}

// J399: a partial import (some results not imported) leaves the state
// Downloading for retry.
func TestProcess_PartialImport_NotMarked_J399(t *testing.T) {
	t.Parallel()
	ser := &series.Series{ID: 1, Path: "/tv/S"}
	td := completedTD("dl1", ser, "/dl/x")
	// One imported + one rejected (rejected decision → not Imported).
	rejected := &episodeimport.ImportResult{Decision: episodeimport.NewImportDecision(episodeimport.NewLocalEpisode(), episodeimport.NewImportRejection(episodeimport.ImportRejectionError, "no")), Errors: []string{"no"}}
	imp := &fakeImporter{results: []*episodeimport.ImportResult{importedResult(), rejected}}
	if n := newSvc(t, fakeStore{tds: []*trackeddownloads.TrackedDownload{td}}, imp).ProcessTrackedDownloads(context.Background()); n != 0 {
		t.Errorf("n=%d, want 0 (partial)", n)
	}
	if td.State != trackeddownloads.StateDownloading {
		t.Errorf("state = %v, want still Downloading", td.State)
	}
}

// J399: an already-Imported download is skipped.
func TestProcess_AlreadyImported_Skipped_J399(t *testing.T) {
	t.Parallel()
	td := completedTD("dl1", &series.Series{ID: 1}, "/dl/x")
	td.State = trackeddownloads.StateImported
	imp := &fakeImporter{}
	if n := newSvc(t, fakeStore{tds: []*trackeddownloads.TrackedDownload{td}}, imp).ProcessTrackedDownloads(context.Background()); n != 0 || imp.calls != 0 {
		t.Errorf("n=%d calls=%d, want 0/0", n, imp.calls)
	}
}

// J400: a failed + matched download publishes a DownloadFailedEvent and
// is marked Failed. The failure message comes from the client item.
func TestProcess_FailedMatched_PublishesAndMarks_J400(t *testing.T) {
	t.Parallel()
	ser := &series.Series{ID: 42}
	td := failedTD("dl1", ser)
	pub := &fakeFailPub{}
	svc := newSvc(t, fakeStore{tds: []*trackeddownloads.TrackedDownload{td}}, &fakeImporter{})
	svc.SetFailurePublisher(pub)

	svc.ProcessTrackedDownloads(context.Background())

	if len(pub.evs) != 1 {
		t.Fatalf("published %d events, want 1", len(pub.evs))
	}
	ev := pub.evs[0]
	if ev.SeriesID != 42 || ev.DownloadID != "dl1" || ev.Message != "extraction failed" {
		t.Errorf("event = %+v, want SeriesID=42 DownloadID=dl1 Message=\"extraction failed\"", ev)
	}
	if ev.SkipRedownload {
		t.Error("SkipRedownload = true, want false (auto-failure should allow redownload)")
	}
	if ev.SourceTitle != "Show.S01E01.720p" {
		t.Errorf("SourceTitle = %q, want the download-item title", ev.SourceTitle)
	}
	if td.State != trackeddownloads.StateFailed {
		t.Errorf("state = %v, want Failed", td.State)
	}
}

// J400: an encrypted download (Status not Failed) is still detected and
// uses the "Encrypted download detected" message.
func TestProcess_Encrypted_PublishesEncryptedMessage_J400(t *testing.T) {
	t.Parallel()
	td := failedTD("dl1", &series.Series{ID: 7})
	td.DownloadItem.Status = downloadclients.DownloadItemStatusDownloading
	td.DownloadItem.IsEncrypted = true
	pub := &fakeFailPub{}
	svc := newSvc(t, fakeStore{tds: []*trackeddownloads.TrackedDownload{td}}, &fakeImporter{})
	svc.SetFailurePublisher(pub)

	svc.ProcessTrackedDownloads(context.Background())

	if len(pub.evs) != 1 || pub.evs[0].Message != "Encrypted download detected" {
		t.Fatalf("evs=%d msg=%q, want 1 / \"Encrypted download detected\"", len(pub.evs), func() string {
			if len(pub.evs) > 0 {
				return pub.evs[0].Message
			}
			return ""
		}())
	}
	if td.State != trackeddownloads.StateFailed {
		t.Errorf("state = %v, want Failed", td.State)
	}
}

// J400: a failed but unmatched download (no series) is skipped — not
// published, not marked (C# warns "wasn't grabbed by Sonarr").
func TestProcess_FailedUnmatched_Skipped_J400(t *testing.T) {
	t.Parallel()
	td := failedTD("dl1", nil)
	pub := &fakeFailPub{}
	svc := newSvc(t, fakeStore{tds: []*trackeddownloads.TrackedDownload{td}}, &fakeImporter{})
	svc.SetFailurePublisher(pub)

	svc.ProcessTrackedDownloads(context.Background())

	if len(pub.evs) != 0 {
		t.Errorf("published %d events, want 0", len(pub.evs))
	}
	if td.State != trackeddownloads.StateDownloading {
		t.Errorf("state = %v, want still Downloading", td.State)
	}
}

// J400: with no publisher wired the failure arm no-ops (preserves prior
// behavior; the SAFE default for the no-clients contract goldens).
func TestProcess_FailedNoPublisher_NoOp_J400(t *testing.T) {
	t.Parallel()
	td := failedTD("dl1", &series.Series{ID: 1})
	svc := newSvc(t, fakeStore{tds: []*trackeddownloads.TrackedDownload{td}}, &fakeImporter{})
	svc.ProcessTrackedDownloads(context.Background())
	if td.State != trackeddownloads.StateDownloading {
		t.Errorf("state = %v, want still Downloading (unwired)", td.State)
	}
}

// J400: a publish error leaves the download Downloading for retry (no
// silent state transition without the side effect).
func TestProcess_FailedPublishError_NotMarked_J400(t *testing.T) {
	t.Parallel()
	td := failedTD("dl1", &series.Series{ID: 1})
	pub := &fakeFailPub{err: context.Canceled}
	svc := newSvc(t, fakeStore{tds: []*trackeddownloads.TrackedDownload{td}}, &fakeImporter{})
	svc.SetFailurePublisher(pub)
	svc.ProcessTrackedDownloads(context.Background())
	if td.State != trackeddownloads.StateDownloading {
		t.Errorf("state = %v, want still Downloading (publish failed)", td.State)
	}
}

// fakeCompletePub captures published DownloadCompletedEvents (J449).
type fakeCompletePub struct {
	evs []trackeddownloads.DownloadCompletedEvent
	err error
}

func (f *fakeCompletePub) PublishDownloadCompleted(_ context.Context, ev trackeddownloads.DownloadCompletedEvent) error {
	if f.err != nil {
		return f.err
	}
	f.evs = append(f.evs, ev)
	return nil
}

// importedResultWithFile returns an Imported result carrying an
// EpisodeFile row (the file the writer produced), so the completion
// event's EpisodeFiles slice can be asserted.
func importedResultWithFile(fileID int) *episodeimport.ImportResult {
	le := episodeimport.NewLocalEpisode()
	return episodeimport.NewImportedImportResult(episodeimport.NewImportDecision(le), &episodefiles.File{ID: fileID})
}

// J449: a fully-imported completed download publishes a
// DownloadCompletedEvent carrying the tracked download, matched series
// id, and the imported EpisodeFile rows.
func TestProcess_CompletedMatched_PublishesCompleted_J449(t *testing.T) {
	t.Parallel()
	ser := &series.Series{ID: 42, Path: "/tv/Show"}
	td := completedTD("dl1", ser, "/dl/Show.S01E01")
	imp := &fakeImporter{results: []*episodeimport.ImportResult{importedResultWithFile(7), importedResultWithFile(8)}}
	pub := &fakeCompletePub{}
	svc := newSvc(t, fakeStore{tds: []*trackeddownloads.TrackedDownload{td}}, imp)
	svc.SetCompletionPublisher(pub)
	if n := svc.ProcessTrackedDownloads(context.Background()); n != 1 {
		t.Fatalf("n=%d, want 1", n)
	}
	if td.State != trackeddownloads.StateImported {
		t.Errorf("state = %v, want Imported", td.State)
	}
	if len(pub.evs) != 1 {
		t.Fatalf("published %d events, want 1", len(pub.evs))
	}
	ev := pub.evs[0]
	if ev.TrackedDownload != td {
		t.Errorf("event TrackedDownload = %v, want td", ev.TrackedDownload)
	}
	if ev.SeriesID != 42 {
		t.Errorf("event SeriesID = %d, want 42", ev.SeriesID)
	}
	if len(ev.EpisodeFiles) != 2 || ev.EpisodeFiles[0].ID != 7 || ev.EpisodeFiles[1].ID != 8 {
		t.Errorf("event EpisodeFiles = %+v, want file ids [7 8]", ev.EpisodeFiles)
	}
}

// J449: with no completion publisher wired the import still happens
// (state Imported, counted) but nothing is published (prior J399 behavior).
func TestProcess_CompletedNoCompletionPublisher_NoEvent_J449(t *testing.T) {
	t.Parallel()
	ser := &series.Series{ID: 1, Path: "/tv/Show"}
	td := completedTD("dl1", ser, "/dl/Show.S01E01")
	imp := &fakeImporter{results: []*episodeimport.ImportResult{importedResult()}}
	svc := newSvc(t, fakeStore{tds: []*trackeddownloads.TrackedDownload{td}}, imp)
	if n := svc.ProcessTrackedDownloads(context.Background()); n != 1 {
		t.Fatalf("n=%d, want 1", n)
	}
	if td.State != trackeddownloads.StateImported {
		t.Errorf("state = %v, want Imported", td.State)
	}
}

// J449: a partial import publishes NO completion event (the gate is
// all-episodes-imported; the download stays Downloading for retry).
func TestProcess_PartialImport_NoCompletedEvent_J449(t *testing.T) {
	t.Parallel()
	ser := &series.Series{ID: 1, Path: "/tv/Show"}
	td := completedTD("dl1", ser, "/dl/Show.S01E01")
	rejected := &episodeimport.ImportResult{Decision: episodeimport.NewImportDecision(episodeimport.NewLocalEpisode(), episodeimport.NewImportRejection(episodeimport.ImportRejectionError, "no")), Errors: []string{"no"}}
	imp := &fakeImporter{results: []*episodeimport.ImportResult{importedResultWithFile(7), rejected}}
	pub := &fakeCompletePub{}
	svc := newSvc(t, fakeStore{tds: []*trackeddownloads.TrackedDownload{td}}, imp)
	svc.SetCompletionPublisher(pub)
	if n := svc.ProcessTrackedDownloads(context.Background()); n != 0 {
		t.Fatalf("n=%d, want 0", n)
	}
	if len(pub.evs) != 0 {
		t.Errorf("published %d events, want 0 (partial import)", len(pub.evs))
	}
	if td.State != trackeddownloads.StateDownloading {
		t.Errorf("state = %v, want still Downloading", td.State)
	}
}

// J449: a completion-publish error is best-effort — the files are
// already on disk, so the download stays Imported (the event is an
// audit/notification signal, not a transactional gate).
func TestProcess_CompletionPublishError_StaysImported_J449(t *testing.T) {
	t.Parallel()
	ser := &series.Series{ID: 1, Path: "/tv/Show"}
	td := completedTD("dl1", ser, "/dl/Show.S01E01")
	imp := &fakeImporter{results: []*episodeimport.ImportResult{importedResult()}}
	pub := &fakeCompletePub{err: context.Canceled}
	svc := newSvc(t, fakeStore{tds: []*trackeddownloads.TrackedDownload{td}}, imp)
	svc.SetCompletionPublisher(pub)
	if n := svc.ProcessTrackedDownloads(context.Background()); n != 1 {
		t.Fatalf("n=%d, want 1 (import succeeded)", n)
	}
	if td.State != trackeddownloads.StateImported {
		t.Errorf("state = %v, want Imported (publish error is best-effort)", td.State)
	}
}
